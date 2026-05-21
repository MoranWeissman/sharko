package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"regexp"
	"time"

	"github.com/MoranWeissman/sharko/internal/gitops"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
	"github.com/MoranWeissman/sharko/internal/verify"

	"gopkg.in/yaml.v3"
)

// supportedProviders enumerates the cluster-provider values RegisterCluster
// accepts.  V125-1.1 adds "kubeconfig" — the inline-kubeconfig path used by
// the wizard's "Generic K8s (kubeconfig)" option.  GKE / AKS / exec-plugin
// auth remain V125-1.x material.
var supportedProviders = map[string]bool{
	"eks":        true,
	"kubeconfig": true,
}

// ErrClusterAlreadyExists is returned when attempting to register a cluster
// that already exists in ArgoCD.
var ErrClusterAlreadyExists = errors.New("cluster already exists")

var validClusterName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)

// RegisterCluster orchestrates cluster registration via the V125-1-8
// reconciler-owned model:
//
//  1. Validate input
//  1b. Merge default addons
//  2. Check for duplicate cluster in ArgoCD (adopt if exists; never duplicate-register)
//  3. Fetch credentials from provider (kubeconfig or AWS-SM/EKS path)
//  3a. Verify connectivity via Stage 1 (UX win — fail fast on bad creds)
//  4. Create addon secrets on remote cluster (if configured)
//  5. Generate values file + commit via PR (create + auto-merge if configured)
//  6. Trigger the reconciler (V125-1-8.4 wires reconciler.Trigger) so the
//     ArgoCD cluster Secret is created/updated immediately post-merge
//     rather than waiting for the 30s safety-net tick.
//
// V125-1-8.3 retirement: the old Step 3b (`argoSecretManager.Ensure`
// pre-merge) and old Step 6 (direct `argocd.RegisterCluster` API call
// pre-merge) were deleted. The reconciler now owns the entire ArgoCD
// cluster-Secret lifecycle — see design doc §12 V125-1-8 step 5. The
// register flow no longer creates anything in the argocd namespace
// before the managed-clusters.yaml PR merges, closing the orphan-on-PR-close
// bug class (BUG-058) at the architectural level.
func (o *Orchestrator) RegisterCluster(ctx context.Context, req RegisterClusterRequest) (*RegisterClusterResult, error) {
	// Step 1: Validate input.
	if req.Name == "" {
		return nil, fmt.Errorf("cluster name is required")
	}
	if !validClusterName.MatchString(req.Name) {
		return nil, fmt.Errorf("invalid cluster name %q: must be alphanumeric with hyphens, starting with an alphanumeric character", req.Name)
	}

	// Step 1a: Validate provider against the supported set.
	// V125-1.1 widens this to accept "kubeconfig" alongside the original
	// "eks" path. Empty provider remains valid for backward-compat with
	// pre-V125 callers (treated as the EKS path via credProvider).
	if req.Provider != "" && !supportedProviders[req.Provider] {
		return nil, fmt.Errorf("provider %q not yet implemented; supported: eks, kubeconfig", req.Provider)
	}

	// Step 1b: Merge default addons if no addons specified.
	if len(req.Addons) == 0 && len(o.defaultAddons) > 0 {
		req.Addons = make(map[string]bool)
		for k, v := range o.defaultAddons {
			req.Addons[k] = v
		}
	}

	// Step 2: Check whether the cluster already exists in ArgoCD.
	// If it does, we adopt it (skip ArgoCD registration) instead of returning an error.
	clusters, err := o.argocd.ListClusters(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking for existing cluster %q: %w", req.Name, err)
	}
	alreadyInArgoCD := false
	for _, c := range clusters {
		if c.Name == req.Name {
			alreadyInArgoCD = true
			break
		}
	}

	result := &RegisterClusterResult{
		Cluster: ClusterResult{
			Name:   req.Name,
			Addons: req.Addons,
		},
		Adopted: alreadyInArgoCD,
	}
	var steps []string

	// Step 3: Acquire credentials.
	// V125-1.1: when Provider == "kubeconfig" the caller supplies the
	// kubeconfig YAML inline on the request, so we parse it directly and
	// skip o.credProvider.GetCredentials (the credProvider may legitimately
	// be nil in this path — generic-K8s registration must not require an
	// AWS-SM/k8s-secrets backend to be configured).  For every other
	// provider we keep the original credProvider lookup.
	var creds *providers.Kubeconfig
	if req.Provider == "kubeconfig" {
		var parseErr error
		creds, parseErr = providers.ParseInlineKubeconfig(req.Kubeconfig)
		if parseErr != nil {
			return nil, fmt.Errorf("parsing inline kubeconfig for cluster %q: %w", req.Name, parseErr)
		}
		steps = append(steps, "parse_kubeconfig")
	} else {
		if o.credProvider == nil {
			return nil, fmt.Errorf("credentials provider not configured (required for provider %q)", req.Provider)
		}
		// If an explicit secretPath is provided, use it directly (bypasses prefix logic).
		credLookupName := req.Name
		if req.SecretPath != "" {
			credLookupName = req.SecretPath
		}
		var fetchErr error
		creds, fetchErr = o.credProvider.GetCredentials(credLookupName)
		if fetchErr != nil {
			return nil, fmt.Errorf("fetching credentials for cluster %q: %w", req.Name, fetchErr)
		}
		steps = append(steps, "fetch_credentials")
	}
	result.Cluster.Server = creds.Server

	// Step 3a: Verify connectivity via Stage 1 (secret CRUD cycle on remote cluster).
	// Only runs when the remote client factory is available (always in production,
	// may be nil in legacy tests that don't call SetSecretManagement).
	if o.remoteClientFn != nil && creds.Raw != nil {
		remoteClient, clientErr := o.remoteClientFn(creds.Raw)
		if clientErr != nil {
			return nil, fmt.Errorf("building remote client for verification of cluster %q: %w", req.Name, clientErr)
		}
		verifyResult := verify.Stage1(ctx, remoteClient, verify.TestNamespace())
		result.Verification = &verifyResult
		if verifyResult.ServerVersion != "" {
			result.Cluster.ServerVersion = verifyResult.ServerVersion
		}
		if !verifyResult.Success {
			return nil, fmt.Errorf("cluster %q connectivity verification failed: [%s] %s",
				req.Name, verifyResult.ErrorCode, verifyResult.ErrorMessage)
		}
		steps = append(steps, "verify_stage1")
		slog.Info("Stage 1 verification passed", "cluster", req.Name, "version", verifyResult.ServerVersion)
	}

	// Dry-run exit point: return a preview of what would happen, with zero side effects.
	//
	// V125-1.4 (BUG-049): all slice fields are initialized to non-nil empty
	// slices when there is no data, so the JSON response carries `[]` (not
	// `null`) for every field. The ClustersOverview preview panel reads
	// `.length` on these arrays and crashed (caught by the ErrorBoundary)
	// when the V125-1.1 kubeconfig path with no addons selected returned
	// nil arrays. Both providers now share the same result-construction
	// shape — see TestRegisterCluster_DryRun_Kubeconfig_ShapeParity.
	if req.DryRun {
		// Compute effective addon names — start from a non-nil empty slice
		// so a request with no enabled addons still serializes as `[]`.
		addonNames := []string{}
		for a, enabled := range req.Addons {
			if enabled {
				addonNames = append(addonNames, a)
			}
		}

		valuesPath := path.Join(o.paths.ClusterValues, req.Name+".yaml")
		clusterAddonsPath := o.paths.ManagedClusters
		if clusterAddonsPath == "" {
			clusterAddonsPath = "configuration/managed-clusters.yaml"
		}

		// Determine file actions: "create" or "update" based on whether the file already exists.
		filePreviews := []FilePreview{
			{Path: valuesPath, Action: o.fileAction(ctx, valuesPath)},
			{Path: clusterAddonsPath, Action: o.fileAction(ctx, clusterAddonsPath)},
		}

		// Provider-aware PR title:
		//   - kubeconfig path:    "<commitPrefix> register cluster <name> (kubeconfig provider)"
		//   - eks/legacy path:    "<commitPrefix> register cluster <name>"
		// Mirrors the audit-event split (cluster_registered vs
		// cluster_registered_kubeconfig) so the preview tells the operator
		// which credentials path will be used.
		prTitle := fmt.Sprintf("%s register cluster %s", o.gitops.CommitPrefix, req.Name)
		if req.Provider == "kubeconfig" {
			prTitle = fmt.Sprintf("%s register cluster %s (kubeconfig provider)", o.gitops.CommitPrefix, req.Name)
		}

		// listSecretsToCreate returns nil when no secret defs are configured
		// (typical kubeconfig / kind path) or when no enabled addon matches
		// a known def. Coalesce to [] so the JSON response is uniform.
		secretsToCreate := o.listSecretsToCreate(req.Addons)
		if secretsToCreate == nil {
			secretsToCreate = []string{}
		}

		dryResult := &DryRunResult{
			EffectiveAddons: addonNames,
			FilesToWrite:    filePreviews,
			PRTitle:         prTitle,
			SecretsToCreate: secretsToCreate,
		}
		if result.Verification != nil {
			dryResult.Verification = result.Verification
		}

		result.Status = "success"
		result.DryRun = dryResult
		result.CompletedSteps = steps
		return result, nil
	}

	// V125-1-8.3 retirement: the old Step 3b — `argoSecretManager.Ensure`
	// pre-merge cluster-Secret creation — was deleted. The
	// V125-1-8 reconciler (internal/clusterreconciler) is the single
	// owner of ArgoCD cluster-Secret lifecycle. We no longer write
	// anything in the argocd namespace before the managed-clusters.yaml
	// PR is merged; the reconciler picks the new cluster up via either
	// the post-merge trigger (V125-1-8.4 wires prTracker.SetOnMergeFn)
	// or the periodic 30s safety-net tick. See design doc §12
	// V125-1-8 step 5 — this closes the BUG-058 orphan-on-PR-close bug
	// class architecturally rather than via the V125-1-7 recovery
	// surface alone.

	// Step 4: Create addon secrets on remote cluster (if configured).
	// Uses partial-success semantics: individual failures are tracked but don't stop the flow.
	secretResult, secretErr := o.createAddonSecrets(ctx, creds.Raw, req.Addons)
	if secretErr != nil {
		// Fatal error (e.g. can't connect to remote cluster at all).
		result.Status = "partial"
		result.CompletedSteps = steps
		result.FailedStep = "create_secrets"
		result.Error = secretErr.Error()
		result.Message = "Addon secret creation failed. ArgoCD registration and PR not started."
		return result, nil
	}
	if len(secretResult.Created) > 0 || len(secretResult.Failed) > 0 {
		steps = append(steps, "create_secrets")
		result.Secrets = secretResult.Created
		result.FailedSecrets = secretResult.Failed
	}

	// Step 5: Generate cluster values file and commit to Git via PR.
	// Values file must exist before ArgoCD labels trigger ApplicationSet deployment.
	//
	// Idempotency (Story 6.3): check if an open PR already exists for this cluster.
	// If so, skip PR creation and return the existing PR info.
	existingPR, existingPRErr := o.findOpenPRForCluster(ctx, req.Name, "register")
	if existingPRErr == nil && existingPR != nil {
		slog.Info("Found existing open PR for cluster registration — skipping PR creation",
			"cluster", req.Name, "pr", existingPR.URL)
		gitResult := &GitResult{
			PRUrl:  existingPR.URL,
			PRID:   existingPR.ID,
			Branch: existingPR.SourceBranch,
			Merged: false,
		}
		result.Status = "success"
		result.CompletedSteps = steps
		result.Git = gitResult
		result.Message = "Existing open PR found — skipped PR creation (idempotent retry)"
		return result, nil
	}

	// Idempotency: check if the values file already exists on the base branch.
	valuesPath := path.Join(o.paths.ClusterValues, req.Name+".yaml")
	valuesExist := false
	if _, valuesCheckErr := o.git.GetFileContent(ctx, valuesPath, o.gitops.BaseBranch); valuesCheckErr == nil {
		valuesExist = true
		slog.Info("Values file already exists — will update instead of create",
			"cluster", req.Name, "path", valuesPath)
	}
	_ = valuesExist // Used for logging; file is always (re)generated to ensure correctness.

	var catalog []models.AddonCatalogEntry
	catalogData, catalogErr := o.git.GetFileContent(ctx, "configuration/addons-catalog.yaml", o.gitops.BaseBranch)
	if catalogErr == nil && catalogData != nil {
		catalog, _ = parseAddonsCatalog(catalogData)
	}
	valuesContent := generateClusterValues(req.Name, req.Region, req.Addons, catalog)

	// Read cluster-addons.yaml and add this cluster's entry so the /api/v1/clusters
	// endpoint recognises the cluster as managed after the PR merges.
	clusterAddonsPath := o.paths.ManagedClusters
	if clusterAddonsPath == "" {
		clusterAddonsPath = "configuration/managed-clusters.yaml"
	}
	clusterAddonsData, clusterAddonsErr := o.git.GetFileContent(ctx, clusterAddonsPath, o.gitops.BaseBranch)
	if clusterAddonsErr != nil {
		// File doesn't exist yet — bootstrap a minimal document.
		slog.Info("managed-clusters.yaml not found, bootstrapping", "cluster", req.Name)
		clusterAddonsData = []byte("clusters:\n")
	}

	// Build labels in "true"/"false" format to match cluster-addons.yaml convention.
	clusterLabels := make(map[string]string, len(req.Addons))
	for addon, enabled := range req.Addons {
		if enabled {
			clusterLabels[addon] = "true"
		} else {
			clusterLabels[addon] = "false"
		}
	}

	// AddClusterEntry is itself idempotent — if the cluster already exists, it returns
	// the data unchanged (no error). This makes retry-after-partial-failure safe.
	updatedClusterAddons, addEntryErr := gitops.AddClusterEntry(clusterAddonsData, gitops.ClusterEntryInput{
		Name:       req.Name,
		Region:     req.Region,
		SecretPath: req.SecretPath,
		Labels:     clusterLabels,
	})
	if addEntryErr != nil {
		slog.Error("failed to add cluster entry to cluster-addons.yaml — continuing with values file only",
			"cluster", req.Name, "error", addEntryErr,
		)
		// Non-fatal: fall back to values-file-only commit so registration still proceeds.
		updatedClusterAddons = nil
	}

	files := map[string][]byte{
		valuesPath: valuesContent,
	}
	if updatedClusterAddons != nil {
		files[clusterAddonsPath] = updatedClusterAddons
	}

	gitResult, err := o.commitChangesWithMeta(ctx, files, nil, fmt.Sprintf("register cluster %s", req.Name), PRMetadata{
		OperationCode:     "register-cluster",
		Cluster:           req.Name,
		Title:             fmt.Sprintf("Register cluster %s", req.Name),
		AutoMergeOverride: req.AutoMerge, // BUG-031: per-request override
	})
	if err != nil {
		if gitResult != nil {
			// PR created but merge failed — partial success with PR info.
			slog.Error("RegisterCluster: PR opened but auto-merge failed",
				"cluster", req.Name, "pr_url", gitResult.PRUrl, "error", err)
			result.Status = "partial"
			result.CompletedSteps = steps
			result.FailedStep = "pr_merge"
			result.Error = err.Error()
			result.Message = "Secrets created and PR opened, but auto-merge failed. Merge manually then ArgoCD registration will be needed: " + gitResult.PRUrl
			result.Git = gitResult
			return result, nil
		}
		// Complete Git failure (couldn't even create PR).
		// V125-1-13.y.3: surface the underlying error so operators can
		// distinguish branch-create / batch-write / PR-create failures
		// without a full debug build. Previously the failure mode was
		// invisible — the response carries it in result.Error but the
		// server logs were silent, masking diagnosis during e2e triage.
		slog.Error("RegisterCluster: git commit failed",
			"cluster", req.Name, "error", err)
		result.Status = "partial"
		result.CompletedSteps = steps
		result.FailedStep = "git_commit"
		result.Error = err.Error()
		result.Message = "Secrets created but values file commit failed. ArgoCD registration not started. Manual Git commit required."
		return result, nil
	}
	steps = append(steps, "git_commit")
	gitResult.ValuesFile = valuesPath

	if gitResult != nil && !gitResult.Merged {
		slog.Info("PR created but not auto-merged — cluster will appear as managed after PR is merged",
			"cluster", req.Name, "pr", gitResult.PRUrl)
	}

	// V125-1-8.3 retirement: the old Step 6 — direct
	// `o.argocd.RegisterCluster(...)` pre-merge API call — was deleted.
	// The reconciler now owns ArgoCD-side registration. The adoption
	// short-circuit is preserved as a no-op log line (the cluster is
	// already in ArgoCD; the reconciler will mark it managed-by sharko
	// on its next tick via the adoption code path in
	// argosecrets.Manager.Ensure).
	if alreadyInArgoCD {
		slog.Info("cluster already in ArgoCD — reconciler will adopt on next tick", "cluster", req.Name)
		steps = append(steps, "argocd_adopt")
	} else {
		steps = append(steps, "reconciler_handoff")
	}

	// V125-1-8.3: nudge the reconciler so post-merge convergence happens
	// immediately rather than waiting for the 30s safety-net tick. When
	// auto-merge is off the PR is still open — the nudge is harmless
	// (reconciler's poll will find no new managed-clusters.yaml entry
	// until the user merges). The seam is wired in V125-1-8.4 via
	// cmd/sharko/serve.go calling SetReconcilerTrigger; tests inject a
	// counter to assert it fires on every register path.
	o.fireReconcilerTrigger()

	result.CompletedSteps = steps
	result.Git = gitResult

	// Determine final status. With Step 6 retired the only partial-failure
	// modes left are addon-secret failures (Step 4) and Git failures
	// (already handled above with their own early returns).
	if len(result.FailedSecrets) > 0 {
		result.Status = "partial"
		result.FailedStep = "create_secrets"
		result.Message = fmt.Sprintf("Registration completed but %d addon secret(s) failed to create.", len(result.FailedSecrets))
	} else {
		result.Status = "success"
	}
	return result, nil
}

// DeregisterCluster removes a cluster from ArgoCD and deletes its values file.
// The order is designed to drain ArgoCD-managed addons before hard-deleting the cluster:
//
//  1. Remove addon labels from ArgoCD (ApplicationSet prunes addon Applications)
//  2. Brief wait to give ArgoCD time to react (simplified — no full prune polling)
//  3. Delete Sharko-managed secrets from remote cluster (best-effort)
//  4. Delete the ArgoCD cluster registration
//  5. Delete values file via PR
func (o *Orchestrator) DeregisterCluster(ctx context.Context, name string, serverURL string) (*RegisterClusterResult, error) {
	result := &RegisterClusterResult{
		Cluster: ClusterResult{Name: name, Server: serverURL},
	}

	// Step 1: Disable all addon labels on the ArgoCD cluster so ApplicationSet
	// stops managing addons (prunes the generated Applications).
	// We set all known addon labels to "disabled" rather than removing them,
	// because UpdateClusterLabels merges — an empty map would be a no-op.
	disableLabels := make(map[string]string)
	if o.secretDefs != nil {
		for addonName := range o.secretDefs {
			disableLabels[addonName] = "disabled"
		}
	}
	// Also read the cluster's current labels from ArgoCD to catch addons not in secretDefs.
	clusters, listErr := o.argocd.ListClusters(ctx)
	if listErr == nil {
		for _, c := range clusters {
			if c.Name == name {
				// Any label that looks like an addon (not a system label) gets disabled.
				for k := range c.Labels {
					if k != "name" && k != "server" && k != "env" && k != "region" {
						disableLabels[k] = "disabled"
					}
				}
				break
			}
		}
	}
	if len(disableLabels) > 0 {
		if err := o.argocd.UpdateClusterLabels(ctx, serverURL, disableLabels); err != nil {
			return nil, fmt.Errorf("disabling addon labels on cluster %q in ArgoCD: %w", name, err)
		}
	}

	// Step 2: Brief wait to give ArgoCD time to react and begin pruning addon Applications.
	// A full prune-poll (via GetClusterApplications) would be more correct but is left as
	// a future improvement; this sleep is a deliberate simplification.
	if o.drainSleep > 0 {
		time.Sleep(o.drainSleep)
	}

	// Step 3: Delete Sharko-managed secrets from remote cluster (best-effort).
	if o.credProvider != nil {
		creds, credErr := o.credProvider.GetCredentials(name)
		if credErr == nil {
			o.deleteAllAddonSecrets(ctx, creds.Raw) // best-effort, don't fail deregister for this
		}
	}

	// Step 4: Delete cluster registration from ArgoCD.
	if err := o.argocd.DeleteCluster(ctx, serverURL); err != nil {
		return nil, fmt.Errorf("deleting cluster %q from ArgoCD: %w", name, err)
	}

	// Step 5: Delete values file from Git.
	valuesPath := path.Join(o.paths.ClusterValues, name+".yaml")
	gitResult, err := o.commitChangesWithMeta(ctx, nil, []string{valuesPath}, fmt.Sprintf("deregister cluster %s", name), PRMetadata{
		OperationCode: "remove-cluster",
		Cluster:       name,
		Title:         fmt.Sprintf("Deregister cluster %s", name),
	})
	if err != nil {
		if gitResult != nil {
			// PR created but merge failed — partial success with PR info.
			result.Status = "partial"
			result.CompletedSteps = []string{"remove_argocd_labels", "delete_from_argocd"}
			result.FailedStep = "pr_merge"
			result.Error = err.Error()
			result.Message = fmt.Sprintf("Cluster %s removed from ArgoCD and PR created, but auto-merge failed. Merge manually: %s", name, gitResult.PRUrl)
			result.Git = gitResult
			return result, nil
		}
		// Complete Git failure (couldn't even create PR).
		result.Status = "partial"
		result.CompletedSteps = []string{"remove_argocd_labels", "delete_from_argocd"}
		result.FailedStep = "git_commit"
		result.Error = err.Error()
		result.Message = fmt.Sprintf("Cluster %s removed from ArgoCD but values file deletion failed. The values file at %s may need manual cleanup.", name, valuesPath)
		return result, nil
	}

	result.Status = "success"
	result.Git = gitResult
	return result, nil
}

// UpdateClusterAddons updates addon labels in ArgoCD and the values file in Git.
// Secrets must exist for enabled addons before ArgoCD labels trigger deployment:
//
//  1. Fetch credentials (needed for secret operations on the remote cluster)
//  2. Create secrets for newly enabled addons (non-best-effort: abort if fails)
//  3. Delete secrets for disabled addons (best-effort: continue on failure)
//  4. Update values file via PR
//  5. Update ArgoCD labels (all at once — LAST, after secrets and values exist)
//
// BUG-031: autoMergeOverride is the per-request auto-merge decision (nil =
// fall back to o.gitops.PRAutoMerge). Passed through to commitChangesWithMeta
// via PRMetadata.AutoMergeOverride — never mutates o.gitops.PRAutoMerge.
func (o *Orchestrator) UpdateClusterAddons(ctx context.Context, name string, serverURL string, region string, addons map[string]bool, autoMergeOverride *bool) (*RegisterClusterResult, error) {
	result := &RegisterClusterResult{
		Cluster: ClusterResult{Name: name, Server: serverURL, Addons: addons},
	}

	// Step 1: Fetch credentials if provider is configured (needed for secret operations).
	var rawKubeconfig []byte
	if o.credProvider != nil {
		creds, credErr := o.credProvider.GetCredentials(name)
		if credErr == nil {
			rawKubeconfig = creds.Raw
		}
	}

	// Step 2: Create secrets for enabled addons before ArgoCD sees them.
	// Fatal errors (can't connect) abort; individual secret failures are recorded as partial.
	if rawKubeconfig != nil {
		enabledAddons := make(map[string]bool)
		for a, e := range addons {
			if e {
				enabledAddons[a] = true
			}
		}
		secretRes, secretErr := o.createAddonSecrets(ctx, rawKubeconfig, enabledAddons)
		if secretErr != nil {
			return nil, fmt.Errorf("creating secrets for enabled addons on cluster %q: %w", name, secretErr)
		}
		result.Secrets = secretRes.Created
		result.FailedSecrets = secretRes.Failed
	}

	// Step 3: Delete secrets for disabled addons (best-effort — continue on failure).
	if rawKubeconfig != nil {
		disabledAddons := make(map[string]bool)
		for a, e := range addons {
			if !e {
				disabledAddons[a] = false
			}
		}
		o.deleteAddonSecrets(ctx, rawKubeconfig, disabledAddons) //nolint:errcheck // best-effort
	}

	// Step 4: Update values file in Git.
	var catalog []models.AddonCatalogEntry
	catalogData, catalogErr := o.git.GetFileContent(ctx, "configuration/addons-catalog.yaml", o.gitops.BaseBranch)
	if catalogErr == nil && catalogData != nil {
		catalog, _ = parseAddonsCatalog(catalogData)
	}
	valuesContent := generateClusterValues(name, region, addons, catalog)
	valuesPath := path.Join(o.paths.ClusterValues, name+".yaml")

	files := map[string][]byte{
		valuesPath: valuesContent,
	}

	gitResult, err := o.commitChangesWithMeta(ctx, files, nil, fmt.Sprintf("update addons for cluster %s", name), PRMetadata{
		OperationCode:     "update-cluster",
		Cluster:           name,
		Title:             fmt.Sprintf("Update addons for cluster %s", name),
		AutoMergeOverride: autoMergeOverride, // BUG-031: per-request override
	})
	if err != nil {
		if gitResult != nil {
			// PR created but merge failed — partial success with PR info.
			result.Status = "partial"
			result.CompletedSteps = []string{"create_secrets", "delete_secrets"}
			result.FailedStep = "pr_merge"
			result.Error = err.Error()
			result.Message = fmt.Sprintf("Secrets updated for cluster %s and PR created, but auto-merge failed. Merge manually then update ArgoCD labels: %s", name, gitResult.PRUrl)
			result.Git = gitResult
			return result, nil
		}
		// Complete Git failure (couldn't even create PR).
		result.Status = "partial"
		result.CompletedSteps = []string{"create_secrets", "delete_secrets"}
		result.FailedStep = "git_commit"
		result.Error = err.Error()
		result.Message = fmt.Sprintf("Secrets updated for cluster %s but Git commit failed. ArgoCD labels not updated. Values file at %s may be stale.", name, valuesPath)
		return result, nil
	}

	gitResult.ValuesFile = valuesPath

	// Step 5: Update ArgoCD cluster labels (LAST — secrets and values file exist by now).
	labels := make(map[string]string)
	for addon, enabled := range addons {
		if enabled {
			labels[addon] = "enabled"
		} else {
			labels[addon] = "disabled"
		}
	}

	if err := o.argocd.UpdateClusterLabels(ctx, serverURL, labels); err != nil {
		result.Status = "partial"
		result.CompletedSteps = append(result.CompletedSteps, "git_commit")
		result.FailedStep = "update_argocd_labels"
		result.Error = err.Error()
		result.Message = fmt.Sprintf("Secrets updated and values PR merged for cluster %s, but ArgoCD label update failed. Labels may be stale.", name)
		result.Git = gitResult
		return result, nil
	}

	result.Status = "success"
	result.CompletedSteps = append(result.CompletedSteps, "git_commit", "update_argocd_labels")
	result.Git = gitResult
	return result, nil
}

// RefreshClusterCredentials validates that fresh credentials are reachable
// in the credentials provider and nudges the V125-1-8 reconciler so the
// ArgoCD cluster Secret is updated immediately rather than on the next
// 30s safety-net tick.
//
// V125-1-8.3 retirement: the previous implementation called
// o.argocd.RegisterCluster directly to upsert the Secret with fresh
// CAData/Token. That direct write is now owned by the reconciler — when
// it picks the cluster up, it re-fetches credentials via the same
// credProvider this function probes and writes the new Secret payload
// via argosecrets.Manager.Ensure. The probe is kept so the API endpoint
// can fail fast (404 / 401) without dispatching a no-op reconcile.
func (o *Orchestrator) RefreshClusterCredentials(_ context.Context, name string, _ string) error {
	if o.credProvider == nil {
		// No credProvider configured (e.g. kubeconfig-only deployment) —
		// nothing to refresh; let the reconciler drive on its own cadence.
		o.fireReconcilerTrigger()
		return nil
	}
	if _, err := o.credProvider.GetCredentials(name); err != nil {
		return fmt.Errorf("fetching fresh credentials for cluster %q: %w", name, err)
	}
	// Probe succeeded — hand off to reconciler. Secret write happens
	// inside reconciler.pollOnce, which re-reads the credentials and
	// reconciles the Secret payload via argosecrets.Manager.Ensure.
	o.fireReconcilerTrigger()
	return nil
}

// addonsCatalogFile mirrors the YAML structure of addons-catalog.yaml.
// Duplicated here to avoid an import cycle with the config package.
type addonsCatalogFile struct {
	ApplicationSets []models.AddonCatalogEntry `yaml:"applicationsets"`
}

// parseAddonsCatalog unmarshals raw addons-catalog.yaml bytes into a slice of catalog entries.
func parseAddonsCatalog(data []byte) ([]models.AddonCatalogEntry, error) {
	var file addonsCatalogFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parsing addons-catalog.yaml: %w", err)
	}
	return file.ApplicationSets, nil
}

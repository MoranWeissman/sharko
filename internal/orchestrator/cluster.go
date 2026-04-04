package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"time"
)

// ErrClusterAlreadyExists is returned when attempting to register a cluster
// that already exists in ArgoCD.
var ErrClusterAlreadyExists = errors.New("cluster already exists")

var validClusterName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)

// RegisterCluster orchestrates cluster registration. Secrets and values must
// exist before ArgoCD sees the cluster, so ArgoCD registration is LAST:
//
//  1. Validate input
//  1b. Merge default addons
//  2. Check for duplicate cluster in ArgoCD (409 if exists)
//  3. Fetch credentials from provider
//  4. Create addon secrets on remote cluster (if configured)
//  5. Generate values file + commit via PR (create + auto-merge if configured)
//  6. Register cluster in ArgoCD with addon labels (LAST — secrets and values
//     file exist by this point)
func (o *Orchestrator) RegisterCluster(ctx context.Context, req RegisterClusterRequest) (*RegisterClusterResult, error) {
	// Step 1: Validate input.
	if req.Name == "" {
		return nil, fmt.Errorf("cluster name is required")
	}
	if !validClusterName.MatchString(req.Name) {
		return nil, fmt.Errorf("invalid cluster name %q: must be alphanumeric with hyphens, starting with an alphanumeric character", req.Name)
	}

	// Step 1b: Merge default addons if no addons specified.
	if len(req.Addons) == 0 && len(o.defaultAddons) > 0 {
		req.Addons = make(map[string]bool)
		for k, v := range o.defaultAddons {
			req.Addons[k] = v
		}
	}

	// Step 2: Check for duplicate — cluster must not already exist in ArgoCD.
	clusters, err := o.argocd.ListClusters(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking for existing cluster %q: %w", req.Name, err)
	}
	for _, c := range clusters {
		if c.Name == req.Name {
			return nil, fmt.Errorf("%w: %q in ArgoCD", ErrClusterAlreadyExists, req.Name)
		}
	}

	result := &RegisterClusterResult{
		Cluster: ClusterResult{
			Name:   req.Name,
			Addons: req.Addons,
		},
	}
	var steps []string

	// Step 3: Fetch credentials from provider.
	creds, err := o.credProvider.GetCredentials(req.Name)
	if err != nil {
		return nil, fmt.Errorf("fetching credentials for cluster %q: %w", req.Name, err)
	}
	steps = append(steps, "fetch_credentials")
	result.Cluster.Server = creds.Server

	// Step 4: Create addon secrets on remote cluster (if configured).
	// Secrets must exist before ArgoCD sees the cluster — if this fails, abort early.
	secretNames, secretErr := o.createAddonSecrets(ctx, creds.Raw, req.Addons)
	if secretErr != nil {
		result.Status = "partial"
		result.CompletedSteps = steps
		result.Secrets = secretNames
		result.FailedStep = "create_secrets"
		result.Error = secretErr.Error()
		result.Message = "Addon secret creation failed. ArgoCD registration and PR not started."
		return result, nil
	}
	if len(secretNames) > 0 {
		steps = append(steps, "create_secrets")
		result.Secrets = secretNames
	}

	// Step 5: Generate cluster values file and commit to Git via PR.
	// Values file must exist before ArgoCD labels trigger ApplicationSet deployment.
	valuesContent := generateClusterValues(req.Name, req.Region, req.Addons)
	valuesPath := path.Join(o.paths.ClusterValues, req.Name+".yaml")

	files := map[string][]byte{
		valuesPath: valuesContent,
	}

	gitResult, err := o.commitChanges(ctx, files, nil, fmt.Sprintf("register cluster %s", req.Name))
	if err != nil {
		if gitResult != nil {
			// PR created but merge failed — partial success with PR info.
			result.Status = "partial"
			result.CompletedSteps = steps
			result.FailedStep = "pr_merge"
			result.Error = err.Error()
			result.Message = "Secrets created and PR opened, but auto-merge failed. Merge manually then ArgoCD registration will be needed: " + gitResult.PRUrl
			result.Git = gitResult
			return result, nil
		}
		// Complete Git failure (couldn't even create PR).
		result.Status = "partial"
		result.CompletedSteps = steps
		result.FailedStep = "git_commit"
		result.Error = err.Error()
		result.Message = "Secrets created but values file commit failed. ArgoCD registration not started. Manual Git commit required."
		return result, nil
	}
	steps = append(steps, "git_commit")
	gitResult.ValuesFile = valuesPath

	// Step 6: Register cluster in ArgoCD with addon labels.
	// This is LAST — by now secrets exist and the values file is merged.
	labels := make(map[string]string)
	for addon, enabled := range req.Addons {
		if enabled {
			labels[addon] = "enabled"
		} else {
			labels[addon] = "disabled"
		}
	}

	if err := o.argocd.RegisterCluster(ctx, req.Name, creds.Server, creds.CAData, creds.Token, labels); err != nil {
		result.Status = "partial"
		result.CompletedSteps = steps
		result.FailedStep = "argocd_register"
		result.Error = err.Error()
		result.Message = "Secrets created and values PR merged, but ArgoCD registration failed. Register the cluster manually."
		result.Git = gitResult
		return result, nil
	}
	steps = append(steps, "argocd_register")

	result.Status = "success"
	result.CompletedSteps = steps
	result.Git = gitResult
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

	// Step 1: Remove addon labels from ArgoCD so ApplicationSet prunes addon Applications.
	if err := o.argocd.UpdateClusterLabels(ctx, serverURL, map[string]string{}); err != nil {
		return nil, fmt.Errorf("removing addon labels from cluster %q in ArgoCD: %w", name, err)
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
	gitResult, err := o.commitChanges(ctx, nil, []string{valuesPath}, fmt.Sprintf("deregister cluster %s", name))
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
func (o *Orchestrator) UpdateClusterAddons(ctx context.Context, name string, serverURL string, region string, addons map[string]bool) (*RegisterClusterResult, error) {
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
	// This is NOT best-effort — if secret creation fails, don't proceed.
	if rawKubeconfig != nil {
		enabledAddons := make(map[string]bool)
		for a, e := range addons {
			if e {
				enabledAddons[a] = true
			}
		}
		if _, err := o.createAddonSecrets(ctx, rawKubeconfig, enabledAddons); err != nil {
			return nil, fmt.Errorf("creating secrets for enabled addons on cluster %q: %w", name, err)
		}
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
	valuesContent := generateClusterValues(name, region, addons)
	valuesPath := path.Join(o.paths.ClusterValues, name+".yaml")

	files := map[string][]byte{
		valuesPath: valuesContent,
	}

	gitResult, err := o.commitChanges(ctx, files, nil, fmt.Sprintf("update addons for cluster %s", name))
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
		return nil, fmt.Errorf("updating labels on cluster %q: %w", name, err)
	}

	result.Status = "success"
	result.Git = gitResult
	return result, nil
}

// RefreshClusterCredentials fetches fresh credentials from the provider
// and re-registers the cluster in ArgoCD.
func (o *Orchestrator) RefreshClusterCredentials(ctx context.Context, name string, serverURL string) error {
	creds, err := o.credProvider.GetCredentials(name)
	if err != nil {
		return fmt.Errorf("fetching fresh credentials for cluster %q: %w", name, err)
	}

	if err := o.argocd.RegisterCluster(ctx, name, creds.Server, creds.CAData, creds.Token, nil); err != nil {
		return fmt.Errorf("re-registering cluster %q with fresh credentials: %w", name, err)
	}

	return nil
}

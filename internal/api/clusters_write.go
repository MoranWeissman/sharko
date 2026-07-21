package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/gitops"
	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/metrics"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
)

var validClusterNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)

// handleRegisterCluster godoc
//
// @Summary Register cluster
// @Description Registers a new cluster in ArgoCD and creates its GitOps configuration.
// @Description Pass "dry_run": true to preview what would happen without making changes.
// @Description Provider may be "eks" (default; uses configured secrets provider) or
// @Description "kubeconfig" (caller supplies kubeconfig YAML inline via the
// @Description "kubeconfig" field — bearer-token auth only).
// @Description Optionally set "creds_source" to state explicitly where credentials come
// @Description from: "inline-kubeconfig", "secret-kubeconfig", or "eks-token". When omitted
// @Description it is derived: provider "kubeconfig" → inline; a pasted "kubeconfig" with no
// @Description provider → inline (the paste is authoritative — it is never silently ignored
// @Description in favour of a backend lookup); anything else → backend. When set it wins
// @Description over "provider". The effective source is recorded on the cluster's
// @Description managed-clusters.yaml entry as "credsSource" and later credential fetches
// @Description (test/diagnose/secrets/addon operations) route by it, so inline-registered
// @Description clusters keep working when a secrets-backend connection is configured.
// @Description Optionally set "role_arn" (eks-token creds source only) to record the
// @Description per-cluster IAM role Sharko assumes when minting EKS tokens for this
// @Description cluster — the discovery flow passes the cross-account role that found
// @Description the cluster so token minting keeps using the same identity. Precedence
// @Description at token-mint time: the structured SM secret's own roleArn, then this
// @Description per-cluster role_arn, then the connection-level provider default.
// @Description Optionally set "connection_managed_by" to declare who owns the ArgoCD
// @Description cluster secret: "sharko" (default — Sharko writes and rotates it) or
// @Description "user" (self-managed — you create the secret by hand; Sharko never writes
// @Description it and only syncs addon labels onto it). Credentials are OPTIONAL at
// @Description registration for every connection mode (V2-cleanup-88.3 — lazy credentials):
// @Description when supplied, connectivity verification still runs; when absent (no
// @Description kubeconfig, no secret_path, no working secrets-provider lookup), it is
// @Description skipped and the cluster registers as connection-only. Sharko only asks
// @Description for credentials later, when a secret-bearing addon is enabled on the cluster.
// @Tags clusters
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body orchestrator.RegisterClusterRequest true "Cluster registration request (supports dry_run + kubeconfig fields)"
// @Success 200 {object} map[string]interface{} "Dry-run preview"
// @Success 201 {object} map[string]interface{} "Cluster registered"
// @Success 207 {object} map[string]interface{} "Partial success"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 409 {object} map[string]interface{} "Cluster already exists"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /clusters [post]
// handleRegisterCluster handles POST /api/v1/clusters — register a new cluster.
func (s *Server) handleRegisterCluster(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.register") {
		return
	}

	// V2-3 SLO surface: cluster_registration. End-to-end timing only
	// for PR 1; per-phase wiring deferred to V2-3.x. Status code is
	// captured via the responseStatusRecorder below.
	start := time.Now()
	rec := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
	w = rec
	defer func() {
		code := strconv.Itoa(rec.statusCode)
		metrics.Observe(metrics.PathClusterRegistration, "total", time.Since(start).Seconds(), logging.RequestID(r.Context()))
		metrics.IncTotal(metrics.PathClusterRegistration, code)
		if rec.statusCode >= 400 {
			metrics.IncError(metrics.PathClusterRegistration, code)
		}
	}()

	// Decode + validate request body BEFORE any upstream call so that
	// an empty body doesn't burn external API quota or return a
	// confusing upstream-error message.
	var req orchestrator.RegisterClusterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}
	if !validClusterNameRe.MatchString(req.Name) {
		writeError(w, http.StatusBadRequest, "invalid cluster name: must be alphanumeric with hyphens, starting with alphanumeric")
		return
	}

	// Provider-scoped field validation.
	//
	// The two registration paths use disjoint sets of request fields and
	// must reject cross-provider field bleed (a kubeconfig request that
	// also fills in role_arn is almost certainly a UI bug; an EKS request
	// that pastes a kubeconfig wants the kubeconfig path). Catching this
	// at the handler edge keeps the orchestrator branch logic
	// straightforward and gives the caller a clear, field-specific 400.
	//
	// creds-reframe-1: the edge checks now key on the EFFECTIVE creds
	// source, not on Provider alone. When creds_source is set it wins over
	// Provider (it is the new honest axis); when it is absent the source is
	// derived from Provider so existing requests behave exactly as before.
	// An unknown creds_source value is a caller error → 400 (the orchestrator
	// validates it too, but rejecting here avoids a wasted upstream call).
	effectiveSource, srcErr := orchestrator.ResolveCredsSource(req)
	if srcErr != nil {
		writeError(w, http.StatusBadRequest, srcErr.Error())
		return
	}

	// Connection-ownership mode (V2-cleanup-57.2). An unknown value is a
	// caller error; a typo must NOT silently fall back to Sharko-managed
	// (that would take over a connection the caller explicitly tried to
	// keep).
	if req.ConnectionManagedBy != "" &&
		req.ConnectionManagedBy != models.ConnectionManagedBySharko &&
		req.ConnectionManagedBy != models.ConnectionManagedByUser {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"unknown connection_managed_by %q (want %q or %q)",
			req.ConnectionManagedBy, models.ConnectionManagedBySharko, models.ConnectionManagedByUser))
		return
	}

	// Credentials are OPTIONAL at registration, for EVERY connection mode
	// (V2-cleanup-88.3 — lazy credentials). Sharko's only ongoing need for
	// its own spoke-cluster credentials is pushing addon secrets, and that
	// need does not exist until an addon that declares a `secrets:` block
	// is actually enabled — see EnableAddon's pre-flight gate. Registering
	// a cluster is a Git + ArgoCD-connection concern, not a credentials
	// concern, so neither an absent inline kubeconfig nor a missing/failed
	// secrets-provider lookup blocks registration here; the orchestrator
	// degrades to a connection-only registration and records the skip.
	if effectiveSource == orchestrator.CredsSourceInlineKubeconfig {
		if req.SecretPath != "" {
			writeError(w, http.StatusBadRequest, "field \"secret_path\" is not valid for an inline-kubeconfig registration")
			return
		}
		if req.Region != "" {
			writeError(w, http.StatusBadRequest, "field \"region\" is not valid for an inline-kubeconfig registration")
			return
		}
		if req.RoleARN != "" {
			writeError(w, http.StatusBadRequest, "field \"role_arn\" is not valid for an inline-kubeconfig registration")
			return
		}
	} else {
		// Backend (secret) source: secret-kubeconfig / eks-token.
		if req.Kubeconfig != "" {
			writeError(w, http.StatusBadRequest, "field \"kubeconfig\" is only valid for an inline-kubeconfig registration")
			return
		}
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	// Tier 1: operational action — service token + co-author trailer.
	ctx, git, _, err := s.GitProviderForTier(r.Context(), r, audit.Tier1)
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	orch := orchestrator.New(&s.gitMu, s.credProvider(), ac, git, s.gitopsConfig(), s.repoPaths, nil)
	s.attachPRTracker(orch)
	orch.SetSecretManagement(s.addonSecretDefs, s.secretFetcher, remoteclient.NewClientFromKubeconfig)
	orch.SetAllowInlineCredentialsFn(s.settingsStore.IsInlineCredentialsAllowed)
	if len(s.defaultAddons) > 0 {
		orch.SetDefaultAddons(s.defaultAddons)
	}
	if s.argoSecretManager != nil {
		roleARN := ""
		if s.addonSecretCfg() != nil {
			roleARN = s.addonSecretCfg().RoleARN
		}
		orch.SetArgoSecretManager(&argoManagerAdapter{mgr: s.argoSecretManager}, roleARN)
	}
	result, err := orch.RegisterCluster(ctx, req)
	if err != nil {
		// Admin kill switch for inline credential paste (V2-cleanup-89.6):
		// allow_inline_credentials is false and this request actually
		// supplied inline kubeconfig bytes. Admin-policy rejection → 403.
		if orchestrator.IsInlineCredentialsDisabled(err) {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		// Invalid creds_source (creds-reframe-1): unknown value, or an
		// inline source with no kubeconfig. Caller error → 400.
		if orchestrator.IsInvalidCredsSource(err) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// Invalid connection_managed_by (V2-cleanup-57.2). The edge check
		// above already catches this; the orchestrator validates too so
		// non-HTTP callers get the same contract. Caller error → 400.
		if orchestrator.IsInvalidConnectionMode(err) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// Referential-integrity rejection (V2-cleanup-22): one or more
		// requested addons are not in the catalog. Caller error → 422 with
		// the orchestrator's message listing the bad name(s).
		if orchestrator.IsAddonNotInCatalog(err) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// Dry-run: return preview without side effects.
	if req.DryRun {
		writeJSON(w, http.StatusOK, result)
		return
	}

	if s.clusterRecon != nil {
		s.clusterRecon.Trigger()
	}

	// Record verification observation if Stage1 ran.
	if result.Verification != nil && s.obsStore != nil {
		if err := s.obsStore.RecordTestResult(ctx, req.Name, *result.Verification); err != nil {
			slog.Error("failed to record verification observation during registration",
				"request_id", logging.RequestID(ctx),
				"cluster", req.Name, "error", err)
		}
	}

	status := http.StatusCreated
	if result.Status == "partial" {
		status = http.StatusMultiStatus
	}

	// Distinct audit event for the inline-kubeconfig registration path so
	// audit history can tell backend (EKS-via-AWS-SM / secret-kubeconfig)
	// registrations from inline-kubeconfig ones without parsing the
	// resource string. Keys on the effective creds source so an explicit
	// creds_source is honored even when Provider is left blank.
	auditEvent := "cluster_registered"
	if effectiveSource == orchestrator.CredsSourceInlineKubeconfig {
		auditEvent = "cluster_registered_kubeconfig"
	}
	audit.Enrich(ctx, audit.Fields{
		Event:    auditEvent,
		Resource: fmt.Sprintf("cluster:%s", req.Name),
	})

	writeJSON(w, status, result)
}

// handleDeregisterCluster godoc
//
// @Summary Remove cluster
// @Description Removes a cluster with configurable cleanup scope.
// @Description Pass cleanup=all (default) to remove Git config and clean up ArgoCD + remote secrets.
// @Description Pass cleanup=git to remove Git config only. Pass cleanup=none for managed-clusters entry only.
// @Description Requires yes=true for confirmation. Pass dry_run=true to preview.
// @Tags clusters
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Param body body orchestrator.RemoveClusterRequest true "Removal request"
// @Success 200 {object} orchestrator.RemoveClusterResult "Cluster removed (or dry-run preview)"
// @Success 207 {object} orchestrator.RemoveClusterResult "Partial success"
// @Failure 400 {object} map[string]interface{} "Bad request or missing confirmation"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /clusters/{name} [delete]
// handleDeregisterCluster handles DELETE /api/v1/clusters/{name} — remove a cluster.
func (s *Server) handleDeregisterCluster(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.remove") {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	// Tier 1: operational action — service token + co-author trailer.
	ctx, git, _, err := s.GitProviderForTier(r.Context(), r, audit.Tier1)
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	// Parse request body for cleanup/confirmation options.
	var req orchestrator.RemoveClusterRequest
	if r.Body != nil && r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
	}
	req.Name = name

	orch := orchestrator.New(&s.gitMu, s.credProvider(), ac, git, s.gitopsConfig(), s.repoPaths, nil)
	s.attachPRTracker(orch)
	orch.SetSecretManagement(s.addonSecretDefs, s.secretFetcher, remoteclient.NewClientFromKubeconfig)
	if s.argoSecretManager != nil {
		roleARN := ""
		if s.addonSecretCfg() != nil {
			roleARN = s.addonSecretCfg().RoleARN
		}
		orch.SetArgoSecretManager(&argoManagerAdapter{mgr: s.argoSecretManager}, roleARN)
	}

	result, orchErr := orch.RemoveCluster(ctx, req)
	if orchErr != nil {
		// Check for confirmation error.
		if orchErr.Error() == "confirmation required: set yes: true in request body" {
			writeError(w, http.StatusBadRequest, orchErr.Error())
			return
		}
		writeError(w, http.StatusBadGateway, orchErr.Error())
		return
	}

	// Dry-run: return preview without side effects.
	if req.DryRun {
		writeJSON(w, http.StatusOK, result)
		return
	}

	// Trigger reconciler after removal.
	if s.clusterRecon != nil {
		s.clusterRecon.Trigger()
	}

	audit.Enrich(ctx, audit.Fields{
		Event:    "cluster_deregistered",
		Resource: fmt.Sprintf("cluster:%s", name),
	})

	status := http.StatusOK
	if result.Status == "partial" {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, result)
}

// handleUpdateClusterAddons godoc
//
// @Summary Update cluster addons or settings
// @Description Updates the addon selections (enabled/disabled) and/or cluster settings (secret_path) for a specific cluster
// @Tags clusters
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Param body body map[string]interface{} true "Cluster update request with addons map and/or secret_path"
// @Success 200 {object} map[string]interface{} "Addons updated"
// @Success 207 {object} map[string]interface{} "Partial success"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Cluster not found"
// @Failure 422 {object} map[string]interface{} "Addon not in catalog"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /clusters/{name} [patch]
// handleUpdateClusterAddons handles PATCH /api/v1/clusters/{name} — update addon labels.
func (s *Server) handleUpdateClusterAddons(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.update-addons") {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	// Tier 1: operational action — service token + co-author trailer.
	ctx, git, _, err := s.GitProviderForTier(r.Context(), r, audit.Tier1)
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	// Decode body into the named type so the per-request auto_merge
	// override flows through with stable field tags.
	var req orchestrator.UpdateClusterAddonsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	serverURL, err := resolveClusterServer(ctx, name, ac)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to list ArgoCD clusters: "+err.Error())
		return
	}
	if serverURL == "" {
		writeError(w, http.StatusNotFound, "cluster not found in ArgoCD: "+name)
		return
	}

	orch := orchestrator.New(&s.gitMu, s.credProvider(), ac, git, s.gitopsConfig(), s.repoPaths, nil)
	s.attachPRTracker(orch)
	orch.SetSecretManagement(s.addonSecretDefs, s.secretFetcher, remoteclient.NewClientFromKubeconfig)

	// Handle secret_path update (metadata-only change via managed-clusters.yaml).
	//
	// V2-cleanup-23: this used to hand-roll branch+commit+PR+track+auto-merge
	// +branch-cleanup inline, which is exactly the kind of out-of-funnel
	// re-implementation that drifts from the shared auto-merge behavior. It
	// now routes through orch.CommitFilesAsPRWithMeta → commitChangesWithMeta
	// (the funnel), so the per-request override, connection default, PR
	// tracking, and post-merge branch cleanup all come from the one shared
	// code path via the prMeta builder.
	if req.SecretPath != nil {
		managedPath := s.repoPaths.ManagedClusters
		if managedPath == "" {
			managedPath = "configuration/managed-clusters.yaml"
		}
		mcData, err := git.GetFileContent(ctx, managedPath, s.gitopsConfig().BaseBranch)
		if err != nil {
			writeError(w, http.StatusBadGateway, "reading managed-clusters.yaml: "+err.Error())
			return
		}
		updated, err := gitops.UpdateClusterSecretPath(mcData, name, *req.SecretPath)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		user := r.Header.Get("X-Sharko-User")
		if user == "" {
			user = "system"
		}

		// Dry-run: return preview without side effects.
		if req.DryRun {
			filePreviews := []orchestrator.FilePreview{
				{Path: managedPath, Action: "update"},
			}
			dryRunResult := &orchestrator.DryRunResult{
				EffectiveAddons: []string{},
				FilesToWrite:    filePreviews,
				PRTitle:         fmt.Sprintf("Update secret_path for cluster %s", name),
				SecretsToCreate: []string{},
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"status":  "success",
				"dry_run": dryRunResult,
			})
			return
		}

		files := map[string][]byte{managedPath: updated}
		gitResult, prErr := orch.CommitFilesAsPRWithMeta(ctx, files,
			fmt.Sprintf("update secret_path for cluster %s", name),
			orchestrator.PRMetadata{
				OperationCode:     "update-cluster",
				Cluster:           name,
				Title:             fmt.Sprintf("Update secret_path for cluster %s", name),
				User:              user,
				Source:            "api",
				AutoMergeOverride: req.AutoMerge,
			})
		if prErr != nil {
			writeError(w, http.StatusBadGateway, "creating PR: "+prErr.Error())
			return
		}

		// If no addons to update, return early with the secret_path result.
		if len(req.Addons) == 0 {
			prURL := ""
			if gitResult != nil {
				prURL = gitResult.PRUrl
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"status":  "success",
				"message": fmt.Sprintf("secret_path updated for cluster %s", name),
				"pr_url":  prURL,
			})
			return
		}
	}
	// Region is empty — PATCH only updates addon labels, not cluster metadata.
	// Region is set during RegisterCluster and not exposed via the update API.
	// Pass per-request auto_merge override (nil = fall back to
	// connection-level PRAutoMerge). The orchestrator forwards it to
	// commitChangesWithMeta via PRMetadata.AutoMergeOverride.
	result, err := orch.UpdateClusterAddons(ctx, name, serverURL, "", req.Addons, req.AutoMerge, req.DryRun)
	if err != nil {
		if orchestrator.IsAddonNotInCatalog(err) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// Dry-run: return preview without side effects.
	if req.DryRun {
		writeJSON(w, http.StatusOK, result)
		return
	}

	// Trigger the ArgoCD secrets reconciler to pick up the new addon state.
	// We do NOT call Manager.Ensure() directly here because this handler does not have
	// access to the cluster's Region (set at RegisterCluster time), and omitting Region
	// would produce a malformed execProviderConfig in the ArgoCD cluster secret.
	// The reconciler reads the full cluster spec from cluster-addons.yaml (including region)
	// and will update the secret within its next cycle (default: 3 minutes).
	if s.clusterRecon != nil {
		s.clusterRecon.Trigger()
	}

	audit.Enrich(ctx, audit.Fields{
		Event:    "cluster_updated",
		Resource: fmt.Sprintf("cluster:%s", name),
	})

	status := http.StatusOK
	if result.Status == "partial" {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, result)
}

// handleRefreshClusterCredentials godoc
//
// @Summary Refresh cluster credentials
// @Description Rotates and re-syncs the cluster credentials from the secrets provider to ArgoCD
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Success 200 {object} map[string]interface{} "Credentials refreshed"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Cluster not found"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Failure 503 {object} map[string]interface{} "Credentials provider not configured"
// @Router /clusters/{name}/refresh [post]
// handleRefreshClusterCredentials handles POST /api/v1/clusters/{name}/refresh.
func (s *Server) handleRefreshClusterCredentials(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.refresh-credentials") {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}

	if s.credProvider() == nil {
		writeMissingProviderError(w)
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	serverURL, err := resolveClusterServer(r.Context(), name, ac)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to list ArgoCD clusters: "+err.Error())
		return
	}
	if serverURL == "" {
		writeError(w, http.StatusNotFound, "cluster not found in ArgoCD: "+name)
		return
	}

	orch := orchestrator.New(&s.gitMu, s.credProvider(), ac, nil, s.gitopsConfig(), s.repoPaths, nil)
	s.attachPRTracker(orch)
	if err := orch.RefreshClusterCredentials(r.Context(), name, serverURL); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "cluster_credentials_refreshed",
		Resource: fmt.Sprintf("cluster:%s", name),
	})
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "success",
		"message": "credentials refreshed for cluster " + name,
	})
}

// resolveClusterServer looks up a cluster by name in ArgoCD and returns its server URL.
// Returns empty string if not found.
func resolveClusterServer(ctx context.Context, name string, ac *argocd.Client) (string, error) {
	clusters, err := ac.ListClusters(ctx)
	if err != nil {
		return "", err
	}
	for _, c := range clusters {
		if c.Name == name {
			return c.Server, nil
		}
	}
	return "", nil
}

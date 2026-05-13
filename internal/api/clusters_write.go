package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/gitops"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/prtracker"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
)

var validClusterNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)

// handleRegisterCluster godoc
//
// @Summary Register cluster
// @Description Registers a new cluster in ArgoCD and creates its GitOps configuration.
// @Description Pass "dry_run": true to preview what would happen without making changes.
// @Description Provider may be "eks" (default; uses configured secrets provider) or
// @Description "kubeconfig" (V125-1.1; caller supplies kubeconfig YAML inline via the
// @Description "kubeconfig" field — bearer-token auth only).
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
// @Failure 503 {object} map[string]interface{} "Credentials provider not configured (V124-4.1; eks provider only)"
// @Router /clusters [post]
// handleRegisterCluster handles POST /api/v1/clusters — register a new cluster.
func (s *Server) handleRegisterCluster(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.register") {
		return
	}

	// V124-4.5 (BUG-019 class): decode + validate request body BEFORE any
	// upstream call. Pre-V124-4 the handler resolved ArgoCD + Git provider
	// connections first, so an empty body burned external API quota and
	// returned a confusing upstream-error message instead of the field-
	// specific validation message.
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

	// V125-1.1: provider-scoped field validation.
	//
	// The two registration paths use disjoint sets of request fields and
	// must reject cross-provider field bleed (a kubeconfig request that
	// also fills in role_arn is almost certainly a UI bug; an EKS request
	// that pastes a kubeconfig wants the kubeconfig path).  Catching this
	// at the handler edge keeps the orchestrator branch logic
	// straightforward and gives the caller a clear, field-specific 400.
	switch req.Provider {
	case "kubeconfig":
		if strings.TrimSpace(req.Kubeconfig) == "" {
			writeError(w, http.StatusBadRequest, "kubeconfig is required when provider is \"kubeconfig\"")
			return
		}
		if req.SecretPath != "" {
			writeError(w, http.StatusBadRequest, "field \"secret_path\" is not valid for provider \"kubeconfig\"")
			return
		}
		if req.Region != "" {
			writeError(w, http.StatusBadRequest, "field \"region\" is not valid for provider \"kubeconfig\"")
			return
		}
	default:
		// Empty provider == legacy EKS path.
		if req.Kubeconfig != "" {
			writeError(w, http.StatusBadRequest, "field \"kubeconfig\" is only valid when provider is \"kubeconfig\"")
			return
		}
		// EKS path still needs the secrets provider configured at
		// runtime; preserve the original V124-4.1 503 response.  The
		// kubeconfig path explicitly does NOT require credProvider —
		// that's the whole point of the inline path for kind users.
		if s.credProvider == nil {
			writeMissingProviderError(w)
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

	orch := orchestrator.New(&s.gitMu, s.credProvider, ac, git, s.gitopsCfg, s.repoPaths, nil)
	s.attachPRTracker(orch)
	orch.SetSecretManagement(s.addonSecretDefs, s.secretFetcher, remoteclient.NewClientFromKubeconfig)
	if len(s.defaultAddons) > 0 {
		orch.SetDefaultAddons(s.defaultAddons)
	}
	if s.argoSecretManager != nil {
		roleARN := ""
		if s.providerCfg != nil {
			roleARN = s.providerCfg.RoleARN
		}
		orch.SetArgoSecretManager(&argoManagerAdapter{mgr: s.argoSecretManager}, roleARN)
	}
	result, err := orch.RegisterCluster(ctx, req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// Dry-run: return preview without side effects.
	if req.DryRun {
		writeJSON(w, http.StatusOK, result)
		return
	}

	if s.argoSecretReconciler != nil {
		s.argoSecretReconciler.Trigger()
	}

	// Record verification observation if Stage1 ran.
	if result.Verification != nil && s.obsStore != nil {
		if err := s.obsStore.RecordTestResult(ctx, req.Name, *result.Verification); err != nil {
			slog.Error("failed to record verification observation during registration",
				"cluster", req.Name, "error", err)
		}
	}

	status := http.StatusCreated
	if result.Status == "partial" {
		status = http.StatusMultiStatus
	}

	// V125-1.1: distinct audit event for the kubeconfig registration path
	// so audit history can tell EKS-via-AWS-SM from inline-kubeconfig
	// registrations without parsing the resource string.
	auditEvent := "cluster_registered"
	if req.Provider == "kubeconfig" {
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

	orch := orchestrator.New(&s.gitMu, s.credProvider, ac, git, s.gitopsCfg, s.repoPaths, nil)
	s.attachPRTracker(orch)
	orch.SetSecretManagement(s.addonSecretDefs, s.secretFetcher, remoteclient.NewClientFromKubeconfig)
	if s.argoSecretManager != nil {
		roleARN := ""
		if s.providerCfg != nil {
			roleARN = s.providerCfg.RoleARN
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
	if s.argoSecretReconciler != nil {
		s.argoSecretReconciler.Trigger()
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

	// BUG-031: promoted from an anonymous struct to a named type
	// (orchestrator.UpdateClusterAddonsRequest) so the per-request
	// auto_merge override can flow through without losing field tags.
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

	// Handle secret_path update (metadata-only change via managed-clusters.yaml).
	if req.SecretPath != nil {
		managedPath := s.repoPaths.ManagedClusters
		if managedPath == "" {
			managedPath = "configuration/managed-clusters.yaml"
		}
		mcData, err := git.GetFileContent(ctx, managedPath, s.gitopsCfg.BaseBranch)
		if err != nil {
			writeError(w, http.StatusBadGateway, "reading managed-clusters.yaml: "+err.Error())
			return
		}
		updated, err := gitops.UpdateClusterSecretPath(mcData, name, *req.SecretPath)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		s.gitMu.Lock()
		branchName := fmt.Sprintf("%supdate-secret-path-%s", s.gitopsCfg.BranchPrefix, name)
		if err := git.CreateBranch(ctx, branchName, s.gitopsCfg.BaseBranch); err != nil {
			s.gitMu.Unlock()
			writeError(w, http.StatusBadGateway, "creating branch: "+err.Error())
			return
		}
		commitMsg := fmt.Sprintf("%s update secret_path for cluster %s", s.gitopsCfg.CommitPrefix, name)
		if err := git.CreateOrUpdateFile(ctx, managedPath, updated, branchName, commitMsg); err != nil {
			s.gitMu.Unlock()
			writeError(w, http.StatusBadGateway, "committing secret_path update: "+err.Error())
			return
		}
		pr, prErr := git.CreatePullRequest(ctx,
			fmt.Sprintf("Update secret_path for cluster %s", name),
			fmt.Sprintf("Sets secret_path to %q for cluster %s", *req.SecretPath, name),
			branchName, s.gitopsCfg.BaseBranch,
		)
		s.gitMu.Unlock()
		if prErr != nil {
			writeError(w, http.StatusBadGateway, "creating PR: "+prErr.Error())
			return
		}

		// V125-1-6: secret_path update bypasses orchestrator.commitChanges
		// (it's a metadata-only change), so the dashboard PR-tracker write
		// is performed inline. Skipped silently when no tracker is wired
		// (test seam / no in-cluster cmstore).
		if s.prTracker != nil && pr != nil {
			user := r.Header.Get("X-Sharko-User")
			if user == "" {
				user = "system"
			}
			_ = s.prTracker.TrackPR(ctx, prtracker.PRInfo{
				PRID:       pr.ID,
				PRUrl:      pr.URL,
				PRBranch:   branchName,
				PRTitle:    fmt.Sprintf("Update secret_path for cluster %s", name),
				PRBase:     s.gitopsCfg.BaseBranch,
				Cluster:    name,
				Operation:  "update-cluster",
				User:       user,
				Source:     "api",
				CreatedAt:  time.Now(),
				LastStatus: "open",
			})
		}

		// Auto-merge if configured. BUG-031: per-request override wins
		// over the connection-level default. BUG-032: clean up the
		// source branch after a successful merge — best-effort, never
		// fail the operation on DeleteBranch error.
		if pr != nil {
			autoMerge := req.AutoMerge
			if autoMerge == nil {
				v := s.gitopsCfg.PRAutoMerge
				autoMerge = &v
			}
			if *autoMerge {
				if mergeErr := git.MergePullRequest(ctx, pr.ID); mergeErr == nil {
					if delErr := git.DeleteBranch(ctx, branchName); delErr != nil {
						slog.Warn("failed to delete branch after secret_path merge",
							"branch", branchName, "error", delErr)
					}
				}
			}
		}

		// If no addons to update, return early with the secret_path result.
		if len(req.Addons) == 0 {
			prURL := ""
			if pr != nil {
				prURL = pr.URL
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"status":  "success",
				"message": fmt.Sprintf("secret_path updated for cluster %s", name),
				"pr_url":  prURL,
			})
			return
		}
	}

	orch := orchestrator.New(&s.gitMu, s.credProvider, ac, git, s.gitopsCfg, s.repoPaths, nil)
	s.attachPRTracker(orch)
	orch.SetSecretManagement(s.addonSecretDefs, s.secretFetcher, remoteclient.NewClientFromKubeconfig)
	// Region is empty — PATCH only updates addon labels, not cluster metadata.
	// Region is set during RegisterCluster and not exposed via the update API.
	// BUG-031: pass per-request auto_merge override (nil = fall back to
	// connection-level PRAutoMerge). The orchestrator forwards it to
	// commitChangesWithMeta via PRMetadata.AutoMergeOverride.
	result, err := orch.UpdateClusterAddons(ctx, name, serverURL, "", req.Addons, req.AutoMerge)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// Trigger the ArgoCD secrets reconciler to pick up the new addon state.
	// We do NOT call Manager.Ensure() directly here because this handler does not have
	// access to the cluster's Region (set at RegisterCluster time), and omitting Region
	// would produce a malformed execProviderConfig in the ArgoCD cluster secret.
	// The reconciler reads the full cluster spec from cluster-addons.yaml (including region)
	// and will update the secret within its next cycle (default: 3 minutes).
	if s.argoSecretReconciler != nil {
		s.argoSecretReconciler.Trigger()
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
// @Failure 503 {object} map[string]interface{} "Credentials provider not configured (V124-4.1)"
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

	if s.credProvider == nil {
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

	orch := orchestrator.New(&s.gitMu, s.credProvider, ac, nil, s.gitopsCfg, s.repoPaths, nil)
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

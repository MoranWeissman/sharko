package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/metrics"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
)

// handleEnableAddon godoc
//
// @Summary Enable addon on cluster
// @Description Enables a specific addon on a cluster. Updates the cluster values file
// @Description (sets addon to true) and managed-clusters.yaml (sets label to enabled) via PR.
// @Description If the addon declares secrets, Sharko must have resolvable credentials for
// @Description the cluster (V2-cleanup-88.3 — lazy credentials): registration succeeds with
// @Description no credentials, but enabling a secret-bearing addon on a cred-less cluster is
// @Description rejected with 422 naming what's missing. Secret-less addons enable with no
// @Description credential requirement at all. Requires yes=true for confirmation. Pass
// @Description dry_run=true to preview (the credentials gate applies to the preview too).
// @Tags clusters
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Param addon path string true "Addon name"
// @Param body body orchestrator.EnableAddonRequest true "Enable addon request"
// @Success 200 {object} orchestrator.EnableAddonResult "Addon enabled (or dry-run preview)"
// @Success 207 {object} orchestrator.EnableAddonResult "Partial success"
// @Failure 400 {object} map[string]interface{} "Bad request or missing confirmation"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Failure 422 {object} map[string]interface{} "Addon not in catalog, or addon needs secrets but Sharko has no credentials for the cluster"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /clusters/{name}/addons/{addon} [post]
func (s *Server) handleEnableAddon(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.enable") {
		return
	}

	// V2-3 SLO surface: addon_cycle (enable side). End-to-end timing
	// only for PR 1; the full async PR -> merge -> reconciler -> ArgoCD
	// sync timeline is deferred to V2-3.x once perf-baselines.yaml
	// covers the real cycle (currently it only captures dry-run phases).
	start := time.Now()
	rec := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
	w = rec
	defer func() {
		code := strconv.Itoa(rec.statusCode)
		metrics.Observe(metrics.PathAddonCycle, "enable", time.Since(start).Seconds(), logging.RequestID(r.Context()))
		metrics.IncTotal(metrics.PathAddonCycle, code)
		if rec.statusCode >= 400 {
			metrics.IncError(metrics.PathAddonCycle, code)
		}
	}()

	clusterName := r.PathValue("name")
	addonName := r.PathValue("addon")
	if clusterName == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}
	if addonName == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	git, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	var req orchestrator.EnableAddonRequest
	if r.Body != nil && r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
	}
	req.Cluster = clusterName
	req.Addon = addonName

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

	result, orchErr := orch.EnableAddon(r.Context(), req)
	if orchErr != nil {
		if orchErr.Error() == "confirmation required: set yes: true in request body" {
			writeError(w, http.StatusBadRequest, orchErr.Error())
			return
		}
		// Referential-integrity rejection (V2-cleanup-22): the addon is not
		// in the catalog. This is a caller error, not an upstream failure —
		// map it to 422 with the orchestrator's actionable message.
		if orchestrator.IsAddonNotInCatalog(orchErr) {
			writeError(w, http.StatusUnprocessableEntity, orchErr.Error())
			return
		}
		// Lazy-credentials pre-flight rejection (V2-cleanup-88.3): the addon
		// needs secrets pushed to the cluster but Sharko has no resolvable
		// credentials for it. Caller-actionable — map to 422, same status
		// family as the catalog-membership rejection above, never a 500/502.
		if orchestrator.IsMissingClusterCredentials(orchErr) {
			writeError(w, http.StatusUnprocessableEntity, orchErr.Error())
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

	// Trigger reconciler after addon enable.
	if s.argoSecretReconciler != nil {
		s.argoSecretReconciler.Trigger()
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "addon_enabled_on_cluster",
		Resource: fmt.Sprintf("cluster:%s addon:%s", clusterName, addonName),
	})

	status := http.StatusOK
	if result.Status == "partial" {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, result)
}

// handleDisableAddon godoc
//
// @Summary Disable addon on cluster
// @Description Disables a specific addon on a cluster with configurable cleanup scope.
// @Description Pass cleanup=all (default) to update values + labels and delete remote secrets.
// @Description Pass cleanup=labels to update values + labels only. Pass cleanup=none for values only.
// @Description Requires yes=true for confirmation. Pass dry_run=true to preview.
// @Tags clusters
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Param addon path string true "Addon name"
// @Param body body orchestrator.DisableAddonRequest true "Disable addon request"
// @Success 200 {object} orchestrator.DisableAddonResult "Addon disabled (or dry-run preview)"
// @Success 207 {object} orchestrator.DisableAddonResult "Partial success"
// @Failure 400 {object} map[string]interface{} "Bad request or missing confirmation"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /clusters/{name}/addons/{addon} [delete]
func (s *Server) handleDisableAddon(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.disable") {
		return
	}

	// V2-3 SLO surface: addon_cycle (disable side). See handleEnableAddon
	// for the per-phase wiring follow-up note.
	start := time.Now()
	rec := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
	w = rec
	defer func() {
		code := strconv.Itoa(rec.statusCode)
		metrics.Observe(metrics.PathAddonCycle, "disable", time.Since(start).Seconds(), logging.RequestID(r.Context()))
		metrics.IncTotal(metrics.PathAddonCycle, code)
		if rec.statusCode >= 400 {
			metrics.IncError(metrics.PathAddonCycle, code)
		}
	}()

	clusterName := r.PathValue("name")
	addonName := r.PathValue("addon")
	if clusterName == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}
	if addonName == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	git, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	var req orchestrator.DisableAddonRequest
	if r.Body != nil && r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
	}
	req.Cluster = clusterName
	req.Addon = addonName

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

	result, orchErr := orch.DisableAddon(r.Context(), req)
	if orchErr != nil {
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

	// Trigger reconciler after addon disable.
	if s.argoSecretReconciler != nil {
		s.argoSecretReconciler.Trigger()
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "addon_disabled_on_cluster",
		Resource: fmt.Sprintf("cluster:%s addon:%s", clusterName, addonName),
	})

	status := http.StatusOK
	if result.Status == "partial" {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, result)
}

package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
)

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

	orch := orchestrator.New(&s.gitMu, s.credProvider, ac, git, s.gitopsCfg, s.repoPaths, nil)
	orch.SetSecretManagement(s.addonSecretDefs, s.secretFetcher, remoteclient.NewClientFromKubeconfig)
	if s.argoSecretManager != nil {
		roleARN := ""
		if s.providerCfg != nil {
			roleARN = s.providerCfg.RoleARN
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

	s.auditLog.Add(audit.Entry{
		Level:    "info",
		Event:    "addon_disabled",
		User:     "sharko",
		Action:   "disable",
		Resource: fmt.Sprintf("addon:%s/cluster:%s", addonName, clusterName),
		Source:   "api",
		Result:   result.Status,
	})

	status := http.StatusOK
	if result.Status == "partial" {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, result)
}

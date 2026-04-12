package api

import (
	"log/slog"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
)

// handleListClusterSecrets godoc
//
// @Summary List cluster secrets
// @Description Lists managed addon secrets on a remote cluster
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Success 200 {object} map[string]interface{} "Cluster secrets"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 501 {object} map[string]interface{} "Provider not configured"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /clusters/{name}/secrets [get]
func (s *Server) handleListClusterSecrets(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.secrets.list") {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}
	if s.credProvider == nil {
		writeError(w, http.StatusNotImplemented, "secrets provider not configured")
		return
	}

	creds, err := s.credProvider.GetCredentials(name)
	if err != nil {
		writeError(w, http.StatusBadGateway, "fetching cluster credentials: "+err.Error())
		return
	}

	client, err := remoteclient.NewClientFromKubeconfig(creds.Raw)
	if err != nil {
		writeError(w, http.StatusBadGateway, "connecting to cluster: "+err.Error())
		return
	}

	// Collect declared namespaces from addon secret definitions.
	allowedNamespaces := make(map[string]bool)
	s.addonSecretDefsMu.RLock()
	for _, def := range s.addonSecretDefs {
		if def.Namespace != "" {
			allowedNamespaces[def.Namespace] = true
		}
	}
	s.addonSecretDefsMu.RUnlock()

	var allSecrets []remoteclient.ManagedSecretInfo
	if len(allowedNamespaces) > 0 {
		// List secrets only in declared namespaces.
		for ns := range allowedNamespaces {
			secrets, err := remoteclient.ListManagedSecrets(r.Context(), client, ns)
			if err != nil {
				// Log but continue — namespace may not exist yet.
				slog.Warn("listing secrets in namespace", "namespace", ns, "error", err)
				continue
			}
			allSecrets = append(allSecrets, secrets...)
		}
	} else {
		// No addon secret definitions configured — fall back to listing by label only.
		slog.Warn("no addon secret definitions — listing all managed secrets")
		secrets, err := remoteclient.ListManagedSecrets(r.Context(), client, "")
		if err != nil {
			writeError(w, http.StatusBadGateway, "listing secrets: "+err.Error())
			return
		}
		allSecrets = secrets
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"cluster": name,
		"secrets": allSecrets,
	})
}

// handleRefreshClusterSecrets godoc
//
// @Summary Refresh cluster secrets
// @Description Re-creates all managed addon secrets on a remote cluster
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Success 200 {object} map[string]interface{} "Secrets refreshed"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 501 {object} map[string]interface{} "Provider not configured"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /clusters/{name}/secrets/refresh [post]
func (s *Server) handleRefreshClusterSecrets(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.secrets.refresh") {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}
	if s.credProvider == nil {
		writeError(w, http.StatusNotImplemented, "secrets provider not configured")
		return
	}

	creds, err := s.credProvider.GetCredentials(name)
	if err != nil {
		writeError(w, http.StatusBadGateway, "fetching cluster credentials: "+err.Error())
		return
	}

	// Refresh all defined addon secrets for this cluster.
	s.addonSecretDefsMu.RLock()
	allEnabled := make(map[string]bool)
	for addonName := range s.addonSecretDefs {
		allEnabled[addonName] = true
	}
	s.addonSecretDefsMu.RUnlock()

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

	orch := orchestrator.New(&s.gitMu, s.credProvider, ac, git, s.gitopsCfg, s.repoPaths, nil)
	orch.SetSecretManagement(s.addonSecretDefs, s.secretFetcher, remoteclient.NewClientFromKubeconfig)

	created, secretErr := orch.CreateAddonSecretsForCluster(r.Context(), creds.Raw, allEnabled)
	if secretErr != nil {
		writeError(w, http.StatusBadGateway, "refreshing secrets: "+secretErr.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"cluster":           name,
		"secrets_refreshed": created,
	})
}

package api

import (
	"net/http"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
)

func (s *Server) handleListClusterSecrets(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
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

	secrets, err := remoteclient.ListManagedSecrets(r.Context(), client, "")
	if err != nil {
		writeError(w, http.StatusBadGateway, "listing secrets: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"cluster": name,
		"secrets": secrets,
	})
}

func (s *Server) handleRefreshClusterSecrets(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
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

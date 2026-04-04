package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
)

var validClusterNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)

// handleRegisterCluster handles POST /api/v1/clusters — register a new cluster.
func (s *Server) handleRegisterCluster(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.credProvider == nil {
		writeError(w, http.StatusNotImplemented, "secrets provider not configured")
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

	orch := orchestrator.New(&s.gitMu, s.credProvider, ac, git, s.gitopsCfg, s.repoPaths, nil)
	orch.SetSecretManagement(s.addonSecretDefs, s.secretFetcher, remoteclient.NewClientFromKubeconfig)
	result, err := orch.RegisterCluster(r.Context(), req)
	if err != nil {
		if errors.Is(err, orchestrator.ErrClusterAlreadyExists) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	status := http.StatusCreated
	if result.Status == "partial" {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, result)
}

// handleDeregisterCluster handles DELETE /api/v1/clusters/{name} — remove a cluster.
func (s *Server) handleDeregisterCluster(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
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

	git, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
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

	orch := orchestrator.New(&s.gitMu, s.credProvider, ac, git, s.gitopsCfg, s.repoPaths, nil)
	orch.SetSecretManagement(s.addonSecretDefs, s.secretFetcher, remoteclient.NewClientFromKubeconfig)
	result, err := orch.DeregisterCluster(r.Context(), name, serverURL)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	status := http.StatusOK
	if result.Status == "partial" {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, result)
}

// handleUpdateClusterAddons handles PATCH /api/v1/clusters/{name} — update addon labels.
func (s *Server) handleUpdateClusterAddons(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
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

	git, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	var req struct {
		Addons map[string]bool `json:"addons"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
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

	orch := orchestrator.New(&s.gitMu, s.credProvider, ac, git, s.gitopsCfg, s.repoPaths, nil)
	orch.SetSecretManagement(s.addonSecretDefs, s.secretFetcher, remoteclient.NewClientFromKubeconfig)
	// Region is empty — PATCH only updates addon labels, not cluster metadata.
	// Region is set during RegisterCluster and not exposed via the update API.
	result, err := orch.UpdateClusterAddons(r.Context(), name, serverURL, "", req.Addons)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	status := http.StatusOK
	if result.Status == "partial" {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, result)
}

// handleRefreshClusterCredentials handles POST /api/v1/clusters/{name}/refresh.
func (s *Server) handleRefreshClusterCredentials(w http.ResponseWriter, r *http.Request) {
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
	if err := orch.RefreshClusterCredentials(r.Context(), name, serverURL); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

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

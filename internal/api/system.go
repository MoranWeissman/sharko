package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/providers"
)

// handleGetProviders godoc
//
// @Summary Get providers
// @Description Returns the configured credentials provider type and connection status
// @Tags system
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "Provider info"
// @Router /providers [get]
// handleGetProviders handles GET /api/v1/providers — return provider configuration.
func (s *Server) handleGetProviders(w http.ResponseWriter, r *http.Request) {
	availableTypes := []string{"aws-sm", "k8s-secrets"}

	if s.providerCfg == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"configured_provider": nil,
			"available_types":     availableTypes,
		})
		return
	}

	// Test connectivity to report status.
	status := "configured"
	var statusError string
	if s.credProvider != nil {
		if _, err := s.credProvider.ListClusters(); err == nil {
			status = "connected"
		} else {
			status = "error"
			statusError = err.Error()
			slog.Warn("[provider] ListClusters failed", "type", s.providerCfg.Type, "region", s.providerCfg.Region, "prefix", s.providerCfg.Prefix, "error", err)
		}
	}

	providerInfo := map[string]interface{}{
		"type":   s.providerCfg.Type,
		"region": s.providerCfg.Region,
		"status": status,
	}
	if statusError != "" {
		providerInfo["error"] = statusError
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"configured_provider": providerInfo,
		"available_types":     availableTypes,
	})
}

// handleTestProvider godoc
//
// @Summary Test provider connectivity
// @Description Tests the configured or provided credentials provider connectivity
// @Tags system
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body map[string]interface{} false "Optional provider type and region to test"
// @Success 200 {object} map[string]interface{} "Test result"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 501 {object} map[string]interface{} "No provider configured"
// @Router /providers/test [post]
// handleTestProvider handles POST /api/v1/providers/test — test provider connectivity.
// Reads optional type/region from request body. If empty, tests the configured provider.
func (s *Server) handleTestProvider(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type   string `json:"type"`
		Region string `json:"region"`
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
	}

	var provider providers.ClusterCredentialsProvider
	var provType string

	if req.Type != "" {
		prov, err := providers.New(providers.Config{
			Type:   req.Type,
			Region: req.Region,
		})
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"status":  "error",
				"message": err.Error(),
			})
			return
		}
		provider = prov
		provType = req.Type
	} else if s.credProvider != nil {
		provider = s.credProvider
		provType = s.providerCfg.Type
	} else {
		writeError(w, http.StatusNotImplemented, "no provider configured and no type specified in request")
		return
	}

	clusters, err := provider.ListClusters()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":         "connected",
		"clusters_found": len(clusters),
		"message":        fmt.Sprintf("Connected to %s, found %d cluster secrets", provType, len(clusters)),
	})
}

// handleTestProviderConfig godoc
//
// @Summary Test ad-hoc provider config
// @Description Tests an arbitrary provider configuration without saving it
// @Tags system
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body map[string]interface{} true "Provider config to test"
// @Success 200 {object} map[string]interface{} "Test result"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Router /providers/test-config [post]
// handleTestProviderConfig tests an ad-hoc provider configuration.
func (s *Server) handleTestProviderConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type      string `json:"type"`
		Region    string `json:"region"`
		Prefix    string `json:"prefix"`
		Namespace string `json:"namespace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	prov, err := providers.New(providers.Config{
		Type:      req.Type,
		Region:    req.Region,
		Prefix:    req.Prefix,
		Namespace: req.Namespace,
	})
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	clusters, err := prov.ListClusters()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":         "connected",
		"clusters_found": len(clusters),
	})
}

// handleGetConfig godoc
//
// @Summary Get server config
// @Description Returns non-sensitive server configuration including repo paths, GitOps settings, and ArgoCD status
// @Tags system
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "Server configuration"
// @Router /config [get]
// handleGetConfig handles GET /api/v1/config — return non-sensitive server configuration.
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg := map[string]interface{}{
		"repo_paths": map[string]string{
			"cluster_values": s.repoPaths.ClusterValues,
			"global_values":  s.repoPaths.GlobalValues,
			"charts":         s.repoPaths.Charts,
			"bootstrap":      s.repoPaths.Bootstrap,
		},
		"gitops": map[string]interface{}{
			"pr_auto_merge": s.gitopsCfg.PRAutoMerge,
			"branch_prefix": s.gitopsCfg.BranchPrefix,
			"commit_prefix": s.gitopsCfg.CommitPrefix,
			"base_branch":   s.gitopsCfg.BaseBranch,
		},
	}

	// Provider info (type + region, no secrets).
	if s.providerCfg != nil {
		cfg["provider"] = map[string]string{
			"type":   s.providerCfg.Type,
			"region": s.providerCfg.Region,
		}
	}

	// ArgoCD connection status.
	argocdInfo := map[string]interface{}{
		"connected": false,
	}
	if ac, err := s.connSvc.GetActiveArgocdClient(); err == nil {
		if ver, err := ac.GetVersion(r.Context()); err == nil {
			argocdInfo["connected"] = true
			argocdInfo["version"] = ver
		}
	}
	cfg["argocd"] = argocdInfo

	writeJSON(w, http.StatusOK, cfg)
}

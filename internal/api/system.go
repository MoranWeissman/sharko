package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
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

	displayType, displayRegion, displayPrefix := s.providerDisplay()
	if displayType == "" && displayRegion == "" && displayPrefix == "" && s.credProvider() == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"configured_provider": nil,
			"available_types":     availableTypes,
		})
		return
	}

	// Test connectivity to report status using a lightweight health check.
	// A 3-second timeout ensures this never stalls the page load.
	status := "configured"
	var statusError string
	if s.credProvider() != nil {
		hctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := s.credProvider().HealthCheck(hctx); err == nil {
			status = "connected"
		} else {
			status = "error"
			statusError = err.Error()
			slog.Warn("[provider] HealthCheck failed", "type", displayType, "region", displayRegion, "prefix", displayPrefix, "error", err)
		}
	}

	providerInfo := map[string]interface{}{
		"type":   displayType,
		"region": displayRegion,
		// prefix was previously computed (providerDisplay) but only ever
		// warn-logged — the response silently dropped it. Report it so
		// API consumers see the same provider identity the server uses
		// (V2-cleanup-55.5).
		"prefix": displayPrefix,
		"status": status,
	}
	if statusError != "" {
		providerInfo["error"] = statusError
	}

	// V3-P1.1: surface addon-secret backend status so the UI can detect the
	// "argocd selected for cluster-creds but no addon-secret backend" trap.
	addonSecretStatus, addonSecretMessage := s.addonSecretBackendStatus()
	if addonSecretStatus != "ok" {
		providerInfo["addon_secret_status"] = addonSecretStatus
		if addonSecretMessage != "" {
			providerInfo["addon_secret_message"] = addonSecretMessage
		}
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
//
// This endpoint tests cluster-test (ClusterCredentialsProvider) backends
// only — the cluster-test factory accepts argocd, aws-sm, k8s-secrets, and
// "" (auto-default) since the V2-cleanup-53.1 arm restore. For any other
// req.Type the factory's error message lists the valid options.
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
		prov, err := providers.NewClusterTestProvider(providers.ClusterTestProviderConfig{
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
	} else if s.credProvider() != nil {
		provider = s.credProvider()
		if s.clusterTestCfg() != nil {
			provType = s.clusterTestCfg().Type
		}
		if provType == "" && s.addonSecretCfg() != nil {
			provType = s.addonSecretCfg().Type
		}
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

	audit.Enrich(r.Context(), audit.Fields{
		Event: "provider_tested",
	})
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
// handleTestProviderConfig tests an ad-hoc cluster-test provider configuration.
//
// The cluster-test factory accepts argocd, aws-sm, k8s-secrets, and ""
// (auto-default) since the V2-cleanup-53.1 arm restore. req.Namespace is
// passed to BOTH namespace slots because this is an ad-hoc, explicitly-typed
// test (each factory arm reads only its own field, so there is no
// cross-contamination risk here — unlike the connection fan-through, where
// the stored namespace's provenance is ambiguous).
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

	prov, err := providers.NewClusterTestProvider(providers.ClusterTestProviderConfig{
		Type:            req.Type,
		ArgoCDNamespace: req.Namespace,
		Region:          req.Region,
		Prefix:          req.Prefix,
		Namespace:       req.Namespace,
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

	audit.Enrich(r.Context(), audit.Fields{
		Event: "provider_tested",
	})
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
			"pr_auto_merge": s.gitopsConfig().PRAutoMerge,
			"branch_prefix": s.gitopsConfig().BranchPrefix,
			"commit_prefix": s.gitopsConfig().CommitPrefix,
			"base_branch":   s.gitopsConfig().BaseBranch,
		},
	}

	// Provider info (type + region, no secrets).
	if displayType, displayRegion, _ := s.providerDisplay(); displayType != "" || displayRegion != "" {
		cfg["provider"] = map[string]string{
			"type":   displayType,
			"region": displayRegion,
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

// providerDisplay returns the (type, region, prefix) triple to show in
// /providers and /config response payloads. The display unifies the two
// typed configs: addon-secret carries the richer fields
// (Type/Region/Prefix/RoleARN) while cluster-test carries the
// authoritative cluster-creds Type. When both are set, addon-secret
// wins for Type/Region/Prefix; if addon-secret is unset
// (cluster-test-only install), we fall back to cluster-test's Type and
// empty Region/Prefix.
func (s *Server) providerDisplay() (typ, region, prefix string) {
	if s.addonSecretCfg() != nil {
		typ = s.addonSecretCfg().Type
		region = s.addonSecretCfg().Region
		prefix = s.addonSecretCfg().Prefix
	}
	if typ == "" && s.clusterTestCfg() != nil {
		typ = s.clusterTestCfg().Type
	}
	return typ, region, prefix
}

// addonSecretBackendStatus returns (status, message) indicating whether the
// addon-secret backend is properly configured (V3-P1.1). Valid statuses:
//   "ok"              — addon-secret backend Type is valid (not empty, not "argocd")
//   "missing"         — no addon-secret backend configured (Type is empty)
//   "invalid_argocd"  — Type is "argocd" (rejected by NewAddonSecretProvider)
//
// The UI (story 1.2) uses this to require an explicit addon-secret provider
// choice when the user picks "argocd" for cluster-credentials.
func (s *Server) addonSecretBackendStatus() (status, message string) {
	cfg := s.addonSecretCfg()
	if cfg == nil {
		return "missing", "No addon-secret backend configured"
	}

	switch cfg.Type {
	case "":
		return "missing", "No addon-secret backend configured"
	case "argocd":
		return "invalid_argocd", "ArgoCD provider is cluster-credentials-only; configure a separate backend (aws-sm, k8s-secrets, gcp-sm, azure-kv) for addon secrets"
	default:
		return "ok", ""
	}
}

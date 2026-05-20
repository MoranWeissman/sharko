package api

import (
	"net/http"

	"github.com/MoranWeissman/sharko/internal/platform"
)

// handleHealth handles GET /api/v1/health
//
// BUG-041: the response now also carries `cluster_test_available` — a
// capability flag the UI uses to gate the per-cluster "Test" button. When
// no credentials provider is wired up on the active connection (typically
// `--demo` mode with no secrets backend), the test endpoint returns
// HTTP 503 / error_code=no_secrets_backend. Surfacing that fact up-front
// via /health lets the UI render the button disabled with a tooltip
// instead of leaving operators to discover the unavailability by clicking
// and getting a confusing 503.
//
// @Summary Health check
// @Description Returns server health status, version, deployment mode, and capability flags. `cluster_test_available` is true when a secrets backend is configured on the active connection (the cluster-connectivity test endpoint requires one).
// @Tags system
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /health [get]
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	mode := platform.Detect()
	deploymentMode := "Local Development"
	if mode == platform.ModeKubernetes {
		deploymentMode = "Kubernetes"
	}

	v := s.version
	if v == "" {
		v = "dev"
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":                  "healthy",
		"version":                 v,
		"mode":                    deploymentMode,
		"cluster_test_available":  s.credProvider != nil,
	})
}

package api

import (
	"net/http"

	"github.com/MoranWeissman/sharko/internal/platform"
)

// handleHealth handles GET /api/v1/health
//
// @Summary Health check
// @Description Returns server health status and version
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

	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "healthy",
		"version": v,
		"mode":    deploymentMode,
	})
}

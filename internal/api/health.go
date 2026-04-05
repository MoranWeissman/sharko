package api

import (
	"net/http"
	"os"
	"strings"

	"github.com/MoranWeissman/sharko/internal/platform"
)

// appVersion is read once at startup from the VERSION file.
var appVersion = readVersionFile()

func readVersionFile() string {
	// Try common locations
	for _, path := range []string{"version.txt", "/app/version.txt", "VERSION", "/app/VERSION"} {
		data, err := os.ReadFile(path)
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return "dev"
}

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

	writeJSON(w, http.StatusOK, map[string]string{
		"status":   "healthy",
		"version":  appVersion,
		"mode":     deploymentMode,
		"dev_mode": os.Getenv("SHARKO_DEV_MODE"),
	})
}

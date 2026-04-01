package api

import (
	"net/http"
	"os"
	"strings"

	"github.com/moran/argocd-addons-platform/internal/platform"
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
		"dev_mode": os.Getenv("AAP_DEV_MODE"),
	})
}

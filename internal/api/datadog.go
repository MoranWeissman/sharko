package api

import (
	"net/http"
	"regexp"
	"time"
)

var validK8sName = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

func (s *Server) handleDatadogClusterMetrics(w http.ResponseWriter, r *http.Request) {
	if s.ddClient == nil || !s.ddClient.IsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "Datadog is not configured")
		return
	}

	clusterName := r.PathValue("clusterName")
	if clusterName == "" {
		writeError(w, http.StatusBadRequest, "clusterName is required")
		return
	}
	if !validK8sName.MatchString(clusterName) {
		writeError(w, http.StatusBadRequest, "invalid clusterName")
		return
	}

	// Get ArgoCD client to look up addon namespaces for this cluster
	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "ArgoCD not configured: "+err.Error())
		return
	}

	apps, err := ac.ListApplications(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to list ArgoCD applications: "+err.Error())
		return
	}

	// Collect unique destination namespaces for the requested cluster
	nsSet := make(map[string]struct{})
	for _, app := range apps {
		if app.DestinationName == clusterName {
			if app.DestinationNamespace != "" {
				nsSet[app.DestinationNamespace] = struct{}{}
			}
		}
	}
	namespaces := make([]string, 0, len(nsSet))
	for ns := range nsSet {
		namespaces = append(namespaces, ns)
	}

	if len(namespaces) == 0 {
		writeError(w, http.StatusNotFound, "no addon namespaces found for cluster "+clusterName)
		return
	}

	metrics, err := s.ddClient.GetClusterAddonMetrics(r.Context(), clusterName, namespaces, 15*time.Minute)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to fetch cluster metrics: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, metrics)
}

func (s *Server) handleDatadogStatus(w http.ResponseWriter, r *http.Request) {
	enabled := s.ddClient != nil && s.ddClient.IsEnabled()
	site := ""
	if s.ddClient != nil {
		site = s.ddClient.Site()
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"enabled": enabled,
		"site":    site,
	})
}

func (s *Server) handleDatadogNamespaceMetrics(w http.ResponseWriter, r *http.Request) {
	if s.ddClient == nil || !s.ddClient.IsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "Datadog is not configured")
		return
	}

	namespace := r.PathValue("namespace")
	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace is required")
		return
	}
	if !validK8sName.MatchString(namespace) {
		writeError(w, http.StatusBadRequest, "invalid namespace")
		return
	}

	metrics, err := s.ddClient.GetNamespaceMetrics(r.Context(), namespace, 15*time.Minute)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to fetch metrics: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, metrics)
}

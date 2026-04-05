package api

import (
	"net/http"
)

// handleGetClusterHistory godoc
//
// @Summary Get cluster change history
// @Description Returns recent sync activity and changes for a specific cluster
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /clusters/{name}/history [get]
func (s *Server) handleGetClusterHistory(w http.ResponseWriter, r *http.Request) {
	clusterName := r.PathValue("name")
	if clusterName == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		// Git provider is optional for history — resource alerts will be skipped
		gp = nil
	}

	overview, err := s.observabilitySvc.GetOverview(r.Context(), ac, gp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Filter recent_syncs to only this cluster
	var filtered []interface{}
	for _, entry := range overview.RecentSyncs {
		if entry.ClusterName == clusterName {
			filtered = append(filtered, entry)
		}
	}
	if filtered == nil {
		filtered = []interface{}{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"cluster_name": clusterName,
		"history":      filtered,
	})
}

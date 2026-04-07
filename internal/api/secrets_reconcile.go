package api

import (
	"net/http"
)

// handleTriggerReconcile godoc
//
// @Summary Trigger secret reconciliation
// @Description Manually triggers a full secret reconciliation pass across all registered clusters.
// @Description Returns immediately with 202 Accepted; reconciliation runs in the background.
// @Tags secrets
// @Produce json
// @Security BearerAuth
// @Success 202 {object} map[string]string "Reconcile triggered"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 503 {object} map[string]string "Secrets reconciler not configured"
// @Router /secrets/reconcile [post]
func (s *Server) handleTriggerReconcile(w http.ResponseWriter, r *http.Request) {
	if s.secretReconciler == nil {
		writeError(w, http.StatusServiceUnavailable, "secrets reconciler not configured")
		return
	}
	s.secretReconciler.Trigger()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "reconcile triggered"})
}

// handleReconcileStatus godoc
//
// @Summary Get secret reconciliation status
// @Description Returns statistics from the most recent secret reconciliation run,
// @Description including counts of checked, created, updated, deleted, and skipped secrets,
// @Description error count, duration, and the timestamp of the last run.
// @Tags secrets
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "Reconciliation stats"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 503 {object} map[string]string "Secrets reconciler not configured"
// @Router /secrets/status [get]
func (s *Server) handleReconcileStatus(w http.ResponseWriter, r *http.Request) {
	if s.secretReconciler == nil {
		writeError(w, http.StatusServiceUnavailable, "secrets reconciler not configured")
		return
	}
	stats := s.secretReconciler.GetStats()
	writeJSON(w, http.StatusOK, stats)
}

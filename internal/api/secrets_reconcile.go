package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
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
	if !authz.RequireWithResponse(w, r, "reconciler.trigger") {
		return
	}
	if s.secretReconciler == nil {
		writeError(w, http.StatusServiceUnavailable, "secrets reconciler not configured")
		return
	}
	s.secretReconciler.Trigger()
	audit.Enrich(r.Context(), audit.Fields{
		Event: "reconcile_triggered",
	})
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "reconcile triggered"})
}

// handleCheckSecrets godoc
//
// @Summary Check every addon-values secret against its source
// @Description Checks every addon-values secret against the secrets store right now, WITHOUT writing anything — no secret is created, changed or deleted.
// @Description This is what the Managed Secrets page's "Refresh all" drives on this engine. To actually push values, use POST /secrets/reconcile or the per-row sync endpoint.
// @Description Returns 202 as soon as the check is accepted — the check itself runs in the background.
// @Tags secrets
// @Produce json
// @Security BearerAuth
// @Success 202 {object} map[string]string "Check started"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 403 {object} map[string]string "Forbidden — requires operator role or higher"
// @Failure 503 {object} map[string]string "Secrets reconciler not configured"
// @Router /secrets/check [post]
//
// handleCheckSecrets handles POST /api/v1/secrets/check.
//
// It runs in a goroutine with its own context: a check over 50 clusters can
// take longer than a caller is willing to hold a request open, and the page
// re-reads the rows on its own anyway. The request's own context is NOT
// reused — cancelling it must not abandon a check halfway through.
func (s *Server) handleCheckSecrets(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "reconciler.trigger") {
		return
	}
	if s.secretReconciler == nil {
		writeError(w, http.StatusServiceUnavailable, "secrets reconciler not configured")
		return
	}

	recon := s.secretReconciler
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := recon.CheckAll(ctx); err != nil {
			slog.Warn("[secrets] check-all could not run", "error", err, "component", "api")
		}
	}()

	audit.Enrich(r.Context(), audit.Fields{
		Event:  "addon_values_secret_check_triggered",
		Detail: "read-only check of every addon-values secret",
	})
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "checking every addon-values secret — nothing is written"})
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

package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
)

// secretsEngineNotRunning is the 503 a person reads on every endpoint that
// needs the addon-values engine and does not have it. It is shared by
// secrets_reconcile.go and cluster_secrets.go so a person hears one story.
//
// The old text was the bare "secrets reconciler not configured" — the
// component's own name, in four places. The wire code beside it in
// cluster_secrets.go stays "secrets_reconciler_not_configured": that is an
// identifier a caller matches on, not a sentence anybody reads.
// secretsEngineNotConfiguredCode is the machine-readable half of that 503.
// It keeps the component's name on purpose: a caller matches on this string,
// and changing it breaks every client that switches on it. It is a wire value,
// not a sentence, which is the same distinction the Git guard draws with its
// delimited-token rule.
const secretsEngineNotConfiguredCode = "secrets_reconciler_not_configured"

const secretsEngineNotRunning = "The part of Sharko that delivers addon secrets to your clusters is not running on this server."

// secretsEngineNotRunningHint is the "what to do about it" line that travels
// with the 503 above. It also used to write Git in lowercase, and the Git
// guard could not see it: the guard reads a string as prose only when it opens
// with a capital letter, and this one opened with "configure".
const secretsEngineNotRunningHint = "Configure a secrets backend under Settings → Connections, or POST /api/v1/connections/ with the backend config. The engine that reads addon secrets from Git starts once a backend is configured."

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
// @Failure 503 {object} map[string]string "The part of Sharko that delivers addon secrets is not running on this server"
// @Router /secrets/reconcile [post]
func (s *Server) handleTriggerReconcile(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "reconciler.trigger") {
		return
	}
	if s.secretReconciler == nil {
		writeError(w, http.StatusServiceUnavailable, secretsEngineNotRunning)
		return
	}
	s.secretReconciler.Trigger()
	// P3-F1: this entry used to carry an event name and nothing else — no
	// resource, no blast radius. It now names the surface it acted on and
	// says how many secrets that pass covers.
	audit.Enrich(r.Context(), audit.Fields{
		Event:    "reconcile_triggered",
		Resource: resourceAddonValuesSecrets,
		Detail:   valuesReconcileBlastRadius(s.secretReconciler.KnownItemCount()),
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
// @Failure 409 {object} map[string]string "The addon-values engine is switched off — nothing to check"
// @Failure 503 {object} map[string]string "The part of Sharko that delivers addon secrets is not running on this server"
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
		writeError(w, http.StatusServiceUnavailable, secretsEngineNotRunning)
		return
	}

	// M6 (code review): checked synchronously, BEFORE the 202 and BEFORE the
	// audit entry below. This used to 202 and write a "checked N secrets"
	// audit entry unconditionally, then start the goroutine that called
	// CheckAll — which discovered ErrReconcilerDisabled and only logged a
	// warning. The audit trail said a check ran on every addon-values secret
	// when the engine was off and nothing was checked at all.
	if !s.secretReconciler.IsEnabled(r.Context()) {
		writeError(w, http.StatusConflict,
			"Sharko has the addon-values engine switched off — there is nothing to check. Turn it on in Settings, then try again.")
		return
	}

	recon := s.secretReconciler
	go func() { // #nosec G118 -- deliberately detached from the request context (see the handler comment above): cancelling the HTTP request must not abandon a fleet-wide check halfway through; the 5-minute timeout below bounds it instead
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := recon.CheckAll(ctx); err != nil {
			slog.Warn("[secrets] check-all could not run", "error", err, "component", "api")
		}
	}()

	// P3-F1: this is the entry the page's "Refresh all" leaves behind on
	// this engine, and it used to be an entry about nothing — no resource
	// at all, and a detail line that said "every" without saying how many.
	// It now names the surface and states the real count (see
	// audit_blast_radius.go). The count is read BEFORE the goroutine above
	// gets anywhere, which is the honest thing to report: it is the size of
	// the set this check was started over.
	audit.Enrich(r.Context(), audit.Fields{
		Event:    "addon_values_secret_check_triggered",
		Resource: resourceAddonValuesSecrets,
		Detail:   valuesCheckBlastRadius(recon.KnownItemCount()),
	})
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "checking every addon-values secret — nothing is written"})
}

// ReconcileStatusResponse is the public shape of GET /secrets/status. It maps
// internal/secrets.ReconcileStats field-for-field, except LastRun: the
// internal struct always carries a time.Time (Go's zero value —
// "0001-01-01T00:00:00Z" — before any run has completed), but that zero time
// must never reach a caller. LastRun here is a plain RFC3339 string, omitted
// entirely when no reconcile run has completed yet.
type ReconcileStatusResponse struct {
	Checked  int    `json:"checked"`
	Created  int    `json:"created"`
	Updated  int    `json:"updated"`
	Deleted  int    `json:"deleted"`
	Skipped  int    `json:"skipped"`
	Errors   int    `json:"errors"`
	Duration string `json:"duration"`
	// LastRun is RFC3339, omitted (not null, not the Go zero time) when no
	// reconcile run has completed yet.
	LastRun string `json:"last_run,omitempty"`
}

// handleReconcileStatus godoc
//
// @Summary Get secret reconciliation status
// @Description Returns statistics from the most recent secret reconciliation run,
// @Description including counts of checked, created, updated, deleted, and skipped secrets,
// @Description error count, duration, and the timestamp of the last run.
// @Description last_run is omitted entirely when no reconcile run has completed yet — never
// @Description Go's zero time, never a fabricated timestamp.
// @Tags secrets
// @Produce json
// @Security BearerAuth
// @Success 200 {object} ReconcileStatusResponse "Reconciliation stats"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 503 {object} map[string]string "The part of Sharko that delivers addon secrets is not running on this server"
// @Router /secrets/status [get]
func (s *Server) handleReconcileStatus(w http.ResponseWriter, r *http.Request) {
	if s.secretReconciler == nil {
		writeError(w, http.StatusServiceUnavailable, secretsEngineNotRunning)
		return
	}
	stats := s.secretReconciler.GetStats()
	resp := ReconcileStatusResponse{
		Checked:  stats.Checked,
		Created:  stats.Created,
		Updated:  stats.Updated,
		Deleted:  stats.Deleted,
		Skipped:  stats.Skipped,
		Errors:   stats.Errors,
		Duration: stats.Duration,
	}
	if !stats.LastRun.IsZero() {
		resp.LastRun = stats.LastRun.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, resp)
}

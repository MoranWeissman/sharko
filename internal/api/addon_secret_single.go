package api

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
)

// addon_secret_single.go — per-row Refresh / Sync for one addon-values
// secret (S4, Managed Secrets page). Before this, the only lever on an
// addon-values secret was the fleet-wide POST /secrets/reconcile — good for
// "check everything now", useless for "check just this one row". Both
// handlers here drive internal/secrets.Reconciler's single-item path
// (CheckOne / SyncOne) instead of a whole pass, keyed by cluster+addon —
// the same granularity the System page's addon_values_secrets rows already
// read at (see system_managed_secrets.go's buildAddonValuesSecretRows).

// addonValuesSecretActionResult is the response body for both endpoints
// below. outcome mirrors internal/secrets.ItemOutcome as a plain string
// ("created", "updated", "unchanged", "out_of_sync", "missing") — never the
// secret's own content, per the honesty lock on values-secret visibility
// (S3(b)): these endpoints report what happened, not what the secret holds.
type addonValuesSecretActionResult struct {
	Status  string `json:"status"`
	Cluster string `json:"cluster"`
	Addon   string `json:"addon"`
	Outcome string `json:"outcome"`
	Message string `json:"message"`
}

// handleRefreshAddonValuesSecret godoc
//
// @Summary Re-check one addon-values secret against its source
// @Description Checks a single cluster+addon addon-values secret against the secrets provider (the vault) right now, WITHOUT writing anything — the read-only counterpart to Sync below.
// @Description Reports whether the live secret matches its source, or that it does not exist yet — never the secret's own content.
// @Tags secrets
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Param addon path string true "Addon name"
// @Success 200 {object} addonValuesSecretActionResult "Check result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden — requires operator role or higher"
// @Failure 422 {object} map[string]interface{} "Could not check — no Git connection, incomplete catalog definition, cluster unreachable, or this cluster+addon has no addon-values secret defined"
// @Failure 503 {object} map[string]interface{} "The addon-values secrets engine is not running on this server"
// @Router /clusters/{name}/addons/{addon}/secret/refresh [post]
func (s *Server) handleRefreshAddonValuesSecret(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon-secret.refresh") {
		return
	}

	cluster := r.PathValue("name")
	addon := r.PathValue("addon")

	if s.secretReconciler == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "the addon-values secrets engine is not running on this server (no Git connection or credentials provider configured) — there is nothing to check",
		})
		return
	}

	outcome, err := s.secretReconciler.CheckOne(r.Context(), cluster, addon)
	if err != nil {
		// H1 (code review): CheckOne can return a wrapped stage error
		// (credentials, connect, provider fetch, ...) whose text comes from
		// a credentials or secrets-provider SDK — the reconciler's own doc
		// on LastItemError already says callers MUST NOT render that text.
		// This used to pass err.Error() straight to writeError. The raw
		// detail is logged here, server-side only; the response body gets
		// the canned sentence.
		slog.Warn("[secrets] could not check one addon-values secret",
			"cluster", cluster, "addon", addon, "error", err, "component", "api")
		writeError(w, http.StatusUnprocessableEntity, addonValuesSecretCheckFailureSentence(err.Error()))
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "addon_values_secret_refresh_triggered",
		Resource: fmt.Sprintf("cluster:%s/addon:%s", cluster, addon),
		Detail:   fmt.Sprintf("outcome=%s", outcome),
	})

	writeJSON(w, http.StatusOK, addonValuesSecretActionResult{
		Status:  "checked",
		Cluster: cluster,
		Addon:   addon,
		Outcome: outcome,
		Message: addonValuesSecretCheckMessage(cluster, addon, outcome),
	})
}

// handleSyncAddonValuesSecret godoc
//
// @Summary Push one addon-values secret to match its source
// @Description Re-pushes a single cluster+addon addon-values secret from the secrets provider (the vault) to the remote cluster right now — creates it if missing, rotates it if the content differs, no-ops if it already matches.
// @Description Drives the same write primitive the periodic addon-values pass uses, scoped to exactly this one secret.
// @Tags secrets
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Param addon path string true "Addon name"
// @Success 200 {object} addonValuesSecretActionResult "Sync result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden — requires operator role or higher"
// @Failure 422 {object} map[string]interface{} "Could not sync — no Git connection, incomplete catalog definition, cluster unreachable, no vault configured, or this cluster+addon has no addon-values secret defined"
// @Failure 503 {object} map[string]interface{} "The addon-values secrets engine is not running on this server"
// @Router /clusters/{name}/addons/{addon}/secret/sync [post]
func (s *Server) handleSyncAddonValuesSecret(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon-secret.sync") {
		return
	}

	cluster := r.PathValue("name")
	addon := r.PathValue("addon")

	if s.secretReconciler == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "the addon-values secrets engine is not running on this server (no Git connection or credentials provider configured) — there is nothing to sync",
		})
		return
	}

	outcome, err := s.secretReconciler.SyncOne(r.Context(), cluster, addon)
	if err != nil {
		// H1 (code review): same fix as Refresh above, sync-side twin
		// sentence — SyncOne's wrapped errors are never safe to render
		// straight to the browser. Raw detail goes to the server log only.
		slog.Warn("[secrets] could not sync one addon-values secret",
			"cluster", cluster, "addon", addon, "error", err, "component", "api")
		writeError(w, http.StatusUnprocessableEntity, addonValuesSecretSyncFailureSentence(err.Error()))
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "addon_values_secret_sync_triggered",
		Resource: fmt.Sprintf("cluster:%s/addon:%s", cluster, addon),
		Detail:   fmt.Sprintf("outcome=%s", outcome),
	})

	writeJSON(w, http.StatusOK, addonValuesSecretActionResult{
		Status:  "synced",
		Cluster: cluster,
		Addon:   addon,
		Outcome: outcome,
		Message: addonValuesSecretSyncMessage(cluster, addon, outcome),
	})
}

// addonValuesSecretCheckMessage renders Refresh's outcome as one honest
// plain-English sentence. "skipped" is unreachable in practice — CheckOne
// always pairs it with a non-nil error, which the handler returns before
// ever reaching this function — but the branch stays as a defensive
// fallback rather than an assumption baked into the switch.
func addonValuesSecretCheckMessage(cluster, addon, outcome string) string {
	switch outcome {
	case "unchanged":
		return fmt.Sprintf("this secret already matches its source for cluster %q, addon %q.", cluster, addon)
	case "out_of_sync":
		return fmt.Sprintf("this secret does not match its source for cluster %q, addon %q — click Sync to push the current value.", cluster, addon)
	case "missing":
		return fmt.Sprintf("this secret does not exist yet on cluster %q — click Sync to create it.", cluster)
	default:
		return fmt.Sprintf("checked this secret for cluster %q, addon %q.", cluster, addon)
	}
}

// addonValuesSecretSyncMessage renders Sync's outcome as one honest
// plain-English sentence. "skipped"/"error" are unreachable in practice —
// SyncOne always pairs them with a non-nil error, which the handler returns
// before ever reaching this function — but the branches stay as a
// defensive fallback rather than an assumption baked into the switch.
func addonValuesSecretSyncMessage(cluster, addon, outcome string) string {
	switch outcome {
	case "created":
		return fmt.Sprintf("secret created for cluster %q, addon %q.", cluster, addon)
	case "updated":
		return fmt.Sprintf("secret updated for cluster %q, addon %q.", cluster, addon)
	case "unchanged":
		return fmt.Sprintf("nothing to push — this secret already matched its source for cluster %q, addon %q.", cluster, addon)
	default:
		return fmt.Sprintf("synced this secret for cluster %q, addon %q.", cluster, addon)
	}
}

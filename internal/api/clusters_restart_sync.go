package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"log/slog"
)

// RestartSyncResult is the response body for a successful restart-sync call.
type RestartSyncResult struct {
	Terminated bool `json:"terminated"` // true when a prior operation was terminated
	Synced     bool `json:"synced"`     // always true on success
}

// handleRestartAddonSync godoc
//
// @Summary Restart addon sync on cluster
// @Description Terminates any in-flight ArgoCD sync operation for the addon on the given cluster
// @Description and immediately re-triggers a sync. Use this to recover from a stale or permanently-
// @Description failing operation without having to open the ArgoCD UI.
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Param addon path string true "Addon name"
// @Success 200 {object} RestartSyncResult "Sync restarted"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Failure 404 {object} map[string]interface{} "Application not found"
// @Failure 502 {object} map[string]interface{} "ArgoCD gateway error"
// @Router /clusters/{name}/addons/{addon}/restart-sync [post]
func (s *Server) handleRestartAddonSync(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.restart-sync") {
		return
	}

	clusterName := r.PathValue("name")
	addonName := r.PathValue("addon")
	if clusterName == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}
	if addonName == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "addon_sync_restarted",
		Resource: fmt.Sprintf("cluster:%s addon:%s", clusterName, addonName),
	})

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	// Resolve the application name: Sharko's naming convention is addon-cluster.
	appName := addonName + "-" + clusterName
	app, err := ac.GetApplication(r.Context(), appName)
	if err != nil {
		writeError(w, http.StatusNotFound,
			fmt.Sprintf("application %q not found in ArgoCD: %s", appName, err.Error()))
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("application %q not found in ArgoCD", appName))
		return
	}

	result := RestartSyncResult{}

	// Terminate only when an operation is actively in flight.
	// ArgoCD retains the last operationState (phase Failed/Succeeded/Error) after
	// an operation finishes; attempting to terminate a finished op returns a 400
	// "No operation is in progress" error and blocks the subsequent sync.
	// Use OperationFinishedAt (populated by #418) when available; fall back to
	// phase-only check (Running or Terminating = still active).
	opInFlight := false
	if app.OperationFinishedAt != "" {
		opInFlight = false // finishedAt set → op is done, nothing to terminate
	} else if app.OperationPhase == "Running" || app.OperationPhase == "Terminating" {
		opInFlight = true
	}

	if opInFlight {
		if err := ac.TerminateOperation(r.Context(), appName); err != nil {
			// Benign race: ArgoCD reports "No operation is in progress" (400).
			// The op finished between our GetApplication call and the DELETE.
			// Treat as harmless — continue to sync.
			if isBenignTerminateError(err) {
				slog.Warn("restart-sync: terminate returned benign 'no operation' error; continuing to sync",
					"app", appName, "error", err)
			} else {
				writeError(w, http.StatusBadGateway, "failed to terminate operation: "+err.Error())
				return
			}
		} else {
			result.Terminated = true
		}
	}

	// Re-trigger sync.
	if err := ac.SyncApplication(r.Context(), appName); err != nil {
		writeError(w, http.StatusBadGateway, "failed to sync application: "+err.Error())
		return
	}
	result.Synced = true

	writeJSON(w, http.StatusOK, result)
}

// isBenignTerminateError returns true when an TerminateOperation error
// indicates that no operation was in progress — i.e. the operation already
// finished between our GetApplication check and the terminate call. These
// race-window errors are safe to ignore; the subsequent sync call should
// proceed normally.
func isBenignTerminateError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no operation is in progress") ||
		(strings.Contains(msg, "unexpected status 400") && strings.Contains(msg, "no operation"))
}

package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"log/slog"
)

// syncNotPermitted is what an operator is told when the ArgoCD account Sharko
// uses may not sync applications.
//
// It is one fixed sentence, and it is deliberate that it names the permission,
// says who grants it, and says what still works without it. Sharko does not
// grant itself this permission and installing Sharko does not change ArgoCD's
// permission settings, so an operator who never added the policy has no reason
// to expect this action to work — and being told "ArgoCD gateway error" would
// send them looking for a broken ArgoCD instead.
const syncNotPermitted = "Restarting a sync asks ArgoCD to sync an application, and the ArgoCD account " +
	"Sharko is configured with is not allowed to do that. Installing Sharko does not grant this " +
	"permission — an ArgoCD administrator adds it, scoped to Sharko's own project. Everything else is " +
	"unaffected: Git still holds the desired state and ArgoCD still applies it on its own schedule. " +
	"See the Sharko operator security page, section \"Letting Sharko restart a sync\"."

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
		writeNoActiveArgocdConnection(w, r)
		return
	}

	// Resolve the application name: Sharko's naming convention is addon-cluster.
	appName := addonName + "-" + clusterName
	app, err := ac.GetApplication(r.Context(), appName)
	if err != nil {
		// The reason lives in the server log, found by the request id, and
		// nowhere else. What came back from ArgoCD is ArgoCD's own text and
		// Sharko does not repeat it — that rule holds whether or not today's
		// trace says a token could reach this particular line.
		slog.Error("restart-sync: could not read the application from ArgoCD", "app", appName, "error", err)
		writeError(w, http.StatusNotFound,
			fmt.Sprintf("Sharko could not read application %q from ArgoCD. Open the application in ArgoCD, and check Sharko's ArgoCD connection in Settings.", appName))
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

	// Ask ArgoCD whether this is allowed BEFORE terminating anything. A
	// definite "no" means the whole action is unavailable, and saying so here
	// avoids the shape this endpoint used to have: terminate the running
	// operation, then get refused on the sync, leaving the application worse
	// off than before the button was pressed. An unanswerable check falls
	// through — the sync call below is the authority.
	if ac.CanSyncApplication(r.Context(), app.Project, appName) == argocd.CapabilityDenied {
		writeError(w, http.StatusForbidden, syncNotPermitted)
		return
	}

	if opInFlight {
		if err := ac.TerminateOperation(r.Context(), appName); err != nil {
			// Benign race: there was nothing left to terminate. The operation
			// finished between our GetApplication call and the DELETE. The
			// ArgoCD client decides this from the call it made and the status
			// it got back, and says so with a sentinel — nothing here reads
			// ArgoCD's words to work it out.
			if errors.Is(err, argocd.ErrNoOperationInProgress) {
				slog.Warn("restart-sync: terminate returned benign 'no operation' error; continuing to sync",
					"app", appName, "error", err)
			} else {
				writeError(w, http.StatusBadGateway,
					"Sharko could not stop the sync that was already running. "+argocd.SafeWriteFailure(err))
				return
			}
		} else {
			result.Terminated = true
		}
	}

	// Re-trigger sync.
	if err := ac.SyncApplication(r.Context(), appName); err != nil {
		// A refusal is not a gateway failure. ArgoCD answered, and what it
		// said was "not allowed" — which is a 403 with the explanation above,
		// never a 502 that sends the operator hunting for a broken ArgoCD.
		if errors.Is(err, argocd.ErrPermissionDenied) {
			writeError(w, http.StatusForbidden, syncNotPermitted)
			return
		}
		writeError(w, http.StatusBadGateway,
			"Sharko could not start a new sync for this addon. "+argocd.SafeWriteFailure(err))
		return
	}
	result.Synced = true

	writeJSON(w, http.StatusOK, result)
}

// The old isBenignTerminateError lived here. It lowercased the error and
// searched it for the words "no operation is in progress" — Sharko deciding
// what to do next by reading ArgoCD's prose. internal/argocd now says the same
// thing with a type (argocd.ErrNoOperationInProgress), decided from the call
// and the status code, so the reply itself can be thrown away.

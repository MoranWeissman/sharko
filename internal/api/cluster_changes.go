package api

import (
	"net/http"

	"github.com/MoranWeissman/sharko/internal/changelog"
	"github.com/MoranWeissman/sharko/internal/models"
)

// clusterChangeResponse is a single durable change-log entry
// (internal/changelog.Entry) joined at READ TIME with the addon's CURRENT
// ArgoCD sync/health status. Sharko does not persist "what the outcome was
// the moment the PR merged" — DeployOutcome always reflects live ArgoCD
// state as of this request, computed fresh every call.
type clusterChangeResponse struct {
	changelog.Entry
	DeployOutcome string `json:"deploy_outcome"` // "healthy" | "failed" | "unknown"
}

// handleGetClusterChanges godoc
//
// @Summary Get cluster change log
// @Description Returns the durable, capped (100 entries/cluster) change log for a cluster — one
// @Description entry per completed (merged or closed) pull request, newest first. Each entry is
// @Description joined at read time with the addon's current ArgoCD deploy outcome. If ArgoCD is
// @Description unreachable the entries still return, with deploy_outcome "unknown" for every
// @Description entry — this endpoint never fails just because ArgoCD is unavailable.
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /clusters/{name}/changes [get]
func (s *Server) handleGetClusterChanges(w http.ResponseWriter, r *http.Request) {
	clusterName := r.PathValue("name")
	if clusterName == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}

	entries := s.changeLogStore.List(clusterName)

	// Best-effort live join: an unreachable/unconfigured ArgoCD connection
	// must never turn this into a 5xx — every entry still returns, just
	// with deploy_outcome "unknown".
	var overview *models.ObservabilityOverviewResponse
	if ac, err := s.connSvc.GetActiveArgocdClient(); err == nil {
		gp, gpErr := s.connSvc.GetActiveGitProvider()
		if gpErr != nil {
			gp = nil
		}
		if ov, ovErr := s.observabilitySvc.GetOverview(r.Context(), ac, gp); ovErr == nil {
			overview = ov
		}
	}

	changes := make([]clusterChangeResponse, 0, len(entries))
	for _, e := range entries {
		changes = append(changes, clusterChangeResponse{
			Entry:         e,
			DeployOutcome: deployOutcomeFor(overview, e.Addon, clusterName),
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"cluster_name": clusterName,
		"changes":      changes,
	})
}

// deployOutcomeFor looks up the addon's current per-cluster ArgoCD health
// from an already-fetched observability overview (see
// internal/api/cluster_history.go for the same GetOverview usage pattern).
// Returns "unknown" whenever the overview is unavailable (ArgoCD
// unreachable/unconfigured) or the addon+cluster pair has no current
// health entry (e.g. the addon was since removed). Otherwise maps ArgoCD's
// "Healthy"/"Degraded" health strings (see internal/service/observability.go)
// to the coarse "healthy"/"failed" outcome; any other ArgoCD health value
// (Progressing, Missing, Suspended, Unknown, "") also maps to "unknown" —
// deploy_outcome only makes a firm call when ArgoCD is unambiguous.
func deployOutcomeFor(overview *models.ObservabilityOverviewResponse, addon, cluster string) string {
	if overview == nil {
		return "unknown"
	}
	for _, detail := range overview.AddonHealth {
		if detail.AddonName != addon {
			continue
		}
		for _, ch := range detail.Clusters {
			if ch.ClusterName != cluster {
				continue
			}
			switch ch.Health {
			case "Healthy":
				return "healthy"
			case "Degraded":
				return "failed"
			default:
				return "unknown"
			}
		}
	}
	return "unknown"
}

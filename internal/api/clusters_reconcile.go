package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/models"
)

// clusters_reconcile.go — per-cluster reconcile visibility + manual "sync
// now" (V2-cleanup-89.4). Before this, a failed cluster-secret reconcile
// (a vault fetch error, a rejected K8s API call) was visible only in the
// server log — ArgoCD shows a failed apply, Sharko showed nothing. This
// file wires:
//
//  1. applyLastReconcile — projects clusterreconciler.Reconciler's
//     in-memory per-cluster record (reconcile_status.go) onto the cluster
//     read model. Called from the same three read surfaces that already
//     compute TargetPlatform / AddonSecretsReady (handleListClusters,
//     handleGetCluster, handleGetClusterComparison in clusters.go).
//  2. handleReconcileCluster — POST /clusters/{name}/reconcile, a manual
//     "sync now" the UI can call instead of waiting for the reconciler's
//     30s safety-net tick.

// applyLastReconcile sets c.LastReconcile from the reconciler's in-memory
// per-cluster record, if one exists. A no-op when recon is nil (reconciler
// not wired in this deployment mode) or when the reconciler has never
// processed this cluster (fresh startup, or a registration PR that hasn't
// merged yet) — the field is left nil/omitted either way.
//
// V3 G1: also copies the LabelDrift field when present.
func applyLastReconcile(c *models.Cluster, recon *clusterreconciler.Reconciler) {
	if recon == nil {
		return
	}
	rec, ok := recon.LastReconcile(c.Name)
	if !ok {
		return
	}
	lastRec := &models.ClusterLastReconcile{
		Time:    rec.Time.Format(time.RFC3339),
		Outcome: string(rec.Outcome),
		Message: rec.Message,
	}
	// V3 G1 — copy drift info if present
	if rec.LabelDrift != nil {
		lastRec.LabelDrift = &models.ClusterLastReconcileLabelDrift{
			Added:   rec.LabelDrift.Added,
			Removed: rec.LabelDrift.Removed,
			Changed: rec.LabelDrift.Changed,
		}
	}
	c.LastReconcile = lastRec
}

// handleReconcileCluster godoc
//
// @Summary Trigger a manual cluster reconcile
// @Description Nudges the cluster-secret reconciler to run immediately instead of waiting for its periodic tick.
// @Description This is a fleet-wide pass, not a targeted single-cluster reconcile — see the 202 response message.
// @Description Returns 202 as soon as the trigger is accepted — the reconcile itself runs asynchronously.
// @Description Poll GET /clusters/{name} and read the updated last_reconcile field once the pass completes.
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Success 202 {object} map[string]interface{} "Reconcile triggered"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden — requires operator role or higher"
// @Failure 404 {object} map[string]interface{} "Cluster not found"
// @Failure 503 {object} map[string]interface{} "Reconciler not running on this server"
// @Router /clusters/{name}/reconcile [post]
// handleReconcileCluster handles POST /api/v1/clusters/{name}/reconcile.
//
// IMPLEMENTATION NOTE — global pass, not a targeted single-cluster
// reconcile: this fires the reconciler's existing Trigger() channel, the
// same low-latency nudge prTracker uses after a PR merge. Reconciler.pollOnce
// always diffs the full desired-vs-live set in one pass (see
// internal/clusterreconciler/reconciler.go); carving out a scoped
// single-cluster code path would duplicate that diff logic for no real
// latency win — a full pass over the fleet is the same work the 30s
// safety-net tick already does continuously, so triggering it early is
// cheap. The UI polls GET /clusters/{name} afterward and reads the updated
// last_reconcile field once the triggered pass completes.
//
// Handler order (V2-cleanup-90.3 / review finding L2): the cheap
// "is a reconciler even wired on this server" 503 check runs FIRST, before
// the Git/ArgoCD round-trips needed for the 404 existence check — no reason
// to pay for two upstream calls just to then discover there is nowhere to
// route the trigger. The 404 check still runs after that gate, so a request
// for an unknown cluster on a server WITH a reconciler wired still gets 404
// rather than a false 202.
//
// name is never empty here: the only route to this handler is
// "POST /clusters/{name}/reconcile" (see router.go), and Go 1.22
// ServeMux path-cleaning redirects "/clusters//reconcile" away from this
// pattern before the handler ever runs — so there is no reachable path that
// reaches this handler with an empty PathValue("name").
func (s *Server) handleReconcileCluster(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.reconcile") {
		return
	}

	name := r.PathValue("name")

	if s.reconcilerTrigger == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "the cluster reconciler is not running on this server (no in-cluster Kubernetes client or credentials provider configured) — addon labels are not auto-synced to ArgoCD on this deployment",
		})
		return
	}

	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeServerError(w, http.StatusServiceUnavailable, "get_active_git_provider", err)
		return
	}
	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeServerError(w, http.StatusServiceUnavailable, "get_active_argocd_client", err)
		return
	}

	detail, err := s.clusterSvc.GetClusterDetail(r.Context(), name, gp, ac)
	if err != nil {
		writeUpstreamError(w, "reconcile_cluster", err)
		return
	}
	if detail == nil {
		writeError(w, http.StatusNotFound, "cluster not found")
		return
	}

	s.reconcilerTrigger()

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "cluster_reconcile_triggered",
		Resource: fmt.Sprintf("cluster:%s", name),
	})

	writeJSON(w, http.StatusAccepted, map[string]string{
		"status":  "accepted",
		"message": fmt.Sprintf("reconcile pass triggered — the fleet-wide pass includes cluster %q", name),
	})
}

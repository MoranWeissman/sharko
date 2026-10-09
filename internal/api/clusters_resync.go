package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/models"
)

// resyncFailNotRunning is the 503 a person reads when this server has no
// cluster-connection machinery wired at all.
//
// It names no component and no wiring. The old text said "the cluster
// reconciler is not running on this server (no in-cluster Kubernetes client
// or credentials provider configured)" — which named Sharko's internals
// twice over, once as a component and once as its plumbing. Neither is a
// thing the reader has ever heard of, and neither tells them anything they
// can act on. The house wording for this exact situation is already in
// connection_comparison.go (failNoReconciler) and connection_repair.go
// (repairFailNoReconciler); this is the same sentence with the resync tail.
const resyncFailNotRunning = "The part of Sharko that manages cluster connections is not running on this server, so there is nothing to resync."

// clusters_resync.go — one-time re-sync from the drift view (v4-8-5).
//
// handleReconcileCluster (clusters_reconcile.go) nudges the reconciler's
// fleet-wide pass early; it does NOT correct drift when
// managed_cluster_self_heal is OFF (the tick only records the drift then).
// This handler is different on purpose: POST /clusters/{name}/resync always
// re-applies Sharko's own addon-label keys for ONE cluster, ONCE, to match
// Git — regardless of the self-heal setting, and without reading or
// changing that setting. It is the "Re-sync now" action on the drift view.
//
// It does this by calling clusterreconciler.Reconciler.ResyncClusterLabels,
// which itself reuses the reconciler's existing write primitives
// (selfHealManagedCluster / syncSelfManaged) — there is exactly one code
// path that ever writes these labels, tick or on-demand.

// handleResyncCluster godoc
//
// @Summary Re-sync one cluster's addon labels to match Git (one time)
// @Description Re-applies Sharko's own addon-label keys onto this cluster's ArgoCD cluster Secret ONCE, to match Git.
// @Description Regardless of the managed_cluster_self_heal setting, and without reading or changing it.
// @Description Touches only Sharko's own addon-label keys — never foreign labels, Secret Data, or annotations.
// @Description Reports the label diff this resync applied (added / removed / changed / unchanged).
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Success 200 {object} models.ClusterResyncResponse "Resync result"
// @Failure 400 {object} map[string]interface{} "Cluster has no entry in the Git-managed cluster list"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden — requires operator role or higher"
// @Failure 404 {object} map[string]interface{} "Cluster not found"
// @Failure 503 {object} map[string]interface{} "The part of Sharko that manages cluster connections is not running on this server"
// @Router /clusters/{name}/resync [post]
//
// Handler order mirrors handleReconcileCluster (V2-cleanup-90.3 / review
// finding L2): the cheap "is a reconciler even wired" 503 check runs first,
// before the Git/ArgoCD round-trips needed for the 404 existence check.
func (s *Server) handleResyncCluster(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.resync") {
		return
	}

	name := r.PathValue("name")

	if s.clusterRecon == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": resyncFailNotRunning,
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
		writeUpstreamError(w, "resync_cluster", err)
		return
	}
	if detail == nil {
		writeError(w, http.StatusNotFound, "cluster not found")
		return
	}

	result, err := s.clusterRecon.ResyncClusterLabels(r.Context(), name)
	if err != nil {
		if errors.Is(err, clusterreconciler.ErrClusterNotManaged) {
			writeError(w, http.StatusBadRequest, "this cluster has no entry in the Git-managed cluster list (managed-clusters.yaml / managed-clusters.yaml) — nothing to resync")
			return
		}
		writeUpstreamError(w, "resync_cluster", err)
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "cluster_resync_triggered",
		Resource: fmt.Sprintf("cluster:%s", name),
		Detail:   fmt.Sprintf("outcome=%s", result.Outcome),
	})

	writeJSON(w, http.StatusOK, models.ClusterResyncResponse{
		Status:  "resynced",
		Cluster: name,
		Outcome: string(result.Outcome),
		Message: resyncMessage(name, result),
		LabelDiff: models.ClusterResyncLabelDiff{
			Added:     result.Added,
			Removed:   result.Removed,
			Changed:   result.Changed,
			Unchanged: result.Unchanged,
		},
	})
}

// resyncMessage builds the plain-English confirmation the UI shows after a
// resync — always states what happened AND that the self-heal setting was
// left alone, per the story's contract for this action.
func resyncMessage(name string, result clusterreconciler.ResyncResult) string {
	switch result.Outcome {
	case clusterreconciler.OutcomeSucceeded:
		if len(result.Added) == 0 && len(result.Removed) == 0 && len(result.Changed) == 0 {
			return fmt.Sprintf("cluster %q already matched Git — no addon labels needed to change. The self-heal setting was not changed.", name)
		}
		return fmt.Sprintf("re-applied Sharko's addon labels for cluster %q to match Git — %d added, %d removed, %d changed. The self-heal setting was not changed.",
			name, len(result.Added), len(result.Removed), len(result.Changed))
	case clusterreconciler.OutcomeSkipped:
		msg := result.Message
		if msg == "" {
			msg = "nothing to resync yet."
		}
		return fmt.Sprintf("cluster %q resync was skipped — %s The self-heal setting was not changed.", name, msg)
	default: // OutcomeFailed
		// B10: this used to be result.Message, which is the reconciler's RAW
		// per-cluster record text — and several of the call sites that write
		// that record build it by appending a Kubernetes API or git-provider
		// error straight onto a fixed English prefix (see
		// clusterreconciler.FailureSentence's own doc). So the raw error text
		// reached this 200 body, on the one path that skipped the choke point
		// every other reader of that record already went through:
		// Reconciler.LastError, applyLastReconcile's lastReconcileMessage and
		// connectionSecretCheckError all map it first. This one did not.
		//
		// Only the Failed branch is mapped, matching lastReconcileMessage
		// exactly: a skipped record carries one of this package's own fixed
		// sentences and never wrapped error text, and mapping an
		// already-safe sentence a second time collapses it into the generic
		// fallback.
		msg := clusterreconciler.FailureSentence(result.Message)
		if msg == "" {
			msg = "the resync failed."
		}
		return fmt.Sprintf("cluster %q resync failed — %s The self-heal setting was not changed.", name, msg)
	}
}

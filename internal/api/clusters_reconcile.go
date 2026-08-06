package api

import (
	"context"
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
		Time:             rec.Time.Format(time.RFC3339),
		Outcome:          string(rec.Outcome),
		Message:          rec.Message,
		ComparedRevision: rec.ComparedRevision,
		ComparedPath:     rec.ComparedPath,
		AppliedRevision:  rec.AppliedRevision,
		// P2-D D3: the connection engine's fight counter, read straight off
		// the reconciler by cluster name — 0 for every cluster that has
		// never had a fight-detection tick run against it (Sharko-managed
		// clusters included; FightCount is nil-safe and only ever nonzero
		// for a self-managed connection mid-fight).
		FightCount: recon.FightCount(c.Name),
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

// applyManagedSecretFields sets the two fields the "Managed cluster secret"
// panel needs (walk day 4 locks, S1 + S2):
//
//   - ManagedSecretName: the real "namespace/name" of the ArgoCD cluster
//     Secret this cluster's page is about — the same Secret argosecrets and
//     the reconciler manage. The Secret's Name always equals c.Name (see
//     argosecrets.Manager.Ensure) and its Namespace is whatever the
//     reconciler is configured to write to, so this is a deterministic fact
//     about the live Secret, not a display guess.
//   - AlreadyManagedBySharko: true when that Secret already carries the
//     app.kubernetes.io/managed-by=sharko label. This mirrors the exact
//     check clusters_takeover.go's preflight uses for its "secret-owner"
//     finding (in.ManagedBy == argosecrets.ManagedByValue) — a cluster
//     already owned by Sharko has nothing left for "Take ownership" to do,
//     so the UI hides that button instead of showing a no-op action.
//
// Both are a no-op when s.clusterRecon is not wired (out-of-cluster, or no
// credentials provider) or the Secret cannot be read — the fields simply
// stay at their zero values (omitted from JSON), which is the safe
// direction: the title falls back to its generic form and the takeover
// button stays visible rather than being hidden on a guess.
func (s *Server) applyManagedSecretFields(ctx context.Context, c *models.Cluster) {
	_, ns, ok := s.k8sClientAndNamespace()
	if ok && ns != "" {
		c.ManagedSecretName = ns + "/" + c.Name
	}
	secret, err := s.getSecretIfPresent(ctx, c.Name)
	if err == nil && secret != nil {
		c.AlreadyManagedBySharko = clusterreconciler.IsManagedBySharko(secret)
	}
}

// handleReconcileCluster godoc
//
// @Summary Check cluster connection secrets against git
// @Description Asks the cluster-secret reconciler to CHECK right now instead of waiting for its periodic pass: it reads git, reads the live ArgoCD cluster secrets, compares them, and records what it found. Nothing is created, changed or deleted.
// @Description This check covers every cluster, not only the one named in the path — see the 202 response message.
// @Description To actually apply what a check found, use POST /clusters/{name}/resync.
// @Description Returns 202 as soon as the check is accepted — the check itself runs asynchronously.
// @Description Poll GET /clusters/{name} and read the updated last_reconcile field once the check completes.
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
// IMPLEMENTATION NOTE — this is a CHECK, and it covers every cluster.
//
// It fires the reconciler's TriggerCheck() channel (P1-A A2), which runs the
// read-only pass: read git, list the live ArgoCD cluster secrets, compare,
// record. No secret is created, changed or deleted by anything this handler
// starts. That matters because every button in the UI that reaches this
// endpoint is labelled "Refresh" and its help text says it checks — before
// this, it fired the full write pass instead, which created secrets, deleted
// orphans and stamped labels.
//
// Applying what a check found is a different endpoint on purpose:
// POST /clusters/{name}/resync (handleResyncCluster). The 30-second
// safety-net loop and the post-merge trigger still run the WRITE pass —
// that loop is the agent doing continuous reconciliation, and nothing here
// changes it.
//
// The check is not scoped to one cluster: the reconciler always diffs the
// full desired-vs-live set in one pass, and carving out a single-cluster
// code path would duplicate that logic for no real win. The cluster name in
// the path still matters — it is what the 404 check below verifies, and it
// is what the response message names — but the pass covers the rest too,
// and the message says so. The UI polls GET /clusters/{name} afterward and
// reads the updated last_reconcile field once the check completes.
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

	if s.reconcilerCheckTrigger == nil {
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

	s.reconcilerCheckTrigger()

	// P3-F1: the entry states the REAL blast radius. This endpoint takes one
	// cluster name in its path but the check it starts covers every cluster
	// (see the note above), and the old detail line — "read-only check of
	// every cluster's connection secret" — left a reader with no idea
	// whether "every" meant 3 clusters or 300. clusterCheckBlastRadius
	// counts what the reconciler actually holds records for, and says
	// nothing numeric when it cannot count.
	audit.Enrich(r.Context(), audit.Fields{
		Event:    "cluster_connection_secret_check_triggered",
		Resource: fmt.Sprintf("cluster:%s", name),
		Detail:   clusterCheckBlastRadius(s.clusterRecon.KnownClusterCount()),
	})

	writeJSON(w, http.StatusAccepted, map[string]string{
		"status":  "accepted",
		"message": fmt.Sprintf("checking every cluster's connection secret now, %q included — nothing is written", name),
	})
}

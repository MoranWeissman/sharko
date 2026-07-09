package clusterreconciler

import "time"

// reconcile_status.go — per-cluster reconcile visibility (V2-cleanup-89.4).
//
// Before this, a reconcile failure for a single cluster (a vault fetch
// error, a K8s API rejection, a self-managed connection still waiting on
// the user) went to slog + the audit log only. An operator looking at one
// cluster had no way to tell whether the last reconcile attempt for THAT
// cluster succeeded, failed, or was deliberately skipped — ArgoCD shows a
// failed apply; Sharko showed nothing. This file adds an in-memory,
// thread-safe last-outcome record per cluster name, and a getter the API
// layer uses to project it onto the cluster read model
// (models.Cluster.LastReconcile).
//
// Deliberately in-memory only, never persisted: this is derived,
// self-healing state exactly like the reconcile loop itself — every tick
// recomputes the outcome for every cluster it touches, so a server
// restart losing the history is a non-issue (the next tick, at most
// DefaultTickInterval later, repopulates it).

// ReconcileOutcome classifies the result of the most recent reconcile
// attempt for a single cluster.
type ReconcileOutcome string

const (
	// OutcomeSucceeded means the cluster's ArgoCD cluster Secret is in sync
	// with managed-clusters.yaml as of this tick — created just now,
	// already up to date from a previous tick, or (self-managed
	// connections) addon labels synced onto the user's Secret.
	OutcomeSucceeded ReconcileOutcome = "succeeded"

	// OutcomeFailed means this tick attempted a write for this cluster and
	// it failed — a vault credential fetch, building the Secret payload, or
	// the ArgoCD cluster Secret K8s API call itself.
	OutcomeFailed ReconcileOutcome = "failed"

	// OutcomeSkipped means this tick deliberately did not write anything
	// for this cluster — not an error, but not "in sync and done" either.
	// Covers: an unlabeled same-name Secret already exists (Adopt
	// territory), or a self-managed connection's Secret hasn't been
	// created by the user yet.
	OutcomeSkipped ReconcileOutcome = "skipped"
)

// ClusterReconcileRecord is the last known reconcile outcome for one
// cluster. Message is a plain-English explanation of what happened —
// populated on Failed and Skipped, empty on Succeeded.
type ClusterReconcileRecord struct {
	Time    time.Time
	Outcome ReconcileOutcome
	Message string
}

// recordReconcile stores the outcome of the most recent reconcile attempt
// for a single cluster, overwriting any previous record. Uses the
// reconciler's clock seam (r.now()) rather than time.Now() directly so
// tests can drive a deterministic timestamp the same way they do for the
// registration-pending grace window.
func (r *Reconciler) recordReconcile(name string, outcome ReconcileOutcome, message string) {
	r.lastReconcileMu.Lock()
	defer r.lastReconcileMu.Unlock()
	if r.lastReconcile == nil {
		r.lastReconcile = make(map[string]ClusterReconcileRecord)
	}
	r.lastReconcile[name] = ClusterReconcileRecord{
		Time:    r.now(),
		Outcome: outcome,
		Message: message,
	}
}

// LastReconcile returns the most recently recorded reconcile outcome for
// the named cluster. ok is false when no reconcile has ever run for this
// cluster on this server instance — a fresh startup before the first tick,
// or a cluster whose registration PR hasn't merged into
// managed-clusters.yaml yet. Safe to call concurrently with the reconcile
// goroutine; this is the read side the API layer polls per request.
func (r *Reconciler) LastReconcile(name string) (ClusterReconcileRecord, bool) {
	r.lastReconcileMu.RLock()
	defer r.lastReconcileMu.RUnlock()
	rec, ok := r.lastReconcile[name]
	return rec, ok
}

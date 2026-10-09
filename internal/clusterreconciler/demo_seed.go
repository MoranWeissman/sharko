package clusterreconciler

import "time"

// demo_seed.go — a narrow, exported seam that lets demo mode (and tests)
// put a specific, chosen outcome into a Reconciler's in-memory per-cluster
// record, without running a real reconcile pass against a real (or fake)
// Kubernetes API.
//
// FOR DEMO MODE AND TESTS ONLY. Production wiring (cmd/sharko/serve.go)
// must never call this — the real reconciler always derives
// ClusterReconcileRecord from an actual git-vs-live comparison
// (reconcileDiff / syncSelfManaged), and this method bypasses that entirely.
// Calling it from a real deployment would let the System page's Managed
// Secrets view show a state Sharko never actually observed.

// SeedReconcileRecordForDemo sets the named cluster's in-memory reconcile
// record directly to the given outcome/message/drift/timestamp, the same
// shape recordReconcile writes on a real tick. FOR DEMO MODE AND TESTS
// ONLY — see the package doc comment above.
//
// Exists so internal/demo can render a believable spread of connection-secret
// states (in sync / out of sync / missing / unknown) on a generated estate
// without actually running the reconcile loop against the demo's fake
// Kubernetes clientset, which would need the fake Secrets, git-rendered addon
// labels, and self-heal setting to all agree in order to land on a chosen
// state — far more moving parts than a demo needs to control to show the
// four honest states connectionSecretState (internal/api) already computes
// from Outcome + Message + LabelDrift.
func (r *Reconciler) SeedReconcileRecordForDemo(clusterName string, outcome ReconcileOutcome, message string, at time.Time, drift *LabelDrift) {
	r.lastReconcileMu.Lock()
	defer r.lastReconcileMu.Unlock()
	if r.lastReconcile == nil {
		r.lastReconcile = make(map[string]ClusterReconcileRecord)
	}
	r.lastReconcile[clusterName] = ClusterReconcileRecord{
		Time:       at,
		Outcome:    outcome,
		Message:    message,
		LabelDrift: drift,
	}
}

// SeedReconcileRevisionForDemo (P2-C4) patches the two-revision + path facts
// (P2-C1) onto a cluster's ALREADY-seeded record — call this after
// SeedReconcileRecordForDemo for the same cluster. A separate method rather
// than widening SeedReconcileRecordForDemo's own parameter list: that
// function's shape mirrors what a real tick writes in one call
// (recordReconcile), while these three facts are demo-only bookkeeping this
// package's real write path derives from its OWN pass-level/per-cluster
// state (setPassCompared / stampAppliedRevision) — state a direct record
// seed has no pass to derive them from. FOR DEMO MODE AND TESTS ONLY — see
// the package doc comment above.
//
// appliedRevision is set on the reconciler's persisted per-cluster map
// (the same one stampAppliedRevision writes) rather than only on this one
// record, so it also flows into whatever THIS cluster's record looks like
// after a later demo "Refresh" re-seed — matching production's "a check
// pass carries AppliedRevision forward untouched" contract.
func (r *Reconciler) SeedReconcileRevisionForDemo(clusterName, comparedRevision, comparedPath, appliedRevision string) {
	if appliedRevision != "" {
		r.appliedRevMu.Lock()
		if r.appliedRevision == nil {
			r.appliedRevision = make(map[string]string)
		}
		r.appliedRevision[clusterName] = appliedRevision
		r.appliedRevMu.Unlock()
	}

	r.lastReconcileMu.Lock()
	defer r.lastReconcileMu.Unlock()
	rec, ok := r.lastReconcile[clusterName]
	if !ok {
		return
	}
	rec.ComparedRevision = comparedRevision
	rec.ComparedPath = comparedPath
	rec.AppliedRevision = appliedRevision
	r.lastReconcile[clusterName] = rec
}

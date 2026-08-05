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

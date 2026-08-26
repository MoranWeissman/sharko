package secrets

import (
	"context"
	"errors"
	"testing"

	"k8s.io/client-go/kubernetes/fake"
)

// enabled_switch_test.go — gitops-proud P4-I (D2), the addon-values
// engine's own off switch. Fixtures reused from reconciler_test.go /
// ownership_gate_test.go: standardGitReader wires catalogWithSecrets
// (addon "datadog", secret "datadog-secret" in "monitoring") against
// clusterAddonsYAML (cluster "prod-cluster" with "datadog" enabled).

// alwaysDisabled/alwaysEnabled are trivial SetEnabledFn callbacks — the
// gate itself doesn't care about ctx, so the fixtures don't need to either.
func alwaysDisabled(context.Context) bool { return false }
func alwaysEnabled(context.Context) bool  { return true }

func disableableReconciler(client *fake.Clientset) *Reconciler {
	return newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("the-api-key"),
			"secrets/datadog/app-key": []byte("the-app-key"),
		}},
		fakeRemoteClientFn(client),
	)
}

// TestReconcile_Disabled_RunsNoPass — the WRITE pass. Switched off, a tick
// must not create, update or delete anything, and must not even touch
// stats (mirrors TestReconcile_NoGitConnection's "quiet no-op" contract —
// an admin turning the engine off is not a failed run).
func TestReconcile_Disabled_RunsNoPass(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := disableableReconciler(client)
	r.SetEnabledFn(alwaysDisabled)

	r.reconcile()

	if got := writeActions(client); len(got) != 0 {
		t.Fatalf("reconcile() wrote while the engine was switched off: %v", got)
	}
	stats := r.GetStats()
	if stats.Checked != 0 || stats.Created != 0 || stats.Updated != 0 || stats.Errors != 0 {
		t.Errorf("expected all-zero stats while switched off, got %+v", stats)
	}
}

// TestCheckAll_Disabled_ReturnsErrReconcilerDisabled — the CHECK pass.
// Switched off, "Check all now" has nothing to check with — same shape as
// TestCheckAll_NoGitConnection, a real error for this request-driven call.
func TestCheckAll_Disabled_ReturnsErrReconcilerDisabled(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := disableableReconciler(client)
	r.SetEnabledFn(alwaysDisabled)

	if err := r.CheckAll(context.Background()); !errors.Is(err, ErrReconcilerDisabled) {
		t.Errorf("CheckAll error = %v, want ErrReconcilerDisabled", err)
	}
	if got := writeActions(client); len(got) != 0 {
		t.Fatalf("CheckAll wrote while the engine was switched off: %v", got)
	}
}

// TestReconcile_EnabledFn_Nil_RunsNormally — no SetEnabledFn call at all
// (out-of-cluster/dev mode, no settings store wired) must behave exactly
// like every other existing reconcile test: the engine stays on.
func TestReconcile_EnabledFn_Nil_RunsNormally(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := disableableReconciler(client)
	// No SetEnabledFn call — enabledFn is nil.

	r.reconcile()

	stats := r.GetStats()
	if stats.Created != 1 {
		t.Errorf("expected the normal pass to create the missing secret when no enabledFn is wired, got stats %+v", stats)
	}
}

// TestReconcile_EnabledFn_ExplicitlyTrue_RunsNormally proves the gate is a
// real read of the callback, not a hardcoded skip — flipping it back to
// true lets the pass run.
func TestReconcile_EnabledFn_ExplicitlyTrue_RunsNormally(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := disableableReconciler(client)
	r.SetEnabledFn(alwaysEnabled)

	r.reconcile()

	stats := r.GetStats()
	if stats.Created != 1 {
		t.Errorf("expected the pass to create the missing secret when enabledFn reports true, got stats %+v", stats)
	}
}

// TestCheckOne_IgnoresTheEngineSwitch — a single, explicitly-requested
// action (the row's own "Refresh" button) is NOT gated by the fleet-wide
// off switch, the same design the managed_cluster_self_heal precedent set
// for its own manual resync path (clusterreconciler.ResyncClusterLabels):
// an operator clicking one row is presumed to want it done regardless of
// the automation switch. Only the two PASSES (reconcile/CheckAll) stop.
func TestCheckOne_IgnoresTheEngineSwitch(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := disableableReconciler(client)
	r.SetEnabledFn(alwaysDisabled)

	outcome, err := r.CheckOne(context.Background(), "prod-cluster", "datadog")
	if err != nil {
		t.Fatalf("CheckOne while switched off: unexpected error: %v", err)
	}
	if outcome != string(ItemOutcomeMissing) {
		t.Errorf("outcome = %q, want %q", outcome, ItemOutcomeMissing)
	}
}

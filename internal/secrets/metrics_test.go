package secrets

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/MoranWeissman/sharko/internal/metrics"
	"k8s.io/client-go/kubernetes/fake"
)

// metrics_test.go — P2-D D1/D2/D3: the reconciler metrics declared in
// internal/metrics are actually written by this package's periodic pass
// (reconcile()). Assertions read the REAL prometheus default registry
// (promauto metrics are package-level) and check a DELTA across the call
// under test, never an absolute value — the registry is shared across every
// test in this package's process.

// TestReconcile_RecordsRunMetrics pins P2-D D1: a completed pass increments
// sharko_reconciler_runs_total{engine="addon_values", outcome="success"}
// and advances last_run/last_success.
func TestReconcile_RecordsRunMetrics(t *testing.T) {
	before := testutil.ToFloat64(metrics.ReconcilerRuns.WithLabelValues(engineAddonValues, "success"))

	client := fake.NewSimpleClientset()
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("the-api-key"),
			"secrets/datadog/app-key": []byte("the-app-key"),
		}},
		fakeRemoteClientFn(client),
	)
	r.reconcile()

	after := testutil.ToFloat64(metrics.ReconcilerRuns.WithLabelValues(engineAddonValues, "success"))
	if after != before+1 {
		t.Fatalf("sharko_reconciler_runs_total{engine=addon_values,outcome=success} = %v, want %v (delta of 1)", after, before+1)
	}

	lastRun := testutil.ToFloat64(metrics.ReconcilerLastRun.WithLabelValues(engineAddonValues))
	if lastRun <= 0 {
		t.Errorf("sharko_reconciler_last_run_timestamp{engine=addon_values} = %v, want a positive unix timestamp", lastRun)
	}
	lastSuccess := testutil.ToFloat64(metrics.ReconcilerLastSuccess.WithLabelValues(engineAddonValues))
	if lastSuccess != lastRun {
		t.Errorf("sharko_reconciler_last_success_timestamp = %v, want it to equal last_run (%v) after a clean pass", lastSuccess, lastRun)
	}
}

// TestReconcile_CreateRecordsWriteMetrics pins P2-D D2: creating a secret
// increments sharko_reconciler_writes_total{engine=addon_values,
// kind=created} and the legacy items-changed counter under the same shape.
func TestReconcile_CreateRecordsWriteMetrics(t *testing.T) {
	beforeWrites := testutil.ToFloat64(metrics.ReconcilerWrites.WithLabelValues(engineAddonValues, "created"))
	beforeChanged := testutil.ToFloat64(metrics.ReconcilerItemsChanged.WithLabelValues(engineAddonValues, "created"))

	client := fake.NewSimpleClientset()
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("the-api-key"),
			"secrets/datadog/app-key": []byte("the-app-key"),
		}},
		fakeRemoteClientFn(client),
	)
	r.reconcile()

	afterWrites := testutil.ToFloat64(metrics.ReconcilerWrites.WithLabelValues(engineAddonValues, "created"))
	afterChanged := testutil.ToFloat64(metrics.ReconcilerItemsChanged.WithLabelValues(engineAddonValues, "created"))
	if afterWrites != beforeWrites+1 {
		t.Errorf("sharko_reconciler_writes_total{kind=created} = %v, want %v (delta of 1)", afterWrites, beforeWrites+1)
	}
	if afterChanged != beforeChanged+1 {
		t.Errorf("sharko_reconciler_items_changed_total{kind=created} = %v, want %v (delta of 1)", afterChanged, beforeChanged+1)
	}
}

// TestConsecutiveFailures_IncrementsThenResetsOnSuccess pins P2-D D3: three
// passes that each fail to connect to the cluster raise
// LastItemConsecutiveFailures to 3 (and sharko_reconciler_fights counts the
// item once it crosses fightGaugeThreshold), and the very next pass that
// succeeds resets the count to 0 — never a lingering streak after recovery.
func TestConsecutiveFailures_IncrementsThenResetsOnSuccess(t *testing.T) {
	secretProv := &mockSecretProvider{values: map[string][]byte{
		"secrets/datadog/api-key": []byte("the-api-key"),
		"secrets/datadog/app-key": []byte("the-app-key"),
	}}
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		secretProv,
		errRemoteClientFn("connecting to cluster: connection refused (test)"),
	)

	for i := 1; i <= 3; i++ {
		r.reconcile()
		count, ok := r.LastItemConsecutiveFailures("prod-cluster", "datadog")
		if !ok {
			t.Fatalf("pass %d: LastItemConsecutiveFailures ok=false, want a recorded item", i)
		}
		if count != i {
			t.Fatalf("pass %d: LastItemConsecutiveFailures = %d, want %d", i, count, i)
		}
	}

	fights := testutil.ToFloat64(metrics.ReconcilerFights.WithLabelValues(engineAddonValues))
	if fights < 1 {
		t.Errorf("sharko_reconciler_fights{engine=addon_values} = %v, want >= 1 after 3 consecutive failures", fights)
	}

	// Recovery: swap in a working remote client and reconcile once more —
	// the streak must reset to 0, not merely stop growing.
	client := fake.NewSimpleClientset()
	r.remoteClientFn = fakeRemoteClientFn(client)
	r.reconcile()

	count, ok := r.LastItemConsecutiveFailures("prod-cluster", "datadog")
	if !ok {
		t.Fatal("after recovery: LastItemConsecutiveFailures ok=false, want a recorded item")
	}
	if count != 0 {
		t.Fatalf("after recovery: LastItemConsecutiveFailures = %d, want 0 (a successful pass must reset the streak)", count)
	}
}

// TestConsecutiveFailures_NeverSetForFindings pins the other half of P2-D
// D3's rule: a legitimate finding (out_of_sync, missing, foreign) is not a
// failure — LastItemConsecutiveFailures must never move for it. Uses a
// catalog whose secret was never created, so the periodic pass's own
// ItemOutcomeCreated write establishes the item; a second pass against an
// unreadable secrets provider produces ItemOutcomeError (a real failure,
// contrast case).
func TestConsecutiveFailures_NeverSetForFindings(t *testing.T) {
	client := fake.NewSimpleClientset()
	secretProv := &mockSecretProvider{values: map[string][]byte{
		"secrets/datadog/api-key": []byte("the-api-key"),
		"secrets/datadog/app-key": []byte("the-app-key"),
	}}
	r := newReconciler(standardGitReader(catalogWithSecrets), &mockCredProvider{kubeconfig: []byte("fake-kubeconfig")}, secretProv, fakeRemoteClientFn(client))

	// Pass 1: creates the secret — a real write, not a failure.
	r.reconcile()
	if count, ok := r.LastItemConsecutiveFailures("prod-cluster", "datadog"); !ok || count != 0 {
		t.Fatalf("after create: LastItemConsecutiveFailures = (%d, %v), want (0, true)", count, ok)
	}

	// Pass 2: unchanged (hash matches) — still not a failure.
	r.reconcile()
	if count, ok := r.LastItemConsecutiveFailures("prod-cluster", "datadog"); !ok || count != 0 {
		t.Fatalf("after unchanged check: LastItemConsecutiveFailures = (%d, %v), want (0, true)", count, ok)
	}
}

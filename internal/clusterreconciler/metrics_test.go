package clusterreconciler

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/metrics"
)

var errFakeGit = errors.New("fakeGit: simulated git host unreachable")

// metrics_test.go — P2-D D1/D2/D3: the reconciler metrics declared in
// internal/metrics are actually written by this package's write pass
// (pollOnce) and check pass (checkOnce). Every assertion here reads the
// REAL prometheus default registry (promauto metrics are package-level, so
// there is nothing to mock) and asserts a DELTA across the call under
// test — never an absolute value — since the registry is shared with every
// other test in this package's process and t.Parallel() means other tests'
// passes may also be incrementing the same series concurrently.

// TestPollOnce_RecordsRunMetrics pins P2-D D1: a completed write pass
// increments sharko_reconciler_runs_total{engine="cluster_connection",
// outcome="success"} and advances sharko_reconciler_last_run_timestamp +
// sharko_reconciler_last_success_timestamp for the same engine.
func TestPollOnce_RecordsRunMetrics(t *testing.T) {
	// Not t.Parallel(): asserts an absolute gauge value at the end
	// (last_run/last_success timestamps), which only holds if this is the
	// last pollOnce/checkOnce call to touch this engine's gauges before the
	// assertion runs.
	ctx := context.Background()

	before := testutil.ToFloat64(metrics.ReconcilerRuns.WithLabelValues(engineClusterConnection, "success"))

	k8sClient := fake.NewSimpleClientset()
	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, &auditCollector{}, envelopedManagedClusters())
	r.pollOnce(ctx)

	after := testutil.ToFloat64(metrics.ReconcilerRuns.WithLabelValues(engineClusterConnection, "success"))
	if after != before+1 {
		t.Fatalf("sharko_reconciler_runs_total{engine=cluster_connection,outcome=success} = %v, want %v (delta of 1)", after, before+1)
	}

	lastRun := testutil.ToFloat64(metrics.ReconcilerLastRun.WithLabelValues(engineClusterConnection))
	if lastRun <= 0 {
		t.Errorf("sharko_reconciler_last_run_timestamp{engine=cluster_connection} = %v, want a positive unix timestamp", lastRun)
	}
	lastSuccess := testutil.ToFloat64(metrics.ReconcilerLastSuccess.WithLabelValues(engineClusterConnection))
	if lastSuccess != lastRun {
		t.Errorf("sharko_reconciler_last_success_timestamp = %v, want it to equal last_run (%v) after a clean pass", lastSuccess, lastRun)
	}
}

// TestPollOnce_AbortedTick_RecordsFailureOutcome pins P2-D D1's "failure"
// outcome: a git-read failure aborts pollOnce before any per-cluster work,
// and that must record outcome="failure", never "success" or "partial".
func TestPollOnce_AbortedTick_RecordsFailureOutcome(t *testing.T) {
	ctx := context.Background()

	before := testutil.ToFloat64(metrics.ReconcilerRuns.WithLabelValues(engineClusterConnection, "failure"))

	gp := &fakeGit{err: errFakeGit}
	k8sClient := fake.NewSimpleClientset()
	r := newReconcilerForTest(t, gp, k8sClient, &fakeVault{}, &auditCollector{}, nil)
	r.pollOnce(ctx)

	after := testutil.ToFloat64(metrics.ReconcilerRuns.WithLabelValues(engineClusterConnection, "failure"))
	if after != before+1 {
		t.Fatalf("sharko_reconciler_runs_total{engine=cluster_connection,outcome=failure} = %v, want %v (delta of 1)", after, before+1)
	}
}

// TestCheckOnce_RecordsRunMetrics pins P2-D D1 for the check pass: a
// completed checkOnce also increments runs_total under the SAME engine
// label (cluster_connection) — Refresh and the write loop are two passes
// of one engine, not two engines.
func TestCheckOnce_RecordsRunMetrics(t *testing.T) {
	ctx := context.Background()

	before := testutil.ToFloat64(metrics.ReconcilerRuns.WithLabelValues(engineClusterConnection, "success"))

	k8sClient := fake.NewSimpleClientset()
	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, &auditCollector{}, envelopedManagedClusters())
	r.checkOnce(ctx)

	after := testutil.ToFloat64(metrics.ReconcilerRuns.WithLabelValues(engineClusterConnection, "success"))
	if after != before+1 {
		t.Fatalf("sharko_reconciler_runs_total{engine=cluster_connection,outcome=success} = %v, want %v (delta of 1) after checkOnce", after, before+1)
	}
}

// TestPollOnce_CreateRecordsWriteMetrics pins P2-D D2: creating a cluster
// secret increments sharko_reconciler_writes_total{engine=cluster_connection,
// kind=created} and the legacy items-changed counter under the same shape.
func TestPollOnce_CreateRecordsWriteMetrics(t *testing.T) {
	ctx := context.Background()

	beforeWrites := testutil.ToFloat64(metrics.ReconcilerWrites.WithLabelValues(engineClusterConnection, "created"))
	beforeChanged := testutil.ToFloat64(metrics.ReconcilerItemsChanged.WithLabelValues(engineClusterConnection, "created"))

	k8sClient := fake.NewSimpleClientset()
	r := newReconcilerForTest(t, nil, k8sClient, checkVault("new-cluster"), &auditCollector{}, envelopedManagedClusters("new-cluster"))
	r.pollOnce(ctx)

	afterWrites := testutil.ToFloat64(metrics.ReconcilerWrites.WithLabelValues(engineClusterConnection, "created"))
	afterChanged := testutil.ToFloat64(metrics.ReconcilerItemsChanged.WithLabelValues(engineClusterConnection, "created"))
	if afterWrites != beforeWrites+1 {
		t.Errorf("sharko_reconciler_writes_total{kind=created} = %v, want %v (delta of 1)", afterWrites, beforeWrites+1)
	}
	if afterChanged != beforeChanged+1 {
		t.Errorf("sharko_reconciler_items_changed_total{kind=created} = %v, want %v (delta of 1)", afterChanged, beforeChanged+1)
	}
}

// TestFightCount_ExposedAfterThreeReverts pins P2-D D3: FightCount() reads
// the same revert streak recordFightCheck maintains, and
// sharko_reconciler_fights only counts a cluster once its streak reaches
// fightGaugeThreshold (3) — the UI/metric bar, deliberately higher than
// fightRevertThreshold (2, the warning-text bar this lane does not touch).
func TestFightCount_ExposedAfterThreeReverts(t *testing.T) {
	ctx := context.Background()

	body := envelopedWithModes(testClusterEntry{
		Name:   "fighting-cluster",
		Mode:   "user",
		Labels: map[string]string{"addon-foo": "enabled"},
	})
	k8sClient := fake.NewSimpleClientset(userSecret("fighting-cluster", nil))
	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, &auditCollector{}, body)

	r.pollOnce(ctx) // tick 1: establishes baseline

	for i := 0; i < 3; i++ {
		revertLiveLabel(t, k8sClient, "fighting-cluster", "addon-foo", "disabled")
		r.pollOnce(ctx)
	}

	if got := r.FightCount("fighting-cluster"); got < fightGaugeThreshold {
		t.Fatalf("FightCount(%q) = %d, want >= %d after 3 consecutive reverts", "fighting-cluster", got, fightGaugeThreshold)
	}

	fights := testutil.ToFloat64(metrics.ReconcilerFights.WithLabelValues(engineClusterConnection))
	if fights < 1 {
		t.Errorf("sharko_reconciler_fights{engine=cluster_connection} = %v, want >= 1", fights)
	}

	// A cluster with no fight-tracking state at all reports 0, never a
	// stale value or a panic on an unknown name.
	if got := r.FightCount("never-seen-cluster"); got != 0 {
		t.Errorf("FightCount for an unknown cluster = %d, want 0", got)
	}
}

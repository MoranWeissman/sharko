package clusterreconciler

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// V2-cleanup-60.2 (H2) — orphan-sweep sanity guard. When the desired state
// parses to ZERO clusters while managed-clusters.yaml exists non-empty in
// git AND at least one sharko-labeled cluster Secret is live, the sweep is
// withheld for the tick (Error log + orphan_sweep_held audit event) instead
// of deleting the whole fleet. Fresh installs — file missing, or a
// genuinely empty file — keep sweeping as before. These tests pin all
// three cases plus the guard's truth table and the end-to-end
// unknown-apiVersion abort.

// TestOrphanSweepHeld_TruthTable pins the pure guard function.
func TestOrphanSweepHeld_TruthTable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name            string
		desiredCount    int
		fileNonEmpty    bool
		observedManaged int
		want            bool
	}{
		{"zero desired + non-empty file + live secrets -> HELD", 0, true, 1, true},
		{"zero desired + missing/empty file + live secrets -> sweep", 0, false, 1, false},
		{"zero desired + non-empty file + no live secrets -> sweep", 0, true, 0, false},
		{"non-zero desired + non-empty file + live secrets -> sweep", 1, true, 2, false},
		{"all zero -> sweep", 0, false, 0, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := orphanSweepHeld(tc.desiredCount, tc.fileNonEmpty, tc.observedManaged); got != tc.want {
				t.Fatalf("orphanSweepHeld(%d, %v, %d) = %v, want %v",
					tc.desiredCount, tc.fileNonEmpty, tc.observedManaged, got, tc.want)
			}
		})
	}
}

// Case 1 — HELD: the file exists non-empty but parses to zero clusters
// (here: a valid envelope with an explicitly empty list — the same shape a
// silent misread produces) while a labeled Secret is live. The Secret must
// SURVIVE the tick and the operator must get the orphan_sweep_held signal.
func TestPollOnce_ZeroDesiredNonEmptyFile_LiveSecrets_SweepHeld(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedManagedClusters() // non-empty file, zero clusters
	orphan := keeperSecret()           // any labeled, non-adopted, non-pending Secret
	k8sClient := fake.NewSimpleClientset(orphan)
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, audits, body)
	r.pollOnce(ctx)

	if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, orphan.Name, metav1.GetOptions{}); err != nil {
		t.Fatalf("sweep must be HELD — the labeled Secret was deleted: %v", err)
	}
	entries := audits.Snapshot()
	if !hasEvent(entries, "orphan_sweep_held", "partial") {
		t.Fatalf("expected an orphan_sweep_held audit event; got %v", entries)
	}
	if hasEventForResource(entries, "cluster_secret_delete", "cluster:"+orphan.Name) {
		t.Fatalf("a delete audit fired while the sweep was held; got %v", entries)
	}
	// No delete action may have reached the API server.
	for _, a := range k8sClient.Actions() {
		if a.GetVerb() == "delete" {
			t.Fatalf("unexpected delete action while sweep held: %s", actionTarget(a))
		}
	}
}

// Case 2 — fresh install, file MISSING from git: the sweep proceeds as
// before (an orphaned labeled Secret is still cleaned up; no held event).
func TestPollOnce_FileMissing_SweepProceeds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	orphan := keeperSecret()
	k8sClient := fake.NewSimpleClientset(orphan)
	audits := &auditCollector{}

	// body=nil → the fakeGit has no file at the default path → ErrFileNotFound.
	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, audits, nil)
	r.pollOnce(ctx)

	if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, orphan.Name, metav1.GetOptions{}); err == nil {
		t.Fatal("missing file is a normal fresh-install state — the orphan Secret should have been swept, but it survived")
	}
	entries := audits.Snapshot()
	if hasEvent(entries, "orphan_sweep_held", "partial") {
		t.Fatalf("orphan_sweep_held must NOT fire when the file is missing; got %v", entries)
	}
	if !hasEventForResource(entries, "cluster_secret_delete", "cluster:"+orphan.Name) {
		t.Fatalf("expected a delete audit for the orphan; got %v", entries)
	}
}

// Case 3 — fresh install, file exists but is GENUINELY EMPTY (whitespace
// only): the sweep proceeds as before; no held event.
func TestPollOnce_FileGenuinelyEmpty_SweepProceeds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	orphan := keeperSecret()
	k8sClient := fake.NewSimpleClientset(orphan)
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, audits, []byte("\n   \n"))
	r.pollOnce(ctx)

	if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, orphan.Name, metav1.GetOptions{}); err == nil {
		t.Fatal("genuinely empty file is a normal fresh-install state — the orphan Secret should have been swept, but it survived")
	}
	entries := audits.Snapshot()
	if hasEvent(entries, "orphan_sweep_held", "partial") {
		t.Fatalf("orphan_sweep_held must NOT fire for a genuinely empty file; got %v", entries)
	}
}

// End-to-end pin of the (a) forward guard from the reconciler's seat: a
// managed-clusters.yaml carrying an unknown sharko.* apiVersion (written by
// a newer/unknown Sharko) aborts the tick at the parse step — hard error,
// no bare-YAML fallthrough, ZERO deletions. Before V2-cleanup-60.2 this
// exact input silently parsed as zero clusters and the sweep deleted every
// managed Secret.
func TestPollOnce_UnknownSharkoAPIVersion_AbortsTick_NoDeletes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := []byte(`apiVersion: sharko.dev/v2
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters: []
`)
	orphan := keeperSecret()
	k8sClient := fake.NewSimpleClientset(orphan)
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, audits, body)
	r.pollOnce(ctx)

	if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, orphan.Name, metav1.GetOptions{}); err != nil {
		t.Fatalf("unknown sharko.* apiVersion must abort the tick before any mutation — Secret was deleted: %v", err)
	}
	entries := audits.Snapshot()
	if !hasEvent(entries, "cluster_secret_reconcile", "failure") {
		t.Fatalf("expected a failure audit for the rejected file; got %v", entries)
	}
	for _, a := range k8sClient.Actions() {
		if a.GetVerb() == "delete" {
			t.Fatalf("unexpected delete action after aborted tick: %s", actionTarget(a))
		}
	}
}

package clusterreconciler

import (
	"context"
	"errors"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// V2-cleanup-90.2 — reconciler record honesty (review findings M1, M3,
// M5a, M5b, L10, L5/M6b).
//
// These tests pin the six fixes described in reconciler.go /
// reconcile_status.go's updated doc comments:
//
//  1. M1  — two consecutive Sharko-side write failures on a self-managed
//     connection must never produce a false "fight" warning.
//  2. M3  — lastReconcile / fightState entries for clusters no longer
//     desired AND no longer live get pruned; a re-registered cluster of
//     the same name starts with a clean slate.
//  3. M5a — a pass that aborts before per-cluster work (git read failure,
//     schema-validation rejection, ArgoCD Secret listing failure) stamps
//     every cluster the record map already knows about as Failed.
//  4. M5b — the already-in-sync branch's success message says what was
//     actually verified (Secret presence, or presence + labels), using
//     only data the pass already fetched.
//  5. L10 — deleteOne now records outcomes for orphan cleanup, and that
//     record's pruning lifetime is pinned: it survives for as long as the
//     orphan Secret itself exists, then is pruned exactly one pass after
//     the orphan is actually gone.
//  6. L5  — a self-managed Secret observed with a nil (fully wiped) label
//     map is compared against an empty map, not silently skipped as
//     "unknown".

// --- Fix 1 (M1) ---

// TestFightDetection_ConsecutiveWriteFailures_NoFalseWarning is the M1
// regression test: two consecutive Sharko-side write failures (not
// reverts by another actor) must never accumulate into a fight warning.
// Before the fix, recordFightCheck's pre-write baseline advance meant a
// failed write looked exactly like an external revert on the following
// tick.
func TestFightDetection_ConsecutiveWriteFailures_NoFalseWarning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedWithModes(testClusterEntry{
		Name:   "user-cluster",
		Mode:   "user",
		Labels: map[string]string{"addon-foo": "enabled"},
	})
	k8sClient := fake.NewSimpleClientset(userSecret("user-cluster", nil))

	var updateCalls int
	simulated := errors.New("simulated transient update failure")
	k8sClient.PrependReactor("update", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		updateCalls++
		if updateCalls <= 2 {
			return true, nil, simulated
		}
		return false, nil, nil // fall through to the default reactor chain
	})

	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, &auditCollector{}, body)

	r.pollOnce(ctx) // tick 1: write fails
	rec1, ok := r.LastReconcile("user-cluster")
	if !ok || rec1.Outcome != OutcomeFailed {
		t.Fatalf("tick 1: expected Failed record, got %+v (ok=%v)", rec1, ok)
	}

	r.pollOnce(ctx) // tick 2: write fails again — the buggy path would now
	// have a revert streak of "2" ready to fire on the NEXT successful tick.
	rec2, ok := r.LastReconcile("user-cluster")
	if !ok || rec2.Outcome != OutcomeFailed {
		t.Fatalf("tick 2: expected Failed record, got %+v (ok=%v)", rec2, ok)
	}

	r.pollOnce(ctx) // tick 3: write succeeds
	rec3, ok := r.LastReconcile("user-cluster")
	if !ok {
		t.Fatal("tick 3: expected a LastReconcile record")
	}
	if rec3.Outcome != OutcomeSucceeded {
		t.Fatalf("tick 3: Outcome = %q, want %q", rec3.Outcome, OutcomeSucceeded)
	}
	if rec3.Message != "" {
		t.Fatalf("tick 3: Message = %q, want empty — two Sharko-side write failures must never surface a fight warning (M1)", rec3.Message)
	}
}

// --- Fix 2 (M3) + Fix 5 (L10) interaction ---

// TestPruning_RemovedCluster_RecordGoneThenFreshOnReregistration pins the
// core M3 contract: once a cluster is gone from BOTH managed-clusters.yaml
// and the live ArgoCD Secret set, its lastReconcile record is pruned — and
// re-registering the same name starts with no residue from the old record.
func TestPruning_RemovedCluster_RecordGoneThenFreshOnReregistration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	vault := &fakeVault{creds: map[string]*providers.Kubeconfig{
		"c1":     {Server: "https://c1.example.com", CAData: []byte("ca"), Token: "tk"},
		"keeper": {Server: "https://keeper.example.com", CAData: []byte("ca"), Token: "tk"},
	}}
	fg := &fakeGit{files: map[string][]byte{
		DefaultManagedClustersPath: envelopedManagedClusters("c1", "keeper"),
	}}
	k8sClient := fake.NewSimpleClientset()

	r := newReconcilerForTest(t, fg, k8sClient, vault, &auditCollector{}, nil)
	r.pollOnce(ctx) // tick 1: creates c1 + keeper
	if _, ok := r.LastReconcile("c1"); !ok {
		t.Fatal("tick 1: expected a record for c1")
	}

	fg.files[DefaultManagedClustersPath] = envelopedManagedClusters("keeper") // c1 removed from git

	r.pollOnce(ctx) // tick 2: c1 is an orphan candidate this tick and gets
	// deleted; it's still in `existing` (the pre-delete snapshot) so the
	// pruning union keeps its (now delete-outcome) record for this pass.
	if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "c1", metav1.GetOptions{}); err == nil {
		t.Fatal("tick 2: expected c1's Secret to be deleted")
	}
	if _, ok := r.LastReconcile("c1"); !ok {
		t.Fatal("tick 2: expected c1's record to still be present (it was in `existing` this pass)")
	}

	r.pollOnce(ctx) // tick 3: c1 is now absent from BOTH desired and
	// existing — pruned.
	if _, ok := r.LastReconcile("c1"); ok {
		t.Fatal("tick 3: expected c1's record to be pruned once it's gone from both desired and existing")
	}

	// Re-register c1 under the same name.
	fg.files[DefaultManagedClustersPath] = envelopedManagedClusters("c1", "keeper")
	r.pollOnce(ctx)
	rec, ok := r.LastReconcile("c1")
	if !ok {
		t.Fatal("expected a fresh record for re-registered c1")
	}
	if rec.Outcome != OutcomeSucceeded {
		t.Fatalf("re-registered c1: Outcome = %q, want %q", rec.Outcome, OutcomeSucceeded)
	}
	if rec.Message != "" {
		t.Fatalf("re-registered c1: Message = %q, want empty — a fresh create carries no leftover message", rec.Message)
	}
}

// TestPruning_FailedOrphanDelete_RecordSurvivesUntilOrphanGone is the pinned
// M3/L10 interaction test called for by the story: a failed orphan delete's
// record must survive pruning for exactly as long as the orphan itself
// exists, and get pruned exactly one pass after the delete finally
// succeeds — never in the SAME pass the outcome was recorded.
func TestPruning_FailedOrphanDelete_RecordSurvivesUntilOrphanGone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	vault := &fakeVault{creds: map[string]*providers.Kubeconfig{
		"c1":     {Server: "https://c1.example.com", CAData: []byte("ca"), Token: "tk"},
		"keeper": {Server: "https://keeper.example.com", CAData: []byte("ca"), Token: "tk"},
	}}
	fg := &fakeGit{files: map[string][]byte{
		DefaultManagedClustersPath: envelopedManagedClusters("c1", "keeper"),
	}}
	k8sClient := fake.NewSimpleClientset()

	failDeletes := 2 // the first two delete attempts on c1 fail
	simulated := errors.New("simulated delete conflict")
	k8sClient.PrependReactor("delete", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		da, ok := action.(k8stesting.DeleteActionImpl)
		if !ok || da.GetName() != "c1" {
			return false, nil, nil
		}
		if failDeletes > 0 {
			failDeletes--
			return true, nil, simulated
		}
		return false, nil, nil
	})

	r := newReconcilerForTest(t, fg, k8sClient, vault, &auditCollector{}, nil)
	r.pollOnce(ctx) // tick 1: creates c1 + keeper

	fg.files[DefaultManagedClustersPath] = envelopedManagedClusters("keeper") // c1 becomes an orphan

	r.pollOnce(ctx) // tick 2: delete attempt #1 fails
	rec, ok := r.LastReconcile("c1")
	if !ok || rec.Outcome != OutcomeFailed {
		t.Fatalf("tick 2: expected a Failed record for the failed orphan delete, got %+v (ok=%v)", rec, ok)
	}
	if !strings.Contains(rec.Message, simulated.Error()) {
		t.Fatalf("tick 2: Message = %q, want it to contain the underlying error %q", rec.Message, simulated.Error())
	}

	r.pollOnce(ctx) // tick 3: delete attempt #2 fails; the orphan still
	// exists, so the record must SURVIVE this pass's pruning.
	rec, ok = r.LastReconcile("c1")
	if !ok || rec.Outcome != OutcomeFailed {
		t.Fatalf("tick 3: expected the failed-delete record to survive pruning while the orphan still exists, got %+v (ok=%v)", rec, ok)
	}

	r.pollOnce(ctx) // tick 4: delete succeeds this tick.
	rec, ok = r.LastReconcile("c1")
	if !ok || rec.Outcome != OutcomeSucceeded {
		t.Fatalf("tick 4: expected the delete-succeeded record, got %+v (ok=%v)", rec, ok)
	}
	if rec.Message != "orphaned Secret removed" {
		t.Fatalf("tick 4: Message = %q, want %q", rec.Message, "orphaned Secret removed")
	}
	if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "c1", metav1.GetOptions{}); err == nil {
		t.Fatal("tick 4: c1's Secret should be deleted now")
	}

	r.pollOnce(ctx) // tick 5: c1 is now gone from BOTH desired and existing
	// — pruned exactly one pass after the successful delete, never sooner.
	if _, ok := r.LastReconcile("c1"); ok {
		t.Fatal("tick 5: expected c1's record to be pruned one pass after the successful delete")
	}
}

// --- Fix 3 (M5a) ---

// TestStampAbortedTick_GitReadFailure_StampsAllKnownClusters pins the M5a
// contract for the git-read-failure abort site: every cluster already in
// the record map gets stamped Failed with a "reconciler pass aborted:"
// message, so an operator watching one cluster sees the abort instead of a
// stale success.
func TestStampAbortedTick_GitReadFailure_StampsAllKnownClusters(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedManagedClusters("c1", "c2")
	vault := &fakeVault{creds: map[string]*providers.Kubeconfig{
		"c1": {Server: "https://c1.example.com", CAData: []byte("ca"), Token: "tk"},
		"c2": {Server: "https://c2.example.com", CAData: []byte("ca"), Token: "tk"},
	}}
	k8sClient := fake.NewSimpleClientset()
	r := newReconcilerForTest(t, nil, k8sClient, vault, &auditCollector{}, body)

	r.pollOnce(ctx) // tick 1: both clusters reconcile successfully
	for _, name := range []string{"c1", "c2"} {
		rec, ok := r.LastReconcile(name)
		if !ok || rec.Outcome != OutcomeSucceeded {
			t.Fatalf("tick 1: expected Succeeded record for %s, got %+v (ok=%v)", name, rec, ok)
		}
	}

	// Swap in a failing git provider for tick 2.
	gitErr := errors.New("simulated git API 503")
	r.deps.GitProvider = func() gitprovider.GitProvider { return &fakeGit{err: gitErr} }

	r.pollOnce(ctx) // tick 2: aborts before any per-cluster work

	for _, name := range []string{"c1", "c2"} {
		rec, ok := r.LastReconcile(name)
		if !ok {
			t.Fatalf("tick 2: expected %s's record to still be present after the abort", name)
		}
		if rec.Outcome != OutcomeFailed {
			t.Fatalf("tick 2: %s Outcome = %q, want %q (abort must stamp every known cluster Failed)", name, rec.Outcome, OutcomeFailed)
		}
		if !strings.Contains(rec.Message, "reconciler pass aborted") {
			t.Fatalf("tick 2: %s Message = %q, want it to name the abort", name, rec.Message)
		}
	}
}

// TestStampAbortedTick_SchemaValidationFailure_StampsAllKnownClusters is
// the M5a regression test for the second named abort site: a
// schema-validation rejection of managed-clusters.yaml.
func TestStampAbortedTick_SchemaValidationFailure_StampsAllKnownClusters(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedManagedClusters("c1")
	vault := &fakeVault{creds: map[string]*providers.Kubeconfig{
		"c1": {Server: "https://c1.example.com", CAData: []byte("ca"), Token: "tk"},
	}}
	k8sClient := fake.NewSimpleClientset()
	fg := &fakeGit{files: map[string][]byte{DefaultManagedClustersPath: body}}

	r := newReconcilerForTest(t, fg, k8sClient, vault, &auditCollector{}, nil)
	r.pollOnce(ctx) // tick 1: c1 reconciles successfully
	if rec, ok := r.LastReconcile("c1"); !ok || rec.Outcome != OutcomeSucceeded {
		t.Fatalf("tick 1: expected Succeeded record for c1, got %+v (ok=%v)", rec, ok)
	}

	// tick 2: managed-clusters.yaml now fails schema validation (wrong kind).
	fg.files[DefaultManagedClustersPath] = []byte(`apiVersion: sharko.dev/v1
kind: AddonCatalog
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: ghost-cluster
`)
	r.pollOnce(ctx)

	rec, ok := r.LastReconcile("c1")
	if !ok {
		t.Fatal("tick 2: expected c1's record to still be present after the abort")
	}
	if rec.Outcome != OutcomeFailed {
		t.Fatalf("tick 2: Outcome = %q, want %q", rec.Outcome, OutcomeFailed)
	}
	if !strings.Contains(rec.Message, "reconciler pass aborted") {
		t.Fatalf("tick 2: Message = %q, want it to name the abort", rec.Message)
	}
}

// TestStampAbortedTick_ListSecretsFailure_StampsAllKnownClusters covers
// the third abort-before-per-cluster-work site: listManagedSecrets failing
// inside reconcileDiff.
func TestStampAbortedTick_ListSecretsFailure_StampsAllKnownClusters(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedManagedClusters("c1")
	vault := &fakeVault{creds: map[string]*providers.Kubeconfig{
		"c1": {Server: "https://c1.example.com", CAData: []byte("ca"), Token: "tk"},
	}}
	k8sClient := fake.NewSimpleClientset()
	r := newReconcilerForTest(t, nil, k8sClient, vault, &auditCollector{}, body)

	r.pollOnce(ctx) // tick 1: c1 reconciles successfully
	if rec, ok := r.LastReconcile("c1"); !ok || rec.Outcome != OutcomeSucceeded {
		t.Fatalf("tick 1: expected Succeeded record for c1, got %+v (ok=%v)", rec, ok)
	}

	simulated := errors.New("simulated list API failure")
	k8sClient.PrependReactor("list", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, simulated
	})

	r.pollOnce(ctx) // tick 2: listManagedSecrets fails — aborts before any per-cluster work

	rec, ok := r.LastReconcile("c1")
	if !ok {
		t.Fatal("tick 2: expected c1's record to still be present after the abort")
	}
	if rec.Outcome != OutcomeFailed {
		t.Fatalf("tick 2: Outcome = %q, want %q", rec.Outcome, OutcomeFailed)
	}
	if !strings.Contains(rec.Message, "reconciler pass aborted") {
		t.Fatalf("tick 2: Message = %q, want it to name the abort", rec.Message)
	}
}

// --- Fix 4 (M5b) ---

// TestAlreadyInSync_MessageReflectsLabelVerification pins the honest
// wording: when the already-in-sync branch's cheap comparison shows the
// live Secret's labels matching what git wants, the message says so.
func TestAlreadyInSync_MessageReflectsLabelVerification(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedManagedClusters("c1") // no addon labels declared
	vault := &fakeVault{creds: map[string]*providers.Kubeconfig{
		"c1": {Server: "https://c1.example.com", CAData: []byte("ca"), Token: "tk"},
	}}
	k8sClient := fake.NewSimpleClientset()
	r := newReconcilerForTest(t, nil, k8sClient, vault, &auditCollector{}, body)

	r.pollOnce(ctx) // tick 1: creates c1
	r.pollOnce(ctx) // tick 2: already in sync — the (empty) desired label set matches

	rec, ok := r.LastReconcile("c1")
	if !ok {
		t.Fatal("expected a record for c1")
	}
	if rec.Outcome != OutcomeSucceeded {
		t.Fatalf("Outcome = %q, want %q", rec.Outcome, OutcomeSucceeded)
	}
	if rec.Message != "cluster Secret present; labels verified" {
		t.Fatalf("Message = %q, want %q", rec.Message, "cluster Secret present; labels verified")
	}
}

// TestAlreadyInSync_LabelDrift_MessageStaysPresentOnly is the honesty
// guard: reconcileDiff does not repair label drift on an already-managed
// Secret, so it must NOT claim "labels verified" when they demonstrably do
// not match what git wants — only that the Secret is present.
func TestAlreadyInSync_LabelDrift_MessageStaysPresentOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedWithModes(testClusterEntry{
		Name:   "c1",
		Labels: map[string]string{"addon-foo": "enabled"},
	})
	vault := &fakeVault{creds: map[string]*providers.Kubeconfig{
		"c1": {Server: "https://c1.example.com", CAData: []byte("ca"), Token: "tk"},
	}}
	k8sClient := fake.NewSimpleClientset()
	r := newReconcilerForTest(t, nil, k8sClient, vault, &auditCollector{}, body)

	r.pollOnce(ctx) // tick 1: creates c1 with addon-foo=enabled

	// Simulate drift: something external changes the live label after
	// creation.
	sec, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "c1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	sec = sec.DeepCopy()
	sec.Labels["addon-foo"] = "disabled"
	if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Update(ctx, sec, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update: %v", err)
	}

	r.pollOnce(ctx) // tick 2: same-name Secret exists (already-in-sync branch), but labels have drifted

	rec, ok := r.LastReconcile("c1")
	if !ok {
		t.Fatal("expected a record for c1")
	}
	if rec.Outcome != OutcomeSucceeded {
		t.Fatalf("Outcome = %q, want %q (Secret presence alone is still success — the reconciler doesn't fail on drift it doesn't repair)", rec.Outcome, OutcomeSucceeded)
	}
	if rec.Message != "cluster Secret present" {
		t.Fatalf("Message = %q, want %q (must NOT claim labels verified when they don't match)", rec.Message, "cluster Secret present")
	}
}

// --- Fix 6 (L5 / M6b) ---

// TestFightDetection_LabelsWipedToNil_DetectedAsRevert is the L5
// regression test: a live Secret observed with a nil (fully wiped) label
// map must be compared against an empty map, not treated as "read
// failed/missing" and silently skipped. Before the fix, an external actor
// wiping every label with a Replace=true sync would never trip the fight
// detector because a nil observedLive looked identical to "the Get
// failed".
func TestFightDetection_LabelsWipedToNil_DetectedAsRevert(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedWithModes(testClusterEntry{
		Name:   "user-cluster",
		Mode:   "user",
		Labels: map[string]string{"addon-foo": "enabled"},
	})
	k8sClient := fake.NewSimpleClientset(userSecret("user-cluster", nil))
	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, &auditCollector{}, body)

	r.pollOnce(ctx) // tick 1: writes addon-foo=enabled, establishes baseline

	wipeAllLabels := func() {
		sec, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "user-cluster", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		sec = sec.DeepCopy()
		sec.Labels = nil // simulates an external Replace=true sync wiping every label
		if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Update(ctx, sec, metav1.UpdateOptions{}); err != nil {
			t.Fatalf("update: %v", err)
		}
	}

	wipeAllLabels()
	r.pollOnce(ctx) // tick 2: revert #1 — the live Secret has literally no labels

	wipeAllLabels()
	r.pollOnce(ctx) // tick 3: revert #2 — threshold reached

	rec, ok := r.LastReconcile("user-cluster")
	if !ok {
		t.Fatal("expected a LastReconcile record")
	}
	if rec.Message == "" {
		t.Fatal("expected a fight warning — a Secret with ALL labels wiped must be compared, not silently skipped (L5)")
	}
	if !strings.Contains(rec.Message, "overwriting") {
		t.Errorf("Message = %q, want it to describe the overwrite pattern", rec.Message)
	}
}

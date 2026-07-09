package clusterreconciler

import (
	"context"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/MoranWeissman/sharko/internal/providers"
)

// V2-cleanup-89.4 — per-cluster reconcile visibility.
//
// Before this, a per-cluster reconcile failure (vault fetch, K8s API
// rejection) was slog + audit-log only — an operator looking at ONE
// cluster had no way to tell whether its last reconcile attempt succeeded,
// failed, or was deliberately skipped. These tests pin the LastReconcile
// contract: every managed cluster gets a record every tick it's touched,
// the message is plain English (not a raw Go error dump), and a cluster
// the reconciler has never seen reports ok=false rather than a zero value
// that could be misread as "succeeded".

// TestLastReconcile_UnknownCluster_NotOK asserts a fresh Reconciler (no
// ticks run yet) reports ok=false for any cluster name — the API layer
// relies on this to omit last_reconcile entirely rather than render a
// misleading zero-value record.
func TestLastReconcile_UnknownCluster_NotOK(t *testing.T) {
	t.Parallel()
	r := New(Deps{})
	_, ok := r.LastReconcile("never-seen")
	if ok {
		t.Fatal("expected ok=false for a cluster the reconciler has never reconciled")
	}
}

// TestPollOnce_LastReconcile_RecordsSucceededOnCreate — happy path: a
// cluster created this tick gets a Succeeded record with no message.
func TestPollOnce_LastReconcile_RecordsSucceededOnCreate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedManagedClusters("prod-eu")
	vault := &fakeVault{
		creds: map[string]*providers.Kubeconfig{
			"prod-eu": {Server: "https://prod-eu.example.com", CAData: []byte("ca"), Token: "tk"},
		},
	}
	k8sClient := fake.NewSimpleClientset()
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, vault, audits, body)
	r.pollOnce(ctx)

	rec, ok := r.LastReconcile("prod-eu")
	if !ok {
		t.Fatal("expected a LastReconcile record for prod-eu after the tick that created it")
	}
	if rec.Outcome != OutcomeSucceeded {
		t.Fatalf("Outcome = %q, want %q", rec.Outcome, OutcomeSucceeded)
	}
	if rec.Message != "" {
		t.Fatalf("Message = %q, want empty on success", rec.Message)
	}
	if rec.Time.IsZero() {
		t.Fatal("Time must be set on a recorded outcome")
	}
}

// TestPollOnce_LastReconcile_AlreadyInSyncStaysSucceeded — a cluster whose
// Secret already exists and needs no write this tick must STILL get a
// fresh Succeeded record (the read model would otherwise go stale between
// the rare ticks that actually create or delete something).
func TestPollOnce_LastReconcile_AlreadyInSyncStaysSucceeded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedManagedClusters("c1")
	vault := &fakeVault{
		creds: map[string]*providers.Kubeconfig{
			"c1": {Server: "https://c1.example.com", CAData: []byte("ca"), Token: "tk"},
		},
	}
	k8sClient := fake.NewSimpleClientset()
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, vault, audits, body)

	r.pollOnce(ctx) // tick 1: creates the Secret
	first, ok := r.LastReconcile("c1")
	if !ok || first.Outcome != OutcomeSucceeded {
		t.Fatalf("tick 1: expected Succeeded record, got %+v (ok=%v)", first, ok)
	}

	r.pollOnce(ctx) // tick 2: no-op, Secret already in sync
	second, ok := r.LastReconcile("c1")
	if !ok {
		t.Fatal("tick 2: expected a LastReconcile record to still be present")
	}
	if second.Outcome != OutcomeSucceeded {
		t.Fatalf("tick 2: Outcome = %q, want %q (already-in-sync clusters still record success)", second.Outcome, OutcomeSucceeded)
	}
	if !second.Time.After(first.Time) && !second.Time.Equal(first.Time) {
		t.Fatalf("tick 2 record's Time (%v) should not be before tick 1's (%v)", second.Time, first.Time)
	}
}

// TestPollOnce_LastReconcile_RecordsFailedOnVaultError — a vault fetch
// failure for one cluster must record Failed with a plain-English message
// that STILL includes the real error text (not just a generic string),
// while leaving the other clusters unaffected.
func TestPollOnce_LastReconcile_RecordsFailedOnVaultError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedManagedClusters("c1", "c2")
	underlying := errors.New("simulated vault outage for c2")
	vault := &fakeVault{
		creds: map[string]*providers.Kubeconfig{
			"c1": {Server: "https://c1.example.com", CAData: []byte("ca"), Token: "tk"},
		},
		errs: map[string]error{"c2": underlying},
	}
	k8sClient := fake.NewSimpleClientset()
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, vault, audits, body)
	r.pollOnce(ctx)

	rec, ok := r.LastReconcile("c2")
	if !ok {
		t.Fatal("expected a LastReconcile record for c2 despite the vault error")
	}
	if rec.Outcome != OutcomeFailed {
		t.Fatalf("Outcome = %q, want %q", rec.Outcome, OutcomeFailed)
	}
	if !strings.Contains(rec.Message, underlying.Error()) {
		t.Fatalf("Message = %q, want it to contain the underlying error %q", rec.Message, underlying.Error())
	}
	if rec.Message == underlying.Error() {
		t.Fatalf("Message = %q, want a plain-English lead-in before the raw error, not the raw error alone", rec.Message)
	}

	// c1 must be unaffected — per-cluster error isolation.
	other, ok := r.LastReconcile("c1")
	if !ok || other.Outcome != OutcomeSucceeded {
		t.Fatalf("c1 should have reconciled successfully despite c2's vault error; got %+v (ok=%v)", other, ok)
	}
}

// TestPollOnce_LastReconcile_RecordsSkippedOnUnlabeledSecret — Adopt
// territory: a same-name Secret exists without the sharko label, so the
// reconciler skips (not fails) the cluster and records Skipped with an
// explanatory message.
func TestPollOnce_LastReconcile_RecordsSkippedOnUnlabeledSecret(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedManagedClusters("foreign-cluster")
	foreign := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "foreign-cluster",
			Namespace: DefaultArgoCDNamespace,
			Labels:    map[string]string{"created-by": "human-operator"},
		},
		Type: corev1.SecretTypeOpaque,
	}
	k8sClient := fake.NewSimpleClientset(foreign)
	vault := &fakeVault{
		creds: map[string]*providers.Kubeconfig{
			"foreign-cluster": {Server: "https://x.example.com", CAData: []byte("ca"), Token: "tk"},
		},
	}
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, vault, audits, body)
	r.pollOnce(ctx)

	rec, ok := r.LastReconcile("foreign-cluster")
	if !ok {
		t.Fatal("expected a LastReconcile record for foreign-cluster")
	}
	if rec.Outcome != OutcomeSkipped {
		t.Fatalf("Outcome = %q, want %q", rec.Outcome, OutcomeSkipped)
	}
	if rec.Message == "" {
		t.Fatal("expected a plain-English explanation on a skipped outcome")
	}
}

// TestPollOnce_LastReconcile_SelfManagedPending — a self-managed
// connection (connectionManagedBy: user) whose Secret the user hasn't
// created yet records Skipped, not Failed — this is an expected wait
// state, not an error.
func TestPollOnce_LastReconcile_SelfManagedPending(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedWithModes(testClusterEntry{Name: "user-cluster", Mode: "user"})
	k8sClient := fake.NewSimpleClientset() // no Secret created by the user yet
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, audits, body)
	r.pollOnce(ctx)

	rec, ok := r.LastReconcile("user-cluster")
	if !ok {
		t.Fatal("expected a LastReconcile record for the pending self-managed cluster")
	}
	if rec.Outcome != OutcomeSkipped {
		t.Fatalf("Outcome = %q, want %q (waiting on the user is a skip, not a failure)", rec.Outcome, OutcomeSkipped)
	}
	if rec.Message == "" {
		t.Fatal("expected a plain-English explanation telling the operator to create the Secret")
	}
}

// TestPollOnce_LastReconcile_SelfManagedSynced — once the user's Secret
// exists, a self-managed connection's label sync records Succeeded.
func TestPollOnce_LastReconcile_SelfManagedSynced(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedWithModes(testClusterEntry{
		Name:   "user-cluster",
		Mode:   "user",
		Labels: map[string]string{"addon-foo": "enabled"},
	})
	k8sClient := fake.NewSimpleClientset(userSecret("user-cluster", nil))
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, audits, body)
	r.pollOnce(ctx)

	rec, ok := r.LastReconcile("user-cluster")
	if !ok {
		t.Fatal("expected a LastReconcile record for the synced self-managed cluster")
	}
	if rec.Outcome != OutcomeSucceeded {
		t.Fatalf("Outcome = %q, want %q", rec.Outcome, OutcomeSucceeded)
	}
	if rec.Message != "" {
		t.Fatalf("Message = %q, want empty on success", rec.Message)
	}
}

// TestPollOnce_LastReconcile_SelfManagedSyncFailure — a K8s API error
// while syncing labels onto the user's Secret must record Failed with the
// underlying error surfaced in plain English.
func TestPollOnce_LastReconcile_SelfManagedSyncFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedWithModes(testClusterEntry{
		Name:   "user-cluster",
		Mode:   "user",
		Labels: map[string]string{"addon-foo": "enabled"},
	})
	k8sClient := fake.NewSimpleClientset(userSecret("user-cluster", nil))
	simulated := errors.New("simulated update conflict")
	k8sClient.PrependReactor("update", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, simulated
	})
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, audits, body)
	r.pollOnce(ctx)

	rec, ok := r.LastReconcile("user-cluster")
	if !ok {
		t.Fatal("expected a LastReconcile record despite the update failure")
	}
	if rec.Outcome != OutcomeFailed {
		t.Fatalf("Outcome = %q, want %q", rec.Outcome, OutcomeFailed)
	}
	if !strings.Contains(rec.Message, simulated.Error()) {
		t.Fatalf("Message = %q, want it to contain the underlying error %q", rec.Message, simulated.Error())
	}
}

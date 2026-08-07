package clusterreconciler

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// pendingSecret builds a sharko-labeled, argocd-cluster-type Secret carrying
// the registration-pending annotation stamped at `stamped`. Mirrors the shape
// the orchestrator direct-write + argosecrets.Manager.Ensure produce.
func pendingSecret(name string, stamped time.Time) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: DefaultArgoCDNamespace,
			Labels: map[string]string{
				LabelManagedBy:                   LabelValueSharko,
				"argocd.argoproj.io/secret-type": "cluster",
			},
			Annotations: map[string]string{
				models.AnnotationRegistrationPending: models.RegistrationPendingTimestamp(stamped),
			},
		},
		Type: corev1.SecretTypeOpaque,
	}
}

// Test 11.1-a — direct-write then immediate orphan sweep within the grace
// window: the Secret SURVIVES. This is the load-bearing race: the cluster is
// not yet in managed-clusters.yaml (git empty) but the Secret was just
// direct-written, so the sweep must skip it.
func TestPollOnce_PendingSecretWithinGrace_Survives(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	now := time.Date(2026, 6, 7, 8, 40, 53, 0, time.UTC)
	// Stamped 1 second ago — well inside the 10m window.
	secret := pendingSecret("cluster-test-1", now.Add(-1*time.Second))

	body := envelopedManagedClusters() // git has zero clusters yet
	k8sClient := fake.NewSimpleClientset(secret)
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, audits, body)
	r.nowFn = func() time.Time { return now }
	r.pollOnce(ctx)

	if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "cluster-test-1", metav1.GetOptions{}); err != nil {
		t.Fatalf("registration-pending Secret within grace window was deleted: %v", err)
	}
	entries := audits.Snapshot()
	if hasEventForResource(entries, "cluster_secret_delete", "cluster:cluster-test-1") {
		t.Fatalf("a delete audit fired for a pending-within-grace Secret; got %v", entries)
	}
}

// Test 11.1-b — pending Secret whose annotation timestamp is older than the
// grace window AND still unmanaged → it IS reaped (no permanent leak).
// Git keeps ONE other cluster ("keeper") so the V2-cleanup-60.2 fleet-wipe
// guard does not hold the sweep.
func TestPollOnce_PendingSecretExpired_StillUnmanaged_Reaped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	now := time.Date(2026, 6, 7, 9, 0, 0, 0, time.UTC)
	// Stamped 11 minutes ago — past the 10m window.
	secret := pendingSecret("ghost-cluster", now.Add(-11*time.Minute))

	body := envelopedManagedClusters("keeper") // ghost-cluster still not in git
	k8sClient := fake.NewSimpleClientset(secret, keeperSecret())
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, audits, body)
	r.nowFn = func() time.Time { return now }
	r.pollOnce(ctx)

	if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "ghost-cluster", metav1.GetOptions{}); err == nil {
		t.Fatal("expired registration-pending Secret should have been reaped, but it still exists")
	}
	entries := audits.Snapshot()
	if !hasEventForResource(entries, "cluster_secret_delete", "cluster:ghost-cluster") {
		t.Fatalf("expected a delete audit for the expired pending Secret; got %v", entries)
	}
}

// Test 11.1-c — restart safety: a freshly-constructed reconciler (no memory of
// when it started) honors a within-window pending Secret because expiry is
// derived from the annotation timestamp, not process start. We simulate the
// restart by stamping the Secret 9m ago and setting now such that only 9m have
// elapsed — still inside the 10m window even though "this reconciler" has just
// come up.
func TestPollOnce_RestartMidWindow_HonorsAnnotationTimestamp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	now := time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)
	secret := pendingSecret("restart-cluster", now.Add(-9*time.Minute)) // 9m < 10m

	body := envelopedManagedClusters() // not in git
	k8sClient := fake.NewSimpleClientset(secret)
	audits := &auditCollector{}

	// Brand-new reconciler instance — stands in for a process that just
	// restarted mid-window. It has no stored "start time"; the only signal is
	// the annotation timestamp on the Secret.
	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, audits, body)
	r.nowFn = func() time.Time { return now }
	r.pollOnce(ctx)

	if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "restart-cluster", metav1.GetOptions{}); err != nil {
		t.Fatalf("within-window pending Secret was reaped after a simulated restart: %v", err)
	}
}

// Test 11.1-d — cluster becomes managed: when the cluster appears in
// managed-clusters.yaml, the next reconcile STRIPS the registration-pending
// annotation (converting it to a normal managed Secret) and KEEPS the Secret.
func TestPollOnce_ClusterBecomesManaged_ClearsPendingAnnotation_KeepsSecret(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	now := time.Date(2026, 6, 7, 11, 0, 0, 0, time.UTC)
	secret := pendingSecret("now-managed", now.Add(-30*time.Second))

	// The registration PR merged → the cluster is now in git.
	body := envelopedManagedClusters("now-managed")
	vault := &fakeVault{
		creds: map[string]*providers.Kubeconfig{
			"now-managed": {Server: "https://now-managed.example.com", CAData: []byte("ca"), Token: "tk"},
		},
	}
	k8sClient := fake.NewSimpleClientset(secret)
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, vault, audits, body)
	r.nowFn = func() time.Time { return now }
	r.pollOnce(ctx)

	got, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "now-managed", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("managed Secret must be kept, but it was deleted: %v", err)
	}
	if _, has := got.Annotations[models.AnnotationRegistrationPending]; has {
		t.Fatalf("registration-pending annotation should have been cleared once managed; annotations=%v", got.Annotations)
	}
	if !IsManagedBySharko(got) {
		t.Fatalf("Secret lost its sharko ownership label during annotation clear; labels=%v", got.Labels)
	}
	entries := audits.Snapshot()
	if !hasEventForResource(entries, "cluster_secret_clear_pending", "cluster:now-managed") {
		t.Fatalf("expected a cluster_secret_clear_pending audit; got %v", entries)
	}
}

// TestPollOnce_ClearPending_RaceStrippedLabelBetweenListAndGet is L11's pin
// (code review): clearRegistrationPending re-Gets the Secret fresh before
// writing (by design — see its own doc comment), but until this fix it
// never re-checked ownership on that fresh copy. If something else (kubectl,
// another controller) stripped the app.kubernetes.io/managed-by=sharko
// label in the window between the tick's earlier listManagedSecrets
// snapshot (what put this cluster in the "convert pending -> managed" loop
// in the first place) and this pass's own re-Get, the old code would
// DeepCopy the fresh object, blindly re-stamp the ownership label via
// ApplyManagedBySharkoLabel, and Update — silently re-taking ownership of a
// Secret that is no longer Sharko's. deleteOne already guards the
// equivalent race for delete; this pins the same guard now exists here.
func TestPollOnce_ClearPending_RaceStrippedLabelBetweenListAndGet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	now := time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC)
	secret := pendingSecret("race-cluster", now.Add(-30*time.Second))

	body := envelopedManagedClusters("race-cluster") // the registration PR just merged
	vault := &fakeVault{
		creds: map[string]*providers.Kubeconfig{
			"race-cluster": {Server: "https://race-cluster.example.com", CAData: []byte("ca"), Token: "tk"},
		},
	}
	k8sClient := fake.NewSimpleClientset(secret)

	// The tick reaches this cluster's connectivity-check label sync BEFORE
	// clearRegistrationPending runs (clearRegistrationPending's own doc
	// comment: it runs LAST among a tick's per-cluster label writes) — that
	// earlier read must see the real, labeled object so its own unrelated
	// write proceeds normally. Only the SECOND and later "get" of this
	// Secret (clearRegistrationPending's own fresh re-Get) sees the
	// ownership label already stripped — simulating something else
	// (kubectl, another controller) removing it in the window between the
	// tick's earlier listManagedSecrets snapshot and this pass's re-Get.
	gets := 0
	k8sClient.PrependReactor("get", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		getAction, ok := action.(k8stesting.GetAction)
		if !ok || getAction.GetName() != "race-cluster" {
			return false, nil, nil
		}
		gets++
		if gets == 1 {
			return false, nil, nil // let the tracker answer normally
		}
		stripped := secret.DeepCopy()
		delete(stripped.Labels, LabelManagedBy)
		return true, stripped, nil
	})

	audits := &auditCollector{}
	r := newReconcilerForTest(t, nil, k8sClient, vault, audits, body)
	r.nowFn = func() time.Time { return now }
	r.pollOnce(ctx)

	// clearRegistrationPending's write, if it happened, is the ONE update
	// that both applies the ownership label AND deletes the
	// registration-pending annotation — no other write this tick touches
	// that annotation. Its absence among the recorded actions is the guard
	// actually holding.
	for _, action := range k8sClient.Actions() {
		if action.GetVerb() != "update" || action.GetResource().Resource != "secrets" {
			continue
		}
		updated, ok := action.(k8stesting.UpdateAction).GetObject().(*corev1.Secret)
		if !ok {
			continue
		}
		if _, stillPending := updated.Annotations[models.AnnotationRegistrationPending]; !stillPending {
			t.Fatalf("clearRegistrationPending's write landed on a Secret that lost its ownership label between listing and this write: %v", updated)
		}
	}

	rec, ok := r.LastReconcile("race-cluster")
	if !ok || rec.Outcome != OutcomeSkipped {
		t.Errorf("LastReconcile outcome = (%+v, %v), want OutcomeSkipped", rec, ok)
	}
}

// Test 11.1-e — idempotency of the clear: once the annotation is gone, a
// subsequent in-sync tick performs no further mutation for that cluster.
func TestPollOnce_ClearPending_IsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	secret := pendingSecret("idem", now.Add(-10*time.Second))

	body := envelopedManagedClusters("idem")
	vault := &fakeVault{
		creds: map[string]*providers.Kubeconfig{
			"idem": {Server: "https://idem.example.com", CAData: []byte("ca"), Token: "tk"},
		},
	}
	k8sClient := fake.NewSimpleClientset(secret)
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, vault, audits, body)
	r.nowFn = func() time.Time { return now }

	// Tick 1: clears the annotation (one update).
	r.pollOnce(ctx)
	resetClientActions(k8sClient)

	// Tick 2: state in sync, annotation already gone → zero mutations.
	r.pollOnce(ctx)
	if got := countMutations(k8sClient); got != 0 {
		t.Fatalf("tick 2 after clear must be a no-op, recorded %d mutations", got)
	}
}

// Test 11.1-f — malformed annotation timestamp → treated as NOT pending: a
// Secret carrying an unparseable timestamp is eligible for the normal orphan
// sweep (fail-safe — it must never become immune forever).
func TestPollOnce_MalformedPendingAnnotation_TreatedAsOrphan(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	now := time.Date(2026, 6, 7, 13, 0, 0, 0, time.UTC)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "malformed",
			Namespace: DefaultArgoCDNamespace,
			Labels: map[string]string{
				LabelManagedBy:                   LabelValueSharko,
				"argocd.argoproj.io/secret-type": "cluster",
			},
			Annotations: map[string]string{
				models.AnnotationRegistrationPending: "not-a-timestamp",
			},
		},
		Type: corev1.SecretTypeOpaque,
	}

	// "keeper" keeps the desired state non-zero so the V2-cleanup-60.2
	// fleet-wipe guard does not hold the sweep.
	body := envelopedManagedClusters("keeper") // "malformed" not in git
	k8sClient := fake.NewSimpleClientset(secret, keeperSecret())
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, audits, body)
	r.nowFn = func() time.Time { return now }
	r.pollOnce(ctx)

	if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "malformed", metav1.GetOptions{}); err == nil {
		t.Fatal("Secret with a malformed registration-pending annotation should be reaped as a normal orphan, but it survived")
	}
}

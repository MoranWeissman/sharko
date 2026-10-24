package clusterreconciler

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// EP3 dry run round R: the orphan sweep never deletes a connection that
// carries a waiting mark (R1), a connection whose Secret name is a cluster
// in Git after a rename inside ArgoCD (R6), or one of two Sharko Secrets
// that show the same cluster name (R13).

func secretExists(t *testing.T, k8s *fake.Clientset, name string) *corev1.Secret {
	t.Helper()
	got, err := k8s.CoreV1().Secrets(DefaultArgoCDNamespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Secret %q was deleted: %v", name, err)
	}
	return got
}

func TestSweep_WaitingMark_IsNeverDeleted(t *testing.T) {
	for _, mark := range argosecrets.PendingPRMarks {
		t.Run(mark, func(t *testing.T) {
			sec := keptOrphanSecret("marked", "")
			sec.Annotations = map[string]string{mark: "7"}
			k8s := fake.NewSimpleClientset(sec, keeperSecret())
			audits := &auditCollector{}
			r := newReconcilerForTest(t, nil, k8s, &fakeVault{}, audits, envelopedManagedClusters("keeper"))
			// The tracker no longer has the pull request.
			r.deps.KeptConnectionsFn = func(context.Context) (map[string]models.ConnectionHold, error) {
				return map[string]models.ConnectionHold{}, nil
			}
			r.pollOnce(context.Background())

			got := secretExists(t, k8s, "marked")
			if got.Annotations[mark] != "7" || got.Labels[LabelManagedBy] != LabelValueSharko {
				t.Fatalf("the sweep changed a marked connection: labels %v annotations %v", got.Labels, got.Annotations)
			}
			if hasEventForResource(audits.Snapshot(), "cluster_secret_delete", "cluster:marked") {
				t.Fatal("a delete was audited for a marked connection")
			}
		})
	}
}

// A kept connection the tracker still has is handed back: Sharko's label
// and the keep mark both come off in the same write.
func TestSweep_KeptConnection_TakesTheKeepMarkOffToo(t *testing.T) {
	sec := keptOrphanSecret("kept", "")
	sec.Annotations = map[string]string{argosecrets.AnnotationRemoveKeepPendingPR: "3"}
	k8s := fake.NewSimpleClientset(sec, keeperSecret())
	r := newReconcilerForTest(t, nil, k8s, &fakeVault{}, &auditCollector{}, envelopedManagedClusters("keeper"))
	r.deps.KeptConnectionsFn = func(context.Context) (map[string]models.ConnectionHold, error) {
		return map[string]models.ConnectionHold{"kept": {ReleasePR: 3, Hold: true}}, nil
	}
	r.pollOnce(context.Background())

	got := secretExists(t, k8s, "kept")
	if _, still := got.Labels[LabelManagedBy]; still {
		t.Fatalf("Sharko's label is still on: %v", got.Labels)
	}
	if _, still := got.Annotations[argosecrets.AnnotationRemoveKeepPendingPR]; still {
		t.Fatalf("the keep mark is still on: %v", got.Annotations)
	}
}

// R6: someone renamed the cluster inside ArgoCD. The Secret is still
// called prod-eu, and prod-eu is in Git. The sweep must not delete it,
// even though the create for prod-eu finds it already there.
func TestSweep_RenamedInsideArgoCD_LiveSecretIsNotDeleted(t *testing.T) {
	sec := keptOrphanSecret("prod-eu", "prod-eu-renamed")
	k8s := fake.NewSimpleClientset(sec)
	audits := &auditCollector{}
	vault := &fakeVault{creds: map[string]*providers.Kubeconfig{
		"prod-eu": {Server: "https://prod-eu.example.com", CAData: []byte("ca"), Token: "tk"},
	}}
	r := newReconcilerForTest(t, nil, k8s, vault, audits, envelopedManagedClusters("prod-eu"))
	r.pollOnce(context.Background())

	got := secretExists(t, k8s, "prod-eu")
	if string(got.Data["name"]) != "prod-eu-renamed" {
		t.Fatalf("the live Secret was replaced: %v", got.Data)
	}
	if hasEventForResource(audits.Snapshot(), "cluster_secret_delete", "cluster:prod-eu") {
		t.Fatal("a delete was audited for the renamed cluster's live Secret")
	}
}

// R13: two Sharko Secrets show the same cluster name and that name left
// Git. Sharko cannot tell which one is meant, so it deletes neither.
func TestSweep_TwoSecretsSameShownName_DeletesNeither(t *testing.T) {
	k8s := fake.NewSimpleClientset(
		keptOrphanSecret("cluster-a-1", "twin"),
		keptOrphanSecret("cluster-a-2", "twin"),
		keeperSecret(),
	)
	r := newReconcilerForTest(t, nil, k8s, &fakeVault{}, &auditCollector{}, envelopedManagedClusters("keeper"))
	r.pollOnce(context.Background())
	secretExists(t, k8s, "cluster-a-1")
	secretExists(t, k8s, "cluster-a-2")
}

// R4: every pass runs the waiting-marks step once, before the diff.
func TestPollOnce_RunsTheWaitingMarksStep(t *testing.T) {
	k8s := fake.NewSimpleClientset(keeperSecret())
	r := newReconcilerForTest(t, nil, k8s, &fakeVault{}, &auditCollector{}, envelopedManagedClusters("keeper"))
	calls := 0
	r.deps.WaitingMarksFn = func(context.Context) { calls++ }
	r.pollOnce(context.Background())
	r.pollOnce(context.Background())
	if calls != 2 {
		t.Fatalf("the waiting-marks step ran %d times in two passes, want 2", calls)
	}
}

// EP3 dry run round S, S1: a cleanup=all removal that merged but has not
// finished holds the connection. The sweep must not delete it first: the
// finish still needs it to reach the cluster and remove the addon secrets.
func TestSweep_UnfinishedCleanupAllRemoval_HoldsTheConnection_RoundS(t *testing.T) {
	sec := keptOrphanSecret("going", "")
	k8s := fake.NewSimpleClientset(sec, keeperSecret())
	audits := &auditCollector{}
	r := newReconcilerForTest(t, nil, k8s, &fakeVault{}, audits, envelopedManagedClusters("keeper"))
	r.deps.KeptConnectionsFn = func(context.Context) (map[string]models.ConnectionHold, error) {
		return map[string]models.ConnectionHold{"going": {Hold: true}}, nil
	}
	r.pollOnce(context.Background())

	got := secretExists(t, k8s, "going")
	if got.Labels[LabelManagedBy] != LabelValueSharko {
		t.Fatalf("a held connection was changed: %v", got.Labels)
	}
	if hasEventForResource(audits.Snapshot(), "cluster_secret_delete", "cluster:going") {
		t.Fatal("a delete was audited for a connection an unfinished removal holds")
	}
}

// EP3 dry run round S, S2: a merged removal kept the connection, but a new
// registration of the same cluster now waits on the Secret. The sweep
// hands nothing back: the label and the marks stay as they are.
func TestSweep_KeptConnection_NewRegistrationWaiting_NotHandedBack_RoundS(t *testing.T) {
	sec := keptOrphanSecret("again", "")
	sec.Annotations = map[string]string{
		argosecrets.AnnotationRemoveKeepPendingPR: "3",
		models.AnnotationRegistrationPending:      "2020-01-01T00:00:00Z",
	}
	k8s := fake.NewSimpleClientset(sec, keeperSecret())
	r := newReconcilerForTest(t, nil, k8s, &fakeVault{}, &auditCollector{}, envelopedManagedClusters("keeper"))
	r.deps.KeptConnectionsFn = func(context.Context) (map[string]models.ConnectionHold, error) {
		return map[string]models.ConnectionHold{"again": {ReleasePR: 3, Hold: true}}, nil
	}
	r.pollOnce(context.Background())

	got := secretExists(t, k8s, "again")
	if got.Labels[LabelManagedBy] != LabelValueSharko {
		t.Fatalf("the sweep took the label off a new registration: %v", got.Labels)
	}
	if got.Annotations[argosecrets.AnnotationRemoveKeepPendingPR] != "3" {
		t.Fatalf("the sweep changed the marks on a new registration: %v", got.Annotations)
	}
}

// EP3 dry run round S, S4: handing back a kept connection for pull request
// 3 never takes off the keep mark another pull request (9) put on.
func TestSweep_KeptConnection_OtherPRsKeepMarkStays_RoundS(t *testing.T) {
	sec := keptOrphanSecret("kept", "")
	sec.Annotations = map[string]string{argosecrets.AnnotationRemoveKeepPendingPR: models.PendingPRMark{PR: 9}.String()}
	k8s := fake.NewSimpleClientset(sec, keeperSecret())
	r := newReconcilerForTest(t, nil, k8s, &fakeVault{}, &auditCollector{}, envelopedManagedClusters("keeper"))
	r.deps.KeptConnectionsFn = func(context.Context) (map[string]models.ConnectionHold, error) {
		return map[string]models.ConnectionHold{"kept": {ReleasePR: 3, Hold: true}}, nil
	}
	r.pollOnce(context.Background())

	got := secretExists(t, k8s, "kept")
	if !models.PendingPRMarkNames(got.Annotations[argosecrets.AnnotationRemoveKeepPendingPR], 9) {
		t.Fatalf("the hand-back for PR 3 took off PR 9's keep mark: %v", got.Annotations)
	}
}

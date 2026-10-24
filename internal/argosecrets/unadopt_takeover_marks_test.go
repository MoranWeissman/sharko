package argosecrets

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/models"
)

// EP3-DRYRUN-FIXES call 9: un-adopt hands the connection back, so the
// take-over marks go too. Left behind, they would still say Sharko took
// the Secret over. Everything else on the Secret stays.
func TestUnadopt_RemovesTheTakeOverMarks(t *testing.T) {
	t.Parallel()
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "taken-over",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType: "cluster",
				LabelManagedBy:  ManagedByValue,
				"team":          "payments",
			},
			Annotations: map[string]string{
				AnnotationAdopted:                 "true",
				AnnotationTakenOverAt:             "2026-10-23T10:00:00Z",
				AnnotationTakeoverPreservedLabels: "team",
				"owner-note":                      "keep me",
			},
		},
		Type: corev1.SecretTypeOpaque,
	}
	client := fake.NewSimpleClientset(existing)
	if err := NewManager(client, testNamespace).Unadopt(context.Background(), "taken-over"); err != nil {
		t.Fatalf("Unadopt: %v", err)
	}
	got, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "taken-over", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	for _, key := range []string{AnnotationAdopted, AnnotationTakenOverAt, AnnotationTakeoverPreservedLabels} {
		if _, still := got.Annotations[key]; still {
			t.Errorf("annotation %s is still on the Secret after un-adopt", key)
		}
	}
	if _, still := got.Labels[LabelManagedBy]; still {
		t.Errorf("Sharko's ownership label is still on the Secret after un-adopt")
	}
	if got.Annotations["owner-note"] != "keep me" || got.Labels["team"] != "payments" {
		t.Errorf("un-adopt touched something that is not Sharko's: labels %v annotations %v", got.Labels, got.Annotations)
	}
}

// EP3 dry run round S, S4: a hand-back for one pull request never takes
// off the keep mark another pull request put on, and a mark is matched by
// the pull request it names, in whichever form it was written.
func TestReleaseKeptConnection_OnlyItsOwnKeepMark_RoundS(t *testing.T) {
	ctx := context.Background()
	other := models.PendingPRMark{PR: 9, Op: models.PendingMarkOpRemove}.String()
	s := argoAddedSecret(argoK8sName, "legacy-1", map[string]string{LabelManagedBy: ManagedByValue})
	s.Annotations = map[string]string{AnnotationRemoveKeepPendingPR: other}
	client := fake.NewSimpleClientset(s)
	m := NewManager(client, testNamespace)
	if _, err := m.ReleaseKeptConnection(ctx, "legacy-1", 4); err != nil {
		t.Fatal(err)
	}
	live, _ := client.CoreV1().Secrets(testNamespace).Get(ctx, argoK8sName, metav1.GetOptions{})
	if live.Annotations[AnnotationRemoveKeepPendingPR] != other {
		t.Fatalf("the hand-back for PR 4 took off PR 9's mark: %v", live.Annotations)
	}
	if _, still := live.Labels[LabelManagedBy]; still {
		t.Fatalf("the label should come off: %v", live.Labels)
	}
	if _, err := m.ReleaseKeptConnection(ctx, "legacy-1", 9); err != nil {
		t.Fatal(err)
	}
	live, _ = client.CoreV1().Secrets(testNamespace).Get(ctx, argoK8sName, metav1.GetOptions{})
	if _, still := live.Annotations[AnnotationRemoveKeepPendingPR]; still {
		t.Fatalf("PR 9's own hand-back must take its mark off: %v", live.Annotations)
	}
}

func TestRemoveAnnotationIfValue_MatchesTheMarksPR_RoundS(t *testing.T) {
	ctx := context.Background()
	s := argoAddedSecret(argoK8sName, "legacy-1", map[string]string{LabelManagedBy: ManagedByValue})
	s.Annotations = map[string]string{AnnotationAdoptPendingPR: models.PendingPRMark{PR: 12, Repo: "acme/gitops"}.String()}
	client := fake.NewSimpleClientset(s)
	m := NewManager(client, testNamespace)
	if removed, _ := m.RemoveAnnotationIfValue(ctx, "legacy-1", AnnotationAdoptPendingPR, "13"); removed {
		t.Fatal("PR 13 took off PR 12's mark")
	}
	if removed, err := m.RemoveAnnotationIfValue(ctx, "legacy-1", AnnotationAdoptPendingPR, "12"); err != nil || !removed {
		t.Fatalf("PR 12 could not take its own mark off: %v %v", removed, err)
	}
}

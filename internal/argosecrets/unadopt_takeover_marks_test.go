package argosecrets

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
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

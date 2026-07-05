package argosecrets

// StripOwnershipLabel tests (V2-cleanup-60.1): the handover-at-removal-time
// primitive. It must remove ONLY app.kubernetes.io/managed-by: sharko,
// leave every other byte of the secret alone, and be a safe no-op on
// missing secrets and on secrets Sharko does not own.

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestStripOwnershipLabel_RemovesOnlyTheSharkoLabel(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "byo-conn",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelManagedBy:  ManagedByValue,
				LabelSecretType: "cluster",
				"monitoring":    "enabled",
				"team":          "payments",
			},
			Annotations: map[string]string{
				"user-note": "hands off",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte("byo-conn"),
			"server": []byte("https://byo.example.com"),
			"config": []byte(`{"bearerToken":"user-token"}`),
		},
	}

	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	stripped, err := mgr.StripOwnershipLabel(context.Background(), "byo-conn")
	if err != nil {
		t.Fatalf("StripOwnershipLabel() error: %v", err)
	}
	if !stripped {
		t.Fatal("expected stripped=true for a sharko-labeled secret")
	}

	got, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "byo-conn", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret vanished after strip: %v", err)
	}
	if _, ok := got.Labels[LabelManagedBy]; ok {
		t.Errorf("ownership label still present: %q", got.Labels[LabelManagedBy])
	}
	// Everything else stays, verbatim.
	if got.Labels[LabelSecretType] != "cluster" {
		t.Errorf("secret-type label modified: %q", got.Labels[LabelSecretType])
	}
	if got.Labels["monitoring"] != "enabled" || got.Labels["team"] != "payments" {
		t.Errorf("addon/foreign labels modified: %v", got.Labels)
	}
	if got.Annotations["user-note"] != "hands off" {
		t.Errorf("annotations modified: %v", got.Annotations)
	}
	if string(got.Data["config"]) != `{"bearerToken":"user-token"}` {
		t.Errorf("connection data modified: %q", got.Data["config"])
	}
}

func TestStripOwnershipLabel_MissingSecret_IsNoOp(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := NewManager(client, testNamespace)

	stripped, err := mgr.StripOwnershipLabel(context.Background(), "never-created")
	if err != nil {
		t.Fatalf("missing secret must not error, got: %v", err)
	}
	if stripped {
		t.Fatal("expected stripped=false for a missing secret")
	}
}

func TestStripOwnershipLabel_UnlabeledOrForeignSecret_IsNoWrite(t *testing.T) {
	for name, labels := range map[string]map[string]string{
		"no-managed-by": {LabelSecretType: "cluster"},
		"foreign-owner": {LabelSecretType: "cluster", LabelManagedBy: "terraform"},
	} {
		existing := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testNamespace,
				Labels:    labels,
			},
			Type: corev1.SecretTypeOpaque,
		}
		client := fake.NewSimpleClientset(existing)
		mgr := NewManager(client, testNamespace)

		stripped, err := mgr.StripOwnershipLabel(context.Background(), name)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if stripped {
			t.Errorf("%s: expected stripped=false", name)
		}
		for _, a := range client.Actions() {
			if a.GetVerb() == "update" {
				t.Errorf("%s: expected no update action on a secret sharko does not own", name)
			}
		}
	}
}

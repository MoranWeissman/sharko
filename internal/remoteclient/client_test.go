package remoteclient

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func TestNewClientFromKubeconfig_InvalidBytes(t *testing.T) {
	_, err := NewClientFromKubeconfig([]byte("not valid kubeconfig"))
	if err == nil {
		t.Error("expected error for invalid kubeconfig")
	}
}

func TestEnsureSecret_CreatesNew(t *testing.T) {
	client := fake.NewSimpleClientset()
	err := EnsureSecret(context.Background(), client, "datadog", "datadog-keys", map[string][]byte{
		"api-key": []byte("secret-value"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	secret, err := client.CoreV1().Secrets("datadog").Get(context.Background(), "datadog-keys", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not found: %v", err)
	}
	if secret.Labels["app.kubernetes.io/managed-by"] != "sharko" {
		t.Error("expected managed-by label")
	}
	if string(secret.Data["api-key"]) != "secret-value" {
		t.Errorf("unexpected data: %s", secret.Data["api-key"])
	}
}

func TestEnsureSecret_UpdatesExisting(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "datadog-keys", Namespace: "datadog",
			Labels: map[string]string{"app.kubernetes.io/managed-by": "sharko"},
		},
		Data: map[string][]byte{"api-key": []byte("old-value")},
	}
	client := fake.NewSimpleClientset(existing)
	err := EnsureSecret(context.Background(), client, "datadog", "datadog-keys", map[string][]byte{
		"api-key": []byte("new-value"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	secret, _ := client.CoreV1().Secrets("datadog").Get(context.Background(), "datadog-keys", metav1.GetOptions{})
	if string(secret.Data["api-key"]) != "new-value" {
		t.Errorf("expected updated value, got %s", secret.Data["api-key"])
	}
}

func TestDeleteManagedSecrets(t *testing.T) {
	secrets := []runtime.Object{
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: "datadog-keys", Namespace: "datadog",
				Labels: map[string]string{"app.kubernetes.io/managed-by": "sharko"},
			},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: "user-secret", Namespace: "datadog",
				// No managed-by label — should NOT be deleted.
			},
		},
	}
	client := fake.NewSimpleClientset(secrets...)

	deleted, err := DeleteManagedSecrets(context.Background(), client, "datadog")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != "datadog-keys" {
		t.Errorf("expected [datadog-keys], got %v", deleted)
	}

	// User secret should still exist.
	_, err = client.CoreV1().Secrets("datadog").Get(context.Background(), "user-secret", metav1.GetOptions{})
	if err != nil {
		t.Error("user secret should not have been deleted")
	}
}

func TestListManagedSecrets(t *testing.T) {
	secrets := []runtime.Object{
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: "s1", Namespace: "ns1",
				Labels: map[string]string{"app.kubernetes.io/managed-by": "sharko"},
			},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: "s2", Namespace: "ns2",
				Labels: map[string]string{"app.kubernetes.io/managed-by": "sharko"},
			},
		},
	}
	client := fake.NewSimpleClientset(secrets...)

	result, err := ListManagedSecrets(context.Background(), client, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 secrets, got %d", len(result))
	}
}

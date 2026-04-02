package providers

import (
	"encoding/base64"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// testKubeconfig returns a minimal kubeconfig YAML for testing.
func testKubeconfig(server string) []byte {
	ca := base64.StdEncoding.EncodeToString([]byte("fake-ca-data"))
	return []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: ` + server + `
    certificate-authority-data: ` + ca + `
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: test-user
  name: test-context
current-context: test-context
users:
- name: test-user
  user:
    token: test-token-123
`)
}

func TestGetCredentials_ValidSecret(t *testing.T) {
	kubeconfig := testKubeconfig("https://api.cluster-1.example.com:6443")

	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-1",
			Namespace: "sharko",
		},
		Data: map[string][]byte{
			"kubeconfig": kubeconfig,
		},
	})

	provider := newKubernetesSecretProviderWithClient(client, "sharko")

	kc, err := provider.GetCredentials("cluster-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if kc.Server != "https://api.cluster-1.example.com:6443" {
		t.Errorf("expected server %q, got %q", "https://api.cluster-1.example.com:6443", kc.Server)
	}

	if kc.Token != "test-token-123" {
		t.Errorf("expected token %q, got %q", "test-token-123", kc.Token)
	}

	if len(kc.Raw) == 0 {
		t.Error("expected non-empty Raw kubeconfig")
	}

	if len(kc.CAData) == 0 {
		t.Error("expected non-empty CAData")
	}
}

func TestGetCredentials_MissingSecret(t *testing.T) {
	client := fake.NewSimpleClientset()
	provider := newKubernetesSecretProviderWithClient(client, "sharko")

	_, err := provider.GetCredentials("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing secret, got nil")
	}
}

func TestGetCredentials_MissingKubeconfigKey(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-no-kc",
			Namespace: "sharko",
		},
		Data: map[string][]byte{
			"other-key": []byte("not-a-kubeconfig"),
		},
	})

	provider := newKubernetesSecretProviderWithClient(client, "sharko")

	_, err := provider.GetCredentials("cluster-no-kc")
	if err == nil {
		t.Fatal("expected error for missing kubeconfig key, got nil")
	}
}

func TestListClusters(t *testing.T) {
	kubeconfig := testKubeconfig("https://api.cluster-a.example.com:6443")
	labels := map[string]string{
		"app.kubernetes.io/managed-by": "sharko",
		"region":                       "us-east-1",
	}

	client := fake.NewSimpleClientset(
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster-a",
				Namespace: "sharko",
				Labels:    labels,
			},
			Data: map[string][]byte{
				"kubeconfig": kubeconfig,
			},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster-b",
				Namespace: "sharko",
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "sharko",
					"region":                       "eu-west-1",
				},
			},
			Data: map[string][]byte{
				"kubeconfig": kubeconfig,
			},
		},
		// Secret without kubeconfig key — should be skipped
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "not-a-cluster",
				Namespace: "sharko",
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "sharko",
				},
			},
			Data: map[string][]byte{
				"other": []byte("data"),
			},
		},
	)

	provider := newKubernetesSecretProviderWithClient(client, "sharko")

	clusters, err := provider.ListClusters()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(clusters) != 2 {
		t.Fatalf("expected 2 clusters, got %d", len(clusters))
	}

	// Build a map for easier assertions
	byName := make(map[string]ClusterInfo)
	for _, c := range clusters {
		byName[c.Name] = c
	}

	if c, ok := byName["cluster-a"]; !ok {
		t.Error("expected cluster-a in results")
	} else if c.Region != "us-east-1" {
		t.Errorf("expected region us-east-1, got %q", c.Region)
	}

	if c, ok := byName["cluster-b"]; !ok {
		t.Error("expected cluster-b in results")
	} else if c.Region != "eu-west-1" {
		t.Errorf("expected region eu-west-1, got %q", c.Region)
	}
}

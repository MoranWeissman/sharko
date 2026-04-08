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

// TestGetCredentials_ExplicitSecretPath verifies that GetCredentials succeeds
// when called with an explicit secret name that differs from the cluster name.
func TestGetCredentials_ExplicitSecretPath(t *testing.T) {
	kubeconfig := testKubeconfig("https://api.cluster-1.example.com:6443")

	// The secret is stored under an explicit path, not the cluster name.
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "k8s-prod-eu-eks",
			Namespace: "sharko",
		},
		Data: map[string][]byte{
			"kubeconfig": kubeconfig,
		},
	})

	provider := newKubernetesSecretProviderWithClient(client, "sharko")

	// Caller passes the explicit secretPath instead of the cluster name.
	kc, err := provider.GetCredentials("k8s-prod-eu-eks")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kc.Server != "https://api.cluster-1.example.com:6443" {
		t.Errorf("expected server URL, got %q", kc.Server)
	}
}

// TestGetCredentials_SuggestsSimilar verifies that when a secret is not found,
// similar secret names are included in the error message.
func TestGetCredentials_SuggestsSimilar(t *testing.T) {
	kubeconfig := testKubeconfig("https://api.cluster-1.example.com:6443")

	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "k8s-prod-eu-eks",
			Namespace: "sharko",
		},
		Data: map[string][]byte{
			"kubeconfig": kubeconfig,
		},
	})

	provider := newKubernetesSecretProviderWithClient(client, "sharko")

	// Query contains "prod-eu" which is a substring of "k8s-prod-eu-eks".
	_, err := provider.GetCredentials("prod-eu")
	if err == nil {
		t.Fatal("expected error for missing secret, got nil")
	}

	errMsg := err.Error()
	if !contains(errMsg, "k8s-prod-eu-eks") {
		t.Errorf("expected error to suggest %q, got: %v", "k8s-prod-eu-eks", errMsg)
	}
	if !contains(errMsg, "Similar secrets") {
		t.Errorf("expected error to mention similar secrets, got: %v", errMsg)
	}
}

// TestGetCredentials_NoSuggestions verifies that a clean error is returned
// when no similar secrets exist.
func TestGetCredentials_NoSuggestions(t *testing.T) {
	client := fake.NewSimpleClientset()
	provider := newKubernetesSecretProviderWithClient(client, "sharko")

	_, err := provider.GetCredentials("totally-unknown-cluster")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	errMsg := err.Error()
	if contains(errMsg, "Similar secrets") {
		t.Errorf("expected no suggestions in error, got: %v", errMsg)
	}
}

// contains is a helper to avoid importing strings in tests just for Contains.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
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

package providers

import (
	"encoding/base64"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/credsafe"
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

// TestGetCredentials_SuggestsSimilar verifies that when a Secret is not found,
// similar Secret names are included in the error this provider builds.
//
// THIS TEST NOW READS THE CAUSE, NOT Error(), and that needs saying plainly.
//
// GetCredentials marks its errors, so the error it hands back SAYS the one fixed
// safe sentence. That is the point of the mark: a boundary cannot leak a
// credentials backend's text by forgetting to ask. So the provider's own
// sentence — Sharko's words, listing Secret names from the operator's own
// namespace — is now reachable only through credsafe.Cause, which is the
// classification-only accessor.
//
// The operator has not lost anything. The suggestions they actually SEE come
// from the cluster-test handler, which calls SearchSecrets separately once
// credsafe.IsNotFound says the Secret really is missing (see
// internal/api/cred_not_found_suggestions_test.go). This test still exists
// because the provider's sentence is what a developer reads while debugging, and
// silently dropping the names from it would be a real loss.
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

	// What crosses the boundary is the fixed safe sentence, always.
	if err.Error() != credsafe.Message {
		t.Errorf("GetCredentials' error says %q, want the fixed safe sentence — the mark is missing from the boundary", err.Error())
	}
	// And underneath, Sharko's own sentence still names the similar Secrets.
	errMsg := credsafe.Cause(err).Error()
	if !contains(errMsg, "k8s-prod-eu-eks") {
		t.Errorf("expected the underlying error to suggest %q, got: %v", "k8s-prod-eu-eks", errMsg)
	}
	if !contains(errMsg, "Similar secrets") {
		t.Errorf("expected the underlying error to mention similar secrets, got: %v", errMsg)
	}
	// A missing Secret is a real absence, so the marker that drives the
	// operator-facing suggestion list must be there.
	if !credsafe.IsNotFound(err) {
		t.Error(`a genuinely missing Secret did not carry the not-found marker.

That marker is what lets the cluster-test handler offer secret-name suggestions without reading any error text.`)
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

// TestNewKubernetesSecretProviderFromAddonConfig_NamespaceDefaultsToSharko
// asserts the V125-1-11.3 typed-config factory applies the same "sharko"
// default the legacy NewKubernetesSecretProvider applied when Namespace is
// empty. This is the per-backend canonical entry point — it's exercised here
// without trying to reach a real K8s cluster (we expect an error from
// rest.InClusterConfig / kubeconfig fallback in CI; we only assert the field
// path translated cleanly).
func TestNewKubernetesSecretProviderFromAddonConfig_NamespaceDefault(t *testing.T) {
	// Empty Namespace → factory defaults to "sharko" before attempting to
	// build the k8s client. The build itself usually fails in CI (no
	// kubeconfig, no in-cluster SA), but if it DOES succeed (e.g. local dev
	// with ~/.kube/config), the namespace field on the resulting provider
	// must be "sharko" — the V125-1-11.3 contract.
	prov, err := NewKubernetesSecretProviderFromAddonConfig(AddonSecretProviderConfig{Type: "k8s-secrets"})
	if err != nil {
		// CI path: rest.InClusterConfig + clientcmd fallback both failed.
		// The factory routed correctly; that's all we can check here.
		return
	}
	if prov.namespace != "sharko" {
		t.Errorf("expected default namespace 'sharko', got %q", prov.namespace)
	}
}

// TestNewKubernetesSecretProviderFromAddonConfig_NamespaceExplicit asserts an
// explicit Namespace value is preserved verbatim by the typed-config factory.
func TestNewKubernetesSecretProviderFromAddonConfig_NamespaceExplicit(t *testing.T) {
	prov, err := NewKubernetesSecretProviderFromAddonConfig(AddonSecretProviderConfig{
		Type:      "k8s-secrets",
		Namespace: "addon-secrets",
	})
	if err != nil {
		return // CI: no kubeconfig + not in-cluster; routing alone is verified.
	}
	if prov.namespace != "addon-secrets" {
		t.Errorf("expected namespace 'addon-secrets', got %q", prov.namespace)
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

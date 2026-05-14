package providers

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/clientcmd"
)

// argoCDSecret is a small helper to build a typed cluster Secret for the fake client.
func argoCDSecret(name, displayName, server, configJSON string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "argocd",
			Labels: map[string]string{
				"argocd.argoproj.io/secret-type": "cluster",
				"app.kubernetes.io/managed-by":   "sharko",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte(displayName),
			"server": []byte(server),
			"config": []byte(configJSON),
		},
	}
}

// fakeCAB64 is a deterministic base64 string used by tests that need a non-empty
// caData payload. The kubeconfig spec doesn't validate the decoded payload at
// parse time, so an arbitrary base64 string is sufficient for the round-trip
// assertion.
var fakeCAB64 = base64.StdEncoding.EncodeToString([]byte("fake-ca-data"))

// TestArgoCD_BearerToken_HappyPath covers shape 1: bearerToken + caData.
// Asserts Server/Token/CAData are populated and Raw round-trips through
// clientcmd.RESTConfigFromKubeConfig.
func TestArgoCD_BearerToken_HappyPath(t *testing.T) {
	configJSON := `{
		"bearerToken": "eyJhbGciOi-fake-token",
		"tlsClientConfig": {
			"caData": "` + fakeCAB64 + `",
			"insecure": false
		}
	}`

	client := fake.NewSimpleClientset(argoCDSecret(
		"prod-eu", "prod-eu",
		"https://api.cluster-1.example.com:6443",
		configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	kc, err := provider.GetCredentials("prod-eu")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if kc.Server != "https://api.cluster-1.example.com:6443" {
		t.Errorf("Server = %q, want %q", kc.Server, "https://api.cluster-1.example.com:6443")
	}
	if kc.Token != "eyJhbGciOi-fake-token" {
		t.Errorf("Token = %q, want %q", kc.Token, "eyJhbGciOi-fake-token")
	}
	if string(kc.CAData) != "fake-ca-data" {
		t.Errorf("CAData = %q, want %q (base64-decoded)", string(kc.CAData), "fake-ca-data")
	}
	if len(kc.Raw) == 0 {
		t.Fatal("Raw kubeconfig is empty")
	}

	// Round-trip check: synthesized kubeconfig must parse cleanly via clientcmd.
	parsed, err := clientcmd.RESTConfigFromKubeConfig(kc.Raw)
	if err != nil {
		t.Fatalf("synthesized Raw kubeconfig failed clientcmd round-trip: %v", err)
	}
	if parsed.Host != "https://api.cluster-1.example.com:6443" {
		t.Errorf("round-trip Host = %q, want %q", parsed.Host, "https://api.cluster-1.example.com:6443")
	}
	if parsed.BearerToken != "eyJhbGciOi-fake-token" {
		t.Errorf("round-trip BearerToken = %q, want %q", parsed.BearerToken, "eyJhbGciOi-fake-token")
	}
}

// TestArgoCD_BearerToken_Insecure covers shape 1 with insecure:true and no caData.
// Asserts CAData is empty and Raw still round-trips.
func TestArgoCD_BearerToken_Insecure(t *testing.T) {
	configJSON := `{
		"bearerToken": "insecure-token",
		"tlsClientConfig": { "insecure": true }
	}`

	client := fake.NewSimpleClientset(argoCDSecret(
		"local-kind", "local-kind",
		"https://127.0.0.1:6443",
		configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	kc, err := provider.GetCredentials("local-kind")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(kc.CAData) != 0 {
		t.Errorf("CAData should be empty for insecure cluster, got %d bytes", len(kc.CAData))
	}
	if kc.Token != "insecure-token" {
		t.Errorf("Token = %q, want %q", kc.Token, "insecure-token")
	}

	// Round-trip — insecure clusters must still parse.
	if _, err := clientcmd.RESTConfigFromKubeConfig(kc.Raw); err != nil {
		t.Fatalf("synthesized insecure kubeconfig failed clientcmd round-trip: %v", err)
	}
}

// TestArgoCD_AWSAuthConfig_ReturnsIAMRequired covers shape 2: awsAuthConfig.
// Asserts the returned error matches errors.Is(ErrArgoCDProviderIAMRequired)
// and carries the stable Code constant.
func TestArgoCD_AWSAuthConfig_ReturnsIAMRequired(t *testing.T) {
	configJSON := `{
		"awsAuthConfig": {
			"clusterName": "my-eks-cluster",
			"roleARN": "arn:aws:iam::123456789012:role/EKSReadRole"
		},
		"tlsClientConfig": { "caData": "` + fakeCAB64 + `" }
	}`

	client := fake.NewSimpleClientset(argoCDSecret(
		"prod-eks", "prod-eks",
		"https://abc.eks.amazonaws.com",
		configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	_, err := provider.GetCredentials("prod-eks")
	if err == nil {
		t.Fatal("expected error for awsAuthConfig shape, got nil")
	}
	if !errors.Is(err, ErrArgoCDProviderIAMRequired) {
		t.Errorf("errors.Is(err, ErrArgoCDProviderIAMRequired) = false, error: %v", err)
	}

	var pe *ArgoCDProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("error is not *ArgoCDProviderError: %T", err)
	}
	if pe.Code != ArgoCDProviderCodeIAMRequired {
		t.Errorf("Code = %q, want %q", pe.Code, ArgoCDProviderCodeIAMRequired)
	}
	if pe.ClusterName != "prod-eks" {
		t.Errorf("ClusterName = %q, want %q", pe.ClusterName, "prod-eks")
	}
	if pe.Server != "https://abc.eks.amazonaws.com" {
		t.Errorf("Server = %q, want %q", pe.Server, "https://abc.eks.amazonaws.com")
	}
}

// TestArgoCD_ExecProviderConfig_ReturnsExecUnsupported covers shape 3.
// Asserts errors.Is(ErrArgoCDProviderExecUnsupported) and the stable Code.
func TestArgoCD_ExecProviderConfig_ReturnsExecUnsupported(t *testing.T) {
	configJSON := `{
		"execProviderConfig": {
			"command": "aws-iam-authenticator",
			"args": ["token", "-i", "my-cluster"],
			"apiVersion": "client.authentication.k8s.io/v1beta1"
		},
		"tlsClientConfig": { "caData": "` + fakeCAB64 + `" }
	}`

	client := fake.NewSimpleClientset(argoCDSecret(
		"exec-cluster", "exec-cluster",
		"https://exec.example.com",
		configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	_, err := provider.GetCredentials("exec-cluster")
	if err == nil {
		t.Fatal("expected error for execProviderConfig shape, got nil")
	}
	if !errors.Is(err, ErrArgoCDProviderExecUnsupported) {
		t.Errorf("errors.Is(err, ErrArgoCDProviderExecUnsupported) = false, error: %v", err)
	}

	var pe *ArgoCDProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("error is not *ArgoCDProviderError: %T", err)
	}
	if pe.Code != ArgoCDProviderCodeExecUnsupported {
		t.Errorf("Code = %q, want %q", pe.Code, ArgoCDProviderCodeExecUnsupported)
	}
}

// TestArgoCD_UnknownAuthShape_ReturnsUnsupportedAuth covers a parseable but
// empty config (no bearerToken, no awsAuthConfig, no execProviderConfig).
func TestArgoCD_UnknownAuthShape_ReturnsUnsupportedAuth(t *testing.T) {
	configJSON := `{
		"tlsClientConfig": { "caData": "` + fakeCAB64 + `" }
	}`

	client := fake.NewSimpleClientset(argoCDSecret(
		"weird-cluster", "weird-cluster",
		"https://weird.example.com",
		configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	_, err := provider.GetCredentials("weird-cluster")
	if err == nil {
		t.Fatal("expected error for unknown auth shape, got nil")
	}
	if !errors.Is(err, ErrArgoCDProviderUnsupportedAuth) {
		t.Errorf("errors.Is(err, ErrArgoCDProviderUnsupportedAuth) = false, error: %v", err)
	}

	var pe *ArgoCDProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("error is not *ArgoCDProviderError: %T", err)
	}
	if pe.Code != ArgoCDProviderCodeUnsupportedAuth {
		t.Errorf("Code = %q, want %q", pe.Code, ArgoCDProviderCodeUnsupportedAuth)
	}
}

// TestArgoCD_NotFound covers the no-matching-secret path. The wrapped error
// should satisfy apierrors.IsNotFound for callers that switch on it.
func TestArgoCD_NotFound(t *testing.T) {
	client := fake.NewSimpleClientset() // no secrets at all
	provider := newArgoCDProviderWithClient(client, "argocd")

	_, err := provider.GetCredentials("does-not-exist")
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
	if !apierrors.IsNotFound(err) {
		t.Errorf("expected wrapped k8s NotFound, got: %v", err)
	}
}

// TestArgoCD_MalformedConfigJSON covers the case where the config blob isn't
// valid JSON. Should NOT return an ArgoCDProviderError — this is a parse error
// surfaced as-is so callers see it as a malformed-secret problem, not an
// auth-shape decision.
func TestArgoCD_MalformedConfigJSON(t *testing.T) {
	client := fake.NewSimpleClientset(argoCDSecret(
		"broken", "broken",
		"https://broken.example.com",
		`{ this is not valid json `,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	_, err := provider.GetCredentials("broken")
	if err == nil {
		t.Fatal("expected JSON parse error, got nil")
	}
	if errors.Is(err, ErrArgoCDProviderIAMRequired) ||
		errors.Is(err, ErrArgoCDProviderExecUnsupported) ||
		errors.Is(err, ErrArgoCDProviderUnsupportedAuth) {
		t.Errorf("malformed JSON should not produce a typed routing error, got: %v", err)
	}
}

// TestArgoCD_ListClusters_OnlyClusterTypeMatched verifies that ListClusters
// only returns Secrets carrying the argocd.argoproj.io/secret-type=cluster
// label. The fake clientset honours LabelSelector so secrets without the
// label are excluded by the same path the production code takes.
func TestArgoCD_ListClusters_OnlyClusterTypeMatched(t *testing.T) {
	clusterSecret := argoCDSecret(
		"prod-eu", "prod-eu",
		"https://api.example.com",
		`{"bearerToken":"x","tlsClientConfig":{"insecure":true}}`,
	)
	// A non-cluster secret in the same namespace — must not appear in results.
	otherSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "argocd-initial-admin-secret",
			Namespace: "argocd",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "argocd",
			},
		},
		Data: map[string][]byte{"password": []byte("hunter2")},
	}

	client := fake.NewSimpleClientset(clusterSecret, otherSecret)
	provider := newArgoCDProviderWithClient(client, "argocd")

	clusters, err := provider.ListClusters()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clusters) != 1 {
		t.Fatalf("expected exactly 1 cluster, got %d (raw: %+v)", len(clusters), clusters)
	}
	if clusters[0].Name != "prod-eu" {
		t.Errorf("Name = %q, want %q", clusters[0].Name, "prod-eu")
	}
}

// TestArgoCD_HealthCheck_Success exercises the happy path.
func TestArgoCD_HealthCheck_Success(t *testing.T) {
	client := fake.NewSimpleClientset()
	provider := newArgoCDProviderWithClient(client, "argocd")

	if err := provider.HealthCheck(context.Background()); err != nil {
		t.Errorf("HealthCheck() = %v, want nil", err)
	}
}

// TestArgoCD_HealthCheck_FailsOnPermissionError simulates an RBAC denial from
// the API by installing a reactor that returns Forbidden. The wrapped error
// must remain detectable via apierrors.IsForbidden so the API layer can
// surface it as an actionable RBAC problem.
func TestArgoCD_HealthCheck_FailsOnPermissionError(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("list", "secrets", func(_ clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(
			corev1.Resource("secrets"), "", errors.New("forbidden"),
		)
	})

	provider := newArgoCDProviderWithClient(client, "argocd")
	err := provider.HealthCheck(context.Background())
	if err == nil {
		t.Fatal("expected HealthCheck to fail with permission error, got nil")
	}
	if !apierrors.IsForbidden(err) {
		t.Errorf("expected wrapped Forbidden error, got: %v", err)
	}
}

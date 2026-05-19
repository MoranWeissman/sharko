package providers

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
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

// TestResolveArgoCDNamespaceTyped covers the V125-1-11.4 canonical behaviour:
// the typed ClusterTestProviderConfig.ArgoCDNamespace field is the single
// authoritative source. SHARKO_ARGOCD_NAMESPACE env var remains a deprecated
// compat alias (slog.Warn). The hardcoded "argocd" default applies last.
func TestResolveArgoCDNamespaceTyped(t *testing.T) {
	tests := []struct {
		name        string
		cfg         ClusterTestProviderConfig
		envValue    string // empty string → unset
		setEnv      bool   // true → call t.Setenv even if envValue is ""
		wantNS      string
		description string
	}{
		{
			name:        "default_when_cfg_empty_and_no_env",
			cfg:         ClusterTestProviderConfig{Type: "argocd"},
			wantNS:      "argocd",
			description: "no inputs → hardcoded argocd default",
		},
		{
			name:        "canonical_field_used",
			cfg:         ClusterTestProviderConfig{Type: "argocd", ArgoCDNamespace: "custom"},
			wantNS:      "custom",
			description: "ClusterTestProviderConfig.ArgoCDNamespace is the canonical source",
		},
		{
			name:        "canonical_field_wins_over_env",
			cfg:         ClusterTestProviderConfig{Type: "argocd", ArgoCDNamespace: "from-config"},
			envValue:    "from-env",
			setEnv:      true,
			wantNS:      "from-config",
			description: "typed config field takes precedence over the deprecated env var",
		},
		{
			name:        "env_var_used_when_field_empty",
			cfg:         ClusterTestProviderConfig{Type: "argocd"},
			envValue:    "legacy-ns",
			setEnv:      true,
			wantNS:      "legacy-ns",
			description: "SHARKO_ARGOCD_NAMESPACE deprecated compat alias still works",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv("SHARKO_ARGOCD_NAMESPACE", tt.envValue)
			} else {
				// Defensive: if a parent process has SHARKO_ARGOCD_NAMESPACE set,
				// neutralise it for tests that expect the hardcoded default.
				t.Setenv("SHARKO_ARGOCD_NAMESPACE", "")
			}

			got := resolveArgoCDNamespaceTyped(tt.cfg)
			if got != tt.wantNS {
				t.Errorf("resolveArgoCDNamespaceTyped(%+v) = %q, want %q (%s)",
					tt.cfg, got, tt.wantNS, tt.description)
			}
		})
	}
}

// TestResolveArgoCDNamespaceTyped_DeprecationWarnEmitted confirms the
// SHARKO_ARGOCD_NAMESPACE compat alias emits a slog.Warn so operators see they
// are on the legacy path. The warn is the deprecation signal; removed in v1.26
// per V125-1-11 planning doc OQ #4.
func TestResolveArgoCDNamespaceTyped_DeprecationWarnEmitted(t *testing.T) {
	t.Setenv("SHARKO_ARGOCD_NAMESPACE", "legacy-ns")

	// Capture slog output through a buffer + JSON handler, restoring the
	// default at test end so other tests aren't affected.
	var buf bytes.Buffer
	prevDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prevDefault) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	got := resolveArgoCDNamespaceTyped(ClusterTestProviderConfig{Type: "argocd"})
	if got != "legacy-ns" {
		t.Fatalf("resolveArgoCDNamespaceTyped(empty field, env=legacy-ns) = %q, want %q", got, "legacy-ns")
	}
	out := buf.String()
	if !strings.Contains(out, "SHARKO_ARGOCD_NAMESPACE") || !strings.Contains(out, "deprecated") {
		t.Errorf("expected deprecation slog.Warn mentioning SHARKO_ARGOCD_NAMESPACE + deprecated, got: %q", out)
	}
	if !strings.Contains(out, "legacy-ns") {
		t.Errorf("expected warn payload to include the env value %q, got: %q", "legacy-ns", out)
	}
}

// TestResolveArgoCDNamespaceTyped_NoWarnWhenCanonicalFieldUsed confirms the
// happy path is silent — operators on the canonical typed config do NOT see
// the deprecation warn.
func TestResolveArgoCDNamespaceTyped_NoWarnWhenCanonicalFieldUsed(t *testing.T) {
	// Set env var so we can prove the canonical field's precedence suppresses
	// the warn (env wouldn't be consulted at all).
	t.Setenv("SHARKO_ARGOCD_NAMESPACE", "should-be-ignored")

	var buf bytes.Buffer
	prevDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prevDefault) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	got := resolveArgoCDNamespaceTyped(ClusterTestProviderConfig{Type: "argocd", ArgoCDNamespace: "from-config"})
	if got != "from-config" {
		t.Fatalf("resolveArgoCDNamespaceTyped(canonical field) = %q, want %q", got, "from-config")
	}
	if strings.Contains(buf.String(), "deprecated") {
		t.Errorf("did not expect deprecation warn when canonical field is set, got: %q", buf.String())
	}
}

// TestResolveArgoCDNamespaceTyped_CrossContaminationStaysClosed is the
// V125-1-11.6 successor to the V125-1-10.8 compat-shim regression check.
// With providers.Config retired, the cross-contamination from k8s-secrets
// addon-secret config into ArgoCD namespace resolution is now structurally
// impossible — there's no shared field to leak through. This test pins the
// canonical resolution behaviour: empty ClusterTestProviderConfig.ArgoCDNamespace
// + unset SHARKO_ARGOCD_NAMESPACE → defaults to "argocd", regardless of any
// other config in the program.
func TestResolveArgoCDNamespaceTyped_CrossContaminationStaysClosed(t *testing.T) {
	t.Setenv("SHARKO_ARGOCD_NAMESPACE", "")
	cfg := ClusterTestProviderConfig{Type: "argocd"} // no ArgoCDNamespace
	if got := resolveArgoCDNamespaceTyped(cfg); got != "argocd" {
		t.Errorf("unset typed namespace + unset env should resolve to default %q, got %q", "argocd", got)
	}
}

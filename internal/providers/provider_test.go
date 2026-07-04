package providers

import (
	"errors"
	"strings"
	"testing"

	"k8s.io/client-go/rest"
)

// withInClusterFn temporarily replaces the in-cluster probe used by
// NewClusterTestProvider() and arranges automatic restoration via t.Cleanup.
func withInClusterFn(t *testing.T, fn func() (*rest.Config, error)) {
	t.Helper()
	orig := inClusterConfigFn
	inClusterConfigFn = fn
	t.Cleanup(func() { inClusterConfigFn = orig })
}

// fakeInCluster makes inClusterConfigFn return (non-nil, nil) — i.e. "we're
// running inside a K8s pod with a service-account token mounted." We return a
// minimal *rest.Config with just Host set; NewClusterTestProvider only checks
// the error, never the value.
func fakeInCluster() (*rest.Config, error) {
	return &rest.Config{Host: "https://kubernetes.default.svc"}, nil
}

// fakeNotInCluster makes inClusterConfigFn return rest.ErrNotInCluster — the
// canonical "we're running outside K8s" signal.
func fakeNotInCluster() (*rest.Config, error) {
	return nil, rest.ErrNotInCluster
}

// --- NewClusterTestProvider (canonical V125-1-11.6+) ---------------------

// Case 1: explicit Type=="argocd" routes to NewArgoCDProviderFromConfig.
// We can't fully construct one without an in-cluster config OR a kubeconfig,
// so the test passes as long as we DIDN'T fall through to the "unknown
// provider type" error — that's the regression we're guarding.
func TestNewClusterTestProvider_ArgoCDExplicit(t *testing.T) {
	_, err := NewClusterTestProvider(ClusterTestProviderConfig{Type: "argocd"})
	if err == nil {
		// Some test environments have a usable ~/.kube/config; success is
		// also acceptable, it just means we routed correctly AND constructed
		// the client. That's what we want.
		return
	}
	if strings.Contains(err.Error(), "unknown cluster-test provider type") {
		t.Errorf("factory should have routed to ArgoCDProvider for explicit 'argocd', got: %v", err)
	}
}

// Case 2: Type=="" + in-cluster simulated true → returns *ArgoCDProvider, no
// error. We use the test indirection to simulate in-cluster without touching
// KUBERNETES_SERVICE_HOST (which would race other tests in the binary).
func TestNewClusterTestProvider_AutoDefaultInCluster(t *testing.T) {
	withInClusterFn(t, fakeInCluster)
	prov, err := NewClusterTestProvider(ClusterTestProviderConfig{Type: ""})
	if err != nil {
		t.Fatalf("expected auto-default to succeed in-cluster, got error: %v", err)
	}
	if _, ok := prov.(*ArgoCDProvider); !ok {
		t.Errorf("expected *ArgoCDProvider, got %T", prov)
	}
}

// Case 3: Type=="" + in-cluster simulated false → returns the legacy
// "no secrets provider configured" error, unchanged.
func TestNewClusterTestProvider_AutoDefaultNotInCluster(t *testing.T) {
	withInClusterFn(t, fakeNotInCluster)
	_, err := NewClusterTestProvider(ClusterTestProviderConfig{Type: ""})
	if err == nil {
		t.Fatal("expected error when not in-cluster + no provider configured, got nil")
	}
	if !strings.Contains(err.Error(), "configure provider in Settings or via API") {
		t.Errorf("expected legacy 'no provider configured' error, got: %v", err)
	}
}

// Case 3b: Type=="" + in-cluster probe fails for a non-NotInCluster reason →
// surface the underlying probe error so operators can fix bad config instead
// of silently getting "no provider configured."
func TestNewClusterTestProvider_AutoDefaultProbeFails(t *testing.T) {
	probeErr := errors.New("malformed in-cluster config: bad SA token")
	withInClusterFn(t, func() (*rest.Config, error) {
		return nil, probeErr
	})
	_, err := NewClusterTestProvider(ClusterTestProviderConfig{Type: ""})
	if err == nil {
		t.Fatal("expected error when in-cluster probe fails, got nil")
	}
	if !strings.Contains(err.Error(), "auto-default provider probe failed") {
		t.Errorf("expected probe-failure error, got: %v", err)
	}
	if !errors.Is(err, probeErr) {
		t.Errorf("expected wrapped probe error to be unwrapped via errors.Is, got: %v", err)
	}
}

// Case (V2-cleanup-53.1): the aws-sm cluster-creds arm is RESTORED. The
// factory must return the SM-backed provider type — construction succeeds
// without real AWS credentials (the SDK defers credential resolution to the
// first API call), so this assertion is deterministic in CI.
func TestNewClusterTestProvider_AWSSMArm_ReturnsSMProvider(t *testing.T) {
	for _, typ := range []string{"aws-sm", "aws-secrets-manager"} {
		prov, err := NewClusterTestProvider(ClusterTestProviderConfig{
			Type:    typ,
			Region:  "eu-west-1",
			Prefix:  "clusters/",
			RoleARN: "arn:aws:iam::000000000000:role/sharko-test",
		})
		if err != nil {
			t.Fatalf("expected aws-sm arm to construct for type %q, got error: %v", typ, err)
		}
		smProv, ok := prov.(*AWSSecretsManagerProvider)
		if !ok {
			t.Fatalf("expected *AWSSecretsManagerProvider for type %q, got %T", typ, prov)
		}
		if smProv.prefix != "clusters/" {
			t.Errorf("expected configured prefix to reach the provider, got %q", smProv.prefix)
		}
		if smProv.roleARN != "arn:aws:iam::000000000000:role/sharko-test" {
			t.Errorf("expected configured roleARN to reach the provider, got %q", smProv.roleARN)
		}
	}
}

// Case (V2-cleanup-53.1): the k8s-secrets cluster-creds arm is RESTORED. The
// factory must ROUTE to the K8s constructor — full construction needs an
// in-cluster config or ~/.kube/config, which CI may not have, so on error we
// assert only that we did not fall through to "unknown cluster-test provider
// type"; on success we assert the concrete type.
func TestNewClusterTestProvider_K8sSecretsArm_Routes(t *testing.T) {
	for _, typ := range []string{"k8s-secrets", "kubernetes"} {
		prov, err := NewClusterTestProvider(ClusterTestProviderConfig{Type: typ, Namespace: "sharko"})
		if err != nil {
			if strings.Contains(err.Error(), "unknown cluster-test provider type") {
				t.Errorf("factory should have routed to K8s provider for %q, got: %v", typ, err)
			}
			continue
		}
		if _, ok := prov.(*KubernetesSecretProvider); !ok {
			t.Errorf("expected *KubernetesSecretProvider for type %q, got %T", typ, prov)
		}
	}
}

// Case: gcp/azure cluster-creds arms STAY retired (V2-cleanup-53.1 scope
// guard) — only aws-sm + k8s-secrets were restored. Addon-secret consumers
// of gcp/azure remain reachable via NewAddonSecretProvider (stubs).
func TestNewClusterTestProvider_GCPAzureStayRetired(t *testing.T) {
	for _, typ := range []string{"gcp", "gcp-sm", "google-secret-manager", "azure", "azure-kv", "azure-key-vault"} {
		_, err := NewClusterTestProvider(ClusterTestProviderConfig{Type: typ})
		if err == nil {
			t.Errorf("expected error for retired cluster-creds type %q, got nil", typ)
			continue
		}
		if !strings.Contains(err.Error(), "unknown cluster-test provider type") {
			t.Errorf("expected 'unknown cluster-test provider type' error for %q, got: %v", typ, err)
		}
	}
}

func TestNewClusterTestProvider_UnknownType(t *testing.T) {
	_, err := NewClusterTestProvider(ClusterTestProviderConfig{Type: "vault"})
	if err == nil {
		t.Fatal("expected error for unknown type, got nil")
	}
	if !strings.Contains(err.Error(), "unknown cluster-test provider type") {
		t.Errorf("expected error to mention unknown cluster-test provider type, got: %v", err)
	}
	// Verify the help text advertises the full supported option set
	// (argocd + the V2-cleanup-53.1 restored arms).
	for _, want := range []string{"argocd", "aws-sm", "k8s-secrets"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected unknown-type error to advertise the %q option, got: %v", want, err)
		}
	}
}

// --- ClusterTestConfigFromConnection (shared boot/hot-reload mapper) -------

// The mapper is the single source of truth for connection→cluster-test config
// fan-through (V2-cleanup-53.1). These tests pin each branch, most importantly
// the V125-1-10.8 cross-contamination guard: the connection-level namespace
// must NEVER land in ArgoCDNamespace.
func TestClusterTestConfigFromConnection(t *testing.T) {
	cases := []struct {
		name string
		typ  string
		want ClusterTestProviderConfig
	}{
		{
			name: "argocd ignores namespace entirely (V125-1-10.8 guard)",
			typ:  "argocd",
			want: ClusterTestProviderConfig{Type: "argocd", ArgoCDNamespace: ""},
		},
		{
			name: "aws-sm carries region/prefix/roleARN, never namespaces",
			typ:  "aws-sm",
			want: ClusterTestProviderConfig{Type: "aws-sm", Region: "eu-west-1", Prefix: "clusters/", RoleARN: "arn:aws:iam::000000000000:role/x"},
		},
		{
			name: "aws-secrets-manager alias behaves like aws-sm",
			typ:  "aws-secrets-manager",
			want: ClusterTestProviderConfig{Type: "aws-secrets-manager", Region: "eu-west-1", Prefix: "clusters/", RoleARN: "arn:aws:iam::000000000000:role/x"},
		},
		{
			name: "k8s-secrets carries namespace into the distinct Namespace field",
			typ:  "k8s-secrets",
			want: ClusterTestProviderConfig{Type: "k8s-secrets", Namespace: "sharko"},
		},
		{
			name: "kubernetes alias behaves like k8s-secrets",
			typ:  "kubernetes",
			want: ClusterTestProviderConfig{Type: "kubernetes", Namespace: "sharko"},
		},
		{
			name: "empty type returns zero config (auto-default decides)",
			typ:  "",
			want: ClusterTestProviderConfig{},
		},
		{
			name: "retired gcp-sm returns zero config (auto-default, pre-53.1 behavior)",
			typ:  "gcp-sm",
			want: ClusterTestProviderConfig{},
		},
		{
			name: "retired azure-kv returns zero config (auto-default, pre-53.1 behavior)",
			typ:  "azure-kv",
			want: ClusterTestProviderConfig{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClusterTestConfigFromConnection(tc.typ, "eu-west-1", "clusters/", "sharko", "arn:aws:iam::000000000000:role/x")
			if got != tc.want {
				t.Errorf("ClusterTestConfigFromConnection(%q, ...) = %+v, want %+v", tc.typ, got, tc.want)
			}
			if got.ArgoCDNamespace != "" {
				t.Errorf("ArgoCDNamespace must NEVER be populated from the connection namespace (V125-1-10.8 guard), got %q", got.ArgoCDNamespace)
			}
		})
	}
}

// --- NewAddonSecretProvider (canonical) regression sweep ------------------

// NewAddonSecretProvider rejects argocd type — argocd is not a SecretProvider
// backend (it serves cluster credentials, not addon-secret VALUES).
func TestNewAddonSecretProvider_ArgoCDRefused(t *testing.T) {
	_, err := NewAddonSecretProvider(AddonSecretProviderConfig{Type: "argocd"})
	if err == nil {
		t.Fatal("expected error for argocd type on NewAddonSecretProvider, got nil")
	}
	if !strings.Contains(err.Error(), "cluster-credentials-only") {
		t.Errorf("expected explicit cluster-credentials-only refusal, got: %v", err)
	}
}

// NewAddonSecretProvider returns the legacy "no provider configured" error
// when Type is empty.
func TestNewAddonSecretProvider_EmptyType(t *testing.T) {
	_, err := NewAddonSecretProvider(AddonSecretProviderConfig{Type: ""})
	if err == nil {
		t.Fatal("expected error for empty type on NewAddonSecretProvider, got nil")
	}
	if !strings.Contains(err.Error(), "no secrets provider configured") {
		t.Errorf("expected 'no secrets provider configured' error, got: %v", err)
	}
}

// NewAddonSecretProvider returns "unknown provider type" for an unmapped type.
func TestNewAddonSecretProvider_UnknownType(t *testing.T) {
	_, err := NewAddonSecretProvider(AddonSecretProviderConfig{Type: "not-a-real-backend"})
	if err == nil {
		t.Fatal("expected error for unknown type, got nil")
	}
	if !strings.Contains(err.Error(), "unknown provider type") {
		t.Errorf("expected 'unknown provider type' error, got: %v", err)
	}
}

// NewAddonSecretProvider routes aws-sm + alias to the AWS factory. We assert
// only that we didn't fall through to "unknown provider type" — actually
// loading AWS creds may fail in CI without IAM/IRSA.
func TestNewAddonSecretProvider_AWSAliases(t *testing.T) {
	for _, typ := range []string{"aws-sm", "aws-secrets-manager"} {
		_, err := NewAddonSecretProvider(AddonSecretProviderConfig{Type: typ, Region: "us-east-1"})
		if err == nil {
			continue
		}
		if strings.Contains(err.Error(), "unknown provider type") {
			t.Errorf("factory should have routed to AWS provider for %q alias, got: %v", typ, err)
		}
	}
}

// NewAddonSecretProvider routes k8s-secrets + alias to the K8s factory. Same
// "didn't fall through to unknown" guard — fully constructing the client
// requires an in-cluster config or ~/.kube/config.
func TestNewAddonSecretProvider_K8sAliases(t *testing.T) {
	for _, typ := range []string{"k8s-secrets", "kubernetes"} {
		_, err := NewAddonSecretProvider(AddonSecretProviderConfig{Type: typ, Namespace: "sharko"})
		if err == nil {
			continue
		}
		if strings.Contains(err.Error(), "unknown provider type") {
			t.Errorf("factory should have routed to K8s provider for %q alias, got: %v", typ, err)
		}
	}
}

// NewAddonSecretProvider routes GCP aliases to the (stub) GCP factory. The
// stub always returns "not yet implemented" — we assert that error surfaces
// instead of "unknown provider type", which would mean we mis-routed.
func TestNewAddonSecretProvider_GCPAliases(t *testing.T) {
	for _, typ := range []string{"gcp", "gcp-sm", "google-secret-manager"} {
		_, err := NewAddonSecretProvider(AddonSecretProviderConfig{Type: typ})
		if err == nil {
			t.Errorf("expected stub error for GCP type %q, got nil", typ)
			continue
		}
		if strings.Contains(err.Error(), "unknown provider type") {
			t.Errorf("factory should have routed to GCP provider for %q alias, got: %v", typ, err)
		}
		if !strings.Contains(err.Error(), "not yet implemented") {
			t.Errorf("expected stub 'not yet implemented' error for %q, got: %v", typ, err)
		}
	}
}

// NewAddonSecretProvider routes Azure aliases to the (stub) Azure factory.
func TestNewAddonSecretProvider_AzureAliases(t *testing.T) {
	for _, typ := range []string{"azure", "azure-kv", "azure-key-vault"} {
		_, err := NewAddonSecretProvider(AddonSecretProviderConfig{Type: typ})
		if err == nil {
			t.Errorf("expected stub error for Azure type %q, got nil", typ)
			continue
		}
		if strings.Contains(err.Error(), "unknown provider type") {
			t.Errorf("factory should have routed to Azure provider for %q alias, got: %v", typ, err)
		}
		if !strings.Contains(err.Error(), "not yet implemented") {
			t.Errorf("expected stub 'not yet implemented' error for %q, got: %v", typ, err)
		}
	}
}

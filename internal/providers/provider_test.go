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

// Case: NewClusterTestProvider rejects legacy aws-sm cluster-creds type. After
// V125-1-11.6 the cluster-test dispatcher accepts ONLY argocd + "" — the
// legacy aws-sm/k8s-secrets/gcp-sm/azure-kv arms (deprecated since V125-1-10.2
// auto-default) are retired one cycle earlier than the provider.go doc-comment
// promise. Addon-secret consumers of those backends remain functional via
// NewAddonSecretProvider — only the cluster-creds usage is killed.
func TestNewClusterTestProvider_RejectsLegacyClusterCredsTypes(t *testing.T) {
	for _, typ := range []string{"aws-sm", "aws-secrets-manager", "k8s-secrets", "kubernetes", "gcp", "gcp-sm", "azure", "azure-kv"} {
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
	// Verify the help text advertises the supported options.
	if !strings.Contains(err.Error(), "argocd") {
		t.Errorf("expected unknown-type error to advertise the 'argocd' option, got: %v", err)
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

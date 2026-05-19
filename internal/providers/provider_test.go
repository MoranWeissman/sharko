package providers

import (
	"errors"
	"strings"
	"testing"

	"k8s.io/client-go/rest"
)

// withInClusterFn temporarily replaces the in-cluster probe used by New() and
// arranges automatic restoration via t.Cleanup.
func withInClusterFn(t *testing.T, fn func() (*rest.Config, error)) {
	t.Helper()
	orig := inClusterConfigFn
	inClusterConfigFn = fn
	t.Cleanup(func() { inClusterConfigFn = orig })
}

// fakeInCluster makes inClusterConfigFn return (non-nil, nil) — i.e. "we're
// running inside a K8s pod with a service-account token mounted." We return a
// minimal *rest.Config with just Host set; New() only checks the error, never
// the value.
func fakeInCluster() (*rest.Config, error) {
	return &rest.Config{Host: "https://kubernetes.default.svc"}, nil
}

// fakeNotInCluster makes inClusterConfigFn return rest.ErrNotInCluster — the
// canonical "we're running outside K8s" signal.
func fakeNotInCluster() (*rest.Config, error) {
	return nil, rest.ErrNotInCluster
}

// --- Cases (Story V125-1-10.2 acceptance criteria) -------------------------

// Case 1: explicit Type=="argocd" routes to NewArgoCDProvider. We can't fully
// construct one without an in-cluster config OR a kubeconfig, so the test
// passes as long as we DIDN'T fall through to the "unknown provider type"
// error — that's the regression we're guarding.
func TestNew_ArgoCDExplicit(t *testing.T) {
	_, err := New(Config{Type: "argocd"})
	if err == nil {
		// Some test environments have a usable ~/.kube/config; success is also
		// acceptable, it just means we routed correctly AND constructed the
		// client. That's what we want.
		return
	}
	if strings.Contains(err.Error(), "unknown provider type") {
		t.Errorf("factory should have routed to ArgoCDProvider for explicit 'argocd', got: %v", err)
	}
}

// Case 2: Type=="" + in-cluster simulated true → returns *ArgoCDProvider, no
// error. We use the test indirection to simulate in-cluster without touching
// KUBERNETES_SERVICE_HOST (which would race other tests in the binary).
func TestNew_AutoDefaultInCluster(t *testing.T) {
	withInClusterFn(t, fakeInCluster)
	prov, err := New(Config{Type: ""})
	if err != nil {
		t.Fatalf("expected auto-default to succeed in-cluster, got error: %v", err)
	}
	if _, ok := prov.(*ArgoCDProvider); !ok {
		t.Errorf("expected *ArgoCDProvider, got %T", prov)
	}
}

// Case 3: Type=="" + in-cluster simulated false → returns the legacy
// "no secrets provider configured" error, unchanged.
func TestNew_AutoDefaultNotInCluster(t *testing.T) {
	withInClusterFn(t, fakeNotInCluster)
	_, err := New(Config{Type: ""})
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
func TestNew_AutoDefaultProbeFails(t *testing.T) {
	probeErr := errors.New("malformed in-cluster config: bad SA token")
	withInClusterFn(t, func() (*rest.Config, error) {
		return nil, probeErr
	})
	_, err := New(Config{Type: ""})
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

// Case 4: NewSecretProvider(Type=="argocd") returns the explicit
// "cluster-credentials-only" refusal — addon secret values must come from a
// real secrets backend (vault/aws-sm/k8s-secrets/gcp-sm/azure-kv).
func TestNewSecretProvider_ArgoCDRefused(t *testing.T) {
	_, err := NewSecretProvider(Config{Type: "argocd"})
	if err == nil {
		t.Fatal("expected error for argocd type on NewSecretProvider, got nil")
	}
	if !strings.Contains(err.Error(), "cluster-credentials-only") {
		t.Errorf("expected explicit cluster-credentials-only refusal, got: %v", err)
	}
}

// NewSecretProvider for empty Type still returns the legacy error (auto-default
// only applies to ClusterCredentialsProvider via New()).
func TestNewSecretProvider_EmptyType(t *testing.T) {
	_, err := NewSecretProvider(Config{Type: ""})
	if err == nil {
		t.Fatal("expected error for empty type on NewSecretProvider, got nil")
	}
	if !strings.Contains(err.Error(), "no secrets provider configured") {
		t.Errorf("expected 'no secrets provider configured' error, got: %v", err)
	}
}

// --- Existing-types regression sweep ---------------------------------------
//
// These are smoke-routing tests: they confirm the factory dispatches to the
// right constructor. The constructors themselves may fail for environment
// reasons (no AWS creds, no kubeconfig), and that's fine — we only assert
// that we didn't fall through to "unknown provider type."

// Case 5 (Type=="" auto-default failing case is covered by
// TestNew_AutoDefaultNotInCluster above; explicit empty-type with
// in-cluster mocked false IS the legacy behavior.)
func TestNew_EmptyType_LegacyBehavior(t *testing.T) {
	withInClusterFn(t, fakeNotInCluster)
	_, err := New(Config{Type: ""})
	if err == nil {
		t.Fatal("expected error for empty type, got nil")
	}
	if !strings.Contains(err.Error(), "configure provider in Settings or via API") {
		t.Errorf("expected error to mention configuration instructions, got: %v", err)
	}
}

func TestNew_UnknownType(t *testing.T) {
	_, err := New(Config{Type: "vault"})
	if err == nil {
		t.Fatal("expected error for unknown type, got nil")
	}
	if !strings.Contains(err.Error(), "unknown provider type") {
		t.Errorf("expected error to mention unknown provider type, got: %v", err)
	}
	// Verify the new "argocd" alias is advertised in the help text.
	if !strings.Contains(err.Error(), "argocd") {
		t.Errorf("expected unknown-type error to advertise the new 'argocd' option, got: %v", err)
	}
}

func TestNew_K8sSecrets(t *testing.T) {
	_, err := New(Config{Type: "k8s-secrets"})
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), "unknown provider type") {
		t.Errorf("factory should have routed to K8s provider, got: %v", err)
	}
}

func TestNew_KubernetesAlias(t *testing.T) {
	_, err := New(Config{Type: "kubernetes"})
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), "unknown provider type") {
		t.Errorf("factory should have routed to K8s provider for 'kubernetes' alias, got: %v", err)
	}
}

func TestNew_AWSSM(t *testing.T) {
	_, err := New(Config{Type: "aws-sm", Region: "us-east-1"})
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), "unknown provider type") {
		t.Errorf("factory should have routed to AWS provider, got: %v", err)
	}
}

func TestNew_AWSSecretsManagerAlias(t *testing.T) {
	_, err := New(Config{Type: "aws-secrets-manager", Region: "us-east-1"})
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), "unknown provider type") {
		t.Errorf("factory should have routed to AWS provider for alias, got: %v", err)
	}
}

func TestNew_GCPAliases(t *testing.T) {
	for _, typ := range []string{"gcp", "gcp-sm", "google-secret-manager"} {
		_, err := New(Config{Type: typ})
		if err == nil {
			continue
		}
		if strings.Contains(err.Error(), "unknown provider type") {
			t.Errorf("factory should have routed to GCP provider for %q alias, got: %v", typ, err)
		}
	}
}

func TestNew_AzureAliases(t *testing.T) {
	for _, typ := range []string{"azure", "azure-kv", "azure-key-vault"} {
		_, err := New(Config{Type: typ})
		if err == nil {
			continue
		}
		if strings.Contains(err.Error(), "unknown provider type") {
			t.Errorf("factory should have routed to Azure provider for %q alias, got: %v", typ, err)
		}
	}
}

// --- V125-1-11.3: NewAddonSecretProvider (canonical) regression sweep ------
//
// These tests exercise the new AddonSecretProviderConfig-consuming dispatcher
// directly (the compat-shim NewSecretProvider(Config) translates to this and
// is covered by the older tests above).

// NewAddonSecretProvider rejects argocd type the same way the legacy
// NewSecretProvider does — argocd is not a SecretProvider backend.
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
// when Type is empty — matches the existing NewSecretProvider contract.
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

// addonSecretConfigFromLegacy copies all five fields verbatim from the old
// Config to the new AddonSecretProviderConfig. The compat-shim chain depends
// on this being a pure no-op translation — if a future refactor introduces
// any field transformation, this test catches it.
func TestAddonSecretConfigFromLegacy_IdentityTranslation(t *testing.T) {
	legacy := Config{
		Type:      "aws-sm",
		Region:    "us-west-2",
		Prefix:    "clusters/",
		Namespace: "sharko",
		RoleARN:   "arn:aws:iam::123456789012:role/EKSReadRole",
	}
	got := addonSecretConfigFromLegacy(legacy)
	want := AddonSecretProviderConfig{
		Type:      "aws-sm",
		Region:    "us-west-2",
		Prefix:    "clusters/",
		Namespace: "sharko",
		RoleARN:   "arn:aws:iam::123456789012:role/EKSReadRole",
	}
	if got != want {
		t.Errorf("addonSecretConfigFromLegacy mistranslated: got %+v, want %+v", got, want)
	}
}

// NewSecretProvider (compat shim) and NewAddonSecretProvider must produce the
// same outcome for equivalent inputs — the shim is a pure bridge. We pick a
// rejection case (argocd) because it has a deterministic outcome that doesn't
// depend on the runtime environment.
func TestNewSecretProvider_CompatShimMatchesCanonical(t *testing.T) {
	_, legacyErr := NewSecretProvider(Config{Type: "argocd"})
	_, canonicalErr := NewAddonSecretProvider(AddonSecretProviderConfig{Type: "argocd"})
	if (legacyErr == nil) != (canonicalErr == nil) {
		t.Fatalf("compat shim disagrees with canonical: legacy=%v canonical=%v", legacyErr, canonicalErr)
	}
	if legacyErr != nil && canonicalErr != nil {
		if legacyErr.Error() != canonicalErr.Error() {
			t.Errorf("compat shim and canonical produced different error messages:\n  legacy:    %v\n  canonical: %v", legacyErr, canonicalErr)
		}
	}
}

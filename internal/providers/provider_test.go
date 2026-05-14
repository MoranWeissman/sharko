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

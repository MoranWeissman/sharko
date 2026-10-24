package providers

import (
	"errors"
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
)

// EP3-DRYRUN-FIXES join, design call 3: Test and Diagnose read a cluster's
// connection through this provider, so it must find the ArgoCD cluster
// Secret the same way every other path does (argosecrets.GetClusterSecret).

const sharedLookupConfig = `{"bearerToken":"tok","tlsClientConfig":{"insecure":true}}`

// A Secret made by `argocd cluster add` is named cluster-<host>-<hash>; it is
// found by the cluster name ArgoCD shows (the `name` inside it).
func TestArgoCD_FindsArgocdAddNamedSecretByShownName(t *testing.T) {
	client := fake.NewSimpleClientset(argoCDSecret(
		"cluster-10.0.0.5-3621829410", "legacy-1", "https://10.0.0.5:6443", sharedLookupConfig))
	provider := newArgoCDProviderWithClient(client, "argocd")

	kc, err := provider.GetCredentials("legacy-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kc.Server != "https://10.0.0.5:6443" {
		t.Fatalf("Server = %q, want the legacy-1 connection", kc.Server)
	}
}

// Two Secrets that both say they are "dup" are a clear error, never a
// guess at one of them.
func TestArgoCD_TwoSecretsWithSameClusterName_IsAnError(t *testing.T) {
	client := fake.NewSimpleClientset(
		argoCDSecret("cluster-10.0.0.5-1", "dup", "https://10.0.0.5:6443", sharedLookupConfig),
		argoCDSecret("cluster-10.0.0.6-2", "dup", "https://10.0.0.6:6443", sharedLookupConfig),
	)
	provider := newArgoCDProviderWithClient(client, "argocd")

	_, err := provider.GetCredentials("dup")
	if err == nil {
		t.Fatal("want an error for two Secrets called dup, got credentials for one of them")
	}
	if !errors.Is(err, argosecrets.ErrAmbiguousClusterSecret) {
		t.Fatalf("want ErrAmbiguousClusterSecret, got: %v", err)
	}
}

// Passing a server URL still works (the old fallback is kept).
func TestArgoCD_ServerURLFallbackStillWorks(t *testing.T) {
	client := fake.NewSimpleClientset(argoCDSecret(
		"cluster-10.0.0.5-3621829410", "legacy-1", "https://10.0.0.5:6443", sharedLookupConfig))
	provider := newArgoCDProviderWithClient(client, "argocd")

	kc, err := provider.GetCredentials("https://10.0.0.5:6443")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kc.Server != "https://10.0.0.5:6443" {
		t.Fatalf("Server = %q", kc.Server)
	}
}

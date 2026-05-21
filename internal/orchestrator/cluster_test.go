package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/providers"
)

// V125-1-8.3 contract change: RefreshClusterCredentials no longer calls
// o.argocd.RegisterCluster directly. The reconciler owns Secret writes;
// refresh now probes the credentials provider (fail-fast UX) and nudges
// the reconciler trigger seam. These tests pin the new behaviour: probe
// succeeds → trigger fires + no direct ArgoCD API write; probe fails →
// error propagates + no trigger fires.

func TestRefreshClusterCredentials_Success(t *testing.T) {
	argocd := newMockArgocd()
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"prod-eu": {
				Server: "https://k8s-refreshed.example.com:6443",
				CAData: []byte("new-ca"),
				Token:  "new-token",
			},
		},
	}

	orch := New(nil, creds, argocd, newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)
	triggers := 0
	orch.SetReconcilerTrigger(func() { triggers++ })

	err := orch.RefreshClusterCredentials(context.Background(), "prod-eu", "https://k8s.example.com:6443")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// V125-1-8.3: NO direct ArgoCD register API call — the reconciler does that.
	if _, ok := argocd.registeredClusters["prod-eu"]; ok {
		t.Error("V125-1-8.3 contract violated: refresh must NOT call argocd.RegisterCluster directly (reconciler owns it)")
	}
	// Trigger MUST fire so reconciler picks up the new credentials immediately.
	if triggers != 1 {
		t.Errorf("expected reconciler trigger to fire exactly once, got %d", triggers)
	}
}

func TestRefreshClusterCredentials_CredProviderError(t *testing.T) {
	argocd := newMockArgocd()
	creds := &mockCredProvider{
		err: errors.New("provider unavailable"),
	}

	orch := New(nil, creds, argocd, newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)
	triggers := 0
	orch.SetReconcilerTrigger(func() { triggers++ })

	err := orch.RefreshClusterCredentials(context.Background(), "prod-eu", "https://k8s.example.com:6443")
	if err == nil {
		t.Fatal("expected error from credentials provider")
	}
	if !strings.Contains(err.Error(), "fetching fresh credentials") {
		t.Errorf("unexpected error message: %v", err)
	}
	// Probe failed → trigger MUST NOT fire (otherwise reconciler is woken to
	// fetch the same broken creds for nothing).
	if triggers != 0 {
		t.Errorf("trigger must not fire when probe fails, got %d invocations", triggers)
	}
}

func TestRefreshClusterCredentials_NoCredProvider(t *testing.T) {
	// V125-1-8.3: with a kubeconfig-only deployment (nil credProvider) the
	// refresh has nothing to probe; it still fires the trigger so the
	// reconciler can opportunistically re-reconcile.
	argocd := newMockArgocd()
	orch := New(nil, nil, argocd, newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)
	triggers := 0
	orch.SetReconcilerTrigger(func() { triggers++ })

	err := orch.RefreshClusterCredentials(context.Background(), "kind-sharko", "https://127.0.0.1:60123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if triggers != 1 {
		t.Errorf("expected reconciler trigger to fire exactly once, got %d", triggers)
	}
}

func TestParseAddonsCatalog_Valid(t *testing.T) {
	data := []byte(`applicationsets:
  - name: cert-manager
    chart: cert-manager
    repoURL: https://charts.jetstack.io
    version: 1.14.0
    namespace: cert-manager
  - name: metrics-server
    chart: metrics-server
    repoURL: https://kubernetes-sigs.github.io/metrics-server
    version: 0.6.0
    namespace: kube-system
`)

	entries, err := parseAddonsCatalog(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Name != "cert-manager" {
		t.Errorf("expected cert-manager, got %q", entries[0].Name)
	}
	if entries[1].Version != "0.6.0" {
		t.Errorf("expected version 0.6.0 for metrics-server, got %q", entries[1].Version)
	}
}

func TestParseAddonsCatalog_Empty(t *testing.T) {
	data := []byte(`applicationsets: []`)
	entries, err := parseAddonsCatalog(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestParseAddonsCatalog_InvalidYAML(t *testing.T) {
	data := []byte(`{invalid yaml: [`)
	_, err := parseAddonsCatalog(data)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/providers"
)

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
	err := orch.RefreshClusterCredentials(context.Background(), "prod-eu", "https://k8s.example.com:6443")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// ArgoCD should have the cluster re-registered.
	if _, ok := argocd.registeredClusters["prod-eu"]; !ok {
		t.Error("expected prod-eu to be re-registered in ArgoCD")
	}
}

func TestRefreshClusterCredentials_CredProviderError(t *testing.T) {
	argocd := newMockArgocd()
	creds := &mockCredProvider{
		err: errors.New("provider unavailable"),
	}

	orch := New(nil, creds, argocd, newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)
	err := orch.RefreshClusterCredentials(context.Background(), "prod-eu", "https://k8s.example.com:6443")
	if err == nil {
		t.Fatal("expected error from credentials provider")
	}
	if !strings.Contains(err.Error(), "fetching fresh credentials") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRefreshClusterCredentials_ArgoCDError(t *testing.T) {
	argocd := newMockArgocd()
	argocd.registerErr = errors.New("argocd unavailable")

	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"prod-eu": {
				Server: "https://k8s.example.com:6443",
				CAData: []byte("ca"),
				Token:  "tok",
			},
		},
	}

	orch := New(nil, creds, argocd, newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)
	err := orch.RefreshClusterCredentials(context.Background(), "prod-eu", "https://k8s.example.com:6443")
	if err == nil {
		t.Fatal("expected error from ArgoCD re-registration")
	}
	if !strings.Contains(err.Error(), "re-registering cluster") {
		t.Errorf("unexpected error message: %v", err)
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

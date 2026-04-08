package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

func TestConfigureAddon_NoName(t *testing.T) {
	orch := New(nil, defaultCreds(), newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)
	_, err := orch.ConfigureAddon(context.Background(), ConfigureAddonRequest{})
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestConfigureAddon_NoUpdatableFields(t *testing.T) {
	git := newMockGitProvider()
	git.files["configuration/addons-catalog.yaml"] = []byte(`applicationsets:
  - name: cert-manager
    chart: cert-manager
    repoURL: https://charts.jetstack.io
    version: 1.14.0
    namespace: cert-manager
`)
	orch := New(nil, defaultCreds(), newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)
	_, err := orch.ConfigureAddon(context.Background(), ConfigureAddonRequest{Name: "cert-manager"})
	if err == nil {
		t.Fatal("expected error for no updatable fields")
	}
	if !strings.Contains(err.Error(), "no updatable fields") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConfigureAddon_UpdateSelfHeal(t *testing.T) {
	git := newMockGitProvider()
	argocd := newMockArgocd()

	git.files["configuration/addons-catalog.yaml"] = []byte(`applicationsets:
  - name: cert-manager
    chart: cert-manager
    repoURL: https://charts.jetstack.io
    version: 1.14.0
    namespace: cert-manager
`)

	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	selfHeal := true
	result, err := orch.ConfigureAddon(context.Background(), ConfigureAddonRequest{
		Name:     "cert-manager",
		SelfHeal: &selfHeal,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	updated := string(git.files["configuration/addons-catalog.yaml"])
	if !strings.Contains(updated, "selfHeal: true") {
		t.Errorf("expected selfHeal: true in catalog, got:\n%s", updated)
	}
}

func TestConfigureAddon_AdditionalSources(t *testing.T) {
	git := newMockGitProvider()
	git.files["configuration/addons-catalog.yaml"] = []byte(`applicationsets:
  - name: cert-manager
    chart: cert-manager
    repoURL: https://charts.jetstack.io
    version: 1.14.0
    namespace: cert-manager
`)
	orch := New(nil, defaultCreds(), newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)
	result, err := orch.ConfigureAddon(context.Background(), ConfigureAddonRequest{
		Name:              "cert-manager",
		AdditionalSources: []models.AddonSource{{RepoURL: "https://example.com", Chart: "extra-chart", Version: "1.0.0"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	updated := string(git.files["configuration/addons-catalog.yaml"])
	if !strings.Contains(updated, "https://example.com") {
		t.Errorf("expected additional source repoURL in catalog, got:\n%s", updated)
	}
}

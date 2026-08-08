package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
)

// updateAddonsV4Fixture is a v4 repo with "prod-eu" registered and the
// three addons v4TestApprovedCatalog approves — same fixture shape
// newV4TestOrchestrator uses for EnableAddonV4/DisableAddonV4, so the
// label-patch door and the sharpened-pipeline doors agree on what "in the
// catalog" means.
func updateAddonsV4Fixture(t *testing.T) *mockGitProvider {
	t.Helper()
	git := adoptV4Git()
	git.files[V4ManagedClustersPath] = []byte("clusters:\n  - name: prod-eu\n")
	git.files[config.AddonCatalogPath] = v4TestApprovedCatalog(t)
	return git
}

func TestUpdateClusterAddons_V4_MultiAddonOnOff_OnePR(t *testing.T) {
	git := updateAddonsV4Fixture(t)
	orch := New(nil, nil, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.UpdateClusterAddons(context.Background(), "prod-eu", "https://prod-eu.example.com", "",
		map[string]bool{"cert-manager": true, "metrics-server": false}, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s (error: %s)", result.Status, result.Error)
	}

	// One PR, one branch — the whole request landed as a single commit.
	if len(git.branches) != 1 {
		t.Errorf("expected exactly 1 branch (one PR for the whole request), got %d: %v", len(git.branches), git.branches)
	}
	if len(git.prs) != 1 {
		t.Errorf("expected exactly 1 PR, got %d", len(git.prs))
	}

	addonsBytes, ok := git.files["cluster-addons/prod-eu.yaml"]
	if !ok {
		t.Fatal("expected cluster-addons/prod-eu.yaml to be written")
	}
	spec, err := models.LoadClusterAddons(addonsBytes)
	if err != nil {
		t.Fatalf("assignment file does not parse: %v", err)
	}
	if !spec.Addons["cert-manager"].Enabled {
		t.Error("expected cert-manager enabled=true")
	}
	if spec.Addons["metrics-server"].Enabled {
		t.Error("expected metrics-server enabled=false")
	}
	// This door never touches ArgoCD directly on v4 — PR-only, the
	// reconciler converges from git.
	if _, ok := git.files["configuration/managed-clusters.yaml"]; ok {
		t.Error("v3 registry must not be written")
	}
}

func TestUpdateClusterAddons_V4_KeepsExistingAddonsNotMentioned(t *testing.T) {
	git := updateAddonsV4Fixture(t)
	existing, err := models.SaveClusterAddons(models.ClusterAddonsSpec{
		Cluster: "prod-eu",
		Addons:  map[string]models.ClusterAddonsAddon{"external-secrets": {Enabled: true, Version: "0.9.19"}},
	})
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	git.files["cluster-addons/prod-eu.yaml"] = existing

	orch := New(nil, nil, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)
	_, err = orch.UpdateClusterAddons(context.Background(), "prod-eu", "https://prod-eu.example.com", "",
		map[string]bool{"cert-manager": true}, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spec, err := models.LoadClusterAddons(git.files["cluster-addons/prod-eu.yaml"])
	if err != nil {
		t.Fatalf("assignment file does not parse: %v", err)
	}
	if !spec.Addons["cert-manager"].Enabled {
		t.Error("expected cert-manager enabled=true")
	}
	if !spec.Addons["external-secrets"].Enabled || spec.Addons["external-secrets"].Version != "0.9.19" {
		t.Errorf("expected external-secrets to survive untouched, got %+v", spec.Addons["external-secrets"])
	}
}

func TestUpdateClusterAddons_V4_RefusesAddonNotInCatalog(t *testing.T) {
	git := updateAddonsV4Fixture(t)
	orch := New(nil, nil, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.UpdateClusterAddons(context.Background(), "prod-eu", "https://prod-eu.example.com", "",
		map[string]bool{"not-approved": true}, nil, false)
	if err == nil {
		t.Fatal("expected an error for an addon not in the catalog")
	}
	if !errors.Is(err, ErrV4AddonNotInCatalog) {
		t.Errorf("expected ErrV4AddonNotInCatalog, got %v", err)
	}
	if _, ok := git.files["cluster-addons/prod-eu.yaml"]; ok {
		t.Error("nothing may be written when an addon fails the catalog gate")
	}
}

func TestUpdateClusterAddons_V4_ClusterNotRegistered_404s(t *testing.T) {
	git := adoptV4Git() // v4, but no cluster registered
	git.files[config.AddonCatalogPath] = v4TestApprovedCatalog(t)
	orch := New(nil, nil, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.UpdateClusterAddons(context.Background(), "never-registered", "https://x.example.com", "",
		map[string]bool{"cert-manager": true}, nil, false)
	if err == nil {
		t.Fatal("expected an error for an unregistered cluster")
	}
	if !errors.Is(err, ErrV4ClusterNotFound) {
		t.Errorf("expected ErrV4ClusterNotFound, got %v", err)
	}
}

func TestUpdateClusterAddons_V4_DryRun_ShowsOneFileAndWritesNothing(t *testing.T) {
	git := updateAddonsV4Fixture(t)
	orch := New(nil, nil, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.UpdateClusterAddons(context.Background(), "prod-eu", "https://prod-eu.example.com", "",
		map[string]bool{"cert-manager": true}, nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.DryRun == nil {
		t.Fatal("expected a dry-run preview")
	}
	if len(result.DryRun.FilesToWrite) != 1 {
		t.Fatalf("expected exactly 1 file preview, got %d: %+v", len(result.DryRun.FilesToWrite), result.DryRun.FilesToWrite)
	}
	if result.DryRun.FilesToWrite[0].Path != "cluster-addons/prod-eu.yaml" {
		t.Errorf("unexpected preview path: %s", result.DryRun.FilesToWrite[0].Path)
	}
	if len(result.DryRun.EffectiveAddons) != 1 || result.DryRun.EffectiveAddons[0] != "cert-manager" {
		t.Errorf("expected EffectiveAddons=[cert-manager], got %v", result.DryRun.EffectiveAddons)
	}
	if _, ok := git.files["cluster-addons/prod-eu.yaml"]; ok {
		t.Error("a dry run must not write anything")
	}
	if len(git.branches) != 0 {
		t.Error("a dry run must not create a branch")
	}
}

// TestUpdateClusterAddons_V3Repo_UnaffectedByV4LabelPatch is the
// regression guard: existing v3 label-patch tests already cover the happy
// path elsewhere in this package (TestUpdateClusterAddons); this pins that
// the new v4 probe does not change v3 behaviour when the repo has no
// engine pin.
func TestUpdateClusterAddons_V3Repo_UnaffectedByV4LabelPatch(t *testing.T) {
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: prod-eu\n    labels: {}\n")
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	result, err := orch.UpdateClusterAddons(context.Background(), "prod-eu", "https://k8s.example.com:6443", "eu-west-1",
		map[string]bool{"monitoring": true}, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s", result.Status)
	}
	if _, ok := git.files["cluster-addons/prod-eu.yaml"]; ok {
		t.Error("v4 cluster-addons file must not be written on a v3 repo")
	}
	valuesPath := "configuration/addons-clusters-values/prod-eu.yaml"
	if _, ok := git.files[valuesPath]; !ok {
		t.Error("expected the v3 values file to still be written")
	}
	if !strings.Contains(string(git.files["configuration/managed-clusters.yaml"]), "monitoring") {
		t.Error("expected the v3 registry to carry the addon label")
	}
}

package orchestrator

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitops"
)

// catalogDeleteFakeGit wraps mockGitProvider with a real ListDirectory —
// DeleteFromCatalog enumerates cluster-addons/*.yaml to check whether the
// addon it is about to remove is still enabled anywhere, and the shared
// mockGitProvider stubs ListDirectory to (nil, nil). Mirrors the
// migrationFakeGit precedent (migration_v3v4_test.go) but as a thin
// override on top of the shared mock instead of a full reimplementation.
type catalogDeleteFakeGit struct {
	*mockGitProvider
}

func (f *catalogDeleteFakeGit) ListDirectory(_ context.Context, dir, _ string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := strings.TrimSuffix(dir, "/") + "/"
	var out []string
	for p := range f.files {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		if strings.Contains(rest, "/") {
			continue // one level only, like the real providers
		}
		out = append(out, rest)
	}
	sort.Strings(out)
	return out, nil
}

func newCatalogDeleteFakeGit() *catalogDeleteFakeGit {
	return &catalogDeleteFakeGit{mockGitProvider: newMockGitProvider()}
}

func newV4TestOrchestratorForDelete(t *testing.T, git *catalogDeleteFakeGit) *Orchestrator {
	t.Helper()
	if _, ok := git.files[EnginePinPath]; !ok {
		git.files[EnginePinPath] = []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n")
	}
	return New(&sync.Mutex{}, nil, newMockArgocd(), git,
		GitOpsConfig{BranchPrefix: "sharko/", CommitPrefix: "sharko:", BaseBranch: "main"},
		RepoPathsConfig{}, nil)
}

// seedClusterAddonsEnabled writes a real cluster-addons/<cluster>.yaml via
// the production mutator (gitops.SetClusterAddonsAddon) so the fake git
// provider holds exactly what a real repo would. Named distinctly from
// upgrade_v4_test.go's seedClusterAddons (different signature: this one
// takes an enabled flag, not a version).
func seedClusterAddonsEnabled(t *testing.T, git *catalogDeleteFakeGit, cluster, addon string, enabled bool) {
	t.Helper()
	body, err := gitops.SetClusterAddonsAddon(nil, cluster, addon, enabled, nil, nil)
	if err != nil {
		t.Fatalf("SetClusterAddonsAddon: %v", err)
	}
	p, err := v4ClusterAddonsPath(cluster)
	if err != nil {
		t.Fatalf("v4ClusterAddonsPath: %v", err)
	}
	git.files[p] = body
}

// TestDeleteFromCatalog_RefusesWhenEnabledOnAnyCluster is the locked
// design decision: a delete pull request must never leave the repo
// semantically invalid.
func TestDeleteFromCatalog_RefusesWhenEnabledOnAnyCluster(t *testing.T) {
	git := newCatalogDeleteFakeGit()
	o := newV4TestOrchestratorForDelete(t, git)
	seedCatalogForEdit(t, git.mockGitProvider, config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"cert-manager": {RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", Version: "1.14.5"},
		},
	})
	seedClusterAddonsEnabled(t, git, "prod-eu", "cert-manager", true)
	seedClusterAddonsEnabled(t, git, "staging-eu", "cert-manager", false) // disabled elsewhere — must not appear

	_, err := o.DeleteFromCatalog(context.Background(), DeleteFromCatalogRequest{Name: "cert-manager", Yes: true})
	var blocked *CatalogDeleteBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("expected *CatalogDeleteBlockedError, got %T: %v", err, err)
	}
	if blocked.Addon != "cert-manager" {
		t.Errorf("Addon = %q, want cert-manager", blocked.Addon)
	}
	if len(blocked.Clusters) != 1 || blocked.Clusters[0] != "prod-eu" {
		t.Errorf("Clusters = %+v, want [prod-eu] only (staging-eu has it disabled)", blocked.Clusters)
	}
	if len(git.deletedFiles) != 0 {
		t.Errorf("nothing should have been deleted, got %+v", git.deletedFiles)
	}
	if len(git.branches) != 0 {
		t.Error("nothing should have been written — no branch should exist")
	}
}

// TestDeleteFromCatalog_ConfirmationRequired_NoYesNoDryRun mirrors the v3
// RemoveAddon contract: without confirmation and without a dry-run, the
// caller gets an impact report naming every file the delete would touch.
func TestDeleteFromCatalog_ConfirmationRequired_NoYesNoDryRun(t *testing.T) {
	git := newCatalogDeleteFakeGit()
	o := newV4TestOrchestratorForDelete(t, git)
	seedCatalogForEdit(t, git.mockGitProvider, config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"cert-manager": {RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", Version: "1.14.5"},
		},
	})
	globalValuesPath, _ := v4GlobalValuesPath("cert-manager")
	git.files[globalValuesPath] = []byte("# values\n")

	_, err := o.DeleteFromCatalog(context.Background(), DeleteFromCatalogRequest{Name: "cert-manager"})
	var confirmErr *CatalogDeleteConfirmationError
	if !errors.As(err, &confirmErr) {
		t.Fatalf("expected *CatalogDeleteConfirmationError, got %T: %v", err, err)
	}
	if confirmErr.Addon != "cert-manager" {
		t.Errorf("Addon = %q, want cert-manager", confirmErr.Addon)
	}
	wantFiles := map[string]bool{config.AddonCatalogPath: true, globalValuesPath: true}
	if len(confirmErr.FilesRemoved) != len(wantFiles) {
		t.Fatalf("FilesRemoved = %+v, want exactly %v", confirmErr.FilesRemoved, wantFiles)
	}
	for _, p := range confirmErr.FilesRemoved {
		if !wantFiles[p] {
			t.Errorf("unexpected file in impact report: %q", p)
		}
	}
	if len(git.branches) != 0 {
		t.Error("the confirmation gate must not write anything")
	}
}

// TestDeleteFromCatalog_DryRun_PreviewsWithDiffsNoWrite: dry_run shows the
// catalog.yaml diff and a delete preview for the global values file, with
// zero side effects.
func TestDeleteFromCatalog_DryRun_PreviewsWithDiffsNoWrite(t *testing.T) {
	git := newCatalogDeleteFakeGit()
	o := newV4TestOrchestratorForDelete(t, git)
	seedCatalogForEdit(t, git.mockGitProvider, config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"cert-manager": {RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", Version: "1.14.5"},
			"vault":        {RepoURL: "https://helm.releases.hashicorp.com", Chart: "vault", Version: "0.27.0"},
		},
	})
	globalValuesPath, _ := v4GlobalValuesPath("cert-manager")
	git.files[globalValuesPath] = []byte("# cert-manager values\n")

	result, err := o.DeleteFromCatalog(context.Background(), DeleteFromCatalogRequest{Name: "cert-manager", DryRun: true})
	if err != nil {
		t.Fatalf("DeleteFromCatalog dry-run: %v", err)
	}
	if result.DryRun == nil {
		t.Fatalf("expected a DryRun preview, got %+v", result)
	}
	if len(result.DryRun.FilesToWrite) != 2 {
		t.Fatalf("expected 2 file previews (catalog.yaml update + values delete), got %+v", result.DryRun.FilesToWrite)
	}
	var sawCatalogUpdate, sawValuesDelete bool
	for _, p := range result.DryRun.FilesToWrite {
		switch p.Path {
		case config.AddonCatalogPath:
			sawCatalogUpdate = p.Action == "update" && strings.Contains(p.Diff, "cert-manager")
		case globalValuesPath:
			sawValuesDelete = p.Action == "delete"
		}
	}
	if !sawCatalogUpdate {
		t.Errorf("expected an 'update' preview naming cert-manager for catalog.yaml, got %+v", result.DryRun.FilesToWrite)
	}
	if !sawValuesDelete {
		t.Errorf("expected a 'delete' preview for %s, got %+v", globalValuesPath, result.DryRun.FilesToWrite)
	}
	if len(git.branches) != 0 || len(git.deletedFiles) != 0 {
		t.Error("dry_run must not write or delete anything")
	}
	spec := readWrittenCatalog(t, git.mockGitProvider)
	if _, ok := spec.Addons["cert-manager"]; !ok {
		t.Error("dry_run must not actually remove the entry from the stored file")
	}
}

// TestDeleteFromCatalog_HappyPath_DeletesEntryAndValuesFiles is the real
// write: one PR removing the catalog.yaml entry plus deletePaths for the
// global values file and any stray per-cluster values file.
func TestDeleteFromCatalog_HappyPath_DeletesEntryAndValuesFiles(t *testing.T) {
	git := newCatalogDeleteFakeGit()
	o := newV4TestOrchestratorForDelete(t, git)
	seedCatalogForEdit(t, git.mockGitProvider, config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"cert-manager": {RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", Version: "1.14.5"},
			"vault":        {RepoURL: "https://helm.releases.hashicorp.com", Chart: "vault", Version: "0.27.0"},
		},
	})
	globalValuesPath, _ := v4GlobalValuesPath("cert-manager")
	git.files[globalValuesPath] = []byte("# cert-manager values\n")
	// A stray per-cluster values file left over from an earlier
	// enable/disable cycle on a cluster where the addon is now disabled —
	// this must be swept too even though it was never "enabled" there.
	seedClusterAddonsEnabled(t, git, "staging-eu", "cert-manager", false)
	strayValuesPath, _ := v4ClusterValuesPath("staging-eu", "cert-manager")
	git.files[strayValuesPath] = []byte("# leftover override\n")

	result, err := o.DeleteFromCatalog(context.Background(), DeleteFromCatalogRequest{Name: "cert-manager", Yes: true})
	if err != nil {
		t.Fatalf("DeleteFromCatalog: %v", err)
	}
	if result.PRUrl == "" {
		t.Fatalf("expected a PR result, got %+v", result)
	}

	spec := readWrittenCatalog(t, git.mockGitProvider)
	if _, ok := spec.Addons["cert-manager"]; ok {
		t.Errorf("cert-manager should be gone from the catalog, got %+v", spec.Addons)
	}
	if _, ok := spec.Addons["vault"]; !ok {
		t.Error("vault must be untouched by deleting cert-manager")
	}

	wantDeleted := map[string]bool{globalValuesPath: true, strayValuesPath: true}
	if len(git.deletedFiles) != len(wantDeleted) {
		t.Fatalf("deletedFiles = %+v, want exactly %v", git.deletedFiles, wantDeleted)
	}
	for _, p := range git.deletedFiles {
		if !wantDeleted[p] {
			t.Errorf("unexpected file deleted: %q", p)
		}
	}
	if _, exists := git.files[globalValuesPath]; exists {
		t.Error("global values file should have been removed")
	}
	if _, exists := git.files[strayValuesPath]; exists {
		t.Error("stray per-cluster values file should have been removed")
	}
}

// TestDeleteFromCatalog_NotFound: deleting an addon that was never in the
// catalog is a 404, not a silent success.
func TestDeleteFromCatalog_NotFound(t *testing.T) {
	git := newCatalogDeleteFakeGit()
	o := newV4TestOrchestratorForDelete(t, git)
	seedCatalogForEdit(t, git.mockGitProvider, config.AddonCatalogSpec{Addons: map[string]config.AddonCatalogEntry{}})

	_, err := o.DeleteFromCatalog(context.Background(), DeleteFromCatalogRequest{Name: "does-not-exist", Yes: true})
	if !errors.Is(err, ErrV4AddonNotInCatalog) {
		t.Fatalf("expected ErrV4AddonNotInCatalog, got %v", err)
	}
}

// TestDeleteFromCatalog_EmptyCatalogFile: a present-but-blank catalog.yaml
// is the caller's repo to fix.
func TestDeleteFromCatalog_EmptyCatalogFile(t *testing.T) {
	git := newCatalogDeleteFakeGit()
	o := newV4TestOrchestratorForDelete(t, git)
	git.files[config.AddonCatalogPath] = []byte("")

	_, err := o.DeleteFromCatalog(context.Background(), DeleteFromCatalogRequest{Name: "cert-manager", Yes: true})
	if !errors.Is(err, ErrCatalogFileEmpty) {
		t.Fatalf("expected ErrCatalogFileEmpty, got %v", err)
	}
}

// TestDeleteFromCatalog_RefusesHalfConvertedRepo mirrors the same refusal
// on AddToCatalog/EditCatalogEntry.
func TestDeleteFromCatalog_RefusesHalfConvertedRepo(t *testing.T) {
	git := newCatalogDeleteFakeGit()
	git.files[EnginePinPath] = []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n")
	git.files[V3SecondaryMarkerPath] = []byte("clusters:\n  - name: prod-eu\n")
	o := New(&sync.Mutex{}, nil, newMockArgocd(), git,
		GitOpsConfig{BranchPrefix: "sharko/", CommitPrefix: "sharko:", BaseBranch: "main"},
		RepoPathsConfig{}, nil)
	seedCatalogForEdit(t, git.mockGitProvider, config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"cert-manager": {RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", Version: "1.14.5"},
		},
	})

	_, err := o.DeleteFromCatalog(context.Background(), DeleteFromCatalogRequest{Name: "cert-manager", Yes: true})
	if !errors.Is(err, ErrMixedRepoLayout) {
		t.Fatalf("expected ErrMixedRepoLayout, got %v", err)
	}
}

// TestDeleteFromCatalog_RefusesV3Repo: no engine pin at all means no
// catalog.yaml for this door to delete from.
func TestDeleteFromCatalog_RefusesV3Repo(t *testing.T) {
	git := newCatalogDeleteFakeGit() // no EnginePinPath seeded
	o := New(&sync.Mutex{}, nil, newMockArgocd(), git,
		GitOpsConfig{BranchPrefix: "sharko/", CommitPrefix: "sharko:", BaseBranch: "main"},
		RepoPathsConfig{}, nil)

	_, err := o.DeleteFromCatalog(context.Background(), DeleteFromCatalogRequest{Name: "cert-manager", Yes: true})
	if !IsV3RepoUnsupported(err) {
		t.Fatalf("expected ErrV3RepoUnsupported, got %v", err)
	}
}

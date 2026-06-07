// V2-cleanup-14 coverage: the catalog "Add addon" operation now honors the
// per-request auto-merge decision (AddAddonRequest.AutoMerge → PRMetadata.
// AutoMergeOverride → resolveAutoMerge) and supports a dry-run preview
// (AddAddonRequest.DryRun → DryRunResult on GitResult) with NO git side
// effects. These tests mirror auto_merge_test.go and the register-cluster
// dry-run parity tests.
package orchestrator

import (
	"context"
	"testing"
)

// seedCatalogGit returns a mock git provider pre-populated with an empty
// addons-catalog.yaml so AddAddon can read it.
func seedCatalogGit() *mockGitProvider {
	git := newMockGitProvider()
	git.files["configuration/addons-catalog.yaml"] = []byte("applicationsets:\n")
	return git
}

// addonReq is a minimal valid AddAddonRequest with the given overrides applied.
func addonReq(autoMerge *bool, dryRun bool) AddAddonRequest {
	return AddAddonRequest{
		Name:      "prometheus",
		Chart:     "kube-prometheus-stack",
		RepoURL:   "https://prometheus-community.github.io/helm-charts",
		Version:   "45.0.0",
		Namespace: "monitoring",
		AutoMerge: autoMerge,
		DryRun:    dryRun,
	}
}

// TestAddAddon_AutoMerge_Override covers the per-request override matrix: an
// explicit true/false on the request wins over the connection-level default,
// and nil falls back to the connection default. Mirrors the precedence rows
// asserted for register/init/remove.
func TestAddAddon_AutoMerge_Override(t *testing.T) {
	tests := []struct {
		name        string
		autoMerge   *bool
		connDefault bool
		wantMerged  bool
	}{
		{name: "request_true_wins_over_conn_false", autoMerge: boolPtr(true), connDefault: false, wantMerged: true},
		{name: "request_false_wins_over_conn_true", autoMerge: boolPtr(false), connDefault: true, wantMerged: false},
		{name: "request_nil_falls_back_to_conn_true", autoMerge: nil, connDefault: true, wantMerged: true},
		{name: "request_nil_falls_back_to_conn_false", autoMerge: nil, connDefault: false, wantMerged: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			git := seedCatalogGit()
			cfg := defaultGitOps()
			cfg.PRAutoMerge = tt.connDefault
			orch := New(nil, defaultCreds(), newMockArgocd(), git, cfg, defaultPaths(), nil)

			result, err := orch.AddAddon(context.Background(), addonReq(tt.autoMerge, false))
			if err != nil {
				t.Fatalf("AddAddon: %v", err)
			}

			if result.Merged != tt.wantMerged {
				t.Errorf("Merged = %v, want %v", result.Merged, tt.wantMerged)
			}
			// A PR is always created for a non-dry-run add.
			if len(git.prs) != 1 {
				t.Fatalf("expected exactly 1 PR created, got %d", len(git.prs))
			}
			// When auto-merged, the source branch is cleaned up; when not, it
			// is left for manual review.
			if tt.wantMerged {
				if len(git.deletedBranches) != 1 {
					t.Errorf("expected source branch deleted after auto-merge, got %d deletions", len(git.deletedBranches))
				}
			} else {
				if len(git.deletedBranches) != 0 {
					t.Errorf("expected no branch deletion for manual PR, got %v", git.deletedBranches)
				}
			}
		})
	}
}

// TestAddAddon_DryRun_NoSideEffects asserts that a dry-run request returns a
// preview (DryRunResult with FilesToWrite) and touches NOTHING in git: no
// branch, no PR, no committed files, no deletions.
func TestAddAddon_DryRun_NoSideEffects(t *testing.T) {
	git := seedCatalogGit()
	// Snapshot the file count before the dry-run so we can prove the catalog
	// was not mutated and no new files were written.
	filesBefore := len(git.files)

	orch := New(nil, defaultCreds(), newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)

	// Even with the connection default set to auto-merge, dry-run must merge
	// nothing.
	result, err := orch.AddAddon(context.Background(), addonReq(boolPtr(true), true))
	if err != nil {
		t.Fatalf("AddAddon dry-run: %v", err)
	}

	if result.DryRun == nil {
		t.Fatal("expected DryRunResult on dry-run, got nil")
	}
	if len(result.DryRun.FilesToWrite) == 0 {
		t.Error("expected FilesToWrite to be populated on dry-run")
	}
	if result.Merged {
		t.Error("dry-run must not report Merged=true")
	}
	if result.PRID != 0 || result.PRUrl != "" {
		t.Errorf("dry-run must not carry PR identity, got PRID=%d PRUrl=%q", result.PRID, result.PRUrl)
	}

	// No git side effects whatsoever.
	if len(git.branches) != 0 {
		t.Errorf("dry-run created branches: %v", git.branches)
	}
	if len(git.prs) != 0 {
		t.Errorf("dry-run created PRs: %d", len(git.prs))
	}
	if len(git.deletedFiles) != 0 {
		t.Errorf("dry-run deleted files: %v", git.deletedFiles)
	}
	if len(git.files) != filesBefore {
		t.Errorf("dry-run mutated the file set: before=%d after=%d", filesBefore, len(git.files))
	}
	// The catalog must be unchanged: still the seeded empty document.
	if got := string(git.files["configuration/addons-catalog.yaml"]); got != "applicationsets:\n" {
		t.Errorf("dry-run mutated the catalog content:\n%s", got)
	}
}

// TestAddAddon_DryRun_FileSetMatchesRealWrite asserts the critical invariant:
// the set of file PATHS the dry-run previews equals the set of paths the real
// (non-dry-run) path actually writes. If these diverge, the preview lies.
func TestAddAddon_DryRun_FileSetMatchesRealWrite(t *testing.T) {
	// Real write.
	realGit := seedCatalogGit()
	realOrch := New(nil, defaultCreds(), newMockArgocd(), realGit, defaultGitOps(), defaultPaths(), nil)
	if _, err := realOrch.AddAddon(context.Background(), addonReq(nil, false)); err != nil {
		t.Fatalf("AddAddon real write: %v", err)
	}
	// The files the real path wrote are exactly those present after the seed,
	// minus the originally-seeded catalog (which is overwritten, not added).
	// We compare on the full set of paths present in git.files.
	realPaths := map[string]bool{}
	for p := range realGit.files {
		realPaths[p] = true
	}

	// Dry-run.
	dryGit := seedCatalogGit()
	dryOrch := New(nil, defaultCreds(), newMockArgocd(), dryGit, defaultGitOps(), defaultPaths(), nil)
	result, err := dryOrch.AddAddon(context.Background(), addonReq(nil, true))
	if err != nil {
		t.Fatalf("AddAddon dry-run: %v", err)
	}
	if result.DryRun == nil {
		t.Fatal("expected DryRunResult, got nil")
	}
	dryPaths := map[string]bool{}
	for _, fp := range result.DryRun.FilesToWrite {
		dryPaths[fp.Path] = true
	}

	// Every previewed path must be a path the real write produced.
	for p := range dryPaths {
		if !realPaths[p] {
			t.Errorf("dry-run previewed path %q that the real write never produced", p)
		}
	}
	// Every path the real write produced must be previewed.
	for p := range realPaths {
		if !dryPaths[p] {
			t.Errorf("real write produced path %q that the dry-run never previewed", p)
		}
	}
}

// V2-cleanup-13 coverage: cluster removal and addon-disable now honor the
// same per-request auto-merge override as init/register, plumbed through
// PRMetadata.AutoMergeOverride into commitChangesWithMeta.
//
//   - auto_merge=true  -> removal/disable PR merged + source branch deleted.
//   - auto_merge=false -> PR opened, NOT merged, branch kept.
//   - auto_merge=nil   -> falls back to the connection PRAutoMerge default
//     (tested with both a true and a false connection default).
//
// The mock git provider only calls DeleteBranch after a successful merge
// (see commitChangesWithMeta), so a non-empty deletedBranches slice is a
// faithful proxy for "the PR was merged".
package orchestrator

import (
	"context"
	"testing"
)

// managedClustersWithCertManager is reused by the disable tests so the
// values-file mutation path fires and a PR is produced.
const managedClustersWithCertManager = "clusters:\n  - name: prod-eu\n    labels:\n      cert-manager: true\n"
const certManagerValuesFile = "# values\ncert-manager:\n  enabled: true\n"

func TestRemoveCluster_AutoMergeOverride(t *testing.T) {
	tests := []struct {
		name           string
		connAutoMerge  bool
		override       *bool
		wantMerged     bool
		wantBranchGone bool
	}{
		{name: "override_true_beats_conn_false", connAutoMerge: false, override: boolPtr(true), wantMerged: true, wantBranchGone: true},
		{name: "override_false_beats_conn_true", connAutoMerge: true, override: boolPtr(false), wantMerged: false, wantBranchGone: false},
		{name: "override_nil_falls_back_to_conn_true", connAutoMerge: true, override: nil, wantMerged: true, wantBranchGone: true},
		{name: "override_nil_falls_back_to_conn_false", connAutoMerge: false, override: nil, wantMerged: false, wantBranchGone: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			git := newMockGitProvider()
			git.files["configuration/managed-clusters.yaml"] = []byte(managedClustersWithCertManager)
			git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte(certManagerValuesFile)

			cfg := defaultGitOps()
			cfg.PRAutoMerge = tt.connAutoMerge
			orch := New(nil, nil, newMockArgocd(), git, cfg, defaultPaths(), nil)

			result, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
				Name:      "prod-eu",
				Cleanup:   "git", // skip remote/ArgoCD secret cleanup — focus on the PR/merge path
				Yes:       true,
				AutoMerge: tt.override,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Git == nil {
				t.Fatal("expected a git result (PR should have been created)")
			}
			if result.Git.Merged != tt.wantMerged {
				t.Errorf("Merged=%v, want %v", result.Git.Merged, tt.wantMerged)
			}
			if len(git.prs) != 1 {
				t.Errorf("expected exactly one PR created, got %d", len(git.prs))
			}
			branchDeleted := len(git.deletedBranches) > 0
			if branchDeleted != tt.wantBranchGone {
				t.Errorf("branch deleted=%v, want %v (deletedBranches=%v)", branchDeleted, tt.wantBranchGone, git.deletedBranches)
			}
		})
	}
}

func TestDisableAddon_AutoMergeOverride(t *testing.T) {
	tests := []struct {
		name           string
		connAutoMerge  bool
		override       *bool
		wantMerged     bool
		wantBranchGone bool
	}{
		{name: "override_true_beats_conn_false", connAutoMerge: false, override: boolPtr(true), wantMerged: true, wantBranchGone: true},
		{name: "override_false_beats_conn_true", connAutoMerge: true, override: boolPtr(false), wantMerged: false, wantBranchGone: false},
		{name: "override_nil_falls_back_to_conn_true", connAutoMerge: true, override: nil, wantMerged: true, wantBranchGone: true},
		{name: "override_nil_falls_back_to_conn_false", connAutoMerge: false, override: nil, wantMerged: false, wantBranchGone: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			git := newMockGitProvider()
			git.files["configuration/managed-clusters.yaml"] = []byte(managedClustersWithCertManager)
			git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte(certManagerValuesFile)

			cfg := defaultGitOps()
			cfg.PRAutoMerge = tt.connAutoMerge
			orch := New(nil, nil, newMockArgocd(), git, cfg, defaultPaths(), nil)

			result, err := orch.DisableAddon(context.Background(), DisableAddonRequest{
				Cluster:   "prod-eu",
				Addon:     "cert-manager",
				Cleanup:   "labels", // skip remote secret cleanup — focus on the PR/merge path
				Yes:       true,
				AutoMerge: tt.override,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Git == nil {
				t.Fatal("expected a git result (PR should have been created)")
			}
			if result.Git.Merged != tt.wantMerged {
				t.Errorf("Merged=%v, want %v", result.Git.Merged, tt.wantMerged)
			}
			if len(git.prs) != 1 {
				t.Errorf("expected exactly one PR created, got %d", len(git.prs))
			}
			branchDeleted := len(git.deletedBranches) > 0
			if branchDeleted != tt.wantBranchGone {
				t.Errorf("branch deleted=%v, want %v (deletedBranches=%v)", branchDeleted, tt.wantBranchGone, git.deletedBranches)
			}
		})
	}
}

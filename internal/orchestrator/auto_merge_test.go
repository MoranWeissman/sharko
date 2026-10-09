// BUG-031 + BUG-032 coverage:
//   - resolveAutoMerge / ResolveAutoMerge precedence (per-request override
//     wins over connection default; nil falls back).
//   - commitChangesWithMeta plumbs PRMetadata.AutoMergeOverride through
//     the merge decision and through the post-merge DeleteBranch call.
//   - DeleteBranch failure is best-effort: the merge operation still
//     succeeds (mirrors the AzureDevOps "not yet implemented" path).
package orchestrator

import (
	"context"
	"errors"
	"testing"
)

// TestResolveAutoMerge covers the 4-row precedence matrix from the
// V124-BUG-031 brief — per-request override wins over the connection
// default; nil means "fall back to connection default" (back-compat for
// legacy clients).
func TestResolveAutoMerge(t *testing.T) {
	tests := []struct {
		name        string
		req         *bool
		connDefault bool
		want        bool
	}{
		{
			name:        "request_true_wins_over_conn_false",
			req:         boolPtr(true),
			connDefault: false,
			want:        true,
		},
		{
			name:        "request_false_wins_over_conn_true",
			req:         boolPtr(false),
			connDefault: true,
			want:        false,
		},
		{
			name:        "request_nil_falls_back_to_conn_true",
			req:         nil,
			connDefault: true,
			want:        true,
		},
		{
			name:        "request_nil_falls_back_to_conn_false",
			req:         nil,
			connDefault: false,
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveAutoMerge(tt.req, tt.connDefault); got != tt.want {
				t.Errorf("ResolveAutoMerge(%v, %v) = %v, want %v",
					tt.req, tt.connDefault, got, tt.want)
			}
			// Internal alias must agree.
			if got := resolveAutoMerge(tt.req, tt.connDefault); got != tt.want {
				t.Errorf("resolveAutoMerge(%v, %v) = %v, want %v",
					tt.req, tt.connDefault, got, tt.want)
			}
		})
	}
}

// TestCommitChangesWithMeta_AutoMergeOverride covers the same matrix
// end-to-end through commitChangesWithMeta — the funnel every register/
// adopt/update/init flow shares.
func TestCommitChangesWithMeta_AutoMergeOverride(t *testing.T) {
	tests := []struct {
		name           string
		connAutoMerge  bool
		reqOverride    *bool
		wantMerged     bool
		wantBranchGone bool
	}{
		{
			name:           "override_true_beats_conn_false",
			connAutoMerge:  false,
			reqOverride:    boolPtr(true),
			wantMerged:     true,
			wantBranchGone: true,
		},
		{
			name:           "override_false_beats_conn_true",
			connAutoMerge:  true,
			reqOverride:    boolPtr(false),
			wantMerged:     false,
			wantBranchGone: false,
		},
		{
			name:           "override_nil_falls_back_to_conn_true",
			connAutoMerge:  true,
			reqOverride:    nil,
			wantMerged:     true,
			wantBranchGone: true,
		},
		{
			name:           "override_nil_falls_back_to_conn_false",
			connAutoMerge:  false,
			reqOverride:    nil,
			wantMerged:     false,
			wantBranchGone: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			git := newMockGitProvider()
			cfg := defaultGitOps()
			cfg.PRAutoMerge = tt.connAutoMerge
			orch := New(nil, defaultCreds(), newMockArgocd(), git, cfg, defaultPaths(), nil)

			result, err := orch.commitChangesWithMeta(
				context.Background(),
				map[string][]byte{"test.yaml": []byte("ok")},
				nil,
				"test op",
				PRMetadata{
					OperationCode:     "test",
					AutoMergeOverride: tt.reqOverride,
				},
			)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Merged != tt.wantMerged {
				t.Errorf("Merged=%v, want %v", result.Merged, tt.wantMerged)
			}
			// BUG-032: the branch must be deleted iff the PR was merged.
			deleted := len(git.deletedBranches) > 0
			if deleted != tt.wantBranchGone {
				t.Errorf("DeleteBranch called=%v, want %v (deleted=%v)",
					deleted, tt.wantBranchGone, git.deletedBranches)
			}
			if tt.wantBranchGone && len(git.deletedBranches) > 0 {
				if git.deletedBranches[0] != result.Branch {
					t.Errorf("DeleteBranch(%q), want %q",
						git.deletedBranches[0], result.Branch)
				}
			}
		})
	}
}

// TestCommitChangesWithMeta_DeleteBranchBestEffort verifies the BUG-032
// best-effort guarantee: a DeleteBranch failure (e.g. AzureDevOps's
// "not yet implemented" string error, branch already deleted by an
// external user, transient API hiccup) is logged but never causes the
// merge operation itself to fail.
func TestCommitChangesWithMeta_DeleteBranchBestEffort(t *testing.T) {
	git := newMockGitProvider()
	git.deleteBranchErr = errors.New("azure devops: DeleteBranch not yet implemented")
	cfg := autoMergeGitOps()
	orch := New(nil, defaultCreds(), newMockArgocd(), git, cfg, defaultPaths(), nil)

	result, err := orch.commitChangesWithMeta(
		context.Background(),
		map[string][]byte{"test.yaml": []byte("ok")},
		nil,
		"test op",
		PRMetadata{OperationCode: "test"},
	)
	if err != nil {
		t.Fatalf("merge must succeed even when DeleteBranch errors: %v", err)
	}
	if !result.Merged {
		t.Errorf("Merged=false, want true")
	}
	if len(git.deletedBranches) != 1 {
		t.Errorf("DeleteBranch should still be called once, got %v", git.deletedBranches)
	}
}

func boolPtr(b bool) *bool { return &b }

package orchestrator

import (
	"context"
	"testing"
)

// TestRegisterCluster_V4_FollowsGlobalAutoMerge is the story G2 behaviour
// pin. The register dialog tells the user whether the pull request merges by
// itself, and it reads that from the same setting this code reads: the
// connection's GitOps auto-merge switch (o.gitops.PRAutoMerge). The register
// dialog never sends auto_merge, so the request override is nil there.
//
// Root cause of the E2-R2 report: the first-run wizard's "Set Up &
// Auto-Merge" button sends auto_merge only on the one setup request
// (internal/api/init.go ResolveAutoMerge) and never turns the switch on, so
// the register pull request after it correctly waited for a human.
func TestRegisterCluster_V4_FollowsGlobalAutoMerge(t *testing.T) {
	yes := true
	no := false
	cases := []struct {
		name       string
		global     bool
		override   *bool
		wantMerged bool
	}{
		{name: "switch off, no override: waits for a human", global: false, override: nil, wantMerged: false},
		{name: "switch on, no override: merges by itself", global: true, override: nil, wantMerged: true},
		{name: "switch off, request asks to merge: merges", global: false, override: &yes, wantMerged: true},
		{name: "switch on, request asks not to merge: waits", global: true, override: &no, wantMerged: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			git := newMockGitProvider()
			git.files[EnginePinPath] = []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n")
			gitops := defaultGitOps()
			gitops.PRAutoMerge = tc.global
			orch := New(nil, defaultCreds(), newMockArgocd(), git, gitops, defaultPaths(), nil)

			result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
				Name:      "prod-eu",
				AutoMerge: tc.override,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Git == nil {
				t.Fatalf("expected a Git result, got %+v", result)
			}
			if result.Git.Merged != tc.wantMerged {
				t.Errorf("Merged = %v, want %v", result.Git.Merged, tc.wantMerged)
			}
			wantCalls := 0
			if tc.wantMerged {
				wantCalls = 1
			}
			if git.mergeCalls != wantCalls {
				t.Errorf("MergePullRequest called %d times, want %d", git.mergeCalls, wantCalls)
			}
		})
	}
}

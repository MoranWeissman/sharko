package orchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// EP3 dry run round S join: a waiting mark with PR 0 is never written.
// The waiting pass skips an adopt or take over mark with PR 0, so nothing
// would ever finish it. putJoinMark refuses it, the caller gets the plain
// partial answer (ErrMarkNotWritten), the pull request is never
// auto-merged, and the tracker still holds it.

const noNumberPRURL = "https://git.example.com/example/repo/pulls/open"

func TestPutJoinMark_RefusesPRZero(t *testing.T) {
	orch, _, asm := adoptPendingSetup(t)
	err := orch.putJoinMark(context.Background(), AnnotationAdoptPendingPR, "prod-eu", 0, noNumberPRURL, nil)
	if err == nil {
		t.Fatal("putJoinMark wrote a mark with PR 0")
	}
	if v, ok := asm.annotations["prod-eu"][AnnotationAdoptPendingPR]; ok {
		t.Fatalf("a mark went on: %q", v)
	}
}

func TestAdoptV4_NoPRNumber_AutoMerge_PartialNotMerged(t *testing.T) {
	orch, git, asm := adoptPendingSetup(t)
	orch.gitops = autoMergeGitOps()
	orch.git = noIDGit{mockGitProvider: git, stripURL: true}
	tracker := &stubPRTracker{}
	orch.SetPRTracker(tracker)

	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"}, Clusters: []string{"prod-eu"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cr := result.Results[0]
	want := MarkNotWrittenMessage("adopt", "prod-eu", noNumberPRURL)
	if cr.Status != "partial" || cr.Message != want {
		t.Fatalf("status %q message %q, want partial and %q", cr.Status, cr.Message, want)
	}
	if cr.Merged || (cr.Git != nil && cr.Git.Merged) || git.mergeCalls != 0 {
		t.Fatalf("merged (calls %d); a pull request without a mark must wait", git.mergeCalls)
	}
	if len(tracker.snapshot()) != 1 {
		t.Fatal("the pull request must still be tracked")
	}
	if v, ok := asm.annotations["prod-eu"][AnnotationAdoptPendingPR]; ok {
		t.Fatalf("a mark with no PR number went on: %q", v)
	}
	if len(asm.takenOver) != 0 {
		t.Fatalf("the connection changed: %v", asm.takenOver)
	}
}

func TestTakeover_NoPRNumber_AutoMerge_MarkNotWritten(t *testing.T) {
	git := v4Git()
	asm := newMockArgoSecretManager()
	gitops := defaultGitOps()
	gitops.PRAutoMerge = true
	orch := New(nil, defaultCreds(), newMockArgocd(), noIDGit{mockGitProvider: git, stripURL: true}, gitops, defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")
	tracker := &stubPRTracker{}
	orch.SetPRTracker(tracker)

	res, err := orch.TakeoverClusterGit(context.Background(), TakeoverClusterRequest{Name: "prod-eu", Yes: true})
	if !errors.Is(err, ErrMarkNotWritten) || res == nil || res.Git == nil || res.Git.Merged {
		t.Fatalf("res %+v err %v, want ErrMarkNotWritten and an open pull request", res, err)
	}
	if git.mergeCalls != 0 {
		t.Fatalf("merged %d times; it must wait for the mark", git.mergeCalls)
	}
	if len(tracker.snapshot()) != 1 {
		t.Fatal("the pull request must still be tracked")
	}
	if v, ok := asm.annotations["prod-eu"][AnnotationTakeoverPendingPR]; ok {
		t.Fatalf("a mark with no PR number went on: %q", v)
	}
}

func TestTakeover_ReRun_OpenPRWithNoNumber_MarkNotWritten(t *testing.T) {
	git := v4Git()
	git.prs = append(git.prs, &gitprovider.PullRequest{Title: "sharko: take over cluster prod-eu", URL: noNumberPRURL})
	asm := newMockArgoSecretManager()
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	res, err := orch.TakeoverClusterGit(context.Background(), TakeoverClusterRequest{Name: "prod-eu", Yes: true})
	if !errors.Is(err, ErrMarkNotWritten) || res == nil || !res.AlreadyWaiting {
		t.Fatalf("res %+v err %v, want ErrMarkNotWritten on the open pull request", res, err)
	}
	if v, ok := asm.annotations["prod-eu"][AnnotationTakeoverPendingPR]; ok {
		t.Fatalf("a mark with no PR number went on: %q", v)
	}
}

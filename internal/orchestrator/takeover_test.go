package orchestrator

import (
	"context"
	"errors"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// v4Git returns a mock repo that already looks like a v4 repo (the engine
// pin is the probe every v4 write path uses).
func v4Git() *mockGitProvider {
	git := newMockGitProvider()
	git.files[EnginePinPath] = []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n")
	return git
}

func TestTakeoverClusterGit_WritesBothV4FilesAndTurnsNothingOn(t *testing.T) {
	git := v4Git()
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	res, err := orch.TakeoverClusterGit(context.Background(), TakeoverClusterRequest{
		Name: "prod-eu",
		Yes:  true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.AlreadyInFleet {
		t.Error("a fresh takeover should not report the cluster as already in the fleet")
	}

	connBytes, ok := git.files[V4ManagedClustersPath]
	if !ok {
		t.Fatalf("%s was not written; wrote: %v", V4ManagedClustersPath, keysOf(git.files))
	}
	spec, err := models.LoadManagedClusters(connBytes)
	if err != nil {
		t.Fatalf("the fleet record does not parse: %v", err)
	}
	var entry *models.ManagedClusterEntry
	for i := range spec.Clusters {
		if spec.Clusters[i].Name == "prod-eu" {
			entry = &spec.Clusters[i]
		}
	}
	if entry == nil {
		t.Fatal("no prod-eu entry in the fleet record")
	}
	if len(entry.Labels) != 0 {
		t.Errorf("a takeover must not author addon labels on the fleet entry, got %v", entry.Labels)
	}

	addonsBytes, ok := git.files["cluster-addons/prod-eu.yaml"]
	if !ok {
		t.Fatalf("cluster-addons/prod-eu.yaml was not written; wrote: %v", keysOf(git.files))
	}
	addons, err := models.LoadClusterAddons(addonsBytes)
	if err != nil {
		t.Fatalf("the addon assignment file does not parse: %v", err)
	}
	if addons.Cluster != "prod-eu" {
		t.Errorf("cluster name in the assignment file = %q", addons.Cluster)
	}
	if len(addons.Addons) != 0 {
		t.Errorf("a takeover must turn nothing on, got %v", addons.Addons)
	}
}

func TestTakeoverClusterGit_RefusesWithoutConfirmation(t *testing.T) {
	git := v4Git()
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	_, err := orch.TakeoverClusterGit(context.Background(), TakeoverClusterRequest{Name: "prod-eu"})
	if err == nil {
		t.Fatal("a takeover without yes:true must be refused")
	}
	if !strings.Contains(err.Error(), "confirmation required") {
		t.Errorf("error should name the missing confirmation: %v", err)
	}
	if _, ok := git.files[V4ManagedClustersPath]; ok {
		t.Error("nothing may be written when the confirmation is missing")
	}
}

func TestTakeoverClusterGit_DryRunWritesNothingAndShowsBothFiles(t *testing.T) {
	git := v4Git()
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	res, err := orch.TakeoverClusterGit(context.Background(), TakeoverClusterRequest{
		Name:   "prod-eu",
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.DryRun == nil {
		t.Fatal("a dry run must return a plan")
	}
	if len(res.DryRun.FilesToWrite) != 2 {
		t.Fatalf("the plan should cover both v4 files, got %d", len(res.DryRun.FilesToWrite))
	}
	if len(res.DryRun.EffectiveAddons) != 0 {
		t.Errorf("a takeover plan enables nothing, got %v", res.DryRun.EffectiveAddons)
	}
	if _, ok := git.files[V4ManagedClustersPath]; ok {
		t.Error("a dry run wrote to the repo")
	}
	if _, ok := git.files["cluster-addons/prod-eu.yaml"]; ok {
		t.Error("a dry run wrote to the repo")
	}
}

func TestTakeoverClusterGit_RefusesOnAV3Repo(t *testing.T) {
	git := newMockGitProvider() // no engine pin -> still v3
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	_, err := orch.TakeoverClusterGit(context.Background(), TakeoverClusterRequest{Name: "prod-eu", Yes: true})
	if err != ErrTakeoverNeedsV4Repo {
		t.Fatalf("want ErrTakeoverNeedsV4Repo, got %v", err)
	}
	if _, ok := git.files[V4ManagedClustersPath]; ok {
		t.Error("no v4 fleet record may be written on a v3 repo")
	}
	if _, ok := git.files["cluster-addons/prod-eu.yaml"]; ok {
		t.Error("no v4 assignment file may be written on a v3 repo")
	}
}

func TestTakeoverClusterGit_IsSafeToRepeat(t *testing.T) {
	git := v4Git()
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	if _, err := orch.TakeoverClusterGit(context.Background(), TakeoverClusterRequest{Name: "prod-eu", Yes: true}); err != nil {
		t.Fatalf("first takeover: %v", err)
	}
	// While the first pull request is open, a second take over points at
	// it and opens nothing new (episode 3 dry run, design call 8).
	waiting, err := orch.TakeoverClusterGit(context.Background(), TakeoverClusterRequest{Name: "prod-eu", Yes: true})
	if err != nil {
		t.Fatalf("second takeover: %v", err)
	}
	if !waiting.AlreadyWaiting || waiting.Git == nil || waiting.Git.PRID != 1 {
		t.Fatalf("second takeover = %+v, want it to point at pull request #1", waiting)
	}
	if len(git.prs) != 1 {
		t.Fatalf("second takeover opened another pull request: %d open", len(git.prs))
	}

	// After the merge (the mock already wrote to main), a third take over
	// finds everything in place and opens no pull request at all.
	git.prs = nil
	res, err := orch.TakeoverClusterGit(context.Background(), TakeoverClusterRequest{Name: "prod-eu", Yes: true})
	if err != nil {
		t.Fatalf("third takeover: %v", err)
	}
	if !res.AlreadyInFleet {
		t.Error("a repeat takeover must report that the cluster is already in the fleet")
	}
	if !res.NothingToWrite || res.Git != nil || len(git.prs) != 0 {
		t.Errorf("a takeover with nothing to change must open no pull request: %+v, prs %d", res, len(git.prs))
	}

	spec, err := models.LoadManagedClusters(git.files[V4ManagedClustersPath])
	if err != nil {
		t.Fatalf("fleet record does not parse: %v", err)
	}
	count := 0
	for _, c := range spec.Clusters {
		if c.Name == "prod-eu" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("repeating the takeover produced %d entries, want exactly 1", count)
	}
}

// TestTakeoverClusterGit_KeepsAnExistingAssignmentFile — a cluster that
// somehow already has an addon file (a half-finished earlier attempt)
// must not have it overwritten with an empty one.
func TestTakeoverClusterGit_KeepsAnExistingAssignmentFile(t *testing.T) {
	git := v4Git()
	existing, err := models.SaveClusterAddons(models.ClusterAddonsSpec{
		Cluster: "prod-eu",
		Addons:  map[string]models.ClusterAddonsAddon{"datadog": {Enabled: true}},
	})
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	git.files["cluster-addons/prod-eu.yaml"] = existing

	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
	if _, err := orch.TakeoverClusterGit(context.Background(), TakeoverClusterRequest{Name: "prod-eu", Yes: true}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	after, err := models.LoadClusterAddons(git.files["cluster-addons/prod-eu.yaml"])
	if err != nil {
		t.Fatalf("assignment file does not parse: %v", err)
	}
	if !after.Addons["datadog"].Enabled {
		t.Error("an existing addon assignment was wiped by the takeover")
	}
}

func TestTakeoverClusterGit_RefusesANameThatCouldEscapeTheClustersFolder(t *testing.T) {
	git := v4Git()
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	_, err := orch.TakeoverClusterGit(context.Background(), TakeoverClusterRequest{
		Name: "../../engine/application",
		Yes:  true,
	})
	if err == nil {
		t.Fatal("a traversal name must be refused")
	}
	if _, ok := git.files[V4ManagedClustersPath]; ok {
		t.Error("nothing may be written for a refused name")
	}
	// The name is refused BEFORE the v4-repo probe, so the engine pin the
	// probe would read must still be exactly the fixture we seeded.
	if string(git.files[EnginePinPath]) != "apiVersion: argoproj.io/v1alpha1\nkind: Application\n" {
		t.Error("a refused takeover touched the engine pin")
	}
}

func TestTakeoverClusterGit_RequiresAName(t *testing.T) {
	orch := New(nil, defaultCreds(), newMockArgocd(), v4Git(), defaultGitOps(), defaultPaths(), nil)
	if _, err := orch.TakeoverClusterGit(context.Background(), TakeoverClusterRequest{Yes: true}); err == nil {
		t.Fatal("an empty cluster name must be refused")
	}
}

// TestFindOpenPRForCluster_NameMustEndThere — "prod" must not match the
// pull request for "prod-eu".
func TestFindOpenPRForCluster_NameMustEndThere(t *testing.T) {
	git := v4Git()
	git.prs = append(git.prs, &gitprovider.PullRequest{ID: 4, Title: "sharko: take over cluster prod-eu", URL: "u4"})
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
	if pr, err := orch.findOpenPRForCluster(context.Background(), "prod", "take over"); err != nil || pr != nil {
		t.Fatalf("prod matched another cluster's pull request: %+v, %v", pr, err)
	}
	if pr, _ := orch.findOpenPRForCluster(context.Background(), "prod-eu", "take over"); pr == nil || pr.ID != 4 {
		t.Fatalf("prod-eu did not find its own pull request: %+v", pr)
	}
	git.prs = append(git.prs, &gitprovider.PullRequest{ID: 5, Title: "sharko: register cluster prod (kubeconfig provider)", URL: "u5"})
	if pr, _ := orch.findOpenPRForCluster(context.Background(), "prod", "register"); pr == nil || pr.ID != 5 {
		t.Fatalf("a title with decoration after the name must still match: %+v", pr)
	}
}

// TestTakeoverClusterGit_DryRunSaysAPullRequestIsAlreadyOpen — the preview
// still shows the files, and says which pull request is already waiting.
func TestTakeoverClusterGit_DryRunSaysAPullRequestIsAlreadyOpen(t *testing.T) {
	git := v4Git()
	git.prs = append(git.prs, &gitprovider.PullRequest{ID: 9, Title: "sharko: take over cluster prod-eu", URL: "u9"})
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
	res, err := orch.TakeoverClusterGit(context.Background(), TakeoverClusterRequest{Name: "prod-eu", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.AlreadyWaiting || res.Git == nil || res.Git.PRID != 9 || res.DryRun == nil {
		t.Fatalf("dry run = %+v", res)
	}
}

// Round S, S5: an open adopt pull request would finish as an adopt, so a
// take over (real or dry run) refuses with the plain sentence and opens
// nothing. (Before, it took the adopt pull request as its own.)
func TestTakeoverClusterGit_OpenAdoptPR_IsRefused_RoundS(t *testing.T) {
	for _, dry := range []bool{false, true} {
		git := v4Git()
		git.prs = append(git.prs, &gitprovider.PullRequest{ID: 9, Title: "sharko: adopt cluster prod-eu", URL: "https://github.com/example/repo/pull/9"})
		orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
		_, err := orch.TakeoverClusterGit(context.Background(), TakeoverClusterRequest{Name: "prod-eu", DryRun: dry, Yes: true})
		var other *OtherJoinPRError
		if !errors.As(err, &other) || other.PRID != 9 || other.Kind != "adopt" || other.PRUrl != "https://github.com/example/repo/pull/9" {
			t.Fatalf("dry=%v: err = %v", dry, err)
		}
		if want := "A pull request to adopt prod-eu is already open. Merge or close it first."; err.Error() != want {
			t.Fatalf("dry=%v: message %q", dry, err.Error())
		}
		if len(git.prs) != 1 {
			t.Fatalf("dry=%v: a second pull request was opened", dry)
		}
	}
}

// Round S, S7: the take over mark goes on before the pull request is
// tracked, with the label choice; a re-run that finds its own open pull
// request puts a lost mark back; a mark that cannot go on is
// ErrMarkNotWritten and the pull request is not merged.
func TestTakeoverClusterGit_WaitingMark_RoundS(t *testing.T) {
	newOrch := func() (*Orchestrator, *mockGitProvider, *mockArgoSecretManager) {
		git := v4Git()
		asm := newMockArgoSecretManager()
		orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
		orch.SetArgoSecretManager(asm, "")
		return orch, git, asm
	}

	t.Run("on before tracking", func(t *testing.T) {
		orch, _, asm := newOrch()
		seen := ""
		orch.SetPRTracker(prTrackerFunc(func(_ context.Context, p TrackedPR) error {
			seen = asm.annotations["prod-eu"][AnnotationTakeoverPendingPR]
			return nil
		}))
		if _, err := orch.TakeoverClusterGit(context.Background(), TakeoverClusterRequest{Name: "prod-eu", Yes: true}); err != nil {
			t.Fatal(err)
		}
		m, ok := models.ParsePendingPRMark(seen)
		if !ok || m.PR == 0 || m.KeepOldLabels == nil || *m.KeepOldLabels || m.At == "" {
			t.Fatalf("mark when the pull request was tracked = %q", seen)
		}
	})

	t.Run("re-run puts it back", func(t *testing.T) {
		orch, git, asm := newOrch()
		git.prs = append(git.prs, &gitprovider.PullRequest{ID: 4, Title: "sharko: take over cluster prod-eu", URL: "https://github.com/example/repo/pull/4"})
		res, err := orch.TakeoverClusterGit(context.Background(), TakeoverClusterRequest{Name: "prod-eu", Yes: true, PreserveLegacyLabels: true})
		if err != nil || !res.AlreadyWaiting {
			t.Fatalf("res %+v err %v", res, err)
		}
		m, ok := models.ParsePendingPRMark(asm.annotations["prod-eu"][AnnotationTakeoverPendingPR])
		if !ok || m.PR != 4 || m.Repo != "example/repo" || m.KeepOldLabels == nil || !*m.KeepOldLabels {
			t.Fatalf("mark = %q", asm.annotations["prod-eu"][AnnotationTakeoverPendingPR])
		}
	})

	t.Run("cannot go on", func(t *testing.T) {
		orch, git, asm := newOrch()
		gitops := defaultGitOps()
		gitops.PRAutoMerge = true
		orch.gitops = gitops
		tracker := &stubPRTracker{}
		orch.SetPRTracker(tracker)
		asm.annotationErr = errors.New("secret update refused")
		res, err := orch.TakeoverClusterGit(context.Background(), TakeoverClusterRequest{Name: "prod-eu", Yes: true})
		if !errors.Is(err, ErrMarkNotWritten) || res == nil || res.Git == nil || res.Git.Merged {
			t.Fatalf("res %+v err %v", res, err)
		}
		if git.mergeCalls != 0 {
			t.Fatalf("merged %d times; it must wait for the mark", git.mergeCalls)
		}
		if len(tracker.snapshot()) != 1 {
			t.Fatal("the pull request must still be tracked")
		}
	})
}

package orchestrator

import (
	"context"
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
	res, err := orch.TakeoverClusterGit(context.Background(), TakeoverClusterRequest{Name: "prod-eu", Yes: true})
	if err != nil {
		t.Fatalf("second takeover: %v", err)
	}
	if !res.AlreadyInFleet {
		t.Error("a repeat takeover must report that the cluster is already in the fleet")
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

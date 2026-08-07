package orchestrator

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// unadoptV4Fixture builds a v4 repo with an adopted, fully-populated
// cluster: a fleet entry, an addon-assignment file, and two per-cluster
// values files — everything a real "take over then enable a couple of
// addons" history would leave behind.
func unadoptV4Fixture(t *testing.T) *mockGitProvider {
	t.Helper()
	git := adoptV4Git() // engine pin present -> v4
	spec := models.ManagedClustersSpec{Clusters: []models.ManagedClusterEntry{
		{Name: "prod-eu"},
		{Name: "prod-us"}, // a second cluster that must survive untouched
	}}
	body, err := models.SaveManagedClusters(spec)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	git.files[V4ManagedClustersPath] = body

	addons, err := models.SaveClusterAddons(models.ClusterAddonsSpec{
		Cluster: "prod-eu",
		Addons:  map[string]models.ClusterAddonsAddon{"datadog": {Enabled: true}},
	})
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	git.files["cluster-addons/prod-eu.yaml"] = addons
	git.files["values/clusters/prod-eu/datadog.yaml"] = []byte("datadog:\n  enabled: true\n")
	git.files["values/clusters/prod-eu/keda.yaml"] = []byte("keda:\n  minReplicas: 1\n")
	return git
}

func TestUnadoptCluster_V4_RemovesEverythingThisClusterOwns(t *testing.T) {
	git := unadoptV4Fixture(t)
	asm := newMockArgoSecretManager()
	asm.annotations["prod-eu"] = map[string]string{AnnotationAdopted: "true"}

	orch := New(nil, nil, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	result, err := orch.UnadoptCluster(context.Background(), "prod-eu", UnadoptClusterRequest{Yes: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s (error: %s)", result.Status, result.Error)
	}
	if len(asm.unadopted) != 1 || asm.unadopted[0] != "prod-eu" {
		t.Errorf("expected ArgoCD unadopt call for prod-eu, got %v", asm.unadopted)
	}

	wantDeleted := []string{
		"cluster-addons/prod-eu.yaml",
		"values/clusters/prod-eu/datadog.yaml",
		"values/clusters/prod-eu/keda.yaml",
	}
	gotDeleted := append([]string(nil), git.deletedFiles...)
	sort.Strings(gotDeleted)
	sort.Strings(wantDeleted)
	if fmt.Sprint(gotDeleted) != fmt.Sprint(wantDeleted) {
		t.Errorf("deleted files = %v, want %v", gotDeleted, wantDeleted)
	}

	spec, err := models.LoadManagedClusters(git.files[V4ManagedClustersPath])
	if err != nil {
		t.Fatalf("fleet record does not parse: %v", err)
	}
	names := map[string]bool{}
	for _, c := range spec.Clusters {
		names[c.Name] = true
	}
	if names["prod-eu"] {
		t.Error("prod-eu should have been removed from managed-clusters.yaml")
	}
	if !names["prod-us"] {
		t.Error("prod-us must survive untouched")
	}
}

// TestUnadoptCluster_V4_ListDirectoryFailure_FailsClosed pins the fail-
// closed contract on the values-folder listing: a failure that is NOT
// "the folder does not exist" must abort the whole unadopt — never delete
// fewer files than it should have — and must not mutate ArgoCD state at
// all (the read happens before Step 2's Unadopt call).
func TestUnadoptCluster_V4_ListDirectoryFailure_FailsClosed(t *testing.T) {
	git := unadoptV4Fixture(t)
	git.listDirErr = fmt.Errorf("git host rate limited this request")
	asm := newMockArgoSecretManager()
	asm.annotations["prod-eu"] = map[string]string{AnnotationAdopted: "true"}

	orch := New(nil, nil, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	_, err := orch.UnadoptCluster(context.Background(), "prod-eu", UnadoptClusterRequest{Yes: true})
	if err == nil {
		t.Fatal("expected an error when the values-folder listing fails")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("expected the upstream error to surface, got: %v", err)
	}
	if len(git.deletedFiles) != 0 {
		t.Errorf("nothing may be deleted when the listing failed, got %v", git.deletedFiles)
	}
	if len(asm.unadopted) != 0 {
		t.Error("ArgoCD must not be mutated when the pre-write listing failed")
	}
	if _, ok := git.files[V4ManagedClustersPath]; !ok {
		t.Error("managed-clusters.yaml must be untouched, not merely unwritten-to")
	}
}

// TestUnadoptCluster_V4_IdempotentReRun — a second unadopt call on an
// already-unadopted cluster must not error: the fleet entry and every file
// are already gone, so there is simply nothing left to remove.
func TestUnadoptCluster_V4_IdempotentReRun(t *testing.T) {
	git := unadoptV4Fixture(t)
	asm := newMockArgoSecretManager()
	asm.annotations["prod-eu"] = map[string]string{AnnotationAdopted: "true"}

	orch := New(nil, nil, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	if _, err := orch.UnadoptCluster(context.Background(), "prod-eu", UnadoptClusterRequest{Yes: true}); err != nil {
		t.Fatalf("first unadopt: %v", err)
	}

	// Re-run against the now-cleaned-up repo. The adopted annotation is
	// still whatever the mock last had it as (Unadopt doesn't clear it in
	// this fake), so the annotation check still passes — the interesting
	// assertion is that the git-side re-run does not error even though
	// every file it would have deleted is already gone.
	result, err := orch.UnadoptCluster(context.Background(), "prod-eu", UnadoptClusterRequest{Yes: true})
	if err != nil {
		t.Fatalf("second unadopt: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected the re-run to succeed as a no-op, got %s (error: %s)", result.Status, result.Error)
	}
}

func TestUnadoptCluster_V4_DryRun_ListsEveryFileAndWritesNothing(t *testing.T) {
	git := unadoptV4Fixture(t)
	asm := newMockArgoSecretManager()
	asm.annotations["prod-eu"] = map[string]string{AnnotationAdopted: "true"}

	orch := New(nil, nil, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	result, err := orch.UnadoptCluster(context.Background(), "prod-eu", UnadoptClusterRequest{DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.DryRun == nil {
		t.Fatal("expected a dry-run preview")
	}
	// managed-clusters.yaml update + 3 deletes.
	if len(result.DryRun.FilesToWrite) != 4 {
		t.Fatalf("expected 4 file previews, got %d: %+v", len(result.DryRun.FilesToWrite), result.DryRun.FilesToWrite)
	}
	if len(git.deletedFiles) != 0 || len(asm.unadopted) != 0 {
		t.Error("a dry run must not delete anything or touch ArgoCD")
	}
	if _, ok := git.files["cluster-addons/prod-eu.yaml"]; !ok {
		t.Error("a dry run must not actually delete files")
	}
}

// TestUnadoptCluster_V3Repo_UnaffectedByV4Unadopt is the regression guard:
// a v3 repo keeps writing configuration/managed-clusters.yaml and deleting
// the combined values file, exactly as before.
func TestUnadoptCluster_V3Repo_UnaffectedByV4Unadopt(t *testing.T) {
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: cluster-a\n    labels: {}\n")
	asm := newMockArgoSecretManager()
	asm.annotations["cluster-a"] = map[string]string{AnnotationAdopted: "true"}

	orch := New(nil, nil, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	result, err := orch.UnadoptCluster(context.Background(), "cluster-a", UnadoptClusterRequest{Yes: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s (error: %s)", result.Status, result.Error)
	}
	valuesPath := defaultPaths().ClusterValues + "/cluster-a.yaml"
	found := false
	for _, p := range git.deletedFiles {
		if p == valuesPath {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the v3 combined values file %s to be deleted, got %v", valuesPath, git.deletedFiles)
	}
}

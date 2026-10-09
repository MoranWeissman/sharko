package orchestrator

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// removeV4Fixture builds a v4 repo with an adopted, fully-populated cluster
// — the same shape unadoptV4Fixture (unadopt_v4_test.go) builds, since a
// full removal and a full unadopt delete the exact same set of files
// (remove.go reuses buildUnadoptV4Plan directly). A second cluster is
// present so every test can assert it survives untouched.
func removeV4Fixture(t *testing.T) *mockGitProvider {
	t.Helper()
	git := adoptV4Git() // engine pin present -> v4
	spec := models.ManagedClustersSpec{Clusters: []models.ManagedClusterEntry{
		{Name: "prod-eu"},
		{Name: "prod-us"},
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

// TestRemoveCluster_V4_AllCleanup_DeletesEverythingAndArgoCDSecret is the
// happy path: cleanup=all on a v4 repo removes the fleet entry, deletes the
// addon-assignment file and every per-cluster values file, and — because
// the ArgoCD cluster Secret carries Sharko's ownership label — deletes the
// ArgoCD cluster Secret too.
func TestRemoveCluster_V4_AllCleanup_DeletesEverythingAndArgoCDSecret(t *testing.T) {
	git := removeV4Fixture(t)
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "prod-eu", Server: "https://k8s.example.com:6443"},
	}
	mgr := newMockArgoSecretManager()
	mgr.managedByLabel["prod-eu"] = "sharko"

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(mgr, "")

	result, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
		Name:    "prod-eu",
		Cleanup: "all",
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s (error: %s)", result.Status, result.Error)
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

	if len(argocd.deletedServers) != 1 {
		t.Fatalf("expected the ArgoCD cluster Secret to be deleted, got %v", argocd.deletedServers)
	}
	foundDelete := false
	for _, s := range result.CompletedSteps {
		if s == "delete_argocd_cluster" {
			foundDelete = true
		}
		if s == "delete_cluster_files" {
			// present as expected — the v4 equivalent of v3's
			// delete_values_file step.
		}
	}
	if !foundDelete {
		t.Fatalf("expected delete_argocd_cluster step, got %v", result.CompletedSteps)
	}
}

// TestRemoveCluster_V4_DryRun_ShowsRealDiffs pins the dry-run preview: the
// registry update and every file delete must carry a REAL diff (the old
// content, marked as removed), not an empty placeholder, and nothing may
// actually be written.
func TestRemoveCluster_V4_DryRun_ShowsRealDiffs(t *testing.T) {
	git := removeV4Fixture(t)
	argocd := newMockArgocd()
	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
		Name:    "prod-eu",
		Cleanup: "all",
		DryRun:  true,
	})
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

	byPath := map[string]FilePreview{}
	for _, p := range result.DryRun.FilesToWrite {
		byPath[p.Path] = p
	}

	registryPreview, ok := byPath[V4ManagedClustersPath]
	if !ok {
		t.Fatalf("expected a preview for %s", V4ManagedClustersPath)
	}
	if registryPreview.Action != "update" {
		t.Errorf("registry preview action = %q, want update", registryPreview.Action)
	}
	if !strings.Contains(registryPreview.Diff, "-") || !strings.Contains(registryPreview.Diff, "prod-eu") {
		t.Errorf("registry diff does not show the removed entry: %q", registryPreview.Diff)
	}

	addonsPreview, ok := byPath["cluster-addons/prod-eu.yaml"]
	if !ok {
		t.Fatalf("expected a delete preview for cluster-addons/prod-eu.yaml")
	}
	if addonsPreview.Action != "delete" {
		t.Errorf("cluster-addons preview action = %q, want delete", addonsPreview.Action)
	}
	if !strings.Contains(addonsPreview.Diff, "datadog") {
		t.Errorf("cluster-addons delete diff does not show the real content being removed: %q", addonsPreview.Diff)
	}

	valuesPreview, ok := byPath["values/clusters/prod-eu/datadog.yaml"]
	if !ok {
		t.Fatalf("expected a delete preview for the datadog values file")
	}
	if !strings.Contains(valuesPreview.Diff, "enabled: true") {
		t.Errorf("values delete diff does not show the real content being removed: %q", valuesPreview.Diff)
	}

	if len(git.deletedFiles) != 0 {
		t.Error("a dry run must not delete anything")
	}
	if len(git.prs) != 0 {
		t.Error("a dry run must not open a pull request")
	}
	if _, ok := git.files["cluster-addons/prod-eu.yaml"]; !ok {
		t.Error("a dry run must not actually delete files")
	}
}

// TestRemoveCluster_V4_ListDirectoryFailure_FailsClosed pins the
// fail-closed contract on the values-folder listing for cleanup=all: a
// listing failure that is NOT "the folder does not exist" must abort the
// whole removal — never delete fewer files than it should have — and must
// not touch ArgoCD or the fleet record at all.
func TestRemoveCluster_V4_ListDirectoryFailure_FailsClosed(t *testing.T) {
	git := removeV4Fixture(t)
	git.listDirErr = fmt.Errorf("git host rate limited this request")
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "prod-eu", Server: "https://k8s.example.com:6443"},
	}
	mgr := newMockArgoSecretManager()
	mgr.managedByLabel["prod-eu"] = "sharko"
	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(mgr, "")

	_, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
		Name:    "prod-eu",
		Cleanup: "all",
		Yes:     true,
	})
	if err == nil {
		t.Fatal("expected an error when the values-folder listing fails")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("expected the upstream error to surface, got: %v", err)
	}
	if len(git.deletedFiles) != 0 {
		t.Errorf("nothing may be deleted when the listing failed, got %v", git.deletedFiles)
	}
	if len(git.prs) != 0 {
		t.Error("no pull request may be opened when the listing failed")
	}
	if len(argocd.deletedServers) != 0 {
		t.Error("ArgoCD must not be touched when the pre-write listing failed")
	}
	if _, ok := git.files[V4ManagedClustersPath]; !ok {
		t.Error("managed-clusters.yaml must be untouched, not merely unwritten-to")
	}
}

// TestRemoveCluster_V4_NoneCleanup_SkipsEnumeration_EvenOnListingFailure —
// cleanup=none never looks at the values folder at all, so an unrelated
// ListDirectory failure must not block a registry-only removal.
func TestRemoveCluster_V4_NoneCleanup_SkipsEnumeration_EvenOnListingFailure(t *testing.T) {
	git := removeV4Fixture(t)
	git.listDirErr = fmt.Errorf("git host rate limited this request")
	orch := New(nil, nil, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
		Name:    "prod-eu",
		Cleanup: "none",
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("cleanup=none must not be blocked by an unrelated listing failure: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s (error: %s)", result.Status, result.Error)
	}
	if len(git.deletedFiles) != 0 {
		t.Errorf("cleanup=none must not delete any file, got %v", git.deletedFiles)
	}
	spec, err := models.LoadManagedClusters(git.files[V4ManagedClustersPath])
	if err != nil {
		t.Fatalf("fleet record does not parse: %v", err)
	}
	for _, c := range spec.Clusters {
		if c.Name == "prod-eu" {
			t.Error("prod-eu should have been removed from managed-clusters.yaml")
		}
	}
}

// TestRemoveCluster_V4_IdempotentOnPartiallyRemovedState — a retry against a
// repo where a previous, partially-completed removal already deleted some
// (but not all) of the cluster's files and already dropped the fleet entry
// must not error: there is simply less left to remove.
func TestRemoveCluster_V4_IdempotentOnPartiallyRemovedState(t *testing.T) {
	git := removeV4Fixture(t)
	// Simulate a partially-completed prior removal: the fleet entry and the
	// addon-assignment file are already gone, but one values file survived.
	spec := models.ManagedClustersSpec{Clusters: []models.ManagedClusterEntry{{Name: "prod-us"}}}
	body, err := models.SaveManagedClusters(spec)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	git.files[V4ManagedClustersPath] = body
	delete(git.files, "cluster-addons/prod-eu.yaml")
	delete(git.files, "values/clusters/prod-eu/keda.yaml") // already deleted
	// values/clusters/prod-eu/datadog.yaml is still there.

	argocd := newMockArgocd()
	mgr := newMockArgoSecretManager() // no managed-by label — Secret already handled/absent
	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(mgr, "")

	result, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
		Name:    "prod-eu",
		Cleanup: "all",
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("a retry against a partially-removed cluster must not error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected the retry to succeed, got %s (error: %s)", result.Status, result.Error)
	}
	if len(git.deletedFiles) != 1 || git.deletedFiles[0] != "values/clusters/prod-eu/datadog.yaml" {
		t.Errorf("expected only the one remaining file to be deleted, got %v", git.deletedFiles)
	}
	spec2, err := models.LoadManagedClusters(git.files[V4ManagedClustersPath])
	if err != nil {
		t.Fatalf("fleet record does not parse: %v", err)
	}
	if len(spec2.Clusters) != 1 || spec2.Clusters[0].Name != "prod-us" {
		t.Errorf("prod-us must survive untouched, got %+v", spec2.Clusters)
	}
}

// TestRemoveCluster_V4_RequiresConfirmation mirrors the v3 confirmation
// pin (TestRemoveCluster_RequiresConfirmation) on a v4 repo: without
// yes: true nothing is read for a plan, nothing is written, and the error
// says so in plain words.
func TestRemoveCluster_V4_RequiresConfirmation(t *testing.T) {
	git := removeV4Fixture(t)
	orch := New(nil, nil, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
		Name:    "prod-eu",
		Cleanup: "all",
		Yes:     false,
	})
	if err == nil {
		t.Fatal("expected error for missing confirmation")
	}
	if !strings.Contains(err.Error(), "confirmation required") {
		t.Errorf("unexpected error: %v", err)
	}
	if len(git.deletedFiles) != 0 || len(git.prs) != 0 {
		t.Errorf("nothing may be written without confirmation: deleted=%v prs=%d", git.deletedFiles, len(git.prs))
	}
}

// TestRemoveCluster_V3Repo_UnaffectedByV4Removal is the regression guard:
// a v3 repo keeps writing configuration/managed-clusters.yaml and deleting
// the combined values file exactly as before — the v4 probe added in
// remove.go must be inert on a v3 repo.
func TestRemoveCluster_V3Repo_UnaffectedByV4Removal(t *testing.T) {
	git := newMockGitProvider() // no engine pin => v3 repo
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: cluster-a\n    labels: {}\n")

	orch := New(nil, nil, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
		Name:    "cluster-a",
		Cleanup: "all",
		Yes:     true,
	})
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

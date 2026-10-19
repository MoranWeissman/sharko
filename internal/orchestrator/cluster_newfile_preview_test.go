package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// TestRegisterCluster_NewFilePreview verifies that when the managed-clusters.yaml
// file does not exist yet, the preview shows "@@ -0,0 +1,N @@" (create) instead
// of "@@ -1,1 +1,N @@" (edit). This test was added for VF-7.
func TestRegisterCluster_NewFilePreview(t *testing.T) {
	// Set up orchestrator with mock Git provider that returns "not found" for
	// the managed-clusters file.
	git := &mockGitProvider{
		files: map[string][]byte{
			// No managed-clusters.yaml present
		},
	}
	argocd := &mockArgocd{
		registeredClusters: make(map[string]string),
	}

	o := &Orchestrator{
		git:    git,
		argocd: argocd,
		gitops: GitOpsConfig{
			BaseBranch: "main",
		},
		paths: RepoPathsConfig{
			ManagedClusters: "configuration/managed-clusters.yaml",
		},
	}

	req := RegisterClusterRequest{
		Name:   "test-cluster",
		Region: "us-east-1",
		Addons: map[string]bool{},
		DryRun: true,
	}

	result, err := o.RegisterCluster(context.Background(), req)
	if err != nil {
		t.Fatalf("RegisterCluster failed: %v", err)
	}

	// Find the managed-clusters preview
	if result.DryRun == nil {
		t.Fatal("expected DryRun result, got nil")
	}

	var preview *FilePreview
	for i := range result.DryRun.FilesToWrite {
		if result.DryRun.FilesToWrite[i].Path == "configuration/managed-clusters.yaml" {
			preview = &result.DryRun.FilesToWrite[i]
			break
		}
	}
	if preview == nil {
		t.Fatal("managed-clusters.yaml preview not found")
	}

	// Verify action is "create"
	if preview.Action != "create" {
		t.Errorf("expected action=create, got %q", preview.Action)
	}

	// Verify diff header shows @@ -0,0 +1,N @@ for a new file
	// Extract the header line
	lines := strings.Split(preview.Diff, "\n")
	var headerLine string
	for _, line := range lines {
		if strings.HasPrefix(line, "@@") {
			headerLine = line
			break
		}
	}
	if headerLine == "" {
		t.Fatalf("no diff header found in preview.Diff:\n%s", preview.Diff)
	}

	// Check the exact header: @@ -0,0 +1,N @@, N = number of added lines.
	if want := newFileHunkHeader(t, preview.Diff); headerLine != want {
		t.Errorf("expected diff header %q for new file, got: %q", want, headerLine)
	}

	// Ensure the old wrong header is NOT present
	if strings.Contains(headerLine, "@@ -1,1 +1,") {
		t.Errorf("diff header should not show '@@ -1,1 +1,N @@' for a new file, got: %s", headerLine)
	}

	// Verify that all content lines are additions (start with +)
	for i, line := range lines[1:] { // the first line is the header
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "@@") {
			t.Errorf("line %d: expected all non-header lines to start with '+' for create action, got: %q", i+2, line)
		}
	}
}

// newFileHunkHeader counts the added lines in a unified diff and returns the
// one header a brand-new file must have: "@@ -0,0 +1,N @@". It fails the test
// if the diff has fewer than two added lines, because GNU diff writes a
// one-line hunk as "+1" with no count and this helper does not cover that.
func newFileHunkHeader(t *testing.T, diff string) string {
	t.Helper()
	added := 0
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			added++
		}
	}
	if added < 2 {
		t.Fatalf("expected at least two added lines in the diff, got %d:\n%s", added, diff)
	}
	return fmt.Sprintf("@@ -0,0 +1,%d @@", added)
}

// findPreview returns the preview for path, or nil.
func findPreview(previews []FilePreview, path string) *FilePreview {
	for i := range previews {
		if previews[i].Path == path {
			return &previews[i]
		}
	}
	return nil
}

// firstHunkHeader returns the first "@@" line of a diff.
func firstHunkHeader(t *testing.T, diff string) string {
	t.Helper()
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "@@") {
			return line
		}
	}
	t.Fatalf("no diff header found in:\n%s", diff)
	return ""
}

// TestUpdateClusterAddons_NewFilePreview covers the brand-new-file preview in
// UpdateClusterAddons (VF-7). The cluster is registered in
// managed-clusters.yaml but has no values file yet. The catalog is the
// default test catalog, so the addon is known and the call really reaches
// the preview.
//
// What must be true: the values file is shown as "create" with exactly
// "@@ -0,0 +1,N @@", and every line in it is an added line.
//
// (When managed-clusters.yaml itself is missing, the dry run stops with the
// same failed result as the real run — see
// TestUpdateClusterAddons_DryRunMatchesRealRunWhenFileMissing.)
func TestUpdateClusterAddons_NewFilePreview(t *testing.T) {
	git := newMockGitProvider() // seeds configuration/addons-catalog.yaml (has keda)
	if _, ok := git.files["configuration/addons-catalog.yaml"]; !ok {
		t.Fatal("test setup: the mock has no addon catalog")
	}
	git.files["configuration/managed-clusters.yaml"] = []byte(managedClustersWithKedaEnabled)
	o := setupUpdateClusterAddonsOrch(git)

	result, err := o.UpdateClusterAddons(context.Background(), "test", "https://k8s.example.com", "us-east-1",
		map[string]bool{"keda": true}, nil, true)
	if err != nil {
		t.Fatalf("UpdateClusterAddons dry-run failed: %v", err)
	}
	if result.DryRun == nil {
		t.Fatalf("expected a dry-run result, got status %q error %q", result.Status, result.Error)
	}

	valuesPath := "configuration/addons-clusters-values/test.yaml"
	values := findPreview(result.DryRun.FilesToWrite, valuesPath)
	if values == nil {
		t.Fatalf("no preview for %s; got %+v", valuesPath, result.DryRun.FilesToWrite)
	}
	if values.Action != "create" {
		t.Errorf("values file: expected action \"create\", got %q", values.Action)
	}
	if got, want := firstHunkHeader(t, values.Diff), newFileHunkHeader(t, values.Diff); got != want {
		t.Errorf("values file: expected diff header %q, got %q\n%s", want, got, values.Diff)
	}
	for i, line := range strings.Split(values.Diff, "\n") {
		if line == "" || strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "@@") {
			continue
		}
		if !strings.HasPrefix(line, "+") {
			t.Errorf("values file line %d: a new file can only have added lines, got %q", i+1, line)
		}
	}
}

// TestUpdateClusterAddons_ExistingManagedClustersPreview is the other half:
// when managed-clusters.yaml exists, its preview says "update" and its diff
// starts from the real old file, not from an empty one.
func TestUpdateClusterAddons_ExistingManagedClustersPreview(t *testing.T) {
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte(managedClustersWithKedaEnabled)
	git.files["configuration/addons-clusters-values/test.yaml"] = []byte("clusterGlobalValues:\n")
	o := setupUpdateClusterAddonsOrch(git)

	result, err := o.UpdateClusterAddons(context.Background(), "test", "https://k8s.example.com", "",
		map[string]bool{"keda": false}, nil, true)
	if err != nil {
		t.Fatalf("UpdateClusterAddons dry-run failed: %v", err)
	}
	if result.DryRun == nil {
		t.Fatalf("expected a dry-run result, got status %q error %q", result.Status, result.Error)
	}

	mc := findPreview(result.DryRun.FilesToWrite, "configuration/managed-clusters.yaml")
	if mc == nil {
		t.Fatalf("no managed-clusters.yaml preview; got %+v", result.DryRun.FilesToWrite)
	}
	if mc.Action != "update" {
		t.Errorf("expected action \"update\" for an existing file, got %q", mc.Action)
	}
	header := firstHunkHeader(t, mc.Diff)
	if strings.HasPrefix(header, "@@ -0,0 ") {
		t.Errorf("an existing file must not get a new-file header, got %q", header)
	}
	// The old file's lines must show up as removed or kept, which only
	// happens when the diff starts from the real old file.
	var removedOld, addedDisabled bool
	for _, line := range strings.Split(mc.Diff, "\n") {
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") && strings.TrimSpace(strings.TrimPrefix(line, "-")) == "keda: enabled" {
			removedOld = true
		}
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") && strings.TrimSpace(strings.TrimPrefix(line, "+")) == "keda: disabled" {
			addedDisabled = true
		}
	}
	if !removedOld || !addedDisabled {
		t.Errorf("expected the diff to remove an old \"keda: enabled\" line and add \"keda: disabled\":\n%s", mc.Diff)
	}
}

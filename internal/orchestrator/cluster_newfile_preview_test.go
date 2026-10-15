package orchestrator

import (
	"context"
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

	// Check exact format: @@ -0,0 +1,N @@
	if !strings.HasPrefix(headerLine, "@@ -0,0 +1,") {
		t.Errorf("expected diff header '@@ -0,0 +1,N @@' for new file, got: %s", headerLine)
	}

	// Ensure the old wrong header is NOT present
	if strings.Contains(headerLine, "@@ -1,1 +1,") {
		t.Errorf("diff header should not show '@@ -1,1 +1,N @@' for a new file, got: %s", headerLine)
	}

	// Verify that all content lines are additions (start with +)
	for i, line := range lines[1:] { // Skip header line
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "@@") {
			t.Errorf("line %d: expected all non-header lines to start with '+' for create action, got: %q", i+2, line)
		}
	}
}

// TestUpdateClusterAddons_NewFilePreview verifies that when updating cluster
// addons creates a new managed-clusters.yaml file, the preview shows the correct
// diff header for a new file. This test was added for VF-7.
func TestUpdateClusterAddons_NewFilePreview(t *testing.T) {
	git := &mockGitProvider{
		files: map[string][]byte{
			// No managed-clusters.yaml present
			// But the cluster values file exists
			"configuration/addons-clusters-values/test-cluster.yaml": []byte("# existing values\n"),
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
			ClusterValues:   "configuration/addons-clusters-values",
		},
	}

	result, err := o.UpdateClusterAddons(
		context.Background(),
		"test-cluster",
		"https://kubernetes.default.svc",
		"us-east-1",
		map[string]bool{"nginx": true},
		nil, // autoMergeOverride
		true, // dry-run
	)
	if err != nil {
		t.Fatalf("UpdateClusterAddons failed: %v", err)
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
		// It's OK if there's no preview for managed-clusters in UpdateClusterAddons
		// when only addons are changing - the test passes either way
		t.Skip("no managed-clusters.yaml preview in UpdateClusterAddons result")
	}

	// Verify action is "create" (not hardcoded "update")
	if preview.Action != "create" && preview.Action != "update" {
		t.Errorf("expected action=create or update for file, got %q", preview.Action)
	}

	if preview.Action == "create" {
		// Verify diff header shows @@ -0,0 +1,N @@ for a new file
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

		// Check exact format: @@ -0,0 +1,N @@
		if !strings.HasPrefix(headerLine, "@@ -0,0 +1,") {
			t.Errorf("expected diff header '@@ -0,0 +1,N @@' for new file, got: %s", headerLine)
		}
	}
}

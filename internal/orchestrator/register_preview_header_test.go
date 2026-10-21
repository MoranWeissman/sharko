package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// TestRegisterCluster_V4EmptyRepo_PreviewMatchesWrittenFile is the story G1
// guard. On a v4 repo with no managed-clusters.yaml yet, the Register
// cluster preview must show exactly the text the pull request writes: the
// three plain-English header lines included, and the same number of lines.
//
// Before the fix the preview was built by its own copy of the code that
// left the header out, so the preview said "@@ -0,0 +1,6 @@" while the pull
// request added 9 lines.
func TestRegisterCluster_V4EmptyRepo_PreviewMatchesWrittenFile(t *testing.T) {
	git := newMockGitProvider()
	git.files[EnginePinPath] = []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n")
	if _, ok := git.files[V4ManagedClustersPath]; ok {
		t.Fatalf("test setup: %s must not exist yet", V4ManagedClustersPath)
	}
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	req := RegisterClusterRequest{Name: "prod-eu", Region: "eu-west-1"}

	// 1. The preview.
	dryReq := req
	dryReq.DryRun = true
	dry, err := orch.RegisterCluster(context.Background(), dryReq)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if dry.DryRun == nil {
		t.Fatalf("dry run: no preview, status %q error %q", dry.Status, dry.Error)
	}
	preview := findPreview(dry.DryRun.FilesToWrite, V4ManagedClustersPath)
	if preview == nil {
		t.Fatalf("no preview for %s; got %+v", V4ManagedClustersPath, dry.DryRun.FilesToWrite)
	}
	if preview.Action != "create" {
		t.Errorf("preview action = %q, want \"create\"", preview.Action)
	}
	if _, ok := git.files[V4ManagedClustersPath]; ok {
		t.Fatal("the dry run wrote managed-clusters.yaml")
	}

	// 2. The real write, on the same (still empty) repo.
	if _, err := orch.RegisterCluster(context.Background(), req); err != nil {
		t.Fatalf("real run: %v", err)
	}
	written, ok := git.files[V4ManagedClustersPath]
	if !ok {
		t.Fatalf("real run did not write %s; files: %v", V4ManagedClustersPath, keysOf(git.files))
	}

	// The written file opens with the three header lines, word for word.
	wantHeader := []string{
		"# The clusters Sharko manages.",
		"# Registering or removing a cluster through Sharko connects it to ArgoCD, or disconnects it.",
		"# Day-to-day addon changes never touch this file — see " + V4ClustersDir + "/<cluster>.yaml for that.",
	}
	writtenLines := strings.Split(strings.TrimSuffix(string(written), "\n"), "\n")
	if len(writtenLines) < len(wantHeader) {
		t.Fatalf("written file too short:\n%s", written)
	}
	for i, want := range wantHeader {
		if writtenLines[i] != want {
			t.Errorf("written line %d = %q, want %q", i+1, writtenLines[i], want)
		}
	}

	// The preview's added lines are exactly the written file's lines.
	var added []string
	for _, line := range strings.Split(preview.Diff, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			added = append(added, strings.TrimPrefix(line, "+"))
		}
	}
	if strings.Join(added, "\n") != strings.Join(writtenLines, "\n") {
		t.Errorf("preview does not show what the pull request writes.\npreview added lines (%d):\n%s\n\nwritten file (%d lines):\n%s",
			len(added), strings.Join(added, "\n"), len(writtenLines), written)
	}

	// And the hunk header counts the same number of lines as the file.
	if got, want := firstHunkHeader(t, preview.Diff), fmt.Sprintf("@@ -0,0 +1,%d @@", len(writtenLines)); got != want {
		t.Errorf("preview hunk header = %q, want %q", got, want)
	}
}

// TestRegisterCluster_V4ExistingRegistry_NoHeaderAdded is the other half:
// headers ride creation only. When managed-clusters.yaml already exists,
// neither the preview nor the written file gets the header.
func TestRegisterCluster_V4ExistingRegistry_NoHeaderAdded(t *testing.T) {
	git := newMockGitProvider()
	git.files[EnginePinPath] = []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n")
	git.files[V4ManagedClustersPath] = []byte("clusters:\n  - name: other\n")
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	dry, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{Name: "prod-eu", DryRun: true})
	if err != nil || dry.DryRun == nil {
		t.Fatalf("dry run: err=%v result=%+v", err, dry)
	}
	preview := findPreview(dry.DryRun.FilesToWrite, V4ManagedClustersPath)
	if preview == nil {
		t.Fatalf("no preview for %s", V4ManagedClustersPath)
	}
	if strings.Contains(preview.Diff, "# The clusters Sharko manages.") {
		t.Errorf("preview of an existing file must not add the header:\n%s", preview.Diff)
	}
	if _, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{Name: "prod-eu"}); err != nil {
		t.Fatalf("real run: %v", err)
	}
	if strings.Contains(string(git.files[V4ManagedClustersPath]), "# The clusters Sharko manages.") {
		t.Errorf("an existing file must not be given the header:\n%s", git.files[V4ManagedClustersPath])
	}
}

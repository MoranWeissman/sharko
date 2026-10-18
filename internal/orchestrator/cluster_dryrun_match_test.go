package orchestrator

// cluster_dryrun_match_test.go — a dry run must give the same answer as the
// real run, and only a real "file not found" from Git counts as a missing
// file. Before, a dry run of UpdateClusterAddons said "success" when
// managed-clusters.yaml was missing (the real run said "failed"), and any Git
// error at all was read as "the file is missing".

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

const mcPath = "configuration/managed-clusters.yaml"

// errGitDown is a Git error that is NOT "file not found".
var errGitDown = errors.New("the git host did not answer")

// failReadGit is the mock Git provider, but reading one chosen path fails
// with a chosen error.
type failReadGit struct {
	*mockGitProvider
	path string
	err  error
}

func (f *failReadGit) GetFileContent(ctx context.Context, p, ref string) ([]byte, error) {
	if p == f.path {
		return nil, f.err
	}
	return f.mockGitProvider.GetFileContent(ctx, p, ref)
}

// managedClustersWithoutTest has no entry for cluster "test".
const managedClustersWithoutTest = `clusters:
  - name: other-cluster
    labels:
      keda: enabled
`

// runBoth runs UpdateClusterAddons twice on fresh copies of the same repo:
// once as a dry run and once for real.
func runBoth(t *testing.T, seed func(*mockGitProvider)) (dry, real *RegisterClusterResult, realGit *mockGitProvider) {
	t.Helper()
	for _, dryRun := range []bool{true, false} {
		git := newMockGitProvider()
		seed(git)
		o := setupUpdateClusterAddonsOrch(git)
		res, err := o.UpdateClusterAddons(context.Background(), "test", "https://k8s.example.com", "",
			map[string]bool{"keda": true}, nil, dryRun)
		if err != nil {
			t.Fatalf("UpdateClusterAddons(dryRun=%v): %v", dryRun, err)
		}
		if dryRun {
			dry = res
		} else {
			real, realGit = res, git
		}
	}
	return dry, real, realGit
}

func assertSameFailure(t *testing.T, dry, real *RegisterClusterResult) {
	t.Helper()
	if dry.Status != "failed" {
		t.Errorf("dry run said %q; the real run says %q", dry.Status, real.Status)
	}
	if dry.DryRun != nil {
		t.Errorf("dry run still shows a preview of a change that can never happen: %+v", dry.DryRun.FilesToWrite)
	}
	if dry.Status != real.Status || dry.FailedStep != real.FailedStep || dry.Error != real.Error || dry.Message != real.Message {
		t.Errorf("dry run and real run do not match:\ndry:  %q %q %q %q\nreal: %q %q %q %q",
			dry.Status, dry.FailedStep, dry.Error, dry.Message,
			real.Status, real.FailedStep, real.Error, real.Message)
	}
}

func TestUpdateClusterAddons_DryRunMatchesRealRunWhenFileMissing(t *testing.T) {
	dry, real, realGit := runBoth(t, func(*mockGitProvider) {})
	assertSameFailure(t, dry, real)

	if real.FailedStep != "update_addon_label" {
		t.Errorf("FailedStep = %q", real.FailedStep)
	}
	if real.Error != "managed-clusters.yaml not found" {
		t.Errorf("Error = %q", real.Error)
	}
	const want = "Cluster test is not registered in managed-clusters.yaml, so addon labels cannot be updated. No change was committed."
	if real.Message != want {
		t.Errorf("Message = %q, want %q", real.Message, want)
	}
	if len(realGit.prs) != 0 {
		t.Errorf("a PR was opened although nothing could change: %d", len(realGit.prs))
	}
}

func TestUpdateClusterAddons_DryRunMatchesRealRunWhenClusterNotInFile(t *testing.T) {
	dry, real, realGit := runBoth(t, func(g *mockGitProvider) {
		g.files[mcPath] = []byte(managedClustersWithoutTest)
	})
	assertSameFailure(t, dry, real)

	if real.Error != `cluster "test" not found in managed-clusters.yaml` {
		t.Errorf("Error = %q", real.Error)
	}
	const want = "Could not update the addon label for keda on cluster test; no change was committed. Make sure the cluster is registered in managed-clusters.yaml, then retry."
	if real.Message != want {
		t.Errorf("Message = %q, want %q", real.Message, want)
	}
	if len(realGit.prs) != 0 {
		t.Errorf("a PR was opened although nothing could change: %d", len(realGit.prs))
	}
}

// TestUpdateClusterAddons_GitReadErrorIsReturned: a Git error that is not
// "file not found" must come back as an error, in both the dry run and the
// real run — not be read as "the cluster is not registered".
func TestUpdateClusterAddons_GitReadErrorIsReturned(t *testing.T) {
	const want = "could not read configuration/managed-clusters.yaml from the main branch, so Sharko stopped before changing the addons of cluster test: the git host did not answer"
	for _, dryRun := range []bool{true, false} {
		git := newMockGitProvider()
		git.files[mcPath] = []byte(managedClustersWithKedaEnabled)
		fg := &failReadGit{mockGitProvider: git, path: mcPath, err: errGitDown}
		o := New(nil, nil, newMockArgocd(), fg, autoMergeGitOps(), defaultPaths(), nil)

		res, err := o.UpdateClusterAddons(context.Background(), "test", "https://k8s.example.com", "",
			map[string]bool{"keda": false}, nil, dryRun)
		if err == nil {
			t.Fatalf("dryRun=%v: want an error, got status %q error %q", dryRun, res.Status, res.Error)
		}
		if err.Error() != want {
			t.Errorf("dryRun=%v: error = %q, want %q", dryRun, err.Error(), want)
		}
		if !errors.Is(err, errGitDown) {
			t.Errorf("dryRun=%v: the Git error is not kept inside: %v", dryRun, err)
		}
		if res != nil {
			t.Errorf("dryRun=%v: a result came back with the error: %+v", dryRun, res)
		}
		if len(git.prs) != 0 {
			t.Errorf("dryRun=%v: a PR was opened", dryRun)
		}
	}
}

// TestUpdateClusterAddons_WrappedNotFoundCountsAsMissing: a provider that
// wraps ErrFileNotFound in its own words is still "missing".
func TestUpdateClusterAddons_WrappedNotFoundCountsAsMissing(t *testing.T) {
	git := newMockGitProvider()
	fg := &failReadGit{mockGitProvider: git, path: mcPath,
		err: errors.Join(errors.New("get file content"), gitprovider.ErrFileNotFound)}
	o := New(nil, nil, newMockArgocd(), fg, autoMergeGitOps(), defaultPaths(), nil)
	res, err := o.UpdateClusterAddons(context.Background(), "test", "https://k8s.example.com", "",
		map[string]bool{"keda": true}, nil, true)
	if err != nil {
		t.Fatalf("a not-found must not be an error: %v", err)
	}
	if res.Status != "failed" || res.Error != "managed-clusters.yaml not found" {
		t.Errorf("got status %q error %q", res.Status, res.Error)
	}
}

func newRegisterPreviewOrch(git gitprovider.GitProvider) *Orchestrator {
	return &Orchestrator{
		git:    git,
		argocd: &mockArgocd{registeredClusters: make(map[string]string)},
		gitops: GitOpsConfig{BaseBranch: "main"},
		paths:  RepoPathsConfig{ManagedClusters: mcPath},
	}
}

func registerPreviewReq() RegisterClusterRequest {
	return RegisterClusterRequest{
		Name:   "test-cluster",
		Region: "us-east-1",
		Addons: map[string]bool{},
		DryRun: true,
	}
}

// TestRegisterCluster_DryRunGitReadErrorIsPartial: before, any Git error
// made the preview say "this creates managed-clusters.yaml", which is wrong
// when the file is there and Git just did not answer. LO-7 R5: the dry run
// then answers in the same shape as the real registration does for the same
// failure — a "partial" result with failed step read_cluster_registry — not
// a Go error (which the API turned into a 502 with other words).
func TestRegisterCluster_DryRunGitReadErrorIsPartial(t *testing.T) {
	git := &mockGitProvider{files: map[string][]byte{}}
	o := newRegisterPreviewOrch(&failReadGit{mockGitProvider: git, path: mcPath, err: errGitDown})

	res, err := o.RegisterCluster(context.Background(), registerPreviewReq())
	if err != nil {
		t.Fatalf("want a partial result, got a Go error: %v", err)
	}
	if res.Status != "partial" || res.FailedStep != "read_cluster_registry" {
		t.Errorf("status %q failed step %q, want partial / read_cluster_registry", res.Status, res.FailedStep)
	}
	const wantErr = "could not read configuration/managed-clusters.yaml from the main branch, so Sharko could not show what registering cluster test-cluster would change: the git host did not answer"
	if res.Error != wantErr {
		t.Errorf("error = %q, want %q", res.Error, wantErr)
	}
	const want = "Sharko could not read the cluster registry from Git, so it could not show what registering this cluster would change. Nothing was changed. Try again once Git is reachable."
	if res.Message != want {
		t.Errorf("message:\n got: %q\nwant: %q", res.Message, want)
	}
	if res.DryRun != nil {
		t.Errorf("a preview came back for a registry Sharko could not read: %+v", res.DryRun)
	}
}

// TestRegisterCluster_DryRunGitReadErrorMatchesTheRealRun: the same Git
// read failure, once as a dry run and once for real, gives the same status,
// failed step and completed steps, and both errors hold the Git error. (The
// words before the Git error differ on purpose: one stops a preview, the
// other stops a write.)
func TestRegisterCluster_DryRunGitReadErrorMatchesTheRealRun(t *testing.T) {
	run := func(dry bool) *RegisterClusterResult {
		t.Helper()
		git := &mockGitProvider{files: map[string][]byte{}}
		o := newRegisterPreviewOrch(&failReadGit{mockGitProvider: git, path: mcPath, err: errGitDown})
		req := registerPreviewReq()
		req.DryRun = dry
		res, err := o.RegisterCluster(context.Background(), req)
		if err != nil {
			t.Fatalf("dry run %v: want a partial result, got a Go error: %v", dry, err)
		}
		return res
	}
	dry, real := run(true), run(false)
	if dry.Status != real.Status || dry.FailedStep != real.FailedStep ||
		!strings.HasSuffix(dry.Error, ": the git host did not answer") ||
		!strings.HasSuffix(real.Error, ": the git host did not answer") ||
		dry.Message == "" || real.Message == "" ||
		strings.Join(dry.CompletedSteps, ",") != strings.Join(real.CompletedSteps, ",") {
		t.Errorf("the dry run and the real run answer differently:\n dry:  %q %q %q %v\n real: %q %q %q %v",
			dry.Status, dry.FailedStep, dry.Error, dry.CompletedSteps,
			real.Status, real.FailedStep, real.Error, real.CompletedSteps)
	}
	if real.FailedStep != "read_cluster_registry" {
		t.Fatalf("setup: the real run did not stop at read_cluster_registry: %+v", real)
	}
}

// TestRegisterCluster_DryRunExistingFileSaysUpdate: when the file is there,
// the preview says "update".
func TestRegisterCluster_DryRunExistingFileSaysUpdate(t *testing.T) {
	git := &mockGitProvider{files: map[string][]byte{mcPath: []byte(managedClustersWithoutTest)}}
	o := newRegisterPreviewOrch(git)

	res, err := o.RegisterCluster(context.Background(), registerPreviewReq())
	if err != nil {
		t.Fatalf("RegisterCluster: %v", err)
	}
	if res.DryRun == nil {
		t.Fatalf("no preview; status %q error %q", res.Status, res.Error)
	}
	p := findPreview(res.DryRun.FilesToWrite, mcPath)
	if p == nil {
		t.Fatalf("no preview for %s", mcPath)
	}
	if p.Action != "update" {
		t.Errorf("action = %q, want \"update\"", p.Action)
	}
}

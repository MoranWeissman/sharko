package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
)

// migration_review_fixes_test.go — pinning tests for the smaller v4 Wave 2
// review findings that ride along with the runtime handoff: H-4, M-7, L-9,
// L-10, and the isV4Repo fail-open flag.

// ─── a git fake that fails on purpose ────────────────────────────────────

// flakyGit wraps migrationFakeGit and makes named paths answer with a
// TRANSPORT failure rather than "file not found". That distinction is the
// whole point of findings L-9 and the isV4Repo flag: a missing file is an
// ordinary answer, a git host that will not talk to us is not.
type flakyGit struct {
	*migrationFakeGit
	failPaths map[string]bool
	failAll   bool
}

var errGitHostDown = errors.New("connection reset by peer")

func newFlakyGit(files map[string][]byte, failPaths ...string) *flakyGit {
	f := &flakyGit{migrationFakeGit: newMigrationFakeGit(files), failPaths: map[string]bool{}}
	for _, p := range failPaths {
		f.failPaths[p] = true
	}
	return f
}

func (f *flakyGit) GetFileContent(ctx context.Context, path, ref string) ([]byte, error) {
	if f.failAll || f.failPaths[path] {
		return nil, fmt.Errorf("reading %s: %w", path, errGitHostDown)
	}
	return f.migrationFakeGit.GetFileContent(ctx, path, ref)
}

// ─── L-9: a status probe that cannot reach the git host says so ──────────

// TestMigrationStatus_TransportFailure_IsNotAnEmptyRepo is the pinning
// test for review finding L-9. Reporting "empty" here is not a vague
// answer, it is a confidently wrong one — the empty-repo message tells the
// person to initialize, and initializing a live v3 repo seeds the whole v4
// folder tree on top of a running fleet.
func TestMigrationStatus_TransportFailure_IsNotAnEmptyRepo(t *testing.T) {
	git := newFlakyGit(newV3FixtureRepo())
	git.failAll = true
	orch := newMigrationOrchestrator(t, git)

	got, err := orch.MigrationStatus(context.Background())
	if err == nil {
		t.Fatalf("MigrationStatus returned %+v with no error — a git host that will not answer must be an error", got)
	}
	if !errors.Is(err, errGitHostDown) {
		t.Errorf("the upstream cause should be preserved for the status classifier, got: %v", err)
	}
}

// TestMigrationStatus_MissingFiles_IsStillAnEmptyRepo is the other half:
// genuinely absent markers stay the ordinary "not set up yet" answer, so
// the fix above cannot break the first-run flow.
func TestMigrationStatus_MissingFiles_IsStillAnEmptyRepo(t *testing.T) {
	orch := newMigrationOrchestrator(t, newMigrationFakeGit(nil))

	got, err := orch.MigrationStatus(context.Background())
	if err != nil {
		t.Fatalf("MigrationStatus on a genuinely empty repo: %v", err)
	}
	if got.Format != RepoFormatEmpty {
		t.Errorf("Format = %q, want %q", got.Format, RepoFormatEmpty)
	}
}

// ─── the isV4Repo fail-open flag ─────────────────────────────────────────

// TestRefuseOnV4Repo_FailsClosedOnAProbeFailure is the pinning test for
// the flag raised by the takeover lane. A write gate that cannot tell
// which layout the repo uses must REFUSE, not assume v3 — assuming v3 on
// a repo that is really v4 recreates configuration/managed-clusters.yaml
// beside managed-clusters.yaml, and the reconciler prefers the v3 file,
// so every v4-registered cluster loses its ArgoCD connection Secret.
func TestRefuseOnV4Repo_FailsClosedOnAProbeFailure(t *testing.T) {
	git := newFlakyGit(nil, EnginePinPath)
	orch := newMigrationOrchestrator(t, git)

	err := orch.refuseOnV4Repo(context.Background(), "removing a cluster")
	if err == nil {
		t.Fatal("the gate let a v3 write through although it could not read the engine pin")
	}
	if !errors.Is(err, errGitHostDown) {
		t.Errorf("the upstream cause should be preserved, got: %v", err)
	}
	// A probe failure is NOT "this repo is v4" — the two deserve different
	// status codes, so it must not carry ErrV4RepoUnsupported.
	if IsV4RepoUnsupported(err) {
		t.Error("a probe failure was reported as a v4-repo refusal — the API would answer 409 " +
			"(\"not supported on a v4 repo\") for something that is really the git host being down")
	}
}

// TestRefuseOnV4Repo_MissingPinIsStillFine — an absent engine pin is the
// ordinary v3 answer and must not become a refusal, or every v3 write
// would stop working.
func TestRefuseOnV4Repo_MissingPinIsStillFine(t *testing.T) {
	orch := newMigrationOrchestrator(t, newMigrationFakeGit(newV3FixtureRepo()))
	if err := orch.refuseOnV4Repo(context.Background(), "removing a cluster"); err != nil {
		t.Errorf("a v3 repo was refused: %v", err)
	}
}

// TestRefuseOnV3Repo_FailsClosedOnAProbeFailure — the mirror gate takes
// the same stance for the same reason.
func TestRefuseOnV3Repo_FailsClosedOnAProbeFailure(t *testing.T) {
	git := newFlakyGit(nil, EnginePinPath)
	orch := newMigrationOrchestrator(t, git)

	if err := orch.RefuseOnV3Repo(context.Background()); err == nil {
		t.Fatal("the v3 gate let a write through although it could not read the engine pin")
	}
}

// ─── H-4: a failed auto-merge is a warning, not a lost pull request ──────

type mergeFailingGit struct {
	*migrationFakeGit
}

func (g *mergeFailingGit) MergePullRequest(context.Context, int) error {
	return errors.New("required status check is still pending")
}

// TestMigrate_AutoMergeFailure_KeepsThePullRequest is the pinning test for
// review finding H-4. The pull request IS open and correct; only the merge
// failed. Returning an error threw the GitResult away with it, so the
// caller lost the link to a pull request that definitely exists, and the
// person was told the migration failed when it had not.
func TestMigrate_AutoMergeFailure_KeepsThePullRequest(t *testing.T) {
	git := &mergeFailingGit{migrationFakeGit: newMigrationFakeGit(newV3FixtureRepo())}
	orch := newMigrationOrchestrator(t, git)
	autoMerge := true

	res, err := orch.MigrateV3ToV4(context.Background(), MigrateRequest{Yes: true, AutoMerge: &autoMerge})
	if err != nil {
		t.Fatalf("a failed auto-merge was reported as a failed migration: %v", err)
	}
	if res.Git == nil || res.Git.PRUrl == "" {
		t.Fatal("the pull request link was thrown away — there is an open PR nobody can find")
	}
	if res.Git.Merged {
		t.Error("Merged = true although the merge failed")
	}
	if len(res.Warnings) == 0 {
		t.Fatal("no warning about the failed merge — the person would think it merged")
	}
	if !strings.Contains(res.Warnings[0], res.Git.PRUrl) {
		t.Errorf("the warning should point at the pull request, got %q", res.Warnings[0])
	}
	// The branch must survive: deleting it would throw away a valid,
	// reviewable migration.
	for _, b := range git.deletedBranches {
		if b == res.Git.Branch {
			t.Error("the branch was deleted, taking the reviewable migration with it")
		}
	}
}

// ─── M-7a: a dangling version pin is not silently dropped ────────────────

const danglingPinClustersYAML = `clusters:
  - name: prod-eu
    secretPath: k8s-prod-eu
    region: eu-central-1
    credsSource: secret-kubeconfig
    labels:
      cert-manager: enabled
      metrics-server-version: 3.12.1
      unknown-thing-version: 9.9.9
      env: prod
`

// TestMigrate_DanglingVersionPin_BecomesASwitchedOffEntry pins review
// finding M-7a. `metrics-server-version` with no `metrics-server` label
// used to vanish without a word. It now becomes what it always meant — a
// pin — on a kept-but-off entry, plus a note.
func TestMigrate_DanglingVersionPin_BecomesASwitchedOffEntry(t *testing.T) {
	files := newV3FixtureRepo()
	files["configuration/managed-clusters.yaml"] = []byte(danglingPinClustersYAML)
	delete(files, "configuration/addons-clusters-values/prod-eu.yaml")
	git := newMigrationFakeGit(files)
	orch := newMigrationOrchestrator(t, git)

	if _, err := orch.MigrateV3ToV4(context.Background(), MigrateRequest{Yes: true}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	body, ok := git.branchWrites["cluster-addons/prod-eu.yaml"]
	if !ok {
		t.Fatal("no assignment file written for prod-eu")
	}
	spec, err := models.LoadClusterAddons(body)
	if err != nil {
		t.Fatalf("reading the assignment file back: %v", err)
	}
	entry, ok := spec.Addons["metrics-server"]
	if !ok {
		t.Fatal("the dangling metrics-server pin was dropped silently — that is the loss M-7 is about")
	}
	if entry.Enabled {
		t.Error("metrics-server was switched ON; the cluster was not running it")
	}
	if entry.Version != "3.12.1" {
		t.Errorf("Version = %q, want the pin that was there", entry.Version)
	}

	// The connection record must not keep the v3-shaped key either.
	conn, ok := git.branchWrites[V4ManagedClustersPath]
	if !ok {
		t.Fatal("no connections file written")
	}
	if strings.Contains(string(conn), "metrics-server-version") {
		t.Error("the v3 version key survived onto the v4 connection record, where nothing reads it")
	}
	// A pin for something not in the addon list at all stays as a plain
	// label — lossless — but the person is told.
	if !strings.Contains(string(conn), "unknown-thing-version") {
		t.Error("a version label for an unknown addon was dropped instead of carried across as a label")
	}
}

// ─── M-7b: an unregistered cluster's values are moved, not deleted ───────

// TestMigrate_OrphanClusterValues_AreMovedNotDeleted pins review finding
// M-7b. The fleet-wide side already refuses to delete hand-written values
// it cannot place; the per-cluster side used to sweep them away without a
// word. Both sides now behave the same.
func TestMigrate_OrphanClusterValues_AreMovedNotDeleted(t *testing.T) {
	files := newV3FixtureRepo()
	files["configuration/addons-clusters-values/retired-dc.yaml"] = []byte(
		"cert-manager:\n  replicaCount: 7\n")
	git := newMigrationFakeGit(files)
	orch := newMigrationOrchestrator(t, git)

	res, err := orch.MigrateV3ToV4(context.Background(), MigrateRequest{Yes: true})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	moved, ok := git.branchWrites["values/clusters/retired-dc/cert-manager.yaml"]
	if !ok {
		t.Fatal("the unregistered cluster's values were not moved — they were deleted silently")
	}
	if !strings.Contains(string(moved), "replicaCount: 7") {
		t.Errorf("the moved file lost its content: %s", moved)
	}

	var noted bool
	for _, n := range res.Plan.Notes {
		if strings.Contains(n, "retired-dc") {
			noted = true
		}
	}
	if !noted {
		t.Errorf("no note about retired-dc; the move happened but nobody was told. Notes: %v", res.Plan.Notes)
	}
}

// TestMigrate_BootstrapExampleValuesFile_IsStillRemoved — the one file the
// rescue must NOT rescue. cluster-example.yaml is scaffold the v3
// bootstrap shipped, not somebody's work, and leaving it behind would
// create a nonsense values/clusters/cluster-example/ folder.
func TestMigrate_BootstrapExampleValuesFile_IsStillRemoved(t *testing.T) {
	const example = "configuration/addons-clusters-values/cluster-example.yaml"
	files := newV3FixtureRepo()
	if _, shipped := files[example]; !shipped {
		t.Skipf("%s is no longer part of the v3 bootstrap template tree", example)
	}
	git := newMigrationFakeGit(files)
	orch := newMigrationOrchestrator(t, git)

	if _, err := orch.MigrateV3ToV4(context.Background(), MigrateRequest{Yes: true}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !git.removedOnBranch(example) {
		t.Errorf("%s was kept; the bootstrap's own example is scaffold and should go", example)
	}
	for p := range git.branchWrites {
		if strings.HasPrefix(p, "values/clusters/cluster-example/") {
			t.Errorf("the bootstrap example was turned into a real cluster folder: %s", p)
		}
	}
}

// compile-time proof the flaky fake still satisfies the provider contract.
var _ gitprovider.GitProvider = (*flakyGit)(nil)
var _ gitprovider.GitProvider = (*mergeFailingGit)(nil)

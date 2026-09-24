package serverrender

// releasecommit_wiring_test.go — S8. Three lines in the release workflow are
// load-bearing for the release-commit binding, and none of them is exercised
// by any pull-request check, because release.yml only ever runs on a
// `workflow_run` of Release. A change that dropped one of them would go
// unnoticed until a release, and the failure would look like a signing
// problem rather than a missing build argument.
//
// So they are pinned here, in a test that runs on every pull request:
//
//  1. Both "Verify signed catalog before embedding" steps pass
//     --release-commit. Without it the gate refuses to run at all — which is
//     fail-closed and therefore safe, but it stops the release with a message
//     about a flag rather than about signatures, and there is no reason to
//     find that out during a release.
//  2. The image build passes COMMIT. The Dockerfile already declared
//     `ARG COMMIT=dev` and fed it to `-X main.commit`, but the workflow never
//     supplied a value, so every published image's binary carried the literal
//     string `dev` — read back out of the published v4.0.1 image to confirm
//     it. `sharko serve` now needs that stamp to accept its own embedded
//     catalogue, so without this argument the container image would refuse its
//     own catalogue while the goreleaser CLI archives accepted theirs.
//  3. The Dockerfile still passes COMMIT into the ldflags. A build argument
//     nothing reads is the same as no build argument.
//
// This is a source pin and its limits are worth saying plainly: it proves the
// text is there, not that GitHub Actions expands it. The value itself
// (`workflow_run.head_sha`) is checked as a spelling too, because the whole
// point is that the expected commit comes from GitHub's record of what is
// being released rather than from a certificate or from the tip of `main`.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// readReleaseWorkflow returns release.yml's text. Empty or unreadable is a
// failure, never a quiet pass: a sweep that read nothing looks exactly like a
// sweep that found nothing wrong.
func readReleaseWorkflow(t *testing.T) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), ".github", "workflows", "release.yml")
	body, err := os.ReadFile(path) // a fixed path inside the repository
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		t.Fatalf("%s is empty, so nothing below is checking anything", path)
	}
	return string(body)
}

// TestReleaseWorkflow_VerifyStepsPassTheReleaseCommit — both embed sites.
// There are exactly two places a signed catalogue is copied over
// catalog/addons.yaml (build-and-push and goreleaser), each preceded by a
// verify step, and both must bind to the release commit.
func TestReleaseWorkflow_VerifyStepsPassTheReleaseCommit(t *testing.T) {
	text := readReleaseWorkflow(t)

	// The verify invocations.
	verifyCalls := strings.Count(text, "go run ./cmd/catalog-sign --verify")
	if verifyCalls != 2 {
		t.Fatalf("found %d catalog-sign --verify invocations, want 2 (one before each "+
			"embed swap). If a site was added or removed, this test needs updating "+
			"deliberately rather than by accident.", verifyCalls)
	}

	commitFlags := strings.Count(text, `--release-commit "${{ github.event.workflow_run.head_sha }}"`)
	if commitFlags != 2 {
		t.Errorf("found %d --release-commit flags carrying workflow_run.head_sha, want 2. "+
			"Without it the gate refuses to run and the release stops with a message "+
			"about a flag instead of about signatures.", commitFlags)
	}

	// The embed swaps, counted so the pin above cannot drift out of step with
	// the number of places a catalogue actually gets embedded.
	embedSwaps := strings.Count(text, "cp _dist/catalog/addons.yaml.signed catalog/addons.yaml")
	if embedSwaps != verifyCalls {
		t.Errorf("%d embed swaps but %d verify steps — every swap must be preceded by "+
			"a verify", embedSwaps, verifyCalls)
	}
}

// TestReleaseWorkflow_ImageBuildPassesTheCommit — the build argument that
// decides whether the published container image can verify its own catalogue.
func TestReleaseWorkflow_ImageBuildPassesTheCommit(t *testing.T) {
	text := readReleaseWorkflow(t)

	if !strings.Contains(text, "COMMIT=${{ github.event.workflow_run.head_sha }}") {
		t.Error("the image build does not pass COMMIT=${{ github.event.workflow_run.head_sha }}. " +
			"Without it the image's binary is stamped with the literal string `dev`, " +
			"which is not a release commit, so the image refuses its own embedded " +
			"catalogue while the CLI archives accept theirs.")
	}

	// It has to sit in the build-args block of the build-push-action step, not
	// merely somewhere in the file. The block is matched rather than the bare
	// line so a stray occurrence in a comment cannot satisfy this.
	buildArgs := regexp.MustCompile(`(?s)build-args: \|\n(( +\S.*\n)+)`)
	m := buildArgs.FindStringSubmatch(text)
	if m == nil {
		t.Fatal("no build-args block found in release.yml")
	}
	if !strings.Contains(m[1], "COMMIT=${{ github.event.workflow_run.head_sha }}") {
		t.Errorf("COMMIT is not inside the build-args block:\n%s", m[1])
	}
	// The same value both places, so the release gate and the runtime compare
	// against the same commit.
	if !strings.Contains(m[1], "CACHE_BUST=${{ github.event.workflow_run.head_sha }}") {
		t.Errorf("CACHE_BUST no longer uses workflow_run.head_sha, so the two build "+
			"arguments may now describe different commits:\n%s", m[1])
	}
}

// TestDockerfile_StampsTheCommitArgIntoTheBinary — a build argument nothing
// reads would make the workflow line above decorative.
func TestDockerfile_StampsTheCommitArgIntoTheBinary(t *testing.T) {
	path := filepath.Join(repoRoot(t), "Dockerfile")
	body, err := os.ReadFile(path) // a fixed path inside the repository
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	text := string(body)

	if !strings.Contains(text, "ARG COMMIT") {
		t.Error("the Dockerfile no longer declares ARG COMMIT, so the workflow's " +
			"build argument would be silently ignored")
	}
	if !strings.Contains(text, "-X main.commit=${COMMIT}") {
		t.Error("the Dockerfile no longer stamps ${COMMIT} into -X main.commit, so the " +
			"image's binary would carry no release commit and would refuse its own " +
			"embedded catalogue")
	}
}

// TestGoReleaser_StampsTheFullCommit — goreleaser's `{{.Commit}}` is the FULL
// hash; `{{.ShortCommit}}` is the abbreviated one. The binding compares full
// hashes only, so a switch to the short form would stop every CLI archive
// verifying its own catalogue.
//
// This is a real difference and it is easy to make by accident: the
// Makefile's local `build` target stamps `git rev-parse --short HEAD`, so the
// short form is the one already written down elsewhere in this repository.
func TestGoReleaser_StampsTheFullCommit(t *testing.T) {
	path := filepath.Join(repoRoot(t), ".goreleaser.yaml")
	body, err := os.ReadFile(path) // a fixed path inside the repository
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	text := string(body)

	if !strings.Contains(text, "-X main.commit={{.Commit}}") {
		t.Error("goreleaser no longer stamps -X main.commit={{.Commit}}")
	}
	if strings.Contains(text, "main.commit={{.ShortCommit}}") {
		t.Error("goreleaser stamps main.commit from {{.ShortCommit}}. That is an " +
			"abbreviated hash, and the release-commit binding compares full " +
			"40-character hashes, so every published CLI archive would refuse its " +
			"own embedded catalogue.")
	}
}

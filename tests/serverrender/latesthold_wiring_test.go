package serverrender

// latesthold_wiring_test.go — S4(c). The release workflow holds the three
// moving container-image tags (`latest`, `X.Y` and `X`) back until the image
// SBOM evidence has passed, and nothing in any pull-request check exercises
// that. release.yml only ever runs on a `workflow_run` of Release, so the first
// time any of this executes is during a real release, in front of a real
// registry. An edit that quietly undid it would not turn anything red until
// then, and the damage would already be public.
//
// So the load-bearing lines are pinned here, in a test that runs on every pull
// request. Four things, each one a different way for the hold to stop working:
//
//  1. `flavor: latest=false` on the immutable metadata step. This is the line
//     that is least obviously load-bearing and most easily lost.
//     docker/metadata-action defaults to `latest=auto`, and for a
//     non-prerelease semver version `auto` means the action adds `latest` on
//     its own — the `type=raw,value=latest` line the list used to carry was
//     never what produced it. So deleting that line alone would have left
//     `latest` in the immutable list and changed nothing whatsoever. Only
//     `latest=false` actually holds it back, and a reviewer reading a diff that
//     removes it has no reason to think anything happened.
//  2. The build-and-push step pushes the immutable list. Pointing it at the
//     moving list, or at both, puts the moving tags straight back into the
//     early push.
//  3. The step that applies the moving tags runs after the SBOM step, and runs
//     last. "After the evidence" is the entire property being bought; a step
//     that moved above the evidence would look almost identical in a diff.
//  4. That step still checks what it did — index digest unchanged, and both
//     architectures still in the index behind each tag.
//
// The fifth test is about the OTHER half of the failure story, and it is here
// because the operator documentation makes a claim that only the job graph can
// keep true. For the cluster end-to-end suites a failure genuinely does prevent
// publication, because `release-evidence-gate` sits upstream of every publish
// job. For the image evidence steps it genuinely does not, because the image is
// already pushed. Both statements are true, about different things, and the
// first one is only true while the graph says so — so the graph is asserted
// rather than described.
//
// This is a source pin and its limit is worth saying plainly: it proves the
// text and the shape are there, not that GitHub Actions behaves as expected
// when it expands them. What the retag mechanism does to a real
// multi-architecture index was measured separately, against a throwaway local
// registry, and written up in the block comment above the step itself.

import (
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// releaseStep is one step of one job, read loosely on purpose: `with:` and
// `env:` hold booleans and numbers as well as strings, so the values are taken
// as `any` and flattened when they are compared. Unmarshalling them as strings
// would fail on `push: true` and the failure would look like a broken test
// rather than a workflow change.
type releaseStep struct {
	Name string         `yaml:"name"`
	ID   string         `yaml:"id"`
	Uses string         `yaml:"uses"`
	With map[string]any `yaml:"with"`
	Env  map[string]any `yaml:"env"`
	Run  string         `yaml:"run"`
}

type releaseJob struct {
	Needs yaml.Node     `yaml:"needs"`
	Steps []releaseStep `yaml:"steps"`
}

type releaseWorkflow struct {
	Jobs map[string]releaseJob `yaml:"jobs"`
}

// parseReleaseWorkflow reads release.yml as YAML. A parse failure, a missing
// job or an empty step list is fatal, never a quiet pass: a test that read
// nothing reports success in exactly the same way as a test that found nothing
// wrong.
func parseReleaseWorkflow(t *testing.T) releaseWorkflow {
	t.Helper()
	var wf releaseWorkflow
	if err := yaml.Unmarshal([]byte(readReleaseWorkflow(t)), &wf); err != nil {
		t.Fatalf("cannot parse .github/workflows/release.yml as YAML, so nothing below "+
			"is checking anything: %v", err)
	}
	if len(wf.Jobs) == 0 {
		t.Fatal("release.yml parsed but has no jobs, so nothing below is checking anything")
	}
	return wf
}

// buildAndPushSteps returns the steps of the image job.
func buildAndPushSteps(t *testing.T) []releaseStep {
	t.Helper()
	job, ok := parseReleaseWorkflow(t).Jobs["build-and-push"]
	if !ok {
		t.Fatal("release.yml has no build-and-push job. If the image job was renamed, " +
			"every check in this file needs updating deliberately rather than by accident.")
	}
	if len(job.Steps) == 0 {
		t.Fatal("build-and-push has no steps")
	}
	return job.Steps
}

// stepByID finds one step, and its index, by the `id:` the workflow gives it.
func stepByID(t *testing.T, steps []releaseStep, id string) (int, releaseStep) {
	t.Helper()
	for i, s := range steps {
		if s.ID == id {
			return i, s
		}
	}
	t.Fatalf("build-and-push has no step with id %q", id)
	return -1, releaseStep{}
}

// flat renders a `with:` or `env:` value as the text the workflow carries, so a
// multi-line block and a single line compare the same way.
func flat(v any) string {
	if v == nil {
		return ""
	}
	return strings.Join(strings.Fields(fmt.Sprint(v)), " ")
}

// TestReleaseWorkflow_TheImmutableTagListCannotCarryLatest — the metadata step
// that feeds the early push, and the `flavor` line that is the only thing
// keeping `latest` out of it.
func TestReleaseWorkflow_TheImmutableTagListCannotCarryLatest(t *testing.T) {
	steps := buildAndPushSteps(t)
	_, meta := stepByID(t, steps, "meta")

	if !strings.HasPrefix(meta.Uses, "docker/metadata-action@") {
		t.Fatalf("the step with id `meta` uses %q, not docker/metadata-action. This file "+
			"assumes that action's tag and flavor inputs.", meta.Uses)
	}

	if !strings.Contains(flat(meta.With["flavor"]), "latest=false") {
		t.Errorf("the immutable metadata step no longer sets `flavor: latest=false` "+
			"(it has %q).\n\n"+
			"That one line is what holds `latest` back, and it is not obvious. "+
			"docker/metadata-action defaults to latest=auto, and for a non-prerelease "+
			"semver version auto makes the action emit `latest` BY ITSELF — no "+
			"type=raw line needed. Without latest=false, `latest` goes back into the "+
			"list the build pushes, which is the whole defect this was fixing: "+
			"`latest` public before any image SBOM evidence has run.",
			flat(meta.With["flavor"]))
	}

	if tags := flat(meta.With["tags"]); strings.Contains(tags, "latest") {
		t.Errorf("the immutable tag list mentions `latest`: %q.\n\n"+
			"The immutable list is the one pushed with the build, before the image "+
			"SBOM is generated, checked and attested. `latest` belongs in the "+
			"meta_moving list, which is applied at the end of the job.", tags)
	}

	// And the push step has to consume that list rather than the moving one.
	_, build := stepByID(t, steps, "build")
	if got := flat(build.With["tags"]); got != "${{ steps.meta.outputs.tags }}" {
		t.Errorf("the image build pushes tags %q, want ${{ steps.meta.outputs.tags }}. "+
			"Pointing it at meta_moving, or at both lists, would push the moving tags "+
			"in the early push and undo the hold entirely.", got)
	}
}

// movingTagStep finds the step that applies the moving tags, by what it does
// rather than by its name, so a reworded step name does not silently turn this
// whole file into a pass.
func movingTagStep(t *testing.T, steps []releaseStep) (int, releaseStep) {
	t.Helper()
	found := -1
	for i, s := range steps {
		if strings.Contains(s.Run, "docker buildx imagetools create") {
			if found >= 0 {
				t.Fatalf("two steps run `docker buildx imagetools create` (%d and %d). "+
					"This file assumes exactly one applies the moving tags.", found+1, i+1)
			}
			found = i
		}
	}
	if found < 0 {
		t.Fatal("no step in build-and-push runs `docker buildx imagetools create`, so " +
			"nothing applies the moving tags. Either `latest`, `X.Y` and `X` are no " +
			"longer published at all, or they went back into the early push — check " +
			"which, because those are opposite mistakes.")
	}
	return found, steps[found]
}

// TestReleaseWorkflow_TheMovingTagsAreAppliedAfterTheImageEvidence — the
// ordering that is the entire point.
func TestReleaseWorkflow_TheMovingTagsAreAppliedAfterTheImageEvidence(t *testing.T) {
	steps := buildAndPushSteps(t)
	sbomAt, sbom := stepByID(t, steps, "image_sboms")
	movingAt, moving := movingTagStep(t, steps)

	// The evidence step is the generation, the completeness guard, the digest
	// guard and the attestation, all in one script. Confirm it still is, so
	// "after the evidence" keeps meaning what it says.
	for _, want := range []string{"syft scan", "cosign attest", "pkg:apk/", "pkg:golang/"} {
		if !strings.Contains(sbom.Run, want) {
			t.Errorf("the image_sboms step no longer contains %q, so ordering the moving "+
				"tags after it may no longer mean they wait for the SBOM evidence", want)
		}
	}

	if movingAt <= sbomAt {
		t.Fatalf("the moving tags are applied at step %d and the image SBOM evidence "+
			"runs at step %d. The moving tags must come AFTER the evidence — that "+
			"ordering is the whole property: a consumer pulling `latest` must never "+
			"land on a build whose image evidence failed.", movingAt+1, sbomAt+1)
	}
	if movingAt != len(steps)-1 {
		t.Errorf("the moving tags are applied at step %d of %d, not last. Anything added "+
			"after it runs while the moving tags are already public, which is the "+
			"failure this split exists to remove — put the new step before it.",
			movingAt+1, len(steps))
	}

	if got := flat(moving.Env["MOVING_TAGS"]); got != "${{ steps.meta_moving.outputs.tags }}" {
		t.Errorf("the moving-tag step reads MOVING_TAGS from %q, want "+
			"${{ steps.meta_moving.outputs.tags }}", got)
	}
	if got := flat(moving.Env["DIGEST"]); got != "${{ steps.build.outputs.digest }}" {
		t.Errorf("the moving-tag step reads DIGEST from %q, want "+
			"${{ steps.build.outputs.digest }}. It has to be the digest the build "+
			"actually pushed, or the tags could be pointed at some other image.", got)
	}

	// The moving list itself: all three tags, and latest=false there too so the
	// list is exactly what is written down rather than whatever the action's
	// defaults produce.
	_, metaMoving := stepByID(t, steps, "meta_moving")
	movingTags := flat(metaMoving.With["tags"])
	for _, want := range []string{
		"type=semver,pattern={{major}}.{{minor}}",
		"type=semver,pattern={{major}}",
		"type=raw,value=latest",
	} {
		if !strings.Contains(movingTags, want) {
			t.Errorf("the moving tag list no longer produces %q (it has %q). All three of "+
				"`latest`, `X.Y` and `X` move from one release to the next and all three "+
				"are held for the same reason; dropping one from this list stops it being "+
				"published at all.", want, movingTags)
		}
	}
	if !strings.Contains(flat(metaMoving.With["flavor"]), "latest=false") {
		t.Errorf("the moving metadata step no longer sets `flavor: latest=false` (it has "+
			"%q). `latest` is listed explicitly here, so the flavor default would only "+
			"add a duplicate — the point of pinning it is that this list stays exactly "+
			"the three tags written down rather than whatever the action decides.",
			flat(metaMoving.With["flavor"]))
	}
}

// TestReleaseWorkflow_TheMovingTagStepProvesTheIndexSurvived — the step checks
// its own work, and those checks are load-bearing.
//
// Measured against a throwaway local registry: asking `imagetools create` to
// retag ONE architecture's manifest does not error, it builds a fresh
// single-platform index and points the tag at that. So a mistake here publishes
// a `latest` that only works on amd64 and exits 0 while doing it. The digest
// comparison and the per-architecture assertion are what turn that into a
// failure, and each was planted and proven to fire on its own.
func TestReleaseWorkflow_TheMovingTagStepProvesTheIndexSurvived(t *testing.T) {
	_, moving := movingTagStep(t, buildAndPushSteps(t))
	run := moving.Run

	for _, want := range []struct{ text, why string }{
		{`if [ "$got" != "$DIGEST" ]; then`,
			"the index-digest comparison. Equality is what proves the index was copied " +
				"rather than rebuilt — and because a cosign signature lives against a " +
				"digest and not against a tag name, it is also what makes the image " +
				"signature cover the moving tags."},
		{`for arch in amd64 arm64; do`,
			"the per-architecture assertion. Without it a multi-architecture index " +
				"flattened to one architecture is published silently, and nobody finds " +
				"out until somebody pulls on arm64."},
		{`sha256sum "$raw"`,
			"hashing the manifest bytes the registry returns. A digest IS the sha256 of " +
				"those bytes, so this is how the tag's digest is read without depending " +
				"on a buildx output template."},
		{`grep -qx "${IMAGE}:latest"`,
			"the check that `latest` is actually in the list. An empty or short list " +
				"would apply no moving tags, exit 0, and leave `latest` pointing at the " +
				"previous release with nothing saying so."},
	} {
		if !strings.Contains(run, want.text) {
			t.Errorf("the moving-tag step no longer contains %q.\n\nThat is %s",
				want.text, want.why)
		}
	}

	// It must fail on a bad result rather than warn about one.
	if strings.Count(run, "exit 1") < 4 {
		t.Errorf("the moving-tag step has %d `exit 1` lines, want at least 4 (empty list, "+
			"missing latest, digest mismatch, missing architecture). A check that prints "+
			"a warning and carries on publishes the thing it was checking.",
			strings.Count(run, "exit 1"))
	}
	if !strings.Contains(run, "set -euo pipefail") {
		t.Error("the moving-tag step no longer sets `set -euo pipefail`, so a failing " +
			"imagetools or jq call would not stop it")
	}
}

// TestReleaseWorkflow_TheEvidenceGateStillGatesEveryPublishJob — the other half
// of the failure story, and the half the operator documentation depends on.
//
// docs/site/operator/supply-chain.md tells an operator that a
// release-evidence-gate failure prevents publication outright, and that an
// image-evidence failure does not. The first of those is only true while this
// graph holds: the gate has to sit upstream of the first publish job, and every
// other publish job has to reach back to it. If someone gave a publish job its
// own `needs:` that bypassed the chain, the documentation would start lying and
// nothing else would notice.
func TestReleaseWorkflow_TheEvidenceGateStillGatesEveryPublishJob(t *testing.T) {
	wf := parseReleaseWorkflow(t)

	needsOf := func(job string) []string {
		j, ok := wf.Jobs[job]
		if !ok {
			t.Fatalf("release.yml has no %s job", job)
		}
		var one string
		if err := j.Needs.Decode(&one); err == nil {
			return []string{one}
		}
		var many []string
		if err := j.Needs.Decode(&many); err != nil {
			t.Fatalf("cannot read the `needs:` of %s: %v", job, err)
		}
		return many
	}
	requires := func(job, want string) {
		for _, n := range needsOf(job) {
			if n == want {
				return
			}
		}
		t.Errorf("%s does not need %s (it needs %v).\n\n"+
			"Every publish step has to reach back to release-evidence-gate, or a tag "+
			"can publish without the cluster e2e suites, the perf gate and the strict "+
			"docs build having run. docs/site/operator/supply-chain.md tells operators "+
			"that a failure there prevents publication outright — this graph is the "+
			"only thing that makes that sentence true.", job, want, needsOf(job))
	}

	// The gate collects all five evidence jobs by name.
	for _, evidence := range []string{
		"release-gate-e2e",
		"release-gate-e2e-live-gitea",
		"release-gate-e2e-helm",
		"release-gate-perf",
		"release-gate-docs",
	} {
		requires("release-evidence-gate", evidence)
	}

	// And the publish chain hangs off it.
	requires("sign-catalog-entries", "release-evidence-gate")
	requires("build-and-push", "sign-catalog-entries")
	requires("helm-package", "build-and-push")
	requires("helm-package-engine", "build-and-push")
	requires("goreleaser", "build-and-push")
}

package logging

// log_guard_unswept_test.go — the files the log guard's walk cannot read, and
// the written reason each of them is allowed to stay out of reach (BF7).
//
// # The gap this closes
//
// log_error_guard_test.go proves the log-line list still describes the tree by
// asserting the type-checked walk read EXACTLY wantSweptGoFiles files. That
// number is honest about what the walk reads. It said nothing at all about
// what the walk does NOT read.
//
// It does not read all of them. The walk type-checks the default build, so a
// file that is behind a //go:build tag the default build does not set is
// invisible to it — the type checker never loads the file, so no log line
// inside it can ever be classified, and the count assertion agrees happily
// because the file was never in the count to begin with.
//
// That is only acceptable while such a file cannot reach a running Sharko. So
// this file states the whole of it:
//
//   - logGuardUnsweptFiles is a LIST — one entry per file, naming the file,
//     the exact build constraint that keeps it out, and the written reason it
//     cannot ship. Not a count, not a directory prefix, not a glob: a glob
//     would swallow the next unclassified file in silence, which is the exact
//     failure this guard exists to prevent.
//
//   - swept + classified must equal the number of non-test .go files in the
//     tree, EXACTLY. A new file is therefore either read by the walk (and its
//     log lines classified by logGuardSites) or named here with a reason.
//     There is no third place for it to be.
//
//   - a listed file that has gone, or that no longer carries the constraint
//     the list records, is a stale entry and fails just as loudly. A file
//     quietly losing its tag is precisely how a non-shipping file becomes a
//     shipping one.
//
// # And the thing that would actually make one of them ship
//
// None of these files can reach an artifact for one reason only: nothing that
// builds a binary or an image passes -tags. That is a property of the build
// configuration, not of the Go source, so asserting it here is what keeps the
// reasons above true. TestLogGuard_NoBinaryBuildPassesBuildTags reads the real
// build files — found by walking, never hand-listed — and fails if a binary or
// image build ever starts passing a build tag. `go test -tags=e2e` is a test
// invocation, is legitimate, appears many times, and is deliberately not
// flagged; how the two are told apart is written into the code below.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// reasonE2EHarness is why every //go:build e2e file is out of reach and cannot
// ship. It is one sentence shared by the files it is true of, rather than
// twenty-six copies of the same paragraph.
const reasonE2EHarness = "the e2e build tag is passed by `go test` invocations only — several in the Makefile, plus the Gitea live-loop step in .github/workflows/e2e.yml and .github/workflows/release.yml. No binary build and no image build in this repository passes it, so this file compiles into nothing that is released or run."

// reasonTLSBypass is why the destination-TLS bypass is out of reach.
const reasonTLSBypass = "the sharko_unverified_dest_ok tag is passed by NOTHING in this repository — not a Makefile target, not a Dockerfile, not GoReleaser, not a workflow. The default build compiles tlsguard_default.go instead, where the constant is false and the compiler can prove the bypass branch dead. The tag exists so somebody compiling their own binary for a self-signed estate can opt in on purpose, which docs/site/release-notes.md documents; it is not a state any released artifact can be in."

// unsweptGoFile is one file the walk cannot read, with the constraint that
// keeps it out and the reason that is allowed.
type unsweptGoFile struct {
	// path is repo-relative, slash-separated.
	path string
	// buildTag is the constraint expression exactly as the file's //go:build
	// line spells it, without the "//go:build " prefix. It is part of the
	// entry on purpose: a file that loses or changes its tag is a different
	// file as far as this guard is concerned, and must be looked at again.
	buildTag string
	// reason is why this file cannot reach a released artifact.
	reason string
}

// logGuardUnsweptFiles is THE LIST of files under the swept directories that
// the type-checked walk does not read. Every one is here by name.
var logGuardUnsweptFiles = []unsweptGoFile{
	{path: "internal/remoteclient/tlsguard_bypass.go", buildTag: "sharko_unverified_dest_ok", reason: reasonTLSBypass},

	{path: "tests/e2e/harness/apiclient.go", buildTag: "e2e", reason: reasonE2EHarness},
	{path: "tests/e2e/harness/apiclient_addon_admin.go", buildTag: "e2e", reason: reasonE2EHarness},
	{path: "tests/e2e/harness/apiclient_addon_cluster.go", buildTag: "e2e", reason: reasonE2EHarness},
	{path: "tests/e2e/harness/apiclient_ai.go", buildTag: "e2e", reason: reasonE2EHarness},
	{path: "tests/e2e/harness/apiclient_auth.go", buildTag: "e2e", reason: reasonE2EHarness},
	{path: "tests/e2e/harness/apiclient_catalog.go", buildTag: "e2e", reason: reasonE2EHarness},
	{path: "tests/e2e/harness/apiclient_cluster.go", buildTag: "e2e", reason: reasonE2EHarness},
	{path: "tests/e2e/harness/apiclient_dashboard.go", buildTag: "e2e", reason: reasonE2EHarness},
	{path: "tests/e2e/harness/apiclient_init.go", buildTag: "e2e", reason: reasonE2EHarness},
	{path: "tests/e2e/harness/apiclient_pr.go", buildTag: "e2e", reason: reasonE2EHarness},
	{path: "tests/e2e/harness/apiclient_values.go", buildTag: "e2e", reason: reasonE2EHarness},
	{path: "tests/e2e/harness/argocd.go", buildTag: "e2e", reason: reasonE2EHarness},
	{path: "tests/e2e/harness/auth.go", buildTag: "e2e", reason: reasonE2EHarness},
	{path: "tests/e2e/harness/doc.go", buildTag: "e2e", reason: reasonE2EHarness},
	{path: "tests/e2e/harness/fixtures.go", buildTag: "e2e", reason: reasonE2EHarness},
	{path: "tests/e2e/harness/ghmock.go", buildTag: "e2e", reason: reasonE2EHarness},
	{path: "tests/e2e/harness/gitfake.go", buildTag: "e2e", reason: reasonE2EHarness},
	{path: "tests/e2e/harness/gitfake_helm.go", buildTag: "e2e", reason: reasonE2EHarness},
	{path: "tests/e2e/harness/kind.go", buildTag: "e2e", reason: reasonE2EHarness},
	{path: "tests/e2e/harness/phases.go", buildTag: "e2e", reason: reasonE2EHarness},
	{path: "tests/e2e/harness/sharko.go", buildTag: "e2e", reason: reasonE2EHarness},
	{path: "tests/e2e/harness/sharko_helm.go", buildTag: "e2e", reason: reasonE2EHarness},
	{path: "tests/e2e/harness/sharko_helm_auth.go", buildTag: "e2e", reason: reasonE2EHarness},
	{path: "tests/e2e/harness/stats.go", buildTag: "e2e", reason: reasonE2EHarness},
	{path: "tests/e2e/harness/timing.go", buildTag: "e2e", reason: reasonE2EHarness},
	{path: "tests/e2e/lifecycle/cluster_helpers.go", buildTag: "e2e", reason: reasonE2EHarness},
}

// logGuardTreeFiles walks the swept directories and returns every non-test .go
// file in them, repo-relative. This is the denominator: the whole tree the
// guard claims to account for.
func logGuardTreeFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, dir := range logGuardSweepDirs {
		abs := filepath.Join(root, dir)
		if _, err := os.Stat(abs); err != nil {
			t.Fatalf("swept directory %q does not exist — the guard would account for a tree that is not there: %v", dir, err)
		}
		walkErr := filepath.WalkDir(abs, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			name := d.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			out = append(out, filepath.ToSlash(rel))
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walking %q: %v", dir, walkErr)
		}
	}
	if len(out) == 0 {
		t.Fatal("the tree walk found no Go files at all — it is not reaching the repository, and every count below would agree with any list")
	}
	sort.Strings(out)
	return out
}

// logGuardWalkedFiles returns the set of files the log guard's type-checked
// walk actually reads, filtered exactly the way walkSlogCalls filters them.
// This is the ground truth for "swept": not a re-implementation of Go's build
// constraint rules, but the same package load the guard itself runs.
func logGuardWalkedFiles(t *testing.T, root string) map[string]bool {
	t.Helper()
	pkgs := loadTypedPackages(t, root, "./...")
	walked := map[string]bool{}
	for _, pkg := range pkgs {
		for i := range pkg.Syntax {
			if i >= len(pkg.CompiledGoFiles) {
				continue
			}
			path := pkg.CompiledGoFiles[i]
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			if !inSweptDir(rel) {
				continue
			}
			walked[rel] = true
		}
	}
	if len(walked) == 0 {
		t.Fatal("the type-checked walk read no files at all — nothing below could be trusted")
	}
	return walked
}

// buildTagOf reads a file's //go:build constraint, or "" when it has none.
func buildTagOf(t *testing.T, root, rel string) (string, bool) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "//go:build ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "//go:build ")), true
		}
		// The constraint must appear before the package clause.
		if strings.HasPrefix(line, "package ") {
			break
		}
	}
	return "", true
}

// TestLogGuard_TheUnsweptListIsWellFormed pins the list itself before it is
// used to judge anything.
func TestLogGuard_TheUnsweptListIsWellFormed(t *testing.T) {
	if len(logGuardUnsweptFiles) == 0 {
		t.Fatal("logGuardUnsweptFiles is empty — an empty list agrees with everything and proves nothing")
	}
	seen := map[string]bool{}
	for _, f := range logGuardUnsweptFiles {
		if seen[f.path] {
			t.Errorf("%s is listed twice — a duplicate hides whichever entry is wrong", f.path)
		}
		seen[f.path] = true
		if strings.TrimSpace(f.buildTag) == "" {
			t.Errorf("%s records no build constraint. The constraint is what keeps the file out of every artifact; an entry without one is claiming nothing.", f.path)
		}
		if strings.TrimSpace(f.reason) == "" {
			t.Errorf("%s is classified with no reason. A reason in words is the whole cost of leaving a file outside the guard's reach.", f.path)
		}
		if !inSweptDir(f.path) {
			t.Errorf("%s is not under any swept directory (%s), so the guard was never going to read it and listing it here says something untrue about the sweep", f.path, strings.Join(logGuardSweepDirs, ", "))
		}
		if strings.HasSuffix(f.path, "_test.go") {
			t.Errorf("%s is a test file. Test files are skipped by the walk for a different reason and are not part of this accounting.", f.path)
		}
	}
}

// TestLogGuard_EveryGoFileIsEitherSweptOrClassified is the accounting. Every
// non-test Go file in the swept directories is either read by the walk — and
// so its log lines are judged by logGuardSites — or named in
// logGuardUnsweptFiles with the constraint that keeps it out and a written
// reason. Nothing may fall between the two.
func TestLogGuard_EveryGoFileIsEitherSweptOrClassified(t *testing.T) {
	root := logGuardRepoRoot(t)
	tree := logGuardTreeFiles(t, root)
	walked := logGuardWalkedFiles(t, root)

	classified := map[string]unsweptGoFile{}
	for _, f := range logGuardUnsweptFiles {
		classified[f.path] = f
	}

	var unaccounted, alsoWalked []string
	for _, rel := range tree {
		if walked[rel] {
			if _, isClassified := classified[rel]; isClassified {
				alsoWalked = append(alsoWalked, rel)
			}
			continue
		}
		if _, isClassified := classified[rel]; !isClassified {
			unaccounted = append(unaccounted, rel)
		}
	}

	// Stale entries: a classified file that is no longer in the tree.
	inTree := map[string]bool{}
	for _, rel := range tree {
		inTree[rel] = true
	}
	var vanished, wrongTag []string
	for _, f := range logGuardUnsweptFiles {
		if !inTree[f.path] {
			vanished = append(vanished, f.path)
			continue
		}
		tag, readOK := buildTagOf(t, root, f.path)
		if !readOK {
			vanished = append(vanished, f.path+" (could not be read)")
			continue
		}
		if tag != f.buildTag {
			if tag == "" {
				wrongTag = append(wrongTag, f.path+": the list says //go:build "+f.buildTag+", the file now carries NO build constraint at all")
				continue
			}
			wrongTag = append(wrongTag, f.path+": the list says //go:build "+f.buildTag+", the file says //go:build "+tag)
		}
	}

	report := func(label string, items []string, why string) {
		if len(items) == 0 {
			return
		}
		sort.Strings(items)
		t.Errorf("%s (%d):\n  %s\n\n%s", label, len(items), strings.Join(items, "\n  "), why)
	}

	report("Go files the walk does not read and nobody classified", unaccounted,
		"Each of these is invisible to the log guard: the type checker never loads it, so no slog call inside it can ever be judged. "+
			"Work out why it is out of reach — almost always a //go:build tag the default build does not set — and either bring it "+
			"into the default build or add it to logGuardUnsweptFiles with its constraint and a written reason it cannot reach a "+
			"released artifact. Do not widen the entry into a directory or a glob: a glob would swallow the next one in silence.")

	report("entries in logGuardUnsweptFiles whose file is gone", vanished,
		"A stale entry is a hole. The accounting stops describing the tree and the next unreachable file can hide behind the "+
			"mismatch. Delete the entry if the file really went; do not delete it to make a failure quiet.")

	report("entries whose build constraint has changed", wrongTag,
		"The constraint is what keeps the file out of every shipped artifact, so it is part of the entry. A file that loses or "+
			"changes its tag has to be looked at again: if it is now in the default build the walk reads it and its log lines need "+
			"classifying in logGuardSites; if it is behind a different tag, that tag needs its own reason.")

	report("entries in logGuardUnsweptFiles that the walk DOES read", alsoWalked,
		"These files are inside the guard's reach after all, so classifying them as unreachable says something untrue. Remove the "+
			"entry and make sure any slog call in the file appears in logGuardSites.")

	// The exact arithmetic, last, so the named disagreements above are read
	// first. Exact on purpose: a floor with room in it is a hole.
	if len(walked)+len(logGuardUnsweptFiles) != len(tree) {
		t.Errorf(`the accounting does not add up: %d files read by the walk + %d files classified as out of reach = %d, but the tree holds %d non-test .go files under %s.

Every file has to be in exactly one of the two. The named disagreements above say which ones are not.`,
			len(walked), len(logGuardUnsweptFiles), len(walked)+len(logGuardUnsweptFiles), len(tree), strings.Join(logGuardSweepDirs, ", "))
	}

	// And the walk's own count has to be the number the log-line guard pins,
	// or the two halves of this accounting are describing different trees.
	if len(walked) != wantSweptGoFiles {
		t.Errorf("the walk reads %d files; log_error_guard_test.go pins wantSweptGoFiles at %d. Both numbers describe the same sweep and must agree.", len(walked), wantSweptGoFiles)
	}
}

// ---------------------------------------------------------------------------
// The build configuration: nothing that produces an artifact may pass -tags.
// ---------------------------------------------------------------------------

// buildTagFlag matches a -tags / --tags flag as a whole token, so a word that
// merely contains "tags" cannot trigger it.
var buildTagFlag = regexp.MustCompile(`(^|\s)--?tags([=\s]|$)`)

// goreleaserTagsKey matches GoReleaser's own `tags:` build field, which sets
// build tags without any -tags flag ever appearing.
var goreleaserTagsKey = regexp.MustCompile(`(?m)^\s*-?\s*tags:`)

// goSubcommand reports which `go` subcommand a command line runs, or "".
//
// This is how a build is told apart from a test, and the distinction is the
// whole point of the check:
//
//   - `go build`, `go install`, `go run` PRODUCE something. A build tag there
//     changes which files are compiled into an artifact, which is exactly what
//     would turn a tag-gated file such as tlsguard_bypass.go from unreachable
//     into shipping. Not allowed.
//   - `go test` and `go vet` do not produce an artifact. `go test -tags=e2e`
//     is how the e2e suite runs at all; it appears many times in the Makefile
//     and in the workflows and is legitimate. Allowed.
func goSubcommand(line string) string {
	fields := strings.Fields(line)
	for i, f := range fields {
		// Trim a leading path, so `/usr/local/go/bin/go build` is still `go`.
		if filepath.Base(f) != "go" || i+1 >= len(fields) {
			continue
		}
		switch next := fields[i+1]; next {
		case "build", "install", "run", "test", "vet":
			return next
		}
	}
	return ""
}

func producesArtifact(sub string) bool {
	return sub == "build" || sub == "install" || sub == "run"
}

// logicalLines joins backslash-continued lines, because a Dockerfile RUN and a
// Makefile recipe both spread one command over several physical lines and a
// -tags flag could sit on any of them. The returned line number is where the
// logical line started.
func logicalLines(body string) []struct {
	num  int
	text string
} {
	var out []struct {
		num  int
		text string
	}
	raw := strings.Split(body, "\n")
	i := 0
	for i < len(raw) {
		start := i + 1
		joined := strings.TrimSuffix(raw[i], "\r")
		for strings.HasSuffix(strings.TrimSpace(joined), `\`) && i+1 < len(raw) {
			joined = strings.TrimSuffix(strings.TrimSpace(joined), `\`) + " " + strings.TrimSpace(raw[i+1])
			i++
		}
		out = append(out, struct {
			num  int
			text string
		}{num: start, text: joined})
		i++
	}
	return out
}

// isBuildConfigFile reports whether a file configures how Sharko is built or
// packaged. The set is matched by shape, and the files are found by walking —
// hand-listing them is how a new Dockerfile or a new workflow gets missed.
func isBuildConfigFile(rel string) bool {
	base := filepath.Base(rel)
	switch {
	case base == "Makefile" || strings.HasPrefix(base, "Makefile."):
		return true
	case base == "Dockerfile" || strings.HasPrefix(base, "Dockerfile.") || strings.HasSuffix(base, ".dockerfile"):
		return true
	case base == ".goreleaser.yaml" || base == ".goreleaser.yml":
		return true
	case strings.HasPrefix(rel, ".github/workflows/") && (strings.HasSuffix(base, ".yml") || strings.HasSuffix(base, ".yaml")):
		return true
	}
	return false
}

// logGuardBuildConfigFiles finds every build configuration file by walking the
// repository.
func logGuardBuildConfigFiles(t *testing.T, root string) []string {
	t.Helper()
	skipDir := map[string]bool{
		".git": true, "node_modules": true, "_dist": true,
		".worktrees": true, "vendor": true, "dist": true,
	}
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipDir[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if isBuildConfigFile(rel) {
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository for build configuration files: %v", err)
	}
	sort.Strings(out)
	return out
}

// TestLogGuard_NoBinaryBuildPassesBuildTags is what keeps the reasons in
// logGuardUnsweptFiles true.
//
// Every entry there rests on one fact: no build of a Sharko binary or image
// passes -tags, so a file behind //go:build e2e or //go:build
// sharko_unverified_dest_ok compiles into nothing that ships. The day a build
// line starts passing a tag, that stops being true — and the file-accounting
// test above would still pass, because the Go source did not change. This is
// the test that notices.
func TestLogGuard_NoBinaryBuildPassesBuildTags(t *testing.T) {
	root := logGuardRepoRoot(t)
	files := logGuardBuildConfigFiles(t, root)
	if len(files) == 0 {
		t.Fatal("found no build configuration files at all — the scan is not reaching the repository and would agree with anything")
	}

	var offenders []string
	// Counted so the scan cannot pass by reading nothing: taggedTests proves
	// it can see a -tags flag, artifactBuilds proves it can see a build line.
	taggedTests := 0
	buildLines := map[string]int{}
	installLines := map[string]int{}

	for _, rel := range files {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("reading build configuration file %q: %v", rel, err)
		}
		text := string(body)

		// GoReleaser sets build tags declaratively, with no `go build` line to
		// find, so its own field is checked directly.
		base := filepath.Base(rel)
		if base == ".goreleaser.yaml" || base == ".goreleaser.yml" {
			if goreleaserTagsKey.MatchString(text) {
				offenders = append(offenders, rel+": GoReleaser's `tags:` build field is set — every released CLI archive would be compiled with those tags")
			}
		}

		for _, ln := range logicalLines(text) {
			line := ln.text
			// A comment is not an invocation. Dockerfiles, Makefiles and
			// workflow YAML all comment with #, and this repository's build
			// files describe their own `go build` step in prose right above
			// it — counting those would make the scan report builds that
			// nothing runs.
			if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
				continue
			}
			if strings.Contains(line, "GOFLAGS") && buildTagFlag.MatchString(line) {
				offenders = append(offenders, rel+":"+strconv.Itoa(ln.num)+": GOFLAGS carries a build tag, which every `go build` under it would pick up silently — "+strings.TrimSpace(line))
				continue
			}
			sub := goSubcommand(line)
			if sub == "" {
				continue
			}
			switch sub {
			case "build":
				buildLines[rel]++
			case "install":
				installLines[rel]++
			}
			if !buildTagFlag.MatchString(line) {
				continue
			}
			if producesArtifact(sub) {
				offenders = append(offenders, rel+":"+strconv.Itoa(ln.num)+": `go "+sub+"` passes a build tag — "+strings.TrimSpace(line))
				continue
			}
			taggedTests++
		}
	}

	if len(offenders) != 0 {
		sort.Strings(offenders)
		t.Errorf(`a build that produces an artifact passes build tags (%d):
  %s

Passing -tags to `+"`go build`"+`, `+"`go install`"+` or `+"`go run`"+` changes which Go files are compiled into
the thing that ships. Files behind a build tag — the e2e harness, and
internal/remoteclient/tlsguard_bypass.go, which switches destination TLS
verification off — are classified in logGuardUnsweptFiles as unreachable
precisely because no build passes a tag. If a build genuinely needs one, that
classification has to be redone first: the file comes inside the log guard's
reach and every log line in it gets classified in logGuardSites.`,
			len(offenders), strings.Join(offenders, "\n  "))
	}

	// Non-vacuity. Exact numbers, compared with !=, on the three files that
	// actually produce a released Sharko artifact. If one of them stops
	// matching, the scan has gone blind or the way Sharko is built has
	// changed — either way somebody has to look.
	if taggedTests == 0 {
		t.Error("the scan saw no `go test -tags=...` line anywhere. The repository has several; seeing none means the scan is not reading the files it walked, so its silence about build lines proves nothing.")
	}
	if got := buildLines["Dockerfile"]; got != 1 {
		t.Errorf("Dockerfile has %d `go build` lines, want exactly 1 — that line is how the released container image is compiled, and the scan has to be able to see it", got)
	}
	if got := buildLines["Makefile"]; got != 1 {
		t.Errorf("Makefile has %d `go build` lines, want exactly 1 (the build-go target) — the scan has to be able to see it", got)
	}
	if got := installLines["Makefile"]; got != 1 {
		t.Errorf("Makefile has %d `go install` lines, want exactly 1 — the scan has to be able to see an install line too, since -tags there would be just as effective", got)
	}
	goreleaserSeen := false
	for _, rel := range files {
		if base := filepath.Base(rel); base == ".goreleaser.yaml" || base == ".goreleaser.yml" {
			goreleaserSeen = true
		}
	}
	if !goreleaserSeen {
		t.Error("the walk found no GoReleaser configuration. Every released CLI archive is built from it, so a scan that cannot see it is not checking the release build at all.")
	}
}

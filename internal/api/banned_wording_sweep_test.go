package api

// banned_wording_sweep_test.go — the wording ban that actually sweeps.
//
// # Why this exists
//
// "…what Sharko intends" survived in FIVE Go files, two swagger summaries,
// four CLI strings and a live server sentence, while SEVEN separate
// banned-phrase tests were already running. Every one of them banned a
// complete VERDICT SENTENCE inside ONE named file. A fragment that travelled
// across files was invisible to all of them.
//
// So this test does the opposite: it walks whole packages and looks for the
// FRAGMENT, case-insensitively, and reports every hit with its file and line.
// It is deliberately blunt. A phrase the product owner has killed should be
// impossible to reintroduce anywhere in the Go tree, including in a comment
// — comments are where the next person learns the wrong vocabulary.
//
// # It reads the tree, not this package
//
// The sweep walks up from this package to the repository root and covers the
// Go directories that produce user-visible words: the API, both reconcilers,
// the shared comparison core, the models and the CLI. `ui/` is deliberately
// out of scope (the TypeScript lists own that side) and `.bmad/` is out of
// scope on purpose — those are historical records of decisions, and
// rewriting history to match today's vocabulary would destroy the audit
// trail rather than improve it.
//
// # Line-wrapped hits count
//
// A comment can split a banned phrase across two lines with a `//` and
// leading spaces in between, which is exactly how one occurrence of this
// phrase hid from a plain grep. The sweep normalises comment markers and
// runs of whitespace to single spaces before matching, so a wrapped phrase
// is found too.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// bannedGoWordings are phrases the product owner has retired. Each entry
// says WHAT replaced it, because a test that only says "no" teaches nobody
// what to write instead.
var bannedGoWordings = []struct {
	phrase  string
	instead string
}{
	{
		// Ruling (b), 2026-08-19. The locked model is "Git defines the
		// connection; Sharko resolves credential references and maintains
		// the rendered Secret" — so a difference is a difference from GIT,
		// never from an intention of Sharko's.
		phrase: "sharko intends",
		instead: `"the Git-defined connection". Where a full sentence is needed, the ruled ` +
			`text is exactly: "At least one compared field differs from the Git-defined connection."`,
	},
}

// bannedSweepDirs are the Go directories the sweep covers, relative to the
// repository root.
var bannedSweepDirs = []string{
	"internal/api",
	"internal/clusterreconciler",
	"internal/argosecrets",
	"internal/connectioncompare",
	"internal/models",
	"cmd/sharko",
}

// bannedSweepExempt are files whose JOB is to name a banned phrase. Each one
// needs a reason here; anything else is a hit.
var bannedSweepExempt = map[string]string{
	"internal/api/banned_wording_sweep_test.go":            "this sweep — it has to hold the phrases to look for them",
	"internal/api/connection_messages_round6_test.go":      "per-file banned-phrase lists; naming the phrase is the assertion",
	"internal/api/connection_reconciliation.go":            "the replacement sentence's doc comment names the phrase it replaced",
	"cmd/sharko/repair_connection_test.go":                 "asserts the CLI no longer prints the phrase",
	"internal/clusterreconciler/drift_notice_text_test.go": "per-file banned-phrase list",
	"internal/connectioncompare/compare_test.go":           "per-file banned-phrase list",
}

// commentAndSpaceRun collapses `//` comment markers and any run of
// whitespace (including newlines) to a single space, so a phrase wrapped
// across two comment lines still matches.
var commentAndSpaceRun = regexp.MustCompile(`(?://+|\s)+`)

// bannedWrapWindow is how many consecutive lines are normalised together
// when looking for a phrase. Two would do for anything gofmt produces; three
// leaves room for a phrase broken by a blank comment line.
const bannedWrapWindow = 3

// repoRootForSweep walks up from the working directory until it finds go.mod.
func repoRootForSweep(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 12; i++ {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not find the repository root (no go.mod above the working directory)")
	return ""
}

// TestBannedWordings_NotAnywhereInTheGoTree is the sweep. It fails listing
// EVERY hit — not the first — because a phrase that spread once will have
// spread again, and fixing them one test run at a time is how the last five
// files got missed.
func TestBannedWordings_NotAnywhereInTheGoTree(t *testing.T) {
	root := repoRootForSweep(t)

	type hit struct {
		file, line, text, instead string
	}
	var hits []hit
	filesScanned := 0

	for _, dir := range bannedSweepDirs {
		abs := filepath.Join(root, dir)
		if _, err := os.Stat(abs); err != nil {
			t.Fatalf("swept directory %q does not exist — the sweep silently covers less than it claims: %v", dir, err)
		}
		walkErr := filepath.Walk(abs, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			rel = filepath.ToSlash(rel)
			if _, exempt := bannedSweepExempt[rel]; exempt {
				return nil
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			filesScanned++

			lines := strings.Split(string(body), "\n")
			for _, banned := range bannedGoWordings {
				// A sliding window over the lines, normalised, so a phrase
				// wrapped across a comment break is caught AND the reported
				// line number is the line it starts on — not every line
				// that happens to share a word with it.
				//
				// bannedWrapWindow lines is enough for any phrase gofmt
				// could split; the window is normalised as a unit, and a
				// hit is attributed to the first line of the window that is
				// not itself already the start of an earlier reported hit.
				lastReported := -1
				for i := range lines {
					end := i + bannedWrapWindow
					if end > len(lines) {
						end = len(lines)
					}
					window := strings.ToLower(commentAndSpaceRun.ReplaceAllString(strings.Join(lines[i:end], "\n"), " "))
					if !strings.Contains(window, banned.phrase) {
						continue
					}
					// The window starting at i matches. If the window
					// starting at i+1 also matches, the phrase begins later
					// — keep sliding so the reported line is exact.
					if i+1 < len(lines) {
						nextEnd := i + 1 + bannedWrapWindow
						if nextEnd > len(lines) {
							nextEnd = len(lines)
						}
						next := strings.ToLower(commentAndSpaceRun.ReplaceAllString(strings.Join(lines[i+1:nextEnd], "\n"), " "))
						if strings.Contains(next, banned.phrase) {
							continue
						}
					}
					if i == lastReported {
						continue
					}
					lastReported = i
					hits = append(hits, hit{rel, itoa(i + 1), strings.TrimSpace(lines[i]), banned.instead})
				}
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walking %s: %v", dir, walkErr)
		}
	}

	if filesScanned < 100 {
		t.Fatalf("the sweep only scanned %d Go files — it has lost its reach and would pass vacuously", filesScanned)
	}
	if len(hits) > 0 {
		var b strings.Builder
		b.WriteString("banned wording found in the Go tree:\n")
		for _, h := range hits {
			b.WriteString("  " + h.file + ":" + h.line + "  " + h.text + "\n")
		}
		b.WriteString("\nWrite instead: " + hits[0].instead + "\n")
		b.WriteString("\nThis sweep exists because the last banned fragment survived in five files while\n")
		b.WriteString("seven per-file banned-SENTENCE tests were passing. Fix every line above.\n")
		t.Fatal(b.String())
	}
	t.Logf("swept %d Go files across %d directories for %d banned wording(s)", filesScanned, len(bannedSweepDirs), len(bannedGoWordings))
}

// TestBannedWordings_SweepActuallyCatchesAWrappedPhrase proves the sweep is
// not passing vacuously: it feeds the matcher a phrase split across two
// comment lines — the exact shape that hid one real occurrence from grep —
// and requires a match.
func TestBannedWordings_SweepActuallyCatchesAWrappedPhrase(t *testing.T) {
	wrapped := "// successful response means the connection already matched what Sharko\n" +
		"\t// intends — worth saying plainly rather than implying a fix happened."
	normalised := strings.ToLower(commentAndSpaceRun.ReplaceAllString(wrapped, " "))
	if !strings.Contains(normalised, "sharko intends") {
		t.Fatalf("the sweep cannot see a phrase wrapped across two comment lines — it would pass vacuously.\nnormalised: %q", normalised)
	}

	// And a plain single-line occurrence, the ordinary case.
	plain := `	condComparisonDrift = "At least one compared field does not match what Sharko intends."`
	if !strings.Contains(strings.ToLower(commentAndSpaceRun.ReplaceAllString(plain, " ")), "sharko intends") {
		t.Fatal("the sweep cannot see a plain single-line occurrence")
	}

	// A phrase that is NOT banned must not match — the matcher is specific,
	// not a substring free-for-all.
	clean := "At least one compared field differs from the Git-defined connection."
	if strings.Contains(strings.ToLower(commentAndSpaceRun.ReplaceAllString(clean, " ")), "sharko intends") {
		t.Fatal("the sweep matched the approved replacement sentence")
	}
}

// itoa keeps the sweep free of a strconv import for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

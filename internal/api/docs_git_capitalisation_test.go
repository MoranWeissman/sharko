package api

// docs_git_capitalisation_test.go — the guard that Git is capitalised in the
// documentation a person reads, the same way git_capitalisation_test.go
// guards the sentences the server itself writes.
//
// # Why a second guard
//
// The rule is one rule: Git is a proper noun in text a person reads (product
// owner, 2026-08-20). The server side was fixed and guarded first. The
// documentation side was not guarded at all, and on 2026-08-21 it carried
// about a hundred and six lowercase prose occurrences across README.md and
// thirty files under docs/site/ — "desired state lives in git", "re-checked
// against git right then", "is a git file in your GitOps repository". A rule
// that holds in the product and not in the manual is half a rule.
//
// # The boundary, which is the same delimited-token rule as the Go guard
//
// The word stays lowercase where it is not prose at all — a value, a path
// segment, a field name, a flag or the command itself. Each of those is the
// word immediately after a quote, a backtick, an `=`, a `/`, a `-` or a `--`.
// Stating the rule once beats listing the sites, because the sites change and
// the rule does not, and TestDocsGitCapitalisation_DelimiterRuleHoldsBothWays
// pins it in both directions so it cannot quietly widen into an excuse.
//
// Markdown adds three things the Go guard does not have to think about, and
// each is skipped whole rather than pattern-matched:
//
//   - FENCED CODE BLOCKS. `git clone`, `git rev-parse`, a YAML sample with a
//     git: key. Fifty-eight occurrences live in these, and every one belongs
//     in lowercase.
//   - INLINE CODE SPANS. `.git`, `o.git.MergePullRequest`,
//     `connection.git.repoURL`, `$(git rev-parse --short HEAD)`. The
//     delimiter rule alone would miss these, because the character
//     immediately before the word is a dot or a bracket, not the backtick.
//   - LINK DESTINATIONS. `](git-native-config.md)` and every anchor. Renaming
//     a page is a different job and would break links, so a destination is
//     never a hit.
//
// # WHAT THIS GUARD DELIBERATELY DOES NOT COVER
//
//   - HYPHENATED COMPOUNDS: git-native, git-managed, git-backed, git-declared,
//     git-wins, in-git, out-of-band-git. Seventy-two of them, and "git-native"
//     in particular may be a deliberate product term — it names a page,
//     docs/site/operator/git-native-config.md. The product owner rules on the
//     whole set in one go; until then a compound is left exactly as written
//     and this guard does not fire on it. That is a NAMED gap, not an
//     accident, and TestDocsGitCapitalisation_DelimiterRuleHoldsBothWays says
//     so out loud so the next reader does not think it is covered.
//   - EVERY MARKDOWN FILE OUTSIDE README.md AND docs/site/. .claude/,
//     .bmad/, CONTRIBUTING.md, docs/design/ and docs/proposals/ are notes,
//     role files and historical records rather than the published manual.
//     docs/site/ is what mkdocs builds and README.md is the front door.
//
// # There is no count and no floor anywhere in this file
//
// A floor ("at least N files", "at least N sentences") is the shape that lets
// a guard go blind and stay green, and the sibling sweep had exactly that
// number in it until B19 took it out. What stands in for it: the sweep FATALS
// if it walks no files, if either root contributes no file, if it reads no
// prose line at all, or if it never sees the word in any casing anywhere; and
// the REAL detector — the same function the sweep calls — is run over a probe
// containing every shape it claims to see, and must find every one of them and
// nothing else.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// docsGitRoots are the published-documentation paths walked. A file is a root
// on its own; a directory is walked recursively. There is NO FILE LIST — a
// page written tomorrow is covered the day it is written.
var docsGitRoots = []string{"README.md", "docs/site"}

// markdownFence matches an opening or closing fence line, indented or not.
var markdownFence = regexp.MustCompile("^\\s*(```|~~~)")

// markdownInlineCode matches a single-backtick inline code span.
var markdownInlineCode = regexp.MustCompile("`[^`]*`")

// markdownLinkDestination matches the destination half of an inline link or
// image, so a filename or anchor is never read as prose.
var markdownLinkDestination = regexp.MustCompile(`\]\([^)]*\)`)

// docsDelimitedGit matches the occurrences that are a value, a path segment, a
// field name, a flag or the command itself: the word immediately preceded by a
// quote, a backtick, an `=`, a `/` or a `-` (which covers `--` too).
var docsDelimitedGit = regexp.MustCompile("(\"|'|`|=|/|-)git\\b")

// anyCaseGitWord matches the word in any casing. It exists only so the sweep
// can tell "the documentation is clean" apart from "the reader is broken and
// saw nothing at all".
var anyCaseGitWord = regexp.MustCompile(`(?i)\bgit\b`)

// docsAllowedLowercaseGit names the prose occurrences that have been looked at
// and KEPT lowercase, keyed by file and by the exact line text, one each with
// the reason.
//
// Keyed by the exact text on purpose: reword the line and the exception goes
// stale and fails, so the decision gets made again rather than inherited.
// Stale entries fail too — see the second half of the sweep. It is empty
// today, and an empty list is the honest state: every prose occurrence in the
// published docs was capitalised rather than excused.
var docsAllowedLowercaseGit = map[string]string{}

// blankOut replaces a match with spaces of the same length, so every column
// after it still lines up with the original line.
func blankOut(re *regexp.Regexp, line string) string {
	return re.ReplaceAllStringFunc(line, func(m string) string { return strings.Repeat(" ", len(m)) })
}

// markdownProseLine is one line of a published page that a person actually
// reads: `number` is the 1-based line number, `raw` is the line as written,
// and `readable` is the same line with every inline code span and every link
// destination blanked out to spaces of the same width, so columns still line
// up with the original.
type markdownProseLine struct {
	number   int
	raw      string
	readable string
}

// markdownProse is THE markdown walker, and there is only one of it on
// purpose.
//
// Two guards read the published pages — this one for the capitalisation of
// Git, and docs_wording_sweep_test.go for sentences that name Sharko's insides
// or promise a timeframe. Both need exactly the same three things skipped
// (fenced blocks, inline code spans, link destinations), and two copies of
// that logic is two things that can drift apart. It is the same reason
// banned_wording_sweep_test.go walks gitCapitalisationRoots rather than
// keeping a second list of trees.
//
// Fenced blocks are dropped entirely — they are not lines a person reads as
// prose — so a caller counting the lines it got back is counting prose lines,
// which is what tells "found nothing wrong" apart from "looked at nothing".
func markdownProse(body string) []markdownProseLine {
	var out []markdownProseLine
	inFence := false

	for i, raw := range strings.Split(body, "\n") {
		line := strings.TrimRight(raw, "\r")
		if markdownFence.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		readable := blankOut(markdownInlineCode, line)
		readable = blankOut(markdownLinkDestination, readable)
		out = append(out, markdownProseLine{number: i + 1, raw: line, readable: readable})
	}
	return out
}

// markdownGitHitsIn is THE detector. The sweep and the self-proof below both
// run this same function, so a change that blinds the detector blinds the
// proof too and cannot leave a green suite behind. proseLines and wordsSeen
// are counters the caller uses only to tell "found nothing wrong" apart from
// "looked at nothing".
func markdownGitHitsIn(rel, body string, proseLines, wordsSeen *int) []gitHit {
	var hits []gitHit

	for _, prose := range markdownProse(body) {
		line, readable := prose.raw, prose.readable
		*proseLines++
		*wordsSeen += len(anyCaseGitWord.FindAllString(readable, -1))

		stripped := blankOut(docsDelimitedGit, readable)
		for _, loc := range lowercaseGitWord.FindAllStringIndex(stripped, -1) {
			// A hyphenated compound — git-native, git-managed — is out of
			// scope until the product owner rules on the whole set. See this
			// file's header.
			if strings.HasPrefix(stripped[loc[1]:], "-") {
				continue
			}
			hits = append(hits, gitHit{rel, prose.number, "docs", strings.TrimSpace(line)})
		}
	}
	return hits
}

// docsMarkdownFiles lists every .md file under the walked roots,
// repo-relative. A root that cannot be read is fatal rather than skipped:
// silently covering less is the failure mode.
func docsMarkdownFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, entry := range docsGitRoots {
		abs := filepath.Join(root, entry)
		info, statErr := os.Stat(abs)
		if statErr != nil {
			t.Fatalf("documentation root %q cannot be read — the guard would silently cover less than it claims: %v", entry, statErr)
		}
		before := len(out)
		if !info.IsDir() {
			out = append(out, filepath.ToSlash(entry))
		} else {
			walkErr := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() || !strings.HasSuffix(path, ".md") {
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
				t.Fatalf("walking documentation root %q: %v", entry, walkErr)
			}
		}
		// Exact, not a floor. "This root contributed nothing" cannot be true
		// of any root while the guard is doing its job.
		if len(out) == before {
			t.Fatalf("documentation root %q contributed no markdown file at all — the guard covers nothing there", entry)
		}
	}
	sort.Strings(out)
	return out
}

// TestDocsGitCapitalisation_NoPageWritesItLowercase is the sweep.
func TestDocsGitCapitalisation_NoPageWritesItLowercase(t *testing.T) {
	root := repoRootForSweep(t)

	files := docsMarkdownFiles(t, root)
	if len(files) == 0 {
		t.Fatal("no markdown to walk — this guard would pass vacuously")
	}

	proseLines, wordsSeen := 0, 0
	var hits []gitHit
	sawAllowed := map[string]bool{}

	for _, rel := range files {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		for _, hit := range markdownGitHitsIn(rel, string(body), &proseLines, &wordsSeen) {
			key := gitExceptionKey(hit.file, hit.text)
			if _, ok := docsAllowedLowercaseGit[key]; ok {
				sawAllowed[key] = true
				continue
			}
			hits = append(hits, hit)
		}
	}

	// Standing in for a floor: two ways this guard could go blind, each fatal
	// rather than quietly green. Neither is a number that rots.
	if proseLines == 0 {
		t.Fatal("not one line of prose was read in the whole documentation tree — the reader is finding nothing and this guard is blind")
	}
	if wordsSeen == 0 {
		t.Fatal("the word does not appear, in any casing, anywhere in the published documentation — that cannot be true of this project, so the reader is broken")
	}

	if len(hits) > 0 {
		lines := make([]string, 0, len(hits))
		for _, h := range hits {
			lines = append(lines, h.String())
		}
		sort.Strings(lines)
		t.Errorf("%d documentation line(s) write Git in lowercase prose. It is a proper noun in text a\n"+
			"person reads (product owner, 2026-08-20). A value, a URL, a flag, a filename or the `git`\n"+
			"command stays lowercase — write it delimited (`git`, cleanup=git, /webhooks/git,\n"+
			"--git-repo) or inside a code fence, and this guard leaves it alone. Hyphenated compounds\n"+
			"(git-native, git-managed) are out of scope pending a product ruling. If it is genuinely\n"+
			"prose that must stay lowercase, add it to docsAllowedLowercaseGit with the reason:\n%s\n\n"+
			"(read %d lines of prose holding %d occurrences of the word across %d files)",
			len(hits), strings.Join(lines, "\n"), proseLines, wordsSeen, len(files))
	}

	// A kept exception that no longer exists is worse than no list: it reads
	// as a reviewed decision covering something that is gone.
	for key := range docsAllowedLowercaseGit {
		if !sawAllowed[key] {
			rel, text, _ := strings.Cut(key, "\x00")
			t.Errorf("docsAllowedLowercaseGit still excuses %s for %q, but no such line exists there any more — remove the stale entry.", rel, text)
		}
	}
}

// TestDocsGitCapitalisation_DetectorSeesEveryShape proves the detector fires,
// and on exactly what it claims.
//
// A sweep that reports nothing is indistinguishable from a sweep that looks at
// nothing. So the REAL detector — markdownGitHitsIn, the same function the
// sweep calls — runs over a markdown probe holding every shape it must catch
// and every shape it must leave alone, and must return exactly the catches.
func TestDocsGitCapitalisation_DetectorSeesEveryShape(t *testing.T) {
	probe := strings.Join([]string{
		"# Why Sharko",
		"",
		"Desired state lives in git, not in a CustomResource.",      // hit
		"Sharko re-checks the Secret against Git right then.",       // clean
		"Hidden directories such as `.git` are not descended.",      // inline code
		"Run `git rev-parse --short HEAD` to get the tag.",          // inline code
		"See [the git-native page](git-native-config.md) for how.",  // compound + link destination
		"The engine is git-managed and git-backed.",                 // compounds only
		"Pass cleanup=git to remove Git config only.",               // delimited value
		"POST /api/v1/webhooks/git accepts the push.",               // path segment
		"Run sharko connect --git-provider github to set it up.",    // flag
		"The two-direction policy is \"in ArgoCD but not in git\".", // hit
		"",
		"```bash",
		"git clone https://example.com/repo",
		"git log --oneline | head",
		"```",
		"",
		"Check the git provider quota before retrying.", // hit
	}, "\n")

	proseLines, wordsSeen := 0, 0
	var found []string
	for _, h := range markdownGitHitsIn("probe.md", probe, &proseLines, &wordsSeen) {
		found = append(found, fmt.Sprintf("%d: %s", h.line, h.text))
	}

	want := []string{
		"3: Desired state lives in git, not in a CustomResource.",
		`12: The two-direction policy is "in ArgoCD but not in git".`,
		"19: Check the git provider quota before retrying.",
	}
	if strings.Join(found, "\n") != strings.Join(want, "\n") {
		t.Errorf("the detector found:\n%s\n\nwant:\n%s\n\nThe real sweep's silence would mean nothing.",
			strings.Join(found, "\n"), strings.Join(want, "\n"))
	}

	// The counters are what tell "nothing is wrong" apart from "nothing was
	// looked at" in the sweep, so they must move on this probe.
	if proseLines == 0 || wordsSeen == 0 {
		t.Errorf("the detector read %d prose lines holding %d occurrences on a probe that plainly has both — the sweep's blindness checks would never fire", proseLines, wordsSeen)
	}

	// The fence really is skipped, rather than happening to hold nothing the
	// rule would fire on: the two lines inside it both open with the bare
	// lowercase word, which is the loudest possible hit.
	for _, h := range markdownGitHitsIn("probe.md", probe, &proseLines, &wordsSeen) {
		if h.line == 15 || h.line == 16 {
			t.Errorf("the detector fired inside a fenced code block, at line %d: %q", h.line, h.text)
		}
	}
}

// TestDocsGitCapitalisation_DelimiterRuleHoldsBothWays pins the judgement in
// this guard: which occurrences are a value or a compound rather than prose.
// Getting it wrong in one direction hides real defects; in the other it fires
// on flag names, filenames and shell commands and gets switched off.
func TestDocsGitCapitalisation_DelimiterRuleHoldsBothWays(t *testing.T) {
	mustFire := []string{
		"is an operator whose desired state lives in git, not in a CustomResource.",
		"The source of truth for default addons is a git file in your GitOps repository:",
		"- The secret is re-checked against git right then.",
		"| Real git provider round-trip (GitHub / GitLab) | ~3x |",
		"### What if git is rate-limited or unreachable?",
		"One direction is automatic because git is the declared source of truth.",
	}
	for _, line := range mustFire {
		n, w := 0, 0
		if len(markdownGitHitsIn("probe.md", line, &n, &w)) == 0 {
			t.Errorf("the guard cannot see lowercase Git in %q", line)
		}
	}

	mustNotFire := []string{
		"The live connection no longer matches what Git defines.",
		"Pass cleanup=git to remove Git config only.",
		"POST /api/v1/webhooks/git",
		"Run `git rev-parse --short HEAD` in the repository.",
		"Hidden directories (anything starting with `.`, e.g. `.git`, `.github`).",
		"See [the page](git-native-config.md) and [the other](git-provider-unreachable.md).",
		"`o.git.MergePullRequest` fails after a successful PR creation.",
		"Run sharko connect --git-provider github --git-repo https://example.com/org/addons",
		"gitops, gitProvider, github.com and Gitea are all left alone.",
		// The named gap. These are compounds the product owner has not ruled
		// on, and the guard must stay silent on them until that happens — a
		// guard that fires here would be switched off before the ruling lands.
		"The engine is git-native, git-managed, git-backed and git-declared.",
		"the in-argocd minus in-git delta",
	}
	for _, line := range mustNotFire {
		n, w := 0, 0
		if hits := markdownGitHitsIn("probe.md", line, &n, &w); len(hits) > 0 {
			t.Errorf("the guard fired on %q, which it must leave alone", line)
		}
	}
}

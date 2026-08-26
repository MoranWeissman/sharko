package api

// git_capitalisation_test.go — the guard that Git is capitalised in every
// sentence a person reads, anywhere in the server.
//
// # The rule
//
// Product owner, 2026-08-20: Git is a proper noun and is capitalised in text a
// person reads. Not code identifiers, not URLs, not command names, not the
// `git` binary — the word as a person reads it.
//
// # Why this file replaced two guards that were passing while the rule was broken
//
// There used to be two guards here, and each read a HAND-WRITTEN LIST of
// files: four files for sentence constants, one for inline literals and
// swagger lines. Both were green on 2026-08-21 while twelve shipped sentences
// and eleven published swagger descriptions wrote the word in lowercase —
// because every one of them lived in a file nobody had put on either list.
// internal/api/connection_credential_check.go was the defect in one line: it
// was on the sentence CATALOG's contract list, so the tree plainly considered
// it a file that authors sentences, and it was not on the Git guard's list.
//
// A list somebody has to remember to extend is a guard that is one file behind
// whoever forgets. Two rounds running have now found exactly that shape (the
// routing guard, B17; this one, B18). So there is NO FILE LIST here. Every
// non-test .go file under internal/ and cmd/ is walked, so a file written
// tomorrow is covered the day it is written.
//
// # What counts as a sentence
//
//   - A string literal whose FIRST RUNE IS UPPERCASE and which contains a
//     space. That is the same prose test the sentence catalog's inline sweep
//     uses (connection_sentence_catalog_test.go), and it is what separates
//     "Sharko could not read Git" from the wire values that live beside it —
//     "git_definition", "out_of_sync", "eks_token".
//   - Every swagger annotation line, whatever its shape. Those are published
//     API documentation a person reads, and eleven of them were wrong.
//
// WHAT IT DELIBERATELY DOES NOT COVER, so the boundary is visible rather than
// implied:
//
//   - Ordinary comments. `git clone` in an example belongs in lowercase and a
//     guard that fired on it would be worse than nothing.
//   - Go error strings, which by convention start lowercase and so are not
//     prose by the rule above. Those are wrapped and re-worded at the API
//     boundary before a person sees them — clusterreconciler.ErrClusterNotManaged
//     is the worked example: its own text says "git-managed cluster list" and
//     internal/api/clusters_resync.go replaces it wholesale with a
//     Git-capitalised sentence before writing the response. An error string
//     that DOES reach a person unchanged is a separate defect from this one
//     and is reported rather than papered over here.
//
// # One sentence shape that does NOT start with a capital
//
// The prose rule above misses a real class, found by hand while fixing this:
// the message recorded with recordReconcile. A SUCCEEDED or SKIPPED record's
// message reaches the browser EXACTLY AS RECORDED — only a Failed one goes
// through FailureSentence (lastReconcileMessage, api/clusters_reconcile.go) —
// and two of them opened lowercase, so the prose rule could not see them:
//
//	"connection repaired — rewritten to the connection git defines…"
//	"drift corrected — git-desired addon labels converged"
//
// So every string literal handed to recordReconcile is treated as a sentence
// whatever its first letter. That is the one place in this guard where a
// FUNCTION NAME is load-bearing, and it is narrow on purpose: widening to all
// lowercase-opening strings would fire on every wrapped Go error and every log
// line in the tree, and a guard that fires on those gets switched off.
//
// # The delimited-token rule, instead of an exception per site
//
// Most places the word must stay lowercase, it is not prose at all — it is a
// value, a path segment or a flag wearing prose's clothes:
//
//	"How much to remove: \"all\" (default), \"git\", or \"none\""   flag value
//	"Pass cleanup=git to remove Git config only."                   wire value
//	"POST /api/v1/webhooks/git"                                     URL path
//	"  sharko connect --git-provider github"                        CLI flag
//	"a `git` or `argocd` block"                                     field name
//
// Each of those is the word immediately after a quote, an `=`, a `/`, a
// backtick, or a `--`. Stating that once is better than listing the sites,
// because the sites change and the rule does not. Both directions of the rule
// are pinned by TestGitCapitalisation_DelimiterRuleHoldsBothWays, so it cannot
// quietly widen into an excuse.
//
// # There is no count and no floor anywhere in this file
//
// A floor ("at least N sentences were found") is the shape that lets a guard
// go blind and stay green, and this repo has already been bitten by one. What
// stands in for it: the sweep FATALS if it walks no files, finds no prose at
// all, or finds no swagger lines at all; and the REAL detector — the same
// function the sweep calls — is run over a probe that contains every shape it
// claims to see, and must find every one of them and nothing else.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// gitCapitalisationRoots are the trees walked, recursively. There is NO file
// list — see this file's header for why. A root that cannot be read is fatal
// rather than skipped: silently covering less is the failure mode.
var gitCapitalisationRoots = []string{"internal", "cmd"}

// lowercaseGitWord matches "git" as a standalone word — so "gitops",
// "gitProvider" and "github.com" are not hits, while "git defines" and
// "git-managed" are.
var lowercaseGitWord = regexp.MustCompile(`\bgit\b`)

// delimitedLowercaseGit matches the occurrences that are a value, a path
// segment, a field name or a flag rather than prose: the word immediately
// preceded by a quote, a backtick, an `=`, a `/`, or a `--`.
var delimitedLowercaseGit = regexp.MustCompile("(\"|'|`|=|/|--)git\\b")

// allowedLowercaseGit names the prose occurrences that have been looked at and
// KEPT lowercase, keyed by file and by the exact text, one line each with the
// reason.
//
// Keyed by the EXACT text on purpose: reword the sentence and the exception
// goes stale and fails, so the decision gets made again rather than inherited.
// Stale entries fail too — see the second half of the sweep.
var allowedLowercaseGit = map[string]string{
	gitExceptionKey("internal/envreg/registry.go",
		"Image tag the e2e harness deploys the in-cluster git fake from."): "names the gitfake harness component (tests/e2e/harness/gitfake), not the product",

	gitExceptionKey("internal/orchestrator/cluster.go",
		"RegisterCluster: git commit failed"): "names the `git commit` operation in an internal error, never shown as prose",
}

func gitExceptionKey(rel, text string) string { return rel + "\x00" + text }

// gitHit is one place a sentence writes the word in lowercase.
type gitHit struct {
	file string
	line int
	kind string // "sentence" or "swagger"
	text string
}

func (h gitHit) String() string {
	return fmt.Sprintf("  %s:%d  (%s)\n    %q", h.file, h.line, h.kind, h.text)
}

// looksLikeProse reports whether a string literal is text a person reads: an
// uppercase opening and at least one space.
func looksLikeProse(s string) bool {
	if !strings.Contains(s, " ") {
		return false
	}
	for _, r := range s {
		return unicode.IsUpper(r)
	}
	return false
}

// violatesGitRule reports whether text writes the word in lowercase somewhere
// that is NOT a delimited token.
func violatesGitRule(text string) bool {
	stripped := delimitedLowercaseGit.ReplaceAllString(text, "")
	return lowercaseGitWord.MatchString(stripped)
}

// gitHitsIn is THE detector. The sweep and the self-proof below both run this
// same function, so a change that blinds the detector blinds the proof too and
// cannot leave a green suite behind. proseSeen and swaggerSeen are counters the
// caller uses only to tell "found nothing wrong" from "looked at nothing".
func gitHitsIn(fset *token.FileSet, file *ast.File, rel string, proseSeen, swaggerSeen *int) []gitHit {
	var hits []gitHit

	// String literals handed to recordReconcile are sentences whatever their
	// first letter — see this file's header.
	recorded := map[ast.Node]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || calleeName(call.Fun) != "recordReconcile" {
			return true
		}
		for _, arg := range call.Args {
			if lit, isLit := arg.(*ast.BasicLit); isLit && lit.Kind == token.STRING {
				recorded[lit] = true
			}
		}
		return true
	})

	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		text, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		if !looksLikeProse(text) && !(recorded[lit] && strings.Contains(text, " ")) {
			return true
		}
		*proseSeen++
		if violatesGitRule(text) {
			hits = append(hits, gitHit{rel, fset.Position(lit.Pos()).Line, "sentence", text})
		}
		return true
	})

	for _, group := range file.Comments {
		for _, c := range group.List {
			line := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
			if !strings.HasPrefix(line, "@") {
				continue
			}
			*swaggerSeen++
			if violatesGitRule(line) {
				hits = append(hits, gitHit{rel, fset.Position(c.Pos()).Line, "swagger", line})
			}
		}
	}
	return hits
}

// calleeName renders the name of the function a call names, bare or
// qualified: both recordReconcile(...) and r.recordReconcile(...) resolve.
func calleeName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	}
	return ""
}

// gitSweepFiles lists every non-test .go file under the walked roots,
// repo-relative.
func gitSweepFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, dir := range gitCapitalisationRoots {
		walkErr := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
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
			t.Fatalf("walked tree %q cannot be read — the guard would silently cover less than it claims: %v", dir, walkErr)
		}
	}
	sort.Strings(out)
	return out
}

// TestGitCapitalisation_NoSentenceWritesItLowercase is the sweep.
func TestGitCapitalisation_NoSentenceWritesItLowercase(t *testing.T) {
	root := repoRootForSweep(t)

	files := gitSweepFiles(t, root)
	if len(files) == 0 {
		t.Fatal("no files to walk — this guard would pass vacuously")
	}

	proseSeen, swaggerSeen := 0, 0
	var hits []gitHit
	sawAllowed := map[string]bool{}

	for _, rel := range files {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parsing %s: %v", rel, err)
		}
		for _, hit := range gitHitsIn(fset, file, rel, &proseSeen, &swaggerSeen) {
			key := gitExceptionKey(hit.file, hit.text)
			if _, ok := allowedLowercaseGit[key]; ok {
				sawAllowed[key] = true
				continue
			}
			hits = append(hits, hit)
		}
	}

	// Standing in for a floor: three ways this guard could go blind, each
	// fatal rather than quietly green. None of them is a number that rots.
	if proseSeen == 0 {
		t.Fatal("not one sentence was found in the whole tree — the parser is finding nothing and this guard is blind")
	}
	if swaggerSeen == 0 {
		t.Fatal("not one swagger annotation was found in the whole tree — comments are not being parsed and every published description is unguarded")
	}

	if len(hits) > 0 {
		lines := make([]string, 0, len(hits))
		for _, h := range hits {
			lines = append(lines, h.String())
		}
		sort.Strings(lines)
		t.Errorf("%d sentence(s) a person reads write Git in lowercase. It is a proper noun in text a\n"+
			"person reads (product owner, 2026-08-20). A value, a URL, a flag or a field name stays\n"+
			"lowercase — write it delimited (\"git\", `git`, cleanup=git, /webhooks/git, --git-repo) and\n"+
			"this guard leaves it alone. If it is genuinely prose that must stay lowercase, add it to\n"+
			"allowedLowercaseGit with the reason:\n%s\n\n(looked at %d sentences and %d swagger lines across %d files)",
			len(hits), strings.Join(lines, "\n"), proseSeen, swaggerSeen, len(files))
	}

	// A kept exception that no longer exists is worse than no list: it reads
	// as a reviewed decision covering something that is gone.
	for key := range allowedLowercaseGit {
		if !sawAllowed[key] {
			rel, text, _ := strings.Cut(key, "\x00")
			t.Errorf("allowedLowercaseGit still excuses %s for %q, but no such sentence exists there any more — remove the stale entry.", rel, text)
		}
	}
}

// TestGitCapitalisation_DetectorSeesEveryShape proves the detector actually
// fires, and on exactly what it claims.
//
// A sweep that reports nothing is indistinguishable from a sweep that looks at
// nothing. So the REAL detector — gitHitsIn, the same function the sweep calls
// — runs over a probe holding every shape it must catch and every shape it
// must leave alone, and must return exactly the catches.
func TestGitCapitalisation_DetectorSeesEveryShape(t *testing.T) {
	const src = "package api\n" + `
// @Summary Check cluster connection secrets against git
// @Description Pass cleanup=git to remove Git config only.
// @Router /webhooks/git [post]
// git clone https://example.com/repo — an ordinary comment, never a hit.
func probe() []string {
	return []string{
		"Sharko could not read git, so this check did not finish.",
		"This cluster has no entry in the git-managed cluster list.",
		"How much to remove: \"all\" (default), \"git\", or \"none\"",
		"Sharko re-applied the labels Git declares.",
		"A request whose ` + "`git`" + ` block is empty is left alone.",
		"  sharko connect --git-provider github",
		"POST /api/v1/webhooks/git",
		"git_definition",
		"gitops and gitProvider and github.com",
		"cluster has no entry in the git-managed cluster list",
	}
}

func probeRecords(r *Reconciler) {
	r.recordReconcile("prod-eu", OutcomeSucceeded, "drift corrected — git-desired addon labels converged", nil)
	recordReconcile("prod-eu", OutcomeSkipped, "left alone — Git could not be read", nil)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "probe.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing the probe: %v", err)
	}

	prose, swagger := 0, 0
	var found []string
	for _, h := range gitHitsIn(fset, file, "probe.go", &prose, &swagger) {
		found = append(found, h.kind+": "+h.text)
	}
	sort.Strings(found)

	want := []string{
		"sentence: Sharko could not read git, so this check did not finish.",
		"sentence: This cluster has no entry in the git-managed cluster list.",
		"sentence: drift corrected — git-desired addon labels converged",
		"swagger: @Summary Check cluster connection secrets against git",
	}
	if strings.Join(found, "\n") != strings.Join(want, "\n") {
		t.Errorf("the detector found:\n%s\nwant:\n%s\n\nThe real sweep's silence would mean nothing.",
			strings.Join(found, "\n"), strings.Join(want, "\n"))
	}

	// The counters are what tell "nothing is wrong" apart from "nothing was
	// looked at" in the sweep, so they must move on this probe.
	if prose == 0 || swagger == 0 {
		t.Errorf("the detector reported %d sentences and %d swagger lines on a probe that plainly has both — the sweep's blindness checks would never fire", prose, swagger)
	}
}

// TestGitCapitalisation_DelimiterRuleHoldsBothWays pins the one piece of
// judgement in this guard: which occurrences are a value rather than prose.
// Getting it wrong in one direction hides real defects; in the other it fires
// on flag names and URLs and gets switched off.
func TestGitCapitalisation_DelimiterRuleHoldsBothWays(t *testing.T) {
	mustFire := []string{
		"The live connection no longer matches what git defines.",
		"This cluster has no entry in the git-managed cluster list.",
		"Sharko is not connected to a git repository right now.",
		"Sharko couldn't read the addon catalog or managed-clusters file in git.",
		"Check that Sharko can reach your git host, then click Refresh.",
		"Sharko couldn't converge git-desired addon labels on this Secret.",
		"@Failure 404 {object} map[string]interface{} \"This cluster is not in the git-managed cluster list\"",
	}
	for _, s := range mustFire {
		if !violatesGitRule(s) {
			t.Errorf("the guard cannot see lowercase Git in %q", s)
		}
	}
	mustNotFire := []string{
		"The live connection no longer matches what Git defines.",
		"Pass cleanup=git to remove Git config only.",
		"POST /api/v1/webhooks/git",
		"On a v4 repo the cleanup scope is (cleanup=all/git) instead.",
		"How much to remove: \"all\" (default), \"git\", or \"none\"",
		"A request whose `git` or `argocd` block is empty is left alone.",
		"Run: sharko connect --git-provider github --git-repo https://github.com/org/addons",
		"Rows filter: exact source match (e.g. `git`, AWS Secrets Manager)",
		"git_definition",
		"gitops",
		"gitProvider",
		"Clone it from github.com and try again.",
	}
	for _, s := range mustNotFire {
		if violatesGitRule(s) {
			t.Errorf("the guard fired on %q, which it must leave alone", s)
		}
	}

	// The prose filter itself, both ways: a wire value beside a sentence in
	// the same file must not be treated as text a person reads.
	for _, s := range []string{"Sharko could not read Git.", "A B"} {
		if !looksLikeProse(s) {
			t.Errorf("looksLikeProse rejects %q, which is a sentence", s)
		}
	}
	for _, s := range []string{"git_definition", "out_of_sync", "Blocked", "full_connection", ""} {
		if looksLikeProse(s) {
			t.Errorf("looksLikeProse accepts %q, which is not prose a person reads", s)
		}
	}
}

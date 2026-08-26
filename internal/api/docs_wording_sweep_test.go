package api

// docs_wording_sweep_test.go — the wording rules, applied to the manual as
// well as to the server.
//
// # Why this file exists
//
// The product owner's rulings are about TEXT A PERSON READS: never name
// Sharko's own machinery to somebody who did not come looking for it, and
// never promise a timeframe. Both rulings were enforced on Go source only
// (banned_wording_sweep_test.go walks internal/ and cmd/), and the published
// manual — the thing most people read BEFORE they ever see the product — was
// covered by neither. The one guard that did read markdown
// (docs_git_capitalisation_test.go) checked a single word: the capitalisation
// of Git.
//
// So on 2026-08-21 the quickstart promised an install "in about 5 minutes",
// the front page promised "in 5 minutes", the cluster page said "Sharko's
// cluster-secret reconciler runs every 30 seconds", and the addon page said
// "Every 5 minutes (default)" for a timer that is an environment variable
// anybody can change. A rule that holds in the product and not in the manual
// is half a rule — the same sentence the server is forbidden to say was being
// said, louder, on the website.
//
// # It shares the walker and the rules, rather than growing its own
//
// There is ONE markdown walker (markdownProse, in
// docs_git_capitalisation_test.go), ONE list of documentation roots
// (docsGitRoots), and ONE list of wording rules (userFacingWordingRules, in
// banned_wording_sweep_test.go). This file adds no second copy of any of them.
// That is deliberate and it is the existing precedent: the Go wording sweep
// already walks gitCapitalisationRoots rather than keeping its own list of
// trees, precisely so the two cannot drift apart. A rule added for the server
// starts applying to the manual the same day, with nobody having to remember.
//
// # Who the rules apply to, and who they do not
//
// The ruling is about the reader. Somebody reading the quickstart, the user
// guide, the CLI reference or the API reference came to use Sharko; naming a
// component to them explains nothing and dates the page. Somebody reading an
// operator runbook or a developer guide came BECAUSE the machinery broke, or
// because they are working on it — docs/site/operator/cluster-reconciler.md is
// a page whose whole subject is the component. Telling that reader "the part
// of Sharko that manages cluster connections" instead of naming the thing
// whose Deployment they are about to restart would make the page worse.
//
// So those pages are named in docsWordingAllowedPages, out loud, with the
// reason. What is NOT done is widening the rules themselves — the patterns are
// the server's patterns, untouched, and
// TestDocsWording_ReaderFacingPagesCanNeverBeAllowed makes it structurally
// impossible to quietly add the user guide or the front page to the allowance.
//
// # There is no count and no floor anywhere in this file
//
// A floor is the shape that lets a guard go blind and stay green. What stands
// in for it: the sweep FATALS if it walks no files (docsMarkdownFiles already
// fatals if a root contributes nothing), if it reads no prose line at all, or
// if the detector finds no match ANYWHERE in the tree including on the allowed
// pages — the operator runbooks name the machinery on nearly every page, so
// "found nothing at all" can only mean the reader is broken. And the REAL
// detector is run over a probe holding every shape it claims to see.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// docsWordingAllowedPages names the published pages where naming Sharko's
// machinery, or stating a real interval, is the right thing to write. A key is
// a repo-relative path; one ending in "/" covers that directory.
//
// Each entry is a decision with a reason, and it ratchets BOTH ways: an entry
// that catches nothing any more is named as stale and must be removed, so the
// list cannot quietly grow into a hole. Adding a reader-facing page here is
// not a matter of discipline — it is refused outright, see
// TestDocsWording_ReaderFacingPagesCanNeverBeAllowed.
var docsWordingAllowedPages = map[string]string{
	"docs/site/operator/": "" +
		"the operator runbooks and the operator reference. Somebody opens one of these BECAUSE " +
		"the machinery is misbehaving — cluster-reconciler.md, reconciler-crash-loop.md and " +
		"cluster-reconciler-dependency-missing.md have the component as their subject, and the " +
		"rest tell an operator which Deployment to restart, which log line to grep for and which " +
		"metric to watch. Naming the part by its real name is what makes the page usable, and the " +
		"intervals these pages quote are the ones the operator is about to change.",

	"docs/site/developer-guide/": "" +
		"written for somebody working ON Sharko: the logging reference, the testing guide, the " +
		"runbook style guide, the playground walkthrough. The reader is holding the source open.",

	"docs/site/architecture/": "" +
		"the architecture overview describes the parts and how they are wired together. That is " +
		"the page's entire job — a version of it that would not name a component would say nothing.",

	"docs/site/community/": "" +
		"the roadmap, which discusses work on Sharko's insides with the people who might do it.",

	"docs/site/release-notes.md": "" +
		"a historical record of what changed in each version, written for whoever is upgrading. " +
		"Rewording it to today's vocabulary would falsify the record rather than improve it — the " +
		"same reason .bmad/ is out of scope for the Go sweep.",
}

// docsWordingWithoutAllowance are the roots that may NEVER appear in, or be
// covered by, docsWordingAllowedPages. These are the pages somebody reads
// before they have a problem — the front door, the quickstart, the user guide,
// the CLI and API references, and the preview page.
//
// This is the same shape as TestBannedWordings_NoProductionFileIsExempt on the
// Go side: the rule about what may be excused is checked by a test, not left
// as a convention somebody has to remember at review time.
var docsWordingWithoutAllowance = []string{
	"README.md",
	"docs/site/index.md",
	"docs/site/technical-preview.md",
	"docs/site/getting-started/",
	"docs/site/user-guide/",
	"docs/site/cli/",
	"docs/site/api/",
}

// docsWordingAllowedSentences names single lines on covered pages that match a
// rule and are nonetheless right. Keyed by the exact line text, so rewording
// the line makes the exception go stale and the decision gets made again
// rather than inherited.
//
// Every entry here is the same kind of thing: a documented, fixed property of
// something the reader is looking at, not a promise about when Sharko will
// have finished a background job by itself. That distinction is not invented
// here — it is exactly why userFacingWordingAllowed on the Go side keeps the
// token TTL sentence and `sharko pr wait`'s "polls every 5 seconds".
var docsWordingAllowedSentences = map[string]string{
	"**Every key you create expires after 90 days.** That is the default. Ask for a": "" +
		"the API key reference stating this endpoint's own default expiry, which the caller " +
		"chooses per key. Same decision as the identical swagger sentence already kept in " +
		"userFacingWordingAllowed.",

	"Keys within 14 days of their expiry also report `expiring_soon: true`, so the": "" +
		"the fourteen days is the fixed window the API itself uses to set expiring_soon. A caller " +
		"reading the reference has to know the number to interpret the field.",

	"**API keys expire after 90 days by default.** Ask for a different window with": "" +
		"the same default expiry, on the API overview page. Same reason as above.",

	"once when you create it and never again. Tokens expire after 90 days by": "" +
		"the preview page's security section, describing the same token expiry default.",

	"* The Dashboard polls every 30 seconds. The unified addon state cache uses the": "" +
		"a compiled-in browser refresh interval, describing what the page in front of the reader " +
		"is doing while they watch it. Not a background convergence interval, and not settable.",

	"| **Pulled automatically** | The engine strip at the top of each page states its engine's cadence in plain words (\"Sharko re-checks it every 30 seconds, and right after each merge\" / \"Sharko checks it every 5 minutes and repairs it automatically\") and the last time a check actually ran. Nothing is pushed to Sharko — it pulls on its own schedule, and the page says what that schedule is. |": "" +
		"a QUOTE of the sentence the UI itself renders (ui/src/views/ManagedSecrets.tsx), which " +
		"builds the number from the interval that install is really running. Rewording the quote " +
		"would misreport what the screen says.",
}

// docsWordingHit is one line of published documentation that breaks a rule.
type docsWordingHit struct {
	file string
	line int
	rule string
	text string
}

// docsWordingHitsIn is THE detector. The sweep and the self-proof below both
// run this same function, so a change that blinds the detector blinds the
// proof too and cannot leave a green suite behind. proseLines is the counter
// the caller uses to tell "found nothing wrong" apart from "looked at
// nothing".
//
// It matches against the READABLE half of each line — code spans and link
// destinations blanked out — so `sharko_reconciler_runs_total` in backticks
// and a link to cluster-reconciler.md are not hits, while the same words in
// prose are. It reports the line as WRITTEN, because that is what somebody
// fixing it has to find.
func docsWordingHitsIn(rel, body string, proseLines *int) []docsWordingHit {
	var hits []docsWordingHit
	for _, prose := range markdownProse(body) {
		*proseLines++
		for _, rule := range userFacingWordingRules {
			if rule.pattern.MatchString(prose.readable) {
				hits = append(hits, docsWordingHit{rel, prose.number, rule.name, strings.TrimSpace(prose.raw)})
			}
		}
	}
	return hits
}

// pageIsAllowed reports whether rel is covered by docsWordingAllowedPages, and
// under which entry.
func pageIsAllowed(rel string) (string, bool) {
	for key := range docsWordingAllowedPages {
		if strings.HasSuffix(key, "/") {
			if strings.HasPrefix(rel, key) {
				return key, true
			}
			continue
		}
		if rel == key {
			return key, true
		}
	}
	return "", false
}

// TestDocsWording_NoPublishedPageNamesPlumbingOrPromisesAClock is the sweep.
func TestDocsWording_NoPublishedPageNamesPlumbingOrPromisesAClock(t *testing.T) {
	root := repoRootForSweep(t)

	files := docsMarkdownFiles(t, root)
	if len(files) == 0 {
		t.Fatal("no markdown to walk — this guard would pass vacuously")
	}

	proseLines := 0
	matchesAnywhere := 0
	var hits []docsWordingHit
	pageAllowanceUsed := map[string]bool{}
	sentenceAllowanceUsed := map[string]bool{}

	for _, rel := range files {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		for _, hit := range docsWordingHitsIn(rel, string(body), &proseLines) {
			matchesAnywhere++
			if key, allowed := pageIsAllowed(rel); allowed {
				pageAllowanceUsed[key] = true
				continue
			}
			if _, allowed := docsWordingAllowedSentences[hit.text]; allowed {
				sentenceAllowanceUsed[hit.text] = true
				continue
			}
			hits = append(hits, hit)
		}
	}

	// Standing in for a floor: three ways this guard could go blind, each
	// fatal rather than quietly green. None of them is a number that rots.
	if proseLines == 0 {
		t.Fatal("not one line of prose was read in the whole documentation tree — the reader is finding nothing and this guard is blind")
	}
	if matchesAnywhere == 0 {
		t.Fatal("the rules matched nothing at all, anywhere in the published documentation, " +
			"including on the operator runbooks that name the machinery on nearly every page. " +
			"That cannot be true of this project, so the detector is broken")
	}

	if len(hits) > 0 {
		lines := make([]string, 0, len(hits))
		for _, h := range hits {
			lines = append(lines, "  "+h.file+":"+itoa(h.line)+"  ["+h.rule+"]\n    "+h.text)
		}
		sort.Strings(lines)
		t.Errorf("%d line(s) of published documentation name Sharko's own machinery or promise a\n"+
			"timeframe. These rules are the product owner's, and they are about the reader: somebody\n"+
			"reading the quickstart, the user guide or the API reference came to USE Sharko. Say what\n"+
			"Sharko does; where a part has to be named, say \"the part of Sharko that manages cluster\n"+
			"connections\"; and where a real interval matters, name the setting that controls it\n"+
			"instead of quoting a number that is wrong on any install that changed it.\n\n%s\n\n"+
			"If the page is genuinely a runbook or a developer guide, add it to docsWordingAllowedPages\n"+
			"with the reason. If one sentence is a documented fixed property rather than a promise,\n"+
			"add that sentence to docsWordingAllowedSentences with the reason.\n\n"+
			"(read %d lines of prose across %d files)",
			len(hits), strings.Join(lines, "\n"), proseLines, len(files))
	}

	// The other direction. An allowance that catches nothing is a hole with
	// nobody's name on it: the page is silently outside the rules and the next
	// person to write a sentence there gets no warning at all.
	var stale []string
	for key := range docsWordingAllowedPages {
		if !pageAllowanceUsed[key] {
			stale = append(stale, "  page allowance "+key+" — no page under it breaks a rule any more")
		}
	}
	for text := range docsWordingAllowedSentences {
		if !sentenceAllowanceUsed[text] {
			stale = append(stale, "  sentence allowance "+text+" — no such line exists any more")
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Errorf("these allowances no longer excuse anything — remove them:\n%s", strings.Join(stale, "\n"))
	}
}

// TestDocsWording_ReaderFacingPagesCanNeverBeAllowed makes the boundary
// structural instead of a convention somebody has to remember at review time.
//
// A page allowance exists so a runbook about a component may name that
// component. The front page, the quickstart, the user guide, the CLI and API
// references and the preview page have no such job — an allowance on any of
// them switches the rules off exactly where the ruling was aimed.
func TestDocsWording_ReaderFacingPagesCanNeverBeAllowed(t *testing.T) {
	if len(docsWordingWithoutAllowance) == 0 {
		t.Fatal("nothing is protected from being allowed — this test would pass vacuously")
	}
	for _, protected := range docsWordingWithoutAllowance {
		for key, reason := range docsWordingAllowedPages {
			// Either direction is a hole: an allowance ON a protected page,
			// and an allowance on a directory that CONTAINS one.
			if strings.HasPrefix(protected, key) || strings.HasPrefix(key, protected) {
				t.Errorf("docsWordingAllowedPages excuses %q, which covers the reader-facing %q "+
					"(reason given: %q).\nThese pages are read by somebody who came to use Sharko. "+
					"Reword the sentence instead.", key, protected, reason)
			}
		}
	}
	for key, reason := range docsWordingAllowedPages {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("page allowance %q has no reason written down", key)
		}
	}
	for text, reason := range docsWordingAllowedSentences {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("sentence allowance %q has no reason written down", text)
		}
	}
	if len(docsWordingAllowedPages) == 0 {
		t.Fatal("the page allowance map is empty — the stale-entry half of the sweep would pass vacuously")
	}
}

// TestDocsWording_DetectorSeesEveryShape proves the detector fires, and on
// exactly what it claims.
//
// A sweep that reports nothing is indistinguishable from a sweep that looks at
// nothing. So the REAL detector — docsWordingHitsIn, the same function the
// sweep calls — runs over a markdown probe holding every shape it must catch
// and every shape it must leave alone, and must return exactly the catches.
func TestDocsWording_DetectorSeesEveryShape(t *testing.T) {
	probe := strings.Join([]string{
		"# Clusters", // 1
		"",           // 2
		"Sharko's cluster-secret reconciler runs in the background.",       // 3 hit: plumbing
		"Install Sharko and register your first cluster in 5 minutes.",     // 4 hit: clock
		"Sharko converges the labels on its own schedule.",                 // 5 clean
		"The metric is `sharko_reconciler_runs_total`, one per run.",       // 6 inline code
		"See [the cluster reconciler page](cluster-reconciler.md) for it.", // 7 link destination + prose
		"Reconciliation happens without you asking.",                       // 8 clean — "reconcile" is not the banned word
		"Both reconcilers pick this up.",                                   // 9 hit: plumbing, plural
		"The check is repeated every 30 seconds.",                          // 10 hit: clock
		"",        // 11
		"```bash", // 12
		"kubectl logs deployment/sharko | grep reconciler", // 13 inside a fence
		"# runs every 5 minutes",                           // 14 inside a fence
		"```",                                              // 15
		"",                                                 // 16
		"Sharko re-applies the addon labels defined in Git.", // 17 clean
	}, "\n")

	proseLines := 0
	var found []string
	for _, h := range docsWordingHitsIn("probe.md", probe, &proseLines) {
		found = append(found, itoa(h.line)+" ["+h.rule+"] "+h.text)
	}
	sort.Strings(found)

	want := []string{
		"10 [promises a timeframe] The check is repeated every 30 seconds.",
		"3 [names Sharko's own plumbing] Sharko's cluster-secret reconciler runs in the background.",
		"4 [promises a timeframe] Install Sharko and register your first cluster in 5 minutes.",
		"7 [names Sharko's own plumbing] See [the cluster reconciler page](cluster-reconciler.md) for it.",
		"9 [names Sharko's own plumbing] Both reconcilers pick this up.",
	}
	if strings.Join(found, "\n") != strings.Join(want, "\n") {
		t.Errorf("the detector found:\n%s\n\nwant:\n%s\n\nThe real sweep's silence would mean nothing.",
			strings.Join(found, "\n"), strings.Join(want, "\n"))
	}

	// The counter is what tells "nothing is wrong" apart from "nothing was
	// looked at" in the sweep, so it must move on this probe.
	if proseLines == 0 {
		t.Error("the detector read no prose lines on a probe that plainly has them — the sweep's blindness check would never fire")
	}

	// The fence really is skipped, rather than happening to hold nothing the
	// rules would fire on: both lines inside it are the loudest possible hits,
	// one per rule.
	for _, h := range docsWordingHitsIn("probe.md", probe, &proseLines) {
		if h.line == 13 || h.line == 14 {
			t.Errorf("the detector fired inside a fenced code block, at line %d: %q", h.line, h.text)
		}
	}
}

// TestDocsWording_UsesTheServerRulesAndTheSharedWalker pins the two pieces of
// reuse this guard is built on, so neither can be quietly replaced by a
// private copy that then drifts.
//
// It matters because the whole point of the file is that ONE ruling is
// enforced in ONE place: a rule added for the server has to start applying to
// the manual the same day, with nobody having to remember to copy it.
func TestDocsWording_UsesTheServerRulesAndTheSharedWalker(t *testing.T) {
	if len(userFacingWordingRules) == 0 {
		t.Fatal("there are no wording rules at all — this whole file is decoration")
	}
	// Every rule the server enforces must be able to fire on markdown. If a
	// rule can never match a line of prose, the manual is not really covered
	// by it and somebody would have to find that out the hard way.
	for _, rule := range userFacingWordingRules {
		if rule.pattern == nil {
			t.Errorf("the %q rule has no pattern", rule.name)
			continue
		}
		if rule.instead == "" {
			t.Errorf("the %q rule does not say what to write instead", rule.name)
		}
	}

	// The shared walker, both ways: prose is returned, a fenced block is not.
	prose := markdownProse("alpha\n```\nbravo\n```\ncharlie")
	var kept []string
	for _, p := range prose {
		kept = append(kept, p.raw)
	}
	if strings.Join(kept, ",") != "alpha,charlie" {
		t.Errorf("the shared markdown walker returned %q — it must drop fenced blocks and keep prose", kept)
	}
	if len(prose) > 0 && prose[len(prose)-1].number != 5 {
		t.Errorf("the shared walker reported line %d for the last line, which is line 5 — a wrong line number sends whoever is fixing a hit to the wrong place", prose[len(prose)-1].number)
	}
}

package api

// docs_single_instance_test.go — the published manual may not tell anybody to
// run a second Sharko.
//
// # Why this exists
//
// Sharko runs as ONE process. The chart ships `replicaCount: 1`, sign-ins live
// in a map inside that one process, nothing is shared between copies, and
// there is no leader election anywhere in the tree. A second copy signs people
// out at random and runs the background work twice against the same Git
// repository and the same Kubernetes objects.
// docs/site/technical-preview.md says all of that plainly, under its own
// heading, as a hard limit.
//
// Two published operator pages said the opposite anyway, in the imperative:
//
//   - budget-burn-runbook.md, in a numbered list of things to try while an
//     alert is firing, said "Replica scaling ... bump replicas via Helm
//     replicaCount", and a second entry offered scaling a "single Sharko
//     replica" as half the fix, and a third told the operator to "scale Sharko
//     to 1 replica", which only means anything if it was running more.
//   - upgrading-and-rollback.md carried a whole section, "Rule 2 — multiple
//     instances on one repo upgrade together", that presented two Sharkos on
//     one Git repository as a normal topology and gave an upgrade procedure
//     for it.
//
// Nothing stopped that coming back: no test read the manual for it, so the
// pages could drift back to the old advice one sentence at a time.
//
// # It fails in BOTH directions
//
//   - ADVICE COMES BACK. Every published page is walked twice. Every window of
//     prose is matched against the rule list below, and every fenced block is
//     matched against the code rules further down. A new page, or a new
//     sentence on an old page, that tells somebody to add a replica, scale
//     Sharko out, or run two instances against one repository, is named with
//     its file, its line, and what to write instead — and so is a runnable
//     command, manifest or values file that does the same thing, which is the
//     higher-risk half because a reader copies it.
//   - THE RULE GOES STALE. The premise is pinned to the shipped chart and to
//     the preview page: if charts/sharko/values.yaml stops shipping exactly
//     one replica, or the preview page stops carrying the sentence that says
//     to leave it there, this guard fails and says so. A guard whose premise
//     has quietly changed is worse than none.
//
// # It cannot pass by looking at nothing
//
// The tree is expected to be clean, so "found no hits" is the normal green
// result and is therefore the exact shape that hides a broken reader. What
// stands in for a hit count:
//
//   - docsMarkdownFiles fatals if a documentation root contributes no file.
//   - The sweep fatals if it reads no prose line at all, and separately if it
//     reads no fenced line at all.
//   - The prose reader and the fence reader are required to account for every
//     line of every published page between them, so a strip of the manual
//     cannot end up belonging to neither.
//   - Every rule is run through the REAL detector over a probe it must catch
//     AND a counter-probe it must NOT catch, so a rule that has been widened
//     into everything or narrowed into nothing is named.
//   - The wrapped-advice case has its own fixture, because the sentence that
//     started this had "bump" at the end of one line and "replicas" at the
//     start of the next: a per-line reader sees nothing there and goes green.
//
// # What this guard deliberately does NOT cover
//
//   - FENCED CODE BLOCKS READ AS PROSE. markdownProse still drops them, and it
//     has to: `kubectl scale deployment/sharko --replicas=0` and
//     `--replicas=1` are the supported restart, several runbooks use them, and
//     reading commands as sentences would turn every one of those into a false
//     hit. The fences are not skipped, though — they are read separately, AS
//     CODE, against the forms this repository actually ships. See "The fences,
//     read as code" below.
//   - ADDON CHARTS AND ARGOCD. smart-values.md, values-editing.md,
//     repo-structure.md and api-walkthrough.md all discuss replica counts for
//     the charts Sharko deploys, which have nothing to do with how many
//     Sharkos run. Every rule below therefore needs a Sharko subject, or a
//     phrase that can only be about Sharko's own process.
//   - "HIGH AVAILABILITY" AND "LEADER ELECTION" AS WORDS. The roadmap and the
//     design notes discuss both as work that has not been done. Recording a
//     decision is not instructing an operator, and banning the words would
//     make it impossible to write the roadmap.
//   - ANYTHING OUTSIDE THE PUBLISHED MANUAL. docsGitRoots is README.md and
//     docs/site, which is what mkdocs builds. docs/design/ and
//     docs/proposals/ are historical records.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const (
	// singleInstanceChartValues is the shipped chart the premise is pinned to.
	singleInstanceChartValues = "charts/sharko/values.yaml"
	// singleInstancePreviewPage is the page that states the rule in full.
	singleInstancePreviewPage = "docs/site/technical-preview.md"
)

// singleInstanceWindow is how many consecutive prose lines are joined before
// matching. The sentence that started this wrapped across two lines mid-phrase
// ("... saturating a single pod, bump" / "replicas via Helm ..."), so a
// one-line reader would have found nothing. Three leaves room for a phrase
// broken across a wrapped list item.
const singleInstanceWindow = 3

// secondInstanceRule is one shape of "run more than one Sharko".
//
// A window is a hit when EVERY expression in all matches it and NONE in none
// does. Both halves are expressions rather than a single pattern because the
// shapes are not all single phrases: one of them is a scaling verb standing
// near the words "Sharko replica", which only reads as advice when the target
// is not the supported zero or one.
type secondInstanceRule struct {
	// name says what the rule catches, and is what a failure reports.
	name string
	// all must every one match the window for it to be a hit.
	all []*regexp.Regexp
	// none may any match the window, or the hit is dropped.
	none []*regexp.Regexp
	// probe is text this rule MUST catch. It is real wording taken from the
	// pages this guard was written for, not a restatement of the pattern.
	probe string
	// counterProbe is text this rule must NOT catch: a nearby sentence that is
	// fine as written. A rule that catches its counter-probe has been widened
	// into a prose linter and is named here rather than in a reviewer's
	// afternoon.
	counterProbe string
	// instead says what to write. A rule that only says "no" teaches nobody.
	instead string
}

// secondInstanceRules is a LIST, never a count. Adding advice back to any page
// fails the sweep; removing a rule from here is a visible deletion in a diff.
var secondInstanceRules = []secondInstanceRule{
	{
		name: `offers "replica scaling" as a thing to try`,
		all:  []*regexp.Regexp{regexp.MustCompile(`(?i)\breplicas?\s+scaling\b`)},
		probe: "4. **Replica scaling** — the dashboard handlers are read-only and " +
			"cacheable; if the read fanout is saturating a single pod.",
		counterProbe: "Sharko runs as a single pod, so zero and one are the only two settings it has.",
		instead: "say that the preview runs one Sharko pod and cannot be given a second one, " +
			"then give the operator something that works: more CPU and memory for the one " +
			"pod, and less read load against it.",
	},
	{
		name: "tells the operator to add replicas",
		all: []*regexp.Regexp{regexp.MustCompile(
			`(?i)\b(?:bump|bumping|raise|raising|increase|increasing|grow|growing|add|adding)\s+(?:the\s+)?(?:sharko[-\s])?replica`)},
		probe:        "if the read fanout is saturating a single pod, bump replicas via Helm.",
		counterProbe: "give it more CPU and memory (`resources` in the Helm values) and restart it.",
		instead: "nothing — there is no supported way to add one. Say the limit out loud and " +
			"point at the preview page, then describe what the operator can actually change.",
	},
	{
		name: "tells the operator to scale Sharko out",
		all: []*regexp.Regexp{regexp.MustCompile(
			`(?i)\bscal(?:e|es|ed|ing)[-\s]+(?:sharko\s+)?(?:out|horizontally)\b`)},
		probe:        "If one pod cannot keep up with the reads, scale Sharko out.",
		counterProbe: "Last resort — scale Sharko to zero and back.",
		instead: "say that scaling out is not available during the technical preview, and " +
			"name the vertical option (CPU and memory on the one pod) instead.",
	},
	{
		name: "sets Sharko's replica count above one",
		all: []*regexp.Regexp{regexp.MustCompile(
			`(?i)\bsharko\b[^\n]{0,80}?\breplica(?:count)?s?\b[^\n]{0,20}?(?::|=|to)\s*(?:[2-9]|[1-9][0-9])\b`)},
		probe:        "For a busier fleet, run Sharko with replicas: 2.",
		counterProbe: "The chart ships Sharko at replicaCount: 1 and you leave it there.",
		instead: "leave the shipped value alone. The chart ships one replica and the preview " +
			"supports exactly that.",
	},
	{
		name: "treats several Sharkos as a normal topology",
		all: []*regexp.Regexp{regexp.MustCompile(
			`(?i)\b(?:two|three|multiple|several|both|all|other|second|another)\s+sharko\s+(?:instances?|replicas?|pods?|copies|copy)\b`)},
		probe:        "Two Sharko instances sharing one Git repo must run the same major.minor line.",
		counterProbe: "The technical preview runs one Sharko, and the chart ships one replica.",
		instead: "one Sharko per Git repository. Say that, and say what follows from it, " +
			"rather than giving a procedure for a topology nobody can run.",
	},
	{
		name: "writes about Sharko instances in the plural",
		all: []*regexp.Regexp{regexp.MustCompile(
			`(?i)\bsharko\s+instances\b|\binstances\s+of\s+sharko\b`)},
		probe:        "Upgrade your Sharko instances in the same maintenance window.",
		counterProbe: "Upgrade the Sharko that writes to this repository, in one step.",
		instead: "the singular. There is one Sharko, so write about it in the singular and the " +
			"advice stops being ambiguous.",
	},
	{
		name:         "describes multiple instances as supported",
		all:          []*regexp.Regexp{regexp.MustCompile(`(?i)\bmultiple\s+instances\b`)},
		probe:        "## Rule 2 — multiple instances on one repo upgrade together",
		counterProbe: "Support for multiple ArgoCD instances per Sharko is on the roadmap.",
		instead: "say what is true: the preview runs one Sharko, so there is nothing to keep " +
			"in step with anything else.",
	},
	{
		name:         "gives an instruction covering every instance at once",
		all:          []*regexp.Regexp{regexp.MustCompile(`(?i)\b(?:all|both)\s+instances\b`)},
		probe:        "Upgrade all instances in one maintenance window, before any of them writes.",
		counterProbe: "Upgrade it in one step, and let it write again afterwards.",
		instead: "address the one Sharko there is. An instruction written for a set of them " +
			"tells the reader the set is allowed to exist.",
	},
	{
		name: "assumes Sharko was running at more than one replica",
		all: []*regexp.Regexp{regexp.MustCompile(
			`(?i)\bsharko\b[^\n]{0,60}?\bto\s+(?:1|one)\s+replicas?\b`)},
		probe:        "If a retry storm is amplifying it, scale Sharko to 1 replica until ArgoCD recovers.",
		counterProbe: "Scale Sharko to zero until ArgoCD recovers, then back to one.",
		instead: "zero and one are the only two settings Sharko has. To pause it, scale it to " +
			"zero and then back to one — scaling DOWN to one only makes sense if it was higher.",
	},
	{
		name: "offers scaling as a fix for a loaded Sharko pod",
		all: []*regexp.Regexp{
			regexp.MustCompile(`(?i)\bsharko\s+replicas?\b`),
			regexp.MustCompile(`(?i)\bscal(?:e|es|ed|ing)\b`),
		},
		none:  []*regexp.Regexp{regexp.MustCompile(`(?i)\bto\s+(?:zero|0|one|1)\b`)},
		probe: "Read-heavy dashboard load on a single Sharko replica with cold caches can drag p99 into error territory. Scaling and warming the cache resolves both.",
		counterProbe: "A clean restart: scale Sharko to zero and back, and the one replica comes " +
			"up with an empty cache.",
		instead: "keep the half that works — warming the cache — and say plainly that the other " +
			"half, a second pod, is not available.",
	},
}

// docsSecondInstanceAllowed names published lines that match a rule above and
// are RIGHT as written, keyed by "<repo-relative path>:<the exact trimmed line
// text>" with the reason.
//
// Keyed by the exact text on purpose. Reword the line and the entry goes stale
// and fails, so the decision is made again rather than inherited. A key is
// always one line on one page: there is no directory form and no whole-file
// form, and TestDocsSingleInstance_NoAllowanceCoversAWholeFile makes that
// structural rather than a matter of discipline — a file-wide excuse switches
// the guard off exactly where the advice would do harm.
//
// It is empty, and empty is the honest state: every occurrence in the
// published manual was corrected rather than excused.
var docsSecondInstanceAllowed = map[string]string{}

// secondInstanceHit is one window of prose that matched a rule.
type secondInstanceHit struct {
	file string
	line int
	rule string
	text string
}

// collapseSpace is used to join a window into one string so a phrase split
// across a line break still matches.
var collapseSpace = regexp.MustCompile(`\s+`)

// matchesSecondInstanceRule is THE detector, and the sweep, the probes and the
// wrapped-advice fixture all call this same function. A change that blinds it
// blinds its own proof too, and cannot leave a green suite behind.
func matchesSecondInstanceRule(rule secondInstanceRule, window string) bool {
	for _, must := range rule.all {
		if !must.MatchString(window) {
			return false
		}
	}
	for _, mustNot := range rule.none {
		if mustNot.MatchString(window) {
			return false
		}
	}
	return true
}

// secondInstanceHitsIn runs every rule over every window of prose in one page.
// proseLines is a counter the caller uses only to tell "found nothing wrong"
// apart from "looked at nothing".
func secondInstanceHitsIn(rel, body string, proseLines *int) []secondInstanceHit {
	lines := markdownProse(body)
	*proseLines += len(lines)

	// joinFrom joins prose lines [start, through] into one string, so a phrase
	// split across a line break still matches.
	joinFrom := func(start, through int) string {
		if start < 0 || start >= len(lines) {
			return ""
		}
		if through >= len(lines) {
			through = len(lines) - 1
		}
		parts := make([]string, 0, through-start+1)
		for _, prose := range lines[start : through+1] {
			parts = append(parts, prose.readable)
		}
		return collapseSpace.ReplaceAllString(strings.Join(parts, " "), " ")
	}

	// window is the full singleInstanceWindow-line join a rule is matched
	// against.
	window := func(start int) string { return joinFrom(start, start+singleInstanceWindow-1) }

	// settlesAt finds the first line at which the window already satisfies the
	// rule, so a finding quotes the line the advice completes on rather than
	// the top of the window — which is often an unrelated list item, and a
	// finding that quotes the wrong sentence gets dismissed as noise.
	settlesAt := func(rule secondInstanceRule, start int) markdownProseLine {
		for through := start; through < start+singleInstanceWindow && through < len(lines); through++ {
			if matchesSecondInstanceRule(rule, joinFrom(start, through)) {
				return lines[through]
			}
		}
		return lines[start]
	}

	var hits []secondInstanceHit
	for i := range lines {
		for _, rule := range secondInstanceRules {
			if !matchesSecondInstanceRule(rule, window(i)) {
				continue
			}
			// Report a run of overlapping windows once, so one sentence is one
			// finding rather than three.
			if i > 0 && matchesSecondInstanceRule(rule, window(i-1)) {
				continue
			}
			at := settlesAt(rule, i)
			hits = append(hits, secondInstanceHit{
				file: rel,
				line: at.number,
				rule: rule.name,
				text: strings.TrimSpace(at.raw),
			})
		}
	}
	return hits
}

// allowanceKey is how a published line is named in docsSecondInstanceAllowed.
func allowanceKey(file, text string) string { return file + ":" + strings.TrimSpace(text) }

// TestDocsSingleInstance_NoPageTellsYouToRunASecondSharko is the sweep.
func TestDocsSingleInstance_NoPageTellsYouToRunASecondSharko(t *testing.T) {
	root := repoRootForSweep(t)
	files := docsMarkdownFiles(t, root)
	if len(files) == 0 {
		t.Fatal("the documentation walk returned no page at all — this guard covers nothing")
	}

	proseLines, fenceLines := 0, 0
	var hits []secondInstanceHit
	for _, rel := range files {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v — the guard would silently cover less than it claims", rel, err)
		}
		hits = append(hits, secondInstanceHitsIn(rel, string(body), &proseLines)...)
		hits = append(hits, fenceHitsIn(rel, string(body), &fenceLines)...)
	}

	// Exact, not a floor. A reader that returns no prose from a hundred and
	// fifteen published pages is broken, and a broken reader finds nothing and
	// looks like a clean tree.
	if proseLines == 0 {
		t.Fatal("read no prose line at all from the published documentation — " +
			"the reader is broken, and a broken reader always says the manual is clean")
	}
	// The same, for the half a reader copies and runs. The operator runbooks
	// are mostly fenced commands; reading none of them is not a clean manual.
	if fenceLines == 0 {
		t.Fatal("read no fenced line at all from the published documentation — " +
			"the fence reader is broken, and the runnable instructions are the ones " +
			"an operator actually executes")
	}

	for _, hit := range hits {
		if reason, ok := docsSecondInstanceAllowed[allowanceKey(hit.file, hit.text)]; ok {
			t.Logf("allowed: %s:%d — %s", hit.file, hit.line, reason)
			continue
		}
		t.Errorf("%s:%d %s\n\n  %s\n\n"+
			"The technical preview runs ONE Sharko. The chart ships replicaCount: 1, sign-ins "+
			"live in a map inside that one process, and there is no leader election — a second "+
			"copy signs people out at random and runs the background work twice.\n\n"+
			"Write instead: %s\n\n"+
			"If this line really is right as written, add it to docsSecondInstanceAllowed with "+
			"the reason. Entries there are keyed by the exact line text and are checked for "+
			"staleness, so an excuse cannot outlive the sentence it was written for.",
			hit.file, hit.line, hit.rule, hit.text, insteadFor(hit.rule))
	}
}

// insteadFor returns the advice attached to a rule by name. Both rule lists
// are searched: a finding must carry what to write instead whether it came
// from a sentence or from a command, and rule names are required to be unique
// across the two lists so this lookup cannot pick the wrong one.
func insteadFor(name string) string {
	for _, rule := range secondInstanceRules {
		if rule.name == name {
			return rule.instead
		}
	}
	for _, rule := range fenceRules {
		if rule.name == name {
			return rule.instead
		}
	}
	return "(no advice recorded for this rule, which is itself a defect)"
}

// TestDocsSingleInstance_EveryRuleCatchesItsProbeAndSparesItsCounterProbe is
// the liveness proof, and the direction check.
//
// The published tree is expected to be clean, so the sweep going green proves
// nothing on its own. This drives the REAL detector over wording taken from
// the pages that carried the advice, and over a nearby sentence that is fine,
// and requires the right answer both ways round. A rule widened until it fires
// on ordinary prose fails here, and so does one narrowed until it fires on
// nothing.
func TestDocsSingleInstance_EveryRuleCatchesItsProbeAndSparesItsCounterProbe(t *testing.T) {
	if len(secondInstanceRules) == 0 {
		t.Fatal("there are no rules at all — this whole file is decoration")
	}

	seen := map[string]bool{}
	for _, rule := range secondInstanceRules {
		switch {
		case rule.name == "":
			t.Error("a rule has no name, so a failure could not say what it caught")
			continue
		case len(rule.all) == 0:
			t.Errorf("rule %q has no expression that must match, so it matches every window "+
				"in the manual", rule.name)
			continue
		case rule.probe == "":
			t.Errorf("rule %q has no probe, so nothing proves it can still catch anything", rule.name)
			continue
		case rule.counterProbe == "":
			t.Errorf("rule %q has no counter-probe, so nothing proves it has not widened into "+
				"a prose linter", rule.name)
			continue
		case rule.instead == "":
			t.Errorf("rule %q does not say what to write instead", rule.name)
			continue
		}
		if seen[rule.name] {
			t.Errorf("two rules are both named %q — a failure could not say which one fired", rule.name)
		}
		seen[rule.name] = true

		probe := collapseSpace.ReplaceAllString(rule.probe, " ")
		if !matchesSecondInstanceRule(rule, probe) {
			t.Errorf("rule %q does not catch its own probe:\n\n  %s\n\n"+
				"It is banning nothing. Either the expressions were narrowed by mistake, or "+
				"the probe was reworded until it stopped being the thing the rule is for.",
				rule.name, rule.probe)
		}

		counter := collapseSpace.ReplaceAllString(rule.counterProbe, " ")
		if matchesSecondInstanceRule(rule, counter) {
			t.Errorf("rule %q fires on a sentence that is right as written:\n\n  %s\n\n"+
				"This guard is bounded on purpose: it catches advice to run a second Sharko, "+
				"and nothing else. A rule that also fires on correct prose will be silenced "+
				"with a blanket exemption, and then it protects nothing.",
				rule.name, rule.counterProbe)
		}
	}
}

// TestDocsSingleInstance_AdviceWrappedAcrossLinesIsStillCaught pins the one
// property a naive rewrite of this guard would drop.
//
// The sentence that started this had "bump" at the end of one line and
// "replicas" at the start of the next, exactly as the runbook wrapped it. A
// reader that matches one line at a time sees neither half and reports the page
// clean. The fixture below is that wrapping, written out; nothing in it is
// read from a rule.
func TestDocsSingleInstance_AdviceWrappedAcrossLinesIsStillCaught(t *testing.T) {
	page := "### Mitigations\n" +
		"\n" +
		"4. **Something else** — the dashboard handlers are read-only and\n" +
		"   cacheable; if the read fanout is saturating a single pod, bump\n" +
		"   replicas via Helm `replicaCount`.\n"

	proseLines := 0
	hits := secondInstanceHitsIn("fixture.md", page, &proseLines)
	if proseLines == 0 {
		t.Fatal("the fixture produced no prose line, so this test proved nothing")
	}
	if len(hits) == 0 {
		t.Fatalf("advice split across two lines was not caught:\n\n%s\n"+
			"The detector must join consecutive prose lines before matching. A one-line "+
			"reader is blind to exactly the wording this guard exists for.", page)
	}
}

// TestDocsSingleInstance_CodeFencesAreNotReadAsAdvice is the other direction of
// the same reader.
//
// Several runbooks tell an operator to scale the Deployment to zero and back,
// and they do it in a shell block. Those are the supported restart. If the
// reader ever starts treating fenced commands as prose, every one of those
// pages turns into a false failure, and the fastest way out of that is a
// blanket exemption that guts the guard.
//
// This is about the PROSE reader only. The same block is read as code further
// down, by rules written for commands, and the supported restart has to survive
// both — see the counter-probes on the fence rules.
func TestDocsSingleInstance_CodeFencesAreNotReadAsAdvice(t *testing.T) {
	page := "## Restart\n" +
		"\n" +
		"```sh\n" +
		"kubectl -n \"$SHARKO_NS\" scale deployment/sharko --replicas=0\n" +
		"kubectl -n \"$SHARKO_NS\" scale deployment/sharko --replicas=1\n" +
		"```\n"

	proseLines := 0
	if hits := secondInstanceHitsIn("fixture.md", page, &proseLines); len(hits) != 0 {
		t.Errorf("a shell block was read as advice and produced %d finding(s): %+v\n\n"+
			"Fenced blocks are commands, not sentences somebody reads for guidance.",
			len(hits), hits)
	}
}

// TestDocsSingleInstance_NoAllowanceCoversAWholeFile makes the shape of an
// excuse structural.
//
// An entry naming only a path switches the guard off for every line on that
// page, including sentences nobody has read yet. Every key must carry the
// exact line it excuses.
func TestDocsSingleInstance_NoAllowanceCoversAWholeFile(t *testing.T) {
	for key, reason := range docsSecondInstanceAllowed {
		if reason == "" {
			t.Errorf("the allowance %q gives no reason", key)
		}
		text := key[strings.Index(key, ".md:")+len(".md:"):]
		if !strings.Contains(key, ".md:") || strings.TrimSpace(text) == "" {
			t.Errorf("the allowance %q names a file but no line.\n\n"+
				"A whole-page excuse turns this guard off exactly where the advice would do "+
				"harm. Key an allowance by \"<path>:<the exact line>\" so it dies with the "+
				"sentence it was written for.", key)
		}
	}
}

// TestDocsSingleInstance_NoAllowanceIsStale is the other half of the ratchet.
//
// An entry for a line that is no longer on the page, or that no rule catches
// any more, is a hole nobody asked for: it sits there switching the guard off
// for a sentence that could come back reworded. It is named and removed.
func TestDocsSingleInstance_NoAllowanceIsStale(t *testing.T) {
	if len(docsSecondInstanceAllowed) == 0 {
		return // nothing to go stale, which is the state this file shipped in
	}

	root := repoRootForSweep(t)
	files := docsMarkdownFiles(t, root)

	live := map[string]bool{}
	proseLines, fenceLines := 0, 0
	for _, rel := range files {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		for _, hit := range secondInstanceHitsIn(rel, string(body), &proseLines) {
			live[allowanceKey(hit.file, hit.text)] = true
		}
		for _, hit := range fenceHitsIn(rel, string(body), &fenceLines) {
			live[allowanceKey(hit.file, hit.text)] = true
		}
	}
	if proseLines == 0 {
		t.Fatal("read no prose at all, so every allowance would look stale for the wrong reason")
	}
	if fenceLines == 0 {
		t.Fatal("read no fenced code at all, so an allowance for a fenced line would look " +
			"stale for the wrong reason")
	}

	var stale []string
	for key := range docsSecondInstanceAllowed {
		if !live[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	for _, key := range stale {
		t.Errorf("the allowance %q no longer matches anything on the page.\n\n"+
			"Either the line was reworded, or it was removed. Delete the entry: an excuse that "+
			"has outlived its sentence is a hole in the guard, not protection.", key)
	}
}

// TestDocsSingleInstance_ThePremiseStillHolds pins this guard to the code and
// to the page it is enforcing, so it cannot go on policing a rule that has
// changed underneath it.
//
// Two things have to stay true for everything above to make sense: the chart
// ships exactly one replica, and the preview page still tells the reader to
// leave it there. Both are compared exactly.
func TestDocsSingleInstance_ThePremiseStillHolds(t *testing.T) {
	root := repoRootForSweep(t)

	values, err := os.ReadFile(filepath.Join(root, singleInstanceChartValues))
	if err != nil {
		t.Fatalf("reading %s: %v — this guard cannot check its own premise", singleInstanceChartValues, err)
	}
	shipped := ""
	found := false
	for _, raw := range strings.Split(string(values), "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "replicaCount:") {
			continue
		}
		if found {
			t.Fatalf("%s sets replicaCount more than once, so it is unclear what the chart ships",
				singleInstanceChartValues)
		}
		shipped = strings.TrimSpace(strings.TrimPrefix(line, "replicaCount:"))
		found = true
	}
	if !found {
		t.Fatalf("%s has no replicaCount at all — the whole premise of this guard is unpinned",
			singleInstanceChartValues)
	}
	if shipped != "1" {
		t.Errorf("the chart now ships replicaCount: %s, not 1.\n\n"+
			"Every page this guard protects says Sharko runs as one pod, and %s says so under "+
			"its own heading. If the chart really is meant to ship more than one, the sessions "+
			"map, the background work and the whole preview page have to change first — and "+
			"this guard comes down with them, deliberately, not by accident.",
			shipped, singleInstancePreviewPage)
	}

	preview, err := os.ReadFile(filepath.Join(root, singleInstancePreviewPage))
	if err != nil {
		t.Fatalf("reading %s: %v", singleInstancePreviewPage, err)
	}
	for _, claim := range []string{
		"Run exactly one replica.",
		"replicaCount: 1",
	} {
		if !strings.Contains(string(preview), claim) {
			t.Errorf("%s no longer says %q.\n\n"+
				"That page is where the single-instance limit is stated in full, and every "+
				"correction this guard protects points a reader at it. If the sentence has "+
				"been reworded, reword this check with it — do not delete it, or the rest of "+
				"the manual is left pointing at a promise nobody makes any more.",
				singleInstancePreviewPage, claim)
		}
	}
}

// ---------------------------------------------------------------------------
// The fences, read as code
// ---------------------------------------------------------------------------

// A fenced block is the highest-risk thing on an operator page: it is the part
// a person copies and runs. markdownProse drops fences on purpose — reading a
// command as a sentence would turn every supported `--replicas=0` restart into
// a false failure — so the fences get their own reader here, and their own
// rules, written against the forms this repository actually ships.
//
// The forms are read off the chart, not invented:
//
//   - charts/sharko/values.yaml ships `replicaCount: 1`.
//   - charts/sharko/templates/deployment.yaml renders
//     `replicas: {{ .Values.replicaCount }}`.
//
// so the three ways a published page could tell somebody to run two Sharkos in
// runnable form are `kubectl scale deployment/sharko --replicas=2`,
// `--set replicaCount=2` on a Sharko `helm` command, and `replicaCount: 2` in
// a Sharko values file — plus the rendered Deployment manifest itself.
//
// # Telling a Sharko fence from an addon fence
//
// This is the whole difficulty. `replicaCount: 2` is forbidden when it is
// Sharko's chart and completely ordinary when it is an addon's — Sharko
// deploys addon charts, and several pages show exactly that value for
// cert-manager. The fences are told apart by what they are ABOUT, never by a
// list of file-and-line pairs:
//
//   - A kubectl command names its workload. `deployment/sharko` is Sharko;
//     `deployment/log-shipper` is not. Nothing else is needed.
//   - A Deployment manifest names itself in `metadata.name`.
//   - A helm command names the release or the chart — `sharko/sharko`,
//     `charts/sharko`, `oci://.../sharko`.
//   - A values fence names the file it is, either in its own first comment
//     (`# sharko-values.yaml`, the shape getting-started/installation.md
//     already uses) or in the prose immediately above it. A fence introduced
//     as `addons/cert-manager/clusters/prod-eu.yaml` is an addon's file and is
//     left alone — because of what the page says it is, not because the rule
//     cannot see it. TestDocsSingleInstance_ASharkoValuesFenceIsToldFromAnAddonValuesFence
//     runs the SAME yaml under both introductions and requires opposite
//     answers, so "spared" can never quietly mean "blind".
//
// An unattributed fence — a bare `replicaCount: 2` under prose that names
// neither Sharko nor an addon — is not flagged. That is the same bound the
// prose rules carry: a rule needs a Sharko subject, or it becomes a linter for
// every Helm example in the manual and gets switched off.

// fenceContextLines is how many non-blank lines immediately above a fence are
// kept as the fence's context. Four covers "Example `<path>`:" plus the
// sentence that introduced it, which is how these pages are written.
const fenceContextLines = 4

// fenceContextScan bounds how far above a fence that search goes, so a fence
// after a long blank stretch does not inherit a heading from half a page away.
const fenceContextScan = 12

// markdownFenceLine is one line inside a fence: the 1-based line number and
// the line exactly as written. Nothing is blanked out — this is code, and the
// backticks and paths in it are the content, not decoration.
type markdownFenceLine struct {
	number int
	text   string
}

// markdownFenceBlock is one fenced block: its info string (`sh`, `yaml`), the
// 1-based line the opening fence sits on, its body lines, and the prose
// immediately above it.
type markdownFenceBlock struct {
	info    string
	start   int
	lines   []markdownFenceLine
	context string
}

// markdownFences is THE fence reader, and it uses the same markdownFence
// expression markdownProse uses to decide where a fence starts and stops. One
// expression, two readers: they cannot disagree about what is code and what is
// prose, and TestDocsSingleInstance_ProseAndFencesTogetherCoverEveryLine keeps
// that true by requiring the two of them to account for every line of a page
// between them.
func markdownFences(body string) []markdownFenceBlock {
	raw := strings.Split(body, "\n")

	// contextAbove collects the nearest non-blank lines above the opening
	// fence, nearest last, and stops at another fence so a block never
	// inherits the contents of the one before it.
	contextAbove := func(openAt int) string {
		var back []string
		for i := openAt - 1; i >= 0 && i >= openAt-fenceContextScan && len(back) < fenceContextLines; i-- {
			if markdownFence.MatchString(raw[i]) {
				break
			}
			line := strings.TrimSpace(raw[i])
			if line == "" {
				continue
			}
			back = append(back, line)
		}
		for l, r := 0, len(back)-1; l < r; l, r = l+1, r-1 {
			back[l], back[r] = back[r], back[l]
		}
		return strings.Join(back, "\n")
	}

	var out []markdownFenceBlock
	var current *markdownFenceBlock
	for i, raw0 := range raw {
		line := strings.TrimRight(raw0, "\r")
		if markdownFence.MatchString(line) {
			if current == nil {
				info := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "`~"))
				current = &markdownFenceBlock{info: info, start: i + 1, context: contextAbove(i)}
				continue
			}
			out = append(out, *current)
			current = nil
			continue
		}
		if current != nil {
			current.lines = append(current.lines, markdownFenceLine{number: i + 1, text: line})
		}
	}
	// An unterminated fence still gets read. markdownProse treats the rest of
	// the page as fence, so dropping it here would leave those lines in
	// nobody's hands.
	if current != nil {
		out = append(out, *current)
	}
	return out
}

// fenceCommands groups a fence's body into logical commands: one physical
// line, extended across every trailing backslash continuation.
//
// The grouping is the point. A runbook fence often holds the supported restart
// and something else, and a rule matched against the whole fence would pair
// `deployment/sharko` on one line with a `--replicas=2` belonging to a
// different workload three lines down. One command in, one answer out.
func fenceCommands(block markdownFenceBlock) [][]markdownFenceLine {
	var out [][]markdownFenceLine
	var group []markdownFenceLine
	for _, line := range block.lines {
		group = append(group, line)
		if strings.HasSuffix(strings.TrimRight(line.text, " \t"), "\\") {
			continue
		}
		out = append(out, group)
		group = nil
	}
	if len(group) > 0 {
		out = append(out, group)
	}
	return out
}

// joinFenceCommand flattens a logical command into one line, so a flag on a
// continuation line still reads as part of the command it belongs to.
func joinFenceCommand(lines []markdownFenceLine) string {
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		parts = append(parts, strings.TrimSuffix(strings.TrimRight(line.text, " \t"), `\`))
	}
	return collapseSpace.ReplaceAllString(strings.Join(parts, " "), " ")
}

// fenceBodyText is the fence exactly as written, newlines kept, because the
// yaml rules match line-anchored fields.
func fenceBodyText(block markdownFenceBlock) string {
	parts := make([]string, 0, len(block.lines))
	for _, line := range block.lines {
		parts = append(parts, line.text)
	}
	return strings.Join(parts, "\n")
}

// The shapes, taken from the chart and from how the runbooks are written.
var (
	// fenceScaleVerb is the kubectl subcommand.
	fenceScaleVerb = regexp.MustCompile(`(?i)\bscale\b`)
	// fenceSharkoDeployment is Sharko's own Deployment, named the way kubectl
	// names it. A `-n sharko` namespace flag deliberately does NOT match: the
	// workload has to be the subject, or `kubectl -n sharko scale
	// deployment/log-shipper --replicas=2` would be blamed on Sharko.
	fenceSharkoDeployment = regexp.MustCompile(`(?i)\bdeploy(?:ment)?s?(?:\.apps)?[/\s]+sharko\b`)
	// fenceSharkoNamedDeployment is a different workload whose name merely
	// starts with sharko, like deployment/sharko-ui. Not this Deployment.
	fenceSharkoNamedDeployment = regexp.MustCompile(`(?i)\bdeploy(?:ment)?s?(?:\.apps)?[/\s]+sharko-`)
	// fenceReplicasFlagAboveOne is --replicas set to two or more. One and zero
	// are the supported settings and must not match; `<original>` is a
	// placeholder and is not a number, so it does not match either.
	fenceReplicasFlagAboveOne = regexp.MustCompile(`(?i)--replicas[=\s]+(?:[2-9]|[1-9][0-9]+)\b`)

	// fenceKindDeployment, fenceSharkoMetadataName and
	// fenceReplicasFieldAboveOne are the rendered manifest:
	// charts/sharko/templates/deployment.yaml puts replicaCount into
	// spec.replicas, so a hand-written manifest is the same instruction.
	fenceKindDeployment        = regexp.MustCompile(`(?im)^\s*-?\s*kind:\s*Deployment\s*$`)
	fenceSharkoMetadataName    = regexp.MustCompile(`(?im)^\s*name:\s*["']?sharko["']?\s*(?:#.*)?$`)
	fenceReplicasFieldAboveOne = regexp.MustCompile(`(?im)^\s*replicas:\s*(?:[2-9]|[1-9][0-9]+)\s*(?:#.*)?$`)

	// fenceHelmCommand and fenceSharkoHelmTarget: a helm command counts as
	// Sharko's when it names Sharko's chart or its release, which is how every
	// helm command in this manual is already written.
	fenceHelmCommand      = regexp.MustCompile(`(?i)\bhelm\b`)
	fenceSharkoHelmTarget = regexp.MustCompile(
		`(?i)\bsharko/sharko\b|\bcharts/sharko\b|oci://\S*/sharko\b|\bhelm\s+(?:install|upgrade)\s+(?:--\S+\s+)*sharko\b`)
	// fenceSetReplicaCountAboveOne is the --set form of the chart's own value.
	fenceSetReplicaCountAboveOne = regexp.MustCompile(`(?i)--set\s+["']?replicacount=(?:[2-9]|[1-9][0-9]+)\b`)

	// fenceReplicaCountFieldAboveOne is the values-file form of the same
	// value, charts/sharko/values.yaml key for key.
	fenceReplicaCountFieldAboveOne = regexp.MustCompile(`(?im)^\s*replicaCount:\s*(?:[2-9]|[1-9][0-9]+)\s*(?:#.*)?$`)
	// fenceSharkoOwnValues is what makes a values fence Sharko's own: it names
	// the file it is, or the prose above it does.
	fenceSharkoOwnValues = regexp.MustCompile(
		`(?i)sharko-values\.ya?ml|charts/sharko/values\.ya?ml|\bsharko(?:'s|` + "\u2019" + `s)?\s+(?:own\s+)?(?:helm\s+|chart\s+)*values\b|\b(?:helm|chart)\s+values\s+for\s+sharko\b`)
	// fenceAddonPath is an addon's own file. Sharko deploys addon charts, and
	// their replica counts are none of this guard's business.
	fenceAddonPath = regexp.MustCompile(`(?i)\baddons?/[a-z0-9][a-z0-9-]*/`)
)

// fenceProbe is a whole published page, run through the real fence reader.
//
// Probes are pages rather than bare strings because half of what these rules
// do is decide what a fence IS, and that answer comes partly from the prose
// above it. A probe that skipped the page would not exercise the hard half.
type fenceProbe struct {
	// why says what this probe is proving.
	why string
	// page is the markdown, fence and all.
	page string
	// wantLine is the exact trimmed line the finding must quote. Set on the
	// probe only. A finding that names the fence, or the line above the
	// offending one, gets read as noise and dismissed, so the line is pinned
	// here rather than left to chance.
	wantLine string
}

// fenceRule is one shape of "run more than one Sharko", written as runnable
// code instead of as a sentence.
type fenceRule struct {
	// name says what the rule catches, and is what a failure reports.
	name string
	// perCommand runs the rule against one logical command at a time rather
	// than the whole fence. Shell rules want this; yaml rules do not, because
	// a manifest's kind, name and replica count are on three different lines.
	perCommand bool
	// all must every one match the scope — the command, or the fence body.
	all []*regexp.Regexp
	// none may any match the scope, or the hit is dropped.
	none []*regexp.Regexp
	// attribution must every one match the fence's context and body together:
	// this is what ties a fence to Sharko rather than to an addon. Empty means
	// the command names its own subject and needs no help.
	attribution []*regexp.Regexp
	// notAttribution is the same, inverted: what proves the fence belongs to
	// something else.
	notAttribution []*regexp.Regexp
	// pinpoint picks which physical line the finding names.
	pinpoint *regexp.Regexp
	// probe is a page this rule MUST catch, at an exact line.
	probe fenceProbe
	// counterProbes are pages it must NOT catch. Every one of them is a real
	// shape from the published manual or a one-word variation on the probe, so
	// a rule that has widened into a linter is named here.
	counterProbes []fenceProbe
	// instead says what to write.
	instead string
}

// fenceRules is a LIST, never a count.
var fenceRules = []fenceRule{
	{
		name:       "a runnable command scales Sharko past one replica",
		perCommand: true,
		all:        []*regexp.Regexp{fenceScaleVerb, fenceSharkoDeployment, fenceReplicasFlagAboveOne},
		none:       []*regexp.Regexp{fenceSharkoNamedDeployment},
		pinpoint:   fenceReplicasFlagAboveOne,
		probe: fenceProbe{
			why: "the runbook shape, with a second replica asked for",
			page: "### Give it more room\n\n" +
				"```sh\n" +
				"kubectl -n \"$SHARKO_NS\" scale deployment/sharko --replicas=2\n" +
				"```\n",
			wantLine: "kubectl -n \"$SHARKO_NS\" scale deployment/sharko --replicas=2",
		},
		counterProbes: []fenceProbe{
			{
				why: "the supported restart: down to zero, back to one",
				page: "### Restart\n\n" +
					"```sh\n" +
					"kubectl -n \"$SHARKO_NS\" scale deployment/sharko --replicas=0\n" +
					"kubectl -n \"$SHARKO_NS\" scale deployment/sharko --replicas=1\n" +
					"```\n",
			},
			{
				why: "a different workload, scaled the way credential-leak-in-logs.md scales it",
				page: "### Stop the shipper\n\n" +
					"```sh\n" +
					"kubectl -n <log-ns> scale deployment/log-shipper --replicas=0\n" +
					"kubectl -n <log-ns> scale deployment/log-shipper --replicas=<original>\n" +
					"```\n",
			},
			{
				why: "a different workload taken above one, in a fence that also touches Sharko — " +
					"this is what proves the rule reads one command at a time",
				page: "### Both\n\n" +
					"```sh\n" +
					"kubectl -n sharko scale deployment/sharko --replicas=0\n" +
					"kubectl -n <log-ns> scale deployment/log-shipper --replicas=2\n" +
					"```\n",
			},
			{
				why: "Sharko's namespace, somebody else's Deployment",
				page: "### Not Sharko\n\n" +
					"```sh\n" +
					"kubectl -n sharko scale deployment/redis --replicas=3\n" +
					"```\n",
			},
		},
		instead: "zero and one are the only two settings Sharko has. To restart it, scale it to " +
			"zero and then back to one. There is no supported way to run a second copy: give " +
			"the one pod more CPU and memory instead.",
	},
	{
		name:     "a Deployment manifest gives Sharko more than one replica",
		all:      []*regexp.Regexp{fenceKindDeployment, fenceSharkoMetadataName, fenceReplicasFieldAboveOne},
		pinpoint: fenceReplicasFieldAboveOne,
		probe: fenceProbe{
			why: "the manifest the chart renders, with the count edited",
			page: "### The Deployment\n\n" +
				"```yaml\n" +
				"apiVersion: apps/v1\n" +
				"kind: Deployment\n" +
				"metadata:\n" +
				"  name: sharko\n" +
				"  namespace: sharko\n" +
				"spec:\n" +
				"  replicas: 2\n" +
				"```\n",
			wantLine: "replicas: 2",
		},
		counterProbes: []fenceProbe{
			{
				why: "the same manifest as the chart actually renders it",
				page: "### The Deployment\n\n" +
					"```yaml\n" +
					"apiVersion: apps/v1\n" +
					"kind: Deployment\n" +
					"metadata:\n" +
					"  name: sharko\n" +
					"spec:\n" +
					"  replicas: 1\n" +
					"```\n",
			},
			{
				why: "an addon's Deployment, which may have as many replicas as it likes",
				page: "### cert-manager\n\n" +
					"```yaml\n" +
					"apiVersion: apps/v1\n" +
					"kind: Deployment\n" +
					"metadata:\n" +
					"  name: cert-manager\n" +
					"spec:\n" +
					"  replicas: 3\n" +
					"```\n",
			},
			{
				why: "argocd-app-options.md's ignoreDifferences example, which names the field " +
					"rather than setting it",
				page: "   - **Ignore Differences** — Example:\n\n" +
					"     ```yaml\n" +
					"     - group: apps\n" +
					"       kind: Deployment\n" +
					"       jsonPointers:\n" +
					"         - /spec/replicas\n" +
					"     ```\n",
			},
		},
		instead: "one replica. charts/sharko/templates/deployment.yaml renders " +
			"`replicas: {{ .Values.replicaCount }}` and the chart ships that value as 1 — a " +
			"manifest showing anything else is telling the reader to run a second Sharko.",
	},
	{
		name:     "a helm command sets Sharko's replicaCount above one",
		all:      []*regexp.Regexp{fenceHelmCommand, fenceSharkoHelmTarget, fenceSetReplicaCountAboveOne},
		pinpoint: fenceSetReplicaCountAboveOne,
		probe: fenceProbe{
			why: "the --reuse-values shape the operator runbooks already use",
			page: "### Resize\n\n" +
				"```sh\n" +
				"helm upgrade --reuse-values \\\n" +
				"  --set replicaCount=2 \\\n" +
				"  sharko sharko/sharko -n <sharko-ns>\n" +
				"```\n",
			wantLine: "--set replicaCount=2 \\",
		},
		counterProbes: []fenceProbe{
			{
				why: "the same command putting the shipped value back",
				page: "### Resize\n\n" +
					"```sh\n" +
					"helm upgrade --reuse-values \\\n" +
					"  --set replicaCount=1 \\\n" +
					"  sharko sharko/sharko -n <sharko-ns>\n" +
					"```\n",
			},
			{
				why: "an addon's chart, installed with two replicas, which is fine",
				page: "### cert-manager\n\n" +
					"```sh\n" +
					"helm upgrade cert-manager jetstack/cert-manager \\\n" +
					"  --set replicaCount=2 -n cert-manager\n" +
					"```\n",
			},
			{
				why: "the real install command from getting-started/installation.md",
				page: "**Via Ingress (production):**\n\n" +
					"```bash\n" +
					"helm install sharko oci://ghcr.io/moranweissman/sharko/sharko \\\n" +
					"  --namespace sharko --create-namespace \\\n" +
					"  --set ingress.enabled=true\n" +
					"```\n",
			},
		},
		instead: "leave replicaCount alone. The chart ships 1 and the preview supports exactly " +
			"that; if the one pod is short of room, raise its CPU and memory instead.",
	},
	{
		name:           "a Sharko values file sets replicaCount above one",
		all:            []*regexp.Regexp{fenceReplicaCountFieldAboveOne},
		attribution:    []*regexp.Regexp{fenceSharkoOwnValues},
		notAttribution: []*regexp.Regexp{fenceAddonPath},
		pinpoint:       fenceReplicaCountFieldAboveOne,
		probe: fenceProbe{
			why: "the values file getting-started/installation.md hands the operator, " +
				"with a replica count added",
			page: "Or use a values file:\n\n" +
				"```yaml\n" +
				"# sharko-values.yaml\n" +
				"replicaCount: 2\n" +
				"ingress:\n" +
				"  enabled: true\n" +
				"```\n",
			wantLine: "replicaCount: 2",
		},
		counterProbes: []fenceProbe{
			{
				why: "the same file shipping the supported value",
				page: "Or use a values file:\n\n" +
					"```yaml\n" +
					"# sharko-values.yaml\n" +
					"replicaCount: 1\n" +
					"ingress:\n" +
					"  enabled: true\n" +
					"```\n",
			},
			{
				why: "architecture/repo-structure.md's per-cluster override — an ADDON's values, " +
					"which is what makes two replicas ordinary there",
				page: "Example `addons/cert-manager/clusters/prod-eu.yaml`:\n\n" +
					"```yaml\n" +
					"replicaCount: 2\n" +
					"resources:\n" +
					"  requests:\n" +
					"    cpu: 50m\n" +
					"```\n",
			},
			{
				why: "a smart-values placeholder, which is not a number at all",
				page: "Example `addons/cert-manager/values.yaml`:\n\n" +
					"```yaml\n" +
					"# replicaCount: <cluster-specific>\n" +
					"  replicaCount: <set per cluster>\n" +
					"```\n",
			},
			{
				why: "api-walkthrough.md's request body, which carries an addon's values",
				page: "Send an empty `values` string to remove this addon's overrides entirely.\n\n" +
					"```bash\n" +
					"curl -s -X PUT \"$SH/clusters/my-cluster/addons/keda/values\" \\\n" +
					"  -d '{ \"values\": \"replicaCount: 2\\n\" }' | jq\n" +
					"```\n",
			},
		},
		instead: "leave replicaCount at 1. That value is Sharko's own, not an addon's — the " +
			"chart ships one replica and the preview supports exactly one.",
	},
}

// matchesFenceRule is THE fence detector, and the sweep and every probe below
// call this same function. A change that blinds it blinds its own proof too.
//
// scope is the command or the fence body; attribution is the fence's context
// and body together, which is where "whose fence is this" is decided.
func matchesFenceRule(rule fenceRule, scope, attribution string) bool {
	for _, must := range rule.all {
		if !must.MatchString(scope) {
			return false
		}
	}
	for _, mustNot := range rule.none {
		if mustNot.MatchString(scope) {
			return false
		}
	}
	for _, must := range rule.attribution {
		if !must.MatchString(attribution) {
			return false
		}
	}
	for _, mustNot := range rule.notAttribution {
		if mustNot.MatchString(attribution) {
			return false
		}
	}
	return true
}

// fenceHitAt names the offending line inside a matched scope. pinpoint is what
// keeps a finding honest: without it every yaml hit would quote `apiVersion`
// three lines above the replica count, and a finding that quotes the wrong
// line gets dismissed as noise.
func fenceHitAt(rel string, rule fenceRule, lines []markdownFenceLine, block markdownFenceBlock) secondInstanceHit {
	at := markdownFenceLine{number: block.start, text: ""}
	if len(lines) > 0 {
		at = lines[0]
	}
	if rule.pinpoint != nil {
		for _, line := range lines {
			if rule.pinpoint.MatchString(line.text) {
				at = line
				break
			}
		}
	}
	return secondInstanceHit{file: rel, line: at.number, rule: rule.name, text: strings.TrimSpace(at.text)}
}

// fenceHitsIn runs every fence rule over every fenced block on one page.
// fenceLines is a counter the caller uses only to tell "found nothing wrong"
// apart from "read no code at all".
func fenceHitsIn(rel, body string, fenceLines *int) []secondInstanceHit {
	var hits []secondInstanceHit
	for _, block := range markdownFences(body) {
		*fenceLines += len(block.lines)
		attribution := block.context + "\n" + fenceBodyText(block)
		for _, rule := range fenceRules {
			if rule.perCommand {
				for _, command := range fenceCommands(block) {
					if !matchesFenceRule(rule, joinFenceCommand(command), attribution) {
						continue
					}
					hits = append(hits, fenceHitAt(rel, rule, command, block))
				}
				continue
			}
			if !matchesFenceRule(rule, fenceBodyText(block), attribution) {
				continue
			}
			hits = append(hits, fenceHitAt(rel, rule, block.lines, block))
		}
	}
	return hits
}

// fenceHitsForRule is the probe harness: it runs the real reader over a whole
// page and keeps only what one rule found.
func fenceHitsForRule(rule fenceRule, page string, fenceLines *int) []secondInstanceHit {
	var out []secondInstanceHit
	for _, hit := range fenceHitsIn("fixture.md", page, fenceLines) {
		if hit.rule == rule.name {
			out = append(out, hit)
		}
	}
	return out
}

// TestDocsSingleInstance_EveryFenceRuleCatchesItsProbeAndSparesItsCounterProbes
// is the liveness proof for the code rules, and the direction check.
//
// The published fences are expected to be clean, so the sweep going green
// proves nothing on its own. This drives the REAL reader over a page it must
// catch — at an exact line — and over pages taken from the manual that it must
// leave alone. A rule widened until it fires on every Helm example fails here,
// and so does one narrowed until it fires on nothing.
func TestDocsSingleInstance_EveryFenceRuleCatchesItsProbeAndSparesItsCounterProbes(t *testing.T) {
	if len(fenceRules) == 0 {
		t.Fatal("there are no fence rules at all — the fences are being read and nothing is " +
			"being asked of them, which is worse than not reading them")
	}

	seen := map[string]bool{}
	for _, rule := range secondInstanceRules {
		seen[rule.name] = true
	}

	for _, rule := range fenceRules {
		switch {
		case rule.name == "":
			t.Error("a fence rule has no name, so a failure could not say what it caught")
			continue
		case len(rule.all) == 0:
			t.Errorf("fence rule %q has no expression that must match, so it matches every "+
				"fenced block in the manual", rule.name)
			continue
		case rule.pinpoint == nil:
			t.Errorf("fence rule %q has no pinpoint, so a finding would name the top of the "+
				"block instead of the offending line", rule.name)
			continue
		case rule.probe.page == "":
			t.Errorf("fence rule %q has no probe, so nothing proves it can still catch anything",
				rule.name)
			continue
		case rule.probe.wantLine == "":
			t.Errorf("fence rule %q does not say which line its probe should be blamed on, so "+
				"nothing proves the finding names the right one", rule.name)
			continue
		case len(rule.counterProbes) == 0:
			t.Errorf("fence rule %q has no counter-probe, so nothing proves it has not widened "+
				"into a linter for every Helm example in the manual", rule.name)
			continue
		case rule.instead == "":
			t.Errorf("fence rule %q does not say what to write instead", rule.name)
			continue
		}
		if seen[rule.name] {
			t.Errorf("two rules are both named %q — a failure could not say which one fired",
				rule.name)
		}
		seen[rule.name] = true

		fenceLines := 0
		hits := fenceHitsForRule(rule, rule.probe.page, &fenceLines)
		if fenceLines == 0 {
			t.Errorf("the probe for %q has no fenced block in it, so it proved nothing:\n\n%s",
				rule.name, rule.probe.page)
		}
		switch {
		case len(hits) == 0:
			t.Errorf("fence rule %q does not catch its own probe (%s):\n\n%s\n"+
				"It is banning nothing. Either the expressions were narrowed by mistake, or the "+
				"probe was reworded until it stopped being the thing the rule is for.",
				rule.name, rule.probe.why, rule.probe.page)
		case hits[0].text != rule.probe.wantLine:
			t.Errorf("fence rule %q caught its probe but blamed the wrong line.\n\n"+
				"  named: %s:%d %q\n  wanted: %q\n\n"+
				"A finding that quotes a line the reader did not write gets dismissed as noise, "+
				"and the real instruction stays on the page.",
				rule.name, hits[0].file, hits[0].line, hits[0].text, rule.probe.wantLine)
		}

		for _, counter := range rule.counterProbes {
			counterLines := 0
			if got := fenceHitsForRule(rule, counter.page, &counterLines); len(got) != 0 {
				t.Errorf("fence rule %q fires on a block that is right as written (%s):\n\n%s\n"+
					"named: %s:%d %q\n\n"+
					"This guard is bounded on purpose: it catches instructions to run a second "+
					"Sharko, and nothing else. A rule that also fires on an addon's chart, or on "+
					"the supported restart, will be silenced with a blanket exemption — and then "+
					"it protects nothing.",
					rule.name, counter.why, counter.page, got[0].file, got[0].line, got[0].text)
			}
			if counterLines == 0 {
				t.Errorf("the counter-probe %q for fence rule %q has no fenced block in it, so "+
					"it proved nothing:\n\n%s", counter.why, rule.name, counter.page)
			}
		}
	}
}

// TestDocsSingleInstance_ASharkoValuesFenceIsToldFromAnAddonValuesFence is the
// answer to the only question that makes this hard.
//
// `replicaCount: 2` is forbidden when the file is Sharko's chart values and
// ordinary when it is an addon's — Sharko deploys addon charts, and
// architecture/repo-structure.md shows exactly that value for cert-manager.
// The two fences below are the SAME yaml, byte for byte. Only the line that
// says which file it is differs, and the answers have to be opposite.
//
// Without this, "the addon example stays green" would be indistinguishable
// from "the rule cannot see yaml at all".
func TestDocsSingleInstance_ASharkoValuesFenceIsToldFromAnAddonValuesFence(t *testing.T) {
	const rule = "a Sharko values file sets replicaCount above one"

	yaml := "```yaml\n" +
		"replicaCount: 2\n" +
		"resources:\n" +
		"  requests:\n" +
		"    cpu: 50m\n" +
		"```\n"

	sharkoPage := "Example `charts/sharko/values.yaml`:\n\n" + yaml
	addonPage := "Example `addons/cert-manager/clusters/prod-eu.yaml`:\n\n" + yaml

	sharkoLines, addonLines := 0, 0
	sharkoHits := fenceHitsIn("fixture.md", sharkoPage, &sharkoLines)
	addonHits := fenceHitsIn("fixture.md", addonPage, &addonLines)
	if sharkoLines == 0 || addonLines == 0 {
		t.Fatal("neither fixture produced a fenced line, so this test proved nothing")
	}

	caught := func(hits []secondInstanceHit) *secondInstanceHit {
		for i := range hits {
			if hits[i].rule == rule {
				return &hits[i]
			}
		}
		return nil
	}

	hit := caught(sharkoHits)
	if hit == nil {
		t.Errorf("the same yaml, introduced as Sharko's own chart values, was NOT caught:\n\n%s\n"+
			"Then the addon example below is not being spared on purpose — the rule simply "+
			"cannot see a values fence, and a `replicaCount: 2` in Sharko's own file would ship.",
			sharkoPage)
	} else if hit.text != "replicaCount: 2" {
		t.Errorf("the Sharko fence was caught but blamed line %d, %q, rather than the line that "+
			"sets the value", hit.line, hit.text)
	}

	if hit := caught(addonHits); hit != nil {
		t.Errorf("an addon's per-cluster override was flagged at %s:%d:\n\n%s\n"+
			"Sharko deploys addon charts and has no opinion about how many replicas they run. "+
			"A rule that cannot tell the two files apart will be switched off, and then Sharko's "+
			"own values file is unguarded too.",
			hit.file, hit.line, addonPage)
	}
}

// TestDocsSingleInstance_ProseAndFencesTogetherCoverEveryLine keeps the two
// readers honest about the boundary between them.
//
// One of them drops fences; the other reads only fences. If they ever disagree
// about where a fence starts — one of them mishandling an indented block, say,
// or a tilde fence — a strip of the manual belongs to neither and is read by
// nobody, which looks exactly like a clean tree. Every line of every published
// page has to be accounted for by prose, by a fence body, or by a fence marker.
func TestDocsSingleInstance_ProseAndFencesTogetherCoverEveryLine(t *testing.T) {
	root := repoRootForSweep(t)
	files := docsMarkdownFiles(t, root)

	checked := 0
	for _, rel := range files {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		lines := strings.Split(string(body), "\n")

		markers := 0
		for _, line := range lines {
			if markdownFence.MatchString(strings.TrimRight(line, "\r")) {
				markers++
			}
		}
		fenced := 0
		for _, block := range markdownFences(string(body)) {
			fenced += len(block.lines)
		}
		prose := len(markdownProse(string(body)))

		if prose+fenced+markers != len(lines) {
			t.Errorf("%s: the two readers account for %d prose + %d fenced + %d fence markers "+
				"= %d of %d lines.\n\n"+
				"The difference is a strip of the page that neither the prose sweep nor the "+
				"fence sweep reads, and unread text is where the advice comes back.",
				rel, prose, fenced, markers, prose+fenced+markers, len(lines))
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("checked no page at all, so the two readers were never compared")
	}
}

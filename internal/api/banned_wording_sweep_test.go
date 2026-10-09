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
// The sweep walks up from this package to the repository root and covers
// EVERY Go file under internal/ and cmd/. There is no directory list and no
// file list — see wordingSweepRoots for what a list cost this tree three
// rounds running. `ui/` is deliberately out of scope (the TypeScript lists own
// that side) and `.bmad/` is out of scope on purpose — those are historical
// records of decisions, and rewriting history to match today's vocabulary
// would destroy the audit trail rather than improve it.
//
// # Line-wrapped hits count
//
// A comment can split a banned phrase across two lines with a `//` and
// leading spaces in between, which is exactly how one occurrence of this
// phrase hid from a plain grep. The sweep normalises comment markers and
// runs of whitespace to single spaces before matching, so a wrapped phrase
// is found too.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
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
	{
		// Product ruling, 2026-08-19: information text speaks in the user's
		// terms and never explains Sharko's own plumbing. "the reconciler's
		// next pass" named a component the reader has never heard of AND
		// implied a clock the operator can change. It reached the connection
		// page in three sentences; two were fixed first and the third was
		// left, so a reader saw a plain sentence next to one naming the
		// reconciler in the same block.
		phrase: "reconciler's next pass",
		instead: `"Sharko automatically <does the thing>." for a sentence a person reads. ` +
			`In a comment explaining the mechanism to a developer, say "the next time the ` +
			`cluster reconciler runs" or "on its own schedule".`,
	},
	{
		// Product owner, 2026-08-20. The replacement for "the reconciler's
		// next pass" traded one problem for another: it dropped the machinery
		// but picked up a chatty, conversational tail that the product owner
		// had already rejected once. The approved register for an automatic
		// action is professional and short — state what Sharko does, name Git
		// as the source of truth, promise no timeframe, and stop.
		//
		// This entry bans the tail itself rather than the whole sentence,
		// because the tail is what travelled: it reached two plan sentences,
		// one missing-Secret sentence and a repair refusal, plus four comment
		// blocks that argued for it as a house style.
		phrase: "nobody has to ask for it",
		instead: `"Sharko automatically <does the thing>." — e.g. "Sharko automatically ` +
			`reapplies the addon labels defined in Git." Name no machinery, promise no ` +
			`timeframe, and do not add a conversational tail explaining that the reader ` +
			`need not act.`,
	},
	{
		// B3, 2026-08-20. One sentence promised "Sharko automatically creates
		// a missing connection" and it was written on BOTH of the repair
		// endpoint's 404 paths. It is true on the full-connection path, where
		// the cluster's credentials really can be re-read and the connection
		// really is rebuilt from Git plus that source. It is false on the
		// labels-only path, which is reached by a self-managed connection
		// (Sharko never creates that Secret), a legacy pasted-kubeconfig one
		// (the credential existed only inside the Secret that is gone), a
		// connection whose recorded credentials source Sharko does not
		// understand, and one whose secrets backend Sharko cannot read.
		//
		// The mode-blind wording is what is banned, not the idea. The promise
		// survives, qualified, in repairFailSecretGoneSharkoCreates — which
		// names where the connection is rebuilt FROM, so it can only be
		// written where that source exists.
		phrase: "creates a missing connection",
		instead: `name the source the connection is rebuilt from, so the sentence cannot ` +
			`be reused where there is no source: "Sharko automatically creates it from Git ` +
			`and the configured credentials source." Where Sharko will not or cannot build ` +
			`the connection, promise nothing at all.`,
	},
	{
		// B3a, 2026-08-20. The missing-Secret sentence for a cluster whose
		// record does not name a credentials source Sharko understands said
		// Sharko therefore would not create one. Sharko's create path never
		// reads the recorded source: the periodic pass partitions on
		// connectionManagedBy alone, and the credential resolver uses the
		// cluster's lookup key and its role. The identifier CredsSource is in
		// no production file in internal/clusterreconciler at all. So the one
		// group of clusters most likely to hit this — everything registered
		// before the field existed, on every upgraded install — was told
		// Sharko would stand still while Sharko was about to build the
		// connection.
		//
		// The refusal is banned, not the sentence, because the refusal is the
		// part that would come back: somebody restating the limit would reach
		// for the same words. The honest register for a connection Sharko
		// attempts but may not manage is "still tries".
		phrase: "will not create one",
		instead: `say what Sharko actually does. Sharko refuses to build a connection Secret in exactly ` +
			`ONE case — the cluster's Git record says connectionManagedBy: user — and ` +
			`connectioncompare.LimitReasonSecretMissingSelfManaged is the sentence for it. Everywhere ` +
			`else Sharko attempts the build, so say so and say what may stop it: ` +
			`"Sharko still tries to build it from Git and the credentials backend, but …".`,
	},
	{
		// B4, 2026-08-20. The bootstrap probe's catch-all branch built its
		// answer as fmt.Sprintf("listing argocd applications failed: %v",
		// err) — so whatever the ArgoCD client, the Go HTTP transport or a
		// TLS stack had produced went into the init-status response body and
		// into the POST /init operation message. The branch is reached
		// exactly when Sharko does NOT know what the failure is, which is
		// also exactly when it cannot know what is inside the text.
		//
		// The prefix is banned rather than the whole formatted string,
		// because the prefix is the part that would come back: somebody
		// re-adding the error value would reach for the same opening words.
		phrase: "listing argocd applications failed",
		instead: `the fixed sentence bootstrapProbeFailedDetail in internal/api/init.go, which ` +
			`says Sharko could not reach ArgoCD and does not claim to know anything about ` +
			`the bootstrap application. Never put the error value in the answer, and never ` +
			`put it in the log line either — this branch is the one where nobody knows what ` +
			`the error contains.`,
	},
	{
		// B1, 2026-08-20. Sixty-four handlers in internal/api and four
		// methods in internal/remediation each built their answer as this
		// prefix followed by the underlying error. That error comes from
		// building a Git provider out of the saved connection, and one of
		// the ways it fails is net/url refusing the repository URL — whose
		// error value quotes in full the string it failed on. A Git repo URL
		// is routinely written with the token inside it, so the prefix below
		// was the opening of a 502 body that handed the repository's access
		// token to any signed-in caller, from read-only endpoints included.
		//
		// The prefix is banned WITH its trailing colon and space, because
		// that is the shape that carried the leak. Fixed sentences that
		// merely mention a missing Git connection in passing — the doctor
		// report's own wording, a doc comment naming a status value — are
		// not hits and are not meant to be.
		//
		// B4 could not add this entry: the thirty-three production files
		// still carried the phrase. Adding it is what proves B1 finished.
		phrase: "no active Git connection: ",
		instead: `writeNoActiveGitConnection(w, r) from internal/api/connection_gate.go, or ` +
			`credsafe.ErrNoActiveGitConnection where the failure is returned rather than ` +
			`written. Neither takes an error or a string, so there is no parameter an ` +
			`error's words could be passed through — the sentence is a constant in ` +
			`internal/credsafe and is the same for every cause. Do not log the error ` +
			`value either: on the Git side the reason the client could not be built may ` +
			`BE the token.`,
	},
	{
		// The ArgoCD half of the same set. It leaks less spectacularly —
		// there is no repository URL on this side — but the two share
		// getActiveConnCache, so a config-store error reaches both, and the
		// same rule applies for the same reason.
		phrase: "no active ArgoCD connection: ",
		instead: `writeNoActiveArgocdConnection(w, r) from internal/api/connection_gate.go, ` +
			`or credsafe.ErrNoActiveArgocdConnection where the failure is returned rather ` +
			`than written. See the Git entry above for why neither helper can be misused.`,
	},
}

// wordingSweepRoots are the trees BOTH sweeps in this file walk, recursively.
//
// THERE IS NO DIRECTORY LIST AND NO FILE LIST HERE ANY MORE, and that is the
// whole point of the B19 change. What used to stand here was seven named
// directories: internal/api, internal/clusterreconciler, internal/argosecrets,
// internal/connectioncompare, internal/models, internal/remediation and
// cmd/sharko. Everything else in the tree was outside both guards — including
// internal/orchestrator, which authors MixedLayoutMessage, a sentence the doc
// comment beside it says "the UI can show as-is", and which named the cluster
// reconciler to a person for as long as it has existed.
//
// This is the third round running that a hand-written list has been the defect
// (B16 routing, B17 metrics, B18 Git capitalisation). Each time the list was
// one file behind whoever forgot to extend it. So the list is gone and the
// trees are walked, and a package written tomorrow is covered the day it is
// written.
//
// It is deliberately the SAME variable the Git guard walks
// (git_capitalisation_test.go), so the two cannot drift apart by somebody
// editing one and not the other.
var wordingSweepRoots = gitCapitalisationRoots

// bannedSweepExempt are files whose JOB is to name a banned phrase. Each one
// needs a reason here; anything else is a hit.
//
// PRODUCTION FILES MAY NEVER BE EXEMPTED. A whole-file exemption on a
// non-test file switches the ban off exactly where the phrase would do harm
// — and that is not hypothetical: connection_reconciliation.go, the file
// holding the live user-facing sentence this ruling was aimed at, was
// exempted here so its doc comment could quote the phrase it replaced. The
// banned sentence could be put straight back into the shipped constant and
// the whole Go suite passed. The doc comment was reworded instead, and
// TestBannedWordings_NoProductionFileIsExempt now makes the rule structural.
// TestBannedWordings_NoExemptionIsStale is the other half of the ratchet: an
// entry here for a file that holds no banned phrase is a hole nobody asked
// for, and is named and removed. Two entries
// (internal/clusterreconciler/drift_notice_text_test.go,
// internal/connectioncompare/compare_test.go) were removed that way on
// 2026-08-20 — their per-file lists had stopped naming any of the phrases
// above, so the exemptions were switching the ban off for two files for no
// reason at all.
var bannedSweepExempt = map[string]string{
	"internal/api/banned_wording_sweep_test.go":         "this sweep — it has to hold the phrases to look for them",
	"internal/api/connection_messages_round6_test.go":   "per-file banned-phrase lists; naming the phrase is the assertion",
	"cmd/sharko/repair_connection_test.go":              "asserts the CLI no longer prints the phrase",
	"internal/api/connection_reconciliation_test.go":    "asserts the reconciliation response no longer carries the phrase",
	"internal/connectioncompare/missing_secret_test.go": "refusesCreation() has to name the retired refusal phrasings to recognise them coming back",
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

// TestBannedWordings_MatchingIsCaseInsensitiveBothWays is the regression guard
// on the hole B1 found.
//
// The sweep lowercased the file it was reading and compared the phrase as
// written. That made it case-insensitive in one direction only: a file could
// SHOUT a banned phrase and be caught, but a phrase entered with a capital
// letter — which is what happens the moment a phrase names Git, ArgoCD,
// Kubernetes or Sharko — could never match anything at all. The ban would be
// added, the suite would go green, and nothing would be banned.
//
// This drives the real matching path with a phrase in every casing and
// requires a hit each time.
func TestBannedWordings_MatchingIsCaseInsensitiveBothWays(t *testing.T) {
	const line = `writeError(w, 502, "No Active ArgoCD Connection: "+err.Error())`
	haystack := strings.ToLower(commentAndSpaceRun.ReplaceAllString(line, " "))

	for _, phrase := range []string{
		"no active argocd connection: ",
		"No active ArgoCD connection: ",
		"NO ACTIVE ARGOCD CONNECTION: ",
	} {
		if !strings.Contains(haystack, strings.ToLower(phrase)) {
			t.Errorf("a banned phrase written as %q does not match the line it is meant to ban.\n\n"+
				"The sweep must lowercase BOTH sides. Lowercasing only the file makes every capitalised "+
				"entry in bannedGoWordings a no-op, and a no-op ban is worse than no ban: it looks like "+
				"protection in the diff and protects nothing.", phrase)
		}
	}
}

// TestBannedWordings_EveryPhraseCanActuallyMatchItself is the cheap standing
// check that no entry is dead on arrival. A phrase must find itself in a line
// that contains it — if it cannot, it is banning nothing.
func TestBannedWordings_EveryPhraseCanActuallyMatchItself(t *testing.T) {
	if len(bannedGoWordings) == 0 {
		t.Fatal("there are no banned phrases at all — this whole file is decoration")
	}
	for _, banned := range bannedGoWordings {
		if banned.phrase == "" {
			t.Error("an entry has an empty phrase, which matches every file in the tree")
			continue
		}
		if banned.instead == "" {
			t.Errorf("the entry for %q does not say what to write instead", banned.phrase)
		}
		carrier := "// some line that says " + banned.phrase + " in passing"
		haystack := strings.ToLower(commentAndSpaceRun.ReplaceAllString(carrier, " "))
		if !strings.Contains(haystack, strings.ToLower(banned.phrase)) {
			t.Errorf("the banned phrase %q cannot find itself in a line that contains it — it is banning nothing", banned.phrase)
		}
	}
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

	for _, dir := range wordingSweepRoots {
		abs := filepath.Join(root, dir)
		if _, err := os.Stat(abs); err != nil {
			t.Fatalf("swept tree %q does not exist — the sweep silently covers less than it claims: %v", dir, err)
		}
		inThisRoot := 0
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
			inThisRoot++

			lines := strings.Split(string(body), "\n")
			for _, banned := range bannedGoWordings {
				// The window is lowercased below, so the phrase has to be
				// lowercased too.
				//
				// It was not, until B1. The sweep read as case-insensitive
				// and was advertised as case-insensitive, but a phrase
				// containing any capital letter could never match anything —
				// it was compared against an all-lowercase haystack. Every
				// entry that existed happened to be lowercase already, so
				// nothing had ever exposed it. B1's two entries name a Git
				// provider and ArgoCD, so they arrived capitalised and were
				// silently dead: the ban was added, the suite went green, and
				// the phrase was still free to come back anywhere in the tree.
				// TestBannedWordings_MatchingIsCaseInsensitiveBothWays is the
				// regression guard.
				phrase := strings.ToLower(banned.phrase)
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
					if !strings.Contains(window, phrase) {
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
						if strings.Contains(next, phrase) {
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
		// Exact, not a floor. A floor ("at least 100 files") is a number
		// with room in it: the sweep can lose most of a tree and stay
		// green. "This root contributed nothing" cannot be true of either
		// root while the guard is doing its job.
		if inThisRoot == 0 {
			t.Fatalf("the sweep walked tree %q and read no Go file at all — it is covering nothing there", dir)
		}
	}

	if filesScanned == 0 {
		t.Fatal("the sweep read no Go files at all — it would pass vacuously")
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
	t.Logf("swept %d Go files across %d trees for %d banned wording(s)", filesScanned, len(wordingSweepRoots), len(bannedGoWordings))
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

// TestBannedWordings_NoExemptionIsStale is the other half of the ratchet.
//
// The sweep above fails on a NEW violation. Nothing failed on a STALE one: an
// exemption for a file that no longer holds any banned phrase is a hole with
// nobody's name on it — the file is silently outside the ban, and the next
// person to add a sentence to it gets no warning at all. The exemption map is
// a LIST of decisions, so every entry has to still be a decision.
func TestBannedWordings_NoExemptionIsStale(t *testing.T) {
	root := repoRootForSweep(t)

	var stale []string
	for rel := range bannedSweepExempt {
		path := filepath.Join(root, filepath.FromSlash(rel))
		body, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("exempt file %q cannot be read — the exemption names a file that is not there: %v", rel, err)
			continue
		}
		text := strings.ToLower(commentAndSpaceRun.ReplaceAllString(string(body), " "))
		holds := false
		for _, banned := range bannedGoWordings {
			// Lowercased for the same reason the main sweep lowercases it —
			// text above is already lowercase, so a capitalised phrase would
			// never match and every exemption would read as stale.
			if strings.Contains(text, strings.ToLower(banned.phrase)) {
				holds = true
				break
			}
		}
		if !holds {
			stale = append(stale, "  "+rel+"  ("+bannedSweepExempt[rel]+")")
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Errorf("these files are exempt from the banned-wording sweep but hold no banned phrase:\n%s\n"+
			"An exemption exists so a file whose JOB is to name a banned phrase is not reported as a hit.\n"+
			"A file that no longer names one has no such job, and the exemption now only hides the next\n"+
			"violation. Remove the entry.", strings.Join(stale, "\n"))
	}
}

// ── the same rule, widened from one spelling to the idea ────────────────
//
// # Why the phrase list above was not enough
//
// The product owner's ruling was: user-facing text speaks in the user's terms,
// never explains Sharko's own plumbing, and promises no timeframe. What got
// written down was the literal "reconciler's next pass". Two live sentences
// said exactly that thing in different words and neither was a hit:
//
//	connection_repair.go   "Wait for the reconciler to create it on its next pass"
//	clusters_doctor.go     "Sharko's reconciler adds it automatically within ~30 seconds"
//
// Same class as the case-sensitivity hole closed earlier in this round, one
// level up: a ban that only recognises one phrasing of the thing it forbids.
// So this guard matches PATTERNS, not spellings.
//
// # Why it reads string literals and not whole files
//
// The sweep above is deliberately blunt and reads comments too. That works for
// a short, retired phrase. It cannot work for "the reconciler": the word is the
// name of a real package and a real type, and it appears in some two hundred
// comments across the swept directories that are talking to a developer, not
// to a person using Sharko. Banning it everywhere would mean rewriting all of
// them to say something vaguer, which makes the code worse to read and the
// product no better.
//
// So this guard is narrow where the other is blunt: only string LITERALS, only
// in non-test files, and only ones that read as a sentence a person would be
// shown. That is the surface the ruling is about.
//
// SWAGGER ANNOTATION LINES ARE COVERED, as of B19. They were not, and that
// was the whole of the second defect: GET/PUT /settings, both cluster
// reconcile endpoints, the cluster-secrets refresh and all three
// /secrets/* endpoints named the reconciler in their published descriptions
// while this guard was green, because it parsed with comments switched off.
// A swagger line is not an ordinary comment — it is the API reference — so
// covering it does not reopen the two-hundred-comment problem above.
//
// WHAT IT DELIBERATELY DOES NOT COVER, so the boundary is visible:
//   - Ordinary comments. Explaining the reconciler to the next developer is
//     what a comment is for.
//   - Test files. A test that asserts a sentence is gone has to hold the
//     sentence.
//   - "in a moment" and friends. repairFailWrite's "Try again in a moment."
//     is a deliberate, pinned exception — it tells a PERSON what to do next,
//     which is not a promise about what Sharko does by itself. The clock rule
//     here is aimed at NUMBERS, which are always a promise.

// userFacingWordingRules are the patterns banned from user-facing sentences.
// Each says what to write instead, for the same reason the list above does.
var userFacingWordingRules = []struct {
	name    string
	pattern *regexp.Regexp
	instead string
}{
	{
		name: "names Sharko's own plumbing",
		// Any way of naming the reconciler to a person: "the reconciler",
		// "Sharko's reconciler", "its reconciler", "a reconciler pass", the
		// possessive, the plural.
		pattern: regexp.MustCompile(`(?i)\breconcilers?('s)?\b`),
		instead: `"Sharko automatically <does the thing>." Where the component itself has to be ` +
			`named — a "it is not running" message — say "the part of Sharko that manages cluster ` +
			`connections", which is the wording repairFailNoReconciler already uses.`,
	},
	{
		name: "promises a timeframe",
		// A number attached to a duration. "within ~30 seconds", "in about
		// 5 minutes", "after 2 hours". The interval is operator-settable, so
		// any number here is false on some installation.
		pattern: regexp.MustCompile(`(?i)\b(within|in|after|every)\s+(about\s+|around\s+|roughly\s+)?~?\s*\d+\s*(second|minute|hour|day)s?\b`),
		instead: `nothing — state what Sharko does and stop. The check interval is operator-settable, ` +
			`so a number is wrong on any installation that sets it differently.`,
	},
}

// userFacingWordingAllowed is the reviewable record of every sentence that
// matches a rule above and is nonetheless correct. One line per sentence, with
// the reason, and it ratchets BOTH ways: a new match is a failure until
// somebody writes the reason down here, and an entry that no longer matches
// anything is named as stale and must be removed, so the list cannot quietly
// grow into a hole.
//
// It is keyed by the whole sentence, not by file and line, so moving the code
// does not silently move the exception with it.
var userFacingWordingAllowed = map[string]string{
	// ── found when B19 removed the directory list and started parsing
	// comments. Each of these was outside the guard's reach until then, and
	// each has been looked at rather than inherited. ──────────────────────

	`@Param source query string false "Filter by source (\"api\", \"webhook\", \"reconciler\", etc.)"`: "" +
		"the audit log's source field really does take the literal value \"reconciler\" " +
		"(internal/audit/log.go, and cmd/sharko/serve.go sets it). This line documents the values a " +
		"caller may pass, so the word here is a wire value, not the name of a component being " +
		"explained to somebody. It is written quoted for exactly that reason — the same delimited-token " +
		"rule the Git guard uses.",

	"@Description Creates a new long-lived API token for programmatic access. The token expires after 90 days unless expires_in_days (1-365) says otherwise. The plaintext value is shown once and never again. A token can carry at most the role of the caller who created it — asking for a higher role is refused with 403.": "" +
		"the ninety days is this endpoint's own default TTL and the sentence names the parameter that " +
		"overrides it. The clock rule exists because a background interval is operator-settable, so a " +
		"number promising when Sharko will have finished something is false on some install. This " +
		"number is not that: it is a documented property of the token being created, and the caller " +
		"chooses it.",

	"Reconciler invocations by outcome":       metricHelpReason,
	"Reconciler run duration":                 metricHelpReason,
	"Items processed per reconciler":          metricHelpReason,
	"Reconciler writes to Kubernetes by kind": metricHelpReason,

	"RegisterCluster: direct ArgoCD cluster Secret write failed (continuing — Git + reconciler can still converge)": "" +
		"a slog.Error message, not a sentence on anybody's screen. It opens with a capital because it " +
		"opens with a Go identifier, which is what makes the prose rule pick it up now that the guard " +
		"walks internal/orchestrator. Naming the component is right here: the reader is whoever is " +
		"holding the server log open. Reported to the product owner rather than reworded — the same " +
		"class as internal/clusterreconciler/repair.go:160.",

	"Block until the PR is merged (exit 0), closed without merge (exit 1),\nor the timeout expires (exit 2). Polls every 5 seconds.": "" +
		"`sharko pr wait`'s own help text. The five seconds is a compiled-in constant of that " +
		"command's loop (cmd/sharko/pr.go), not a settable server interval, and it describes what " +
		"the command is doing while the operator watches it rather than promising when Sharko " +
		"will have finished something by itself.",
}

// looksLikeShownToAPerson reports whether a string literal reads as a sentence
// a person would be shown.
//
// THE RULE: it starts with an uppercase letter and it contains a space. That
// is the same rule the connection-sentence catalog guard uses, and it is what
// separates prose from a wire value: "git_definition" and "addon_labels_only"
// have no space, and a wrapped Go error ("test connection: unexpected status")
// opens lowercase by convention.
func looksLikeShownToAPerson(value string) bool {
	if !strings.Contains(value, " ") {
		return false
	}
	// Leading whitespace is trimmed before the first-rune test, and that is
	// not a detail. Printf lines are routinely indented inside the string —
	// "      Waiting for the cluster reconciler to pick up %s\n" — and the
	// first rune of that is a space, which is not uppercase, so the whole
	// line was invisible to this guard. Two live ones were found that way,
	// and one of them promised a timeframe as well.
	for _, r := range strings.TrimLeft(value, " \t\n") {
		return unicode.IsUpper(r)
	}
	return false
}

// userFacingItem is one thing a person can read: a sentence in the product, or
// a line of published API documentation.
type userFacingItem struct {
	file string
	line int
	kind string // "sentence" or "swagger"
	text string
}

// userFacingItemsIn is THE detector. The sweep and the self-proof below both
// call this same function, so a change that blinds the detector blinds the
// proof too and cannot leave a green suite behind. sentencesSeen and
// swaggerSeen are counters the caller uses only to tell "found nothing wrong"
// apart from "looked at nothing".
//
// # The two holes B19 found in what used to stand here
//
// 1. It parsed with mode 0 — comments dropped on the floor. Swagger
// annotations ARE comments, so every published @Summary, @Description and
// @Failure line in the tree was outside the guard. Eight of them named the
// reconciler to whoever reads the API reference, and the guard was green the
// whole time. The Git guard next door had already learned this and parses with
// parser.ParseComments; this one had not.
//
// 2. It only looked at literals whose first letter is a capital. That test is
// right for telling prose from a wire value, and wrong for response bodies:
// the live 503 on POST /clusters/{name}/resync read "the cluster reconciler is
// not running on this server (no in-cluster Kubernetes client or credentials
// provider configured) — there is nothing to resync", and opened lowercase, so
// the guard never looked at it. Four more messages behind writeError had the
// same shape.
//
// So a string handed to a WRITER — any helper whose name begins "write", plus
// recordReconcile — is a sentence whatever its first letter. That is the one
// place in this guard where function names are load-bearing, and it is narrow
// on purpose: widening to every lowercase-opening literal would fire on every
// wrapped Go error and every log line in the tree, and a guard that fires on
// those is a guard somebody switches off.
func userFacingItemsIn(fset *token.FileSet, file *ast.File, rel string, sentencesSeen, swaggerSeen *int) []userFacingItem {
	var items []userFacingItem

	// Pass one: every string literal anywhere inside a call to a writer.
	written := map[ast.Node]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isUserVisibleWriter(calleeName(call.Fun)) {
			return true
		}
		for _, arg := range call.Args {
			ast.Inspect(arg, func(inner ast.Node) bool {
				if lit, isLit := inner.(*ast.BasicLit); isLit && lit.Kind == token.STRING {
					written[lit] = true
				}
				return true
			})
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
		if !looksLikeShownToAPerson(text) && !(written[lit] && strings.Contains(text, " ")) {
			return true
		}
		*sentencesSeen++
		items = append(items, userFacingItem{rel, fset.Position(lit.Pos()).Line, "sentence", text})
		return true
	})

	// Pass two: swagger annotation lines. Published API documentation a person
	// reads, and the half of the surface this guard used to be blind to.
	for _, group := range file.Comments {
		for _, c := range group.List {
			line := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
			if !strings.HasPrefix(line, "@") {
				continue
			}
			*swaggerSeen++
			items = append(items, userFacingItem{rel, fset.Position(c.Pos()).Line, "swagger", line})
		}
	}
	return items
}

// isUserVisibleWriter reports whether a callee name is one that puts words in
// front of a person.
//
// It is a NAME SHAPE, not a list of names, for the same reason there is no
// file list any more: a list is one entry behind whoever adds the next helper.
// Every response writer in internal/api is spelled writeSomething —
// writeError, writeJSON, writeCodedError, writeServerError, writeUpstreamError
// and a dozen more — and recordReconcile writes the sentence the cluster page
// shows for a finished run.
//
// The lower-case "w" matters: it keeps io.Writer's Write, os.WriteFile and
// bytes.Buffer.WriteString out, which carry no product sentences.
func isUserVisibleWriter(name string) bool {
	return strings.HasPrefix(name, "write") || name == "recordReconcile"
}

// metricHelpReason is one reason shared by the four Prometheus HELP lines
// below, because it is one decision, not four.
const metricHelpReason = "" +
	"a Prometheus HELP string describing a metric whose own NAME contains the word — " +
	"sharko_reconciler_runs_total, sharko_reconciler_duration_seconds and the rest " +
	"(internal/metrics/metrics.go). The reader is an operator looking at /metrics or at a " +
	"dashboard, and the HELP line has to describe the metric beside it. Rewording the HELP " +
	"without renaming the metric would make the two disagree, and renaming the metric breaks " +
	"every dashboard and alert anybody has already built on it."

// TestBannedWordings_UserFacingTextNamesNoPlumbingAndPromisesNoClock is the
// widened guard. Like the sweep above it reports EVERY hit, not the first.
func TestBannedWordings_UserFacingTextNamesNoPlumbingAndPromisesNoClock(t *testing.T) {
	root := repoRootForSweep(t)

	type hit struct{ file, rule, text, instead string }
	var hits []hit
	sentencesScanned := 0
	allowedSeen := map[string]bool{}

	swaggerScanned := 0

	for _, dir := range wordingSweepRoots {
		abs := filepath.Join(root, dir)
		if _, err := os.Stat(abs); err != nil {
			t.Fatalf("swept tree %q does not exist — the guard silently covers less than it claims: %v", dir, err)
		}
		inThisRoot := 0
		walkErr := filepath.Walk(abs, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			rel = filepath.ToSlash(rel)
			inThisRoot++

			fset := token.NewFileSet()
			// parser.ParseComments, not 0. Parsing without comments is why
			// eight published swagger descriptions naming the reconciler were
			// invisible to this guard while it was green — see the note above
			// userFacingItemsIn.
			file, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if parseErr != nil {
				return parseErr
			}
			for _, item := range userFacingItemsIn(fset, file, rel, &sentencesScanned, &swaggerScanned) {
				for _, rule := range userFacingWordingRules {
					if !rule.pattern.MatchString(item.text) {
						continue
					}
					if _, allowed := userFacingWordingAllowed[item.text]; allowed {
						allowedSeen[item.text] = true
						continue
					}
					hits = append(hits, hit{
						file:    item.file + ":" + itoa(item.line),
						rule:    rule.name,
						text:    item.text,
						instead: rule.instead,
					})
				}
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walking %s: %v", dir, walkErr)
		}
		// Exact, not a floor. "at least 200 sentences" was the number that
		// used to stand here, and a number with room in it is a guard that
		// can lose most of its reach and stay green.
		if inThisRoot == 0 {
			t.Fatalf("the guard walked tree %q and read no Go file at all — it is covering nothing there", dir)
		}
	}

	if sentencesScanned == 0 {
		t.Fatal("not one user-facing sentence was found in the whole tree — the parser is finding nothing and this guard is blind")
	}
	if swaggerScanned == 0 {
		t.Fatal("not one swagger annotation was found in the whole tree — comments are not being parsed and every published description is unguarded")
	}
	// The other direction: an allowance for a sentence that is no longer in
	// the tree, or that no longer matches any rule, is a hole with nobody's
	// name on it.
	var staleAllowances []string
	for text := range userFacingWordingAllowed {
		if !allowedSeen[text] {
			staleAllowances = append(staleAllowances, "  "+strconv.Quote(text))
		}
	}
	if len(staleAllowances) > 0 {
		sort.Strings(staleAllowances)
		t.Errorf("these sentences are allowed to break a wording rule but no longer do — remove the entries:\n%s",
			strings.Join(staleAllowances, "\n"))
	}
	if len(hits) > 0 {
		var b strings.Builder
		b.WriteString("user-facing sentences that name Sharko's plumbing or promise a timeframe:\n")
		for _, h := range hits {
			b.WriteString("  " + h.file + "  [" + h.rule + "]\n    " + h.text + "\n")
		}
		b.WriteString("\nWrite instead: " + hits[0].instead + "\n")
		t.Fatal(b.String())
	}
	t.Logf("checked %d user-facing sentences and %d swagger lines against %d rules", sentencesScanned, swaggerScanned, len(userFacingWordingRules))
}

// TestBannedWordings_DetectorSeesEveryShape proves the detector actually
// fires, and on exactly what it claims.
//
// A sweep that reports nothing is indistinguishable from a sweep that looks at
// nothing. So the REAL detector — userFacingItemsIn, the same function the
// sweep calls — runs over a probe holding every shape it must see and every
// shape it must leave alone, and must return exactly the first set.
//
// The probe carries, deliberately, the two shapes B19 found the guard blind
// to: a swagger annotation line, and a lowercase-opening message handed to
// writeError.
func TestBannedWordings_DetectorSeesEveryShape(t *testing.T) {
	const src = "package api\n" + `
// @Summary Trigger secret reconciliation
// @Failure 503 {object} map[string]string "Secrets reconciler not configured"
// An ordinary comment explaining the reconciler to a developer. Never an item.
func probe(w http.ResponseWriter) {
	writeError(w, 503, "the cluster reconciler is not running on this server")
	writeJSON(w, 503, map[string]string{
		"error": "secrets reconciler not configured",
		"code":  "secrets_reconciler_not_configured",
	})
	log.Error("RegisterCluster: reconciler can still converge", "cluster", "prod-eu")
	_ = []string{
		"Sharko automatically reapplies the addon labels defined in Git.",
		"out_of_sync",
		"unwrapped go error: reconciler is not wired",
	}
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "probe.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing the probe: %v", err)
	}

	sentences, swagger := 0, 0
	var found []string
	for _, item := range userFacingItemsIn(fset, file, "probe.go", &sentences, &swagger) {
		found = append(found, item.kind+": "+item.text)
	}
	sort.Strings(found)

	want := []string{
		`sentence: RegisterCluster: reconciler can still converge`,
		`sentence: Sharko automatically reapplies the addon labels defined in Git.`,
		`sentence: secrets reconciler not configured`,
		`sentence: the cluster reconciler is not running on this server`,
		`swagger: @Failure 503 {object} map[string]string "Secrets reconciler not configured"`,
		`swagger: @Summary Trigger secret reconciliation`,
	}
	if strings.Join(found, "\n") != strings.Join(want, "\n") {
		t.Errorf("the detector found:\n%s\n\nwant:\n%s\n\nThe real sweep's silence would mean nothing.",
			strings.Join(found, "\n"), strings.Join(want, "\n"))
	}

	// The counters are what tell "nothing is wrong" apart from "nothing was
	// looked at" in the sweep, so they must move on this probe.
	if sentences == 0 || swagger == 0 {
		t.Errorf("the detector reported %d sentences and %d swagger lines on a probe that plainly has both — the sweep's blindness checks would never fire", sentences, swagger)
	}

	// And the writer rule both ways, stated once rather than implied by the
	// list above: it is a name SHAPE, not a list of names.
	for _, name := range []string{"writeError", "writeJSON", "writeCodedError", "writeSomethingInventedTomorrow", "recordReconcile"} {
		if !isUserVisibleWriter(name) {
			t.Errorf("%q is not recognised as a writer, so anything it is handed is unguarded", name)
		}
	}
	for _, name := range []string{"Write", "WriteString", "WriteFile", "Error", "Info", "Sprintf", "Errorf"} {
		if isUserVisibleWriter(name) {
			t.Errorf("%q is treated as a writer — every log line and wrapped error in the tree becomes a product sentence", name)
		}
	}
}

// TestBannedWordings_WidenedGuardCatchesBothRealSentences is the positive
// control, and it is the point of the whole story: the two sentences that
// shipped past the one-spelling ban are fed to the matcher here, written out,
// and both must be hits. If either stops matching, the guard has narrowed back
// to recognising one phrasing.
func TestBannedWordings_WidenedGuardCatchesBothRealSentences(t *testing.T) {
	mustMatch := []struct{ why, text string }{
		{"the repair 404 that named the reconciler",
			"This cluster's ArgoCD connection Secret does not exist yet. Wait for the reconciler to create it on its next pass, then repair if needed."},
		{"the doctor fix that named the reconciler AND promised 30 seconds",
			"Sharko's reconciler adds it automatically within ~30 seconds; if it doesn't appear, check that the reconciler is running."},
		{"a phrasing neither of them used",
			"Sharko will fix this on the next reconciler run."},
		{"the timeframe promise on its own",
			"This is applied within about 5 minutes."},
		{"a plural, to prove the pattern is not one spelling",
			"Both reconcilers will pick this up."},
	}
	for _, tc := range mustMatch {
		matched := false
		for _, rule := range userFacingWordingRules {
			if rule.pattern.MatchString(tc.text) {
				matched = true
			}
		}
		if !matched {
			t.Errorf("the widened guard does not catch %s:\n  %q", tc.why, tc.text)
		}
		if !looksLikeShownToAPerson(tc.text) {
			t.Errorf("the guard would not even look at %s — it does not read as a sentence shown to a person:\n  %q", tc.why, tc.text)
		}
	}

	// And the approved replacements must NOT match, or the guard bans the
	// thing it is asking for.
	mustNotMatch := []string{
		"There is no ArgoCD connection for this cluster to repair. Sharko automatically creates it from Git and the configured credentials source.",
		"There is no ArgoCD connection for this cluster, so there are no addon labels for Sharko to re-apply. Sharko changed nothing. Run the connection check on this cluster to see why the connection is missing and what to do about it.",
		"Sharko automatically adds this label. If it does not appear, check that the part of Sharko that manages cluster connections is running on this server.",
		"The part of Sharko that manages cluster connections is not running on this server, so it cannot repair this connection.",
		"Sharko could not write this cluster's connection. Nothing was changed. Try again in a moment.",
		"Sharko automatically reapplies the addon labels defined in Git.",
	}
	for _, text := range mustNotMatch {
		for _, rule := range userFacingWordingRules {
			if rule.pattern.MatchString(text) {
				t.Errorf("the %s rule fired on an approved sentence:\n  %q", rule.name, text)
			}
		}
	}

	// A wire value must never be looked at, whatever it says.
	for _, notProse := range []string{"addon_labels_only", "reconciler_not_wired", "ERR_RBAC"} {
		if looksLikeShownToAPerson(notProse) {
			t.Errorf("the guard treats the wire value %q as a sentence shown to a person", notProse)
		}
	}
}

// TestBannedWordings_NoProductionFileIsExempt makes the exemption rule
// structural instead of a convention somebody has to remember.
//
// An exemption exists so a file whose JOB is to name a banned phrase — a
// banned-phrase list, this sweep itself — is not reported as a hit. A
// production file has no such job. Exempting one switches the ban off
// exactly where the phrase reaches a user, which is how the live sentence
// this ruling was aimed at ended up unguarded.
func TestBannedWordings_NoProductionFileIsExempt(t *testing.T) {
	for path, reason := range bannedSweepExempt {
		if !strings.HasSuffix(path, "_test.go") {
			t.Errorf("production file %q is exempt from the banned-wording sweep (reason given: %q).\n"+
				"Only test files may be exempt. Reword the production file so it does not contain the phrase.",
				path, reason)
		}
		if strings.TrimSpace(reason) == "" {
			t.Errorf("exemption for %q has no reason", path)
		}
	}
	if len(bannedSweepExempt) == 0 {
		t.Fatal("the exemption map is empty — this test would pass vacuously")
	}
}

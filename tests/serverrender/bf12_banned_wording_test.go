package serverrender

// bf12_banned_wording_test.go — a few sentences about the address rule are
// banned from the chart and the documentation, and this is what keeps them out.
//
// # The first banned sentence, and why banning is the right shape
//
// The chart refuses an address only when the user information sits in the host
// part of it. An "@" in the PATH is ordinary and always installs, as
// https://github.com/org/repo@v1 does. During the BF12 closure pass the
// documentation described the rule as refusing "anything before an" @, which
// promised a stricter chart than the one that ships, and the same wrong
// sentence was copied into four places before anybody noticed.
//
// So the wording is banned outright rather than corrected once. This project
// has repeatedly fixed a sentence in one file while an identical wrong one
// survived in another, and a reviewer reading the corrected copy has no way to
// see the survivor.
//
// The walk is a walk, not a list of files: a list goes stale the day somebody
// adds a page, which is the same failure in a different shape. Reading nothing
// is a failure, not a pass.
//
// # What BF13-4 changed here, and why it had to
//
// The control on this sweep used to be this:
//
//	planted := "the chart refuses " + bannedAddressWording + " `@`"
//	if !strings.Contains(strings.ToLower(planted), bannedAddressWording) {
//
// which proves that strings.Contains works. It proves nothing whatever about
// the walk. If the walk had descended into no directory, or read no file, or
// skipped every extension, that control would still have passed and the sweep
// would have reported the whole tree clean — and "clean" from a sweep that
// read nothing looks exactly like "clean" from a sweep that read everything.
// This is the same defect that was repaired elsewhere in BF12-4 and then put
// back in BF12-5, so it is not a hypothetical way for it to return.
//
// The control now plants a real file, holding the real banned sentences, into
// a directory the real walk really descends into, and requires the real walk
// to COME BACK WITH THEM. Only then is the same walk trusted to say the tree
// is clean. The planting, the finding and the removal all live inside
// sweepProvenToFindAPlantedCopy, which is also the only way to reach the sweep
// at all, so the control cannot be dropped without deleting the sweep with it.
// The planted file is removed on the way out and again by a cleanup that runs
// even when the test fails.
//
// # What BF13-5 changed here, and why it had to
//
// Two more sentences are banned now, both of them claims about how far the
// chart's copy of the address rule can be trusted:
//
//   - that the chart's rule IS internal/credsafe.ClassifyAddress. It is a
//     second copy of the same decision, and the two are not identical: they
//     read what is inside square brackets differently, so "[::1::2]:80" is
//     credential-free to the chart and unclassifiable to Go. Nothing in that
//     difference can carry a credential, but a sentence saying the two are one
//     rule is still a sentence nobody can rely on.
//   - that the day the two stop agreeing is the day a test fails. The tests
//     compare the two copies over two written-down lists. A disagreement about
//     an address in neither list turns nothing red, which is exactly how this
//     class of bug stayed invisible twice already.
//
// Both of those sentences were WRAPPED across a line break where they shipped,
// and the sweep used to read one line at a time, so it could not have found
// either of them. A ban the sweep cannot enforce is worse than no ban, so the
// reading changed with the ban: a file is now flattened into one run of words
// first, with line breaks, indentation and leading comment markers taken out,
// and the phrases are looked for in that. The planted control writes every
// banned phrase broken across a line break, with a comment marker on the
// second half, so the flattening itself is what the control proves.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bannedWordings are the sentences that must not be anywhere a person reads.
//
// Each one is assembled from its words so that this file is not itself a copy
// of the thing it bans. They are matched against flattened text, so the words
// have to be written here the way they are read, one space apart.
var bannedWordings = []string{
	strings.Join([]string{"anything", "before", "an"}, " "),
	strings.Join([]string{"the", "same", "rule", "as", "internal/credsafe"}, " "),
	strings.Join([]string{"is", "the", "day", "a", "test", "fails"}, " "),
	// BANNERS-V4, 2026-08-26. The pre-release banners across README.md,
	// SECURITY.md, GOVERNANCE.md and the docs/site install pages all told the
	// reader to hold off — "don't install yet", "wait for v4", "no release to
	// install" — some of it wrapped in a markdown admonition title, some of it
	// inside a backtick around `v4`. The product owner ruled that meaning out:
	// the reader must be told what to do (install only published v4.0.0+
	// artifacts, v3 and earlier stay retired, not for production), never to
	// wait. These four are the phrases that actually shipped, banned so a
	// future edit cannot bring the "hold off" framing back under a new banner.
	strings.Join([]string{"don't", "install", "yet"}, " "),
	strings.Join([]string{"do", "not", "install", "yet"}, " "),
	strings.Join([]string{"wait", "for", "v4"}, " "),
	strings.Join([]string{"no", "release", "to", "install"}, " "),
	// TAGPREP-DOCS, 2026-08-26. The "published chart still installs v3.0.0"
	// family and the release-notes "not tagged yet ... written ahead of the
	// tag" wording both go false the moment the v4 chart publishes or the tag
	// is cut. Both were replaced with state-independent wording that reads
	// true before and after. Banning "chart still installs" (not the bare
	// "still installs") keeps this clear of docs/site/operator/git-native-config.md's
	// unrelated address-shape sentence, which legitimately uses "still installs".
	strings.Join([]string{"chart", "still", "installs"}, " "),
	strings.Join([]string{"written", "ahead", "of", "the", "tag"}, " "),
	strings.Join([]string{"not", "tagged", "yet"}, " "),
	// TAGPREP-DOCS amendment, 2026-08-26. The first commit's replacement
	// sentence — "any chart version below v4.0.0 installs the retired,
	// unsupported v3 line" — was itself wrong: a v2 chart installs the v2
	// line, not the v3 line. The product owner ruled it out even though the
	// safety advice (don't install below v4.0.0) was right. Replaced with a
	// version-line-agnostic sentence: "do not install any Sharko chart
	// version below v4.0.0, all earlier release lines are retired and
	// unsupported."
	strings.Join([]string{"installs", "the", "retired", "unsupported", "v3", "line"}, " "),
	// TAGCLOSE-DOCS, 2026-08-27. "v3.0.0, the only public release so far, and
	// every earlier tag remain retired and unsupported" named v3.0.0 as the
	// only public release, which stops being true the moment a later line
	// ships. Replaced with "v3.0.0 and earlier remain retired and
	// unsupported," which stays true regardless of what else gets released.
	strings.Join([]string{"only", "public", "release", "so", "far"}, " "),
	// TRUTHCLOSE-DOCS, 2026-08-27. The owner found seven more places still
	// telling the reader "hold off" or "nothing to install" after the v4.0.0
	// technical-preview line was ruled the durable status. Checked against
	// the whole extended corpus (this sweep's own roots, normalized) with
	// zero collisions, so none of these needed narrowing: docs/ historical
	// pages like design-history.md do not use this exact wording.
	strings.Join([]string{"no", "supported", "release"}, " "),
	strings.Join([]string{"is", "in", "active", "development"}, " "),
	strings.Join([]string{"is", "in", "development", "on"}, " "),
	strings.Join([]string{"nothing", "to", "install"}, " "),
	strings.Join([]string{"not", "proven", "by", "a", "release", "yet"}, " "),
	strings.Join([]string{"waiting", "on", "moran's", "word", "for", "the"}, " "),
}

// wordingSweptExtensions are the kinds of file a person reads: prose, chart
// values, chart templates and the install notes. Go sources are deliberately
// out, so a test that has to name the phrases can name them.
var wordingSweptExtensions = map[string]bool{
	".md": true, ".yaml": true, ".yml": true, ".tpl": true, ".txt": true,
}

// wordingSweptRoots are the trees an operator's copy of one of these sentences
// could live in. SECURITY.md and GOVERNANCE.md were added by BANNERS-V4: both
// carried the retired install-banner wording and neither is under docs/ or
// named README.md, so the walk would have missed them.
var wordingSweptRoots = []string{
	"docs", "charts", "templates", "README.md", "SECURITY.md", "GOVERNANCE.md",
	// TRUTHCLOSE-DOCS, 2026-08-27. These five live-status files carried the
	// same stale "no release right now" / "in development" family the sweep
	// was already banning elsewhere, but none of them sit under docs/ or
	// carry one of the names already listed above, so the walk was missing
	// them.
	"MAINTAINERS.md", "CONTRIBUTING.md", "CHANGELOG.md",
	".claude/team/product-manager.md", ".claude/team/project-manager.md",
}

// commentMarkers are the characters a line of prose can start with when it is
// sitting inside a comment or a list. They are dropped before the words are
// read, because a sentence wrapped inside a YAML comment has a "#" in the
// middle of it and is the same sentence.
const commentMarkers = "#/*->"

// wordEdgeTrim is the set of markdown and punctuation characters trimmed from
// each word's edges before it is compared. BANNERS-V4 added this: the real
// banned sentences wrote `v4` inside backticks and ended with a period —
// "wait for `v4`." — so a word-for-word phrase match needs "v4" to reach the
// comparison as "v4", not "`v4`.". Trimming only the edges (never the middle)
// is what keeps "don't" and "internal/credsafe" intact.
const wordEdgeTrim = "`*.,:;!?()[]{}\"'"

// flatFile is one file read as a single run of lowercase words, with a record
// of which line each word came from.
//
// Reading a file this way is what lets the sweep see a sentence that was
// wrapped across a line break — which is how both of the sentences BF13-5
// banned were actually written.
type flatFile struct {
	text   string
	starts []int
	lines  []int
}

// flattenForWording drops line breaks, indentation and leading comment markers
// and returns what is left as one run of words.
func flattenForWording(body string) flatFile {
	var f flatFile
	var b strings.Builder
	for i, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimLeft(strings.TrimSpace(line), commentMarkers)
		for _, word := range strings.Fields(trimmed) {
			// BANNERS-V4: trim markdown/punctuation off the word's edges
			// (never the middle) so "`v4`." compares as "v4" and a banned
			// phrase written with markdown around it still matches.
			word = strings.Trim(word, wordEdgeTrim)
			if word == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			f.starts = append(f.starts, b.Len())
			f.lines = append(f.lines, i+1)
			b.WriteString(strings.ToLower(word))
		}
	}
	f.text = b.String()
	return f
}

// lineAt says which source line the word covering offset at came from. An
// offset past the last word reports the last line rather than nothing, so a
// report is never silently dropped.
func (f flatFile) lineAt(at int) int {
	line := 0
	for i, start := range f.starts {
		if start > at {
			break
		}
		line = f.lines[i]
	}
	return line
}

// occurrences returns every place phrase appears in the flattened text.
func (f flatFile) occurrences(phrase string) []int {
	var at []int
	for from := 0; from <= len(f.text)-len(phrase); {
		i := strings.Index(f.text[from:], phrase)
		if i < 0 {
			break
		}
		at = append(at, f.lineAt(from+i))
		from += i + 1
	}
	return at
}

// bannedHit is one banned sentence found in one place.
type bannedHit struct {
	wording string
	where   string
}

// sweepForBannedWording is the walk itself, and the ONLY reader of the tree.
// It returns what it found and how many files it looked in, and it has no
// opinion about either — the judging is done by its callers, and one of those
// callers is the control that proves this walk works at all.
func sweepForBannedWording(t *testing.T, root string) (found []bannedHit, read int) {
	t.Helper()
	for _, rel := range wordingSweptRoots {
		start := filepath.Join(root, rel)
		if _, err := os.Stat(start); err != nil {
			t.Fatalf("cannot reach %s, so this sweep did not read what it claims to read: %v", rel, err)
		}
		err := filepath.Walk(start, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !wordingSweptExtensions[strings.ToLower(filepath.Ext(path))] {
				return nil
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			read++
			rp, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rp = path
			}
			flat := flattenForWording(string(body))
			for _, wording := range bannedWordings {
				for _, line := range flat.occurrences(wording) {
					found = append(found, bannedHit{
						wording: wording,
						where:   filepath.ToSlash(rp) + ":" + itoa(line),
					})
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", rel, err)
		}
	}
	return found, read
}

// plantedControlDir and plantedControlFile name what the control writes into
// the documentation tree and then takes away again. They are deliberately
// obvious: if one is ever left behind by a run that was killed part way, the
// name says what it is and that it should not be there.
//
// The file goes into a directory BELOW docs/ rather than into docs/ itself, so
// the control also fails when the walk stops descending. A control planted at
// the top of a tree cannot tell a walk that recurses from one that does not.
const (
	plantedControlDir  = "zz-bf12-banned-wording-control-do-not-commit"
	plantedControlFile = "planted.md"
)

// plantedControlBody writes every banned sentence into one page, and writes
// each of them BROKEN ACROSS A LINE BREAK with a comment marker on the second
// half.
//
// That is not decoration. Both sentences BF13-5 banned were wrapped exactly
// like this where they shipped, and the sweep that came before could only read
// one line at a time, so it could not have found either. Planting them whole
// on one line would prove the sweep works on the one shape that was never the
// problem.
func plantedControlBody() string {
	var b strings.Builder
	b.WriteString("# control file\n\n")
	for _, wording := range bannedWordings {
		words := strings.Fields(wording)
		split := len(words) / 2
		if split == 0 {
			split = 1
		}
		b.WriteString("The chart refuses " + strings.Join(words[:split], " ") + "\n")
		b.WriteString("# " + strings.Join(words[split:], " ") + " `@`.\n\n")
	}
	return b.String()
}

// sweepProvenToFindAPlantedCopy is the sweep, with the proof that it works
// wrapped around it, and it is the only way to reach the sweep.
//
// It plants a real file holding every real banned sentence in a directory
// below docs/, which is one of the trees the real walk descends into, and
// requires the real walk to report that exact file for every one of them. A
// walk that read nothing, descended nowhere, matched nothing, or lost a
// sentence at the line break fails HERE, loudly, instead of going on to report
// a clean tree. Then the planted file is taken away and the same walk runs
// again, and what that second run found is what the caller judges.
func sweepProvenToFindAPlantedCopy(t *testing.T) []bannedHit {
	t.Helper()
	root := repoRoot(t)
	dir := filepath.Join(root, "docs", plantedControlDir)
	planted := filepath.Join(dir, plantedControlFile)

	if len(bannedWordings) == 0 {
		t.Fatal("no wording is banned, so this sweep would report every tree clean whatever is in it")
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatalf("docs/%s is already there before this test planted it. A run that was killed part "+
			"way left it behind; delete it, because it holds the sentences this test bans.", plantedControlDir)
	}
	// Registered before anything can fail, so the planted tree goes away
	// whichever way this function leaves, including on a failure below.
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("cannot make docs/%s for the control copy: %v", plantedControlDir, err)
	}
	if err := os.WriteFile(planted, []byte(plantedControlBody()), 0o644); err != nil {
		t.Fatalf("cannot plant the control copy of the banned wording: %v", err)
	}

	withPlanted, readWithPlanted := sweepForBannedWording(t, root)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("cannot take the planted control file away again: %v", err)
	}

	if readWithPlanted == 0 {
		t.Fatal("the walk read no file at all, so it could not have found anything and its silence " +
			"about the rest of the tree means nothing")
	}
	want := "docs/" + plantedControlDir + "/" + plantedControlFile
	for _, wording := range bannedWordings {
		seen := 0
		for _, hit := range withPlanted {
			if hit.wording == wording && strings.HasPrefix(hit.where, want+":") {
				seen++
			}
		}
		if seen != 1 {
			t.Fatalf("the planted page holds %q once and the walk reported it %d time(s) there. The "+
				"walk is not reading what it claims to read — most likely it lost the sentence at the "+
				"line break — so it would call this tree clean whether it was or not.\n"+
				"  it read %d files and reported: %v", wording, seen, readWithPlanted, withPlanted)
		}
	}

	found, read := sweepForBannedWording(t, root)
	if read == 0 {
		t.Fatal("the second walk read no file at all, so this guard proves nothing")
	}
	if read != readWithPlanted-1 {
		t.Fatalf("the walk read %d files with the control file planted and %d after it was taken away. "+
			"Those should differ by exactly the one planted file, so the two runs are not looking at "+
			"the same tree.", readWithPlanted, read)
	}
	return found
}

// TestTheBannedAddressWordingIsNowhereInTheDocumentationOrTheChart is the
// judgement, over a walk that has just been shown to work.
func TestTheBannedAddressWordingIsNowhereInTheDocumentationOrTheChart(t *testing.T) {
	found := sweepProvenToFindAPlantedCopy(t)
	if len(found) != 0 {
		var lines []string
		for _, hit := range found {
			lines = append(lines, hit.where+" says "+hit.wording)
		}
		t.Errorf("banned wording about the address rule is back in %d place(s):\n  %s\n\n"+
			"The chart refuses user information in the HOST part of an address, and an \"@\" in the "+
			"path is ordinary and installs. The chart's rule is a SECOND COPY of the Go one, not the "+
			"same rule, and the tests compare the two over written-down lists rather than over every "+
			"address there is. Say what is actually true instead.",
			len(found), strings.Join(lines, "\n  "))
	}
}

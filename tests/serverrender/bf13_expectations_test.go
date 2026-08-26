package serverrender

// bf13_expectations_test.go — no test of the address rule may work out what it
// expects by asking the address rule.
//
// # The shape being banned
//
// Two of the tests that were supposed to be guarding this rule used to compute
// their own expectation from the code they were checking:
//
//	wantOpen := ClassifyAddress(raw) == AddressCredentialFree
//
// checked against two thin wrappers over ClassifyAddress itself. Loosen the
// classifier and the expectation loosens with it, in the same direction, in
// the same run. A test written that way cannot fail, whatever the code does,
// and a guard that reports clean whether or not the tree is clean is worse
// than no guard because it buys confidence that is not there. Both of those
// were removed in BF13-1; this file is what stops the shape coming back
// anywhere else.
//
// # What is banned, and what is not
//
// Banned: a variable that names itself an expectation — want, expected,
// should — being assigned from an expression that calls the address rule.
//
// NOT banned: comparing two SEPARATE implementations of the rule against each
// other. tests/serverrender/bf13_chart_corpus_test.go asks the chart for a
// verdict and uses internal/credsafe's answer as the expectation, and that is
// a real test: the two are different code, written in different languages, and
// the whole point is that they must agree. Those are written down below, and
// the list is checked BOTH ways — a new one fails here, and one that has gone
// away fails here too.
//
// # Where the names come from
//
// Nothing about the rule is typed out here. The functions that decide about an
// address are read out of internal/credsafe's own sources, and the chart's
// address templates are read out of the chart, so a rename or a NEW decision
// function is picked up without anybody remembering this file exists. A
// hand-written list of function names would go stale exactly the way the
// hand-written list of addresses did.

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// crossImplementationComparison is one place where one implementation of the
// address rule is used, on purpose, as the expectation for a DIFFERENT
// implementation of it.
type crossImplementationComparison struct {
	// reference is the implementation being used as the expectation. Every
	// expectation the sweep finds in this file has to come from it.
	reference string
	// lines is how many such expectations the file holds. It is an EXACT
	// number compared with "not equal": one more is a place nobody looked
	// at, one fewer is an entry that has gone stale.
	lines int
}

// deliberateCrossImplementationExpectations is the whole of the exception.
//
// tests/serverrender/bf13_chart_corpus_test.go asks the CHART for a verdict
// and uses internal/credsafe's answer as the expectation. That is not a test
// agreeing with itself: the two are separate code in separate languages, and
// the entire point of the file is that they must give the same answer. The
// written-down list at testdata/address-rule-corpus.yaml is what neither of
// them can shape, and it is what both are separately checked against.
//
// The reference is stored by name rather than as a copy of the source line, so
// that this file is not itself an example of the shape it bans. It is checked
// both ways: a file the sweep finds that is not written here fails, an entry
// here the sweep no longer finds fails, and a file that grows a second
// expectation fails on the count.
var deliberateCrossImplementationExpectations = map[string]crossImplementationComparison{
	"tests/serverrender/bf13_chart_corpus_test.go": {reference: "credsafe.ClassifyAddress", lines: 1},
}

// expectationSweptRoots are the trees holding the tests of the address rule.
var expectationSweptRoots = []string{
	filepath.Join("internal", "credsafe"),
	filepath.Join("internal", "addresscorpus"),
	filepath.Join("tests", "serverrender"),
}

var (
	// expectationAssignment matches a variable naming itself an expectation
	// being assigned something.
	expectationAssignment = regexp.MustCompile(`\b(want|wanted|expect|expected|should)[A-Za-z0-9_]*\s*(:=|=)\s*(\S.*)$`)
	// goFuncDeclaration finds the exported functions a Go package declares,
	// with or without a receiver.
	goFuncDeclaration = regexp.MustCompile(`(?m)^func\s+(?:\([^)]*\)\s+)?([A-Z][A-Za-z0-9_]*)\s*\(`)
	// chartDefine finds the names of the chart's named templates.
	chartDefine = regexp.MustCompile(`\{\{-?\s*define\s+"([^"]+)"`)
	// identifier picks the words out of an expression, so each one can be
	// looked up among the names that decide about an address.
	identifier = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_.]*`)
)

// addressDecisionNames reads, rather than lists, every name that decides
// something about an address: the exported functions internal/credsafe
// declares in its own non-test sources, and the chart's named templates whose
// name says address.
func addressDecisionNames(t *testing.T) map[string]bool {
	t.Helper()
	root := repoRoot(t)
	names := map[string]bool{}

	pkg := filepath.Join(root, "internal", "credsafe")
	entries, err := os.ReadDir(pkg)
	if err != nil {
		t.Fatalf("cannot read internal/credsafe, so the names below were never worked out: %v", err)
	}
	readSources := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		body, readErr := os.ReadFile(filepath.Join(pkg, e.Name()))
		if readErr != nil {
			t.Fatalf("cannot read internal/credsafe/%s: %v", e.Name(), readErr)
		}
		readSources++
		for _, m := range goFuncDeclaration.FindAllStringSubmatch(string(body), -1) {
			names[m[1]] = true
		}
	}
	if readSources == 0 {
		t.Fatal("no non-test source was read out of internal/credsafe, so the sweep below has no name " +
			"to look for and would report every test file clean")
	}

	_, rel := chartTemplateFiles(t)
	dir := filepath.Join(root, "charts", "sharko", "templates")
	for _, name := range rel {
		body, readErr := os.ReadFile(filepath.Join(dir, name))
		if readErr != nil {
			t.Fatalf("cannot read the chart template %s: %v", name, readErr)
		}
		for _, m := range chartDefine.FindAllStringSubmatch(string(body), -1) {
			if strings.Contains(strings.ToLower(m[1]), "address") {
				names[m[1]] = true
			}
		}
	}

	// The two the whole thing is about. If either has stopped being found,
	// the reading above is broken and every sweep built on it is empty.
	for _, must := range []string{"ClassifyAddress", "sharko.classifyAddress"} {
		if !names[must] {
			t.Fatalf("%q was not found among the names that decide about an address, so the sweep "+
				"below is looking for the wrong things", must)
		}
	}
	return names
}

// sweepForSelfReferentialExpectations is the walk. It returns, for every test
// file under the swept roots, each line where a variable naming itself an
// expectation is assigned from something that calls the address rule.
func sweepForSelfReferentialExpectations(t *testing.T, root string, names map[string]bool) (found map[string][]string, read int) {
	t.Helper()
	found = map[string][]string{}
	for _, rel := range expectationSweptRoots {
		start := filepath.Join(root, rel)
		if _, err := os.Stat(start); err != nil {
			t.Fatalf("cannot reach %s, so this sweep did not read what it claims to read: %v", rel, err)
		}
		err := filepath.WalkDir(start, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
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
			rp = filepath.ToSlash(rp)
			for _, line := range strings.Split(string(body), "\n") {
				trimmed := strings.TrimSpace(line)
				// A whole-line comment is prose. Two of them quote the
				// banned shape on purpose, to say what it was.
				if strings.HasPrefix(trimmed, "//") {
					continue
				}
				m := expectationAssignment.FindStringSubmatch(trimmed)
				if m == nil {
					continue
				}
				if !callsAnAddressDecision(m[3], names) {
					continue
				}
				found[rp] = append(found[rp], whitespaceRun.ReplaceAllString(trimmed, " "))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", rel, err)
		}
	}
	return found, read
}

// callsAnAddressDecision reports whether the right-hand side of an assignment
// names any of the things that decide about an address.
func callsAnAddressDecision(rhs string, names map[string]bool) bool {
	for _, word := range identifier.FindAllString(rhs, -1) {
		if names[word] {
			return true
		}
		// A qualified call such as credsafe.ClassifyAddress, and a chart
		// template named inside a string.
		if i := strings.LastIndex(word, "."); i >= 0 && names[word[i+1:]] {
			return true
		}
	}
	for name := range names {
		if strings.Contains(name, ".") && strings.Contains(rhs, name) {
			return true
		}
	}
	return false
}

// sweepProvenToFindAPlantedExpectation is the sweep with the proof that it
// works wrapped around it, and it is the only way to reach the sweep.
//
// It writes a test file holding the exact banned shape into a directory the
// real walk descends into, requires the real walk to come back with it, then
// takes it away and returns what the same walk found without it. A walk that
// read nothing, matched nothing or descended nowhere fails HERE rather than
// going on to report the tests clean.
//
// The planted file goes under a directory named testdata. The go tool ignores
// every directory with that name, so nothing is ever compiled from it, while
// an ordinary file walk descends into it like any other directory.
func sweepProvenToFindAPlantedExpectation(t *testing.T, names map[string]bool) map[string][]string {
	t.Helper()
	root := repoRoot(t)
	// The go tool ignores every directory named testdata, so nothing here is
	// ever compiled, while an ordinary file walk descends into it like any
	// other directory.
	parent := filepath.Join(root, "tests", "serverrender", "testdata")
	dir := filepath.Join(parent, plantedExpectationDir)
	planted := filepath.Join(dir, "zz_planted_control_test.go")

	if _, err := os.Stat(dir); err == nil {
		t.Fatalf("tests/serverrender/testdata/%s is already there before this test planted it. A run "+
			"that was killed part way left it behind; delete it.", plantedExpectationDir)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
		// And the testdata directory itself when this test made it. Remove
		// refuses a directory that is not empty, so a testdata directory
		// holding anything else is left exactly as it was.
		_ = os.Remove(parent)
	})

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("cannot make the directory for the planted control: %v", err)
	}
	// The banned shape, exactly as it was written the day it was removed.
	body := "package planted\n\nfunc controlCase(raw string) {\n" +
		"\twantOpen := credsafe.ClassifyAddress(raw) == credsafe.AddressCredentialFree\n" +
		"\t_ = wantOpen\n}\n"
	if err := os.WriteFile(planted, []byte(body), 0o644); err != nil {
		t.Fatalf("cannot plant the control expectation: %v", err)
	}

	withPlanted, readWithPlanted := sweepForSelfReferentialExpectations(t, root, names)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("cannot take the planted control file away again: %v", err)
	}
	_ = os.Remove(parent)

	if readWithPlanted == 0 {
		t.Fatal("the sweep read no test file at all, so it could not have found anything and its " +
			"silence about the rest of the tree means nothing")
	}
	wantFile := "tests/serverrender/testdata/" + plantedExpectationDir + "/zz_planted_control_test.go"
	if len(withPlanted[wantFile]) == 0 {
		t.Fatalf("a file holding the banned shape was written to %s and the sweep did not report it. "+
			"The sweep is not reading what it claims to read, so it would call these tests clean "+
			"whether they were or not.\n  it read %d test files and reported: %v",
			wantFile, readWithPlanted, withPlanted)
	}

	found, read := sweepForSelfReferentialExpectations(t, root, names)
	if read == 0 {
		t.Fatal("the second sweep read no test file at all, so this guard proves nothing")
	}
	if read != readWithPlanted-1 {
		t.Fatalf("the sweep read %d test files with the control planted and %d after it was taken "+
			"away. Those should differ by exactly the one planted file, so the two runs are not "+
			"looking at the same tree.", readWithPlanted, read)
	}
	return found
}

// plantedExpectationDir is the directory the control writes and removes. The
// name says what it is, so one left behind by a run that was killed part way
// is obvious rather than mysterious.
const plantedExpectationDir = "zz-bf13-expectation-control-do-not-commit"

// TestNoAddressTestWorksOutWhatItExpectsFromTheAddressRule is the judgement,
// over a sweep that has just been shown to work.
func TestNoAddressTestWorksOutWhatItExpectsFromTheAddressRule(t *testing.T) {
	names := addressDecisionNames(t)
	found := sweepProvenToFindAPlantedExpectation(t, names)

	// Fails on growth: a place that computes an expectation from the rule and
	// is not one of the deliberate cross-implementation comparisons, or one
	// that is in such a file but reads a different implementation.
	var unexplained []string
	for file, lines := range found {
		allowed, deliberate := deliberateCrossImplementationExpectations[file]
		for _, line := range lines {
			if deliberate && strings.Contains(line, allowed.reference) {
				continue
			}
			unexplained = append(unexplained, file+": "+line)
		}
		if deliberate && len(lines) != allowed.lines {
			unexplained = append(unexplained, file+" holds "+itoa(len(lines))+
				" expectations taken from another implementation of the rule; "+
				itoa(allowed.lines)+" is written down here")
		}
	}
	if len(unexplained) != 0 {
		sort.Strings(unexplained)
		t.Errorf("these tests work out what they expect by asking the address rule itself:\n  %s\n\n"+
			"An expectation computed from the code under test moves whenever that code moves, so the "+
			"test cannot fail. Write the expected answer out by hand, or take it from "+
			"testdata/address-rule-corpus.yaml, which was written from the rule rather than from "+
			"either copy of it.", strings.Join(unexplained, "\n  "))
	}

	// Fails on a stale entry: something written down here as deliberate that
	// the sweep no longer finds. An exception nobody prunes is an exception
	// nobody is looking at.
	var stale []string
	for file, allowed := range deliberateCrossImplementationExpectations {
		if len(found[file]) == 0 {
			stale = append(stale, file+", which was said to take its expectation from "+allowed.reference)
		}
	}
	if len(stale) != 0 {
		sort.Strings(stale)
		t.Errorf("these are written down here as deliberate comparisons between two implementations of "+
			"the rule, and the sweep no longer finds one:\n  %s\nDelete the entry, or put back what "+
			"it was protecting.", strings.Join(stale, "\n  "))
	}
}

package connectioncompare

// leaves_comparison_guard_test.go — B15's guard.
//
// The rule this package exists to keep: "synced" means Sharko compared
// EVERYTHING it owns on this connection and it all matched. Compare enforces
// that with one clause — synced needs a full scope AND an empty NotChecked
// list. That clause only holds if every place a field can leave the
// comparison without being compared either records the field in NotChecked or
// is genuinely outside Sharko's scope.
//
// So this file writes those places down as a LIST, not a count, and checks the
// list against the real source. It fails in both directions:
//
//   - a new way to leave the comparison appears and is not on the list;
//   - a listed entry no longer exists in the source (stale entry).
//
// The floor is the EXACT number of exit sites, never "at least". A floor with
// room in it is a hole, and a hole exactly like that is what B15 found.

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"

	"strings"
	"testing"
)

// leavesComparison is one way a field can leave the comparison without being
// compared, in the three functions that actually walk fields.
type leavesComparison struct {
	// fn is the function it lives in; cond is the guard condition, printed
	// from the source exactly as written.
	fn   string
	cond string
	// occurrences is how many times this exact guard appears in that
	// function. Written down rather than inferred so a copy-pasted second
	// one fails the guard instead of hiding behind the first.
	occurrences int
	// recorded is true when the field is named in NotChecked on the way out.
	// false means it is safe not to record, and why says why.
	recorded bool
	why      string
}

// The list. Every entry was read off the source and classified by hand.
var leavesComparisonSites = []leavesComparison{
	{
		fn: "compareLabels", cond: "!ownedLabelKey(key, scope)", occurrences: 2,
		recorded: false,
		why: "The key is not Sharko's at this scope — a foreign or system label, or one of Sharko's own " +
			"labels on a connection Sharko does not own. Sharko never claimed it, so there is nothing " +
			"it failed to verify. Once for the Git-declared side, once for the live side.",
	},
	{
		fn: "compareLabels", cond: "skip[key]", occurrences: 2,
		recorded: true,
		why: "The Git-declared side records it in NotChecked (Sharko claims the key and did not compare " +
			"it). The live side does not, and that asymmetry is the point: a skipped key Sharko does " +
			"NOT declare is a label the previous owner kept, outside Sharko's scope entirely. " +
			"TestCompare_PreservedLabelGitAlsoDeclaresIsNotChecked and " +
			"TestCompare_ForeignPreservedLabelIsNotAGapInTheCheck pin the two halves.",
	},
	{
		fn: "compareLabels", cond: "_, declared := expected[key]; declared", occurrences: 1,
		recorded: false,
		why:      "Not an exit at all — the key was already compared by the loop above. Skipping it here avoids reporting it twice.",
	},
	{
		fn: "compareConnectionData", cond: "expectedSecret == nil", occurrences: 1,
		recorded: true,
		why:      "Records data.name, data.server and data.config in NotChecked before returning.",
	},
	{
		fn: "compareConnectionData", cond: "!wantOK && !haveOK", occurrences: 1,
		recorded: false,
		why: "Neither side has the key. Both sides agreeing that a field is absent IS a comparison that " +
			"found no difference, not a field that went unchecked.",
	},
	{
		fn: "compareConnectionData", cond: "req.Policy.Mode == ModeEKSToken", occurrences: 1,
		recorded: true,
		why:      "Records data.config in NotChecked before returning — the backend stores no credential to compare against.",
	},
}

// exclusionSite is a way a field leaves the comparison that is NOT a continue
// or a return — a whole block that is only entered at some scopes, or a set of
// field names the comparison never reaches for. These are pinned by an exact
// piece of source text, so deleting or reshaping one fails this test.
type exclusionSite struct {
	name     string
	file     string
	anchor   string
	recorded bool
	why      string
}

var comparisonExclusionSites = []exclusionSite{
	{
		name: "identity, type and connection data are only compared on a connection Sharko owns",
		file: "compare.go", anchor: "if req.Policy.Scope == ScopeFull || req.Policy.Scope == ScopeLimited {",
		recorded: false,
		why: "At the two scopes this block is skipped at, Sharko owns the addon labels and nothing else, " +
			"so these fields are not a narrower version of Sharko's job — they are not Sharko's job. " +
			"The scope word and the mode's own limit sentence already say so, and the answer can never " +
			"be synced-at-full-scope from there.",
	},
	{
		name: "labels a takeover recorded as the previous owner's",
		file: "fields.go", anchor: "func labelSkipSet(live *corev1.Secret) map[string]bool {",
		recorded: true,
		why: "Feeds the skip set consumed by compareLabels above, where the Git-declared half is recorded " +
			"in NotChecked.",
	},
	{
		name: "Kubernetes-owned metadata that changes on its own",
		file: "fields.go", anchor: "var volatileMetadataFields = []string{",
		recorded: false,
		why: "resourceVersion, uid, generation, creationTimestamp and managedFields are Kubernetes', not " +
			"Sharko's. The comparison never reads them; they are named for the record.",
	},
	{
		name: "annotations are never compared",
		file: "fields.go", anchor: "var comparedAnnotationKeys = []string{}",
		recorded: false,
		why: "Not one annotation Sharko writes has a stable expected value, so there is no expectation to " +
			"fail. TestComparedAnnotationsIsDeliberatelyEmpty pins the emptiness itself.",
	},
}

// walkedFunctions are the three functions that decide, field by field, what
// gets compared. Every continue and every early return in them is a way out.
var walkedFunctions = []string{"compareLabels", "compareIdentityAndType", "compareConnectionData"}

func TestEveryWayAFieldLeavesTheComparisonIsListed(t *testing.T) {
	if len(leavesComparisonSites) == 0 || len(comparisonExclusionSites) == 0 {
		t.Fatal("the guard's own lists are empty — it would pass no matter what the source did")
	}

	found := scanExitSites(t, "compare.go")
	if len(found) == 0 {
		t.Fatal("no exit sites found in compare.go at all — the scan is broken, not the code")
	}

	declared := map[string]int{}
	declaredTotal := 0
	for _, s := range leavesComparisonSites {
		if s.occurrences < 1 {
			t.Errorf("%s :: %s declares %d occurrences — an entry that matches nothing is a stale entry", s.fn, s.cond, s.occurrences)
		}
		if !s.recorded && strings.TrimSpace(s.why) == "" {
			t.Errorf("%s :: %s is classified safe-not-to-record with no reason given", s.fn, s.cond)
		}
		declared[s.fn+" :: "+s.cond] += s.occurrences
		declaredTotal += s.occurrences
	}

	for key, gotN := range found {
		wantN, listed := declared[key]
		if !listed {
			t.Errorf("a field can leave the comparison here and it is NOT on the list:\n  %s\n"+
				"Add it to leavesComparisonSites and say whether the field is recorded in NotChecked "+
				"or is safe not to record — and why. An unlisted skip is how a connection ends up "+
				"reported as synced without having been checked.", key)
			continue
		}
		if gotN != wantN {
			t.Errorf("%s appears %d time(s) in the source, the list says %d", key, gotN, wantN)
		}
	}
	for key := range declared {
		if _, still := found[key]; !still {
			t.Errorf("the list names an exit that is no longer in the source: %s\n"+
				"Either it moved, or it is gone. A stale entry makes the exact count wrong and the "+
				"guard stops being exact.", key)
		}
	}

	foundTotal := 0
	for _, n := range found {
		foundTotal += n
	}
	if foundTotal != declaredTotal {
		t.Errorf("compare.go has %d ways for a field to leave the comparison, the list accounts for %d. "+
			"The floor is the EXACT number, never a minimum — a floor with room in it is a hole.",
			foundTotal, declaredTotal)
	}

	for _, ex := range comparisonExclusionSites {
		src := readPackageFile(t, ex.file)
		if n := strings.Count(src, ex.anchor); n != 1 {
			t.Errorf("exclusion %q: its anchor appears %d time(s) in %s, want exactly 1.\n  anchor: %s\n"+
				"Either the code it names changed shape, or a second copy appeared. Re-read the code "+
				"and update the entry.", ex.name, n, ex.file, ex.anchor)
		}
		if !ex.recorded && strings.TrimSpace(ex.why) == "" {
			t.Errorf("exclusion %q is classified safe-not-to-record with no reason given", ex.name)
		}
	}
}

// scanExitSites parses one file of this package and returns, per
// "function :: condition", how many continue or early-return statements sit
// directly under that condition inside the walked functions.
func scanExitSites(t *testing.T, file string) map[string]int {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", file, err)
	}

	walked := map[string]bool{}
	for _, fn := range walkedFunctions {
		walked[fn] = true
	}

	out := map[string]int{}
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || !walked[fn.Name.Name] {
			continue
		}
		var closing ast.Stmt
		if n := len(fn.Body.List); n > 0 {
			closing = fn.Body.List[n-1]
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			ifStmt, ok := n.(*ast.IfStmt)
			if !ok || ifStmt.Body == nil {
				return true
			}
			exits := false
			for _, stmt := range ifStmt.Body.List {
				switch s := stmt.(type) {
				case *ast.BranchStmt:
					if s.Tok == token.CONTINUE {
						exits = true
					}
				case *ast.ReturnStmt:
					if ast.Stmt(s) != closing {
						exits = true
					}
				}
			}
			if exits {
				out[fn.Name.Name+" :: "+printCondition(fset, ifStmt)]++
			}
			return true
		})
	}
	return out
}

func printCondition(fset *token.FileSet, ifStmt *ast.IfStmt) string {
	render := func(node ast.Node) string {
		var buf bytes.Buffer
		if err := printer.Fprint(&buf, fset, node); err != nil {
			return "<unprintable>"
		}
		return strings.Join(strings.Fields(buf.String()), " ")
	}
	cond := render(ifStmt.Cond)
	if ifStmt.Init != nil {
		return render(ifStmt.Init) + "; " + cond
	}
	return cond
}

func readPackageFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}

// TestExitSitesAreGroupedForReading is a small readability check on the
// guard's own list — all the entries for one function sit together, so
// somebody adding one can see where it belongs.
func TestExitSitesAreGroupedForReading(t *testing.T) {
	seen := map[string]bool{}
	prev := ""
	for _, s := range leavesComparisonSites {
		if s.fn != prev {
			if seen[s.fn] {
				t.Errorf("%s appears in two separate runs of leavesComparisonSites — keep one function's entries together", s.fn)
			}
			seen[s.fn] = true
			prev = s.fn
		}
	}
}

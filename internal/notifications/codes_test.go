package notifications

// codes_test.go — the guards that make the identifier a real contract rather
// than a field somebody remembers to fill in.
//
// The product owner's ruling has two browser break tests attached to it:
// change a notification's title and prove both connection panels still receive
// their details, and change its identifier and prove the wrong panel cannot.
// Those are browser tests and live with the browser. These are their server
// halves — the guarantees that make them possible and make them true:
//
//   - the identifier does not depend on the wording (TestCodes_...IndependentOfWording)
//   - no two kinds share an identifier      (TestCodes_AreDistinct)
//   - every declared identifier is emitted  (TestCodes_EveryDeclaredCodeIsEmitted)
//   - every notification carries one        (TestCodes_EveryNotificationSetsACode)
//
// EVERY LIST HERE NAMES THINGS. Not one of them counts. This repository has
// been bitten repeatedly by a guard that asserted a number and reported
// "expected 8, got 7" about a thing nobody could then find.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestCodes_WireValuesExact pins every identifier as a LITERAL.
//
// The literals are typed out here on purpose. Writing
// `CodeGitConnectionBroken == CodeGitConnectionBroken` compares a symbol with
// itself and passes no matter what the value is — that exact mistake has been
// found three times in this repository, most recently in a browser "pin" that
// let a live mismatch survive. These strings ship in the API response, the
// OpenAPI spec and the generated TypeScript, so changing one is a wire change
// and has to be a deliberate edit here too.
//
// It is a SLICE OF NAMED CASES, not a map keyed by the value: keyed by value,
// two constants holding the same string would collapse into one entry and the
// test would pass having checked one fewer, saying so nowhere.
func TestCodes_WireValuesExact(t *testing.T) {
	exact := []struct {
		name string
		got  Code
		want string
	}{
		{"CodeGitConnectionBroken", CodeGitConnectionBroken, "git_connection_broken"},
		{"CodeArgoRepoBroken", CodeArgoRepoBroken, "argocd_repo_broken"},
		{"CodeArgoAuthFailed", CodeArgoAuthFailed, "argocd_auth_failed"},
		{"CodeArgoUnreachable", CodeArgoUnreachable, "argocd_unreachable"},
		{"CodeArgoForbidden", CodeArgoForbidden, "argocd_forbidden"},
		{"CodeAddonUpgradeAvailable", CodeAddonUpgradeAvailable, "addon_upgrade_available"},
		{"CodeAddonMajorUpdate", CodeAddonMajorUpdate, "addon_major_update"},
		{"CodeAddonVersionDrift", CodeAddonVersionDrift, "addon_version_drift"},
	}

	for _, tc := range exact {
		if string(tc.got) != tc.want {
			t.Errorf("the notification code %s changed:\n got %q\nwant %q\nThese ship on the wire — a rename breaks every consumer.",
				tc.name, string(tc.got), tc.want)
		}
	}

	// Every pinned case must actually be declared, and every declared code
	// must be pinned — otherwise a new code could be added and this pin would
	// simply not mention it.
	pinned := make(map[Code]string, len(exact))
	for _, tc := range exact {
		pinned[tc.got] = tc.name
	}
	for _, declared := range DeclaredCodes() {
		if _, ok := pinned[declared]; !ok {
			t.Errorf("the code %q is declared but is not pinned in this test — add it, so its wire value cannot change unnoticed", declared)
		}
	}
	for _, tc := range exact {
		if !tc.got.IsDeclared() {
			t.Errorf("this test pins %s but it is not in declaredCodes — Store.Add would refuse every notification using it", tc.name)
		}
	}
}

// TestCodes_AreDistinct proves no two kinds share an identifier. Two kinds on
// one identifier collapses the routing they exist to separate: whichever panel
// the browser maps that identifier to would receive both, and the other would
// get nothing.
//
// It reports the COLLIDING NAMES, not a count.
func TestCodes_AreDistinct(t *testing.T) {
	seen := make(map[Code]int)
	for _, code := range DeclaredCodes() {
		seen[code]++
	}
	for code, count := range seen {
		if count > 1 {
			// Name every Go constant holding this value, so the report says
			// which two to look at rather than only which string clashed.
			var holders []string
			for name, value := range declaredCodeSourceNames(t) {
				if value == code {
					holders = append(holders, name)
				}
			}
			t.Errorf("the identifier %q is declared %d times — the constants sharing it are %s. Two kinds on one identifier cannot be routed apart.",
				code, count, strings.Join(holders, ", "))
		}
	}
}

// TestCodes_EmptyIsNotDeclared is what makes the field effectively required:
// a Notification built without a Code holds the zero value, and the zero value
// must never pass IsDeclared, or Store.Add's refusal would let it through.
func TestCodes_EmptyIsNotDeclared(t *testing.T) {
	if Code("").IsDeclared() {
		t.Error("the empty code is declared — a notification built with no code at all would be accepted and served")
	}
	if Code("something_nobody_declared").IsDeclared() {
		t.Error("IsDeclared accepted a value that is not in the declared set — the closed set is not closed")
	}
	// And the guard is not passing vacuously.
	if !CodeGitConnectionBroken.IsDeclared() {
		t.Error("IsDeclared rejected a code that IS declared — the check is broken in the other direction")
	}
}

// TestCodes_DeclaredCodesCannotBeMutatedByACaller proves the closed set stays
// closed: DeclaredCodes hands out a copy, so a caller editing the slice it
// receives cannot quietly redefine what the server may emit.
func TestCodes_DeclaredCodesCannotBeMutatedByACaller(t *testing.T) {
	handedOut := DeclaredCodes()
	if len(handedOut) == 0 {
		t.Fatal("no codes are declared")
	}
	handedOut[0] = "tampered_with"

	for _, code := range DeclaredCodes() {
		if code == "tampered_with" {
			t.Error("editing the slice DeclaredCodes returned changed the declared set — it must return a copy")
		}
	}
	if Code("tampered_with").IsDeclared() {
		t.Error("a caller managed to add a code to the closed set by editing the returned slice")
	}
}

// ---------------------------------------------------------------------------
// Source sweeps
// ---------------------------------------------------------------------------

// producerFiles are the files that construct notifications, relative to the
// repository root. A new notification kind lives in one of these or adds to
// the list.
var producerFiles = []string{
	"internal/notifications/connection_poller.go",
	"internal/notifications/checker.go",
}

// codeReferenceFiles are swept for USES of each declared code. It is
// deliberately wider than producerFiles: cmd/sharko/serve.go picks the ArgoCD
// codes apart inside its health-probe closure, which is the only place three
// of them are chosen.
var codeReferenceFiles = []string{
	"internal/notifications/connection_poller.go",
	"internal/notifications/checker.go",
	"cmd/sharko/serve.go",
}

// TestCodes_EveryDeclaredCodeIsEmitted fails BY NAME on any declared code that
// no production file ever uses.
//
// A declared-but-unreachable code is a promise the API does not keep: it ships
// in the OpenAPI spec and the generated TypeScript, so a browser author writes
// a branch for it that can never run, and nobody finds out.
//
// It names the unreachable ones. It does not count them — a guard that says
// "expected 8 references, found 7" tells you a number and leaves you to find
// the missing one by hand.
func TestCodes_EveryDeclaredCodeIsEmitted(t *testing.T) {
	root := repoRoot(t)

	// Collect every identifier used across the reference files. codes.go is
	// deliberately NOT swept: it holds the declarations and the declaredCodes
	// list, so sweeping it would make every code look referenced.
	used := make(map[string]bool)
	for _, rel := range codeReferenceFiles {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", rel, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if ident, ok := n.(*ast.Ident); ok {
				used[ident.Name] = true
			}
			return true
		})
	}
	if len(used) == 0 {
		t.Fatal("the sweep collected no identifiers at all — it has lost its reach and would pass vacuously")
	}

	names := declaredCodeSourceNames(t)
	if len(names) == 0 {
		t.Fatal("no code constants were found in codes.go — this guard would pass having checked nothing")
	}

	var unreachable []string
	for name, value := range names {
		if !used[name] {
			unreachable = append(unreachable, name+" ("+string(value)+")")
		}
	}
	if len(unreachable) > 0 {
		sortStrings(unreachable)
		t.Errorf("these notification codes are declared but never emitted anywhere:\n  %s\n"+
			"They ship in the OpenAPI spec and the generated TypeScript, so a browser branch for them can never run. "+
			"Either emit them or delete them.\nFiles swept: %s",
			strings.Join(unreachable, "\n  "), strings.Join(codeReferenceFiles, ", "))
	}
}

// TestCodes_EveryNotificationGoesThroughNew is the coverage guard.
//
// It walks the producer files and fails BY NAME (file and line) on two things:
// a call to New() whose first argument is not a Code constant, and any
// hand-written `Notification{...}` composite literal.
//
// The second is the one that matters. New() is the only way to put dynamic
// detail on a notification — its Params field is unexported, so a struct
// literal cannot carry any, and a producer that writes one gets the generic
// sentence for every alert it raises while nothing errors. That is a silent
// loss of information, so it is caught in the build.
//
// This used to check that every Notification literal set a Code. There are no
// Notification literals in the producers any more, so a guard written that way
// would now sweep nothing and pass.
func TestCodes_EveryNotificationGoesThroughNew(t *testing.T) {
	root := repoRoot(t)

	checked := 0
	for _, rel := range producerFiles {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", rel, err)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			if lit, ok := n.(*ast.CompositeLit); ok {
				if ident, ok := lit.Type.(*ast.Ident); ok && ident.Name == "Notification" {
					t.Errorf("%s:%d: this notification is built as a struct literal instead of with New(). "+
						"A literal cannot carry Params (the field is unexported), so every alert it raises would "+
						"render the generic sentence with nothing failing. Call New(code, reason, Params{...}, ts).",
						rel, fset.Position(lit.Pos()).Line)
				}
				return true
			}
			call, ok := n.(*ast.CallExpr)
			if !ok || calleeName(call.Fun) != "New" {
				return true
			}
			checked++
			if len(call.Args) == 0 {
				t.Errorf("%s:%d: New() was called with no arguments", rel, fset.Position(call.Pos()).Line)
				return true
			}
			// A Code constant, or a variable holding one (the poller picks its
			// code at runtime from the probe's answer). What is rejected is a
			// string literal or a call — the two shapes that mean somebody
			// typed a wire value in by hand or derived one from an error.
			switch call.Args[0].(type) {
			case *ast.Ident, *ast.SelectorExpr:
			default:
				t.Errorf("%s:%d: New()'s first argument is not a Code constant or a variable holding one. "+
					"Every notification needs a stable identifier from codes.go, and Store.Add refuses one it does not know.",
					rel, fset.Position(call.Args[0].Pos()).Line)
			}
			return true
		})
	}

	if checked == 0 {
		t.Errorf("no calls to New() were found in %s — this guard has lost its reach and would pass vacuously",
			strings.Join(producerFiles, ", "))
	}
}

// TestCodes_TitlesAreNotUsedAsKeys sweeps the package for the wording-keyed
// behaviour this story removed, so it cannot come back quietly.
//
// The three that existed: the store deduplicated by Title, resolved by Title,
// and merged persisted state by Title; the poller remembered which alert was
// open by its Title, and built the notification ID by interpolating the Title.
// Each is a comparison or a key over a sentence a person reads.
func TestCodes_TitlesAreNotUsedAsKeys(t *testing.T) {
	root := repoRoot(t)

	// Patterns that mean "a title is being compared or used as a key". Each
	// is written the way the removed code was actually written.
	banned := []struct{ pattern, why string }{
		{".Title ==", "comparing titles routes on wording — compare Code instead"},
		{".Title !=", "comparing titles routes on wording — compare Code instead"},
		{"lastTitle", "remembering which alert is open by its title makes recovery depend on the sentence matching — track the Code"},
		{"Resolve(*lastTitle", "resolving by title means a reworded sentence leaves the alert stuck on the bell"},
	}

	swept := 0
	for _, rel := range []string{
		"internal/notifications/store.go",
		"internal/notifications/connection_poller.go",
		"internal/notifications/checker.go",
		"internal/notifications/render.go",
	} {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		swept++

		for _, line := range strings.Split(string(raw), "\n") {
			// Comments explain the history on purpose; only code counts.
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, b := range banned {
				if strings.Contains(line, b.pattern) {
					t.Errorf("%s: %q is back — %s\n  %s", rel, b.pattern, b.why, strings.TrimSpace(line))
				}
			}
		}
	}
	if swept == 0 {
		t.Fatal("no files were swept — this guard would pass vacuously")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// declaredCodeSourceNames parses codes.go and returns Go constant name → value
// for every constant of type Code. Reading the source rather than a
// hand-written list means a constant added tomorrow is covered without anybody
// remembering to add it here.
func declaredCodeSourceNames(t *testing.T) map[string]Code {
	t.Helper()

	path := filepath.Join(repoRoot(t), "internal/notifications/codes.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parsing codes.go: %v", err)
	}

	found := make(map[string]Code)
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			// Only constants explicitly typed `Code`.
			typeIdent, ok := vs.Type.(*ast.Ident)
			if !ok || typeIdent.Name != "Code" {
				continue
			}
			for i, val := range vs.Values {
				lit, ok := val.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING || i >= len(vs.Names) {
					continue
				}
				text, unquoteErr := strconv.Unquote(lit.Value)
				if unquoteErr != nil {
					continue
				}
				found[vs.Names[i].Name] = Code(text)
			}
		}
	}
	return found
}

// repoRoot walks up from the working directory until it finds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find the repository root (no go.mod in any parent directory)")
		}
		dir = parent
	}
}

// sortStrings is a tiny in-place sort so failure output is stable between
// runs — a report whose lines reorder every run is harder to diff.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

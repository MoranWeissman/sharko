package main

// main_test.go — the generator reads the real catalog and holds no second copy
// of it, plus the rendering rules the output format depends on.
//
// The risk being closed is a generator carrying its own hardcoded list of
// identifiers: it would produce a browser file that looks generated, carries a
// generator's authority, and is wrong the moment Go declares a new code. So
// that is proved structurally rather than by anyone remembering the rule —
// no string literal anywhere in main.go is a declared code value, and main.go
// imports internal/notifications while importing no Go-source parser.
//
// Note what is deliberately NOT here: any assertion of the shape
// `DeclaredCodes()[0] == CodeGitConnectionBroken`. That compares a symbol with
// itself and is green forever while proving nothing. The wire values are
// pinned as LITERALS in internal/notifications/codes_test.go.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/notifications"
)

const generatorSource = "main.go"

// parseGenerator parses main.go once for the structural guards below.
func parseGenerator(t *testing.T) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, generatorSource, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing %s: %v", generatorSource, err)
	}
	return fset, file
}

// TestGenerator_HoldsNoCopyOfAnyCode is the structural guard: not one of the
// identifiers this generator emits may appear as a string literal inside it.
// If somebody ever "helpfully" inlines the list, this fails by name.
func TestGenerator_HoldsNoCopyOfAnyCode(t *testing.T) {
	fset, file := parseGenerator(t)

	declared := make(map[string]bool)
	for _, code := range notifications.DeclaredCodes() {
		declared[string(code)] = true
	}
	if len(declared) == 0 {
		t.Fatal("no codes are declared, so this guard would pass having checked nothing")
	}

	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		text, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		if declared[text] {
			t.Errorf("%s:%d: the generator contains the notification code %q as a literal — it must read every code from notifications.DeclaredCodes(), never hold a copy",
				generatorSource, fset.Position(lit.Pos()).Line, text)
		}
		return true
	})

	// The comments are source too, and a code pasted into a doc comment is a
	// copy that goes stale just as quietly.
	raw, err := os.ReadFile(generatorSource)
	if err != nil {
		t.Fatalf("reading %s: %v", generatorSource, err)
	}
	for code := range declared {
		if strings.Contains(string(raw), code) {
			t.Errorf("the notification code %q appears in %s (including its comments) — the generator must not carry a copy of any code, not even as an example",
				code, generatorSource)
		}
	}
}

// TestGenerator_ReadsTheCatalogRatherThanParsingSource pins the mechanism.
// A generator that walked Go source could not resolve a constant declared in
// another package, and would go wrong the first time one moved.
func TestGenerator_ReadsTheCatalogRatherThanParsingSource(t *testing.T) {
	_, file := parseGenerator(t)

	imports := make(map[string]bool)
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Fatalf("unquoting import %s: %v", imp.Path.Value, err)
		}
		imports[path] = true
	}

	const catalogPkg = "github.com/MoranWeissman/sharko/internal/notifications"
	if !imports[catalogPkg] {
		t.Errorf("%s does not import %s — it cannot be reading the real catalog", generatorSource, catalogPkg)
	}
	for _, parserPkg := range []string{"go/ast", "go/parser", "go/token", "go/types"} {
		if imports[parserPkg] {
			t.Errorf("%s imports %s — it must read the catalog as a runtime value, not re-parse Go source", generatorSource, parserPkg)
		}
	}
}

// TestTSKey_Conversion pins the snake_case → camelCase rule with LITERAL
// expectations on both sides, so neither half can drift into agreeing with a
// broken implementation.
func TestTSKey_Conversion(t *testing.T) {
	cases := []struct{ in, want string }{
		{"git_connection_broken", "gitConnectionBroken"},
		{"argocd_auth_failed", "argocdAuthFailed"},
		{"single", "single"},
		{"a_b_c_d", "aBCD"},
		{"code_with_2_digits", "codeWith2Digits"},
	}
	for _, tc := range cases {
		got, err := tsKey(notifications.Code(tc.in))
		if err != nil {
			t.Errorf("tsKey(%q) returned an error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("tsKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestTSKey_RejectsWhatCannotBeAPropertyName proves the validation is not
// decorative — each of these would otherwise produce TypeScript that needs
// quoting or does not compile.
func TestTSKey_RejectsWhatCannotBeAPropertyName(t *testing.T) {
	bad := []string{
		"",                 // no value at all
		"has spaces",       // prose, not an identifier
		"Has_Capitals",     // the wire format is lower_snake_case
		"trailing_",        // empty word
		"_leading",         // empty word
		"has-a-hyphen",     // not snake_case
		"2_starts_a_digit", // not a valid property name
	}
	for _, in := range bad {
		if got, err := tsKey(notifications.Code(in)); err == nil {
			t.Errorf("tsKey(%q) returned %q with no error — that value cannot be a bare TypeScript property name", in, got)
		}
	}
}

// TestRenderTypeScript_OverASyntheticCatalog drives the renderer over a list
// this test owns, so the expected output can be written out in full without
// re-typing anything from production.
func TestRenderTypeScript_OverASyntheticCatalog(t *testing.T) {
	out, err := renderTypeScript([]notifications.Code{"alpha_thing", "beta"})
	if err != nil {
		t.Fatalf("renderTypeScript: %v", err)
	}

	for _, want := range []string{
		"// Code generated by cmd/gen-notification-codes. DO NOT EDIT.\n",
		"export const NOTIFICATION_CODES = {\n",
		"  alphaThing: \"alpha_thing\",\n",
		"  beta: \"beta\",\n",
		"} as const\n",
		"export type NotificationCode = (typeof NOTIFICATION_CODES)[keyof typeof NOTIFICATION_CODES]\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the rendered output is missing %q:\n%s", want, out)
		}
	}

	// Declaration order, not alphabetical: re-sorting here would hide a
	// reordering in Go instead of showing it in the diff.
	if strings.Index(out, "alphaThing") > strings.Index(out, "beta:") {
		t.Error("the renderer reordered the catalog — it must emit DeclaredCodes()' own order")
	}
}

// TestRenderTypeScript_RefusesACollision proves two codes that would collapse
// into one TypeScript key fail the generator rather than silently losing one.
func TestRenderTypeScript_RefusesACollision(t *testing.T) {
	if _, err := renderTypeScript([]notifications.Code{"same_thing", "same_thing"}); err == nil {
		t.Error("the renderer accepted a duplicated code — one entry would silently overwrite the other")
	}
}

// TestRun_RefusesAnEmptyCatalog proves the generator will not quietly write an
// empty contract, which would make the browser's every code lookup undefined.
func TestRun_RefusesAnEmptyCatalog(t *testing.T) {
	out := t.TempDir() + "/notification-codes.ts"
	if err := run(nil, out); err == nil {
		t.Error("run accepted an empty catalog — it must refuse rather than write an empty contract")
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("run wrote a file despite refusing the catalog")
	}
}

// TestRun_WritesTheFile covers the write path end to end over a synthetic
// catalog, including creating the output directory.
func TestRun_WritesTheFile(t *testing.T) {
	out := t.TempDir() + "/nested/notification-codes.ts"
	if err := run([]notifications.Code{"alpha_thing"}, out); err != nil {
		t.Fatalf("run: %v", err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading the generated file: %v", err)
	}
	if !strings.Contains(string(body), "alphaThing: \"alpha_thing\"") {
		t.Errorf("the generated file does not contain the rendered entry:\n%s", body)
	}
}

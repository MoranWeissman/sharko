package main

// main_test.go — the generator reads the real catalog and holds no second copy
// of it, plus the rendering rules the output format depends on.
//
// Same shape as cmd/gen-notification-codes/main_test.go, for the same reason.
// The risk being closed is a generator carrying its own hardcoded list of
// names: it would produce a browser file that looks generated, carries a
// generator's authority, and is wrong the moment Go declares a new event. So
// that is proved structurally rather than by anyone remembering the rule — no
// string literal anywhere in main.go is a declared event name.
//
// Note what is deliberately NOT here: any assertion of the shape
// `Declared()[0] == ClusterSecretCreate`. That compares a symbol with itself
// and is green forever while proving nothing. The wire values are pinned as
// LITERALS in internal/lifecycleevents/events_test.go.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/lifecycleevents"
)

const generatorSource = "main.go"

func parseGenerator(t *testing.T) *ast.File {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, generatorSource, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing %s: %v", generatorSource, err)
	}
	return file
}

// TestGenerator_HoldsNoCopyOfAnyEvent is the structural guard: not one of the
// names this generator emits may appear as a string literal inside it. If
// somebody ever "helpfully" inlines the list, this fails by name.
func TestGenerator_HoldsNoCopyOfAnyEvent(t *testing.T) {
	file := parseGenerator(t)

	declared := make(map[string]bool)
	for _, e := range lifecycleevents.Declared() {
		declared[string(e)] = true
	}
	if len(declared) == 0 {
		t.Fatal("no events are declared, so this guard would pass having checked nothing")
	}

	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		if declared[value] {
			t.Errorf(`%s holds a copy of the event name %q.

The whole point of this generator is that there is ONE list of these names and
it is in Go. A literal here is a second list that goes stale silently.`, generatorSource, value)
		}
		return true
	})

	// The comments are checked too. A "for example: cluster_secret_create"
	// in a doc comment is a copy that rots exactly like a literal does, and
	// it is the shape somebody adds while explaining the code.
	for _, group := range file.Comments {
		for _, c := range group.List {
			for name := range declared {
				if strings.Contains(c.Text, name) {
					t.Errorf("a comment in %s quotes the event name %q — that is a second copy of the contract, sitting in prose", generatorSource, name)
				}
			}
		}
	}
}

// TestRun_RefusesAnEmptyCatalog: writing an empty contract would leave the
// browser's feed table with nothing to be checked against, and every drift
// test would pass having compared nothing.
func TestRun_RefusesAnEmptyCatalog(t *testing.T) {
	err := run(nil, t.TempDir()+"/out.ts")
	if err == nil {
		t.Fatal("run accepted an empty catalog — it must refuse rather than write an empty contract")
	}
}

// TestRenderTypeScript_RejectsADuplicate: two identical names would render two
// identical keys, the second silently overwriting the first, and the output
// would be one entry short with nothing saying so.
func TestRenderTypeScript_RejectsADuplicate(t *testing.T) {
	if _, err := renderTypeScript([]lifecycleevents.Event{"aa_bb", "aa_bb"}); err == nil {
		t.Fatal("renderTypeScript accepted a duplicate event name")
	}
}

// TestTsKey_CamelCasesTheWireValue over made-up values, so this test holds no
// copy of the contract either.
func TestTsKey_CamelCasesTheWireValue(t *testing.T) {
	cases := map[string]string{
		"one":            "one",
		"one_two":        "oneTwo",
		"one_two_threes": "oneTwoThrees",
	}
	for in, want := range cases {
		got, err := tsKey(lifecycleevents.Event(in))
		if err != nil {
			t.Fatalf("tsKey(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("tsKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestTsKey_RejectsWhatWouldNotBeAPropertyName — the generator fails rather
// than emitting TypeScript that needs a per-key decision.
func TestTsKey_RejectsWhatWouldNotBeAPropertyName(t *testing.T) {
	for _, bad := range []string{"", "Has_Capitals", "has spaces", "trailing_", "9leading"} {
		if _, err := tsKey(lifecycleevents.Event(bad)); err == nil {
			t.Errorf("tsKey(%q) was accepted — it cannot be a bare property name", bad)
		}
	}
}

// TestRenderTypeScript_IsDeterministic — the CI drift job diffs the output, so
// two runs over one input must be byte-identical or the job fails on noise.
func TestRenderTypeScript_IsDeterministic(t *testing.T) {
	in := lifecycleevents.Declared()
	first, err := renderTypeScript(in)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	second, err := renderTypeScript(in)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if first != second {
		t.Error("two renders of the same catalog differ — the drift job would fail on noise")
	}
}

// TestRenderTypeScript_EmitsEveryDeclaredEvent. Not a comparison of a symbol
// with itself: it asserts that the rendered TEXT contains each value, which is
// what the browser will actually read.
func TestRenderTypeScript_EmitsEveryDeclaredEvent(t *testing.T) {
	out, err := renderTypeScript(lifecycleevents.Declared())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, e := range lifecycleevents.Declared() {
		if !strings.Contains(out, `"`+string(e)+`"`) {
			t.Errorf("the rendered contract does not carry %q", e)
		}
	}
}

package orchestrator

// secret_failure_guard_test.go — the guard behind SafeSecretMessage.
//
// # Why there is a guard at all when the type already stops this
//
// SafeSecretMessage has an unexported field, so no package OUTSIDE
// internal/orchestrator can build one — that part needs no guard, the compiler
// holds it. What the type does not stop is a future edit INSIDE this package:
// `SafeSecretMessage{sentence: err.Error()}` compiles fine here.
//
// So the guard covers exactly the gap the type leaves, and nothing more:
// SafeSecretMessage literals may only be written in secret_failure.go, where
// the catalog is. Everywhere else in the package has to go through the
// constructors, which take a code and cannot be handed raw text.
//
// # What it deliberately does NOT reach
//
//   - It does not check the whole package for raw error text. internal/orchestrator
//     has many legitimate `.Error()` calls on Git and Kubernetes paths, and
//     banning them here would be a different, much larger story.
//     RegisterClusterResult.Error, RemoveClusterResult.Error and
//     AdoptClusterResult.Error all still carry raw Git/Kubernetes text — that
//     is REPORTED, not fixed, and this guard makes no claim about them.
//   - It does not look at internal/api or the UI.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// safeSecretMessageHome is the ONE file allowed to build a SafeSecretMessage.
const safeSecretMessageHome = "secret_failure.go"

// wantConstructors is the ANCHOR — a LIST, not a count. If the walk stops
// finding these, the guard is covering nothing and names the one it lost
// rather than passing over an empty tree.
var wantConstructors = []string{
	"newSecretFetchFailure",
	"newSecretWriteFailure",
	"secretFailureMessage",
}

// TestSafeSecretMessage_OnlyBuiltInItsHomeFile fails on any SafeSecretMessage
// composite literal outside secret_failure.go.
func TestSafeSecretMessage_OnlyBuiltInItsHomeFile(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}

	type hit struct {
		file string
		line int
	}
	var hits []hit
	foundConstructors := map[string]bool{}
	homeSeen := false
	filesWalked := 0

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		filesWalked++
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, name, nil, 0)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", name, parseErr)
		}
		if name == safeSecretMessageHome {
			homeSeen = true
			for _, decl := range file.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok {
					foundConstructors[fn.Name.Name] = true
				}
			}
			continue // the catalog lives here; building one here is the point
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			ident, ok := lit.Type.(*ast.Ident)
			if !ok || ident.Name != "SafeSecretMessage" {
				return true
			}
			hits = append(hits, hit{file: name, line: fset.Position(lit.Pos()).Line})
			return true
		})
	}

	if filesWalked == 0 {
		t.Fatal("no non-test Go files walked — this guard is covering nothing")
	}
	if !homeSeen {
		t.Fatalf("%s was not found — either the catalog moved or this guard stopped seeing it", safeSecretMessageHome)
	}
	for _, want := range wantConstructors {
		if !foundConstructors[want] {
			t.Errorf("expected the constructor %s in %s — it is gone, so either the safe path was removed or this guard stopped seeing that file",
				want, safeSecretMessageHome)
		}
	}

	for _, h := range hits {
		t.Errorf("%s:%d builds a SafeSecretMessage literal outside %s — the sentence must come from the catalog, so call newSecretFetchFailure or newSecretWriteFailure instead",
			h.file, h.line, safeSecretMessageHome)
	}
}

// TestSecretErrorLiterals_OnlyBuiltByTheConstructors fails on any SecretError
// composite literal outside secret_failure.go.
//
// This is the second half: SafeSecretMessage being unforgeable stops the
// MESSAGE going wrong, but a hand-built SecretError could still set Code and
// Addon inconsistently with the sentence — say a fetch sentence on a write
// failure — which would mislead an operator just as effectively as a leak.
func TestSecretErrorLiterals_OnlyBuiltByTheConstructors(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}

	type hit struct {
		file string
		line int
	}
	var hits []hit

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if name == safeSecretMessageHome {
			continue
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, name, nil, 0)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", name, parseErr)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			ident, ok := lit.Type.(*ast.Ident)
			if !ok || ident.Name != "SecretError" {
				return true
			}
			hits = append(hits, hit{file: name, line: fset.Position(lit.Pos()).Line})
			return true
		})
	}

	for _, h := range hits {
		t.Errorf("%s:%d builds a SecretError literal outside %s — use newSecretFetchFailure or newSecretWriteFailure so the code, the fields and the sentence cannot disagree",
			h.file, h.line, safeSecretMessageHome)
	}
}

// TestSecretFailureCatalog_SentencesAreCompleteAndPointSomewhere pins the
// property that keeps this fix from becoming its own defect: a redaction that
// leaves "something went wrong" has traded one problem for another.
//
// Checked as a LIST over the catalog, so a new sentence added later has to
// meet the same bar.
func TestSecretFailureCatalog_SentencesAreCompleteAndPointSomewhere(t *testing.T) {
	// Words that name a THING TO GO AND LOOK AT. Every sentence must contain
	// at least one, so none of them can collapse into a shrug.
	pointers := []string{"secrets store", "cluster", "namespace", "catalog"}

	for code, sentence := range secretFailureSentences {
		if strings.TrimSpace(sentence) == "" {
			t.Errorf("%s has an empty sentence", code)
			continue
		}
		if !strings.HasSuffix(strings.TrimSpace(sentence), ".") {
			t.Errorf("%s is not a finished sentence: %q", code, sentence)
		}
		if len(sentence) < 60 {
			t.Errorf("%s is too short to say what went wrong AND where to look: %q", code, sentence)
		}
		found := false
		for _, p := range pointers {
			if strings.Contains(sentence, p) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s does not name anything the operator can go and look at (expected one of %v): %q", code, pointers, sentence)
		}
	}
}

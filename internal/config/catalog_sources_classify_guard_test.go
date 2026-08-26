package config

// catalog_sources_classify_guard_test.go — BF16 acceptance item 7: on the
// catalog-source rejection path, classification is by TYPE and by branch,
// never by matching message text. `strings.Contains(err.Error(), ...)` is
// banned here because text-matching an error message quietly turns the
// message's exact words into load-bearing API — and the whole point of BF16
// is that these words changed and may change again.
//
// Guard shape, per the house rules: it WALKS the tree (never a hand-written
// file list), it refuses to pass vacuously (the walk must read the EXACT
// number of shipping Go files this package holds, compared with !=), and it
// fails on growth — a new file in this package is swept automatically, and
// the pinned count forces one person's eyes onto the fact that the package
// grew.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// wantConfigShippingGoFiles is the EXACT number of non-test .go files in
// internal/config. Exact on purpose: a floor with room in it would let the
// walk silently read nothing and agree with any conclusion. Adding or
// removing a shipping file here changes this number; update it by hand so
// the change is seen.
const wantConfigShippingGoFiles = 9

func TestCatalogSourcePath_NoErrorTextClassification(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("could not read the package directory: %v", err)
	}

	var files []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files = append(files, name)
	}

	// Vacuity refusal: the walk must have read exactly the number of
	// shipping files this package holds.
	if len(files) != wantConfigShippingGoFiles {
		t.Fatalf("the walk read %d shipping .go files, want exactly %d — either the package grew/shrank (update the constant, eyes on the diff) or the walk is broken and every conclusion below is void.\nfiles: %v",
			len(files), wantConfigShippingGoFiles, files)
	}

	fset := token.NewFileSet()
	checked := 0
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("could not parse %s: %v", name, err)
		}
		checked++
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "strings" || sel.Sel.Name != "Contains" {
				return true
			}
			// strings.Contains(...) — flag it if ANY argument (at any
			// depth) is a call to something.Error(), i.e. the message
			// text of an error being used as a classifier.
			for _, arg := range call.Args {
				ast.Inspect(arg, func(inner ast.Node) bool {
					innerCall, ok := inner.(*ast.CallExpr)
					if !ok {
						return true
					}
					innerSel, ok := innerCall.Fun.(*ast.SelectorExpr)
					if ok && innerSel.Sel.Name == "Error" && len(innerCall.Args) == 0 {
						t.Errorf("%s: strings.Contains over err.Error() — classification by message text is banned on this path; classify by type or by branch instead",
							fset.Position(innerCall.Pos()))
					}
					return true
				})
			}
			return true
		})
	}

	// Second vacuity refusal: zero files parsed can never pass.
	if checked != wantConfigShippingGoFiles {
		t.Fatalf("parsed %d files, want exactly %d", checked, wantConfigShippingGoFiles)
	}
}

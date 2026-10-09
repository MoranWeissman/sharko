package api

// doctorcheck_guard_test.go — a source-reading guard over one sink:
// doctorCheck.Detail and doctorCheck.Fix.
//
// # Why a guard and not a type
//
// The orchestrator's SecretError got the strong treatment in S2: its message
// field is a SafeSecretMessage with an unexported field, so a backend's words
// cannot be put there by any package. doctorCheck did not get that, and this
// comment says why rather than leaving it looking like an oversight:
// doctorCheck.Detail is a plain string on purpose — most of the sentences on
// it are literals composed inline with the cluster name, and forcing every one
// of the ~40 doctorCheck literals in this package through a constructor would
// be a large refactor of code that is mostly already correct.
//
// So this sink is GUARDED, not made inexpressible. That is a real difference
// and it is stated plainly: a guard can be deleted, a type cannot be
// circumvented.
//
// # What it closes
//
// internal/verify/credsafe_guard_test.go's Guard 2 wrote down that it could
// NOT reach `.Detail` — "without type checking, X.Detail = … cannot be told
// apart from the unrelated Detail fields on doctorCheck and friends". This
// guard closes exactly that hole for doctorCheck, by matching the EXPLICIT
// composite-literal type rather than the field name alone, so there is no
// ambiguity to resolve.
//
// # What it deliberately does NOT reach
//
//   - Only `doctorCheck{...}` composite literals. A `var c doctorCheck; c.Detail = err.Error()`
//     assignment is invisible to it, for the same type-ambiguity reason
//     Guard 2 hit. No such assignment exists today; the anchor below does not
//     prove it never will.
//   - It cannot see through a variable. `Detail: msg` passes without the guard
//     knowing what is in msg. Inlining the expression is what makes it
//     checkable — every fixed site in this file's package does inline it.
//   - It says nothing about any other package, or about the ~271 writeError
//     call sites in internal/api. The repo-wide raw-error backlog item is NOT
//     closed by this file.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// doctorSafeConverters are the functions allowed to turn an error into a
// sentence on a doctorCheck. Every one is a boundary whose only job is to
// produce a safe sentence from a CLASSIFIED error — by type, never by matching
// the error's words.
//
// A LIST, written here, rather than a rule about name shapes: "anything
// starting with safe" would let the next person launder a leak by naming it
// safeThing.
var doctorSafeConverters = []string{
	"credsafe.Sentence",
	"connectionSecretReadDetail",
	"connectionSecretReadFix",
	"argoListFailureSentence",
	"doctorFixForArgoCDError",
	"verify.FriendlyMessage",
	"FriendlyMessage",
	// verify.AssumeRoleHint reads the real AWS message to CHOOSE between
	// hints that are all written in internal/verify/errors.go, and echoes not
	// one character of what it read. Same class as FriendlyMessage, and
	// internal/verify's own guard already pins it by name as a sanctioned
	// reader.
	"verify.AssumeRoleHint",
}

// doctorSafeErrorFields are the error FIELDS that may be read into a
// doctorCheck sentence, written out one by one.
//
// A field read looks exactly like a leak to the walker below — argoErr.Detail
// and argoErr.Error() are both "get words out of an error" — so each allowed
// one has to earn its place here by having the guarantee written down at the
// type.
//
//   - argoErr.Detail: providers.ArgoCDProviderError.Detail is documented
//     PUBLIC and Sharko-authored ("it must only ever hold Sharko's own
//     words — never an underlying AWS or Kubernetes error's text"), and the
//     raw cause lives on a separate Cause field that Error() never returns.
//     So reading .Detail cannot reach a backend's words; reading .Cause
//     would, and .Cause is deliberately NOT on this list.
var doctorSafeErrorFields = []string{
	"argoErr.Detail",
}

// wantDoctorConverterUse is the ANCHOR: these converters MUST still be found
// in use on a doctorCheck. A LIST, not a count — a count answers "did I see
// enough?", which fails with a misleading message the day the file is
// restructured. This names the one that went missing.
//
// If a restructure stops the parser seeing these literals, the guard is
// covering nothing and says so instead of passing quietly.
var wantDoctorConverterUse = []string{
	"credsafe.Sentence",
	"connectionSecretReadDetail",
	"connectionSecretReadFix",
	"argoListFailureSentence",
}

// doctorErrNamedIdent matches an identifier holding an error by convention.
// Same shape internal/verify's guard uses, so the two agree on what "a bare
// error handed to a formatting verb" looks like.
var doctorErrNamedIdent = regexp.MustCompile(`^(err|.*Err)$`)

// TestDoctorCheck_MessagesNeverBuiltFromRawErrors fails on any doctorCheck
// literal whose Detail or Fix is built out of a raw error.
func TestDoctorCheck_MessagesNeverBuiltFromRawErrors(t *testing.T) {
	root := repoRootForDoctorGuard(t)
	pkgDir := filepath.Join(root, "internal", "api")

	type hit struct {
		file, field, snippet string
		line                 int
	}
	var hits []hit
	usedConverters := map[string]bool{}
	literalsSeen := 0

	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("reading %s: %v", pkgDir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(pkgDir, entry.Name())
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", path, parseErr)
		}
		relPath, _ := filepath.Rel(root, path)

		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			ident, ok := lit.Type.(*ast.Ident)
			if !ok || ident.Name != "doctorCheck" {
				return true
			}
			literalsSeen++
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || (key.Name != "Detail" && key.Name != "Fix") {
					continue
				}
				noteConverters(kv.Value, usedConverters)
				if snippet, bad := doctorRawErrorInExpr(kv.Value); bad {
					hits = append(hits, hit{
						file: relPath, field: "doctorCheck." + key.Name, snippet: snippet,
						line: fset.Position(kv.Pos()).Line,
					})
				}
			}
			return true
		})
	}

	// Anchor 1: the guard must actually be looking at something.
	if literalsSeen == 0 {
		t.Fatal("no doctorCheck literals found in internal/api — this guard is covering nothing")
	}
	// Anchor 2: the safe converters must still be in use, named individually.
	for _, want := range wantDoctorConverterUse {
		if !usedConverters[want] {
			t.Errorf("expected %s to still be used on a doctorCheck Detail or Fix — it is gone, so either the safe path was removed or this guard stopped seeing it", want)
		}
	}

	for _, h := range hits {
		t.Errorf("%s:%d sets %s from raw error text (%s) — classify the error and pick a sentence instead; see connectionSecretReadDetail or argoListFailureSentence in this package",
			h.file, h.line, h.field, h.snippet)
	}
}

// noteConverters records which sanctioned converters an expression calls.
func noteConverters(expr ast.Expr, into map[string]bool) {
	ast.Inspect(expr, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := doctorExprText(call.Fun)
		for _, allowed := range doctorSafeConverters {
			if name == allowed {
				into[name] = true
			}
		}
		return true
	})
}

// doctorRawErrorInExpr reports whether expr reaches an error's own words.
//
// A sanctioned converter's subtree is PRUNED rather than special-cased at the
// top, so "reading the catalog: " + credsafe.Sentence(err) is accepted while
// the same concatenation with a bare err in it is not.
func doctorRawErrorInExpr(expr ast.Expr) (string, bool) {
	var snippet string
	var bad bool

	ast.Inspect(expr, func(n ast.Node) bool {
		if n == nil || bad {
			return false
		}
		if call, ok := n.(*ast.CallExpr); ok {
			name := doctorExprText(call.Fun)
			for _, allowed := range doctorSafeConverters {
				if name == allowed {
					return false // sanctioned conversion — prune the subtree
				}
			}
			if fn, ok := call.Fun.(*ast.SelectorExpr); ok {
				if fn.Sel.Name == "Error" && len(call.Args) == 0 {
					snippet, bad = doctorExprText(fn.X)+".Error()", true
					return false
				}
			}
		}
		// A named-safe field read on an error — see doctorSafeErrorFields.
		// Pruned BEFORE the bare-identifier arm below, which would otherwise
		// fire on the argoErr part of argoErr.Detail.
		if sel, ok := n.(*ast.SelectorExpr); ok {
			text := doctorExprText(sel)
			for _, allowed := range doctorSafeErrorFields {
				if text == allowed {
					return false
				}
			}
		}
		if id, ok := n.(*ast.Ident); ok && doctorErrNamedIdent.MatchString(id.Name) {
			snippet, bad = id.Name, true
			return false
		}
		return true
	})
	return snippet, bad
}

func doctorExprText(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return doctorExprText(t.X) + "." + t.Sel.Name
	case *ast.CallExpr:
		return doctorExprText(t.Fun) + "(…)"
	case *ast.ParenExpr:
		return "(" + doctorExprText(t.X) + ")"
	}
	return "<expr>"
}

// repoRootForDoctorGuard walks up from the working directory until it finds
// go.mod.
func repoRootForDoctorGuard(t *testing.T) string {
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

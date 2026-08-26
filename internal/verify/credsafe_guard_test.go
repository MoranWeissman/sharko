package verify

// credsafe_guard_test.go — two guards that read the SOURCE, so they cover
// branches no test happens to drive.
//
// # What they are for
//
// internal/credsafe is the one classifier, and it classifies by TYPE. The way
// a message escapes it is not a missing branch — it is somebody calling
// err.Error() and putting the answer somewhere a person reads. Until now the
// only thing stopping that on the connectivity-check path was a doc comment,
// and one of those comments was actively wrong: FriendlyMessage documented its
// shape as "<raw cause> — <hint>", which is the leak written down as the
// design.
//
// A runtime test could not hold this. Stage 1 has six failure branches and a
// test drives the two or three that are easy to fake; the raw text sat on all
// six. So both guards below parse the tree instead.
//
// # Guard 1: no raw error text on the credential paths
//
// Over the packages listed in credentialPathRoots, a call to .Error() is
// banned outright. The one sanctioned way to read an error's words is
// credsafe.Cause(err).Error(), which credsafe's own doc restricts to a fixed
// lookup table that echoes not one character of what it read —
// verify.ClassifyError and verify.AssumeRoleHint are the two, and they are
// pinned by name below.
//
// This is STRICTLY STRONGER than the "no strings.Contains(err.Error(), …) on
// credential paths" rule that has been on the backlog, for these packages
// only: strings.Contains(err.Error(), …) needs an .Error() call, and there is
// no .Error() call left to give it.
//
// # Guard 2: every verify.Result and verify.Step message, wherever it is built
//
// These guards are the BACKSTOP, not the main defence. The main defence is
// structural: FriendlyMessage takes an ErrorCode, so raw text cannot be
// expressed as an argument, and Result.diagnostic is unexported, so
// encoding/json cannot reach it. Neither of those can be got around by
// forgetting something.
//
// What the types do NOT cover is a caller who never goes near FriendlyMessage
// and just fills the struct in by hand — `verify.Result{ErrorMessage:
// err.Error()}` in some other package compiles fine. Guard 2 walks the WHOLE
// tree for exactly that, so a new producer in a package with nothing to do
// with credentials is still caught.
//
// # WHAT THESE GUARDS DELIBERATELY DO NOT REACH
//
// Written out rather than implied, because a guard that claims more than it
// checks is worse than none.
//
//   - Guard 1 covers ONLY credentialPathRoots. The rest of the tree still has
//     strings.Contains(err.Error(), …) in it — internal/api/catalog_validate.go,
//     addons_write.go, catalog_repo_charts.go and router.go all match on error
//     text. Those are Helm-index, catalog and password-change paths, not
//     credential paths, and this guard does not touch them. The backlog item
//     for a repo-wide ban is NOT closed by this file.
//   - Guard 2 cannot see through a variable. `ErrorMessage: safeMsg` passes
//     without the guard knowing what is in safeMsg. That shape is real and
//     correct today (internal/api/clusters_discover.go builds safeMsg with
//     credsafe.Sentence one line earlier) but the guard is trusting it, not
//     checking it. Inlining the expression is what makes it checkable.
//   - Guard 2 covers assignments to a `.ErrorMessage` field, but NOT to a
//     `.Detail` field: without type checking, `X.Detail = …` cannot be told
//     apart from the unrelated Detail fields on doctorCheck and friends.
//   - Neither guard looks at the UI. If the browser ever builds its own
//     sentence out of an API field, nothing here sees it.

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

// credentialPathRoots are the packages and files Guard 1 covers, relative to
// the repository root. Each is on the path a credential travels: the backends
// that fetch it, the client built out of it, the assume-role call that mints
// it, and the connectivity check that reports on it.
var credentialPathRoots = []string{
	"internal/verify",
	"internal/providers",
	"internal/remoteclient",
	"internal/capabilities/assume_role.go",
}

// sanctionedRead is one credsafe.Cause(err).Error() that MUST still be there.
//
// This is the anchor, and it is a LIST rather than a count on purpose. A count
// answers "did I see enough things?", which is the question that fails with a
// misleading message the day the tree is restructured. A list answers "is THIS
// one still here?", and names the missing one when it is not.
type sanctionedRead struct{ file, fn string }

var wantSanctionedReads = []sanctionedRead{
	{"internal/verify/errors.go", "ClassifyError"},
	{"internal/verify/errors.go", "AssumeRoleHint"},
}

// TestCredentialPaths_NoRawErrorText fails on any .Error() call in the
// credential-path packages whose receiver is not credsafe.Cause(...).
func TestCredentialPaths_NoRawErrorText(t *testing.T) {
	root := repoRootForCredsafeGuard(t)

	type hit struct {
		file, snippet string
		line          int
	}
	var hits []hit
	var found []sanctionedRead

	for _, rel := range credentialPathRoots {
		abs := filepath.Join(root, rel)
		if _, err := os.Stat(abs); err != nil {
			t.Fatalf("guarded path %q does not exist — this guard silently covers less than it claims: %v", rel, err)
		}

		filesInRoot := 0
		walkErr := filepath.Walk(abs, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			filesInRoot++

			fset := token.NewFileSet()
			file, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if parseErr != nil {
				t.Fatalf("parsing %s: %v", path, parseErr)
			}
			relPath, _ := filepath.Rel(root, path)

			// credsafe.go is the classifier itself. Sentence returns
			// err.Error() for a non-credentials error and the notFound
			// wrapper passes its cause's text through — that IS the
			// implementation, and banning it there would ban the fix.
			if relPath == "internal/credsafe/credsafe.go" {
				return nil
			}

			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) != 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Error" {
					return true
				}
				if isCredsafeCall(sel.X, "Cause") {
					found = append(found, sanctionedRead{relPath, enclosingFuncName(file, call.Pos())})
					return true
				}
				hits = append(hits, hit{
					file:    relPath,
					line:    fset.Position(call.Pos()).Line,
					snippet: exprText(sel.X) + ".Error()",
				})
				return true
			})
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walking %s: %v", rel, walkErr)
		}
		if filesInRoot == 0 {
			t.Fatalf("guarded path %q matched no non-test Go files — the guard covers nothing there", rel)
		}
	}

	// The anchor. If a restructure stops the parser seeing these files, this
	// fails naming the read that went missing, instead of passing quietly.
	for _, want := range wantSanctionedReads {
		var ok bool
		for _, got := range found {
			if got.file == want.file && got.fn == want.fn {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("expected the sanctioned classifier read credsafe.Cause(err).Error() in %s, function %s — it is gone, so either the classifier changed or this guard stopped seeing that file",
				want.file, want.fn)
		}
	}

	for _, h := range hits {
		t.Errorf("%s:%d reads an error's words directly with %s — on a credential path the only sanctioned reads are credsafe.Sentence(err) for a message and credsafe.Cause(err).Error() for classification",
			h.file, h.line, h.snippet)
	}
}

// resultMessageProducers are the files that MUST be found to build a
// verify.Result or verify.Step message. The anchor for Guard 2: if the walk
// stops finding these, the guard is covering nothing and says which file it
// lost.
var resultMessageProducers = []string{
	"internal/verify/stage1.go",
	"internal/verify/stage2.go",
	"internal/api/clusters_discover.go",
}

// errNamedIdent matches an identifier that holds an error by convention —
// err, fetchErr, clientErr, credErr. Used to catch an error handed to a
// formatting verb (fmt.Sprintf("…: %v", err)), which leaks exactly as much as
// calling .Error() on it.
var errNamedIdent = regexp.MustCompile(`^(err|.*Err)$`)

// TestVerifyResultAndStep_MessagesGoThroughCredsafe walks the whole tree and
// fails on any verify.Result.ErrorMessage or verify.Step.Detail built out of a
// raw error.
func TestVerifyResultAndStep_MessagesGoThroughCredsafe(t *testing.T) {
	root := repoRootForCredsafeGuard(t)

	type hit struct {
		file, field, snippet string
		line                 int
	}
	var hits []hit
	foundProducers := map[string]bool{}

	skipDir := map[string]bool{
		".git": true, "ui": true, "node_modules": true, "_dist": true,
		".worktrees": true, "docs": true, "charts": true, "templates": true,
	}

	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if skipDir[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return nil // not our business; the build gate catches unparseable Go
		}
		relPath, _ := filepath.Rel(root, path)
		inVerifyPkg := file.Name.Name == "verify"

		record := func(field string, value ast.Expr, pos token.Pos) {
			foundProducers[relPath] = true
			if snippet, bad := rawErrorInExpr(value); bad {
				hits = append(hits, hit{
					file: relPath, field: field, snippet: snippet,
					line: fset.Position(pos).Line,
				})
			}
		}

		// checkLit reads the message fields of one verify.Result / verify.Step
		// literal. kind is passed in rather than re-derived, because an
		// element of a []verify.Step literal has NO type of its own.
		checkLit := func(lit *ast.CompositeLit, kind string) {
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				if (kind == "Result" && key.Name == "ErrorMessage") ||
					(kind == "Step" && key.Name == "Detail") {
					record(kind+"."+key.Name, kv.Value, kv.Pos())
				}
			}
		}

		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CompositeLit:
				// A slice or array literal: `[]verify.Step{{Detail: …}}`. Its
				// ELEMENTS carry no type of their own — Type is nil — so
				// without this they were invisible to the guard. That hole was
				// found by a break test: a new call site using the slice shape
				// went unreported while the same field in a named literal was
				// caught.
				if arr, ok := node.Type.(*ast.ArrayType); ok {
					if kind, ok := verifyLitKind(arr.Elt, inVerifyPkg); ok {
						for _, elt := range node.Elts {
							if inner, ok := elt.(*ast.CompositeLit); ok && inner.Type == nil {
								checkLit(inner, kind)
							}
						}
					}
					return true
				}
				kind, ok := verifyLitKind(node.Type, inVerifyPkg)
				if !ok {
					return true
				}
				checkLit(node, kind)
			case *ast.AssignStmt:
				// X.ErrorMessage = <expr>. Field name only — see the "does
				// not reach" note about .Detail assignments.
				for i, lhs := range node.Lhs {
					sel, ok := lhs.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "ErrorMessage" || i >= len(node.Rhs) {
						continue
					}
					record("ErrorMessage", node.Rhs[i], node.Pos())
				}
			}
			return true
		})
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walking the tree: %v", walkErr)
	}

	for _, want := range resultMessageProducers {
		if !foundProducers[want] {
			t.Errorf("expected %s to build a verify.Result/verify.Step message — none found, so this guard has stopped covering it", want)
		}
	}

	for _, h := range hits {
		t.Errorf("%s:%d sets %s from raw error text (%s) — build it with credsafe.Sentence(err) so a credentials-backend error says the fixed safe sentence instead of its own words",
			h.file, h.line, h.field, h.snippet)
	}
}

// verifyLitKind reports whether a composite-literal type is verify.Result or
// verify.Step — written either qualified (outside the package) or bare
// (inside it).
func verifyLitKind(typ ast.Expr, inVerifyPkg bool) (string, bool) {
	switch t := typ.(type) {
	case *ast.SelectorExpr:
		pkg, ok := t.X.(*ast.Ident)
		if ok && pkg.Name == "verify" && (t.Sel.Name == "Result" || t.Sel.Name == "Step") {
			return t.Sel.Name, true
		}
	case *ast.Ident:
		if inVerifyPkg && (t.Name == "Result" || t.Name == "Step") {
			return t.Name, true
		}
	}
	return "", false
}

// sanctionedConverters are the functions allowed to turn an error into a
// string a person reads. Every one of them is a boundary whose whole job is to
// produce a safe sentence:
//
//   - credsafe.Sentence — the fixed safe sentence for a credentials-backend
//     error, the error's own text otherwise.
//   - FriendlyMessage — takes only an ErrorCode, so raw text cannot reach it.
//   - safeDetail — one line, calls FriendlyMessage(ClassifyError(err)).
//
// A LIST, written here, rather than a rule about name shapes: "any function
// starting with safe" would let the next person launder a leak by naming it
// safeThing.
var sanctionedConverters = []string{
	"credsafe.Sentence",
	"verify.FriendlyMessage",
	"FriendlyMessage",
	"safeDetail",
}

// isSanctionedConverter reports whether call is one of the converters above.
func isSanctionedConverter(call *ast.CallExpr) bool {
	name := exprText(call.Fun)
	for _, allowed := range sanctionedConverters {
		if name == allowed {
			return true
		}
	}
	return false
}

// rawErrorInExpr reports whether expr reaches an error's own words.
//
// A sanctioned converter's subtree is PRUNED rather than special-cased at the
// top, so a concatenation that mixes a Sharko-written prefix with a properly
// converted error — "building client: " + credsafe.Sentence(err) — is
// accepted, while the same concatenation with a bare err in it is not.
func rawErrorInExpr(expr ast.Expr) (string, bool) {
	var snippet string
	var bad bool

	var walk func(ast.Node) bool
	walk = func(n ast.Node) bool {
		if n == nil || bad {
			return false
		}
		if call, ok := n.(*ast.CallExpr); ok {
			if isSanctionedConverter(call) {
				return false // sanctioned conversion — prune the whole subtree
			}
			if fn, ok := call.Fun.(*ast.SelectorExpr); ok {
				if fn.Sel.Name == "Error" && len(call.Args) == 0 {
					snippet, bad = exprText(fn.X)+".Error()", true
					return false
				}
			}
		}
		if id, ok := n.(*ast.Ident); ok && errNamedIdent.MatchString(id.Name) {
			snippet, bad = id.Name, true
			return false
		}
		return true
	}
	ast.Inspect(expr, walk)
	return snippet, bad
}

// isCredsafeCall reports whether e is a call to credsafe.<name>(...).
func isCredsafeCall(e ast.Expr, name string) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "credsafe"
}

// enclosingFuncName returns the name of the function declaration containing
// pos, or "" when pos is not inside one.
func enclosingFuncName(file *ast.File, pos token.Pos) string {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fn.Pos() <= pos && pos <= fn.End() {
			return fn.Name.Name
		}
	}
	return ""
}

// exprText renders a small expression for an error message. Only the shapes
// that show up as an .Error() receiver need to read well.
func exprText(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return exprText(t.X) + "." + t.Sel.Name
	case *ast.CallExpr:
		return exprText(t.Fun) + "(…)"
	case *ast.ParenExpr:
		return "(" + exprText(t.X) + ")"
	}
	return "<expr>"
}

// repoRootForCredsafeGuard walks up from the working directory until it finds
// go.mod.
func repoRootForCredsafeGuard(t *testing.T) string {
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

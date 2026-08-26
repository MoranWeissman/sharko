package argocd

// write_refused_guard_test.go — the guards that read the SOURCE, so they cover
// branches no runtime test happens to drive (BF6).
//
// # Why source guards and not more runtime tests
//
// The way ArgoCD's reply gets back out is not a missing branch. It is somebody
// adding a `body` argument to an error a year from now, in a method that did
// not exist when this was written, on a status code nobody thought to fake. A
// runtime test can only cover the calls it makes.
//
// So there are five guards, and each one is a LIST rather than a count. A
// count answers "did I see enough things?", which still passes when the N+1st
// unsafe thing appears. A list answers "is THIS one still here, and is there
// anything here that is not on it?", and it fails in both directions.
//
// # WHAT THESE GUARDS DELIBERATELY DO NOT REACH
//
// Written out rather than implied, because a guard that claims more than it
// checks is worse than none.
//
//   - Guard 1 follows an identifier assigned from io.ReadAll within the
//     function that assigned it. It cannot follow that value into another
//     function, or through a struct field. What closes that class instead is
//     Guard 2: there is no field on the error type for a body to be put in.
//   - Guard 3 covers three roots, not the tree. internal/api still matches on
//     error text on the Helm-index, catalog and password-change paths
//     (catalog_validate.go, addons_write.go, catalog_repo_charts.go,
//     router.go, connectivity_status.go). Those are not ArgoCD write paths and
//     this guard does not touch them. A repo-wide ban is NOT closed here.
//   - Guard 5 resolves calls through the type checker, so it sees a call
//     through an interface only when that interface's method has the same
//     signature as the one on *argocd.Client. A wrapper that changed the
//     signature — say, one that swallowed the error and returned a bool —
//     would be invisible to it, and it would also have nothing left to leak.
//   - None of them look at the browser. If the UI ever builds its own sentence
//     out of an API field, nothing here sees it.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// --- shared helpers ---------------------------------------------------------

// repoRootForWriteGuard walks up from the working directory until it finds
// go.mod.
func repoRootForWriteGuard(t *testing.T) string {
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

// walkShippingGoFiles calls fn for every non-test .go file under rel, and
// fails when rel does not exist or holds no shipping Go file at all. A walk
// that reads nothing agrees with any list, including an empty one.
func walkShippingGoFiles(t *testing.T, root, rel string, fn func(relPath string, fset *token.FileSet, file *ast.File)) {
	t.Helper()
	abs := filepath.Join(root, rel)
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("guarded path %q does not exist — this guard silently covers less than it claims: %v", rel, err)
	}
	read := 0
	walkErr := filepath.Walk(abs, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		read++
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", path, parseErr)
		}
		relPath, _ := filepath.Rel(root, path)
		fn(filepath.ToSlash(relPath), fset, file)
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walking %s: %v", rel, walkErr)
	}
	if read == 0 {
		t.Fatalf("guarded path %q matched no shipping Go files — the guard covers nothing there", rel)
	}
}

// selectorText renders a selector or identifier as the name a person would
// call it by. Used only for messages and for matching package-qualified calls.
func selectorText(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return selectorText(v.X) + "." + v.Sel.Name
	case *ast.CallExpr:
		return selectorText(v.Fun) + "(…)"
	case *ast.ParenExpr:
		return "(" + selectorText(v.X) + ")"
	}
	return "<expr>"
}

// --- Guard 1: a response body reaches no error and no log -------------------

// readAllSite is one place in internal/argocd that reads an HTTP response body
// into a variable. This is the anchor list: if the walk stops seeing these, it
// says which one went missing instead of passing quietly.
type readAllSite struct{ file, fn string }

var wantReadAllSites = []readAllSite{
	{"internal/argocd/client.go", "doGet"},
	{"internal/argocd/client_write.go", "doPost"},
	{"internal/argocd/client_write.go", "doPut"},
	{"internal/argocd/client_write.go", "doDelete"},
}

// bodySinks are the calls a response body must never appear inside. Each one
// ends with the value being printed, wrapped into an error, or handed to the
// log — the three ways it has actually escaped.
var bodySinks = []string{
	"fmt.Errorf", "fmt.Sprintf", "fmt.Sprint", "fmt.Sprintln",
	"errors.New",
	"slog.Error", "slog.Warn", "slog.Info", "slog.Debug", "slog.Log",
}

// TestArgocdClient_ResponseBodyNeverReachesAnErrorOrALog is Guard 1.
//
// It finds every variable in internal/argocd assigned from io.ReadAll and
// fails when that variable appears anywhere inside one of the sinks above, in
// the same function.
func TestArgocdClient_ResponseBodyNeverReachesAnErrorOrALog(t *testing.T) {
	root := repoRootForWriteGuard(t)

	type hit struct {
		file, name, sink string
		line             int
	}
	var hits []hit
	var found []readAllSite

	walkShippingGoFiles(t, root, "internal/argocd", func(relPath string, fset *token.FileSet, file *ast.File) {
		for _, decl := range file.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			if !isFunc || fn.Body == nil {
				continue
			}

			// Which identifiers in this function hold a response body?
			bodyNames := map[string]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				assign, isAssign := n.(*ast.AssignStmt)
				if !isAssign {
					return true
				}
				for _, rhs := range assign.Rhs {
					call, isCall := rhs.(*ast.CallExpr)
					if !isCall || selectorText(call.Fun) != "io.ReadAll" {
						continue
					}
					for _, lhs := range assign.Lhs {
						if id, isIdent := lhs.(*ast.Ident); isIdent && id.Name != "_" && id.Name != "err" {
							bodyNames[id.Name] = true
						}
					}
				}
				return true
			})
			if len(bodyNames) == 0 {
				continue
			}
			found = append(found, readAllSite{relPath, fn.Name.Name})

			// Does any of them reach a sink?
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, isCall := n.(*ast.CallExpr)
				if !isCall {
					return true
				}
				name := selectorText(call.Fun)
				isSink := false
				for _, sink := range bodySinks {
					if name == sink {
						isSink = true
						break
					}
				}
				if !isSink {
					return true
				}
				for _, arg := range call.Args {
					ast.Inspect(arg, func(inner ast.Node) bool {
						id, isIdent := inner.(*ast.Ident)
						if isIdent && bodyNames[id.Name] {
							hits = append(hits, hit{
								file: relPath, name: id.Name, sink: name,
								line: fset.Position(id.Pos()).Line,
							})
						}
						return true
					})
				}
				return true
			})
		}
	})

	for _, want := range wantReadAllSites {
		seen := false
		for _, got := range found {
			if got == want {
				seen = true
				break
			}
		}
		if !seen {
			t.Errorf("expected %s, function %s, to read an HTTP response body with io.ReadAll — it no longer does, so either the client changed or this guard stopped seeing that file",
				want.file, want.fn)
		}
	}
	for _, got := range found {
		listed := false
		for _, want := range wantReadAllSites {
			if got == want {
				listed = true
				break
			}
		}
		if !listed {
			t.Errorf("%s, function %s, reads an HTTP response body and is not on this guard's list — add it, having first checked the body cannot reach an error or a log from there",
				got.file, got.fn)
		}
	}
	for _, h := range hits {
		t.Errorf("%s:%d puts the ArgoCD response body (%s) inside %s. ArgoCD quotes the repository address it was working on inside its replies, token and all — the body is dropped, never printed, never truncated, never masked.",
			h.file, h.line, h.name, h.sink)
	}
}

// --- Guard 2: the error type has nowhere to put a body ----------------------

// wantWriteRefusedFields is the EXACT field list of WriteRefusedError. Every
// one of them is a value Sharko itself produced. The guard fails if a field is
// added, removed or renamed, which is what a `Body`, a `Message`, a `Detail`
// or a `Raw` would have to do to get in.
var wantWriteRefusedFields = []string{"Verb", "Endpoint", "Status", "Code"}

// TestWriteRefusedError_HasNowhereToPutAResponseBody is Guard 2.
func TestWriteRefusedError_HasNowhereToPutAResponseBody(t *testing.T) {
	root := repoRootForWriteGuard(t)

	var got []string
	var gotTypes []string
	seenType := false
	var writeCallErrorParams []string
	seenWriteCallError := false

	walkShippingGoFiles(t, root, "internal/argocd", func(relPath string, fset *token.FileSet, file *ast.File) {
		ast.Inspect(file, func(n ast.Node) bool {
			if spec, isSpec := n.(*ast.TypeSpec); isSpec && spec.Name.Name == "WriteRefusedError" {
				structType, isStruct := spec.Type.(*ast.StructType)
				if !isStruct {
					t.Fatalf("WriteRefusedError is not a struct any more, in %s", relPath)
				}
				seenType = true
				for _, field := range structType.Fields.List {
					for _, name := range field.Names {
						got = append(got, name.Name)
						gotTypes = append(gotTypes, selectorText(field.Type))
					}
					if len(field.Names) == 0 {
						got = append(got, "<embedded "+selectorText(field.Type)+">")
						gotTypes = append(gotTypes, selectorText(field.Type))
					}
				}
			}
			if fn, isFunc := n.(*ast.FuncDecl); isFunc && fn.Name.Name == "writeCallError" && fn.Recv == nil {
				seenWriteCallError = true
				for _, param := range fn.Type.Params.List {
					for range param.Names {
						writeCallErrorParams = append(writeCallErrorParams, exprTypeText(param.Type))
					}
				}
			}
			return true
		})
	})

	if !seenType {
		t.Fatal("the walk never found the WriteRefusedError type — this guard checked nothing")
	}
	if !seenWriteCallError {
		t.Fatal("the walk never found writeCallError — this guard checked nothing")
	}

	sortedGot := append([]string(nil), got...)
	sort.Strings(sortedGot)
	sortedWant := append([]string(nil), wantWriteRefusedFields...)
	sort.Strings(sortedWant)
	if strings.Join(sortedGot, ",") != strings.Join(sortedWant, ",") {
		t.Errorf("WriteRefusedError's fields are %v; this guard expects exactly %v.\n\nA new field is how ArgoCD's own reply gets back in wearing a helper's name. If the new field really is Sharko's own value, add it here deliberately.",
			got, wantWriteRefusedFields)
	}
	for i, typeName := range gotTypes {
		if strings.Contains(typeName, "byte") || strings.Contains(typeName, "[]") {
			t.Errorf("WriteRefusedError.%s has type %s — a byte slice or a slice on this type is a response body with a different name", got[i], typeName)
		}
	}
	for _, paramType := range writeCallErrorParams {
		if strings.Contains(paramType, "[]byte") {
			t.Errorf("writeCallError takes a %s parameter again. There must be no parameter for a caller to hand the response body to.", paramType)
		}
	}
}

// exprTypeText renders a type expression well enough to spot a byte slice.
func exprTypeText(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.ArrayType:
		return "[]" + exprTypeText(v.Elt)
	case *ast.StarExpr:
		return "*" + exprTypeText(v.X)
	case *ast.SelectorExpr:
		return selectorText(v.X) + "." + v.Sel.Name
	}
	return "<type>"
}

// --- Guard 3: no control flow decided by reading an error's words -----------

// argocdWritePathRoots are the paths where a decision must never be made by
// reading an error's text. Each is on the path an ArgoCD write error travels
// out of the client and into a decision.
var argocdWritePathRoots = []string{
	"internal/argocd",
	"internal/remediation",
	"internal/api/clusters_restart_sync.go",
}

// stringMatchers are the strings functions that, applied to an error's words,
// turn provider prose into a branch.
var stringMatchers = map[string]bool{
	"Contains": true, "ContainsAny": true, "HasPrefix": true, "HasSuffix": true,
	"EqualFold": true, "Index": true, "LastIndex": true, "ToLower": true,
	"ToUpper": true, "Split": true, "TrimPrefix": true, "TrimSuffix": true,
	"Fields": true, "Count": true,
}

// TestArgocdWritePaths_NoDecisionsFromProviderProse is Guard 3.
func TestArgocdWritePaths_NoDecisionsFromProviderProse(t *testing.T) {
	root := repoRootForWriteGuard(t)

	type hit struct {
		file, snippet string
		line          int
	}
	var hits []hit

	for _, rel := range argocdWritePathRoots {
		walkShippingGoFiles(t, root, rel, func(relPath string, fset *token.FileSet, file *ast.File) {
			ast.Inspect(file, func(n ast.Node) bool {
				call, isCall := n.(*ast.CallExpr)
				if !isCall {
					return true
				}
				sel, isSel := call.Fun.(*ast.SelectorExpr)
				if !isSel {
					return true
				}
				pkg, isIdent := sel.X.(*ast.Ident)
				if !isIdent || pkg.Name != "strings" || !stringMatchers[sel.Sel.Name] {
					return true
				}
				for _, arg := range call.Args {
					if containsErrorCall(arg) {
						hits = append(hits, hit{
							file:    relPath,
							line:    fset.Position(call.Pos()).Line,
							snippet: "strings." + sel.Sel.Name + "(… " + selectorText(arg) + " …)",
						})
					}
				}
				return true
			})
		})
	}

	for _, h := range hits {
		t.Errorf("%s:%d decides something by reading an error's words (%s). On an ArgoCD write path the answer comes from a type — errors.Is against argocd.ErrNoOperationInProgress, ErrTokenInvalid or ErrPermissionDenied, or errors.As to *argocd.WriteRefusedError.",
			h.file, h.line, h.snippet)
	}
}

// containsErrorCall reports whether expr reaches an error's own words through
// a zero-argument .Error() call anywhere inside it.
func containsErrorCall(expr ast.Expr) bool {
	reaches := false
	ast.Inspect(expr, func(n ast.Node) bool {
		call, isCall := n.(*ast.CallExpr)
		if !isCall || len(call.Args) != 0 {
			return true
		}
		if sel, isSel := call.Fun.(*ast.SelectorExpr); isSel && sel.Sel.Name == "Error" {
			reaches = true
			return false
		}
		return true
	})
	return reaches
}

// --- Guard 4: the retired wording stays retired -----------------------------

// retiredWordings are the exact strings the old code produced and matched on.
// Banning the OLD wording as well as requiring the new one is what stops a
// revert coming back quietly: a fix whose only test says the new sentence is
// present would still pass with both in the tree.
var retiredWordings = []string{
	"unexpected status %d from",
	"unexpected status 400",
	"no operation is in progress",
	"No operation is in progress",
}

// TestRetiredWordings_AreGoneFromShippingCode is Guard 4. It walks internal/,
// cmd/ and tests/, so a copy of the old matcher cannot reappear in a package
// nobody thought to look at.
//
// It reads STRING LITERALS, not raw file text. A comment that records what the
// old wording was — this file has several — is documentation and is welcome; a
// literal is the thing that gets printed or matched on.
func TestRetiredWordings_AreGoneFromShippingCode(t *testing.T) {
	root := repoRootForWriteGuard(t)

	type hit struct {
		file, wording, literal string
		line                   int
	}
	var hits []hit
	filesRead := 0

	for _, rel := range []string{"internal", "cmd", "tests"} {
		walkShippingGoFiles(t, root, rel, func(relPath string, fset *token.FileSet, file *ast.File) {
			filesRead++
			ast.Inspect(file, func(n ast.Node) bool {
				lit, isLit := n.(*ast.BasicLit)
				if !isLit || lit.Kind != token.STRING {
					return true
				}
				for _, wording := range retiredWordings {
					if strings.Contains(lit.Value, wording) {
						hits = append(hits, hit{
							file: relPath, wording: wording, literal: lit.Value,
							line: fset.Position(lit.Pos()).Line,
						})
					}
				}
				return true
			})
		})
	}

	if filesRead == 0 {
		t.Fatal("the walk read no shipping Go files at all — this guard checked nothing")
	}
	for _, h := range hits {
		t.Errorf("%s:%d has a string literal carrying the retired wording %q (%s). That string was either ArgoCD's reply pasted into an error, or Sharko matching on ArgoCD's reply to decide what to do. Both are gone; neither comes back.",
			h.file, h.line, h.wording, h.literal)
	}
}

// TestRetiredWordingGuard_FindsAPlantedLiteral is Guard 4's positive control.
//
// Guard 4 asserts an absence, and an absence is what a matcher looking in the
// wrong place also reports. So a file carrying each retired wording, once in a
// literal and once in a comment, is parsed the same way the guard parses the
// tree: the literal must be found and the comment must not.
func TestRetiredWordingGuard_FindsAPlantedLiteral(t *testing.T) {
	for _, wording := range retiredWordings {
		src := "package p\n\n// a comment saying " + wording + " and nothing else\nvar x = \"" + wording + "\"\n"
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "planted.go", src, parser.ParseComments)
		if err != nil {
			t.Fatalf("parsing the planted source for %q: %v", wording, err)
		}
		inLiteral, inComment := 0, 0
		ast.Inspect(file, func(n ast.Node) bool {
			if lit, isLit := n.(*ast.BasicLit); isLit && lit.Kind == token.STRING &&
				strings.Contains(lit.Value, wording) {
				inLiteral++
			}
			return true
		})
		for _, group := range file.Comments {
			for _, c := range group.List {
				if strings.Contains(c.Text, wording) {
					inComment++
				}
			}
		}
		if inLiteral != 1 {
			t.Errorf("the guard's matcher found %d literals carrying %q, want exactly 1 — it cannot find a wording somebody planted, so its absences prove nothing", inLiteral, wording)
		}
		if inComment != 1 {
			t.Errorf("the planted comment carrying %q was not there, so this control did not test the comment half", wording)
		}
	}
}

// --- Guard 5: exactly these files call an ArgoCD write ----------------------

// argocdWriteMethods are the nine methods a caller can get a
// *WriteRefusedError out of, plus RefreshApplication, which is on the same
// surface and was audited with them.
var argocdWriteMethods = map[string]bool{
	"TerminateOperation":  true,
	"SyncApplication":     true,
	"RefreshApplication":  true,
	"RegisterCluster":     true,
	"DeleteCluster":       true,
	"UpdateClusterLabels": true,
	"CreateProject":       true,
	"CreateApplication":   true,
	"AddRepository":       true,
}

// wantWriteCallers is THE LIST: every shipping file that calls one of those
// methods, on *argocd.Client or through an interface with the same signature.
//
// It was produced by the walk below, not typed from memory, and then every
// entry was opened and read. It fails in both directions — a new caller that
// is not here, and an entry that no longer calls anything — so a consumer
// cannot be added without somebody looking at what it shows a person.
var wantWriteCallers = []string{
	"internal/ai/tools_write.go",
	"internal/api/clusters_orphan_delete.go",
	"internal/api/clusters_restart_sync.go",
	"internal/api/init.go",
	"internal/orchestrator/cluster.go",
	"internal/orchestrator/init.go",
	"internal/orchestrator/remove.go",
	"internal/remediation/remediation.go",
}

// sinkPackagesThatMustNotSeeAWriteError are the two sinks the contract asks
// about that this error must never reach: Kubernetes events and the
// notification store. Neither is on the list above, and this asserts it
// mechanically rather than by reading the list by eye.
var sinkPackagesThatMustNotSeeAWriteError = []string{
	"internal/clusterreconciler/",
	"internal/notifications/",
	"internal/events/",
	"internal/audit/",
}

// TestArgocdWriteCallers_AreExactlyTheAuditedList is Guard 5.
func TestArgocdWriteCallers_AreExactlyTheAuditedList(t *testing.T) {
	root := repoRootForWriteGuard(t)

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
			packages.NeedSyntax | packages.NeedTypesInfo,
		Dir: root,
	}
	pkgs, loadErr := packages.Load(cfg, "./internal/...", "./cmd/...", "./tests/...")
	if loadErr != nil {
		t.Fatalf("type-checking the tree: %v", loadErr)
	}
	if len(pkgs) == 0 {
		t.Fatal("the type checker returned no packages, so this guard covered nothing")
	}

	// The method set on *argocd.Client, to compare interface methods against.
	clientMethods := map[string]*types.Signature{}
	for _, pkg := range pkgs {
		if pkg.PkgPath != "github.com/MoranWeissman/sharko/internal/argocd" {
			continue
		}
		obj := pkg.Types.Scope().Lookup("Client")
		if obj == nil {
			t.Fatal("internal/argocd has no Client type — this guard cannot resolve anything")
		}
		named, isNamed := obj.Type().(*types.Named)
		if !isNamed {
			t.Fatal("argocd.Client is not a named type")
		}
		for i := 0; i < named.NumMethods(); i++ {
			m := named.Method(i)
			if sig, isSig := m.Type().(*types.Signature); isSig {
				clientMethods[m.Name()] = sig
			}
		}
	}
	if len(clientMethods) == 0 {
		t.Fatal("no methods were resolved on argocd.Client — this guard covered nothing")
	}
	for name := range argocdWriteMethods {
		if clientMethods[name] == nil {
			t.Fatalf("argocd.Client has no method %q any more — this guard's method list is stale and it is silently covering less", name)
		}
	}

	callers := map[string]bool{}
	filesRead := 0
	for _, pkg := range pkgs {
		for i, syntax := range pkg.Syntax {
			if i >= len(pkg.CompiledGoFiles) {
				continue
			}
			path := pkg.CompiledGoFiles[i]
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			if !strings.HasPrefix(rel, "internal/") && !strings.HasPrefix(rel, "cmd/") && !strings.HasPrefix(rel, "tests/") {
				continue
			}
			// The ArgoCD package's own calls are the implementation, not a
			// consumer of it.
			if strings.HasPrefix(rel, "internal/argocd/") {
				continue
			}
			filesRead++

			ast.Inspect(syntax, func(n ast.Node) bool {
				call, isCall := n.(*ast.CallExpr)
				if !isCall {
					return true
				}
				sel, isSel := call.Fun.(*ast.SelectorExpr)
				if !isSel || !argocdWriteMethods[sel.Sel.Name] {
					return true
				}
				fn, isFn := pkg.TypesInfo.ObjectOf(sel.Sel).(*types.Func)
				if !isFn {
					return true
				}
				sig, isSig := fn.Type().(*types.Signature)
				if !isSig || sig.Recv() == nil {
					return true
				}
				want := clientMethods[sel.Sel.Name]
				if want == nil {
					return true
				}
				// Same name AND the same signature as the method on
				// *argocd.Client — either the concrete method itself, or an
				// interface that stands in for it.
				if types.Identical(stripRecv(sig), stripRecv(want)) {
					callers[rel] = true
				}
				return true
			})
		}
	}

	if filesRead == 0 {
		t.Fatal("the walk read no shipping Go files outside internal/argocd — this guard covered nothing")
	}

	want := map[string]bool{}
	for _, f := range wantWriteCallers {
		want[f] = true
	}
	var got []string
	for f := range callers {
		got = append(got, f)
	}
	sort.Strings(got)

	for _, f := range got {
		if !want[f] {
			t.Errorf("%s calls an ArgoCD write method and is not on this guard's list. Add it, having first read what it shows a person when that call fails.", f)
		}
	}
	for _, f := range wantWriteCallers {
		if !callers[f] {
			t.Errorf("%s is listed as an ArgoCD write caller and no longer calls one — a stale entry means this guard is covering less than it says.", f)
		}
	}

	for _, sink := range sinkPackagesThatMustNotSeeAWriteError {
		for _, f := range got {
			if strings.HasPrefix(f, sink) {
				t.Errorf("%s is in %s, which writes Kubernetes events, notifications or audit records straight from what it is given. An ArgoCD write error must not be handed to it without a boundary in between.", f, sink)
			}
		}
	}
}

// stripRecv returns sig with its receiver removed, so an interface method and
// a concrete method can be compared on their parameters and results alone.
func stripRecv(sig *types.Signature) *types.Signature {
	return types.NewSignatureType(nil, nil, nil, sig.Params(), sig.Results(), sig.Variadic())
}

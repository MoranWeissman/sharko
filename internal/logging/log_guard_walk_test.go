package logging

// log_guard_walk_test.go — how the log guard finds a log line, and what it
// counts as something dangerous being handed to one (B16).
//
// # What changed here and why
//
// The walk used to be pure syntax. It recognised a log call only when the
// receiver was written as one of five bare names —
//
//	slog.Warn(...)   logger.Warn(...)   log.Warn(...)   l.Warn(...)   lg.Warn(...)
//
// — so the same line written through a chained logger,
//
//	slog.Default().With("component", "x").Warn(...)
//
// was not a log line as far as the guard was concerned. And it decided what
// counted as a dangerous value by the NAME of the variable: anything ending in
// "err"/"error", plus a hardcoded list of six payload names (body, respBody,
// payload, output, stdout, stderr). A response body in a variable called
// anything else was invisible; so was an error in a variable called `problem`.
//
// Both of those are the same mistake — reading the spelling instead of the
// thing. The walk now resolves types:
//
//   - A log call is a call to a function or method that BELONGS to the
//     log/slog package. That is true of slog.Warn, of a *slog.Logger held in a
//     variable under any name, of one held in a struct field, and of one
//     produced by a chain of calls. It is false for http.Error and for
//     err.Error(), which the old name list had to dodge by hand.
//
//   - A carrier is decided by the value's TYPE, never by its name. A value
//     whose type implements `error` is an error value. A value whose type is
//     []byte or []string is an opaque payload. `string(x)` of a non-string,
//     `fmt.Sprint*` and a zero-argument `.Error()` are flattening calls, which
//     is a fact about the expression, not about any name in it.
//
// # What it still cannot see, said plainly
//
// A credential laundered into a plain `string` variable first —
//
//	msg := err.Error()
//	slog.Error("...", "detail", msg)
//
// — still gets past the walk at the second line, because by then the value's
// type is `string` and a string carries no information about where it came
// from. `string` is the type of a cluster name too. That class is not left
// open, but it is not closed HERE: RedactHandler closes it at the sink, by
// structure rather than by name — a sensitive key, a JWT shape, a long base64
// blob, an error VALUE, or a URL that net/url says has a userinfo section
// (which is what a chart repository address with a token in it is). See
// redact.go. TestRedactedSinkStripsACredentialURLUnderAnUnlistedKey in
// redact_url_test.go is the standing proof of the last one.

import (
	"go/ast"
	"go/types"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// logGuardSweepDirs are the Go directories walked. Everything in the tree that
// compiles into a Sharko binary or a Sharko test harness.
var logGuardSweepDirs = []string{"internal", "cmd", "tests"}

// logGuardLevels are the slog emit methods. A call to any of them is a log
// line leaving the process. The name alone is not enough to make it a log
// call — the function it resolves to must belong to log/slog.
var logGuardLevels = map[string]bool{
	"Debug": true, "Info": true, "Warn": true, "Error": true,
	"DebugContext": true, "InfoContext": true, "WarnContext": true, "ErrorContext": true,
	"Log": true, "LogAttrs": true,
}

// carrier kinds. The verdict rules turn on these.
const (
	// carrierErrValue: an error VALUE handed to slog whole. The sink sees a
	// type and replaces the words with credsafe.LogClass.
	carrierErrValue = "errvalue"
	// carrierRawString: text that reaches slog already flattened, or an
	// opaque payload ([]byte, []string). The sink cannot classify by type,
	// so nothing downstream can help.
	carrierRawString = "rawstring"
	// carrierSafeCall: the value went through internal/credsafe first, so it
	// is already one of Sharko's own fixed sentences.
	carrierSafeCall = "safecall"
)

// logGuardSite is one entry in the list.
type logGuardSite struct {
	file     string
	fn       string
	msg      string // the log message literal, "" when it is not a literal
	carriers string // sorted "kind:detail" pairs, exactly as the tree shows
	verdict  string // "sink" or "safe"
	reason   string // required when verdict is "safe"
}

// discoveredLogSite is what the walk finds for one slog call.
type discoveredLogSite struct {
	carriers map[string]bool
	kinds    map[string]bool
}

// errorInterface is the universe's `error`, used to ask a type whether it is
// one. This is the whole replacement for the old "does the name end in err"
// heuristic.
func errorInterface() *types.Interface {
	return types.Universe.Lookup("error").Type().Underlying().(*types.Interface)
}

func implementsError(t types.Type) bool {
	if t == nil || t == types.Typ[types.Invalid] {
		return false
	}
	if _, isTuple := t.(*types.Tuple); isTuple {
		return false
	}
	return types.Implements(t, errorInterface())
}

// isOpaquePayload reports whether a type is a bag of bytes or a bag of
// strings — a response body, a subprocess's output, a library's list of
// messages. Whatever is inside came from somewhere else, and the sink cannot
// classify it, so a line carrying one needs a written reason.
func isOpaquePayload(t types.Type) bool {
	if t == nil || t == types.Typ[types.Invalid] {
		return false
	}
	slice, isSlice := t.Underlying().(*types.Slice)
	if !isSlice {
		return false
	}
	basic, isBasic := slice.Elem().Underlying().(*types.Basic)
	if !isBasic {
		return false
	}
	return basic.Kind() == types.Byte || basic.Kind() == types.String
}

// isSlogCall reports whether a selector call is a log line leaving the
// process, and it asks the type checker rather than the source.
//
// It is true for the package functions (slog.Warn) and for the methods on
// *slog.Logger, however the logger was obtained — a bare name, a struct
// field, the result of Default().With(...). It is false for http.Error and
// for a plain err.Error(), which is what the old receiver-name list was
// really working around.
func isSlogCall(info *types.Info, sel *ast.SelectorExpr) bool {
	if !logGuardLevels[sel.Sel.Name] {
		return false
	}
	fn, isFn := info.ObjectOf(sel.Sel).(*types.Func)
	if !isFn || fn.Pkg() == nil {
		return false
	}
	return fn.Pkg().Path() == "log/slog"
}

// collectCarriers walks one slog argument expression and records every raw
// carrier in it, by type.
//
// It stops descending at a credsafe call, at a `.Error()` call and at a
// string conversion: each is a terminal decision, and descending past one
// would report the inner value twice under two different verdicts. It also
// stops at len() and cap(), which cannot carry the value — only its size.
func collectCarriers(info *types.Info, expr ast.Expr, site *discoveredLogSite) {
	ast.Inspect(expr, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			return collectFromCall(info, node, site)
		case *ast.SelectorExpr:
			if isPackageQualifier(info, node.X) {
				// pkg.Something — the qualifier is not a value.
				return !classifyValue(info.TypeOf(node), exprLabel(node), site)
			}
			if classifyValue(info.TypeOf(node), exprLabel(node), site) {
				return false
			}
			// The FIELD is what is being logged, not the struct it came out
			// of, so a non-carrier field ends the descent here. Reading the
			// receiver as a carrier is how `failure.Kind` used to report the
			// whole *ValidationFailure as an error value.
			return false
		case *ast.Ident:
			if isPackageQualifier(info, node) {
				return false
			}
			classifyValue(info.TypeOf(node), node.Name, site)
		}
		return true
	})
}

// collectFromCall handles the call shapes that are decisions in themselves.
// Returns whether to keep descending into the call.
func collectFromCall(info *types.Info, call *ast.CallExpr, site *discoveredLogSite) bool {
	// len(x) / cap(x): a size, never the value.
	if ident, isIdent := call.Fun.(*ast.Ident); isIdent {
		if builtin, isBuiltin := info.ObjectOf(ident).(*types.Builtin); isBuiltin {
			if builtin.Name() == "len" || builtin.Name() == "cap" {
				return false
			}
		}
	}

	// A conversion, e.g. string(respBody). info.Types says whether the callee
	// is a TYPE rather than a function.
	if tv, known := info.Types[call.Fun]; known && tv.IsType() {
		if basic, isBasic := tv.Type.Underlying().(*types.Basic); isBasic && basic.Kind() == types.String && len(call.Args) == 1 {
			argType := info.TypeOf(call.Args[0])
			if argBasic, isBasic := argType.Underlying().(*types.Basic); !isBasic || argBasic.Kind() != types.String {
				site.carriers["conv:string()"] = true
				site.kinds[carrierRawString] = true
				return false
			}
		}
		return true
	}

	sel, isSel := call.Fun.(*ast.SelectorExpr)
	if !isSel {
		return true
	}
	if fn, isFn := info.ObjectOf(sel.Sel).(*types.Func); isFn && fn.Pkg() != nil {
		pkgPath := fn.Pkg().Path()
		if strings.HasSuffix(pkgPath, "/internal/credsafe") {
			site.carriers["call:credsafe."+sel.Sel.Name] = true
			site.kinds[carrierSafeCall] = true
			return false
		}
		if pkgPath == "fmt" && strings.HasPrefix(sel.Sel.Name, "Sprint") {
			site.carriers["call:fmt."+sel.Sel.Name] = true
			site.kinds[carrierRawString] = true
			return false
		}
	}
	if sel.Sel.Name == "Error" && len(call.Args) == 0 && implementsError(info.TypeOf(sel.X)) {
		site.carriers["call:.Error()"] = true
		site.kinds[carrierRawString] = true
		return false
	}
	return true
}

// classifyValue records a carrier when the value's TYPE says it is one.
// Returns true when it recorded something.
func classifyValue(t types.Type, label string, site *discoveredLogSite) bool {
	switch {
	case implementsError(t):
		site.carriers["ident:"+label] = true
		site.kinds[carrierErrValue] = true
		return true
	case isOpaquePayload(t):
		site.carriers["ident:"+label] = true
		site.kinds[carrierRawString] = true
		return true
	}
	return false
}

func isPackageQualifier(info *types.Info, expr ast.Expr) bool {
	ident, isIdent := expr.(*ast.Ident)
	if !isIdent {
		return false
	}
	_, isPkg := info.ObjectOf(ident).(*types.PkgName)
	return isPkg
}

// exprLabel renders an expression as the name a human would call it by
// ("err", "rdErr.Err"). It is a LABEL for the list, never the thing the
// verdict turns on — the type is.
func exprLabel(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprLabel(v.X) + "." + v.Sel.Name
	}
	return "?"
}

// loadTypedPackages type-checks the whole module. This is the cost of not
// reading spellings: roughly seven seconds, once, for a walk that resolves
// every call and every value to what it actually is.
func loadTypedPackages(t *testing.T, dir string, patterns ...string) []*packages.Package {
	t.Helper()
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
			packages.NeedSyntax | packages.NeedTypesInfo,
		Dir: dir,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		t.Fatalf("type-checking %v in %s: %v", patterns, dir, err)
	}
	var loadErrs []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			loadErrs = append(loadErrs, p.PkgPath+": "+e.Error())
		}
	})
	if len(loadErrs) > 0 {
		sort.Strings(loadErrs)
		t.Fatalf("the type checker could not load the tree, so the walk would silently see less than it claims:\n  %s", strings.Join(loadErrs, "\n  "))
	}
	if len(pkgs) == 0 {
		t.Fatalf("type-checking %v in %s returned no packages at all", patterns, dir)
	}
	return pkgs
}

// walkSlogCalls runs fn for every slog call in the given typed packages,
// skipping test files, and returns how many shipping files it read.
func walkSlogCalls(pkgs []*packages.Package, root string, fn func(file, funcName, msg string, site discoveredLogSite)) int {
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
				rel = path
			}
			rel = filepath.ToSlash(rel)
			if !inSweptDir(rel) {
				continue
			}
			filesRead++

			for _, decl := range syntax.Decls {
				funcDecl, isFunc := decl.(*ast.FuncDecl)
				if !isFunc {
					continue
				}
				ast.Inspect(funcDecl, func(n ast.Node) bool {
					call, isCall := n.(*ast.CallExpr)
					if !isCall {
						return true
					}
					sel, isSel := call.Fun.(*ast.SelectorExpr)
					if !isSel || !isSlogCall(pkg.TypesInfo, sel) {
						return true
					}
					site := discoveredLogSite{carriers: map[string]bool{}, kinds: map[string]bool{}}
					for _, arg := range call.Args {
						collectCarriers(pkg.TypesInfo, arg, &site)
					}
					if len(site.carriers) == 0 {
						return true
					}
					msg := ""
					for _, arg := range call.Args {
						lit, isLit := arg.(*ast.BasicLit)
						if isLit && lit.Kind.String() == "STRING" {
							if unquoted, unqErr := strconv.Unquote(lit.Value); unqErr == nil {
								msg = unquoted
							}
							break
						}
					}
					fn(rel, funcDecl.Name.Name, msg, site)
					return true
				})
			}
		}
	}
	return filesRead
}

func joinSet(set map[string]bool) string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

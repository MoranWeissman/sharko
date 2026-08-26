package audit

// guard_test.go — the source-reading guards.
//
// The runtime tests prove that what goes INTO Add comes out safe. These read
// the tree instead, and cover the two things a runtime test cannot see:
//
//  1. That nothing outside this package has, or grows, a way to put text into
//     Entry.Error. The type already makes it a compile error; this catches the
//     change that would UNDO that — an exported constructor, an exported field.
//
//  2. That no writer builds an audit Detail out of an error. Detail is the one
//     free-text field left, no error object travels with an entry, so the sink
//     has nothing to compare it against at runtime. This is the honest place to
//     enforce it, and the honest limit of the enforcement is stated in the test
//     itself.
//
// Guards need LISTS, not counts: every failure here names the file, the line
// and the expression, so a person can go and look rather than being told a
// number moved.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// repoRoot returns the repository root. Tests run with the package directory as
// the working directory, so the root is two levels up — checked, not assumed.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Clean(filepath.Join(wd, "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("expected the repo root at %s (no go.mod there): %v", root, err)
	}
	return root
}

// goSourceFiles walks the tree and returns every non-generated .go file under
// internal/, cmd/ and tests/.
func goSourceFiles(t *testing.T, includeTests bool) []string {
	t.Helper()
	root := repoRoot(t)
	var out []string
	for _, dir := range []string{"internal", "cmd", "tests"} {
		err := filepath.Walk(filepath.Join(root, dir), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if info.Name() == "node_modules" || info.Name() == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			if !includeTests && strings.HasSuffix(path, "_test.go") {
				return nil
			}
			out = append(out, path)
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}
	if len(out) < 50 {
		t.Fatalf("only %d source files found — this guard has lost its reach", len(out))
	}
	return out
}

// ── guard 1: Entry.Error stays unforgeable ──────────────────────────────────

// TestSafeText_CannotBeBuiltOutsideThisPackage is the structural claim behind
// the whole design, asserted rather than assumed.
//
// SafeText's fields must all be unexported (so no struct literal builds one)
// and no exported function in package audit may return one (so no constructor
// hands one out). Together those mean the only producer is sentenceFor, which
// is a map lookup over the catalog — so the set of strings Entry.Error can ever
// hold is exactly the set of sentences in safeSentences.
//
// A change that adds `func NewSafeText(s string) SafeText` reopens the leak
// this story closed, and fails here.
func TestSafeText_CannotBeBuiltOutsideThisPackage(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "internal", "audit")
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing internal/audit: %v", err)
	}

	sawType := false
	var problems []string
	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			rel := filepath.Base(path)
			ast.Inspect(file, func(n ast.Node) bool {
				// The type declaration: every field must be unexported.
				if ts, ok := n.(*ast.TypeSpec); ok && ts.Name.Name == "SafeText" {
					sawType = true
					st, isStruct := ts.Type.(*ast.StructType)
					if !isStruct {
						problems = append(problems, rel+": SafeText is no longer a struct — the unexported-field guarantee is gone")
						return true
					}
					for _, field := range st.Fields.List {
						for _, name := range field.Names {
							if name.IsExported() {
								problems = append(problems, rel+": SafeText has an EXPORTED field "+name.Name+
									" — any package can now build one with a struct literal")
							}
						}
					}
				}
				// Any exported function or method returning a SafeText.
				fn, ok := n.(*ast.FuncDecl)
				if !ok || !fn.Name.IsExported() || fn.Type.Results == nil {
					return true
				}
				for _, res := range fn.Type.Results.List {
					if ident, isIdent := res.Type.(*ast.Ident); isIdent && ident.Name == "SafeText" {
						problems = append(problems, rel+": exported "+fn.Name.Name+
							" returns a SafeText — that is a constructor, and it lets a caller choose the text again")
					}
				}
				return true
			})
		}
	}
	if !sawType {
		t.Fatal("did not find the SafeText type declaration — this guard checked nothing")
	}
	for _, p := range problems {
		t.Error(p)
	}
}

// TestSanitizeBuildsErrorOnlyFromTheCatalog is the permanent form of break
// test 4, and it is a SOURCE check on purpose.
//
// Break test 4 is "restore direct raw-error persistence at the sink". A
// behavioural test cannot catch that on its own: the restoration arrives with a
// new field, and no existing test knows to fill a field that does not exist yet.
// The first attempt at this break proved exactly that — the mutation compiled,
// ran, and failed nothing, which is a missing test rather than safe code.
//
// So this reads sanitize.go instead and pins the invariant directly: every
// assignment to entry.Error must be a call to sentenceFor, and nothing else. A
// mutation that writes `entry.Error = SafeText{sentence: entry.RawError}` fails
// here whatever it calls the new field, and so does one that copies a
// caller-supplied SafeText through.
func TestSanitizeBuildsErrorOnlyFromTheCatalog(t *testing.T) {
	path := filepath.Join(repoRoot(t), "internal", "audit", "sanitize.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parsing sanitize.go: %v", err)
	}

	assignments := 0
	ast.Inspect(file, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range assign.Lhs {
			sel, isSel := lhs.(*ast.SelectorExpr)
			if !isSel || sel.Sel.Name != "Error" {
				continue
			}
			recv, isIdent := sel.X.(*ast.Ident)
			if !isIdent || recv.Name != "entry" {
				continue
			}
			assignments++
			line := strconv.Itoa(fset.Position(assign.Pos()).Line)
			if i >= len(assign.Rhs) {
				t.Errorf("sanitize.go:%s assigns entry.Error in a form this guard cannot read", line)
				continue
			}
			call, isCall := assign.Rhs[i].(*ast.CallExpr)
			if !isCall {
				t.Errorf(`sanitize.go:%s assigns entry.Error from something that is not a call.

entry.Error may only ever be sentenceFor(entry.Reason). Anything else is a
second source of text at the sink, which is the leak this story closed.`, line)
				continue
			}
			fn, isIdentFn := call.Fun.(*ast.Ident)
			if !isIdentFn || fn.Name != "sentenceFor" {
				t.Errorf(`sanitize.go:%s assigns entry.Error from a call that is not sentenceFor.

entry.Error may only ever come from the reason catalog.`, line)
			}
		}
		return true
	})

	if assignments != 1 {
		t.Errorf(`sanitize assigns entry.Error %d time(s), want exactly 1.

More than one means there is a second, conditional path — which is precisely
the shape the old CredentialFailure design had. None means the field is no
longer rebuilt at all, so a caller-supplied value would survive.`, assignments)
	}

	// And no SafeText may be built anywhere except sentenceFor and the
	// UnmarshalJSON round-trip. A composite literal is the other way to put
	// arbitrary text into the type from inside this package.
	dir := filepath.Join(repoRoot(t), "internal", "audit")
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing internal/audit: %v", err)
	}
	for _, pkg := range pkgs {
		for p, f := range pkg.Files {
			base := filepath.Base(p)
			var fnName string
			ast.Inspect(f, func(n ast.Node) bool {
				if fd, ok := n.(*ast.FuncDecl); ok {
					fnName = fd.Name.Name
				}
				lit, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				ident, isIdent := lit.Type.(*ast.Ident)
				if !isIdent || ident.Name != "SafeText" {
					return true
				}
				if fnName == "sentenceFor" {
					return true
				}
				t.Errorf(`%s:%d builds a SafeText literal inside %s.

Only sentenceFor may build one, because only sentenceFor reads the catalog.
A literal anywhere else can carry any string at all.`,
					base, fset.Position(lit.Pos()).Line, fnName)
				return true
			})
		}
	}
}

// TestAdd_NoStringFieldOnEntryCanCarryTextIntoError is the behavioural half of
// break test 4, and it is written by REFLECTION so it covers fields that do not
// exist yet.
//
// It fills every settable string field on Entry with the sentinel, stores the
// entry, and asserts the stored Error is still a catalog sentence. A future
// field added as a back door — RawError, ErrText, Message, anything — is filled
// automatically and caught here without anybody remembering to extend the test.
func TestAdd_NoStringFieldOnEntryCanCarryTextIntoError(t *testing.T) {
	const probe = "PROBE-9f3a-must-never-become-the-stored-sentence"

	entry := Entry{}
	v := reflect.ValueOf(&entry).Elem()
	filled := 0
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if f.Kind() == reflect.String && f.CanSet() {
			f.SetString(probe)
			filled++
		}
	}
	if filled < 8 {
		t.Fatalf("only %d string fields filled — this probe has lost its reach", filled)
	}
	entry.Reason = ReasonUpstream // a valid category, so there is a real sentence to compare

	log := NewLog(10)
	log.Add(entry)
	stored := log.List(0)[0]

	if stored.Error.String() != safeSentences[ReasonUpstream] {
		t.Errorf(`the stored Error is %q, want the catalog sentence for %s.

Some field on Entry carried text into Error. Entry.Error must be a pure
function of Reason — see sanitize.go.`, stored.Error.String(), ReasonUpstream)
	}
	if strings.Contains(stored.Error.String(), probe) {
		t.Errorf("the stored Error carries the probe string: %q", stored.Error.String())
	}
}

// TestOnlySanitizeWritesEntryError keeps the tree-wide half: no file other
// than sanitize.go may assign to an Entry's Error at all.
func TestOnlySanitizeWritesEntryError(t *testing.T) {
	var offenders []string
	fset := token.NewFileSet()

	for _, path := range goSourceFiles(t, true) {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		rel := relTo(t, path)
		ast.Inspect(file, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, lhs := range assign.Lhs {
				sel, isSel := lhs.(*ast.SelectorExpr)
				if !isSel || sel.Sel.Name != "Error" {
					continue
				}
				recv, isIdent := sel.X.(*ast.Ident)
				if !isIdent || recv.Name != "entry" {
					continue
				}
				if rel == "internal/audit/sanitize.go" {
					continue
				}
				offenders = append(offenders,
					rel+":"+strconv.Itoa(fset.Position(assign.Pos()).Line)+" assigns entry.Error")
			}
			return true
		})
	}
	for _, o := range offenders {
		t.Errorf(`%s

Only sanitize may write Entry.Error, and only from the reason catalog. Any other
writer is a second path into the field, which is how the old design leaked.`, o)
	}
}

// ── guard 2: no audit Detail is built from an error ─────────────────────────

// TestNoAuditDetailIsBuiltFromAnError is the build-time half of the Detail
// protection, and the runtime half's honest limit.
//
// Detail is the one free-text field an audit writer still fills. No error
// object travels with an entry — Entry has no error-typed field, deliberately —
// so the sink cannot compare Detail against anything at runtime. What it does
// unconditionally is drop Detail for the two credential reasons; for every
// other reason Detail survives, because a Sharko-authored sentence naming the
// cluster and the PR is exactly the useful context the ruling says to keep.
//
// So the rule that Detail must not echo an error is enforced HERE, by reading
// the tree: no `.Error()` call and no error-named identifier may appear in a
// Detail expression inside an audit.Entry or audit.Fields literal.
//
// WHAT THIS DOES NOT REACH: a writer that assigns the words to a plainly-named
// local first (`msg := err.Error(); ... Detail: msg`). That is a real gap and it
// is written down rather than papered over.
func TestNoAuditDetailIsBuiltFromAnError(t *testing.T) {
	var offenders []string
	fset := token.NewFileSet()

	for _, path := range goSourceFiles(t, false) {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		rel := relTo(t, path)

		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || !isAuditLiteral(lit) {
				return true
			}
			for _, elt := range lit.Elts {
				kv, isKV := elt.(*ast.KeyValueExpr)
				if !isKV {
					continue
				}
				key, isIdent := kv.Key.(*ast.Ident)
				if !isIdent || key.Name != "Detail" {
					continue
				}
				if why := errorDerived(kv.Value); why != "" {
					offenders = append(offenders,
						rel+":"+strconv.Itoa(fset.Position(kv.Pos()).Line)+" — Detail is built from "+why)
				}
			}
			return true
		})
	}

	for _, o := range offenders {
		t.Errorf(`%s

An audit Detail must not carry an error's own words. The error goes to the
server log; the audit record gets a Reason, and Add turns that into a catalog
sentence in Error. See internal/audit/reason.go.`, o)
	}
}

// isAuditLiteral reports whether lit is an audit.Entry / audit.Fields literal
// (or a bare Entry / Fields literal inside package audit itself).
func isAuditLiteral(lit *ast.CompositeLit) bool {
	switch t := lit.Type.(type) {
	case *ast.SelectorExpr:
		pkg, ok := t.X.(*ast.Ident)
		return ok && pkg.Name == "audit" && (t.Sel.Name == "Entry" || t.Sel.Name == "Fields")
	case *ast.Ident:
		return t.Name == "Entry" || t.Name == "Fields"
	}
	return false
}

// errorDerived reports why expr looks like it was built from an error, or "".
func errorDerived(expr ast.Expr) string {
	var why string
	ast.Inspect(expr, func(n ast.Node) bool {
		if why != "" {
			return false
		}
		switch node := n.(type) {
		case *ast.CallExpr:
			if sel, ok := node.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Error" && len(node.Args) == 0 {
				why = "a .Error() call"
				return false
			}
		case *ast.Ident:
			lower := strings.ToLower(node.Name)
			if lower == "err" || strings.HasSuffix(lower, "err") || strings.HasSuffix(lower, "error") {
				why = "the identifier " + node.Name + ", which names an error"
				return false
			}
		}
		return true
	})
	return why
}

func relTo(t *testing.T, path string) string {
	t.Helper()
	rel, err := filepath.Rel(repoRoot(t), path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

// ── guard 3: the catalog is complete, checked as a LIST ─────────────────────

// declaredReasons parses the Reason const block in reason.go and returns every
// reason declared there.
//
// It reads the SOURCE rather than taking a hand-written list, so a reason added
// tomorrow is covered without anybody remembering to come here — and it returns
// the names, so a failure says WHICH reason is missing a sentence instead of
// "expected 13, got 12".
func declaredReasons(t *testing.T) []Reason {
	t.Helper()
	path := filepath.Join(repoRoot(t), "internal", "audit", "reason.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parsing reason.go: %v", err)
	}

	var reasons []Reason
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		ident, ok := spec.Type.(*ast.Ident)
		if !ok || ident.Name != "Reason" {
			return true
		}
		for _, v := range spec.Values {
			lit, isLit := v.(*ast.BasicLit)
			if !isLit || lit.Kind != token.STRING {
				continue
			}
			if unquoted, unqErr := strconv.Unquote(lit.Value); unqErr == nil {
				reasons = append(reasons, Reason(unquoted))
			}
		}
		return true
	})

	if len(reasons) == 0 {
		t.Fatal("parsed no Reason constants out of internal/audit/reason.go — this check covers nothing in that state")
	}
	return reasons
}

// TestReasonCatalog_EveryDeclaredReasonHasASentence fails BY NAME on any
// declared reason with no catalog sentence.
//
// A reason with no sentence is worse than useless: sentenceFor returns the zero
// SafeText, so the entry would say nothing at all while looking like it had
// been classified.
func TestReasonCatalog_EveryDeclaredReasonHasASentence(t *testing.T) {
	declared := map[Reason]bool{}
	for _, r := range declaredReasons(t) {
		declared[r] = true
		sentence, ok := safeSentences[r]
		if !ok {
			t.Errorf("%s is declared in reason.go but has no sentence in safeSentences — an entry classified as this would carry no message at all", r)
			continue
		}
		if strings.TrimSpace(sentence) == "" {
			t.Errorf("%s has an empty sentence", r)
		}
		if !r.Valid() {
			t.Errorf("%s is declared but Valid() rejects it — sanitize would blank it", r)
		}
	}
	for r := range safeSentences {
		if !declared[r] {
			t.Errorf("safeSentences has an entry for %s, which is not a declared Reason — dead prose", r)
		}
	}
}

// TestSafeSentences_CarryNoRawErrorVocabulary pins that the catalog is written
// in a person's words, not a machine's. A sentence that quoted client-go or SDK
// vocabulary would mean somebody had pasted an error message in here — which is
// the leak, arriving by the one route the type system cannot see.
func TestSafeSentences_CarryNoRawErrorVocabulary(t *testing.T) {
	banned := []string{
		"dial tcp", "x509:", "connection refused", "no such host",
		"deadline exceeded", "%!", "0x", "err:", "error:", "AccessDenied",
		"operation error", "https://", "status code",
	}
	for reason, sentence := range safeSentences {
		lower := strings.ToLower(sentence)
		for _, phrase := range banned {
			if strings.Contains(lower, strings.ToLower(phrase)) {
				t.Errorf("the %s sentence contains machine vocabulary %q — catalog sentences are written for a person: %q",
					reason, phrase, sentence)
			}
		}
	}
}

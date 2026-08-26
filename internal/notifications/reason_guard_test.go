package notifications

// reason_guard_test.go — the guards that keep the text channel shut.
//
// The fix in security story S4 is structural: a connection notification's
// description is two catalog lookups keyed by two enums, and there is no
// parameter left for a backend's own words to arrive through. These guards are
// what stop that being quietly undone — by adding a string field back to
// HealthResult, by passing an error's text where a Reason belongs, by adding a
// code with no catalog sentence, or by giving this package the ability to write
// to the audit log.
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
	"reflect"
	"strings"
	"testing"
)

// ── the catalogs are complete, checked as LISTS ─────────────────────────────

// TestReason_WireValuesExact pins every reason as a LITERAL.
//
// Writing ReasonUnreachable == ReasonUnreachable compares a symbol with itself
// and passes no matter what the value is — that exact mistake has been found
// repeatedly in this repository. These strings ship in the API response and in
// the OpenAPI spec, so changing one is a wire change.
func TestReason_WireValuesExact(t *testing.T) {
	exact := []struct {
		name string
		got  Reason
		want string
	}{
		{"ReasonUnreachable", ReasonUnreachable, "unreachable"},
		{"ReasonTLS", ReasonTLS, "tls"},
		{"ReasonTimeout", ReasonTimeout, "timed_out"},
		{"ReasonCredentials", ReasonCredentials, "credentials"},
		{"ReasonPermission", ReasonPermission, "permission_denied"},
		{"ReasonNotFound", ReasonNotFound, "not_found"},
		{"ReasonNotSynced", ReasonNotSynced, "not_synced"},
		{"ReasonUpstream", ReasonUpstream, "upstream_failure"},
		{"ReasonUnspecified", ReasonUnspecified, "unspecified"},
	}
	for _, tc := range exact {
		if string(tc.got) != tc.want {
			t.Errorf("the wire value of %s drifted:\n got %q\nwant %q", tc.name, tc.got, tc.want)
		}
	}

	// And the declared list holds exactly these, named. A reason declared in
	// the source but missing from this pin would ship unchecked.
	pinned := map[Reason]bool{}
	for _, tc := range exact {
		pinned[tc.got] = true
	}
	for _, r := range DeclaredReasons() {
		if !pinned[r] {
			t.Errorf("%q is declared but not pinned as a literal in this test — add it here so a wire change is a deliberate edit", r)
		}
	}
	for _, tc := range exact {
		if !tc.got.Valid() {
			t.Errorf("%s is pinned here but is not in the declared set", tc.name)
		}
	}
}

// TestReason_EveryDeclaredReasonHasASentence fails BY NAME on any declared
// reason with no catalog sentence — it would render as the unspecified
// fallback, silently.
func TestReason_EveryDeclaredReasonHasASentence(t *testing.T) {
	for _, r := range DeclaredReasons() {
		sentence, ok := reasonSentences[r]
		if !ok {
			t.Errorf("%q is declared but has no sentence in reasonSentences", r)
			continue
		}
		if strings.TrimSpace(sentence) == "" {
			t.Errorf("%q has an empty sentence", r)
		}
	}
	declared := map[Reason]bool{}
	for _, r := range DeclaredReasons() {
		declared[r] = true
	}
	for r := range reasonSentences {
		if !declared[r] {
			t.Errorf("reasonSentences has an entry for %q, which is not a declared Reason — dead prose", r)
		}
	}
}

// TestReason_SentencesCarryNoMachineVocabulary pins that the catalog is
// written in a person's words. A sentence quoting network or client-go
// vocabulary would mean somebody had pasted an error message in here.
func TestReason_SentencesCarryNoMachineVocabulary(t *testing.T) {
	banned := []string{
		"dial tcp", "x509:", "connection refused", "no such host",
		"deadline exceeded", "%!", "0x", "err:", "error:", "http status",
	}
	for r, sentence := range reasonSentences {
		lower := strings.ToLower(sentence)
		for _, phrase := range banned {
			if strings.Contains(lower, strings.ToLower(phrase)) {
				t.Errorf("the %q sentence contains machine vocabulary %q — catalog sentences are written for a person: %q",
					r, phrase, sentence)
			}
		}
	}
}

// TestReason_EveryConnectionCodeHasALead is the one that matters most for the
// sink. connectionLeads IS the definition of "this code is a connection
// alert", and Store.Add only re-derives the description of codes in it. A
// connection code missing from the map would keep whatever description its
// caller built — which is the hole this story closed.
//
// The list of connection codes is read from the SOURCE rather than typed out
// here: every Code passed to UnhealthyResultWithCode or to evaluate() is one.
func TestReason_EveryConnectionCodeHasALead(t *testing.T) {
	root := repoRoot(t)
	emitted := connectionCodesFromSource(t, root)
	if len(emitted) == 0 {
		t.Fatal("the sweep found no connection codes at all — it has lost its reach and would pass vacuously")
	}

	var missing []string
	for _, name := range emitted {
		value, ok := declaredCodeSourceNames(t)[name]
		if !ok {
			continue // not a Code constant; evaluate()'s other arguments
		}
		if _, has := connectionLeads[value]; !has {
			missing = append(missing, name+" ("+string(value)+")")
		}
	}
	if len(missing) > 0 {
		sortStrings(missing)
		t.Errorf("these codes are raised as connection alerts but have no lead sentence in connectionLeads:\n  %s\n"+
			"Without one, descriptionFor gives them the reason sentence on its own — a person is told what KIND of "+
			"thing went wrong but never which connection it was.",
			strings.Join(missing, "\n  "))
	}

	// And nothing in the map that is not a declared code.
	declared := map[Code]bool{}
	for _, v := range declaredCodeSourceNames(t) {
		declared[v] = true
	}
	for code := range connectionLeads {
		if !declared[code] {
			t.Errorf("connectionLeads has an entry for %q, which is not a declared Code — dead prose", code)
		}
	}
}

// ── the structural guarantees ───────────────────────────────────────────────

// TestHealthResult_HasNoTextChannel is the structural claim, asserted rather
// than assumed. HealthResult used to carry `detail string`, which both health
// probes filled with a backend's own error text, and then `title string`, which
// every probe filled with prose that got persisted and shown on the bell. There
// is no string-typed field left. One added back — by either of those names or
// any other — is caught here by NAME.
func TestHealthResult_HasNoTextChannel(t *testing.T) {
	allowed := map[string]string{
		"determined": "whether the probe could reach a conclusion at all",
		"healthy":    "the conclusion",
		"reason":     "the failure CATEGORY — an enum, and the only channel a probe has for saying what went wrong",
		"code":       "which alert this is",
	}

	typ := reflect.TypeOf(HealthResult{})
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if _, ok := allowed[name]; !ok {
			t.Errorf("HealthResult has a field %q (%s) that this guard does not know about.\n"+
				"If it is a new free-text failure field, it is the leak security story S4 removed: a probe would fill it "+
				"with an error's words and the store would persist them into the sharko-notifications ConfigMap. "+
				"Describe the failure with a Reason instead. If it is genuinely safe, add it to this list with a reason.",
				name, typ.Field(i).Type)
		}
	}
	for name := range allowed {
		found := false
		for i := 0; i < typ.NumField(); i++ {
			if typ.Field(i).Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("this guard expects a HealthResult field %q that no longer exists — the guard is out of date and covers less than it claims", name)
		}
	}
}

// TestReason_NoRawTextIsPassedToAHealthResult walks every call to
// UnhealthyResult and UnhealthyResultWithCode in the tree — production code and
// tests alike — and rejects any argument that could be carrying raw text.
//
// Allowed: a string literal, a bare identifier, a package selector (e.g.
// notifications.ReasonTimeout), and a call to ClassifyReason, which is the
// sanctioned way to turn a live error into a category.
//
// Rejected: string concatenation, fmt.Sprintf, and any other call — which is
// what err.Error() is. The two original leaks were exactly
// `UnhealthyResult(err.Error())` and a detail string built with %v.
func TestReason_NoRawTextIsPassedToAHealthResult(t *testing.T) {
	root := repoRoot(t)

	checked := 0
	for _, rel := range goFilesUnder(t, root) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, 0)
		if err != nil {
			continue // not our problem; the build catches unparseable files
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := calleeName(call.Fun)
			if name != "UnhealthyResult" && name != "UnhealthyResultWithCode" {
				return true
			}
			checked++
			for i, arg := range call.Args {
				if safeHealthResultArg(arg) {
					continue
				}
				t.Errorf("%s:%d: argument %d of %s is an expression that can carry raw text.\n"+
					"  A backend's own words in a connection alert get PERSISTED into the sharko-notifications "+
					"ConfigMap and served back on every restart — that is security story S4.\n"+
					"  Pass a Reason instead: ClassifyReason(err) where the error is still alive, and log the error itself.",
					rel, fset.Position(arg.Pos()).Line, i+1, name)
			}
			return true
		})
	}

	if checked == 0 {
		t.Fatal("no calls to UnhealthyResult or UnhealthyResultWithCode were found anywhere — this guard has lost its reach and would pass vacuously")
	}
}

// TestNotifications_CannotWriteToTheAuditLog pins the one thing this package
// borrows from internal/audit. Classify is a pure, type-based classifier and
// reusing it means there is one such list in the tree instead of two that can
// drift. Everything else in that package writes records, and a notification
// has no business doing that.
func TestNotifications_CannotWriteToTheAuditLog(t *testing.T) {
	root := repoRoot(t)
	allowed := map[string]bool{
		"Classify":          true,
		"Reason":            true,
		"ReasonCredentials": true, "ReasonSecretValue": true, "ReasonNotFound": true,
		"ReasonPermission": true, "ReasonUnreachable": true, "ReasonTLS": true,
		"ReasonTimeout": true, "ReasonCanceled": true, "ReasonUpstream": true,
	}

	swept := 0
	for _, rel := range packageGoFiles(t, root) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", rel, err)
		}
		swept++
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "audit" {
				return true
			}
			if !allowed[sel.Sel.Name] {
				t.Errorf("%s:%d: this package uses audit.%s. It may use audit.Classify (and the Reason constants it returns) "+
					"and nothing else — a notification must not write to the audit log.",
					rel, fset.Position(sel.Pos()).Line, sel.Sel.Name)
			}
			return true
		})
	}
	if swept == 0 {
		t.Fatal("no package files were swept — this guard would pass vacuously")
	}
}

// TestReason_TheOldConcatenationCannotComeBack sweeps the package for the exact
// shapes the leak was written in, so it cannot return quietly.
func TestReason_TheOldConcatenationCannotComeBack(t *testing.T) {
	banned := []struct{ pattern, why string }{
		{`" Reason: "`, "this is the concatenation that put a backend's error text into a persisted description"},
		{"res.detail", "HealthResult has no detail field any more — describe the failure with a Reason"},
		{".Error()", "an error's words must not be read in this package; classify the error where it is still alive"},
		{"err.Error", "an error's words must not be read in this package"},
	}

	swept := 0
	for _, rel := range []string{
		"internal/notifications/store.go",
		"internal/notifications/connection_poller.go",
		"internal/notifications/checker.go",
		"internal/notifications/reason.go",
		"internal/notifications/codes.go",
		"internal/notifications/render.go",
	} {
		raw, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		swept++
		for i, line := range strings.Split(string(raw), "\n") {
			// Comments explain the history on purpose; only code counts.
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, b := range banned {
				if strings.Contains(line, b.pattern) {
					t.Errorf("%s:%d: %q is back — %s\n  %s", rel, i+1, b.pattern, b.why, strings.TrimSpace(line))
				}
			}
		}
	}
	if swept == 0 {
		t.Fatal("no files were swept — this guard would pass vacuously")
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

// calleeName returns the final identifier of a call's function expression, so
// `UnhealthyResult(...)` and `notifications.UnhealthyResult(...)` both answer
// "UnhealthyResult".
func calleeName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

// safeHealthResultArg reports whether an argument to UnhealthyResult* is a
// shape that cannot be carrying a backend's words.
func safeHealthResultArg(arg ast.Expr) bool {
	switch a := arg.(type) {
	case *ast.BasicLit, *ast.Ident, *ast.SelectorExpr:
		// A literal, a local constant or a package constant. The titles and
		// codes come through here.
		return true
	case *ast.CallExpr:
		// The one sanctioned call: turning a live error into a category.
		return calleeName(a.Fun) == "ClassifyReason"
	}
	return false
}

// packageGoFiles lists this package's own .go files, relative to the root.
func packageGoFiles(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "internal", "notifications"))
	if err != nil {
		t.Fatalf("listing the notifications package: %v", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		out = append(out, filepath.Join("internal", "notifications", e.Name()))
	}
	return out
}

// goFilesUnder walks the repository for .go files, skipping vendored and
// generated trees. Returns paths relative to root.
func goFilesUnder(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "vendor", "ui", "_dist", ".worktrees":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
	return out
}

// connectionCodesFromSource returns the NAMES of every Code identifier passed
// as the code argument to UnhealthyResultWithCode, or as the defaultCode
// argument to evaluate(), anywhere in the tree. Those are exactly the codes
// that can be raised as connection alerts.
func connectionCodesFromSource(t *testing.T, root string) []string {
	t.Helper()
	seen := map[string]bool{}
	for _, rel := range goFilesUnder(t, root) {
		if strings.HasSuffix(rel, "_test.go") {
			continue // tests raise deliberately odd combinations
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, 0)
		if err != nil {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch calleeName(call.Fun) {
			case "UnhealthyResultWithCode":
				if len(call.Args) > 0 {
					if name := calleeName(call.Args[0]); name != "" {
						seen[name] = true
					}
				}
			case "evaluate":
				// p.evaluate(ctx, probe, last, lastCode, defaultCode, defaultTitle)
				if len(call.Args) >= 5 {
					if name := calleeName(call.Args[4]); name != "" {
						seen[name] = true
					}
				}
			}
			return true
		})
	}
	var out []string
	for name := range seen {
		out = append(out, name)
	}
	sortStrings(out)
	return out
}

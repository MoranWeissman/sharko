package envreg

// readsites_test.go — what counts as a file that really READS a setting.
//
// # The hole this closes
//
// The first version of these guards asked one question of a file:
//
//	strings.Contains(string(body), `"`+s.Name+`"`)
//
// a raw byte scan over the whole file. A COMMENT qualifies. So does
// `var _ = "SHARKO_X"`. Proven end to end by the adversarial review: a
//
//	// TODO: one day we may honour "SHARKO_MAGIC_KNOB" here.
//
// comment in cmd/sharko/serve.go, a registry entry naming that file as its
// reader, and a row in the operator configuration page telling people to
// set it — and both the registry guard and the documentation guard
// reported ok. A setting documented for operators, classified production,
// and read by nothing at all, passed every rule.
//
// That is the same failure the registry was built to remove: a claim
// counting as its own evidence.
//
// # What counts now
//
// The name has to reach a real read. The repository is parsed with
// go/parser, so comments are not in the tree at all, and a SHARKO_ string
// counts only when it is the argument of a call that reads the
// environment:
//
//	os.Getenv / os.LookupEnv     the direct reads
//	envreg.Lookup                the registry resolver
//	any function that passes its own parameter to one of those, to
//	whatever depth — which is how SHARKO_CONN_GIT_PROVIDER counts: it is a
//	const, handed to desiredStringFromEnv, which calls os.Getenv(key).
//
// A name declared as a constant is attributed to BOTH the file that
// declares the constant and the file that makes the call, because those
// are genuinely two different honest answers to "where is this read".
// SHARKO_TRUSTED_PROXIES is the live example: the constant lives in
// internal/api/clientip.go and cmd/sharko/serve.go makes the os.Getenv
// call through it.
//
// # What it deliberately does not do
//
// No type checking. A method call s.foo(x) is resolved by name within the
// package, so two methods of the same name on different types are treated
// as one. That over-approximates — it can only ever make the analysis see
// MORE reads, never fewer — and it costs none of the guarantees above,
// because a fabricated read still has to be a real call to a real function
// that really reaches os.Getenv.
//
// Names assembled at runtime (os.Getenv(prefix + suffix)) are not seen.
// Sharko has none, and the honest failure of a scan that cannot see a read
// is a name that looks unregistered — noisy, not silent.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// modulePath is this module's import path, so an import of one of our own
// packages can be turned back into a directory.
const modulePath = "github.com/MoranWeissman/sharko"

// envNameRe matches a complete SHARKO_ setting name.
var envNameRe = regexp.MustCompile(`^SHARKO_[A-Z0-9_]*[A-Z0-9]$`)

// readKind says HOW a name is read. It is a bit set: one name can be read
// both ways.
type readKind uint8

const (
	// readDirect — the value comes from os.Getenv or os.LookupEnv,
	// bypassing the registry entirely.
	readDirect readKind = 1 << iota
	// readRegistry — the value comes through envreg.Lookup, so deprecated
	// aliases resolve and conflicts are refused.
	readRegistry
)

// envReadIndex is every real read of every SHARKO_ name: name → file →
// how it is read there.
type envReadIndex map[string]map[string]readKind

func (idx envReadIndex) add(name, file string, kinds readKind) {
	if idx[name] == nil {
		idx[name] = map[string]readKind{}
	}
	idx[name][file] |= kinds
}

// readsIn says whether file really reads name.
func (idx envReadIndex) readsIn(file, name string) bool {
	_, ok := idx[name][filepath.ToSlash(file)]
	return ok
}

// directReadersOutside returns the files that read name straight from the
// environment, ignoring any under the given directory prefix. It is what
// the alias guard needs: an alias can only work for callers who go through
// envreg.Lookup.
func (idx envReadIndex) directReadersOutside(name, dirPrefix string) []string {
	var out []string
	for file, kinds := range idx[name] {
		if kinds&readDirect == 0 {
			continue
		}
		if file == dirPrefix || strings.HasPrefix(file, dirPrefix+"/") {
			continue
		}
		out = append(out, file)
	}
	sortStrings(out)
	return out
}

func sortStrings(in []string) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j] < in[j-1]; j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}

// ---------------------------------------------------------------------
// parsing
// ---------------------------------------------------------------------

// goSourceFile is one parsed file with the two things call resolution
// needs: which package it is in, and what its import names mean.
type goSourceFile struct {
	rel     string // repo-relative, slash separated
	pkgPath string // import path of the package it belongs to
	syntax  *ast.File
	imports map[string]string // local name → import path
}

// constDecl is a declared string constant or variable whose value is a
// SHARKO_ name.
type constDecl struct {
	value string
	file  string
}

var readIndexSkipDirs = map[string]bool{
	".git": true, "node_modules": true, ".bmad": true, "site": true,
	"dist": true, "docs": true, "charts": true, ".claude": true,
	".worktrees": true, "ui": true,
}

// parseRepoGoFiles parses every Go file in the tree, tests included: the
// registry's TestHarness entries name readers under tests/, and Rule 3
// has to be able to check those the same way as any other.
//
// Files are parsed with mode 0, so comments are not attached to the tree
// anywhere. That alone kills the TODO-comment attack; the read-expression
// rule below is what kills `var _ = "SHARKO_X"`.
func parseRepoGoFiles(t *testing.T, root string) []*goSourceFile {
	t.Helper()
	fset := token.NewFileSet()
	var out []*goSourceFile

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable path is not this test's business
		}
		if info.IsDir() {
			if path != root && readIndexSkipDirs[info.Name()] {
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
		rel = filepath.ToSlash(rel)

		syntax, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", rel, parseErr)
		}

		dir := filepath.ToSlash(filepath.Dir(rel))
		pkgPath := modulePath
		if dir != "." {
			pkgPath = modulePath + "/" + dir
		}

		imports := map[string]string{}
		for _, spec := range syntax.Imports {
			p, unqErr := strconv.Unquote(spec.Path.Value)
			if unqErr != nil {
				continue
			}
			name := p
			if i := strings.LastIndex(p, "/"); i >= 0 {
				name = p[i+1:]
			}
			if spec.Name != nil {
				name = spec.Name.Name
			}
			imports[name] = p
		}

		out = append(out, &goSourceFile{rel: rel, pkgPath: pkgPath, syntax: syntax, imports: imports})
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return out
}

// ---------------------------------------------------------------------
// the analysis
// ---------------------------------------------------------------------

// calleeKey names the function a call expression calls, as
// "<import path>|<function name>".
func calleeKey(fun ast.Expr, f *goSourceFile) (string, bool) {
	switch e := fun.(type) {
	case *ast.Ident:
		return f.pkgPath + "|" + e.Name, true
	case *ast.SelectorExpr:
		if x, isIdent := e.X.(*ast.Ident); isIdent {
			if p, isImport := f.imports[x.Name]; isImport {
				return p + "|" + e.Sel.Name, true
			}
		}
		// A method call, or a call on the result of something. Resolved by
		// name inside this package: an over-approximation that can only
		// widen what counts as a reader, never narrow it.
		return f.pkgPath + "|" + e.Sel.Name, true
	}
	return "", false
}

// stringLiteral returns a SHARKO_ name written as a literal.
func stringLiteral(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil || !envNameRe.MatchString(v) {
		return "", false
	}
	return v, true
}

// repoEnvReadIndex is the index of the real repository, built once. Six
// guards ask it questions; parsing the tree six times would just be slow.
var (
	repoIndexOnce sync.Once
	repoIndex     envReadIndex
)

func repoEnvReadIndex(t *testing.T, root string) envReadIndex {
	t.Helper()
	repoIndexOnce.Do(func() { repoIndex = buildEnvReadIndex(t, root) })
	return repoIndex
}

// buildEnvReadIndex is the whole analysis.
func buildEnvReadIndex(t *testing.T, root string) envReadIndex {
	t.Helper()
	files := parseRepoGoFiles(t, root)

	// Declared string constants and variables whose value is a SHARKO_
	// name, per package.
	consts := map[string]map[string]constDecl{}
	for _, f := range files {
		ast.Inspect(f.syntax, func(n ast.Node) bool {
			decl, ok := n.(*ast.GenDecl)
			if !ok || (decl.Tok != token.CONST && decl.Tok != token.VAR) {
				return true
			}
			for _, spec := range decl.Specs {
				vs, isValue := spec.(*ast.ValueSpec)
				if !isValue || len(vs.Names) != len(vs.Values) {
					continue
				}
				for i, name := range vs.Names {
					if name.Name == "_" {
						continue
					}
					v, isEnvName := stringLiteral(vs.Values[i])
					if !isEnvName {
						continue
					}
					if consts[f.pkgPath] == nil {
						consts[f.pkgPath] = map[string]constDecl{}
					}
					consts[f.pkgPath][name.Name] = constDecl{value: v, file: f.rel}
				}
			}
			return true
		})
	}

	// Reading positions: which parameter of which function ends up being
	// read from the environment. Seeded with the reads themselves, then
	// grown to a fixpoint through every helper that forwards a parameter.
	readingPos := map[string]map[int]readKind{
		"os|Getenv":                            {0: readDirect},
		"os|LookupEnv":                         {0: readDirect},
		modulePath + "/internal/envreg|Lookup": {0: readRegistry},
	}
	mark := func(key string, pos int, kinds readKind) bool {
		if readingPos[key] == nil {
			readingPos[key] = map[int]readKind{}
		}
		if readingPos[key][pos]&kinds == kinds {
			return false
		}
		readingPos[key][pos] |= kinds
		return true
	}

	for changed := true; changed; {
		changed = false
		for _, f := range files {
			for _, decl := range f.syntax.Decls {
				fd, isFunc := decl.(*ast.FuncDecl)
				if !isFunc || fd.Body == nil {
					continue
				}
				params := map[string]int{}
				idx := 0
				for _, field := range fd.Type.Params.List {
					if len(field.Names) == 0 {
						idx++
						continue
					}
					for _, n := range field.Names {
						params[n.Name] = idx
						idx++
					}
				}
				selfKey := f.pkgPath + "|" + fd.Name.Name

				ast.Inspect(fd.Body, func(n ast.Node) bool {
					call, isCall := n.(*ast.CallExpr)
					if !isCall {
						return true
					}
					key, resolved := calleeKey(call.Fun, f)
					if !resolved {
						return true
					}
					for i, arg := range call.Args {
						kinds := readingPos[key][i]
						if kinds == 0 {
							continue
						}
						id, isIdent := arg.(*ast.Ident)
						if !isIdent {
							continue
						}
						if j, isParam := params[id.Name]; isParam {
							if mark(selfKey, j, kinds) {
								changed = true
							}
						}
					}
					return true
				})
			}
		}
	}

	// The collection pass: every name that reaches a reading position.
	index := envReadIndex{}
	for _, f := range files {
		ast.Inspect(f.syntax, func(n ast.Node) bool {
			call, isCall := n.(*ast.CallExpr)
			if !isCall {
				return true
			}
			key, resolved := calleeKey(call.Fun, f)
			if !resolved {
				return true
			}
			for i, arg := range call.Args {
				kinds := readingPos[key][i]
				if kinds == 0 {
					continue
				}
				if v, isLiteral := stringLiteral(arg); isLiteral {
					index.add(v, f.rel, kinds)
					continue
				}
				if c, found := resolveConst(arg, f, consts); found {
					// Both files honestly read it: one names it, one
					// makes the call.
					index.add(c.value, c.file, kinds)
					index.add(c.value, f.rel, kinds)
				}
			}
			return true
		})
	}
	return index
}

// resolveConst follows an identifier or a package-qualified identifier to a
// declared SHARKO_ constant.
func resolveConst(e ast.Expr, f *goSourceFile, consts map[string]map[string]constDecl) (constDecl, bool) {
	switch v := e.(type) {
	case *ast.Ident:
		c, ok := consts[f.pkgPath][v.Name]
		return c, ok
	case *ast.SelectorExpr:
		x, isIdent := v.X.(*ast.Ident)
		if !isIdent {
			return constDecl{}, false
		}
		p, isImport := f.imports[x.Name]
		if !isImport {
			return constDecl{}, false
		}
		c, ok := consts[p][v.Sel.Name]
		return c, ok
	}
	return constDecl{}, false
}

// ---------------------------------------------------------------------
// the analysis, checked against itself
// ---------------------------------------------------------------------

// TestReadIndex_ARealReadIsSeen is the positive control. If the analysis
// ever goes blind, every other guard in this package quietly stops
// meaning anything, so the shapes Sharko really uses are pinned by name.
func TestReadIndex_ARealReadIsSeen(t *testing.T) {
	root := repoRootForEnvSweep(t)
	index := buildEnvReadIndex(t, root)

	for _, tc := range []struct {
		name  string
		file  string
		shape string
	}{
		{"SHARKO_CONFIG", "cmd/sharko/serve.go", "os.Getenv with the name written out"},
		// The listen-port pair. Both are read in httpport.go through
		// consts declared in that same file, which is why both are
		// attributed to it. SHARKO_PORT used to be pinned here as a bare
		// os.Getenv in cmd/sharko/serve.go; that read is gone, replaced by
		// envreg.ResolveHTTPPort.
		{"SHARKO_HTTP_PORT", "internal/envreg/httpport.go", "a const declared and read by os.LookupEnv in the same file"},
		{"SHARKO_PORT", "internal/envreg/httpport.go", "the deprecated alias, read by name alongside its canonical setting"},
		{"SHARKO_LOG_LEVEL", "cmd/sharko/serve.go", "a literal through the getEnvDefault helper"},
		{"SHARKO_CONN_GIT_PROVIDER", "internal/config/connection_gitnative.go", "a const through desiredStringFromEnv, two calls deep"},
		{"SHARKO_TRUSTED_PROXIES", "internal/api/clientip.go", "a const declared here and read by os.Getenv in another package"},
		{"SHARKO_TRUSTED_PROXIES", "cmd/sharko/serve.go", "the same const, at the call site"},
		{"SHARKO_BOOTSTRAP_ADMIN_PASSWORD", "internal/auth/store.go", "a const read by os.Getenv in the same file"},
		{"SHARKO_E2E_IMAGE_TAG", "tests/e2e/harness/sharko_helm.go", "a literal inside strings.TrimSpace(os.Getenv(…))"},
		{"SHARKO_PROBE_MODE", "internal/settings/store.go", "a literal through the desiredSettingFromEnv helper"},
	} {
		if !index.readsIn(tc.file, tc.name) {
			t.Errorf("%s is not seen as read in %s (%s). The analysis has gone blind: a guard that "+
				"cannot see a real read cannot tell a real reader from an invented one.",
				tc.name, tc.file, tc.shape)
		}
	}
}

// TestReadIndex_ACommentIsNotARead is the blocker, as a test.
//
// Both halves of the proven attack are driven here against the real
// analysis, in a scratch tree, so the rule is checked and not just
// asserted about.
func TestReadIndex_ACommentIsNotARead(t *testing.T) {
	root := t.TempDir()
	writeScratchModule(t, root, map[string]string{
		"looks_like_a_reader.go": `package scratch

import "os"

// TODO: one day we may honour "SHARKO_MAGIC_KNOB" here.
var _ = "SHARKO_BLANK_ASSIGNED"

const unusedName = "SHARKO_DECLARED_NEVER_READ"

func realRead() string { return os.Getenv("SHARKO_REALLY_READ") }
`,
	})

	index := buildEnvReadIndex(t, root)

	for _, name := range []string{"SHARKO_MAGIC_KNOB", "SHARKO_BLANK_ASSIGNED", "SHARKO_DECLARED_NEVER_READ"} {
		if _, seen := index[name]; seen {
			t.Errorf("%s counts as read. A comment, a blank assignment and an unused constant are "+
				"all just the name written down; none of them makes the server read anything.", name)
		}
	}
	if !index.readsIn("looks_like_a_reader.go", "SHARKO_REALLY_READ") {
		t.Error("the real os.Getenv read in the same file was missed — the rule has gone blind rather than strict")
	}
}

// writeScratchModule lays out a throwaway module the analysis can walk.
func writeScratchModule(t *testing.T, root string, files map[string]string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+modulePath+"\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
}

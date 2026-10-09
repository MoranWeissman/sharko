package main

// serve_env_validation_test.go — two questions about startup that a
// comment cannot answer.
//
//  1. Does the configuration check really run before Sharko touches a
//     cluster or a configuration store? Asked of the parsed statements
//     of serveCmd's RunE, in order, not of a comment saying it does.
//
//  2. Can the rule be dodged by putting the misspelling in a file?
//     secrets.env calls os.Setenv eighty-odd lines after the check has
//     already walked the environment, so a name introduced there used to
//     be invisible to it.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------
// 1. ordering
// ---------------------------------------------------------------------

// runEStatementIndex returns, for each named call, the index of the
// top-level statement of serveCmd's RunE that first contains it — or -1.
//
// Top-level statements are the right granularity: the check has to
// happen before the statement that builds the store, not merely before
// some expression inside it.
func runEStatementIndex(t *testing.T, wanted []string) map[string]int {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join("serve.go"), nil, 0)
	if err != nil {
		t.Fatalf("parsing serve.go: %v", err)
	}

	var body *ast.BlockStmt
	ast.Inspect(file, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, isIdent := kv.Key.(*ast.Ident)
		if !isIdent || key.Name != "RunE" {
			return true
		}
		if fn, isFunc := kv.Value.(*ast.FuncLit); isFunc {
			body = fn.Body
		}
		return false
	})
	if body == nil {
		t.Fatal("could not find serveCmd's RunE function literal in serve.go — this test is not " +
			"looking at what it thinks it is")
	}

	found := map[string]int{}
	for _, name := range wanted {
		found[name] = -1
	}
	for i, stmt := range body.List {
		ast.Inspect(stmt, func(n ast.Node) bool {
			call, isCall := n.(*ast.CallExpr)
			if !isCall {
				return true
			}
			name := callName(call.Fun)
			if name == "" {
				return true
			}
			if at, wantedHere := found[name]; wantedHere && at == -1 {
				found[name] = i
			}
			return true
		})
	}
	return found
}

// callName renders a call target as "pkg.Func" or "Func".
func callName(fun ast.Expr) string {
	switch e := fun.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		if x, ok := e.X.(*ast.Ident); ok {
			return x.Name + "." + e.Sel.Name
		}
	}
	return ""
}

// TestConfigurationIsCheckedBeforeAnythingIsBuilt is the ordering proof.
//
// The check is worth nothing if it runs after Sharko has already opened
// a store, built a client or started the server: by then a bad setting
// has been acted on, and stopping is no longer refusing to start, it is
// stopping halfway.
func TestConfigurationIsCheckedBeforeAnythingIsBuilt(t *testing.T) {
	const (
		validateRegistry = "envreg.Validate"
		validateEnv      = "envreg.ValidateEnvironment"
	)
	// Everything below reaches out of the process or opens state Sharko
	// then owns. NewK8sStore and NewFileStore are the configuration
	// store; NewServer reaches the auth store, which is the first real
	// network call to the cluster; loadSecretsEnv writes to the
	// environment the checks have just read.
	after := []string{
		"loadSecretsEnv",
		"config.NewK8sStore",
		"config.NewFileStore",
		"api.NewServer",
		"platform.Detect",
	}

	found := runEStatementIndex(t, append([]string{validateRegistry, validateEnv}, after...))

	for _, name := range []string{validateRegistry, validateEnv} {
		if found[name] == -1 {
			t.Fatalf("%s is not called from serveCmd's RunE at all. The configuration check is not "+
				"running at startup.", name)
		}
	}

	var checked int
	for _, name := range after {
		at := found[name]
		if at == -1 {
			t.Errorf("%s is no longer called from RunE — this test is now checking less than it "+
				"claims to. Update the list to name whatever replaced it.", name)
			continue
		}
		checked++
		for _, check := range []string{validateRegistry, validateEnv} {
			if found[check] >= at {
				t.Errorf("%s runs at statement %d, at or after %s at statement %d. The configuration "+
					"check has to finish before Sharko opens a store, builds a client or writes to its "+
					"own environment — otherwise a bad setting has already been acted on by the time "+
					"the server refuses to start.", check, found[check], name, at)
			}
		}
	}
	if checked == 0 {
		t.Fatal("none of the calls this test orders against were found, so it ordered nothing")
	}
}

// ---------------------------------------------------------------------
// 2. the secrets.env hole
// ---------------------------------------------------------------------

func writeSecretsEnv(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secrets.env")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

// TestAnUnknownSettingInSecretsEnvStopsStartup is the hole, as a test.
//
// loadSecretsEnv runs long after the environment has been checked and
// calls os.Setenv. Before this, a misspelled SHARKO_ name in that file
// reached the process and nothing ever looked at it — the rule shipped
// with a bypass, and the bypass was a file.
func TestAnUnknownSettingInSecretsEnvStopsStartup(t *testing.T) {
	path := writeSecretsEnv(t, strings.Join([]string{
		"# a local development secrets file",
		"SHARKO_ENCRYPTION_KEY=0123456789abcdef0123456789abcdef",
		"SHARKO_ENTIRELY_MADE_UP_KNOB=please-ignore-me",
		"",
	}, "\n"))

	err := loadSecretsEnv(path)
	if err == nil {
		t.Fatal("an unknown SHARKO_ name in secrets.env was accepted. The rule that stops a " +
			"misspelled setting in the Pod environment can then be dodged by writing the same " +
			"misspelling into a file instead.")
	}
	if !strings.Contains(err.Error(), "SHARKO_ENTIRELY_MADE_UP_KNOB") {
		t.Errorf("the error does not name the key: %v", err)
	}
	if !strings.Contains(err.Error(), "line 3") {
		t.Errorf("the error does not say which line of the file, which is the one thing that makes "+
			"it quick to fix: %v", err)
	}
	if strings.Contains(err.Error(), "please-ignore-me") {
		t.Errorf("the error repeats the value, out of the SECRETS file of all places: %v", err)
	}
	if os.Getenv("SHARKO_ENTIRELY_MADE_UP_KNOB") != "" {
		t.Error("the unknown name was exported into the process anyway")
	}
}

func TestAMisspellingInSecretsEnvOffersTheRealName(t *testing.T) {
	path := writeSecretsEnv(t, "SHARKO_ENCRYPTON_KEY=whatever\n")

	err := loadSecretsEnv(path)
	if err == nil {
		t.Fatal("a one-letter slip in secrets.env was accepted")
	}
	if !strings.Contains(err.Error(), "SHARKO_ENCRYPTION_KEY") {
		t.Errorf("the error does not offer the real name: %v", err)
	}
}

func TestAGoodSecretsEnvIsStillLoaded(t *testing.T) {
	// The rule has to leave a working file working, or it is not a rule,
	// it is a wall.
	const value = "0123456789abcdef0123456789abcdef"
	path := writeSecretsEnv(t, strings.Join([]string{
		"# comment",
		"",
		"SHARKO_ENCRYPTION_KEY=" + value,
		`SHARKO_LOG_LEVEL="debug"`,
		"SHARKO_PORT=9090",
		"NOT_A_SHARKO_NAME=fine",
		"",
	}, "\n"))

	t.Setenv("SHARKO_ENCRYPTION_KEY", "")
	t.Setenv("SHARKO_LOG_LEVEL", "")
	t.Setenv("SHARKO_PORT", "")
	t.Setenv("NOT_A_SHARKO_NAME", "")

	if err := loadSecretsEnv(path); err != nil {
		t.Fatalf("a secrets file naming only real settings was refused: %v", err)
	}
	for name, want := range map[string]string{
		"SHARKO_ENCRYPTION_KEY": value,
		"SHARKO_LOG_LEVEL":      "debug",
		"SHARKO_PORT":           "9090", // a registered deprecated alias, still honoured
		"NOT_A_SHARKO_NAME":     "fine",
	} {
		if got := os.Getenv(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestAMissingSecretsEnvIsNotAnError(t *testing.T) {
	if err := loadSecretsEnv(filepath.Join(t.TempDir(), "no-such-file.env")); err != nil {
		t.Errorf("a missing secrets.env is the normal case in production and must not stop the "+
			"server: %v", err)
	}
}

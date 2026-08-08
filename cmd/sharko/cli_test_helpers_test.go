package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// newOSPipe and swapStdout back captureStdoutT (takeover_test.go): every
// command under test writes its human-readable output straight to
// fmt.Print*, which reads os.Stdout at call time, so redirecting os.Stdout
// for the duration of the call is the only way to capture it.
func newOSPipe() (*os.File, *os.File, error) {
	return os.Pipe()
}

func swapStdout(f *os.File) *os.File {
	orig := os.Stdout
	os.Stdout = f
	return orig
}

// startCLITestServer spins up an httptest.Server and points the CLI's
// on-disk config at it (a fresh SHARKO_CONFIG_DIR per test, so tests never
// share state or touch a developer's real ~/.sharko). The server is closed
// automatically via t.Cleanup.
func startCLITestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	t.Setenv("SHARKO_CONFIG_DIR", dir)
	configHomeWarned = false

	if err := saveConfig(&SharkoConfig{Server: srv.URL, Token: "test-token"}); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	// serverFlag is a package-level var backing rootCmd's persistent
	// --server flag. A previous test invoking a command with --server set
	// would otherwise leak into this one, silently overriding the saved
	// config's URL (effectiveServer prefers the flag). Reset it so every
	// test starts from "use the saved config", which is what
	// startCLITestServer just wrote.
	serverFlag = ""
	t.Cleanup(func() { serverFlag = "" })

	return srv
}

// resetFlags restores every flag on cmd to its default value and clears
// Changed. Every command's RunE function is a closure over a package-level
// *cobra.Command var, so flags set by one test (Set marks Changed=true)
// would otherwise leak into the next test that invokes the same command —
// this is the first place this codebase drives cobra commands directly by
// calling RunE rather than through rootCmd.Execute(), so nothing enforced
// that isolation before.
func resetFlags(cmd *cobra.Command) {
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		// Repeatable flags (StringArray/StringSlice) implement SliceValue,
		// whose Set() APPENDS rather than replaces — Set(f.DefValue) on one
		// of those would leave a stray "[]" entry instead of an empty
		// slice. Replace(nil) is the correct reset for that family; every
		// other flag type resets fine through Set(f.DefValue).
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			_ = sv.Replace(nil)
		} else {
			_ = f.Value.Set(f.DefValue)
		}
		f.Changed = false
	})
}

// setFlags sets each flag by name to the given string form and marks it
// Changed, matching what cobra's own parser does when a flag is passed on
// the command line. For a repeatable flag, call it once per value — Set()
// on those appends. Call resetFlags first so unrelated flags from a
// previous test don't leak in.
func setFlags(t *testing.T, cmd *cobra.Command, values map[string]string) {
	t.Helper()
	for name, val := range values {
		if err := cmd.Flags().Set(name, val); err != nil {
			t.Fatalf("set flag %s=%q: %v", name, val, err)
		}
	}
}

// setRepeatedFlag calls Set once per value for a repeatable
// (StringArray/StringSlice) flag, since Set() appends rather than replaces.
func setRepeatedFlag(t *testing.T, cmd *cobra.Command, name string, values ...string) {
	t.Helper()
	for _, v := range values {
		if err := cmd.Flags().Set(name, v); err != nil {
			t.Fatalf("set flag %s=%q: %v", name, v, err)
		}
	}
}

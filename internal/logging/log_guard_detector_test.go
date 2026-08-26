package logging

// log_guard_detector_test.go — the positive control for the walk itself (B16).
//
// TestLogErrorGuard_ListMatchesTheTree compares two lists. If the walk quietly
// stopped recognising slog calls, both would be empty-ish and agree perfectly,
// and a broken detector makes the whole guard pass for free. So the walk is
// handed source it MUST flag, and required to flag it — including the two
// shapes an independent reviewer got past the old, syntax-reading version:
//
//   - a log line written through a CHAINED logger, and
//   - a carrier held in a variable whose name is in no list anywhere.
//
// The planted source is type-checked the same way the tree is, so the control
// exercises the real production path of this walk rather than a simplified
// copy of it.

import (
	"os"
	"path/filepath"
	"testing"
)

// plantedModule writes a tiny throwaway module containing src and returns its
// directory. It ships its own internal/credsafe so the credsafe rule (which
// matches on package PATH, not on a receiver name) has something real to
// resolve against.
func plantedModule(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, body string) {
		full := filepath.Join(dir, rel)
		if mkErr := os.MkdirAll(filepath.Dir(full), 0o750); mkErr != nil {
			t.Fatalf("mkdir %s: %v", rel, mkErr)
		}
		if wErr := os.WriteFile(full, []byte(body), 0o600); wErr != nil {
			t.Fatalf("write %s: %v", rel, wErr)
		}
	}
	write("go.mod", "module planted.example\n\ngo 1.26\n")
	write("internal/credsafe/credsafe.go",
		"package credsafe\n\nfunc Sentence(err error) string { _ = err; return \"a fixed sentence\" }\n")
	write("internal/plant/plant.go", src)
	return dir
}

// carriersOfPlanted type-checks the planted module and returns the carriers
// the walk finds on the single log call in it.
func carriersOfPlanted(t *testing.T, src string) map[string]bool {
	t.Helper()
	dir := plantedModule(t, src)
	pkgs := loadTypedPackages(t, dir, "./internal/plant")
	carriers := map[string]bool{}
	sites := 0
	walkSlogCalls(pkgs, dir, func(_, _, _ string, site discoveredLogSite) {
		sites++
		for k := range site.carriers {
			carriers[k] = true
		}
	})
	if sites > 1 {
		t.Fatalf("the planted source was meant to hold one flagged log call; the walk reported %d", sites)
	}
	return carriers
}

const plantedPreamble = `package plant

import (
	"errors"
	"fmt"
	"log/slog"

	"planted.example/internal/credsafe"
)

type wrapped struct{ inner error }

func (w *wrapped) Error() string { return w.inner.Error() }

func f() {
	err := errors.New("boom")
	var zzNobodyNamedThis []byte
	var alsoUnlisted []string
	problem := &wrapped{inner: err}
	repoURL := "https://token@example.invalid/org/repo"
	_, _, _, _, _ = err, zzNobodyNamedThis, alsoUnlisted, problem, repoURL
	// Keep both imports used whichever single line is planted below. These
	// are function VALUES, not calls, and neither sits inside a slog call.
	_ = fmt.Sprint
	_ = credsafe.Sentence
`

func TestLogGuardWalk_FlagsEveryCarrierShape(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string
		want string
	}{
		{"an error value", `slog.Error("x", "error", err)`, "ident:err"},
		{
			"an error under a name no list contains",
			`slog.Warn("x", "error", problem)`,
			"ident:problem",
		},
		{
			"a log line written through a CHAINED logger",
			`slog.Default().With("component", "c").Warn("x", "error", err)`,
			"ident:err",
		},
		{
			"a payload in a variable no list contains",
			`slog.Error("x", "d", zzNobodyNamedThis)`,
			"ident:zzNobodyNamedThis",
		},
		{
			"a []string of somebody else's messages",
			`slog.Error("x", "d", alsoUnlisted)`,
			"ident:alsoUnlisted",
		},
		{"a response body flattened to a string", `slog.Error("x", "body", string(zzNobodyNamedThis))`, "conv:string()"},
		{"err.Error()", `slog.Error("x", "error", err.Error())`, "call:.Error()"},
		{"an fmt.Sprintf of it", `slog.Error("x", "d", fmt.Sprintf("%v", err))`, "call:fmt.Sprintf"},
		{"a credsafe sentence", `slog.Warn("x", "error", credsafe.Sentence(err))`, "call:credsafe.Sentence"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			carriers := carriersOfPlanted(t, plantedPreamble+"\t"+tc.line+"\n}\n")
			if !carriers[tc.want] {
				t.Fatalf(`the walk did NOT flag a planted %s.

line:     %s
carriers: %v

A walk that cannot find a carrier somebody put there proves nothing about the
ones it says are absent.`, tc.name, tc.line, carriers)
			}
		})
	}
}

// TestLogGuardWalk_SaysNothingAboutACleanLine keeps the control honest in the
// other direction: a walk that fired on everything would make every entry in
// logGuardSites noise, and the list would stop meaning anything.
func TestLogGuardWalk_SaysNothingAboutACleanLine(t *testing.T) {
	for _, line := range []string{
		`slog.Info("registered", "cluster", "prod-eu", "region", "eu-west-1")`,
		`slog.Info("read", "size", len(zzNobodyNamedThis))`,
		`slog.Info("repo", "url", repoURL)`,
	} {
		carriers := carriersOfPlanted(t, plantedPreamble+"\t"+line+"\n}\n")
		if len(carriers) != 0 {
			t.Errorf("the walk fired on %s, naming %v — every entry in the list would be noise", line, carriers)
		}
	}
}

// TestLogGuardWalk_IgnoresLookalikesThatAreNotLogCalls pins the other half of
// resolving by type: `Error` is a very common method name, and the old walk
// had to dodge that with a receiver-name allowlist that also hid the chained
// loggers. Asking the type checker which package the function belongs to gets
// both right at once.
func TestLogGuardWalk_IgnoresLookalikesThatAreNotLogCalls(t *testing.T) {
	src := `package plant

import (
	"errors"
	"net/http"
)

func f(w http.ResponseWriter) {
	err := errors.New("boom")
	http.Error(w, "session: "+err.Error(), http.StatusInternalServerError)
}
`
	dir := plantedModule(t, src)
	pkgs := loadTypedPackages(t, dir, "./internal/plant")
	sites := 0
	walkSlogCalls(pkgs, dir, func(_, _, _ string, _ discoveredLogSite) { sites++ })
	if sites != 0 {
		t.Errorf("the walk counted http.Error as a log line (%d site(s)) — it is resolving names, not functions", sites)
	}
}

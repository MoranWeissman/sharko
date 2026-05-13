package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// mockFileInfo is a controllable os.FileInfo used by the evaluateDirSafety
// pure-unit tests. Sys() is the load-bearing field — pass a *syscall.Stat_t
// with a chosen Uid to simulate a wrong-owner directory without actually
// being root.
type mockFileInfo struct {
	name string
	mode os.FileMode
	sys  any
}

func (m mockFileInfo) Name() string       { return m.name }
func (m mockFileInfo) Size() int64        { return 0 }
func (m mockFileInfo) Mode() os.FileMode  { return m.mode }
func (m mockFileInfo) ModTime() time.Time { return time.Time{} }
func (m mockFileInfo) IsDir() bool        { return m.mode.IsDir() }
func (m mockFileInfo) Sys() any           { return m.sys }

// V124-2.5 — config directory resolution and the BUG-007 fallback.
//
// BUG-007: Running `sharko login` inside the official Docker image failed
// with "mkdir /.sharko: permission denied" because os.UserHomeDir() returns
// "" when $HOME is unset and the resolver previously joined that to
// .sharko, producing /.sharko (root-owned, unwritable for uid 1001).
//
// The fix introduces an os.TempDir()-based fallback and a SHARKO_CONFIG_DIR
// override. These tests pin both behaviours.

// withTempEnv installs HOME and SHARKO_CONFIG_DIR for the duration of the
// test and resets the one-shot warning so each test starts fresh.
func withTempEnv(t *testing.T, home, sharkoDir string) {
	t.Helper()
	t.Setenv("HOME", home)
	if sharkoDir == "" {
		t.Setenv("SHARKO_CONFIG_DIR", "")
		_ = os.Unsetenv("SHARKO_CONFIG_DIR")
	} else {
		t.Setenv("SHARKO_CONFIG_DIR", sharkoDir)
	}
	configHomeWarned = false
}

func TestConfigDir_HomeSet(t *testing.T) {
	tmp := t.TempDir()
	withTempEnv(t, tmp, "")
	got := configDir()
	want := filepath.Join(tmp, ".sharko")
	if got != want {
		t.Errorf("configDir() = %q, want %q", got, want)
	}
}

func TestConfigDir_HomeEmpty_FallsBackToTempDir(t *testing.T) {
	withTempEnv(t, "", "")
	got := configDir()
	expectedPrefix := os.TempDir()
	if !strings.HasPrefix(got, expectedPrefix) {
		t.Errorf("configDir() = %q, want prefix %q", got, expectedPrefix)
	}
	if !strings.HasSuffix(got, ".sharko") {
		t.Errorf("configDir() = %q, want suffix .sharko", got)
	}
}

func TestConfigDir_HomeRoot_FallsBackToTempDir(t *testing.T) {
	// $HOME=/ would resolve to /.sharko which is unwritable for non-root
	// users. The fallback must kick in for this case too — that is the
	// exact failure mode reported in BUG-007 (mkdir /.sharko: permission
	// denied inside the official image).
	withTempEnv(t, "/", "")
	got := configDir()
	if got == "/.sharko" {
		t.Errorf("configDir() = %q — fallback should have engaged", got)
	}
	if !strings.HasSuffix(got, ".sharko") {
		t.Errorf("configDir() = %q, want suffix .sharko", got)
	}
}

func TestConfigDir_OverrideHonored(t *testing.T) {
	override := t.TempDir()
	withTempEnv(t, "/", override)
	got := configDir()
	if got != override {
		t.Errorf("configDir() = %q, want SHARKO_CONFIG_DIR=%q", got, override)
	}
}

// L11 — SHARKO_CONFIG_DIR with a leading "~/" must expand to the user's
// home directory. The previous behaviour passed "~/sharko-test" through
// verbatim, which is a surprising UX failure: shells expand ~ before
// invoking the binary, but env-var values set via `export
// SHARKO_CONFIG_DIR=~/sharko-test` in some configurations (or set
// programmatically) preserve the literal tilde. configDir now applies
// standard tilde expansion to match what users expect.
func TestConfigDir_OverrideExpandsTilde(t *testing.T) {
	home := t.TempDir()
	withTempEnv(t, home, "~/sharko-test")
	got := configDir()
	want := filepath.Join(home, "sharko-test")
	if got != want {
		t.Errorf("configDir() = %q, want %q (tilde must expand to $HOME)", got, want)
	}
}

// L11 — bare "~" alone (no trailing slash) expands to $HOME with no
// suffix appended. Mirrors shell semantics.
func TestConfigDir_OverrideBareTilde(t *testing.T) {
	home := t.TempDir()
	withTempEnv(t, home, "~")
	got := configDir()
	if got != home {
		t.Errorf("configDir() = %q, want %q (bare ~ must expand to $HOME)", got, home)
	}
}

// L11 — an embedded tilde (not at the start) must be left alone. Standard
// shells only expand the leading ~ of a word; we mirror that.
func TestConfigDir_OverrideEmbeddedTildeUntouched(t *testing.T) {
	home := t.TempDir()
	embedded := "/var/run/~not-home/sharko"
	withTempEnv(t, home, embedded)
	got := configDir()
	if got != embedded {
		t.Errorf("configDir() = %q, want %q (embedded ~ must not be expanded)", got, embedded)
	}
}

// L11 — when $HOME cannot be resolved, expansion is a no-op rather than
// silently rewriting to the empty string. Better to surface the literal
// path so the operator sees what they configured.
func TestConfigDir_OverrideTildeWithNoHome(t *testing.T) {
	withTempEnv(t, "", "~/sharko-test")
	got := configDir()
	if got != "~/sharko-test" {
		t.Errorf("configDir() = %q, want %q (no $HOME → leave tilde literal)", got, "~/sharko-test")
	}
}

func TestSaveAndLoadConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	withTempEnv(t, "/", dir)

	in := &SharkoConfig{Server: "http://localhost:8080", Token: "xyz"}
	if err := saveConfig(in); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
	out, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if out.Server != in.Server || out.Token != in.Token {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", out, in)
	}

	wantPath := filepath.Join(dir, "config")
	if _, statErr := os.Stat(wantPath); statErr != nil {
		t.Errorf("expected config at %q, stat err: %v", wantPath, statErr)
	}
}

func TestSaveConfig_HomelessEnvironment_DoesNotCrash(t *testing.T) {
	// Simulate a Docker container where HOME is unset and the binary runs
	// as a non-root user. With the fix in place, saveConfig must succeed
	// against the os.TempDir() fallback. This is the BUG-007 regression
	// test.
	withTempEnv(t, "", "")
	if err := saveConfig(&SharkoConfig{Server: "http://x", Token: "t"}); err != nil {
		t.Fatalf("saveConfig with empty HOME: %v", err)
	}
	if _, err := loadConfig(); err != nil {
		t.Fatalf("loadConfig after saveConfig: %v", err)
	}
}

// ---------------------------------------------------------------------------
// V124-2.11 — TOCTOU/symlink guard on the os.TempDir() fallback.
//
// The fallback path (added in V124-2.5 to fix the missing-HOME container
// case) opened a symlink-squatting attack on shared hosts: an attacker who
// pre-creates /tmp/.sharko or symlinks it could capture the bearer token
// at write time. resolveSafeConfigDir now refuses any pre-existing dir at
// the fallback path that is a symlink or owned by a different uid.
// SHARKO_CONFIG_DIR overrides bypass these checks (operator opted in).
// ---------------------------------------------------------------------------

func TestResolveSafeConfigDir_NonExistingDir_OK(t *testing.T) {
	// Fallback path doesn't exist yet — the helper must approve it so the
	// downstream MkdirAll(0700) creates a fresh dir owned by the current
	// user. This is the normal first-run case.
	withTempEnv(t, "", "")
	dir := filepath.Join(t.TempDir(), "fresh-sharko")
	got, err := resolveSafeConfigDir(dir)
	if err != nil {
		t.Fatalf("unexpected error on non-existent dir: %v", err)
	}
	if got != dir {
		t.Errorf("got = %q, want %q", got, dir)
	}
}

func TestResolveSafeConfigDir_OwnedByCurrentUser_OK(t *testing.T) {
	// Pre-create a dir owned by the current user — the helper must
	// approve it. This is the second-and-later-invocation case where
	// /tmp/.sharko already exists from a previous successful login.
	withTempEnv(t, "", "")
	dir := filepath.Join(t.TempDir(), "mine")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	got, err := resolveSafeConfigDir(dir)
	if err != nil {
		t.Fatalf("unexpected error on own-dir: %v", err)
	}
	if got != dir {
		t.Errorf("got = %q, want %q", got, dir)
	}
}

func TestResolveSafeConfigDir_Symlink_Refused(t *testing.T) {
	// Attacker symlinks the fallback to a path they control. Even though
	// the symlink target is also owned by the current user in the test,
	// the helper must refuse on the basis of "is a symlink" alone — the
	// target could change between Lstat and Open in a real attack.
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows; covered by Lstat ModeSymlink check")
	}
	withTempEnv(t, "", "")
	scratch := t.TempDir()
	target := filepath.Join(scratch, "real")
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	link := filepath.Join(scratch, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	_, err := resolveSafeConfigDir(link)
	if err == nil {
		t.Fatal("expected error on symlinked fallback dir, got nil")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error does not mention symlink: %v", err)
	}
	if !strings.Contains(err.Error(), "SHARKO_CONFIG_DIR") {
		t.Errorf("error does not direct operator at SHARKO_CONFIG_DIR override: %v", err)
	}
}

func TestResolveSafeConfigDir_SHARKO_CONFIG_DIR_BypassesSafetyChecks(t *testing.T) {
	// SHARKO_CONFIG_DIR is the operator's explicit opt-in; the safety
	// checks must NOT block it even if the path is a symlink. This keeps
	// the override usable as a workaround when the safety checks are too
	// aggressive for a given environment.
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	scratch := t.TempDir()
	target := filepath.Join(scratch, "real")
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	link := filepath.Join(scratch, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	withTempEnv(t, "", link)
	got, err := resolveSafeConfigDir(link)
	if err != nil {
		t.Fatalf("expected SHARKO_CONFIG_DIR override to bypass safety checks, got: %v", err)
	}
	if got != link {
		t.Errorf("got = %q, want %q (override should pass through unchanged)", got, link)
	}
}

func TestSaveConfig_SymlinkedFallback_RefusedWithClearError(t *testing.T) {
	// End-to-end: simulate the BUG-007 environment (HOME unset → fallback
	// kicks in) but with the fallback dir pre-symlinked by an attacker.
	// saveConfig must propagate the clear error rather than write the
	// token through the symlink.
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	scratch := t.TempDir()
	target := filepath.Join(scratch, "attacker-controlled")
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Symlink the would-be fallback path. We can't easily intercept the
	// real os.TempDir() lookup, so we redirect TMPDIR to a controlled
	// scratch dir and pre-symlink the .sharko entry inside it.
	tmpdirHost := t.TempDir()
	t.Setenv("TMPDIR", tmpdirHost)
	link := filepath.Join(tmpdirHost, ".sharko")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	withTempEnv(t, "", "")

	err := saveConfig(&SharkoConfig{Server: "http://x", Token: "secret"})
	if err == nil {
		t.Fatal("expected saveConfig to refuse symlinked fallback, got nil")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error does not mention symlink: %v", err)
	}
	// Confirm the attacker target is empty — the token must not have
	// been written through the symlink.
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Errorf("attacker target wrote %d files — token leaked through symlink", len(entries))
	}
}

// ---------------------------------------------------------------------------
// V124-2.11 (continued) — pure-unit tests for evaluateDirSafety.
//
// These tests exist because the wrong-owner branch of resolveSafeConfigDir is
// otherwise impossible to exercise without root privileges (we can't chown a
// dir to a different uid as a non-root test runner). evaluateDirSafety takes
// info + statErr + currentEUID as plain inputs, so we can simulate every
// decision without touching the filesystem.
//
// Decision values asserted: dirSafeNotExist, dirSafeOK, dirUnsafeSymlink,
// dirUnsafeWrongOwner, dirUnsafeStatError.
// ---------------------------------------------------------------------------

func TestEvaluateDirSafety_NotExist_ReturnsSafeNotExist(t *testing.T) {
	got := evaluateDirSafety(nil, os.ErrNotExist, 1000)
	if got != dirSafeNotExist {
		t.Errorf("got %v, want dirSafeNotExist", got)
	}
}

func TestEvaluateDirSafety_NotExist_WrappedError_ReturnsSafeNotExist(t *testing.T) {
	// errors.Is must unwrap — Lstat in practice returns *PathError wrapping
	// syscall.ENOENT, which satisfies errors.Is(err, os.ErrNotExist).
	wrapped := &os.PathError{Op: "lstat", Path: "/nope", Err: syscall.ENOENT}
	got := evaluateDirSafety(nil, wrapped, 1000)
	if got != dirSafeNotExist {
		t.Errorf("got %v, want dirSafeNotExist (wrapped ENOENT)", got)
	}
}

func TestEvaluateDirSafety_StatError_ReturnsUnsafeStatError(t *testing.T) {
	// Any stat error other than NotExist (e.g. EACCES) must NOT silently
	// allow the dir — we can't prove safety, so we refuse.
	otherErr := errors.New("permission denied")
	got := evaluateDirSafety(nil, otherErr, 1000)
	if got != dirUnsafeStatError {
		t.Errorf("got %v, want dirUnsafeStatError", got)
	}
}

func TestEvaluateDirSafety_Symlink_ReturnsUnsafeSymlink(t *testing.T) {
	info := mockFileInfo{
		name: "fallback",
		mode: os.ModeDir | os.ModeSymlink,
		// Sys is irrelevant — symlink check fires first.
		sys: nil,
	}
	got := evaluateDirSafety(info, nil, 1000)
	if got != dirUnsafeSymlink {
		t.Errorf("got %v, want dirUnsafeSymlink", got)
	}
}

func TestEvaluateDirSafety_WrongOwner_ReturnsUnsafeWrongOwner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ownership check is skipped on windows; see TestEvaluateDirSafety_Windows")
	}
	info := mockFileInfo{
		name: "fallback",
		mode: os.ModeDir | 0700,
		sys:  &syscall.Stat_t{Uid: 9999}, // attacker uid
	}
	got := evaluateDirSafety(info, nil, 1000) // current process uid
	if got != dirUnsafeWrongOwner {
		t.Errorf("got %v, want dirUnsafeWrongOwner", got)
	}
}

func TestEvaluateDirSafety_OwnedByCurrentUser_ReturnsSafeOK(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Sys() shape differs on windows; see TestEvaluateDirSafety_Windows")
	}
	info := mockFileInfo{
		name: "fallback",
		mode: os.ModeDir | 0700,
		sys:  &syscall.Stat_t{Uid: 1000},
	}
	got := evaluateDirSafety(info, nil, 1000)
	if got != dirSafeOK {
		t.Errorf("got %v, want dirSafeOK", got)
	}
}

func TestEvaluateDirSafety_NilInfoNilErr_DefensiveUnsafeStatError(t *testing.T) {
	// Defensive branch — should never happen in practice, but we want to
	// fail closed rather than panic.
	got := evaluateDirSafety(nil, nil, 1000)
	if got != dirUnsafeStatError {
		t.Errorf("got %v, want dirUnsafeStatError (defensive nil branch)", got)
	}
}

func TestEvaluateDirSafety_UnknownSysShape_ReturnsSafeOK(t *testing.T) {
	// If Sys() returns something that's not *syscall.Stat_t (e.g. a custom
	// FileInfo from an embedded FS), we can't prove ownership but the
	// symlink check passed. Be conservative-but-usable: allow it.
	info := mockFileInfo{
		name: "fallback",
		mode: os.ModeDir | 0700,
		sys:  "not a stat_t",
	}
	got := evaluateDirSafety(info, nil, 1000)
	if got != dirSafeOK {
		t.Errorf("got %v, want dirSafeOK (unknown Sys shape)", got)
	}
}

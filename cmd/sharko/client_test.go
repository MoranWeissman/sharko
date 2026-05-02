package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

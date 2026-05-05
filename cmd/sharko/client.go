package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

// SharkoConfig holds CLI configuration (~/.sharko/config).
type SharkoConfig struct {
	Server string `yaml:"server"`
	Token  string `yaml:"token"`
}

// configHomeWarned ensures the "$HOME not set" warning is only printed once
// per CLI invocation, even when both load + save call configDir.
var configHomeWarned bool

// configDir returns the directory where the CLI config lives.
//
// Resolution order:
//  1. SHARKO_CONFIG_DIR — explicit override (used by tests and constrained
//     environments). Bypasses every safety check below — operator opted in.
//     A leading "~/" or bare "~" is expanded to the user's home directory
//     so values like SHARKO_CONFIG_DIR=~/sharko-test work as a shell user
//     would expect (review L11). When $HOME is missing the literal "~/"
//     is left intact — better to surface the path than silently rewrite it.
//  2. ~/.sharko — the normal case, when $HOME is set to a real user home.
//  3. <os.TempDir()>/.sharko — fallback when $HOME is missing or resolves to
//     an unwritable root path (e.g. inside a container running with no HOME
//     env var, where os.UserHomeDir() can return "" or "/").
//
// The fallback exists so `sharko login` does not crash with
// "mkdir /.sharko: permission denied" when run as a non-root user inside a
// minimal container image. The first time the fallback fires, a one-line
// warning is printed to stderr so the operator notices the unusual
// resolution.
//
// The fallback path itself is NOT inspected here; safety checks happen
// later in resolveSafeConfigDir, which loadConfig/saveConfig call before
// writing. configDir intentionally stays a pure path-resolver so it can be
// composed in tests and downstream tooling without I/O.
func configDir() string {
	if v := os.Getenv("SHARKO_CONFIG_DIR"); v != "" {
		return expandHomeTilde(v)
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" && home != "/" {
		return filepath.Join(home, ".sharko")
	}
	fallback := filepath.Join(os.TempDir(), ".sharko")
	if !configHomeWarned {
		fmt.Fprintf(os.Stderr,
			"warning: $HOME not set, using %s for config storage (set $HOME or SHARKO_CONFIG_DIR to override)\n",
			fallback)
		configHomeWarned = true
	}
	return fallback
}

// expandHomeTilde rewrites a leading "~/" (or bare "~") in path to the
// caller's home directory. Anything else is returned unchanged. If the
// home directory cannot be resolved, the original path is returned so the
// caller surfaces the literal value rather than silently substituting an
// empty string (review L11).
//
// Only the leading segment is rewritten — embedded tildes (e.g.
// "/foo/~bar") are intentionally untouched to mirror standard shell
// behaviour where only ~ at the start of a word expands.
func expandHomeTilde(path string) string {
	if path == "" {
		return path
	}
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, "~"+string(os.PathSeparator)) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	// Strip the leading "~" — the remainder starts with the OS path
	// separator (or "/" on Unix), so filepath.Join handles the rest.
	return filepath.Join(home, path[1:])
}

// configPath returns the path to the CLI config file.
func configPath() string {
	return filepath.Join(configDir(), "config")
}

// dirSafetyDecision is the result of evaluating a candidate config dir for
// safety. evaluateDirSafety returns one of these values; resolveSafeConfigDir
// translates the decision into the user-visible error.
type dirSafetyDecision int

const (
	// dirSafeOK — the dir exists, is not a symlink, and (on Unix) is owned
	// by the current euid. Caller may use it as-is.
	dirSafeOK dirSafetyDecision = iota
	// dirSafeNotExist — the dir does not exist. Caller is expected to call
	// MkdirAll(0700) which will create it owned by the current user.
	dirSafeNotExist
	// dirUnsafeSymlink — the dir exists and is a symlink. Refuse — an
	// attacker on a shared host could redirect token writes.
	dirUnsafeSymlink
	// dirUnsafeWrongOwner — the dir exists and is owned by a different
	// uid than the current euid. Refuse — another user could read or
	// replace the token.
	dirUnsafeWrongOwner
	// dirUnsafeStatError — Lstat failed with an error other than
	// os.ErrNotExist. Refuse rather than guess.
	dirUnsafeStatError
)

// evaluateDirSafety inspects info (typically from os.Lstat) and decides
// whether the dir is safe to use as the CLI config directory.
//
// Decision matrix:
//
//   - statErr is os.ErrNotExist        → dirSafeNotExist
//   - statErr is any other error       → dirUnsafeStatError
//   - info.Mode() has ModeSymlink set  → dirUnsafeSymlink
//   - on Windows                       → dirSafeOK (TempDir is per-user;
//     ownership check via *syscall.Stat_t is not portable)
//   - info.Sys() is *syscall.Stat_t with Uid != currentEUID → dirUnsafeWrongOwner
//   - otherwise                        → dirSafeOK
//
// This helper is deliberately I/O-free — it operates purely on the
// (info, statErr, currentEUID) tuple. That makes the wrong-owner branch
// testable without actually being root: tests construct a mockFileInfo
// whose Sys() returns a *syscall.Stat_t with a chosen Uid.
func evaluateDirSafety(info os.FileInfo, statErr error, currentEUID int) dirSafetyDecision {
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return dirSafeNotExist
		}
		return dirUnsafeStatError
	}
	if info == nil {
		// Defensive: a nil info with nil statErr should never happen, but
		// treat it as unsafe rather than panic.
		return dirUnsafeStatError
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return dirUnsafeSymlink
	}
	if runtime.GOOS == "windows" {
		// TempDir on Windows is C:\Users\<user>\AppData\Local\Temp —
		// already per-user. The *syscall.Stat_t shape doesn't apply.
		return dirSafeOK
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		// Sys() returned something we don't understand. Be conservative —
		// the symlink check passed, but we can't prove ownership, so
		// allow it. The realistic case where Sys() is not *syscall.Stat_t
		// on Linux/macOS is a custom os.FileInfo (e.g. an embedded FS) —
		// not the os.Lstat output we get from configDir().
		return dirSafeOK
	}
	if int(stat.Uid) != currentEUID {
		return dirUnsafeWrongOwner
	}
	return dirSafeOK
}

// resolveSafeConfigDir returns dir after applying TOCTOU/symlink-squatting
// guards. It is a no-op for SHARKO_CONFIG_DIR overrides (the operator opted
// in explicitly) but enforces ownership + non-symlink for the os.TempDir()
// fallback, which is otherwise vulnerable on shared hosts (CI runners,
// dev servers, multi-tenant containers): an attacker who pre-creates
// /tmp/.sharko or symlinks it can capture the bearer token at write time
// because os.MkdirAll is a no-op on existing dirs and os.WriteFile follows
// symlinks (review finding H1).
//
// Decision logic is delegated to evaluateDirSafety so the wrong-owner branch
// can be unit-tested without root privileges. resolveSafeConfigDir handles
// only the SHARKO_CONFIG_DIR override and the user-visible error formatting.
//
// On Windows the ownership check is skipped (TempDir is per-user and the
// shared-squat threat is not the same shape) but the symlink check still
// runs.
func resolveSafeConfigDir(dir string) (string, error) {
	if os.Getenv("SHARKO_CONFIG_DIR") != "" {
		return dir, nil
	}
	info, statErr := os.Lstat(dir)
	switch evaluateDirSafety(info, statErr, os.Geteuid()) {
	case dirSafeOK, dirSafeNotExist:
		return dir, nil
	case dirUnsafeSymlink:
		return "", fmt.Errorf(
			"refusing to use config dir at %s: is a symlink "+
				"(security risk on shared hosts where another user could redirect token writes). "+
				"Set SHARKO_CONFIG_DIR to override.",
			dir)
	case dirUnsafeWrongOwner:
		// We re-derive the uid here purely for the error message — the
		// safety decision was already made above.
		var ownerUID int = -1
		if info != nil {
			if stat, ok := info.Sys().(*syscall.Stat_t); ok {
				ownerUID = int(stat.Uid)
			}
		}
		return "", fmt.Errorf(
			"refusing to use config dir at %s: owned by uid %d but current process is uid %d "+
				"(security risk on shared hosts where another user could read token writes). "+
				"Set SHARKO_CONFIG_DIR to override.",
			dir, ownerUID, os.Geteuid())
	case dirUnsafeStatError:
		return "", fmt.Errorf("inspecting config directory %s: %w. Set SHARKO_CONFIG_DIR to override.", dir, statErr)
	default:
		// Unreachable — evaluateDirSafety only returns the values above.
		return "", fmt.Errorf("internal: unrecognised dir safety decision for %s", dir)
	}
}

// loadConfig reads the CLI config file.
func loadConfig() (*SharkoConfig, error) {
	dir, err := resolveSafeConfigDir(configDir())
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "config")

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config not found: run 'sharko login' first")
	}

	var cfg SharkoConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config file: %w", err)
	}
	return &cfg, nil
}

// saveConfig writes the CLI config file.
func saveConfig(cfg *SharkoConfig) error {
	dir, err := resolveSafeConfigDir(configDir())
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "config")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("cannot create config directory %s: %w", dir, err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("cannot marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("cannot write config: %w", err)
	}
	return nil
}

// buildHTTPClient creates an HTTP client with a 15-second timeout.
// If insecure is true, TLS certificate verification is skipped.
func buildHTTPClient(insecure bool) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: transport,
	}
}

// apiRequest sends an authenticated HTTP request to the Sharko server.
// Returns the response body bytes and status code.
//
// Server URL precedence (V124-3.5 / BUG-010): the global --server flag, when
// set, overrides the saved config's server URL. This lets users run
// `sharko list-clusters --server URL` against a different server than the
// one they're logged into without rewriting ~/.sharko/config. The token is
// always taken from the saved config — --server is a URL override, not a
// new auth context.
func apiRequest(method, path string, body interface{}) ([]byte, int, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, 0, err
	}
	if cfg.Token == "" {
		return nil, 0, fmt.Errorf("not authenticated — run 'sharko login' first")
	}

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("cannot marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	server := effectiveServer(cfg.Server)
	url := server + path
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("cannot create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Content-Type", "application/json")

	insecure, _ := rootCmd.PersistentFlags().GetBool("insecure")
	client := buildHTTPClient(insecure)
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("cannot read response: %w", err)
	}

	return respBody, resp.StatusCode, nil
}

// apiGet is a convenience wrapper for GET requests.
func apiGet(path string) ([]byte, int, error) {
	return apiRequest(http.MethodGet, path, nil)
}

// apiPost is a convenience wrapper for POST requests.
func apiPost(path string, body interface{}) ([]byte, int, error) {
	return apiRequest(http.MethodPost, path, body)
}

// apiPatch is a convenience wrapper for PATCH requests.
func apiPatch(path string, body interface{}) ([]byte, int, error) {
	return apiRequest(http.MethodPatch, path, body)
}

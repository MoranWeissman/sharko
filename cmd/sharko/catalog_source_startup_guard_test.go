package main

// catalog_source_startup_guard_test.go — BF16: the built binary, on the real
// startup path, never prints a rejected catalog source address.
//
// Why the built binary and not an in-process call: the demonstrated leak was
// the DOUBLED stderr print — cobra prints the RunE error, and main
// (cmd/sharko/root.go) prints it again before os.Exit(1). Only running the
// real binary exercises both prints and the real exit code. In Kubernetes a
// crash-looping pod reprints this on every restart, which is why the address
// (whose path can carry an auth token, per the documented private-catalog
// form) must never be in it.
//
// Probe discipline: the positive control runs FIRST — the probe must find a
// planted copy of the sentinel in the same captured output before any
// absence is claimed. A probe that cannot find a planted value has proved
// nothing.
//
// Determinism: no case here needs DNS. The leading valid source is a
// literal public IP (203.0.113.7, the TEST-NET-3 documentation range), so
// the SSRF check classifies it without a lookup; the rejected sources are a
// malformed URL, an http:// scheme, and the magic "localhost" hostname —
// all decided before any resolver is asked.

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// startupSentinel is the synthetic token planted in the path of every
// rejected address, where the documented private-catalog form puts it.
// Synthetic only. The ":" gives it a percent-encoded form (%3A) distinct
// from the raw form so the probe can tell them apart.
const startupSentinel = "SYNTHtok:ee55ff66aa77bb88"

var (
	buildBinaryOnce sync.Once
	builtBinaryPath string
	buildBinaryErr  error
)

// buildSharkoBinary builds the sharko binary ONCE per test process, in the
// normal inherited environment (never with a rewritten HOME — that would
// relocate Go's module cache), and returns its path.
func buildSharkoBinary(t *testing.T) string {
	t.Helper()
	buildBinaryOnce.Do(func() {
		dir, err := os.MkdirTemp("", "sharko-bf16-bin-*")
		if err != nil {
			buildBinaryErr = err
			return
		}
		builtBinaryPath = filepath.Join(dir, "sharko")
		cmd := exec.Command("go", "build", "-o", builtBinaryPath, ".")
		cmd.Env = os.Environ() // normal environment for the BUILD
		if out, err := cmd.CombinedOutput(); err != nil {
			buildBinaryErr = fmt.Errorf("go build failed: %v\n%s", err, out)
		}
	})
	if buildBinaryErr != nil {
		t.Fatalf("could not build the sharko binary: %v", buildBinaryErr)
	}
	return builtBinaryPath
}

// startupSentinelForms is every shape of the sentinel the probe scans for:
// raw, percent-encoded (query and path flavors), and every 12-byte window
// of the raw form (partial echoes).
func startupSentinelForms() map[string]string {
	forms := map[string]string{
		"raw":           startupSentinel,
		"query_escaped": url.QueryEscape(startupSentinel),
		"path_escaped":  url.PathEscape(startupSentinel),
	}
	const window = 12
	for i := 0; i+window <= len(startupSentinel); i++ {
		forms[fmt.Sprintf("partial_%d", i)] = startupSentinel[i : i+window]
	}
	return forms
}

// probeStartupOutput returns the names of every sentinel form found.
func probeStartupOutput(text string) []string {
	var found []string
	for name, form := range startupSentinelForms() {
		if strings.Contains(text, form) {
			found = append(found, name)
		}
	}
	return found
}

// runStartupRejection runs `sharko serve --demo` with the given
// SHARKO_CATALOG_URLS value in a fully isolated environment and returns
// (stdout, stderr, exitCode). It fails the test BEFORE execution if the
// isolation is not in place — a probe that could read the real
// ~/.sharko/config has no business running.
func runStartupRejection(t *testing.T, catalogURLs string) (string, string, int) {
	t.Helper()
	bin := buildSharkoBinary(t)

	cfgDir := filepath.Join(t.TempDir(), "sharko-config")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("could not create isolated config dir: %v", err)
	}
	homeDir := t.TempDir()

	// The child environment is built from scratch — never inherited — so no
	// SHARKO_* value from the developer's shell can reach the probe, and
	// the run is isolated from the real user home.
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + homeDir,
		"TMPDIR=" + os.TempDir(),
		"KUBECONFIG=/nonexistent",
		"SHARKO_CONFIG_DIR=" + cfgDir,
		"SHARKO_CATALOG_URLS=" + catalogURLs,
	}

	// Assert the isolation BEFORE the command runs.
	isolated := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "SHARKO_CONFIG_DIR=") && strings.Contains(kv, cfgDir) {
			isolated = true
		}
	}
	if !isolated {
		t.Fatal("refusing to run: SHARKO_CONFIG_DIR is not pinned to an isolated directory")
	}
	if fi, err := os.Stat(cfgDir); err != nil || !fi.IsDir() {
		t.Fatalf("refusing to run: isolated config dir is not usable: %v", err)
	}

	cmd := exec.Command(bin, "serve", "--demo")
	cmd.Env = env
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatalf("could not start the binary: %v", err)
	}
	go func() { done <- cmd.Wait() }()
	select {
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("the binary did not exit — a rejected catalog source must refuse to start.\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	case err := <-done:
		exitCode := 0
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if err != nil {
			t.Fatalf("unexpected run error: %v", err)
		}
		return stdout.String(), stderr.String(), exitCode
	}
	return "", "", -1 // unreachable
}

// startupRejectionClasses are the three reproduced rejection classes, each
// configured as the 2ND source after a valid one so the position in the
// message is meaningful.
func startupRejectionClasses() []struct {
	name       string
	badURL     string
	wantReason string
} {
	return []struct {
		name       string
		badURL     string
		wantReason string
	}{
		{
			name:       "malformed_url",
			badURL:     "https://catalogs.example.com/private/" + startupSentinel + "/%zz-bad",
			wantReason: "not a well-formed web address",
		},
		{
			name:       "scheme_not_allowed",
			badURL:     "http://catalogs.example.com/private/" + startupSentinel + "/catalog.yaml",
			wantReason: `scheme "http" is not allowed`,
		},
		{
			name:       "loopback_host",
			badURL:     "https://localhost/private/" + startupSentinel + "/catalog.yaml",
			wantReason: "loopback",
		},
	}
}

// TestStartupRejection_BothPrintsNeverCarryTheAddress is the BF16 guard:
// for each of the three reproduced rejection classes, the built binary
// refuses to start (exit 1), prints the rejection exactly twice on stderr
// (cobra + main), and neither print — nor anything else on either stream —
// carries the configured address in any form.
func TestStartupRejection_BothPrintsNeverCarryTheAddress(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the real binary; skipped in -short mode")
	}
	const leadingValid = "https://203.0.113.7/catalog.yaml"

	for _, tc := range startupRejectionClasses() {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, exitCode := runStartupRejection(t, leadingValid+","+tc.badURL)
			combined := stdout + "\n" + stderr

			// POSITIVE CONTROL FIRST: the probe must find a planted copy
			// of the sentinel in this exact captured output before any
			// absence below means anything.
			planted := combined + "\nplanted: " + startupSentinel
			control := probeStartupOutput(planted)
			controlHasRaw := false
			for _, name := range control {
				if name == "raw" {
					controlHasRaw = true
				}
			}
			if !controlHasRaw {
				t.Fatalf("POSITIVE CONTROL FAILED: the probe cannot find a planted sentinel in the captured output — every absence claim below is void (found %v)", control)
			}

			// Behaviour unchanged: Sharko still refuses to start.
			if exitCode != 1 {
				t.Errorf("exit code = %d, want exactly 1 (refuse to start)", exitCode)
			}

			// BOTH prints happen, and only those two: cobra's "Error: ..."
			// line and main's second print. The count is EXACT — one print
			// would mean the doubled-print path changed shape under this
			// test's feet, and more than two would mean a new print
			// appeared unreviewed.
			marker := "load catalog sources from env: SHARKO_CATALOG_URLS:"
			if got := strings.Count(stderr, marker); got != 2 {
				t.Errorf("rejection appears %d times on stderr, want exactly 2 (cobra + main)\nstderr:\n%s", got, stderr)
			}

			// The address never appears — not raw, not percent-encoded,
			// not partial — on either stream.
			if found := probeStartupOutput(combined); len(found) != 0 {
				t.Errorf("the configured address leaked into startup output (forms %v)\nstdout:\n%s\nstderr:\n%s", found, stdout, stderr)
			}

			// The message still tells the operator everything they need:
			// the setting, the constant identity, the position, a reason.
			for _, want := range []string{
				"SHARKO_CATALOG_URLS",
				"(redacted)",
				"the 2nd source",
				tc.wantReason,
			} {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr lost the operator-facing fragment %q\nstderr:\n%s", want, stderr)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Credential-shaped scheme (BF16 follow-up), through the built binary.
//
// url.Parse treats everything before the first colon as the scheme whenever
// it fits the scheme character set, so a credential pasted in the
// historically common shape TOKEN:x-oauth-basic@host/path parses with the
// TOKEN as the scheme — and the old rejection message echoed it verbatim,
// on both stderr prints, on every restart of a crash-looping pod. This
// guard runs the real binary and proves neither print carries the token in
// any derived form.
// ---------------------------------------------------------------------------

// credSchemeStartupSentinel is the synthetic token in the scheme slot.
// Purely alphanumeric on purpose — that is the class that parses as a
// scheme and was echoed before the fix. Synthetic only.
const credSchemeStartupSentinel = "zq7nSYNTHtok3ee55ff66aa77bb88xk2m"

// credSchemeStartupForms is every derived form of the sentinel: raw,
// lowercase, percent-encoded and double-encoded, base64 standard and
// URL-safe with and without padding, hex, and every first-N / last-N
// partial for N from 3 to 12.
func credSchemeStartupForms() map[string]string {
	s := credSchemeStartupSentinel
	b := []byte(s)
	forms := map[string]string{
		"raw":            s,
		"lowercase":      strings.ToLower(s),
		"percent":        url.QueryEscape(s),
		"double_percent": url.QueryEscape(url.QueryEscape(s)),
		"b64_std":        base64.StdEncoding.EncodeToString(b),
		"b64_std_nopad":  base64.RawStdEncoding.EncodeToString(b),
		"b64_url":        base64.URLEncoding.EncodeToString(b),
		"b64_url_nopad":  base64.RawURLEncoding.EncodeToString(b),
		"hex":            hex.EncodeToString(b),
	}
	for n := 3; n <= 12 && n <= len(s); n++ {
		forms[fmt.Sprintf("first_%d", n)] = s[:n]
		forms[fmt.Sprintf("last_%d", n)] = s[len(s)-n:]
	}
	return forms
}

// probeCredSchemeStartup returns the names of every derived form found.
func probeCredSchemeStartup(text string) []string {
	var found []string
	for name, form := range credSchemeStartupForms() {
		if strings.Contains(text, form) {
			found = append(found, name)
		}
	}
	return found
}

// TestStartupRejection_CredentialShapedScheme_BothPrintsClean runs the
// built binary with the reproduced credential shape as the 2nd source and
// asserts: exit 1, the rejection printed exactly twice on stderr (cobra +
// main), and no derived form of the token on either stream — with the
// positive control checked FIRST.
func TestStartupRejection_CredentialShapedScheme_BothPrintsClean(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the real binary; skipped in -short mode")
	}
	const leadingValid = "https://203.0.113.7/catalog.yaml"
	credURL := credSchemeStartupSentinel + ":x-oauth-basic@github.com/acme/catalog.yaml"

	stdout, stderr, exitCode := runStartupRejection(t, leadingValid+","+credURL)
	combined := stdout + "\n" + stderr

	// POSITIVE CONTROL FIRST: the probe must find planted copies (raw AND
	// hex) of the sentinel in this exact captured output before any
	// absence below means anything.
	planted := combined + "\nplanted-raw: " + credSchemeStartupSentinel +
		"\nplanted-hex: " + hex.EncodeToString([]byte(credSchemeStartupSentinel))
	control := probeCredSchemeStartup(planted)
	controlHasRaw, controlHasHex := false, false
	for _, name := range control {
		if name == "raw" {
			controlHasRaw = true
		}
		if name == "hex" {
			controlHasHex = true
		}
	}
	if !controlHasRaw || !controlHasHex {
		t.Fatalf("POSITIVE CONTROL FAILED: the probe cannot find a planted sentinel in the captured output (raw=%v hex=%v, found=%v) — every absence claim below is void",
			controlHasRaw, controlHasHex, control)
	}

	// Behaviour unchanged: Sharko still refuses to start.
	if exitCode != 1 {
		t.Errorf("exit code = %d, want exactly 1 (refuse to start)", exitCode)
	}

	// BOTH prints happen, and only those two.
	marker := "load catalog sources from env: SHARKO_CATALOG_URLS:"
	if got := strings.Count(stderr, marker); got != 2 {
		t.Errorf("rejection appears %d times on stderr, want exactly 2 (cobra + main)\nstderr:\n%s", got, stderr)
	}

	// The token never appears — in ANY derived form — on either stream.
	if found := probeCredSchemeStartup(combined); len(found) != 0 {
		t.Errorf("the credential-shaped scheme leaked into startup output (forms %v)\nstdout:\n%s\nstderr:\n%s", found, stdout, stderr)
	}
	// The rest of the supplied address stays out too, and no scheme is
	// named at all for an off-allowlist scheme.
	for _, part := range []string{"x-oauth-basic", "github.com", "acme", `scheme "`} {
		if strings.Contains(combined, part) {
			t.Errorf("startup output carries the supplied fragment %q\nstdout:\n%s\nstderr:\n%s", part, stdout, stderr)
		}
	}

	// The message still tells the operator everything they need.
	for _, want := range []string{
		"SHARKO_CATALOG_URLS",
		"(redacted)",
		"the 2nd source",
		"uses a scheme other than https",
		"https://",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lost the operator-facing fragment %q\nstderr:\n%s", want, stderr)
		}
	}
}

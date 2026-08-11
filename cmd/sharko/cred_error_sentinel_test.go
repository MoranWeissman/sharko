package main

// cred_error_sentinel_test.go — the CLI half of the credential-error hotfix.
//
// The CLI does not sanitize anything, and it should not: it PRINTS what the
// server sends. `sharko test-cluster` reads error_message out of the response
// and writes it straight to stdout, so whatever the API puts in that field is
// what an operator sees on their terminal and pastes into a ticket.
//
// That makes the CLI a real public boundary even though the fix for it lives in
// internal/api. This test pins the consequence from the CLI's own side: given the
// response shape the fixed API now returns, nothing that looks like a
// credentials-backend error's text reaches stdout. If somebody later widens the
// API to pass raw provider text back through error_message, this fails.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// cliCredSentinel is unique to this file.
const cliCredSentinel = "TX9L-cli-cred-sentinel-6b3n8d-never-printed-d2f7"

// assertNoCLICredSentinel sweeps stdout for the sentinel in every form a leak
// could take.
func assertNoCLICredSentinel(t *testing.T, where, text string) {
	t.Helper()
	sum := sha256.Sum256([]byte(cliCredSentinel))
	n := len(cliCredSentinel)

	for name, form := range map[string]string{
		"the value itself":    cliCredSentinel,
		"base64 (std)":        base64.StdEncoding.EncodeToString([]byte(cliCredSentinel)),
		"base64 (raw std)":    base64.RawStdEncoding.EncodeToString([]byte(cliCredSentinel)),
		"base64 (url)":        base64.URLEncoding.EncodeToString([]byte(cliCredSentinel)),
		"base64 (raw url)":    base64.RawURLEncoding.EncodeToString([]byte(cliCredSentinel)),
		"SHA-256 hex":         hex.EncodeToString(sum[:]),
		"SHA-256 base64":      base64.StdEncoding.EncodeToString(sum[:]),
		"first 8 characters":  cliCredSentinel[:8],
		"first 16 characters": cliCredSentinel[:16],
		"last 8 characters":   cliCredSentinel[n-8:],
		"last 16 characters":  cliCredSentinel[n-16:],
	} {
		if strings.Contains(text, form) {
			t.Errorf("%s carries %s of the credentials error text (%q)\n\noutput:\n%s", where, name, form, text)
		}
	}

	for _, shape := range []string{
		fmt.Sprintf("%d bytes", n), fmt.Sprintf("%d chars", n), fmt.Sprintf("%d characters", n),
		fmt.Sprintf(`"length":%d`, n), fmt.Sprintf(`"len":%d`, n),
		fmt.Sprintf(`"bytes":%d`, n), fmt.Sprintf(`"size":%d`, n),
		fmt.Sprintf("length=%d", n), fmt.Sprintf("len=%d", n), fmt.Sprintf("bytes=%d", n),
	} {
		if strings.Contains(text, shape) {
			t.Errorf("%s carries the error's byte length (%q) — a length narrows a guess", where, shape)
		}
	}

	for _, ch := range []string{"*", "•", "x", "●"} {
		for _, l := range []int{n - 1, n, n + 1} {
			if strings.Contains(text, strings.Repeat(ch, l)) {
				t.Errorf("%s carries a mask whose length tracks the error (%d of %q)", where, l, ch)
			}
		}
	}
}

// TestCLI_TestCluster_PrintsTheSafeSentenceNotProviderText drives the real
// `sharko test-cluster` command against the response shape the FIXED API returns
// for a credentials-backend failure.
func TestCLI_TestCluster_PrintsTheSafeSentenceNotProviderText(t *testing.T) {
	// This is what the fixed API sends: Sharko's own fixed sentence, no provider
	// text anywhere in the payload.
	const safeSentence = "Sharko could not read this cluster's sign-in details from the configured credentials source. The server log for this request id says which step failed."

	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/clusters/prod-eu/test" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":          "prod-eu",
			"reachable":     false,
			"success":       false,
			"stage":         "credentials",
			"error_code":    "ERR_AUTH",
			"error_message": safeSentence,
			"steps": []map[string]string{
				{"name": "Fetch credentials", "status": "fail", "detail": safeSentence},
			},
		})
	})

	resetFlags(testClusterCmd)
	out, err := captureStdoutT(t, func() error {
		return testClusterCmd.RunE(testClusterCmd, []string{"prod-eu"})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertNoCLICredSentinel(t, "the `sharko test-cluster` stdout", out)

	// And the operator is still told something. A silent CLI would pass the sweep
	// above and be worse than useless.
	if !strings.Contains(out, "Reachable: no") {
		t.Errorf("the CLI no longer reports the cluster as unreachable; output:\n%s", out)
	}
	if !strings.Contains(out, "sign-in details") {
		t.Errorf("the CLI printed no reason at all; output:\n%s", out)
	}
}

// TestCLI_TestCluster_WouldPrintWhateverTheAPISends is the honest half of this
// file, and it is why the fix has to live in the API.
//
// The CLI is a pass-through by design. Fed a response that DOES carry provider
// text, it prints it — so this test asserts that, rather than pretending the CLI
// has a guard it does not have. It is the reason the boundary work is in
// internal/api: there is nothing downstream that would catch a mistake there.
func TestCLI_TestCluster_WouldPrintWhateverTheAPISends(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": "prod-eu", "reachable": false,
			"error_message": "AccessDenied: " + cliCredSentinel,
		})
	})

	resetFlags(testClusterCmd)
	out, err := captureStdoutT(t, func() error {
		return testClusterCmd.RunE(testClusterCmd, []string{"prod-eu"})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, cliCredSentinel) {
		t.Errorf(`the CLI did NOT print what the server sent, which means it has started filtering.

That is not necessarily wrong, but it changes where the guarantee lives — update this test and say so, rather than leaving two half-guards nobody can reason about. output:
%s`, out)
	}
}

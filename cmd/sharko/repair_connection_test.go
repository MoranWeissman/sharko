package main

// repair_connection_test.go — R3-3 and R3-6's CLI surface.
//
// The CLI is a door onto the public endpoint, so what these tests pin is that it
// uses that door correctly: it checks before it writes, it sends back the commit
// it was shown, it never prints a credential, and it refuses rather than guessing
// when the commit is unknown.

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

// cliRepairSentinel is a made-up credential value used only in this file. If the
// CLI ever printed a value, this is what would show up.
const cliRepairSentinel = "p3j8dn5qcli-repair-sentinel-do-not-copy"

// TestRepairConnection_ChecksFirstThenRepairsWithTheReviewedCommit is the shape
// of the whole command: the read-only check runs first, and the repair carries
// that check's own commit so what gets written is what was shown.
func TestRepairConnection_ChecksFirstThenRepairsWithTheReviewedCommit(t *testing.T) {
	const commit = "1234567890abcdef1234567890abcdef12345678"
	var sawCheck, sawRepair bool
	var repairQuery string

	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/clusters/prod-eu/connection-comparison":
			sawCheck = true
			if sawRepair {
				t.Error("the repair was sent before the check ran — the check is what supplies the reviewed commit")
			}
			writeCLIJSON(t, w, map[string]interface{}{
				"cluster":         "prod-eu",
				"status":          "out_of_sync",
				"scope":           "full",
				"ownership_mode":  "backend_stored_credentials",
				"branch":          "main",
				"compared_commit": commit,
				"differences": []map[string]interface{}{
					{"path": "data.server", "status": "different", "expected": "https://right.invalid", "live": "https://wrong.invalid"},
					{"path": "data.config", "status": "different", "sensitive": true},
				},
				"not_checked":         []interface{}{},
				"repair_available":    true,
				"repair_scope":        "full_connection",
				"checked_at":          "2026-08-12T00:00:00Z",
				"checked_field_count": 9,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/clusters/prod-eu/connection-repair":
			sawRepair = true
			if !sawCheck {
				t.Error("the repair ran without a check first")
			}
			repairQuery = r.URL.RawQuery
			writeCLIJSON(t, w, map[string]interface{}{
				"cluster":            "prod-eu",
				"repaired":           true,
				"scope_applied":      "full_connection",
				"fields_repaired":    []string{"data.config", "data.server"},
				"branch":             "main",
				"repaired_at_commit": commit,
				"repaired_at":        "2026-08-12T00:00:01Z",
				"message":            "Sharko rewrote 2 part(s) of this cluster's connection.",
				"comparison": map[string]interface{}{
					"cluster":         "prod-eu",
					"status":          "synced",
					"scope":           "full",
					"branch":          "main",
					"compared_commit": commit,
					"differences":     []interface{}{},
					"not_checked":     []interface{}{},
				},
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	resetFlags(repairConnectionCmd)
	if err := repairConnectionCmd.Flags().Set("yes", "true"); err != nil {
		t.Fatalf("set --yes: %v", err)
	}

	out, err := captureStdoutT(t, func() error {
		return repairConnectionCmd.RunE(repairConnectionCmd, []string{"prod-eu"})
	})
	if err != nil {
		t.Fatalf("repair-connection: %v (output=%s)", err, out)
	}

	if !sawCheck || !sawRepair {
		t.Fatalf("check ran = %v, repair ran = %v; both must happen", sawCheck, sawRepair)
	}
	if !strings.Contains(repairQuery, "reviewed_commit="+commit) {
		t.Errorf(`the repair did not send the commit the check reported (query = %q).

Without it the server cannot tell whether the caller reviewed what is about to be written.`, repairQuery)
	}

	// It shows what was wrong, then what it did, then where things stand.
	// The closing status word is the ruled wording (ruling b, 2026-08-19):
	// Git defines the connection, so the CLI says so. The old phrase is
	// banned below.
	for _, want := range []string{
		"Connection check for prod-eu",
		"data.server",
		"data.config",
		"After the repair:",
		"matches the Git-defined connection",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(strings.ToLower(out), "sharko intends") {
		t.Errorf("the CLI still prints the banned phrase\n---\n%s", out)
	}
}

// TestRepairConnection_NeverPrintsACredentialValue: the server never sends a
// value, and the CLI never invents one. A sensitive field is named with its state
// and nothing more.
func TestRepairConnection_NeverPrintsACredentialValue(t *testing.T) {
	const commit = "abcdefabcdefabcdefabcdefabcdefabcdefabcd"

	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/clusters/prod-eu/connection-comparison":
			writeCLIJSON(t, w, map[string]interface{}{
				"cluster": "prod-eu", "status": "out_of_sync", "scope": "full",
				"branch": "main", "compared_commit": commit,
				"differences": []map[string]interface{}{
					{"path": "data.config", "status": "different", "sensitive": true},
				},
				"not_checked": []interface{}{}, "repair_available": true, "repair_scope": "full_connection",
			})
		case "/api/v1/clusters/prod-eu/connection-repair":
			writeCLIJSON(t, w, map[string]interface{}{
				"cluster": "prod-eu", "repaired": true, "scope_applied": "full_connection",
				"fields_repaired": []string{"data.config"},
				"branch":          "main", "repaired_at_commit": commit,
				"message": "done",
				"comparison": map[string]interface{}{
					"cluster": "prod-eu", "status": "limited", "scope": "limited",
					"branch": "main", "compared_commit": commit,
					"differences": []interface{}{}, "not_checked": []interface{}{},
				},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})

	resetFlags(repairConnectionCmd)
	_ = repairConnectionCmd.Flags().Set("yes", "true")

	out, err := captureStdoutT(t, func() error {
		return repairConnectionCmd.RunE(repairConnectionCmd, []string{"prod-eu"})
	})
	if err != nil {
		t.Fatalf("repair-connection: %v", err)
	}

	assertNoCLIRepairSentinel(t, "the CLI's printed output", out)
	// The sensitive field is named, and it says the value is not shown.
	if !strings.Contains(out, "data.config") {
		t.Errorf("the sensitive field was not named at all:\n%s", out)
	}
	if !strings.Contains(out, "value not shown") {
		t.Errorf("the output does not say the sign-in details are not shown:\n%s", out)
	}
}

// TestRepairConnection_NeverPrintsAFieldValueEvenWhenTheServerSendsOne is the
// CLI's half of the R3-14 sentinel sweep.
//
// The real server never sends a field value: a sensitive difference carries a
// path, a status and sensitive true and nothing else, and that is proven on the
// server side. This pins the OTHER half — if a future server change, a proxy or a
// hand-written response ever DID carry one, the CLI must not be the thing that
// puts it on somebody's terminal and into their scrollback.
//
// So the fake server here wrongly fills expected and live on both a sensitive
// difference and an ordinary one, in raw, base64, hash and fragment form. None of
// it may be printed.
//
// # What this test deliberately does NOT do
//
// It does not put the sentinel into the server's own message, its not_checked
// reasons, or its field-path list. Those are fields the server fills with its own
// fixed sentences and with field PATHS, and the CLI printing them verbatim is the
// design — TestRepairConnection_ServerRefusalIsPrintedAsIs pins exactly that, so
// the CLI does not invent a second wording for something the server already said
// safely. A test that smuggled a credential into a fixed sentence would be
// testing the fake server, not the CLI.
func TestRepairConnection_NeverPrintsAFieldValueEvenWhenTheServerSendsOne(t *testing.T) {
	const commit = "cafebabecafebabecafebabecafebabecafebabe"
	sum := sha256.Sum256([]byte(cliRepairSentinel))

	// Every difference wrongly carries both sides, in a different form each time.
	hostile := map[string]interface{}{
		"cluster": "prod-eu", "status": "out_of_sync", "scope": "full",
		"branch": "main", "compared_commit": commit,
		"differences": []map[string]interface{}{
			{
				"path": "data.config", "status": "different", "sensitive": true,
				"expected": cliRepairSentinel,
				"live":     base64.StdEncoding.EncodeToString([]byte(cliRepairSentinel)),
			},
			{
				"path": "data.server", "status": "different",
				"expected": hex.EncodeToString(sum[:]),
				"live":     cliRepairSentinel[:16],
			},
			{
				"path": "metadata.labels[datadog]", "status": "different",
				"expected": cliRepairSentinel[len(cliRepairSentinel)-16:],
				"live":     base64.RawURLEncoding.EncodeToString([]byte(cliRepairSentinel)),
			},
		},
		"not_checked":      []interface{}{},
		"repair_available": true, "repair_scope": "full_connection",
	}

	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/clusters/prod-eu/connection-comparison":
			writeCLIJSON(t, w, hostile)
		case "/api/v1/clusters/prod-eu/connection-repair":
			writeCLIJSON(t, w, map[string]interface{}{
				"cluster": "prod-eu", "repaired": true, "scope_applied": "full_connection",
				"fields_repaired": []string{"data.config", "data.server"},
				"branch":          "main", "repaired_at_commit": commit,
				"message":    "Sharko rewrote 2 part(s) of this cluster's connection.",
				"comparison": hostile,
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})

	resetFlags(repairConnectionCmd)
	_ = repairConnectionCmd.Flags().Set("yes", "true")

	out, err := captureStdoutT(t, func() error {
		return repairConnectionCmd.RunE(repairConnectionCmd, []string{"prod-eu"})
	})
	if err != nil {
		t.Fatalf("repair-connection: %v", err)
	}

	assertNoCLIRepairSentinel(t, "the CLI's printed output", out)

	// And it really did print the differences, so the sweep above had something
	// to sweep — a command that printed nothing would pass it for free.
	for _, want := range []string{"data.config", "data.server", "metadata.labels[datadog]"} {
		if !strings.Contains(out, want) {
			t.Errorf("the CLI did not print the difference %q at all, so the value sweep proves nothing:\n%s", want, out)
		}
	}
}

// TestRepairConnection_HasNowhereToHoldAFieldValue is the structural half of the
// test above.
//
// The reason the CLI cannot print a value is not that it is careful — it is that
// its decode structs have no field to put one in. json.Unmarshal drops what a
// struct does not name, so an expected or live value in a response never reaches
// a variable, never mind a fmt.Print. That property is worth pinning on its own:
// somebody adding an Expected string to connectionCheckResult one day to "show a
// bit more" would break it, and this fails the moment they do.
func TestRepairConnection_HasNowhereToHoldAFieldValue(t *testing.T) {
	var check connectionCheckResult
	body := []byte(`{
		"cluster": "prod-eu", "status": "out_of_sync",
		"differences": [{"path": "data.config", "status": "different", "sensitive": true,
		                 "expected": "` + cliRepairSentinel + `", "live": "` + cliRepairSentinel + `"}]
	}`)
	if err := json.Unmarshal(body, &check); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(check.Differences) != 1 {
		t.Fatalf("expected one difference, got %d", len(check.Differences))
	}

	// Whatever the response carried, what the CLI now holds must not contain it.
	// %+v prints every field of the decoded struct, named or not.
	assertNoCLIRepairSentinel(t, "the decoded check result printed with %+v",
		fmt.Sprintf("%+v", check))

	// The same for the repair result, which embeds the comparison.
	var repair connectionRepairResult
	repairBody := []byte(`{
		"cluster": "prod-eu", "repaired": true,
		"comparison": {"differences": [{"path": "data.config", "status": "different",
		                                "sensitive": true, "expected": "` + cliRepairSentinel + `",
		                                "live": "` + cliRepairSentinel + `"}]}
	}`)
	if err := json.Unmarshal(repairBody, &repair); err != nil {
		t.Fatalf("decoding the repair result: %v", err)
	}
	assertNoCLIRepairSentinel(t, "the decoded repair result printed with %+v",
		fmt.Sprintf("%+v", repair))
}

// assertNoCLIRepairSentinel fails when text carries cliRepairSentinel in any
// form, its byte length in a labelled shape, or a mask whose length tracks it.
//
// It mirrors the server-side sweep so the two doors are held to one standard.
func assertNoCLIRepairSentinel(t *testing.T, where, text string) {
	t.Helper()
	sum := sha256.Sum256([]byte(cliRepairSentinel))
	n := len(cliRepairSentinel)

	for _, f := range []string{
		cliRepairSentinel,
		base64.StdEncoding.EncodeToString([]byte(cliRepairSentinel)),
		base64.RawStdEncoding.EncodeToString([]byte(cliRepairSentinel)),
		base64.URLEncoding.EncodeToString([]byte(cliRepairSentinel)),
		base64.RawURLEncoding.EncodeToString([]byte(cliRepairSentinel)),
		hex.EncodeToString(sum[:]),
		base64.StdEncoding.EncodeToString(sum[:]),
		cliRepairSentinel[:8],
		cliRepairSentinel[:16],
		cliRepairSentinel[n-8:],
		cliRepairSentinel[n-16:],
	} {
		if strings.Contains(text, f) {
			t.Errorf("%s carries a form of the credential value (%q)", where, f)
		}
	}
	for _, shape := range []string{
		fmt.Sprintf("%d bytes", n),
		fmt.Sprintf("%d chars", n),
		fmt.Sprintf("%d characters", n),
		fmt.Sprintf("length=%d", n),
		fmt.Sprintf("len=%d", n),
	} {
		if strings.Contains(text, shape) {
			t.Errorf("%s carries the credential's byte length (%q) — a length narrows a guess", where, shape)
		}
	}
	for _, ch := range []string{"*", "•", "x", "●"} {
		for _, l := range []int{n - 1, n, n + 1} {
			if strings.Contains(text, strings.Repeat(ch, l)) {
				t.Errorf("%s carries a mask whose length tracks the credential (%d of %q)", where, l, ch)
			}
		}
	}
}

// TestRepairConnection_RefusesWhenTheCommitIsUnknown is R3-4 at the CLI: a check
// that could not name a commit means no repair is attempted at all.
func TestRepairConnection_RefusesWhenTheCommitIsUnknown(t *testing.T) {
	var sawRepair bool
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/clusters/prod-eu/connection-repair" {
			sawRepair = true
			t.Error("the CLI sent a repair even though no commit could be confirmed")
		}
		writeCLIJSON(t, w, map[string]interface{}{
			"cluster": "prod-eu", "status": "out_of_sync", "scope": "full",
			"branch": "main", "compared_commit": "", // git could not say
			"differences": []map[string]interface{}{
				{"path": "data.server", "status": "different"},
			},
			"not_checked": []interface{}{}, "repair_available": true, "repair_scope": "full_connection",
		})
	})

	resetFlags(repairConnectionCmd)
	_ = repairConnectionCmd.Flags().Set("yes", "true")

	out, err := captureStdoutT(t, func() error {
		return repairConnectionCmd.RunE(repairConnectionCmd, []string{"prod-eu"})
	})
	if err == nil {
		t.Fatal("expected a non-zero exit when no commit could be confirmed")
	}
	if sawRepair {
		t.Error("a repair was attempted with no confirmed commit")
	}
	if !strings.Contains(out, "cannot tell which commit") {
		t.Errorf("the output does not explain why nothing was done:\n%s", out)
	}
}

// TestRepairConnection_CheckOnlyWritesNothing: --check-only looks and stops.
func TestRepairConnection_CheckOnlyWritesNothing(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			t.Fatalf("--check-only sent a %s to %s; it must change nothing", r.Method, r.URL.Path)
		}
		writeCLIJSON(t, w, map[string]interface{}{
			"cluster": "prod-eu", "status": "out_of_sync", "scope": "full",
			"branch": "main", "compared_commit": "aaaabbbbccccdddd",
			"differences": []map[string]interface{}{
				{"path": "data.server", "status": "different"},
			},
			"not_checked": []interface{}{}, "repair_available": true, "repair_scope": "full_connection",
		})
	})

	resetFlags(repairConnectionCmd)
	if err := repairConnectionCmd.Flags().Set("check-only", "true"); err != nil {
		t.Fatalf("set --check-only: %v", err)
	}

	out, err := captureStdoutT(t, func() error {
		return repairConnectionCmd.RunE(repairConnectionCmd, []string{"prod-eu"})
	})
	if err != nil {
		t.Fatalf("--check-only returned an error: %v", err)
	}
	if strings.Contains(out, "Repairing") {
		t.Errorf("--check-only said it was repairing:\n%s", out)
	}
}

// TestRepairConnection_CheckOnlyFailedCheckExitsNonZero is R3-17(b): a check that
// did not finish is a failure whether or not a write was going to follow.
//
// The early `if checkOnly { return nil }` used to sit ABOVE the check_failed
// branch, so `sharko repair-connection X --check-only` printed a failed check and
// exited ZERO. A script reading that exit code got the wrong answer: it could not
// tell "I looked and it was fine" from "I could not look at all". Round 2 fixed
// the ordering for the repair path and left the early return in front of it.
//
// It also proves --check-only still writes nothing on this path: the only request
// that may reach the server is the read.
func TestRepairConnection_CheckOnlyFailedCheckExitsNonZero(t *testing.T) {
	var requests []string

	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		writeCLIJSON(t, w, map[string]interface{}{
			"cluster":        "prod-eu",
			"status":         "check_failed",
			"scope":          "full",
			"failure_reason": "Sharko could not read this cluster's connection from its own cluster, so the check did not finish.",
			"branch":         "main", "compared_commit": "aaaabbbbccccdddd",
			"differences": []interface{}{}, "not_checked": []interface{}{},
			// A failed check withdraws the repair offer (R3-8). The exit code must
			// still be non-zero, and it must NOT come from the
			// "!repair_available → exit zero" branch.
			"repair_available": false, "repair_scope": "none",
		})
	})

	resetFlags(repairConnectionCmd)
	if err := repairConnectionCmd.Flags().Set("check-only", "true"); err != nil {
		t.Fatalf("set --check-only: %v", err)
	}

	out, err := captureStdoutT(t, func() error {
		return repairConnectionCmd.RunE(repairConnectionCmd, []string{"prod-eu"})
	})

	if err == nil {
		t.Fatalf(`--check-only exited ZERO on a check that did not finish.

A script cannot tell "I looked and it was fine" from "I could not look at all", which is the whole reason the exit code exists. Output was:
%s`, out)
	}
	if !strings.Contains(out, "The check did not finish") {
		t.Errorf("the output does not say the check did not finish:\n%s", out)
	}

	// Nothing was written, and nothing but the read was even attempted.
	if len(requests) != 1 {
		t.Errorf("--check-only made %d request(s) (%v); it must make exactly one, the read", len(requests), requests)
	}
	for _, req := range requests {
		if !strings.HasPrefix(req, http.MethodGet+" ") {
			t.Errorf("--check-only sent %q; it must change nothing", req)
		}
	}
}

// TestRepairConnection_CheckOnlyHealthyCheckStillExitsZero is the other side of
// the same fix: the failed-check case must not be fixed by failing everything.
//
// A healthy check with --check-only is a success. It writes nothing and exits
// zero, and it does so even when a repair WOULD have been available — the command
// was asked to look, it looked, and it stopped.
func TestRepairConnection_CheckOnlyHealthyCheckStillExitsZero(t *testing.T) {
	var requests []string

	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		writeCLIJSON(t, w, map[string]interface{}{
			"cluster": "prod-eu", "status": "synced", "scope": "full",
			"branch": "main", "compared_commit": "aaaabbbbccccdddd",
			"differences": []interface{}{}, "not_checked": []interface{}{},
			"repair_available": true, "repair_scope": "full_connection",
		})
	})

	resetFlags(repairConnectionCmd)
	if err := repairConnectionCmd.Flags().Set("check-only", "true"); err != nil {
		t.Fatalf("set --check-only: %v", err)
	}

	out, err := captureStdoutT(t, func() error {
		return repairConnectionCmd.RunE(repairConnectionCmd, []string{"prod-eu"})
	})
	if err != nil {
		t.Fatalf("--check-only on a healthy check exited non-zero: %v (output=%s)", err, out)
	}
	if strings.Contains(out, "Repairing") {
		t.Errorf("--check-only said it was repairing:\n%s", out)
	}
	if len(requests) != 1 {
		t.Errorf("--check-only made %d request(s) (%v); it must make exactly one, the read", len(requests), requests)
	}
}

// TestRepairConnection_DoesNotRepairWhatItMayNotRepair: a connection another tool
// owns comes back with repair_available false, and the CLI stops there.
func TestRepairConnection_DoesNotRepairWhatItMayNotRepair(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			t.Fatal("the CLI attempted a repair on a connection Sharko may not change")
		}
		writeCLIJSON(t, w, map[string]interface{}{
			"cluster": "prod-eu", "status": "ownership_conflict", "scope": "ownership_conflict",
			"ownership_mode": "owned_by_another_tool",
			"limit_reason":   "This cluster's ArgoCD connection is marked as owned by another tool.",
			"branch":         "main", "compared_commit": "aaaabbbb",
			"differences": []interface{}{}, "not_checked": []interface{}{},
			"repair_available": false, "repair_scope": "none",
		})
	})

	resetFlags(repairConnectionCmd)
	_ = repairConnectionCmd.Flags().Set("yes", "true")

	out, err := captureStdoutT(t, func() error {
		return repairConnectionCmd.RunE(repairConnectionCmd, []string{"prod-eu"})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "will not change this connection") {
		t.Errorf("the output does not say Sharko is leaving it alone:\n%s", out)
	}
}

// TestRepairConnection_ServerRefusalIsPrintedAsIs: when the server refuses with a
// 409, the CLI shows the server's own safe sentence rather than inventing a
// second wording for the same thing.
func TestRepairConnection_ServerRefusalIsPrintedAsIs(t *testing.T) {
	const moved = "Your git branch moved while you were looking at this connection, so what you reviewed is not what Sharko would write now."
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": moved})
			return
		}
		writeCLIJSON(t, w, map[string]interface{}{
			"cluster": "prod-eu", "status": "out_of_sync", "scope": "full",
			"branch": "main", "compared_commit": "aaaabbbbccccdddd",
			"differences": []map[string]interface{}{
				{"path": "data.server", "status": "different"},
			},
			"not_checked": []interface{}{}, "repair_available": true, "repair_scope": "full_connection",
		})
	})

	resetFlags(repairConnectionCmd)
	_ = repairConnectionCmd.Flags().Set("yes", "true")

	out, runErr := captureStdoutT(t, func() error {
		return repairConnectionCmd.RunE(repairConnectionCmd, []string{"prod-eu"})
	})
	if runErr == nil {
		t.Fatal("expected a non-zero exit when the server refuses the repair")
	}
	combined := out + runErr.Error()
	if !strings.Contains(combined, "branch moved") {
		t.Errorf("the server's own explanation did not reach the person:\nstdout=%s\nerr=%v", out, runErr)
	}
}

// writeCLIJSON writes a JSON body for the fake server.
func writeCLIJSON(t *testing.T, w http.ResponseWriter, body interface{}) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Fatalf("encoding test response: %v", err)
	}
}

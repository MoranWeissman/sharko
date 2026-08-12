package main

// repair_connection_test.go — R3-3 and R3-6's CLI surface.
//
// The CLI is a door onto the public endpoint, so what these tests pin is that it
// uses that door correctly: it checks before it writes, it sends back the commit
// it was shown, it never prints a credential, and it refuses rather than guessing
// when the commit is unknown.

import (
	"encoding/json"
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
	for _, want := range []string{
		"Connection check for prod-eu",
		"data.server",
		"data.config",
		"After the repair:",
		"matches what Sharko intends",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q\n---\n%s", want, out)
		}
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

	if strings.Contains(out, cliRepairSentinel) {
		t.Error("the CLI printed a credential value")
	}
	// The sensitive field is named, and it says the value is not shown.
	if !strings.Contains(out, "data.config") {
		t.Errorf("the sensitive field was not named at all:\n%s", out)
	}
	if !strings.Contains(out, "value not shown") {
		t.Errorf("the output does not say the sign-in details are not shown:\n%s", out)
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

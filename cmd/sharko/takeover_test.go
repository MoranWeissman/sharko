package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// captureStdout runs fn with os.Stdout replaced by a pipe and returns
// everything written to it, so tests can assert on the human-readable
// output these commands print (there is no other observable surface —
// RunE's return value is only the error).
func captureStdoutT(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	r, w, err := newOSPipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := swapStdout(w)
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	runErr := fn()

	w.Close()
	swapStdout(orig)
	out := <-done
	return out, runErr
}

func TestTakeoverPreflight_HappyPath(t *testing.T) {
	srv := startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/clusters/prod-eu/takeover/preflight" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		report := preflightReport{
			Cluster: "prod-eu",
			Ready:   true,
			Summary: "Looks straightforward.",
			Findings: []preflightFinding{
				{ID: "secret-owner", Title: "Connection owner", Status: "ok", Detail: "Owned by argocd.", WhatToDo: "Nothing to do."},
			},
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(report)
	})
	_ = srv

	resetFlags(takeoverPreflightCmd)
	out, err := captureStdoutT(t, func() error {
		return takeoverPreflightCmd.RunE(takeoverPreflightCmd, []string{"prod-eu"})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "prod-eu") || !strings.Contains(out, "ready") {
		t.Errorf("missing cluster/ready in output: %q", out)
	}
	if !strings.Contains(out, "secret-owner") {
		t.Errorf("missing finding id in output: %q", out)
	}
}

// TestTakeover_AckRoundTrip exercises the full flow: a takeover call
// without --acknowledge gets a 409 naming the warning id, and the same
// call WITH that id passed back succeeds. This is the round trip the v4
// takeover contract is built on (internal/api/clusters_takeover.go
// unacknowledgedFindings) — the CLI has to surface it usably or a warning
// finding becomes a dead end.
func TestTakeover_AckRoundTrip(t *testing.T) {
	report := preflightReport{
		Cluster: "prod-eu",
		Ready:   true,
		Summary: "One warning to read first.",
		Findings: []preflightFinding{
			{ID: "appset-deletion-safety", Title: "ApplicationSet reacts on removal", Status: "warning", Detail: "Something would delete workloads.", WhatToDo: "Read this and acknowledge it."},
		},
	}

	calls := 0
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/clusters/prod-eu/takeover/preflight":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(report)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/clusters/prod-eu/takeover":
			calls++
			var req struct {
				Yes                  bool     `json:"yes"`
				AcknowledgedFindings []string `json:"acknowledged_findings"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			hasAck := false
			for _, id := range req.AcknowledgedFindings {
				if id == "appset-deletion-safety" {
					hasAck = true
				}
			}
			if !hasAck {
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"error":                   "1 warning has not been read yet",
					"unacknowledged_findings": []string{"appset-deletion-safety"},
					"preflight":               report,
				})
				return
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(takeoverResponse{
				Cluster: "prod-eu",
				Status:  "success",
				Server:  "https://prod-eu.example",
				Message: "Sharko now owns prod-eu.",
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	resetFlags(takeoverCmd)
	setFlags(t, takeoverCmd, map[string]string{"yes": "true"})

	// First run: no --acknowledge, expect a 409 surfaced as an error that
	// names the finding id and prints a rerun command.
	out, err := captureStdoutT(t, func() error {
		return takeoverCmd.RunE(takeoverCmd, []string{"prod-eu"})
	})
	if err == nil {
		t.Fatal("expected an error from the unacknowledged-warning 409")
	}
	if !strings.Contains(err.Error(), "warning") {
		t.Errorf("error should mention the warning: %v", err)
	}
	if !strings.Contains(out, "appset-deletion-safety") {
		t.Errorf("output should name the finding id: %q", out)
	}
	if !strings.Contains(out, "sharko takeover prod-eu -y --acknowledge appset-deletion-safety") {
		t.Errorf("output should print the exact rerun command: %q", out)
	}

	// Second run: pass the id back via --acknowledge, expect success.
	resetFlags(takeoverCmd)
	setFlags(t, takeoverCmd, map[string]string{"yes": "true"})
	setRepeatedFlag(t, takeoverCmd, "acknowledge", "appset-deletion-safety")

	out2, err2 := captureStdoutT(t, func() error {
		return takeoverCmd.RunE(takeoverCmd, []string{"prod-eu"})
	})
	if err2 != nil {
		t.Fatalf("unexpected error on acknowledged rerun: %v (output: %s)", err2, out2)
	}
	if !strings.Contains(out2, "Sharko now owns prod-eu") {
		t.Errorf("expected success message, got: %q", out2)
	}
	if calls != 2 {
		t.Errorf("expected 2 POST calls, got %d", calls)
	}
}

func TestTakeover_DryRunPrintsDiff(t *testing.T) {
	report := preflightReport{Cluster: "prod-eu", Ready: true, Summary: "All clear."}
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(report)
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(takeoverResponse{
				Cluster: "prod-eu",
				Status:  "planned",
				DryRun: &cliDryRunResult{
					PRTitle: "sharko: take over cluster prod-eu",
					FilesToWrite: []cliFilePreview{
						{Path: "managed-clusters.yaml", Action: "update", Diff: "+ - name: prod-eu\n+   region: \"\""},
					},
				},
				Message: "Plan: prod-eu joins the fleet.",
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	resetFlags(takeoverCmd)
	setFlags(t, takeoverCmd, map[string]string{"dry-run": "true"})

	out, err := captureStdoutT(t, func() error {
		return takeoverCmd.RunE(takeoverCmd, []string{"prod-eu"})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "managed-clusters.yaml") {
		t.Errorf("expected file path in dry-run output: %q", out)
	}
	if !strings.Contains(out, "name: prod-eu") {
		t.Errorf("expected the diff content itself to be printed, not just the filename: %q", out)
	}
}

func TestDropLegacyLabels_AckRoundTrip(t *testing.T) {
	calls := 0
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/clusters/prod-eu/takeover/legacy-labels/drop" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		calls++
		var req struct {
			AcknowledgedFindings []string `json:"acknowledged_findings"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		acked := false
		for _, id := range req.AcknowledgedFindings {
			if id == "appset:legacy-selector" {
				acked = true
			}
		}
		if !acked {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error":                   "1 warning has not been read yet",
				"unacknowledged_findings": []string{"appset:legacy-selector"},
				"warnings":                []string{"The ApplicationSet \"legacy-selector\" picks clusters using team."},
				"warning_ids":             []string{"appset:legacy-selector"},
				"labels":                  []string{"team"},
			})
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(dropLegacyLabelsResponse{
			Cluster: "prod-eu",
			Status:  "success",
			Removed: []string{"team"},
			Message: "Removed 1 label(s).",
		})
	})

	resetFlags(dropLegacyLabelsCmd)
	setFlags(t, dropLegacyLabelsCmd, map[string]string{"yes": "true"})

	out, err := captureStdoutT(t, func() error {
		return dropLegacyLabelsCmd.RunE(dropLegacyLabelsCmd, []string{"prod-eu"})
	})
	if err == nil {
		t.Fatal("expected error from unacknowledged 409")
	}
	if !strings.Contains(out, "appset:legacy-selector") {
		t.Errorf("expected warning id in output: %q", out)
	}
	if !strings.Contains(out, "sharko drop-legacy-labels prod-eu -y --label team --acknowledge appset:legacy-selector") {
		t.Errorf("expected exact rerun command: %q", out)
	}

	resetFlags(dropLegacyLabelsCmd)
	setFlags(t, dropLegacyLabelsCmd, map[string]string{"yes": "true"})
	setRepeatedFlag(t, dropLegacyLabelsCmd, "acknowledge", "appset:legacy-selector")

	out2, err2 := captureStdoutT(t, func() error {
		return dropLegacyLabelsCmd.RunE(dropLegacyLabelsCmd, []string{"prod-eu"})
	})
	if err2 != nil {
		t.Fatalf("unexpected error: %v (output: %s)", err2, out2)
	}
	if !strings.Contains(out2, "Removed:   team") {
		t.Errorf("expected removed label in output: %q", out2)
	}
	if calls != 2 {
		t.Errorf("expected 2 POST calls, got %d", calls)
	}
}

func TestUnregisterConsequences_HappyPath(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/clusters/prod-eu/unregister/consequences" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(unregisterConsequencesResponse{
			Cluster:              "prod-eu",
			Summary:              "Unregistering prod-eu looks straightforward.",
			ConfirmationRequired: "Send DELETE /api/v1/clusters/prod-eu with yes:true.",
			Consequences: []unregisterConsequence{
				{ID: "git-record", Title: "Its files leave the repo", Severity: "info"},
			},
		})
	})

	out, err := captureStdoutT(t, func() error {
		return unregisterConsequencesCmd.RunE(unregisterConsequencesCmd, []string{"prod-eu"})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Its files leave the repo") {
		t.Errorf("expected consequence title in output: %q", out)
	}
	if !strings.Contains(out, "DELETE /api/v1/clusters/prod-eu") {
		t.Errorf("expected confirmation instructions in output: %q", out)
	}
}

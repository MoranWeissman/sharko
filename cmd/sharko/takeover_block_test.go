package main

// takeover_block_test.go — door-parity pins for the server-side takeover
// block (task #150 lane C). The server decides; these pin that the CLI door
// surfaces a block loudly: non-zero exits, no rerun hint that isn't real,
// and a remove-cluster that can actually confirm.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestTakeoverPreflight_NotReadyExitsNonZero — a blocked preflight must
// exit non-zero so a script or CI gate can branch on it, while still
// printing the full report a human needs.
func TestTakeoverPreflight_NotReadyExitsNonZero(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		report := preflightReport{
			Cluster: "prod-eu",
			Ready:   false,
			Summary: "prod-eu is not ready to take over yet: 1 thing to fix first.",
			Findings: []preflightFinding{
				{ID: "secret-owner", Title: "Who owns this cluster's connection today", Status: "blocked",
					Detail: `The connection is marked as owned by "terraform".`, WhatToDo: "Stop terraform first, then run this check again."},
			},
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(report)
	})

	resetFlags(takeoverPreflightCmd)
	out, err := captureStdoutT(t, func() error {
		return takeoverPreflightCmd.RunE(takeoverPreflightCmd, []string{"prod-eu"})
	})
	if err == nil {
		t.Fatal("a NOT READY preflight must exit non-zero")
	}
	if !strings.Contains(err.Error(), "not ready") {
		t.Errorf("the error must say the cluster is not ready: %v", err)
	}
	if !strings.Contains(out, "NOT READY") {
		t.Errorf("the report header must say NOT READY: %q", out)
	}
	if !strings.Contains(out, "secret-owner") || !strings.Contains(out, "terraform") {
		t.Errorf("the blocked finding and its owner must be in the output: %q", out)
	}
}

// TestTakeover_Blocked409_ExitsNonZeroWithNoRerunHint — the blocked 409
// (empty unacknowledged_findings) must fail the command WITHOUT printing an
// --acknowledge rerun line: there is nothing to acknowledge, something has
// to be fixed first.
func TestTakeover_Blocked409_ExitsNonZeroWithNoRerunHint(t *testing.T) {
	blockedReport := &preflightReport{
		Cluster: "prod-eu",
		Ready:   false,
		Summary: "prod-eu is not ready to take over yet: 1 thing to fix first.",
		Findings: []preflightFinding{
			{ID: "secret-owner", Title: "Who owns this cluster's connection today", Status: "blocked",
				Detail: `The connection is marked as owned by "terraform".`, WhatToDo: "Stop terraform first."},
		},
	}
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/takeover/preflight"):
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(blockedReport)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/takeover"):
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(takeover409Body{
				Error:     "this cluster is not ready to be taken over yet — 1 thing to fix first.",
				Preflight: blockedReport,
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	resetFlags(takeoverCmd)
	if err := takeoverCmd.Flags().Set("yes", "true"); err != nil {
		t.Fatalf("set -y: %v", err)
	}
	out, err := captureStdoutT(t, func() error {
		return takeoverCmd.RunE(takeoverCmd, []string{"prod-eu"})
	})
	if err == nil {
		t.Fatal("a blocked takeover must exit non-zero")
	}
	if !strings.Contains(err.Error(), "not ready") {
		t.Errorf("the error must carry the server's refusal: %v", err)
	}
	if strings.Contains(out, "--acknowledge") {
		t.Errorf("a blocked 409 must not print an --acknowledge rerun hint — there is nothing to acknowledge: %q", out)
	}
}

// TestRemoveCluster_RefusesWithoutYes — the server requires "yes": true, so
// a bare remove-cluster used to just get a 400. The command now refuses
// locally with instructions (consequences first, then -y) and never calls
// the API.
func TestRemoveCluster_RefusesWithoutYes(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("no API call may happen without -y, got: %s %s", r.Method, r.URL.Path)
	})

	resetFlags(removeClusterCmd)
	_, err := captureStdoutT(t, func() error {
		return removeClusterCmd.RunE(removeClusterCmd, []string{"prod-eu"})
	})
	if err == nil {
		t.Fatal("remove-cluster without -y must refuse")
	}
	for _, want := range []string{"unregister-consequences", "-y"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must point at %q: %v", want, err)
		}
	}
}

// TestRemoveCluster_YesSendsConfirmationAndCleanup — with -y the command
// sends the confirmation body the server demands, and --cleanup rides along
// when set.
func TestRemoveCluster_YesSendsConfirmationAndCleanup(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/clusters/prod-eu" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Yes     bool   `json:"yes"`
			Cleanup string `json:"cleanup"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("body does not parse: %v (%q)", err, raw)
		}
		if !body.Yes {
			t.Errorf(`the confirmation body must carry "yes": true, got %q`, raw)
		}
		if body.Cleanup != "git" {
			t.Errorf(`--cleanup git must ride in the body, got %q`, raw)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "success",
			"message": "Cluster prod-eu removed.",
		})
	})

	resetFlags(removeClusterCmd)
	if err := removeClusterCmd.Flags().Set("yes", "true"); err != nil {
		t.Fatalf("set -y: %v", err)
	}
	if err := removeClusterCmd.Flags().Set("cleanup", "git"); err != nil {
		t.Fatalf("set --cleanup: %v", err)
	}
	out, err := captureStdoutT(t, func() error {
		return removeClusterCmd.RunE(removeClusterCmd, []string{"prod-eu"})
	})
	if err != nil {
		t.Fatalf("confirmed removal must succeed: %v", err)
	}
	if !strings.Contains(out, "removed") {
		t.Errorf("the success line is missing: %q", out)
	}
}

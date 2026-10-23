package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// EP3 dry run round R, R5 and R14: take over waits for its pull request,
// says so, and exits 0; --acknowledge takes "a,b"; a "not ready" 409 prints
// the server's reason.

const takeoverWaitingServerMsg = "Pull request #1 is open: https://example.invalid/pr/1. When it is merged, Sharko takes ownership of prod-eu's ArgoCD connection. If it is closed without merging, Sharko changes nothing on the connection."

func TestTakeover_PRWaits_SaysWaitingAndExitsZero(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			jsonReply(w, 200, preflightReport{Cluster: "prod-eu", Ready: true})
			return
		}
		jsonReply(w, 200, map[string]interface{}{
			"cluster": "prod-eu", "status": "success", "secret_swapped": false, "waiting_for_merge": true,
			"git":     map[string]interface{}{"pr_url": "https://example.invalid/pr/1", "pr_id": 1, "merged": false},
			"message": takeoverWaitingServerMsg,
		})
	})
	resetFlags(takeoverCmd)
	setFlags(t, takeoverCmd, map[string]string{"yes": "true"})
	out, err := captureStdoutT(t, func() error { return takeoverCmd.RunE(takeoverCmd, []string{"prod-eu"}) })
	if err != nil {
		t.Fatalf("a take over waiting for its merge must exit 0, got %v (out=%q)", err, out)
	}
	if !strings.Contains(out, "Taking over cluster prod-eu... waiting for merge\n") {
		t.Errorf("progress line: %q", out)
	}
	if !strings.Contains(out, "\nPR opened, waiting for merge: https://example.invalid/pr/1\n") {
		t.Errorf("waiting line missing: %q", out)
	}
	if !strings.Contains(out, "  "+takeoverWaitingServerMsg+"\n") {
		t.Errorf("server message missing: %q", out)
	}
	for _, banned := range []string{"Sharko now owns", "merge manually", "done\n"} {
		if strings.Contains(out, banned) {
			t.Errorf("the old wording %q is printed while the pull request waits: %q", banned, out)
		}
	}
}

func TestTakeover_PRMerged_SaysSharkoOwnsIt(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			jsonReply(w, 200, preflightReport{Cluster: "prod-eu", Ready: true})
			return
		}
		jsonReply(w, 200, map[string]interface{}{
			"cluster": "prod-eu", "status": "success", "secret_swapped": true,
			"git":     map[string]interface{}{"pr_url": "https://example.invalid/pr/1", "merged": true},
			"message": "x",
		})
	})
	resetFlags(takeoverCmd)
	setFlags(t, takeoverCmd, map[string]string{"yes": "true"})
	out, err := captureStdoutT(t, func() error { return takeoverCmd.RunE(takeoverCmd, []string{"prod-eu"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Sharko now owns prod-eu.\n") || strings.Contains(out, "waiting for merge") {
		t.Errorf("a merged take over must say Sharko owns it: %q", out)
	}
}

func TestTakeover_AcknowledgeTakesCommaSeparatedIDs(t *testing.T) {
	var sent []string
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			jsonReply(w, 200, preflightReport{Cluster: "prod-eu", Ready: true})
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Acks []string `json:"acknowledged_findings"`
		}
		_ = json.Unmarshal(raw, &body)
		sent = body.Acks
		jsonReply(w, 200, map[string]interface{}{"cluster": "prod-eu", "status": "success", "message": "x"})
	})
	resetFlags(takeoverCmd)
	setFlags(t, takeoverCmd, map[string]string{"yes": "true"})
	setRepeatedFlag(t, takeoverCmd, "acknowledge", "secret-owner,appset-deletion-safety", "secret-owner")
	if _, err := captureStdoutT(t, func() error { return takeoverCmd.RunE(takeoverCmd, []string{"prod-eu"}) }); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 || sent[0] != "secret-owner" || sent[1] != "appset-deletion-safety" {
		t.Fatalf("acknowledged_findings sent = %v, want [secret-owner appset-deletion-safety]", sent)
	}
}

func TestTakeover_NotReady409_ReturnsTheServerReason(t *testing.T) {
	const reason = "prod-eu cannot be taken over yet: another tool owns its connection."
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			jsonReply(w, 200, preflightReport{Cluster: "prod-eu", Ready: false})
			return
		}
		jsonReply(w, 409, map[string]interface{}{"error": reason})
	})
	resetFlags(takeoverCmd)
	setFlags(t, takeoverCmd, map[string]string{"yes": "true"})
	out, err := captureStdoutT(t, func() error { return takeoverCmd.RunE(takeoverCmd, []string{"prod-eu"}) })
	if err == nil || err.Error() != reason {
		t.Fatalf("err = %v, want %q", err, reason)
	}
	if strings.Contains(out, "Rerun with") {
		t.Errorf("a blocked take over has nothing to acknowledge: %q", out)
	}
}

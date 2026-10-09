package main

// single_cluster_exit_test.go — R2-10.
//
// R2-9 fixed the fan-out commands. The five single-cluster ones were left
// behind: `add-cluster`, `remove-cluster`, `update-cluster`,
// `unadopt-cluster` and `takeover` each printed something like "Cluster
// registered with warnings (partial success)." and then returned nil. A
// script wrapping any of them was told the operation completed when it had
// stopped half-way with real changes left behind. `remove-cluster` was worse
// again: the endpoint answers 200 with a body saying "failed", and the
// command printed "Cluster prod-eu removed." over the top of it.
//
// These tests drive the REAL cobra commands against a real HTTP test server
// and read the REAL return value — the thing that becomes the process exit
// code — for each of the four answers a single-cluster command can meet.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// singleAnswer is one thing the server can come back with, written out here
// rather than read off the production constants, so nothing below compares a
// constant with itself.
type singleAnswer struct {
	what string
	// status is the `status` field in the response body.
	status string
	// httpStatus is what the endpoint really answers for that body.
	httpStatus int
	// completed says whether this answer means the cluster finished every
	// step. Written out independently of fanout.Outcome.
	completed bool
	// partWay says whether real changes may have been left behind, which is
	// the case that needs the "go and look" sentence.
	partWay bool
}

// singleAnswers is the four cases every one of the five commands is driven
// over: fully completed, partly completed, failed, and a status this build
// does not recognise.
func singleAnswers(okHTTP int) []singleAnswer {
	return []singleAnswer{
		{what: "fully completed", status: "success", httpStatus: okHTTP, completed: true},
		{what: "partly completed", status: "partial", httpStatus: 207, partWay: true},
		// The endpoints answer 200/201 for an outright failure — only
		// "partial" is mapped to 207 — which is precisely why the exit code
		// has to be read from the body.
		{what: "failed", status: "failed", httpStatus: okHTTP},
		{what: "a status this build does not know", status: "half-done-ish", httpStatus: okHTTP},
		// A 207 with a body that still claims a clean completion: the server
		// says plainly that not all of it went through, so the smaller claim
		// has to win.
		{what: "207 over a body claiming success", status: "success", httpStatus: 207, partWay: true},
	}
}

// completionWordsCLI are the words that may not appear about a run that did
// not complete. "completed" is left out on purpose — "partly completed" is a
// count, not a verdict.
var completionWordsCLI = []string{"success", "succeeded", "with warnings"}

// assertSingleExitAgreesWithOutput is the ruling applied to one cluster:
// exit 0 only on a full completion, no completion wording otherwise, and the
// "real changes may be left behind" sentence exactly when something stopped
// part-way.
func assertSingleExitAgreesWithOutput(t *testing.T, a singleAnswer, out string, err error, cluster string) {
	t.Helper()

	if a.completed {
		if err != nil {
			t.Errorf("the cluster completed, yet the command exits non-zero: %v\noutput:\n%s", err, out)
		}
		if !standaloneDone.MatchString(out) {
			t.Errorf("the cluster completed, yet the command never says so:\n%s", out)
		}
		if strings.Contains(out, "Nothing was undone") {
			t.Errorf("the cluster completed, yet a review warning was printed:\n%s", out)
		}
		return
	}

	// ── It did not complete ──────────────────────────────────────────────
	if err == nil {
		t.Errorf("%s: the cluster did not complete and the command still exits 0. A script "+
			"checking the exit code is told everything worked.\noutput:\n%s", a.what, out)
	}
	if standaloneDone.MatchString(out) {
		t.Errorf("%s: the command printed \"done\" for a run that did not complete:\n%s", a.what, out)
	}
	lower := strings.ToLower(out)
	for _, w := range completionWordsCLI {
		if strings.Contains(lower, w) {
			t.Errorf("%s: a run that did not complete used the word %q:\n%s", a.what, w, out)
		}
	}
	if !strings.Contains(out, "not finished") {
		t.Errorf("%s: nothing in the output says the run did not finish:\n%s", a.what, out)
	}
	// stdout and the exit message have to be telling the same story.
	if err != nil && !strings.Contains(err.Error(), cluster) {
		t.Errorf("%s: the exit message %q does not name the cluster it is about", a.what, err)
	}

	// Part-way work needs the "go and look" sentence, and only part-way work.
	if a.partWay && !strings.Contains(out, "Nothing was undone") {
		t.Errorf("%s: the cluster stopped part-way and nothing warns that real changes may "+
			"already be out there:\n%s", a.what, out)
	}
	if !a.partWay && strings.Contains(out, "Nothing was undone") {
		t.Errorf("%s: nothing stopped part-way, yet the review warning was printed:\n%s", a.what, out)
	}
}

// singleClusterBody is the response shape all five endpoints share, with the
// extra fields each one carries folded in — unset fields simply do not print.
func singleClusterBody(a singleAnswer) map[string]any {
	return map[string]any{
		"status":      a.status,
		"cluster":     map[string]any{"name": "prod-eu", "server": "https://prod-eu.example"},
		"server":      "https://prod-eu.example",
		"failed_step": "pr_merge",
		"error":       "",
		"message":     "",
	}
}

func serveSingleCluster(t *testing.T, a singleAnswer, wantPath string) {
	t.Helper()
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != wantPath {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(a.httpStatus)
		_ = json.NewEncoder(w).Encode(singleClusterBody(a))
	})
}

func TestAddCluster_ExitCodeAndOutputAgree(t *testing.T) {
	for _, a := range singleAnswers(201) {
		a := a
		t.Run(a.what, func(t *testing.T) {
			serveSingleCluster(t, a, "/api/v1/clusters")
			resetFlags(addClusterCmd)
			out, err := captureStdoutT(t, func() error {
				return addClusterCmd.RunE(addClusterCmd, []string{"prod-eu"})
			})
			assertSingleExitAgreesWithOutput(t, a, out, err, "prod-eu")
		})
	}
}

func TestRemoveCluster_ExitCodeAndOutputAgree(t *testing.T) {
	for _, a := range singleAnswers(200) {
		a := a
		t.Run(a.what, func(t *testing.T) {
			serveSingleCluster(t, a, "/api/v1/clusters/prod-eu")
			resetFlags(removeClusterCmd)
			setFlags(t, removeClusterCmd, map[string]string{"yes": "true"})
			out, err := captureStdoutT(t, func() error {
				return removeClusterCmd.RunE(removeClusterCmd, []string{"prod-eu"})
			})
			assertSingleExitAgreesWithOutput(t, a, out, err, "prod-eu")
		})
	}
}

func TestUpdateCluster_ExitCodeAndOutputAgree(t *testing.T) {
	for _, a := range singleAnswers(200) {
		a := a
		t.Run(a.what, func(t *testing.T) {
			serveSingleCluster(t, a, "/api/v1/clusters/prod-eu")
			resetFlags(updateClusterCmd)
			setFlags(t, updateClusterCmd, map[string]string{"add-addon": "argo-rollouts"})
			out, err := captureStdoutT(t, func() error {
				return updateClusterCmd.RunE(updateClusterCmd, []string{"prod-eu"})
			})
			assertSingleExitAgreesWithOutput(t, a, out, err, "prod-eu")
		})
	}
}

func TestUnadoptCluster_ExitCodeAndOutputAgree(t *testing.T) {
	for _, a := range singleAnswers(200) {
		a := a
		t.Run(a.what, func(t *testing.T) {
			serveSingleCluster(t, a, "/api/v1/clusters/prod-eu/unadopt")
			resetFlags(unadoptClusterCmd)
			setFlags(t, unadoptClusterCmd, map[string]string{"yes": "true"})
			out, err := captureStdoutT(t, func() error {
				return unadoptClusterCmd.RunE(unadoptClusterCmd, []string{"prod-eu"})
			})
			assertSingleExitAgreesWithOutput(t, a, out, err, "prod-eu")
		})
	}
}

// TestTakeover_ExitCodeAndOutputAgree. `sharko takeover` always fetches the
// preflight report first and only then posts, so the test server has to
// answer both.
func TestTakeover_ExitCodeAndOutputAgree(t *testing.T) {
	for _, a := range singleAnswers(200) {
		a := a
		t.Run(a.what, func(t *testing.T) {
			startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet &&
					r.URL.Path == "/api/v1/clusters/prod-eu/takeover/preflight":
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(preflightReport{
						Cluster: "prod-eu", Ready: true, Summary: "All clear.",
					})
				case r.Method == http.MethodPost &&
					r.URL.Path == "/api/v1/clusters/prod-eu/takeover":
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(a.httpStatus)
					_ = json.NewEncoder(w).Encode(takeoverResponse{
						Cluster: "prod-eu",
						Status:  a.status,
						Server:  "https://prod-eu.example",
					})
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
			})
			resetFlags(takeoverCmd)
			setFlags(t, takeoverCmd, map[string]string{"yes": "true"})
			out, err := captureStdoutT(t, func() error {
				return takeoverCmd.RunE(takeoverCmd, []string{"prod-eu"})
			})
			assertSingleExitAgreesWithOutput(t, a, out, err, "prod-eu")
		})
	}
}

// TestSingleClusterDryRuns_StillExitZero. A preview changes nothing, so there
// is no completion to confirm and nothing for an exit code to warn about —
// the same exception `sharko adopt --dry-run` gets. The bodies below carry a
// status that is NOT a clean completion on purpose: if a dry run ever started
// taking its exit code from the body, these would go red.
func TestSingleClusterDryRuns_StillExitZero(t *testing.T) {
	t.Run("add-cluster", func(t *testing.T) {
		startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":  "planned",
				"dry_run": map[string]any{"pr_title": "sharko: register prod-eu"},
			})
		})
		resetFlags(addClusterCmd)
		setFlags(t, addClusterCmd, map[string]string{"dry-run": "true"})
		if _, err := captureStdoutT(t, func() error {
			return addClusterCmd.RunE(addClusterCmd, []string{"prod-eu"})
		}); err != nil {
			t.Errorf("a preview changed nothing and the command still exits non-zero: %v", err)
		}
	})

	t.Run("unadopt-cluster", func(t *testing.T) {
		startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":  "planned",
				"dry_run": map[string]any{"pr_title": "sharko: unadopt prod-eu"},
			})
		})
		resetFlags(unadoptClusterCmd)
		setFlags(t, unadoptClusterCmd, map[string]string{"dry-run": "true"})
		if _, err := captureStdoutT(t, func() error {
			return unadoptClusterCmd.RunE(unadoptClusterCmd, []string{"prod-eu"})
		}); err != nil {
			t.Errorf("a preview changed nothing and the command still exits non-zero: %v", err)
		}
	})

	t.Run("takeover", func(t *testing.T) {
		startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode(preflightReport{
					Cluster: "prod-eu", Ready: true, Summary: "All clear.",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":  "planned",
				"dry_run": map[string]any{"pr_title": "sharko: take over prod-eu"},
			})
		})
		resetFlags(takeoverCmd)
		setFlags(t, takeoverCmd, map[string]string{"dry-run": "true"})
		if _, err := captureStdoutT(t, func() error {
			return takeoverCmd.RunE(takeoverCmd, []string{"prod-eu"})
		}); err != nil {
			t.Errorf("a preview changed nothing and the command still exits non-zero: %v", err)
		}
	})
}

// TestSingleClusterExit_CarriesNoBackendErrorText. Everything the new wording
// prints is counts and fixed sentences plus the cluster's name. A message
// from Git, Kubernetes or a credentials backend must not ride out through the
// exit error, which is the one string a wrapping script is most likely to log.
func TestSingleClusterExit_CarriesNoBackendErrorText(t *testing.T) {
	leak := "x509: certificate signed by unknown authority (dial tcp 10.0.0.1:443, token AccessDenied 403)"
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(207)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":      "partial",
			"failed_step": "pr_merge",
			"error":       leak,
			"message":     leak,
			"cluster":     map[string]any{"name": "prod-eu"},
		})
	})
	resetFlags(addClusterCmd)
	_, err := captureStdoutT(t, func() error {
		return addClusterCmd.RunE(addClusterCmd, []string{"prod-eu"})
	})
	if err == nil {
		t.Fatal("a partial exited 0")
	}
	for _, bad := range []string{"x509", "dial tcp", "AccessDenied", "403", "token"} {
		if strings.Contains(err.Error(), bad) {
			t.Errorf("the exit message carries backend error text %q: %v", bad, err)
		}
	}
}

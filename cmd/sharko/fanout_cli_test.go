package main

// fanout_cli_test.go — R2-9.
//
// `sharko add-clusters` and `sharko adopt` both printed "done" the moment the
// HTTP call came back and then returned nil, so BOTH exited 0 for a batch in
// which every single cluster failed. A script had no way to notice. The adopt
// endpoint made it worse: it answers 200 for an all-failed adoption, so the
// status code did not contradict the word "done" either.
//
// These tests drive the REAL cobra commands against a real HTTP test server
// and read the REAL return value — the thing that becomes the process exit
// code — for each of the outcome shapes the product owner named.

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/fanout/fanouttest"
)

// standaloneDone matches the word "done" on its own — not the "done" inside
// "undone", which is part of the review warning.
var standaloneDone = regexp.MustCompile(`\bdone\b`)

// batchBodyFor is the response POST /api/v1/clusters/batch really sends for a
// shape, older counters included: `failed` counts every cluster that did not
// FULLY succeed, partials among them.
func batchBodyFor(s fanouttest.Shape) map[string]any {
	results := []map[string]any{}
	succeeded, failed, partial := 0, 0, 0
	for i, status := range s.Statuses() {
		results = append(results, map[string]any{
			"status":  status,
			"cluster": map[string]any{"name": clusterNameFor(i)},
			"error":   "",
			"message": "",
		})
		switch status {
		case "partial":
			partial++
			failed++
		case "failed":
			failed++
		default:
			succeeded++
		}
	}
	return map[string]any{
		"total": len(results), "succeeded": succeeded, "failed": failed, "partial": partial,
		"results": results,
	}
}

func adoptBodyFor(s fanouttest.Shape) map[string]any {
	results := []map[string]any{}
	for i, status := range s.Statuses() {
		results = append(results, map[string]any{"name": clusterNameFor(i), "status": status})
	}
	return map[string]any{"results": results}
}

func clusterNameFor(i int) string { return "c" + string(rune('0'+i)) }

// batchHTTPStatusFor / adoptHTTPStatusFor mirror what each endpoint really
// answers, written out here independently of the server code, so these tests
// exercise the CLI against the statuses it will actually meet — including the
// 200 an all-failed adoption comes back with.
func batchHTTPStatusFor(s fanouttest.Shape) int {
	if s.PartlyCompleted+s.Failed > 0 {
		return http.StatusMultiStatus
	}
	return http.StatusOK
}

func adoptHTTPStatusFor(s fanouttest.Shape) int {
	if s.Failed > 0 && s.Completed+s.PartlyCompleted+s.Unrecognized > 0 {
		return http.StatusMultiStatus
	}
	return http.StatusOK
}

// TestAddClusters_ExitCodeAndSummaryAgreeWithTheOutcome drives the real
// command over every shape the ruling names.
func TestAddClusters_ExitCodeAndSummaryAgreeWithTheOutcome(t *testing.T) {
	for _, shape := range fanouttest.Ruled() {
		shape := shape
		t.Run(shape.What, func(t *testing.T) {
			srv := startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/clusters/batch" {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(batchHTTPStatusFor(shape))
				_ = json.NewEncoder(w).Encode(batchBodyFor(shape))
			})
			_ = srv

			names := make([]string, 0, shape.Total())
			for i := 0; i < shape.Total(); i++ {
				names = append(names, clusterNameFor(i))
			}

			resetFlags(addClustersCmd)
			out, err := captureStdoutT(t, func() error {
				return addClustersCmd.RunE(addClustersCmd, []string{strings.Join(names, ",")})
			})

			assertExitAgreesWithOutput(t, shape, out, err, "Cluster registration")
		})
	}
}

// TestAdopt_ExitCodeAndSummaryAgreeWithTheOutcome is the same table for
// `sharko adopt`, whose endpoint answers 200 even when every cluster failed —
// which is exactly why the exit code has to come from the body.
func TestAdopt_ExitCodeAndSummaryAgreeWithTheOutcome(t *testing.T) {
	for _, shape := range fanouttest.Ruled() {
		shape := shape
		t.Run(shape.What, func(t *testing.T) {
			startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/clusters/adopt" {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(adoptHTTPStatusFor(shape))
				_ = json.NewEncoder(w).Encode(adoptBodyFor(shape))
			})

			names := make([]string, 0, shape.Total())
			for i := 0; i < shape.Total(); i++ {
				names = append(names, clusterNameFor(i))
			}

			resetFlags(adoptCmd)
			setFlags(t, adoptCmd, map[string]string{"yes": "true"})
			out, err := captureStdoutT(t, func() error {
				return adoptCmd.RunE(adoptCmd, names)
			})

			assertExitAgreesWithOutput(t, shape, out, err, "Adoption")
		})
	}
}

// assertExitAgreesWithOutput is the ruling's CLI section, checked all at once.
func assertExitAgreesWithOutput(t *testing.T, shape fanouttest.Shape, out string, err error, operation string) {
	t.Helper()
	// All three counts, always.
	for _, want := range []string{"fully completed", "partly completed", "failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("the summary does not carry %q:\n%s", want, out)
		}
	}

	if shape.EverythingCompleted() {
		if err != nil {
			t.Errorf("every cluster completed, yet the command exits non-zero: %v", err)
		}
		if !standaloneDone.MatchString(out) {
			t.Errorf("every cluster completed, yet the command never says so:\n%s", out)
		}
		if strings.Contains(out, "only got part of the way") {
			t.Errorf("every cluster completed, yet a review warning was printed:\n%s", out)
		}
		return
	}

	// ── Something did not finish ─────────────────────────────────────────
	if err == nil {
		t.Errorf("%s: %d cluster(s) stopped part-way and %d failed, and the command still "+
			"exits 0. A script checking the exit code is told everything worked.",
			shape.What, shape.PartlyCompleted, shape.Failed)
	}
	// Completion wording is banned for a run that did not complete.
	// \bdone\b, not a substring search: "Nothing was undone" is the review
	// warning and contains the letters.
	if standaloneDone.MatchString(out) {
		t.Errorf("%s: the command printed \"done\" for a run that did not complete:\n%s", shape.What, out)
	}
	if !strings.Contains(out, "did NOT finish for every cluster") {
		t.Errorf("%s: nothing in the output says the run did not finish:\n%s", shape.What, out)
	}
	// stdout and the exit message have to be telling the same story.
	if err != nil && !strings.Contains(err.Error(), operation) {
		t.Errorf("%s: the exit message %q does not name what was attempted", shape.What, err)
	}

	// Part-way work needs the "go and look" sentence.
	if shape.PartlyCompleted > 0 && !strings.Contains(out, "Nothing was undone") {
		t.Errorf("%s: %d cluster(s) stopped part-way and nothing warns that real changes may "+
			"already be out there:\n%s", shape.What, shape.PartlyCompleted, out)
	}
	if shape.PartlyCompleted == 0 && strings.Contains(out, "Nothing was undone") {
		t.Errorf("%s: nothing stopped part-way, yet the review warning was printed:\n%s", shape.What, out)
	}
}

// TestAdoptDryRun_IsStillASuccess. A preview adopts nothing, so there is no
// completion to confirm and nothing for an exit code to warn about.
func TestAdoptDryRun_IsStillASuccess(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{{"name": "c0", "status": "failed"}},
		})
	})
	resetFlags(adoptCmd)
	setFlags(t, adoptCmd, map[string]string{"dry-run": "true"})
	_, err := captureStdoutT(t, func() error { return adoptCmd.RunE(adoptCmd, []string{"c0"}) })
	if err != nil {
		t.Errorf("a dry run previewed successfully and the command still exits non-zero: %v", err)
	}
}

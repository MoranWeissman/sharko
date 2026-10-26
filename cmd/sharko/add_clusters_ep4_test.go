package main

// add_clusters_ep4_test.go — what the episode 4 dry run found about
// `sharko add-clusters`: it asked nothing, had no dry run, gave up after 15
// seconds and printed "failed" while the server was still working, and it
// registered names that had no login stored without a word.
//
// Every test talks to a real HTTP test server that answers in the shapes the
// real POST /api/v1/clusters/batch sends (orchestrator.BatchResult, including
// the 207).

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// batchCalls records what the test server was asked.
type batchCalls struct {
	mu    sync.Mutex
	dry   [][]batchClusterEntry
	real  [][]batchClusterEntry
	paths []string
}

func (c *batchCalls) realCount() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.real) }
func (c *batchCalls) dryCount() int  { c.mu.Lock(); defer c.mu.Unlock(); return len(c.dry) }

// previewAnswer is what the server sends for a dry-run entry. hasLogin says
// whether it found a stored secret: it reports that in completed_steps.
func previewAnswer(name string, hasLogin bool) map[string]any {
	step := "fetch_credentials"
	if !hasLogin {
		step = "skip_credentials"
	}
	return map[string]any{
		"status":          "success",
		"cluster":         map[string]any{"name": name, "addons": map[string]bool{}},
		"completed_steps": []string{step},
		"dry_run": map[string]any{
			"effective_addons":  []string{},
			"files_to_write":    []map[string]any{{"path": "configuration/managed-clusters.yaml", "action": "update", "diff": "+x"}},
			"pr_title":          "sharko: register cluster " + name,
			"secrets_to_create": []string{},
		},
	}
}

// batchServer answers dry-run entries from noLogin (names with no secret) and
// real entries with realStatus and realBody.
func batchServer(t *testing.T, calls *batchCalls, noLogin map[string]bool, realStatus int, realBody func(names []string) map[string]any) {
	t.Helper()
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/clusters/batch" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
			return
		}
		var req batchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("bad body: %v", err)
		}
		calls.mu.Lock()
		isDry := len(req.Clusters) > 0 && req.Clusters[0].DryRun
		for _, c := range req.Clusters {
			if c.DryRun != isDry {
				t.Errorf("a page mixes dry-run and real entries")
			}
		}
		if len(req.Clusters) > 10 {
			t.Errorf("a page carries %d clusters, the limit is 10", len(req.Clusters))
		}
		if isDry {
			calls.dry = append(calls.dry, req.Clusters)
		} else {
			calls.real = append(calls.real, req.Clusters)
		}
		calls.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		names := []string{}
		for _, c := range req.Clusters {
			names = append(names, c.Name)
		}
		if isDry {
			results := []map[string]any{}
			for _, n := range names {
				results = append(results, previewAnswer(n, !noLogin[n]))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"total": len(results), "succeeded": len(results), "results": results})
			return
		}
		w.WriteHeader(realStatus)
		_ = json.NewEncoder(w).Encode(realBody(names))
	})
}

func allOK(names []string) map[string]any {
	results := []map[string]any{}
	for _, n := range names {
		results = append(results, map[string]any{
			"status":  "success",
			"cluster": map[string]any{"name": n, "server": "https://" + n + ".example"},
			"git":     map[string]any{"pr_url": "https://git.example/pr/1", "branch": "b"},
		})
	}
	return map[string]any{"total": len(names), "succeeded": len(names), "results": results}
}

func runAddClusters(t *testing.T, stdin string, flags map[string]string, args ...string) (string, error) {
	t.Helper()
	resetFlags(addClustersCmd)
	setFlags(t, addClustersCmd, flags)
	old := batchStdin
	batchStdin = strings.NewReader(stdin)
	t.Cleanup(func() { batchStdin = old })
	return captureStdoutT(t, func() error { return addClustersCmd.RunE(addClustersCmd, args) })
}

func TestAddClusters_ShowsTheListAndAsksBeforeChangingAnything(t *testing.T) {
	calls := &batchCalls{}
	batchServer(t, calls, nil, 200, allOK)

	out, err := runAddClusters(t, "n\n", nil, "a,b")
	if err != nil {
		t.Fatalf("an answer of no is not an error: %v", err)
	}
	if calls.realCount() != 0 {
		t.Fatalf("the clusters were registered although the answer was no")
	}
	if calls.dryCount() != 1 {
		t.Fatalf("the list was not checked first (dry calls: %d)", calls.dryCount())
	}
	for _, want := range []string{"Clusters (2):", "- a", "- b", "Register 2 cluster(s)? [y/N]:", "Aborted. Nothing was changed."} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestAddClusters_NoAnswerAtAllMeansNo(t *testing.T) {
	calls := &batchCalls{}
	batchServer(t, calls, nil, 200, allOK)
	// An empty stdin is what a script without --yes gives.
	_, err := runAddClusters(t, "", nil, "a")
	if err != nil || calls.realCount() != 0 {
		t.Fatalf("an empty answer must not register anything (err=%v, real calls=%d)", err, calls.realCount())
	}
}

func TestAddClusters_YesAnswerRegisters(t *testing.T) {
	calls := &batchCalls{}
	batchServer(t, calls, nil, 200, allOK)
	out, err := runAddClusters(t, "y\n", nil, "a,b")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if calls.realCount() != 1 {
		t.Fatalf("expected one real call, got %d", calls.realCount())
	}
}

func TestAddClusters_YesFlagSkipsTheQuestion(t *testing.T) {
	calls := &batchCalls{}
	batchServer(t, calls, nil, 200, allOK)
	out, err := runAddClusters(t, "", map[string]string{"yes": "true"}, "a,b")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if strings.Contains(out, "[y/N]") {
		t.Errorf("--yes still asked:\n%s", out)
	}
	if calls.realCount() != 1 {
		t.Fatalf("--yes did not register (real calls: %d)", calls.realCount())
	}
}

func TestAddClusters_DryRunChangesNothingAndSaysSo(t *testing.T) {
	calls := &batchCalls{}
	batchServer(t, calls, nil, 200, allOK)
	out, err := runAddClusters(t, "y\n", map[string]string{"dry-run": "true"}, "a,b")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if calls.realCount() != 0 {
		t.Fatalf("--dry-run made a real call")
	}
	for _, want := range []string{"This was a dry run. Nothing was changed.", "PR: sharko: register cluster a", "[update] configuration/managed-clusters.yaml"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "[y/N]") {
		t.Errorf("--dry-run asked a question:\n%s", out)
	}
}

func TestAddClusters_WarnsByNameAboutClustersWithNoLogin(t *testing.T) {
	calls := &batchCalls{}
	batchServer(t, calls, map[string]bool{"b": true, "d": true}, 200, allOK)

	out, _ := runAddClusters(t, "n\n", nil, "a,b,c,d")
	for _, want := range []string{
		"- a  -- login found",
		"- b  -- no login found",
		"- d  -- no login found",
		"WARNING: no login (secret) was found for: b, d",
		"These would be registered with no connection. Sharko cannot reach them until a login is stored for each.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "found for: a") || strings.Contains(out, "for: b, c") {
		t.Errorf("a cluster with a login is named in the warning:\n%s", out)
	}
}

func TestAddClusters_NoWarningWhenEveryClusterHasALogin(t *testing.T) {
	calls := &batchCalls{}
	batchServer(t, calls, nil, 200, allOK)
	out, _ := runAddClusters(t, "n\n", nil, "a,b")
	if strings.Contains(out, "WARNING") {
		t.Errorf("a warning for clusters that all have a login:\n%s", out)
	}
}

func TestAddClusters_StopsIfTheServerDoesNotDoADryRun(t *testing.T) {
	var realCalls int
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		realCalls++
		w.Header().Set("Content-Type", "application/json")
		// An old server that ignores dry_run answers like a real registration.
		_ = json.NewEncoder(w).Encode(allOK([]string{"a"}))
	})
	out, err := runAddClusters(t, "y\n", nil, "a")
	if err == nil || !strings.Contains(err.Error(), "did not do a dry run") {
		t.Fatalf("expected a stop, got err=%v\n%s", err, out)
	}
	if realCalls != 1 {
		t.Fatalf("it went on after the server ignored the dry run (%d calls)", realCalls)
	}
}

func TestAddClusters_ElevenClustersGoInTwoPages(t *testing.T) {
	calls := &batchCalls{}
	batchServer(t, calls, nil, 200, allOK)
	names := []string{}
	for i := 0; i < 11; i++ {
		names = append(names, clusterNameFor(i))
	}
	_, err := runAddClusters(t, "", map[string]string{"yes": "true"}, strings.Join(names, ","))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls.dryCount() != 2 || calls.realCount() != 2 {
		t.Fatalf("expected 2 check pages and 2 real pages, got %d and %d", calls.dryCount(), calls.realCount())
	}
}

func TestBatchTimeout_IsLongEnoughForTenClustersAndOtherCallsKeepFifteenSeconds(t *testing.T) {
	// About 5 to 6 seconds per cluster, 10 clusters: about a minute. The
	// value is two minutes.
	if batchRequestTimeout != 2*time.Minute {
		t.Errorf("the batch timeout is %s, want 2m0s", batchRequestTimeout)
	}
	if defaultRequestTimeout != 15*time.Second {
		t.Errorf("an ordinary call waits %s, want 15s", defaultRequestTimeout)
	}
	if got := buildHTTPClient(false).Timeout; got != 15*time.Second {
		t.Errorf("buildHTTPClient timeout = %s, want 15s", got)
	}
}

// A server that takes longer than the client waits, but is still working.
func TestAddClusters_WhenTheClientStopsWaitingItDoesNotSayFailed(t *testing.T) {
	old := batchRequestTimeout
	batchRequestTimeout = 300 * time.Millisecond
	t.Cleanup(func() { batchRequestTimeout = old })

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	var mu sync.Mutex
	n := 0
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req batchRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		n++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if len(req.Clusters) > 0 && req.Clusters[0].DryRun {
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{previewAnswer("a", true)}})
			return
		}
		select { // the real registration is slow
		case <-release:
		case <-r.Context().Done():
		}
	})

	out, err := runAddClusters(t, "", map[string]string{"yes": "true"}, "a")
	if err == nil {
		t.Fatalf("a timeout must not exit 0")
	}
	for _, want := range []string{
		"Stopped waiting for the server after 300ms.",
		"The server may still be working on the clusters",
		"sharko pr list",
		"the Clusters page",
	} {
		if !strings.Contains(out+err.Error(), want) {
			t.Errorf("missing %q:\nout: %s\nerr: %v", want, out, err)
		}
	}
	for _, banned := range []string{"failed", "timed out", "Failed"} {
		if strings.Contains(out, banned) || strings.Contains(err.Error(), banned) {
			t.Errorf("a timeout says %q:\nout: %s\nerr: %v", banned, out, err)
		}
	}
	if !strings.Contains(err.Error(), "may still be working") {
		t.Errorf("the exit message does not say the server may still be working: %v", err)
	}
}

func TestAddClusters_Prints207ResultsTruthfully(t *testing.T) {
	calls := &batchCalls{}
	batchServer(t, calls, nil, http.StatusMultiStatus, func(names []string) map[string]any {
		return map[string]any{
			"total": 4, "succeeded": 1, "failed": 3, "partial": 1,
			"results": []map[string]any{
				{"status": "success", "cluster": map[string]any{"name": "a", "server": "https://a.example"},
					"git": map[string]any{"pr_url": "https://git.example/pr/7", "branch": "b"}, "completed_steps": []string{"skip_credentials"}},
				{"status": "partial", "cluster": map[string]any{"name": "b"}, "message": "PR opened, ArgoCD step stopped",
					"git": map[string]any{"pr_url": "https://git.example/pr/8"}},
				{"status": "failed", "cluster": map[string]any{"name": "c"}, "error": "git said no"},
				{"status": "mystery", "cluster": map[string]any{"name": "d"}},
			},
		}
	})
	out, err := runAddClusters(t, "", map[string]string{"yes": "true"}, "a,b,c,d")
	if err == nil {
		t.Fatalf("a 207 with failures must not exit 0")
	}
	for _, want := range []string{
		"[ok] a (https://a.example) — PR: https://git.example/pr/7 — registered with no connection (no login was found)",
		"[!!] b — partial: PR opened, ArgoCD step stopped — PR: https://git.example/pr/8",
		"[XX] c — git said no",
		`[??] d — the server said "mystery"`,
		"not all clusters finished",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if standaloneDone.MatchString(out) {
		t.Errorf("the word done appears for a run that did not finish:\n%s", out)
	}
}

func TestAddCluster_HelpDescribesTheEKSTokenTruthfully(t *testing.T) {
	long := addClusterCmd.Long
	want := "A short-lived token that Sharko makes and renews before it\n                     runs out."
	if !strings.Contains(long, want) {
		t.Errorf("the eks-token help lacks the new sentence; help is:\n%s", long)
	}
	for _, banned := range []string{"Nothing to store", "nothing to store", "each time it connects"} {
		if strings.Contains(long, banned) {
			t.Errorf("the eks-token help still says %q", banned)
		}
	}
}

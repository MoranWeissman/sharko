package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const waitPR = "https://git.example/acme/fleet/pull/7"

func jsonReply(w http.ResponseWriter, code int, v interface{}) {
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// remove-cluster must not say "removed" while its pull request still waits.
func TestRemoveCluster_WaitingPRSaysWaitingNotRemoved(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, map[string]interface{}{
			"status": "success",
			"git":    map[string]interface{}{"pr_url": waitPR, "merged": false},
		})
	})
	resetFlags(removeClusterCmd)
	setFlags(t, removeClusterCmd, map[string]string{"yes": "true"})
	out, err := captureStdoutT(t, func() error { return removeClusterCmd.RunE(removeClusterCmd, []string{"legacy-1"}) })
	if err != nil {
		t.Fatalf("a waiting pull request is not an error: %v", err)
	}
	if !strings.Contains(out, "PR opened, waiting for merge: "+waitPR+"\n") {
		t.Errorf("missing the waiting line: %q", out)
	}
	if !strings.Contains(out, "Nothing has changed on cluster legacy-1 yet. Sharko finishes the removal after the pull request is merged.") {
		t.Errorf("missing the nothing-changed sentence: %q", out)
	}
	for _, banned := range []string{"removed.", "... done"} {
		if strings.Contains(out, banned) {
			t.Errorf("old wording %q must not appear while the pull request waits: %q", banned, out)
		}
	}
}

func TestRemoveCluster_MergedPRSaysRemoved(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, map[string]interface{}{
			"status": "success",
			"git":    map[string]interface{}{"pr_url": waitPR, "merged": true},
		})
	})
	resetFlags(removeClusterCmd)
	setFlags(t, removeClusterCmd, map[string]string{"yes": "true"})
	out, err := captureStdoutT(t, func() error { return removeClusterCmd.RunE(removeClusterCmd, []string{"legacy-1"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Cluster legacy-1 removed.") || strings.Contains(out, "waiting for merge") {
		t.Errorf("a merged removal is done: %q", out)
	}
}

func TestUnadopt_WaitingPRSaysWaitingNotUnadopted(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, map[string]interface{}{
			"name": "legacy-1", "status": "success",
			"git": map[string]interface{}{"pr_url": waitPR, "merged": false},
		})
	})
	resetFlags(unadoptClusterCmd)
	setFlags(t, unadoptClusterCmd, map[string]string{"yes": "true"})
	out, err := captureStdoutT(t, func() error { return unadoptClusterCmd.RunE(unadoptClusterCmd, []string{"legacy-1"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "PR opened, waiting for merge: "+waitPR+"\n") {
		t.Errorf("missing the waiting line: %q", out)
	}
	if strings.Contains(out, "un-adopted.") || strings.Contains(out, "... done") {
		t.Errorf("old wording must not appear: %q", out)
	}
}

func TestAdopt_WaitingPRSaysWaitingNotAdopted(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, map[string]interface{}{
			"results": []map[string]interface{}{{
				"name": "legacy-1", "status": "success",
				"git": map[string]interface{}{"pr_url": waitPR, "merged": false},
			}},
		})
	})
	resetFlags(adoptCmd)
	setFlags(t, adoptCmd, map[string]string{"yes": "true"})
	out, err := captureStdoutT(t, func() error { return adoptCmd.RunE(adoptCmd, []string{"legacy-1"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "legacy-1: PR opened, waiting for merge: "+waitPR+"\n") {
		t.Errorf("missing the waiting line: %q", out)
	}
	if !strings.Contains(out, "Nothing is adopted yet: 1 cluster waits for the pull request to be merged.") {
		t.Errorf("missing the summary: %q", out)
	}
	for _, banned := range []string{"legacy-1: adopted", "finished for every cluster", "open — merge manually"} {
		if strings.Contains(out, banned) {
			t.Errorf("old wording %q must not appear: %q", banned, out)
		}
	}
}

func TestAdopt_MergedPRSaysAdopted(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, map[string]interface{}{
			"results": []map[string]interface{}{{
				"name": "legacy-1", "status": "success",
				"git": map[string]interface{}{"pr_url": waitPR, "merged": true},
			}},
		})
	})
	resetFlags(adoptCmd)
	setFlags(t, adoptCmd, map[string]string{"yes": "true"})
	out, _ := captureStdoutT(t, func() error { return adoptCmd.RunE(adoptCmd, []string{"legacy-1"}) })
	if !strings.Contains(out, "legacy-1: adopted") {
		t.Errorf("a merged adoption is adopted: %q", out)
	}
}

// takeover without --acknowledge: the checks print once, the words are the
// CLI's own, and the error is not printed a second time by the command.
func TestTakeover_UnreadWarning_PrintsChecksOnceWithCLIWords(t *testing.T) {
	report := preflightReport{
		Cluster: "legacy-1", Ready: true, Summary: "One warning to read first.",
		Findings: []preflightFinding{{ID: "appset-deletion-safety", Title: "ApplicationSet reacts on removal", Status: "warning"}},
	}
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			jsonReply(w, 200, report)
			return
		}
		jsonReply(w, 409, map[string]interface{}{
			"error":                   "1 warning has not been read yet — look at appset-deletion-safety in the checks below, then send its id back in acknowledged_findings: x",
			"unacknowledged_findings": []string{"appset-deletion-safety"},
			"preflight":               report,
		})
	})
	resetFlags(takeoverCmd)
	setFlags(t, takeoverCmd, map[string]string{"yes": "true"})
	out, err := captureStdoutT(t, func() error { return takeoverCmd.RunE(takeoverCmd, []string{"legacy-1"}) })
	if err == nil {
		t.Fatal("expected an error")
	}
	if n := strings.Count(out, "Preflight for legacy-1"); n != 1 {
		t.Errorf("the checks must print once, printed %d times: %q", n, out)
	}
	want := "1 warning has not been read yet. Look at appset-deletion-safety in the checks above, then run the command again with --acknowledge appset-deletion-safety"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
	if strings.Contains(out, want) {
		t.Errorf("the command must not print the error itself, Execute does: %q", out)
	}
	if !strings.Contains(out, "Rerun with: sharko takeover legacy-1 -y --acknowledge appset-deletion-safety\n") {
		t.Errorf("Rerun line lost: %q", out)
	}
	for _, banned := range []string{"acknowledged_findings", "checks below", "send its id back"} {
		if strings.Contains(out, banned) || strings.Contains(err.Error(), banned) {
			t.Errorf("old wording %q must not appear", banned)
		}
	}
}

func TestTakeoverAckSentence_ManyWarnings(t *testing.T) {
	got := takeoverAckSentence([]string{"a", "b"})
	want := "2 warnings have not been read yet. Look at a, b in the checks above, then run the command again with --acknowledge a --acknowledge b"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// An error prints once and without the usage text.
func TestRoot_SilencesUsageAndErrors(t *testing.T) {
	if !rootCmd.SilenceUsage || !rootCmd.SilenceErrors {
		t.Fatal("rootCmd must set SilenceUsage and SilenceErrors")
	}
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"takeover"}) // missing the cluster name
	t.Cleanup(func() { rootCmd.SetOut(nil); rootCmd.SetErr(nil); rootCmd.SetArgs(nil) })
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected an argument error")
	}
	if buf.Len() != 0 {
		t.Errorf("cobra must print nothing itself (main prints the error once): %q", buf.String())
	}
}

func TestHelp_Wording(t *testing.T) {
	usage := takeoverCmd.Flags().Lookup("preserve-legacy-labels").Usage
	if strings.Contains(usage, "default true") {
		t.Errorf("the flag text must not carry its own default: %q", usage)
	}
	full := takeoverCmd.Flags().FlagUsages()
	if n := strings.Count(full, "(default true)"); n != 1 {
		t.Errorf("preserve-legacy-labels help shows its default %d times, want once: %q", n, full)
	}
	if strings.Contains(adoptCmd.Long, "values file") || strings.Contains(adoptCmd.Short+adoptCmd.Long, "default-addons") {
		t.Errorf("adopt help still uses v3 words: %q", adoptCmd.Long)
	}
	if strings.Contains(testClusterCmd.Long, "credentials stored in the secrets") {
		t.Errorf("test-cluster help is untrue for ArgoCD-held connections: %q", testClusterCmd.Long)
	}
	if !strings.Contains(strings.Join(strings.Fields(testClusterCmd.Long), " "), "the one stored in ArgoCD") {
		t.Errorf("test-cluster help must cover ArgoCD-held connections: %q", testClusterCmd.Long)
	}
}

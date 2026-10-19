package main

// add_cluster_dryrun_partial_test.go — LO-7 R5 (epic video-leftovers,
// 2026-10-18). A register dry run that stopped part-way answers 207 with a
// "partial" result now (before: 502). The CLI says why and exits non-zero.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAddClusterDryRun_PartialSaysWhyAndExitsNonZero(t *testing.T) {
	const msg = "Sharko could not read the cluster registry from Git, so it could not show what registering this cluster would change. Nothing was changed. Try again once Git is reachable."
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":      "partial",
			"failed_step": "read_cluster_registry",
			"error":       "could not read configuration/managed-clusters.yaml from the main branch, so Sharko could not show what registering cluster prod-eu would change: the git host did not answer",
			"message":     msg,
		})
	})
	resetFlags(addClusterCmd)
	setFlags(t, addClusterCmd, map[string]string{"dry-run": "true"})
	out, err := captureStdoutT(t, func() error {
		return addClusterCmd.RunE(addClusterCmd, []string{"prod-eu"})
	})
	if err == nil {
		t.Error("the preview could not be built, and the command still exits zero")
	} else if err.Error() != "the preview for cluster prod-eu could not be built" {
		t.Errorf("error = %q", err.Error())
	}
	for _, want := range []string{msg, "Failed step: read_cluster_registry", "the git host did not answer"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

// LO-8 R6: the real registration (not the preview) that could not read the
// cluster registry from Git answers 207 with no pull request. The CLI must
// print the server's sentence, never claim a pull request, and exit non-zero.
func TestAddCluster_ReadRegistryFailedSaysWhyAndExitsNonZero(t *testing.T) {
	const msg = "Sharko could not read the cluster registry from Git, so it stopped before opening a pull request — writing one built from an empty file would have dropped every other cluster out of the registry. Nothing was committed. Try again once Git is reachable."
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":          "partial",
			"cluster":         map[string]any{"name": "prod-eu"},
			"completed_steps": []string{"validate"},
			"failed_step":     "read_cluster_registry",
			"error":           "the git host did not answer",
			"message":         msg,
		})
	})
	resetFlags(addClusterCmd)
	out, err := captureStdoutT(t, func() error {
		return addClusterCmd.RunE(addClusterCmd, []string{"prod-eu"})
	})
	if err == nil {
		t.Error("the registration stopped before Git, and the command still exits zero")
	}
	for _, want := range []string{
		"Cluster registration did not finish on prod-eu.",
		"  Failed step: read_cluster_registry\n",
		"  Error:       the git host did not answer\n",
		"  Note:    " + msg + "\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	for _, banned := range []string{"Cluster registered:", "Git:     PR", "submitted as a pull request"} {
		if strings.Contains(out, banned) {
			t.Errorf("output says %q, but no pull request was opened:\n%s", banned, out)
		}
	}
}

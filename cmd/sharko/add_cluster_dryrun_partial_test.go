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

package main

import (
	"net/http"
	"testing"
)

// EP3 story T1c: a printed "run this" line keeps every flag the user typed.
// Before, remove-cluster legacy-1 --cleanup git printed a line without
// --cleanup git, and following it also removed the ArgoCD connection.

func TestRemoveCluster_WithoutYes_SuggestionKeepsCleanup(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("no API call may happen without -y, got: %s %s", r.Method, r.URL.Path)
	})
	for _, tc := range []struct{ cleanup, want string }{
		{"git", "removing a cluster needs an explicit confirmation.\nFirst see what it would do:  sharko unregister-consequences legacy-1\nThen run:                    sharko remove-cluster legacy-1 -y --cleanup git"},
		{"none", "removing a cluster needs an explicit confirmation.\nFirst see what it would do:  sharko unregister-consequences legacy-1\nThen run:                    sharko remove-cluster legacy-1 -y --cleanup none"},
		{"", "removing a cluster needs an explicit confirmation.\nFirst see what it would do:  sharko unregister-consequences legacy-1\nThen run:                    sharko remove-cluster legacy-1 -y"},
	} {
		resetFlags(removeClusterCmd)
		if tc.cleanup != "" {
			setFlags(t, removeClusterCmd, map[string]string{"cleanup": tc.cleanup})
		}
		_, err := captureStdoutT(t, func() error { return removeClusterCmd.RunE(removeClusterCmd, []string{"legacy-1"}) })
		if err == nil || err.Error() != tc.want {
			t.Errorf("cleanup %q:\n got %v\nwant %q", tc.cleanup, err, tc.want)
		}
	}
}

// The take over rerun line carries every flag that was typed, in one fixed order.
func TestTakeoverRerunLine_KeepsEveryTypedFlag(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			jsonReply(w, 200, preflightReport{Cluster: "legacy-1", Ready: true})
			return
		}
		jsonReply(w, 409, map[string]interface{}{
			"error":                   "1 warning has not been read yet",
			"unacknowledged_findings": []string{"secret-owner"},
		})
	})
	resetFlags(takeoverCmd)
	setFlags(t, takeoverCmd, map[string]string{
		"yes": "true", "dry-run": "true", "region": "eu-west-1",
		"auto-merge": "false", "preserve-legacy-labels": "false",
	})
	setRepeatedFlag(t, takeoverCmd, "acknowledge", "appset-deletion-safety")
	out, err := captureStdoutT(t, func() error { return takeoverCmd.RunE(takeoverCmd, []string{"legacy-1"}) })
	if err == nil {
		t.Fatal("expected the 409 error")
	}
	want := "Rerun with: sharko takeover legacy-1 -y --acknowledge appset-deletion-safety --acknowledge secret-owner --dry-run --region eu-west-1 --auto-merge=false --preserve-legacy-labels=false\n"
	if len(out) < len(want) || out[len(out)-len(want):] != want {
		t.Errorf("rerun line wrong or not last:\n got %q\nwant suffix %q", out, want)
	}
}

// The drop-legacy-labels rerun line keeps --label, --acknowledge and --dry-run.
func TestDropLegacyLabelsRerunLine_KeepsEveryTypedFlag(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 409, map[string]interface{}{
			"error":                   "1 warning has not been read yet",
			"unacknowledged_findings": []string{"appset:legacy-selector"},
			"warnings":                []string{"The ApplicationSet picks clusters using team."},
			"warning_ids":             []string{"appset:legacy-selector"},
			"labels":                  []string{"team"},
		})
	})
	resetFlags(dropLegacyLabelsCmd)
	setFlags(t, dropLegacyLabelsCmd, map[string]string{"yes": "true", "dry-run": "true"})
	setRepeatedFlag(t, dropLegacyLabelsCmd, "label", "team")
	setRepeatedFlag(t, dropLegacyLabelsCmd, "acknowledge", "other-id")
	out, err := captureStdoutT(t, func() error { return dropLegacyLabelsCmd.RunE(dropLegacyLabelsCmd, []string{"legacy-1"}) })
	if err == nil {
		t.Fatal("expected the 409 error")
	}
	want := "Rerun with: sharko drop-legacy-labels legacy-1 -y --label team --acknowledge other-id --acknowledge appset:legacy-selector --dry-run\n"
	if len(out) < len(want) || out[len(out)-len(want):] != want {
		t.Errorf("rerun line wrong or not last:\n got %q\nwant suffix %q", out, want)
	}
}

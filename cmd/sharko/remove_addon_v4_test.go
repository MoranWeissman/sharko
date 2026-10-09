package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// v4.0.4 U9a: on a repo in the new format, `sharko remove-addon` must use
// DELETE /api/v1/catalog/addons/{name}. The old door refuses v4 repos with
// repo_layout, so a CLI still calling it can never remove anything.

func repoStatusHandler(format string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"initialized": true, "bootstrap_synced": true, "format": format,
		})
	}
}

func TestRemoveAddon_V4Repo_UsesCatalogDoor(t *testing.T) {
	var deletes []string
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repo/status":
			repoStatusHandler("v4")(w)
		case r.Method == http.MethodDelete:
			deletes = append(deletes, r.URL.Path+"?"+r.URL.RawQuery)
			if r.URL.Path != "/api/v1/catalog/addons/cert-manager" {
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "repo layout", "code": "repo_layout"})
				return
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"pr_url": "https://git.example/pr/21", "pr_id": 21, "merged": false})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	resetFlags(removeAddonCmd)
	setFlags(t, removeAddonCmd, map[string]string{"confirm": "true"})
	out, err := captureStdoutT(t, func() error {
		return removeAddonCmd.RunE(removeAddonCmd, []string{"cert-manager"})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v (output: %s)", err, out)
	}
	if len(deletes) != 1 || deletes[0] != "/api/v1/catalog/addons/cert-manager?confirm=true" {
		t.Fatalf("expected one DELETE on the catalog door with confirm, got %v", deletes)
	}
	if !strings.Contains(out, "Pull request opened to remove addon cert-manager from the catalog: https://git.example/pr/21") {
		t.Errorf("expected the PR sentence, got %q", out)
	}
	if !strings.Contains(out, "Nothing changes until it is merged.") {
		t.Errorf("expected the not-merged sentence, got %q", out)
	}
	if strings.Contains(out, "Addon cert-manager removed from catalog.") {
		t.Errorf("must not claim removal while the PR is still open: %q", out)
	}
}

func TestRemoveAddon_V4Repo_StillOnNamesTheClusters(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repo/status":
			repoStatusHandler("v4")(w)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/catalog/addons/cert-manager":
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "addon is enabled", "code": "addon_enabled_on_clusters",
				"addon": "cert-manager", "clusters": []string{"spoke-eu", "spoke-us"},
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	resetFlags(removeAddonCmd)
	setFlags(t, removeAddonCmd, map[string]string{"confirm": "true"})
	_, err := captureStdoutT(t, func() error {
		return removeAddonCmd.RunE(removeAddonCmd, []string{"cert-manager"})
	})
	want := "cert-manager is still switched on for spoke-eu, spoke-us. Switch it off there first (sharko disable-addon <cluster> cert-manager), then remove it from the catalog."
	if err == nil || err.Error() != want {
		t.Fatalf("expected %q, got %v", want, err)
	}
}

func TestRemoveAddon_V4Repo_NoConfirmPrintsImpact(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repo/status":
			repoStatusHandler("v4")(w)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/catalog/addons/cert-manager":
			if r.URL.RawQuery != "" {
				t.Errorf("dry run must not send confirm, got %q", r.URL.RawQuery)
			}
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error":  "confirmation required",
				"impact": map[string]interface{}{"addon": "cert-manager", "files_removed": []string{"catalog.yaml", "values/global/cert-manager.yaml"}},
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	resetFlags(removeAddonCmd)
	out, err := captureStdoutT(t, func() error {
		return removeAddonCmd.RunE(removeAddonCmd, []string{"cert-manager"})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, s := range []string{"values/global/cert-manager.yaml", "Run with --confirm to execute removal."} {
		if !strings.Contains(out, s) {
			t.Errorf("expected %q in output: %q", s, out)
		}
	}
}

func TestRemoveAddon_V3Repo_KeepsOldDoor(t *testing.T) {
	var deleted string
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repo/status":
			repoStatusHandler("v3")(w)
		case r.Method == http.MethodDelete:
			deleted = r.URL.Path
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	resetFlags(removeAddonCmd)
	setFlags(t, removeAddonCmd, map[string]string{"confirm": "true"})
	if _, err := captureStdoutT(t, func() error {
		return removeAddonCmd.RunE(removeAddonCmd, []string{"cert-manager"})
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted != "/api/v1/addons/cert-manager" {
		t.Fatalf("a v3 repo must keep the old door, got %q", deleted)
	}
}

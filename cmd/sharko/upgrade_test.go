package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestUpgradeAddon_V3RepoUnchanged pins the existing behavior: on a v3 repo
// (or whenever the format probe can't be answered), upgrade-addon still
// hits the old global-upgrade route exactly as it always has.
func TestUpgradeAddon_V3RepoUnchanged(t *testing.T) {
	var hitUpgradeRoute bool
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/migration/status":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(repoFormatStatus{Format: "v3"})
		case r.URL.Path == "/api/v1/addons/cert-manager/upgrade":
			hitUpgradeRoute = true
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"pr_url": "https://git.example/pr/1"})
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
	})

	resetFlags(upgradeAddonCmd)
	setFlags(t, upgradeAddonCmd, map[string]string{"version": "1.15.0"})

	out, err := captureStdoutT(t, func() error {
		return upgradeAddonCmd.RunE(upgradeAddonCmd, []string{"cert-manager"})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v (output: %s)", err, out)
	}
	if !hitUpgradeRoute {
		t.Error("expected the v3 /upgrade route to be called")
	}
}

// TestUpgradeAddon_V4WithCluster_RedirectsToUpgradeClusters pins the
// "transparently uses the upgrade-clusters route" behavior the spec calls
// for: on a v4 repo, upgrade-addon with --cluster should hit
// /addons/{name}/upgrade-clusters instead of the dead-end v3 route.
func TestUpgradeAddon_V4WithCluster_RedirectsToUpgradeClusters(t *testing.T) {
	var hitClustersRoute bool
	var gotBody map[string]interface{}
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/migration/status":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(repoFormatStatus{Format: "v4"})
		case r.URL.Path == "/api/v1/addons/cert-manager/upgrade-clusters":
			hitClustersRoute = true
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(cliGitResult{PRUrl: "https://git.example/pr/2"})
		case r.URL.Path == "/api/v1/addons/cert-manager/upgrade":
			t.Fatal("v4 repo must not hit the dead-end v3 /upgrade route")
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
	})

	resetFlags(upgradeAddonCmd)
	setFlags(t, upgradeAddonCmd, map[string]string{"version": "1.15.0", "cluster": "staging-us"})

	out, err := captureStdoutT(t, func() error {
		return upgradeAddonCmd.RunE(upgradeAddonCmd, []string{"cert-manager"})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v (output: %s)", err, out)
	}
	if !hitClustersRoute {
		t.Fatal("expected the v4 upgrade-clusters route to be called")
	}
	if !strings.Contains(out, "routing through 'upgrade-clusters'") {
		t.Errorf("expected the CLI to say which route it used: %q", out)
	}
	clusters, _ := gotBody["clusters"].([]interface{})
	if len(clusters) != 1 || clusters[0] != "staging-us" {
		t.Errorf("expected clusters=[staging-us], got %v", gotBody["clusters"])
	}
}

// TestUpgradeAddon_V4WithoutCluster_PlainEnglishError pins the "dead end
// becomes a plain-English error with the exact next command" requirement.
func TestUpgradeAddon_V4WithoutCluster_PlainEnglishError(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/migration/status" {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(repoFormatStatus{Format: "v4"})
			return
		}
		t.Fatalf("unexpected request past the format probe: %s", r.URL.Path)
	})

	resetFlags(upgradeAddonCmd)
	setFlags(t, upgradeAddonCmd, map[string]string{"version": "1.15.0"})

	_, err := captureStdoutT(t, func() error {
		return upgradeAddonCmd.RunE(upgradeAddonCmd, []string{"cert-manager"})
	})
	if err == nil {
		t.Fatal("expected an error explaining the v4 per-cluster pin model")
	}
	if !strings.Contains(err.Error(), "sharko upgrade-clusters cert-manager --version 1.15.0") {
		t.Errorf("expected the exact next command in the error: %v", err)
	}
}

// TestUpgradeAddon_MixedRepo_SurfacesServerMessage pins the "mixed" branch:
// the server's own message is what gets shown, not a CLI-invented one.
func TestUpgradeAddon_MixedRepo_SurfacesServerMessage(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/migration/status" {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(repoFormatStatus{
				Format:  "mixed",
				Message: "this repo carries both the old and the new file layout — finish the migration before upgrading addons",
			})
			return
		}
		t.Fatalf("unexpected request past the format probe: %s", r.URL.Path)
	})

	resetFlags(upgradeAddonCmd)
	setFlags(t, upgradeAddonCmd, map[string]string{"version": "1.15.0"})

	_, err := captureStdoutT(t, func() error {
		return upgradeAddonCmd.RunE(upgradeAddonCmd, []string{"cert-manager"})
	})
	if err == nil || !strings.Contains(err.Error(), "finish the migration") {
		t.Fatalf("expected the server's mixed-layout message verbatim, got: %v", err)
	}
}

// TestUpgradeAddons_V4_ListsOneCommandPerAddon pins the batch-command
// behavior: on v4, upgrade-addons has no single equivalent call, so it
// prints one upgrade-clusters command per addon in the batch.
func TestUpgradeAddons_V4_ListsOneCommandPerAddon(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/migration/status" {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(repoFormatStatus{Format: "v4"})
			return
		}
		t.Fatalf("unexpected request past the format probe: %s", r.URL.Path)
	})

	_, err := captureStdoutT(t, func() error {
		return upgradeAddonsCmd.RunE(upgradeAddonsCmd, []string{"cert-manager=1.15.0"})
	})
	if err == nil {
		t.Fatal("expected an error explaining there is no v4 batch route")
	}
	if !strings.Contains(err.Error(), "sharko upgrade-clusters cert-manager --version 1.15.0") {
		t.Errorf("expected the per-addon command in the error: %v", err)
	}
}

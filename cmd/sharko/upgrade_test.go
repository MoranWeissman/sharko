package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Pre-release walk 4: on the current repo layout `sharko upgrade-addon`
// without --cluster refused to run ("this repo uses the v4 format ... pick
// the clusters"), and `sharko upgrade-addons` had no working route at all.
// The command line now does what the Upgrade button does: one call to
// POST /addons/{name}/upgrade, for every layout, and the server decides
// what an upgrade writes.

func TestUpgradeAddon_AllClusters_CallsTheUpgradeRoute(t *testing.T) {
	var gotBody map[string]interface{}
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/addons/podinfo/upgrade":
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"pr_url": "https://git.example/pr/1"})
		default:
			t.Fatalf("unexpected request: %s — the command must not ask about the repo layout first, or use another route", r.URL.Path)
		}
	})

	resetFlags(upgradeAddonCmd)
	setFlags(t, upgradeAddonCmd, map[string]string{"version": "6.8.0"})

	out, err := captureStdoutT(t, func() error {
		return upgradeAddonCmd.RunE(upgradeAddonCmd, []string{"podinfo"})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v (output: %s)", err, out)
	}
	if gotBody["version"] != "6.8.0" {
		t.Errorf("body = %v, want version 6.8.0", gotBody)
	}
	if _, has := gotBody["cluster"]; has {
		t.Errorf("no --cluster was given, so the body must not name one: %v", gotBody)
	}
	if !strings.Contains(out, "Upgrading addon podinfo to 6.8.0 on all clusters... done") {
		t.Errorf("output must say the upgrade is for all clusters: %q", out)
	}
	if !strings.Contains(out, "PR: https://git.example/pr/1") {
		t.Errorf("output must show the pull request: %q", out)
	}
	for _, banned := range []string{"this repo uses the v4 format", "routing through", "globally"} {
		if strings.Contains(out, banned) {
			t.Errorf("old wording %q is back: %q", banned, out)
		}
	}
}

func TestUpgradeAddon_OneCluster_SendsTheClusterAndPrintsWarnings(t *testing.T) {
	var gotBody map[string]interface{}
	const warning = "values/global/podinfo.yaml is shared by every cluster and still sets the old chart's value for: image.tag (6.7.1, new chart default 6.8.0)."
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/addons/podinfo/upgrade" {
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"pr_url": "https://git.example/pr/2", "warnings": []string{warning},
		})
	})

	resetFlags(upgradeAddonCmd)
	setFlags(t, upgradeAddonCmd, map[string]string{"version": "6.8.0", "cluster": "spoke-1"})

	out, err := captureStdoutT(t, func() error {
		return upgradeAddonCmd.RunE(upgradeAddonCmd, []string{"podinfo"})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v (output: %s)", err, out)
	}
	if gotBody["cluster"] != "spoke-1" {
		t.Errorf("body = %v, want cluster spoke-1", gotBody)
	}
	if !strings.Contains(out, "on cluster spoke-1") {
		t.Errorf("output must name the cluster: %q", out)
	}
	if !strings.Contains(out, "Warning: "+warning) {
		t.Errorf("the server's warning must be printed, so the command line never reports a clean upgrade while the old image keeps running: %q", out)
	}
}

func TestUpgradeAddon_DryRun_SendsDryRunAndPrintsThePreview(t *testing.T) {
	var gotBody map[string]interface{}
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(cliGitResult{DryRun: &cliDryRunResult{
			PRTitle:      "sharko: upgrade addon podinfo to 6.8.0 on all clusters",
			FilesToWrite: []cliFilePreview{{Path: "catalog.yaml", Action: "update"}},
		}})
	})

	resetFlags(upgradeAddonCmd)
	setFlags(t, upgradeAddonCmd, map[string]string{"version": "6.8.0", "dry-run": "true"})

	out, err := captureStdoutT(t, func() error {
		return upgradeAddonCmd.RunE(upgradeAddonCmd, []string{"podinfo"})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v (output: %s)", err, out)
	}
	if gotBody["dry_run"] != true {
		t.Errorf("body = %v, want dry_run true", gotBody)
	}
	if !strings.Contains(out, "Dry-run preview (no changes made)") || !strings.Contains(out, "[update] catalog.yaml") {
		t.Errorf("the preview must be printed: %q", out)
	}
}

// A version that does not exist: the server's sentence reaches the person,
// and the command fails.
func TestUpgradeAddon_VersionThatDoesNotExist_ShowsTheServerSentence(t *testing.T) {
	const sentence = "Version 99.0.0 of podinfo was not found in its chart repository (https://stefanprodan.github.io/podinfo). Nothing was changed. Pick a version that the repository lists."
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": sentence, "code": "version_not_found"})
	})

	resetFlags(upgradeAddonCmd)
	setFlags(t, upgradeAddonCmd, map[string]string{"version": "99.0.0"})

	_, err := captureStdoutT(t, func() error {
		return upgradeAddonCmd.RunE(upgradeAddonCmd, []string{"podinfo"})
	})
	if err == nil {
		t.Fatal("expected the command to fail")
	}
	if !strings.Contains(err.Error(), sentence) {
		t.Errorf("error = %v, want the server's sentence in it", err)
	}
}

func TestUpgradeAddons_Batch_CallsTheBatchRoute(t *testing.T) {
	var gotBody map[string]interface{}
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/addons/upgrade-batch" {
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"pr_url": "https://git.example/pr/3"})
	})

	resetFlags(upgradeAddonsCmd)
	out, err := captureStdoutT(t, func() error {
		return upgradeAddonsCmd.RunE(upgradeAddonsCmd, []string{"podinfo=6.9.0,reloader=2.2.22"})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v (output: %s)", err, out)
	}
	upgrades, _ := gotBody["upgrades"].(map[string]interface{})
	if upgrades["podinfo"] != "6.9.0" || upgrades["reloader"] != "2.2.22" {
		t.Errorf("body = %v", gotBody)
	}
	if !strings.Contains(out, "PR: https://git.example/pr/3") {
		t.Errorf("output must show the pull request: %q", out)
	}
}

// Walk 4 finding 6: `--cluster spoke-1 --cluster spoke-1` was counted as two
// clusters.
func TestUpgradeClusters_SameClusterTwice_IsCountedOnce(t *testing.T) {
	var gotBody map[string]interface{}
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(cliGitResult{PRUrl: "https://git.example/pr/4"})
	})

	resetFlags(upgradeClustersCmd)
	setFlags(t, upgradeClustersCmd, map[string]string{"version": "6.8.0", "yes": "true"})
	setRepeatedFlag(t, upgradeClustersCmd, "cluster", "spoke-1", "spoke-1")

	out, err := captureStdoutT(t, func() error {
		return upgradeClustersCmd.RunE(upgradeClustersCmd, []string{"podinfo"})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v (output: %s)", err, out)
	}
	if !strings.Contains(out, "on 1 cluster(s)") {
		t.Errorf("one cluster named twice is one cluster: %q", out)
	}
	if clusters, _ := gotBody["clusters"].([]interface{}); len(clusters) != 1 {
		t.Errorf("body clusters = %v, want one", gotBody["clusters"])
	}
}

// The --help text is the only place the command line says what "for all
// clusters" changes, so it is pinned.
func TestUpgradeAddon_HelpText(t *testing.T) {
	want := `Upgrades one addon in one pull request.

Without --cluster the addon is upgraded for ALL clusters: the version in the
catalog changes, and a cluster that had its own version for this addon goes
back to following the catalog. A cluster that is already on a newer version
is left as it is.

With --cluster only that cluster moves. The catalog and the other clusters
stay as they are. To move several clusters at once, use 'sharko
upgrade-clusters'.

The version must exist in the addon's chart repository. A version that is
not there is refused and no pull request is opened.`
	if upgradeAddonCmd.Long != want {
		t.Errorf("help text changed:\n%s", upgradeAddonCmd.Long)
	}
	for _, banned := range []string{"v4", "global or per-cluster", "per-cluster override"} {
		if strings.Contains(upgradeAddonCmd.Long+upgradeAddonCmd.Short, banned) {
			t.Errorf("old wording %q is back in the help text", banned)
		}
	}
}

package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestEnableAddon_HappyPath(t *testing.T) {
	var gotBody map[string]interface{}
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/v4/clusters/prod-eu/addons/cert-manager" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(cliGitResult{PRUrl: "https://git.example/pr/9", Merged: true})
	})

	resetFlags(enableAddonCmd)
	setFlags(t, enableAddonCmd, map[string]string{
		"version": "1.15.0",
		"yes":     "true",
	})

	out, err := captureStdoutT(t, func() error {
		return enableAddonCmd.RunE(enableAddonCmd, []string{"prod-eu", "cert-manager"})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v (output: %s)", err, out)
	}
	if !strings.Contains(out, "https://git.example/pr/9") {
		t.Errorf("expected PR URL in output: %q", out)
	}
	if gotBody["version"] != "1.15.0" {
		t.Errorf("expected version=1.15.0 in request body, got %v", gotBody)
	}
	if gotBody["yes"] != true {
		t.Errorf("expected yes=true in request body, got %v", gotBody)
	}
}

func TestEnableAddon_ClearVersionAndVersionConflict(t *testing.T) {
	resetFlags(enableAddonCmd)
	setFlags(t, enableAddonCmd, map[string]string{
		"version":       "1.0.0",
		"clear-version": "true",
	})
	_, err := captureStdoutT(t, func() error {
		return enableAddonCmd.RunE(enableAddonCmd, []string{"prod-eu", "cert-manager"})
	})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutually-exclusive error, got: %v", err)
	}
}

func TestEnableAddon_ValuesJSONAndSemanticValidationError(t *testing.T) {
	var gotBody map[string]interface{}
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error":    "cluster prod-eu is missing required values for cert-manager",
			"code":     "validation_failed",
			"cluster":  "prod-eu",
			"addon":    "cert-manager",
			"problems": []string{"installCRDs is required"},
		})
	})

	resetFlags(enableAddonCmd)
	setFlags(t, enableAddonCmd, map[string]string{
		"values-json": `{"installCRDs": true}`,
		"yes":         "true",
	})

	_, err := captureStdoutT(t, func() error {
		return enableAddonCmd.RunE(enableAddonCmd, []string{"prod-eu", "cert-manager"})
	})
	if err == nil {
		t.Fatal("expected an error for the 422 response")
	}
	if !strings.Contains(err.Error(), "validation_failed") {
		t.Errorf("expected the code to be surfaced: %v", err)
	}
	if !strings.Contains(err.Error(), "installCRDs is required") {
		t.Errorf("expected the problems list to be surfaced: %v", err)
	}
	values, ok := gotBody["values"].(map[string]interface{})
	if !ok || values["installCRDs"] != true {
		t.Errorf("expected values.installCRDs=true in request body, got %v", gotBody)
	}
}

func TestDisableAddon_HappyPath(t *testing.T) {
	var gotMethod string
	var gotBody map[string]interface{}
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		if r.URL.Path != "/api/v1/v4/clusters/prod-eu/addons/cert-manager" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(cliGitResult{Branch: "sharko/disable-cert-manager"})
	})

	resetFlags(disableAddonCmd)
	setFlags(t, disableAddonCmd, map[string]string{"remove": "true", "yes": "true"})

	out, err := captureStdoutT(t, func() error {
		return disableAddonCmd.RunE(disableAddonCmd, []string{"prod-eu", "cert-manager"})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("expected DELETE, got %s", gotMethod)
	}
	if gotBody["remove"] != true {
		t.Errorf("expected remove=true in DELETE body, got %v", gotBody)
	}
	if !strings.Contains(out, "sharko/disable-cert-manager") {
		t.Errorf("expected branch name in output: %q", out)
	}
}

func TestUpgradeClusters_HappyPath(t *testing.T) {
	var gotBody map[string]interface{}
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/addons/cert-manager/upgrade-clusters" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		// Wrapped in the attribution shape — exercises unwrapAttribution.
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"result":              cliGitResult{PRUrl: "https://git.example/pr/11"},
			"attribution_warning": "no_per_user_pat",
		})
	})

	resetFlags(upgradeClustersCmd)
	setFlags(t, upgradeClustersCmd, map[string]string{"version": "1.16.0", "yes": "true"})
	setRepeatedFlag(t, upgradeClustersCmd, "cluster", "prod-eu", "staging-us")

	out, err := captureStdoutT(t, func() error {
		return upgradeClustersCmd.RunE(upgradeClustersCmd, []string{"cert-manager"})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v (output: %s)", err, out)
	}
	if !strings.Contains(out, "https://git.example/pr/11") {
		t.Errorf("expected unwrapped PR URL in output: %q", out)
	}
	if !strings.Contains(out, "personal access token") {
		t.Errorf("expected the attribution note to be printed: %q", out)
	}
	clusters, ok := gotBody["clusters"].([]interface{})
	if !ok || len(clusters) != 2 {
		t.Errorf("expected 2 clusters in request body, got %v", gotBody["clusters"])
	}
}

func TestUpgradeClusters_RequiresClusterFlag(t *testing.T) {
	resetFlags(upgradeClustersCmd)
	setFlags(t, upgradeClustersCmd, map[string]string{"version": "1.16.0"})

	_, err := captureStdoutT(t, func() error {
		return upgradeClustersCmd.RunE(upgradeClustersCmd, []string{"cert-manager"})
	})
	if err == nil || !strings.Contains(err.Error(), "--cluster is required") {
		t.Fatalf("expected a --cluster-required error, got: %v", err)
	}
}

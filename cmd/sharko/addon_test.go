package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestListAddons_UsesListRoute pins the 404-bug fix: the old code called
// GET /api/v1/addons, a route that does not exist (the real one is
// GET /api/v1/addons/list). This also pins the response shape fix — the
// route returns models.AddonCatalogEntry objects under the "applicationsets"
// key (the server's real field name), not the "addons" shape the old,
// never-actually-exercised code assumed.
func TestListAddons_UsesListRoute(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/addons/list" {
			t.Fatalf("unexpected request: %s %s (list-addons must call /api/v1/addons/list, not /api/v1/addons)", r.Method, r.URL.Path)
		}
		selfHeal := true
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"applicationsets": []addonCatalogEntry{
				{
					Name:        "cert-manager",
					RepoURL:     "https://charts.jetstack.io",
					Chart:       "cert-manager",
					Version:     "1.15.0",
					Namespace:   "cert-manager",
					SelfHeal:    &selfHeal,
					SyncOptions: []string{"CreateNamespace=true"},
				},
			},
		})
	})

	resetFlags(listAddonsCmd)
	setFlags(t, listAddonsCmd, map[string]string{"show-config": "true"})

	out, err := captureStdoutT(t, func() error {
		return listAddonsCmd.RunE(listAddonsCmd, nil)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v (output: %s)", err, out)
	}
	if !strings.Contains(out, "cert-manager") || !strings.Contains(out, "1.15.0") {
		t.Errorf("expected addon name/version in output: %q", out)
	}
	if !strings.Contains(out, "true") {
		t.Errorf("expected self-heal column in --show-config output: %q", out)
	}
}

// TestDescribeAddon_UsesRealRoute pins the second 404-bug fix: the old code
// called GET /api/v1/addons/{name}/detail, a route that does not exist (the
// real one is GET /api/v1/addons/{name}). This also pins the response
// shape fix — models.AddonDetailResponse{addon: AddonCatalogItem, ...},
// not the made-up "value/is_default" shape the old, never-exercised code
// assumed.
func TestDescribeAddon_UsesRealRoute(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/addons/cert-manager" {
			t.Fatalf("unexpected request: %s %s (describe-addon must call /api/v1/addons/{name}, not /detail)", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(addonDetailResponse{
			Addon: addonCatalogItem{
				AddonName:               "cert-manager",
				Chart:                   "cert-manager",
				RepoURL:                 "https://charts.jetstack.io",
				Namespace:               "cert-manager",
				Version:                 "1.15.0",
				TotalClusters:           3,
				EnabledClusters:         2,
				DeployedClusterCount:    2,
				TotalTargetClusterCount: 2,
				Applications: []addonDeploymentInfo{
					{ClusterName: "prod-eu", Enabled: true, DeployedVersion: "1.15.0", SyncStatus: "Synced", HealthStatus: "Healthy"},
				},
			},
			ApplicationSet: &addonApplicationSetStatus{Name: "cert-manager-appset", GeneratedApps: 2},
		})
	})

	out, err := captureStdoutT(t, func() error {
		return describeAddonCmd.RunE(describeAddonCmd, []string{"cert-manager"})
	})
	if err != nil {
		t.Fatalf("unexpected error: %v (output: %s)", err, out)
	}
	if !strings.Contains(out, "cert-manager") || !strings.Contains(out, "2/3 enabled") {
		t.Errorf("expected addon name and cluster counts in output: %q", out)
	}
	if !strings.Contains(out, "prod-eu") {
		t.Errorf("expected the per-cluster deployment row in output: %q", out)
	}
	if !strings.Contains(out, "cert-manager-appset") {
		t.Errorf("expected the ApplicationSet name in output: %q", out)
	}
}

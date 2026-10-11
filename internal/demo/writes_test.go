package demo

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/api"
)

// TestDemoServer_AddToCatalogAndEnable_PreviewAndApply does the two writes
// a demo user tries first, through the real HTTP router, against the demo
// backends: "Add to catalog" (POST /api/v1/catalog/addons) and "Add addon"
// on a cluster (POST /api/v1/v4/clusters/{name}/addons/{addon}). Each one
// is done twice: once as a preview (dry_run) and once for real, and the
// real one must come back with a pull request.
//
// Both used to fail with "repo has both layouts..." (code repo_layout),
// because the demo repo carried the old v3 files next to the v4 ones.
// The mixed-layout check is right to refuse that; the demo was wrong.
func TestDemoServer_AddToCatalogAndEnable_PreviewAndApply(t *testing.T) {
	// The real catalog add downloads the chart's values.yaml. Point the
	// chart at a local server that has nothing, so the test never touches
	// the network. Sharko then writes its "could not fetch" stub values
	// file and the add still goes ahead, which is the real behavior.
	charts := httptest.NewServer(http.NotFoundHandler())
	defer charts.Close()

	for name, cfg := range map[string]ScaleConfig{
		"default":   DefaultScaleConfig,
		"generated": {Clusters: 6, Addons: 4, Seed: 1},
	} {
		t.Run(name, func(t *testing.T) {
			srv := newTestServer(t)
			cleanup, err := SetupDemoServer(srv, cfg)
			if err != nil {
				t.Fatalf("SetupDemoServer: %v", err)
			}
			defer cleanup()

			router := api.NewRouter(srv, nil)
			token := demoLoginToken(t, router)

			// Pick a cluster and an addon that is in the catalog but not yet
			// on that cluster, from what the demo itself serves.
			cluster, addon := pickClusterAndMissingAddon(t, router, token)

			catalogEntry := map[string]any{
				"name":      "demo-test-addon",
				"repo_url":  charts.URL,
				"chart":     "demo-test-addon",
				"version":   "1.0.0",
				"namespace": "demo-test-addon",
			}

			// Add to catalog: preview, then for real.
			preview := demoPost(t, router, token, "/api/v1/catalog/addons",
				map[string]any{"addons": []any{catalogEntry}, "dry_run": true})
			wantStatus(t, "catalog add preview", preview, http.StatusCreated)

			applied := demoPost(t, router, token, "/api/v1/catalog/addons",
				map[string]any{"addons": []any{catalogEntry}})
			wantStatus(t, "catalog add", applied, http.StatusCreated)
			wantPR(t, "catalog add", applied, "result")

			// Enable on a cluster: preview, then for real.
			path := "/api/v1/v4/clusters/" + cluster + "/addons/" + addon
			preview = demoPost(t, router, token, path, map[string]any{"dry_run": true})
			wantStatus(t, "enable preview", preview, http.StatusOK)

			applied = demoPost(t, router, token, path, map[string]any{"yes": true})
			wantStatus(t, "enable", applied, http.StatusOK)
			wantPR(t, "enable", applied, "")
		})
	}
}

// pickClusterAndMissingAddon reads the version matrix and returns the first
// (cluster, addon) pair where the addon is approved but not on the
// cluster. It prefers datadog, which needs no required values.
func pickClusterAndMissingAddon(t *testing.T, router http.Handler, token string) (string, string) {
	t.Helper()
	rw := demoGet(t, router, token, "/api/v1/addons/version-matrix")
	wantStatus(t, "version matrix", rw, http.StatusOK)
	var m struct {
		Clusters []string `json:"clusters"`
		Addons   []struct {
			AddonName string                     `json:"addon_name"`
			Cells     map[string]json.RawMessage `json:"cells"`
		} `json:"addons"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode version matrix: %v", err)
	}
	type pair struct{ cluster, addon string }
	var first *pair
	for _, row := range m.Addons {
		for _, c := range m.Clusters {
			if _, on := row.Cells[c]; on {
				continue
			}
			if row.AddonName == "datadog" {
				return c, row.AddonName
			}
			if first == nil {
				first = &pair{c, row.AddonName}
			}
		}
	}
	if first == nil {
		t.Fatal("demo has no (cluster, addon) pair where the addon is off; nothing to enable")
	}
	return first.cluster, first.addon
}

func demoPost(t *testing.T, router http.Handler, token, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rw := httptest.NewRecorder()
	router.ServeHTTP(rw, req)
	return rw
}

func demoGet(t *testing.T, router http.Handler, token, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rw := httptest.NewRecorder()
	router.ServeHTTP(rw, req)
	return rw
}

func wantStatus(t *testing.T, what string, rw *httptest.ResponseRecorder, want int) {
	t.Helper()
	if strings.Contains(rw.Body.String(), `"repo_layout"`) {
		t.Fatalf("%s was refused as a mixed-layout repo (the demo carries v3 files next to v4): %s", what, rw.Body.String())
	}
	if rw.Code != want {
		t.Fatalf("%s: status = %d, want %d; body = %s", what, rw.Code, want, rw.Body.String())
	}
}

// wantPR checks a pull request id and URL came back. under names the
// object that holds them ("" means the top level).
func wantPR(t *testing.T, what string, rw *httptest.ResponseRecorder, under string) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: decode: %v; body = %s", what, err, rw.Body.String())
	}
	if under != "" {
		inner, ok := body[under].(map[string]any)
		if !ok {
			t.Fatalf("%s: no %q object in response: %s", what, under, rw.Body.String())
		}
		body = inner
	}
	url, _ := body["pr_url"].(string)
	id, _ := body["pr_id"].(float64)
	if url == "" || id == 0 {
		t.Fatalf("%s: no pull request came back (pr_id=%v pr_url=%q): %s", what, body["pr_id"], url, rw.Body.String())
	}
}

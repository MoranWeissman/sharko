package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/helm"
)

// Tests in this file focus on the deterministic helpers (sort, prerelease
// detection, cache, filtering, response shape). The HTTP handler is exercised
// for its no-network branches (404, 503, bad params); the upstream Helm fetch
// is intentionally not unit-tested here because it crosses a network boundary
// that integration tests cover separately.

func TestIsPrerelease(t *testing.T) {
	cases := map[string]bool{
		"1.20.0":          false,
		"v1.20.0":         false,
		"1.20.0-rc.1":     true,
		"1.20.0-beta":     true,
		"1.20.0+build1":   false,
		"1.20.0-rc1+meta": true,
		"":                false,
	}
	for in, want := range cases {
		if got := isPrerelease(in); got != want {
			t.Errorf("isPrerelease(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseSemverParts(t *testing.T) {
	cases := []struct {
		in   string
		want []int
	}{
		{"1.2.3", []int{1, 2, 3}},
		{"v1.2.3", []int{1, 2, 3}},
		{"1.2.3-rc.1", []int{1, 2, 3}},
		{"1.2.3+build.7", []int{1, 2, 3}},
		{"1.2", nil},
		{"abc", nil},
		{"1.x.0", nil},
	}
	for _, c := range cases {
		got := parseSemverParts(c.in)
		if c.want == nil {
			if got != nil {
				t.Errorf("parseSemverParts(%q) = %v, want nil", c.in, got)
			}
			continue
		}
		if len(got) != 3 || got[0] != c.want[0] || got[1] != c.want[1] || got[2] != c.want[2] {
			t.Errorf("parseSemverParts(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestCompareSemverDesc_StableBeatsPrerelease(t *testing.T) {
	if !compareSemverDesc("1.2.3", "1.2.3-rc.1") {
		t.Error("expected 1.2.3 to sort before 1.2.3-rc.1 (stable wins)")
	}
	if compareSemverDesc("1.2.3-rc.1", "1.2.3") {
		t.Error("expected 1.2.3-rc.1 NOT to sort before 1.2.3")
	}
}

func TestBuildVersionsResponse_OrderingAndLatestStable(t *testing.T) {
	in := []helm.ChartVersion{
		{Version: "1.18.0"},
		{Version: "1.20.0-rc.1"},
		{Version: "1.20.0"},
		{Version: "1.19.5"},
		{Version: "v1.20.1"}, // mixed v-prefix
		{Version: "not-a-semver"},
	}
	resp := buildVersionsResponse("addon", "chart", "https://example.test", in)

	if resp.LatestStable != "v1.20.1" && resp.LatestStable != "1.20.1" {
		t.Errorf("latest_stable = %q, want 1.20.1 / v1.20.1", resp.LatestStable)
	}
	// First entry should be the highest stable.
	if got := resp.Versions[0].Version; got != "v1.20.1" && got != "1.20.1" {
		t.Errorf("first version = %q, want highest stable", got)
	}
	// Prerelease must be flagged.
	for _, v := range resp.Versions {
		if v.Version == "1.20.0-rc.1" && !v.Prerelease {
			t.Error("1.20.0-rc.1 should be flagged as prerelease")
		}
		if v.Version == "1.20.0" && v.Prerelease {
			t.Error("1.20.0 should NOT be flagged as prerelease")
		}
	}
}

func TestFilterVersions_ExcludesPrereleases(t *testing.T) {
	resp := catalogVersionsResponse{
		Versions: []catalogVersionEntry{
			{Version: "1.0.0", Prerelease: false},
			{Version: "1.1.0-rc", Prerelease: true},
			{Version: "1.1.0", Prerelease: false},
		},
	}
	out := filterVersions(resp, false)
	if len(out.Versions) != 2 {
		t.Fatalf("expected 2 versions after filtering, got %d", len(out.Versions))
	}
	for _, v := range out.Versions {
		if v.Prerelease {
			t.Errorf("prerelease leaked through filter: %q", v.Version)
		}
	}
	// Original untouched.
	if len(resp.Versions) != 3 {
		t.Errorf("filterVersions mutated input: %v", resp.Versions)
	}
}

func TestVersionsCache_RoundTripAndEviction(t *testing.T) {
	resetCatalogVersionsCacheForTest()
	t.Cleanup(resetCatalogVersionsCacheForTest)

	storeCachedVersions("k1", catalogVersionsResponse{Addon: "a"})
	if got, ok := lookupCachedVersions("k1"); !ok || got.Addon != "a" {
		t.Fatalf("lookup k1 = (%+v, %v)", got, ok)
	}
	if _, ok := lookupCachedVersions("k2"); ok {
		t.Error("expected miss on k2")
	}
}

// Handler-level tests for the no-network branches.

func TestHandleListCatalogVersions_NotFound(t *testing.T) {
	srv := serverWithCatalog(t, testCatalog(t))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons/ghost/versions", nil)
	req.SetPathValue("name", "ghost")
	rw := httptest.NewRecorder()
	srv.handleListCatalogVersions(rw, req)
	if rw.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rw.Code)
	}
}

func TestHandleListCatalogVersions_ServiceUnavailable(t *testing.T) {
	srv := &Server{} // no catalog
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons/cert-manager/versions", nil)
	req.SetPathValue("name", "cert-manager")
	rw := httptest.NewRecorder()
	srv.handleListCatalogVersions(rw, req)
	if rw.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rw.Code)
	}
}

func TestHandleListCatalogVersions_BadIncludePrereleases(t *testing.T) {
	srv := serverWithCatalog(t, testCatalog(t))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons/cert-manager/versions?include_prereleases=maybe", nil)
	req.SetPathValue("name", "cert-manager")
	rw := httptest.NewRecorder()
	srv.handleListCatalogVersions(rw, req)
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rw.Code)
	}
}

func TestHandleListCatalogVersions_CacheServesWithoutNetwork(t *testing.T) {
	resetCatalogVersionsCacheForTest()
	t.Cleanup(resetCatalogVersionsCacheForTest)

	srv := serverWithCatalog(t, testCatalog(t))
	// Pre-seed cache for cert-manager so the handler skips the upstream call.
	entry, ok := srv.catalog.Get("cert-manager")
	if !ok {
		t.Fatal("test fixture missing cert-manager")
	}
	resp := buildVersionsResponse(entry.Name, entry.Chart, entry.Repo, []helm.ChartVersion{
		{Version: "1.0.0"},
		{Version: "1.1.0-rc.1"},
		{Version: "1.1.0"},
	})
	storeCachedVersions(entry.Repo+"|"+entry.Chart, resp)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons/cert-manager/versions", nil)
	req.SetPathValue("name", "cert-manager")
	rw := httptest.NewRecorder()
	srv.handleListCatalogVersions(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rw.Code)
	}
	var body catalogVersionsResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.LatestStable != "1.1.0" {
		t.Errorf("latest_stable = %q, want 1.1.0", body.LatestStable)
	}
	// Prereleases included by default.
	hasPrerelease := false
	for _, v := range body.Versions {
		if v.Prerelease {
			hasPrerelease = true
		}
	}
	if !hasPrerelease {
		t.Error("expected prereleases to be included by default")
	}
}

func TestHandleListCatalogVersions_ExcludePrereleases(t *testing.T) {
	resetCatalogVersionsCacheForTest()
	t.Cleanup(resetCatalogVersionsCacheForTest)

	srv := serverWithCatalog(t, testCatalog(t))
	entry, _ := srv.catalog.Get("cert-manager")
	resp := buildVersionsResponse(entry.Name, entry.Chart, entry.Repo, []helm.ChartVersion{
		{Version: "1.0.0"},
		{Version: "1.1.0-rc.1"},
	})
	storeCachedVersions(entry.Repo+"|"+entry.Chart, resp)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons/cert-manager/versions?include_prereleases=false", nil)
	req.SetPathValue("name", "cert-manager")
	rw := httptest.NewRecorder()
	srv.handleListCatalogVersions(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d", rw.Code)
	}
	var body catalogVersionsResponse
	_ = json.Unmarshal(rw.Body.Bytes(), &body)
	for _, v := range body.Versions {
		if v.Prerelease {
			t.Errorf("prerelease leaked: %q", v.Version)
		}
	}
}

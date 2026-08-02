package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/helm"
)

// fakeFreshnessLister is a minimal VersionsLister test double, local to
// this file so catalog_freshness_test.go doesn't reach into
// internal/catalog's own unexported test helpers.
type fakeFreshnessLister struct {
	versions map[string][]helm.ChartVersion
	err      map[string]error
}

func (f *fakeFreshnessLister) ListVersions(_ context.Context, repo, chart string) ([]helm.ChartVersion, error) {
	key := repo + "|" + chart
	if err, ok := f.err[key]; ok {
		return nil, err
	}
	return f.versions[key], nil
}

func TestHandleListCatalogVersions_PrefersFreshnessSnapshot(t *testing.T) {
	resetCatalogVersionsCacheForTest()
	t.Cleanup(resetCatalogVersionsCacheForTest)

	c := testCatalog(t)
	srv := serverWithCatalog(t, c)

	entry, ok := c.Get("cert-manager")
	if !ok {
		t.Fatal("test fixture missing cert-manager")
	}

	lister := &fakeFreshnessLister{
		versions: map[string][]helm.ChartVersion{
			entry.Repo + "|" + entry.Chart: {{Version: "1.2.0"}, {Version: "1.1.0"}},
		},
	}
	sched := catalog.NewFreshnessScheduler(c, lister, nil, time.Hour)
	sched.RefreshForTest()
	srv.SetFreshness(sched)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons/cert-manager/versions", nil)
	req.SetPathValue("name", "cert-manager")
	rw := httptest.NewRecorder()
	srv.handleListCatalogVersions(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
	var body catalogVersionsResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.LatestStable != "1.2.0" {
		t.Errorf("latest_stable = %q, want 1.2.0 (from the freshness snapshot, not the on-demand cache)", body.LatestStable)
	}
	if body.CachedAt == "" {
		t.Error("expected a non-empty cached_at (the snapshot's real CheckedAt)")
	}
}

func TestHandleListCatalogVersions_FreshnessUnknownDegradesGracefully(t *testing.T) {
	resetCatalogVersionsCacheForTest()
	t.Cleanup(resetCatalogVersionsCacheForTest)

	c := testCatalog(t)
	srv := serverWithCatalog(t, c)

	entry, ok := c.Get("cert-manager")
	if !ok {
		t.Fatal("test fixture missing cert-manager")
	}

	lister := &fakeFreshnessLister{
		err: map[string]error{
			entry.Repo + "|" + entry.Chart: helm.ErrOCIVersionCheckUnsupported,
		},
	}
	sched := catalog.NewFreshnessScheduler(c, lister, nil, time.Hour)
	sched.RefreshForTest()
	srv.SetFreshness(sched)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons/cert-manager/versions", nil)
	req.SetPathValue("name", "cert-manager")
	rw := httptest.NewRecorder()
	srv.handleListCatalogVersions(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (never an error page for stale/unknown data); body = %s", rw.Code, rw.Body.String())
	}
	var body catalogVersionsResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.VersionCheckUnknown {
		t.Error("expected version_check_unknown = true")
	}
	if body.CachedAt == "" {
		t.Error("expected a non-empty cached_at even for an unknown-version snapshot — staleness must be dated, not hidden")
	}
}

func TestHandleListCatalogVersions_NoFreshnessSnapshotFallsThrough(t *testing.T) {
	resetCatalogVersionsCacheForTest()
	t.Cleanup(resetCatalogVersionsCacheForTest)

	c := testCatalog(t)
	srv := serverWithCatalog(t, c)
	// A freshness scheduler is wired but has never run — VersionSnapshot
	// returns ok=false for every addon, so the on-demand cache path (this
	// test seeds it) must still be reachable.
	sched := catalog.NewFreshnessScheduler(c, &fakeFreshnessLister{}, nil, time.Hour)
	srv.SetFreshness(sched)

	entry, ok := c.Get("cert-manager")
	if !ok {
		t.Fatal("test fixture missing cert-manager")
	}
	resp := buildVersionsResponse(entry.Name, entry.Chart, entry.Repo, []helm.ChartVersion{{Version: "9.9.9"}})
	storeCachedVersions(entry.Repo+"|"+entry.Chart, resp)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/addons/cert-manager/versions", nil)
	req.SetPathValue("name", "cert-manager")
	rw := httptest.NewRecorder()
	srv.handleListCatalogVersions(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
	var body catalogVersionsResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.LatestStable != "9.9.9" {
		t.Errorf("latest_stable = %q, want 9.9.9 (from the pre-seeded on-demand cache)", body.LatestStable)
	}
}

func TestHandleGetCatalogFreshness_Disabled(t *testing.T) {
	srv := serverWithCatalog(t, testCatalog(t))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/freshness", nil)
	rw := httptest.NewRecorder()
	srv.handleGetCatalogFreshness(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rw.Code)
	}
	var body catalogFreshnessResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Enabled {
		t.Error("expected enabled=false when no scheduler is wired")
	}
}

func TestHandleGetCatalogFreshness_Enabled(t *testing.T) {
	c := testCatalog(t)
	srv := serverWithCatalog(t, c)

	entry, ok := c.Get("cert-manager")
	if !ok {
		t.Fatal("test fixture missing cert-manager")
	}
	lister := &fakeFreshnessLister{
		versions: map[string][]helm.ChartVersion{
			entry.Repo + "|" + entry.Chart: {{Version: "1.0.0"}},
		},
	}
	checkFn := func(ctx context.Context) (*catalog.EnginePinStatus, error) {
		return &catalog.EnginePinStatus{V4Repo: true, UpgradeAvailable: true, Message: "upgrade available"}, nil
	}
	sched := catalog.NewFreshnessScheduler(c, lister, checkFn, 6*time.Hour)
	sched.RefreshForTest()
	srv.SetFreshness(sched)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/freshness", nil)
	rw := httptest.NewRecorder()
	srv.handleGetCatalogFreshness(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
	var body catalogFreshnessResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Enabled {
		t.Fatal("expected enabled=true")
	}
	if body.LastRun == "" || body.NextRun == "" {
		t.Errorf("expected non-empty last_run/next_run, got %+v", body)
	}
	if body.IntervalSecs != int((6 * time.Hour).Seconds()) {
		t.Errorf("interval_seconds = %d, want %d", body.IntervalSecs, int((6 * time.Hour).Seconds()))
	}
	if body.EnginePin == nil || !body.EnginePin.UpgradeAvailable {
		t.Errorf("expected engine_pin.upgrade_available = true, got %+v", body.EnginePin)
	}
}

func TestHandleRefreshCatalogFreshness_NotEnabled(t *testing.T) {
	srv := serverWithCatalog(t, testCatalog(t))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/freshness/refresh", nil)
	rw := httptest.NewRecorder()
	srv.handleRefreshCatalogFreshness(rw, req)

	if rw.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rw.Code)
	}
}

func TestHandleRefreshCatalogFreshness_TriggersScheduler(t *testing.T) {
	c := testCatalog(t)
	srv := serverWithCatalog(t, c)
	lister := &fakeFreshnessLister{}
	sched := catalog.NewFreshnessScheduler(c, lister, nil, time.Hour)
	srv.SetFreshness(sched)
	sched.Start()
	t.Cleanup(sched.Stop)

	// Let the initial Start()-driven pass complete before triggering a
	// second one, so LastRun() has a baseline to compare against.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && sched.LastRun().IsZero() {
		time.Sleep(time.Millisecond)
	}
	firstRun := sched.LastRun()
	if firstRun.IsZero() {
		t.Fatal("expected the scheduler's initial pass to have completed")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/freshness/refresh", nil)
	rw := httptest.NewRecorder()
	srv.handleRefreshCatalogFreshness(rw, req)

	if rw.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", rw.Code, rw.Body.String())
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !sched.LastRun().After(firstRun) {
		time.Sleep(time.Millisecond)
	}
	if !sched.LastRun().After(firstRun) {
		t.Error("expected Trigger() (via the refresh endpoint) to cause a second pass")
	}
}

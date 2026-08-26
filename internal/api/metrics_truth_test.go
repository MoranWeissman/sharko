package api

// metrics_truth_test.go — the two metrics B5 made real, driven through
// the code an operator's traffic actually goes through.
//
// Neither test sets a metric and reads it back; that proves the
// Prometheus library works, not that Sharko is wired to it. Each one
// changes the underlying thing — signs somebody in, hands the server a
// catalog file — and then asks the registry what the number is.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/MoranWeissman/sharko/internal/metrics"
)

// gaugeFromDefaultRegistry gathers the default registry and returns the
// single unlabelled value of the named gauge, plus whether the metric
// appeared in the scrape at all.
func gaugeFromDefaultRegistry(t *testing.T, name string) (value float64, present bool) {
	t.Helper()
	gathered, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range gathered {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			if m.GetGauge() == nil {
				t.Fatalf("%s is not a gauge in the scrape (%s)", name, mf.GetType())
			}
			return m.GetGauge().GetValue(), true
		}
	}
	return 0, false
}

// mustGauge is gaugeFromDefaultRegistry for the cases where absence is
// itself the failure.
func mustGauge(t *testing.T, name string) float64 {
	t.Helper()
	v, ok := gaugeFromDefaultRegistry(t, name)
	if !ok {
		t.Fatalf("%s did not appear in the scrape at all", name)
	}
	return v
}

// TestActiveSessionsCountsRealLogins signs a user in through handleLogin
// and out through handleLogout, and reads sharko_active_sessions off the
// registry in between. Nothing here touches the gauge directly.
func TestActiveSessionsCountsRealLogins(t *testing.T) {
	// NewRouter is what tells the gauge where to count from, and it is
	// what a running server calls. Build one.
	srv := newTestServer()
	_ = NewRouter(srv, nil)

	// Other tests in this package leave sessions in the map. Take the
	// number as it stands and assert on the movement, not the absolute.
	before := mustGauge(t, "sharko_active_sessions")

	if err := srv.authStore.AddUser("metrics-alice", "correct-horse-battery-staple", "admin"); err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	token := loginForTest(t, srv, "metrics-alice", "correct-horse-battery-staple")
	t.Cleanup(func() { deleteSessionForTest(token) })

	if got := mustGauge(t, "sharko_active_sessions"); got != before+1 {
		t.Fatalf("after one real login the gauge is %v, want %v — the login path is not reaching the metric", got, before+1)
	}

	// A session that has run out must stop counting immediately, without
	// waiting for the hourly sweep. This is the whole reason the gauge
	// counts at scrape time instead of being written on login/logout.
	expireSessionForTest(token)
	if got := mustGauge(t, "sharko_active_sessions"); got != before {
		t.Fatalf("an expired session is still being counted: gauge is %v, want %v", got, before)
	}

	// And a real logout takes it back down too.
	token2 := loginForTest(t, srv, "metrics-alice", "correct-horse-battery-staple")
	if got := mustGauge(t, "sharko_active_sessions"); got != before+1 {
		t.Fatalf("second login not counted: gauge is %v, want %v", got, before+1)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+token2)
	rw := httptest.NewRecorder()
	srv.handleLogout(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("logout: status = %d, body = %s", rw.Code, rw.Body.String())
	}
	if got := mustGauge(t, "sharko_active_sessions"); got != before {
		t.Fatalf("after logout the gauge is %v, want %v", got, before)
	}
}

// TestCatalogEntriesCountsTheApprovedCatalog drives loadOrgCatalog — the
// one funnel every read of the live catalog.yaml goes through, including
// the background freshness scheduler's — and checks the gauge against
// the file it was handed.
func TestCatalogEntriesCountsTheApprovedCatalog(t *testing.T) {
	const name = "sharko_catalog_entries_count"

	body := []byte(`apiVersion: sharko.dev/v1
kind: AddonCatalog
addons:
  cert-manager:
    version: "1.14.5"
  external-dns:
    version: "1.14.0"
  metrics-server:
    version: "3.12.1"
`)

	srv := serverWithOrgCatalog(t, testCatalog(t), &fakeCatalogGitProvider{catalogBody: body})
	gp, err := srv.connSvc.GetActiveGitProvider()
	if err != nil {
		t.Fatalf("GetActiveGitProvider: %v", err)
	}

	spec, err := srv.loadOrgCatalog(context.Background(), gp)
	if err != nil {
		t.Fatalf("loadOrgCatalog: %v", err)
	}
	if len(spec.Addons) != 3 {
		t.Fatalf("fixture parsed to %d addons, want 3 — fix the fixture before trusting the metric", len(spec.Addons))
	}
	if got := mustGauge(t, name); got != 3 {
		t.Fatalf("%s = %v after reading a catalog with 3 approved addons, want 3", name, got)
	}

	// A repo with no catalog.yaml has genuinely approved nothing. That
	// zero is a measurement and must be published.
	empty := serverWithOrgCatalog(t, testCatalog(t), &fakeCatalogGitProvider{})
	egp, err := empty.connSvc.GetActiveGitProvider()
	if err != nil {
		t.Fatalf("GetActiveGitProvider: %v", err)
	}
	if _, err := empty.loadOrgCatalog(context.Background(), egp); err != nil {
		t.Fatalf("loadOrgCatalog on a fresh repo: %v", err)
	}
	if got := mustGauge(t, name); got != 0 {
		t.Fatalf("%s = %v for a repo with no catalog.yaml, want 0", name, got)
	}
}

// TestCatalogEntriesIsAbsentUntilSharkoHasLooked is the false-zero test.
// Before any read, the metric must not be in the scrape at all — an
// absent series says "not measured", a published 0 says "your org has
// approved nothing", and only one of those is true at startup.
func TestCatalogEntriesIsAbsentUntilSharkoHasLooked(t *testing.T) {
	const name = "sharko_catalog_entries_count"
	metrics.ForgetCatalogEntriesForTest()

	if _, present := gaugeFromDefaultRegistry(t, name); present {
		t.Fatalf("%s is published before Sharko has read any catalog — that zero is not a measurement", name)
	}

	metrics.SetCatalogEntries(0)
	v, present := gaugeFromDefaultRegistry(t, name)
	if !present {
		t.Fatalf("%s stayed absent after a measured zero — a measured zero is a fact and belongs on the graph", name)
	}
	if v != 0 {
		t.Fatalf("%s = %v after SetCatalogEntries(0), want 0", name, v)
	}
}

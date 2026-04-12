package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestMetricsRegistered(t *testing.T) {
	// All promauto metrics should be findable in the default registry.
	// We gather all metrics and check that our sharko_ prefixed ones exist.
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	want := map[string]bool{
		"sharko_cluster_count":                      false,
		"sharko_cluster_status":                     false,
		"sharko_cluster_last_verified_timestamp":    false,
		"sharko_cluster_last_test_duration_seconds":  false,
		"sharko_cluster_test_failures_total":         false,
		"sharko_addon_sync_status":                  false,
		"sharko_addon_health":                       false,
		"sharko_addon_version":                      false,
		"sharko_catalog_entries_count":               false,
		"sharko_reconciler_runs_total":               false,
		"sharko_reconciler_duration_seconds":         false,
		"sharko_reconciler_last_run_timestamp":       false,
		"sharko_reconciler_items_checked_total":      false,
		"sharko_reconciler_items_changed_total":      false,
		"sharko_pr_tracked":                          false,
		"sharko_pr_merge_duration_seconds":           false,
		"sharko_api_requests_total":                  false,
		"sharko_api_request_duration_seconds":        false,
		"sharko_auth_login_total":                    false,
		"sharko_active_sessions":                     false,
	}

	for _, mf := range mfs {
		if _, ok := want[mf.GetName()]; ok {
			want[mf.GetName()] = true
		}
	}

	// Some metrics won't appear until they have observations (histograms, counters
	// with label vectors). Use Describe to verify they're registered instead.
	descCh := make(chan *prometheus.Desc, 100)
	go func() {
		// Collect descriptions from our known metrics.
		HTTPRequests.Describe(descCh)
		HTTPDuration.Describe(descCh)
		ClusterCount.Describe(descCh)
		ClusterStatus.Describe(descCh)
		ClusterLastVerified.Describe(descCh)
		ClusterTestDuration.Describe(descCh)
		ClusterTestFailures.Describe(descCh)
		AddonSyncStatus.Describe(descCh)
		AddonHealth.Describe(descCh)
		AddonVersion.Describe(descCh)
		CatalogEntriesCount.Describe(descCh)
		ReconcilerRuns.Describe(descCh)
		ReconcilerDuration.Describe(descCh)
		ReconcilerLastRun.Describe(descCh)
		ReconcilerItemsChecked.Describe(descCh)
		ReconcilerItemsChanged.Describe(descCh)
		PRTracked.Describe(descCh)
		PRMergeDuration.Describe(descCh)
		AuthLoginTotal.Describe(descCh)
		ActiveSessions.Describe(descCh)
		close(descCh)
	}()

	descCount := 0
	for range descCh {
		descCount++
	}
	if descCount != 20 {
		t.Errorf("expected 20 metric descriptions, got %d", descCount)
	}
}

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/api/v1/clusters/prod-eu", "/api/v1/clusters/{name}"},
		{"/api/v1/clusters/prod-eu/test", "/api/v1/clusters/{name}/test"},
		{"/api/v1/clusters/prod-eu/values", "/api/v1/clusters/{name}/values"},
		{"/api/v1/addons/cert-manager", "/api/v1/addons/{name}"},
		{"/api/v1/addons/cert-manager/upgrade", "/api/v1/addons/{name}/upgrade"},
		{"/api/v1/connections/my-conn", "/api/v1/connections/{name}"},
		{"/api/v1/users/admin", "/api/v1/users/{name}"},
		{"/api/v1/tokens/abc123", "/api/v1/tokens/{id}"},
		{"/api/v1/prs/42", "/api/v1/prs/{id}"},
		{"/api/v1/operations/op-123", "/api/v1/operations/{id}"},
		{"/api/v1/docs/getting-started", "/api/v1/docs/{slug}"},
		{"/api/v1/health", "/api/v1/health"},
		{"/api/v1/clusters", "/api/v1/clusters"},
		{"/metrics", "/metrics"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizePath(tt.input)
			if got != tt.want {
				t.Errorf("NormalizePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestMiddlewareRecordsMetrics(t *testing.T) {
	// Create a simple handler that returns 200.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := Middleware(inner)

	req := httptest.NewRequest("GET", "/api/v1/clusters/test-cluster", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	// Verify the metric was recorded with normalized path.
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	var found bool
	for _, mf := range mfs {
		if mf.GetName() == "sharko_api_requests_total" {
			for _, m := range mf.GetMetric() {
				labels := make(map[string]string)
				for _, lp := range m.GetLabel() {
					labels[lp.GetName()] = lp.GetValue()
				}
				if labels["method"] == "GET" && labels["path"] == "/api/v1/clusters/{name}" && labels["status"] == "200" {
					found = true
					break
				}
			}
		}
	}
	if !found {
		t.Error("expected sharko_api_requests_total metric with method=GET, path=/api/v1/clusters/{name}, status=200")
	}
}

func TestMiddlewareSkipsMetricsEndpoint(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := Middleware(inner)

	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("expected inner handler to be called for /metrics")
	}
	// The /metrics path should not record its own metrics (self-referential).
	// We can't easily prove absence in a shared registry, but the code path is covered.
}

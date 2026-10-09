package api

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/metrics"
)

// TestMetricsHandlerExposesLegacyAndSLOFamilies asserts that the
// /metrics endpoint composes the legacy default-registry metrics
// (sharko_api_requests_total via promauto) with the V2-3 SLO custom
// registry. This is the integration guard for the prometheus.Gatherers
// composition in internal/api/metrics.go.
func TestMetricsHandlerExposesLegacyAndSLOFamilies(t *testing.T) {
	// Touch one metric family in each registry so they appear in the
	// scrape output (a counter with no observations may be elided).
	metrics.HTTPRequests.WithLabelValues("GET", "/api/v1/health", "200").Inc()
	for _, p := range metrics.SLOPaths {
		metrics.Observe(p, "total", 0.001, "")
		metrics.IncTotal(p, "200")
	}

	h := metricsHandler()
	req := httptest.NewRequest("GET", "/metrics", nil)
	req.Header.Set("Accept", "application/openmetrics-text;version=1.0.0,text/plain;version=0.0.4;q=0.5,*/*;q=0.1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := rec.Result()
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	bodyStr := string(body)

	// Legacy metric (default registry / promauto).
	if !strings.Contains(bodyStr, "sharko_api_requests_total") {
		t.Errorf("/metrics body missing legacy family sharko_api_requests_total")
	}

	// V2-3 SLO families (custom registry composed via Gatherers).
	for _, p := range metrics.SLOPaths {
		want := "sharko_" + p + "_duration_seconds"
		if !strings.Contains(bodyStr, want) {
			t.Errorf("/metrics body missing SLO family %q", want)
		}
	}
}

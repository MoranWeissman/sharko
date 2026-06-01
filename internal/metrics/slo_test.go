package metrics

import (
	"io"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
)

// scrapeHandler renders the SLO registry through promhttp.HandlerFor with
// EnableOpenMetrics=true (matching internal/api/metrics.go) and returns
// the body + content-type. This is the per-package fixture; the
// router-mounted variant is exercised in internal/api/metrics_test.go.
func scrapeHandler(t *testing.T) (string, string) {
	t.Helper()
	h := promhttp.HandlerFor(SLORegistry(), promhttp.HandlerOpts{EnableOpenMetrics: true})
	req := httptest.NewRequest("GET", "/metrics", nil)
	req.Header.Set("Accept", "application/openmetrics-text;version=1.0.0,text/plain;version=0.0.4;q=0.5,*/*;q=0.1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := rec.Result()
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read scrape body: %v", err)
	}
	return string(body), res.Header.Get("Content-Type")
}

// withFreshSLORegistry resets the package-private SLO registry so the
// test starts from zero observations. Tests must NOT run in parallel
// with one another while using this helper — the registry is process
// global.
func withFreshSLORegistry(t *testing.T) {
	t.Helper()
	ResetSLORegistryForTest()
}

// gatherSLO returns the metric families in the SLO registry keyed by name.
func gatherSLO(t *testing.T) map[string]*dto.MetricFamily {
	t.Helper()
	mfs, err := SLORegistry().Gather()
	if err != nil {
		t.Fatalf("gather SLO registry: %v", err)
	}
	out := make(map[string]*dto.MetricFamily, len(mfs))
	for _, mf := range mfs {
		out[mf.GetName()] = mf
	}
	return out
}

// findHistogramBucket returns the bucket with the matching upper bound
// from a histogram metric, or nil if not present.
func findHistogramBucket(m *dto.Metric, upperBound float64) *dto.Bucket {
	if m == nil || m.Histogram == nil {
		return nil
	}
	const eps = 1e-12
	for _, b := range m.Histogram.GetBucket() {
		if abs(b.GetUpperBound()-upperBound) < eps {
			return b
		}
	}
	return nil
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func TestSLOPathConstantsLocked(t *testing.T) {
	// Lock the V2-1.2 baseline path IDs verbatim. Renaming any of these
	// breaks the perf-baselines.yaml mapping and the V2-3.3
	// PrometheusRule chart references downstream.
	want := []string{
		"cluster_registration",
		"addon_cycle",
		"catalog_scan",
		"dashboard_read",
	}
	got := []string{
		PathClusterRegistration,
		PathAddonCycle,
		PathCatalogScan,
		PathDashboardRead,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SLO path constants drifted from V2-1.2 baselines:\ngot  %v\nwant %v", got, want)
	}
	if !reflect.DeepEqual(SLOPaths, want) {
		t.Fatalf("SLOPaths order/contents drifted: got %v want %v", SLOPaths, want)
	}
}

func TestSLOBucketsMatchLiterals(t *testing.T) {
	// Regression guard — if someone edits buckets.go, the literal slice
	// here must be updated too. The intent: explicit lock-in of bucket
	// boundaries so a code-review notices any drift from the V2-1.2
	// sizing rationale documented in buckets.go comments.
	cases := []struct {
		path string
		want []float64
	}{
		{PathClusterRegistration, []float64{
			0.005, 0.010, 0.020,
			0.050, 0.100, 0.250, 0.500, 1.0, 2.5, 5.0,
		}},
		{PathAddonCycle, prometheus.DefBuckets},
		{PathCatalogScan, []float64{
			0.0001, 0.0003, 0.0005,
			0.001, 0.002, 0.003, 0.005, 0.010, 0.025, 0.050,
		}},
		{PathDashboardRead, []float64{
			0.00005, 0.0001, 0.0002,
			0.0005, 0.001, 0.002, 0.005, 0.010, 0.025, 0.050,
		}},
	}
	for _, tc := range cases {
		got := bucketsForPath(tc.path)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("bucketsForPath(%q) drift:\n  got  %v\n  want %v", tc.path, got, tc.want)
		}
	}
}

func TestSLORegistryHasAllFamilies(t *testing.T) {
	withFreshSLORegistry(t)
	// Force at least one observation per family so the family appears
	// in the gather output. Counter Vecs only show up after at least
	// one labelled child is touched.
	for _, p := range SLOPaths {
		Observe(p, "warmup", 0.001, "")
		IncTotal(p, "200")
		IncError(p, "502")
	}
	mfs := gatherSLO(t)
	wantNames := []string{}
	for _, p := range SLOPaths {
		wantNames = append(wantNames,
			"sharko_"+p+"_duration_seconds",
			"sharko_"+p+"_total",
			"sharko_"+p+"_errors_total",
		)
	}
	for _, name := range wantNames {
		if _, ok := mfs[name]; !ok {
			t.Errorf("expected metric family %q in SLO registry; not found", name)
		}
	}
}

func TestObserveLandsInExpectedBucket(t *testing.T) {
	withFreshSLORegistry(t)
	// A 50ms observation on cluster_registration must land in the
	// 0.050 bucket and every wider bucket — cumulative semantics.
	Observe(PathClusterRegistration, "argocd_secret_created", 0.050, "")

	mfs := gatherSLO(t)
	mf := mfs["sharko_cluster_registration_duration_seconds"]
	if mf == nil {
		t.Fatalf("histogram not gathered")
	}
	if len(mf.Metric) != 1 {
		t.Fatalf("want 1 labelled metric, got %d", len(mf.Metric))
	}
	bucket := findHistogramBucket(mf.Metric[0], 0.050)
	if bucket == nil {
		t.Fatalf("0.050 bucket not present; family has %d buckets", len(mf.Metric[0].Histogram.Bucket))
	}
	if bucket.GetCumulativeCount() != 1 {
		t.Errorf("expected 0.050 bucket cumulative count 1, got %d", bucket.GetCumulativeCount())
	}
}

func TestIncErrorOnlyIncrementsErrorCounter(t *testing.T) {
	withFreshSLORegistry(t)
	IncError(PathDashboardRead, "502")
	mfs := gatherSLO(t)

	errFam := mfs["sharko_dashboard_read_errors_total"]
	if errFam == nil || len(errFam.Metric) != 1 {
		t.Fatalf("expected 1 error metric, got %v", errFam)
	}
	if got := errFam.Metric[0].Counter.GetValue(); got != 1 {
		t.Errorf("error counter = %v, want 1", got)
	}

	totalFam := mfs["sharko_dashboard_read_total"]
	if totalFam != nil && len(totalFam.Metric) > 0 {
		// total may be absent (no labelled child created yet); if it
		// did appear, it must be zero.
		for _, m := range totalFam.Metric {
			if got := m.Counter.GetValue(); got != 0 {
				t.Errorf("total counter incremented unexpectedly: %v", got)
			}
		}
	}
}

func TestIncTotalIncrementsTotalCounter(t *testing.T) {
	withFreshSLORegistry(t)
	IncTotal(PathCatalogScan, "200")
	IncTotal(PathCatalogScan, "200")
	IncTotal(PathCatalogScan, "404")

	mfs := gatherSLO(t)
	mf := mfs["sharko_catalog_scan_total"]
	if mf == nil {
		t.Fatalf("total counter family missing")
	}
	values := map[string]float64{}
	for _, m := range mf.Metric {
		for _, l := range m.Label {
			if l.GetName() == "code" {
				values[l.GetValue()] = m.Counter.GetValue()
			}
		}
	}
	if values["200"] != 2 {
		t.Errorf("code=200 got %v, want 2", values["200"])
	}
	if values["404"] != 1 {
		t.Errorf("code=404 got %v, want 1", values["404"])
	}
}

func TestHandlerExposesAllFourPathFamilies(t *testing.T) {
	withFreshSLORegistry(t)
	for _, p := range SLOPaths {
		Observe(p, "total", 0.01, "")
		IncTotal(p, "200")
		IncError(p, "502")
	}

	body, contentType := scrapeHandler(t)
	if !strings.Contains(contentType, "text/plain") && !strings.Contains(contentType, "application/openmetrics-text") {
		t.Fatalf("unexpected content-type %q", contentType)
	}
	for _, p := range SLOPaths {
		wantSubstrings := []string{
			"sharko_" + p + "_duration_seconds",
			"sharko_" + p + "_total",
			"sharko_" + p + "_errors_total",
		}
		for _, s := range wantSubstrings {
			if !strings.Contains(body, s) {
				t.Errorf("/metrics body missing metric family %q", s)
			}
		}
	}
}

func TestExemplarAttachedWhenTraceIDProvided(t *testing.T) {
	withFreshSLORegistry(t)
	Observe(PathCatalogScan, "catalog_load", 0.001, "req-abc-123")

	mfs := gatherSLO(t)
	mf := mfs["sharko_catalog_scan_duration_seconds"]
	if mf == nil || len(mf.Metric) == 0 {
		t.Fatalf("histogram not gathered")
	}
	hist := mf.Metric[0].Histogram
	if hist == nil {
		t.Fatalf("histogram nil")
	}
	var exemplarFound bool
	for _, b := range hist.Bucket {
		ex := b.GetExemplar()
		if ex == nil {
			continue
		}
		for _, lp := range ex.Label {
			if lp.GetName() == "request_id" && lp.GetValue() == "req-abc-123" {
				exemplarFound = true
			}
		}
	}
	if !exemplarFound {
		t.Errorf("expected exemplar with request_id=req-abc-123, none found")
	}
}

func TestObserveUnknownPathIsNoop(t *testing.T) {
	withFreshSLORegistry(t)
	// Must not panic + must not register a bogus metric family.
	Observe("typo_path", "phase", 0.5, "")
	IncTotal("typo_path", "200")
	IncError("typo_path", "500")

	mfs := gatherSLO(t)
	for name := range mfs {
		if strings.Contains(name, "typo_path") {
			t.Errorf("unknown path leaked into registry as %q", name)
		}
	}
}

func TestObserveConcurrentRaceFree(t *testing.T) {
	withFreshSLORegistry(t)
	const goroutines = 32
	const perGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				Observe(PathClusterRegistration, "ui_submit", 0.01, "")
				IncTotal(PathClusterRegistration, "200")
				if i%5 == 0 {
					IncError(PathClusterRegistration, "502")
				}
			}
		}(g)
	}
	wg.Wait()

	mfs := gatherSLO(t)
	mf := mfs["sharko_cluster_registration_duration_seconds"]
	if mf == nil || len(mf.Metric) == 0 {
		t.Fatalf("histogram not gathered after concurrent observations")
	}
	// +Inf bucket must capture the total observation count.
	bucket := mf.Metric[0].Histogram
	want := uint64(goroutines * perGoroutine)
	if bucket.GetSampleCount() != want {
		t.Errorf("sample count = %d, want %d", bucket.GetSampleCount(), want)
	}
}

func TestSLORegistryExposesLegacyAndSLOFamilies(t *testing.T) {
	// Composition guard: SLORegistry only returns SLO families. The
	// /metrics endpoint composes it with prometheus.DefaultGatherer via
	// prometheus.Gatherers — that join is asserted in
	// internal/api/metrics_test.go.
	mfs := gatherSLO(t)
	for name := range mfs {
		if !strings.HasPrefix(name, "sharko_") {
			t.Errorf("non-sharko family %q in SLO registry", name)
		}
	}
}

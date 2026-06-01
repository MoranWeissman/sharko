// SLO-surface metrics (V2-3.1 + V2-3.2).
//
// This file defines the public API for instrumenting the four SLO
// surfaces identified in V2-1.2 (the perf-baselines sprint):
//
//   - cluster_registration
//   - addon_cycle
//   - catalog_scan
//   - dashboard_read
//
// The four surfaces are exposed as three metric families each:
//
//	sharko_<path>_duration_seconds   histogram { phase=<phase_id> }
//	sharko_<path>_total              counter   { code=<status_or_code> }
//	sharko_<path>_errors_total       counter   { code=<status_or_code> }
//
// Metric names + label cardinality follow OTEL semantic conventions and
// are locked in docs/site/operator/metrics-naming.md. The histogram
// bucket boundaries are sized to the V2-1.2 baselines (see buckets.go).
//
// Exemplars are enabled on the duration histograms via OpenMetrics
// exposition: passing a non-empty traceID to Observe attaches a
// {request_id=<id>} exemplar to the bucket so a Grafana drill-down can
// jump straight from a histogram quantile to the matching slog line
// (V2-2.2 correlation_id).

package metrics

import "github.com/prometheus/client_golang/prometheus"

// SLO path identifiers — V2-1.2 baselines and V2-3 sprint plan use these
// exact strings; the V2-3.3 PrometheusRule chart in PR 2 will reference
// them verbatim in its recording-rule names.
const (
	PathClusterRegistration = "cluster_registration"
	PathAddonCycle          = "addon_cycle"
	PathCatalogScan         = "catalog_scan"
	PathDashboardRead       = "dashboard_read"
)

// SLOPaths lists every SLO path id in a stable order. Used to register
// the metric families and by tests + docs to enumerate the surface.
var SLOPaths = []string{
	PathClusterRegistration,
	PathAddonCycle,
	PathCatalogScan,
	PathDashboardRead,
}

// Observe records a duration observation for an SLO surface.
//
//   - path is one of the SLO path constants (PathClusterRegistration etc).
//     Unknown paths are silently no-op so a typo in instrumentation never
//     panics in a hot handler.
//   - phase identifies the sub-stage being measured. V2-1.2 phase IDs
//     (e.g. "argocd_secret_created", "fleet_status") are preferred so
//     histogram dimensions line up with the baselines. Use "total" for
//     end-to-end observations when per-phase wiring is impractical.
//   - durationSec is the elapsed time, seconds (NOT milliseconds).
//   - traceID is the V2-2.2 correlation ID (logging.RequestID(ctx)). When
//     non-empty it is attached as an OpenMetrics exemplar — Grafana
//     surfaces it as a clickable jump from a histogram bucket to the
//     matching slog line. Pass "" when no request_id is available
//     (background reconcilers, init code).
//
// Observe is safe for concurrent use; the underlying HistogramVec uses
// its own mutex.
func Observe(path, phase string, durationSec float64, traceID string) {
	fam := familyFor(path)
	if fam == nil {
		return
	}
	obs := fam.duration.WithLabelValues(phase)
	if traceID == "" {
		obs.Observe(durationSec)
		return
	}
	// OpenMetrics exemplar — older scrapers ignore unknown fields, so
	// this degrades gracefully on Prometheus <2.43.
	if exObs, ok := obs.(prometheus.ExemplarObserver); ok {
		exObs.ObserveWithExemplar(durationSec, prometheus.Labels{"request_id": traceID})
		return
	}
	obs.Observe(durationSec)
}

// IncTotal increments the per-path total counter. code is an HTTP status
// or domain code (e.g. "200", "502", "ok", "timeout"); pass "" for paths
// without a natural code. Unknown paths are silently no-op.
func IncTotal(path, code string) {
	fam := familyFor(path)
	if fam == nil {
		return
	}
	fam.total.WithLabelValues(code).Inc()
}

// IncError increments the per-path error counter. Errors are counted
// SEPARATELY from total: a handler that returns 502 should call IncTotal
// with code="502" AND IncError with code="502" so SLO dashboards can
// compute error-rate as errors/total without double-counting.
// Unknown paths are silently no-op.
func IncError(path, code string) {
	fam := familyFor(path)
	if fam == nil {
		return
	}
	fam.errors.WithLabelValues(code).Inc()
}

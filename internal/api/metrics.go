package api

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/MoranWeissman/sharko/internal/metrics"
)

// metricsHandler returns the /metrics HTTP handler composing the legacy
// promauto-registered Sharko metrics (default registry) with the V2-3
// SLO-surface metrics (custom registry).
//
// Design decisions (V2-3.1 + V2-3.2 — see
// docs/site/operator/metrics-naming.md):
//
//   - Unauthenticated. Industry standard for Prometheus scraping; the
//     security boundary is the cluster's network policy / Service /
//     ServiceMonitor, NOT a per-request auth check.
//   - No swagger annotation. Prometheus exposition format is not a JSON
//     API; OpenAPI annotations do not model it.
//   - OpenMetrics negotiated via Accept header so histogram exemplars
//     surface in Grafana when the scraper supports them; older scrapers
//     receive plain text/plain and ignore the exemplar field.
//   - Composes prometheus.DefaultGatherer with metrics.SLORegistry() via
//     prometheus.Gatherers so a single /metrics scrape returns BOTH
//     legacy metric families (sharko_api_requests_total etc.) AND the
//     V2-3 SLO families (sharko_<path>_duration_seconds etc.).
func metricsHandler() http.Handler {
	gatherer := prometheus.Gatherers{
		prometheus.DefaultGatherer,
		metrics.SLORegistry(),
	}
	return promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
		ErrorHandling:     promhttp.ContinueOnError,
	})
}

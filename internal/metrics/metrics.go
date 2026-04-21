// Package metrics defines all Prometheus metrics for Sharko.
// All metrics are auto-registered with the default prometheus registry via promauto.
package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Cluster metrics
var (
	ClusterCount = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sharko_cluster_count",
		Help: "Number of clusters by status",
	}, []string{"status"})

	ClusterStatus = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sharko_cluster_status",
		Help: "Cluster status (0 or 1 one-hot)",
	}, []string{"cluster", "status"})

	ClusterLastVerified = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sharko_cluster_last_verified_timestamp",
		Help: "Unix timestamp of last successful verification",
	}, []string{"cluster"})

	ClusterTestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sharko_cluster_last_test_duration_seconds",
		Help:    "Test duration per stage",
		Buckets: prometheus.DefBuckets,
	}, []string{"cluster", "stage"})

	ClusterTestFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "sharko_cluster_test_failures_total",
		Help: "Test failures by error code",
	}, []string{"cluster", "error_code"})
)

// Addon metrics
var (
	AddonSyncStatus = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sharko_addon_sync_status",
		Help: "ArgoCD sync status per addon (0/1)",
	}, []string{"cluster", "addon", "status"})

	AddonHealth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sharko_addon_health",
		Help: "ArgoCD health status per addon (0/1)",
	}, []string{"cluster", "addon", "health"})

	AddonVersion = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sharko_addon_version",
		Help: "Addon version (gauge with version label)",
	}, []string{"cluster", "addon", "version"})

	CatalogEntriesCount = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "sharko_catalog_entries_count",
		Help: "Total addons in catalog",
	})
)

// Catalog sources metrics (v1.23 Subsystem A — third-party catalog fetch loop).
// The fetcher (internal/catalog/sources) emits these per configured URL.
var (
	CatalogSourceFetchTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "sharko_catalog_source_fetch_total",
		Help: "Third-party catalog fetch attempts by source URL and outcome (ok|stale|failed)",
	}, []string{"url", "status"})

	CatalogSourceLastSuccess = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sharko_catalog_source_last_success_timestamp",
		Help: "Unix timestamp of last successful fetch per third-party catalog source URL",
	}, []string{"url"})

	CatalogSourceEntries = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sharko_catalog_source_entries",
		Help: "Number of entries in the current snapshot of a third-party catalog source URL",
	}, []string{"url"})
)

// Reconciler metrics
var (
	ReconcilerRuns = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "sharko_reconciler_runs_total",
		Help: "Reconciler invocations by outcome",
	}, []string{"reconciler", "result"})

	ReconcilerDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sharko_reconciler_duration_seconds",
		Help:    "Reconciler run duration",
		Buckets: prometheus.DefBuckets,
	}, []string{"reconciler"})

	ReconcilerLastRun = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sharko_reconciler_last_run_timestamp",
		Help: "Unix timestamp of last run",
	}, []string{"reconciler"})

	ReconcilerItemsChecked = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "sharko_reconciler_items_checked_total",
		Help: "Items processed per reconciler",
	}, []string{"reconciler"})

	ReconcilerItemsChanged = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "sharko_reconciler_items_changed_total",
		Help: "Items changed by action type",
	}, []string{"reconciler", "action"})
)

// PR metrics
var (
	PRTracked = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sharko_pr_tracked",
		Help: "Count of tracked PRs by status",
	}, []string{"status"})

	PRMergeDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "sharko_pr_merge_duration_seconds",
		Help:    "Time from PR creation to merge",
		Buckets: []float64{10, 30, 60, 120, 300, 600, 1800, 3600},
	})
)

// HTTP metrics
var (
	HTTPRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "sharko_api_requests_total",
		Help: "API requests by method, path, status",
	}, []string{"method", "path", "status"})

	HTTPDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sharko_api_request_duration_seconds",
		Help:    "API request duration",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})
)

// Auth metrics
var (
	AuthLoginTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "sharko_auth_login_total",
		Help: "Login attempts by outcome",
	}, []string{"result"})

	ActiveSessions = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "sharko_active_sessions",
		Help: "Current session count",
	})
)

// Catalog / OpenSSF Scorecard metrics (v1.21).
var (
	ScorecardRefreshTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "sharko_scorecard_refresh_total",
		Help: "OpenSSF Scorecard refresh operations by outcome",
	}, []string{"status"})

	ScorecardLastRefresh = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "sharko_scorecard_last_refresh_timestamp",
		Help: "Unix timestamp of last Scorecard refresh cycle",
	})
)

// AI annotate metrics (v1.21 Epic V121-7). Outcome label is one of:
//   "ok", "not_configured", "empty_input", "oversize", "secret_blocked",
//   "timeout", "llm_error", "parse_error", "opted_out", "disabled".
//
// Operators use these to spot LLM cost runaway (high call rate),
// LLM-provider degradation (rising "timeout" / "llm_error" rate), or
// consistent secret-leak hits (rising "secret_blocked" — usually a sign
// the maintainer has secrets baked into a chart and should fix that).
var (
	AIAnnotateTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "sharko_ai_annotate_total",
		Help: "AI annotate calls by outcome (ok, not_configured, oversize, secret_blocked, timeout, llm_error, parse_error, opted_out, disabled)",
	}, []string{"outcome"})

	AIAnnotateLatencySeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sharko_ai_annotate_latency_seconds",
		Help:    "Latency of AI annotate calls, including secret-guard scan and LLM round-trip, partitioned by outcome",
		Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 20, 30, 60},
	}, []string{"outcome"})
)

// ScorecardMetricsAdapter implements internal/catalog.ScorecardMetrics against
// the Prometheus counters declared above. Use this when wiring the Scheduler
// from serve.go.
type ScorecardMetricsAdapter struct{}

func (ScorecardMetricsAdapter) IncRefreshTotal(status string, delta int) {
	if delta <= 0 {
		return
	}
	ScorecardRefreshTotal.WithLabelValues(status).Add(float64(delta))
}

func (ScorecardMetricsAdapter) SetLastRefreshTimestamp(ts time.Time) {
	ScorecardLastRefresh.Set(float64(ts.Unix()))
}

// RecordHTTPRequest is a convenience function to record an HTTP request in
// both the request counter and the duration histogram.
func RecordHTTPRequest(method, path string, status int, duration time.Duration) {
	statusStr := strconv.Itoa(status)
	normalized := NormalizePath(path)
	HTTPRequests.WithLabelValues(method, normalized, statusStr).Inc()
	HTTPDuration.WithLabelValues(method, normalized).Observe(duration.Seconds())
}

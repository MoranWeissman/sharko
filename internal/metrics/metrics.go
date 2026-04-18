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

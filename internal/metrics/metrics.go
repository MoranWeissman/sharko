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

// Reconciler metrics (P2-D — wired from internal/clusterreconciler's write
// pass + check pass and internal/secrets' periodic pass; see each package's
// doc comment on the wiring choice — a direct import, no hook interface,
// matching how internal/catalog and internal/orchestrator already call this
// package directly).
//
// Label vocabulary, deliberately tiny (never a cluster or secret name):
//   - engine: "cluster_connection" | "addon_values" — one value per engine,
//     matching the two rows in GET /system/managed-secrets' "engines"
//     section.
//   - outcome (ReconcilerRuns only): "success" | "partial" | "failure".
var (
	ReconcilerRuns = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "sharko_reconciler_runs_total",
		Help: "Reconciler invocations by outcome",
	}, []string{"engine", "outcome"})

	ReconcilerDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "sharko_reconciler_duration_seconds",
		Help:    "Reconciler run duration",
		Buckets: prometheus.DefBuckets,
	}, []string{"engine"})

	ReconcilerLastRun = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sharko_reconciler_last_run_timestamp",
		Help: "Unix timestamp of last completed run (any outcome)",
	}, []string{"engine"})

	// ReconcilerLastSuccess is the "last completed run that did NOT abort"
	// counterpart to ReconcilerLastRun — set only on a "success" outcome
	// pass, never on "partial" or "failure". This is the gauge the pass-age
	// alert (charts/sharko/templates/prometheusrules.yaml) actually watches:
	// time() - this, one expression, no need to reason about outcome labels
	// inside the alert itself.
	ReconcilerLastSuccess = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sharko_reconciler_last_success_timestamp",
		Help: "Unix timestamp of last run that completed without aborting",
	}, []string{"engine"})

	ReconcilerItemsChecked = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "sharko_reconciler_items_checked_total",
		Help: "Items processed per reconciler",
	}, []string{"engine"})

	ReconcilerItemsChanged = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "sharko_reconciler_items_changed_total",
		Help: "Items changed by action type",
	}, []string{"engine", "action"})

	// ManagedSecretsState is a snapshot gauge: how many secrets this engine
	// currently knows about are in each state, right now — set (never
	// incremented) at the end of every completed pass, all known states
	// written every time so a state that just emptied out shows 0 instead
	// of a stale last-nonzero value. States: "in_sync" | "out_of_sync" |
	// "missing" | "foreign" | "unknown" — the same vocabulary
	// internal/api/system_managed_secrets.go renders per row.
	ManagedSecretsState = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sharko_managed_secrets_state",
		Help: "Count of managed secrets currently in each state, by engine",
	}, []string{"engine", "state"})

	// ReconcilerItemFailures is the stuck-loop counter's failure half (P2-D
	// D2): one increment per item whose check or write itself could not
	// complete this pass — never for a legitimate state like out_of_sync or
	// missing, which are findings, not failures. reason is a SMALL fixed
	// set keyed the same way each package's FailureSentence functions key
	// failing stages (e.g. "git_read", "credentials", "write_failed") —
	// never free text, so this counter's cardinality stays bounded
	// regardless of what the underlying error actually said.
	ReconcilerItemFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "sharko_reconciler_item_failures_total",
		Help: "Per-item check/write failures by reason",
	}, []string{"engine", "reason"})

	// ReconcilerWrites is the stuck-loop counter's write half (P2-D D2): one
	// increment per actual Kubernetes write a pass makes. A flat non-zero
	// rate here alongside an unchanging ManagedSecretsState gauge is the
	// "Sharko keeps repairing the same thing forever" signal — something
	// else keeps reverting what Sharko just wrote. kind is "created" |
	// "updated" | "deleted".
	ReconcilerWrites = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "sharko_reconciler_writes_total",
		Help: "Reconciler writes to Kubernetes by kind",
	}, []string{"engine", "kind"})

	// ReconcilerFights is a snapshot gauge (P2-D D3): how many items this
	// engine currently considers "in a fight" — the connection engine's
	// label-fight detector (three or more consecutive reverted ticks) or
	// the values engine's consecutive-failure count reaching the same
	// three-in-a-row bar. Set at the end of every completed pass.
	ReconcilerFights = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sharko_reconciler_fights",
		Help: "Count of items currently in a fight or consecutive-failure state (>=3 in a row), by engine",
	}, []string{"engine"})

	// ReconcilerEnabled (M7, code review) is 1 when an engine is currently
	// switched on, 0 when an admin has deliberately turned it off (Settings
	// -> Addon Values Engine — the only engine with an off switch today; the
	// cluster-connection engine has none, on purpose, and always reports 1).
	// Written on Start(), on every settings toggle, and at the end of every
	// pass — so the value is never more than one tick stale. This is the
	// gauge charts/sharko/templates/prometheusrules.yaml's pass-age and
	// sustained-bad-state alerts gate on: a deliberately-off engine must not
	// page anybody for having gone quiet, since going quiet is exactly what
	// turning it off was for.
	ReconcilerEnabled = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sharko_reconciler_enabled",
		Help: "1 when this engine is switched on, 0 when an admin has turned it off",
	}, []string{"engine"})
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

// AI annotate metrics. Outcome label is one of:
//
//	"ok", "not_configured", "empty_input", "oversize", "secret_blocked",
//	"timeout", "llm_error", "parse_error", "opted_out", "disabled".
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

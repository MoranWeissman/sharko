// Package metrics defines all Prometheus metrics for Sharko.
// All metrics are auto-registered with the default prometheus registry via promauto.
package metrics

import (
	"strconv"
	"sync"
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

	// CatalogEntriesCount is how many addons the org has approved — the
	// number of entries in catalog.yaml on the base branch, as of the last
	// time Sharko read that file.
	//
	// It is a knownOnlyGauge, not a plain gauge, and that is the whole
	// point of it (B5). A plain gauge publishes 0 from process start, so a
	// server that has not yet read the file — or has no Git connection at
	// all — reports "your org has approved nothing" as if it were a
	// measurement. It is not; it is the absence of one. This collector
	// stays silent until SetCatalogEntries is called, exactly as a
	// labelled collector with no children does.
	//
	// A measured zero IS published: a repo with no catalog.yaml has
	// genuinely approved nothing, and that is worth graphing.
	CatalogEntriesCount = mustRegisterKnownOnlyGauge(prometheus.GaugeOpts{
		Name: "sharko_catalog_entries_count",
		Help: "Number of addons in the org's approved catalog (catalog.yaml), as of the last read; absent until Sharko has read it once",
	})
)

// SetCatalogEntries records how many addons the org's approved catalog
// holds. Call this ONLY where the live catalog.yaml on the base branch has
// just been read — never from a preview, a candidate body being validated
// before a pull request, or a migration, because those are proposals and
// not the state of the repo.
//
// The single caller today is loadOrgCatalog in
// internal/api/catalog_org.go, which is the one funnel every API read of
// the live approved catalog goes through, and which the background
// freshness scheduler also drives on its own timer.
func SetCatalogEntries(n int) {
	CatalogEntriesCount.Set(float64(n))
}

// ForgetCatalogEntriesForTest puts the catalog gauge back to "never
// measured", so a test can prove it publishes nothing before Sharko has
// read anything. Test-only; production code never calls it.
func ForgetCatalogEntriesForTest() { CatalogEntriesCount.forgetForTest() }

// Catalog sources metrics (v1.23 Subsystem A — third-party catalog fetch loop).
// The fetcher (internal/catalog/sources) emits these per fetch attempt.
//
// The `url` label is ALWAYS the single fixed word "redacted" — never the
// address the operator wrote, and never anything computed from it. GET
// /metrics needs no login, and a private catalog is addressed by writing a
// token into the address's own path, where no grammar can spot it — so the
// configured address is sensitive because of what it is, not what a check
// finds in it. Every configured source therefore shares that one label, and
// their numbers add up together on one line. Each Help string below repeats
// that, because it is what an operator reads on a dashboard.
var (
	CatalogSourceFetchTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "sharko_catalog_source_fetch_total",
		Help: "Third-party catalog fetch attempts by outcome (ok|stale|failed). The url label is always the fixed word \"redacted\" — a configured source address is never published — so every configured source is counted together on that one line, and the count is the true total across all of them.",
	}, []string{"url", "status"})

	CatalogSourceLastSuccess = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sharko_catalog_source_last_success_timestamp",
		Help: "Unix timestamp of the last successful third-party catalog fetch. The url label is always the fixed word \"redacted\" — a configured source address is never published — so every configured source shares that one line, and it shows whichever of them succeeded most recently.",
	}, []string{"url"})

	CatalogSourceEntries = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "sharko_catalog_source_entries",
		Help: "Number of entries in the most recently written third-party catalog snapshot. The url label is always the fixed word \"redacted\" — a configured source address is never published — so every configured source shares that one line, and it shows the most recent one written, not a total.",
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
)

// sharko_pr_merge_duration_seconds used to be declared here, and nothing
// ever observed into it. It was removed in B5 rather than wired up,
// because Sharko does not hold the two timestamps that histogram claims
// to measure.
//
// The start is fine: prtracker.PRInfo.CreatedAt is when Sharko opened the
// pull request. The end is not. The only merge signal Sharko has is
// GitProvider.GetPullRequestStatus (internal/gitprovider/provider.go),
// which returns the word "merged" and no timestamp, observed by a poll
// loop running every 30s by default (SHARKO_PR_POLL_INTERVAL). So the
// only end time available is "when the tracker next looked", which is up
// to a full poll interval late and arbitrarily later than that if the
// server was down.
//
// On a histogram whose first bucket was 10 seconds, and with auto-merge
// turned on — where most Sharko pull requests merge in under a second —
// that would have put nearly every merge in a bucket it did not belong
// in. A wrong number on a graph is worse than no number, so there is no
// number. Wiring this back needs a real merged-at timestamp from the Git
// provider first; ListPullRequests already surfaces one
// (gitprovider.PullRequest.ClosedAt, filled from GitHub's merged_at), so
// the honest version of this metric starts there, not here.

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

	// ActiveSessions is how many human logins are valid right now.
	//
	// It is a GaugeFunc, counted at scrape time, not a gauge somebody
	// remembers to Set (B5). Sessions do not only appear and disappear on
	// login and logout — they also just run out, 24 hours after the login,
	// and the sweep that removes them from the map only runs once an hour.
	// A gauge written on login and logout would therefore report people as
	// still signed in for up to an hour after their session had died.
	// Counting at scrape time, with the expiry checked per entry, cannot
	// drift.
	//
	// Before a source is registered this reports 0, and 0 is the truth
	// then: the session map lives in the HTTP router, so a process with no
	// router has nobody signed in to count.
	ActiveSessions = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "sharko_active_sessions",
		Help: "Number of human login sessions that are valid right now (expired ones are not counted, whether or not they have been swept yet)",
	}, activeSessionsValue)
)

// activeSessionsSource is set once, by the HTTP router, to a function that
// counts the sessions that have not expired. Stored through a mutex rather
// than read directly because the /metrics scrape and NewRouter are on
// different goroutines.
var (
	activeSessionsMu     sync.RWMutex
	activeSessionsSource func() int
)

// SetActiveSessionsSource tells the sharko_active_sessions gauge where to
// count from. internal/api's NewRouter is the only caller — that is the
// package the session map lives in.
func SetActiveSessionsSource(fn func() int) {
	activeSessionsMu.Lock()
	activeSessionsSource = fn
	activeSessionsMu.Unlock()
}

func activeSessionsValue() float64 {
	activeSessionsMu.RLock()
	fn := activeSessionsSource
	activeSessionsMu.RUnlock()
	if fn == nil {
		return 0
	}
	return float64(fn())
}

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
//	"timeout", "llm_error", "parse_error".
//
// THAT LIST IS EXACTLY WHAT THE WRITER CAN EMIT, AND IT USED TO BE WRONG.
// The comment and the Help string below both named "opted_out" and
// "disabled" as outcomes. Neither is ever written: the only writer is
// recordAnnotate (internal/orchestrator/ai_annotate.go), and every value it
// can be handed is in the list above. A Help string is the contract an
// operator writes an alert rule against, so naming a label value nothing
// emits is a promise the product cannot keep — an alert on
// outcome="disabled" would sit green forever. The docs branch corrected the
// published page and correctly left this file alone; this is the code half.
// "empty_input", which IS emitted, was missing from the Help string too.
//
// Operators use these to spot LLM cost runaway (high call rate),
// LLM-provider degradation (rising "timeout" / "llm_error" rate), or
// consistent secret-leak hits (rising "secret_blocked" — usually a sign
// the maintainer has secrets baked into a chart and should fix that).
var (
	AIAnnotateTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "sharko_ai_annotate_total",
		Help: "AI annotate calls by outcome (ok, not_configured, empty_input, oversize, secret_blocked, timeout, llm_error, parse_error)",
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

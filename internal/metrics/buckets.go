package metrics

// SLO-surface histogram bucket boundaries.
//
// Bucket boundaries are sized to the V2-1.2 perf baselines captured in
// docs/site/operator/perf-baselines.yaml. The "right edge" of each bucket
// set is anchored at the slowest phase's p99 of the corresponding path,
// rounded up to give headroom for cold-cache / degraded-mode observations.
//
// All values are in seconds (Prometheus histogram convention for
// `*_duration_seconds` metric families).
//
// If you change boundaries here, regenerate the per-metric inventory in
// docs/site/operator/metrics-naming.md and update the regression test
// `TestSLOBucketsMatchLiterals` in metrics_test.go.

import "github.com/prometheus/client_golang/prometheus"

// clusterRegistrationBuckets — cluster_registration path.
//
// V2-1.2 baselines (phases):
//
//	ui_submit                    p50=21.6ms   p99=2150.9ms   (slowest p99)
//	argocd_secret_created        p50=443.6ms  p99=916.9ms
//	argocd_application_reachable p50=13.2ms   p99=37.7ms
//
// Right edge anchored at the slowest p99 (~2.15s), rounded up to 5s for
// ~2.3x headroom. ~3 buckets below median p50 (~22ms), ~7 above.
var clusterRegistrationBuckets = []float64{
	0.005, 0.010, 0.020,
	0.050, 0.100, 0.250, 0.500, 1.0, 2.5, 5.0,
}

// addonCycleBuckets — addon_cycle path.
//
// The V2-3 SLO defines addon_cycle as "PR open -> merge -> reconciler
// converge -> ArgoCD sync" — a minutes-long asynchronous flow. The V2-1.2
// baselines only measure the dry-run phases (enable_dry_run,
// disable_dry_run, upgrade_global) which all complete in <1ms and are
// NOT representative of the real SLO surface.
//
// Until per-phase wiring lands the real cycle (V2-3.x follow-up), use the
// Prometheus default buckets which cover 5ms..10s. Observations of the
// real async cycle will saturate to +Inf — that is an honest signal until
// the baselines are refreshed against the real path.
//
// TODO V2-3.x: refresh bucket sizing once baselines cover the real
// PR -> merge -> reconciler -> ArgoCD sync cycle (multi-second to
// multi-minute), not just the dry-run sub-ms phases.
var addonCycleBuckets = prometheus.DefBuckets

// catalogScanBuckets — catalog_scan path.
//
// V2-1.2 baselines (phases):
//
//	catalog_load     p50=0.962ms p99=1.515ms (slowest p99)
//	list_addons      p50=0.599ms p99=1.091ms
//	sources_refresh  p50=0.411ms p99=0.593ms
//
// Right edge anchored at slowest p99 (~1.5ms), rounded up to 50ms (~33x
// headroom — generous because catalog ops can balloon under cold cache or
// large catalog sweeps). ~3 buckets below median p50 (~0.6ms), ~7 above.
var catalogScanBuckets = []float64{
	0.0001, 0.0003, 0.0005,
	0.001, 0.002, 0.003, 0.005, 0.010, 0.025, 0.050,
}

// dashboardReadBuckets — dashboard_read path.
//
// V2-1.2 baselines (phases):
//
//	pull_requests p50=0.140ms p99=0.365ms
//	fleet_status  p50=0.177ms p99=0.479ms (slowest p99)
//	repo_status   p50=0.142ms p99=0.281ms
//
// Right edge anchored at slowest p99 (~0.5ms), rounded up to 50ms (~100x
// headroom — covers cold-cache + degraded ArgoCD list calls). ~3 buckets
// below median p50 (~0.15ms), ~7 above.
var dashboardReadBuckets = []float64{
	0.00005, 0.0001, 0.0002,
	0.0005, 0.001, 0.002, 0.005, 0.010, 0.025, 0.050,
}

// bucketsForPath returns the histogram bucket boundaries for a given SLO
// path ID. Returns prometheus.DefBuckets for unknown path IDs so that
// metric construction never panics on a typo.
func bucketsForPath(path string) []float64 {
	switch path {
	case PathClusterRegistration:
		return clusterRegistrationBuckets
	case PathAddonCycle:
		return addonCycleBuckets
	case PathCatalogScan:
		return catalogScanBuckets
	case PathDashboardRead:
		return dashboardReadBuckets
	default:
		return prometheus.DefBuckets
	}
}

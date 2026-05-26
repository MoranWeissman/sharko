//go:build e2e

// V2-1.1 — locked critical-path + phase definitions.
//
// This file is the canonical source of truth for the 4 critical paths
// the Sharko e2e harness measures for p50/p95/p99 baselines and the
// downstream V2-1.3 SLO targets + V2-1.4 CI regression gate. The phase
// names MUST stay stable across V2-1.x stories — they are written into
// the recorded perf-baselines.md table and read by the in-house stats
// helper, the docs page, and (eventually) the CI gate's threshold map.
//
// Adding a new phase: extend the appropriate Path* slice in this file,
// update docs/site/developer-guide/perf-harness.md with the new
// boundary's semantics, regenerate baselines (re-run the perf-tagged
// test 30+ times and refresh docs/site/operator/perf-baselines.md).
//
// Renaming a phase: forbidden without bumping the harness major and
// invalidating the recorded baseline — the JSON log lines emitted by
// PhaseTimer carry these strings verbatim and downstream consumers
// (stats helper, CI gate) match on them.
package harness

// Critical-path identifiers. These appear as the "path" field in every
// PhaseTimer JSON log line. Keep snake_case; keep them short — they
// appear in the baselines table headers.
const (
	// PathClusterRegistration covers the end-to-end cluster register
	// flow exercised by TestClusterLifecycle: UI submit → ArgoCD secret
	// created → ArgoCD Application reachable.
	PathClusterRegistration = "cluster_registration"

	// PathAddonCycle covers the per-cluster addon enable/disable +
	// global upgrade cycle exercised by TestPerClusterAddonLifecycle.
	PathAddonCycle = "addon_cycle"

	// PathCatalogScan covers the full sweep across enabled catalog
	// sources exercised by TestCatalogReads.
	PathCatalogScan = "catalog_scan"

	// PathDashboardRead covers the aggregated dashboard reads
	// exercised by TestDashboardAndReadsInProcess. The cardinality
	// proxy is N clusters × M addons; in the in-process harness both
	// are zero, but the handler-dispatch + connection-lookup cost is
	// what the baseline captures.
	PathDashboardRead = "dashboard_read"
)

// Phase identifiers per path. The slices below are the AUTHORITATIVE
// list of phases the harness measures — downstream V2-1.3/V2-1.4 stories
// read this file (and perf-harness.md) to size SLO targets + CI
// thresholds.

// Cluster registration phases (TestClusterLifecycle).
const (
	// PhaseUISubmit brackets POST /api/v1/clusters from request send to
	// response decoded. Approximates the UI's "Register" button click
	// latency.
	PhaseUISubmit = "ui_submit"

	// PhaseArgoCDSecretCreated brackets the direct-register
	// compensation that ensures the ArgoCD cluster Secret exists. In
	// production this is owned by internal/clusterreconciler; in the
	// in-process harness it is a synchronous helper. Either way the
	// phase measures "kubeconfig + token transformed into an in-cluster
	// ArgoCD cluster Secret".
	PhaseArgoCDSecretCreated = "argocd_secret_created"

	// PhaseArgoCDApplicationReachable brackets the Eventually loop that
	// asserts the cluster surfaces in GET /api/v1/clusters as
	// Managed=true — the externally observable "Sharko sees it" gate.
	PhaseArgoCDApplicationReachable = "argocd_application_reachable"
)

// Addon enable/disable + upgrade phases (TestPerClusterAddonLifecycle).
const (
	// PhaseEnableDryRun brackets POST /clusters/{c}/addons/{a} with
	// dry_run=true — the per-cluster enable preview round-trip.
	PhaseEnableDryRun = "enable_dry_run"

	// PhaseDisableDryRun brackets DELETE /clusters/{c}/addons/{a} with
	// dry_run=true — the per-cluster disable preview round-trip.
	PhaseDisableDryRun = "disable_dry_run"

	// PhaseUpgradeGlobal brackets POST /addons/{a}/upgrade — the live
	// catalog rewrite + PR open path. Touches only the catalog file;
	// no remote ArgoCD call required.
	PhaseUpgradeGlobal = "upgrade_global"
)

// Catalog scan phases (TestCatalogReads).
const (
	// PhaseCatalogLoad brackets catalog.Load() on the embedded curated
	// catalog. Establishes the parser cost baseline.
	PhaseCatalogLoad = "catalog_load"

	// PhaseListAddons brackets GET /api/v1/catalog/addons. The full
	// list-all-curated-entries response — the dominant catalog-read
	// surface from the marketplace UI.
	PhaseListAddons = "list_addons"

	// PhaseSourcesRefresh brackets POST /api/v1/catalog/sources/refresh.
	// With no third-party fetcher wired the refresh is a no-op but
	// still exercises the same audit + authz path.
	PhaseSourcesRefresh = "sources_refresh"
)

// Dashboard aggregated read phases (TestDashboardAndReadsInProcess).
const (
	// PhasePullRequests brackets GET /api/v1/dashboard/pull-requests.
	// Exercises the active git provider; resilient to a missing ArgoCD
	// connection (returns 200 with the PR list).
	PhasePullRequests = "pull_requests"

	// PhaseFleetStatus brackets GET /api/v1/observability/fleet-status.
	// Resilient aggregate handler — reports git/argocd availability as
	// flags rather than failing the request.
	PhaseFleetStatus = "fleet_status"

	// PhaseRepoStatus brackets GET /api/v1/observability/repo-status.
	// Resilient — reports bootstrap state without hard-failing on a
	// fresh repo.
	PhaseRepoStatus = "repo_status"
)

// PhasesForPath returns the ordered phase list for a given critical
// path. Returns nil for unknown paths. Downstream tooling (stats
// helper, docs generator) iterates these to render per-path tables.
func PhasesForPath(path string) []string {
	switch path {
	case PathClusterRegistration:
		return []string{
			PhaseUISubmit,
			PhaseArgoCDSecretCreated,
			PhaseArgoCDApplicationReachable,
		}
	case PathAddonCycle:
		return []string{
			PhaseEnableDryRun,
			PhaseDisableDryRun,
			PhaseUpgradeGlobal,
		}
	case PathCatalogScan:
		return []string{
			PhaseCatalogLoad,
			PhaseListAddons,
			PhaseSourcesRefresh,
		}
	case PathDashboardRead:
		return []string{
			PhasePullRequests,
			PhaseFleetStatus,
			PhaseRepoStatus,
		}
	default:
		return nil
	}
}

// AllPaths returns the canonical critical-path identifiers in the
// order they appear in the baselines doc.
func AllPaths() []string {
	return []string{
		PathClusterRegistration,
		PathAddonCycle,
		PathCatalogScan,
		PathDashboardRead,
	}
}

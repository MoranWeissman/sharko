# Reference — Metrics, Alerts, and the Grafana Dashboard

This page lists every metric Sharko actually exports on `/metrics`, the alert rules the Helm chart can install, and the Grafana dashboard shipped in the repo. It's the "what exists" reference — for naming rules and per-surface sizing details, see [Metrics Naming](metrics-naming.md); for how the SLO targets were set, see [SLOs](slos.md) and [Perf Baselines](perf-baselines.md).

Sharko serves metrics on the unauthenticated `/metrics` endpoint, in standard Prometheus exposition format.

## SLO surface metrics

Four request paths are instrumented as "SLO surfaces": `cluster_registration`, `addon_cycle`, `catalog_scan`, `dashboard_read`. Each one exports the same three-metric pattern, defined in `internal/metrics/slo.go` and `internal/metrics/slo_registry.go`:

| Metric name pattern | Type | Labels | Meaning |
|---|---|---|---|
| `sharko_<path>_duration_seconds` | Histogram | `phase` | How long this surface's operations take, broken down by sub-stage (`phase`). |
| `sharko_<path>_total` | Counter | `code` | How many times this surface was invoked, by outcome code (HTTP status or a short domain code). |
| `sharko_<path>_errors_total` | Counter | `code` | How many of those invocations failed, by the same code. |

So for `cluster_registration` you get `sharko_cluster_registration_duration_seconds`, `sharko_cluster_registration_total`, and `sharko_cluster_registration_errors_total` — and the same shape repeats for the other three paths. See [Metrics Naming](metrics-naming.md#slo-surface-inventory) for the phase label values and bucket sizing per path.

## Everything else Sharko exports

These are the metrics registered in `internal/metrics/metrics.go` (the "legacy" default-registry metrics — kept stable for existing dashboards, alongside the SLO surfaces above).

### Clusters

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `sharko_cluster_count` | Gauge | `status` | Number of clusters, grouped by status. |
| `sharko_cluster_status` | Gauge | `cluster`, `status` | One-hot: 1 if this cluster is currently in this status, 0 otherwise. |
| `sharko_cluster_last_verified_timestamp` | Gauge | `cluster` | Unix time of the last successful connectivity verification for this cluster. |
| `sharko_cluster_last_test_duration_seconds` | Histogram | `cluster`, `stage` | How long a connectivity test took, per stage. |
| `sharko_cluster_test_failures_total` | Counter | `cluster`, `error_code` | Connectivity test failures, by error code. |

### Addons and catalog

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `sharko_addon_sync_status` | Gauge | `cluster`, `addon`, `status` | ArgoCD sync status per addon (0/1, one-hot). |
| `sharko_addon_health` | Gauge | `cluster`, `addon`, `health` | ArgoCD health status per addon (0/1, one-hot). |
| `sharko_addon_version` | Gauge | `cluster`, `addon`, `version` | The version currently reported for an addon on a cluster. |
| `sharko_catalog_entries_count` | Gauge | — | Total number of addons in the catalog right now. |
| `sharko_catalog_source_fetch_total` | Counter | `url`, `status` | Third-party catalog source fetch attempts, by source URL and outcome (`ok`, `stale`, `failed`). |
| `sharko_catalog_source_last_success_timestamp` | Gauge | `url` | Unix time of the last successful fetch for a third-party catalog source. |
| `sharko_catalog_source_entries` | Gauge | `url` | Number of entries in the current snapshot of a third-party catalog source. |
| `sharko_scorecard_refresh_total` | Counter | `status` | OpenSSF Scorecard refresh runs, by outcome. |
| `sharko_scorecard_last_refresh_timestamp` | Gauge | — | Unix time of the last Scorecard refresh cycle. |

### Reconcilers (cluster-connection and addon-values engines)

Both of Sharko's background reconcilers — the one that keeps ArgoCD cluster-secret labels in step with `managed-clusters.yaml`, and the one that delivers addon secret values — report through the same metric family, distinguished by the `engine` label (`cluster_connection` or `addon_values`):

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `sharko_reconciler_runs_total` | Counter | `engine`, `outcome` | Reconciler passes, by outcome (`success`, `partial`, `failure`). |
| `sharko_reconciler_duration_seconds` | Histogram | `engine` | How long a reconciler pass took. |
| `sharko_reconciler_last_run_timestamp` | Gauge | `engine` | Unix time of the last completed pass, regardless of outcome. |
| `sharko_reconciler_last_success_timestamp` | Gauge | `engine` | Unix time of the last pass that completed without aborting. This is the gauge the `SharkoReconcilerPassAge` alert watches. |
| `sharko_reconciler_items_checked_total` | Counter | `engine` | Items (clusters or secrets) processed per pass. |
| `sharko_reconciler_items_changed_total` | Counter | `engine`, `action` | Items changed, by the kind of change made. |
| `sharko_managed_secrets_state` | Gauge | `engine`, `state` | Snapshot: how many secrets this engine currently sees in each state (`in_sync`, `out_of_sync`, `missing`, `foreign`, `unknown`). Written fresh at the end of every pass. |
| `sharko_reconciler_item_failures_total` | Counter | `engine`, `reason` | Per-item check/write failures, by a small fixed set of reasons (for example `git_read`, `credentials`, `write_failed`) — never free text. |
| `sharko_reconciler_writes_total` | Counter | `engine`, `kind` | Actual Kubernetes writes made by a pass, by kind (`created`, `updated`, `deleted`). |
| `sharko_reconciler_fights` | Gauge | `engine` | Snapshot: how many items are currently stuck in a repeated revert or repeated failure (three or more ticks in a row). |
| `sharko_reconciler_enabled` | Gauge | `engine` | 1 when this engine is switched on, 0 when an admin turned it off (only the addon-values engine has an off switch today — the cluster-connection engine always reports 1). |

### API, auth, and AI

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `sharko_api_requests_total` | Counter | `method`, `path`, `status` | HTTP requests, by method, normalized path, and status code. |
| `sharko_api_request_duration_seconds` | Histogram | `method`, `path` | HTTP request duration. |
| `sharko_auth_login_total` | Counter | `result` | Login attempts, by outcome. |
| `sharko_active_sessions` | Gauge | — | Current number of active sessions. |
| `sharko_pr_tracked` | Gauge | `status` | Count of pull requests Sharko is tracking, by status. |
| `sharko_pr_merge_duration_seconds` | Histogram | — | Time from PR creation to merge. |
| `sharko_ai_annotate_total` | Counter | `outcome` | AI "annotate values" calls, by outcome (`ok`, `not_configured`, `oversize`, `secret_blocked`, `timeout`, `llm_error`, `parse_error`, `opted_out`, `disabled`). |
| `sharko_ai_annotate_latency_seconds` | Histogram | `outcome` | Latency of AI annotate calls (secret-guard scan plus the LLM round-trip), by outcome. |

Path labels are normalized before being recorded (dynamic segments like a cluster or addon name are replaced with a placeholder such as `{name}`), so metric cardinality stays bounded regardless of fleet size.

## Alert rules

The Helm chart can install a `PrometheusRule` (`charts/sharko/templates/prometheusrules.yaml`), gated behind `monitoring.prometheusRules.enabled` (default `false` — not every cluster runs prometheus-operator). It ships two kinds of alerts.

### SLO burn-rate alerts

Each of the four SLO surfaces gets a fast-burn (pages, 1-hour window) and a slow-burn (files a ticket, 6-hour window) alert pair, following the Google SRE book's multi-window burn-rate pattern. These are brief on purpose — the full explanation of the SLO targets and what burning fast vs. slow means lives in [SLOs](slos.md), and the exact steps to take when one fires are in the [Budget-Burn Runbook](budget-burn-runbook.md).

### Reconciler health alerts

Two alerts watch the reconciler metrics directly, since a stalled reconciler or a pile of unrepaired secrets is a correctness problem, not a latency one:

- **`SharkoReconcilerPassAge`** — fires when an engine hasn't completed a successful pass in more than 10x its normal cadence (5 minutes for the cluster-connection engine, 30 minutes for the addon-values engine). In plain words: something is stopping one of Sharko's background loops from finishing cleanly, so drift is piling up unnoticed. Check the pod logs for that engine's tick/pass lines, and confirm its Git connection and Kubernetes access are both healthy. This alert automatically stays quiet for an engine an admin has deliberately switched off.
- **`SharkoManagedSecretsSustainedBadState`** — fires when secrets have sat in `missing` or `out_of_sync` state for 30 minutes straight, meaning Sharko's own repair loop isn't clearing them. In plain words: Sharko knows some secrets are wrong and has been trying to fix them for half an hour without success. Check the Secrets area in the UI (Cluster connections and Addon secrets) for the affected rows and their last check error, and confirm Sharko can reach the cluster and the secrets store. This alert also stays quiet for a switched-off engine, since nothing is going to repair it until it's turned back on.

## Grafana dashboard

The repo ships a ready-to-import Grafana dashboard at `charts/sharko/dashboards/sharko-overview.json`. It has six panels:

- **Cluster Status Overview** — gauge of clusters by status
- **Reconciler Run Duration** — time series, per engine
- **API Request Rate** — time series
- **API Latency Percentiles** — time series
- **Managed Secrets by State** — time series, per engine and state
- **Reconciler Writes & Item Failures** — time series

The chart doesn't wire this dashboard into a Grafana sidecar or ConfigMap automatically — import it by hand: in Grafana, go to **Dashboards → New → Import**, and upload `charts/sharko/dashboards/sharko-overview.json` (or paste its contents). Point it at the Prometheus data source you're scraping Sharko's `/metrics` endpoint with.

## See also

- [Metrics Naming](metrics-naming.md) — the naming scheme, per-surface bucket sizing, and operational notes (unauthenticated scraping, exemplars, cardinality budget)
- [SLOs](slos.md) — how the SLO targets were set and what the error budgets mean
- [Perf Baselines](perf-baselines.md) — the raw measurements the SLO targets are sized against
- [Budget-Burn Runbook](budget-burn-runbook.md) — step-by-step response when a burn-rate alert fires

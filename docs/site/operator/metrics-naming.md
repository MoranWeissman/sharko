# Metrics naming and exposition

Sharko exposes Prometheus metrics on the unauthenticated `/metrics`
endpoint of its HTTP listener. This page documents the naming scheme,
the per-metric inventory for the four V2-3 SLO surfaces, and the
operational choices the maintainers locked when shipping V2-3.1 / V2-3.2.

## Naming scheme

All metric names follow the pattern:

```
sharko_<surface>_<verb>_<unit>
```

- `sharko_` namespace — distinguishes Sharko metrics from kube-state,
  argocd-metrics, or anything else in the cluster's Prometheus.
- `<surface>` — the SLO path id (`cluster_registration`, `addon_cycle`,
  `catalog_scan`, `dashboard_read`) or the subsystem (`reconciler`,
  `api`, `auth`, ...).
- `<verb>` — what is being measured (`duration`, `errors`, `requests`,
  ...). The trailing `_total` suffix follows the OpenMetrics convention
  for monotonically increasing counters.
- `<unit>` — `seconds` for histograms, omitted for counters (since
  counts are unitless).

The four V2-3 SLO surfaces map to three metric families each:

| Path id                | Histogram                                    | Total counter                 | Error counter                        |
| ---------------------- | -------------------------------------------- | ----------------------------- | ------------------------------------ |
| `cluster_registration` | `sharko_cluster_registration_duration_seconds` | `sharko_cluster_registration_total` | `sharko_cluster_registration_errors_total` |
| `addon_cycle`          | `sharko_addon_cycle_duration_seconds`        | `sharko_addon_cycle_total`    | `sharko_addon_cycle_errors_total`    |
| `catalog_scan`         | `sharko_catalog_scan_duration_seconds`       | `sharko_catalog_scan_total`   | `sharko_catalog_scan_errors_total`   |
| `dashboard_read`       | `sharko_dashboard_read_duration_seconds`     | `sharko_dashboard_read_total` | `sharko_dashboard_read_errors_total` |

The path ids match the V2-1.2 baselines in
[`perf-baselines.yaml`](perf-baselines.md) verbatim — renaming any of
them invalidates the baselines and breaks the V2-3.3 recording rules.

## SLO surface inventory

Every SLO histogram carries the `phase` label; every counter carries
the `code` label.

### `cluster_registration`

- Sized to baseline: slowest phase `ui_submit` p99 = 2150.9 ms.
- Right edge: 5.0 s (~2.3x headroom).
- Histogram buckets (seconds):
  `0.005, 0.010, 0.020, 0.050, 0.100, 0.250, 0.500, 1.0, 2.5, 5.0`
- Phase label values: `total` (end-to-end), V2-3.x follow-up will add
  `ui_submit`, `argocd_secret_created`, `argocd_application_reachable`
  once handler structure permits per-phase wiring.
- Code label values: HTTP status (`200`, `201`, `400`, `502`, ...).

### `addon_cycle`

- Sized to baseline: **N/A** — V2-1.2 baselines only cover dry-run
  phases (sub-ms). The real SLO surface is the multi-second-to-minute
  PR-open → merge → reconciler-converge → ArgoCD-sync cycle.
- Histogram buckets (seconds): Prometheus defaults
  (`0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10`).
- TODO V2-3.x: refresh bucket sizing once the perf harness measures the
  real async cycle, not just dry-run phases.
- Phase label values: `enable`, `disable` (PR 1 wires both at the
  handler boundary). V2-3.x follow-up will split each into
  `pr_open`, `pr_merged`, `reconciler_converged`, `argo_sync`.
- Code label values: HTTP status.

### `catalog_scan`

- Sized to baseline: slowest phase `catalog_load` p99 = 1.515 ms.
- Right edge: 50 ms (~33x headroom — covers cold-cache and large
  catalog sweeps).
- Histogram buckets (seconds):
  `0.0001, 0.0003, 0.0005, 0.001, 0.002, 0.003, 0.005, 0.010, 0.025, 0.050`
- Phase label values: `total` (PR 1). V2-3.x will add `catalog_load`,
  `list_addons`, `sources_refresh` once `AddonService.GetCatalog` is
  refactored to expose its internal phases.
- Code label values: HTTP status.

### `dashboard_read`

- Sized to baseline: slowest phase `fleet_status` p99 = 0.479 ms.
- Right edge: 50 ms (~100x headroom — covers cold cache + degraded
  ArgoCD list).
- Histogram buckets (seconds):
  `0.00005, 0.0001, 0.0002, 0.0005, 0.001, 0.002, 0.005, 0.010, 0.025, 0.050`
- Phase label values: `fleet_status` (`/api/v1/dashboard/stats`),
  `attention` (`/api/v1/dashboard/attention`), `pull_requests`
  (`/api/v1/dashboard/pull-requests`).
- Code label values: HTTP status.

## Operational choices

### `/metrics` is unauthenticated

Industry standard for Prometheus scraping. Authentication on the scrape
endpoint adds operational friction (credentials in Prometheus config)
without meaningful security benefit — Sharko's metrics expose no secret
material. The security boundary is the cluster's NetworkPolicy / Service
selector / ServiceMonitor namespace selector, not a per-request auth
check.

If your environment requires authenticated scraping anyway, run Sharko
behind an authenticating reverse proxy that strips the auth header from
`/metrics` before forwarding to Prometheus, or expose `/metrics` on a
separate port bound to a private interface.

### No swagger / OpenAPI annotation on `/metrics`

The Prometheus exposition format is line-oriented text, not JSON;
OpenAPI annotations do not model it well. Including a swagger entry for
`/metrics` would mislead users into thinking it accepts standard JSON
content negotiation. The route is intentionally omitted from
`docs/swagger/`. CI's `swagger-check` job is aware of the exception.

### Histogram exemplars (OpenMetrics)

Histogram observations attach a `request_id` exemplar when the V2-2.2
correlation middleware has populated one on the request context. The
exemplar wire-up requires:

- Prometheus 2.43+ with `--enable-feature=exemplar-storage`.
- Grafana 9.4+ with the Prometheus data source set to
  "Exemplars enabled".

When both are configured, a Grafana drill-down from a histogram bucket
surfaces a clickable `request_id` link; a sibling Loki data source can
then jump straight to the matching slog line:

```logql
{app="sharko"} | json | request_id="<id>"
```

Older scrapers ignore the exemplar field — metrics still scrape
correctly, only the click-through join is unavailable.

### BYO scrape config — ServiceMonitor deferred

V2-3.1 does not ship a `ServiceMonitor` CR in the Helm chart. Operators
can write their own:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: sharko
  labels:
    app.kubernetes.io/name: sharko
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: sharko
  endpoints:
    - port: http
      path: /metrics
      interval: 30s
```

A first-party `ServiceMonitor` (gated by a chart value) is tracked as a
V2-3.x follow-up.

### Cardinality budget

The four SLO surfaces hold a soft cardinality budget of approximately
60 series:

- 4 paths × ≤5 phases × 1 histogram = 20 histogram series (plus 10
  buckets each = ~200 derived series at scrape time).
- 4 paths × ~3 status codes × 2 counters = ~24 counter series.

Adding new phases or paths requires an explicit update to this
inventory so the budget stays bounded. New high-cardinality labels
(e.g., `cluster_name`, `user`) are NOT acceptable on SLO histograms.

## Legacy metrics (default registry)

Sharko's `/metrics` endpoint also exposes the historical metric families
registered via `promauto` in `internal/metrics/metrics.go` (cluster
status, addon health, reconciler runs, API request counters, auth, AI,
catalog signing, Scorecard, etc.). The V2-3 SLO surfaces are composed
on top via `prometheus.Gatherers{prometheus.DefaultGatherer,
metrics.SLORegistry()}` so a single scrape returns both.

Legacy metric names predate the OTEL-aligned V2-3 naming scheme. They
are kept stable for operators relying on existing dashboards; new SLO
work follows the V2-3 scheme exclusively.

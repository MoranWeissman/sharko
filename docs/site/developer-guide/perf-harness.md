# Perf Harness — Critical-Path Timing

The Sharko e2e harness ships per-phase timing instrumentation for the 4
critical paths the V2-1 sprint locked as SLO-bearing for the v2.0.0
production launch. This page is the canonical reference for the locked
phase boundaries that downstream V2-1 stories (SLO targets, CI
regression gate) and the V2-3 Prometheus telemetry epic consume.

**Stability contract:** the path identifiers and phase identifiers
documented here are locked. Renaming them invalidates recorded
baselines, breaks the in-house stats helper's grouping, and corrupts
the V2-1.4 CI gate's threshold map. Adding a NEW phase to a path is
additive (extend the per-path table here, regenerate baselines); a new
critical path needs a new entry in `tests/e2e/harness/phases.go`'s
`AllPaths()` plus a new section in this doc.

## How to run the harness

```bash
# All 4 paths; 30+ iterations each. Cluster-registration is kind-backed
# and skip-graceful when kind/docker/kubectl are absent.
make test-e2e-perf

# Equivalent direct invocation:
go test -tags='e2e perf' -timeout=20m -v \
    -run '^TestPerf$' ./tests/e2e/lifecycle/...

# Run a single path (subtest):
go test -tags='e2e perf' -timeout=10m -v \
    -run '^TestPerf$/catalog_scan$' ./tests/e2e/lifecycle/...
```

Each subtest emits one JSON timing line per `(path, phase, iteration)`
via the harness's `PhaseTimer` helper and logs a rolled-up p50/p95/p99
table to the test output. Sample iteration count is `perfIterations`
(default 30; see `tests/e2e/lifecycle/perf_test.go`). The recorded
baselines live in
[`docs/site/operator/perf-baselines.md`](../operator/perf-baselines.md);
refresh that page by re-running `make test-e2e-perf` on the
measurement environment and updating the per-path tables.

## Timing emission shape

`PhaseTimer.End()` writes one newline-delimited JSON line per phase:

```json
{"path": "cluster_registration", "phase": "argocd_secret_created", "duration_ms": 443.628, "iteration": 7, "ts_ms": 1748278800123}
```

| Field         | Type    | Description |
|---------------|---------|-------------|
| `path`        | string  | One of the 4 locked critical-path identifiers |
| `phase`       | string  | One of the locked phase identifiers for that path |
| `duration_ms` | float64 | Wall-clock duration in milliseconds (sub-ms precision preserved) |
| `iteration`   | int     | 0-based iteration index within a single perf-tagged run |
| `ts_ms`       | int64   | Unix wall-clock end time in milliseconds |

The emissions default to `os.Stderr`. Tests that need to compute
statistics use `harness.SetTimingSink(buf)` to redirect into an
in-memory buffer, then feed the buffer to `harness.ComputeBaselines`
for p50 / p95 / p99 per `(path, phase)`.

## The 4 critical paths

The critical-path identifiers come from
`tests/e2e/harness/phases.go`. Each path has an ordered list of phase
boundaries — instrument the same boundaries when extending the
harness or adding new measurement surfaces (Prometheus histograms in
V2-3, alerting thresholds in V2-3.3).

### 1. `cluster_registration`

End-to-end cluster registration: UI submit → ArgoCD secret created
→ ArgoCD Application reachable.

| Phase | Brackets |
|-------|----------|
| `ui_submit` | `POST /api/v1/clusters` round-trip (request send → response decoded). Approximates the UI's "Register" button click latency. |
| `argocd_secret_created` | The kubeconfig + bearer token transformed into an in-cluster ArgoCD cluster Secret. In production this is owned by `internal/clusterreconciler` (post-merge trigger + 30s drift safety-net); in the in-process perf harness it's a synchronous helper. |
| `argocd_application_reachable` | The Eventually-loop that asserts the cluster surfaces in `GET /api/v1/clusters` as `Managed=true` — the externally observable "Sharko sees it" gate. |

### 2. `addon_cycle`

Per-cluster enable / disable + global upgrade cycle.

| Phase | Brackets |
|-------|----------|
| `enable_dry_run` | `POST /api/v1/clusters/{c}/addons/{a}` with `dry_run=true` — the per-cluster enable preview round-trip. |
| `disable_dry_run` | `DELETE /api/v1/clusters/{c}/addons/{a}` with `dry_run=true` — the per-cluster disable preview round-trip. |
| `upgrade_global` | `POST /api/v1/addons/{a}/upgrade` — live catalog rewrite + PR open path. Touches the catalog YAML in git; no remote ArgoCD call. |

### 3. `catalog_scan`

Catalog parse + read + sources refresh.

| Phase | Brackets |
|-------|----------|
| `catalog_load` | `catalog.Load()` on the embedded curated catalog. Startup-time baseline; flags any future caching regression. |
| `list_addons` | `GET /api/v1/catalog/addons` — full list-all-curated response (dominant marketplace-tab read). |
| `sources_refresh` | `POST /api/v1/catalog/sources/refresh` — admin-only refresh of the configured third-party fetchers. No-op in the in-process harness (no fetchers wired) but exercises the same audit + authz path. |

### 4. `dashboard_read`

Aggregated dashboard reads.

| Phase | Brackets |
|-------|----------|
| `pull_requests` | `GET /api/v1/dashboard/pull-requests` — active-PR list via the git provider. |
| `fleet_status` | `GET /api/v1/observability/fleet-status` — aggregated cluster + addon view; resilient handler reports availability as flags. |
| `repo_status` | `GET /api/v1/observability/repo-status` — bootstrap state; resilient handler reports state without hard-failing on a fresh repo. |

Cardinality of the dashboard reads scales with `N` clusters × `M`
addons. The in-process perf harness boots with `N = M = 0`, so the
recorded baseline is the dispatch + connection-lookup floor; a
populated-topology variant (future story) compares to the same phase
boundaries.

## Phase-boundary discipline

When adding a new measurement (whether for a new V2-x sprint, or
extending an existing path):

1. **Re-use a locked phase identifier when the operation is the same.**
   For example, a future "addon enable via CLI" path should re-use
   `enable_dry_run` rather than minting `cli_enable_dry_run`. Phase
   identity is the contract; per-call-site distinction is handled by
   the `path` field.

2. **Lock a new phase identifier in `tests/e2e/harness/phases.go`
   BEFORE wiring `PhaseTimer.StartPhase` calls in tests.** The phase
   list `PhasesForPath` returns is the authoritative consumer; the
   downstream V2-1.4 CI gate matches on it.

3. **Update this page in the same commit as the phases.go change.**
   The docs and the code are one artifact for the SLO + telemetry
   teams downstream.

4. **Regenerate baselines.** Re-run `make test-e2e-perf` on the
   measurement environment and update the per-path table in
   `docs/site/operator/perf-baselines.md`.

## Statistics

`harness.ComputeBaselines` parses the JSON-Lines emissions and
returns one `PathStats` per critical path, in the order
`harness.AllPaths()` declares. Each `PathStats` contains one
`PhaseStats` per phase with `p50`, `p95`, `p99`, `min`, and `max` in
milliseconds. Quantiles use the Type-7 linear-interpolation formula
(`(n-1) * q`-th rank with linear interpolation between adjacent
samples — the default in NumPy, R, and most stats packages). No
third-party deps; the implementation is ~30 lines in
`tests/e2e/harness/stats.go`.

## Skip-graceful policy

The harness mirrors the rest of the e2e suite's skip-graceful
convention:

- **Kind / docker / kubectl absent** → `cluster_registration` subtest
  skips with a clear message; the other 3 paths still run.
- **Per-iteration registration failure** → logged but not fatal; the
  baseline table reports the `N` iterations that did complete.
- **Empty input to `ComputeBaselines`** → returns one `PhaseStats`
  per phase with `N = 0` and all quantile fields zero; the table
  renderer still emits a row so the developer sees which phases
  produced no samples.

These guardrails keep the perf harness usable on a developer laptop
that's only partially set up.

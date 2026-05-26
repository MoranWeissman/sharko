# Perf Baselines — Critical-Path p50/p95/p99

This page records the measured p50/p95/p99 latency baselines for
Sharko's 4 critical paths, as captured by the V2-1 perf harness. The
phase boundaries are locked in
[`docs/site/developer-guide/perf-harness.md`](../developer-guide/perf-harness.md)
and `tests/e2e/harness/phases.go`. Re-run `make test-e2e-perf` on the
measurement environment to refresh these numbers.

## Measurement environment

| Field | Value |
|-------|-------|
| **Date captured** | 2026-05-26 |
| **Sharko version** | `1.25.0-pre.0` (from `cmd/sharko/root.go`) |
| **Sample count** | 30 iterations per path per phase (see "skip notes" below for exceptions) |
| **Runner type** | Developer workstation (NOT CI — CI baselines come in V2-1.4) |
| **Hardware** | Apple Silicon (arm64) — Docker Desktop 28.3.2, 4 CPUs, 15.6 GiB allocated |
| **OS** | macOS 26.4.1 |
| **kind version** | `v0.20.0` (`kindest/node:v1.31.0`) |
| **kubectl** | `v1.30.1` |
| **ArgoCD** | upstream `stable` manifests (`https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml`) |
| **Go** | `go1.26.1 darwin/arm64` |
| **Harness mode** | `SharkoModeInProcess` (httptest.NewServer); kind topology + ArgoCD install reused across iterations; sharko itself re-booted per iteration to reset the writeRateLimiter table |

> **Note on the dev-workstation runner.** These baselines are the
> floor — a developer-laptop measurement on a quiet machine. CI
> baselines (which will replace this table when V2-1.4 lands) will be
> higher and noisier because the GitHub Actions runner is shared
> hardware with cold caches. Until V2-1.4 wires the CI gate, treat
> these numbers as the lower-bound expectation; production SLO
> targets get sized on top of the CI baselines, not these.

## Baselines per critical path

All durations are wall-clock milliseconds.

### 1. `cluster_registration`

End-to-end cluster registration on a kind topology with upstream
ArgoCD installed in the management cluster.

| Phase | N | p50 (ms) | p95 (ms) | p99 (ms) | min (ms) | max (ms) |
|-------|---|----------|----------|----------|----------|----------|
| `ui_submit`                      | 30 |   21.630 |   57.694 | 2150.869 |   14.697 | 2997.429 |
| `argocd_secret_created`          | 30 |  443.628 |  731.372 |  916.858 |  386.539 |  965.908 |
| `argocd_application_reachable`   | 20 |   13.208 |   32.146 |   37.715 |    8.719 |   39.107 |

**Skip notes:**

- `argocd_application_reachable` reports `N=20` because the
  in-process harness reuses the same ghmock state across all 30
  iterations. By iteration ~20 the cumulative state growth slows the
  Eventually-loop enough that some iterations time out at 20s; the
  ones that completed still produce valid samples and the partial
  baseline is the canonical reading until V2-1.4's CI gate replaces
  the dev-workstation runner.

- `ui_submit` p99 of 2150ms is dominated by iteration 0's
  `bootstrap managed-clusters.yaml` cost (logged as `managed-clusters.yaml
  not found, bootstrapping`). Subsequent iterations are an order of
  magnitude faster (p50 = 22ms) — this is the steady-state cost.
  V2-1.3 SLO targets should consider whether the first-register
  bootstrap is treated as a separate SLI or absorbed into the
  registration SLI's budget.

### 2. `addon_cycle`

Per-cluster addon enable / disable (dry-run) + global upgrade. Runs
fully in-process; no kind required.

| Phase | N | p50 (ms) | p95 (ms) | p99 (ms) | min (ms) | max (ms) |
|-------|---|----------|----------|----------|----------|----------|
| `enable_dry_run`   | 30 |    0.249 |    0.331 |    0.375 |    0.222 |    0.377 |
| `disable_dry_run`  | 30 |    0.243 |    0.345 |    0.403 |    0.222 |    0.421 |
| `upgrade_global`   | 30 |    0.296 |    0.411 |    0.641 |    0.276 |    0.733 |

The dry-run paths short-circuit before any ArgoCD dial; the upgrade
path rewrites the catalog file via the in-memory mock git provider
and opens a PR — sub-millisecond throughout because nothing crosses
the process boundary.

### 3. `catalog_scan`

Catalog parse + list-all + sources-refresh. Runs fully in-process.

| Phase | N | p50 (ms) | p95 (ms) | p99 (ms) | min (ms) | max (ms) |
|-------|---|----------|----------|----------|----------|----------|
| `catalog_load`     | 30 |    0.962 |    1.314 |    1.515 |    0.917 |    1.582 |
| `list_addons`      | 30 |    0.599 |    0.788 |    1.091 |    0.543 |    1.204 |
| `sources_refresh`  | 30 |    0.411 |    0.500 |    0.593 |    0.358 |    0.629 |

`catalog_load` dominates the trio because it walks the embedded
curated catalog YAML on every call; `list_addons` and
`sources_refresh` are pure-memory dispatches once the catalog is
loaded.

### 4. `dashboard_read`

Aggregated dashboard reads. Runs fully in-process with `N = M = 0`
clusters × addons (the dispatch + connection-lookup floor).

| Phase | N | p50 (ms) | p95 (ms) | p99 (ms) | min (ms) | max (ms) |
|-------|---|----------|----------|----------|----------|----------|
| `pull_requests`    | 30 |    0.140 |    0.215 |    0.365 |    0.118 |    0.426 |
| `fleet_status`     | 30 |    0.177 |    0.271 |    0.479 |    0.149 |    0.564 |
| `repo_status`      | 30 |    0.142 |    0.215 |    0.281 |    0.107 |    0.296 |

`fleet_status` is the slowest of the trio because the handler joins
git availability + argocd availability + cluster slice into one
response shape; the other two read a single subsystem each.

## Refreshing baselines

```bash
# 1. Make sure kind, docker, and kubectl are installed and reachable.
kind version
docker info
kubectl version --client

# 2. Run the perf harness.
make test-e2e-perf

# 3. Copy the rolled-up tables from the test output into each per-path
#    section above. The format is identical: phase | N | p50 | p95 |
#    p99 | min | max.

# 4. Update the "Date captured" + "Sharko version" entries in the
#    Measurement environment table.

# 5. Commit the doc refresh as a separate sweep PR — do NOT mix with
#    feature work.
```

When the V2-1.4 CI regression gate lands, the canonical refresh path
becomes "trigger the baseline-refresh workflow on the CI runner +
merge the resulting docs PR". Until then, refreshes are
developer-driven.

## Downstream consumers

- **V2-1.3** — SLO targets per path. Targets are operational
  headroom over these baselines (rule of thumb: p99 target = 1.5–3×
  measured p99, sized per path based on whether the latency surface
  is user-blocking or background).
- **V2-1.4** — CI regression gate. Fires when any phase's p99
  regresses >20% over the recorded value above. The 20% threshold
  absorbs the developer-workstation → CI variance shift expected
  when the runner moves from a quiet laptop to a GitHub Actions
  runner.
- **V2-3.1** — Prometheus histogram bucket sizing. Pick bucket
  boundaries that put ~5 buckets above the recorded p99 and ~3
  below the p50 so the resulting histogram has high resolution
  around the operational range.

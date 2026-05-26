# SLOs — Service Level Objectives for v2.0.0

> **Verified:** 2026-05-24 — targets derived from the V2-1 perf
> baselines captured by the in-process e2e harness on a developer
> workstation (see
> [`docs/site/operator/perf-baselines.md`](perf-baselines.md)). No
> production data exists yet; targets are headroom-sized over the
> baseline and re-tighten after V2-1.4 CI baselines + the first 90
> days of production telemetry.

## v2.0.0 SLO commitment

Sharko's v2.0.0 production launch ships with measurable Service Level
Objectives on the **4 critical paths** that the V2-1 perf harness
instruments:

1. `cluster_registration` — UI submit → ArgoCD Secret → ArgoCD
   Application reachable
2. `addon_cycle` — per-cluster enable / disable + global upgrade
3. `catalog_scan` — catalog load + list-all + sources refresh
4. `dashboard_read` — aggregated dashboard reads (pull requests,
   fleet status, repo status)

These are the **first** SLOs published for Sharko. They are
deliberately framed as operational ceilings — not as the floor
measured by the perf harness — so that operators have headroom to
respond before users feel pain.

### Sizing methodology

Each per-phase p99 target is sized over the recorded baseline using
this rule of thumb (from
[`perf-baselines.md`](perf-baselines.md#downstream-consumers)):

| Surface type | p99 headroom over baseline |
|--------------|----------------------------|
| In-process dispatch (no remote I/O) | ~3× (absorbs the dev→CI→prod gap; real prod adds HTTP round-trip) |
| Real ArgoCD / kube-apiserver call | ~3× (CI runners are slower than dev workstations; production AWS API latency is higher than kind-on-Docker-Desktop) |
| Real git provider round-trip (GitHub / GitLab) | ~3× plus an absolute floor that covers third-party API latency |
| User-blocking UI path | Steady-state baseline first; cold-start outliers absorbed by the rolling budget (see "First-register bootstrap" below) |

Initial v2.0.0 targets carry **extra headroom (~3× p99 vs. the more
typical 1.5×)** to accommodate the gap between developer-workstation
baselines and the production runtime. After V2-1.4 lands the CI gate
and the first 90 days of production data is in hand, the targets get
re-tightened (see "Re-baselining" at the bottom of this page).

### Error budget framing

Each path has a **rolling-30-day error budget** stated as a
percentage of cycles that may breach any per-phase p99 target. The
budget covers all phases jointly: a cycle that breaches `p99` on
*any* phase counts against the same budget — operators don't need to
track per-phase budgets in their head.

### Burn-rate triggers

Each path defines two burn-rate triggers following the Google SRE
book's multi-window, multi-burn-rate pattern:

- **Investigate** — engineering attention warranted; no page. Either
  a moderately fast burn over a long window (slow leak) or a fast
  burn over a short window (transient bump). Triage during business
  hours.
- **Page on-call** — wake someone up. Either a very fast burn over a
  short window (sudden break) or a sustained moderate burn over a
  longer window (steady degradation that will exhaust the budget).

Concrete PromQL recording + alerting rules ship with
[V2-3.3](#cross-references) — this doc fixes the *thresholds*; that
epic wires them into Prometheus.

---

## 1. `cluster_registration`

### What this measures

The end-to-end "register a new cluster with Sharko" user journey: the
UI submits the cluster definition, Sharko creates the corresponding
ArgoCD cluster Secret, and the cluster becomes visible in the fleet
listing. This is the path that determines whether a platform engineer
can onboard their first cluster within the 10-minute time-to-first-
value goal.

### Phases

| Phase | What happens |
|-------|-------------|
| `ui_submit` | The `POST /api/v1/clusters` request lands and returns. Approximates the latency the operator sees when they click "Register" in the UI. |
| `argocd_secret_created` | Sharko transforms the submitted kubeconfig + bearer token into an in-cluster ArgoCD cluster Secret. Owned by `internal/clusterreconciler` in production (post-merge trigger + 30s drift safety-net). |
| `argocd_application_reachable` | The cluster surfaces in `GET /api/v1/clusters` with `Managed=true` — the externally observable "Sharko sees it" gate. |

### Targets

| Phase | Baseline p50 / p95 / p99 (ms) | SLO target p50 / p95 / p99 (ms) |
|-------|-------------------------------|---------------------------------|
| `ui_submit` | 22 / 58 / 2151 | 100 / 500 / 3000 |
| `argocd_secret_created` | 444 / 731 / 917 | 1000 / 2000 / 3000 |
| `argocd_application_reachable` | 13 / 32 / 38 | 50 / 150 / 300 |

### Sizing rationale

- `ui_submit` p99 target of 3000ms accommodates the first-register
  bootstrap outlier (see decision below) plus the dev→prod gap on a
  surface that is otherwise sub-100ms steady-state.
- `argocd_secret_created` p99 target of 3000ms gives ~3.3× headroom
  over the 917ms baseline because the production path hits a real
  ArgoCD API behind kube-apiserver and (commonly) an EKS control
  plane; both add real-network latency the kind-on-Docker-Desktop
  baseline does not see.
- `argocd_application_reachable` p99 target of 300ms gives ~8×
  headroom because the Eventually-loop polls a real-cluster watch
  stream in production where event propagation is bounded by
  kube-apiserver watch fan-out, not in-process channels.

### First-register bootstrap decision (LOCKED — Option B)

The `ui_submit` baseline p99 of 2150ms is dominated by iteration 0's
one-time `bootstrap managed-clusters.yaml` cost. Steady-state
operations are an order of magnitude faster (p50 = 22ms). Two options
were considered:

- **Option A** — treat first-register-after-install as a separate SLI
  with its own (much higher) target
- **Option B** — absorb the bootstrap cost into the registration
  SLI's budget; the budget is wide enough to swallow ~1 first-time
  hit per Sharko install

**Decision: Option B.** First-register-after-install is a
one-time-per-install event. Spending error budget on it is fine and
avoids two parallel SLI definitions that operators would have to
mentally context-switch between. The single registration SLI with a
3000ms p99 target absorbs the one-time bootstrap cleanly while still
constraining the steady-state path.

### Error budget

**≤1% of cluster-registration cycles may exceed any of the per-phase
p99 targets in any rolling 30-day window.**

A registration cycle = one `POST /api/v1/clusters` request through to
the `Managed=true` gate. A cycle that breaches `p99` on any single
phase consumes one budget slot.

### Burn-rate triggers

| Trigger | Burn rate | Window | Why |
|---------|-----------|--------|-----|
| Investigate | 5× normal | 1 hour | Slow leak likely; investigate during business hours. |
| Page on-call | 14× normal | 1 hour **OR** 2× normal | 6 hours | Multi-window per Google SRE: fast break or sustained drag. |

(Normal burn rate = 1× budget consumption that exhausts the 30-day
budget exactly at day 30. At 14× normal burn the budget is gone in
~2 days; at 2× sustained, the budget is gone in ~15 days — both
warrant paging.)

---

## 2. `addon_cycle`

### What this measures

Day-to-day addon lifecycle operations: previewing an enable, previewing
a disable, and pushing a global upgrade across the fleet. The dry-run
paths are the dominant interactive surface (operators preview before
they commit); the global upgrade path is lower-frequency but higher-
stakes (it rewrites the catalog and opens a PR).

### Phases

| Phase | What happens |
|-------|-------------|
| `enable_dry_run` | `POST /api/v1/clusters/{c}/addons/{a}` with `dry_run=true`. The per-cluster enable preview round-trip; no git write, no ArgoCD dial. |
| `disable_dry_run` | `DELETE /api/v1/clusters/{c}/addons/{a}` with `dry_run=true`. The per-cluster disable preview round-trip. |
| `upgrade_global` | `POST /api/v1/addons/{a}/upgrade`. Live catalog rewrite + PR open path against the git provider. |

### Targets

| Phase | Baseline p50 / p95 / p99 (ms) | SLO target p50 / p95 / p99 (ms) |
|-------|-------------------------------|---------------------------------|
| `enable_dry_run` | 0.25 / 0.33 / 0.38 | 100 / 300 / 500 |
| `disable_dry_run` | 0.24 / 0.34 / 0.40 | 100 / 300 / 500 |
| `upgrade_global` | 0.30 / 0.41 / 0.64 | 1500 / 3500 / 5000 |

### Sizing rationale

- `enable_dry_run` and `disable_dry_run` baselines are sub-millisecond
  because the in-process harness short-circuits before any external
  call. The 500ms p99 target is the *production* ceiling once an
  HTTP round-trip from the UI is included; relative to baseline it
  looks huge but absolute-wise it's still well under user-perceptible
  for an interactive preview.
- `upgrade_global` is sub-millisecond in the harness because the mock
  git provider is in-memory. The 5000ms p99 target reflects the
  production reality: the path opens a PR against a real Git host
  (GitHub / GitLab / Bitbucket), which is bounded by third-party API
  latency.

### Error budget

**≤1% of addon-cycle operations may exceed any of the per-phase p99
targets in any rolling 30-day window.**

For lower-volume paths like `upgrade_global` (which fires only when
an operator drives a fleet upgrade), the budget is computed against
*all* `addon_cycle` operations in the window, not per-phase — this
prevents the rare upgrade path from being budget-starved when
dry-run traffic is high.

### Burn-rate triggers

| Trigger | Burn rate | Window | Why |
|---------|-----------|--------|-----|
| Investigate | 5× normal | 1 hour | The dry-run paths are interactive — a 5× burn signals UI sluggishness. |
| Page on-call | 14× normal | 1 hour **OR** 2× normal | 6 hours | Sustained `upgrade_global` failures block fleet upgrades; page even if the slow-burn signal is moderate. |

---

## 3. `catalog_scan`

### What this measures

The catalog read surface: loading the embedded curated catalog,
listing all available addons (the dominant marketplace-tab read), and
refreshing third-party catalog sources. The first two are
high-frequency, low-latency interactive paths; the third is an
admin-driven refresh that may legitimately take seconds because it
hits external HTTP sources.

### Phases

| Phase | What happens |
|-------|-------------|
| `catalog_load` | `catalog.Load()` on the embedded curated catalog. Startup-time baseline; flags any future caching regression. |
| `list_addons` | `GET /api/v1/catalog/addons` — the full list-all-curated response that backs the marketplace tab. |
| `sources_refresh` | `POST /api/v1/catalog/sources/refresh` — admin-only refresh of the configured third-party fetchers. |

### Targets

| Phase | Baseline p50 / p95 / p99 (ms) | SLO target p50 / p95 / p99 (ms) |
|-------|-------------------------------|---------------------------------|
| `catalog_load` | 0.96 / 1.31 / 1.52 | 10 / 30 / 50 |
| `list_addons` | 0.60 / 0.79 / 1.09 | 50 / 150 / 250 |
| `sources_refresh` | 0.41 / 0.50 / 0.59 | 2000 / 6000 / 10000 |

### Sizing rationale

- `catalog_load` and `list_addons` are pure-memory dispatches once
  the embedded YAML is parsed. The 50ms / 250ms p99 targets are well
  above the baseline but stay comfortably below the 100ms perceptual
  threshold for `catalog_load` and the 300ms threshold for
  `list_addons`. The headroom allows the catalog to grow several
  orders of magnitude in entry count without breaching the SLO.
- `sources_refresh` has a 10000ms p99 target because the production
  path fans out to third-party HTTP fetchers (GitHub raw URLs, OCI
  registries, etc.). Each fetcher can legitimately take seconds; the
  refresh is admin-driven and not user-blocking, so the higher
  ceiling is acceptable.

### Error budget

**≤1% of catalog-scan operations may exceed any of the per-phase p99
targets in any rolling 30-day window.**

For `sources_refresh`, transient third-party failures (rate-limited
GitHub raw URLs, OCI registry blips) are expected and *should* burn
budget — the operator should know if the fetcher cadence is too
aggressive or if a source is degraded.

### Burn-rate triggers

| Trigger | Burn rate | Window | Why |
|---------|-----------|--------|-----|
| Investigate | 5× normal | 1 hour | The marketplace tab is high-traffic; a sustained slow `list_addons` degrades the primary UI surface. |
| Page on-call | 14× normal | 1 hour **OR** 2× normal | 6 hours | Standard multi-window pattern. |

---

## 4. `dashboard_read`

### What this measures

The aggregated dashboard read surface: active PR listing, fleet
status (clusters × addons join), and the bootstrap repo state. These
are the read paths that determine whether the dashboard "feels fast"
to operators monitoring the fleet.

### Phases

| Phase | What happens |
|-------|-------------|
| `pull_requests` | `GET /api/v1/dashboard/pull-requests` — active-PR list via the git provider. |
| `fleet_status` | `GET /api/v1/observability/fleet-status` — aggregated cluster + addon view; resilient handler reports git + ArgoCD availability as flags. |
| `repo_status` | `GET /api/v1/observability/repo-status` — bootstrap state of the managed repo. |

### Targets

| Phase | Baseline p50 / p95 / p99 (ms) | SLO target p50 / p95 / p99 (ms) |
|-------|-------------------------------|---------------------------------|
| `pull_requests` | 0.14 / 0.22 / 0.37 | 250 / 750 / 1500 |
| `fleet_status` | 0.18 / 0.27 / 0.48 | 400 / 1000 / 2000 |
| `repo_status` | 0.14 / 0.22 / 0.28 | 250 / 750 / 1500 |

### Sizing rationale

- All three phases baseline sub-millisecond because the in-process
  harness boots with `N = M = 0` clusters × addons (the dispatch +
  connection-lookup floor). The targets reflect the production
  reality: `pull_requests` and `repo_status` round-trip to a real
  git provider; `fleet_status` joins git availability + ArgoCD
  availability + cluster slice.
- `fleet_status` gets a slightly higher p99 target (2000ms vs.
  1500ms) because its handler aggregates across more subsystems —
  cardinality scales with `N × M`, and the absolute ceiling
  accommodates moderately-large fleets (~50 clusters × 20 addons)
  without breaching.
- A populated-topology baseline (future story) will tighten these
  numbers; the current targets are sized for the floor + production
  remote-call cost without assumptions about fleet size.

### Error budget

**≤1% of dashboard-read operations may exceed any of the per-phase
p99 targets in any rolling 30-day window.**

### Burn-rate triggers

| Trigger | Burn rate | Window | Why |
|---------|-----------|--------|-----|
| Investigate | 5× normal | 1 hour | Dashboard sluggishness is visible to every logged-in operator. |
| Page on-call | 14× normal | 1 hour **OR** 2× normal | 6 hours | Standard multi-window pattern. |

---

## Re-baselining

The targets above are sized over **developer-workstation baselines**.
Two events will shift the numbers:

1. **V2-1.4 — CI baseline gate.** When CI runs the perf harness on
   shared GitHub Actions hardware, the recorded baselines will be
   higher and noisier than the dev-workstation numbers. The current
   targets carry extra headroom (~3× p99 vs. the more typical 1.5×)
   precisely to absorb this shift without an SLO breach. Re-evaluate
   the headroom multiplier when V2-1.4's CI baselines land.

2. **First 90 days of production telemetry.** Once V2-3 wires up
   Prometheus histograms and Sharko has 90 days of real production
   p50 / p95 / p99 data, the targets get re-tightened to match the
   observed operational envelope. The aim is to converge on
   ~1.5× p99 headroom over real production data, not the conservative
   ~3× headroom over the dev baselines.

The re-baselining cadence after that initial tightening is **annual**
or **on major surface change** (whichever comes first). Mid-cycle
target changes follow the same review path as any SLO change —
sign-off from the maintainer, doc updated in a dedicated PR, no
mixing with feature work.

## Cross-references

- [`docs/site/operator/perf-baselines.md`](perf-baselines.md) — the
  measured baselines these targets are sized over.
- [`docs/site/developer-guide/perf-harness.md`](../developer-guide/perf-harness.md) —
  the locked phase boundaries the harness, baselines, and these
  targets all consume.
- `docs/site/operator/budget-burn-runbook.md` — what to do when a
  burn-rate trigger fires. **(forthcoming, V2-3.4 / V2-4; not yet
  published)**
- V2-3 — Prometheus exposition for these SLI surfaces; the metric
  recording + alerting rules that operationalize the burn-rate
  triggers above.

## Refreshing this doc

The SLO targets are a product decision and a published commitment;
they are NOT refreshed automatically. Changes go through these steps:

1. Open a dedicated PR for the SLO change (NOT mixed with feature
   work).
2. Cite the data motivating the change — baseline refresh, production
   telemetry, surface change.
3. Update the affected per-phase target table + the sizing rationale.
4. Update the "Verified" header date to match the PR's merge date.
5. Cross-link the PR from the v2.x release notes under "Operator-
   visible changes" so adopters see the new commitment.

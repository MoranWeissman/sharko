# Budget-Burn Runbook

> **Verified:** Authored 2026-06-01 against the V2-3.3 alerts shipped in
> `charts/sharko/templates/prometheusrules.yaml` (PR #372). Every alert in
> that file has a 1:1 section below; section anchors match the
> `runbook_url` annotation of each alert. Re-verify before changing alert
> names, expressions, or thresholds — the recording-rule names, alert
> names, and runbook anchors are load-bearing together.

This page is what on-call reads when a Sharko SLO burn-rate alert fires.
Each section maps to one alert and tells you what it means, where to
look, what to try in order, and which root causes are common for that
surface.

## What budget burn means

Each Sharko SLO path has a 99.9% target (error budget = 0.001 — at most
1 in 1000 requests may fail in a rolling 30-day window). A "burn rate"
is the multiple of that budget being consumed right now: 1× exhausts the
budget exactly at day 30; 14.4× exhausts it in ~2 days. The alerts in
V2-3.3 fire when the burn rate exceeds a threshold over **both** a short
window and a long window simultaneously — this is the multi-window
multi-burn-rate pattern from the
[Google SRE workbook](https://sre.google/workbook/alerting-on-slos/),
chosen because it catches sustained degradation without paging on
transient blips.

Two severities ship per path:

- **`severity: page`** — the FastBurn alerts. Wake someone up. Budget
  is being consumed >14.4× the sustainable rate over a 5m + 1h window;
  left alone, the monthly budget is gone within ~2 days. Mitigate now.
- **`severity: ticket`** — the SlowBurn alerts. File a Jira / GitHub
  issue for next business day. Budget is being consumed >6× the
  sustainable rate over a 30m + 6h window — sustained but not
  catastrophic; investigate during normal hours.

The `sharko_path` label routes alerts by surface
(`cluster_registration`, `addon_cycle`, `catalog_scan`,
`dashboard_read`). Use it in your Alertmanager routing tree to point
each surface at the team that owns it.

The metric families and recording rules referenced below are documented
in [`metrics-naming.md`](metrics-naming.md); the SLO targets they
enforce are in [`slos.md`](slos.md); the baseline data that informed
histogram bucket sizing is in [`perf-baselines.md`](perf-baselines.md).

---

## SharkoClusterRegistrationFastBurn

**Severity:** page
**Path:** `cluster_registration`
**Window:** 5m short + 1h long
**Threshold:** error rate > 14.4 × 0.001 (1.44%) on both windows; alert
holds `for: 2m` to debounce momentary spikes.

### What this means

Cluster registration is failing fast enough that if the current rate
continued, the entire monthly 99.9% error budget would be consumed in
~2 days. This is the path platform engineers use to onboard new
clusters — sustained failure here directly blocks fleet expansion.
Page on-call.

### Where to look

1. **Current short-window burn rate (Prometheus):**

    ```promql
    sum(rate(sharko_cluster_registration_errors_total[5m]))
    /
    clamp_min(sum(rate(sharko_cluster_registration_total[5m])), 1e-9)
    ```

    The pre-computed recording rule
    `sharko:cluster_registration:error_rate_5m` carries the numerator
    alone; pair it with `sharko:cluster_registration:request_rate_5m`
    for the ratio. The success-rate complement is
    `sharko:cluster_registration:success_rate_5m`.

2. **Recent failures in logs.** Filter by path and ERROR level. If
   Grafana exemplars are wired (Prometheus 2.43+ / Grafana 9.4+), click
   any exemplar dot on
   `sharko_cluster_registration_duration_seconds` and Grafana surfaces
   the `request_id` for that observation:

    ```sh
    kubectl logs -n <ns> -l app=sharko --tail=500 \
      | jq 'select(.level == "ERROR" and (.msg | tostring | test("register|cluster"; "i")))'
    ```

    For a Loki-equipped stack:

    ```logql
    {app="sharko"} | json | level="ERROR" | request_id="req-<id>"
    ```

    The `request_id` correlation pattern is documented in the
    [logging guide](../developer-guide/logging.md#correlation-ids).

3. **Sharko health.** `curl <sharko>/api/v1/health` — surfaces ArgoCD
   reachability flags and the bootstrap-repo state without leaking
   credentials.

### Mitigations (try in order)

1. **ArgoCD reachability** — `kubectl -n argocd get pods` and
   `kubectl -n argocd logs deploy/argocd-server --tail=200`. The
   `argocd_secret_created` phase writes a Secret via the kube API and
   then relies on `argocd-application-controller` to surface it; if
   either is unhealthy, registration fails downstream of Sharko.
2. **kube-apiserver health** — `kubectl get --raw /healthz` and
   `kubectl get --raw /readyz`. Cluster registration writes a Secret in
   the `argocd` namespace; a degraded apiserver shows up here first.
3. **Cluster reconciler** — `kubectl logs -n <ns> -l app=sharko
   --tail=500 | jq 'select(.request_id | startswith("recon-"))'`. If
   the post-merge `Trigger()` is firing but converging slowly, the
   30-second drift safety-net is bridging the gap (see
   [`cluster-reconciler.md`](cluster-reconciler.md)).
4. **Admission webhook backpressure** — cert-manager / external-secrets
   / OPA webhooks can block Secret creation. Check
   `kubectl get apiservice` for any `Available=False` rows.
5. **Network policy / firewall** — Sharko → ArgoCD and Sharko →
   kube-apiserver paths must be allowed. A recently-applied
   NetworkPolicy is a common trigger.
6. **Last resort: scale down and drain** — if a retry storm is
   amplifying a transient ArgoCD issue, scale Sharko to 1 replica until
   ArgoCD recovers. Per the
   [logging guide](../developer-guide/logging.md), retry traffic should
   be visible as `Warn` lines with `retry triggered`.

### Root-cause patterns

- **ArgoCD outage** (most common) — Sharko returns 2xx on
  `POST /api/v1/clusters` but the `argocd_application_reachable` phase
  never completes, so the error counter only increments after the
  registration is observed as never converging. Look for ArgoCD
  Application controller crash-looping.
- **Stale service-account token** — Sharko's ArgoCD account token
  expired. Logs show 401/403 from ArgoCD with `token expired`. The
  Helm-installed default token has no expiry; this surfaces only after
  manual rotation.
- **Webhook backpressure** — cert-manager / external-secrets webhooks
  block Secret creation; failure surfaces as a kube API error inside
  the `argocd_secret_created` phase.
- **First-register bootstrap stall** — V2-1.2 baselines documented a
  one-time 2-second bootstrap cost on iteration 0. If this alert
  triggered immediately after a Sharko install with no prior cluster,
  the bootstrap may be racing with the very first registration.
  Re-trying typically clears it. Decision rationale lives in
  [`slos.md` first-register bootstrap decision](slos.md).
- **Per-phase wiring follow-up (V2-3.x)** — PR 1 wired end-to-end only
  for `cluster_registration`. Once per-phase wiring lands (`ui_submit`,
  `argocd_secret_created`, `argocd_application_reachable`), the `phase`
  label on the histogram pinpoints which phase is failing without
  reading logs.

---

## SharkoClusterRegistrationSlowBurn

**Severity:** ticket
**Path:** `cluster_registration`
**Window:** 30m short + 6h long
**Threshold:** error rate > 6 × 0.001 (0.6%) on both windows; alert
holds `for: 15m` to filter sustained-only conditions.

### What this means

Cluster registration is failing at a sustained but not catastrophic
rate — fast enough to exhaust the monthly budget in ~5 days if left
unchecked, but slow enough that the working population can usually
retry their way through. File a ticket and investigate during business
hours.

### Where to look

1. **Sustained error rate (Prometheus):**

    ```promql
    sum(rate(sharko_cluster_registration_errors_total[6h]))
    /
    clamp_min(sum(rate(sharko_cluster_registration_total[6h])), 1e-9)
    ```

2. **Compare against the SLO target** — `sharko:cluster_registration:
   latency_p99_5m` over the same 6h window. If p99 is also breaching
   the target documented in [`slos.md`](slos.md), the burn is
   latency-driven (timeouts) not error-driven. If p99 is in range, the
   burn is genuine failed registrations.
3. **Recurring `request_id`s in logs.** Slow burns often have a
   "stuck" pattern — the same failure repeats on a cadence:

    ```sh
    kubectl logs -n <ns> -l app=sharko --since=6h \
      | jq 'select(.level == "ERROR" and (.msg | tostring | test("register|cluster"; "i"))) | {ts: .time, msg: .msg, error: .error}' \
      | sort | uniq -c | sort -rn | head
    ```

### Mitigations (try in order)

1. **Identify the failing population** — kubectl logs filter on
   ERROR-level register lines, group by `error` field. If only one
   target cluster is failing, the issue is environmental (that
   cluster's kubeconfig, network reachability, or credential rotation).
2. **Re-check ArgoCD account token** — most slow-burn registration
   failures trace to expired or revoked ArgoCD credentials. Same fix
   as the fast-burn path.
3. **Check git provider quota** — if the registration PR opening is
   rate-limited by GitHub / GitLab, the failure manifests as a
   registration error but the root cause is upstream. Look for
   `rate limit hit` Warn lines.
4. **Inspect the cluster reconciler tick** — slow `recon-<ts>` lines
   in the log point to convergence cost growing (large
   `managed-clusters.yaml`, expensive `ArgoCD list`). Track these in
   the Sharko issue tracker.

### Root-cause patterns

- **Credential rotation in flight** — admin rotated the ArgoCD token
  but Helm hasn't been upgraded yet. Logs show a mix of 200s and 401s.
- **One cluster permanently failing** — a single cluster has bad
  credentials; every retry against it bumps the error counter. Remove
  it from `managed-clusters.yaml` while the credential is resolved.
- **Git provider rate limit creeping in** — automation traffic
  combined with operator traffic has crossed the third-party API
  ceiling; tune the PR-open cadence or upgrade the API plan.

---

## SharkoAddonCycleFastBurn

**Severity:** page
**Path:** `addon_cycle`
**Window:** 5m short + 1h long
**Threshold:** error rate > 14.4 × 0.001 (1.44%) on both windows; alert
holds `for: 2m`.

### What this means

Addon enable / disable / upgrade is failing fast. This is the
day-to-day operational surface for fleet maintainers — sustained
failure here blocks upgrades, new addon rollouts, and disable flows.
Page on-call.

### Where to look

1. **Current burn rate (recording rules):**

    ```promql
    sharko:addon_cycle:error_rate_5m
    /
    clamp_min(sharko:addon_cycle:request_rate_5m, 1e-9)
    ```

2. **Per-action breakdown** — the histogram carries a `phase` label
   with values `enable`, `disable`, and `upgrade_global` (per
   [`metrics-naming.md`](metrics-naming.md#addon_cycle)). Pivot by phase
   in Grafana to see whether a single action is failing or the whole
   surface.
3. **PR tracker state** — `kubectl logs -n <ns> -l app=sharko --tail=500
   | jq 'select(.request_id | startswith("prtrack-"))'`. Each cycle
   opens a PR; PR-tracker poll failures correlate with addon-cycle
   errors.
4. **ArgoCD sync status** — addon installs land via ArgoCD. A
   `Degraded` Application after the PR merges shows up as an
   addon_cycle error because the cycle is end-to-end (PR → merge →
   reconcile → ArgoCD sync). `kubectl -n argocd get applications -o
   json | jq '.items[] | select(.status.health.status != "Healthy")'`.

### Mitigations (try in order)

1. **Git provider auth & rate limits** — most addon-cycle errors trace
   to PR-open failures. Test the configured PAT against the provider
   directly:

    ```sh
    curl -H "Authorization: token <PAT>" https://api.github.com/rate_limit
    ```

    Watch for `remaining: 0`. Per
    [the logging guide](../developer-guide/logging.md), rate-limit hits
    log as `Warn` lines.
2. **ArgoCD reconciler queue depth** — `kubectl -n argocd top pod` and
   `argocd_app_reconcile_seconds_bucket` (ArgoCD's own metric). A
   saturated reconciler delays addon convergence past the
   `addon_cycle` SLO p99 ceiling.
3. **Catalog signing verification** — if a recently-rotated catalog
   signing key isn't trusted yet, every enable / upgrade for signed
   addons fails verification. Look for `verification failed` Error
   lines.
4. **PR auto-merge automation** — if the merge bot is down or rate
   limited, PRs open but never merge; the cycle never closes and the
   error increments after timeout. Check Mergify / auto-merge config.
5. **Reconciler convergence** — `kubectl logs -n <ns> -l app=sharko
   --tail=500 | jq 'select(.request_id | startswith("recon-"))'`
   followed by `select(.level == "ERROR" or .level == "WARN")` to
   surface convergence failures.

### Root-cause patterns

- **Git PAT exhausted** (most common) — rate limit on the configured
  Git provider PAT trips the cycle. Rotate to a less-loaded token or
  back off the cycle cadence.
- **ArgoCD Application stuck Degraded** — the PR merged, Sharko sees
  the merge, but the downstream ArgoCD sync never reaches Healthy. The
  addon_cycle counter logs an error because the cycle didn't close.
  Triage the Application directly in ArgoCD UI.
- **Catalog signing key rotated without trust update** — recent
  V125-1-7 / catalog-signing surface; rotation requires updating the
  trust policy in lockstep. See
  [`catalog-trust-policy.md`](catalog-trust-policy.md).
- **Per-phase wiring follow-up** — once per-phase wiring lands
  (`pr_open`, `pr_merged`, `reconciler_converged`, `argo_sync`), this
  section will point at the specific phase that's failing. Today the
  alert only knows "the cycle failed."

---

## SharkoAddonCycleSlowBurn

**Severity:** ticket
**Path:** `addon_cycle`
**Window:** 30m short + 6h long
**Threshold:** error rate > 6 × 0.001 (0.6%) on both windows; alert
holds `for: 15m`.

### What this means

Addon cycle is failing at a sustained moderate rate. Operators can
usually retry their way through, but the budget is draining. File a
ticket for next-business-day triage.

### Where to look

1. **6h error trend (Prometheus):**

    ```promql
    sum(rate(sharko_addon_cycle_errors_total[6h]))
    /
    clamp_min(sum(rate(sharko_addon_cycle_total[6h])), 1e-9)
    ```

2. **Compare error pattern to PR tracker** — if Git-provider errors
   dominate, the slow burn is upstream-driven; if ArgoCD-sync timeouts
   dominate, the slow burn is downstream-driven.
3. **Recurring failures by addon name** — group logged errors by addon
   to find a single misbehaving entry:

    ```sh
    kubectl logs -n <ns> -l app=sharko --since=6h \
      | jq -r 'select(.level == "ERROR" and .addon) | .addon' \
      | sort | uniq -c | sort -rn | head
    ```

### Mitigations (try in order)

1. **Identify the failing addon** — slow burns often trace to a single
   addon whose values file has drifted from its chart's schema. Disable
   it via `sharko remove-addon --confirm` and re-enable cleanly.
2. **Check the PR queue length** — `GET /api/v1/prs` (or the dashboard
   PR pane). A growing queue means PRs are opening faster than they
   merge; the merge bot or the auto-merge rules are the bottleneck.
3. **ArgoCD Application controller throughput** — if ArgoCD is
   spending its reconcile budget on a degraded application, every
   cycle queues behind it.

### Root-cause patterns

- **One addon misbehaving** — a single addon's chart has a bug; its
  cycle keeps failing while others succeed. Visible as a top
  contributor in the per-addon error breakdown.
- **PR queue depth growing** — auto-merge throughput has dropped
  beneath the open-PR cadence; the cycle's wall-clock duration grows
  until it times out.
- **Reconciler tick saturation** — the 30-second drift safety-net is
  consistently arriving before the post-merge `Trigger()` because the
  trigger is failing for another reason. Logs show
  `reconcile via tick` instead of `reconcile via trigger`.

---

## SharkoCatalogScanFastBurn

**Severity:** page
**Path:** `catalog_scan`
**Window:** 5m short + 1h long
**Threshold:** error rate > 14.4 × 0.001 (1.44%) on both windows; alert
holds `for: 2m`.

### What this means

Catalog scan is failing fast. Catalog reads back the marketplace tab
(`list_addons`), the bootstrap startup (`catalog_load`), and the
third-party source refresh (`sources_refresh`). Sustained failure here
breaks addon discovery for every operator. Page on-call.

### Where to look

1. **Burn rate by phase (recording rules + `phase` label):**

    ```promql
    sum by (phase) (rate(sharko_catalog_scan_errors_total[5m]))
    /
    clamp_min(sum by (phase) (rate(sharko_catalog_scan_total[5m])), 1e-9)
    ```

    Phases: `total` today; V2-3.x will split into `catalog_load`,
    `list_addons`, and `sources_refresh` per
    [`metrics-naming.md`](metrics-naming.md#catalog_scan).

2. **Catalog scanner runs.** Sharko ships a daily catalog scanner
   detailed in
   [`../developer-guide/catalog-scan-runbook.md`](../developer-guide/catalog-scan-runbook.md);
   surface recent failures with:

    ```sh
    kubectl logs -n <ns> -l app=sharko --since=1h \
      | jq 'select(.level == "ERROR" and (.msg | tostring | test("catalog"; "i")))'
    ```

3. **Trust policy state** — failures on catalog entries with verified
   signatures often trace to a trust-policy regex change. See
   [`catalog-trust-policy.md`](catalog-trust-policy.md).

### Mitigations (try in order)

1. **Test the embedded catalog directly** — `curl -s
   <sharko>/api/v1/catalog/addons | jq 'length'`. If this returns a
   non-zero count, the embedded path is fine and the failure is in
   third-party sources.
2. **Check configured catalog sources** — `GET /api/v1/catalog/sources`
   surfaces each source's last-fetch status. A 404 / 401 on a
   third-party source spikes errors fast.
3. **Sigstore bundle fetch** — verified-signature catalog entries
   fetch a Sigstore bundle on demand. A bundle fetch failure (cosign
   public-good endpoint degraded) blocks the entry's verify step.
   `curl -sI https://rekor.sigstore.dev/api/v1/log` checks reachability.
4. **Trust policy regex evaluation** — a recent trust-policy update
   that tightened the regex too aggressively rejects previously-valid
   identities. Look for `signature identity does not match policy`
   Error lines.
5. **Source fetcher cadence** — if the cadence is too aggressive
   against a rate-limited HTTP source, every refresh fails and the
   error rate dominates a low total-volume path. Slow the fetcher in
   Helm values.

### Root-cause patterns

- **Third-party source down** — the most common fast-burn cause; one
  configured source (GitHub raw URL, OCI registry, ArtifactHub) is
  returning 5xx and every refresh logs an error.
- **Sigstore upstream degraded** — public-good Sigstore endpoints
  occasionally degrade; signature verification fails until they
  recover. Subscribe to <https://status.sigstore.dev/>.
- **Trust policy too strict** — a recent
  [`catalog-trust-policy.md`](catalog-trust-policy.md) update
  excludes legitimate workflow_run SANs. Log lines surface the
  identity that failed.
- **Catalog parse failure on startup** — if `catalog_load` fails, no
  addons surface at all. This usually traces to a malformed
  third-party catalog YAML that broke the schema.

---

## SharkoCatalogScanSlowBurn

**Severity:** ticket
**Path:** `catalog_scan`
**Window:** 30m short + 6h long
**Threshold:** error rate > 6 × 0.001 (0.6%) on both windows; alert
holds `for: 15m`.

### What this means

Catalog scan is failing at a sustained moderate rate — visible as
occasional gaps in the marketplace tab or rejected verifications. File
a ticket; not user-blocking enough to page.

### Where to look

1. **Sustained error rate over 6h:**

    ```promql
    sum(rate(sharko_catalog_scan_errors_total[6h]))
    /
    clamp_min(sum(rate(sharko_catalog_scan_total[6h])), 1e-9)
    ```

2. **Catalog source health table** — `GET /api/v1/catalog/sources`
   surfaces each source's recent fetch status; sustained slow burn
   often correlates with a single source flaking on a cadence.

### Mitigations (try in order)

1. **Identify the failing source** — group log errors by source name:

    ```sh
    kubectl logs -n <ns> -l app=sharko --since=6h \
      | jq -r 'select(.level == "ERROR" and .source) | .source' \
      | sort | uniq -c | sort -rn
    ```

2. **Disable the failing source temporarily** — remove it from
   `catalog.sources` in Helm values until it recovers; document in the
   ticket so the team remembers to re-enable.
3. **Investigate signing cadence** — Sigstore bundle fetches that
   occasionally fail (DNS flake, transient 5xx) drive a steady-state
   error rate. The
   [`supply-chain.md`](supply-chain.md) runbook covers the keyless
   verification model.

### Root-cause patterns

- **Flaky third-party source** — one source returns 5xx ~5% of the
  time; aggregated, it sits above the slow-burn threshold without
  ever fully tripping fast-burn.
- **Sigstore intermittent failures** — Rekor / Fulcio occasionally
  flake; OCI fetches succeed but the verify step fails sometimes.
- **OCI registry rate limit** — verified-signature catalogs sourced
  from a rate-limited registry burn slowly as the cadence interacts
  with the quota window.

---

## SharkoDashboardReadFastBurn

**Severity:** page
**Path:** `dashboard_read`
**Window:** 5m short + 1h long
**Threshold:** error rate > 14.4 × 0.001 (1.44%) on both windows; alert
holds `for: 2m`.

### What this means

Dashboard reads are failing fast. The dashboard is the primary
operator UI; sustained failure here means every logged-in operator
sees an error state when they open the app. Page on-call.

### Where to look

1. **Burn rate by phase:** the histogram's `phase` label distinguishes
   `fleet_status` (`/api/v1/dashboard/stats`), `attention`
   (`/api/v1/dashboard/attention`), and `pull_requests`
   (`/api/v1/dashboard/pull-requests`) per
   [`metrics-naming.md`](metrics-naming.md#dashboard_read).

    ```promql
    sum by (phase) (rate(sharko_dashboard_read_errors_total[5m]))
    /
    clamp_min(sum by (phase) (rate(sharko_dashboard_read_total[5m])), 1e-9)
    ```

2. **Sharko health endpoint** — `curl <sharko>/api/v1/health`. The
   dashboard handlers degrade gracefully when ArgoCD or Git is
   unreachable (they report availability flags rather than failing);
   if errors are real 5xx, something inside the handlers is broken,
   not the upstream.
3. **Per-handler logs** — handler names match the phase IDs:

    ```sh
    kubectl logs -n <ns> -l app=sharko --tail=500 \
      | jq 'select(.level == "ERROR" and (.msg | tostring | test("dashboard|fleet|pull_requests|attention"; "i")))'
    ```

### Mitigations (try in order)

1. **ArgoCD list API health** — `fleet_status` joins
   `argocd app list`. If ArgoCD's Application controller is degraded,
   the list call times out. `kubectl -n argocd top pod` shows
   resource pressure; `kubectl -n argocd logs deploy/argocd-server`
   surfaces the actual error.
2. **PR tracker** — the `pull_requests` phase reads from the in-memory
   PR tracker populated by the
   [logging guide's prtrack pattern](../developer-guide/logging.md#correlation-ids).
   If the tracker is stuck (last poll >5m old), restart Sharko.
3. **Catalog cache** — `fleet_status` joins addon health which
   depends on the catalog. If `catalog_scan` is also alerting, treat
   that as the upstream root cause.
4. **Replica scaling** — the dashboard handlers are read-only and
   cacheable; if the read fanout is saturating a single pod, bump
   replicas via Helm `replicaCount`.
5. **Worst case: serve degraded** — the handlers already report
   availability flags rather than 5xx for upstream failures; if a
   sustained 5xx is leaking through, that's a Sharko bug — open an
   issue with the request body and the `request_id` from logs.

### Root-cause patterns

- **ArgoCD list timeout** (most common) — Application controller
  back-pressured under load; ArgoCD list calls exceed handler timeout.
- **PR tracker stale** — the background tracker hasn't polled
  recently; the dashboard reads stale or empty data. Look for absent
  `prtrack-<ts>` lines in the log.
- **Cache warm-up race** — under cold-start, the first dashboard read
  after a Sharko restart races the catalog load. The handler retries
  internally, but a flood of concurrent reads at start can overwhelm
  the warm-up; this self-corrects within ~30 seconds.
- **Upstream Git outage** — `pull_requests` handler depends on the
  git provider for the live PR list. A git provider outage surfaces
  here before it surfaces anywhere else operators see.

---

## SharkoDashboardReadSlowBurn

**Severity:** ticket
**Path:** `dashboard_read`
**Window:** 30m short + 6h long
**Threshold:** error rate > 6 × 0.001 (0.6%) on both windows; alert
holds `for: 15m`.

### What this means

Dashboard reads are failing at a sustained moderate rate — the
dashboard "feels slow" but is rendering. File a ticket; investigate
during business hours.

### Where to look

1. **Sustained burn rate over 6h:**

    ```promql
    sum(rate(sharko_dashboard_read_errors_total[6h]))
    /
    clamp_min(sum(rate(sharko_dashboard_read_total[6h])), 1e-9)
    ```

2. **Latency tail vs error rate** — `sharko:dashboard_read:
   latency_p99_5m` against the SLO target. A slow burn that's
   latency-driven means handler timeouts; an error-driven burn means
   real 5xx leaking through.
3. **Per-phase split** — even if per-phase wiring is end-to-end only,
   the inventory in [`metrics-naming.md`](metrics-naming.md#dashboard_read)
   identifies which handlers map to which phase. Cross-check those
   handler logs.

### Mitigations (try in order)

1. **Identify the failing phase** — query the histogram with `phase`
   label split. The slowest-tail phase is usually the cause.
2. **Inspect the upstream that feeds the slow phase** — ArgoCD for
   `fleet_status`, git provider for `pull_requests`, internal cache
   for `attention`. Slow burns here usually trace to upstream latency
   that's not bad enough to fast-burn.
3. **Tune handler timeouts** — if upstream is genuinely slow but
   recoverable, the handler timeout (rather than upstream itself) is
   converting latency into errors. Adjust `config.dashboard.timeout`
   in Helm values.
4. **Replicas + cache** — read-heavy dashboard load on a single
   Sharko replica with cold caches can drag p99 into error territory.
   Scaling and warming the cache via the
   [personal-smoke runbook](../developer-guide/personal-smoke-runbook.md)
   pattern resolves both.

### Root-cause patterns

- **ArgoCD Application controller under steady load** — fleet
  growth has crossed a threshold where the controller's reconcile
  budget is consistently late; dashboard reads observe occasional
  timeouts.
- **Git provider quota pressure** — `pull_requests` polls a
  rate-limited PAT; near the quota window edge, occasional 403s
  surface as dashboard read errors.
- **Caching layer eviction** — if a populated topology pushes the
  in-memory cache past its sizing, eviction-induced cold reads slow
  the tail. Tracks as a sizing issue, not a bug.
- **First-time bootstrap interactions** — fresh installs combined
  with first-cluster registration occasionally collide; the SLO
  target in [`slos.md`](slos.md) is sized to absorb these but a slow
  burn can appear during onboarding peaks.

---

## References

- [`metrics-naming.md`](metrics-naming.md) — the metric inventory,
  exposed labels, and the OTEL-aligned naming scheme every recording
  rule and alert depends on.
- [`perf-baselines.md`](perf-baselines.md) — the V2-1.2 baselines
  that informed both histogram bucket sizing and the SLO target
  headroom multipliers.
- [`slos.md`](slos.md) — the per-path 99.9% target, the error-budget
  framing, and the per-phase p50/p95/p99 ceilings these alerts
  enforce.
- [`../developer-guide/logging.md`](../developer-guide/logging.md) —
  the `request_id` correlation pattern that joins log lines to the
  metric exemplars referenced in every "Where to look" subsection.
- [`cluster-reconciler.md`](cluster-reconciler.md) — operator
  reference for the post-merge trigger + 30s safety-net pattern that
  the `cluster_registration` alerts cover.
- [Google SRE workbook — Alerting on SLOs](https://sre.google/workbook/alerting-on-slos/)
  — the multi-window multi-burn-rate pattern these alerts implement.

> **Note:** If a future V2-4 runbook style guide lands, this runbook
> may be restructured to match — tracked as
> `v2-3-4-runbook-alignment-followup`. The 1:1 alert-to-section
> mapping must be preserved across any restructure so the alert
> `runbook_url` anchors keep resolving.

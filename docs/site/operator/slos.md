# SLOs — the goals Sharko is measured against

> **Checked:** 2026-10-10, against the Helm chart and the code of v4.0.5.
> The numbers on this page are read from
> `charts/sharko/templates/prometheusrules.yaml`. They were not tested on a
> live cluster for this page.

This page says which parts of Sharko are measured, what goal each part
has, and which alerts the Helm chart can install to tell you when a goal
is in danger. What to do when one of those alerts fires is on the
[Budget-Burn Runbook](budget-burn-runbook.md).

## What the words mean

- **SLO** (service level objective) — a goal for how well one part of
  Sharko should work. Here the goal is always the share of requests that
  succeed.
- **Error budget** — the small share of requests that is allowed to fail.
  The goal is 99.9%, so the budget is 0.1%: 1 failed request in every
  1,000, counted over 30 days.
- **Burn rate** — how fast you are using up that budget. A burn rate of 1
  uses up the whole budget in exactly 30 days. A burn rate of 14.4 uses it
  up in about 2 days. A burn rate of 6 uses it up in about 5 days.
- **p50, p95, p99** — speed numbers. p95 = 2 seconds means 95 out of 100
  requests finished in 2 seconds or less. p50 is the middle request, p99
  is the slow end.

## The four parts of Sharko that are measured

Sharko counts and times four kinds of requests. Each kind has a short name.
That name shows up in the metric names, in the alert names and in the
`sharko_path` label on the alerts.

| Short name | What is counted | API route | `phase` label |
|---|---|---|---|
| `cluster_registration` | Registering a cluster | `POST /api/v1/clusters` | `total` |
| `addon_cycle` | Turning an addon on or off for one cluster | `POST /api/v1/clusters/{name}/addons/{addon}` and `DELETE /api/v1/clusters/{name}/addons/{addon}` | `enable`, `disable` |
| `catalog_scan` | Reading the addon catalog | `GET /api/v1/addons/catalog` | `total` |
| `dashboard_read` | Loading the dashboard | `GET /api/v1/dashboard/stats`, `GET /api/v1/dashboard/attention`, `GET /api/v1/dashboard/pull-requests` | `fleet_status`, `attention`, `pull_requests` (in that order) |

For each one Sharko exports three metrics. The
[Metrics](metrics.md#slo-surface-metrics) page lists them.

**What counts as a failed request.** Any reply with an HTTP status of 400
or higher. That includes replies that say the request itself was wrong,
such as a 400 or a 409. So a script that keeps sending the same bad
request can use up the budget even when Sharko is working fine. When an
alert fires, look at the `code` label first: a pile of 4xx codes points
at the caller, 5xx codes point at Sharko or something it talks to.

**Two gaps to know about in v4.0.5:**

- On a v4 repository, the UI turns addons on and off through
  `POST /api/v1/v4/clusters/{name}/addons/{addon}` and
  `DELETE /api/v1/v4/clusters/{name}/addons/{addon}`. Those two routes are
  **not counted**. So `addon_cycle` shows little or no traffic. And the
  older routes in the table above now refuse a v4 repository with a 409,
  so anything that still calls them adds a failure each time.
- A refused permission (403) on registering a cluster or turning an addon
  on or off is not counted. Sharko checks the permission before it starts
  counting.

## The goal and the alerts

**The goal for all four parts is the same: 99.9% of requests succeed.**
So the error budget is 0.1%.

The Helm chart can install this goal as a `PrometheusRule`. It is off by
default, because the `PrometheusRule` kind only exists on clusters that
run prometheus-operator. To turn it on:

```yaml
monitoring:
  prometheusRules:
    enabled: true
    # Optional. Empty means the namespace Sharko is installed in.
    namespace: ""
    # Optional. Add the labels your Prometheus uses to pick up rules.
    labels: {}
```

Each of the four parts then gets two alerts:

| Alert | Fires when | Held for | `severity` label |
|---|---|---|---|
| Fast burn | More than 1.44% of requests failed (burn rate 14.4) in **both** the last 5 minutes **and** the last hour | 2 minutes | `page` |
| Slow burn | More than 0.6% of requests failed (burn rate 6) in **both** the last 30 minutes **and** the last 6 hours | 15 minutes | `ticket` |

The alert names:

| Part | Fast burn | Slow burn |
|---|---|---|
| `cluster_registration` | `SharkoClusterRegistrationFastBurn` | `SharkoClusterRegistrationSlowBurn` |
| `addon_cycle` | `SharkoAddonCycleFastBurn` | `SharkoAddonCycleSlowBurn` |
| `catalog_scan` | `SharkoCatalogScanFastBurn` | `SharkoCatalogScanSlowBurn` |
| `dashboard_read` | `SharkoDashboardReadFastBurn` | `SharkoDashboardReadSlowBurn` |

Every one of them also carries a `sharko_path` label (the short name), a
`burn_rate` label (`fast` or `slow`), and a link to the matching section
of the [Budget-Burn Runbook](budget-burn-runbook.md).

**Why each alert looks at two time windows.** If only the short window
counted, one short bad minute could wake someone up. If only the long
window counted, the alert would keep firing for hours after the problem
was fixed. Asking for both means the problem is real *and* still going on.

**What the alerts do not do:**

- They only look at failed requests. **No alert fires because a request
  was slow.**
- The chart has no setting to change the 99.9% goal. The number `0.001`
  is written into the rule file itself.

The chart installs two more alerts that have nothing to do with these
goals: `SharkoReconcilerPassAge` and `SharkoManagedSecretsSustainedBadState`.
They are explained on the [Metrics](metrics.md#reconciler-health-alerts)
page.

## The ready-made numbers the chart records

With the rules turned on, Prometheus also works out five numbers for each
part every 30 seconds, over the last 5 minutes. They are handy for graphs.
Replace `<part>` with one of the four short names:

| Recorded name | What it is |
|---|---|
| `sharko:<part>:request_rate_5m` | Requests per second |
| `sharko:<part>:error_rate_5m` | Failed requests per second |
| `sharko:<part>:success_rate_5m` | Share of requests that succeeded (1 = all of them) |
| `sharko:<part>:latency_p95_5m` | p95 speed, in seconds, one line per `phase` |
| `sharko:<part>:latency_p99_5m` | p99 speed, in seconds, one line per `phase` |

These names are made by Prometheus from the rules. Sharko itself does not
export them.

**Read the speed lines with care.** A speed metric sorts each request into
a set of fixed boxes ("under 10 ms", "under 25 ms" and so on). A request
slower than the biggest box cannot be placed exactly. The biggest box is:

| Part | Biggest box |
|---|---|
| `cluster_registration` | 5 seconds |
| `addon_cycle` | 10 seconds |
| `catalog_scan` | 50 milliseconds |
| `dashboard_read` | 50 milliseconds |

Reading the catalog or loading the dashboard usually means a call to your
Git host, and that often takes longer than 50 milliseconds. When that
happens, the p95 and p99 lines for those two parts sit flat at `0.05`.
That means "slower than 50 milliseconds". It does not mean "exactly 50
milliseconds".

## Where the numbers came from

The 99.9% goal and the burn rates 14.4 and 6 follow the usual pattern from
Google's SRE book. Sharko has no long record of real use yet, so they are
a starting point, not a measured promise.

Sharko's own test setup also measures speed, step by step, on a single
developer machine. Those numbers are on [Perf Baselines](perf-baselines.md).
They are useful for spotting a change that made Sharko slower. They are not
speed goals for your install, and the steps they name are not the `phase`
values a running Sharko writes.

## See also

- [Metrics](metrics.md) — every metric Sharko exports, and what the other
  two alerts mean
- [Budget-Burn Runbook](budget-burn-runbook.md) — what to do when one of
  the alerts on this page fires
- [Metrics Naming](metrics-naming.md) — how the metric names are built
- [Perf Baselines](perf-baselines.md) — speed numbers from Sharko's own
  test setup

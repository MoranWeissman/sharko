# ArgoCD Dashboard Mockups

**Related:** [APPLICATION_REQUIREMENTS.md](../APPLICATION_REQUIREMENTS.md) Section 5

---

## Template Variables (All Dashboards)

| Variable | Source | Default |
|---|---|---|
| `$cluster` | `argocd.app_controller.app.info` tag `dest_server` | `*` |
| `$addon` | `argocd.app_controller.app.info` tag `name` | `*` |
| `$project` | `argocd.app_controller.app.info` tag `project` | `*` |
| `$sync_status` | `synced`, `outofsync`, `unknown` | `*` |
| `$health_status` | `healthy`, `degraded`, `progressing`, `suspended`, `missing`, `unknown` | `*` |

---

## Dashboard 1: Application Operations Overview

**Purpose:** Single-pane fleet health. First stop for on-call.
**Refresh:** 30 seconds

```
+------------------------------------------------------------------+
|  APPLICATION OPERATIONS OVERVIEW          [cluster] [addon] [proj]|
+------------------------------------------------------------------+
|                                                                    |
|  +--------+ +--------+ +--------+ +--------+ +--------+           |
|  | TOTAL  | | SYNCED | | OUT OF | | HEALTHY| |DEGRADED|           |
|  |  156   | |  149   | | SYNC 7 | |  152   | |   4    |           |
|  +--------+ +--------+ +--------+ +--------+ +--------+           |
|                                                                    |
+------------------------------+-------------------------------------+
|  SYNC STATUS DISTRIBUTION    |  HEALTH STATUS DISTRIBUTION         |
|                              |                                     |
|    [PIE CHART]               |    [PIE CHART]                      |
|    Synced: 95.5%             |    Healthy: 97.4%                   |
|    OutOfSync: 4.5%           |    Progressing: 1.3%                |
|                              |    Degraded: 1.3%                   |
+------------------------------+-------------------------------------+
|  CLUSTER CONNECTION STATUS                                         |
|                                                                    |
|  [CHECK STATUS GRID]                                               |
|  cluster-1: OK  cluster-2: OK  cluster-3: OK  cluster-4: WARN    |
|  cluster-5: OK  cluster-6: OK  cluster-7: OK  cluster-8: OK      |
|  ...                                                               |
+--------------------------------------------------------------------+
|  SYNC ACTIVITY (last 24h)                                          |
|                                                                    |
|  [TIMESERIES - STACKED BAR]                                        |
|  Green = Succeeded | Red = Failed | Orange = Error                 |
|  ████████████████████████████████████████████████████               |
|  ████████████████████████████████████████████████████               |
|                                                                    |
+--------------------------------------------------------------------+
|  ACTIVE ALERTS                                                     |
|                                                                    |
|  [ALERT STATUS WIDGET]                                             |
|  App Out-of-Sync: 2 triggered | Degraded Apps: 1 triggered        |
+--------------------------------------------------------------------+
```

### Panel Specifications

| Panel | Type | Query |
|---|---|---|
| Total Apps | Query Value | `count:argocd.app_controller.app.info{$cluster,$addon}` |
| Synced | Query Value | `count:argocd.app_controller.app.info{sync_status:synced,$cluster,$addon}` |
| Out of Sync | Query Value (red if >0) | `count:argocd.app_controller.app.info{sync_status:outofsync,$cluster,$addon}` |
| Healthy | Query Value | `count:argocd.app_controller.app.info{health_status:healthy,$cluster,$addon}` |
| Degraded | Query Value (red if >0) | `count:argocd.app_controller.app.info{health_status:degraded,$cluster,$addon}` |
| Sync Status Pie | Pie Chart | `argocd.app_controller.app.info` grouped by `sync_status` |
| Health Status Pie | Pie Chart | `argocd.app_controller.app.info` grouped by `health_status` |
| Cluster Connection | Check Status | `argocd.app_controller.cluster.connection_status` by `name` |
| Sync Activity | Timeseries (stacked) | `sum:argocd.app_controller.app.sync.count{$cluster,$addon}.as_count()` by `phase` |
| Active Alerts | Alert Value | Preconfigured monitor IDs |

---

## Dashboard 2: Sync Operations Performance

**Purpose:** Track sync performance trends. Identify slowdowns and capacity issues.
**Refresh:** 60 seconds

```
+------------------------------------------------------------------+
|  SYNC OPERATIONS PERFORMANCE              [cluster] [addon] [proj]|
+------------------------------------------------------------------+
|                                                                    |
|  +------------+ +------------+ +------------+ +------------+       |
|  | SYNCS/HR   | | FAIL RATE  | | RECON p95  | | RECON p99  |      |
|  |    42      | |   2.1%     | |   3.2s     | |   8.7s     |      |
|  +------------+ +------------+ +------------+ +------------+       |
|                                                                    |
+------------------------------------------------------------------+
|  SYNC COUNT OVER TIME (24h)                                        |
|                                                                    |
|  [TIMESERIES]                                                      |
|  ──── Succeeded   ──── Failed   ──── Error                         |
|                                                                    |
+------------------------------+-------------------------------------+
|  RECONCILIATION DURATION     |  WORK QUEUE DEPTH                   |
|                              |                                     |
|  [TIMESERIES]                |  [TIMESERIES]                       |
|  ──── p50  ──── p95  ──── p99|  ──── Queue Depth                   |
|                              |  ──── Pending Reconciliations        |
+------------------------------+-------------------------------------+
|  GIT REQUEST DURATION        |  DEPLOYMENT FREQUENCY (daily)        |
|                              |                                     |
|  [TIMESERIES]                |  [BAR CHART]                        |
|  ──── p50  ──── p95          |  ████ ████ ████ ████ ████ ████     |
+------------------------------+-------------------------------------+
|  SYNC SUCCESS RATE SLO (30d rolling)                               |
|                                                                    |
|  [SLO WIDGET]                                                      |
|  Target: 99%  |  Current: 99.7%  |  Error Budget: 73% remaining   |
+------------------------------------------------------------------+
```

### Panel Specifications

| Panel | Type | Query |
|---|---|---|
| Syncs/hr | Query Value | `sum:argocd.app_controller.app.sync.count{*}.as_count().rollup(sum, 3600)` |
| Fail Rate | Query Value (red if >5%) | `(failed / total) * 100` |
| Recon p95 | Query Value (amber if >10s) | `p95:argocd.app_controller.app.reconcile` |
| Recon p99 | Query Value (red if >30s) | `p99:argocd.app_controller.app.reconcile` |
| Sync Count | Timeseries | `argocd.app_controller.app.sync.count` by `phase` |
| Recon Duration | Timeseries | `argocd.app_controller.app.reconcile` percentiles |
| Queue Depth | Timeseries | `argocd.app_controller.workqueue.depth` |
| Git Duration | Timeseries | `argocd.repo_server.git.request.duration.seconds` |
| Deploy Frequency | Bar Chart | `sum:argocd.app_controller.app.sync.count{phase:succeeded}.rollup(sum, 86400)` |
| SLO | SLO Widget | Metric-based SLO: sync success rate, 99% target, 30d window |

---

## Dashboard 3: Sync Failure Analysis

**Purpose:** Root cause identification. Used when failures are detected.
**Refresh:** 30 seconds

```
+------------------------------------------------------------------+
|  SYNC FAILURE ANALYSIS                    [cluster] [addon] [proj]|
+------------------------------------------------------------------+
|                                                                    |
|  +------------+ +------------+ +------------+                      |
|  | FAILED     | | DEGRADED   | | OUT-OF-SYNC|                     |
|  | SYNCS (1h) | | APPS       | | DURATION   |                     |
|  |    3       | |    4       | |  avg 12m   |                      |
|  +------------+ +------------+ +------------+                      |
|                                                                    |
+------------------------------+-------------------------------------+
|  TOP FAILING APPS (24h)      |  FAILURE CATEGORY DISTRIBUTION     |
|                              |                                     |
|  [TOP LIST]                  |  [PIE CHART]                        |
|  1. datadog-cluster-7    5   |  Manifest Gen: 20%                  |
|  2. keda-cluster-12      3   |  RBAC: 25%                          |
|  3. istio-cluster-3      2   |  Timeout: 15%                       |
|                              |  K8s API: 10%                       |
+------------------------------+-------------------------------------+
|  DEGRADED APPS               |  CLUSTER DISCONNECTIONS             |
|                              |                                     |
|  [TABLE]                     |  [EVENT OVERLAY ON TIMELINE]        |
|  App       | Cluster | Since|  ▼ cluster-4 disconnected 10:32     |
|  datadog   | cl-7    | 12m  |  ▲ cluster-4 reconnected 10:35      |
|  keda      | cl-12   | 25m  |                                     |
+------------------------------+-------------------------------------+
|  ERROR LOGS (filtered)                                             |
|                                                                    |
|  [LOG STREAM]                                                      |
|  source:argocd status:error                                        |
|  10:45 [cluster-7] datadog: helm template failed: missing value    |
|  10:42 [cluster-12] keda: forbidden: cannot create deployment      |
|  10:38 [cluster-3] istio: context deadline exceeded                |
+------------------------------------------------------------------+
```

### Panel Specifications

| Panel | Type | Query |
|---|---|---|
| Failed Syncs (1h) | Query Value | `sum:argocd.app_controller.app.sync.count{phase:failed}.as_count().rollup(sum, 3600)` |
| Degraded Apps | Query Value | `count:argocd.app_controller.app.info{health_status:degraded}` |
| Out-of-Sync Duration | Query Value | Average time apps remain out-of-sync |
| Top Failing Apps | Top List | `argocd.app_controller.app.sync.count{phase:failed}` by `name` |
| Failure Categories | Pie Chart | Log facet `@failure_category` |
| Degraded Apps Table | Table | `argocd.app_controller.app.info{health_status:degraded}` |
| Cluster Disconnections | Event Overlay | `argocd.app_controller.cluster.connection_status` transitions |
| Error Logs | Log Stream | `source:argocd status:error` |

---

## Dashboard 4: Application Troubleshooting

**Purpose:** Per-app deep dive. Used for investigating specific failures.
**Refresh:** 30 seconds

```
+------------------------------------------------------------------+
|  APPLICATION TROUBLESHOOTING    [app_name: ____________] [cluster]|
+------------------------------------------------------------------+
|                                                                    |
|  +--------+ +--------+ +--------+ +--------+                      |
|  | SYNC   | | HEALTH | | LAST   | | LAST   |                      |
|  | STATUS | | STATUS | | SYNC   | | ERROR  |                      |
|  | Synced | |Healthy | | 5m ago | | none   |                      |
|  +--------+ +--------+ +--------+ +--------+                      |
|                                                                    |
+------------------------------------------------------------------+
|  SYNC HISTORY (last 24h)                                           |
|                                                                    |
|  [TIMESERIES WITH EVENT MARKERS]                                   |
|  ▼ Succeeded  ▼ Succeeded  ✕ Failed  ▼ Succeeded                  |
|  10:00        12:00        14:30     15:00                         |
|                                                                    |
+------------------------------+-------------------------------------+
|  HEALTH STATUS TIMELINE      |  RECONCILIATION DURATION            |
|                              |                                     |
|  [TIMESERIES]                |  [TIMESERIES]                       |
|  ──── Healthy  ──── Degraded |  ──── Duration                      |
|  ──── Progressing            |  ---- p95 baseline                  |
+------------------------------+-------------------------------------+
|  RESOURCE OPERATIONS         |  K8S API REQUESTS                   |
|                              |                                     |
|  [TIMESERIES]                |  [TIMESERIES]                       |
|  ──── kubectl apply          |  ──── Request count                 |
|  ──── kubectl delete         |                                     |
+------------------------------+-------------------------------------+
|  APPLICATION LOGS                                                  |
|                                                                    |
|  [LOG STREAM]                                                      |
|  source:argocd @app:$app_name                                      |
|  15:00 Sync succeeded revision=abc123                              |
|  14:30 Sync failed: helm template error in values.yaml line 42     |
|  14:00 Health check: Progressing -> Degraded                       |
+------------------------------------------------------------------+
```

### Panel Specifications

| Panel | Type | Query |
|---|---|---|
| Sync Status | Query Value (color-coded) | `argocd.app_controller.app.info{name:$app_name}` tag `sync_status` |
| Health Status | Query Value (color-coded) | `argocd.app_controller.app.info{name:$app_name}` tag `health_status` |
| Last Sync | Query Value | Time since last `argocd.app_controller.app.sync.count{name:$app_name}` |
| Last Error | Query Value | Most recent error from logs |
| Sync History | Timeseries + events | `argocd.app_controller.app.sync.count{name:$app_name}` by `phase` |
| Health Timeline | Timeseries | `argocd.app_controller.app.info{name:$app_name}` by `health_status` |
| Recon Duration | Timeseries | `argocd.app_controller.app.reconcile{name:$app_name}` |
| Resource Ops | Timeseries | `argocd.app_controller.kubectl.exec.count{name:$app_name}` by `command` |
| K8s Requests | Timeseries | `argocd.app_controller.app.k8s.request.count{name:$app_name}` |
| App Logs | Log Stream | `source:argocd @app:$app_name` |

---

## Dashboard Navigation

```
Overview ──> Failure Analysis ──> Troubleshooting
    │              │                    │
    │              └── Click app ──────>│
    │                                   │
    └── Click degraded/outofsync ──────>│

Performance (standalone, for trend analysis)
```

Each dashboard should link to the others via header links. Clicking an app name in the Failure Analysis dashboard should open Troubleshooting with `$app_name` pre-filled.

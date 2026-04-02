# Application Operations Observability - Requirements Document

**User Story:** #403540 - [Cluster-Addons] Investigation & Requirements - Application Operations Observability
**Date:** 2026-03-11
**Status:** Investigation Complete

---

## Executive Summary

This document defines the requirements for monitoring ArgoCD Application sync operations across our fleet of 50+ EKS clusters managed via ApplicationSets. The goal is to reduce Mean Time to Detect (MTTD) sync failures from 15-30 minutes to under 5 minutes, and Mean Time to Resolve (MTTR) from 30-60 minutes to 15-30 minutes through proactive monitoring, failure categorization, and guided troubleshooting dashboards.

---

## 1. ArgoCD Metrics Catalog

### 1.1 Application Controller Metrics (port 8082)

These are the primary metrics for sync and health monitoring.

#### Sync Operations

| Prometheus Metric | Type | Key Labels | Description |
|---|---|---|---|
| `argocd_app_sync_total` | counter | `name`, `namespace`, `project`, `phase` | Sync operation count. `phase`: Succeeded, Failed, Error |
| `argocd_app_sync_duration_seconds_total` | counter | `name`, `namespace`, `project`, `phase` | Cumulative sync duration (use `rate()`) |
| `argocd_app_reconcile` | histogram | `name`, `namespace`, `project`, `dest_server` | Reconciliation loop duration (detect drift + sync) |

#### Application State

| Prometheus Metric | Type | Key Labels | Description |
|---|---|---|---|
| `argocd_app_info` | gauge | `sync_status`, `health_status`, `name`, `namespace`, `project`, `dest_server` | Real-time app state. sync_status: Synced/OutOfSync/Unknown. health_status: Healthy/Degraded/Progressing/Suspended/Missing/Unknown |
| `argocd_app_condition` | gauge | (conditions) | Error conditions: SyncError, ComparisonError, InvalidSpecError |
| `argocd_app_orphaned_resources_count` | gauge | `name`, `namespace`, `project` | Orphaned resources per app |

#### Resource Operations (kubectl)

| Prometheus Metric | Type | Key Labels | Description |
|---|---|---|---|
| `argocd_kubectl_exec_total` | counter | `command`, `call_status` | kubectl apply/delete executions |
| `argocd_kubectl_exec_pending` | gauge | - | Pending kubectl operations (backlog indicator) |
| `argocd_app_k8s_request_total` | counter | `name`, `namespace`, `project` | K8s API requests per reconciliation |

#### Cluster State

| Prometheus Metric | Type | Key Labels | Description |
|---|---|---|---|
| `argocd_cluster_connection_status` | gauge | `server`, `name` | 1=connected, 0=disconnected |
| `argocd_cluster_cache_age_seconds` | gauge | `server`, `name` | Cache staleness indicator |
| `argocd_cluster_api_resource_objects` | gauge | `server`, `name` | Cached resource object count |
| `argocd_cluster_events_total` | counter | `server`, `name` | Processed K8s events |

#### Work Queue

| Prometheus Metric | Type | Description |
|---|---|---|
| `argocd_workqueue_depth` | gauge | Current queue depth |
| `argocd_workqueue_adds_total` | counter | Queue additions |
| `argocd_workqueue_retries_total` | counter | Queue retries |
| `argocd_workqueue_longest_running_processor_seconds` | gauge | Longest running processor |
| `argocd_workqueue_unfinished_work_seconds` | gauge | Unfinished work (stuck thread indicator) |

### 1.2 ApplicationSet Controller Metrics

| Prometheus Metric | Type | Key Labels | Description |
|---|---|---|---|
| `argocd_appset_info` | gauge | `name`, `namespace`, `resource_update_status` | ApplicationSet state |
| `argocd_appset_reconcile` | histogram | `name`, `namespace` | ApplicationSet reconciliation duration |
| `argocd_appset_owned_applications` | gauge | `name`, `namespace` | Apps owned by the ApplicationSet |

### 1.3 Repo Server Metrics (port 8084)

| Prometheus Metric | Type | Description |
|---|---|---|
| `argocd_git_request_duration_seconds` | histogram | Git request duration |
| `argocd_git_request_total` | counter | Git request count |
| `argocd_git_fetch_fail_total` | counter | Git fetch failures |
| `argocd_repo_pending_request_total` | gauge | Pending repo lock requests |

### 1.4 API Server Metrics (port 8083)

| Prometheus Metric | Type | Description |
|---|---|---|
| `grpc_server_handled_total` | counter | Completed gRPC RPCs |
| `argocd_login_request_total` | counter | Login request count |

### 1.5 Notifications Controller Metrics

| Prometheus Metric | Type | Description |
|---|---|---|
| `argocd_notifications_deliveries_total` | counter | Delivered notifications |
| `argocd_notifications_trigger_eval_total` | counter | Trigger evaluations |

### 1.6 Metrics NOT Available (Gaps)

- **Hook execution details** -- No dedicated metric for PreSync/PostSync hook duration or status. Hook results are embedded in `argocd_app_sync_total` phase.
- **Per-resource create vs update** -- `argocd_kubectl_exec_total` tracks `apply` but doesn't distinguish create from update.
- **Sync retries** -- No dedicated retry counter. Must be inferred from repeated sync increments.
- **Detailed failure reasons** -- Metrics only carry phase labels (Failed/Error), not error category. Need log parsing for categorization.

---

## 2. Metrics Collection Architecture

### 2.1 Datadog Integration Method

Datadog collects ArgoCD metrics via **OpenMetrics** scraping of Prometheus endpoints.

**ArgoCD Component Endpoints:**

| Component | Port | Path | Key Metrics |
|---|---|---|---|
| Application Controller | 8082 | `/metrics` | App sync, health, reconciliation, cluster state |
| API Server | 8083 | `/metrics` | gRPC request counts and latency |
| Repo Server | 8084 | `/metrics` | Git request duration and counts |
| Commit Server | 8087 | `/metrics` | Commit activity |

### 2.2 Datadog Metric Name Mapping

Prometheus metrics are mapped to Datadog's dot-notation with component prefix:

| Prometheus | Datadog |
|---|---|
| `argocd_app_sync_total` | `argocd.app_controller.app.sync.count` |
| `argocd_app_info` | `argocd.app_controller.app.info` |
| `argocd_app_reconcile` | `argocd.app_controller.app.reconcile.*` |
| `argocd_cluster_connection_status` | `argocd.app_controller.cluster.connection_status` |
| `argocd_git_request_duration_seconds` | `argocd.repo_server.git.request.duration.seconds.*` |
| `argocd_appset_info` | `argocd.appset_controller.appset.info` |

### 2.3 Estimated Custom Metrics Volume

~530 custom metrics total across all components. At $1/metric/month = ~$530/month.

---

## 3. ArgoCD API for Sync History

### 3.1 Key API Endpoints

| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/applications/{name}` | Full app details including status.history and operationState |
| GET | `/api/v1/applications` | List all applications with status |
| POST | `/api/v1/applications/{name}/sync` | Trigger sync |
| GET | `/api/v1/applications/{name}/events` | K8s events for the application |
| GET | `/api/v1/stream/applications?name={app}` | SSE stream for real-time state changes |

### 3.2 Sync History Data (`status.history[]`)

Each entry in `status.history` is a **RevisionHistory** object:
- `id` -- Auto-incrementing identifier
- `revision` -- Git commit hash synced
- `deployStartedAt` -- Sync start timestamp
- `deployedAt` -- Sync completion timestamp
- `source` -- Application source (repoURL, path, targetRevision)

**Limit:** Controlled by `spec.revisionHistoryLimit` (default: 10). Stored in etcd on the Application CR.

### 3.3 Current Operation State (`status.operationState`)

The most detailed sync info. Only stores the **most recent** operation:
- `phase` -- Running, Succeeded, Failed, Error
- `message` -- Human-readable error message
- `startedAt` / `finishedAt` -- Timestamps
- `syncResult.revision` -- Git revision
- `syncResult.resources[]` -- Per-resource results:
  - `group`, `version`, `kind`, `namespace`, `name`
  - `status` -- Synced, SyncFailed, Pruned
  - `message` -- Per-resource error message
  - `hookPhase` -- Hook execution phase

### 3.4 Critical Limitation

ArgoCD does **NOT** maintain a long-term sync history database. `operationState` is overwritten on each new sync. To retain historical failure data, we must capture events as they happen via:
- Prometheus metrics (aggregate counters)
- ArgoCD notifications (webhooks to Datadog)
- SSE stream polling
- Log pipeline parsing

---

## 4. Sync Failure Categorization

### 4.1 Failure Categories

| # | Category | Frequency | Phase | Description |
|---|---|---|---|---|
| 1 | Manifest Generation Errors | ~20% | ComparisonError | Helm template failures, invalid YAML, missing values |
| 2 | Kubernetes API / Apply Errors | ~10% | Failed | Schema validation, immutable fields, CRD not installed, quota exceeded |
| 3 | RBAC / Permission Errors | ~25% | Failed | Missing ClusterRole/RoleBindings, PSP/PSA violations |
| 4 | Admission Webhook Rejections | ~10% | Failed | OPA/Gatekeeper/Kyverno policy violations, webhook timeouts |
| 5 | Hook Failures | ~8% | Failed | PreSync/PostSync Job failures, hook OOMKill |
| 6 | Cluster Connectivity Errors | ~5% | Error | Cluster unreachable, TLS errors, network issues |
| 7 | Timeout / Deadline Exceeded | ~15% | Error | Slow cluster API, stuck Progressing state, context deadline |
| 8 | Resource Conflicts | ~5% | Failed | Multiple managers, already exists |
| 9 | Secret-Related Failures | ~2% | Failed/Degraded | ExternalSecret provider errors, decryption failures |

### 4.2 Detection Patterns (Log/Error Message)

**Category 1 - Manifest Generation:**
```
helm template failed
kustomize build failed
failed to generate manifests
error converting YAML
```

**Category 2 - K8s API Errors:**
```
the server could not find the requested resource
is invalid
field is immutable
exceeded quota
```

**Category 3 - RBAC:**
```
forbidden
User "system:serviceaccount:argocd:.*" cannot
RBAC
```

**Category 4 - Admission Webhooks:**
```
admission webhook .* denied the request
validate.*.webhook
mutate.*.webhook
```

**Category 5 - Hooks:**
```
hook failed
job failed
BackoffLimitExceeded
```

**Category 6 - Connectivity:**
```
TLS handshake timeout
connection refused
dial tcp
authentication required
repository not found
```

**Category 7 - Timeouts:**
```
context deadline exceeded
DeadlineExceeded
timeout
```

**Category 8 - Conflicts:**
```
already managed by
resource already exists
conflict
```

### 4.3 Transient vs Persistent Failures

| Type | Characteristics | Action |
|---|---|---|
| Transient | Resolves on retry, typically connectivity/timeout | Alert after 2+ consecutive failures |
| Persistent | Same error on every sync attempt | Alert immediately, requires human intervention |

**Detection method:** Track consecutive failure count per app. If `argocd_app_sync_total{phase=Failed}` increments without a `phase=Succeeded` in between, classify as persistent.

---

## 5. Dashboard Requirements

### 5.1 Dashboard 1: Application Operations Overview

**Purpose:** Single-pane fleet health view. First stop for on-call engineers.

| Panel | Widget | Metric/Query |
|---|---|---|
| Total Applications | Query Value | `count:argocd.app_controller.app.info` |
| Synced Apps | Query Value | `count:argocd.app_controller.app.info{sync_status:synced}` |
| Out-of-Sync Apps | Query Value | `count:argocd.app_controller.app.info{sync_status:outofsync}` |
| Healthy Apps | Query Value | `count:argocd.app_controller.app.info{health_status:healthy}` |
| Degraded Apps | Query Value | `count:argocd.app_controller.app.info{health_status:degraded}` |
| Cluster Connection Status | Check Status | `argocd.app_controller.cluster.connection_status` |
| Sync Status Distribution | Pie Chart | `argocd.app_controller.app.info` by `sync_status` |
| Health Status Distribution | Pie Chart | `argocd.app_controller.app.info` by `health_status` |
| Recent Sync Activity | Timeseries | `argocd.app_controller.app.sync.count` by `phase` |

**Template Variables:** `$cluster`, `$addon`, `$project`, `$sync_status`, `$health_status`

### 5.2 Dashboard 2: Sync Operations Performance

**Purpose:** Track sync performance trends, identify slowdowns.

| Panel | Widget | Metric/Query |
|---|---|---|
| Sync Count Over Time | Timeseries | `sum:argocd.app_controller.app.sync.count{*}.as_count()` |
| Sync Success vs Failure | Stacked Timeseries | `argocd.app_controller.app.sync.count` by `phase` |
| Sync Failure Rate | Query Value | `failed / total * 100` |
| Reconciliation Duration p95 | Timeseries | `p95:argocd.app_controller.app.reconcile` |
| Reconciliation Duration p99 | Query Value | `p99:argocd.app_controller.app.reconcile` |
| Pending Reconciliations | Timeseries | `argocd.app_controller.workqueue.depth` |
| Git Request Duration | Timeseries | `argocd.repo_server.git.request.duration.seconds` |
| Deployment Frequency (daily) | Timeseries | `sum:argocd.app_controller.app.sync.count{phase:succeeded}.rollup(sum, 86400)` |

### 5.3 Dashboard 3: Sync Failure Analysis

**Purpose:** Root cause identification for sync failures.

| Panel | Widget | Metric/Query |
|---|---|---|
| Failed Syncs by App | Top List | `argocd.app_controller.app.sync.count{phase:failed}` by `name` |
| Degraded Apps List | Top List | `argocd.app_controller.app.info{health_status:degraded}` by `name` |
| Error Logs | Log Stream | `source:argocd status:error` |
| Failure Category Distribution | Pie Chart | Log facet `@failure_category` |
| Out-of-Sync Duration | Timeseries | Time since app left `synced` state |
| Cluster Disconnections | Event Overlay | `argocd.app_controller.cluster.connection_status = 0` |

### 5.4 Dashboard 4: Application Troubleshooting

**Purpose:** Deep-dive debugging for specific applications.

| Panel | Widget | Metric/Query |
|---|---|---|
| App Sync History | Table | `argocd.app_controller.app.sync.count` filtered by `$app_name` |
| App Health Timeline | Timeseries | `argocd.app_controller.app.info` by `health_status` for `$app_name` |
| App Reconciliation Duration | Timeseries | `argocd.app_controller.app.reconcile` for `$app_name` |
| App Error Logs | Log Stream | `source:argocd @app:$app_name status:error` |
| Resource Events | Log Stream | K8s events for the app namespace |
| kubectl Operations | Timeseries | `argocd.app_controller.kubectl.exec.count` for `$app_name` |

---

## 6. Alert Requirements

### 6.1 Alert Definitions

| # | Alert Name | Condition | Severity | Notification |
|---|---|---|---|---|
| 1 | App Out-of-Sync | `sync_status:outofsync` persists > 30 min | Warning | Slack |
| 2 | App Degraded | `health_status:degraded` persists > 15 min | Warning | Slack |
| 3 | Multiple Degraded Apps | `count(degraded) > 3` for 2 min | Critical | Slack + PagerDuty |
| 4 | Cluster Disconnected | `cluster.connection_status = 0` | Critical | Slack + PagerDuty |
| 5 | Reconciliation Slow | p95 > 10 seconds | Warning | Slack |
| 6 | Reconciliation Very Slow | p99 > 30 seconds | Critical | Slack |
| 7 | High Sync Failure Rate | `failed/total > 5%` over 1 hour | Warning | Slack |
| 8 | Queue Depth Growing | `workqueue.depth` increasing for > 10 min | Warning | Slack |

### 6.2 SLO Targets

| SLO | Target | Window |
|---|---|---|
| Sync Success Rate | 99% | Rolling 30 days |
| Healthy Resources | 99% | At all times |
| Reconciliation p95 | < 10 seconds | Rolling |
| Reconciliation p99 | < 30 seconds | Rolling |

### 6.3 ArgoCD Notification Triggers (Built-in)

These triggers can send to Slack/webhook/Datadog events:

| Trigger | Fires When |
|---|---|
| `on-sync-failed` | `operationState.phase in ['Error', 'Failed']` |
| `on-health-degraded` | `health.status == 'Degraded'` |
| `on-sync-status-unknown` | `sync.status == 'Unknown'` |
| `on-deployed` | `phase == 'Succeeded' AND health == 'Healthy'` |
| `on-sync-running` | Sync in progress |
| `on-sync-succeeded` | `phase == 'Succeeded'` |

**Key template variables for notifications:**
- `{{.app.metadata.name}}` -- app name
- `{{.app.spec.destination.server}}` -- target cluster
- `{{.app.status.operationState.phase}}` -- operation phase
- `{{.app.status.operationState.message}}` -- error message
- `{{.app.status.operationState.syncResult.resources}}` -- per-resource results

---

## 7. Gap Analysis

### 7.1 Current State

| Capability | Status |
|---|---|
| ArgoCD metrics scraped by Datadog | Not configured |
| Sync failure alerting | None (reactive, user reports) |
| Failure categorization | Manual log search |
| Fleet-wide health dashboard | None |
| Sync performance tracking | None |
| Troubleshooting runbooks | None |

### 7.2 Target State

| Capability | Target |
|---|---|
| Metrics collection | All ArgoCD component metrics via OpenMetrics |
| Alerting | 8 monitors with defined thresholds |
| Failure categorization | Automated via log pipelines (9 categories) |
| Fleet dashboard | 4 Datadog dashboards |
| Performance tracking | p95/p99 reconciliation, sync duration trends |
| Runbooks | Per-failure-category troubleshooting guides |

---

## 8. Success Criteria

### Investigation Phase (this document)

- [x] ArgoCD metrics catalog documented
- [x] API sync history capabilities documented
- [x] Sync failure categorization defined (9 categories)
- [x] Dashboard requirements specified (4 dashboards)
- [x] Alert requirements defined (8 alerts + SLOs)
- [x] Gap analysis completed
- [x] Implementation effort estimated

### Implementation Phase (Story #403541)

- [ ] Datadog OpenMetrics configured for ArgoCD
- [ ] Log parsing pipelines created
- [ ] 4 dashboards deployed
- [ ] 8 monitors configured
- [ ] Baselines collected, thresholds tuned

### Expected Impact

| Metric | Current | Target |
|---|---|---|
| MTTD (sync failures) | 15-30 min | < 5 min |
| MTTR (sync failures) | 30-60 min | 15-30 min |
| Fleet visibility | None | Real-time dashboard |
| Proactive detection | 0% | 80%+ of failures |

---

## 9. Implementation Roadmap

| Phase | Effort | Tasks |
|---|---|---|
| 1. Metrics Collection | 2-3 hours | Configure Datadog OpenMetrics scraping for all ArgoCD components |
| 2. Log Processing | 4-6 hours | Create Datadog log pipelines for 9 failure categories |
| 3. Dashboard Development | 1-2 days | Build 4 dashboards from specifications above |
| 4. Alert Configuration | 1 day + 1-2 weeks baseline | Configure 8 monitors, collect baselines, tune thresholds |
| 5. Documentation | 2-3 days | Runbooks, training materials, usage guides |

**Total:** 1-2 weeks (excluding baseline collection period)

---

## References

### ArgoCD Documentation
- [ArgoCD Metrics](https://argo-cd.readthedocs.io/en/latest/operator-manual/metrics/)
- [ArgoCD API Docs](https://argo-cd.readthedocs.io/en/stable/developer-guide/api-docs/)
- [ArgoCD Resource Health](https://argo-cd.readthedocs.io/en/stable/operator-manual/health/)
- [ArgoCD Notifications - Triggers](https://argo-cd.readthedocs.io/en/stable/operator-manual/notifications/triggers/)
- [ArgoCD Notifications - Templates](https://argo-cd.readthedocs.io/en/stable/operator-manual/notifications/templates/)
- [ArgoCD Sync Options](https://argo-cd.readthedocs.io/en/latest/user-guide/sync-options/)

### Datadog Documentation
- [Datadog ArgoCD Integration](https://docs.datadoghq.com/integrations/argocd/)
- [Monitor ArgoCD with Datadog (Blog)](https://www.datadoghq.com/blog/argo-cd-datadog/)
- [Datadog ArgoCD Deployments](https://docs.datadoghq.com/continuous_delivery/deployments/argocd/)

### Community Resources
- [Grafana ArgoCD Operational Overview Dashboard](https://grafana.com/grafana/dashboards/19993-argocd-operational-overview/)
- [Defining SLOs for ArgoCD (Discussion)](https://github.com/argoproj/argo-cd/discussions/12600)

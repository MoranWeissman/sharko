# Monitoring

Datadog observability for the ArgoCD management cluster and Application operations.

Two monitoring scopes:
- **Host Cluster** — is the infrastructure healthy? (nodes, pods, ArgoCD component resources)
- **Application Operations** — are addon deployments working? (sync status, health, cluster connectivity)

---

## What We Monitor

### Host Cluster — "Is ArgoCD running?"

These monitors watch the management cluster infrastructure. If any of these fire, ArgoCD itself may be impacted.

| Monitor | What it means | When it fires |
|---------|--------------|---------------|
| **App Controller Down** | ArgoCD can't sync any applications. Everything is frozen. | Controller pod has 0 ready replicas for 5 minutes |
| **AppSet Controller Down** | ArgoCD can't generate Applications from ApplicationSets. New addons won't deploy. | AppSet controller pod has 0 ready replicas for 5 minutes |
| **Node NotReady** | A cluster node is having issues. Pods may be evicted. | Any node enters NotReady state |
| **Node Disk Utilization** | A node is running low on disk. May cause evictions. | Disk usage >75% (warning) or >85% (critical) |
| **Node Memory Utilization** | A node is running low on memory. May cause OOMKills. | Memory usage >80% (warning) or >90% (critical) |
| **Container Restart Rate** | A component is crash-looping. Something is broken. | >3 restarts in 15 min (warning) or >5 (critical) |
| **Component CPU Utilization** | A component is running hot. May become slow or unresponsive. | CPU >80% of limit (warning) or >95% (critical) |
| **Component Memory Utilization** | A component is close to its memory limit. May get OOMKilled. | Memory >80% of limit (warning) or >90% (critical) |

### Application Operations — "Are addon deployments working?"

These monitors watch ArgoCD's Application objects. If any of these fire, one or more addons across your clusters may have issues.

| Monitor | What it means | When it fires |
|---------|--------------|---------------|
| **High Sync Failure Rate** | A significant number of syncs are failing. Could be a bad config change, helm error, or cluster issue. | >5% of syncs fail in 1 hour (warning) or >10% (critical). Shows "No Data" when no syncs happened — that's normal. |
| **Application Health Degraded** | An addon is deployed but not working correctly on a specific cluster. The Kubernetes resources exist but aren't healthy. | An app stays in Degraded health status for >15 minutes |
| **Cluster Disconnected** | ArgoCD can't reach a remote cluster. All addons on that cluster are unmanaged until reconnected. | Cluster connection status drops to 0 for 5 minutes |
| **Reconciliation Slow** | ArgoCD is taking too long to check if apps match their desired state. Could indicate controller overload or slow cluster API. | p95 reconciliation >10s (warning) or >30s (critical) |
| **Work Queue Depth Growing** | More apps are waiting to be processed than the controller can handle. It's falling behind. | Queue depth >50 for 15 min (warning) or >100 (critical) |
| **Multiple Applications Degraded** | Several addons are unhealthy at the same time. Likely a systemic issue — cluster problem, shared dependency, or controller issue. | More than 3 apps in Degraded state simultaneously |
| **Application Health Unknown** | ArgoCD can't determine the health of an app. Could mean bad values YAML, missing CRD, or broken health check. | App stays in Unknown health for >10 minutes |
| **Application Sync Failed** | A specific addon is repeatedly failing to sync. Tells you exactly which app on which cluster. | An app fails >2 syncs in 15 minutes (warning at >1) |

---

## Dashboards

| Dashboard | Question it answers | Status |
|-----------|-------------------|--------|
| **ArgoCD Cluster Addons Host Cluster** | Is the management cluster infrastructure healthy? | Live |
| **ArgoCD Cluster Addons Application Operations** | Are addon deployments working across all clusters? | Live |
| **ArgoCD Cluster Addons Application Troubleshooting** | Why is a specific addon failing? | Live |

---

## Folder Structure

```
monitoring/
├── crds/                                        # DatadogMonitor/DatadogDashboard CRD examples
│   ├── monitors/                                # DatadogMonitor CRDs (one per monitor)
│   │   ├── argocd-app-controller-down.yaml      # [Host] App controller pod down
│   │   ├── argocd-appset-controller-down.yaml   # [Host] AppSet controller pod down
│   │   ├── node-notready.yaml                   # [Host] Node NotReady
│   │   ├── node-disk-utilization.yaml           # [Host] Node disk usage
│   │   ├── node-memory-utilization.yaml         # [Host] Node memory usage
│   │   ├── container-restart-rate.yaml          # [Host] Container restart rate
│   │   ├── component-cpu-utilization.yaml       # [Host] Component CPU usage
│   │   ├── component-memory-utilization.yaml    # [Host] Component memory usage
│   │   ├── app-sync-failure-rate.yaml           # [AppOps] High sync failure rate
│   │   ├── app-health-degraded.yaml             # [AppOps] Application health degraded
│   │   ├── app-cluster-disconnected.yaml        # [AppOps] Cluster disconnected
│   │   ├── app-reconciliation-slow.yaml         # [AppOps] Reconciliation slow
│   │   ├── app-queue-depth-growing.yaml         # [AppOps] Work queue depth growing
│   │   └── app-multiple-degraded.yaml           # [AppOps] Multiple apps degraded
│   ├── dashboard-host-cluster.yaml              # DatadogDashboard CRD — host cluster
│   ├── dashboard-app-operations.yaml            # DatadogDashboard CRD — app operations overview
│   └── dashboard-app-troubleshooting.yaml       # DatadogDashboard CRD — app troubleshooting
│
├── dashboards/                                  # Datadog dashboard JSON exports (for import)
│   ├── app-operations.json
│   └── app-troubleshooting.json
│
└── monitors/
    ├── host-cluster/                            # JSON exports — host cluster monitors
    │   ├── argocd-app-controller-down.json
    │   ├── argocd-appset-controller-down.json
    │   ├── node-notready.json
    │   ├── node-disk-utilization.json
    │   ├── node-memory-utilization.json
    │   ├── container-restart-rate.json
    │   ├── component-cpu-utilization.json
    │   └── component-memory-utilization.json
    └── app-operations/                          # JSON exports — application operations monitors
        ├── app-sync-failure-rate.json
        ├── app-sync-failed.json
        ├── app-health-degraded.json
        ├── app-health-unknown.json
        ├── app-cluster-disconnected.json
        ├── app-reconciliation-slow.json
        ├── app-queue-depth-growing.json
        └── app-multiple-degraded.json
```

---

## How to Import

### Dashboard
1. Datadog → Dashboards → New Dashboard
2. Gear icon (top right) → Import dashboard JSON
3. Paste contents of the `.json` file from `dashboards/`
4. Save

### Monitor
1. Datadog → Monitors → New Monitor → Import
2. Paste contents of the `.json` file from `monitors/`
3. Save

### Export Changes Back
1. Dashboard: Edit → gear icon → Export dashboard JSON → update the `.json` file
2. Monitor: Edit → Export → Copy JSON → update the `.json` file

---

## Monitor Tags

All monitors are tagged for easy filtering in Datadog:

```
platform:devex
kubernetes
service:argocd-cluster-addons
component:argocd-cluster-addons-host    # for host cluster monitors
component:argocd-cluster-addons-appops  # for application operations monitors
```

Filter in Datadog Monitors page: `component:argocd-cluster-addons-host` or `component:argocd-cluster-addons-appops`

---

## Notifications

All monitors notify:
- **Email:** team@example.com
- **Microsoft Teams:** platform-alerts channel

---

## Known Limitations

- **Sync failure rate shows "No Data" when no syncs happen** — this is normal. The monitor only evaluates when syncs occur.
- **Cluster connectivity shows server URLs, not cluster names** — the `argocd_cluster_connection_status` metric only has a `server` tag (API URL), not a human-readable cluster name.
- **ArgoCD `project` tag renamed to `argocd_project`** — to avoid collision with the AWS host tag `Project:devops`. See `docs/observability/DATADOG_ARGOCD_INTEGRATION.md` for details.

---

## References

- [Datadog ArgoCD Integration](https://docs.datadoghq.com/integrations/argocd/)
- [ArgoCD Metrics](https://argo-cd.readthedocs.io/en/latest/operator-manual/metrics/)
- [Datadog Operator — DatadogMonitor CRD](https://docs.datadoghq.com/containers/datadog_operator/)
- [Integration Findings](../docs/observability/DATADOG_ARGOCD_INTEGRATION.md) — what Datadog collects, what it doesn't, custom metrics cost

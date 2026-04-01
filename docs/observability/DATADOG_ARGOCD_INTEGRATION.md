# Datadog ArgoCD Integration — What Works and What Doesn't

**Date:** 2026-03-15
**Status:** Validated against live setup

---

## How It Works

ArgoCD exposes Prometheus metrics on each component (application-controller, api-server, repo-server). The Datadog agent scrapes these endpoints using pod annotations (autodiscovery) and maps them to Datadog metric names.

For example, the Prometheus metric `argocd_app_info` becomes `argocd.app_controller.app.info` in Datadog.

---

## What Datadog Collects

These are the ArgoCD metrics that Datadog's built-in integration actually collects:

### Application Metrics
| What you see in Datadog | What it tells you |
|---|---|
| `argocd.app_controller.app.info` | Each app's health (Healthy/Degraded/Progressing) and sync status (Synced/OutOfSync) |
| `argocd.app_controller.app.labels` | Custom labels on Application CRs (like `cluster-name` and `app-type`) |
| `argocd.app_controller.app.sync.count` | How many syncs succeeded or failed |
| `argocd.app_controller.app.reconcile` | How long it takes ArgoCD to check if apps match their desired state |

### Controller Metrics
| What you see in Datadog | What it tells you |
|---|---|
| `argocd.app_controller.workqueue.depth` | How many apps are waiting to be processed — if this grows, the controller is falling behind |
| `argocd.app_controller.kubectl.exec` | How many kubectl apply/delete operations ArgoCD is running |

### Cluster Metrics (limited)
| What you see in Datadog | What it tells you |
|---|---|
| `argocd.app_controller.cluster.cache.age.seconds` | How old the cached cluster state is — if it stops updating, the cluster may be disconnected |
| `argocd.app_controller.cluster.api.resource_objects` | How many Kubernetes objects ArgoCD is tracking per cluster |

---

## What Datadog Does NOT Collect by Default (Resolved via extra_metrics)

These Prometheus metrics are **not in the default integration mapping**, but we collect them via `extra_metrics` in the check annotation:

| Prometheus metric | Datadog metric name | How we enabled it |
|---|---|---|
| `argocd_cluster_connection_status` | `argocd.app_controller.argocd_cluster_connection_status` | `extra_metrics` in pod annotation |
| `argocd_cluster_info` | `argocd.app_controller.argocd_cluster_info` | `extra_metrics` in pod annotation |

Note: `argocd_cluster_connection_status` is also automatically converted to a **service check** (`argocd.app_controller.cluster.connection.status`) by the integration's built-in transformer. The `extra_metrics` config adds it as a regular metric too, so we can use it in dashboard table widgets.

---

## Known Issue: Host Tag Collision

### The Problem

The host running the ArgoCD controller has AWS tags like `Project:devops`. ArgoCD apps have a metric label called `project` (e.g., `project:datadog`, `project:istiod`).

Datadog treats tags as case-insensitive. When you group a dashboard widget `by {project}`, Datadog sees **both** the host tag `Project:devops` and the metric tag `project:datadog`, and merges them into labels like "datadog, devops" or "devops, istiod".

### The Fix

Rename the ArgoCD `project` label to `argocd_project` in the Datadog check configuration. This is done via the `rename_labels` option in the pod annotation:

```yaml
# In ArgoFleet ArgoCD Helm values — controller podAnnotations
ad.datadoghq.com/application-controller.checks: |
  {
    "argocd": {
      "init_config": {},
      "instances": [
        {
          "app_controller_endpoint": "http://%%host%%:8082/metrics",
          "rename_labels": {
            "project": "argocd_project"
          }
        }
      ]
    }
  }
```

After this change, dashboard queries use `argocd_project` instead of `project` for grouping and filtering, and the host tag collision goes away.

---

## Custom Metrics — What They Are and What They Cost

### What is a Custom Metric?

In Datadog, a "custom metric" is any metric that isn't part of the standard infrastructure metrics (CPU, memory, disk, network). Every metric that comes from the ArgoCD integration is a custom metric.

### How Are They Counted?

Each unique combination of **metric name + tag values** counts as one custom metric. For example:

```
argocd.app_controller.app.info{name:datadog-feedlot-dev, health_status:healthy, sync_status:synced}  = 1 custom metric
argocd.app_controller.app.info{name:datadog-ark-dev-eks, health_status:degraded, sync_status:synced}  = 1 custom metric
argocd.app_controller.app.info{name:keda-feedlot-dev, health_status:healthy, sync_status:synced}      = 1 custom metric
```

So if you have 50 clusters × 10 addons = 500 applications, each with ~5 tag combinations, that's roughly 2,500 custom metric series just for `app.info`.

### How Much Does It Cost?

Datadog charges approximately **$1 per custom metric per month** on standard plans. The actual price depends on your contract.

### Our Estimated Cost

| Metric | Cardinality Driver | Estimated Series |
|---|---|---|
| `app.info` | apps × health × sync | ~500-1,000 |
| `app.labels` | apps × label values | ~500 |
| `app.sync.count` | apps × phase | ~200 |
| `app.reconcile` (histogram) | apps × buckets | ~1,000-2,000 |
| `workqueue.*` | few queues | ~10 |
| `cluster.*` | clusters | ~100 |
| **Total** | | **~2,000-4,000** |

**Estimated monthly cost: $200-400/month** at scale (50 clusters, 10 addons each).

### How to Reduce Cost

1. **Filter metrics** — Only scrape what you need. The Datadog check supports `metrics` config to include/exclude specific metrics.
2. **Reduce histogram buckets** — `app.reconcile` is a histogram that generates many series. Use `histogram_buckets_as_distributions: true` to collapse buckets.
3. **Start small** — Begin with one cluster, validate the dashboards, then expand. You only pay for what's scraped.

---

## Setup Summary

### What's Needed

1. **Pod annotations on ArgoCD components** (in ArgoFleet) — tells the Datadog agent where to scrape metrics
2. **`--metrics-application-labels` flag on the application controller** (in ArgoFleet) — exposes custom Application CR labels as metrics
3. **Dashboard JSON** — import into Datadog UI

### Component Endpoints

| Component | Port | Path | What it provides |
|---|---|---|---|
| Application Controller | 8082 | `/metrics` | App health, sync status, reconciliation, cluster state |
| API Server | 8083 | `/metrics` | gRPC request counts |
| Repo Server | 8084 | `/metrics` | Git request duration |

---

## Datadog Secrets Management

### How API and App Keys Are Managed

Both keys are stored in AWS Secrets Manager and fetched into K8s secrets via ExternalSecrets.

**Chart:** `charts/datadog-secrets/` — a single Helm chart that creates ExternalSecrets for both keys.

**API Key** (required for all clusters with Datadog):
- AWS Secret: `datadog-api-keys-integration`
- Lookup: `<projectName>-<env>` (e.g., `feedlot-dev`, `sensehub-dev`)
- K8s Secret: `datadog-api-key`

**App Key** (required only when Datadog Operator is enabled):
- AWS Secret: `datadog-app-keys-integration`
- Lookup: `<projectName>-non-prod` (e.g., `feedlot-non-prod`, `sensehub-non-prod`)
- K8s Secret: `datadog-app-key`
- Only created when `datadog.datadog.operator.enabled: true` in the cluster values

### How Project Names Are Resolved

The `projectName` and `env` for each cluster come from `configuration/datadog-project-mappings.yaml` — the single source of truth. This file is loaded as a values file into the `datadog-secrets` chart by the ApplicationSet.

The chart looks up the cluster name in the mapping to find the correct projectName:
- `ark-dev-eks` → `projectName: ark`, `env: dev`
- `sh-srvc-dev-eks` → `projectName: sensehub`, `env: dev`
- `devops-argocd-addons-dev-eks` → `projectName: devops-argocd-cluster-addons`, `env: dev`

If a cluster is not in the mapping, it falls back to `clusterGlobalValues.projectName` from the per-cluster values file.

### Enabling the Datadog Operator

The Datadog Operator is **disabled by default** (set in global values). To enable it on a cluster:

1. Add the app key to AWS Secrets Manager (`datadog-app-keys-integration`) with key `<projectName>-non-prod`
2. Set `operator.enabled: true` in the cluster's per-cluster values file:
   ```yaml
   datadog:
     datadog:
       operator:
         enabled: true
   ```
3. The ApplicationSet automatically detects this and creates the app key ExternalSecret

### Architecture

```
ApplicationSet (datadog)
  └── sources:
        - Source 1: Datadog Helm chart (agent, cluster-agent, operator)
        - Source 2: datadog-secrets chart (API key + App key ExternalSecrets)
            valueFiles:
              - per-cluster values (for clusterGlobalValues)
              - datadog-project-mappings.yaml (for projectName lookup)
            parameters:
              - appKey.enabled (from operator.enabled in cluster values)
        - Source 3: values reference
```

---

## References

- [Datadog ArgoCD Integration](https://docs.datadoghq.com/integrations/argocd/)
- [Datadog ArgoCD Blog](https://www.datadoghq.com/blog/argo-cd-datadog/)
- [ArgoCD Metrics Documentation](https://argo-cd.readthedocs.io/en/stable/operator-manual/metrics/)
- [Integration Source Code (metrics.py)](https://github.com/DataDog/integrations-core/blob/master/argocd/datadog_checks/argocd/metrics.py)
- [argo-helm #2363 — Datadog container naming for Helm chart](https://github.com/argoproj/argo-helm/issues/2363)

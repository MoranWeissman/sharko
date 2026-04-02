# Application Operations Observability - Investigation Findings

**User Story:** #403540 - [Cluster-Addons] Investigation & Requirements
**Date:** 2026-03-11

---

## Executive Summary

We investigated ArgoCD's observability capabilities for monitoring Application sync operations across 50+ EKS clusters managed via ApplicationSets. ArgoCD exposes comprehensive Prometheus metrics covering sync status, health, reconciliation performance, and cluster connectivity. These can be collected by Datadog's ArgoCD integration via OpenMetrics scraping.

**Key findings:**
- ArgoCD provides all metrics needed for sync monitoring via Prometheus endpoints
- The ArgoCD API stores only the last 10 sync operations per app (configurable) and only the most recent operation's error details
- Sync failures categorize into 9 distinct types, each identifiable via log patterns
- Datadog's built-in ArgoCD integration covers basics but custom dashboards are needed for fleet-scale operations
- Implementation is straightforward: 1-2 weeks for full setup

---

## Investigation Questions Answered

### Q1: What metrics are available for sync monitoring?

ArgoCD exposes metrics via Prometheus endpoints on each component:
- **`argocd_app_sync_total`** -- sync count by phase (Succeeded/Failed/Error)
- **`argocd_app_info`** -- real-time sync and health status per app
- **`argocd_app_reconcile`** -- reconciliation duration histogram
- **`argocd_cluster_connection_status`** -- cluster reachability
- **`argocd_appset_info`** -- ApplicationSet status

Full catalog in [APPLICATION_REQUIREMENTS.md](./APPLICATION_REQUIREMENTS.md#1-argocd-metrics-catalog).

### Q2: What sync history is available via the API?

- `status.history[]` stores last 10 completed syncs (revision, timestamps, source)
- `status.operationState` stores the most recent operation with full error details
- **Limitation:** No long-term history database. Must capture events externally.

Details in [APPLICATION_REQUIREMENTS.md](./APPLICATION_REQUIREMENTS.md#3-argocd-api-for-sync-history).

### Q3: How do sync failures categorize?

9 categories identified with distinct log patterns and resolution procedures:
1. Manifest Generation (Helm errors) -- 20%
2. K8s API Errors -- 10%
3. RBAC Permissions -- 25%
4. Admission Webhooks -- 10%
5. Hook Failures -- 8%
6. Cluster Connectivity -- 5%
7. Timeouts -- 15%
8. Resource Conflicts -- 5%
9. Secret-Related -- 2%

Full taxonomy in [SYNC_FAILURE_CATEGORIZATION.md](./SYNC_FAILURE_CATEGORIZATION.md).

### Q4: What dashboards do we need?

4 dashboards recommended:
1. **Operations Overview** -- Fleet health at a glance
2. **Sync Performance** -- Duration trends, failure rates, deployment frequency
3. **Failure Analysis** -- Top offenders, error categories, log streams
4. **Troubleshooting** -- Per-app deep dive

Specifications in [APPLICATION_REQUIREMENTS.md](./APPLICATION_REQUIREMENTS.md#5-dashboard-requirements).

### Q5: What alerts should we configure?

8 monitors with defined thresholds:
- Out-of-Sync > 30 min, Degraded > 15 min, Multiple Degraded (critical)
- Cluster disconnected (critical)
- Reconciliation slow (p95 > 10s, p99 > 30s)
- High failure rate (> 5% over 1 hour)
- Queue depth growing

SLO targets: 99% sync success rate, 99% healthy resources.

Details in [APPLICATION_REQUIREMENTS.md](./APPLICATION_REQUIREMENTS.md#6-alert-requirements).

---

## Current State Assessment

| Area | Status | Impact |
|---|---|---|
| Metrics collection | Not configured | No data for dashboards or alerts |
| Sync alerting | None | Rely on user reports, MTTD 15-30 min |
| Failure categorization | Manual | Engineers search logs manually, MTTR 30-60 min |
| Fleet visibility | None | Cannot answer "are all apps healthy?" |
| Performance tracking | None | Cannot detect degradation trends |

---

## Proposed Solution Architecture

```
ArgoCD Components                  Datadog
+---------------------------+      +---------------------------+
| app-controller:8082       |----->| OpenMetrics Scraping      |
| api-server:8083           |----->|   argocd.* metrics        |
| repo-server:8084          |----->|                           |
+---------------------------+      +---------------------------+
                                           |
ArgoCD Logs                                v
+---------------------------+      +---------------------------+
| application-controller    |----->| Log Pipeline              |
| repo-server               |----->|   Failure categorization  |
+---------------------------+      |   @failure_category facet |
                                   +---------------------------+
                                           |
ArgoCD Notifications                       v
+---------------------------+      +---------------------------+
| on-sync-failed            |----->| 4 Dashboards              |
| on-health-degraded        |      | 8 Monitors/Alerts         |
| on-deployed               |      | SLO Tracking              |
+---------------------------+      +---------------------------+
```

---

## Success Metrics

| Metric | Current | Target | Measurement |
|---|---|---|---|
| MTTD (sync failures) | 15-30 min | < 5 min | Time from failure to alert |
| MTTR (sync failures) | 30-60 min | 15-30 min | Time from alert to resolution |
| Fleet visibility | 0% | 100% | All apps on dashboard |
| Proactive detection | 0% | 80%+ | Failures caught by alerts vs user reports |

---

## Implementation Roadmap

| Phase | Effort | Deliverable |
|---|---|---|
| 1. Metrics Collection | 2-3 hours | Datadog OpenMetrics config for ArgoCD |
| 2. Log Processing | 4-6 hours | Log pipelines with failure categorization |
| 3. Dashboards | 1-2 days | 4 Datadog dashboards |
| 4. Alerts | 1 day + baseline | 8 monitors + SLO tracking |
| 5. Documentation | 2-3 days | Runbooks + training materials |

**Total:** 1-2 weeks

---

## Risks & Mitigations

| Risk | Mitigation |
|---|---|
| Alert fatigue from noisy thresholds | 1-2 week baseline collection before enabling alerts |
| Custom metrics cost (~$530/month) | Filter to operationally significant metrics only |
| ArgoCD version differences | Document version-specific metric availability |
| Log pipeline false positives | Iterative tuning of grok patterns |

---

## Cost Analysis

| Item | Monthly | Annual |
|---|---|---|
| Custom metrics (~530) | $530 | $6,360 |
| Log indexing (estimated) | $200 | $2,400 |
| **Total** | **$730** | **$8,760** |

**ROI:** MTTR reduction saves ~20-40 hours/month of SRE time. At $100/hour = $2,000-$4,000/month savings. ROI: 3-5x.

---

## Acceptance Criteria

- [ ] All ArgoCD component metrics collected in Datadog
- [ ] Sync failures automatically categorized in logs
- [ ] 4 dashboards operational with real data
- [ ] 8 alerts configured with tuned thresholds
- [ ] MTTD reduced to < 5 minutes (measured over 30 days)
- [ ] Fleet health answerable in < 30 seconds via dashboard

# Status Vocabulary

This page is the contract for every status name and color in the Sharko UI.
One name per state, one state per name, one color per severity. Future UI
work follows this table — if a screen needs a new state, add it here first.

The code-side source of truth for the cluster-connection states is
`ui/src/lib/clusterStatus.ts`; the test ladder and addon states live in
`ui/src/components/StatusBadge.tsx`.

## Color law

| Color | Means | Never used for |
|-------|-------|----------------|
| Green family (green / teal / emerald) | Working as intended | Warnings |
| Blue | A change is in progress — wait | Errors |
| Amber | Needs your attention, not broken | Good states |
| Red | A problem — act now | Anything benign |
| Gray / blue-tinted neutral | Inactive or no information yet | Problems |

Purple is retired. It used to mean both "best state" (Operational) and a
warning (Not in Git) at the same time.

## Cluster connection (ArgoCD → cluster)

Shown on: Clusters table and cards, Dashboard cluster cards, the Clusters
stat cards, and the Clusters legend. This is ArgoCD's own connection to the
cluster.

| Name | Color | Meaning |
|------|-------|---------|
| **Connected** | Green | ArgoCD is connected to this cluster. |
| **Connecting…** | Neutral | Waiting for ArgoCD's first connection result — normal for about a minute after registering. |
| **Not managed** | Amber | In ArgoCD but not in Sharko's Git catalog — adopt it to let Sharko manage its addons. |
| **Disconnected** | Red | ArgoCD tried to reach this cluster and failed. |

Each cluster row shows **one composite status pill**. The pill shows the
worst of the cluster's status parts (ArgoCD connection, deploy check,
Sharko test); click it for the full breakdown. Worst-first order:
**problem → attention → in progress → unknown → good**.

## Cluster test ladder (Sharko → cluster)

Shown on: the Cluster Detail header and test results. These are Sharko's own
test results — they can differ from ArgoCD's connection above.

| Name | Color | Meaning |
|------|-------|---------|
| **Unknown** | Neutral | Not tested yet. Run a test to verify connectivity. |
| **Connected** | Green | Sharko reached the cluster directly and confirmed it can create and manage secrets there. |
| **Verified** | Teal | A test deployment went through ArgoCD successfully — the full deploy path works. |
| **Operational** | Emerald | At least one addon is deployed and healthy on this cluster. |
| **Unreachable** | Red | The last connection test failed. Check IAM and network access. |

## Deploy check (test workload through ArgoCD)

Shown inside the composite pill breakdown and on Cluster Detail.

| Name | Color | Meaning |
|------|-------|---------|
| **Verified** | Green | The check passed — the deploy path to this cluster works. |
| **Running…** | Neutral/blue | The check is still rolling out — usually under a minute. |
| **Failed** | Amber | The test workload could not be deployed to this cluster. |

## Addon deployment coverage (Addons page)

Shown on: catalog tiles, the catalog table, and the catalog stat cards.

| Name | Color | Meaning |
|------|-------|---------|
| **Not deployed yet** | Neutral | In your catalog, not enabled on any cluster yet. Benign — nothing is wrong. |
| **Waiting to deploy** | Amber | Enabled on at least one cluster, but nothing is running yet. |
| **Deploying…** | Blue | A first rollout is in progress. |
| **Running on N clusters** | Green | Deployed and healthy everywhere it's enabled. |
| **Running on N/M clusters** | Blue | Partial coverage — running on N of the M clusters that enabled it. |
| **Sync failing** | Red | ArgoCD keeps failing to sync this addon somewhere. |
| **Missing from ArgoCD** | Red | Enabled in the catalog but ArgoCD has no matching app — needs a look. |

"Not deployed yet" and "Missing from ArgoCD" replaced the old
"Catalog Only" label, which meant both of these very different things.

## Addon application health (per cluster)

Shown on: addon detail, cluster detail, dashboards.

| Name | Color | Meaning |
|------|-------|---------|
| **Healthy** / **Synced** | Green | Running as intended. |
| **Progressing** / **Deploying** | Blue | ArgoCD is rolling out a change or waiting on a workload. |
| **Not Enabled** | Neutral | In the catalog but not enabled on this cluster. |
| **Not managed** | Amber | Running in ArgoCD but not tracked in Sharko's catalog. |
| **Degraded** / **Sync failing** | Red | Unhealthy or failing to sync — act now. |
| **Missing from ArgoCD** | Red | Enabled but ArgoCD has no Application for it. |
| **Unknown** | Neutral | ArgoCD hasn't reported health yet. |

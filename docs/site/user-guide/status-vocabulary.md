# Status Vocabulary

This page lists every status name and color you'll see in Sharko, and what each one means.

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
cluster. The legend on the Clusters page writes out the meaning of every
name below, so you do not need to hover over anything.

| Name | Color | Meaning |
|------|-------|---------|
| **Connected** | Green | ArgoCD is connected to this cluster. |
| **Connecting…** | Neutral | Waiting for ArgoCD's first connection result — normal for about a minute after registering. |
| **Not checked yet** | Neutral | ArgoCD only checks a cluster once something is deployed to it, and nothing is deployed here yet. Enable an addon and ArgoCD will check the connection. |
| **Not connected** | Amber | ArgoCD has no connection for this cluster at all. It may be unreachable from the hub (common for local or kind clusters), or its registration didn't finish. Check the cluster is reachable, then re-run Test connection. |
| **Available to manage** | Indigo | ArgoCD connects to it, but Sharko does not manage it yet. Adopt it or take ownership of it to let Sharko manage its addons. It has its own color because it is an offer, not a fault. |
| **Disconnected** | Red | ArgoCD tried to reach this cluster and failed. |

"Not connected" and "Connecting…" look similar but mean different things:
**Connecting…** is the normal first-minute wait for ArgoCD's first probe result on
a cluster it does have a connection for — it resolves on its own. **Not connected**
means ArgoCD never got a connection for the cluster in the first place, so nothing
is going to resolve it on its own — go check reachability and re-test. **Disconnected**
(red) is one step further still: ArgoCD had a connection and *tried* to reach the
cluster, and that attempt failed.

Each cluster row shows **one status pill**. The pill shows the worst of the
cluster's three status lines. Click it and the popover always lists the same
three lines:

| Line | What it says |
|------|--------------|
| **ArgoCD → cluster** | The connection state from the table above, with its meaning. |
| **Deploy check** | **Passed** ("A small test app was deployed to this cluster and it worked."), **Running…** ("The deploy check is still rolling out — usually under a minute."), **Failed** ("The test workload could not be deployed to this cluster."), **Not needed** ("ArgoCD already confirms it can reach this cluster, so no deploy check was needed."), or **Not run** ("No deploy check has run for this cluster."). |
| **Sharko → cluster** | **Reachable** ("Sharko can reach this cluster directly."), **Test failing** ("Sharko's last direct connection test failed."), or **Not run** ("Sharko has not tested this cluster yet. Use Test connection to run it."). |

A line that says **Not needed** or **Not run** is only shown for information. It never changes
the color of the pill. After you press **Test connection** and it passes, the
third line says **Reachable**, with how many checks passed.

Worst-first order: **problem → attention → in progress → unknown → good**.

## Cluster test ladder (Sharko → cluster)

Shown on: the Cluster Detail header and test results. These are Sharko's own
test results — they can differ from ArgoCD's connection above.

| Name | Color | Meaning |
|------|-------|---------|
| **Unknown** | Neutral | Not tested yet. Run a test to verify connectivity. |
| **Connected** | Green | Sharko reached the cluster directly and confirmed it can create and manage secrets there. |
| **Verified** | Teal | A test deployment went through ArgoCD successfully — the full deploy path works. |
| **Operational** | Emerald | At least one addon is deployed and healthy on this cluster. |
| **Unreachable** | Red | The last connection test failed. Check the cluster address, its sign-in details and the network. |

## Deploy check (test workload through ArgoCD)

Shown inside the status pill popover. The Cluster Detail page does not show a **Passed** word. Its header shows a green **Connectivity verified ✓** badge instead.

The popover says **Passed** only when a test app was really deployed through ArgoCD. If the cluster is verified only because ArgoCD itself connected to it, the badge on Cluster Detail is green, and the popover line says **Not needed**, because ArgoCD already confirms it can reach the cluster and no test app had to be deployed.

| Name | Color | Meaning |
|------|-------|---------|
| **Passed** | Green | A small test app was deployed to this cluster and it worked. The deploy path works. |
| **Running…** | Neutral/blue | The check is still rolling out — usually under a minute. |
| **Failed** | Amber | The test workload could not be deployed to this cluster. |
| **Not needed** | Neutral | ArgoCD already confirms it can reach this cluster, so no deploy check was needed. |
| **Not run** | Neutral | No deploy check has run for this cluster. |
| **Not known** | Neutral | Sharko got a deploy check result this page does not know how to show. This can happen when the server is newer than the page. |

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

![Addon lifecycle: Not deployed yet, then Waiting to deploy once enabled, then Deploying…, then either Running on N/M clusters or straight to Running on N clusters; from there, ArgoCD sync problems lead to Sync failing or Missing from ArgoCD, both of which return to Running on N clusters once resolved, and disabling everywhere returns to Not deployed yet.](../assets/diagrams/06-addon-lifecycle.drawio.svg)

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

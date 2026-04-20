# Dashboard

The dashboard is Sharko's home screen. It rolls up cluster connectivity, addon
health, recent sync activity, and pull-request flow into a single page. This
document explains the **state semantics** the dashboard uses — they are the
same semantics every other Sharko view applies, sourced from a single
in-browser cache.

## Unified addon state model

Sharko polls ArgoCD for application health and surfaces it across the UI
through one shared cache (`useAddonStates`). Every view that shows an addon's
status — Dashboard, Cluster Detail, Addon Detail, Marketplace — reads from
this cache, so they cannot disagree.

The cache buckets every addon-on-cluster into one of five **display states**:

| Display state          | When                                                     | Color      | Meaning for the operator                                       |
|------------------------|----------------------------------------------------------|------------|----------------------------------------------------------------|
| `healthy`              | ArgoCD reports `Healthy` (Synced or OutOfSync)           | Green      | Operational. Nothing to do.                                    |
| `progressing-advisory` | ArgoCD reports `Progressing`                             | Blue       | **Not** a hard error. ArgoCD is rolling out a change or waiting on a workload. Click to investigate but the app is still considered healthy. |
| `degraded`             | ArgoCD reports `Degraded`, `Suspended`, or `Error`       | Red        | Real failure. Look at the conditions in Cluster Detail.        |
| `missing`              | Application missing in ArgoCD                            | Red        | Application not present in ArgoCD. Likely a sync that never ran. |
| `unknown`              | ArgoCD can't determine state                             | Red        | Unsafe default — treat as needing investigation.               |

### Why "Progressing" is split out

A previous version of Sharko bucketed `Progressing` apps into the red "Apps
with issues" widget. That over-stated the urgency: ArgoCD often reports
`Progressing` for transient reasons (a new ReplicaSet rolling out, a webhook
waiting on a CRD). Those apps are still healthy.

The dashboard now shows two separate widgets:

* **Apps with issues** — `degraded`, `missing`, `unknown` only.
* **Progressing — usually temporary** — a smaller, blue panel listing apps in
  `progressing-advisory`. Each entry is a clickable link to the addon-on-cluster
  page so you can dig in without leaving the dashboard.

### Quick-ref links

Every addon name in the Issues and Progressing widgets is a link to
`/clusters/<cluster>?section=addons&addon=<name>`. That URL deep-links into the
Cluster Detail page, scrolls to the addon's row, and briefly highlights it. From
there you can:

* Open the values editor.
* Open the AI assistant scoped to that addon.
* Jump to the upstream ArgoCD application page.

## Pull Requests panel

Sharko opens a Git pull request for every change it makes (addon catalog edits,
cluster registration, values edits). The Dashboard's PR panel has two tabs:

* **Pending** — open PRs Sharko is currently tracking. Auto-refreshes every 30
  seconds and surfaces the merge moment via a toast.
* **Merged** — recently-merged PRs from the GitOps repo. Shows the PR title,
  author, merged-at timestamp, and a link to GitHub. Backed by a 60-second
  server cache to keep the GitHub API call cost bounded under typical PAT
  rate limits (5000 requests per hour).

  **Time window.** The Merged tab lists up to 20 PRs by default — whatever
  the GitHub API returns as the most recently closed for the configured
  repo. There is no fixed "last 30 days" cut-off; the cap is by count,
  not age. Increase the count via the `limit` query parameter on the
  underlying endpoint (`GET /api/v1/prs/merged?limit=50`, max 100) if you
  need to see further back.

  **Note (v1.21 QA Bundle 4):** a bug meant GitHub's list-PRs response
  was reporting merged PRs as plain `closed`, which left this tab empty
  until a new PR was merged through the GitHub API directly. Fixed by
  keying on GitHub's `merged_at` timestamp instead of the (unreliable-in-
  list-responses) `merged` boolean. Upgrade to 1.21 or later for the
  Merged tab to populate.

The current tab is preserved in the URL (`?prs_state=merged`) so deep-links
work — share a dashboard URL with `?prs_state=merged` to land on the merged
view directly.

## Cluster cards

The Dashboard surfaces only **clusters needing attention** (disconnected from
ArgoCD or with unhealthy addons). The full fleet lives in the Clusters page.

## Where to go from the Dashboard

The Dashboard is intentionally read-only. Use the deep-links to drive into
the page that owns the action:

* Click an addon name in the Issues or Progressing widgets → cluster detail page,
  scrolled to that addon's row, with the deep-link highlight applied.
* Open **Addons** in the left rail to discover new addons via the
  [Marketplace](marketplace.md), edit the catalog directly via
  [Managing Addons](addons.md), or open a chart's Values editor via
  [Editing Values](values-editing.md).
* Open a cluster card → cluster detail page, where per-cluster overrides and
  ArgoCD sync controls live.

## Notes on staleness

* The Dashboard polls every 30 seconds. The unified addon state cache uses the
  same interval; manual refresh is available via the refresh button in the
  hero banner.
* If the ArgoCD client becomes unreachable, the cache keeps the last-known
  state rather than flapping every panel to red. The "ArgoCD unreachable"
  banner appears at the top to make the staleness visible.

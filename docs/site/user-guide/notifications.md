# Notifications

Sharko surfaces important events through the notification bell in the top-right corner of the UI. Notifications are generated server-side and do not require external alerting infrastructure.

## Notification Types

| Type | Trigger |
|------|---------|
| **Upgrade available** | A newer version of a catalog addon is detected |
| **Drift detected** | A cluster is running a different addon version than the catalog target |
| **Cluster disconnected** | A cluster is unreachable or its ArgoCD application is in an error state |
| **Sync failed** | An ArgoCD application failed to sync |

## Viewing Notifications

Click the **bell icon** in the top navigation bar to open the notification panel. Unread notifications are indicated by a badge on the bell.

Each notification shows:

- The affected addon or cluster
- The nature of the issue (version mismatch, sync failure, etc.)
- A **Go to** link that navigates directly to the relevant page

## Upgrade Alerts

When a newer version of an addon is available (based on Helm chart version checks), Sharko generates an upgrade alert for each affected addon. The alert includes:

- Current version in the catalog
- Latest available version
- Number of clusters that would be affected by an upgrade

Click the alert to jump to the upgrade workflow. See [Upgrades](upgrades.md) for step-by-step instructions.

## Drift Alerts

Drift alerts are raised when the version running on a cluster diverges from the catalog target. This can happen when:

- A per-cluster override was applied and then superseded by a global upgrade
- A cluster was added after a global upgrade and inherited a stale version

The alert shows which clusters are drifted and by how much. Click **Resolve** to trigger the upgrade workflow for only the drifted clusters.

## Dismissing Notifications

Individual notifications can be dismissed by clicking the **×** on each item. Dismissed notifications do not reappear unless the underlying condition re-triggers.

To clear all notifications, click **Mark all as read** at the top of the notification panel.

## Notification Settings

Notifications are generated on every health check cycle. The check interval is controlled server-side and is not currently user-configurable. Future versions will support configurable alert channels (Slack, PagerDuty, email).

!!! note "External alerting"
    For production alerting (on-call, Slack channels), expose Sharko's health endpoint `/api/v1/health` and fleet status endpoint `/api/v1/fleet/status` to your existing monitoring stack (Prometheus, Datadog, etc.). These endpoints return structured JSON suitable for alert rules.

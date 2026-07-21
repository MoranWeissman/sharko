# GitOps Drift Detection and Self-Heal

Sharko is a GitOps agent for managing Kubernetes cluster registrations and addon labels through Git. For **Sharko-managed clusters**, Sharko now detects when the live cluster Secret's addon labels drift from what Git declares, reports an **OutOfSync** state, shows you exactly what changed, and optionally re-applies the Git-desired state.

This page covers how drift detection works, what you see in the UI, and the opt-in self-heal behavior.

## Two connection types

How drift is handled depends on who created the ArgoCD cluster Secret:

| Connection type | Who owns the Secret | Behavior |
|-----------------|---------------------|----------|
| **Self-managed** (`connectionManagedBy: user`) | You created the cluster Secret yourself, outside Sharko. Sharko only adds its addon labels. | Sharko already self-heals addon labels every ~30 seconds and warns you if it's fighting repeated reverts. This behavior is unchanged. |
| **Sharko-managed** | Sharko created the cluster Secret when you registered the cluster (the default registration flow). | **This is where the v3.0.0 drift detection applies.** Sharko now compares Git-desired addon labels vs live labels, reports **OutOfSync** when they differ, shows the drift diff, and can optionally self-heal. |

The rest of this page focuses on **Sharko-managed clusters** — that's where the new drift/sync surface applies.

## What is OutOfSync?

For a Sharko-managed cluster, Sharko reads the cluster entry from `configuration/managed-clusters.yaml` in your GitOps repository and compares the addon labels declared there against the addon labels on the live ArgoCD cluster Secret.

- **Synced**: the live addon labels match what Git declares (or the cluster has no addons enabled, so there's nothing to compare).
- **OutOfSync**: one or more addon label keys were **added**, **removed**, or **changed** outside Git — for example, if someone edited the ArgoCD Secret directly via `kubectl` or the ArgoCD UI.

**What gets compared:** only Sharko's addon label keys (the ones that say which addons are enabled — bare, unqualified keys like `cert-manager: "enabled"` or `datadog-version`). Ownership bookkeeping labels like `app.kubernetes.io/managed-by` are excluded from drift detection — they're Sharko internals, not part of your declared configuration.

**What does NOT get compared:** Secret Data (the actual kubeconfig), annotations, or non-addon labels. Drift detection is scoped to addon labels only.

## Seeing drift in the UI

The cluster detail page has a **Cluster secret sync** area (added in v3.0.0) that consolidates the sync status, the Sync action, and the drift diff:

- **Synced** (green): Git and live labels match. No drift to show.
- **OutOfSync** (amber): Git and live labels differ. A read-only diff appears below the status pill, showing which label keys were added (green lines), removed (red lines), or changed (both).
- **Sync failed** (red): Sharko tried to sync but encountered an error (check the cluster logs or diagnostics).
- **Reconciling** (blue): Sharko is currently re-syncing the cluster — wait a moment.

The drift diff uses the same rendering style as the [Preview Changes](clusters.md#preview-changes-before-a-pr-opens) diff: green for additions, red for removals. Unlike the preview diff (which shows a proposed PR's content), this is a **live state diff** — Git-desired on one side, live cluster state on the other.

**Secret values are never shown.** The diff only exposes label keys and values — no secret Data, no kubeconfig, no credentials.

**Who can see what:** The sync status pill and read-only drift diff are visible to all users (including viewers). Only the **Sync now** action button is restricted to admins and operators — viewers can see when a cluster is OutOfSync and what drifted, but cannot trigger the sync.

## The Sync action

The **Sync** button (or the automatic reconciliation tick, if self-heal is enabled) re-applies the Git-desired addon labels to the cluster Secret. This mirrors [ArgoCD's own Sync action](https://argo-cd.readthedocs.io/en/stable/user-guide/sync-options/), but for Sharko's cluster registration and addon labels rather than ArgoCD application manifests.

**What Sync does:**

1. Reads the cluster entry from `configuration/managed-clusters.yaml` (the Git truth).
2. Computes which addon labels should exist based on that entry.
3. Fully converges those labels onto the live ArgoCD cluster Secret — **only Sharko's addon label keys (bare, unqualified keys with no `/`) are touched**. This means addon labels that Git declares are added or updated, and addon labels that Git no longer declares are **deleted** from the live Secret (not left behind). Secret Data, annotations, and other labels remain unchanged.

**Honest reporting:** After a Sync (manual or automatic), Sharko re-reads the cluster Secret to verify the live state actually converged. It only reports **Synced** (or "drift corrected") if the live addon labels genuinely match what Git declares. If any drift remains after the write — for example, if a parallel process reverted the change, or if the ownership label was lost — Sharko reports **Sync failed** with the residual drift, never a false success.

**What Sync does NOT do:**

- Sync does NOT overwrite Secret Data (the kubeconfig).
- Sync does NOT touch annotations or non-addon labels.
- Sync does NOT block out-of-band edits at write time. This is **enforcement-by-reconcile**, not an admission webhook. If someone edits the Secret directly, that edit succeeds — Sharko detects the drift and can revert it on the next reconcile tick (if self-heal is on), but it doesn't prevent the write.

Clicking **Sync** triggers an immediate reconciliation for that cluster. If you don't click Sync, Sharko's reconciler runs every ~30 seconds anyway — so drift will be detected and (if self-heal is on) corrected automatically on the next tick.

## Opt-in self-heal (default OFF)

By default, Sharko only **detects and warns** about drift — it does NOT change the cluster automatically. The cluster stays OutOfSync until you click **Sync** or enable self-heal.

You can turn on automatic self-heal via the `managed_cluster_self_heal` setting (admin-only). When ON, Sharko re-applies Git-desired addon labels on every reconcile tick (~30s) without manual intervention. This is enforcement-by-reconcile — analogous to how ArgoCD applications auto-sync when you enable that feature.

**Default behavior (self-heal OFF):**

- Sharko detects drift and reports **OutOfSync**.
- The cluster page shows the drift diff.
- You click **Sync** when you're ready to revert the drift.

**With self-heal ON:**

- Sharko detects drift and reports **OutOfSync** (same as above).
- On the next reconcile tick (~30s), Sharko automatically re-applies Git-desired addon labels.
- The cluster returns to **Synced** without manual action.

Self-heal never touches Secret Data, annotations, or non-addon labels. It only reconciles the addon label keys Sharko owns.

!!! info "Self-heal is NOT an admission webhook"
    Self-heal is enforcement-by-reconcile — an out-of-band edit to the cluster Secret succeeds at write time, and Sharko reverts it on the next reconcile tick (if self-heal is on). It does NOT block the write. An admission-webhook-based hard lock is a future consideration (V3+ backlog), not part of v3.0.0.

## Self-managed vs Sharko-managed summary

| Aspect | Self-managed (`connectionManagedBy: user`) | Sharko-managed (default) |
|--------|-------------------------------------------|--------------------------|
| **Who created the ArgoCD cluster Secret** | You, outside Sharko. | Sharko, when you registered. |
| **Drift detection** | N/A — Sharko already self-heals addon labels every tick. | **Synced / OutOfSync** reported in UI (v3.0.0+). |
| **Drift diff** | N/A | Read-only diff of Git-desired vs live addon labels. |
| **Self-heal behavior** | Always on (pre-existing behavior, unchanged in v3.0.0). Sharko re-applies addon labels every ~30s and warns after repeated fights. | **Opt-in, default OFF.** Enable via `managed_cluster_self_heal` setting. |
| **What Sharko touches** | Only addon labels (never Data/annotations). | Only addon labels (never Data/annotations). |

## Limitations and honest framing

- **Labels only.** Drift detection and self-heal apply to Sharko's addon labels only — not Secret Data (the kubeconfig itself), not annotations, not other labels.
- **Connection Data is not reconciled.** Sharko reconciles the cluster Secret's **addon labels** from Git. The connection details themselves — the server URL, CA certificate, and auth token or exec config that live in the Secret's **Data** field — are **not declared in Git**, so Sharko has no Git-desired version to reconcile them against. If someone hand-edits or breaks the connection Data (wrong token, bad CA, changed server URL), Sharko does **not** auto-repair it. Instead, a broken connection surfaces through the cluster's connectivity checks, where you see the failure and fix it yourself. Connection Data is verified by diagnostics, not reconciled.
- **Ownership during self-heal: adopted clusters stay guests.** When Sharko self-heals a cluster it **owns** (Sharko created the Secret — it carries `app.kubernetes.io/managed-by: sharko`), it preserves and defensively re-applies its ownership label. When a cluster's Secret is **adopted** (another owner created it — e.g. a Helm chart or an ArgoCD Application manages that Secret, and Sharko is a guest on it), Sharko's self-heal converges only its own addon label keys and **never stamps its own ownership label** or touches the other owner's labels, annotations, or Data. Adopting a cluster does not make Sharko claim the Secret — Sharko stays a well-behaved guest and only keeps its addon labels correct.
- **Enforcement-by-reconcile, not a hard lock.** An out-of-band edit to the cluster Secret is detected and (if self-heal is on) reverted on the next reconcile tick, but it's not blocked at write time. An admission webhook for hard locking is a V3+ backlog item, not part of v3.0.0.
- **Self-managed clusters already self-heal.** The v3.0.0 drift detection surface (OutOfSync state, drift diff, opt-in self-heal) applies to **Sharko-managed clusters** only. Self-managed clusters continue to use the pre-existing self-heal behavior (always on, with fight detection).

## What's next

Once Sharko reports **Synced**, the cluster's addon labels match Git. Changes to addon labels (adding or removing addons via the UI or CLI) still go through a pull request — Sharko never mutates the GitOps repository directly. The sync surface only applies to the live ArgoCD cluster Secret, not to Git content.

For more about how Sharko manages clusters and addons through Git, see:

- [Managing Clusters](clusters.md) — registration, connection types, credentials
- [Managing Addons](addons.md) — enabling, upgrading, per-cluster values
- [Status Vocabulary](status-vocabulary.md) — what each status name and color means

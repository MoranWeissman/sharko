# ClusterAddons CRD Reference

> **Reference page for the `ClusterAddons` Custom Resource Definition.**
> This page documents the CRD's fields, kubectl usage, Phase 1 read-only
> behavior, and RBAC requirements. For broader operator development workflows,
> see [`operator-development.md`](../developer-guide/operator-development.md).

Sharko's operator mode exposes a `ClusterAddons` Custom Resource (`sharko.dev/v1alpha1`, Namespaced, shortName `ca`) that provides a native Kubernetes view of addon inventory per managed cluster. In **Phase 1** (default, flag off), the CRD is a **read-only status projection** — Sharko generates these CRs from `managed-clusters.yaml` and the controller writes `.status` to reflect each cluster's addon-reconcile outcome. In **Phase 2** (behind `SHARKO_OPERATOR_DRIVES_LABELS`, flag off by default), the controller DRIVES addon labels on the ArgoCD cluster Secret from the CR spec — ArgoCD's ApplicationSet reads those labels and deploys addons (Sharko stays a guest, not an owner).

---

## API Group and Version

```yaml
apiVersion: sharko.dev/v1alpha1
kind: ClusterAddons
```

**Scope:** Namespaced  
**Short name:** `ca`  
**Typical namespace:** `sharko` (same namespace as the Sharko server Deployment)

---

## Spec Fields

The `spec` section defines the cluster and its desired addon inventory.

| Field | Type | Description |
|-------|------|-------------|
| `cluster` | string | The managed cluster name (matches an entry in `managed-clusters.yaml`). |
| `addons` | array | List of addon definitions for this cluster. Each addon has `name` (string), `version` (string, optional), and `enabled` (bool). |

**Phase 1 note (flag off, default):** Sharko's generator writes the spec from `managed-clusters.yaml`. Users can `kubectl apply` a `ClusterAddons` CR manually, but the controller does not act on spec changes — it only reads the cluster's existing state and writes status.

**Phase 2 note (flag on, `SHARKO_OPERATOR_DRIVES_LABELS=true`):** The controller computes desired addon labels from `spec.addons` and writes them to the ArgoCD cluster Secret. Git stays the source of truth (the generator renders CRs from `managed-clusters.yaml`); enabling drive mode is a flag flip, not a repo recreate.

### Addon Entry Fields

Each entry in `spec.addons[]` has:

- **`name`** (string, required): The addon identifier (e.g. `argocd-image-updater`, `prometheus`).
- **`version`** (string, optional): The Helm chart version (e.g. `0.10.4`). Omit for global catalog default.
- **`enabled`** (bool, required): Whether the addon is enabled on this cluster.

---

## Status Fields

The `status` section is controller-written and reflects the cluster's addon-reconcile outcome.

| Field | Type | Description |
|-------|------|-------------|
| `observedGeneration` | int64 | The generation of the CR the controller last processed. |
| `lastReconcileTime` | metav1.Time | Timestamp of the most recent status update. |
| `syncedAddons` | int | Count of successfully synced addons (shown in the `SYNCED` printer column). |
| `conditions` | array | Standard Kubernetes Condition array. Includes a `Ready` condition. |

### Ready Condition

The `Ready` condition (`type: "Ready"`) summarizes the cluster's addon health. The condition's `reason` value and interpretation depend on which mode the operator is running in (flag off = Phase 1, flag on = Phase 2).

**Phase 1 reasons (flag off, default):**

- **`ReconcileSucceeded`** (True) — the canonical reconciler last ran successfully for this cluster; enabled addons are synced.
- **`ReconcileFailed`** (False) — the canonical reconciler failed; see `message` for details.
- **`ReconcileSkipped`** (Unknown) — the reconciler skipped this cluster (e.g., secret not found, drift detected).
- **`NoReconcileRecord`** (Unknown) — the reconciler has not run for this cluster yet.

**Phase 2 reasons (flag on, `SHARKO_OPERATOR_DRIVES_LABELS=true`):**

- **`LabelsApplied`** (True) — addon labels successfully written to the ArgoCD cluster Secret (or already converged).
- **`SecretNotFound`** (False) — the ArgoCD cluster Secret is missing or not managed by Sharko (the controller did not write).
- **`LabelSyncFailed`** (False) — the label write to the Secret failed; see `message` for the error.
- **`ConfigError`** (False) — the controller is misconfigured (internal error).

---

## kubectl Usage

### List all ClusterAddons

```bash
kubectl get clusteraddons
# or short form:
kubectl get ca
```

**Output columns:**

- `CLUSTER` — the cluster name (from `spec.cluster`)
- `SYNCED` — count of synced addons (from `status.syncedAddons`)
- `READY` — the Ready condition status (`True`, `False`, `Unknown`)
- `AGE` — time since the CR was created

Example:

```
NAME        CLUSTER     SYNCED   READY   AGE
prod-eu     prod-eu     12       True    3d
staging-us  staging-us  8        False   2d
```

### Describe a ClusterAddons CR

```bash
kubectl describe clusteraddons prod-eu
```

This shows:
- Full spec (cluster + addons list)
- Status (observedGeneration, lastReconcileTime, syncedAddons, conditions)
- Events (if any controller or admission errors occurred)

### Get a ClusterAddons CR as YAML

```bash
kubectl get clusteraddons prod-eu -o yaml
```

Use this to inspect the full CR structure, including status conditions and addon version details.

---

## Phase 1 Behavior (Read-Only Status View, Flag Off — Default)

In Phase 1 (flag off, `SHARKO_OPERATOR_DRIVES_LABELS` unset or `false`), Sharko's operator mode:

1. **Generates ClusterAddons CRs** from `managed-clusters.yaml` — one CR per cluster, created automatically by the generator on startup or when managed-clusters.yaml changes.
2. **Writes `.status` only** — the controller reads the cluster's addon-reconcile outcome (which addons are synced, which are degraded) from the existing reconciler (`internal/clusterreconciler`) and projects it as status fields.
3. **Does NOT act on spec changes** — if you `kubectl apply` a modified ClusterAddons CR with new addons in the spec, the controller ignores it. The existing Git-based PR workflow (`managed-clusters.yaml` → PR → merge → reconcile) is still the source of truth for addon deployments.

**What Phase 1 gives you:**

- Native `kubectl get ca` / `kubectl describe ca <name>` view of addon inventory.
- Status conditions integrated with standard Kubernetes tooling (e.g. kubectl wait, monitoring tools that watch Condition arrays).
- A stable CRD that will remain compatible when Phase 2 flips control (spec-driven addon-label convergence).

**What Phase 1 does NOT do:**

- Drive addon labels or modify ArgoCD cluster Secrets (that's Phase 2, behind a flag).
- Prune addons based on spec changes (still GitOps-driven via managed-clusters.yaml).

---

## Phase 2 Behavior (Drive Mode, Flag On — Gated)

In Phase 2 (flag on, `SHARKO_OPERATOR_DRIVES_LABELS=true` — **default off**), the controller DRIVES addon labels on the ArgoCD cluster Secret from the CR spec. This is an opt-in feature-flag; existing Phase 1 behavior (read-only status) is unchanged for clusters with the flag off.

**What turns it on:**

- Helm: `operator.drivesLabels: true` in `charts/sharko/values.yaml`.
- Env var: `SHARKO_OPERATOR_DRIVES_LABELS=true` in the controller pod.

**What it does:**

1. **Computes desired addon labels** from `spec.addons` — for each addon with `enabled: true` (or `enabled` omitted), emit an addon label `<addon-name>: "enabled"`. For addons with `enabled: false`, omit the label (absence = off).
2. **Writes labels to the ArgoCD cluster Secret** via the safe merge primitive (`SyncManagedClusterLabels`) that PRESERVES: the `app.kubernetes.io/managed-by: sharko` ownership label, any foreign (`/`-qualified) labels, and the Secret Data (credentials). The controller converges ONLY the addon-key labels (add/update/remove).
3. **Ownership gate:** the controller ONLY writes Secrets already labeled `managed-by: sharko` (the bootstrap flow at register-time creates the Secret with this label). If the Secret is missing or not managed by Sharko, the controller reports `Ready=False` with reason `SecretNotFound` and does NOT write.
4. **ArgoCD deploys the addons** — ArgoCD's existing ApplicationSet (unchanged) reads the cluster Secret labels and deploys workloads. Sharko stays a guest on ArgoCD (guest-not-owner: Sharko writes labels, ArgoCD owns ApplicationSets and deployment).

**Single-writer guarantee:**

When the flag is ON, the old cluster reconciler (`internal/clusterreconciler`) YIELDS its managed-cluster addon-label write (so there is still exactly ONE writer of those labels). The reconciler KEEPS: drift detection (read-only watchdog + status source), the self-managed (`SyncLabelsOnly`, user-owned secret) path, and register-time bootstrap (Secret creation with ownership label).

**Eventually-consistent convergence:**

When you edit `managed-clusters.yaml` → merge PR → Sharko re-renders the CR → the controller converges the labels. This settles within a few seconds (not instant). Status shows `Ready=True` with reason `LabelsApplied` when the labels land; `Ready=False` with a clear reason (`SecretNotFound`, `LabelSyncFailed`) if the Secret is missing or the write fails.

**Git stays the source of truth:**

The generator renders CRs from `managed-clusters.yaml`. Enabling drive mode is a flag flip, NOT a repo recreate. Git is still the authoritative source; the controller is the executor that converges the Secret state to match what Git says.

**What Phase 2 does NOT do:**

- It does NOT create ArgoCD ApplicationSets from CRs (ArgoCD's existing ApplicationSet is unchanged; Sharko writes labels, ArgoCD reads labels).
- It does NOT deploy workloads directly (ArgoCD's ApplicationSet controller does that).
- It does NOT create the cluster Secret (the bootstrap flow at register-time does that — Phase 2 only writes labels on Secrets that already exist + are managed-by=sharko).

---

## RBAC Requirements

Sharko's controller runs with a least-privilege ClusterRole (`config/rbac/role.yaml`):

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: sharko-manager-role
rules:
  - apiGroups: ["sharko.dev"]
    resources: ["clusteraddons"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: ["sharko.dev"]
    resources: ["clusteraddons/status"]
    verbs: ["get", "update", "patch"]
  - apiGroups: ["coordination.k8s.io"]
    resources: ["leases"]
    verbs: ["get", "list", "watch", "create", "update", "patch"]
```

**Why these permissions:**

- **`clusteraddons` (full CRUD):** The generator creates CRs; the controller updates them; decommissioning a cluster deletes the CR.
- **`clusteraddons/status` subresource:** The controller writes `.status` on every reconcile loop.
- **`leases` (coordination.k8s.io):** Leader election for controller-runtime manager (multi-replica safety, though Phase 1 typically runs single-replica).

**ArgoCD namespace Secret write (Phase 2 adds NO new RBAC):** The operator runs embedded under the SAME service account as the Sharko server (there is no separate operator binary until a later phase). That service account already holds Secret write (get/list/watch/create/update/patch/delete) in the ArgoCD namespace (default `argocd`) via the existing `<release>-argocd-secrets` Role — granted UNCONDITIONALLY, because the cluster reconciler needs that write permission whether or not the flag is set. So enabling Phase 2 adds NO new RBAC grant: flipping `operator.drivesLabels` only sets the `SHARKO_OPERATOR_DRIVES_LABELS` env var on the Deployment. Phase 1 read-only behavior is enforced in CODE (the controller simply does not write when the flag is off), NOT by RBAC — the permission is present in both modes; only Phase 2 exercises it.

---

## Examples

### Sample ClusterAddons CR

A typical ClusterAddons CR (auto-generated by Sharko):

```yaml
apiVersion: sharko.dev/v1alpha1
kind: ClusterAddons
metadata:
  name: prod-eu
  namespace: sharko
spec:
  cluster: prod-eu
  addons:
    - name: argocd-image-updater
      version: "0.10.4"
      enabled: true
    - name: prometheus
      enabled: true
    - name: datadog
      version: "3.74.0"
      enabled: false
status:
  observedGeneration: 1
  lastReconcileTime: "2026-07-22T10:15:30Z"
  syncedAddons: 2
  conditions:
    - type: Ready
      status: "True"
      lastTransitionTime: "2026-07-22T10:15:30Z"
      reason: AllAddonsSynced
      message: "2 of 2 enabled addons synced successfully"
```

### Watch ClusterAddons status updates

```bash
kubectl get ca -w
```

As the controller reconciles, you'll see `SYNCED` counts and `READY` status change in real time.

### Wait for a cluster's addons to be ready

```bash
kubectl wait --for=condition=Ready clusteraddons/prod-eu --timeout=5m
```

This blocks until the `Ready` condition is `True` or the timeout expires. Useful in CI/CD pipelines that deploy Sharko + clusters and want to confirm addon sync before proceeding.

---

## Troubleshooting

### CR exists but status is never updated

**Symptom:** `kubectl describe ca <name>` shows `status: {}` or stale `lastReconcileTime`.

**Possible causes:**

1. **Controller not running:** Check `kubectl get pods -n sharko -l app.kubernetes.io/name=sharko`. If no pods or pods are CrashLoopBackOff, check logs (`kubectl logs -n sharko -l app.kubernetes.io/name=sharko`).
2. **CRD not installed:** The controller cannot watch a CRD that doesn't exist. Verify `kubectl get crd clusteraddons.sharko.dev` succeeds. If missing, run `make install` from the Sharko repo.
3. **RBAC misconfiguration:** The controller needs `get/list/watch` on `clusteraddons` and `update` on `clusteraddons/status`. Check `kubectl describe clusterrole sharko-manager-role`.

### Ready condition is False

**Symptom:** `kubectl get ca` shows `READY=False`.

**Action:** `kubectl describe ca <name>` and read the Ready condition's `message` field. It will name the addon(s) that failed to sync. Then check the addon's ArgoCD Application status (`kubectl get application -n argocd <addon-name>-<cluster-name>`) and ArgoCD's reconciler logs for the root cause.

### Spec changes are ignored (Phase 1 only)

**Expected behavior in Phase 1 (flag off, default):** The controller does not act on spec changes. To add/remove addons or change versions, edit `managed-clusters.yaml`, open a PR, and merge. The reconciler will create/update the ArgoCD cluster Secret, and the controller will update the CR's status to reflect the new state.

**Behavior in Phase 2 (flag on, `SHARKO_OPERATOR_DRIVES_LABELS=true`):** The controller DOES act on spec changes — it drives addon labels from the CR spec to the ArgoCD cluster Secret. However, Git is still the source of truth: the generator renders CRs from `managed-clusters.yaml`, so to change addons you still edit `managed-clusters.yaml` → merge PR → Sharko re-renders the CR → the controller converges the labels.

---

## Further Reading

- [Operator Development Guide](../developer-guide/operator-development.md) — local dev loop, CRD generation, testing CRs
- [Cluster Reconciler](cluster-reconciler.md) — the backend reconciler that Phase 1's status controller reads from
- [Kubebuilder Book](https://book.kubebuilder.io/) — general Kubernetes operator development

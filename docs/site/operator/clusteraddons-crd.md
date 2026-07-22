# ClusterAddons CRD Reference

> **Reference page for the `ClusterAddons` Custom Resource Definition.**
> This page documents the CRD's fields, kubectl usage, Phase 1 read-only
> behavior, and RBAC requirements. For broader operator development workflows,
> see [`operator-development.md`](../developer-guide/operator-development.md).

Sharko's operator mode exposes a `ClusterAddons` Custom Resource (`sharko.dev/v1alpha1`, Namespaced, shortName `ca`) that provides a native Kubernetes view of addon inventory per managed cluster. In **Phase 1** (current), the CRD is a **read-only status projection** — Sharko generates these CRs from `managed-clusters.yaml` and the controller writes `.status` to reflect each cluster's addon-reconcile outcome. The CRD does not drive addon deployments in Phase 1; a future phase will invert this so the CR spec becomes the source of truth.

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

**Phase 1 note:** Sharko's generator writes the spec from `managed-clusters.yaml`. Users can `kubectl apply` a `ClusterAddons` CR manually, but the controller does not act on spec changes yet — it only reads the cluster's existing state and writes status.

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

The `Ready` condition (`type: "Ready"`) summarizes the cluster's addon health:

- **`status: "True"`** — all enabled addons are synced.
- **`status: "False"`** — one or more addons failed to sync or have errors.
- **`status: "Unknown"`** — controller has not reconciled this CR yet, or reconcile data is stale.

The `reason` and `message` fields on the condition provide details.

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

## Phase 1 Behavior (Read-Only Status View)

In Phase 1, Sharko's operator mode:

1. **Generates ClusterAddons CRs** from `managed-clusters.yaml` — one CR per cluster, created automatically by the generator on startup or when managed-clusters.yaml changes.
2. **Writes `.status` only** — the controller reads the cluster's addon-reconcile outcome (which addons are synced, which are degraded) from the existing reconciler (`internal/clusterreconciler`) and projects it as status fields.
3. **Does NOT act on spec changes** — if you `kubectl apply` a modified ClusterAddons CR with new addons in the spec, the controller ignores it. The existing Git-based PR workflow (`managed-clusters.yaml` → PR → merge → reconcile) is still the source of truth for addon deployments.

**What Phase 1 gives you:**

- Native `kubectl get ca` / `kubectl describe ca <name>` view of addon inventory.
- Status conditions integrated with standard Kubernetes tooling (e.g. kubectl wait, monitoring tools that watch Condition arrays).
- A stable CRD that will remain compatible when Phase 2+ inverts the control flow (spec-driven deployments).

**What Phase 1 does NOT do:**

- Drive addon deployments or create ArgoCD ApplicationSets from the CR spec (that's Phase 2).
- Prune addons based on spec changes (still GitOps-driven via managed-clusters.yaml).

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

**No ArgoCD RBAC yet:** Phase 1 does not create ApplicationSets, so the controller does not need ArgoCD API permissions. Phase 2 will add those.

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

### Spec changes are ignored

**Expected behavior in Phase 1:** The controller does not act on spec changes. To add/remove addons or change versions, edit `managed-clusters.yaml`, open a PR, and merge. The reconciler will create/update the ArgoCD cluster Secret, and the controller will update the CR's status to reflect the new state.

Phase 2 will invert this — the CR spec will drive deployments directly.

---

## Further Reading

- [Operator Development Guide](../developer-guide/operator-development.md) — local dev loop, CRD generation, testing CRs
- [Cluster Reconciler](cluster-reconciler.md) — the backend reconciler that Phase 1's status controller reads from
- [Kubebuilder Book](https://book.kubebuilder.io/) — general Kubernetes operator development

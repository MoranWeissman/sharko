# Operator Development

This guide covers the local development loop for Sharko's Kubernetes operator mode (Phase 1+).

## Overview

Sharko is transitioning from an HTTP-only addon management server to a full Kubernetes operator. In operator mode, Sharko exposes Custom Resources (`ClusterAddons`) that provide a native Kubernetes view of addon inventory per cluster.

**Phase 1 (flag off, default):** The `ClusterAddons` CRD is live. Sharko's controller watches these CRs and writes a read-only `.status` projection of each cluster's addon state (which addons are synced, Ready condition). The CRD does not drive addon labels or ArgoCD Secrets — the canonical reconciler does that. The `make manifests`, `make install`, and `make deploy` targets are fully operational.

**Phase 2 (flag on, `SHARKO_OPERATOR_DRIVES_LABELS`):** The controller DRIVES addon labels on ArgoCD cluster Secrets from the CR spec. When the flag is ON, the controller computes desired labels from `spec.addons` and writes them to the Secret (via the safe merge primitive that preserves ownership label, foreign labels, and Secret Data). ArgoCD's ApplicationSet (unchanged) reads those labels and deploys addons. Sharko stays a guest on ArgoCD — Sharko writes labels, ArgoCD owns ApplicationSets and deployment.

## Persistent Dev Cluster

The operator development loop uses a PERSISTENT kind cluster named `sharko-operator-dev`. This cluster is DISTINCT from the throwaway e2e clusters (`sharko-e2e-*`) so `make test-e2e-clean` and `make kind-down` never delete it.

### Provision the dev cluster

```bash
make operator-dev-up
```

This target:
1. Creates `kind-sharko-operator-dev` cluster (if missing)
2. Installs ArgoCD from `stable`
3. Builds the local Sharko Docker image + loads it into the kind cluster
4. Helm-installs Sharko from `charts/sharko/` (local image, `pullPolicy: Never`)
5. Prints port-forward commands for Sharko and ArgoCD

**Idempotent:** Re-running on an existing cluster skips cluster creation and upgrades Sharko instead.

### Access Sharko and ArgoCD

After `make operator-dev-up`, run these in separate terminals:

```bash
# Sharko UI
kubectl --context kind-sharko-operator-dev port-forward -n sharko svc/sharko 8080:80
# Open http://localhost:8080

# ArgoCD UI
kubectl --context kind-sharko-operator-dev port-forward -n argocd svc/argocd-server 18080:443
# Open https://localhost:18080 (accept self-signed cert)
# Get ArgoCD admin password:
kubectl --context kind-sharko-operator-dev get secret -n argocd argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d
```

### Tear down the dev cluster

```bash
make operator-dev-down
```

This target deletes ONLY the `sharko-operator-dev` cluster. It guards by exact cluster name so it can NEVER touch other kind clusters (same safety discipline as `test-e2e-clean`).

## CRD Workflow (Phase 1+)

### Generate CRD YAML from Go types

```bash
make manifests
```

This target:
1. Installs `controller-gen` (from `sigs.k8s.io/controller-tools`) if missing
2. Runs `controller-gen crd rbac:roleName=sharko-manager-role paths=./...`
3. Writes CRD YAML to `config/crd/*.yaml`
4. Writes RBAC manifests to `config/rbac/*.yaml`
5. Copies generated CRDs to `charts/sharko/crds/` for Helm packaging

**Phase 1:** Generates `sharko.dev_clusteraddons.yaml` from `api/v1alpha1/clusteraddons_types.go`. After editing the Go type definitions or kubebuilder markers, run `make manifests` and commit the regenerated YAML.

### Apply CRDs to the cluster

```bash
make install
```

This target runs `kubectl apply -f config/crd/`. CRDs are applied to the CURRENT kubectl context. To target the operator dev cluster:

```bash
kubectl config use-context kind-sharko-operator-dev
make install
```

**Phase 1:** Installs the `sharko.dev/v1alpha1` `ClusterAddons` CRD. After running `make install`, you can create `ClusterAddons` CRs via `kubectl apply`.

### Remove CRDs from the cluster

```bash
make uninstall
```

This target runs `kubectl delete -f config/crd/`. **Warning:** Deleting CRDs also deletes all CRs of that type. Use with caution in production.

## Controller Deployment (Phase 1+)

### Apply controller RBAC + Deployment

```bash
make deploy
```

This target applies `config/rbac/*.yaml` + `config/manager/*.yaml` to the cluster. The controller Deployment runs the `sharko` binary with operator mode enabled.

**Phase 1:** Deploys the Sharko controller with a least-privilege ClusterRole (get/list/watch/update for `clusteraddons` + status subresource, plus lease coordination). The controller runs an embedded `controller-runtime` manager that reconciles `ClusterAddons` CRs and writes read-only `.status` updates.

### Remove controller RBAC + Deployment

```bash
make undeploy
```

## Testing Custom Resources

After `make install` (CRDs applied), test the operator with sample CRs:

```bash
# Apply a ClusterAddons sample (one cluster)
kubectl apply -f config/samples/clusteraddons_sample.yaml

# Watch the reconciler write status updates
kubectl get clusteraddons -w

# Describe the CR to see conditions and syncedAddons count
kubectl describe clusteraddons prod-eu

# Check reconciler logs
kubectl logs -n sharko -l app.kubernetes.io/name=sharko -f
```

**Phase 1 behavior (flag off, default):** The `ClusterAddons` CR shows a read-only view of addon state. The controller writes `.status` but does NOT write addon labels to ArgoCD Secrets. The canonical reconciler (`internal/clusterreconciler`) writes the labels.

**Phase 2 behavior (flag on, `SHARKO_OPERATOR_DRIVES_LABELS=true`):** The controller DRIVES addon labels from the CR spec to the ArgoCD cluster Secret. To observe this locally with `make operator-dev-up`, run the prove-then-flip loop below.

---

## Testing Phase 2 Drive Mode: Local Playground

The **operator playground** is a one-command local kind topology that provisions a hub cluster running ArgoCD + Sharko, plus N spoke clusters (default 2), all connected via GitFake. This lets you prove the Phase 2 label-drive mechanic end-to-end on your laptop — flip the `SHARKO_OPERATOR_DRIVES_LABELS` flag on and off, watch the `ClusterAddons` CR converge, and confirm the operator is writing addon labels to each spoke's ArgoCD cluster Secret.

**What this proves:** The operator driving addon labels from the CR spec when the flag is ON; the reconciler resuming ownership when the flag is OFF. Single-writer handoff, seen live.

**What it honestly can't prove:** Fetching real credential *values* from a cloud secrets backend (AWS Secrets Manager / Vault) — that code path needs a real cloud backend and is not exercisable locally yet. This playground uses GitFake (which only serves reads; no PR-opening path), so it also needs no real GitHub token. The addon-secret-values path stays an EKS/live concern. This is a laptop proof of the label-drive mechanic (which is what Phase 2 owns), not a full ArgoCD-deploys-workloads run.

### Step 1: Spin up the playground

```bash
make operator-playground-up
```

This command:
1. Creates or reuses a persistent hub kind cluster (`sharko-play-hub`) and N spoke clusters (`sharko-play-spoke-1..N`, default N=2).
2. Installs ArgoCD on the hub.
3. Installs Sharko on the hub via Helm with `operator.enabled=true`, `operator.drivesLabels=false` (starts inert), and a dummy git token (no real GITHUB_TOKEN required).
4. Deploys an in-cluster GitFake Pod seeded with `configuration/managed-clusters.yaml` (assigns ~2 addons across `spoke-eu` / `spoke-us`) plus the config files Sharko needs (`addons-catalog.yaml`, `default-addons.yaml`).
5. Registers the 2 spokes as **Sharko-managed** clusters via the REST API so each spoke's ArgoCD cluster Secret exists and carries `app.kubernetes.io/managed-by: sharko`.

**Override spoke count:** `PLAYGROUND_SPOKES=3 make operator-playground-up` to add more spokes.

**Idempotent:** Re-running on an existing playground upgrades in place.

At the end, the command prints access instructions (port-forward for Sharko UI + ArgoCD) and the exact next-step commands (`make operator-playground-status`, `make operator-playground-drive-on`).

### Step 2: Check the drive-OFF baseline

```bash
make operator-playground-status
```

This command prints:
- `ClusterAddons` CRs (name, cluster, SYNCED, Ready) and the Ready condition detail.
- Each spoke's ArgoCD cluster Secret addon labels (addon-key labels only — the labels with no `/` or `:` in the key).
- Current drive mode (operator vs reconciler as the label writer).
- A one-line summary: "DRIVE OFF — the reconciler is the label writer (operator is read-only status only)."

**Expected state with drive OFF:** The operator writes read-only `.status` updates to the `ClusterAddons` CRs. The reconciler (`internal/clusterreconciler`) writes addon labels to the ArgoCD cluster Secrets. The Ready condition reason is `ReconcileSucceeded` or `NoReconcileRecord` (Phase 1 reasons), NOT `LabelsApplied`.

### Step 3: Flip the switch ON — operator drives labels

```bash
make operator-playground-drive-on
```

This command:
1. Helm-upgrades the Sharko release with `--set operator.drivesLabels=true`.
2. Restarts the Sharko deployment to pick up the env change.
3. Waits for the rollout to complete.

At the end, it prints: "Operator is now DRIVING labels. The controller writes addon labels from the ClusterAddons CR spec. Watch convergence: `make operator-playground-status`."

**Watch convergence:**

```bash
make operator-playground-status
```

Now observe:
- The Ready condition reason is `LabelsApplied` (Phase 2 reason), not `ReconcileSucceeded`.
- The addon-key labels on each spoke's ArgoCD cluster Secret match the `ClusterAddons` spec.
- The summary line reads: "DRIVE ON — the operator is the label writer; addon labels present on N/N spokes."

The convergence settles within a few seconds (eventually consistent, not instant).

### Step 4: Flip the switch OFF — reconciler resumes (one-line rollback)

```bash
make operator-playground-drive-off
```

This command:
1. Helm-upgrades the Sharko release with `--set operator.drivesLabels=false`.
2. Restarts the Sharko deployment.
3. Waits for the rollout to complete.

At the end, it prints: "Operator drive OFF — reconciler resumes as the label writer. This is the one-line rollback. Labels remain correct. Watch state: `make operator-playground-status`."

**Verify:**

```bash
make operator-playground-status
```

Observe:
- The Ready condition reason reverts to `ReconcileSucceeded` (Phase 1 reason).
- The addon labels on the Secrets remain as-is (the operator does not delete them on flag flip).
- The summary line reads: "DRIVE OFF — the reconciler is the label writer (operator is read-only status only)."

The reconciler resumes ownership; the operator yields.

### Step 5: Tear down the playground

```bash
make operator-playground-down
```

This command deletes ONLY `sharko-play-*` kind clusters (hub + all spokes). It guards by exact name-prefix match so it can NEVER touch `sharko-e2e-*`, `sharko-operator-dev`, or any other cluster. Safe to run with nothing present (no-op clean exit).

---

### Advanced: Inspect the Kubernetes resources directly

The playground exposes the full Kubernetes state. Use these commands to inspect the resources directly:

```bash
# Switch to the hub context
kubectl config use-context kind-sharko-play-hub

# List all ClusterAddons CRs
kubectl get clusteraddons -A

# Describe a specific ClusterAddons CR (e.g., spoke-eu)
kubectl describe clusteraddons -n sharko spoke-eu

# Get the ArgoCD cluster Secret labels for a spoke
kubectl -n argocd get secret -l argocd.argoproj.io/secret-type=cluster --show-labels

# Check the controller logs
kubectl logs -n sharko -l app.kubernetes.io/name=sharko -f | grep "addon labels"

# Watch ClusterAddons CRs converge in real time
kubectl get clusteraddons -A -w
```

**Label-drive mechanic:** When drive is ON, the controller reads the `ClusterAddons` CR spec, computes desired addon labels via `internal/operator/labels.go` (`AddonAssignmentsToLabels`), and writes those labels to the spoke's ArgoCD cluster Secret via `argosecrets.SyncManagedClusterLabels` (a safe merge primitive that preserves the `app.kubernetes.io/managed-by: sharko` ownership label, Secret Data, and any foreign labels). ArgoCD's ApplicationSet (unchanged) reads those labels and deploys addons. Sharko stays a **guest on ArgoCD** — Sharko writes labels, ArgoCD owns ApplicationSets and deployment.

**ClusterAddonSet:** The sample `config/samples/clusteraddonset_sample.yaml` is for a future phase (Phase 2+) and is not yet wired. You can ignore it for now.

**Edit the samples:** Replace placeholder cluster names (`prod-eu`, `staging-us`) with your real cluster names (must match entries in `managed-clusters.yaml`).

## Helm Integration

The Helm chart in `charts/sharko/` has a top-level `crds/` directory. Helm applies CRDs from this directory BEFORE rendering templates. This ensures the Sharko Deployment (which may create CRs) does not fail due to missing CRDs.

### Populate Helm CRDs

After `make manifests` generates `config/crd/*.yaml`, copy them into the Helm chart:

```bash
cp config/crd/*.yaml charts/sharko/crds/
```

Commit the CRD YAML files to git. The next `helm install` or `helm upgrade` will include the CRDs.

**Helm CRD caveat:** Helm does NOT re-apply CRDs during `helm upgrade`. When the CRD schema changes, apply the updated CRDs manually (`make install`) before upgrading the Helm release. See `charts/sharko/crds/README.md` for details.

## Rebuild Loop (after code changes)

The `operator-dev-up` target installs Sharko via `scripts/helm-install.sh` (builds Docker image + Helm install). After code changes, rebuild + redeploy:

```bash
# Rebuild + re-deploy Sharko
make operator-dev-down && make operator-dev-up
```

Or use the faster `scripts/dev-rebuild.sh` flow (see `docs/site/developer-guide/personal-smoke-runbook.md`):

```bash
# Rebuild Docker image + rollout restart (no cluster teardown)
./scripts/dev-rebuild.sh --cluster sharko-operator-dev
```

## CI Integration (Phase 1+)

When CRDs land, add a CI check to verify they are up-to-date:

```yaml
# .github/workflows/ci.yml
- name: CRDs Up To Date
  run: |
    make manifests
    git diff --exit-code config/crd/ charts/sharko/crds/
```

This catches stale CRD YAML (same pattern as the existing `generate-schemas` check).

## Further Reading

- [Kubebuilder Book](https://book.kubebuilder.io/) — operator development guide
- [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) — the reconciler framework Sharko uses
- [ArgoCD ApplicationSet](https://argo-cd.readthedocs.io/en/stable/operator-manual/applicationset/) — the ArgoCD resource that reads cluster Secret labels and deploys addons (Sharko writes labels, ArgoCD owns ApplicationSets)
- [Helm CRD Best Practices](https://helm.sh/docs/chart_best_practices/custom_resource_definitions/)

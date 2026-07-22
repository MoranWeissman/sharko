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

## Testing Phase 2 Drive Mode (Prove-Then-Flip Loop)

This workflow verifies that enabling `SHARKO_OPERATOR_DRIVES_LABELS` flips the controller from read-only status (Phase 1) to driving addon labels (Phase 2), without recreating the cluster or repo.

**Prerequisite:** A running operator dev cluster with at least one managed cluster registered (so a `ClusterAddons` CR exists and an ArgoCD cluster Secret exists in the `argocd` namespace).

### Step 1: Verify Phase 1 (flag off, default)

```bash
# Context: kind-sharko-operator-dev
kubectl config use-context kind-sharko-operator-dev

# Get the ClusterAddons CR (replace `prod-eu` with your cluster name)
kubectl get clusteraddons prod-eu -o yaml

# Observe: status.conditions[Ready].reason is one of Phase 1's reasons
# (ReconcileSucceeded, ReconcileFailed, NoReconcileRecord)

# Get the ArgoCD cluster Secret labels
kubectl get secret -n argocd cluster-prod-eu -o jsonpath='{.metadata.labels}' | jq

# Observe: addon labels are present (e.g., prometheus: "enabled", datadog: "enabled")
# but they were written by the canonical reconciler, NOT the operator controller.
```

### Step 2: Flip the flag to enable drive mode

```bash
# Helm upgrade with the drive flag ON
helm upgrade sharko charts/sharko/ \
  --namespace sharko \
  --set operator.drivesLabels=true \
  --reuse-values

# Wait for the controller pod to restart
kubectl rollout status -n sharko deployment/sharko
```

### Step 3: Verify Phase 2 (flag on)

```bash
# Get the ClusterAddons CR again
kubectl get clusteraddons prod-eu -o yaml

# Observe: status.conditions[Ready].reason is now one of Phase 2's reasons
# (LabelsApplied, SecretNotFound, LabelSyncFailed)

# Check the controller logs for the Phase 2 reconcile path
kubectl logs -n sharko -l app.kubernetes.io/name=sharko -f | grep "addon labels synced"

# Observe a log line like:
# "addon labels synced to cluster Secret" cluster="prod-eu" changed=false syncedAddons=2 convergedKeys=2
# (changed=false means the labels were already correct; changed=true would appear if the controller wrote a diff)
```

### Step 4: Edit the CR spec and observe convergence

```bash
# Manually edit the ClusterAddons CR to enable a new addon (e.g., grafana)
kubectl edit clusteraddons prod-eu

# Add an addon entry:
#   - name: grafana
#     enabled: true

# Save and exit. The controller will reconcile immediately.

# Watch the Secret labels update in real time
kubectl get secret -n argocd cluster-prod-eu -o jsonpath='{.metadata.labels}' | jq -r '.grafana'
# Observe: "enabled" (the controller added it)

# Check the CR status
kubectl describe clusteraddons prod-eu
# Observe: Ready=True, reason=LabelsApplied, message includes "addon labels applied" or "already in sync"
```

This proves the controller is driving the labels from the CR spec. The convergence settles within a few seconds (not instant — eventually consistent).

### Step 5: Roll back to Phase 1 (one-line rollback)

```bash
# Helm upgrade with the flag back to OFF
helm upgrade sharko charts/sharko/ \
  --namespace sharko \
  --set operator.drivesLabels=false \
  --reuse-values

# Wait for rollout
kubectl rollout status -n sharko deployment/sharko

# Verify: the CR status.conditions[Ready].reason reverts to Phase 1 reasons
kubectl get clusteraddons prod-eu -o jsonpath='{.status.conditions[?(@.type=="Ready")].reason}'
# Observe: ReconcileSucceeded (not LabelsApplied)
```

The addon labels on the Secret remain as-is (the controller does not delete them on flag flip). The canonical reconciler resumes writing labels on the next merge to `managed-clusters.yaml`.

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

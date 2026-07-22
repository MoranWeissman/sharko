# Operator Development

This guide covers the local development loop for Sharko's Kubernetes operator mode (Phase 1+).

## Overview

Sharko is transitioning from an HTTP-only addon management server to a full Kubernetes operator. In operator mode, Sharko exposes Custom Resources (`ClusterAddons`) that provide a native Kubernetes view of addon inventory per cluster.

**Phase 1 (current):** The `ClusterAddons` CRD is live. Sharko's controller watches these CRs and writes a read-only `.status` projection of each cluster's addon state (which addons are synced, Ready condition). The CRD does not drive addon deployments yet — a future phase will invert this so the CR spec becomes the source of truth. The `make manifests`, `make install`, and `make deploy` targets are fully operational.

**Phase 2+:** Additional CRs (e.g. `ClusterAddonSet` for multi-cluster families) will land, and the controller will reconcile ArgoCD `ApplicationSets` from CR specs to deploy addons.

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

# Watch the reconciler write status updates (Phase 1: read-only status projection)
kubectl get clusteraddons -w

# Describe the CR to see conditions and syncedAddons count
kubectl describe clusteraddons prod-eu

# Check reconciler logs
kubectl logs -n sharko -l app.kubernetes.io/name=sharko -f
```

**Phase 1 note:** The `ClusterAddons` CR shows a read-only view of addon state. The controller writes `.status` but does NOT create ArgoCD `ApplicationSets` yet. A future phase will invert this.

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
- [ArgoCD ApplicationSet](https://argo-cd.readthedocs.io/en/stable/operator-manual/applicationset/) — the resource Sharko reconciles
- [Helm CRD Best Practices](https://helm.sh/docs/chart_best_practices/custom_resource_definitions/)

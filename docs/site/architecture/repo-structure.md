# Addons Repository Structure

The addons repository is a Git repository that Sharko manages. It holds configuration files that tell Sharko which addons run on which clusters. The Sharko engine — a Helm chart deployed once — reads these files and creates the ArgoCD Applications that deploy your addons.

## v4 Directory Layout

```
your-addons-repo/
  sharko-engine.yaml         # ArgoCD Application that deploys the engine
  managed-clusters.yaml      # List of clusters Sharko knows about
  catalog.yaml               # Approved addons and their chart details
  cluster-addons/
    prod-eu.yaml             # Which addons run on prod-eu
    staging.yaml             # Which addons run on staging
  values/
    global/
      cert-manager.yaml      # Base values for cert-manager (all clusters)
      monitoring.yaml        # Base values for monitoring
    clusters/
      prod-eu/
        cert-manager.yaml    # prod-eu overrides for cert-manager
        monitoring.yaml      # prod-eu overrides for monitoring
      staging/
        cert-manager.yaml    # staging overrides for cert-manager
  README.md                  # Bootstrap README
```

## The Engine Application

`sharko-engine.yaml` is the one ArgoCD Application that starts everything. It deploys the Sharko engine chart (published as an OCI Helm chart) onto your main cluster. That chart reads the other files in this repo — `managed-clusters.yaml`, `catalog.yaml`, and `cluster-addons/*.yaml` — and creates one ArgoCD Application per (cluster, addon) pair.

Sharko generates `sharko-engine.yaml` during `sharko init` and updates it when the engine version changes. The file is a plain Kubernetes manifest: an ArgoCD Application with `apiVersion`, `kind`, `metadata`, and `spec`.

## Cluster Registry (`managed-clusters.yaml`)

This file lists every cluster Sharko manages. Each entry holds the cluster's name, labels, region, and the location of its credentials in your secrets provider.

Example:

```yaml
apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: fleet
spec:
  clusters:
    - name: prod-eu
      region: eu-west-1
      labels:
        env: prod
    - name: staging-us
      region: us-east-1
      labels:
        env: staging
```

Sharko creates this file during init and adds to it when you register clusters.

## Catalog (`catalog.yaml`)

This file lists the addons your organization has approved for use. Each entry includes the Helm chart location, version, namespace, and any secrets the addon needs.

Example:

```yaml
apiVersion: sharko.dev/v1
kind: AddonCatalog
metadata:
  name: org-catalog
spec:
  addons:
    - name: cert-manager
      chart: cert-manager
      repo: https://charts.jetstack.io
      version: v1.14.5
      namespace: cert-manager
      installCRDs: true
    - name: monitoring
      chart: kube-prometheus-stack
      repo: https://prometheus-community.github.io/helm-charts
      version: 58.0.0
      namespace: monitoring
```

Sharko creates this file when you add your first addon via `sharko add-to-catalog` or `sharko add-addon` (v3 command).

## Cluster Addon Assignments (`cluster-addons/<cluster>.yaml`)

These files say which addons run on which clusters. Each cluster gets its own file. The engine chart reads all of them and creates the ArgoCD Applications.

Example `cluster-addons/prod-eu.yaml`:

```yaml
apiVersion: sharko.dev/v1
kind: ClusterAddons
metadata:
  name: prod-eu
spec:
  addons:
    - name: cert-manager
      enabled: true
      version: v1.14.5
    - name: monitoring
      enabled: true
```

Sharko writes these files when you register a cluster, enable an addon, or upgrade an addon on specific clusters.

## Values Files

### Global Values (`values/global/<addon>.yaml`)

Base Helm values applied to every cluster that runs the addon. Plain YAML, no envelope wrapper.

Example `values/global/cert-manager.yaml`:

```yaml
installCRDs: true
replicaCount: 1
resources:
  requests:
    cpu: 10m
    memory: 32Mi
```

Sharko creates this file when you add the addon to the catalog with base values, or when you edit global values via the UI.

### Per-Cluster Overrides (`values/clusters/<cluster>/<addon>.yaml`)

Overrides for a specific cluster. These values are merged on top of the global values file using Helm's values merge rules.

Example `values/clusters/prod-eu/cert-manager.yaml`:

```yaml
replicaCount: 2
resources:
  requests:
    cpu: 50m
    memory: 64Mi
```

Sharko creates this file when you register a cluster with per-cluster values, or when you edit a cluster's values via the UI.

## How the Engine Uses This Structure

The engine chart (deployed by `sharko-engine.yaml`) does this:

1. Reads `managed-clusters.yaml` to know which clusters exist
2. Reads `catalog.yaml` to know which addons are approved and where their charts live
3. Reads every `cluster-addons/<cluster>.yaml` file to know which addons are enabled on which clusters
4. For each (cluster, addon) pair where `enabled: true`, creates an ArgoCD Application that:
   - Installs the addon's Helm chart at the version from the cluster-addons file (or the catalog default)
   - Merges `values/global/<addon>.yaml` and `values/clusters/<cluster>/<addon>.yaml` as Helm value files
   - Targets the cluster named in `managed-clusters.yaml`

## Version Tracking

Addon versions are stored in `cluster-addons/<cluster>.yaml`, not in the catalog. The catalog holds a default version, but each cluster can pin its own. When you run `sharko upgrade-clusters cert-manager --version 1.15.0 --cluster prod-eu`, Sharko updates `cluster-addons/prod-eu.yaml` with the new version and opens a PR.

This means:
- Each cluster can run a different version of the same addon
- Version drift is visible by comparing cluster-addons files
- Upgrades are explicit — a cluster stays on its pinned version until you change it

## Sharko's Git Operations

All Git operations go through a mutex — one PR at a time, no concurrent writes.

| Operation | Git Action |
|-----------|-----------|
| `sharko init` | Creates the bootstrap files (`sharko-engine.yaml`, `managed-clusters.yaml`, empty folders), opens PR |
| `sharko add-to-catalog` | Adds an entry to `catalog.yaml`, opens PR |
| `sharko add-cluster` | Adds the cluster to `managed-clusters.yaml`, opens PR |
| `sharko enable-addon` | Updates `cluster-addons/<cluster>.yaml`, optionally writes `values/clusters/<cluster>/<addon>.yaml`, opens PR |
| `sharko disable-addon` | Sets `enabled: false` in `cluster-addons/<cluster>.yaml`, opens PR |
| `sharko upgrade-clusters` | Updates version pins in `cluster-addons/<cluster>.yaml` for selected clusters, opens PR |
| `sharko remove-cluster` | Deletes the cluster's entry from `managed-clusters.yaml` and its `cluster-addons/<cluster>.yaml`, opens PR |
| `sharko takeover` | Adds the cluster to `managed-clusters.yaml` and creates an empty `cluster-addons/<cluster>.yaml`, opens PR |

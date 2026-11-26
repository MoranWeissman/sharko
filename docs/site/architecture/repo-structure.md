# Addons Repository Structure

The addons repository is a Git repository that Sharko manages. It holds the files that say which addons your organization approved, which clusters Sharko manages, and which addons run on which cluster. The Sharko engine — a Helm chart deployed once — reads these files and creates the ArgoCD Applications that deploy your addons.

Every file below is plain YAML. The examples are copied from a real repository that Sharko wrote, so what you see here is what you will see in your own repo.

## v4 Directory Layout

```
your-addons-repo/
  sharko-engine.yaml         # ArgoCD Application that deploys the engine
  managed-clusters.yaml      # The clusters Sharko manages
  catalog.yaml               # The addons your organization approved
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

## When each file first appears

A new repository does not have all of these files. Sharko writes each file the first time it has something to put in it. A folder that holds only a `.gitkeep` file is empty on purpose.

| File | First appears when | What a pull request that creates it shows |
|------|--------------------|-------------------------------------------|
| `sharko-engine.yaml`, `README.md`, and the empty `cluster-addons/`, `values/global/` and `values/clusters/` folders | `sharko init` | The bootstrap pull request |
| `managed-clusters.yaml` | You register or adopt the first cluster | One new file with one cluster in it |
| `catalog.yaml` | You add the first addon to the catalog | One new file with one addon in it, plus the addon's global values file |
| `values/global/<addon>.yaml` | You add that addon to the catalog | Written in the same pull request as the catalog entry |
| `cluster-addons/<cluster>.yaml` | You turn the first addon on for that cluster (a taken-over cluster gets an empty one right away) | A new file with the one addon switched on. Registering a cluster does **not** create it. |
| `values/clusters/<cluster>/<addon>.yaml` | You turn that addon on for that cluster for the first time | A new file that holds only comments. Turning the addon on again later does not write it again. |

## The Engine Application

`sharko-engine.yaml` is the one ArgoCD Application that starts everything. It deploys the Sharko engine chart (published as an OCI Helm chart) onto your main cluster. That chart reads `catalog.yaml` and `cluster-addons/*.yaml` and creates one ApplicationSet for every addon in the catalog. Each ApplicationSet creates one ArgoCD Application for every cluster that has the addon turned on.

Sharko generates `sharko-engine.yaml` during `sharko init` and updates it when the engine version changes. The file is a plain Kubernetes manifest: an ArgoCD Application with `apiVersion`, `kind`, `metadata`, and `spec`. It is the only file in the repo with that shape. The other files use a simpler one.

## Cluster Registry (`managed-clusters.yaml`)

This file lists every cluster Sharko manages. Each entry holds the cluster's name and where its credentials come from. There is no `metadata:` or `spec:` block — `clusters:` sits at the top.

Example, copied from a real repo:

```yaml
# The clusters Sharko manages.
# Registering or removing a cluster through Sharko connects it to ArgoCD, or disconnects it.
# Day-to-day addon changes never touch this file — see cluster-addons/<cluster>.yaml for that.
# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/managed-clusters.v1.json
apiVersion: sharko.dev/v1
kind: ManagedClusters
clusters:
    - name: spoke-1
      secretPath: spoke-1
      credsSource: secret-kubeconfig
    - name: ep4-spoke-2
      credsSource: secret-kubeconfig
```

| Field | Meaning |
|-------|---------|
| `name` | The cluster's name (required). |
| `credsSource` | How Sharko gets the cluster's credentials: `inline-kubeconfig`, `secret-kubeconfig` or `eks-token`. |
| `secretPath` | Where the kubeconfig sits in your secrets provider. Left out when the name is enough. |
| `roleArn` | Optional AWS role to use for an `eks-token` cluster. |
| `region`, `labels` | Optional. Sharko only writes them when you set them. |
| `connectionManagedBy` | `user` when you made the ArgoCD connection yourself. Left out when Sharko made it. |

Addon changes never touch this file. Registering a cluster, adopting one or removing one does.

## Catalog (`catalog.yaml`)

This file lists the addons your organization approved. An addon that is not here cannot be turned on for any cluster. `addons:` is a map: the key is the addon's name. Each entry is complete on its own, so the pull request reviewer sees exactly what is entering the organization.

Example, copied from a real repo:

```yaml
# The addons your org has approved.
# Only an addon listed here can be turned on for a cluster.
# Adding an addon here happens through a pull request somebody reviews.
# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/catalog.v1.json
apiVersion: sharko.dev/v1
kind: AddonCatalog
addons:
    reloader:
        repoURL: https://stakater.github.io/stakater-charts
        chart: reloader
        version: 2.2.21
        namespace: reloader
```

Other fields an entry can carry: `settings` (sync options, self-heal, prune, `createNamespace` and similar), `secrets` (see [Addon Secrets](../user-guide/addons.md#addon-secrets)), `additionalSources` and `extraHelmValues`.

Sharko creates this file when you add your first addon with `sharko add-to-catalog` or with **Add to catalog** in the UI. The older `sharko add-addon` command writes to a different layout and is refused on a v4 repo.

## Cluster Addon Assignments (`cluster-addons/<cluster>.yaml`)

These files say which addons run on which clusters. Each cluster has at most one. The file name is the cluster name. `addons:` is a map, and the entry for an addon is `enabled: true` or `enabled: false`.

Example `cluster-addons/spoke-1.yaml`, copied from a real repo:

```yaml
# Which approved addons run on spoke-1.
# Turning an addon on or off for this cluster is done by editing this file.
# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/cluster-addons.v1.json
apiVersion: sharko.dev/v1
kind: ClusterAddons
cluster: spoke-1
addons:
    reloader:
        enabled: true
```

An entry can also carry `version` (a version only this cluster uses) and `settings` (sync options for this cluster only).

A cluster has no file here until the first addon is turned on for it. The file for a cluster you took over starts empty (`addons: {}`).

### What turning an addon on and off writes

- **Turning on** sets `enabled: true` in this file. If it is the first time for this cluster, Sharko also writes the cluster's values file for that addon (`values/clusters/<cluster>/<addon>.yaml`), with comments only.
- **Turning off** sets `enabled: false`. The entry stays in the file. `sharko disable-addon --remove` deletes the entry instead.

After the pull request is merged, Sharko puts the label `addons.sharko.dev/<addon>=enabled` on the cluster's ArgoCD connection Secret, and takes it off again when you turn the addon off. That label is what the ApplicationSet selects on. Sharko keeps this label in line with the file on every check, so a label removed by hand is put back on the next check. See [Drift Detection and Self-Heal](../user-guide/drift-and-sync.md).

### What stays after you turn an addon off

When the pull request is merged, ArgoCD removes the addon's Application and the workloads under it. Everything else stays:

- The `enabled: false` entry in `cluster-addons/<cluster>.yaml`.
- The addon's entry in `catalog.yaml` and its global values file.
- The cluster's values file for the addon, with any values you wrote in it. Turning the addon on again picks them up.
- The addon's ApplicationSet.
- The namespace on the cluster. ArgoCD does not delete a namespace it created. See [What stays on the cluster after you switch an addon off](../user-guide/addons.md#what-stays-on-the-cluster-after-you-switch-an-addon-off).

To take an addon out for good, turn it off on every cluster, then remove it from the catalog.

## Values Files

Both kinds of values file are handed to Helm as plain value files, so the chart's own keys sit at the top of the file. **Do not wrap them under the addon's name.** A wrapped file is not an error. Helm simply finds no keys it knows and ignores everything in it.

### Global Values (`values/global/<addon>.yaml`)

Base Helm values applied to every cluster that runs the addon.

When you add an addon to the catalog, Sharko writes this file as a copy of the chart's own default values, with a header on top. Cluster-specific keys such as `host` or `replicas` are written as comments so you can see they exist. The real file for a small chart is a few hundred lines long. It starts like this:

```yaml
# Generated by Sharko from reloader@2.2.21 on 2026-11-26
# Chart source: https://stakater.github.io/stakater-charts
# AI annotation: disabled
# sharko: managed=true
# Helm values for reloader — applied to all clusters (top-level keys are chart values).

global:
  imageRegistry: ""
  imagePullSecrets: []
```

The review pull request for the catalog entry shows this whole file. See [Smart values](../user-guide/smart-values.md) for how the file is built.

### Per-Cluster Overrides (`values/clusters/<cluster>/<addon>.yaml`)

Overrides for one addon on one cluster. Helm merges them on top of the global file, and the per-cluster value wins.

Sharko writes this file the first time you turn the addon on for the cluster. It holds comments only: the keys the global file marked as cluster-specific, each with a placeholder.

```yaml
# Helm values for reloader on spoke-1 only. These override values/global/reloader.yaml.
# An empty file means no cluster-specific overrides.

# --- per-cluster overrides ---
# Uncomment and set a real value for any field this cluster needs to override.
# "reloader.deployment.replicas": <set per cluster>
```

A real override is written with the chart's own keys at the top, never under the addon's name. To run two replicas of a chart that has `replicaCount`, the file holds:

```yaml
replicaCount: 2
```

and not `podinfo:` followed by `replicaCount: 2`.

## The Connection Secret

For every cluster it manages, Sharko writes one ArgoCD cluster connection Secret in the `argocd` namespace. Besides the credentials, it carries:

| Where | Name | Meaning |
|-------|------|---------|
| Label | `app.kubernetes.io/managed-by: sharko` | Sharko owns this Secret. |
| Label | `argocd.argoproj.io/secret-type: cluster` | ArgoCD reads it as a cluster connection. |
| Label | `addons.sharko.dev/<addon>: enabled` | This addon is turned on for this cluster. |
| Annotation | `sharko.dev/revision` | The Git commit the last write was built from. |
| Annotation | `sharko.dev/source-file` | The Git file the last write was built from. |
| Annotation | `sharko.dev/written-at` | When Sharko last wrote it. |

The three annotations only tell you where the Secret came from. Nothing reads them to decide what to do.

## How the Engine Uses This Structure

The engine chart (deployed by `sharko-engine.yaml`) does this:

1. Reads `catalog.yaml` and creates one ApplicationSet for every addon in it. An addon that is not in the catalog has no ApplicationSet.
2. Each ApplicationSet picks the clusters whose connection Secret carries the label `addons.sharko.dev/<addon>=enabled`. The label is the one that Sharko keeps in line with `cluster-addons/<cluster>.yaml`.
3. For each cluster picked, it creates an ArgoCD Application that:
   - Installs the addon's Helm chart at the version in `cluster-addons/<cluster>.yaml` if that file pins one, or the catalog version if not
   - Passes `values/global/<addon>.yaml` and then `values/clusters/<cluster>/<addon>.yaml` to Helm as value files. A file that does not exist is skipped.
   - Targets that cluster

## Version Tracking

The catalog holds the default version of each addon. A cluster follows it unless its own file pins a version with `version:` under the addon. When you run `sharko upgrade-clusters cert-manager --version 1.15.0 --cluster prod-eu`, Sharko sets that pin in `cluster-addons/prod-eu.yaml` and opens a pull request. See [Upgrades](../user-guide/upgrades.md) for the other ways to move a version.

This means:

- Each cluster can run a different version of the same addon
- Version drift is visible by comparing `cluster-addons` files with the catalog
- Upgrades are explicit — a cluster stays on its pinned version until you change it

## Sharko's Git Operations

All Git operations go through a mutex — one PR at a time, no concurrent writes.

| Operation | Git Action |
|-----------|-----------|
| `sharko init` | Creates the bootstrap files (`sharko-engine.yaml`, `README.md`, empty folders), opens PR |
| `sharko add-to-catalog` | Adds an entry to `catalog.yaml` and writes `values/global/<addon>.yaml`, opens PR. With `--enable-on <cluster>` the same pull request also turns the addon on for that cluster. |
| `sharko add-cluster` | Adds the cluster to `managed-clusters.yaml`, opens PR. Does not create `cluster-addons/<cluster>.yaml`. |
| `sharko enable-addon` | Sets `enabled: true` in `cluster-addons/<cluster>.yaml` (creating the file the first time), optionally writes `values/clusters/<cluster>/<addon>.yaml`, opens PR |
| `sharko disable-addon` | Sets `enabled: false` in `cluster-addons/<cluster>.yaml`, opens PR |
| `sharko upgrade-addon` | Without `--cluster`: changes the version in `catalog.yaml`, removes each cluster's own version for that addon from `cluster-addons/<cluster>.yaml` (a cluster that already runs a newer version is left as it is), and moves values in `values/global/<addon>.yaml` that are still the old chart's values. With `--cluster`: sets the version in that cluster's `cluster-addons/<cluster>.yaml`. Opens PR |
| `sharko upgrade-clusters` | Updates version pins in `cluster-addons/<cluster>.yaml` for selected clusters, opens PR |
| `sharko remove-cluster` | Deletes the cluster's entry from `managed-clusters.yaml`, its `cluster-addons/<cluster>.yaml` and the files under `values/clusters/<cluster>/`, opens PR |
| `sharko takeover` | Adds the cluster to `managed-clusters.yaml` and creates an empty `cluster-addons/<cluster>.yaml`, opens PR |

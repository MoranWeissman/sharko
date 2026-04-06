# Upgrades

Sharko provides flexible upgrade workflows: upgrade a single addon globally, upgrade per-cluster, or batch multiple addons in a single PR.

## How Upgrades Work

Every upgrade is a GitOps operation:

1. Sharko updates the version in the relevant `values.yaml` file(s) in your addons repo
2. A PR is opened in Git (auto-merged if `SHARKO_GITOPS_PR_AUTO_MERGE=true`)
3. ArgoCD detects the change and syncs the new version to the cluster(s)

No changes are applied directly to the cluster — all changes go through the Git PR.

## Global Upgrade

Upgrade an addon to a new version on **all clusters** that have it enabled:

```bash
sharko upgrade-addon cert-manager --version 1.15.0
```

This updates the addon's base `values.yaml` in your repo.

Via UI: **Addons → [addon name] → Upgrade → All Clusters**.

## Per-Cluster Upgrade

Upgrade an addon on a **specific cluster** only (useful for staged rollouts):

```bash
sharko upgrade-addon cert-manager \
  --version 1.15.0 \
  --cluster my-staging-cluster
```

This creates a cluster-specific override in `addons/cert-manager/clusters/my-staging-cluster.yaml`.

Via UI: **Clusters → [cluster name] → [addon name] → Upgrade**.

## Batch Upgrade

Upgrade multiple addons in a single PR:

```bash
sharko upgrade-addons \
  cert-manager=1.15.0,metrics-server=0.7.1,ingress-nginx=4.9.0
```

This creates one PR with all version bumps, keeping your Git history clean.

Via API:

```bash
curl -X POST https://sharko.your-domain.com/api/v1/addons/upgrade-batch \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "upgrades": [
      {"name": "cert-manager", "version": "1.15.0"},
      {"name": "metrics-server", "version": "0.7.1"}
    ]
  }'
```

## Checking for Available Upgrades

The **notification bell** in the UI shows alerts when newer versions of your addons are available. Click an alert to jump directly to the upgrade workflow.

The **Version Matrix** (Addons page) highlights cells where a cluster is running an older version than the catalog target — these are drift indicators.

## Drift Detection

Drift occurs when a cluster is running a different version than the catalog target. Common causes:

- A per-cluster override was applied but the base version was later updated
- A cluster was registered after a global upgrade was performed
- A manual change was made to the ArgoCD application outside of Sharko

To resolve drift:

1. Open the addon's detail view in the UI — drifted clusters are highlighted
2. Select drifted clusters and click **Sync to Target Version**
3. Or run: `sharko upgrade-addon cert-manager --version <target> --cluster <drifted-cluster>`

## Staged Rollout Pattern

For production safety, upgrade in stages:

```bash
# Stage 1: upgrade dev clusters
sharko upgrade-addon cert-manager --version 1.15.0 --cluster dev-cluster

# Validate, then stage 2: staging
sharko upgrade-addon cert-manager --version 1.15.0 --cluster staging-cluster

# Stage 3: all production clusters
sharko upgrade-addon cert-manager --version 1.15.0
```

## Auto-Merge

To auto-merge upgrade PRs without manual review, set:

```yaml
extraEnv:
  - name: SHARKO_GITOPS_PR_AUTO_MERGE
    value: "true"
```

!!! warning
    Auto-merge is convenient but skips human review. Enable it only for addons where automated testing provides sufficient confidence.

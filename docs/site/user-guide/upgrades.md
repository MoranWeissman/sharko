# Upgrades

This page shows you how to move an addon to a new version, on all clusters or on some of them.

## What you'll do

- See which versions exist
- Upgrade one addon on all clusters
- Upgrade one addon on chosen clusters only
- Upgrade several addons in one pull request

## What you need

- Admin or Operator role in Sharko
- The new version number you want

---

## How upgrades work

Every upgrade is a pull request in your Git repo. Sharko never changes a cluster directly.

1. Sharko checks that the version exists in the addon's chart repository. If it does not, nothing is opened.
2. Sharko opens one pull request that moves the version (auto-merged if auto-merge is on, see below).
3. Once it merges, ArgoCD sees the change and installs the new version on the cluster(s).

Where the version lives:

- `catalog.yaml` holds the version of each addon. Every cluster follows it.
- A cluster can have its own version for an addon, in `cluster-addons/<cluster>.yaml`. That cluster then stays on its own version until you move it.

## Upgrade on all clusters

```bash
sharko upgrade-addon cert-manager --version 1.15.0
```

This upgrades the addon on ALL clusters in one pull request. It changes the version in `catalog.yaml`. A cluster that had its own version for this addon goes back to following the catalog. A cluster that is already on a newer version is left as it is, and Sharko names it:

```text
These clusters already run a newer version of podinfo than 6.8.0 and were left as they are: spoke-2 (6.9.0).
```

A cluster where the addon is switched off is not touched.

What the pull request changes:

| File | Change |
|------|--------|
| `catalog.yaml` | The version of the addon |
| `cluster-addons/<cluster>.yaml` | For clusters that had their own version: that version is removed |
| `values/global/<addon>.yaml` | Only when it still holds values copied from the old chart (see [Values on upgrade](#values-on-upgrade)) |

In the UI: **Addons**, open the addon, **Upgrade** tab. Pick a version and click **Analyze**. Then click **Upgrade to X** and confirm. The confirm box says:

> Upgrade podinfo to 6.8.0 on all clusters?
>
> This opens one pull request. Every cluster that has podinfo switched on moves to 6.8.0 once it merges. A cluster that is already on a newer version stays there.

Flags of `upgrade-addon`: `--version` (required), `--cluster`, `--dry-run` (show the files the pull request would change, without opening it) and `--auto-merge`.

## Upgrade on chosen clusters only

One cluster:

```bash
sharko upgrade-addon cert-manager --version 1.15.0 --cluster my-staging-cluster
```

This moves only that cluster. The catalog and the other clusters stay.

Several clusters:

```bash
sharko upgrade-clusters cert-manager --version 1.15.0 \
  --cluster staging-eu --cluster staging-us
```

This moves the chosen clusters and leaves the catalog version alone. Both commands write the version into `cluster-addons/<cluster>.yaml`.

In the UI: on the same **Upgrade** tab, use the box **Upgrade chosen clusters only**. Its text is "Pick clusters and a version. One pull request moves only those clusters. The catalog version stays 6.8.0." Pick the clusters (**Select all** and **Clear** help), click **Preview N changes** to see the files, then **Upgrade N clusters**. When some of the chosen clusters would go down to an older version the button reads **Change N clusters**, and you have to type `DOWNGRADE` to confirm.

Each cluster row on the tab also has its own **Upgrade to X** button when that cluster is older than the catalog. A cluster that is newer than the catalog shows the badge **Ahead of catalog**; Sharko does not offer to move it down from there.

An upgrade never moves a cluster down by itself. Going down on purpose is only possible in the **Upgrade chosen clusters only** box.

## Several addons in one pull request

```bash
sharko upgrade-addons cert-manager=1.15.0,metrics-server=0.7.1,ingress-nginx=4.9.0
```

This upgrades several addons on all clusters in one pull request.

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

## A version that does not exist

The version must exist in the addon's chart repository. If it does not, nothing is opened, and Sharko says:

```text
Version 99.0.0 of podinfo was not found in its chart repository (<address>). Nothing was changed. Pick a version that the repository lists.
```

If Sharko cannot reach the chart repository, it stops the upgrade and does not blame the version:

```text
Sharko could not reach the chart repository of podinfo (<address>), so it could not check that version 6.8.0 exists. Nothing was changed. Try again in a moment.
```

If the addon is already on that version:

```text
podinfo is already on version 6.7.1 on every cluster. Nothing to change.
```

Chart repositories whose address starts with `oci://` are not checked, because Sharko cannot list their versions.

## Values on upgrade

When you add an addon, Sharko writes `values/global/<addon>.yaml` with the chart's own values in it. Some of those belong to one chart version. The image tag is the usual one.

- **Upgrade on all clusters.** When Sharko made that file, the upgrade moves the settings nobody changed to the new chart's values, in the same pull request. A setting you changed stays. A file you wrote yourself is not changed. Lists and multi-line texts are not moved; the result names them.
- **Changing the version another way.** Editing the version of a catalog entry (`PATCH /api/v1/catalog/addons/{name}`), or adding an addon that is already in the catalog with another version, moves the chart on all clusters too. Sharko treats the values file the same way as in an upgrade on all clusters.
- **Upgrade on chosen clusters.** The global values file is shared by every cluster, so Sharko does not change it. If that file still sets the old image tag, the chosen clusters get the new chart with the old image, and Sharko says so in a warning:

```text
values/global/podinfo.yaml is shared by every cluster and still sets the old chart's value for: image.tag (6.7.1, new chart default 6.8.0). The chosen clusters get the new chart but keep these old values. To move them too, upgrade podinfo for all clusters, or set them in the cluster's own values file.
```

Other warnings you can get:

```text
values/global/podinfo.yaml still sets the old chart's value for: <settings>. Sharko did not change these. They stay as they are after the upgrade until you edit that file.
```

```text
Sharko could not compare the old and new chart of podinfo, so it left values/global/podinfo.yaml as it is. If that file still sets the old chart's image tag, the old image keeps running after this upgrade. Check the file.
```

In the UI the warnings show under **Check after this upgrade**. On the command line each one is a `Warning: ...` line.

The **Analyze** step tells you about this before you upgrade. "N settings in the global values file move to the new chart defaults" means: nobody changed these, so an upgrade for all clusters updates them in the same pull request. The conflicts box lists settings that a values file sets and where the chart changed its own value. Your value stays after the upgrade. Check that it still fits the new chart.

## Seeing which versions exist

On the **Upgrade** tab the version list marks the newest released version **LATEST** and marks a beta or release candidate **Pre-release**. The version the addon is on reads **Already on this version**.

The **notification bell** tells you when a newer version of an addon exists. A pre-release is never announced as an upgrade. When a cluster has its own version, the bell says "Set to <version>, catalog has <catalog version>". Notes that stop being true go away at the next check.

On the cluster page, **Running (in cluster)** is the version ArgoCD really deployed last, not the version Git asks for. Right after a pull request merges the two differ until ArgoCD has installed the new version. If ArgoCD cannot load a version, the cluster page says so:

```text
ArgoCD could not load version 99.0.0. Check that this version exists in the chart repository. Version 6.9.0 is still running.
```

## Staged rollout

For production safety, upgrade in stages:

```bash
# Stage 1: dev clusters
sharko upgrade-addon cert-manager --version 1.15.0 --cluster dev-cluster

# Check it, then stage 2: staging
sharko upgrade-addon cert-manager --version 1.15.0 --cluster staging-cluster

# Stage 3: all clusters
sharko upgrade-addon cert-manager --version 1.15.0
```

The last step moves the catalog and takes the own version off the dev and staging clusters, so every cluster follows the catalog again.

## Pull request titles

| What you did | Title |
|--------------|-------|
| All clusters | `upgrade addon podinfo to 6.8.0 on all clusters` |
| Chosen clusters | `upgrade addon podinfo to 6.8.0 on clusters spoke-1` |
| Several addons | `upgrade addons on all clusters: a to 1, b to 2` |
| Version goes down | starts with `downgrade addon ...` |

The preview shows the same title.

## Auto-Merge

To auto-merge upgrade PRs without manual review, set the Helm value:

```yaml
connection:
  gitops:
    prAutoMerge: "true"
```

That is the way to do it — the chart turns the value into the setting the
server reads, so you never have to type the variable name yourself. See
[Git-Native Server Configuration](../operator/git-native-config.md).

If you set it by hand instead, the name is `SHARKO_CONN_GITOPS_PR_AUTO_MERGE`:

```yaml
extraEnv:
  - name: SHARKO_CONN_GITOPS_PR_AUTO_MERGE
    value: "true"
```

!!! danger "If you copied an older version of this page, your server will not start"
    This page used to show the same block naming
    `SHARKO_GITOPS_PR_AUTO_MERGE` — the name without `CONN_` in it.
    **Sharko has never read that name.** It was ignored in silence, so
    auto-merge was never on for anyone who set it, and from this release a
    `SHARKO_` name Sharko does not recognise stops the server at startup
    instead of being ignored. If that block is in your values file, your
    next upgrade will not come up. Fix the name, or delete the block and
    use `connection.gitops.prAutoMerge` above.

!!! warning
    Auto-merge is convenient but skips human review. Enable it only for addons where automated testing provides sufficient confidence.

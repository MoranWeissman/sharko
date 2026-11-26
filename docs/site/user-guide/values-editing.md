# Viewing and Changing Helm Values

Every addon runs with Helm values that come from two files in your addons repository. You can look at both files in Sharko. You change them by editing the files in Git.

!!! note "Editing in Sharko is not in this version"
    The **Values** tab on the addon page and the **Config** tab on the cluster page show the real file, read-only. There is no **Submit changes** button. There is no **Recent changes** panel either. Each tab says:

    > Editing values here is not in this version. To change them, edit the file in your Git repo: `<path>`

    The path is `values/global/<add-on>.yaml` for every cluster, or `values/clusters/<cluster>/<add-on>.yaml` for one cluster. An editor that opens the pull request for you is planned for a later version.

## Two scopes

| Scope | What it changes | Where it lives in Git | File shape | Who's affected |
|-------|-----------------|-----------------------|------------|----------------|
| **Global** | Default values for an addon | `values/global/<addon>.yaml` | **Top-level chart values** (no `<addon>:` wrapper) | Every cluster running this addon |
| **Per-cluster overrides** | Just one addon on one cluster | `values/clusters/<cluster>/<addon>.yaml` | **Top-level chart values** (no `<addon>:` wrapper) | Only the chosen cluster |

Per-cluster overrides win when both are set, exactly the way Helm value composition works.

### File shape: both files are unwrapped

The ApplicationSet passes both files straight to Helm as value files, so the chart's own keys must sit at the top of the file. This is the same for the global file and for the per-cluster file.

!!! warning "A file wrapped under the addon's name does nothing"
    If you write `values/clusters/spoke-1/podinfo.yaml` like this:

    ```yaml
    podinfo:
      replicaCount: 2
    ```

    Helm looks for a chart value called `podinfo`, finds none, and ignores the whole file. ArgoCD still shows the addon as Synced and Healthy, and nothing changes on the cluster. Write it like this instead:

    ```yaml
    replicaCount: 2
    ```

Each addon has its own file, so there is nothing to wrap. The Config tab of the cluster page shows the merged values under the addon's name as a heading. That heading is only the screen's way of showing them. It is not part of the file.

## Looking at the values in Sharko

- **Global values:** open the addon (Addons, then the addon's name) and choose the **Values** tab. It shows `values/global/<addon>.yaml` as it is in Git, including the comments. A file that holds only comments is shown as it is ("This file has only comments, no settings yet. Showing the file as it is:"). An empty file reads "This file is empty."
- **Per-cluster overrides:** open the cluster (Clusters, then the cluster's name), choose the **Config** tab, and pick the addon.

When the file is not in Git yet, the tab says "There is no file at `<path>` in your Git repo yet. Create it there to set values."

When a per-cluster file is wrapped under the addon's name, the cluster page warns: "None of the settings in this file take effect. They are all inside a block named "`<addon>`:". Move them out of that block so they start at the left edge of the file."

Some charts really have a top block with the addon's own name. Reloader is one (`reloader:` then `deployment:` then `replicas: 2` is correct), and so are datadog, loki, tempo, falco and opencost. Such a file is not wrapped, and Sharko does not warn about it. Sharko tells the two apart by looking at the top settings in `values/global/<addon>.yaml`. When that file is missing or holds no settings, Sharko cannot tell and gives no warning, on the cluster page and in `sharko validate-config`.

## Changing values: edit the file in Git

1. Open the file in your addons repository: `values/global/<addon>.yaml` for every cluster, or `values/clusters/<cluster>/<addon>.yaml` for one cluster. If the cluster file does not exist yet, create it.
2. Change the values. Keep the chart's own keys at the top of the file. Do not wrap them.
3. Check the file: `sharko validate-config values/` run in a checkout of the repo reports YAML that cannot be read. It also fails a per-cluster file (`values/clusters/<cluster>/<addon>.yaml`) that is wrapped under the addon's name: "none of the settings in this file take effect: they are all inside a block named "`<addon>`:"", with the advice "Move the settings out of the "`<addon>`:" block so they start at the left edge of the file." (`sharko validate` does not check a v4 repo.)
4. Open a pull request and merge it.
5. ArgoCD picks up the change and reconciles every cluster that runs the addon.

To clear an override for one cluster, delete the lines (or the file) in `values/clusters/<cluster>/<addon>.yaml`. The cluster then follows the global file again.

If the YAML in Git cannot be read, ArgoCD reports a comparison error and keeps the addon's pods running as they were. The Sharko pages show the addon as unknown or in error without saying why. The reason is in the ArgoCD Application.

The same values can be given for one cluster when you turn an addon on from the command line: `sharko enable-addon <cluster> <addon> --values-json '{"replicaCount": 3}'` writes them to the per-cluster file in the same pull request.

## Generated values files

When you add an addon to the catalog, Sharko writes `values/global/<addon>.yaml` as a copy of the chart's own `values.yaml`, with a header and a comment block at the bottom. The file starts like this (a real one, shortened):

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

and ends like this:

```yaml
# --- per-cluster overrides template ---
# Copy these into values/clusters/<cluster>/reloader.yaml for any cluster that needs an override.
#   "reloader.deployment.replicas": <set per cluster>
#   "reloader.deployment.resources": <set per cluster>
```

Three things to notice:

1. **The file holds every chart default.** That is a lot of lines, and it is on purpose: the pull request shows the reviewer everything that will run. Because it holds every default, a value you do not edit is still set in this file. An upgrade for all clusters moves the settings nobody changed to the new chart's values (see [Values on upgrade](upgrades.md#values-on-upgrade)). A setting you changed stays as you set it.
2. **Cluster-specific fields are marked in place.** Sharko's heuristic finds fields like `host`, `replicaCount`, `resources.*`, `nodeSelector`, `tolerations`, `region`, `clusterName` (see the [smart values reference](smart-values.md) for the full list) and leaves a comment such as `# replicas: <cluster-specific>` where each one sits.
3. **The block at the bottom is a list of names, not a file you can paste.** It writes each field as a dotted name in quotes, such as `"reloader.deployment.replicas"`. In a values file, write each value as normal nested YAML instead (`reloader:` then `deployment:` then `replicas: 2`), because Helm reads a dotted name in quotes as one key with dots in it, not as a path.

The first time you turn the addon on for a new cluster, Sharko also writes `values/clusters/<cluster>/<addon>.yaml` with the same names as comments. It holds no real values until you add them.

You can edit the generated files freely. Sharko does not rewrite them on its own.

## Pulling in new chart defaults

When you upgrade an addon for all clusters, Sharko moves the settings in the generated global file that are still exactly the old chart's values to the new chart's values, in the same pull request. A setting you changed stays. A file you wrote yourself is not changed. Lists and multi-line texts are not moved; the upgrade result names them. An upgrade for chosen clusters does not change this file at all. See [Values on upgrade](upgrades.md#values-on-upgrade).

Keys that are new in the newer chart are not added to the file. The chart's own default applies to them. The **Refresh from upstream** and **Pull new fields from upstream** actions are not in this version. To set a new key, add it to the file in Git.


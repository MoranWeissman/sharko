# Managing Addons

This page shows you how to add addons to your catalog, enable them on clusters, and configure their values.

## What you'll do

- Add addons to your catalog
- Enable addons on specific clusters
- Configure Helm values (global and per-cluster)

## What you need

- Admin or Operator role in Sharko
- The Helm chart's repository URL and version
- Values you want to customize (optional)

---

The addon catalog is the source of truth for which Helm charts Sharko manages across your fleet. Adding an addon to the catalog makes it available to assign to clusters. To take an addon away, first switch it off on every cluster that has it, then remove it from the catalog.

Adding to the catalog and enabling on a cluster are two separate operations, but enabling now requires catalog membership first — you cannot enable an addon on a cluster until it is already in the catalog. From the Marketplace, both steps can be done as one combined pull request if you want; nothing forces you to split them.

![Two parallel operations — add to catalog (PR adds the addon entry to catalog.yaml) and enable on a cluster (PR switches the addon on in that cluster's file in cluster-addons/, and Sharko puts the addon label on the cluster's connection Secret) — both feed the ApplicationSet's cluster x cluster-file matrix generator, which creates the ArgoCD Application that deploys the addon.](../assets/diagrams/04-two-operation-model.drawio.svg)

> **Two words, everywhere:** this page covers the **Catalog** — your org's
> approved list, kept in `catalog.yaml` — and enabling an addon on a
> cluster, which requires the addon to already be in the Catalog. The
> **Marketplace** (the read-only browse/discover surface) is covered in
> [Marketplace](marketplace.md). See
> [Marketplace Architecture](../operator/marketplace-architecture.md) for
> how the two fit together.

## Default Addons

When you register or adopt a cluster without specifying which addons to enable, Sharko can auto-enable a set of **default addons**. The source of truth for default addons is a Git file in your GitOps repository:

```
configuration/default-addons.yaml
```

This file is an enveloped document with the following structure:

```yaml
apiVersion: sharko.io/v1
kind: DefaultAddons
metadata:
  name: default-addons
spec:
  addons:
    - cert-manager
    - external-dns
    - metrics-server
```

The addon names listed in `spec.addons` must refer to addons already present in your `catalog.yaml` — they are references, not definitions. When you register a cluster without explicit addon selections, Sharko enables every addon in this list automatically.

### Editing Default Addons via the UI

The UI provides an interface to edit the default addons list. When you make changes and click **Save**, Sharko **opens a pull request** against `configuration/default-addons.yaml` — it never changes the file directly. This keeps default addons consistent with how every other Sharko mutation works (registering clusters, enabling addons, editing values).

One editing session creates one pull request. You review and merge the PR; Sharko applies the new defaults once the PR lands.

The API endpoints for default addons are:

- `GET /api/v1/default-addons` — fetch the current list
- `PUT /api/v1/default-addons` — submit changes (opens a PR)

### Backward Compatibility

If you upgrade to this version and don't yet have `configuration/default-addons.yaml` in your repo, Sharko continues to use the existing default-addons setting from your connection configuration. The Git file takes over once present, but nothing forces you to create it immediately.

### Design Note — Sharko is a GitOps agent

Sharko is built around a single principle: **the UI opens pull requests, it doesn't mutate state directly**. Default addons were the last holdout — they used to save live to the connection config. With the shift to a git-backed file, every action in Sharko follows the same pattern: edit, open PR, review, merge, apply.

## Viewing the Catalog

```bash
# CLI: list all addons with version and deployment stats
sharko list-addons

# Add the SELF-HEAL and SYNC-OPTIONS columns:
sharko list-addons --show-config
```

In the UI, the **Addons** page shows:

- Every addon in the catalog with its current version
- Deployment stats (how many clusters have it enabled). On the addon's page the **Clusters enabled** card is the number of clusters that have it turned on, out of all managed clusters.
- Drift indicators (clusters running a different version)

The **Version Matrix** view shows an addon × cluster grid, making it easy to see which clusters are on another version than the catalog. A cluster that is on a newer version is marked as ahead of the catalog, not as behind.

## Adding an Addon to the Catalog

### Preview Changes Before a PR Opens

Before submitting an addon addition (or removal, or configuration change), the UI offers a **"Preview changes"** button. For a new addon the preview shows the real `values/global/<add-on>.yaml` (the chart's values), without its comment lines and with values that look like passwords hidden. A note says so: "Values files are shown here without their comment lines, and values that look like passwords are hidden. The pull request writes the whole file." When AI notes are on, a second note says the pull request will also carry short explanations written by the AI assistant, which the preview leaves out.

Clicking the button works like this. Clicking it shows exactly what the PR would contain: which files would be written or deleted, the actual line-by-line change inside each file (added/removed lines), the PR title, and the names of any secrets it would create. Secret values are redacted (shown as `<redacted>`) while keys and structure remain visible. This dry-run costs nothing (no branch, no commit, no PR) and gives you full transparency before confirming.

This preview feature works on all addon operations: adding to the catalog, removing an addon, turning addons on or off for a cluster, and saving default addons. See [Preview Changes](clusters.md#preview-changes-before-a-pr-opens) for details, including a diff example and API usage.

### Via CLI

On a v4 repo, use `sharko add-to-catalog`. It adds the addon to `catalog.yaml`, writes the addon's global values file, and opens one pull request.

```bash
# Copy the chart location from the Marketplace entry of the same name:
sharko add-to-catalog cert-manager --from-marketplace --version 1.14.5

# Or name the chart yourself:
sharko add-to-catalog billing-api \
  --repo oci://your-registry/charts \
  --chart billing-api \
  --version 2.4.0 \
  --namespace billing
```

Flags:

| Flag | Required | Description |
|------|----------|-------------|
| `--version` | Always | Chart version. The Marketplace has no version, so you always choose one. It must exist in the chart repository. |
| `--chart` | Unless `--from-marketplace` | Chart name inside the repository |
| `--repo` | Unless `--from-marketplace` | `https://` or `oci://` address of the chart repository — must be credential-free (see below) |
| `--namespace` | No | Namespace to install into |
| `--from-marketplace` | No | Copy chart, repository, namespace and the needed-secrets list from the Marketplace entry of the same name |
| `--enable-on <cluster>` | No | Also turn the addon on for this cluster, in the same pull request |

`sharko add-addon` is the command for the older (v3) layout. On a v4 repo it is refused with a `409`. There is no `--values` flag on either command.

### Via UI

There are two doors, and both end in one pull request.

- **From the Marketplace:** open **Addons**, then the **Marketplace** tab, pick an addon, choose a version, and use the **Add to catalog** panel. The same panel is on the page of an addon you found through ArtifactHub search.
- **Your own chart:** on **Addons**, click **Add addon manually**. The dialog is called **Add your own chart**. Fill in the name, the namespace (optional), the chart repository address, the chart and the version. The namespace is checked: "A namespace can only use lowercase letters, digits and hyphens. It must start and end with a letter or digit, and be 63 characters at most." The version must exist in the chart repository; a version that is not there is refused before any pull request opens. An addon that is already in the catalog with the same chart, version and namespace is not added twice, and no pull request is opened.

Both forms have an optional field, **Also enable on a cluster (optional)**. It starts on **Don't enable yet**. Pick a cluster there and the one pull request adds the addon to the catalog **and** turns it on for that cluster, so the reviewer sees the whole change in one place.

### The chart repository address must be credential-free

Sharko will not accept a chart repository address with a sign-in written into
it — `https://a-token@charts.example/org/charts` and the like. The address ends
up in a YAML file in your Git repository, and Git keeps history for good, so a
token written there is a token you have published.

The rule is:

> Catalog repository URLs in the technical preview must be ones Sharko can
> read in full: a host, an optional port, and an optional path. User
> information in the address, a query string, and a fragment are all refused,
> and so is an address Sharko cannot read. Use a credential-free base URL.

It is checked on the shape of the address, not on whether the text looks
secret, so an ordinary `?ref=main` is refused too. Take the query string off.

An address Sharko cannot read at all is refused as well — a port that is not a
number, say. A scheme is optional, and an `@` in the path is fine, so
`charts.example/org/charts` and `https://charts.example/org/charts@v1` are both
accepted.

Sharko refuses; it never quietly saves a cleaned-up version of what you typed.
Chart repositories that need a sign-in are not supported in this preview —
see [Technical Preview](../technical-preview.md) and
[Security](../operator/security.md).

**Already committed a token?** Revoke or rotate it, and remove it from your
Git history — editing the current file leaves it one `git log -p` away.

The command opens a PR that adds the addon to your Git repo.

## Configuring an Addon

Addon configuration lives in your GitOps repo as Helm values files. In the current repo layout (v4) the files are:

```
catalog.yaml                          # the list of approved addons
values/
  global/
    cert-manager.yaml                 # base values for all clusters
  clusters/
    my-cluster/
      cert-manager.yaml               # overrides for one cluster
cluster-addons/
  my-cluster.yaml                     # which addons are switched on for that cluster
```

Repos in the older layout (v3) keep their values under a `configuration/` folder instead, with one file per cluster that holds every addon.

You change these files in Git, with a pull request. The addon page (**Values** tab) and the cluster page (**Config** tab) show the files read-only. Editing them in Sharko is not in this version. Both files hold plain Helm values with the chart's own keys at the top, never wrapped under the addon's name. See [Viewing and Changing Helm Values](values-editing.md).

## Removing an Addon

```bash
sharko remove-addon cert-manager --confirm
```

Without `--confirm`, the command runs a dry-run and shows what the removal would change. On a repo in the new format, that is the list of files the pull request would remove or change. Nothing is written.

!!! warning
    On a repo in the new format, Sharko will not remove an addon from the catalog while it is still switched on for any cluster. It tells you which clusters still have it. Switch it off on those clusters first, then remove it from the catalog. Removing it from the catalog opens a pull request; nothing changes until that pull request is merged.

Via UI: **Addons → [addon name] → Remove** — the UI requires explicit confirmation.

### What stays on the cluster after you switch an addon off

When you switch an addon off on a cluster and the pull request is merged, ArgoCD deletes the addon's Application and the workloads it was running (Deployments, Services, and so on). Some things can stay behind on the cluster:

- **The namespace.** Sharko asks ArgoCD to create the addon's namespace when the addon is installed. ArgoCD does not delete a namespace it created this way, so the empty namespace stays. Delete it yourself if you no longer want it.
- **CustomResourceDefinitions (CRDs).** Some charts install CRDs (cert-manager does, for example). ArgoCD may leave these on the cluster, even when the chart's own setting asks for them not to be kept. Check with `kubectl get crd` and delete the ones you no longer need. Deleting a CRD also deletes every object of that type on the cluster, so only do it once nothing uses them.

## Addon Secrets

Some addons need API keys or credentials at runtime (e.g., Datadog, New Relic). Sharko can deliver them directly to remote clusters, using the same secrets provider configured for cluster credentials (AWS Secrets Manager or Kubernetes Secrets). No External Secrets Operator is required.

A secret is delivered only when its catalog entry has a **`push:` block**. That block is what tells Sharko three things: the name of the Kubernetes Secret to create, the namespace it goes in, and where each of its values comes from in your secrets store. An entry with only a `name` and a `description` is a note for people. Sharko does not deliver anything for it, and nothing on screen says so.

### Declaring a Secret in the Catalog, step by step

1. **Put the value in your secrets store.** Create one entry in your secrets provider for each value the addon needs. Note the path of each (for example `sharko/addons/datadog/api-key`). With **AWS Secrets Manager**, every path must start with the **prefix** set in **Settings → Secrets Provider**. A path outside the prefix is refused, and with no prefix set, every path is refused.
2. **Add the secret to the addon's entry in `catalog.yaml`**, with a `push:` block:

    ```yaml
    apiVersion: sharko.dev/v1
    kind: AddonCatalog
    addons:
      datadog:
        repoURL: https://helm.datadoghq.com
        chart: datadog
        namespace: datadog
        version: "3.74.0"
        secrets:
          - name: Datadog API key
            description: Needed for the agent to authenticate to Datadog.
            required_for: install
            push:
              secretName: datadog-keys
              namespace: datadog
              keys:
                api-key: sharko/addons/datadog/api-key
    ```

    | Field | Meaning |
    |-------|---------|
    | `name`, `description` | Plain words for the people who read the catalog. Required. |
    | `required_for` | `install` (the chart cannot start without it) or `runtime` (needed later). Leave it out and Sharko reads it as `install`. |
    | `push.secretName` | The name of the Kubernetes Secret Sharko creates on each cluster. |
    | `push.namespace` | The namespace it goes in. Sharko does not create the namespace. Use the addon's own namespace, or one that already exists. |
    | `push.keys` | One line for each key inside the Secret. The left side is the key name as the addon expects it. The right side is the path of the value in your secrets store. |

    All three of `secretName`, `namespace` and `keys` are required inside `push:`.
3. **Open the pull request and merge it.** Sharko reads the catalog from Git, so nothing happens before the merge.
4. **Wait for the next run, or ask for one now:** `sharko refresh-secrets`. Sharko writes the Secret on every cluster that has the addon switched on. With the addon switched on for no cluster, nothing is written.
5. **Check:** `sharko secret-status` shows the last run, or open **Secrets → Addon secrets** in the UI. Each Secret Sharko delivered is listed there, with the cluster it is on. On the cluster, `kubectl get secret datadog-keys -n datadog` shows it. The values are never shown anywhere in Sharko.

If a path in the store is wrong, the run reports an error for that Secret and keeps the Secret that is already on the cluster. Fix the path in the catalog (another pull request) or in the store.

### How Reconciliation Works

Addon-secret syncing runs continuously in the background:

1. On a timer — `SHARKO_SECRET_RECONCILE_INTERVAL`, `5m` unless you change it — Sharko re-fetches all declared secrets from the provider
2. It computes a SHA-256 hash of each value and compares it to the last known hash
3. If a value changed, Sharko updates the Kubernetes Secret on the affected remote clusters
4. ArgoCD is configured to ignore these secrets (resource exclusion), so it never deletes them

That timer is a backstop, not the only way in: merging a pull request that enables an addon also wakes the sync right away, so a newly-enabled addon's secret does not sit waiting for the next check.

All Sharko-managed secrets are labeled `app.kubernetes.io/managed-by: sharko`.

### Manual Trigger

To force an immediate reconcile after rotating a secret in your provider:

```bash
sharko refresh-secrets               # reconcile all clusters
```

The command always covers every cluster. It takes no cluster name.

### Checking Secret Status

```bash
sharko secret-status
# Shows: last reconcile time, hash match status, and any errors per cluster
```

Or via the API:

```bash
curl https://sharko.your-domain.com/api/v1/secrets/status \
  -H "Authorization: Bearer <token>"
```

## AI-Generated Addon Summaries

When the AI provider is configured and `ai.enabled: true`, each addon in the catalog displays an AI-generated summary on its detail page. The summary is produced by passing the addon's chart name, version, and release notes to the configured LLM and is cached in-memory.

The summary appears as a collapsible panel at the top of the addon detail view. It includes:

- A one-paragraph description of what the addon does
- Notable changes in the current version (parsed from the Helm chart changelog)
- Any breaking changes detected in the release notes

To generate a summary on demand via API:

```bash
curl -X POST https://sharko.your-domain.com/api/v1/addons/cert-manager/ai-summary \
  -H "Authorization: Bearer <token>"
# Response: {"summary": "cert-manager v1.14.5 is a...", "generated_at": "2026-04-06T10:00:00Z"}
```

The `ai_summary` field is also available as a query parameter on `GET /api/v1/addons/catalog?ai_summary=true` to include summaries inline with the catalog list response.

!!! note
    AI summaries require `ai.enabled: true` in Helm values. If the AI provider is not configured, the summary panel is hidden.

## Upgrading Addons

See [Upgrades](upgrades.md) for upgrading on all clusters and on chosen clusters.

## Checking Addon Health

The addon detail view (UI) shows:

- Current version deployed per cluster
- Sync status from ArgoCD
- Drift: clusters where the running version differs from the catalog target
- ArgoCD health status (Healthy / Degraded / Progressing)

> **Progressing is temporary, not a hard error.** ArgoCD reports
> `Progressing` while a workload is rolling out or waiting on a dependency.
> Sharko shows these in a separate, blue "Progressing — usually temporary"
> widget on the Dashboard, not in the red "Apps with issues" widget. See
> [Dashboard state semantics](dashboard.md#unified-addon-state-model) for
> the full mapping.

From the CLI:

```bash
sharko status
# Shows cluster-level health including addon counts
```

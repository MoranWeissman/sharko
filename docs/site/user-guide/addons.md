# Managing Addons

The addon catalog is the source of truth for which Helm charts Sharko manages across your fleet. Adding an addon to the catalog makes it available to assign to clusters. Removing it from the catalog removes it from all clusters.

Adding to the catalog and enabling on a cluster are two separate operations that only meet at the ApplicationSet:

![Two parallel operations — add to catalog (PR adds the addon entry) and enable on a cluster (PR sets the addon label on that cluster's values file) — both feed the ApplicationSet's cluster x values-file matrix generator, which creates the ArgoCD Application that deploys the addon.](../assets/diagrams/04-two-operation-model.drawio.svg)

> **Three-layer model:** This page covers the **Catalog** layer (deployable list in `addons-catalog.yaml`) and the **Enablement** layer (per-cluster labels). The **Marketplace** layer (browse/discover surface) is covered in [Marketplace](marketplace.md). See [Marketplace Architecture](../operator/marketplace-architecture.md) for how the three layers fit together.

## Default Addons

When you register or adopt a cluster without specifying which addons to enable, Sharko can auto-enable a set of **default addons**. The source of truth for default addons is a git file in your GitOps repository:

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

The addon names listed in `spec.addons` must refer to addons already present in your `addons-catalog.yaml` — they are references, not definitions. When you register a cluster without explicit addon selections, Sharko enables every addon in this list automatically.

### Editing Default Addons via the UI

The UI provides an interface to edit the default addons list. When you make changes and click **Save**, Sharko **opens a pull request** against `configuration/default-addons.yaml` — it never changes the file directly. This keeps default addons consistent with how every other Sharko mutation works (registering clusters, enabling addons, editing values).

One editing session creates one pull request. You review and merge the PR; Sharko applies the new defaults once the PR lands.

The API endpoints for default addons are:

- `GET /api/v1/default-addons` — fetch the current list
- `PUT /api/v1/default-addons` — submit changes (opens a PR)

### Backward Compatibility

If you upgrade to this version and don't yet have `configuration/default-addons.yaml` in your repo, Sharko continues to use the existing default-addons setting from your connection configuration. The git file takes over once present, but nothing forces you to create it immediately.

### Design Note — Sharko is a GitOps agent

Sharko is built around a single principle: **the UI opens pull requests, it doesn't mutate state directly**. Default addons were the last holdout — they used to save live to the connection config. With the shift to a git-backed file, every action in Sharko follows the same pattern: edit, open PR, review, merge, apply.

## Viewing the Catalog

```bash
# CLI: list all addons with version and deployment stats
sharko list-addons

# Include full catalog config (secrets declarations, values):
sharko list-addons --show-config
```

In the UI, the **Addons** page shows:

- Every addon in the catalog with its current version
- Deployment stats (how many clusters have it enabled)
- Drift indicators (clusters running a different version)

The **Version Matrix** view shows an addon × cluster grid, making it easy to see which clusters are behind.

## Adding an Addon to the Catalog

### Preview Changes Before a PR Opens

Before submitting an addon addition (or removal, or configuration change), the UI offers a **"Preview changes"** button. Clicking it shows exactly what the PR would contain: which files would be written or deleted, the actual line-by-line change inside each file (added/removed lines), the PR title, and the names of any secrets it would create. Secret values are redacted (shown as `<redacted>`) while keys and structure remain visible. This dry-run costs nothing (no branch, no commit, no PR) and gives you full transparency before confirming.

This preview feature works on all addon operations: adding to the catalog, removing an addon, configuring values, and saving default addons. See [Preview Changes](clusters.md#preview-changes-before-a-pr-opens) for details, including a diff example and API usage.

### Via CLI

```bash
sharko add-addon cert-manager \
  --chart cert-manager \
  --repo https://charts.jetstack.io \
  --version 1.14.5
```

Flags:

| Flag | Required | Description |
|------|----------|-------------|
| `--chart` | Yes | Helm chart name |
| `--repo` | Yes | Helm repository URL |
| `--version` | Yes | Helm chart version to track |
| `--namespace` | No | Target namespace (defaults to addon name) |
| `--values` | No | Path to a values YAML file to use as the base |

Via UI: **Addons → Add Addon**, fill in the form.

The command creates a PR that adds the addon's directory structure to your Git repo.

## Configuring an Addon

Addon configuration lives in your addons Git repo as Helm values files:

```
addons/
  cert-manager/
    values.yaml          # base values for all clusters
    clusters/
      my-cluster.yaml    # per-cluster overrides
```

Edit these files directly in your repo, or use the UI's values editor on the cluster detail page to make per-cluster changes (creates a PR automatically when `gitops.actions.enabled: true`).

## Removing an Addon

```bash
sharko remove-addon cert-manager --confirm
```

Without `--confirm`, the command runs a dry-run and shows which clusters would be affected.

!!! warning
    Removing an addon from the catalog creates a PR that removes it from all cluster values files. After the PR is merged and ArgoCD syncs, the addon is uninstalled from every cluster. This is **irreversible without re-adding the addon**.

Via UI: **Addons → [addon name] → Remove** — the UI requires explicit confirmation.

## Addon Secrets

Some addons need API keys or credentials at runtime (e.g., Datadog, New Relic). Sharko delivers secrets directly to remote clusters using the same secrets provider configured for cluster credentials (AWS Secrets Manager or Kubernetes Secrets). No External Secrets Operator is required.

### Declaring Secrets in the Catalog

Secrets are declared in `addons-catalog.yaml` under the `secrets:` field of the addon:

```yaml
addons:
  - name: datadog
    chart: datadog
    repo: https://helm.datadoghq.com
    version: 3.74.0
    secrets:
      - secretName: datadog-keys
        namespace: datadog
        keys:
          api-key: secrets/datadog/api-key
          app-key: secrets/datadog/app-key
```

The `keys` map is `<k8s-key>: <provider-path>`. Sharko resolves each provider path, creates a `datadog-keys` Secret in the `datadog` namespace on every cluster that has the Datadog addon enabled.

### How Reconciliation Works

The secrets reconciler runs continuously in the background:

1. Every 5 minutes (default), Sharko re-fetches all declared secrets from the provider
2. It computes a SHA-256 hash of each value and compares it to the last known hash
3. If a value changed, Sharko updates the Kubernetes Secret on the affected remote clusters
4. ArgoCD is configured to ignore these secrets (resource exclusion), so it never deletes them

All Sharko-managed secrets are labeled `app.kubernetes.io/managed-by: sharko`.

### Manual Trigger

To force an immediate reconcile after rotating a secret in your provider:

```bash
sharko refresh-secrets               # reconcile all clusters
sharko refresh-secrets prod-eu       # reconcile a specific cluster
```

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

See [Upgrades](upgrades.md) for global and per-cluster upgrade workflows.

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

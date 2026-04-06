# Managing Addons

The addon catalog is the source of truth for which Helm charts Sharko manages across your fleet. Adding an addon to the catalog makes it available to assign to clusters. Removing it from the catalog removes it from all clusters.

## Viewing the Catalog

```bash
# CLI: list all addons with version and deployment stats
sharko list-addons  # (or check the UI Addons page)
```

In the UI, the **Addons** page shows:

- Every addon in the catalog with its current version
- Deployment stats (how many clusters have it enabled)
- Drift indicators (clusters running a different version)

The **Version Matrix** view shows an addon × cluster grid, making it easy to see which clusters are behind.

## Adding an Addon to the Catalog

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

Some addons need API keys or credentials at runtime (e.g., Datadog agent, New Relic). Define an addon secret template to have Sharko deliver the secret to each cluster when the addon is enabled:

```bash
curl -X POST https://sharko.your-domain.com/api/v1/addon-secrets \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "addon_name": "datadog",
    "secret_name": "datadog-keys",
    "namespace": "datadog",
    "keys": {
      "api-key": "secrets/datadog/api-key",
      "app-key": "secrets/datadog/app-key"
    }
  }'
```

The `keys` map resolves paths from your secrets provider (AWS Secrets Manager or Kubernetes Secrets) and creates a Kubernetes Secret on the remote cluster.

## Upgrading Addons

See [Upgrades](upgrades.md) for global and per-cluster upgrade workflows.

## Checking Addon Health

The addon detail view (UI) shows:

- Current version deployed per cluster
- Sync status from ArgoCD
- Drift: clusters where the running version differs from the catalog target
- ArgoCD health status (Healthy / Degraded / Progressing)

From the CLI:

```bash
sharko status
# Shows cluster-level health including addon counts
```

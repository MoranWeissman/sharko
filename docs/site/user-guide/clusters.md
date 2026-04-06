# Managing Clusters

Clusters are the units of deployment in Sharko. Each registered cluster gets its own values directory in the addons repo, and ArgoCD manages addon deployments for it via the ApplicationSet.

## Discovering Available Clusters

Before registering, you can see which clusters are available from your secrets provider:

```bash
sharko list-clusters --available
```

Or in the UI: **Clusters → Register Cluster → Browse Available**.

## Adding a Cluster

### Via CLI

```bash
sharko add-cluster my-cluster \
  --addons cert-manager,metrics-server,monitoring \
  --region us-east-1
```

Flags:

| Flag | Description |
|------|-------------|
| `--addons` | Comma-separated list of addons to enable on this cluster |
| `--region` | AWS region (for `aws-sm` secrets provider) |
| `--env` | Environment label (e.g., `prod`, `staging`) — auto-detected from name if `config.environments` is set |

### Via UI

1. Navigate to **Clusters → Register Cluster**
2. Select the cluster from the list of available clusters (fetched from your secrets provider)
3. Choose which addons to enable
4. Click **Register** — Sharko creates a PR in your Git repo

### Batch Registration

Register multiple clusters at once:

```bash
sharko add-clusters cluster-a,cluster-b,cluster-c \
  --addons cert-manager,metrics-server
```

Or via API:

```bash
curl -X POST https://sharko.your-domain.com/api/v1/clusters/batch \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "clusters": [
      {"name": "cluster-a", "addons": ["cert-manager"]},
      {"name": "cluster-b", "addons": ["cert-manager", "monitoring"]}
    ]
  }'
```

## Viewing Cluster Status

```bash
sharko status
```

Output shows all registered clusters with sync status, addon counts, and health indicators.

In the UI, the **Fleet** page shows health cards for every cluster. Click a cluster to see its detail page with:

- Addon list with per-addon health and version
- Drift detection (addons running a different version than the catalog target)
- ArgoCD sync status
- Connected/disconnected indicator

## Updating a Cluster

Update which addons are enabled on a cluster:

```bash
sharko update-cluster my-cluster \
  --addons cert-manager,metrics-server,logging
```

This creates a PR that adds or removes addon entries from the cluster's values file.

Via UI: on the cluster detail page, click **Edit Addons**.

## Removing a Cluster

```bash
sharko remove-cluster my-cluster
```

This creates a PR that removes the cluster's directory from the addons repo. After the PR is merged and ArgoCD syncs, the cluster's ApplicationSet entries are removed.

!!! warning
    Removing a cluster from Sharko does not uninstall addons from that cluster. ArgoCD will stop managing them, but the Helm releases remain in place. Uninstall addons manually if needed.

## Refreshing Cluster Credentials

If a cluster's kubeconfig or credentials have changed in the secrets provider:

```bash
# Via CLI
curl -X POST https://sharko.your-domain.com/api/v1/clusters/my-cluster/refresh \
  -H "Authorization: Bearer <token>"
```

Or in the UI: cluster detail page → **Refresh Credentials**.

## Cluster Environments

If `config.environments` is set (e.g., `"dev,qa,staging,prod"`), Sharko extracts the environment label from the cluster name automatically:

- `nms-core-dev-eks` → env `dev`
- `app-prod-eu-west` → env `prod`

This label is displayed in the UI and can be used for filtering. Set it manually with `--env` if auto-detection doesn't work for your naming convention.

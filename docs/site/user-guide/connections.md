# Managing Connections

Connections tell Sharko how to reach ArgoCD and your Git provider. Without active connections, read operations still work (clusters and addons already in ArgoCD are visible), but write operations (adding clusters, upgrading addons) require both connections to be active.

## Connection Types

| Type | Purpose |
|------|---------|
| **ArgoCD** | Read cluster/app health, sync status, resource trees |
| **Git** | Open PRs, push branches, read values files |

## Adding an ArgoCD Connection

1. Navigate to **Settings** (left sidebar, requires admin role)
2. Select **Connections** in the left navigation panel
3. Click **Add Connection → ArgoCD**
4. Fill in:
   - **Name** — a label for this connection (e.g., `production-argocd`)
   - **Server URL** — the ArgoCD server URL reachable from within the cluster (e.g., `https://argocd-server.argocd.svc.cluster.local`)
   - **Token** — an ArgoCD account token with `applications:get`, `projects:get` permissions
5. Click **Save**
6. Toggle the connection to **Active**

!!! tip "Generating an ArgoCD token"
    ```bash
    argocd account generate-token --account sharko
    ```
    Create a dedicated `sharko` account in ArgoCD with read permissions. Do not use the admin token.

## Adding a Git Connection

1. In **Settings → Connections**, click **Add Connection → Git**
2. Fill in:
   - **Provider** — `GitHub` or `Azure DevOps`
   - **Token** — PAT with `repo` read/write (GitHub) or `Code (Read & Write)` (Azure DevOps)
   - **Repository URL** — the addons repo URL (e.g., `https://github.com/your-org/your-addons-repo`)
3. Click **Save**
4. Toggle the connection to **Active**

## Verifying Connections

Sharko validates each connection when you save it. If validation fails, check:

- The URL is reachable from within the cluster (not just from your laptop)
- The token has not expired
- Network policies allow traffic from the Sharko pod to ArgoCD/GitHub

You can re-test a connection at any time by clicking **Test** next to an existing connection.

## API Keys {#api-keys}

API keys provide long-lived authentication for non-interactive consumers: Backstage plugins, Terraform providers, CI/CD pipelines.

### Creating an API Key

**Via CLI:**

```bash
sharko token create --name ci-pipeline --role viewer
# Output: sharko_a1b2c3d4... (shown once — save it!)
```

**Via UI:** Navigate to **Settings → API Keys → Create Key**.

Key format: `sharko_` prefix followed by 32 hex characters.

!!! warning
    The plaintext key is shown **only once** at creation time. Store it in your secrets manager immediately.

### Assigning Roles

| Role | Permissions |
|------|------------|
| `admin` | Full read/write access, manage users and API keys |
| `viewer` | Read-only access to clusters, addons, and health data |

### Revoking an API Key

```bash
sharko token revoke ci-pipeline
```

Or click **Revoke** in **Settings → API Keys**.

### Listing API Keys

```bash
sharko token list
```

## Rotating Credentials

When a Git token or ArgoCD token expires:

1. Generate a new token in GitHub/ArgoCD
2. In **Settings → Connections**, edit the connection and update the token
3. Click **Save** — Sharko validates and re-encrypts the credential immediately

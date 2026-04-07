# Managing Connections

Connections tell Sharko how to reach ArgoCD and your Git provider. Without active connections, read operations still work (clusters and addons already in ArgoCD are visible), but write operations (adding clusters, upgrading addons) require both connections to be active.

## Connection Types

| Type | Purpose |
|------|---------|
| **ArgoCD** | Read cluster/app health, sync status, resource trees |
| **Git** | Open PRs, push branches, read values files |

## Single Connection Model

Sharko maintains **one active connection per type** (one ArgoCD, one Git). You edit the existing connection — there is no list of multiple connections to manage. This keeps configuration simple: there is always exactly one source of truth for where Sharko sends changes.

## Three Entry Points

### 1. UI First-Run Wizard

On first access, Sharko shows a setup wizard that walks you through configuring the ArgoCD and Git connections. The wizard validates each connection before proceeding and guides you through the `sharko init` step at the end.

### 2. CLI

```bash
# Configure the Git connection:
sharko connect \
  --name production-git \
  --git-provider github \
  --git-repo https://github.com/your-org/addons-repo \
  --git-token ghp_xxxx

# Verify it works:
sharko connect test

# Show current connection:
sharko connect list
```

### 3. API

```bash
# Set/update the Git connection:
curl -X POST https://sharko.your-domain.com/api/v1/connections \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "git",
    "provider": "github",
    "repoURL": "https://github.com/your-org/addons-repo",
    "token": "ghp_xxxx"
  }'
```

## Configuring ArgoCD Connection

Via the UI (Settings → Connections → Edit):

- **Server URL** — ArgoCD server URL reachable from within the cluster (e.g., `https://argocd-server.argocd.svc.cluster.local`)
- **Token** — ArgoCD account token with `applications:get`, `projects:get` permissions

!!! tip "Generating an ArgoCD token"
    ```bash
    argocd account generate-token --account sharko
    ```
    Create a dedicated `sharko` account in ArgoCD with read permissions. Do not use the admin token.

## Configuring Git Connection

Via the UI (Settings → Connections → Edit):

- **Provider** — `GitHub` or `Azure DevOps`
- **Token** — PAT with `repo` read/write (GitHub) or `Code (Read & Write)` (Azure DevOps)
- **Repository URL** — the addons repo URL (e.g., `https://github.com/your-org/your-addons-repo`)

## Verifying Connections

Sharko validates each connection when you save it. If validation fails, check:

- The URL is reachable from within the cluster (not just from your laptop)
- The token has not expired
- Network policies allow traffic from the Sharko pod to ArgoCD/GitHub

You can re-test at any time:

```bash
sharko connect test
```

Or click **Test** in Settings → Connections.

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

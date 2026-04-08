# Settings

After the first-run wizard, all configuration lives in the **Settings** page. It has six sections accessible from the left navigation panel.

## Connection

Sharko maintains one active ArgoCD connection and one active Git connection. You edit these in place — there is no list of multiple connections.

### ArgoCD

| Field | Description |
|-------|-------------|
| **Server URL** | ArgoCD server URL reachable from within the cluster (e.g., `https://argocd-server.argocd.svc.cluster.local`) |
| **Token** | ArgoCD account token with `applications:get`, `projects:get` permissions |

!!! tip "Generating an ArgoCD token"
    ```bash
    argocd account generate-token --account sharko
    ```
    Create a dedicated `sharko` account with read permissions. Do not use the admin token.

### Git

| Field | Description |
|-------|-------------|
| **Provider** | GitHub or Azure DevOps |
| **Repository URL** | The addons repo URL (e.g., `https://github.com/your-org/addons-repo`) |
| **Personal Access Token** | PAT with `repo` read/write (GitHub) or `Code (Read & Write)` (Azure DevOps) |

Sharko validates each connection when you save it. To re-test at any time:

```bash
sharko connect test
```

Or click **Test** in the Connection settings.

### CLI

```bash
# Configure the Git connection:
sharko connect \
  --git-provider github \
  --git-repo https://github.com/your-org/addons-repo \
  --git-token ghp_xxxx

# Verify:
sharko connect test

# Show current:
sharko connect list
```

### Rotating Credentials

When a token expires:

1. Generate a new token in GitHub or ArgoCD
2. In **Settings → Connection**, update the token field
3. Click **Save** — Sharko validates and re-encrypts the credential immediately

## Secrets Provider

Configure how Sharko fetches cluster kubeconfigs.

| Provider | Description |
|----------|-------------|
| `aws-sm` | AWS Secrets Manager — IRSA for auth, no static credentials |
| `k8s-secrets` | Kubernetes Secrets — no cloud dependency |

**For `aws-sm`:** Set the AWS region. Sharko uses IRSA for authentication — configure the IRSA role annotation at Helm install time (see [Installation](../getting-started/installation.md#aws-secrets-manager-optional)).

**For `k8s-secrets`:** Set the Kubernetes namespace where cluster secrets are stored.

## GitOps

Controls how Sharko creates PRs in your addons repo.

| Setting | Description | Default |
|---------|-------------|---------|
| **Auto-merge** | Merge PRs immediately after creation | off |
| **Branch prefix** | Prefix for PR branches | `sharko/` |
| **Commit prefix** | Prefix for commit messages | `sharko:` |
| **Base branch** | Target branch for PRs | `main` |

## Users

Change the admin password.

## API Keys {#api-keys}

API keys provide long-lived authentication for non-interactive consumers: Backstage plugins, Terraform providers, CI/CD pipelines.

Key format: `sharko_` prefix followed by 32 hex characters.

### Creating an API Key

**Via UI:** Navigate to **Settings → API Keys → Create Key**.

**Via CLI:**

```bash
sharko token create --name ci-pipeline --role viewer
# Output: sharko_a1b2c3d4... (shown once — save it!)
```

!!! warning
    The plaintext key is shown **only once** at creation time. Store it in your secrets manager immediately.

### Roles

| Role | Permissions |
|------|------------|
| `admin` | Full read/write access, manage users and API keys |
| `viewer` | Read-only access to clusters, addons, and health data |

### Managing Keys

```bash
sharko token list           # List all keys
sharko token revoke <name>  # Revoke a key
```

Or use **Settings → API Keys** in the UI.

## AI

Configure the AI assistant provider.

| Field | Description |
|-------|-------------|
| **Provider** | `openai`, `claude`, `gemini`, `ollama`, or `custom` |
| **Model** | Model name (e.g., `gpt-4o`, `claude-3-5-sonnet`) |
| **API Key** | Provider API key (not required for Ollama) |
| **Base URL** | Custom endpoint for OpenAI-compatible providers |

The AI assistant appears as a floating panel accessible from any page. It is context-aware — it knows which cluster or addon you are currently viewing.

Write tools (enable/disable addons, upgrade versions, sync ArgoCD apps) require admin role and explicit opt-in in the AI settings.

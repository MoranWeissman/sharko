# CLI Commands

Full reference for all `sharko` commands.

## Authentication

### `sharko login`

Authenticate with a Sharko server. Stores the session token in `~/.sharko/config.yaml`.

```bash
sharko login --server https://sharko.your-domain.com

# Non-interactive (CI/CD):
sharko login --server https://sharko.your-domain.com --username admin --password mypassword
```

| Flag | Description |
|------|-------------|
| `--server <url>` | Sharko server URL (required) |
| `--username <user>` | Username (prompted if not provided) |
| `--password <pass>` | Password (prompted if not provided) |

### `sharko version`

Show the CLI version and the server version.

```bash
sharko version
# CLI: v1.0.0
# Server: v1.0.0
```

---

## Initialization

### `sharko init`

Initialize the addons repository. Creates the initial directory structure (ApplicationSet, base values, cluster directory) in your Git repo via a PR.

```bash
sharko init
```

Run once per repository. Requires an active Git connection in Settings.

Init is asynchronous — the CLI prints an operation ID and streams log lines until the operation completes:

```
Initializing repository...
Operation ID: op_a1b2c3d4
[10:01:05] Creating branch sharko/init-2026-04-06...
[10:01:06] Committing scaffold files (12 files)...
[10:01:08] Creating pull request...
[10:01:09] Auto-merging PR #42...
[10:01:12] Waiting for ArgoCD sync...
[10:01:30] Done. Root application is Healthy.
```

With auto-merge disabled (`SHARKO_GITOPS_PR_AUTO_MERGE=false`), the init completes after the PR is created. Merge the PR manually, then re-run `sharko init --resume <operation-id>` to continue ArgoCD bootstrap.

### `sharko validate`

Validate a catalog YAML file against the Sharko schema without pushing any changes.

```bash
sharko validate                   # validates addons-catalog.yaml in current directory
sharko validate path/to/catalog.yaml
```

Exits 0 on valid, 1 on schema errors (printed to stderr).

---

## Cluster Commands

### `sharko add-cluster`

Register a cluster with Sharko.

```bash
sharko add-cluster <name> [flags]
```

| Flag | Description |
|------|-------------|
| `--addons <list>` | Comma-separated list of addons to enable |
| `--region <region>` | AWS region (for `aws-sm` provider) |
| `--env <env>` | Environment label (`dev`, `staging`, `prod`, etc.) |

Example:

```bash
sharko add-cluster prod-eu \
  --addons cert-manager,monitoring,logging \
  --region eu-west-1 \
  --env prod
```

### `sharko add-clusters`

Batch register multiple clusters in a single API call (up to 10).

```bash
sharko add-clusters cluster-a,cluster-b,cluster-c \
  --addons cert-manager,metrics-server
```

### `sharko remove-cluster`

Deregister a cluster. Creates a PR to remove the cluster's directory.

```bash
sharko remove-cluster <name>
```

### `sharko update-cluster`

Update the addon assignments for a cluster.

```bash
sharko update-cluster <name> --addons cert-manager,metrics-server,logging
```

### `sharko list-clusters`

List all registered clusters.

```bash
sharko list-clusters
```

### `sharko status`

Show cluster status overview: sync status, addon counts, health.

```bash
sharko status
```

---

## Addon Commands

### `sharko add-addon`

Add an addon to the catalog.

```bash
sharko add-addon <name> [flags]
```

| Flag | Required | Description |
|------|----------|-------------|
| `--chart <name>` | Yes | Helm chart name |
| `--repo <url>` | Yes | Helm repository URL |
| `--version <ver>` | Yes | Chart version |
| `--namespace <ns>` | No | Target namespace (defaults to addon name) |
| `--values <file>` | No | Base values YAML file |

Example:

```bash
sharko add-addon ingress-nginx \
  --chart ingress-nginx \
  --repo https://kubernetes.github.io/ingress-nginx \
  --version 4.9.0 \
  --namespace ingress-nginx
```

### `sharko remove-addon`

Remove an addon from the catalog and all clusters.

```bash
sharko remove-addon <name> [--confirm]
```

Without `--confirm`, runs a dry-run and shows affected clusters. With `--confirm`, creates the removal PR.

### `sharko upgrade-addon`

Upgrade an addon version, globally or per-cluster.

```bash
sharko upgrade-addon <name> --version <ver> [--cluster <name>]
```

| Flag | Description |
|------|-------------|
| `--version <ver>` | Target version (required) |
| `--cluster <name>` | Upgrade only this cluster (omit for global) |

Examples:

```bash
# Global upgrade
sharko upgrade-addon cert-manager --version 1.15.0

# Per-cluster upgrade
sharko upgrade-addon cert-manager --version 1.15.0 --cluster staging
```

### `sharko upgrade-addons`

Batch upgrade multiple addons in a single PR.

```bash
sharko upgrade-addons cert-manager=1.15.0,metrics-server=0.7.1
```

### `sharko list-addons`

List all addons in the catalog. Use `--show-config` to include the full catalog configuration for each addon (secrets declarations, values, etc.).

```bash
sharko list-addons
sharko list-addons --show-config
```

---

## Connection Commands

### `sharko connect`

Configure the active Git connection. Replaces the current connection.

```bash
sharko connect \
  --name my-git-connection \
  --git-provider github \
  --git-repo https://github.com/your-org/addons-repo \
  --git-token ghp_xxxx
```

| Flag | Description |
|------|-------------|
| `--name <name>` | Connection name (required) |
| `--git-provider <provider>` | `github` or `azure-devops` (required) |
| `--git-repo <url>` | Addons repository URL (required) |
| `--git-token <token>` | PAT or access token (required) |

### `sharko connect list`

Show the current active connection.

```bash
sharko connect list
```

### `sharko connect test`

Test the current active connection (validates credentials and repo access).

```bash
sharko connect test
```

---

## Secrets Commands

### `sharko refresh-secrets`

Trigger an immediate secrets reconcile. Useful after rotating a secret in your provider.

```bash
sharko refresh-secrets               # reconcile all clusters
sharko refresh-secrets prod-eu       # reconcile a specific cluster
```

### `sharko secret-status`

Show the current reconciler status per cluster: last run time, hash comparison result, and any errors.

```bash
sharko secret-status
```

---

## API Key Commands

### `sharko token create`

Create a new API key.

```bash
sharko token create --name <name> --role <role>
```

| Flag | Description |
|------|-------------|
| `--name <name>` | Key name for identification (required) |
| `--role <role>` | `admin` or `viewer` (required) |

Output includes the plaintext key — shown once only.

### `sharko token list`

List all API keys (names, roles, creation dates — not plaintext keys).

```bash
sharko token list
```

### `sharko token revoke`

Revoke an API key by name.

```bash
sharko token revoke <name>
```

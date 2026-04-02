# Sharko User Guide

This guide covers installing Sharko, configuring it, and using the CLI and fleet dashboard to manage addons across your Kubernetes clusters.

---

## Installation

### Prerequisites

- A Kubernetes cluster (1.27+) with ArgoCD installed
- Helm 3.x
- A GitHub Personal Access Token (PAT) with repo access

### Helm Install

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/charts/sharko \
  --namespace sharko --create-namespace \
  --set secrets.GITHUB_TOKEN=<github-pat>
```

Optional flags:

```bash
# Enable AI assistant (e.g., with OpenAI)
--set ai.enabled=true \
--set ai.provider=openai \
--set ai.apiKey=<openai-api-key> \
--set ai.cloudModel=gpt-4o

# Enable GitOps write operations (PR creation from UI/AI)
--set gitops.actions.enabled=true

# Use an existing secret instead of chart-managed secrets
--set existingSecret=my-sharko-secret
```

### Verify Installation

```bash
kubectl get pods -n sharko
kubectl get svc -n sharko
```

The Sharko server should be running and accessible on port 80 (ClusterIP by default).

---

## First Login

On first install, Sharko creates an admin account with a random password. Retrieve it:

```bash
kubectl get secret sharko -n sharko \
  -o jsonpath='{.data.admin\.initialPassword}' | base64 -d
```

Then log in via the CLI:

```bash
sharko login --server https://sharko.your-cluster.com
# Enter: admin / <initial-password>
```

Or open the UI in a browser and use the same credentials. You will be prompted to change the password on first login.

---

## Configuring Connections

Sharko manages connections to ArgoCD and Git providers through the Settings UI. After install:

1. Open the Sharko UI in your browser
2. Navigate to **Settings**
3. Add an **ArgoCD connection**: provide the ArgoCD server URL and an account token
4. Add a **Git connection**: select GitHub (or Azure DevOps), provide the token and repo URL
5. Set both connections as **active**

You can test each connection from the Settings page before activating it. Connections are stored in an encrypted Kubernetes Secret.

For local development, set `config.devMode: true` in Helm values to enable environment variable fallback (`ARGOCD_TOKEN`, `GITHUB_TOKEN`, etc.).

---

## CLI Usage

The Sharko CLI is a thin HTTP client. Every command sends a request to the Sharko server API.

### Login

```bash
sharko login --server https://sharko.your-cluster.com
# Prompts for username and password, stores token in ~/.sharko/config
```

### Initialize Addons Repo

```bash
sharko init
```

This creates the addons repository structure from the embedded starter templates and pushes it to Git via the configured connection. The generated structure includes bootstrap ApplicationSet templates, directory layout for cluster values, and global values.

### Add an Addon

```bash
sharko add-addon cert-manager \
  --chart cert-manager \
  --repo https://charts.jetstack.io \
  --version 1.14.5 \
  --namespace cert-manager
```

### Register a Cluster

```bash
sharko add-cluster prod-eu \
  --addons cert-manager,metrics-server \
  --region eu-west-1
```

The server fetches cluster credentials from the configured secrets provider, registers the cluster in ArgoCD, creates a values file, and commits to Git (as a PR or direct commit depending on server config).

### Remove a Cluster

```bash
sharko remove-cluster prod-eu
```

### Update Cluster Addons

```bash
sharko update-cluster prod-eu --add-addon istio --remove-addon logging
```

### Fleet Status

```bash
sharko status
```

Example output:

```
Fleet Status
============
Clusters: 12 total, 11 healthy, 1 degraded
Addons:   8 in catalog
Sync:     94 synced, 2 out-of-sync, 1 unknown

Degraded Clusters:
  staging-us  2 addons out-of-sync (cert-manager, monitoring)

Version Drift:
  metrics-server  3 versions across fleet (0.6.3, 0.6.4, 0.7.0)
```

### Check Version

```bash
sharko version
```

---

## Fleet Dashboard

The Sharko UI is a React-based fleet dashboard accessible via the Sharko service URL. It provides:

### Clusters View

- List of all registered clusters with health status (healthy, degraded, unknown)
- Per-cluster detail: deployed addons, sync status, server version
- Config diff view: compare a cluster's values against global defaults

### Addon Catalog

- All addons available in the fleet with chart, version, and deployment count
- Per-addon detail: which clusters have it deployed, version distribution

### Version Matrix

- Grid view: addons (rows) x clusters (columns)
- Color-coded cells showing version alignment and drift
- Quickly identify which clusters are behind on a specific addon

### Observability

- ArgoCD health groups (healthy, degraded, missing)
- Sync activity timeline
- Attention items: clusters or addons that need action

### Dashboard Stats

- Total clusters, healthy count, degraded count
- Total addons, sync status breakdown
- Recent pull requests created by Sharko

---

## AI Assistant Configuration

Sharko includes an AI assistant for troubleshooting and fleet insights. Configure it via Helm values or the Settings UI.

### Supported Providers

| Provider | Helm Key | Notes |
|----------|----------|-------|
| Ollama | `ai.provider: ollama` | Self-hosted, runs alongside Sharko |
| OpenAI | `ai.provider: openai` | Requires API key |
| Claude | `ai.provider: claude` | Requires API key |
| Gemini | `ai.provider: gemini` | Requires API key |
| Custom OpenAI | `ai.provider: custom-openai` | Any OpenAI-compatible endpoint |

### Helm Configuration Example

```yaml
ai:
  enabled: true
  provider: openai
  apiKey: "sk-..."
  cloudModel: "gpt-4o"
  maxIterations: 8
```

### Ollama (Self-Hosted)

```yaml
ai:
  enabled: true
  provider: ollama
  ollama:
    deploy: true              # Auto-deploy Ollama pod
    model: "llama3.2"         # Default model
    agentModel: "llama3.1:8b" # Larger model for agent tool calling
    persistence: true         # Persist downloaded models across restarts
    storageSize: "10Gi"
```

### Runtime Configuration

The AI provider can also be configured at runtime via the Settings UI without redeploying. Runtime settings are persisted in an encrypted Kubernetes Secret and override Helm values.

---

## Embedded Dashboards

Sharko supports embedding external dashboards (Grafana, Datadog, etc.) in the UI.

1. Open the Sharko UI
2. Navigate to **Embedded Dashboards**
3. Add a dashboard URL (e.g., a Grafana dashboard iframe URL)
4. Dashboards are persisted in a Kubernetes ConfigMap

---

## Troubleshooting

### Common Errors

**"no active ArgoCD connection"**
No ArgoCD connection is configured or set as active. Go to Settings and add/activate an ArgoCD connection.

**"no active Git connection"**
Same as above, but for Git. Configure a Git connection in Settings.

**"secrets provider not configured"**
The `SHARKO_PROVIDER_TYPE` environment variable is not set. Set it via Helm values (`extraEnv`) or configure a provider.

**"template filesystem not configured"**
Internal error. The StarterFS should always be available. Check that the Sharko binary was built correctly.

### Check Logs

```bash
kubectl logs -n sharko deployment/sharko -f
```

### Check Health

```bash
curl https://sharko.your-cluster.com/api/v1/health
```

### Reset Admin Password

If you lose the admin password, delete the secret and restart:

```bash
kubectl delete secret sharko -n sharko
kubectl rollout restart deployment/sharko -n sharko
```

A new random admin password will be generated. Retrieve it with the `kubectl get secret` command shown in the First Login section.

### Connection Issues

If ArgoCD or Git connections fail:

1. Test the connection from the Settings UI (click "Test")
2. Check that the ArgoCD account token has sufficient permissions
3. Verify the GitHub PAT has `repo` scope
4. Check network connectivity from the Sharko pod to ArgoCD/GitHub

---

## Environment Variables Reference

| Variable | Description | Default |
|----------|-------------|---------|
| `SHARKO_PORT` | HTTP server port | `8080` |
| `SHARKO_PROVIDER_TYPE` | Secrets provider (`aws-sm`, `k8s-secrets`) | (none) |
| `SHARKO_PROVIDER_REGION` | AWS region for secrets provider | (none) |
| `SHARKO_ENCRYPTION_KEY` | Encryption key for connection store (required in K8s) | (none) |
| `SHARKO_DEV_MODE` | Enable env var fallback for credentials | `false` |
| `SHARKO_GITOPS_DEFAULT_MODE` | Commit mode: `pr` or `direct` | `pr` |
| `SHARKO_GITOPS_BRANCH_PREFIX` | Branch prefix for PR mode | `sharko/` |
| `SHARKO_GITOPS_COMMIT_PREFIX` | Commit message prefix | `sharko:` |
| `SHARKO_GITOPS_BASE_BRANCH` | Target branch | `main` |
| `SHARKO_GITOPS_REPO_URL` | Git repo URL for template placeholders | (none) |
| `GITHUB_TOKEN` | GitHub PAT | (none) |
| `AI_PROVIDER` | AI provider (`ollama`, `openai`, `claude`, `gemini`, `custom-openai`) | (none) |
| `AI_API_KEY` | API key for cloud AI provider | (none) |
| `AI_CLOUD_MODEL` | Model name for cloud AI provider | (none) |

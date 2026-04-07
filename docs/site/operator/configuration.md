# Configuration Reference

All Sharko configuration is managed via Helm values. This page documents every supported option.

## Connection Config

| Value | Type | Default | Description |
|-------|------|---------|-------------|
| `config.connectionSecretName` | string | `"sharko-connections"` | Name of the Kubernetes Secret where connections are stored (encrypted) |
| `config.devMode` | bool | `false` | Enable env var fallback for credentials (`GITHUB_TOKEN`, `ARGOCD_TOKEN`, etc.). Use only for local dev — not in production |
| `config.nodeAccess` | bool | `false` | Grant Sharko read access to Kubernetes Nodes (get/list). Opt-in — adds a ClusterRole rule |
| `config.environments` | string | `""` | Comma-separated keywords extracted from cluster names to infer environment. Example: `"dev,qa,staging,prod"` — cluster `"my-app-prod-eks"` → env `"prod"` |
| `config.repoURL` | string | `""` | Git repo URL for the addons repository. Falls back to the active connection's repo URL if empty |

## Authentication {#auth}

Authentication is managed via Kubernetes-native resources:

- **ConfigMap `sharko-users`** — user accounts (auto-created by Helm)
- **Secret** — bcrypt password hashes (auto-generated on first install)

On first install, an admin account is created with a random password. Retrieve it:

```bash
kubectl get secret sharko -n sharko \
  -o jsonpath='{.data.admin\.initialPassword}' | base64 -d
```

For local development outside Kubernetes, set these environment variables instead of using the K8s resources:

```bash
SHARKO_AUTH_USER=admin
SHARKO_AUTH_PASSWORD=mypassword
```

## Secrets

| Value | Type | Default | Description |
|-------|------|---------|-------------|
| `existingSecret` | string | `""` | Name of an existing Secret containing env vars. When set, the chart does **not** create a secret |
| `secrets.GITHUB_TOKEN` | string | `""` | GitHub PAT for Git operations (used when `existingSecret` is empty) |

!!! warning "Production recommendation"
    Use `existingSecret` with Sealed Secrets or External Secrets Operator rather than passing tokens as Helm values. Helm values are stored in cluster secrets and visible via `helm get values`.

## GitOps Actions

| Value | Type | Default | Description |
|-------|------|---------|-------------|
| `gitops.actions.enabled` | bool | `false` | Enable write operations (PR creation from UI and AI assistant). Set to `true` to allow Sharko to push branches and open PRs |

## AI Provider {#ai-provider}

| Value | Type | Default | Description |
|-------|------|---------|-------------|
| `ai.enabled` | bool | `false` | Enable the AI assistant |
| `ai.provider` | string | `""` | One of: `ollama`, `claude`, `openai`, `gemini`, `custom-openai` |
| `ai.apiKey` | string | `""` | API key for cloud providers (claude/openai/gemini) — stored in the chart-managed Secret |
| `ai.cloudModel` | string | `""` | Model name. Examples: `claude-sonnet-4-20250514`, `gpt-4o`, `gemini-2.5-flash` |
| `ai.baseURL` | string | `""` | Base URL for `custom-openai` providers (enterprise LLM gateways) |
| `ai.authHeader` | string | `""` | Custom auth header name for `custom-openai` (default: `Authorization`) |
| `ai.maxIterations` | int | `8` | Tool-calling loop limit. Increase for complex migration workflows |

### Ollama (self-hosted)

| Value | Type | Default | Description |
|-------|------|---------|-------------|
| `ai.ollama.deploy` | bool | `false` | Deploy an Ollama pod alongside Sharko |
| `ai.ollama.image` | string | `"ollama/ollama:latest"` | Ollama container image (~1.2 GB compressed) |
| `ai.ollama.url` | string | `""` | External Ollama URL. Auto-set when `deploy=true` |
| `ai.ollama.model` | string | `"llama3.2"` | Model for simple queries |
| `ai.ollama.agentModel` | string | `""` | Separate model for agent/tool-calling. Leave empty to use `model` |
| `ai.ollama.gpu` | bool | `false` | Enable GPU support (requires nvidia device plugin) |
| `ai.ollama.persistence` | bool | `false` | Persist downloaded models across restarts. **Strongly recommended** — without this, models are re-downloaded on every pod restart |
| `ai.ollama.storageClassName` | string | `""` | StorageClass for the model PVC (empty = cluster default) |
| `ai.ollama.storageSize` | string | `"10Gi"` | PVC size. 10 Gi fits 1–2 small models; 50 Gi+ for larger models |

**Model resource requirements:**

| Model | RAM | Tool Calling |
|-------|-----|-------------|
| `llama3.2` (3B) | 2–4 GB | Weak |
| `llama3.1:8b` | 6–8 GB | Moderate |
| `qwen2.5` (7B) | 4–6 GB | Good |
| `mistral` (7B) | 4–6 GB | Moderate |
| `llama3.1:70b` | 40+ GB | Strong (needs GPU) |

## Resources

| Value | Default | Description |
|-------|---------|-------------|
| `resources.requests.memory` | `128Mi` | Memory request |
| `resources.requests.cpu` | `100m` | CPU request |
| `resources.limits.memory` | `512Mi` | Memory limit |
| `resources.limits.cpu` | `500m` | CPU limit |

Adjust based on cluster count and expected traffic. For large fleets (100+ clusters), consider increasing memory limits.

## Probes

Liveness and readiness probes hit `/api/v1/health`. Defaults are appropriate for most deployments:

| Value | Default |
|-------|---------|
| `livenessProbe.initialDelaySeconds` | `5` |
| `livenessProbe.periodSeconds` | `10` |
| `readinessProbe.initialDelaySeconds` | `3` |
| `readinessProbe.periodSeconds` | `5` |

## Security Context

Sharko runs as a non-root user with a read-only root filesystem by default:

| Value | Default |
|-------|---------|
| `podSecurityContext.runAsNonRoot` | `true` |
| `podSecurityContext.runAsUser` | `1001` |
| `securityContext.readOnlyRootFilesystem` | `true` |
| `securityContext.allowPrivilegeEscalation` | `false` |
| `securityContext.capabilities.drop` | `["ALL"]` |

## Persistence

For migration state storage:

| Value | Default | Description |
|-------|---------|-------------|
| `persistence.enabled` | `false` | Enable a PersistentVolumeClaim |
| `persistence.storageClassName` | `""` | StorageClass (empty = cluster default) |
| `persistence.size` | `1Gi` | PVC size |

## Scheduling

| Value | Description |
|-------|-------------|
| `nodeSelector` | Node selector labels |
| `tolerations` | Pod tolerations |
| `affinity` | Affinity/anti-affinity rules |
| `hostAliases` | Host aliases for private DNS resolution |

## AWS Secrets Manager — Secret Formats

When using `SHARKO_PROVIDER_TYPE=aws-sm`, each cluster secret in AWS SM can be stored in one of two formats. Sharko auto-detects which format is used.

### Format 1 — Raw Kubeconfig (original)

The secret value is a YAML kubeconfig string:

```yaml
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://...
    certificate-authority-data: <base64>
  name: my-cluster
# ... (full kubeconfig)
```

### Format 2 — Structured JSON

The secret value is a JSON object with individual fields. This is simpler to manage programmatically:

```json
{
  "server": "https://abc123.gr7.us-east-1.eks.amazonaws.com",
  "ca": "<base64-encoded-ca-data>",
  "token": "<bearer-token>"
}
```

For EKS clusters where you want Sharko to generate a short-lived STS token (recommended), provide `cluster_name` and `role_arn` instead of a static token:

```json
{
  "server": "https://abc123.gr7.us-east-1.eks.amazonaws.com",
  "ca": "<base64-encoded-ca-data>",
  "cluster_name": "prod-eu",
  "role_arn": "arn:aws:iam::123456789012:role/EKSReadRole"
}
```

Sharko calls the EKS STS token API to generate a `k8s-aws-v1.*` bearer token on each credential fetch. Tokens are valid for 15 minutes and are never stored.

### IRSA Setup

For STS-based token generation, the Sharko pod must run with an IAM role that has permission to call EKS and assume the target role:

```yaml
# charts/sharko/values.yaml
serviceAccount:
  annotations:
    eks.amazonaws.com/role-arn: "arn:aws:iam::123456789012:role/SharkoIRSARole"
```

Required IAM permissions for the Sharko IRSA role:

```json
{
  "Effect": "Allow",
  "Action": [
    "secretsmanager:GetSecretValue",
    "secretsmanager:ListSecrets",
    "eks:DescribeCluster"
  ],
  "Resource": "*"
}
```

For cross-account EKS clusters, also add:

```json
{
  "Effect": "Allow",
  "Action": "sts:AssumeRole",
  "Resource": "arn:aws:iam::*:role/EKSReadRole"
}
```

## Advanced: Addon Secrets

For addons that require API keys delivered to remote clusters (e.g., Datadog, New Relic), define addon secret templates:

```yaml
# JSON string mapping addon name → secret definition
addonSecrets: |
  {
    "datadog": {
      "addon_name": "datadog",
      "secret_name": "datadog-keys",
      "namespace": "datadog",
      "keys": {
        "api-key": "secrets/datadog/api-key",
        "app-key": "secrets/datadog/app-key"
      }
    }
  }
```

## Advanced: Default Addons

Addons automatically enabled when a cluster is registered without an explicit addon list:

```yaml
defaultAddons: "cert-manager,metrics-server,monitoring"
```

## Advanced: Host Cluster Name

When Sharko runs on one of the managed clusters, set this to use in-cluster credentials for that cluster instead of fetching them from the secrets provider:

```yaml
hostClusterName: "management"
```

## Secrets Reconciler

| Value | Type | Default | Description |
|-------|------|---------|-------------|
| `secrets.reconciler.enabled` | bool | `true` | Enable the background secrets reconciler |
| `secrets.reconciler.interval` | string | `"5m"` | How often to check for secret changes. Maps to `SHARKO_SECRET_RECONCILE_INTERVAL`. Format: Go duration (`5m`, `1h`, `30s`) |
| `secrets.webhookSecret` | string | `""` | HMAC-SHA256 secret for validating `POST /api/v1/webhooks/git`. Maps to `SHARKO_WEBHOOK_SECRET` |

The secrets reconciler uses the **same secrets provider** configured for cluster credentials (`SHARKO_PROVIDER_TYPE`). No separate provider configuration is needed.

When `secrets.webhookSecret` is set, Sharko verifies the `X-Hub-Signature-256` header on every webhook call. Requests without a valid signature are rejected with 401. If the secret is empty, HMAC verification is skipped (useful for internal environments without a gateway).

```yaml
# Example: enable reconciler with webhook verification
secrets:
  reconciler:
    enabled: true
    interval: "10m"
  webhookSecret: "your-hmac-secret"
```

Equivalent env vars:

```yaml
extraEnv:
  - name: SHARKO_SECRET_RECONCILE_INTERVAL
    value: "10m"
  - name: SHARKO_WEBHOOK_SECRET
    valueFrom:
      secretKeyRef:
        name: my-webhook-secret
        key: secret
```

## Extra Environment Variables

Inject arbitrary environment variables into the Sharko pod:

```yaml
extraEnv:
  - name: SHARKO_GITOPS_PR_AUTO_MERGE
    value: "true"
  - name: SHARKO_GITOPS_BASE_BRANCH
    value: "main"
```

Full list of supported env vars is in the [README](https://github.com/MoranWeissman/sharko#configuration).

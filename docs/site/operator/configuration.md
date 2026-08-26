# Configuration Reference

Most Sharko configuration is managed via Helm values, documented on this page. A small set of server-wide settings — `probe_mode` and `allow_inline_credentials` — can be declared in Helm values as of v3.0.0 (git-wins precedence: a Helm-declared value is authoritative and Sharko reconciles toward it; a runtime UI edit is reclaimed within 60 seconds). When left undeclared (empty in Helm values), they are runtime toggles instead: stored in Sharko's in-cluster settings store, admin-only, editable from **Settings** in the UI (or their `/api/v1/settings/*` endpoints) with no restart or redeploy needed. See [Git-Native Server Configuration](git-native-config.md) for the full git-wins model and [Server Settings in the connections guide](../user-guide/connections.md#server-settings) for what each one does.

> Sharko-owned YAML config files wrap content in an
> `apiVersion: sharko.dev/v1` envelope with schema-validated structure.
> ArgoCD cluster Secret lifecycle is managed by an in-Pod reconciler
> goroutine — Secrets are created post-merge (no orphan-on-PR-close),
> gated by an `app.kubernetes.io/managed-by: sharko` ownership label.
> See [cluster reconciler](./cluster-reconciler.md) for ownership
> semantics, reconcile cadence, and recovery scenarios.

## The listen port {#listen-port}

The port Sharko's HTTP server listens on is `SHARKO_HTTP_PORT`. The chart
sets it to `8080` and you do not normally touch it.

| Setting | Default | Description |
|---------|---------|-------------|
| `SHARKO_HTTP_PORT` | `8080` | TCP port the server listens on. A whole number from 1 to 65535 |
| `SHARKO_PORT` | — | The old name for the same thing. Still works, and warns once at startup. Do not use it in new installs |

If the value is not a whole number in range, Sharko does not start. It says
which setting is wrong and stops, rather than listening on some other port
and leaving you to work out why nothing reaches it. Earlier versions read
`80x` as `80` and said nothing.

### If you are upgrading

Nothing breaks. If you set `SHARKO_PORT` yourself, it keeps working and
Sharko logs one line at startup telling you the new name. Rename it to
`SHARKO_HTTP_PORT` when convenient. Do not set both to different numbers —
Sharko will refuse to start rather than guess which one you meant. Setting
both to the same number is fine.

If you install with the Helm chart, this is already done for you: the chart
sets `SHARKO_HTTP_PORT`.

### If you write your own Kubernetes manifests {#service-links}

**You must set `enableServiceLinks: false` on the Sharko Pod.** The chart
does this; a hand-written Deployment will not unless you add it.

```yaml
spec:
  template:
    spec:
      enableServiceLinks: false   # <- required
      containers:
        - name: sharko
          # ...
```

It belongs in the Pod specification, next to `containers` — it is not a
container field.

Here is what goes wrong without it. Kubernetes puts an environment variable
into every Pod for each Service in the same namespace, named after that
Service. Sharko's own Service is normally called `sharko`, so Sharko's Pod
gets handed about ten variables starting with `SHARKO_` that nobody asked
for, including:

```
SHARKO_SERVICE_HOST=10.96.35.88
SHARKO_SERVICE_PORT=80
SHARKO_PORT=tcp://10.96.35.88:80
SHARKO_PORT_80_TCP=tcp://10.96.35.88:80
```

That third one collides with Sharko's own old port setting. Sharko knows
about this and ignores a `SHARKO_PORT` that looks like the address
Kubernetes writes, so it will not misread it as a port and it will not warn
you about a deprecated setting you never set. But the collision is only
handled for this one name, and every one of those variables is noise in
your Pod that exists for a discovery mechanism Sharko does not use. Turn it
off.

Setting the field is the only reliable fix. A list of variable names to
ignore does not work: the names come from the Service name, so they change
the moment somebody installs under a different release name.

## A setting Sharko does not recognise stops it starting {#unknown-settings}

Every environment variable starting with `SHARKO_` has to be one Sharko
really reads. If you set one it does not know, Sharko says so by name and
does not start.

```
<the name you set> is not a Sharko setting. Did you mean <the nearest real
one>? Sharko has not started ...
```

The "did you mean" half only appears when a real setting is close enough
to be the one you meant — one letter dropped from `SHARKO_LOG_LEVEL`, say.

This is on purpose, and it replaces the old behaviour of accepting the
misspelling in silence — which left you with a server on the default
setting and no way to tell that the thing you set had done nothing.

Some notes on what it does and does not cover:

- **It only looks at names starting with `SHARKO_`.** The chart also sets
  `CONNECTION_SECRET_NAME`, `ARGOCD_NAMESPACE`, `APP_ENVIRONMENTS`,
  `GITOPS_ACTIONS_ENABLED` and the `AI_*` family. Those are real settings
  and this check does not look at them.
- **The message names the setting and never the value.** That holds for
  every message Sharko writes about configuration.
- **`secrets.env` is checked too.** Putting the misspelling in that file
  instead of in the Pod does not get past it.
- **The variables Kubernetes writes are fine.** If you run your own
  manifests without `enableServiceLinks: false` (see
  [above](#service-links)), your Pod has `SHARKO_SERVICE_HOST`,
  `SHARKO_PORT_80_TCP` and friends in it. Sharko recognises those and
  starts normally. You should still turn service links off.
- **There is no way to switch it off.** If you need a name Sharko does
  not have, the answer is a change to Sharko, not a flag.

If you set a setting that only the end-to-end test rig reads (for example
`SHARKO_E2E_IMAGE_TAG`), Sharko starts and logs one line saying the name
is set and the server never reads it.

## Server settings you set as environment variables {#env-settings}

These are set with `extraEnv` (or however your own manifests set
environment variables). None of them has a Helm value of its own.

| Setting | Default | What it does |
|---------|---------|--------------|
| `SHARKO_CORS_ORIGIN` | unset — same-origin only | Which web origin may call the API from a browser. See [below](#cors-origin) before you set this |
| `SHARKO_API_TOKENS_FILE` | `~/.sharko/api-tokens.json` | Where API tokens are saved when Sharko is NOT running in Kubernetes. In Kubernetes this setting does nothing — tokens go into the `sharko-api-tokens` Secret instead. The file holds bcrypt hashes, never the tokens themselves, and is written `0600` |
| `SHARKO_REPO_PATH_MANAGED_CLUSTERS` | `configuration/managed-clusters.yaml` | Where the list of managed clusters lives inside your GitOps repository. Change it only if your repository puts that file somewhere else |
| `SHARKO_SETTINGS_RECONCILE_INTERVAL` | `60s` | How often Sharko puts server settings back to what Git says. Only runs in Kubernetes. A value that is not a duration is ignored with a warning and the default is used |
| `SHARKO_CONNECTION_CHECK_INTERVAL` | `60s` | How often Sharko checks its own two connections — Sharko to Git, and ArgoCD to the repository — and raises or clears the bell alert. A value that is not a positive duration is ignored with a warning and the default is used |
| `SHARKO_CONNECTION_CREDENTIAL_CHECK_INTERVAL` | `15m` | How often Sharko re-checks, in the background, whether a cluster's stored credentials still match what the connection says. It only looks; repairing stays a click. A value that is not a duration is ignored with a warning and the default is used |
| `SHARKO_SIGSTORE_TUF_CACHE` | `/tmp/sigstore-tuf` | Directory the Sigstore trust root is cached in, used when checking catalog signatures. The default is inside the container and is lost on restart, which costs one fetch. Point it at a mounted volume to keep it |

Durations are written the Go way: `30s`, `5m`, `1h30m`.

### `SHARKO_CORS_ORIGIN` — leave it unset {#cors-origin}

**Short version: leave it unset. If you must set it, set one exact origin.
Never set it to `*`.**

Unset, Sharko answers browser calls from its own address and nowhere else.
That is what you want almost always, including when the UI and the API are
the same Sharko you are already logged into.

Setting it to one origin, scheme included and no trailing slash, allows
that one:

```yaml
extraEnv:
  - name: SHARKO_CORS_ORIGIN
    value: "https://sharko.example.com"
```

Sharko does not check what you put here. A typo, a trailing slash or a
scheme that does not match will simply never match a real browser origin,
and the calls you were trying to allow will keep being refused with no
message explaining why. Copy the origin out of the browser's address bar.

**Why `*` is the dangerous one.** Sharko uses no cookies anywhere and
never sends the header that would let a browser attach your credentials to
a cross-site call, so `*` cannot hand out anything that already needed a
login. What it does open is everything that needs no login — and that
includes `POST /api/v1/auth/login`, which returns a working session token.

Put plainly: with `*` set on a Sharko your colleagues can reach, any web
page any of them opens can sit there trying passwords against your Sharko
from inside your network and read the answers. The only thing slowing it
down is the login limit of five tries per minute per address. `*` also
opens the built-in UI files and `/swagger/` to any page, because this check
runs before Sharko has worked out who is calling.

There is no case where `*` is the right answer on a real installation.

## Connection Config

| Value | Type | Default | Description |
|-------|------|---------|-------------|
| `config.connectionSecretName` | string | `"sharko-connections"` | Name of the Kubernetes Secret where connections are stored (encrypted) |
| `config.nodeAccess` | bool | `true` | Lets Sharko read Kubernetes Nodes (get/list) across the whole cluster it runs on. **On unless you turn it off.** It adds a rule to the cluster-wide role and feeds the node count on the Dashboard. Set it to `false` and the widget shows an empty state |
| `config.environments` | string | `""` | Comma-separated keywords extracted from cluster names to infer environment. Example: `"dev,qa,staging,prod"` — cluster `"my-app-prod-eks"` → env `"prod"` |

There is no `config.repoURL` value. The real key for the addons repo URL is
`connection.git.repoURL`, one of the non-secret connection fields documented
in [Git-Native Server Configuration](git-native-config.md) — set it and
Sharko keeps the connection's repo URL pinned to it (a runtime edit in the
UI gets reverted back). Leave it empty and the UI-set value is left alone.

### Dev mode (credential env var fallback)

There is no `config.devMode` chart value. The real switch is the `SHARKO_DEV_MODE` environment variable — set it to `"true"` via `extraEnv` to let Sharko fall back to `GITHUB_TOKEN`, `ARGOCD_TOKEN`, `AZURE_DEVOPS_PAT`, and `GITEA_TOKEN` env vars for credentials that are not configured in the Settings UI. Use only for local dev — not in production.

```yaml
extraEnv:
  - name: SHARKO_DEV_MODE
    value: "true"
  - name: GITHUB_TOKEN
    value: "ghp_xxxx"
```

## Authentication {#auth}

Authentication is managed via Kubernetes-native resources:

- **ConfigMap `sharko-users`** — user accounts (auto-created by Helm)
- **Secret** — bcrypt password hashes (auto-generated on first install)

On first install, an admin account is created with a random password. Retrieve it from the `sharko-initial-admin-secret` Secret (the same pattern ArgoCD uses):

```bash
kubectl get secret sharko-initial-admin-secret -n sharko \
  -o jsonpath='{.data.password}' | base64 -d
```

For local development outside Kubernetes, set these environment variables instead of using the K8s resources:

```bash
SHARKO_AUTH_USER=admin
SHARKO_AUTH_PASSWORD=mypassword
```

## Git Token

There is no Helm value for the GitHub PAT (or Azure DevOps / Gitea token) —
Sharko never takes it as a chart value at all. You enter it after install,
either in **Settings → Connection** in the UI or with `sharko connect
--git-token ...` on the CLI. Sharko stores it encrypted in the
`sharko-connections` Kubernetes Secret and never writes it into a Helm
values file, so it never shows up in `helm get values`. See
[Connections](../user-guide/connections.md#connection) for the full steps.

The only Helm-adjacent path around this is `SHARKO_DEV_MODE` (see
[Dev mode](#dev-mode-credential-env-var-fallback) above) — a local-dev-only
env var fallback, not something you'd use in production.

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

!!! warning "`ai.baseURL` and `ai.ollama.url` must carry no credential"
    Both are written into the pod as ordinary environment values, so anyone
    who can read the Deployment can read them. The chart refuses to install
    unless it can read the address and finds no user information in the host
    part, no query string and no fragment — the only three places a credential
    can sit in an address. It turns away most addresses it cannot read as
    well, but it looks at the shape of the address rather than at whether the
    host is a real machine, so a malformed IPv6 address in square brackets can
    still install. Nothing that carries a credential gets through either way.
    `connection.git.repoURL` and `connection.argocd.serverURL` go through the
    same chart check — see
    [Git-Native Server Configuration](git-native-config.md). The credential
    for a cloud provider is `ai.apiKey`, which goes into the chart's Secret.

### Ollama (self-hosted)

| Value | Type | Default | Description |
|-------|------|---------|-------------|
| `ai.ollama.deploy` | bool | `false` | Deploy an Ollama pod alongside Sharko |
| `ai.ollama.image` | string | `"ollama/ollama:0.6.8"` | Ollama container image (~1.2 GB compressed). Pinned, not `latest`, so two installs a month apart run the same thing |
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

Notification read-state (e.g. a dismissed "newer version available" alert) is stored under this
same `/app/data` volume. Without `persistence.enabled: true`, that state lives in an `emptyDir` and
resets on every pod restart, so cleared notifications come back as unread.

## Scheduling

| Value | Description |
|-------|-------------|
| `nodeSelector` | Node selector labels |
| `tolerations` | Pod tolerations |
| `affinity` | Affinity/anti-affinity rules |
| `hostAliases` | Host aliases for private DNS resolution |

## Provider Configuration (3-mechanism split — v1.25+) {#provider-3mech}

v1.25 split the previously-overloaded `providers.Config` struct into **three orthogonal provider concerns**, each with its own typed config block. Operators only need to configure the mechanisms they actually use; defaults are safe for the production happy path (Sharko-in-cluster + ArgoCD-installed).

For the cluster-connectivity story end-to-end, also read [Cluster Connectivity Model](cluster-connectivity-model.md) — it covers the auto-default behaviour, the AWS IAM / exec-plugin auth Sharko mints tokens for, and where each piece of connection truth lives.

### 1. Addon-secret backend — supplies addon secret material

The **addon-secrets** mechanism is the Sharko-native replacement for [External Secrets Operator](https://external-secrets.io). The reconciler fetches addon secret values (Datadog API keys, GitHub tokens, anything referenced by `secrets:` on a catalog entry) from a configured backend and pushes them into target clusters as `app.kubernetes.io/managed-by: sharko` K8s Secrets.

Supported backends:

| Backend | `type` value | Notes |
|---------|--------------|-------|
| AWS Secrets Manager | `aws-sm` / `aws-secrets-manager` | Region + Prefix supported |
| Kubernetes Secrets | `k8s-secrets` / `kubernetes` | Reads from the configured `namespace` (default `sharko`) |
| GCP Secret Manager | `gcp-sm` / `google-secret-manager` | Stub today (not yet implemented) |
| Azure Key Vault | `azure-kv` / `azure-key-vault` | Stub today (not yet implemented) |
| HashiCorp Vault | `vault` | Reserved for future work |

**How to configure** — addon-secret backends are configured via the **active connection** (Settings UI → Connections, or the connections API). The connection's `provider` block carries the backend type, region, prefix, namespace, and role ARN. Helm doesn't have a top-level `addonSecrets:` block — addon secrets are connection-scoped.

Reconciler tuning lives in Helm under `secrets.reconciler.*` (see [Secrets Reconciler](#secrets-reconciler) below) and the AWS-SM payload shape is documented under [AWS Secrets Manager — Secret Formats](#aws-secrets-manager-secret-formats).

### 2. Cluster connectivity (`clusterTest`)

The **cluster-test** mechanism resolves cluster connectivity credentials — the kubeconfig used by the Test connection button, the `POST /api/v1/clusters/{name}/test` endpoint, the dashboard "Verified" state, and the secrets reconciler's push channel.

**v1.25 supports one backend: `argocd`.** ArgoCDProvider reads cluster Secrets from the namespace where ArgoCD is installed. Auto-defaults to `argocd` when Sharko runs in-cluster, so the production happy path needs no explicit configuration.

```yaml
# charts/sharko/values.yaml
clusterTest:
  # K8s namespace where ArgoCD stores its cluster Secrets.
  # Leave empty to fall back to SHARKO_ARGOCD_NAMESPACE env var
  # (deprecated) or the "argocd" default.
  argocdNamespace: ""
```

Precedence (matches `resolveArgoCDNamespaceTyped` in `internal/providers/argocd_provider.go`):

1. `clusterTest.argocdNamespace` (Helm value, when non-empty) — **canonical**
2. `SHARKO_ARGOCD_NAMESPACE` env var — **DEPRECATED** in v1.25, emits a `slog.Warn` on use, removal slated for **v1.26**
3. Hardcoded `"argocd"` default — matches the standard ArgoCD install location

The chart also exposes `rbac.argocdNamespace` — a separate knob that controls the Role/RoleBinding the chart creates in ArgoCD's namespace so Sharko's in-cluster ServiceAccount can read ArgoCD Secrets. On standard installs both values point at the same `argocd` namespace.

!!! warning "Legacy cluster-credentials backends retired in v1.25"
    Before v1.25, operators could route cluster-connectivity credentials through `aws-sm`, `k8s-secrets`, `gcp-sm`, or `azure-kv` by setting `provider.type` on the connection. Those code paths were **retired in v1.25** as part of the three-mechanism split (one cycle earlier than originally promised in `provider.go:55`). Migrate to `argocd` — auto-defaulted when Sharko runs in-cluster.

    The same backend names (`aws-sm`, `k8s-secrets`, `gcp-sm`, `azure-kv`) **remain fully supported as addon-secret backends** — only their cluster-credentials usage was killed. The ESO-replacement layer is unaffected.

### 3. Cluster registration source (`clusterRegSource`)

The **cluster-registration-source** mechanism pre-wires the configuration knob for the future V125-1-8 reconciler — the component that will write ArgoCD cluster Secrets into the configured namespace based on `managed-clusters.yaml` content.

**No consumer in v1.25** — the block is parsed and validated at startup but the reconciler that consumes it ships in a later sprint. Until then, operators register clusters via the ArgoCD UI or `kubectl apply` directly, as today.

```yaml
# charts/sharko/values.yaml
clusterRegSource:
  type: ""              # "" → no reconciler (today); "argocd" → V125-1-8 writes
  argocdNamespace: ""   # "" → defaults to "argocd" when V125-1-8 ships
```

Corresponding env vars (surfaced in startup logs so operators can verify the values are propagating):

| Env var | Helm value | Default |
|---------|------------|---------|
| `SHARKO_CLUSTER_REG_TYPE` | `clusterRegSource.type` | `""` (no reconciler) |
| `SHARKO_CLUSTER_REG_ARGOCD_NAMESPACE` | `clusterRegSource.argocdNamespace` | `""` (will default to `argocd` once V125-1-8 ships) |

### Migration from pre-v1.25 configuration

Most operators see **zero impact** in v1.25:

- **If you only use the ESO-replacement** (`vault` / `aws-sm` / `azure-kv` / `gcp-sm` / `k8s-secrets` supplying addon secret material) — no changes needed. The addon-secrets layer is unchanged.
- **If your connection uses `provider.type: argocd`** (the default for new installs since V125-1-10.2) — no changes needed; the new typed config inherits the same value.
- **If you set `SHARKO_ARGOCD_NAMESPACE`** — it still works but emits a deprecation warning. Migrate to `clusterTest.argocdNamespace` in Helm values. Removed in v1.26.
- **If your connection had `provider.type: aws-sm` / `k8s-secrets` / `gcp-sm` / `azure-kv` to fetch cluster kubeconfigs** (the cluster-credentials usage — NOT the addon-secrets usage) — migrate to `provider.type: argocd`. Sharko reads kubeconfigs from the ArgoCD cluster Secret it (or you) already wrote.

For developers building on Sharko's Go API, the canonical types now live in `internal/providers/config_types.go`:

- `AddonSecretProviderConfig` — backends for addon secret material
- `ClusterTestProviderConfig` — argocd-only cluster connectivity backend
- `ClusterRegistrationSourceConfig` — pre-wire for the V125-1-8 reconciler

The pre-v1.25 `providers.Config` struct and the `providers.New` / `providers.NewSecretProvider` factories were retired in V125-1-11.6 — call `NewAddonSecretProvider` / `NewClusterTestProvider` directly with the typed configs instead.

## AWS Secrets Manager — Secret Formats {#aws-secrets-manager-secret-formats}

When using the `aws-sm` provider (configured via Settings UI or API), each cluster secret in AWS SM can be stored in one of two formats. Sharko auto-detects which format is used.

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

The secret value is a JSON object describing an EKS cluster. Sharko detects this format by one signal: the value parses as JSON **and** has a non-empty `host` key. It then mints a short-lived STS token for the cluster instead of reading a stored credential:

```json
{
  "clusterName": "prod-eu",
  "host": "https://abc123.gr7.us-east-1.eks.amazonaws.com",
  "caData": "<base64-encoded-ca-data>",
  "region": "us-east-1",
  "roleArn": "arn:aws:iam::111122223333:role/EKSReadRole"
}
```

Field reference (names must match exactly — they are the Go struct tags in `internal/providers/aws_sm.go`):

| Field | Required | Meaning |
|-------|----------|---------|
| `host` | yes | Cluster API server URL. Also the format-detection signal. |
| `caData` | yes | Base64-encoded cluster CA certificate. |
| `clusterName` | yes | EKS cluster name, sent as the `x-k8s-aws-id` token header. |
| `region` | recommended | AWS region used for the STS call. |
| `roleArn` | optional | IAM role to assume when minting the token for this cluster. |

`roleArn` is the most specific of three places a role can come from. Precedence at token-mint time:

1. The secret's own `roleArn` (above) — stored with the credential material itself.
2. The per-cluster `role_arn` recorded on the cluster's `managed-clusters.yaml` entry at registration (`roleArn`) — this is how a discovery-registered cross-account cluster keeps minting with the identity that discovered it.
3. The connection-level provider default (`role_arn` on the provider config).

When all three are empty, no role is assumed and the pod's own identity (IRSA) signs the token.

Sharko generates a `k8s-aws-v1.*` bearer token on each credential fetch — the presigned STS URL expires after 60 seconds (matching ArgoCD's `argocd-k8s-auth`), and tokens are never stored.

For a **static bearer token** instead of STS minting, store a full kubeconfig (Format 1) with the token inside it — a bare `{"server": ..., "token": ...}` JSON object is not a supported shape.

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
| `secrets.webhookSecret` | string | `""` | Shared secret the Git webhook is signed with. Empty — the default — means `POST /api/v1/webhooks/git` refuses every call. Maps to `SHARKO_WEBHOOK_SECRET` |

There is no chart value that switches addon-secret syncing on or off, and none
that sets its timer. It starts by itself as soon as a credentials backend and
an addon-secret backend are both configured, and the timer comes from the
`SHARKO_SECRET_RECONCILE_INTERVAL` environment variable (`5m` unless you set
it), which you pass through `extraEnv`. If you have `secrets.reconciler.enabled`
or `secrets.reconciler.interval` in your values file, delete them — the chart
has never read either one, so they have never done anything.

Addon-secret syncing uses the **same secrets provider** configured for cluster credentials (via Settings UI or API). No separate provider configuration is needed.

Sharko checks the `X-Hub-Signature-256` header on every call to
`POST /api/v1/webhooks/git`. A call without a signature that matches
`secrets.webhookSecret` is refused with a 401.

**Leaving `secrets.webhookSecret` empty does not make the endpoint open — it
makes it closed.** Empty is the default, and while it is empty Sharko refuses
every call to the webhook, because nothing could match. That endpoint is the
one route that takes no login and no API token, so the signature is the only
thing in front of it, and there is no setting that turns the check off and
leaves the endpoint answering.

Nothing else stops working meanwhile. Sharko picks up changes on its own
either way; the webhook only makes it notice sooner. To switch the webhook on,
set the same value in two places — this chart value, and your Git provider's
webhook settings.

```yaml
# Example: webhook verification, and a different sync timer
secrets:
  webhookSecret: "your-hmac-secret"
extraEnv:
  - name: SHARKO_SECRET_RECONCILE_INTERVAL
    value: "10m"
```

The webhook secret can also come from a Secret you already have:

```yaml
extraEnv:
  - name: SHARKO_WEBHOOK_SECRET
    valueFrom:
      secretKeyRef:
        name: my-webhook-secret
        key: secret
```

## ArgoCD Resource Exclusions {#argocd-resource-exclusions}

Sharko pushes K8s Secrets labeled `app.kubernetes.io/managed-by: sharko` to remote clusters during the addon secrets reconciliation cycle. Without an exclusion rule, ArgoCD may attempt to manage — or delete — these secrets when it syncs cluster state.

### Required configuration

Add the following block to your `argocd-cm` ConfigMap in the `argocd` namespace:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-cm
  namespace: argocd
data:
  resource.exclusions: |
    - apiGroups: [""]
      kinds: ["Secret"]
      clusters: ["*"]
      labelSelector:
        matchLabels:
          app.kubernetes.io/managed-by: sharko
```

Apply with:

```bash
kubectl apply -f argocd-cm-patch.yaml
```

Or patch in place:

```bash
kubectl patch configmap argocd-cm -n argocd --type merge -p '
{
  "data": {
    "resource.exclusions": "- apiGroups: [\"\"]\n  kinds: [\"Secret\"]\n  clusters: [\"*\"]\n  labelSelector:\n    matchLabels:\n      app.kubernetes.io/managed-by: sharko\n"
  }
}'
```

!!! warning "ArgoCD restart required"
    After patching `argocd-cm`, restart the ArgoCD application controller for the change to take effect:
    ```bash
    kubectl rollout restart deployment argocd-application-controller -n argocd
    ```

### Checking exclusion status

Use the Sharko API to verify the exclusion is configured:

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  https://<sharko-host>/api/v1/argocd/resource-exclusions | jq .
```

Example response when configured:

```json
{
  "configured": true,
  "detail": "resource.exclusions is configured with a sharko Secret exclusion"
}
```

Example response when not configured:

```json
{
  "configured": false,
  "detail": "argocd-cm has no resource.exclusions configured",
  "recommendation": "Add the following to your argocd-cm ConfigMap ..."
}
```

## End-to-End Testing Setup

Sharko includes an E2E test framework that tests against a real ArgoCD instance in a Kind cluster. The E2E suite is not run in the standard `make test` target — it requires a running Kind cluster with ArgoCD.

### Prerequisites

```bash
# Install Kind
brew install kind   # macOS
# or: go install sigs.k8s.io/kind@latest

# Install ArgoCD CLI
brew install argocd
```

### Spin up the E2E environment

```bash
make e2e-setup
# Creates a Kind cluster named "sharko-e2e"
# Installs ArgoCD into the argocd namespace
# Waits for ArgoCD to become ready
# Exports ARGOCD_SERVER and ARGOCD_TOKEN env vars
```

### Run E2E tests

```bash
make e2e
# Runs the test suite in e2e/ against the Kind cluster
# Tests: cluster registration, addon deployment, init flow, secrets reconciliation
```

### Tear down

```bash
make e2e-teardown
# Deletes the Kind cluster
```

### E2E environment variables

| Var | Description |
|-----|-------------|
| `E2E_ARGOCD_SERVER` | ArgoCD server URL (set by `make e2e-setup`) |
| `E2E_ARGOCD_TOKEN` | ArgoCD bearer token (set by `make e2e-setup`) |
| `E2E_SHARKO_SERVER` | Sharko server URL (defaults to `http://localhost:8080`) |
| `E2E_GIT_REPO` | Git repo URL for the addons repo (optional — tests use a local mock repo by default) |

!!! note
    E2E tests are not required to pass before merging feature branches. They are run manually and in a separate CI job (`e2e.yml`) that is not required for PR approval.

## Extra Environment Variables

Inject arbitrary environment variables into the Sharko pod:

```yaml
extraEnv:
  - name: SHARKO_LOG_LEVEL
    value: "debug"
  - name: SHARKO_CONNECTION_CHECK_INTERVAL
    value: "5m"
```

Names starting with `SHARKO_` have to be settings Sharko really reads — see
[A setting Sharko does not recognise stops it starting](#unknown-settings).
This example used to name `SHARKO_GITOPS_PR_AUTO_MERGE` and
`SHARKO_GITOPS_BASE_BRANCH`, neither of which Sharko has ever read. The
ones it does read are `SHARKO_CONN_GITOPS_PR_AUTO_MERGE` and
`SHARKO_CONN_GITOPS_BASE_BRANCH`, and both are better set through
`connection.gitops.*` in Helm values — see
[Git-Native Server Configuration](git-native-config.md).

Full list of supported env vars is in the [README](https://github.com/MoranWeissman/sharko#configuration).

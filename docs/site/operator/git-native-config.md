# Git-Native Server Configuration

**Git wins.** Sharko v3.0.0 makes server settings and connection configuration fully declarable in Helm `values.yaml`. When a setting is declared in Helm/git, that value is authoritative — Sharko reconciles its runtime state toward the git-declared value at boot and on a periodic reclaim, so a runtime UI edit to a declared field is reverted to the git value within 60 seconds.

This is the same self-healing principle that manages cluster labels: operators expose everything via Helm values so they never have to touch the UI/API; git is the source of truth, and ArgoCD syncing the Sharko release keeps runtime state aligned.

## How It Works

When you set a value in `charts/sharko/values.yaml`, the chart renders it as an **environment variable** on the Sharko Deployment. Environment variables are immutable for a pod's life and ArgoCD owns the Deployment, so git is authoritative by construction. Sharko reads the env vars and:

1. **Boot reconcile** — on startup, Sharko overwrites its runtime ConfigMap/Secret with the env-declared values (for the fields that are env-declared).
2. **Periodic reclaim (60s tick)** — a background goroutine re-runs the reconcile every 60 seconds. If a user edits a git-declared field via the UI/API between ticks, Sharko reverts it to the git value on the next tick.

**Undeclared keys** (left empty/unset in Helm values) keep their runtime ConfigMap/Secret value — the API is authoritative for those, exactly like today's pre-v3 behavior. This is full back-compat: if you never touch the Helm values, the UI/API works exactly as before.

## Git-Declarable Settings (v3.0.0)

### Scalar Server Settings

Two server-wide toggles are now Helm-declarable:

| Helm value | Type | Default | What it controls |
|------------|------|---------|------------------|
| `settings.probeMode` | string | `""` (undeclared) | Connectivity probe mode: `"check-app"` (default, auto-deploy a transient connectivity-check Application to new zero-addon clusters) or `"api-test"` (no app ever auto-deployed, reachability from ArgoCD connection state only). Leave empty to keep the runtime API value authoritative. |
| `settings.allowInlineCredentials` | string | `""` (undeclared) | Whether the "Paste a kubeconfig" registration path is enabled server-wide: `"true"` (default) or `"false"`. Set to `"false"` to forbid inline credential paste install-wide; connection-only registrations unaffected. Leave empty to keep the runtime API value authoritative. |

When `settings.probeMode` is set to `"check-app"` or `"api-test"` in Helm values, that becomes the authoritative mode; a runtime `PUT /settings/probe-mode` edit is accepted but reverted to the git value within 60 seconds. The `GET /settings/probe-mode` response includes `managed_by_git: true` so the UI can show "git-managed; your edit will revert."

**Example:**

```yaml
# charts/sharko/values.yaml
settings:
  probeMode: "check-app"                # git wins; UI edit will revert
  allowInlineCredentials: "false"       # git wins; forbid inline paste install-wide
```

### Connection Configuration (Non-Secret Fields)

The **non-secret fields** of the active connection are Helm-declarable with git-wins. Sharko's connection is one encrypted JSON blob in the `sharko-connections` Secret, so "git wins on non-secret fields" is a **field-level merge**: the declared non-secret fields are overwritten from env while the encrypted secret material (git token/PAT, ArgoCD token) is **preserved untouched**, then re-encrypted on save.

**Git-declarable non-secret connection fields:**

```yaml
# charts/sharko/values.yaml
connection:
  git:
    provider: "github"           # "github" | "azuredevops"
    repoURL: ""                  # full repo URL (parsed into owner/repo or org/project/repo)
    owner: "MyOrg"               # GitHub owner
    repo: "argocd-config"        # GitHub repo
    organization: ""             # Azure DevOps organization
    project: ""                  # Azure DevOps project
    repository: ""               # Azure DevOps repository
  argocd:
    serverURL: "https://argocd.example.com"  # ArgoCD API server URL
    namespace: "argocd"          # namespace where ArgoCD is installed
    insecure: "false"            # "true" | "false" — skip TLS verify
  # Cluster-test provider (non-secret fields)
  provider:
    type: "argocd"               # "aws-sm" | "k8s-secrets" | "argocd"
    region: "us-east-1"          # AWS region (aws-sm only)
    prefix: "clusters/"          # secret name prefix
    namespace: "sharko"          # K8s namespace (k8s-secrets only)
    roleArn: "arn:aws:iam::000000000000:role/SharkoRole"  # IAM role ARN (identifier, not a credential)
  # Separate addon-secret provider (non-secret fields)
  addonSecretProvider:
    type: "aws-sm"
    region: "us-west-2"
    prefix: "addons/"
    namespace: ""
    roleArn: ""
  gitops:
    baseBranch: "main"           # default: "main"
    branchPrefix: "sharko/"      # default: "sharko/"
    commitPrefix: "sharko:"      # default: "sharko:"
    prAutoMerge: "true"          # "true" | "false"
    hostClusterName: "management"  # cluster running ArgoCD (in-cluster)
    defaultAddons: "cert-manager,metrics-server"  # comma-separated addon names
```

**Secret material is NEVER declared in values.yaml** — the git token/PAT and ArgoCD token stay in the encrypted `sharko-connections` Secret and are entered once via the Settings UI. This block only carries identifiers and settings that are safe to keep in git.

**Scope:** the **active (or default) connection only**. When no connection exists yet (fresh install, token not entered), this block is a no-op — Sharko never fabricates a connection without credentials.

### AI Provider Configuration (Non-Secret Fields)

The **non-secret fields** of the AI provider config are Helm-declarable with git-wins. The encrypted API key stays in the chart-managed Secret (entered via the Settings UI or `ai.apiKey` in Helm values for the secret path only).

**Git-declarable non-secret AI fields:**

```yaml
# charts/sharko/values.yaml
ai:
  enabled: true
  provider: "claude"             # "ollama" | "claude" | "openai" | "gemini" | "custom-openai"
  cloudModel: "claude-sonnet-4-20250514"  # model name
  baseURL: ""                    # base URL for custom-openai (enterprise LLM gateways)
  authHeader: "Authorization"    # custom auth header name for custom-openai
  maxIterations: 8               # tool-calling loop limit
  # Ollama settings
  ollama:
    deploy: false
    url: ""
    model: "llama3.2"
    agentModel: ""
```

**AI_API_KEY is the secret path** — it's never merged from git-declared env vars. The API key stays encrypted in the chart Secret (if `ai.apiKey` is set) or the Settings UI storage.

**Honest scope note:** AI config reconciles **at boot only** (not on the 60s periodic tick). This is a deliberate limit — periodic AI reclaim is deferred to a future version. A runtime UI edit to an AI non-secret field (provider, model) persists until the next pod restart when `ai.*` values are env-declared.

## How Git-Wins Precedence Works

For each setting/field:

1. **If the Helm value is set (non-empty):**
   - The env var is emitted on the Deployment.
   - Sharko treats it as **authoritative**.
   - A runtime API/UI edit is accepted but **reclaimed** on the next reconcile (boot + 60s tick for settings/connection; boot only for AI).
   - The `GET` response includes `managed_by_git: true` (settings endpoints) or the connection/AI read paths show the git-declared value.

2. **If the Helm value is left empty/unset:**
   - The env var is **not emitted** (tri-state guard in the chart: empty string → no env var).
   - The key is **undeclared**.
   - The runtime ConfigMap/Secret value **persists** — the API is authoritative, exactly like today's pre-v3 behavior (full back-compat).

3. **If a Helm value is malformed** (e.g., `probeMode: "typo"` or `maxIterations: "not-an-int"`):
   - Sharko logs a `slog.Warn` and treats the key as **undeclared** (lenient fallback).
   - Boot never crashes on a settings typo.

4. **If a provider block declares any field (region, prefix, namespace, roleArn) but omits `type`:**
   - Sharko logs a `slog.Warn` listing which fields were declared and **skips that provider block entirely** (lenient fallback).
   - The provider stays undeclared — Sharko never persists a typeless provider.
   - `type` is required whenever any provider field is declared in Helm values.

## What Stays API/Secret-Only

**Secret material is NEVER declared in Helm `values.yaml`** and is NEVER rendered as plaintext into a ConfigMap. The following stay in encrypted Secrets and are entered once via the Settings UI:

- **Git connection:** `Token` (GitHub PAT / Azure DevOps PAT)
- **ArgoCD connection:** `Token`
- **AI provider:** `apiKey` (the actual secret value — `ai.apiKey` in Helm values is the *secret reference path*, not the plaintext)

The field-level merge for connection config preserves these encrypted fields untouched — only the non-secret fields (repo URL, ArgoCD server URL, provider type, etc.) are overwritten from git-declared env values.

## Honest Limitations (v3.0.0)

1. **AI config reclaims at boot only** — not on the 60s periodic tick. A runtime UI edit to an AI non-secret field persists until the next pod restart.
2. **Active connection only** — Sharko holds a list of connections internally; "Helm declares the connection" only coheres for the one active/default connection. Non-active connections are still API-managed.
3. **Removed key freezes** — if you set a Helm value (e.g., `settings.probeMode: "api-test"`), then remove it from values.yaml later, the env var stops being emitted → the key becomes undeclared → the last reconciled value **freezes** and the API becomes authoritative again. It does **NOT** reset to the built-in default. To reset, either set it back to the default explicitly in Helm or edit it via the API.

## Updating Stale Pre-v3 Documentation

Prior to v3.0.0, operator docs described `probe_mode` and `allow_inline_credentials` as "runtime toggles … stored in Sharko's in-cluster settings store, admin-only, editable from Settings in the UI (or their `/api/v1/settings/*` endpoints) with no restart or redeploy needed."

That description is now **partially stale**: in v3.0.0, these settings can ALSO be Helm-declared with git-wins. The full precedence is:

1. **Helm-declared value** (env var emitted) → **git authoritative** (reclaimed on the 60s tick).
2. **Undeclared (env var not emitted)** → **API authoritative** (runtime ConfigMap value persists, no reclaim).

The UI/API endpoints still work (backward-compatible), but when a key is git-declared, a runtime edit is transient — it reverts to the git value within 60 seconds.

## Example: Production Hardening with Git-Wins

Operators who want GitOps-clean server config can now declare everything in Helm values:

```yaml
# charts/sharko/values.yaml (production hardening)
settings:
  probeMode: "check-app"
  allowInlineCredentials: "false"  # forbid inline kubeconfig paste

connection:
  git:
    provider: "github"
    owner: "MyOrg"
    repo: "k8s-config"
  argocd:
    serverURL: "https://argocd.prod.example.com"
    namespace: "argocd"
    insecure: "false"
  provider:
    type: "argocd"
    region: ""
    prefix: ""
    namespace: ""
    roleArn: ""
  addonSecretProvider:
    type: "aws-sm"
    region: "us-west-2"
    prefix: "prod/addons/"
  gitops:
    baseBranch: "main"
    branchPrefix: "sharko/"
    prAutoMerge: "true"
    hostClusterName: "prod-management"
    defaultAddons: "cert-manager,metrics-server,karpenter"

ai:
  enabled: true
  provider: "claude"
  cloudModel: "claude-sonnet-4-20250514"
  maxIterations: 12
```

With the above, git is authoritative for all non-secret config. A UI edit to any of these fields is reverted to the git value within 60 seconds. Secret material (git token, ArgoCD token, AI API key) is still entered once via the Settings UI and stored encrypted — never in the values file.

## Migration from Pre-v3

**No breaking changes.** If you never set the git-native Helm values, Sharko behaves exactly as before:

- `settings.probeMode` and `settings.allowInlineCredentials` left empty → runtime API authoritative (today's behavior).
- `connection.*` fields left empty → active connection is API-managed (today's behavior).
- `ai.*` non-secret fields left empty → AI config is API-managed (today's behavior).

To adopt git-wins incrementally:

1. Set the Helm values you want git to own (e.g., `settings.probeMode: "api-test"`).
2. Upgrade the Sharko release (`helm upgrade`).
3. ArgoCD syncs the Deployment with the new env vars.
4. Sharko's boot reconcile + 60s tick reclaims the settings/connection toward the git-declared values.
5. Verify via the UI — git-managed keys show `managed_by_git: true` in the `GET` response.

## Observability

- **Logs:** Sharko logs `slog.Info` on boot reconcile (which keys were reconciled, which stayed undeclared) and `slog.Warn` on malformed declared values (lenient fallback).
- **API responses:** `GET /settings/probe-mode` and `GET /settings/allow-inline-credentials` include `managed_by_git: true` when the key is git-declared.
- **Connection/AI:** The `GET /connections` and `GET /ai/config` endpoints show the effective (merged) values — git-declared non-secret fields overlay the runtime state, secrets preserved.

## See Also

- [Operator Configuration Reference](configuration.md) — full Helm values catalog
- [Cluster Reconciler](cluster-reconciler.md) — similar git-wins self-heal for cluster labels
- [Marketplace Sources Config](marketplace-sources-config.md) — another git-native precedent (file + env fallback)

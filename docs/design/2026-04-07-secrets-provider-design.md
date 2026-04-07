# Sharko Secrets Provider — Design Spec

> Replaces ESO dependency. Sharko becomes the single control plane for addon secrets.
> Decided during brainstorming 2026-04-07.

---

## Overview

Sharko pushes addon secrets (API keys, credentials) directly to remote Kubernetes clusters. No ESO, no CRDs, no operators on remote clusters. Secret definitions live in `addons-catalog.yaml` alongside addon entries. A background reconciler ensures secrets stay fresh.

---

## Data Model

### AddonSecretRef (new struct in `internal/models/addon.go`)

```go
type AddonSecretRef struct {
    SecretName string            `json:"secretName" yaml:"secretName"`
    Namespace  string            `json:"namespace" yaml:"namespace"`
    Keys       map[string]string `json:"keys" yaml:"keys"` // K8s data key → provider path
}
```

### AddonCatalogEntry — new `Secrets` field

```go
type AddonCatalogEntry struct {
    // ... existing fields
    Secrets []AddonSecretRef `json:"secrets,omitempty" yaml:"secrets,omitempty"`
}
```

### Catalog YAML example

```yaml
applicationsets:
  - name: datadog
    repoURL: https://helm.datadoghq.com
    chart: datadog
    version: "3.82.6"
    namespace: datadog
    secrets:
      - secretName: datadog-keys
        namespace: datadog
        keys:
          api-key: secrets/datadog/api-key
          app-key: secrets/datadog/app-key
```

Provider paths (e.g., `secrets/datadog/api-key`) are references, not values. Same pattern as ESO's `remoteRef.key`. Safe to store in Git.

---

## SecretProvider Interface

New interface in `internal/providers/secret_provider.go`:

```go
type SecretProvider interface {
    GetSecretValue(ctx context.Context, path string) ([]byte, error)
}
```

Replaces the existing `SecretValueFetcher` in `orchestrator/secrets.go`. Moved to `providers` package where it belongs.

### Built-in Backends

**k8s-secrets:** Reads from K8s Secrets in a configured namespace.
- Path format: `<secret-name>/<key>`
- Example: `datadog-keys/api-key` → reads key `api-key` from Secret `datadog-keys`

**aws-sm:** Reads from AWS Secrets Manager.
- Path format: `secrets/datadog/api-key` → fetches that SM secret
- Uses the existing AWS SDK setup in `aws_sm.go`

Factory: `NewSecretProvider(cfg Config) (SecretProvider, error)` — same Config struct, same type switch.

---

## Reconciler

New package: `internal/secrets/`

### reconciler.go

```go
type Reconciler struct {
    credProvider   providers.ClusterCredentialsProvider
    secretProvider providers.SecretProvider
    gitProvider    func() gitprovider.GitProvider  // lazy — reads from active connection
    remoteClientFn orchestrator.RemoteClientFactory
    interval       time.Duration  // default 5 min
    triggerCh      chan struct{}   // manual/webhook trigger
    stopCh         chan struct{}
    stopOnce       sync.Once
}
```

### Reconcile Cycle

Each cycle (whether triggered by timer, webhook, or manual):

1. **Read catalog** from Git → extract addons with `secrets:` definitions
2. **Read cluster-addons.yaml** from Git → get clusters and enabled addons
3. **For each cluster + enabled addon with secrets:**
   a. `credProvider.GetCredentials(clusterName)` → kubeconfig
   b. Connect to remote cluster via `remoteClientFn(kubeconfig)`
   c. For each secret key in the definition:
      - `secretProvider.GetSecretValue(providerPath)` → compute SHA-256 hash
      - Read deployed K8s Secret from remote cluster → compute SHA-256 hash of corresponding key
      - **Match** → `INFO secret up-to-date addon=%s cluster=%s secret=%s` → skip
      - **Differ** → `WARN secret rotated, updating addon=%s cluster=%s secret=%s` → push → `INFO secret updated`
      - **Missing** → `INFO creating secret addon=%s cluster=%s secret=%s` → create → `INFO secret created`
4. **Cleanup orphans:** List Sharko-managed secrets (by `app.kubernetes.io/managed-by: sharko` label) on the cluster. Delete any that no longer match a catalog definition for an enabled addon.
   - Only append to deleted list on successful delete (fix ghost deletion bug)
   - `WARN orphan secret detected cluster=%s secret=%s/%s`
   - `INFO orphan secret deleted cluster=%s secret=%s/%s`
5. **Log summary:** `INFO reconcile complete checked=%d created=%d updated=%d deleted=%d errors=%d duration=%s`

### Error Handling

- One failed cluster doesn't stop the cycle
- Errors logged per-cluster with full context
- Summary includes error count
- Provider fetch errors are retried once with backoff (1s)

### Three Triggers

1. **Timer** — goroutine with `time.NewTicker(interval)`. Default 5 min, configurable via `SHARKO_SECRET_RECONCILE_INTERVAL`.
2. **Manual** — `POST /api/v1/secrets/reconcile` API endpoint OR `sharko refresh-secrets [cluster]` CLI. Sends signal to `triggerCh`.
3. **Webhook** — `handleGitWebhook` (already exists in `webhooks.go`), on push to base branch that modifies catalog files, sends signal to `triggerCh`.

### Start/Stop

```go
func (r *Reconciler) Start()   // launches goroutine
func (r *Reconciler) Stop()    // sync.Once close stopCh
func (r *Reconciler) Trigger() // send to triggerCh (non-blocking)
```

Started in `cmd/sharko/serve.go` after the server is initialized. Stopped on graceful shutdown.

---

## API Endpoints

### Secrets Reconciliation

```
POST /api/v1/secrets/reconcile              → trigger manual reconcile (all clusters)
POST /api/v1/secrets/reconcile/{cluster}    → trigger reconcile for one cluster
GET  /api/v1/secrets/status                 → last reconcile time, stats, errors
```

### Addon Secret Definitions (catalog-driven, read-only from API)

The existing `/api/v1/addon-secrets` endpoints become read-only views into the catalog. Secret definitions are edited via the catalog (Git, UI addon config editor, CLI).

---

## CLI

```bash
sharko refresh-secrets              # trigger reconcile for all clusters
sharko refresh-secrets prod-eu      # trigger reconcile for one cluster
sharko secret-status                # show last reconcile stats
```

---

## Integration Points

### Webhook → Reconciler

In `internal/api/webhooks.go`, when a push to the base branch is detected:
```go
if s.secretReconciler != nil {
    s.secretReconciler.Trigger()
}
```

### add-cluster → Secrets

Existing flow in `orchestrator/secrets.go` (`createAddonSecrets`) stays but reads definitions from the catalog instead of server-side ConfigMap. The `AddonSecretDefinition` struct is replaced by `AddonSecretRef` from the catalog.

### Orchestrator cleanup

- Remove `AddonSecretDefinition` struct (replaced by `models.AddonSecretRef`)
- Remove `SecretValueFetcher` interface (replaced by `providers.SecretProvider`)
- Remove `SetSecretManagement` method (reconciler handles this now)
- Remove server-side addon secret store (`config/addon_secrets_store.go`)
- Remove `SHARKO_ADDON_SECRETS` env var and Helm values for addon secrets

### Ghost deletion fix

In `orchestrator/secrets.go`, `deleteAddonSecrets` and `deleteAllAddonSecrets`:
- Only append to `deleted` slice when `Delete` returns nil (no error at all)
- `NotFound` → log as already-gone, don't append to deleted

---

## Existing Code Reuse

| Component | Exists | Changes |
|-----------|--------|---------|
| `remoteclient.EnsureSecret` | Yes | No change |
| `remoteclient.DeleteSecret` | Yes | No change |
| `providers.ClusterCredentialsProvider` | Yes | Add `SecretProvider` alongside |
| `providers.KubernetesSecretProvider` | Yes | Add `GetSecretValue` method |
| `providers.AWSSecretsManagerProvider` | Yes | Add `GetSecretValue` method |
| `orchestrator.createAddonSecrets` | Yes | Read from catalog instead of server defs |
| `orchestrator.deleteAddonSecrets` | Yes | Fix ghost deletion |
| `orchestrator.RemoteClientFactory` | Yes | Reuse as-is |
| `webhooks.go` | Yes | Add reconciler trigger |
| `config/addon_secrets_store.go` | Yes | Delete (replaced by catalog) |

---

## Configuration

```yaml
# Helm values (new)
secrets:
  reconciler:
    enabled: true
    interval: "5m"     # reconcile interval

# Env vars
SHARKO_SECRET_RECONCILE_INTERVAL=5m
SHARKO_SECRET_RECONCILE_ENABLED=true
```

---

## Testing

- Unit: reconciler cycle with mock providers (create, update, skip, cleanup)
- Unit: hash comparison logic
- Unit: ghost deletion fix
- Unit: SecretProvider implementations (k8s-secrets, aws-sm)
- Integration: full cycle with fake K8s client + mock secret provider

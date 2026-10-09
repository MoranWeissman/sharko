# Sharko — V1 Design Decisions

> Decisions made during QA testing and design brainstorming (2026-04-07).
> These decisions shape the next development phases.

---

## Decision 1: Credential Flow

### Problem
Sharko currently has three disconnected credential paths: Helm secrets (env vars), connection store (encrypted K8s Secret via UI), and CLI config (local file). Users pass tokens via `--set secrets.GITHUB_TOKEN` during Helm install, then the wizard asks for the same token again.

### Decision
**One connection store, three entry points.** Credentials enter through the product, not Helm.

- **UI** — wizard or Settings page
- **CLI** — `sharko connect` command
- **API** — `POST /api/v1/connections`

### Changes Required
- Remove `secrets` block from Helm `values.yaml` (GITHUB_TOKEN, ARGOCD_TOKEN)
- Remove env var credential fallback from `cmd/sharko/serve.go`
- Remove `config.devMode` flag for credential bypass
- Single connection only (no connection list/add/remove until multi-ArgoCD support)
- Settings page shows edit-only connection form, not a list

### Auth Model
- Server always starts, auth always required on all endpoints
- Public endpoints: `/health`, `/version` only
- Unauthenticated requests get 401 regardless of connection state
- No connection configured = limited functionality, not broken UI

### Out of Scope
- Multi-ArgoCD support (V2)
- Terraform provider (future, separate project)

---

## Decision 2: Init Flow

### Problem
`sharko init` creates a PR, but when manual approval is needed, there's no mechanism to wait for the merge and continue with ArgoCD bootstrap.

### Decision
**Three channels, each handles the wait differently.**

#### UI Flow
1. User clicks "Initialize" in wizard Step 4 or Settings
2. Sharko creates PR
3. UI shows choice: "Auto-merge" or "Wait for manual merge"
4. If waiting: UI shows "Waiting for PR merge..." with PR link, polls status
5. Once merged: automatically continues with ArgoCD bootstrap (create project, root app, wait for sync)

#### CLI Flow
1. `sharko init` creates PR
2. CLI prompts: "Auto-merge this PR? [Y/n]"
3. If no: CLI shows live status, polls like `argocd app sync --watch`
4. Once merged: continues with ArgoCD bootstrap

#### API Flow
1. `POST /api/v1/init` with `auto_merge: true/false`
2. Returns immediately with PR URL + operation/session ID
3. Caller polls `GET /api/v1/operations/{id}/status` for progress
4. Server polls PR status in background

### Session Model
- **Heartbeat-based** — session alive = heartbeats arriving (UI every 30s, CLI on each poll)
- **No arbitrary timeout** — active watchers never time out
- **Heartbeats stop = session dies** — no separate timeout clock
- **In-memory storage** — no ConfigMap, no PVC
- **Resumable** — if session dies, `sharko init` detects existing PR and picks up where it left off
- **ArgoCD must be connected before bootstrap** — wizard enforces this (Step 3 before Step 4)

### Operations Endpoint (new)
```
POST   /api/v1/operations          → start an operation (init, etc.), returns session ID
GET    /api/v1/operations/{id}     → get operation status + progress
DELETE /api/v1/operations/{id}     → cancel/abandon operation
POST   /api/v1/operations/{id}/heartbeat → keep session alive
```

---

## Decision 3: Secrets Provider

### Problem
The old architecture required ESO (External Secrets Operator) on every cluster to pull secrets from AWS SM. This is a heavy dependency — ESO operator + CRDs + IAM roles on each cluster. Sharko should provide a complete solution that replaces ESO for addon secret management.

### Decision
**Sharko is the single control plane that pushes secrets to remote clusters.** No ESO, no CRDs, no operator on remote clusters.

### Architecture

#### Declaration: addons-catalog.yaml
Secret requirements are defined alongside addon definitions in the catalog:

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
          api-key: secrets/datadog/api-key      # path in provider
          app-key: secrets/datadog/app-key
```

GitOps-only users edit this file. UI/CLI users manage it through the product. Same source of truth.

#### Provider Interface (pluggable backends)
```go
type SecretProvider interface {
    GetSecret(path string) ([]byte, error)
    ListSecrets(prefix string) ([]string, error)
}
```

Built-in backends:
- `k8s-secrets` — reads from K8s Secrets in a namespace
- `aws-sm` — reads from AWS Secrets Manager

Future (community): HashiCorp Vault, Azure Key Vault, GCP Secret Manager.

#### Reconciler (background goroutine)
Runs continuously in the Sharko server process:

1. Read catalog from Git (cached, refreshed on webhook or timer)
2. For each cluster with addon enabled in cluster-addons.yaml:
   - Check: does the required K8s Secret exist on the remote cluster?
   - Compare hash of provider value with hash of deployed secret
   - If missing or stale → fetch from provider → push to cluster
3. All Sharko-managed secrets labeled: `app.kubernetes.io/managed-by: sharko`
4. Configurable interval (default: 5 minutes, same ballpark as ESO's refreshInterval)

#### Flow: sharko add-cluster
1. Fetch kubeconfig from provider
2. For each addon with secrets defined: fetch values from SecretProvider, create K8s Secrets on remote cluster
3. Register cluster in ArgoCD
4. Update Git (values file + cluster-addons.yaml via PR)

#### Security
- No decrypted secrets cached (no Redis, no plaintext storage)
- Fetch from provider → push to cluster → drop from memory
- Sharko pod has same trust level as ArgoCD (both connect to all clusters)
- ArgoCD configured to ignore Sharko-managed secrets (resource exclusion by label)

#### Resilience
- Pod restarts: K8s restarts pod in seconds, reconciler resumes
- Secrets stale for ~30 seconds during restart (same as ESO pod restart)
- Single point to monitor (vs ESO on N clusters)

### V2 Vision (documented, not V1)
- Full K8s operator with CRDs (`SharkoAddon`, `SharkoSecret`, `SharkoCluster`)
- Controller-runtime based reconciliation instead of goroutine
- Potential to replace Helm for addon management (AppSets created by controller, not Helm range)
- CRDs as the declaration format instead of Helm values
- Same management channels (UI, CLI, API) create CRs instead of editing YAML

### Side Note: GitOps-Only ESO Template
For users who want the old ESO-based approach without Sharko server dependency, provide a reference repo template with ESO + ExternalSecrets preconfigured. This is a separate deliverable, not part of the Sharko server. Tracked in TODO.

---

## Summary of What Gets Built

| Item | Scope | Priority |
|------|-------|----------|
| Remove Helm secrets block | Credential flow | High |
| Single connection (remove list) | Credential flow | High |
| Operations endpoint with sessions | Init flow | High |
| Heartbeat-based session keep-alive | Init flow | High |
| PR merge polling + auto-continue | Init flow | High |
| `secrets:` field in catalog schema | Secrets provider | High |
| SecretProvider interface + AWS SM + K8s backends | Secrets provider | High |
| Background reconciler goroutine | Secrets provider | High |
| Remote cluster secret push (uses existing remoteclient) | Secrets provider | High |
| ArgoCD resource exclusion for managed secrets | Secrets provider | Medium |
| GitOps-only ESO reference template | Separate deliverable | Low |
| Full operator with CRDs (V2) | Future | Deferred |

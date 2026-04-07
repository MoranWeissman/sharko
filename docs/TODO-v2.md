# Sharko — Post v1.2.0 TODO

> Issues found during QA testing on real K8s cluster.

---

## Bugs

- [x] ~~CSP blocks Google Fonts stylesheets~~ — Fixed in PR #90
- [x] ~~Dashboard crashes on uninitialized repo~~ — Fixed in PR #95
- [x] ~~Wizard skips Step 4 (Init)~~ — Fixed in PR #96
- [x] ~~ArgoCD defaults to HTTPS for in-cluster~~ — Fixed in PR #96
- [x] ~~Init fails on empty repos~~ — Fixed in PR #96
- [x] ~~Init writes files one-by-one (slow)~~ — Fixed in PR #96 (batch Git tree API)
- [x] ~~Version shows 1.0.0~~ — Fixed in PR #94

---

## Open Issues

- [ ] **GitHub token not auto-detected from Helm secret** — user passes `--set secrets.GITHUB_TOKEN` during install, but wizard asks for token again. The connection system uses its own encrypted secret and doesn't read the Helm-managed one.
- [ ] **Wizard ArgoCD auto-discovery inconsistent** — tries common service names but may not match all installations. Needs to list services in the ArgoCD namespace and probe them.
- [ ] **Init from Settings doesn't show ArgoCD bootstrap progress** — when initializing from Settings (not wizard), the UI doesn't show sync status polling. The API does the work but the UI doesn't reflect it.
- [ ] **Cluster secrets not managed via GitOps** — the old ESO-based approach (Git → ExternalSecret → AWS SM → K8s Secret) was removed. Sharko now creates secrets imperatively via API (`internal/remoteclient/`). This means cluster registration only works through `sharko add-cluster`, not by editing Git directly. Design decision needed: is imperative sufficient, or do we need a declarative (GitOps) secret mechanism?
- [ ] **No bootstrap-config.yaml was being generated** — root-app.yaml references `{{ .Values.repoURL }}` but the values file with repoURL/targetRevision was missing from the bootstrap template. Fixed in this PR.

---

## Design Decisions (Next Phases)

### 1. Credential Flow — No Helm-Passed Tokens

Remove the pattern of passing secrets via `--set secrets.GITHUB_TOKEN`. Credentials should flow through the product, not Helm values:

- **API** — credentials passed via request headers (Bearer token auth)
- **UI** — credentials entered in the connection wizard, stored encrypted in K8s Secret (`sharko-connections`)
- **CLI** — `sharko connect` passes credentials, stored server-side

The Helm chart should NOT have `secrets.GITHUB_TOKEN`, `secrets.ARGOCD_TOKEN` etc. Users configure credentials through the product after deployment. The `config.devMode` flag that enables env var fallback for credentials should be removed in a future version.

**Action:** Remove `secrets` block from Helm values.yaml. Remove env var credential fallback from serve.go. Ensure wizard + CLI are the only credential entry points.

### 2. Init Flow — Auto-Merge vs Manual Approval Mode

Two modes for repository initialization (and all PR-based operations):

- **Auto-approve** — Sharko creates PR, auto-merges, continues with ArgoCD bootstrap. Fully automated.
- **Manual approval** — Sharko creates PR, then WAITS for the PR to be merged before continuing. The UI shows a "waiting for PR merge" state with a polling mechanism.

**Waiting mechanism options:**
- **Webhook** — Sharko's webhook endpoint (`POST /api/v1/webhooks/git`) already exists. On PR merge event, trigger the continuation (ArgoCD bootstrap).
- **Polling** — UI/API polls the Git provider to check PR merge status every 10 seconds. Simpler, no webhook setup required.

**Recommendation:** Polling is simpler and works everywhere. The UI shows a progress card: "Waiting for PR to be merged..." with a link to the PR. Once merged, automatically continues with ArgoCD bootstrap (create project, root app, wait for sync).

The mode (auto/manual) should be selectable:
- During wizard Step 4
- In Settings → Connections (global setting)
- Via Helm values: `gitops.actions.autoMerge: true/false`

**ArgoCD connection must be configured before bootstrap** — the wizard enforces this (Step 3 before Step 4).

### 3. Secrets Provider — Sharko-Native Secret Management

Replace ESO dependency entirely. Sharko becomes the secrets provider for addon dependencies (Datadog API keys, ESO credentials, etc.).

**Architecture:**
- Sharko reads secrets from a backend (K8s Secrets or AWS Secrets Manager)
- Sharko creates K8s Secrets directly on remote clusters via `internal/remoteclient/`
- All Sharko-managed secrets labeled: `app.kubernetes.io/managed-by: sharko`
- ArgoCD configured to ignore these secrets (resource exclusion)

**Provider interface** (already exists at `internal/providers/provider.go`):
```
ClusterCredentialsProvider interface {
    GetCredentials(clusterName string) (*Kubeconfig, error)
    ListClusters() ([]ClusterInfo, error)
}
```

**Extend with a SecretProvider interface:**
```
SecretProvider interface {
    GetSecret(path string) ([]byte, error)
    ListSecrets(prefix string) ([]string, error)
}
```

**Backends (pluggable):**
- `k8s-secrets` — reads from K8s Secrets in a namespace (built-in)
- `aws-sm` — reads from AWS Secrets Manager (built-in)
- Future: HashiCorp Vault, Azure Key Vault, GCP Secret Manager (community)

**Flow for `sharko add-cluster`:**
1. Fetch kubeconfig from provider
2. For each addon with a secret definition: fetch secret value from SecretProvider, create K8s Secret on remote cluster
3. Register cluster in ArgoCD
4. Update Git (values file + cluster-addons.yaml)

**GitOps-only mode:** For users who don't use the UI/API, Sharko can watch the Git repo (via webhook) for cluster-addons.yaml changes and automatically create secrets on new clusters. This is the declarative path.

**Key difference from ESO/AVP:** No CRDs on remote clusters. No operator running on each cluster. Sharko is the single control plane that pushes secrets. Simpler, fewer moving parts, no security risk of running a privileged operator on every cluster.

---

## Improvements

- [ ] **Auto-detect GitHub token from Helm secret** — connection setup should check if GITHUB_TOKEN exists in the Sharko pod's env vars and pre-fill
- [ ] **Security advisory detection** — parse Helm chart release notes for CVE mentions
- [ ] **Filtering/sorting on list endpoints** — only pagination was done
- [ ] **Audit log for manual changes** — webhook exists but no audit trail persisted
- [ ] **E2E tests** — test against real ArgoCD (Kind + ArgoCD in CI)
- [ ] **AI-parsed release notes** — use AI provider to summarize chart changelogs
- [ ] **Addon dependency ordering** — declare addon B depends on addon A

---

## Tech Debt

- [ ] **Test coverage** — orchestrator ~78%, overall ~23%. Target 50%+
- [ ] **Storybook** — deferred, low ROI for current team size

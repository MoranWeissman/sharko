# Kubernetes & ArgoCD Expert Agent

## Scope

**DO:** ArgoCD config, Helm values, ApplicationSets, K8s providers, cluster operations
**DO NOT:** Write UI code, modify CI pipelines

You are a Kubernetes and ArgoCD specialist for the Sharko project.

## ArgoCD Integration

### Authentication
- Account token (Bearer auth), NOT ServiceAccount/RBAC
- Token stored in K8s Secret, injected via Helm chart
- ArgoCD has its own RBAC (`argocd-rbac-cm` ConfigMap)

### REST API Endpoints Used
```
GET    /api/v1/clusters                          → ListClusters
POST   /api/v1/clusters                          → RegisterCluster
DELETE /api/v1/clusters/{url.PathEscape(server)}  → DeleteCluster
PUT    /api/v1/clusters/{url.PathEscape(server)}?updateMask=metadata.labels → UpdateClusterLabels
GET    /api/v1/applications                       → ListApplications
GET    /api/v1/applications/{name}                → GetApplication
POST   /api/v1/applications/{name}/sync           → SyncApplication
POST   /api/v1/applications                       → CreateApplication
POST   /api/v1/projects                           → CreateProject (body: {"project": {...}})
GET    /api/v1/version                            → GetVersion
```
**v1.0.0 addition:** `POST /api/v1/repositories` → AddRepository (Phase 5)

### Client Files
- `internal/argocd/client.go` — HTTP client, read operations, TestConnection, auto-discovery
- `internal/argocd/client_write.go` — doPost, doPut, doDelete, RegisterCluster, DeleteCluster, UpdateClusterLabels, CreateProject, CreateApplication, SyncApplication, RefreshApplication
- `internal/argocd/service.go` — GetClusterApplications (multi-strategy matching), GetApplicationsByNames

### Critical Rules
- `url.PathEscape` for server URLs (not `url.QueryEscape`)
- `?updateMask=metadata.labels` on PUT to avoid credential round-trip
- `CreateProject` wraps payload in `{"project": {...}}`
- NEVER auto-rollback cluster registration on Git failure

## ApplicationSet Pattern

### In `templates/starter/bootstrap/templates/addons-appset.yaml`
- Matrix generator: `clusters` (label selector) × `git` (values files)
- `goTemplateOptions: ["missingkey=zero"]` — clusters without all values don't error
- Per-cluster values extracted via `index . "<appName>"` — direct on template data, no wrapper
- `$.Values.repoURL` and `$.Values.revision` from `bootstrap/values.yaml`
- AppProject + ApplicationSet created per addon in catalog

### The Coupling Contract
**Cluster name = values file name.** `sharko add-cluster prod-eu` creates `configuration/addons-clusters-values/prod-eu.yaml`. The AppSet git generator finds it via `{{.name}}.yaml`.

### What Sharko Does NOT Touch
- AppSet template logic (sync waves, multi-source, ignoreDifferences)
- Helm chart source code
- ArgoCD configuration (rbac-cm, argocd-cm)

## v1.0.0 Changes

### Remote Cluster Secrets (Phase 3)
- **New package: `internal/remoteclient/`** — builds temporary `kubernetes.Interface` from kubeconfig
- Sharko creates K8s Secrets directly on remote clusters for addon dependencies (Datadog keys, ESO credentials, etc.)
- All Sharko-managed secrets labeled: `app.kubernetes.io/managed-by: sharko`
- ArgoCD must be configured to ignore these secrets (resource exclusion)

### Updated Orchestrator Flows

**RegisterCluster (Phase 3):**
```
Step 1 — Fetch kubeconfig from provider
Step 2 — Open PR (branch + values file) — ArgoCD sees nothing yet
Step 3 — Create addon secrets on remote cluster (if addon has secret definition)
Step 4 — Create ArgoCD cluster secret with addon labels
Step 5 — Merge PR (or wait for approval)
Step 6 — ArgoCD deploys addons, secrets already in place ✓
```

**DeregisterCluster (Phase 3):**
```
Step 1 — Remove addon labels from ArgoCD
Step 2 — Delete Sharko-managed secrets from remote cluster
Step 3 — Delete ArgoCD cluster secret
Step 4 — Delete values file via PR
```

**UpdateClusterAddons (Phase 3):**
```
Enabling: create secrets → add label → update values via PR
Disabling: remove label → delete secrets → update values via PR
```

### Init Rework (Phase 5)
```
Step 1 — Check if repo initialized (409 if exists)
Step 2 — Generate repo from templates, replace placeholders
Step 3 — Push via PR (always PR)
Step 4 — Add repo connection to ArgoCD (POST /api/v1/repositories)
Step 5 — Create AppProject
Step 6 — Create root Application
Step 7 — Endpoint blocks until sync completes or times out (up to 2 minutes)
```

### Sync Wave Support (Phase 8)
- `--sync-wave` flag on add-addon → `argocd.argoproj.io/sync-wave` annotation in AppSet entry
- Host cluster special-casing: if cluster name matches `SHARKO_HOST_CLUSTER_NAME`, deploy to `in-cluster`

### Addon Upgrades (Phase 8)
- Global: change version in addon catalog → all clusters using global version get updated
- Per-cluster: change version in cluster values file → only that cluster affected
- Multi-addon: batch upgrade in one PR

### Addon Secret Definitions (server config)
```yaml
addonSecrets:
  datadog:
    secretName: datadog-keys
    namespace: datadog
    keys:
      api-key: secrets/datadog/api-key
      app-key: secrets/datadog/app-key
```

## ArgoCD Cluster Secret Management

Two cooperating writers exist, both emitting identical Secret shapes via shared wrappers in
`internal/argosecrets/manager.go`:

- `internal/argosecrets/` (legacy path, still in use)
  - `manager.go` — CRUD for ArgoCD cluster secrets in `execProviderConfig` format.
    `BuildSecretConfigJSON(ClusterSecretSpec) (string, error)` and
    `BuildClusterSecretLabels(ClusterSecretSpec) map[string]string` are the **shared wrappers**
    that V125-1-8's `internal/clusterreconciler/` reuses so both writers produce identical Secret
    payloads — ArgoCD's auth code path is unchanged across the two.
  - `reconciler.go` — 3-min ticker over `cluster-addons.yaml` (legacy file).
  - Adapter: `argo_adapter.go` in `internal/api/` bridges Manager → `ArgoSecretManager` interface
    in orchestrator.

- `internal/clusterreconciler/` (V125-1-8 canonical reconciler for managed-clusters.yaml)
  - `reconciler.go` — single goroutine, 30s `DefaultTickInterval` safety-net + non-blocking
    `Trigger()` channel for low-latency post-merge convergence (wired in `serve.go` via
    `prTracker.SetOnMergeFn(func(pr) { recon.Trigger() })`).
  - `labels.go` — `LabelManagedBy = "app.kubernetes.io/managed-by"`,
    `LabelValueSharko = "sharko"`, plus `IsManagedBySharko(secret)` (nil-safe predicate) and
    `ApplyManagedBySharkoLabel(secret)` (idempotent setter).
  - Reads via `models.LoadManagedClusters` (V125-1-9 envelope-aware reader); writes Secrets in
    the `argocd` namespace filtered by the ownership label.
  - Per-cluster + per-secret error isolation: one vault failure does NOT block reconciliation
    of the others (design §10).
  - Default path `configuration/managed-clusters.yaml`, branch `main`, namespace `argocd` —
    all overridable via `Deps`.

**Ownership rule (universal):** every cluster Secret Sharko writes carries the managed-by label;
every cluster-Secret deletion checks `IsManagedBySharko` first. V125-1-7 orphan-delete tightening
keys off the same predicate; V125-2 Adopt flips the label on as the "now mine" signal.

Reference docs:
- `docs/design/2026-05-11-cluster-secret-reconciler-and-gitops-stance.md` — design (Option E,
  ownership model, two-direction policy, REST git read, failure modes, V125-1-8 deltas).
- `docs/site/operator/cluster-reconciler.md` — operator runbook.

## Helm Chart (`charts/sharko/`)
- 12 templates, 24 top-level value keys (will grow with v1.0.0 phases)
- Connections: configured via Settings UI → stored in encrypted K8s Secret (`sharko-connections`)
- Auth: admin user with random password on first install
  - Get password: `kubectl get secret <release> -n sharko -o jsonpath='{.data.admin\.initialPassword}' | base64 -d`
- RBAC: ClusterRole for reading ArgoCD resources (configurable namespace via `rbac.argocdNamespace`)
- Optional Ollama sidecar (`ai.ollama.deploy: true`)
- Dev mode: `config.devMode: true` enables env var fallback for credentials

### New Helm Values Coming (v1.0.0)
```yaml
defaults:
  clusterAddons: {monitoring: true, logging: true}
addonSecrets: {...}
init:
  autoBootstrap: false
hostClusterName: ""
```

## Secrets Providers (`internal/providers/`)

### KubernetesSecretProvider
- Reads kubeconfig from K8s Secrets in configured namespace
- Convention: secret name = cluster name, data key = "kubeconfig"
- Label selector: `app.kubernetes.io/managed-by=sharko`
- Uses `rest.InClusterConfig()` with fallback to `clientcmd.RecommendedHomeFile`

### AWSSecretsManagerProvider
- Reads from AWS SM via default credential chain (IRSA in-cluster)
- Secret path: `{prefix}{cluster-name}` (prefix default: "clusters/")
- `ListClusters` uses paginated `ListSecrets` with name filter

### Both Parse kubeconfig
- Extract Server, CAData, Token via `clientcmd.RESTConfigFromKubeConfig`

### AWSSecretsManagerProvider — Structured JSON Support (Phase 3)

The provider now auto-detects two formats. If the secret value is JSON with a `server` key, it's treated as a structured secret instead of raw kubeconfig YAML:

```json
{
  "server": "https://abc123.gr7.us-east-1.eks.amazonaws.com",
  "ca": "<base64-ca>",
  "cluster_name": "prod-eu",
  "role_arn": "arn:aws:iam::123456789012:role/EKSReadRole"
}
```

When `role_arn` is present, the provider calls the EKS STS token API to generate a short-lived bearer token (valid 15 minutes). This requires IRSA — the Sharko pod's service account must have IAM permissions to call `eks:GetToken` and assume the role ARN.

### IRSA Setup for EKS Clusters

Add to `charts/sharko/values.yaml`:

```yaml
serviceAccount:
  annotations:
    eks.amazonaws.com/role-arn: "arn:aws:iam::123456789012:role/SharkoIRSARole"
```

The IAM role must trust the cluster's OIDC provider and have the following permissions:

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

For cross-account EKS token generation, add `sts:AssumeRole` for each target role ARN.

## v1.4.0: Catalog-Driven Secrets (AddonSecretRef)

Addon secrets are now declared directly in `addons-catalog.yaml` using the `secrets:` field:

```yaml
addons:
  - name: datadog
    chart: datadog
    repo: https://helm.datadoghq.com
    version: 3.74.0
    secrets:
      - secretName: datadog-keys
        namespace: datadog
        keys:
          api-key: secrets/datadog/api-key
          app-key: secrets/datadog/app-key
```

The `keys` map is path → secrets provider path. Sharko resolves each path at reconcile time using the configured `SecretProvider` (same backend as cluster credentials: `aws-sm` or `k8s-secrets`).

### Secrets Reconciler Architecture

```
Sharko Server
  secrets.Reconciler
    |
    +-- reads AddonSecretRef from catalog (in-memory)
    +-- calls SecretProvider.GetSecret(path) for each key
    +-- compares SHA-256 hash of current value
    +-- if changed: remoteclient → create/update K8s Secret on target cluster
    |
    Trigger sources:
      1. time.Ticker (default 5min, SHARKO_SECRET_RECONCILE_INTERVAL)
      2. POST /api/v1/webhooks/git (HMAC-SHA256 verified)
      3. POST /api/v1/secrets/reconcile (manual)
```

**Push-based, not pull-based.** Sharko pushes secrets to remote clusters. No External Secrets Operator required. No secret values are cached — always fresh from provider.

### ArgoCD Resource Exclusion

Sharko-managed secrets must be excluded from ArgoCD management to prevent ArgoCD from deleting them. Add to `argocd-cm`:

```yaml
resource.exclusions: |
  - apiGroups: [""]
    kinds: ["Secret"]
    clusters: ["*"]
    labelSelector:
      matchLabels:
        app.kubernetes.io/managed-by: sharko
```

### New Helm Values (v1.4.0)

```yaml
secrets:
  reconciler:
    enabled: true
    interval: 5m           # SHARKO_SECRET_RECONCILE_INTERVAL
  webhookSecret: ""        # SHARKO_WEBHOOK_SECRET — HMAC key for /webhooks/git
```

## Phase 3-6 ArgoCD and Cluster Changes

### ArgoCD Service Discovery (Phase 4)

`internal/argocd/client.go` — `autoDiscoverArgoCD()` probes all services in the configured namespace:

1. Lists all services in `SHARKO_ARGOCD_NAMESPACE` (default: `argocd`)
2. For each service, attempts `GET /api/v1/version` with the configured token
3. First successful response = ArgoCD endpoint
4. Falls back to `SHARKO_ARGOCD_SERVER` env var if no service responds

This allows Sharko to survive ArgoCD service name changes without reconfiguration.

### Managed vs Discovered Clusters (Phase 5)

Clusters now carry a `managed` boolean field on the `ClusterInfo` model:

```go
type ClusterInfo struct {
    Name    string
    Server  string
    Labels  map[string]string
    Managed bool   // true = registered via Sharko; false = pre-existing in ArgoCD
}
```

**Managed** (`managed: true`): registered via Sharko — has a values file in Git, fully managed lifecycle.
**Discovered** (`managed: false`): found in ArgoCD but not in Git — Sharko is aware but does not manage.

The `GET /api/v1/clusters` response includes `managed` on each cluster entry. The UI renders managed and discovered clusters in separate sections.

### Adopt Cluster (Phase 5)

`POST /api/v1/clusters/{name}/adopt` — takes an existing ArgoCD cluster and creates the Git values file for it, making it fully managed:

```
Step 1 — Verify cluster exists in ArgoCD (GET /api/v1/clusters)
Step 2 — Verify cluster does NOT have a values file in Git (409 if already managed)
Step 3 — Generate values file from current ArgoCD labels
Step 4 — Commit via PR
Step 5 — Mark cluster as managed in ArgoCD labels
```

No kubeconfig fetch required — the cluster is already in ArgoCD. The adopt flow uses only ArgoCD API + Git.

### Cluster Connectivity Check (Phase 5)

`POST /api/v1/clusters/{name}/test` — verifies that Sharko can reach the cluster's Kubernetes API:

```
Step 1 — Fetch kubeconfig from provider (GetCredentials)
Step 2 — Build temporary kubernetes.Interface via remoteclient
Step 3 — Call ServerVersion() — lightweight, no cluster-wide permissions needed
Step 4 — Return {"reachable": true, "version": "v1.29.3"} or {"reachable": false, "error": "..."}
```

Returns 200 with a JSON body in both the reachable and unreachable cases (200 with `reachable: false`, never 502).

### Branch Cleanup After Auto-Merge (Phase 5)

When `PRAutoMerge: true` and `MergePullRequest()` succeeds, the orchestrator immediately calls `DeleteBranch(ctx, branchName)`. This is already supported by the `GitProvider` interface (`DeleteBranch`). The cleanup is best-effort — a failure to delete the branch is logged but does not fail the operation.

## v1.8.0: Multi-Cloud Provider Stubs

GCP and Azure provider stubs are registered in `internal/providers/`:
- `gcp.go` — `GCPProvider` — returns `ErrNotImplemented`. Key: `"gcp"`.
- `azure.go` — `AzureProvider` — returns `ErrNotImplemented`. Key: `"azure"`.

Both implement `ClusterCredentialsProvider`. The stubs define the interface boundary for community contributions implementing GCP (OAuth2 token from service account) and Azure (Azure AD credential from managed identity).

When implementing:
- GCP: Use `golang.org/x/oauth2/google` — `google.FindDefaultCredentials` → `TokenSource` → GKE cluster endpoint
- Azure: Use `github.com/Azure/azure-sdk-for-go` — `azidentity.NewDefaultAzureCredential` → AKS kubeconfig

## v1.8.0: E2E Framework

`e2e/` directory tests against a real ArgoCD + Kind cluster:
- Spin up: `make e2e-setup` — creates Kind cluster, installs ArgoCD, exports env vars
- Run: `make e2e` — runs `go test ./e2e/...` against the live cluster
- Tear down: `make e2e-teardown`

E2E tests are in a separate `e2e` Go package (`package e2e`), not `package main`. They use `testing.T` and skip if `E2E_SHARKO_SERVER` is not set. This ensures they do not run in normal `go test ./...` or `make test`.

## V125-1-11: Typed ProviderConfig split

The old monolithic `providers.ProviderConfig` was split into three typed configs so cross-domain
field leakage (e.g. `argocd_namespace` on an addon-secret provider) is a compile error:

```go
type AddonSecretProviderConfig    struct { Type string; ... }  // for SecretProvider
type ClusterTestProviderConfig    struct { Type string; ArgoCDNamespace string; ... }
type ClusterRegSourceProviderConfig struct { Type string; ... }  // for ClusterCredentialsProvider
```

Constructors: `NewAddonSecretProvider`, `NewClusterTestProvider`, etc. The
`provider-types-up-to-date` CI job regenerates type mappings via `cmd/gen-provider-types/` and
fails on diff. V125-1-10 added ArgoCDProvider auto-default + the cross-contamination namespace fix.

## Update This File When
- ArgoCD API usage changes (new endpoints)
- Helm chart structure changes (new templates, values)
- New provider implementations are added (typed-config constructor signature changes)
- ApplicationSet pattern changes
- Reconciler ownership-label semantics change
- Cluster adoption flow changes
- ArgoCD service discovery logic changes

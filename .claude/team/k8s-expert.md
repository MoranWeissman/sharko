# Kubernetes & ArgoCD Expert Agent

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

## Update This File When
- ArgoCD API usage changes (new endpoints)
- Helm chart structure changes (new templates, values)
- New provider implementations are added
- ApplicationSet pattern changes
- Remote client patterns are established (Phase 3)

# Generic AppSet Template — Design Spec

> Single catalog-driven ApplicationSet template. Zero addon-specific logic.
> All behavior is configured per-addon via catalog fields, exposed through CLI/API/UI.
> Server-side Go logic handles cross-addon computations.

---

## 1. Problem

Sharko currently has two AppSet templates:

- **`templates/starter/`** — minimal, missing safety features (no cluster secret-type filter, no finalizers, no RBAC hardening, no ignoreMissingValueFiles)
- **`templates/bootstrap/`** — production-grade but hardcodes addon-specific logic (Istio sync waves, Datadog multi-source + containerIncludeLogs, ESO IRSA injection, EKS Auto Mode nodepool separation, Kyverno ServerSideApply)

The starter is unsafe for real clusters. The bootstrap is coupled to specific addons and a specific work environment. Neither is what Sharko should ship.

## 2. Solution

One generic template at `templates/bootstrap/templates/addons-appset.yaml`. Every feature is driven by the addon catalog entry — no `if appName == "datadog"` blocks. Server-side Go logic computes derived values (cross-addon namespace aggregation). The starter template directory is deleted.

---

## 3. Addon Catalog Model

### Current model (7 fields)

```go
type AddonCatalogEntry struct {
    AppName           string                   `json:"appName" yaml:"appName"`
    RepoURL           string                   `json:"repoURL" yaml:"repoURL"`
    Chart             string                   `json:"chart" yaml:"chart"`
    Version           string                   `json:"version" yaml:"version"`
    Namespace         string                   `json:"namespace,omitempty" yaml:"namespace,omitempty"`
    InMigration       bool                     `json:"inMigration,omitempty" yaml:"inMigration,omitempty"`
    IgnoreDifferences []map[string]interface{} `json:"ignoreDifferences,omitempty" yaml:"ignoreDifferences,omitempty"`
}
```

### New model (12 fields)

```go
type AddonCatalogEntry struct {
    // Basic (required for add-addon)
    AppName   string `json:"appName" yaml:"appName"`
    RepoURL   string `json:"repoURL" yaml:"repoURL"`
    Chart     string `json:"chart" yaml:"chart"`
    Version   string `json:"version" yaml:"version"`

    // Basic (optional)
    Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"` // defaults to appName

    // Advanced — deployment behavior
    SyncWave    int      `json:"syncWave,omitempty" yaml:"syncWave,omitempty"`       // -2=early, 0=default, 2=late
    SelfHeal    *bool    `json:"selfHeal,omitempty" yaml:"selfHeal,omitempty"`       // nil=true (default), false=allow manual drift
    SyncOptions []string `json:"syncOptions,omitempty" yaml:"syncOptions,omitempty"` // e.g. ["ServerSideApply=true"]

    // Advanced — additional sources
    AdditionalSources []AddonSource `json:"additionalSources,omitempty" yaml:"additionalSources,omitempty"`

    // Advanced — ArgoCD behavior
    IgnoreDifferences []map[string]interface{} `json:"ignoreDifferences,omitempty" yaml:"ignoreDifferences,omitempty"`

    // Advanced — extra Helm configuration
    ExtraHelmValues map[string]string `json:"extraHelmValues,omitempty" yaml:"extraHelmValues,omitempty"` // injected as Helm parameters
}

type AddonSource struct {
    RepoURL    string            `json:"repoURL,omitempty" yaml:"repoURL,omitempty"`       // defaults to GitOps repo
    Path       string            `json:"path,omitempty" yaml:"path,omitempty"`              // folder of manifests or local chart
    Chart      string            `json:"chart,omitempty" yaml:"chart,omitempty"`            // remote Helm chart (path OR chart)
    Version    string            `json:"version,omitempty" yaml:"version,omitempty"`        // chart version (if chart)
    Parameters map[string]string `json:"parameters,omitempty" yaml:"parameters,omitempty"` // Helm parameters
    ValueFiles []string          `json:"valueFiles,omitempty" yaml:"valueFiles,omitempty"` // additional value files
}
```

### Removed fields

- `InMigration` — work-specific migration feature, not part of Sharko

### Defaults

| Field | Default when omitted |
|-------|---------------------|
| `namespace` | addon name |
| `syncWave` | 0 (no ordering) |
| `selfHeal` | true |
| `syncOptions` | `["CreateNamespace=true"]` |
| `additionalSources` | empty (single-source) |
| `ignoreDifferences` | empty |
| `extraHelmValues` | empty |

### Main source constraint

The primary addon source is always a remote Helm chart (`chart` + `repoURL`). No `path` option on the main source. Sharko manages Helm chart addons. Additional sources can use `path` (for companion manifests like CRDs) or `chart`.

---

## 4. The Generic Template

### Location

`templates/bootstrap/templates/addons-appset.yaml`

### Deleted

`templates/starter/` — entire directory removed. `sharko init` uses `templates/bootstrap/`.

### Safety features (always on, not configurable)

- `argocd.argoproj.io/secret-type: cluster` in cluster selector — prevents matching non-cluster secrets
- `resources-finalizer.argocd.argoproj.io` on Applications — prevents orphaning on delete
- Metadata labels: `cluster-name`, `app-type: addon` — enables ArgoCD UI filtering
- `ignoreMissingValueFiles: true` — tolerant of clusters without custom values
- `skipSchemaValidation: true` — tolerant of schema variations across chart versions
- `goTemplateOptions: ["missingkey=zero"]` — safe Go template rendering
- AppProject with full RBAC: `clusterResourceWhitelist: [*/*]`, `namespaceResourceWhitelist: [*/*]`, `sourceRepos: [*]`, `destinations: [*/*, *]`

### Catalog-driven features

All rendered conditionally based on catalog entry values:

```yaml
# Sync wave — only if non-zero
{{- if .syncWave }}
annotations:
  argocd.argoproj.io/sync-wave: "{{ .syncWave }}"
{{- end }}

# Self-heal — from catalog, defaults true
syncPolicy:
  automated:
    prune: true
    selfHeal: {{ if eq .selfHeal false }}false{{ else }}true{{ end }}

# Sync options — from catalog array + always CreateNamespace
syncOptions:
  - CreateNamespace=true
  {{- range .syncOptions }}
  - {{ . }}
  {{- end }}

# Additional sources — each entry becomes a source block
{{- range .additionalSources }}
- repoURL: {{ .repoURL | default $.Values.repoURL }}
  {{- if .path }}
  path: {{ .path }}
  {{- else }}
  chart: {{ .chart }}
  targetRevision: {{ .version }}
  {{- end }}
  {{- if .parameters }}
  helm:
    parameters:
    {{- range $k, $v := .parameters }}
    - name: {{ $k }}
      value: {{ $v }}
    {{- end }}
  {{- end }}
{{- end }}

# Extra Helm values — injected as parameters
{{- range $k, $v := .extraHelmValues }}
- name: {{ $k }}
  value: "{{ $v }}"
{{- end }}

# ignoreDifferences — raw pass-through
{{- if .ignoreDifferences }}
ignoreDifferences:
{{- toYaml .ignoreDifferences | nindent 8 }}
{{- end }}
```

### Host cluster routing

```yaml
destination:
  server: '{{if eq .name "SHARKO_HOST_CLUSTER_NAME"}}https://kubernetes.default.svc{{else}}{{.server}}{{end}}'
```

Placeholder `SHARKO_HOST_CLUSTER_NAME` replaced during `sharko init` with the configured host cluster name.

### What is NOT in the template

- No Datadog-specific logic (parameters, containerIncludeLogs, multi-source, operator CRDs)
- No ESO-specific logic (IRSA injection, ClusterSecretStore source)
- No Istio-specific logic (hardcoded sync waves)
- No Kyverno-specific logic (hardcoded ServerSideApply)
- No EKS Auto Mode / nodepool conditional loading
- No migration mode logic
- No `inMigration` / `migrationIgnoreDifferences`

All of these can be achieved through catalog configuration:
- Istio sync waves → `syncWave: -1` on istio-base catalog entry
- Kyverno SSA → `syncOptions: ["ServerSideApply=true"]` on kyverno catalog entry
- Datadog companion CRDs → `additionalSources` with path to CRDs folder
- ESO ClusterSecretStore → `additionalSources` with path to ESO config chart

---

## 5. Server-Side Computed Values

### Addon Namespace Aggregation

When Sharko generates or updates a cluster values file, it computes which addon namespaces are enabled on that cluster and injects a `_sharko` block into the values.

**Computed on:**
- `RegisterCluster` — initial values file generation
- `UpdateClusterAddons` — values file regeneration when addons change

**Injected into cluster values file:**

```yaml
# Auto-computed by Sharko — do not edit manually
_sharko:
  enabledAddonNamespaces: "cert-manager,external-secrets,datadog"
  enabledAddons:
    - name: cert-manager
      namespace: cert-manager
    - name: external-secrets
      namespace: external-secrets
    - name: datadog
      namespace: datadog
```

**How addons use it:**

Any addon can reference `_sharko.enabledAddonNamespaces` in its per-cluster values or Helm parameters. For example, a monitoring addon that needs to know which namespaces to watch can read this computed value.

**Implementation:**

In the orchestrator's `generateClusterValues()` function:
1. Read the addon catalog from the Git repo (`configuration/addons-catalog.yaml`) via the existing Git client
2. Filter to addons enabled on this cluster (from `req.Addons`)
3. For each enabled addon, look up its namespace from the catalog (or default to addon name)
4. Build the `_sharko` block
5. Include it in the generated values YAML

This replaces the production template's `containerIncludeLogs` Helm template spaghetti with clean Go logic.

---

## 6. CLI Surface

### Progressive disclosure

Basic addon management uses `add-addon` (existing command, extended). Advanced configuration uses new `configure-addon` and `describe-addon` commands.

### `sharko add-addon` (existing, updated)

```bash
sharko add-addon <name> \
  --chart <chart>           # required
  --repo <repoURL>          # required
  --version <version>       # required
  --namespace <namespace>   # optional, defaults to name
  --sync-wave <int>         # optional, defaults to 0
```

Same flags as today plus `--sync-wave` (already exists). No new flags — advanced config goes through `configure-addon`.

### `sharko configure-addon` (new)

```bash
sharko configure-addon <name> \
  --sync-wave <int>                          # deployment ordering
  --self-heal=<bool>                         # true/false
  --sync-option <option>                     # repeatable, e.g. --sync-option ServerSideApply=true
  --ignore-differences '<json>'              # raw ArgoCD ignoreDifferences JSON
  --extra-helm-value <key>=<value>           # repeatable
  --add-source '<json>'                      # AddonSource JSON, repeatable
  --remove-source <index>                    # remove additional source by index
  --version <version>                        # update chart version
```

Each flag is optional — only updates what's provided. Commits changes to catalog via PR.

### `sharko describe-addon` (new)

```bash
sharko describe-addon <name>
```

Outputs full addon configuration with all fields, including defaults. Shows which fields are explicitly set vs defaulted.

### Examples in help text

Each command includes `--help` with real-world examples:

```
Examples:
  # Deploy Istio components in order
  sharko add-addon istio-base --chart base --repo https://istio-release.storage.googleapis.com/charts --version 1.22.0 --namespace istio-system --sync-wave -1
  sharko add-addon istiod --chart istiod --repo https://istio-release.storage.googleapis.com/charts --version 1.22.0 --namespace istio-system --sync-wave 0

  # Configure Kyverno for server-side apply
  sharko configure-addon kyverno --sync-option ServerSideApply=true

  # Ignore HPA replica drift on a deployment-heavy addon
  sharko configure-addon my-addon --ignore-differences '[{"group":"apps","kind":"Deployment","jsonPointers":["/spec/replicas"]}]'

  # Attach a CRDs folder as an additional source
  sharko configure-addon datadog --add-source '{"path":"charts/datadog-crds"}'

  # Disable self-heal for an addon you need to hotfix occasionally
  sharko configure-addon prometheus --self-heal=false
```

---

## 7. API Surface

### Existing endpoints (unchanged)

```
POST   /api/v1/addons          → add addon to catalog (basic fields)
GET    /api/v1/addons          → list all addons in catalog
GET    /api/v1/addons/{name}   → addon detail (fleet-wide status)
DELETE /api/v1/addons/{name}   → remove addon from catalog
```

### Updated: POST /api/v1/addons

Request body accepts all catalog fields. Advanced fields are optional:

```json
{
  "name": "istio-base",
  "chart": "base",
  "repo_url": "https://istio-release.storage.googleapis.com/charts",
  "version": "1.22.0",
  "namespace": "istio-system",
  "sync_wave": -1
}
```

### New: PATCH /api/v1/addons/{name}

Updates any subset of catalog fields. Only provided fields are modified:

```json
{
  "sync_wave": -1,
  "self_heal": false,
  "sync_options": ["ServerSideApply=true"],
  "ignore_differences": [
    {"group": "apps", "kind": "Deployment", "jsonPointers": ["/spec/replicas"]}
  ],
  "extra_helm_values": {
    "datadog.site": "datadoghq.eu"
  },
  "additional_sources": [
    {"path": "charts/datadog-crds", "parameters": {"clusterName": "{{.name}}"}}
  ]
}
```

Response: updated full addon config + Git PR info.

### Updated: GET /api/v1/addons/{name}

Returns full catalog entry with all fields (including defaults):

```json
{
  "appName": "istio-base",
  "repoURL": "https://istio-release.storage.googleapis.com/charts",
  "chart": "base",
  "version": "1.22.0",
  "namespace": "istio-system",
  "syncWave": -1,
  "selfHeal": true,
  "syncOptions": ["CreateNamespace=true"],
  "additionalSources": [],
  "ignoreDifferences": [],
  "extraHelmValues": {}
}
```

---

## 8. UI Surface

### Addon detail page — progressive disclosure

**Basic section (always visible):**
- Name, chart, repo URL, version, namespace
- Edit button for version (quick upgrade path)

**Advanced section (accordion, collapsed by default):**

| Field | UI element | Tooltip |
|-------|-----------|---------|
| Sync Wave | Number input | Controls deployment ordering. Negative = deploy earlier, positive = deploy later. Use for addons with dependencies (e.g., Istio base before Istiod). |
| Self-Heal | Toggle switch | When enabled, ArgoCD automatically reverts manual changes. Disable for addons you need to hotfix via kubectl. |
| Sync Options | Tag input (add/remove strings) | ArgoCD sync options. Common: ServerSideApply=true for CRD-heavy addons like Kyverno. |
| Ignore Differences | JSON editor with syntax highlighting | ArgoCD fields to ignore during sync. Prevents false "out of sync" from expected drift (e.g., HPA modifying replica count). |
| Extra Helm Values | Key-value pair list (add/remove rows) | Additional Helm parameters injected during chart rendering. |
| Additional Sources | List of source cards (add/remove) | Extra Helm charts or manifest folders deployed alongside the main addon chart. |

Each field has an inline "Show example" link that expands a real-world example.

---

## 9. Bundled Addons

Sharko ships with 11 pre-configured addons in the catalog. All disabled by default. `sharko init` generates the catalog and global values files.

| Addon | Category | Bundled global values |
|-------|----------|----------------------|
| cert-manager | TLS/certificates | Yes |
| external-dns | DNS automation | Yes |
| external-secrets | Secrets management | Yes |
| keda | Autoscaling | Yes |
| kyverno | Policy engine | Yes |
| argo-rollouts | Progressive delivery | Yes |
| datadog | Observability | Yes |
| istio-base | Service mesh | Yes |
| istio-cni | Service mesh | Yes |
| istiod | Service mesh | Yes |
| istio-ingress | Service mesh | Yes |

Global values files are sourced from the battle-tested `argocd-cluster-addons` repo, scrubbed of work-specific references.

Users can add more addons with `sharko add-addon` or remove bundled ones they don't need.

### Core/default addons

Server-level configuration (Helm values) that defines which addons every cluster gets automatically on registration:

```yaml
config:
  defaultAddons:
    - cert-manager
    - external-dns
```

Behavior:
- `sharko add-cluster prod-eu` → gets default addons automatically
- `sharko add-cluster prod-eu --addons datadog` → defaults + datadog
- `sharko add-cluster prod-eu --no-defaults --addons datadog` → only datadog
- API: `POST /api/v1/clusters` with empty `addons` → defaults applied
- UI: cluster registration form shows defaults pre-checked, user toggles on/off

---

## 10. Example Helper Templates

Kept in `templates/examples/` as reference — not wired into the main template. Users copy into their repo's `_helpers.tpl` if needed.

**Files:**

```
templates/examples/
  _datadog-helpers.tpl       # containerIncludeLogs computation, ignoreDifferences
  _eso-helpers.tpl           # IRSA role injection, ClusterSecretStore values
  _README.md                 # How to use: copy to your bootstrap/templates/ and include
```

Each file is scrubbed of work-specific references and documented with comments explaining the use case.

---

## 11. Implementation Impact

### Files to modify

**Go backend:**
- `internal/models/addon.go` — update `AddonCatalogEntry` struct, add `AddonSource` struct, remove `InMigration`
- `internal/orchestrator/addon.go` — update catalog generation to include new fields
- `internal/orchestrator/cluster.go` — `generateClusterValues()` adds `_sharko` computed block
- `internal/api/addons_write.go` — update `POST /api/v1/addons` handler for new fields
- `internal/api/addons_write.go` — add `PATCH /api/v1/addons/{name}` handler
- `internal/api/router.go` — register PATCH route
- `cmd/sharko/addon.go` — add `configure-addon` and `describe-addon` commands

**Template:**
- `templates/bootstrap/templates/addons-appset.yaml` — rewrite to generic catalog-driven template
- Delete `templates/starter/` entirely

**UI:**
- `ui/src/views/` — addon detail page with progressive disclosure (advanced accordion)

**Documentation:**
- CLI help text with examples for all commands
- API documentation for PATCH endpoint
- Addon configuration guide with real-world examples

**Bundled addons:**
- Copy and scrub global values from `argocd-cluster-addons`
- Create catalog entries for all 11 addons

### Files to delete

- `templates/starter/` — entire directory
- Any `inMigration` / `migrationIgnoreDifferences` references across codebase
- Addon-specific Helm helpers (`_datadog-helpers.tpl`, `_eso-helpers.tpl` in bootstrap) — moved to `templates/examples/`

---

## 12. Out of Scope

| Item | Reason |
|------|--------|
| Nodepool separation / EKS Auto Mode | Too specific to EKS + Karpenter. Users inject via per-cluster values. |
| Datadog operator CRDs deployment | Work-specific IDP feature, not Sharko core. |
| ESO IRSA injection | Sharko manages secrets directly, ESO's cloud-specific config is user territory. |
| Migration mode (`inMigration`) | Work-specific feature, removed from Sharko. |
| Local charts as main addon source | Sharko manages Helm chart addons. Main source is always remote chart. |
| Kubernetes operator (CRDs) | v2 if adoption justifies. |
| Webhook events | v1.x feature. |

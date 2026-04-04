# ArgoCD Cluster Addons - Modernization Design

## Context

This document proposes improvements to the existing ArgoCD cluster addons management solution as we migrate to:
- **New ArgoCD EKS cluster** (managed by ArgoFleet)
- **GitHub repository** (from Azure DevOps)

The current solution architecture is documented in [README.md](./README.md). This migration provides an opportunity to modernize the solution, address technical debt, and align with best practices.

---

## Executive Summary

### Goals
1. **Simplify** the solution architecture and reduce complexity
2. **Improve security** by deprecating ArgoCD Vault Plugin (AVP)
3. **Standardize** configuration structure for better maintainability
4. **Align** with ArgoFleet patterns for consistency
5. **Reduce** operational overhead and error-prone manual work

### Key Changes
- Bootstrap folder restructure for cleaner hierarchy
- ArgoCD configuration managed externally by ArgoFleet
- Multi-source Datadog deployment for integrated configuration
- Exclusive use of External Secrets Operator (ESO) for security
- Simplified per-cluster configuration model
- Data-driven ApplicationSet templates with minimal hardcoded logic

---

## Current Solution Analysis

### Strengths
✅ Scalable addon management across 50+ clusters \
✅ Environment-based version control \
✅ Flexible per-cluster customization \
✅ Label-based addon enablement \
✅ Integration with AWS Secrets Manager

### Challenges
❌ **Security**: AVP stores secrets in plaintext in Redis cache \
❌ **Complexity**: Nested folder structure (`app-of-apps/cluster-addons/`) \
❌ **Duplication**: Separate apps for related components (datadog-apikey-secret, datadog-tags) \
❌ **Hardcoded logic**: ApplicationSet template has addon-specific if/else chains \
❌ **Configuration sprawl**: Override folders with many small files \
❌ **Missing values handling**: Requires empty files for every cluster-addon combo \
❌ **ArgoCD coupling**: Solution manages ArgoCD config (should be external)

---

## Proposed Architecture

### 1. Bootstrap Restructure

**Current:**
```
app-of-apps/
└── cluster-addons/          # Unnecessary nesting
    ├── Chart.yaml           # name: appset-template (confusing)
    ├── templates/
    │   ├── apps/
    │   │   ├── argocd-config.yaml
    │   │   ├── datadog-apikey-secret.yaml
    │   │   ├── datadog-tags.yaml
    │   │   └── remote-clusters.yaml
    │   └── appsets/
    │       ├── applicationset.yaml
    │       └── addons-app.yaml
    └── values.yaml
```

**Proposed:**
```
bootstrap/                   # Bootstrap IS the folder
├── Chart.yaml              # name: cluster-addons-bootstrap (clear)
├── repository-secret.yaml  # GitHub repo credentials via ESO
├── root-app.yaml           # Root application
└── templates/
    ├── applicationset.yaml # ApplicationSet for cluster addons
    ├── clusters.yaml       # Remote cluster registration
    └── eso.yaml            # External Secrets Operator bootstrap
```

**Benefits:**
- Simpler, flatter structure
- Clear naming (cluster-addons-bootstrap)
- Follows ArgoFleet pattern
- Bootstrap folder is self-contained

**Migration:**
- Move contents up one level
- Update Chart.yaml name
- Update path references in documentation and scripts

---

### 2. External ArgoCD Management

**Current:**
- `bootstrap/templates/apps/argocd-config.yaml` manages ArgoCD settings
- This solution is responsible for ArgoCD configuration

**Proposed:**
- **Remove** argocd-config application entirely
- ArgoCD is managed by **ArgoFleet solution** (separate concern)
- This solution focuses solely on cluster addons

**Rationale:**
- Separation of concerns (ArgoCD infrastructure vs cluster addons)
- ArgoFleet already manages ArgoCD across all clusters
- Reduces coupling and potential conflicts
- Clearer ownership boundaries

**Migration:**
- Delete `bootstrap/templates/apps/argocd-config.yaml`
- Delete `charts/argocd-config/` directory
- Update documentation to clarify external ArgoCD management

---

### 3. Datadog Multi-Source Integration

**Current:**
```
bootstrap/templates/apps/
├── datadog-apikey-secret.yaml    # Separate Application
└── datadog-tags.yaml              # Separate Application

charts/
├── datadog-apikey-secret/         # Small chart
└── datadog-tags/                  # Small chart

ApplicationSet generates:
└── datadog Application (from addons-list.yaml)
```

**Proposed:**
```
charts/
└── datadog-configuration/         # Combined chart (ESO-based)

ApplicationSet generates Datadog with multi-source:
├── Source 1: Official Datadog Helm chart
└── Source 2: Local datadog-configuration chart
```

**Implementation:**
```yaml
# ApplicationSet template - conditional multi-source for Datadog
{{- if eq $appset.appName "datadog" }}
sources:
  # Main Datadog chart
  - repoURL: {{ $appset.repoURL }}
    chart: {{ $appset.chart }}
    targetRevision: {{ $env.version }}
    helm:
      valueFiles:
        - $values/config/values/{{ .name }}.yaml
      values: |
        datadog:
          apiKeyExistingSecret: {{ .name }}
          clusterName: {{ .name }}

  # Configuration chart (secrets via ESO)
  - repoURL: {{ $.Values.repoURL }}
    path: charts/datadog-configuration
    targetRevision: dev
    helm:
      valueFiles:
        - $values/config/values/{{ .name }}.yaml

{{- else }}
# Regular single-source for other addons
source:
  repoURL: {{ $appset.repoURL }}
  # ...
{{- end }}
```

**Benefits:**
- Single Application manages Datadog + its configuration
- Secrets deployed alongside Datadog automatically
- Cleaner than separate applications
- Leverages ArgoCD multi-source feature
- ESO-based (no AVP needed)

**Migration:**
- Create `charts/datadog-configuration/` with ESO templates
- Update ApplicationSet template for multi-source support
- Test with one cluster
- Delete old datadog-apikey-secret and datadog-tags apps/charts

---

### 3.5 ESO Bootstrap Multi-Source Pattern ✅ IMPLEMENTED

**Goal:** Apply same multi-source pattern to ESO bootstrap for consistency

**Problem:**
- ESO bootstrap had hardcoded values (version, region, namespace)
- ClusterSecretStore was in wrong chart (`charts/clusters`)
- No separation between ESO Helm chart and ESO configuration
- Inconsistent with Datadog pattern

**Solution:**
Created `charts/eso-configuration/` following Datadog pattern:

```
charts/eso-configuration/
├── Chart.yaml
├── values.yaml
└── templates/
    └── cluster-secret-store.yaml  # Moved from charts/clusters
```

**ESO Multi-Source Pattern:**
```yaml
# bootstrap/templates/eso.yaml
sources:
  # Source 1: ESO Helm chart
  - repoURL: https://charts.external-secrets.io
    targetRevision: {{ .Values.bootstrap.eso.version }}  # From bootstrap values
    chart: external-secrets
    helm:
      valuesObject:
        # ESO chart configuration from global-values.yaml

  # Source 2: ESO configuration (ClusterSecretStore)
  - repoURL: {{ .Values.repoURL }}
    path: charts/eso-configuration
    helm:
      valuesObject:
        region: {{ .Values.bootstrap.region }}  # Dynamic region
        esoNamespace: {{ .Values.bootstrap.eso.namespace }}
        esoServiceAccount: {{ .Values.bootstrap.eso.serviceAccount }}

  # Source 3: Values reference
  - ref: values
```

**Bootstrap Values:**
```yaml
# configuration/bootstrap-config.yaml
bootstrap:
  region: eu-west-1  # Single source for AWS region
  eso:
    version: 0.9.10  # ESO version (was hardcoded)
    namespace: external-secrets
    serviceAccount: external-secrets
```

**Benefits:**
- ✅ Consistent with Datadog multi-source pattern
- ✅ ESO configuration separated from cluster registration
- ✅ No hardcoded values in templates
- ✅ Dynamic region configuration
- ✅ Single source of truth (`configuration/bootstrap-config.yaml`)
- ✅ Easy to update ESO version, region, namespace

**Charts Cleanup:**
- ✅ Removed `cluster-secret-store.yaml` from `charts/clusters`
- ✅ Removed `external-secrets-sa.yaml` (redundant - ESO creates its own)
- ✅ `charts/clusters` now only handles cluster registration

---

### 3.6 GitHub Repository Credentials via ESO ✅ IMPLEMENTED

**Goal:** Automatically configure ArgoCD to access private GitHub repository using credentials from AWS Secrets Manager

**Problem:**
- ArgoCD needs credentials to access private repository `your-org/argocd-cluster-addons`
- Manual secret creation is error-prone and doesn't scale
- Credentials should be managed in AWS Secrets Manager (single source of truth)

**Solution:**
Use ESO to fetch GitHub PAT from AWS Secrets Manager and create ArgoCD repository secret

**Prerequisites:**
Create AWS Secrets Manager secret in DevOps account (123456789012):
```bash
# Secret name: argocd/your-argocd-cluster
# Keys:
#   - github_user: GitHub username
#   - github_token: GitHub Personal Access Token (PAT)
```

**Implementation:**

**1. Bootstrap Configuration:**
```yaml
# configuration/bootstrap-config.yaml
bootstrap:
  github:
    awsAccount: "123456789012"
    secretName: argocd/your-argocd-cluster
    usernameKey: github_user
    tokenKey: github_token
```

**2. ExternalSecret Resource:**
```yaml
# bootstrap/templates/github-repo-credentials.yaml
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: github-repo-credentials
  namespace: argocd
  annotations:
    argocd.argoproj.io/sync-wave: "-1"  # After ESO, before clusters
spec:
  secretStoreRef:
    name: global-secret-store
    kind: ClusterSecretStore
  target:
    name: github-repo-credentials
    template:
      metadata:
        labels:
          argocd.argoproj.io/secret-type: repository  # ArgoCD auto-detection
      data:
        type: git
        url: {{ .Values.repoURL }}
        username: {{ .github_user }}
        password: {{ .github_token }}
  dataFrom:
    - extract:
        key: {{ .Values.bootstrap.github.secretName }}
```

**How It Works:**
1. ESO deployed (sync-wave -2)
2. ESO fetches credentials from AWS Secrets Manager (DevOps account)
3. Creates ArgoCD repository secret with proper labels
4. ArgoCD automatically detects and uses the secret for repo access
5. All subsequent syncs use authenticated access

**Benefits:**
- ✅ Automated credential management (no manual secret creation)
- ✅ Credentials stored in AWS Secrets Manager (single source of truth)
- ✅ ArgoCD auto-detects repository secret via labels
- ✅ Works across all ArgoCD clusters (portable solution)
- ✅ Follows same ESO pattern as other secrets

**Security:**
- PAT stored securely in AWS Secrets Manager
- ESO uses IAM role for cross-account access
- K8s secret created in `argocd` namespace only
- Credentials never stored in Git

**Architecture Note:**
- GitHub credentials placed in `bootstrap/templates/` (not in `charts/eso-configuration/`)
- Rationale: Repository credentials are ArgoCD infrastructure, not ESO infrastructure
- This maintains pure separation of concerns: ESO domain vs ArgoCD domain
- Follows industry best practice of domain-driven design (bounded contexts)

---

### 4. Security: ESO-Only Approach

**Current:**
- **ESO** for cluster credentials
- **AVP** for Datadog tags injection during sync
- Mixed security model

**Proposed:**
- **ESO exclusively** for all secrets
- Deprecate and remove AVP dependency

**Why Deprecate AVP?**

| Aspect | AVP | ESO |
|--------|-----|-----|
| **Security** | ❌ Plaintext cache in Redis | ✅ Only references in cache |
| **Performance** | ❌ Fetches every sync | ✅ Cached in K8s secrets |
| **ArgoCD Impact** | ❌ Modifies ArgoCD | ✅ External operator |
| **Recommendation** | ❌ Deprecated by ArgoCD team | ✅ Official recommendation |
| **Maintenance** | ❌ Requires plugin installation | ✅ Standard operator |
| **Upgrade Risk** | ❌ Plugin compatibility issues | ✅ Independent versioning |

**Implementation:**
All secrets (Datadog API keys, tags, cluster credentials) managed via ESO:

```yaml
# charts/datadog-configuration/templates/external-secret.yaml
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: datadog-tags
spec:
  secretStoreRef:
    name: aws-secretsmanager
    kind: SecretStore
  target:
    name: datadog-tags
  data:
    - secretKey: DD_TAGS
      remoteRef:
        key: cluster-{{ .Values.clusterName }}
        property: dd_tags
```

**Migration:**
- Update datadog-configuration chart templates to use ESO
- Remove AVP references from documentation
- Remove AVP plugin configuration from ArgoCD (handled by ArgoFleet)
- Create migration guide for teams

---

### 5. Configuration Structure Modernization

#### 5.1 Folder Restructure: values → configuration

**Current:**
```
values/
├── addons-config/
│   ├── defaults.yaml
│   └── overrides/
│       ├── cluster-1/
│       │   ├── addon-1.yaml
│       │   └── addon-2.yaml
│       └── cluster-2/
├── addons-list.yaml
└── clusters.yaml
```

**Proposed:**
```
configuration/
├── addons-catalog.yaml       # Available Helm charts (renamed from addons-list.yaml)
├── cluster-addons.yaml       # Cluster assignments and labels (renamed from clusters.yaml)
├── bootstrap-config.yaml     # Bootstrap-specific configuration (NEW)
├── global-values.yaml        # Global default values (renamed from defaults.yaml)
└── clusters/                 # Per-cluster value files (one file per cluster)
    ├── example-target-cluster.yaml
    ├── my-app-dev.yaml
    └── ...
```

**Benefits:**
- "configuration" is clearer and more comprehensive than "values"
- One file per cluster in `clusters/` subfolder (simpler than override folders)
- Flatter per-cluster structure (no nested overrides/)
- Self-documenting file names
- Separation of concerns (catalog vs assignments vs bootstrap vs values)

#### 5.2 Per-Cluster Values Structure

**Format:**
```yaml
# config/values/example-target-cluster.yaml

# Global values for this cluster (available to all addons)
clusterGlobalValues:
  clusterName: example-target-cluster
  env: dev
  region: eu-west-1
  accountId: "123456789012"
  tags:
    project: devops
    team: platform

# Addon-specific values (key = addon name from addons-list.yaml)
istio-base:
  # istio-base specific values

istiod:
  pilot:
    resources:
      requests:
        cpu: 500m
        memory: 2048Mi

datadog:
  clusterAgent:
    resources:
      limits:
        memory: 1Gi
```

**Benefits:**
- All cluster config in one place
- Global values shared across addons
- Clear addon-specific sections
- Easy to see what's deployed on each cluster

#### 5.3 Remove Environment Labels

**Current:**
```yaml
# values/clusters.yaml
clusters:
  - name: example-target-cluster
    labels:
      env: dev              # ← Remove this
      istio-base: enabled
```

**Proposed:**
```yaml
# config/clusters.yaml
clusters:
  - name: example-target-cluster
    labels:
      # NO env label - all clusters in this repo are non-prod
      istio-base: enabled
      istio-base-version: "1.22.0"
```

**Rationale:**
- All clusters in this repo are non-prod (dev, staging, etc.)
- Environment label is redundant for cluster selection
- Cleaner label structure
- Document in README that this repo = non-prod clusters

---

### 6. ApplicationSet Template Simplification

**Current Issues:**
- Hardcoded parameters for specific addons (84 lines of if/else)
- Namespace logic with chains of conditionals
- Sync wave annotations only for Istio (hardcoded)
- Not DRY, difficult to maintain
- Adding new addon requires template changes

**Proposed: Data-Driven Approach**

#### 6.1 Remove Hardcoded Parameters

**Current:**
```yaml
{{ if eq $appset.appName "datadog" }}
parameters:
  - name: 'datadog.apiKeyExistingSecret'
    value: '{{`{{.name}}`}}'
{{ end }}
{{ if eq $appset.appName "otel" }}
parameters:
  - name: 'projectname'
    value: '{{`{{.name}}`}}'
{{ end }}
# ... 80+ more lines of this
```

**Proposed:**
```yaml
# In addons-list.yaml (metadata-driven)
- appName: datadog
  repoURL: https://helm.datadoghq.com
  chart: datadog
  helmParameters:              # NEW field
    - name: datadog.apiKeyExistingSecret
      value: "{{.name}}"
    - name: datadog.clusterName
      value: "{{.name}}"
```

```yaml
# In ApplicationSet template (generic)
{{- if $appset.helmParameters }}
parameters:
  {{- range $param := $appset.helmParameters }}
  - name: {{ $param.name }}
    value: {{ $param.value }}
  {{- end }}
{{- end }}
```

**Benefits:**
- No template changes when adding addons
- Configuration lives in data files
- Self-documenting (parameters visible in addons-list.yaml)
- Easier to test and validate

#### 6.2 Namespace Logic Simplification

**Current:**
```yaml
{{ if eq $appset.appName "otel" }}
namespace: opentelemetry-operator-system
{{ else if eq $appset.appName "istio-base" }}
namespace: istio-system
{{ else if $appset.namespace }}
namespace: {{ $appset.namespace }}
{{ else }}
namespace: {{ $appset.appName }}
{{ end }}
```

**Proposed:**
```yaml
# In addons-list.yaml
- appName: istio-base
  namespace: istio-system    # Explicit namespace

- appName: datadog
  # No namespace = defaults to addon name
```

```yaml
# In ApplicationSet template
namespace: {{ $appset.namespace | default $appset.appName }}
```

**Benefits:**
- One line in template vs 8+ lines
- Explicit namespace declaration in data
- Clear default behavior (namespace = appName)

#### 6.3 Sync Wave Configuration

**Current:**
```yaml
{{ if eq $appset.appName "istio-base" }}
annotations:
  argocd.argoproj.io/sync-wave: "-1"
{{ else if eq $appset.appName "istio-cni" }}
annotations:
  argocd.argoproj.io/sync-wave: "0"
{{ end }}
```

**Proposed:**
```yaml
# In addons-list.yaml
- appName: istio-base
  syncWave: -1            # NEW field

- appName: istio-cni
  syncWave: 0
```

```yaml
# In ApplicationSet template
{{- if $appset.syncWave }}
annotations:
  argocd.argoproj.io/sync-wave: "{{ $appset.syncWave }}"
{{- end }}
```

**Benefits:**
- Sync order visible in addons-list.yaml
- No hardcoded addon names in template
- Easy to adjust sync order without template changes

#### 6.4 ignoreMissingValueFiles

**Current:**
- Must create empty values files for every cluster-addon combination
- Error if values file doesn't exist
- Proliferation of empty files

**Proposed:**
```yaml
# In ApplicationSet template
source:
  helm:
    valueFiles:
      - '$values/config/values/{{.name}}.yaml'
    ignoreMissingValueFiles: true    # NEW (ArgoCD 2.10+)
```

**Benefits:**
- No need for empty values files
- Clusters can opt-in by creating values file only when needed
- Cleaner repository

---

## Migration Strategy

### Phase 1: Foundation
1. ✅ Bootstrap folder restructure
2. ✅ Remove argocd-config application
3. ✅ Configuration structure refactoring (values → config)

### Phase 2: Datadog & Security
4. Combine Datadog apps using multi-source
5. Complete AVP deprecation (ESO exclusively)

### Phase 3: ApplicationSet Improvements
6. Add ignoreMissingValueFiles
7. Simplify ApplicationSet template (data-driven)

### Phase 4: Validation & Documentation
8. Testing with dev cluster
9. Documentation updates
10. Migration runbook

### Rollback Plan
- Each phase is independently reversible
- Git provides version control
- Test each phase before proceeding
- Keep old solution running in parallel during migration

---

## Testing Strategy

### Test Clusters
- Start with: `example-target-cluster` (single cluster)
- Start with: Istio addons only (istio-base, istiod, istio-cni, istio-ingress)

### Validation Steps
1. **Helm Template Rendering**
   ```bash
   helm template bootstrap/ \
     --values config/addons-list.yaml \
     --values config/clusters.yaml \
     --values config/values/global.yaml \
     | kubectl diff -f -
   ```

2. **ApplicationSet Generation**
   - Verify correct number of Applications generated
   - Check namespace assignments
   - Validate sync wave ordering
   - Confirm multi-source for Datadog

3. **Secret Management**
   - ESO creates secrets correctly
   - Secrets contain expected data
   - No AVP dependencies

4. **Deployment Testing**
   - Deploy to dev cluster
   - Monitor sync status
   - Validate running addons
   - Check for regressions

---

## Comparison: Before vs After

| Aspect | Current (Before) | Proposed (After) |
|--------|------------------|------------------|
| **Bootstrap Structure** | `app-of-apps/cluster-addons/` | `bootstrap/` |
| **Chart Name** | `appset-template` | `cluster-addons-bootstrap` |
| **ArgoCD Config** | Managed by this solution | Managed by ArgoFleet |
| **Datadog Deployment** | 3 separate Applications | 1 Application (multi-source) |
| **Secret Management** | ESO + AVP (mixed) | ESO exclusively |
| **Configuration** | `values/addons-config/overrides/` | `config/values/<cluster>.yaml` |
| **Empty Values Files** | Required | Not required (ignoreMissingValueFiles) |
| **ApplicationSet Logic** | 150+ lines with hardcoded logic | ~50 lines, data-driven |
| **Adding New Addon** | Update template + data | Update data only |
| **Security** | Secrets in Redis cache (AVP) | Secrets in K8s only (ESO) |

---

## Risk Assessment

### Low Risk
- ✅ Bootstrap folder restructure (cosmetic, no logic change)
- ✅ Configuration folder rename (path update only)
- ✅ ignoreMissingValueFiles (additive feature)

### Medium Risk
- ⚠️ Remove argocd-config (requires ArgoFleet to manage ArgoCD)
- ⚠️ Datadog multi-source (new pattern, needs testing)
- ⚠️ ApplicationSet template changes (requires careful validation)

### High Risk (Mitigated)
- 🔴 AVP deprecation → **Mitigation:** Phase migration, ESO already in use
- 🔴 Configuration restructure → **Mitigation:** Start with one cluster, parallel testing

---

## Success Criteria

### Functional
- ✅ All addons deploy successfully to test cluster
- ✅ Secrets managed exclusively via ESO
- ✅ ApplicationSet generates correct Applications
- ✅ Multi-source Datadog deployment works
- ✅ No AVP dependencies

### Non-Functional
- ✅ Simpler codebase (fewer lines, less complexity)
- ✅ Clearer configuration structure
- ✅ Faster onboarding for new team members
- ✅ Easier to add new addons (data-driven)
- ✅ Better security posture (no plaintext caching)

---

## Open Questions

1. **Dynamic Variables in Values Files**
   - Can Helm template variables like `{{.clusterName}}` in valueFiles?
   - Alternative: Use clusterGlobalValues pattern
   - Needs research and testing

2. **Multi-Source ValueFiles**
   - How do valueFiles work with multi-source?
   - Can we reference `$values` from multiple sources?
   - Needs ArgoCD documentation review

3. **Migration Timeline**
   - Deploy to new ArgoCD cluster or migrate existing?
   - Parallel run period?
   - Cutover strategy?

---

## References

- [Current Solution](./README.md)
- [TODO List](./TODO.md)
- [Bootstrap Guide](./BOOTSTRAP.md)
- [ArgoCD Multi-Source Apps](https://argo-cd.readthedocs.io/en/stable/user-guide/multiple_sources/)
- [External Secrets Operator](https://external-secrets.io/)
- [ArgoCD Vault Plugin Deprecation](https://argocd-vault-plugin.readthedocs.io/en/stable/migration/)

---

**Document Status:** Draft for Review
**Last Updated:** 2025-12-29
**Next Review:** After design discussion

# Values Files Guide

This document describes all values files in the project and their purpose.

---

## Table of Contents
- [Overview](#overview)
- [Bootstrap Values](#bootstrap-values)
- [Configuration Values](#configuration-values)
- [Git Files Generator Architecture](#git-files-generator-architecture)
- [Chart Values](#chart-values)
- [Value Precedence](#value-precedence)

---

## Overview

The solution uses a **layered values architecture** with clear separation of concerns:

```
┌─────────────────────────────────────────────────────┐
│ configuration/bootstrap-config.yaml                 │
│ (Bootstrap infrastructure configuration)            │
└────────────────────────┬────────────────────────────┘
                         │
        ┌────────────────┼────────────────┐
        │                │                │
        ▼                ▼                ▼
┌─────────────┐  ┌──────────────┐  ┌──────────────┐
│  Addon      │  │  Cluster     │  │  Global      │
│  Catalog    │  │  Addons      │  │  Values      │
└─────────────┘  └──────────────┘  └──────────────┘
        │                │                │
        └────────────────┼────────────────┘
                         │
                         ▼
              ┌─────────────────────┐
              │ Cluster-Specific    │
              │ Values (48 files)   │
              └─────────────────────┘
```

---

## Bootstrap Values

### 📄 `configuration/bootstrap-config.yaml`

**Purpose**: Configure the bootstrap infrastructure (ESO, ApplicationSets, cluster registration)

**Loaded by**: Root application (`bootstrap/root-app.yaml`)

**Structure**:
```yaml
# Git repository URL
repoURL: 'https://github.com/your-org/argocd-cluster-addons'

# Bootstrap infrastructure configuration
bootstrap:
  # AWS region for ESO and cluster registration
  region: eu-west-1

  # External Secrets Operator configuration
  eso:
    version: 0.9.10              # ESO Helm chart version
    namespace: external-secrets   # Namespace for ESO deployment
    serviceAccount: external-secrets  # Service account name

  # GitHub repository credentials (for private repo access)
  github:
    awsAccount: "123456789012"   # DevOps AWS account
    secretName: argocd/your-cluster-name
    usernameKey: github_user
    tokenKey: github_token

# Application configuration (loaded from configuration files)
appName: ""
applicationsets: []  # Loaded from addons-catalog.yaml
addonsConfig: {}     # Loaded from global-values.yaml
clusters: []         # Loaded from cluster-addons.yaml
```

**Key Values**:
| Value | Purpose | Default |
|-------|---------|---------|
| `repoURL` | Git repository URL for this solution | `https://github.com/your-org/argocd-cluster-addons` |
| `bootstrap.region` | AWS region for ESO ClusterSecretStore | `eu-west-1` |
| `bootstrap.eso.version` | ESO Helm chart version | `0.9.10` |
| `bootstrap.eso.namespace` | ESO deployment namespace | `external-secrets` |
| `bootstrap.eso.serviceAccount` | ESO service account name | `external-secrets` |
| `bootstrap.github.awsAccount` | DevOps AWS account for GitHub PAT | `123456789012` |
| `bootstrap.github.secretName` | Secrets Manager secret name | `argocd/your-cluster-name` |
| `bootstrap.github.usernameKey` | Secret key for GitHub username | `github_user` |
| `bootstrap.github.tokenKey` | Secret key for GitHub token | `github_token` |

**Usage**:
- **ESO bootstrap**: Passes `bootstrap.eso.*` to ESO application
- **ESO configuration**: Passes `bootstrap.region` to ClusterSecretStore
- **GitHub credentials**: ESO creates ArgoCD repository secret from AWS Secrets Manager
- **Multi-source applications**: Uses `repoURL` for values reference

**Prerequisites**:
- AWS Secrets Manager secret `argocd/your-cluster-name` must exist in account 123456789012
- Secret must contain keys: `github_user` and `github_token`
- See `docs/BOOTSTRAP.md` for setup instructions

---

## Configuration Values

### 📄 `configuration/addons-catalog.yaml`

**Purpose**: Define available addons (Helm chart repositories and versions)

**Loaded by**: Root application valueFiles

**Structure**:
```yaml
applicationsets:
  - appName: datadog
    repoURL: https://helm.datadoghq.com
    chart: datadog
    version: 3.70.7  # Default version for all clusters
    namespace: datadog  # Optional: custom namespace
    ignoreDifferences: []  # Optional: ArgoCD ignore differences

  - appName: keda
    repoURL: https://kedacore.github.io/charts
    chart: keda
    version: 2.14.0
```

**Key Fields**:
| Field | Required | Purpose |
|-------|----------|---------|
| `appName` | ✅ | Addon name (matches cluster label) |
| `repoURL` | ✅ | Helm repository URL |
| `chart` | ✅ | Helm chart name |
| `version` | ✅ | Default Helm chart version |
| `namespace` | ❌ | Custom namespace (defaults to appName if omitted) |
| `ignoreDifferences` | ❌ | ArgoCD ignore differences rules |

**Namespace Behavior**:
- **Omit field**: Addon deploys to namespace matching `appName`
  - Example: `datadog` → deployed to `datadog` namespace
- **Specify field**: Addon deploys to custom namespace
  - Example: `istio-base` with `namespace: istio-system` → deployed to `istio-system`

**Usage**:
- Defines the **catalog of available addons**
- ApplicationSets iterate over this list
- Cluster labels match `appName` to enable addons

---

### 📄 `configuration/cluster-addons.yaml`

**Purpose**: Define clusters and their addon assignments via labels

**Loaded by**: Root application valueFiles

**Structure**:
```yaml
# Note: AWS Secrets Manager secret name is automatically prefixed with "k8s-"
#       Example: cluster name "my-app-dev" → secret "k8s-my-app-dev"
clusters:
  - name: my-app-dev
    labels:
      # Enable addons by setting label to "enabled"
      datadog: enabled
      datadog-version: "3.70.7"  # Optional: override default version
      keda: enabled
      istio-base: enabled
```

**Key Fields**:
| Field | Required | Purpose |
|-------|----------|---------|
| `name` | ✅ | Cluster name (used for AWS Secrets Manager lookup with "k8s-" prefix) |
| `labels` | ✅ | Addon enablement and version overrides |

**Label Patterns**:
- `<addon-name>: enabled` - Enable addon with default version
- `<addon-name>-version: "X.Y.Z"` - Override addon version

**Usage**:
- **Cluster registration**: Creates ExternalSecrets for each cluster
- **ApplicationSet generators**: Selects clusters by addon label
- **Version overrides**: Per-cluster addon versions

---

### 📄 `configuration/global-values.yaml`

**Purpose**: Global default values for all addons across all clusters

**Loaded by**: Root application valueFiles

**Structure**:
```yaml
addonsConfig:
  default:
    # ---- Datadog Configuration ---- #
    datadog:
      clusterAgent:
        envFrom:
          - secretRef:
              name: datadog-tags
      datadog:
        logLevel: INFO
        logs:
          enabled: 'true'
          containerCollectAll: 'true'

    # ---- KEDA Configuration ---- #
    keda:
      serviceAccount:
        name: keda-operator-sa
        annotations: {}

    # ---- External Secrets Operator Configuration ---- #
    external-secrets:
      serviceAccount:
        annotations:
          eks.amazonaws.com/role-arn: "arn:aws:iam::ACCOUNT_ID:role/ESO-Role"
```

**Purpose by Addon**:
| Addon | Configuration | Purpose |
|-------|---------------|---------|
| `datadog` | Log settings, resource limits | Default Datadog agent behavior |
| `keda` | Service account name | KEDA operator configuration |
| `external-secrets` | IAM role ARN | ESO AWS authentication |
| `kyverno` | Resource filters, namespaces | Kyverno policy engine config |
| `secrets-store-csi-driver` | Sync settings | CSI driver configuration |

**Usage**:
- Merged into ApplicationSet `valuesObject`
- Can be overridden by cluster-specific values
- Provides sensible defaults for all clusters

---

### 📄 `configuration/addons-clusters-values/<cluster-name>.yaml` (48 files)

**Purpose**: Cluster-specific addon configuration overrides

**Loaded by**: ApplicationSet Git Files generator (parsed at runtime)

**Structure**:
```yaml
# ================================================================ #
# Global Values (used by all addons)
# Define YAML anchors with & for reuse across addon configurations
# ================================================================ #
clusterGlobalValues:
  env: &env dev
  clusterName: &clusterName my-app-dev
  region: &region eu-west-1
  projectName: my-app  # Used for Datadog API key lookup

# ================================================================ #
# Addon-specific overrides
# ================================================================ #

# Datadog Configuration
datadog:
  datadog:
    # Use YAML anchors to reference clusterGlobalValues - no duplication!
    apiKeyExistingSecret: *clusterName
    clusterName: *clusterName
    envFrom:
      - secretRef:
          name: datadog-tags
  clusterAgent:
    resources:
      limits:
        memory: 1Gi

# External Secrets Configuration
external-secrets:
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: "arn:aws:iam::123456789012:role/example-secretsmanager-sa-dev"
```

**Key Sections**:
| Section | Purpose |
|---------|---------|
| `clusterGlobalValues` | Define YAML anchors for cluster metadata |
| `<addon-name>` | Override addon-specific values for this cluster |

**YAML Anchors**:
```yaml
# Define once
clusterGlobalValues:
  clusterName: &clusterName my-app-dev
  region: &region eu-west-1

# Reference many times
datadog:
  clusterName: *clusterName      # Resolves to: my-app-dev

anodot:
  config:
    clusterName: *clusterName    # Resolves to: my-app-dev
    clusterRegion: *region       # Resolves to: eu-west-1
```

**How Values Are Extracted**:

ApplicationSet uses Git Files generator to read and parse these multi-root YAML files:

```yaml
# In ApplicationSet template
generators:
  - matrix:
      generators:
        - clusters: {...}  # Find clusters with addon enabled
        - git:
            files:
              - path: "configuration/addons-clusters-values/{{.name}}.yaml"
```

Git Files generator:
1. Reads the cluster values YAML file from Git
2. Parses YAML into key-value pairs
3. Exposes each root key (datadog, external-secrets, etc.) as variables

ApplicationSet template extracts addon-specific values using GoTemplate:

```yaml
# Extract only this addon's configuration
values: |
  {{- $addonKey := index . "datadog" -}}
  {{- if $addonKey -}}
  {{ $addonKey | toYaml }}
  {{- end -}}
```

**Result**: Each addon receives ONLY its section from the cluster values file, not the entire file.

**Usage**:
- Overrides `global-values.yaml` defaults
- Cluster-specific resource limits, IAM roles, etc.
- YAML anchors eliminate duplication within the file
- Git Files generator enables addon-specific value extraction at deployment time

---

## Git Files Generator Architecture

### How Cluster Values Are Loaded

ApplicationSets use a **Matrix Generator** combining two generators to dynamically read and extract cluster-specific values:

#### 1. Cluster Generator
Discovers clusters with the addon enabled via labels:

```yaml
generators:
  - matrix:
      generators:
        - clusters:
            selector:
              matchLabels:
                argocd.argoproj.io/secret-type: cluster
                datadog: enabled  # Only clusters with datadog enabled
            values:
              revision: '3.70.7'
              app: 'datadog'
```

**Output**: List of cluster names matching the label selector (e.g., `my-app-dev`, `your-argocd-cluster`)

#### 2. Git Files Generator
Reads and parses cluster values files for matched clusters:

```yaml
        - git:
            repoURL: https://github.com/your-org/argocd-cluster-addons
            revision: main
            files:
              - path: "configuration/addons-clusters-values/{{.name}}.yaml"
```

**Process**:
1. For each cluster from Cluster Generator, substitute `{{.name}}` with cluster name
2. Read YAML file: `configuration/addons-clusters-values/my-app-dev.yaml`
3. Parse YAML into key-value pairs
4. Expose each root key as a variable in GoTemplate context

**Variables Available** (example for `my-app-dev.yaml`):
```yaml
# .clusterGlobalValues → { env: "dev", clusterName: "my-app-dev", ... }
# .datadog → { datadog: { ... }, clusterAgent: { ... } }
# .external-secrets → { serviceAccount: { ... } }
# .keda → { ... }
```

#### 3. Matrix Generator Combination
Combines variables from both generators:

```
Cluster Generator variables + Git Files Generator variables
↓
Available in ApplicationSet template:
- .name (cluster name)
- .metadata.labels (cluster labels)
- .metadata.annotations (cluster annotations)
- .datadog (parsed from cluster values file)
- .external-secrets (parsed from cluster values file)
- ... (all other root keys from cluster values file)
```

### Value Extraction with GoTemplate

ApplicationSet template extracts addon-specific values using GoTemplate functions:

```yaml
helm:
  values: |
    {{- $addonKey := index . "datadog" -}}
    {{- if $addonKey -}}
    {{ $addonKey | toYaml }}
    {{- end -}}
```

**Step-by-step**:
1. `index . "datadog"` - Extract the `datadog:` key from parsed YAML
2. `if $addonKey` - Only inject if addon config exists
3. `toYaml` - Convert Go map back to YAML string
4. Result injected as inline `values:` block in Application

**Result**: Each addon Application receives ONLY its configuration section, not the entire cluster values file.

### Benefits

✅ **Single source of truth**: One file per cluster (not 50+ files)
✅ **Addon-specific values**: No values pollution (addons don't see each other's config)
✅ **Native ArgoCD**: Uses Git Files generator (no custom plugins)
✅ **Dynamic**: Values extracted at deployment time (no preprocessing)
✅ **Scalable**: Works with 100+ clusters × 15+ addons

### Example Flow

```
1. Cluster Generator finds: my-app-dev (label: datadog=enabled)
   ↓
2. Git Files Generator reads: configuration/addons-clusters-values/my-app-dev.yaml
   ↓
3. YAML parsed into variables:
   .datadog = { datadog: {...}, clusterAgent: {...} }
   .external-secrets = { serviceAccount: {...} }
   ↓
4. ApplicationSet template extracts:
   {{- $addonKey := index . "datadog" -}}
   ↓
5. Inline values injected:
   values: |
     datadog:
       clusterName: my-app-dev
     clusterAgent:
       resources:
         memory: 1Gi
   ↓
6. Application created: datadog-my-app-dev
   with ONLY datadog configuration (no external-secrets, keda, etc.)
```

---

## Chart Values

### 📄 `charts/eso-configuration/values.yaml`

**Purpose**: Default values for ESO configuration chart

**Structure**:
```yaml
# AWS region for Secrets Manager
region: us-east-1

# ESO service account namespace
esoNamespace: external-secrets
esoServiceAccount: external-secrets
```

**Usage**:
- Overridden by `configuration/bootstrap-config.yaml` (bootstrap.region)
- Used by ClusterSecretStore template
- Pure ESO infrastructure configuration only

---

### 📄 `charts/datadog-configuration/values.yaml`

**Purpose**: Default values for Datadog configuration chart

**Structure**:
```yaml
# Cluster metadata (passed from cluster values file)
clusterGlobalValues:
  clusterName: ""
  region: ""
  projectName: ""
```

**Usage**:
- Receives `clusterGlobalValues` from cluster-specific values
- Creates ExternalSecrets for Datadog API keys and tags

---

### 📄 `charts/clusters/values.yaml`

**Purpose**: Default values for clusters chart

**Structure**:
```yaml
# Cluster list is provided via configuration/cluster-addons.yaml
clusters: []
```

**Usage**:
- Overridden by `cluster-addons.yaml`
- Creates ExternalSecrets for cluster registration

---

## Value Precedence

Values are merged in this order (last wins):

```
┌─────────────────────────────────────────────┐
│ 1. Helm Chart Defaults                     │
│    (from chart repository)                  │
└────────────────┬────────────────────────────┘
                 │ Overridden by ▼
┌─────────────────────────────────────────────┐
│ 2. global-values.yaml                       │
│    (addonsConfig.default.<addon>)           │
│    Loaded via: valueFiles                   │
└────────────────┬────────────────────────────┘
                 │ Overridden by ▼
┌─────────────────────────────────────────────┐
│ 3. Cluster-specific values                  │
│    (Git Files generator extracts addon key) │
│    Loaded via: inline values block          │
└────────────────┬────────────────────────────┘
                 │ Overridden by ▼
┌─────────────────────────────────────────────┐
│ 4. EKS Auto Mode NodePool Config           │
│    (if eksAutoMode: "true" label)           │
│    Loaded via: conditional valueFiles       │
└────────────────┬────────────────────────────┘
                 │ Overridden by ▼
┌─────────────────────────────────────────────┐
│ 5. ApplicationSet Parameters (Highest)      │
│    (cluster annotations via helper funcs)   │
│    Used by: Datadog Secret Backend          │
└─────────────────────────────────────────────┘
```

### ArgoCD Values Hierarchy

ArgoCD merges Helm values in this order (from [ArgoCD documentation](https://argo-cd.readthedocs.io/en/stable/user-guide/multiple_sources/#helm-value-files-from-external-git-repository)):

**Lowest precedence** → **Highest precedence**:
1. `valueFiles` - Files from Git repository
2. `values` - Inline YAML block
3. `valuesObject` - Inline YAML object (deprecated in favor of `values`)
4. `parameters` - Key-value pairs (highest precedence)

### Implementation Details

**Global values** (`valueFiles`):
```yaml
valueFiles:
  - '$values/configuration/addons-global-values/datadog.yaml'
```

**Cluster-specific values** (`values` - inline extraction):
```yaml
values: |
  {{- $addonKey := index . "datadog" -}}
  {{- if $addonKey -}}
  {{ $addonKey | toYaml }}
  {{- end -}}
```
- Git Files generator reads cluster values file
- GoTemplate extracts only the `datadog:` key
- Result injected as inline values (higher precedence than `valueFiles`)

**EKS Auto Mode overrides** (conditional `valueFiles`):
```yaml
valueFiles:
  - '{{if eq (index .metadata.labels "eksAutoMode") "true"}}$values/configuration/addons-global-values/nodepools-config-values/datadog-nodepool-config.yaml{{end}}'
```
- Loaded only if cluster has `eksAutoMode: "true"` label
- Contains nodeSelector and tolerations for infrastructure node pools

**Datadog Secret Backend** (`parameters` - highest precedence):
```yaml
parameters:
  - name: datadog.tags
    value: '{{index .metadata.annotations "dd_tags"}}'
  - name: datadog.apiKey
    value: 'ENC[datadog-api-keys-integration;{{index .metadata.annotations "datadog_apikey_property"}}]'
  - name: datadog.env[0].name
    value: "DD_SECRET_BACKEND_TYPE"
  - name: datadog.env[0].value
    value: "aws.secrets"
```
- Highest precedence (overrides all valueFiles and inline values)
- Injects cluster secret annotations (tags, API key property)
- Injects Secret Backend configuration (type, region)
- Only applies to Datadog addon

### Example

```yaml
# 1. Chart defaults (from Helm repository)
datadog:
  logLevel: WARN
  clusterAgent:
    resources:
      memory: 256Mi
  tags: []

# 2. global-values.yaml (via valueFiles)
datadog:
  logLevel: INFO  # Overrides: WARN → INFO
  datadog:
    logs:
      enabled: true

# 3. Cluster-specific values (via Git Files generator + inline values)
# From: configuration/addons-clusters-values/my-app-dev.yaml
datadog:
  clusterAgent:
    resources:
      memory: 1Gi  # Overrides: 256Mi → 1Gi
    rbac:
      serviceAccountAnnotations:
        eks.amazonaws.com/role-arn: "arn:aws:iam::123456789012:role/Datadog-Agent-example"

# 4. EKS Auto Mode config (if eksAutoMode: "true")
# From: nodepools-config-values/datadog-nodepool-config.yaml
agents:
  nodeSelector:
    node-type: infrastructure
  tolerations:
    - key: infrastructure
      effect: NoSchedule

# 5. Datadog parameters (highest precedence)
datadog:
  tags: 'env:dev,cluster:my-app-dev'  # From cluster secret annotation
  apiKey: 'ENC[datadog-api-keys-integration;my-app-dev]'
  env:
    - name: DD_SECRET_BACKEND_TYPE
      value: "aws.secrets"
    - name: DD_SECRET_BACKEND_CONFIG
      value: '{"aws_session":{"aws_region":"eu-west-1"}}'

# Final merged values for my-app-dev Datadog:
datadog:
  logLevel: INFO          # From global-values.yaml
  tags: 'env:dev,...'     # From parameters (cluster annotations)
  apiKey: 'ENC[...]'      # From parameters (cluster annotations)
  env: [...]              # From parameters (Secret Backend config)
  datadog:
    logs:
      enabled: true       # From global-values.yaml
  clusterAgent:
    resources:
      memory: 1Gi         # From cluster-specific values
    rbac:
      serviceAccountAnnotations:  # From cluster-specific values
        eks.amazonaws.com/role-arn: "..."
  agents:
    nodeSelector:         # From EKS Auto Mode config
      node-type: infrastructure
    tolerations: [...]    # From EKS Auto Mode config
```

### Key Takeaways

- **Global defaults**: Use `valueFiles` for addon defaults across all clusters
- **Cluster overrides**: Git Files generator extracts addon-specific values from multi-root cluster files
- **Conditional config**: Use labels for feature flags (e.g., `eksAutoMode`)
- **Critical overrides**: Use `parameters` for values that MUST take precedence (e.g., Secret Backend config)

---

## Values File Summary Table

| File | Scope | Purpose | Loaded By |
|------|-------|---------|-----------|
| `configuration/bootstrap-config.yaml` | Bootstrap | Bootstrap infrastructure config | Root application |
| `configuration/addons-catalog.yaml` | Global | Available addons catalog | Root application |
| `configuration/cluster-addons.yaml` | Global | Cluster definitions & addon assignments | Root application, clusters chart |
| `configuration/global-values.yaml` | Global | Default addon values for all clusters | Root application |
| `configuration/addons-clusters-values/*.yaml` | Per-cluster | Cluster-specific addon overrides | ApplicationSets (via ignoreMissingValueFiles) |
| `charts/eso-configuration/values.yaml` | Chart | ESO configuration defaults | ESO bootstrap application |
| `charts/datadog-configuration/values.yaml` | Chart | Datadog config chart defaults | Datadog ApplicationSet |
| `charts/clusters/values.yaml` | Chart | Clusters chart defaults | Clusters application |

---

## Best Practices

### ✅ DO
- Define **global defaults** in `global-values.yaml`
- Use **cluster-specific values** for cluster-unique configs (IAM roles, resource limits)
- Use **YAML anchors** in cluster files to avoid duplication
- Define **bootstrap config** in `configuration/bootstrap-config.yaml` (not templates)
- Use **addons-catalog.yaml** for addon versions and repos

### ❌ DON'T
- Hardcode values in templates (use `configuration/bootstrap-config.yaml` instead)
- Duplicate values across multiple cluster files (use `global-values.yaml`)
- Put cluster-specific values in `global-values.yaml`
- Put bootstrap config in cluster-specific files
- Define addon catalogs in multiple places

---

## Migration Notes

### From Old Structure

**Old**:
```
values/addons-config/defaults.yaml      → configuration/global-values.yaml
values/addons-list.yaml                 → configuration/addons-catalog.yaml
values/clusters.yaml                    → configuration/cluster-addons.yaml
values/addons-config/overrides/<cluster>/<addon>.yaml
                                        → configuration/addons-clusters-values/<cluster>.yaml
```

**Key Changes**:
- Multiple files per cluster → Single file per cluster
- Hardcoded values in templates → Centralized in `configuration/bootstrap-config.yaml`
- No bootstrap values → Dedicated `bootstrap` section

---

## Example: Adding a New Cluster

1. **Add cluster to `cluster-addons.yaml`**:
   ```yaml
   - name: new-cluster
     labels:
       datadog: enabled
       keda: enabled
   ```

2. **Create cluster values file** `clusters/new-cluster.yaml`:
   ```yaml
   clusterGlobalValues:
     env: &env dev
     clusterName: &clusterName new-cluster
     region: &region us-east-1
     projectName: new-cluster

   # Only add overrides if needed (otherwise uses global-values.yaml)
   datadog:
     clusterAgent:
       resources:
         memory: 512Mi
   ```

3. **Create AWS Secrets Manager secrets**:
   - Cluster credentials: `k8s-new-cluster`
   - Datadog API key: Add to `datadog-api-keys-integration` secret

Done! ApplicationSets will automatically deploy enabled addons.

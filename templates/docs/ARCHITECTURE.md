# ArgoCD Cluster Addons - Architecture Overview

## Bootstrap Applications (sync order)

### 1. External Secrets Operator (sync-wave: -2)
**File:** `bootstrap/templates/eso.yaml`
**Purpose:** Deploy ESO to the host cluster (ArgoCD control plane)
**What it deploys:**
- ESO Helm chart from https://charts.external-secrets.io
- ESO configuration (ClusterSecretStore for AWS Secrets Manager)
**Dependencies:** None (runs first)
**Target:** Host cluster only

### 2. Clusters Registration (sync-wave: -1)
**File:** `bootstrap/templates/clusters.yaml`
**Purpose:** Register all clusters with ArgoCD and create bootstrap secrets
**What it deploys:**
- Uses `charts/clusters` chart
- Creates ArgoCD cluster connection secrets via ESO
- Creates Datadog secrets on ALL clusters (host and remote)
- Extracts ServiceAccount tokens from remote clusters
**Dependencies:** ESO must be deployed first
**Target:** Host cluster (creates resources for all clusters)

### 3. ESO Remote Prerequisites (sync-wave: -1)
**File:** `bootstrap/templates/eso-remote-prerequisites-appset.yaml`
**Purpose:** Deploy prerequisites on remote clusters for ESO PushSecret
**What it deploys:**
- Uses `charts/eso-remote-prerequisites` chart
- ServiceAccount `eso-push-access` with RBAC on remote clusters
- Datadog namespace on remote clusters
**Dependencies:** Cluster registration (ArgoCD must know about remote clusters)
**Target:** All remote clusters (NOT the host cluster)

### 4. Karpenter NodePools (sync-wave: -2)
**File:** `bootstrap/templates/karpenter-nodepools-appset.yaml`
**Purpose:** Deploy NodePool resources to EKS Auto Mode clusters
**What it deploys:**
- One NodePool Application per cluster (not per addon)
- Filters clusters using `eksAutoMode: true` label
- Deploys infrastructure NodePool for addons
**Dependencies:** Cluster registration (ArgoCD must know about clusters)
**Target:** Clusters with `eksAutoMode: true` label
**Sync Wave:** `-2` (ensures NodePools exist BEFORE addons deploy)

**Key Points:**
- Only ONE NodePool app per cluster (not per addon)
- Uses cluster generator `matchExpressions` on `eksAutoMode` label
- Sync wave `-2` guarantees deployment before all addons

See `docs/KARPENTER-NODEPOOLS-DEPLOYMENT.md` for architecture details.

### 5. Addon ApplicationSets (sync-wave: 0 and above)
**File:** `bootstrap/templates/addons-appset.yaml`
**Purpose:** Deploy addons to clusters based on labels
**What it deploys:**
- Creates ApplicationSets for each addon defined in `addons-catalog.yaml`
- Deploys addons only to clusters with `addon-name: enabled` label
- Supports version overrides via `addon-name-version` label
- Uses Git Files generator to extract cluster-specific values
**Dependencies:** Clusters must be registered, NodePools deployed (if EKS Auto Mode)
**Target:** Clusters with matching labels

**Architecture:**
```yaml
generators:
  - matrix:
      generators:
        # Find clusters with addon enabled
        - clusters:
            selector:
              matchLabels:
                datadog: enabled
        # Read cluster values file
        - git:
            files:
              - path: "configuration/addons-clusters-values/{{.name}}.yaml"
```

**Value Extraction:**
- Git Files generator reads and parses cluster values YAML
- GoTemplate extracts addon-specific key: `{{- $addonKey := index . "datadog" -}}`
- Each addon receives ONLY its configuration section (no values pollution)
- See `docs/VALUES_GUIDE.md` for detailed explanation

**Datadog Special Handling:**
- Uses helper function `datadog.parameters` from `_helpers.tpl`
- Injects cluster annotations (tags, API key, region)
- Configures Datadog Secret Backend for AWS Secrets Manager
- Parameters have highest precedence (override all valueFiles and inline values)

---

## Charts

### charts/clusters
**Purpose:** Cluster registration and Datadog secret bootstrap
**Used by:** `clusters` bootstrap application (sync-wave -1)

**Templates:**
1. **cluster-external-secret.yaml** (Wave 0)
   - Creates ArgoCD cluster connection secrets
   - Fetches cluster credentials from AWS Secrets Manager
   - Embeds Datadog API key in cluster secret
   - One ExternalSecret per cluster

2. **datadog-apikey-host-externalsecret.yaml** (Wave 1)
   - For HOST cluster only
   - Creates `datadog-apikey` secret in datadog namespace
   - Extracts from cluster connection secret
   - No PushSecret needed (already on host)

3. **token-extraction-rbac.yaml** (Wave 0)
   - ServiceAccount `token-extractor` with IAM role
   - RBAC to read/create secrets and ClusterSecretStores
   - Used by token extraction Job

4. **token-extraction-job.yaml** (Wave 2)
   - Runs on HOST cluster
   - Connects to REMOTE clusters using ArgoCD credentials
   - Extracts ServiceAccount token from remote cluster
   - Creates ClusterSecretStore for PushSecret
   - One Job per remote cluster

5. **datadog-pushsecret.yaml** (Wave 3)
   - For REMOTE clusters only
   - Pushes Datadog API key + dd_tags to remote cluster
   - Creates `datadog-apikey` secret in datadog namespace on remote
   - No ESO needed on remote cluster

**Key Flow:**
```
Wave 0: cluster-external-secret.yaml → ArgoCD cluster secret (with Datadog API key embedded)
Wave 1: datadog-apikey-host-externalsecret.yaml → Secret on HOST cluster
Wave 2: token-extraction-job.yaml → Extract token, create ClusterSecretStore
Wave 3: datadog-pushsecret.yaml → Push secret to REMOTE cluster
```

### charts/eso-configuration
**Purpose:** Configure ESO ClusterSecretStore for AWS Secrets Manager
**Used by:** `external-secrets-operator` bootstrap application (sync-wave -2)

**Templates:**
1. **cluster-secret-store.yaml**
   - Global ClusterSecretStore for AWS Secrets Manager
   - Used by all ExternalSecrets to fetch from AWS
   - Uses IRSA (IAM role) for authentication

### charts/eso-remote-prerequisites
**Purpose:** Deploy prerequisites on remote clusters for ESO PushSecret
**Used by:** `eso-remote-prerequisites` ApplicationSet (sync-wave -1)

**Templates:**
1. **namespace.yaml**
   - Creates `datadog` namespace on remote cluster
   - Required for PushSecret to push secrets

2. **serviceaccount.yaml**
   - ServiceAccount `eso-push-access`
   - Role with permissions: get, list, create, update, patch secrets
   - RoleBinding in datadog namespace
   - This is what the token extraction Job creates tokens for

### charts/datadog-configuration
**Purpose:** OLD approach - Datadog secret management via separate chart
**Used by:** DEPRECATED (was used by datadog ApplicationSet)
**Status:** No longer used - secrets are now created by `clusters` chart

**Templates:**
1. **datadog-apikey-externalsecret.yaml**
   - OLD: Created Datadog API key ExternalSecret per cluster
   - Replaced by cluster-external-secret.yaml embedding API key

2. **datadog-tags-externalsecret.yaml**
   - OLD: Created dd_tags ExternalSecret
   - Replaced by embedding in cluster secret

3. **secret-store.yaml**
   - OLD: Created SecretStore per cluster
   - Replaced by global ClusterSecretStore

**Note:** This chart can be deleted or kept for reference.

### charts/infra-karpenter
**Purpose:** Karpenter infrastructure addon
**Used by:** Addon ApplicationSet when `infra-karpenter: enabled` label is set
**Status:** Working addon (not part of bootstrap)

---

## Sync Wave Strategy

**Bootstrap Phase:**
```
Wave -2: ESO Bootstrap (install ESO on host cluster)
         ↓
Wave -1: Clusters App + ESO Remote Prerequisites
         ↓
   Wave 0: ArgoCD cluster credentials (cluster-external-secret.yaml)
   Wave 1: Prerequisites on remote (namespace, ServiceAccount)
           Datadog secret on HOST (datadog-apikey-host-externalsecret.yaml)
   Wave 2: Token extraction Job (token-extraction-job.yaml)
   Wave 3: PushSecret to remote (datadog-pushsecret.yaml)
         ↓
Wave 0+: Addon ApplicationSets (datadog, istio, karpenter, etc.)
```

---

## Secret Flow for Datadog

### Datadog Secret Backend Architecture (Current)

**Approach:** Datadog Agent fetches API keys DIRECTLY from AWS Secrets Manager using IRSA. NO Kubernetes secrets, NO ESO, NO PushSecret.

#### 1. Cluster Secret Annotations (Bootstrap)
`charts/clusters/templates/cluster-external-secret.yaml` creates cluster secrets with annotations:

```yaml
annotations:
  dd_tags: '{{ "{{ .dd_tags }}" }}'              # Datadog tags
  datadog_apikey_property: '{{ $apikeyProperty }}'  # API key property (project-env)
  region: '{{ "{{ .region }}" }}'                # AWS region
```

ESO reads these from AWS Secrets Manager cluster secret (`k8s-<cluster-name>`).

#### 2. ApplicationSet Injects Configuration
When Datadog addon is deployed, ApplicationSet helper function (`datadog.parameters`) injects:

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
  - name: datadog.env[1].name
    value: "DD_SECRET_BACKEND_CONFIG"
  - name: datadog.env[1].value
    value: '{"aws_session":{"aws_region":"{{index .metadata.annotations "region"}}"}}'
```

#### 3. Datadog Agent Runtime
- Agent starts with `ENC[datadog-api-keys-integration;project-env]` notation
- Agent reads `DD_SECRET_BACKEND_TYPE=aws.secrets` and `DD_SECRET_BACKEND_CONFIG`
- Agent uses IRSA (IAM role) to fetch API key from AWS Secrets Manager
- NO Kubernetes secret created (API key never touches cluster)

#### 4. Flow Diagram
```
AWS Secrets Manager
  ├─ k8s-<cluster> secret (dd_tags, region, datadog_apikey_property)
  │    ↓ (ESO reads)
  │  Cluster Secret (annotations)
  │    ↓ (ApplicationSet reads)
  │  Helper function generates parameters
  │    ↓ (ArgoCD injects)
  │  Datadog Helm chart
  │    ↓ (Helm renders)
  │  Datadog Agent pods (with Secret Backend config)
  │    ↓ (Agent runtime)
  │  Agent fetches API key via IRSA
  │    ↓
  └─ datadog-api-keys-integration secret (API keys)
```

**Benefits:**
- ✅ No Kubernetes secrets for Datadog API keys
- ✅ Agent fetches keys directly using IRSA
- ✅ Simpler than PushSecret (no token extraction, no ClusterSecretStore)
- ✅ Official Datadog feature (Secret Backend)
- ✅ Works for both host and remote clusters

See `docs/datadog-secret-backend-investigation.md` for detailed analysis.

### Legacy PushSecret Approach (Deprecated)

**Note:** This approach is no longer used. Keeping for historical reference.

<details>
<summary>Click to expand legacy approach</summary>

### For ALL Clusters (Bootstrap):
1. **AWS Secrets Manager:**
   - `k8s-<cluster-name>` → cluster credentials + dd_tags
   - `datadog-api-keys-integration` → API keys per project/env

2. **Clusters Chart Creates:**
   - ArgoCD cluster secret (with embedded API key)
   - For HOST: ExternalSecret → datadog-apikey secret in datadog namespace
   - For REMOTE: Job extracts token → ClusterSecretStore → PushSecret → datadog-apikey secret in datadog namespace

### For Datadog Addon (When Enabled):
- Datadog Helm chart deployed
- Reads from `datadog-apikey` secret (already exists!)
- Pods start immediately (no waiting for secrets)

</details>

---

## Configuration Files

1. **bootstrap-config.yaml**
   - Host cluster name
   - Git repo URL and revision
   - ESO configuration (version, IAM role)
   - Bootstrap infrastructure settings

2. **cluster-addons.yaml**
   - List of clusters
   - Labels for addon enablement (datadog: enabled, etc.)
   - Labels for cluster features (eksAutoMode: true, etc.)

3. **datadog-project-mappings.yaml**
   - Maps cluster names to Datadog projects
   - Used for API key lookup: `<projectName>-<env>`
   - Pre-computes `datadog_apikey_property` for cluster secrets

4. **addons-catalog.yaml**
   - List of available addons (ApplicationSets)
   - Helm chart URLs and versions
   - Default namespace for each addon

5. **addons-global-values/<addon>.yaml**
   - Global default values for each addon
   - Applied to all clusters via `valueFiles`
   - Lowest precedence (can be overridden by cluster-specific values)

6. **addons-clusters-values/<cluster>.yaml**
   - Per-cluster values for addons (multi-root YAML)
   - Git Files generator reads and parses these files
   - Each addon receives only its section via GoTemplate extraction
   - Structure:
     ```yaml
     clusterGlobalValues: {...}  # YAML anchors for reuse
     datadog: {...}              # Datadog-specific values
     external-secrets: {...}     # ESO-specific values
     keda: {...}                 # KEDA-specific values
     ```

7. **addons-global-values/nodepools-config-values/<addon>-nodepool-config.yaml**
   - EKS Auto Mode NodePool configuration overrides
   - Loaded conditionally when `eksAutoMode: true` label exists
   - Contains nodeSelector and tolerations for infrastructure nodes

8. **karpenter-nodepools-config.yaml**
   - Infrastructure NodePool definition for EKS Auto Mode clusters
   - Deployed by dedicated `karpenter-nodepools` ApplicationSet
   - One NodePool per cluster (not per addon)

---

## Key Design Decisions

1. **Secrets ALWAYS created during bootstrap** - Not conditional on addon enablement
   - When user enables `datadog: enabled`, secret already exists
   - Pods start immediately

2. **ONE ExternalSecret per cluster** - API key embedded in cluster credential
   - Simpler than separate ExternalSecrets
   - Reduces API calls to AWS Secrets Manager

3. **Token extraction via Job** - Not CronJob
   - Runs once during bootstrap (sync-wave 2)
   - Can be re-run by deleting Job (ArgoCD recreates)
   - Simpler than CronJob + separate app

4. **PushSecret instead of ESO on remote** - No ESO installation on remote clusters
   - Remote clusters don't need ESO
   - Simpler architecture
   - Works for any secret type (not just Datadog)

5. **Integrated into clusters chart** - All bootstrap infrastructure in ONE place
   - Not spread across multiple apps/charts
   - Easier to understand and maintain


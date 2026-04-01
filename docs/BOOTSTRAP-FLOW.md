# Bootstrap Flow Diagram

## Complete Bootstrap Process

```
┌─────────────────────────────────────────────────────────────────┐
│                    ArgoCD Bootstrap App                         │
│                  (User applies to cluster)                      │
└────────────────────────────┬────────────────────────────────────┘
                             │
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│                    WAVE -2: ESO Bootstrap                       │
│  bootstrap/templates/eso.yaml                                   │
├─────────────────────────────────────────────────────────────────┤
│  ✓ Install External Secrets Operator on HOST cluster           │
│  ✓ Create global-secret-store (AWS Secrets Manager)            │
└────────────────────────────┬────────────────────────────────────┘
                             │
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│           WAVE -1: Clusters + ESO Remote Prerequisites          │
│  bootstrap/templates/clusters.yaml                              │
│  bootstrap/templates/eso-remote-prerequisites-appset.yaml       │
└────────────────────────────┬────────────────────────────────────┘
                             │
                             ├─────────────────────────────────────┐
                             │                                     │
                             ▼                                     ▼
       ┌─────────────────────────────────┐    ┌─────────────────────────────────┐
       │    charts/clusters (Wave 0)     │    │  eso-remote-prerequisites       │
       │                                 │    │  (Wave 1 - To Remote Clusters)  │
       ├─────────────────────────────────┤    ├─────────────────────────────────┤
       │ cluster-external-secret.yaml    │    │ ✓ Create datadog namespace      │
       │ ✓ Fetch from AWS:               │    │ ✓ Create ServiceAccount         │
       │   - k8s-<cluster> (credentials) │    │   eso-push-access               │
       │   - datadog-api-keys-integration│    │ ✓ Create Role + RoleBinding     │
       │ ✓ Create ArgoCD cluster secret  │    │   (permissions to manage secrets)│
       │   with embedded Datadog API key │    │                                 │
       └────────────┬────────────────────┘    └─────────────────────────────────┘
                    │                                       │
                    ▼                                       ▼
       ┌─────────────────────────────────┐    ┌─────────────────────────────────┐
       │    charts/clusters (Wave 1)     │    │  On Remote Cluster:             │
       ├─────────────────────────────────┤    │  ServiceAccount eso-push-access │
       │ For HOST cluster:               │    │  is ready to receive tokens     │
       │ datadog-apikey-host-            │    └─────────────────────────────────┘
       │   externalsecret.yaml           │
       │ ✓ Extract API key from cluster  │
       │   secret                        │
       │ ✓ Create datadog-apikey secret  │
       │   in datadog namespace (HOST)   │
       └─────────────────────────────────┘
                    │
                    ▼
       ┌─────────────────────────────────┐
       │    charts/clusters (Wave 2)     │
       │                                 │
       ├─────────────────────────────────┤
       │ token-extraction-job.yaml       │
       │ ✓ Job runs on HOST cluster      │
       │ ✓ Connects to REMOTE cluster    │
       │   using ArgoCD credentials      │
       │ ✓ Runs: kubectl create token    │
       │   for eso-push-access SA        │
       │ ✓ Stores token in secret:       │
       │   remote-cluster-token-<name>   │
       │ ✓ Creates ClusterSecretStore:   │
       │   <cluster>-remote-cluster      │
       └────────────┬────────────────────┘
                    │
                    ▼
       ┌─────────────────────────────────┐
       │    charts/clusters (Wave 3)     │
       ├─────────────────────────────────┤
       │ datadog-pushsecret.yaml         │
       │ ✓ PushSecret reads from:        │
       │   - ArgoCD cluster secret       │
       │     (datadog-apikey + dd_tags)  │
       │ ✓ Uses ClusterSecretStore:      │
       │   <cluster>-remote-cluster      │
       │   (authenticates with token)    │
       │ ✓ Creates on REMOTE cluster:    │
       │   datadog-apikey secret         │
       │   in datadog namespace          │
       └────────────┬────────────────────┘
                    │
                    ▼
┌─────────────────────────────────────────────────────────────────┐
│              WAVE 0+: Addon ApplicationSets                     │
│  bootstrap/templates/applicationset.yaml                        │
├─────────────────────────────────────────────────────────────────┤
│  ✓ Deploy addons to clusters with matching labels              │
│  ✓ datadog: enabled → Deploy Datadog chart                     │
│  ✓ Secret already exists! Pods start immediately               │
│  ✓ istio-base, istiod, etc. with proper sync waves             │
└─────────────────────────────────────────────────────────────────┘
```

## Secret Creation per Cluster Type

### HOST Cluster (e.g., devops-argocd-addons-dev-eks)

```
AWS Secrets Manager
├── k8s-devops-argocd-addons-dev-eks (cluster credentials + dd_tags)
└── datadog-api-keys-integration (API keys)
        ↓
cluster-external-secret.yaml
        ↓
Secret: devops-argocd-addons-dev-eks (in argocd namespace)
  - name, server, config (ArgoCD cluster connection)
  - datadog-apikey (embedded)
  - annotations: dd_tags
        ↓
datadog-apikey-host-externalsecret.yaml
        ↓
Secret: datadog-apikey (in datadog namespace)
  - api-key
  - tags
```

### REMOTE Cluster (e.g., devops-automation-dev-eks)

```
AWS Secrets Manager
├── k8s-devops-automation-dev-eks (cluster credentials + dd_tags)
└── datadog-api-keys-integration (API keys)
        ↓
cluster-external-secret.yaml (on HOST)
        ↓
Secret: devops-automation-dev-eks (in argocd namespace on HOST)
  - name, server, config (ArgoCD cluster connection)
  - datadog-apikey (embedded)
  - annotations: dd_tags
        ↓
token-extraction-job.yaml (runs on HOST)
  ├─→ Connects to REMOTE using ArgoCD credentials
  ├─→ kubectl create token eso-push-access -n datadog --duration=87600h
  ├─→ Creates Secret: remote-cluster-token-devops-automation-dev-eks
  └─→ Creates ClusterSecretStore: devops-automation-dev-eks-remote-cluster
        ↓
datadog-pushsecret.yaml (on HOST)
  ├─→ Reads from cluster secret (datadog-apikey + dd_tags)
  ├─→ Uses ClusterSecretStore (authenticates with token)
  └─→ Pushes to REMOTE cluster
        ↓
Secret: datadog-apikey (in datadog namespace on REMOTE)
  - api-key
  - tags
```

## When Adding a New Cluster

### Step 1: Automated Cluster Creation Process
```
1. Create AWS Secret: k8s-<cluster-name>
   {
     "host": "https://...",
     "caData": "...",
     "region": "us-east-1",
     "accountId": "123456789",
     "clusterName": "my-cluster",
     "dd_tags": "env:dev,team:platform"
   }

2. Create values file: configuration/addons-clusters-values/<cluster>.yaml
   clusterGlobalValues:
     clusterName: my-cluster
     env: dev
     projectName: my-project

3. Add to cluster-addons.yaml:
   clusters:
     - name: my-cluster
       labels:
         # Initially no addons enabled
```

### Step 2: ArgoCD Automatically Processes
```
clusters app syncs
  ↓
Creates cluster credential secret
  ↓
Deploys eso-remote-prerequisites to cluster
  ↓
Runs token extraction job
  ↓
Creates PushSecret
  ↓
Datadog secret exists on cluster (ready for use!)
```

### Step 3: User Enables Addon
```
Edit cluster-addons.yaml:
  - name: my-cluster
    labels:
      datadog: enabled    # <-- Add this label

ArgoCD sees label change
  ↓
Datadog ApplicationSet deploys chart
  ↓
Pods start immediately (secret already exists!)
```

## Files Modified Per Cluster

**Bootstrap (Automated):**
- AWS Secrets Manager: `k8s-<cluster-name>`
- `configuration/addons-clusters-values/<cluster>.yaml`
- `configuration/cluster-addons.yaml` (add cluster entry)

**Addon Enablement (Manual):**
- `configuration/cluster-addons.yaml` (add `addon: enabled` label)

**Per-Addon Configuration (Optional):**
- `configuration/addons-clusters-values/<cluster>.yaml` (addon-specific values)


# EKS Auto Mode Infrastructure Nodes with Karpenter

> Comprehensive guide for deploying and managing infrastructure node separation using Karpenter v1 in EKS Auto-Mode clusters

## Table of Contents

1. [Overview](#overview)
2. [Problem & Solution](#problem--solution)
3. [Architecture](#architecture)
4. [Components](#components)
5. [Enablement Guide](#enablement-guide)
6. [Technical Specifications](#technical-specifications)
7. [Resource Management](#resource-management)
8. [Cost Optimization](#cost-optimization)
9. [Lifecycle Management](#lifecycle-management)
10. [Monitoring & Troubleshooting](#monitoring--troubleshooting)
11. [Adding New Components](#adding-new-components)
12. [Best Practices](#best-practices)
13. [Limitations](#limitations)

---

## Overview

This solution isolates critical cluster infrastructure components (Datadog, External Secrets, Istio, etc.) from business logic applications by running them on dedicated Karpenter-managed infrastructure nodes in EKS Auto-Mode clusters.

**Key Benefits:**
- Complete workload isolation via taints and tolerations
- Guaranteed resource availability for platform services
- Independent scaling and lifecycle management
- Automated cost optimization through consolidation
- One NodePool per cluster (not per addon)

---

## Problem & Solution

### Before Implementation

**Problems:**
- Infrastructure components competed for resources with business applications
- No resource isolation between critical platform services and product workloads
- Difficult to guarantee resource availability for essential infrastructure
- Cannot apply different scaling or reliability policies
- NodePools were deployed once per addon (many duplicate Applications)
- No guarantee NodePool exists before addons deploy

### After Implementation

**Solutions:**
- Infrastructure components run on dedicated Karpenter-managed nodes
- Complete workload isolation via taints and tolerations
- Guaranteed resource availability for platform services
- Independent scaling and lifecycle management
- **One NodePool Application per cluster** (not per addon)
- **Sync-wave `-2`** ensures NodePools deploy before all addons
- Dedicated ApplicationSet for infrastructure provisioning

---

## Architecture

### Node Pool Strategy

```
┌─────────────────────────────────────────────────────────────┐
│ EKS Auto-Mode Cluster                                       │
│                                                             │
│  ┌────────────────────────┐    ┌─────────────────────────┐  │
│  │ Infrastructure Nodes   │    │ Application Nodes       │  │
│  │                        │    │ (Managed by Product     │  │
│  │                        │    │  Team)                  │  │
│  │ • Datadog Agents       │    │                         │  │
│  │ • ESO Controller       │    │ • Business Logic Apps   │  │
│  │ • Istio Control Plane  │    │ • Product Workloads     │  │
│  │ • Istio CNI            │    │                         │  │
│  │                        │    │                         │  │
│  │ Taint: infrastructure  │    │ No Infrastructure Taint │  │
│  │ Label: node-type=infra │    │                         │  │
│  └────────────────────────┘    └─────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

### Deployment Flow

```xml
<mxfile host="app.diagrams.net" modified="2026-01-28T00:00:00.000Z" agent="Claude" version="22.1.16">
  <diagram name="EKS Auto Mode NodePool Flow" id="eks-auto-mode-flow">
    <mxGraphModel dx="1434" dy="841" grid="1" gridSize="10" guides="1" tooltips="1" connect="1" arrows="1" fold="1" page="1" pageScale="1" pageWidth="850" pageHeight="1100" math="0" shadow="0">
      <root>
        <mxCell id="0" />
        <mxCell id="1" parent="0" />

        <!-- Node A: AWS Secrets Manager -->
        <mxCell id="A" value="AWS Secrets Manager&lt;br&gt;cluster secret with&lt;br&gt;eksAutoMode: 'true'" style="rounded=1;whiteSpace=wrap;html=1;fillColor=#dae8fc;strokeColor=#6c8ebf;fontSize=12;fontStyle=1" vertex="1" parent="1">
          <mxGeometry x="325" y="40" width="200" height="80" as="geometry" />
        </mxCell>

        <!-- Node B: ESO creates cluster secret -->
        <mxCell id="B" value="ESO creates cluster secret&lt;br&gt;with eksAutoMode as label" style="rounded=1;whiteSpace=wrap;html=1;fillColor=#dae8fc;strokeColor=#6c8ebf;fontSize=12;fontStyle=1" vertex="1" parent="1">
          <mxGeometry x="325" y="160" width="200" height="80" as="geometry" />
        </mxCell>

        <!-- Node C: ApplicationSet detects cluster -->
        <mxCell id="C" value="karpenter-nodepools ApplicationSet&lt;br&gt;detects cluster&lt;br&gt;via label matching" style="rounded=1;whiteSpace=wrap;html=1;fillColor=#fff2cc;strokeColor=#d6b656;fontSize=12;fontStyle=1" vertex="1" parent="1">
          <mxGeometry x="325" y="280" width="200" height="80" as="geometry" />
        </mxCell>

        <!-- Node D: Creates Application -->
        <mxCell id="D" value="Creates Application:&lt;br&gt;karpenter-nodepools-&amp;lt;cluster-name&amp;gt;" style="rounded=1;whiteSpace=wrap;html=1;fillColor=#fff2cc;strokeColor=#d6b656;fontSize=12;fontStyle=1" vertex="1" parent="1">
          <mxGeometry x="325" y="400" width="200" height="80" as="geometry" />
        </mxCell>

        <!-- Node E: Application syncs -->
        <mxCell id="E" value="Application syncs&lt;br&gt;with sync-wave -2&lt;br&gt;BEFORE all addons" style="rounded=1;whiteSpace=wrap;html=1;fillColor=#fff2cc;strokeColor=#d6b656;fontSize=12;fontStyle=1" vertex="1" parent="1">
          <mxGeometry x="325" y="520" width="200" height="80" as="geometry" />
        </mxCell>

        <!-- Node F: NodePool resources deployed -->
        <mxCell id="F" value="NodePool resources&lt;br&gt;deployed to cluster" style="rounded=1;whiteSpace=wrap;html=1;fillColor=#d5e8d4;strokeColor=#82b366;fontSize=12;fontStyle=1" vertex="1" parent="1">
          <mxGeometry x="325" y="640" width="200" height="80" as="geometry" />
        </mxCell>

        <!-- Node G: Addon Applications deploy -->
        <mxCell id="G" value="Addon Applications deploy&lt;br&gt;can schedule on infrastructure nodes" style="rounded=1;whiteSpace=wrap;html=1;fillColor=#e1d5e7;strokeColor=#9673a6;fontSize=12;fontStyle=1" vertex="1" parent="1">
          <mxGeometry x="325" y="760" width="200" height="80" as="geometry" />
        </mxCell>

        <!-- Node H: Addons detect eksAutoMode -->
        <mxCell id="H" value="Addons detect eksAutoMode label&lt;br&gt;load nodepool-config values" style="rounded=1;whiteSpace=wrap;html=1;fillColor=#e1d5e7;strokeColor=#9673a6;fontSize=12;fontStyle=1" vertex="1" parent="1">
          <mxGeometry x="325" y="880" width="200" height="80" as="geometry" />
        </mxCell>

        <!-- Node I: Addon pods schedule -->
        <mxCell id="I" value="Addon pods schedule&lt;br&gt;on infrastructure nodes&lt;br&gt;with tolerations" style="rounded=1;whiteSpace=wrap;html=1;fillColor=#e1d5e7;strokeColor=#9673a6;fontSize=12;fontStyle=1" vertex="1" parent="1">
          <mxGeometry x="325" y="1000" width="200" height="80" as="geometry" />
        </mxCell>

        <!-- Arrows -->
        <mxCell id="arrow-AB" value="" style="edgeStyle=orthogonalEdgeStyle;rounded=0;orthogonalLoop=1;jettySize=auto;html=1;strokeWidth=2;strokeColor=#666666;" edge="1" parent="1" source="A" target="B">
          <mxGeometry relative="1" as="geometry" />
        </mxCell>

        <mxCell id="arrow-BC" value="" style="edgeStyle=orthogonalEdgeStyle;rounded=0;orthogonalLoop=1;jettySize=auto;html=1;strokeWidth=2;strokeColor=#666666;" edge="1" parent="1" source="B" target="C">
          <mxGeometry relative="1" as="geometry" />
        </mxCell>

        <mxCell id="arrow-CD" value="" style="edgeStyle=orthogonalEdgeStyle;rounded=0;orthogonalLoop=1;jettySize=auto;html=1;strokeWidth=2;strokeColor=#666666;" edge="1" parent="1" source="C" target="D">
          <mxGeometry relative="1" as="geometry" />
        </mxCell>

        <mxCell id="arrow-DE" value="" style="edgeStyle=orthogonalEdgeStyle;rounded=0;orthogonalLoop=1;jettySize=auto;html=1;strokeWidth=2;strokeColor=#666666;" edge="1" parent="1" source="D" target="E">
          <mxGeometry relative="1" as="geometry" />
        </mxCell>

        <mxCell id="arrow-EF" value="" style="edgeStyle=orthogonalEdgeStyle;rounded=0;orthogonalLoop=1;jettySize=auto;html=1;strokeWidth=2;strokeColor=#666666;" edge="1" parent="1" source="E" target="F">
          <mxGeometry relative="1" as="geometry" />
        </mxCell>

        <mxCell id="arrow-FG" value="" style="edgeStyle=orthogonalEdgeStyle;rounded=0;orthogonalLoop=1;jettySize=auto;html=1;strokeWidth=2;strokeColor=#666666;" edge="1" parent="1" source="F" target="G">
          <mxGeometry relative="1" as="geometry" />
        </mxCell>

        <mxCell id="arrow-GH" value="" style="edgeStyle=orthogonalEdgeStyle;rounded=0;orthogonalLoop=1;jettySize=auto;html=1;strokeWidth=2;strokeColor=#666666;" edge="1" parent="1" source="G" target="H">
          <mxGeometry relative="1" as="geometry" />
        </mxCell>

        <mxCell id="arrow-HI" value="" style="edgeStyle=orthogonalEdgeStyle;rounded=0;orthogonalLoop=1;jettySize=auto;html=1;strokeWidth=2;strokeColor=#666666;" edge="1" parent="1" source="H" target="I">
          <mxGeometry relative="1" as="geometry" />
        </mxCell>
      </root>
    </mxGraphModel>
  </diagram>
</mxfile>
```

### Sync Wave Order

```
Wave -2: NodePool Applications      (karpenter-nodepools ApplicationSet)
Wave -1: istio-base Applications    (addons ApplicationSet)
Wave  0: istio-cni Applications     (addons ApplicationSet)
Wave  1: istiod + datadog Apps      (addons ApplicationSet)
Wave  2: istio-ingress Apps         (addons ApplicationSet)
Default: All other addons           (addons ApplicationSet)
```

**Why sync-wave -2?**
- Guarantees infrastructure nodes exist before addons deploy
- Prevents pods from being stuck in Pending state
- Ensures proper scheduling from initial deployment

---

## Components

### 1. NodePool Deployment ApplicationSet

**File:** `bootstrap/templates/karpenter-nodepools-appset.yaml`

Dedicated ApplicationSet that deploys NodePool resources:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: karpenter-nodepools
  namespace: argocd
spec:
  goTemplate: true
  goTemplateOptions: ["missingkey=zero"]
  generators:
    - clusters:
        selector:
          matchLabels:
            argocd.argoproj.io/secret-type: cluster
          matchExpressions:
            - key: eksAutoMode
              operator: In
              values: ["true"]
  template:
    metadata:
      name: 'karpenter-nodepools-{{.name}}'
      finalizers:
        - resources-finalizer.argocd.argoproj.io
      annotations:
        argocd.argoproj.io/sync-wave: "-2"  # Before all addons
    spec:
      project: karpenter-nodepools
      sources:
        - repoURL: {{ $.Values.repoURL }}
          targetRevision: {{ $.Values.targetRevision }}
          path: charts/karpenter-nodepools
          helm:
            ignoreMissingValueFiles: true
            valueFiles:
              - '$values/configuration/karpenter-nodepools-config.yaml'
        - repoURL: {{ $.Values.repoURL }}
          targetRevision: {{ $.Values.targetRevision }}
          ref: values
      destination:
        name: '{{.name}}'
        namespace: karpenter
      syncPolicy:
        automated:
          prune: true
        syncOptions:
          - CreateNamespace=true
```

**Key Points:**
- Uses `matchExpressions` on `eksAutoMode` **label** (cluster generator requirement)
- One Application per cluster (not per addon)
- Sync-wave `-2` ensures deployment before all addons

### 2. Karpenter NodePools Chart

**Location:** `charts/karpenter-nodepools/`

Contains Helm chart with:
- `Chart.yaml` - Chart metadata
- `templates/nodepool.yaml` - NodePool CRD template

The template uses `{{- range .Values.nodepools }}` to support multiple NodePool definitions.

### 3. NodePool Configuration

**File:** `configuration/karpenter-nodepools-config.yaml`

Default configuration for the infrastructure NodePool:

```yaml
nodepools:
  - name: infra-karpenter-nodepool
    template:
      metadata:
        labels:
          node-type: infrastructure  # Node label for targeting
      spec:
        nodeClassRef:
          group: eks.amazonaws.com
          kind: NodeClass
          name: default              # EKS Auto-Mode managed NodeClass
        taints:
          - key: infrastructure
            effect: NoSchedule
            value: "true"            # Prevents non-infra pods from scheduling
        requirements:
          - key: node-type
            operator: In
            values: ["infrastructure"]
        expireAfter: 336h            # 14 days node lifecycle
        terminationGracePeriod: 48h  # Graceful draining period
    disruption:
      consolidationPolicy: WhenEmptyOrUnderutilized
      consolidateAfter: 30s          # Fast cost optimization
    limits:
      cpu: "1000"                    # Max 1000 cores
      memory: 1000Gi                 # Max 1000Gi memory
      nodes: 10                      # Max 10 nodes
```

**Key Configuration Elements:**

| Element | Purpose |
|---------|---------|
| `node-type: infrastructure` label | Enables nodeSelector targeting |
| `infrastructure=true:NoSchedule` taint | Prevents non-infrastructure pods from scheduling |
| `nodeClassRef: default` | Uses EKS Auto-Mode managed NodeClass (AWS-managed) |
| `expireAfter: 336h` | Nodes expire after 14 days for security patching |
| `consolidationPolicy` | Automatically right-sizes infrastructure based on usage |
| Resource limits | Prevents unbounded infrastructure scaling |

### 4. Addon NodePool Configuration Overrides

**Location:** `configuration/addons-global-values/nodepools-config-values/`

Pre-configured override files that automatically target infrastructure nodes when `eksAutoMode: "true"`.

**Integrated Addons:**

```
nodepools-config-values/
├── datadog-nodepool-config.yaml          # Datadog agents + cluster agent
├── external-secrets-nodepool-config.yaml # ESO controller
├── istiod-nodepool-config.yaml           # Istio control plane
└── istio-cni-nodepool-config.yaml        # Istio CNI DaemonSet
```

**Example (Datadog):**

```yaml
# configuration/addons-global-values/nodepools-config-values/datadog-nodepool-config.yaml
agents:
  nodeSelector:
    node-type: infrastructure
  tolerations:
    - key: infrastructure
      operator: Equal
      value: "true"
      effect: NoSchedule

clusterAgent:
  nodeSelector:
    node-type: infrastructure
  tolerations:
    - key: infrastructure
      operator: Equal
      value: "true"
      effect: NoSchedule
```

### 5. Automatic Addon Integration

**File:** `bootstrap/templates/addons-appset.yaml:119`

Addons automatically detect `eksAutoMode` label and load NodePool-specific overrides:

```yaml
helm:
  ignoreMissingValueFiles: true
  valueFiles:
    # Global default values
    - '$values/configuration/addons-global-values/{{ $appset.appName }}.yaml'

    # If eksAutoMode is "true", load EKS Auto Mode overrides (tolerations, nodeSelector)
    - '{{`{{if eq (index .metadata.labels "eksAutoMode") "true"}}$values/configuration/addons-global-values/nodepools-config-values/`}}{{ $appset.appName }}{{`-nodepool-config.yaml{{else}}$values/configuration/.skip/no-karpenter-overrides{{end}}`}}'
```

**How It Works:**
1. Cluster secret has label `eksAutoMode: "true"`
2. Addon (e.g., `datadog: enabled`) is enabled for cluster
3. ApplicationSet automatically merges the nodepool-config values file
4. Result: Addon pods scheduled on infrastructure nodes with proper tolerations

### 6. Cluster Secret Configuration

**File:** `charts/clusters/templates/cluster-external-secret.yaml`

ESO template adds `eksAutoMode` as a **label**:

```yaml
target:
  template:
    metadata:
      labels:
        argocd.argoproj.io/secret-type: cluster
        eksAutoMode: '{{ .eksAutoMode }}'  # Label for both ApplicationSet generator AND addon valueFiles conditional
```

The label is used for:
- ApplicationSet cluster generator matching (`matchLabels`)
- Addon conditional valueFiles loading (via GoTemplate if statement)

---

## Enablement Guide

### Step 1: Add eksAutoMode to AWS Secrets Manager

Add `eksAutoMode: "true"` to the cluster secret in AWS Secrets Manager:

```json
{
  "name": "my-eks-auto-mode-cluster",
  "host": "https://...",
  "clusterName": "my-eks-auto-mode-cluster",
  "accountId": "123456789012",
  "region": "eu-west-1",
  "caData": "...",
  "eksAutoMode": "true"   # ← Enables infrastructure NodePool
}
```

**How it works:**
- ESO reads AWS Secrets Manager secret
- Creates cluster secret with `eksAutoMode` as a **label**
- Label triggers `karpenter-nodepools` ApplicationSet (cluster generator)
- Label also triggers addon valueFiles loading (nodepool-config files)

### Step 2: Enable Addons in Cluster Configuration

```yaml
# configuration/cluster-addons.yaml
clusters:
  - name: my-eks-auto-mode-cluster
    labels:
      datadog: enabled             # Will automatically use infra nodes
      external-secrets: enabled    # Will automatically use infra nodes
      istio-base: enabled
      istiod: enabled              # Will automatically use infra nodes
      istio-cni: enabled           # Will automatically use infra nodes
```

### Step 3: Force ESO Reconciliation (Optional)

If you updated an existing secret:

```bash
kubectl annotate externalsecret my-eks-auto-mode-cluster -n argocd \
  force-sync=$(date +%s) --overwrite
```

### Step 4: Verify Cluster Secret

Check that cluster secret has the label:

```bash
# Verify label (used for ApplicationSet generator AND addon valueFiles)
kubectl get secret my-eks-auto-mode-cluster -n argocd \
  -o jsonpath='{.metadata.labels.eksAutoMode}'
# Should return: true
```

### Step 5: Verify NodePool Application Created

```bash
# Check NodePool Application created
kubectl get application karpenter-nodepools-my-eks-auto-mode-cluster -n argocd

# Check Application sync status
kubectl get application karpenter-nodepools-my-eks-auto-mode-cluster -n argocd \
  -o jsonpath='{.status.sync.status}'

# Verify sync wave
kubectl get application karpenter-nodepools-my-eks-auto-mode-cluster -n argocd \
  -o jsonpath='{.metadata.annotations.argocd\.argoproj\.io/sync-wave}'
# Should return: -2
```

### Step 6: Verify NodePool Deployed to Remote Cluster

```bash
# Check NodePool status on remote cluster
kubectl get nodepool --context my-eks-auto-mode-cluster

# Expected output:
NAME                       NODECLASS   NODES   READY
infra-karpenter-nodepool   default     2       True
```

### Step 7: Verify Node Provisioning

```bash
# Check infrastructure nodes on remote cluster
kubectl get nodes -l node-type=infrastructure --context my-eks-auto-mode-cluster

# Verify node taints
kubectl get nodes -l node-type=infrastructure --context my-eks-auto-mode-cluster \
  -o jsonpath='{.items[*].spec.taints}'
```

### Step 8: Verify Pod Scheduling

```bash
# Check that infrastructure pods are scheduled on infrastructure nodes
kubectl get pods -n datadog -o wide --context my-eks-auto-mode-cluster
kubectl get pods -n external-secrets -o wide --context my-eks-auto-mode-cluster
kubectl get pods -n istio-system -o wide --context my-eks-auto-mode-cluster
```

---

## Technical Specifications

### Node Labels

Infrastructure nodes are labeled for targeting:

```yaml
node-type: infrastructure
```

**Usage:**
```yaml
nodeSelector:
  node-type: infrastructure
```

### Node Taints

Infrastructure nodes are tainted to prevent non-infrastructure workloads:

```yaml
- key: infrastructure
  effect: NoSchedule
  value: "true"
```

**Required Toleration:**
```yaml
tolerations:
  - key: infrastructure
    operator: Equal
    value: "true"
    effect: NoSchedule
```

### Required Pod Configuration

For pods to schedule on infrastructure nodes, they **must** include:

**Tolerations (Required):**
```yaml
tolerations:
  - key: infrastructure
    operator: Equal
    value: "true"
    effect: NoSchedule
```

**NodeSelector (Recommended):**
```yaml
nodeSelector:
  node-type: infrastructure
```

### EKS Auto-Mode Integration

**NodeClass Reference:**

The infrastructure NodePool uses EKS Auto-Mode's managed NodeClass:

```yaml
nodeClassRef:
  group: eks.amazonaws.com
  kind: NodeClass
  name: default  # AWS-managed NodeClass for Auto-Mode
```

**Benefits:**
- No need to manage EC2NodeClass resources
- AWS handles AMI management, security groups, and networking
- Simplified configuration and maintenance

**Requirements:**
- ✅ Clusters with EKS Auto-Mode enabled
- ✅ Karpenter v1 (included in Auto-Mode)
- ✅ AWS-managed NodeClass

**Not Supported:**
- ❌ Clusters without Auto-Mode enabled
- ❌ Manual EC2NodeClass configuration

---

## Resource Management

### Critical Requirement: Resource Requests and Limits

**All infrastructure components MUST define proper resource requests and limits.** This is critical for Karpenter node provisioning.

**Why This Matters:**
- Karpenter sizes nodes based on pod resource requests
- Without requests/limits, Karpenter provisions undersized nodes
- Results in CPU/memory exhaustion and unresponsive nodes
- Cluster stability is compromised

**Example Resource Configuration:**

```yaml
# Datadog agent resource configuration
agents:
  containers:
    agent:
      resources:
        requests:
          cpu: 200m
          memory: 256Mi
        limits:
          cpu: 200m
          memory: 256Mi
```

### High Availability Considerations

For HA requirements, use `topologySpreadConstraints` instead of relying solely on nodeSelectors:

```yaml
topologySpreadConstraints:
  - maxSkew: 1
    topologyKey: topology.kubernetes.io/zone
    whenUnsatisfiable: DoNotSchedule
    labelSelector:
      matchLabels:
        app: my-infrastructure-component
```

---

## Cost Optimization

### Automatic Consolidation

The NodePool is configured with aggressive cost optimization:

```yaml
disruption:
  consolidationPolicy: WhenEmptyOrUnderutilized
  consolidateAfter: 30s  # Fast consolidation check interval
```

**Benefits:**
- Automatically right-sizes infrastructure based on actual usage
- Consolidates underutilized nodes (30-second check interval)
- Removes empty nodes immediately
- Reduces cloud costs while maintaining infrastructure availability

**How It Works:**
1. Karpenter checks node utilization every 30 seconds
2. Identifies underutilized nodes (low CPU/memory usage)
3. Migrates pods to other infrastructure nodes
4. Terminates underutilized nodes
5. Result: Optimal node count and cost savings

### Resource Limits

Prevents unbounded infrastructure scaling:

```yaml
limits:
  cpu: "1000"      # Maximum 1000 cores total
  memory: 1000Gi   # Maximum 1000Gi memory total
  nodes: 10        # Maximum 10 nodes
```

**Purpose:**
- Prevents runaway costs from misconfigured workloads
- Ensures predictable infrastructure spending
- Can be adjusted per cluster if needed

---

## Lifecycle Management

### Node Expiration

```yaml
expireAfter: 336h  # 14 days
terminationGracePeriod: 48h
```

**Purpose:**
- Ensures nodes are regularly refreshed for security patches
- Automatic OS and kernel updates via node replacement
- Graceful workload migration during replacement

**Behavior:**
- Nodes expire after 14 days (336 hours)
- Karpenter provisions replacement nodes before terminating old ones
- 48-hour grace period for pod eviction and rescheduling
- Zero-downtime infrastructure refresh

### Node Consolidation

```yaml
disruption:
  consolidationPolicy: WhenEmptyOrUnderutilized
  consolidateAfter: 30s
```

**Behavior:**
- Proactively consolidates underutilized nodes to reduce costs
- 30-second check interval for fast optimization
- Removes empty nodes immediately
- Migrates pods to fewer nodes when possible

---

## Monitoring & Troubleshooting

### Check ApplicationSet Status

```bash
# List all NodePool Applications
kubectl get applications -n argocd -l app.kubernetes.io/instance=karpenter-nodepools

# Check specific Application
kubectl get application karpenter-nodepools-<cluster-name> -n argocd -o yaml
```

### Check NodePool Status

```bash
# On remote cluster
kubectl get nodepool --context <cluster-name>

# Detailed status
kubectl get nodepool infra-karpenter-nodepool --context <cluster-name> -o yaml
```

### Verify Node Provisioning

```bash
# List infrastructure nodes
kubectl get nodes -l node-type=infrastructure --context <cluster-name>

# Check node capacity and allocations
kubectl describe node <node-name> --context <cluster-name>

# Check node count and instance types
kubectl get nodes -l node-type=infrastructure --context <cluster-name> \
  -o custom-columns=NAME:.metadata.name,INSTANCE-TYPE:.metadata.labels.node\\.kubernetes\\.io/instance-type,AGE:.metadata.creationTimestamp
```

### Review Pod Scheduling

```bash
# Check pods with infrastructure tolerations
kubectl get pods -A --context <cluster-name> -o json | \
  jq '.items[] | select(.spec.tolerations[]? | select(.key=="infrastructure"))'

# Check pending pods that might need infrastructure tolerations
kubectl get pods -A --field-selector status.phase=Pending --context <cluster-name>

# Verify infrastructure pods are on infrastructure nodes
kubectl get pods -n datadog -o wide --context <cluster-name>
```

### Debug Consolidation

```bash
# Check Karpenter logs for consolidation decisions
kubectl logs -n karpenter -l app.kubernetes.io/name=karpenter --context <cluster-name> \
  --tail=100 | grep consolidation
```

### Troubleshooting ApplicationSet Not Creating Applications

**Check cluster secret has label:**

```bash
kubectl get secret <cluster-name> -n argocd -o jsonpath='{.metadata.labels}' | jq .
```

Should include: `"eksAutoMode": "true"`

**If missing, force ESO to reconcile:**

```bash
kubectl annotate externalsecret <cluster-name> -n argocd \
  force-sync=$(date +%s) --overwrite
```

### Troubleshooting Application Stuck in Unknown/OutOfSync

**Check AppProject exists:**

```bash
kubectl get appproject karpenter-nodepools -n argocd
```

**Check source repository access:**

```bash
kubectl get application karpenter-nodepools-<cluster-name> -n argocd -o yaml
```

**Manually sync:**

```bash
argocd app sync karpenter-nodepools-<cluster-name>
```

### Troubleshooting NodePool Not Deployed

**Check Application sync status:**

```bash
kubectl get application karpenter-nodepools-<cluster-name> -n argocd
```

**Check sync wave:**

```bash
kubectl get application karpenter-nodepools-<cluster-name> -n argocd \
  -o jsonpath='{.metadata.annotations.argocd\.argoproj\.io/sync-wave}'
```

Should return: `-2`

---

## Adding New Components

To add a new infrastructure component to run on dedicated nodes:

### 1. Create Override Values File

Create: `configuration/addons-global-values/nodepools-config-values/<addon-name>-nodepool-config.yaml`

```yaml
# Example: my-addon-nodepool-config.yaml
controller:
  nodeSelector:
    node-type: infrastructure
  tolerations:
    - key: infrastructure
      operator: Equal
      value: "true"
      effect: NoSchedule
  resources:
    requests:
      cpu: 100m        # CRITICAL: Always define resource requests
      memory: 128Mi
    limits:
      cpu: 100m
      memory: 128Mi
```

### 2. Ensure Cluster Has eksAutoMode Enabled

Verify cluster has `eksAutoMode: "true"` in AWS Secrets Manager secret:

```json
{
  "eksAutoMode": "true"
}
```

### 3. Enable Addon in Cluster Configuration

```yaml
# configuration/cluster-addons.yaml
clusters:
  - name: my-cluster
    labels:
      my-addon: enabled  # Will automatically use infra nodes
```

### 4. Verify Integration

The ApplicationSet will automatically include the nodepool-config file when:
- Cluster secret has `eksAutoMode: "true"` label
- Addon is enabled for the cluster

```bash
# Check addon Application
kubectl get application my-addon-my-cluster -n argocd

# Verify addon pods on infrastructure nodes
kubectl get pods -n <addon-namespace> -o wide --context my-cluster
```

---

## Best Practices

### ✅ DO

- **Always define resource requests and limits** for infrastructure components
- Use `topologySpreadConstraints` for HA requirements
- Test in dev cluster before production rollout
- Monitor node consolidation to ensure cost savings
- Set appropriate node expiration times (14 days recommended)
- Use the default managed NodeClass for EKS Auto-Mode
- Use `eksAutoMode` in AWS Secrets Manager (source of truth)
- Keep `eksAutoMode` as label on cluster secrets
- Review Karpenter logs for consolidation and provisioning decisions

### ❌ DON'T

- Don't deploy infrastructure components without resource requests/limits
- Don't rely solely on nodeSelector for HA (use topologySpreadConstraints)
- Don't manually manage EC2NodeClass in Auto-Mode clusters
- Don't set resource limits too low (causes pod evictions)
- Don't disable consolidation without good reason (increases costs)
- Don't use this solution on non-Auto-Mode clusters
- Don't modify cluster secrets manually (use AWS Secrets Manager)
- Don't remove `eksAutoMode` label from cluster secrets

---

## Limitations

### Cluster Requirements

- **Required:** EKS Auto-Mode enabled
- **Required:** Karpenter v1 (automatically included in Auto-Mode)
- **Not supported:** Clusters without Auto-Mode

### Scope

- **In scope:** Infrastructure components (operators, agents, controllers)
- **Out of scope:** Business logic applications
- **Application node pools:** Managed by product teams, not platform team

### Resource Limits

- Maximum 10 infrastructure nodes per NodePool (configurable)
- Maximum 1000 CPU cores total (configurable)
- Maximum 1000Gi memory total (configurable)

### ApplicationSet Generator Constraints

- `eksAutoMode` **must be a label** for ApplicationSet cluster generator
- Cluster generator uses `matchLabels` for label-based filtering
- Label is also used for addon conditional valueFiles loading

---

## Related Documentation

- [VALUES_GUIDE.md](VALUES_GUIDE.md) - How to configure addon values
- [ARCHITECTURE.md](ARCHITECTURE.md) - Overall bootstrap architecture
- [BOOTSTRAP-FLOW.md](BOOTSTRAP-FLOW.md) - Bootstrap process and sync waves

---

## Support and Contact

**For issues with infrastructure node separation:**

1. Check NodePool Application status: `kubectl get application karpenter-nodepools-<cluster> -n argocd`
2. Check NodePool resource: `kubectl get nodepool --context <cluster>`
3. Verify node provisioning: `kubectl get nodes -l node-type=infrastructure --context <cluster>`
4. Review pod scheduling: Check if infrastructure pods have correct tolerations
5. Monitor costs: Ensure consolidation is working properly
6. Contact DevOps Team: For configuration assistance or issues

**Maintainer:** DevOps Team
**Last Updated:** January 2026

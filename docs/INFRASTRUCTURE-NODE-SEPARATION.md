# Infrastructure Node Separation with Karpenter

> Implementation guide for separating infrastructure components onto dedicated Karpenter-managed node pools in EKS Auto-Mode clusters

## Overview

This document explains the implementation of infrastructure node separation using Karpenter v1 in EKS Auto-Mode clusters. The solution isolates critical cluster infrastructure components (Datadog, External Secrets, Istio, etc.) from business logic applications by running them on dedicated infrastructure nodes.

## Problem Statement

**Before Implementation:**
- Infrastructure components (Datadog agents, ESO controllers, Istio control plane) competed for resources with business logic applications
- No resource isolation between critical platform services and product workloads
- Difficult to guarantee resource availability for essential infrastructure components
- Cannot apply different scaling or reliability policies for infrastructure vs. applications

**After Implementation:**
- Infrastructure components run on dedicated Karpenter-managed node pools
- Complete workload isolation via taints and tolerations
- Guaranteed resource availability for platform services
- Independent scaling and lifecycle management for infrastructure nodes

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

### Components

#### 1. Karpenter NodePools Chart
**Location:** `charts/karpenter-nodepools/`

Creates Karpenter NodePool resources that provision dedicated infrastructure nodes.

**Key Files:**
- `charts/karpenter-nodepools/templates/nodepool.yaml` - NodePool template supporting Karpenter v1 features
- `charts/karpenter-nodepools/Chart.yaml` - Chart metadata

**Deployed by:** Dedicated `karpenter-nodepools` ApplicationSet (not per-addon deployment)

#### 2. NodePool Configuration
**Location:** `configuration/karpenter-nodepools-config.yaml`

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
| `nodeClassRef: default` | Uses EKS Auto-Mode managed NodeClass |
| `expireAfter: 336h` | Nodes expire after 14 days for security patching |
| `consolidationPolicy` | Automatically right-sizes infrastructure based on usage |
| Resource limits | Prevents unbounded infrastructure scaling |

#### 3. Addon Integration Files
**Location:** `configuration/addons-global-values/nodepools-config-values/`

Pre-configured override files that automatically target infrastructure nodes when `eksAutoMode: true`.

**Integrated Addons:**

```
nodepools-config-values/
├── datadog-nodepool-config.yaml          # Datadog agents + cluster agent
├── external-secrets-nodepool-config.yaml # ESO controller
├── istiod-nodepool-config.yaml           # Istio control plane
└── istio-cni-nodepool-config.yaml        # Istio CNI DaemonSet
```

**Example Integration (Datadog):**
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

#### 4. Automatic Integration Logic
**Location:** `bootstrap/templates/addons-appset.yaml:119`

```yaml
helm:
  valueFiles:
  - '$values/configuration/addons-global-values/{{ $appset.appName }}.yaml'
  # If eksAutoMode is "true", load EKS Auto Mode overrides (tolerations, etc.)
  - '{{`{{if eq (index .metadata.labels "eksAutoMode") "true"}}$values/configuration/addons-global-values/nodepools-config-values/`}}{{ $appset.appName }}{{`-nodepool-config.yaml{{else}}$values/configuration/.skip/no-karpenter-overrides{{end}}`}}'
  ignoreMissingValueFiles: true
```

**How It Works:**
1. When a cluster secret has label `eksAutoMode: "true"`
2. AND an addon (e.g., `datadog: enabled`) is enabled
3. The system automatically merges the nodepool-config values file
4. Result: Addon pods run on infrastructure nodes with proper tolerations

#### 5. NodePool Deployment ApplicationSet
**Location:** `bootstrap/templates/karpenter-nodepools-appset.yaml`

A dedicated ApplicationSet deploys NodePool resources:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: karpenter-nodepools
spec:
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
      annotations:
        argocd.argoproj.io/sync-wave: "-2"  # Before all addons
```

**Key Points:**
- One NodePool Application per cluster (not per addon)
- Uses `matchExpressions` on `eksAutoMode` **label** (cluster generator requirement)
- Sync-wave `-2` ensures deployment before all addons

## Enablement Guide

### 1. Enable Infrastructure Node Pool

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

**Enable addons in cluster-addons.yaml:**
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

### 2. Verify ApplicationSet Created NodePool Application

```bash
# Check NodePool Application created
kubectl get application karpenter-nodepools-my-eks-auto-mode-cluster -n argocd

# Check Application sync status
kubectl get application karpenter-nodepools-my-eks-auto-mode-cluster -n argocd -o jsonpath='{.status.sync.status}'
```

### 3. Verify Node Pool Deployed to Remote Cluster

```bash
# Check NodePool status on remote cluster
kubectl get nodepool --context my-eks-auto-mode-cluster

# Expected output:
NAME                       NODECLASS   NODES   READY
infra-karpenter-nodepool   default     2       True
```

### 4. Verify Node Provisioning

```bash
# Check infrastructure nodes on remote cluster
kubectl get nodes -l node-type=infrastructure --context my-eks-auto-mode-cluster

# Verify node taints
kubectl get nodes -l node-type=infrastructure --context my-eks-auto-mode-cluster \
  -o jsonpath='{.items[*].spec.taints}'
```

### 5. Verify Pod Scheduling

```bash
# Check that infrastructure pods are scheduled on infrastructure nodes
kubectl get pods -n datadog -o wide --context my-eks-auto-mode-cluster
kubectl get pods -n external-secrets -o wide --context my-eks-auto-mode-cluster
kubectl get pods -n istio-system -o wide --context my-eks-auto-mode-cluster
```

## Technical Specifications

### Node Labels
Infrastructure nodes are labeled for targeting:
```yaml
node-type: infrastructure
```

### Node Taints
Infrastructure nodes are tainted to prevent non-infrastructure workloads:
```yaml
- key: infrastructure
  effect: NoSchedule
  value: "true"
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

### Resource Limits

Prevents unbounded infrastructure scaling:
```yaml
limits:
  cpu: "1000"      # Maximum 1000 cores total
  memory: 1000Gi   # Maximum 1000Gi memory total
  nodes: 10        # Maximum 10 nodes
```

## Lifecycle Management

### Node Expiration
- **Expiration:** 14 days (336 hours)
- **Purpose:** Ensures nodes are regularly refreshed for security patches
- **Termination Grace Period:** 48 hours for graceful workload migration

### Node Consolidation
- **Policy:** WhenEmptyOrUnderutilized
- **Check Interval:** 30 seconds
- **Behavior:** Proactively consolidates underutilized nodes to reduce costs

## EKS Auto-Mode Integration

### NodeClass Reference

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

### Auto-Mode Requirements

**Supported:**
- ✅ Clusters with EKS Auto-Mode enabled
- ✅ Karpenter v1 (included in Auto-Mode)
- ✅ AWS-managed NodeClass

**Not Supported:**
- ❌ Clusters without Auto-Mode enabled
- ❌ Manual EC2NodeClass configuration (use default managed NodeClass)

## Monitoring and Troubleshooting

### Check NodePool Status
```bash
kubectl get nodepool infra-karpenter-nodepool -o yaml
```

### Verify Node Provisioning
```bash
# List infrastructure nodes
kubectl get nodes -l node-type=infrastructure

# Check node capacity and allocations
kubectl describe node <node-name>
```

### Review Pod Scheduling
```bash
# Check pods with infrastructure tolerations
kubectl get pods -A -o json | jq '.items[] | select(.spec.tolerations[]? | select(.key=="infrastructure"))'

# Check pending pods that might need infrastructure tolerations
kubectl get pods -A --field-selector status.phase=Pending
```

### Monitor Costs
```bash
# Check node count and sizes
kubectl get nodes -l node-type=infrastructure -o custom-columns=NAME:.metadata.name,INSTANCE-TYPE:.metadata.labels.node\\.kubernetes\\.io/instance-type,AGE:.metadata.creationTimestamp
```

### Debug Consolidation
```bash
# Check Karpenter logs for consolidation decisions
kubectl logs -n karpenter -l app.kubernetes.io/name=karpenter --tail=100 | grep consolidation
```

## Adding New Infrastructure Components

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

### 2. Enable on Cluster

Ensure cluster has `eksAutoMode: "true"` in AWS Secrets Manager secret:

```json
{
  "eksAutoMode": "true"
}
```

Enable addon in cluster-addons.yaml:

```yaml
# configuration/cluster-addons.yaml
clusters:
  - name: my-cluster
    labels:
      my-addon: enabled  # Will automatically use infra nodes
```

### 3. Verify Integration

The ApplicationSet will automatically include the nodepool-config file when:
- Cluster secret has `eksAutoMode: "true"` label
- Addon is enabled for the cluster

## Best Practices

### ✅ DO

- **Always define resource requests and limits** for infrastructure components
- Use `topologySpreadConstraints` for HA requirements
- Test in dev cluster before production rollout
- Monitor node consolidation to ensure cost savings
- Set appropriate node expiration times (14 days recommended)
- Use the default managed NodeClass for EKS Auto-Mode

### ❌ DON'T

- Don't deploy infrastructure components without resource requests/limits
- Don't rely solely on nodeSelector for HA (use topologySpreadConstraints)
- Don't manually manage EC2NodeClass in Auto-Mode clusters
- Don't set resource limits too low (causes pod evictions)
- Don't disable consolidation without good reason (increases costs)
- Don't use infra-karpenter on non-Auto-Mode clusters

## Limitations and Constraints

### Cluster Requirements
- **Required:** EKS Auto-Mode enabled
- **Required:** Karpenter v1 (automatically included in Auto-Mode)
- **Not supported:** Clusters without Auto-Mode

### Scope
- **In scope:** Infrastructure components (operators, agents, controllers)
- **Out of scope:** Business logic applications
- **Application node pools:** Managed by product teams, not platform team

### Resource Limits
- Maximum 10 infrastructure nodes per NodePool
- Maximum 1000 CPU cores total
- Maximum 1000Gi memory total
- Limits are configurable per cluster if needed

## Related Documentation

- [KARPENTER-NODEPOOLS-DEPLOYMENT.md](KARPENTER-NODEPOOLS-DEPLOYMENT.md) - Detailed NodePool deployment architecture
- [ARCHITECTURE.md](ARCHITECTURE.md) - Overall bootstrap architecture
- [BOOTSTRAP-FLOW.md](BOOTSTRAP-FLOW.md) - Bootstrap process and sync waves
- [VALUES_GUIDE.md](VALUES_GUIDE.md) - How to configure addon values

## Support and Contact

**For issues with infrastructure node separation:**
1. Check NodePool status: `kubectl get nodepool`
2. Verify node provisioning: `kubectl get nodes -l node-type=infrastructure`
3. Review pod scheduling: Check if infrastructure pods have correct tolerations
4. Monitor costs: Ensure consolidation is working properly
5. Contact DevOps Team: For configuration assistance or issues

**Maintainer:** itay.uliel@msd.com
**Last Updated:** January 2026

# Karpenter NodePools Deployment Architecture

## Overview

This document describes how Karpenter NodePools are deployed to EKS Auto Mode clusters using a dedicated ApplicationSet.

## Problem

Previously, NodePools were deployed as a third source in each addon Application:
- **Issue 1**: NodePool deployed once per addon per cluster (many duplicate Applications)
- **Issue 2**: No guarantee NodePool exists before addons deploy
- **Issue 3**: Violated principle of "one NodePool per cluster"

## Solution

Created a dedicated `karpenter-nodepools` ApplicationSet that:
1. Deploys **one** NodePool Application per cluster (not per addon)
2. Uses sync-wave `-2` to ensure NodePools deploy **before all addons**
3. Filters clusters using `eksAutoMode` label

## Architecture

### Flow

```
1. Cluster secret created (via ESO) with eksAutoMode label
   ↓
2. karpenter-nodepools ApplicationSet detects cluster
   ↓
3. Creates Application: karpenter-nodepools-<cluster-name>
   ↓
4. Application syncs with sync-wave -2 (before all addons)
   ↓
5. NodePool resources deployed to cluster
   ↓
6. Addon Applications deploy (can use infrastructure nodes)
```

### Sync Wave Order

```
-2: NodePool Applications      (from karpenter-nodepools ApplicationSet)
-1: istio-base Applications    (from addons ApplicationSet)
 0: istio-cni Applications     (from addons ApplicationSet)
 1: istiod Applications        (from addons ApplicationSet)
 2: istio-ingress Applications (from addons ApplicationSet)
default (0): All other addons  (datadog, etc.)
```

## Files

### ApplicationSet

**File**: `bootstrap/templates/karpenter-nodepools-appset.yaml`

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
    spec:
      project: karpenter-nodepools
      sources:
        - path: charts/karpenter-nodepools
          helm:
            valueFiles:
              - $values/configuration/karpenter-nodepools-config.yaml
        - ref: values
      destination:
        name: '{{.name}}'
        namespace: karpenter
```

**Key Points:**
- Uses cluster generator with `matchLabels` on `eksAutoMode` label
- `eksAutoMode` must be a **label** (cluster generator requirement)
- Sync-wave `-2` ensures deployment before all addons

### NodePool Chart

**Location**: `charts/karpenter-nodepools/`

Contains Helm chart with:
- `Chart.yaml` - Chart metadata
- `templates/nodepool.yaml` - NodePool CRD template

The template uses `{{- range .Values.nodepools }}` to support multiple NodePool definitions.

### NodePool Configuration

**File**: `configuration/karpenter-nodepools-config.yaml`

Defines the infrastructure NodePool:

```yaml
nodepools:
  - name: infra-karpenter-nodepool
    template:
      metadata:
        labels:
          node-type: infrastructure
      spec:
        nodeClassRef:
          group: eks.amazonaws.com
          kind: NodeClass
          name: default
        taints:
          - key: infrastructure
            effect: NoSchedule
            value: "true"
        requirements:
          - key: node-type
            operator: In
            values: ["infrastructure"]
        expireAfter: 336h  # 14 days
        terminationGracePeriod: 48h
    disruption:
      consolidationPolicy: WhenEmptyOrUnderutilized
      consolidateAfter: 30s
    limits:
      cpu: "1000"
      memory: 1000Gi
      nodes: 10
```

### Addon NodePool Configuration Overrides

**Location**: `configuration/addons-global-values/nodepools-config-values/`

Contains per-addon NodePool configuration overrides (nodeSelector, tolerations) for EKS Auto Mode:

- `datadog-nodepool-config.yaml` - Datadog agent nodeSelector/tolerations
- `istio-cni-nodepool-config.yaml` - Istio CNI nodeSelector/tolerations
- `istiod-nodepool-config.yaml` - Istiod nodeSelector/tolerations
- `external-secrets-nodepool-config.yaml` - ESO nodeSelector/tolerations

These are loaded by addon Applications when `eksAutoMode: "true"` label exists on cluster secret.

## Cluster Secret Configuration

### ESO Template

**File**: `charts/clusters/templates/cluster-external-secret.yaml`

The ExternalSecret template adds `eksAutoMode` as a label:

```yaml
target:
  template:
    metadata:
      labels:
        argocd.argoproj.io/secret-type: cluster
        eksAutoMode: '{{ .eksAutoMode }}'  # Label for ApplicationSet generator and addon valueFiles conditional
```

**Why both?**
- **Label**: Required for ApplicationSet cluster generator `matchExpressions`
- **Annotation**: Used by addon Applications for conditional valueFiles loading

### AWS Secrets Manager

The cluster secret in AWS Secrets Manager must include:

```json
{
  "name": "cluster-name",
  "host": "https://...",
  "clusterName": "cluster-name",
  "accountId": "123456789012",
  "region": "eu-west-1",
  "caData": "...",
  "eksAutoMode": "true"   # ← Required for NodePool deployment
}
```

## Enabling NodePools for a Cluster

1. **Add `eksAutoMode` to AWS Secrets Manager secret**:
   ```json
   {
     "eksAutoMode": "true"
   }
   ```

2. **Wait for ESO to reconcile** (or force sync):
   ```bash
   kubectl annotate externalsecret <cluster-name> -n argocd \
     force-sync=$(date +%s) --overwrite
   ```

3. **Verify label exists**:
   ```bash
   kubectl get secret <cluster-name> -n argocd \
     -o jsonpath='{.metadata.labels.eksAutoMode}'
   ```

4. **Check Application created**:
   ```bash
   kubectl get application karpenter-nodepools-<cluster-name> -n argocd
   ```

5. **Verify NodePool deployed**:
   ```bash
   kubectl get nodepool -n karpenter --context <cluster-name>
   ```

## Addons Integration

Addons automatically detect `eksAutoMode` label and load NodePool-specific overrides:

**File**: `bootstrap/templates/addons-appset.yaml`

```yaml
helm:
  valueFiles:
    - '$values/configuration/addons-global-values/{{ $appset.appName }}.yaml'
    - '{{if eq (index .metadata.labels "eksAutoMode") "true"}}$values/configuration/addons-global-values/nodepools-config-values/{{ $appset.appName }}-nodepool-config.yaml{{else}}$values/configuration/.skip/no-karpenter-overrides{{end}}'
```

**Example**: Datadog addon loads `datadog-nodepool-config.yaml` which adds:
```yaml
agents:
  nodeSelector:
    node-type: infrastructure
  tolerations:
    - key: infrastructure
      operator: Equal
      value: "true"
      effect: NoSchedule
```

This ensures Datadog agents schedule on infrastructure nodes.

## Troubleshooting

### ApplicationSet not creating Applications

**Check cluster secret has label**:
```bash
kubectl get secret <cluster-name> -n argocd -o jsonpath='{.metadata.labels}' | jq .
```

Should include: `"eksAutoMode": "true"`

**If missing**, force ESO to reconcile:
```bash
kubectl annotate externalsecret <cluster-name> -n argocd \
  force-sync=$(date +%s) --overwrite
```

### Application stuck in Unknown/OutOfSync

**Check AppProject exists**:
```bash
kubectl get appproject karpenter-nodepools -n argocd
```

**Check source repository access**:
```bash
kubectl get application karpenter-nodepools-<cluster-name> -n argocd -o yaml
```

### NodePool not deployed to cluster

**Check Application sync status**:
```bash
kubectl get application karpenter-nodepools-<cluster-name> -n argocd
```

**Check sync wave**:
```bash
kubectl get application karpenter-nodepools-<cluster-name> -n argocd \
  -o jsonpath='{.metadata.annotations.argocd\.argoproj\.io/sync-wave}'
```

Should return: `-2`

**Manually sync**:
```bash
argocd app sync karpenter-nodepools-<cluster-name>
```

## Migration from Old Architecture

### Before (Source 3 in addons ApplicationSet)

- NodePool deployed as third source in each addon Application
- Created once per addon per cluster (many Applications)
- No sync wave guarantee

### After (Dedicated ApplicationSet)

- NodePool deployed by separate ApplicationSet
- One Application per cluster
- Sync wave `-2` ensures deployment before addons

### Migration Steps

1. ✅ Created `karpenter-nodepools` ApplicationSet
2. ✅ Removed Source 3 from `addons-appset.yaml`
3. ✅ Added `eksAutoMode` as label in cluster ExternalSecret
4. ✅ Renamed `infra-karpenter` → `karpenter-nodepools`
5. ✅ Renamed `infra-karpenter-values` → `nodepools-config-values`
6. ⚠️ **Manual cleanup**: Delete old `infra-karpenter-*` Applications:
   ```bash
   kubectl delete application infra-karpenter-<cluster-name> -n argocd
   ```

## Benefits

1. **Fewer Applications**: One NodePool app per cluster (not per addon)
2. **Guaranteed Order**: Sync-wave ensures NodePools before addons
3. **Clean Separation**: Infrastructure (NodePools) vs Workloads (addons)
4. **Better Performance**: Fewer ArgoCD Application objects to reconcile
5. **Clearer Intent**: Dedicated ApplicationSet for infrastructure provisioning

## Related Documentation

- [VALUES_GUIDE.md](VALUES_GUIDE.md) - How to configure addon values
- [ARCHITECTURE.md](ARCHITECTURE.md) - Overall system architecture
- [BOOTSTRAP-FLOW.md](BOOTSTRAP-FLOW.md) - Bootstrap deployment flow

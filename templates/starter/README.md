# Addons Repository

This repository is managed by [Sharko](https://github.com/MoranWeissman/sharko) — an addon management server for Kubernetes clusters, built on ArgoCD.

## Structure

- `bootstrap/` — ArgoCD root application and ApplicationSet templates
- `configuration/addons-catalog.yaml` — Available addons and their chart versions
- `configuration/cluster-addons.yaml` — Cluster registry with addon labels
- `configuration/addons-clusters-values/` — Per-cluster Helm value overrides
- `configuration/addons-global-values/` — Default Helm values per addon
- `charts/` — Custom local Helm charts (if any)

## How It Works

1. Addons are defined in `addons-catalog.yaml`
2. Clusters are registered with addon labels in `cluster-addons.yaml`
3. ArgoCD ApplicationSets deploy the matching addons to each cluster
4. Per-cluster overrides customize addon behavior

## Management

Use the Sharko CLI or API to manage clusters and addons:

```bash
sharko add-cluster prod-eu --addons cert-manager,metrics-server
sharko add-addon external-dns --chart external-dns --repo https://kubernetes-sigs.github.io/external-dns --version 1.14.4
sharko status
```

## ArgoCD Resource Exclusions

Sharko manages Kubernetes Secrets on remote clusters (for addon credentials). To prevent ArgoCD from tracking or pruning these Sharko-managed secrets, add the following exclusion to the `argocd-cm` ConfigMap in your ArgoCD namespace:

```yaml
# Add to argocd-cm ConfigMap:
resource.exclusions: |
  - apiGroups:
    - ""
    kinds:
    - Secret
    clusters:
    - "*"
    labelSelectors:
    - app.kubernetes.io/managed-by=sharko
```

Apply with:

```bash
kubectl -n argocd patch configmap argocd-cm --patch '
data:
  resource.exclusions: |
    - apiGroups:
      - ""
      kinds:
      - Secret
      clusters:
      - "*"
      labelSelectors:
      - app.kubernetes.io/managed-by=sharko
'
```

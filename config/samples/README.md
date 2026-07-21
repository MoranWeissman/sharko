# Samples Directory

Example Custom Resource YAML files for testing the Sharko operator.

## Phase 0 (current)

ALPHA samples only — CRDs are not defined yet, so these cannot be applied.

## Phase 1+

After `make install` (applies CRDs to the cluster), you can `kubectl apply` these samples to test the operator:

```bash
# Apply the prod-eu ClusterAddons sample
kubectl apply -f config/samples/clusteraddons_sample.yaml

# Apply the prometheus-stack ClusterAddonSet sample
kubectl apply -f config/samples/clusteraddonset_sample.yaml

# Watch the reconciler create ArgoCD ApplicationSets
kubectl get applicationsets -n argocd -w

# Check reconciler logs
kubectl logs -n sharko -l app.kubernetes.io/name=sharko -f
```

## Customizing Samples

Replace placeholder cluster names (`prod-eu`, `staging-us`) with your real cluster names (must match entries in `managed-clusters.yaml`). Replace placeholder secrets (`${GRAFANA_ADMIN_PASSWORD_PROD}`) with real secret references (see `docs/site/user-guide/secrets-provider.md`).

## Content Policy

All samples use placeholder cluster names and placeholder secret names ONLY. No real organization names, internal domains, employee emails, or AWS account IDs.

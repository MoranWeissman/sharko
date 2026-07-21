# Manager Directory

Deployment manifest for the Sharko operator controller pod (runs the reconciler loop).

## Phase 0 (current)

Empty. The Sharko Deployment lives in `charts/sharko/templates/deployment.yaml` until the operator mode is fully wired.

## Phase 1+

Populated manually or by `make manifests` (if controller-gen is configured to emit a manager Deployment, though typically this is hand-written). Expected files:

- `manager.yaml` — Deployment spec for the controller pod (image, env vars, RBAC ServiceAccount, resource limits)

The manager Deployment runs the same `sharko` binary as the HTTP-only mode, but with an `--operator` flag (or detected automatically if CRDs are installed). The reconciler watches `ClusterAddons` and `ClusterAddonSet` CRs and reconciles ArgoCD ApplicationSets.

## Applying the Manager

```bash
# Apply to the cluster (also applies RBAC)
make deploy

# Remove from the cluster
make undeploy
```

In production, the Helm chart in `charts/sharko/` is the source of truth for the Deployment. The `config/manager/` manifests are for local operator-mode development only (see `make operator-dev-up`).

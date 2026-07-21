# Sharko Operator Config

This directory follows the standard kubebuilder layout for Kubernetes operator artifacts.

## Directory Structure

- **`crd/`** — Custom Resource Definitions (CRDs) generated from Go types with `+kubebuilder` markers via `make manifests`. Applied to the cluster with `make install`. CRDs define the schema for `ClusterAddons` and `ClusterAddonSet` resources.

- **`rbac/`** — RBAC manifests (ClusterRole, ServiceAccount, RoleBinding) generated from `+kubebuilder:rbac` markers in the Go controller code. Applied with `make deploy`. Grants the Sharko controller pod the permissions it needs to watch and reconcile CRDs + manage ArgoCD ApplicationSets.

- **`manager/`** — Deployment manifest for the Sharko controller pod (runs the reconciler loop). Applied with `make deploy`. In Phase 0 this is a scaffold only — the actual Deployment lives in `charts/sharko/templates/deployment.yaml` until the operator mode is fully wired.

- **`samples/`** — Example CR YAML files for `ClusterAddons` and `ClusterAddonSet`. Use these as templates for testing the operator in Phase 1+. Each sample is annotated with placeholder cluster names (`prod-eu`, `staging-us`) — replace with your real cluster names.

## Workflow

1. **Phase 0 (current):** Scaffold only. `make install` / `make deploy` / `make manifests` are no-ops today (exit 0 with a note).

2. **Phase 1:** CRD Go types + controller code land in `internal/`. Run `make manifests` to generate CRD YAML from Go markers and populate `config/crd/`. Run `make install` to apply CRDs to the cluster.

3. **Phase 2+:** Controller reconciler is wired. Run `make deploy` to apply RBAC + manager Deployment. The Sharko pod switches from HTTP-only mode to operator mode (watches CRs, reconciles ApplicationSets).

## Integration with Helm Chart

The Helm chart in `charts/sharko/` carries a top-level `crds/` directory (Helm's standard CRD install location). During Phase 1, `make manifests` output is copied into `charts/sharko/crds/` so a single `helm install` installs both the CRDs and the Sharko deployment. Helm applies CRDs once before any templates (never upgraded by `helm upgrade` — see `charts/sharko/crds/README.md`).

## Local Development Loop

See `docs/site/developer-guide/operator-development.md` (or run `make help` and look for `operator-dev-*` targets).

Quick start:
```bash
# Provision a persistent dev cluster + install Sharko
make operator-dev-up

# After code changes: rebuild image + re-deploy
make operator-dev-down && make operator-dev-up

# Generate CRD YAML from Go markers (Phase 1+)
make manifests

# Apply CRDs to the cluster
make install

# Apply controller RBAC + Deployment
make deploy

# Tear down the dev cluster
make operator-dev-down
```

## Content Policy

All sample manifests use placeholder cluster names (`prod-eu`, `staging-us`) ONLY. No real organization names, internal domains, employee emails, or AWS account IDs.

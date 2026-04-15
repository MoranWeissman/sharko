# Sharko Addons Repository

This repository is managed by [Sharko](https://github.com/MoranWeissman/sharko).

## Structure

- `bootstrap/` — ArgoCD root application and ApplicationSet templates
- `configuration/addons-catalog.yaml` — Addon definitions (single source of truth)
- `configuration/managed-clusters.yaml` — Cluster registrations with addon labels
- `configuration/addons-clusters-values/` — Per-cluster Helm values
- `configuration/addons-global-values/` — Per-addon global Helm values

## How It Works

Sharko manages this repository via pull requests. The `addons-catalog.yaml` file
defines all available addons. ArgoCD ApplicationSets read from this file to
deploy addons to clusters based on their labels in `managed-clusters.yaml`.

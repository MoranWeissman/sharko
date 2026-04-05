# Example Helper Templates

These are optional Helm template helpers for specific addon configurations.
They are NOT included in the main Sharko ApplicationSet template.

## How to use

1. Copy the helper file you need to your bootstrap chart's `templates/` directory
2. Reference the helper in your per-addon global values or per-cluster values

## Files

- `_datadog-helpers.tpl` — containerIncludeLogs namespace computation, operator CRD companion source
- `_eso-helpers.tpl` — IRSA role annotation injection, ClusterSecretStore values

## When you need these

Most addons work out of the box with the generic template. These helpers are for
complex addons that need cross-addon data or cloud-specific injection:

- **Datadog**: needs to know all addon namespaces for container log inclusion
- **External Secrets Operator**: needs AWS IRSA role ARN injected per-cluster

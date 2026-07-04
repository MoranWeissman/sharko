# If You Remove Sharko

> **Reference page, not a runbook.** This page answers one question
> honestly: what actually happens to your clusters if you delete the
> Sharko deployment. Short version — nothing stops, two background
> services stop running, and the exit path is standard ArgoCD.

Every tool that sits between you and your clusters owes you a straight
answer to "what happens when I turn it off?" Here is Sharko's.

## Nothing stops when Sharko stops

Everything ArgoCD deploys is rendered from **your** Git repository, not
from Sharko. The root Application and AppProject, the ApplicationSets,
your addons catalog, and your per-cluster values files all live in the
repo Sharko initialized — the root app
(`templates/bootstrap/root-app.yaml` in the Sharko source) sources
`$values/configuration/bootstrap-config.yaml`,
`addons-catalog.yaml`, and `managed-clusters.yaml` straight from that
repo. Which addons run on which cluster is decided by labels on
ArgoCD's cluster secrets.

Sharko has no database of its own. Delete the Sharko deployment and:

- Every running addon keeps running.
- ArgoCD keeps syncing every Application from your repo, exactly as
  before.
- Nothing is uninstalled, degraded, or orphaned at the moment Sharko
  goes away.

## What degrades over time

Two background services stop, and their absence shows up gradually,
not immediately:

1. **Addon-secret delivery and rotation.** Sharko's reconciler
   delivers addon credentials from your secrets provider to managed
   clusters on a timer. Secrets that were already delivered stay in
   place, but the next rotation or change in your secret store is no
   longer picked up and delivered. If you use External Secrets
   Operator instead of Sharko's delivery, this doesn't apply to you.
2. **The `managed-clusters.yaml` → cluster-secret label sync, and
   cluster-credential rotation.** The
   [cluster reconciler](cluster-reconciler.md) keeps the labels on
   ArgoCD's cluster secrets in step with `managed-clusters.yaml`, and
   refreshes rotated cluster credentials. Current labels stay as they
   are — deployments are unaffected — but editing the YAML no longer
   changes anything, and rotated cluster credentials are no longer
   refreshed.

You also lose the management surface itself: the UI, the REST API, the
audit log, the upgrade advisor, and PR authoring. Your clusters don't
notice; the humans who used those do.

## The exit is standard ArgoCD, not a migration

To keep operating without Sharko, you go back to what you did before
it: hand-edit the labels on ArgoCD's cluster secrets to control which
addons run where, and manage the catalog and values files in the repo
directly. There is no export step, no data to convert, no proprietary
state to unwind — your repo remains a fully self-describing ArgoCD
setup, because that's what it was all along.

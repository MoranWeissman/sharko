# If You Remove Sharko

> **Reference page, not a runbook.** This page answers one question
> honestly: what actually happens to your clusters if you delete the
> Sharko deployment. Short version — nothing stops, two background
> services stop running, and the exit path is standard ArgoCD.

Every tool that sits between you and your clusters owes you a straight
answer to "what happens when I turn it off?" Here is Sharko's.

## Nothing stops when Sharko stops

Everything ArgoCD deploys is rendered from **your** Git repository, not
from Sharko's own storage. One ArgoCD Application — the engine pin,
`sharko-engine.yaml` at the root of your repo — points at Sharko's engine
chart, published once and versioned by that single file; the chart
itself renders the AppProject and one ApplicationSet per addon in your
catalog. Those ApplicationSets read straight from your repo:
`catalog.yaml` (the addons your org approved), `managed-clusters.yaml`
(which clusters, and how to reach them), `cluster-addons/<cluster>.yaml`
(which of those addons run on that cluster, and at what version), and
the `values/` tree (Helm values, fleet-wide and per-cluster). Which
addons run where is decided by two things kept in step with each other:
the `cluster-addons/<cluster>.yaml` file, and the matching label the
reconciler keeps on ArgoCD's own cluster secret.

Sharko has no database of its own. Delete the Sharko deployment and:

- Every running addon keeps running.
- ArgoCD keeps syncing every Application from your repo, exactly as
  before.
- Nothing is uninstalled, degraded, or orphaned at the moment Sharko
  goes away.

> **Re-verified 2026-08-08** on `main@45503903`, including a git change
> applied while Sharko was down — the claim above isn't just a design
> intent, it's been tested against a real repo.

## What degrades over time

Two background services stop, and their absence shows up gradually,
not immediately:

1. **Addon-secret delivery and rotation.** Sharko's reconciler
   delivers addon credentials from your secrets provider to managed
   clusters on a timer. Secrets that were already delivered stay in
   place, but the next rotation or change in your secret store is no
   longer picked up and delivered. If you use External Secrets
   Operator instead of Sharko's delivery, this doesn't apply to you.
2. **Two label syncs, and cluster-credential rotation.** The
   [cluster reconciler](cluster-reconciler.md) keeps two things in step
   with git on a timer: which addons are switched on for a cluster (a
   label read from `cluster-addons/<cluster>.yaml`) and the cluster's own
   connection record (read from `managed-clusters.yaml`) — and it
   refreshes rotated cluster credentials. Whatever labels and connection
   details are live at the moment Sharko stops stay exactly as they are
   — deployments are unaffected — but editing either file no longer
   changes anything, and rotated cluster credentials are no longer
   picked up.

You also lose the management surface itself: the UI, the REST API, the
audit log, the upgrade advisor, and PR authoring. Your clusters don't
notice; the humans who used those do.

## The exit is standard ArgoCD, not a migration

To keep operating without Sharko, you go back to what you did before
it: hand-edit the addon-enablement labels on ArgoCD's cluster secrets to
control which addons run where, and manage the catalog and values files
in the repo directly. There is no export step, no data to convert, no
proprietary state to unwind — your repo remains a fully self-describing
ArgoCD setup, because that's what it was all along.

## Don't want Sharko owning connections in the first place?

Removal is the all-or-nothing version of a stance you can take
per-cluster while Sharko is running:
[Managing cluster connections yourself](self-managed-connections.md)
lets you keep the ArgoCD cluster secret in your own hands — Sharko never
writes, rotates, or deletes it and only syncs the addon labels onto it.
Clusters adopted from an existing ArgoCD get that mode by default.

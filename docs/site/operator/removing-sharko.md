# If You Remove Sharko

> **Reference page, not a runbook.** This page answers one question: what
> happens to your clusters if you delete the Sharko deployment? Short
> answer: nothing stops, two background jobs stop running, and you carry
> on with plain ArgoCD.

## Nothing stops when Sharko stops

Everything ArgoCD deploys comes from **your** Git repo, not from Sharko's
own storage. These are the files ArgoCD reads:

- **`sharko-engine.yaml`**, at the root of the repo. This is the engine
  pin: one ArgoCD Application that points at a fixed version of Sharko's
  engine chart. The engine chart creates the AppProject and one
  ApplicationSet per addon in your catalog.
- **`catalog.yaml`**: the addons your team approved.
- **`managed-clusters.yaml`**: which clusters there are, and how to reach
  them.
- **`cluster-addons/<cluster>.yaml`**: which addons run on that cluster,
  and at what version.
- **the `values/` folder**: Helm values, for the whole fleet and per
  cluster.

The ApplicationSets read these files straight from your repo.

Which addons run where depends on two things that are kept in step: the
`cluster-addons/<cluster>.yaml` file, and a matching label on ArgoCD's own
cluster secret. The reconciler copies the first into the second. (The
reconciler is the part of Sharko that runs in the background and keeps
ArgoCD's cluster secrets in line with your repo.)

Sharko has no database of its own. Delete the Sharko deployment and:

- Every running addon keeps running.
- ArgoCD keeps syncing every Application from your repo, as before.
- Nothing is uninstalled, broken or left without an owner at the moment
  Sharko goes away.

This was tested on a real repo, including a Git change made while Sharko
was down.

## What stops working over time

Two background jobs stop. You notice it slowly, not right away.

1. **Delivering and rotating addon secrets.** Sharko copies addon
   credentials from your secrets store to the managed clusters on a timer.
   Secrets already delivered stay where they are. But a new or changed
   value in your secrets store is no longer delivered.

    If you use External Secrets Operator for this instead of Sharko, this
    point does not apply to you.

2. **Keeping cluster labels and connections in step with Git.** The
   [cluster reconciler](cluster-reconciler.md) does three things on a
   timer:

    - it sets the label that says which addons are on for a cluster (from
      `cluster-addons/<cluster>.yaml`);
    - it keeps the cluster's connection record in line with
      `managed-clusters.yaml`;
    - it picks up cluster credentials that were rotated.

    When Sharko stops, the labels and connection details that are live at
    that moment stay as they are, and deployments carry on. But editing
    either file no longer changes anything, and rotated cluster
    credentials are no longer picked up.

You also lose Sharko itself: the UI, the REST API, Activity History, the
upgrade advice, and the pull requests Sharko opens for you. Your clusters
do not notice. The people who used those things do.

## Leaving is plain ArgoCD, not a migration

To keep going without Sharko, you go back to what you did before it:

- edit the addon labels on ArgoCD's cluster secrets by hand, to control
  which addons run where;
- edit the catalog and values files in your repo directly.

There is nothing to export, no data to convert, and no hidden state to
undo. Your repo is still a complete ArgoCD setup, because that is what it
was all along.

## Don't want Sharko owning connections in the first place?

Removing Sharko is the all-or-nothing version of a choice you can also
make for one cluster at a time, while Sharko is running.
[Managing cluster connections yourself](self-managed-connections.md)
lets you keep the ArgoCD cluster secret in your own hands. Sharko then
never writes, rotates or deletes it. It only keeps the addon labels on it
in step. Clusters adopted from an existing ArgoCD get that mode by
default.

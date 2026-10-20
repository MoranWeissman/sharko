# Upgrading and Rolling Back

This page shows how to move Sharko to a newer v4 release, how to go back
to the version you had, and what is kept or lost when the Sharko pod
restarts.

Sharko v4 is a [technical preview](../technical-preview.md). Only v4
releases are supported. If you still run an older line, do not upgrade it
in place. Install v4 fresh with the [installation guide](installation.md),
and then [migrate your repo](migrating-to-the-new-format.md).

## What you'll do

1. Read the release notes for the version you are moving to.
2. Upgrade the Sharko chart with `helm upgrade`.
3. If the new release ships a newer engine, move the engine pin in your
   repo.

What you need:

- The values file you installed with (in this guide, `sharko-values.yaml`).
- Helm 3, and access to the `sharko` namespace.
- The version number you want. The
  [releases page](https://github.com/MoranWeissman/sharko/releases) lists
  them, with notes for each one.

## Only one Sharko runs

Sharko runs as one pod. The chart ships `replicaCount: 1`. Leave it at 1.
Sign-ins live in that one pod's memory, so a second copy would sign
people out at random and do the same background work twice against the
same repo. See [the preview limits](../technical-preview.md).

This also keeps upgrades simple: there is one Sharko to upgrade, and you
upgrade it in one step. Never let an older Sharko read a repo that a
newer one has already written to.

## Upgrade Sharko

Pick the version, then run:

```bash
helm upgrade sharko oci://ghcr.io/moranweissman/sharko/sharko \
  --version 4.0.7 \
  -n sharko \
  -f sharko-values.yaml
```

Change `4.0.7` to the version you want. Always pass `-f` with your values
file. Without it, Helm goes back to the chart's defaults for anything you
had set.

What happens while it runs:

- The chart does not set its own update strategy, so Kubernetes uses its
  default. It starts the new pod first. When the new pod is ready, it
  stops the old one.
- The UI and API stay reachable, but everyone who was signed in has to
  sign in again. Sign-ins are kept in memory and do not move to the new
  pod.
- Your addons are not touched. ArgoCD keeps deploying them from your repo
  the whole time.

Check that the new version is running:

```bash
kubectl get pods -n sharko
kubectl exec -n sharko deploy/sharko -- \
  wget -qO- http://localhost:8080/api/v1/health
```

The health answer includes `"version"`. It should show the version you
just installed.

## Move the engine pin

The engine is the small Helm chart that ArgoCD runs from your repo. It
creates one ApplicationSet per addon in your catalog, and those
ApplicationSets deploy your addons. Your repo says which engine version to
use in one file at the repo root: `sharko-engine.yaml`. That file is an
ArgoCD Application. The engine version is the `targetRevision` of its
`sharko-engine` chart source.

Upgrading Sharko does not change that file. Each Sharko release ships
with a matching engine version. When your repo pins an older one, move
the pin with a pull request.

Check the pin, then open the pull request:

```bash
# Compare the pin in your repo with the engine this Sharko ships with.
curl -s -H "Authorization: Bearer $SHARKO_TOKEN" \
  "$SHARKO_URL/api/v1/engine/pin" | jq

# See the change without opening anything.
curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" \
  -H 'Content-Type: application/json' -d '{"dry_run": true}' \
  "$SHARKO_URL/api/v1/engine/pin/upgrade" | jq

# Open the pull request.
curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" \
  -H 'Content-Type: application/json' -d '{}' \
  "$SHARKO_URL/api/v1/engine/pin/upgrade" | jq
```

The pull request changes only the `targetRevision` line. Nothing changes
in any cluster until the pull request is merged (by you, or by Sharko if
you turned on auto-merge). You can also make the same one-line
change yourself, in a normal pull request on `sharko-engine.yaml`.

Leave the other source in that file alone. Its `targetRevision` is your
Git branch (for example `main`), not the engine version.

## Roll back

To go back to the Sharko version you had before, use Helm's own history:

```bash
# See the list of releases Helm remembers.
helm history sharko -n sharko

# Go back one step.
helm rollback sharko -n sharko

# Or go back to a given revision number from the list.
helm rollback sharko 3 -n sharko
```

Things to know before you roll back:

- If you also moved the engine pin, rolling back Sharko does not move it
  back. To undo it, revert that pull request in your repo.
- A newer Sharko can write a field into your repo files that an older
  Sharko does not know. When that happens, the older Sharko refuses to
  read the file and says why. It does not read the file as empty, and it
  does not delete anything. If you see this, upgrade again. See
  [Unrecognized Sharko apiVersion](#unrecognized-sharko-apiversion) below.
- As with an upgrade, everyone has to sign in again.

## What survives a restart

An upgrade, a rollback and a plain pod restart all start a new Sharko
pod. This is what the new pod keeps and what it loses.

Kept (stored in Kubernetes, in the `sharko` namespace):

| What | Where it is kept |
|---|---|
| Users and their roles | the `sharko-users` ConfigMap |
| Passwords (stored as hashes) | the `sharko` Secret |
| API tokens (stored as hashes) | the `sharko-api-tokens` Secret |
| Git and ArgoCD connections, including your Git token (encrypted) | the `sharko-connections` Secret |
| Notifications you have cleared | the `sharko-notifications` ConfigMap |
| Pull requests Sharko is still waiting on | the `sharko-pending-prs` ConfigMap |
| Your clusters, catalog, addons and values | your Git repo |

Lost:

- **Sign-ins.** Everyone signs in again.
- **Activity History.** It is kept in memory only, and holds the last
  1000 entries. A restart starts it empty. See
  [Activity History](audit-log.md).

If you turned on `persistence.enabled`, anything Sharko keeps on disk
under `/app/data` is also kept. It is off by default.

## Error messages you may meet after an upgrade

Two checks protect your clusters when a repo file and the Sharko version
reading it do not match. Both are working as intended when you see them.

### Unrecognized Sharko apiVersion

Every Sharko repo file starts with an `apiVersion` line. Today that is
`sharko.dev/v1`. Sharko still reads the older `sharko.io/v1`.

If a file has a `sharko.*` apiVersion this Sharko does not know (for
example one written by a newer release), Sharko stops reading it and logs:

```
unrecognized Sharko apiVersion "sharko.dev/v2": this file was written by a
newer or unknown Sharko — refusing to guess how to read it ...
```

Sharko never treats such a file as empty. What to do: upgrade Sharko to a
version that knows the file. Do not edit the `apiVersion` by hand to hide
the error, unless you know the rest of the file has the right shape.

Files with no `apiVersion`, and files that are not Sharko's, are read as
before.

### Orphan sweep held

The cluster reconciler is the part of Sharko that runs in the background
and keeps ArgoCD's cluster secrets in line with your repo. Part of its job
is the orphan sweep: it deletes the ArgoCD cluster secret of a cluster
that was removed from `managed-clusters.yaml`.

The sweep refuses to run when all three of these are true at once:

1. `managed-clusters.yaml` reads as **zero** clusters,
2. the file is in Git and is **not empty**, and
3. at least one ArgoCD cluster secret that Sharko manages still exists.

That mix usually means the file was misread, not that you removed every
cluster. So Sharko deletes nothing. It skips the sweep, logs an error,
and adds an `orphan_sweep_held` entry to
[Activity History](audit-log.md). It checks again on every pass, so the
hold clears by itself once the files agree again.

What to do when you see `orphan_sweep_held`:

1. Open `managed-clusters.yaml` on the branch Sharko reads (by default
   `main`). Check its `apiVersion` and its contents. A file written by a
   different Sharko version is the most likely cause.
2. If the file is wrong, find what wrote it. Usually an older Sharko was
   run against the repo after a newer one. Upgrade it, or stop it.
3. If you really want zero clusters, remove the clusters through Sharko
   (that deletes their secrets as part of the removal), or delete the
   leftover secrets by hand. Once no Sharko-managed cluster secret is
   left, the check stops holding the sweep.

A fresh install is not affected. A missing `managed-clusters.yaml`, or an
empty one, never holds the sweep.

## Related pages

- [Installation](installation.md) — the first install.
- [Migrating a repo to the new format](migrating-to-the-new-format.md) —
  moving an older repo to the v4 layout.
- [Failure Mode Index](failure-mode-index.md) — search for your error
  message.
- [Cluster Reconciler](cluster-reconciler.md) — how the sweep and the
  ownership label work.
- [Managing Cluster Connections Yourself](self-managed-connections.md) —
  connections you manage yourself are never touched by the sweep.

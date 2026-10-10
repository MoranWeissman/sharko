# Cluster Reconciler

ArgoCD reaches each cluster through a Kubernetes Secret in the `argocd`
namespace, called the ArgoCD cluster Secret. Sharko has a part called the
**cluster reconciler** that keeps these Secrets in line with what your Git
repo says. To [reconcile](../glossary.md#reconcile) means to compare what
Git says with what is really there, and fix the difference.

If you only use Sharko and don't write Kubernetes files by hand, the short
version is:

> **You don't have to do anything.** When you register a cluster, Sharko
> opens a pull request. Once it merges, Sharko creates the ArgoCD cluster
> Secret, marks it as its own, and removes it again when you remove the
> cluster. No `kubectl` work is needed.

The rest of this page is for when something goes wrong, or when you run
Sharko on an ArgoCD that already has cluster Secrets.

Two other pages cover problems with the reconciler itself:
[Reconciler crash loop](reconciler-crash-loop.md) and
[Cluster reconciler dependency missing](cluster-reconciler-dependency-missing.md).

---

## Overview

The reconciler runs inside the Sharko pod. There is no extra operator or
custom resource to install. It reads your cluster list from Git:

- `managed-clusters.yaml` at the root of the repo, which lists the
  clusters, and
- `cluster-addons/<cluster>.yaml`, which says which addons are on for each
  cluster.

On a repo still on the older layout, it reads
`configuration/managed-clusters.yaml` instead. It always reads the branch
Sharko is set up to use.

It then makes ArgoCD's cluster Secrets match. Every Secret Sharko creates
has the label `app.kubernetes.io/managed-by: sharko`. Sharko only ever
creates, changes or deletes Secrets that have that label. Secrets made by
other people or tools are left alone, and you can [adopt](#coexistence-with-clusters-created-outside-sharko)
them if you want Sharko to manage them.

Sharko only creates a Secret **after** the registration pull request
merges. So if someone closes that pull request without merging it, no
leftover Secret is left behind.

---

## The ownership label: `app.kubernetes.io/managed-by: sharko`

This one label is what keeps the reconciler safe.

### What it is

Every ArgoCD cluster Secret Sharko creates in the `argocd` namespace has
the standard Kubernetes ownership label:

```yaml
metadata:
  labels:
    app.kubernetes.io/managed-by: sharko
    argocd.argoproj.io/secret-type: cluster
    # ...plus one addons.sharko.dev/<addon>: enabled label for each addon
    #    that is on in cluster-addons/<cluster>.yaml
```

Sharko adds the label when it creates the Secret. Adding it again changes
nothing.

### Why it exists

Without the label, Sharko could not tell its own Secrets apart from
Secrets someone made with `kubectl` or another tool. Two things depend on
that:

- Cleaning up leftover Secrets. Without the label, Sharko could delete a
  Secret you made by hand just because it was not in
  `managed-clusters.yaml`.
- Adopting clusters. Sharko offers to adopt exactly the Secrets that do
  *not* have the label.

So Sharko only changes Secrets with the label, and only offers to adopt
Secrets without it.

### When Sharko adds it

On every Secret it creates. There is no other way the label gets added.

### What happens if someone removes the label by hand {#what-happens-if-a-user-removes-the-label-manually}

Sharko stops touching the Secret. For the reconciler, the Secret is no
longer one of its own. The **Discard cancelled registration** button on the
Clusters page also won't act on it, and
`DELETE /api/v1/clusters/{name}/orphan` refuses with HTTP 400 (Activity
History shows `cluster_orphan_delete_rejected`). The Secret now counts as
managed outside Sharko, and you can adopt it if you want Sharko to manage
it again.

### What happens if someone adds the label to a Secret Sharko didn't create

On its next check, Sharko sees a Secret with its label but no matching
entry in `managed-clusters.yaml`, and **deletes it**. This is on purpose:
the reconciler makes ArgoCD match what Git says.

**If this happened by mistake:** either add the cluster to
`managed-clusters.yaml` (the better choice, because it says in Git what you
want), or remove the label from the Secret so Sharko leaves it alone.

---

## How often it runs

The reconciler checks in three cases:

- **Every 30 seconds.** This is fixed and can't be changed in the Helm
  values today.
- **Right after a Sharko pull request merges.** If several merge one after
  another, Sharko still runs at least once after the last one. In the
  normal case, the Secret is ready a few seconds after the merge.
- **When you ask for a check.** The **Refresh** button in the **Managed
  cluster secret** panel on the cluster page runs a check of every cluster
  right away, instead of waiting for the next one. This check only reads.
  It writes nothing. The same thing through the API is
  `POST /api/v1/clusters/{name}/reconcile`, which returns `202` at once.
  Then read `last_reconcile` from `GET /api/v1/clusters/{name}` to see the
  result. It returns `503` if the reconciler is not running.

The check every 30 seconds is what catches:

- changes made straight in Git, outside Sharko (someone edited
  `managed-clusters.yaml` by hand),
- Secrets deleted by mistake (`kubectl delete secret ...` in the `argocd`
  namespace),
- a credentials backend that was down for a moment (the next check tries
  again).

---

## The last check result for each cluster (`last_reconcile`)

`GET /api/v1/clusters/{name}`, and the cluster list, include a
`last_reconcile` field for each cluster:

```json
{
  "last_reconcile": {
    "time": "2026-07-09T14:32:07Z",
    "outcome": "failed",
    "message": "The last check didn't finish. Click Refresh to try again."
  }
}
```

`outcome` is `succeeded`, `failed` or `skipped`. `message` is a plain
sentence that says what happened. When the addon labels on the Secret
differ from Git, the field also has a `label_drift` part that lists them.

Sharko keeps this only in memory. It is never written to Git, and it is
lost when Sharko restarts. The next check, at most 30 seconds later, fills
it in again. The field is empty when Sharko has not checked this cluster
yet since it started, or when the registration pull request has not merged
yet. In the UI it is the **Last sync** line in the **Managed cluster
secret** panel on the cluster page.

### When addon labels differ from Git

If the addon labels on a Secret Sharko manages no longer match Git, Sharko
puts them back on its next check. On a repo still on the older layout,
Sharko only does this when the self-heal setting is on. That setting is
off by default, and you change it with
`PUT /api/v1/settings/managed-cluster-self-heal` (admin only). With the
setting off, Sharko only records the difference in `label_drift`.

To put the labels back once, for one cluster, whatever the setting says,
click **Sync addon labels** on the cluster page. It is only enabled when
there is a difference. The same thing through the API is
`POST /api/v1/clusters/{name}/resync`. It only writes Sharko's own addon
label keys.

### When two tools fight over the same labels

This can happen when **you** manage a cluster's connection and another
ArgoCD Application also writes the same Secret from Git. If a label Sharko
just wrote comes back with a different value two checks in a row,
`last_reconcile.outcome` still says `succeeded` (Sharko is still putting
its labels back), but `message` warns you that something keeps overwriting
them. The fix is always to stop the other writer. An `ignoreDifferences`
rule only hides the fight. The full explanation and fix are in
[Managing Cluster Connections Yourself → When another ArgoCD Application also renders this secret](self-managed-connections.md#when-another-argocd-application-also-renders-this-secret).

---

## Git to ArgoCD on its own, ArgoCD to Git only when you ask

The reconciler copies changes from Git to ArgoCD by itself. It never copies
changes from ArgoCD back to Git unless a person asks for it by adopting a
cluster.

| What changed | What the reconciler does | Direction |
| --- | --- | --- |
| New entry in `managed-clusters.yaml` | Creates a Secret with Sharko's label in the `argocd` namespace | Git → ArgoCD (on its own) |
| Entry removed from `managed-clusters.yaml` | Deletes the Secret with Sharko's label | Git → ArgoCD (on its own) |
| A Secret **without** Sharko's label shows up in ArgoCD | Leaves it alone. You can adopt it. | ArgoCD → Git (only when you adopt) |
| A Secret with Sharko's label was deleted outside Sharko | Creates it again on the next check | Git → ArgoCD (on its own) |
| A Secret without Sharko's label was deleted | Nothing. It was never Sharko's. | none |

Why the two directions are different:

- **Git to ArgoCD is safe to do by itself,** because Git says what you want
  and the result is clear.
- **ArgoCD to Git needs a person,** because it rewrites
  `managed-clusters.yaml`. Someone may just be testing with
  `kubectl apply`, and a pull request opened behind their back would be
  unwelcome.

---

## When things go wrong

### What if Sharko is down?

You can't register new clusters, because Sharko can't open pull requests.
The ArgoCD cluster Secrets that already exist **stay as they are**, and
ArgoCD keeps using them. The ArgoCD UI and
`kubectl -n argocd get secrets` keep working.

Sharko runs as one pod. While that pod is down, nothing that writes through
Sharko works.

### What if the credentials backend is down for a moment?

Sharko writes the error to its log and to Activity History
(`cluster_secret_create_failed`), and **carries on** with the other
clusters in the same check. A problem with one cluster does not stop the
others. The failed cluster is tried again on the next check, 30 seconds
later. There is no growing wait between tries.

### What if a labeled Secret is accidentally deleted (`kubectl delete`)? {#what-if-a-labeled-secret-is-accidentally-deleted-kubectl-delete}

**It fixes itself.** On the next check, Sharko sees the cluster in Git but
not in ArgoCD. It fetches the credentials again and creates the Secret
again, with its ownership label and the addon labels. This takes up to 30
seconds. If the Secret does not come back, look in Activity History for
`cluster_secret_create_failed` on that cluster (see
[My cluster's Secret didn't appear](#my-clusters-secret-didnt-appear-after-merging-the-pull-request)).

### What if `managed-clusters.yaml` has a schema-validation error? {#what-if-managed-clustersyaml-has-a-schema-validation-error}

Sharko refuses to act on a file it can't read correctly. It writes a
`cluster_secret_reconcile` entry to Activity History with the action
`schema_validation` and the result `failure`, and **does nothing else in
that check**. The Secrets that already exist stay as they are until the
file is valid again. Fix the YAML with a new pull request. The next check
after the fix works as normal.

This is on purpose: a broken file stops the reconciler, instead of letting
it act on a wrong picture of your clusters.

To catch this before you open a pull request, run the same check on your
own machine, from the root of your repo:

```bash
sharko validate-config managed-clusters.yaml
sharko validate-config cluster-addons/
```

### What if Git is rate-limited or can't be reached?

Sharko writes the error to its log, writes a `cluster_secret_reconcile`
entry to Activity History with the action `git_read` and the result
`failure`, and **skips this check**. The Secrets that already exist stay as
they are. Sharko does nothing it can't undo when it does not have the full
picture. The next check tries again.

Sharko reads only a few files from Git on each check, every 30 seconds.
That is far below the normal API limits of GitHub, Azure DevOps and Gitea.

---

## Coexistence with clusters created outside Sharko

ArgoCD cluster Secrets that do not have the
`app.kubernetes.io/managed-by: sharko` label are left alone. ArgoCD still
shows them as clusters. Sharko does not count them as its own, and its
cleanup refuses to touch them.

To bring one under Sharko, **adopt** it. Adopting writes an entry to
`managed-clusters.yaml` through a pull request. The cluster's connection
stays managed by you, so Sharko does not rewrite or rotate its
credentials. See [Managing Cluster Connections Yourself](self-managed-connections.md).

> **Don't** try to delete a Secret without Sharko's label using the
> **Discard cancelled registration** button. Sharko refuses with HTTP 400.
> If you really want to delete a cluster Secret that Sharko does not
> manage, use `kubectl -n argocd delete secret <name>` yourself.

---

## Troubleshooting

### My cluster's Secret didn't appear after merging the pull request

1. Look in Activity History for `cluster_secret_create` around the time of
   the merge. You can also use the API:
   `GET /api/v1/audit?action=cluster_secret_create&since=<time>`.
2. If you see `cluster_secret_create_failed`, Sharko could not get the
   cluster's credentials. Check that the credentials are stored at the
   cluster's path in your credentials backend, and that Sharko is allowed
   to read them.
3. If you see nothing at all for the cluster, either the reconciler has not
   run yet (wait up to 30 seconds), or a file problem is stopping every
   check. Look for a `cluster_secret_reconcile` entry with the action
   `schema_validation`.
4. If you see a `cluster_secret_reconcile` entry with the action
   `git_read`, check Sharko's Git token and whether your Git host
   (GitHub, Azure DevOps or Gitea) is working.

### A cluster I removed from `managed-clusters.yaml` is still in ArgoCD

1. Check that the Secret has Sharko's label:
   `kubectl -n argocd get secret <name> -o yaml | grep managed-by`.
   If the label is missing, Sharko will not delete the Secret. It counts as
   managed outside Sharko.
2. If the label is there, look in Activity History for
   `cluster_secret_delete` on the next checks. If the delete failed (RBAC,
   or the API server was down), you see `cluster_secret_delete_failed`
   with the error for that cluster.
3. Check that the cluster is really gone from `managed-clusters.yaml` on
   the branch Sharko uses. Sharko reads that branch, not your working
   branch.

### Sharko keeps trying to delete my cluster Secret

You most likely have a Secret with Sharko's label but no entry for it in
`managed-clusters.yaml`. The reconciler is doing its job: making ArgoCD
match Git. Two fixes:

- **Better:** add the cluster to `managed-clusters.yaml`.
- **Or:** remove the `app.kubernetes.io/managed-by: sharko` label from the
  Secret, so Sharko leaves it alone.

### How do I make Sharko act right now, without waiting 30 seconds?

- **To check every cluster's Secret now:** click **Refresh** in the
  **Managed cluster secret** panel, or call
  `POST /api/v1/clusters/{name}/reconcile`. This only reads, and returns
  `202` at once. Then read `last_reconcile` from
  `GET /api/v1/clusters/{name}`.
- **To put one cluster's addon labels back now:** click **Sync addon
  labels**, or call `POST /api/v1/clusters/{name}/resync`.

When a Sharko pull request merges, Sharko already runs at once on its own.
The steps above are for everything else: a change made straight in Git, a
Secret you created again by hand, or just wanting to see the result
without waiting.

### Activity History entries to look for

| Action | Meaning |
| --- | --- |
| `cluster_secret_create` | Sharko created a Secret in the `argocd` namespace |
| `cluster_secret_create_failed` | Creating it failed (only this cluster is affected) |
| `cluster_secret_delete` | Sharko deleted a Secret with its label |
| `cluster_secret_delete_failed` | Deleting it failed |
| `cluster_secret_user_label_sync` | Sharko put addon labels on a Secret you manage |
| `cluster_secret_user_label_sync_failed` | That failed |
| `cluster_secret_managed_self_heal` | Sharko put labels back on a Secret it manages |
| `cluster_secret_managed_self_heal_failed` | That write failed, or the labels still don't match |
| `cluster_secret_user_pending` | You manage the connection, and have not created the Secret yet |
| `cluster_secret_reconcile` with action `schema_validation` | A cluster file in Git is not valid. Nothing was done in that check. |
| `cluster_secret_reconcile` with action `git_read` | Sharko could not read Git. The check was skipped. |
| `cluster_secret_reconcile` with action `list_secrets` | Sharko could not list the Secrets in ArgoCD. The check was skipped. |
| `connection_credential_drift_detected` | A credentials check found a difference |
| `connection_credential_drift_cleared` | A later check found the difference gone |
| `connection_credential_check_failed` | The credentials check itself could not run |
| `connection_credential_check_recovered` | A check that had been failing worked again |
| `cluster_connection_repair_requested` | Someone asked for a repair |
| `cluster_connection_repair` | A repair ran and wrote |
| `cluster_connection_repair_refused` | A repair was refused before touching anything |
| `cluster_connection_repair_failed` | A repair started and failed |

A name without `_failed` is only written when the thing really happened.
Anything that failed gets its own `_failed` name, or a `failure` result.

Each entry also has a `changes` field: `applied`, `none`,
`not_applicable`, or missing. See
[Activity History → the `changes` field](audit-log.md#the-changes-field).

---

## Related reading

- [Configuration reference](./configuration.md) — the Helm values. The
  reconciler is on by default. You don't need to turn it on.
- [Activity History](./audit-log.md) — how to search for the entries
  listed above.
- [Connection Doctor](./connection-doctor.md) — a check you run on one
  cluster when you want it. Its `secret-ownership` check looks for the same
  label fight that the reconciler notices over time.
- [Managing Cluster Connections Yourself](./self-managed-connections.md) —
  connections you manage, and how the reconciler treats them.

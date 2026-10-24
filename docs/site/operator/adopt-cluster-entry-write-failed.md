# Adopting a cluster failed or stopped part-way

This page is for when you adopt a cluster that ArgoCD already knows about (`sharko adopt`, or
`POST /api/v1/clusters/adopt`), and Sharko says that cluster `failed` or stopped `partial`.

**How urgent:** Fix it today. Nothing is deleted. A cluster that failed is left as it was before.
A cluster that stopped part-way may already have a pull request or a change in Git, and needs one
more try.

Before you start, set up the shell variables from [First checks](troubleshooting.md#set-up-once).

**A note on the old log line.** Older versions of this page were about the log line
`failed to add cluster entry`. Only the old v3 repo format can print it. Sharko no longer writes
to a v3-format repo: adopting on one is refused before anything is written (see the first case
below). On a repo in the new format, the same problem shows up as the
`adding "<cluster>" to managed-clusters.yaml: ...` error described below, and Sharko stops
before it writes anything.

## What you see

Sharko answers HTTP 200 (or 207) even when a cluster failed. Always read the answer:

```sh
curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" -H 'Content-Type: application/json' \
  -d '{"clusters":["<cluster>"],"dry_run":true}' \
  "$SHARKO_URL/api/v1/clusters/adopt" | jq '.outcome, .results[] | {name, status, error, message, warnings}'
```

`outcome` has `total`, `completed`, `partly_completed` and `failed`. Anything other than
`completed` equal to `total` means some work did not finish. The `dry_run` above changes nothing,
so you can run it as often as you like.

What you can see, from earliest to latest:

| Where | What it says | What it means |
|---|---|---|
| HTTP 409 | `this repo uses the v3 format — migrate to continue (one PR); reads keep working` | The repo is still in the old format. Nothing was written |
| HTTP 502 | `Sharko stopped before adopting a cluster: could not read sharko-engine.yaml ...` | Sharko could not read Git to find out the repo format. Nothing was written |
| HTTP 502 | `listing ArgoCD clusters: ...` | Sharko could not reach ArgoCD. Nothing was written |
| `failed` | `cluster "<cluster>" not found in ArgoCD — cannot adopt` | The name is wrong, or the cluster is not in ArgoCD |
| `failed` | `Sharko is not connected to the cluster it runs in ...` | Adopting needs Sharko installed in Kubernetes |
| `failed` | `getting secret "<cluster>" in namespace "argocd": ...` | Sharko could not read the cluster's ArgoCD Secret. See [Sharko could not tell who owns a cluster's ArgoCD connection](adopt-managed-by-label-read-failed.md) |
| `failed` | `<cluster> is not ready to take over yet: 1 thing to fix first.` | The checks found something that blocks the adoption. Nothing was written. Step 2 shows how to see which check |
| `failed` | `adding "<cluster>" to managed-clusters.yaml: ...` | The cluster list file in Git can't be read. Nothing was written |
| `failed` | `Git commit failed during adoption` | Sharko could not write to Git. Nothing was written |
| `partial` | `PR created but merge failed for cluster <cluster>: <PR link>` | The pull request is open but did not merge |
| `partial` | `Adoption PR merged but Sharko could not take ownership of the ArgoCD connection ...` | Git has the change, but the ArgoCD Secret was not updated |
| `partial` | `Adoption PR merged, but <cluster>'s ArgoCD connection disappeared ...` | The ArgoCD Secret was removed while Sharko worked |
| `partial` | `The adoption pull request is merged, but Sharko could not mark the cluster's ArgoCD connection as adopted. ...` | Almost done: only the last mark on the Secret is missing. Adopt again to finish |

The `partial` cases also write one of these lines to the Sharko log:

```text
failed to take ownership of the ArgoCD cluster secret during v4 adoption
failed to set adopted annotation on ArgoCD secret — reconciler will retry
```

```sh
kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h | grep -E 'adoption|adopted annotation'
```

## Why it happens

- **The repo is still in the old v3 format.** Sharko only reads such a repo now. It does not
  write to it.
- **Sharko can't reach Git or ArgoCD**, or the Git token has no right to push or open pull
  requests.
- **The cluster list file in Git (`managed-clusters.yaml`) is broken**, for example after a
  hand edit. Sharko will not write to a file it can't read. Most of the time this shows up as a
  blocked check: "Sharko could not read its own list of clusters: ...".
- **The checks block the cluster.** For example, another tool's name is in the
  `app.kubernetes.io/managed-by` label of its ArgoCD Secret, another ArgoCD Application deploys
  that Secret, or Sharko already has a different cluster with the same name.
- **The pull request was made but did not merge**, for example because of a branch rule or a
  required review.
- **Sharko could not change the cluster's ArgoCD Secret** after the merge, most often because of
  missing rights in the ArgoCD namespace.

## How to fix it

1. **Read the `error` and `message` for each cluster** with the command under "What you see". Find
   the line in the table.

2. **Fix the cause.**

   - **v3 format (HTTP 409).** Move the repo to the new format first. See
     [Migrating a repo to the new format](migrating-to-the-new-format.md). Then adopt again.
   - **Git or ArgoCD can't be reached.** Follow [Sharko can't reach Git](git-provider-unreachable.md),
     or test the ArgoCD connection in **Settings > Connection**.
   - **Broken `managed-clusters.yaml`.** Open the file in your Git host and fix the YAML with a
     normal pull request. If you are not sure what is wrong, run
     `sharko validate-config managed-clusters.yaml` on a copy of the file on your own machine.
   - **The checks block the cluster.** See each check and what to do about it:

     ```sh
     curl -s -H "Authorization: Bearer $SHARKO_TOKEN" \
       "$SHARKO_URL/api/v1/clusters/<cluster>/takeover/preflight" | jq '.summary, .findings[] | select(.status != "ok")'
     ```

     Each finding has a `what_to_do` field. The
     [Taking over a fleet that is already running](takeover-playbook.md) page explains them.
   - **The pull request did not merge.** Open the link from `message`, and merge it by hand if it
     looks right. See [auto-merge-failed-after-pr-opened.md](auto-merge-failed-after-pr-opened.md).
   - **Sharko could not change the ArgoCD Secret.** Check that the chart's Role in the ArgoCD
     namespace exists (`sharko-argocd-secrets` when the release is called `sharko`):

     ```sh
     kubectl -n argocd get role,rolebinding | grep argocd-secrets
     ```

     If ArgoCD runs in another namespace, use that one, and make sure `rbac.argocdNamespace` in
     your Helm values names it.

3. **Adopt again.** It is safe to repeat. If a pull request for this cluster is still open, Sharko
   finds it and does not open a second one. Look at the dry run first:

   ```sh
   sharko adopt <cluster> --dry-run
   sharko adopt <cluster>
   ```

   Sharko only changes the cluster's ArgoCD Secret right after its own pull request merges. If you
   merged the pull request by hand later, check the Secret (see the next section). If it still
   does not carry the Sharko label, adopt again.

## How to check it's fixed

- The adopt answer shows `"status": "success"` for the cluster, and `outcome.completed` equals
  `outcome.total`.
- The cluster shows up in `GET /api/v1/clusters` and on the Clusters page in the UI.
- The cluster's ArgoCD Secret carries the `app.kubernetes.io/managed-by: sharko` label. See
  [Look at the cluster's ArgoCD Secret](argocd-cluster-secret-corruption.md#look-at-the-clusters-argocd-secret).
- The log from the last 10 minutes has no new `adoption` errors.

## If that didn't work

Open an issue at <https://github.com/MoranWeissman/sharko/issues>. Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`)
- the log lines from the last hour that mention `adoption` or `adopted annotation`
- the `outcome` and `results` from the adopt answer (the `error` and `message` fields)
- what you tried from this page

Related pages:

- [Sharko could not tell who owns a cluster's ArgoCD connection](adopt-managed-by-label-read-failed.md)
- [Taking over a fleet that is already running](takeover-playbook.md)
- [Migrating a repo to the new format](migrating-to-the-new-format.md)

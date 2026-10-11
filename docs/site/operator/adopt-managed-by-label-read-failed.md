# Sharko could not tell who owns a cluster's ArgoCD connection

This page is for when you adopt a cluster and Sharko can't read that cluster's ArgoCD Secret, so
it can't tell whether another tool owns the connection.

This is logged as a warning (`WARN`), not as an error. But Sharko does not adopt that one cluster.
When it can't read who owns a connection, it refuses rather than take the connection away from
another tool by mistake. Other clusters in the same request carry on as normal.

**How urgent:** Fix it today. Nothing is changed or deleted. The cluster stays exactly as it was,
and you can adopt it again once Sharko can read its Secret.

Before you start, set up the shell variables from [First checks](troubleshooting.md#set-up-once).

## What you see

The adopt answer marks the cluster `failed`. Read the answer:

```sh
curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" -H 'Content-Type: application/json' \
  -d '{"clusters":["<cluster>"],"dry_run":true}' \
  "$SHARKO_URL/api/v1/clusters/adopt" | jq '.results[] | {name, status, error}'
```

The `error` field shows the reason in full, and looks like one of these:

```text
getting secret "<cluster>" in namespace "argocd": secrets "<cluster>" is forbidden: User "system:serviceaccount:sharko:sharko" cannot get resource "secrets" ...
could not confirm who owns cluster "<cluster>"'s ArgoCD connection: getting secret ... — Sharko will not adopt a connection whose owner it cannot read. Fix the read problem and try again.
```

The first one is what you see on a repo in the new format. The second one only comes from the
old v3 format.

On a repo in the new format, the adopt answer is the only place this shows up. On the old v3
format, the Sharko log also has this `WARN` line. In the log, Sharko writes only the kind of
error, not the full text, so use the adopt answer above for the details:

```sh
kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h | grep 'managed-by label'
```

```text
could not read managed-by label — refusing to adopt this cluster
```

The checks for the same cluster also fail to load. This answers HTTP 503 with the same
`getting secret ...` text:

```sh
curl -s -H "Authorization: Bearer $SHARKO_TOKEN" \
  "$SHARKO_URL/api/v1/clusters/<cluster>/takeover/preflight" | jq
```

This page is not about the answer `cluster "<cluster>" is managed by "<tool>", not sharko — cannot
adopt`, or a check that says "The connection is marked as owned by ...". In those cases Sharko
read the Secret fine, and another tool really owns it. See
[Taking over a fleet that is already running](takeover-playbook.md).

## Why it happens

Sharko reads each cluster's ArgoCD Secret in the ArgoCD namespace (`argocd`, or what you set in
the `rbac.argocdNamespace` Helm value). The read can fail because:

- **Sharko is not allowed to read Secrets there.** The chart gives Sharko this right with a Role
  called `<fullname>-argocd-secrets` (`sharko-argocd-secrets` when the release is called
  `sharko`) and a RoleBinding with the same name. If `rbac.create` is `false`, or
  `rbac.argocdNamespace` names the wrong namespace, the Role is missing or in the wrong place.
- **ArgoCD runs in a different namespace** than the one Sharko was told about.
- **The Kubernetes API did not answer** for a moment (a timeout or a refused connection).

## How to fix it

1. **Read the `error` field** with the command under "What you see". If it says `forbidden`, go to
   step 2. If it says `timeout` or `connection refused`, wait a minute and adopt again.

2. **Check that Sharko can read Secrets in the ArgoCD namespace.** Run this from your own machine.
   Use your ArgoCD namespace if it is not `argocd`, and your service account name if you set one
   in `serviceAccount.name`:

   ```sh
   kubectl auth can-i get secrets -n argocd \
     --as="system:serviceaccount:$SHARKO_NS:sharko"
   ```

   It should answer `yes`.

3. **If it answers `no`, check the Role and RoleBinding:**

   ```sh
   kubectl -n argocd get role,rolebinding | grep argocd-secrets
   ```

   - If they are missing, set `rbac.create: true` in your Helm values and run your usual
     `helm upgrade`, with the same chart version. If ArgoCD or another GitOps tool installs
     Sharko, change the values file in Git instead.
   - If ArgoCD runs in another namespace, set `rbac.argocdNamespace` to that namespace in the same
     way. Then restart Sharko so it reads the new namespace:

     ```sh
     kubectl -n "$SHARKO_NS" rollout restart deploy/sharko
     ```

   - If your team manages RBAC by hand (`rbac.create: false`), give the Sharko service account
     `get`, `list`, `watch`, `create`, `update`, `patch` and `delete` on `secrets` in the ArgoCD
     namespace. Those are the rights the chart's Role gives.

4. **Adopt again.** Look at the dry run first:

   ```sh
   sharko adopt <cluster> --dry-run
   sharko adopt <cluster>
   ```

## How to check it's fixed

- `kubectl auth can-i get secrets -n argocd --as="system:serviceaccount:$SHARKO_NS:sharko"`
  answers `yes`.
- The checks load and answer HTTP 200:

  ```sh
  curl -s -H "Authorization: Bearer $SHARKO_TOKEN" \
    "$SHARKO_URL/api/v1/clusters/<cluster>/takeover/preflight" | jq '.ready, .summary'
  ```

- The adopt answer shows `"status": "success"` for the cluster.
- The log from the last 10 minutes has no new `could not read managed-by label` lines.

## If that didn't work

If the adoption now fails for another reason, see
[Adopting a cluster failed or stopped part-way](adopt-cluster-entry-write-failed.md).

Otherwise, open an issue at <https://github.com/MoranWeissman/sharko/issues>. Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`)
- the log lines from the last hour that mention `managed-by label`, if there are any
- the `error` field from the adopt answer
- the output of the `kubectl auth can-i` command in step 2
- what you tried from this page

Related pages:

- [Adopting a cluster failed or stopped part-way](adopt-cluster-entry-write-failed.md)
- [Look at the cluster's ArgoCD Secret](argocd-cluster-secret-corruption.md#look-at-the-clusters-argocd-secret)
- [Taking over a fleet that is already running](takeover-playbook.md)

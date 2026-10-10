# A cluster PR merged, but the cluster never shows up in ArgoCD

Use this page when Sharko opened a pull request to add a cluster, the pull
request was merged, and minutes later ArgoCD still has no connection for that
cluster.

**How urgent:** Fix it now. Git says the cluster is in the fleet, but ArgoCD
does not know about it, so no addon reaches it.

Before you start, set up the shell variables from
[First checks](troubleshooting.md#set-up-once).

If the pull request is still open and was never merged, this is not your page.
Go to [Auto-merge failed after the PR opened](auto-merge-failed-after-pr-opened.md)
or [Git provider unreachable](git-provider-unreachable.md).

## What you see

- Adding the cluster worked. The **Audit Log** page shows
  `cluster_registered`, and then `pr_merged` once Sharko saw the merge.
- No ArgoCD cluster Secret for the cluster appears in the ArgoCD namespace.
  (Use the commands in
  [Look at the cluster's ArgoCD Secret](argocd-cluster-secret-corruption.md#look-at-the-clusters-argocd-secret)
  to look for it.)
- There is no `cluster_secret_create` entry for the cluster after the
  `pr_merged` one. The gap in the activity history is the canonical signal:
  the merge worked, but the Secret was never made.
- More than about two minutes have passed since the merge.

How long it should take: Sharko checks open pull requests every 30 seconds.
When it sees a merge, it starts a reconcile pass right away. The reconcile
pass also runs by itself every 30 seconds. So the Secret normally appears
within a minute of the merge, and often within seconds when Sharko merged the
pull request itself. After two minutes, something is wrong.

Look at the audit entries for the cluster:

```sh
curl -s -H "Authorization: Bearer $SHARKO_TOKEN" \
  "$SHARKO_URL/api/v1/audit?cluster=<cluster>&limit=50" \
  | jq '.entries[] | {timestamp, event, action, result, reason, request_id}'
```

A healthy run ends with `pr_merged` and then `cluster_secret_create` with
`"result": "success"`.

The cluster's own page also shows the last reconcile result for it (the API
field `last_reconcile`):

```sh
curl -s -H "Authorization: Bearer $SHARKO_TOKEN" \
  "$SHARKO_URL/api/v1/clusters/<cluster>" | jq .last_reconcile
```

If `outcome` is `failed`, the `message` says why.

## Why it happens

- **The reconciler is not running.** The loop that writes ArgoCD connections
  has stopped, or never started. The `SharkoReconcilerPassAge` alert fires
  when it has not finished a pass for 5 minutes.
- **The reconciler can't read the cluster's sign-in details** from your
  secrets backend, so it skips that cluster and tries again next pass.
- **Sharko can't write Secrets in the ArgoCD namespace.** Someone removed or
  changed the Role Sharko uses there, or ArgoCD lives in a namespace the chart
  was not told about.
- **A Secret with the same name already exists and Sharko does not own it.**
  Sharko will not overwrite it. That cluster has to be adopted instead.
- **The connection is self-managed.** Sharko never makes the Secret for a
  self-managed connection. It waits for you to create it.
- **The Git file is not what you think.** The merge went to a different
  branch, or someone changed `managed-clusters.yaml` by hand and broke it.
  A broken file stops the whole pass, for every cluster.
- **ArgoCD has the Secret but does not use it.** The ArgoCD application
  controller is unhealthy.

## How to fix it

1. **Look at what the reconciler logged.** This one command covers most of
   the causes above:

    ```sh
    kubectl -n "$SHARKO_NS" logs deploy/sharko --since=15m | grep '\[clusterreconciler\]'
    ```

    What the lines mean:

    | Log line | What to do |
    |---|---|
    | `cluster Secret created` | It worked. Check ArgoCD (step 6). |
    | `vault GetCredentials failed — skipping cluster` | Sharko can't read this cluster's sign-in details. See [Sharko can't read the sign-in details for one cluster](single-cluster-credential-fetch-failed.md). |
    | `Secret Create failed — skipping cluster` | The write was refused. Go to step 3. |
    | `same-name Secret exists without sharko label — skipping (Adopt territory)` | A Secret Sharko does not own is in the way. Adopt the cluster, or remove that Secret if nothing uses it. |
    | `self-managed connection: ArgoCD cluster Secret not created yet — waiting for the user` | Create the Secret yourself. See [Self-managed connections](self-managed-connections.md). |
    | `managed-clusters file rejected — aborting tick` | The Git file is broken. Go to step 4. |
    | `git read failed — aborting tick` | Sharko can't read Git. See [Git provider unreachable](git-provider-unreachable.md). |
    | no `[clusterreconciler]` lines at all | Go to step 2. |

2. **If the reconciler is silent, check that it started.** Look for the
   start-up line:

    ```sh
    kubectl -n "$SHARKO_NS" logs deploy/sharko | grep 'cluster reconciler started'
    ```

    It shows the ArgoCD `namespace`, the Git `branch` and the
    `managed_clusters_path` Sharko uses. Check that all three are what you
    expect. If the line is missing, or the reconciler stopped, follow
    [Sharko's background jobs stopped finishing their passes](reconciler-crash-loop.md). A restart
    also starts a fresh pass:

    ```sh
    kubectl -n "$SHARKO_NS" rollout restart deploy/sharko
    kubectl -n "$SHARKO_NS" rollout status deploy/sharko --timeout=120s
    ```

3. **Check that Sharko may write Secrets in the ArgoCD namespace.** Find
   Sharko's service account, then ask Kubernetes:

    ```sh
    SA=$(kubectl -n "$SHARKO_NS" get deploy sharko -o jsonpath='{.spec.template.spec.serviceAccountName}')
    kubectl auth can-i create secrets -n argocd \
      --as="system:serviceaccount:$SHARKO_NS:$SA"
    ```

    You want `yes`. If the answer is `no`, put the chart's Role back with
    `helm upgrade` and your usual values file. If ArgoCD is not in the
    `argocd` namespace, set both `rbac.argocdNamespace` and
    `clusterTest.argocdNamespace` to the right namespace. See
    [What Sharko needs from ArgoCD](security.md#what-sharko-needs-from-argocd).

4. **Check the Git file.** Open `managed-clusters.yaml` on the base branch
   of your Git repo (at the root of the repo on v4 repos; older repos keep it
   at `configuration/managed-clusters.yaml`). Check that:

    - the cluster is listed,
    - the merge went to the branch shown in the `cluster reconciler started`
      line,
    - the file is valid YAML.

    If the file is broken, fix it with a normal pull request. See
    [Cluster reconciler](cluster-reconciler.md).

5. **Start a pass now.** Once you have fixed the cause, you do not need to
   wait. On the cluster's page, click **Refresh** in the **Argo CD cluster
   connection** box, or call:

    ```sh
    curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" \
      "$SHARKO_URL/api/v1/clusters/<cluster>/reconcile"
    ```

    It answers `202` straight away. The pass runs in the background.

6. **If the Secret exists but ArgoCD ignores it**, look at ArgoCD:

    ```sh
    kubectl -n argocd get pods
    kubectl -n argocd logs statefulset/argocd-application-controller --tail=200
    ```

    If the application controller is restarting or out of memory, fix it on
    the ArgoCD side. If ArgoCD itself can't be reached, see
    [Sharko can't reach ArgoCD](argocd-upstream-unreachable.md).

## How to check it's fixed

- The audit log shows `cluster_secret_create` with `"result": "success"` for
  the cluster.
- `last_reconcile.outcome` is `succeeded`:

    ```sh
    curl -s -H "Authorization: Bearer $SHARKO_TOKEN" \
      "$SHARKO_URL/api/v1/clusters/<cluster>" | jq .last_reconcile
    ```

- ArgoCD lists the cluster in its UI under **Settings > Clusters**.

### Monitoring — alert on the gap in the activity history

Sharko has no metric that counts merges against Secrets made. What you can
use today:

- the `SharkoReconcilerPassAge` alert, which fires when the reconciler has
  not finished a pass in 5 minutes (see
  [budget-burn-runbook.md](budget-burn-runbook.md) and [metrics.md](metrics.md));
- the audit log. Your log tooling can watch for a `pr_merged` entry with no
  `cluster_secret_create` entry for the same cluster within a few minutes.
  The live feed is at `GET /api/v1/audit/stream`. See
  [Audit log](audit-log.md).

## If that didn't work

Open an issue at <https://github.com/MoranWeissman/sharko/issues>. Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`)
- the audit entries for the cluster (the command above)
- the `[clusterreconciler]` log lines from around the merge
- the `cluster reconciler started` line
- what you tried from this page

Related pages:

- [Cluster reconciler](cluster-reconciler.md)
- [Sharko's background jobs stopped finishing their passes](reconciler-crash-loop.md)
- [Sharko can't reach ArgoCD](argocd-upstream-unreachable.md)
- [An addon secret did not reach the cluster](secret-push-silently-failed.md)

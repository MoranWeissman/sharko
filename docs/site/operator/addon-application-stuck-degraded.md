# An addon is stuck Degraded on one cluster

Use this page when Sharko turned an addon on for a cluster, the pull request
merged, but the addon's ArgoCD Application on that cluster stays `Degraded`
(or `Missing`, `Unknown` or `OutOfSync`).

**How urgent:** Depends on the addon. Only this one addon on this one
cluster is affected. Other addons, and the same addon on other clusters, keep
working.

Before you start, set up the shell variables from
[First checks](troubleshooting.md#set-up-once).

## What you see

- On the cluster's page in Sharko, the addon shows errors. Click the error
  count to see them, along with ArgoCD's last sync message.
- In ArgoCD, the Application named `<addon>-<cluster>` (for example
  `datadog-prod-eu`) has health `Degraded`, `Missing` or `Unknown`.
- Sharko's own part went fine. The **Audit Log** page shows
  `addon_enabled_on_cluster_v4` (or `addon_enabled_on_cluster` on older repo
  layouts) with result `success`.

You can also look from your own machine:

```sh
kubectl -n argocd get application datadog-prod-eu \
  -o jsonpath='{.status.health.status} {.status.sync.status}{"\n"}'
```

If **every** addon on the cluster is failing, the problem is the cluster, not
the addon. See
[Sharko can't read the sign-in details for one cluster](single-cluster-credential-fetch-failed.md)
or [A cluster's ArgoCD Secret is broken](argocd-cluster-secret-corruption.md).
If the **same addon fails on every cluster**, the chart or its catalog entry
is the problem.

## Why it happens

Once the pull request merges, ArgoCD installs the addon's Helm chart. The
problem is in that step, so Sharko's own logs usually show nothing. Common
causes:

- **Wrong values.** The per-cluster values file is missing a key the chart
  needs, has a typo, or has a value the chart rejects.
- **A Secret the addon needs is not on the cluster.** The pod fails with
  `secret "..." not found`.
- **Two addons fight over the same thing**, such as the same namespace or
  the same ClusterRoleBinding name. ArgoCD answers `AlreadyExists` or
  `Forbidden`.
- **The new chart version is broken**, or does not accept values that the
  old version did.
- **The cluster can't run it**: no room on the nodes, a quota is full, or the
  image can't be pulled.
- **ArgoCD is not allowed to sync right now**: a sync window is closed, or
  someone turned off automatic sync on the Application.

On v4 repos, the addon's values come from two files in your Git repo:
`values/global/<addon>.yaml` (for every cluster) and
`values/clusters/<cluster>/<addon>.yaml` (for this cluster only). Both files
are plain chart values. They are not wrapped under the addon name.

## How to fix it

1. **Read ArgoCD's own error first.** ArgoCD records the outcome of the
   last sync. This message usually says exactly what went wrong:

    ```sh
    kubectl -n argocd get application datadog-prod-eu \
      -o jsonpath='{.status.operationState.message}{"\n"}'
    ```

    Then list the parts of the addon that are not healthy:

    ```sh
    kubectl -n argocd get application datadog-prod-eu -o json \
      | jq '.status.resources[] | select(.health.status != null and .health.status != "Healthy")
            | {kind, name, namespace, health}'
    ```

    How to read the common messages:

    | Message contains | Go to |
    |---|---|
    | `ValidationError`, `error validating data` | Wrong values. Step 2. |
    | `secret "..." not found` | [An addon secret did not reach the cluster](secret-push-silently-failed.md) |
    | `AlreadyExists`, `Forbidden` | Two addons fight. Step 2 (change the namespace or the name). |
    | `Back-off pulling image`, `0 replicas available` | Look at the pods. Step 3. |
    | `backoff limit` on a Job | A Helm hook failed. Step 3. |

2. **Fix the values for this cluster.** You have three ways:

    - **Edit the file in Git.** Open a pull request that changes
      `values/clusters/<cluster>/<addon>.yaml`. This is also the only way
      to remove a key.
    - **Use the CLI.** `sharko enable-addon` merges the values you give into
      that file and opens the pull request for you. It works when the addon is
      already on:

        ```sh
        sharko enable-addon prod-eu datadog \
          --values-json '{"replicaCount": 2}' --dry-run
        sharko enable-addon prod-eu datadog \
          --values-json '{"replicaCount": 2}'
        ```

        The API behind it is
        `POST /api/v1/v4/clusters/{name}/addons/{addon}` with a body like
        `{"values": {...}, "yes": true}`.

    - **Change the namespace** through the cluster's addon settings
      (`settings.namespace` in `cluster-addons/<cluster>.yaml`), not through
      the chart values.

    Note: on v4 repos, the values editor on the cluster page and
    `PUT /api/v1/clusters/{cluster}/addons/{name}/values` do not save. They
    answer `409` with a message that starts
    `this editor doesn't support the v4 layout yet`. Use one of the ways
    above.

    ArgoCD picks up the change on its next sync once the pull request merges.

3. **Look at the pods on the cluster.** If the message points at a pod or a
   Job, look at its events. Use your own access to that cluster:

    ```sh
    kubectl --context <cluster> -n <addon-namespace> get pods
    kubectl --context <cluster> -n <addon-namespace> get events --sort-by=.lastTimestamp | tail -20
    ```

    The events name the real cause: image pull errors, `FailedScheduling`,
    a missing ConfigMap or Secret, a volume that is not ready.

4. **Start the sync again.** If the values are already right but the last
   sync is stuck or failed on old data, restart it. On the cluster's page,
   open the addon's errors and click **Restart sync**. Or:

    ```sh
    curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" \
      "$SHARKO_URL/api/v1/clusters/prod-eu/addons/datadog/restart-sync"
    ```

    This stops any sync that is still running and starts a new one. Sharko
    needs ArgoCD rights for this. See
    [Letting Sharko restart a sync](security.md#letting-sharko-restart-a-sync).

5. **Go back to the version that worked.** If it broke right after an
   upgrade, pin the old chart version for this cluster only:

    ```sh
    sharko enable-addon prod-eu datadog --version <old-version>
    ```

    Other clusters keep the new version. Run
    `sharko enable-addon prod-eu datadog --clear-version` later to follow the
    catalog version again.

6. **Last try: turn the addon off on this cluster.** This opens a pull
   request. Once it merges, ArgoCD removes the Application:

    ```sh
    sharko disable-addon prod-eu datadog
    ```

    The entry, its version pin and its settings are kept, so turning it back
    on later is easy. Turn it back on with `sharko enable-addon` once the
    cause is fixed.

7. **If a sync window or manual sync policy is the cause**, that is an
   ArgoCD setting, not a Sharko one. Wait for the window, or change the
   project's sync windows in ArgoCD.

## How to check it's fixed

- The Application is healthy and in sync:

    ```sh
    kubectl -n argocd get application datadog-prod-eu \
      -o jsonpath='{.status.health.status} {.status.sync.status}{"\n"}'
    # Healthy Synced
    ```

- The addon on the cluster's page in Sharko shows no errors.

- If you watch ArgoCD with Prometheus, ArgoCD's own metric
  `argocd_app_info{health_status="Degraded"}` no longer lists this
  Application.

## If that didn't work

Open an issue at <https://github.com/MoranWeissman/sharko/issues>. Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`) and the ArgoCD version
- the addon, the cluster, and the chart version
- ArgoCD's sync message from step 1, and the list of unhealthy parts
- what you tried from this page

If the problem is inside the chart itself, the chart's own project is the
better place to ask.

Related pages:

- [An addon secret did not reach the cluster](secret-push-silently-failed.md)
- [A cluster PR merged, but the cluster never shows up in ArgoCD](argocd-pr-merge-no-converge.md)
- [Auto-merge failed after the pull request opened](auto-merge-failed-after-pr-opened.md)
- [budget-burn-runbook.md](budget-burn-runbook.md), when many addons fail at once

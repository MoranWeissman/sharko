# Sharko's background jobs stopped finishing their passes

This page is for when the `SharkoReconcilerPassAge` alert fires, or when the System page shows that
a background job has not finished a good pass for a long time.

**How urgent:** Fix it today. Nothing is deleted, but new clusters, addon changes and addon values
stop reaching ArgoCD and the clusters until a pass works again.

Before you start, set up the shell variables from [First checks](troubleshooting.md#set-up-once).

Sharko runs two background jobs inside the one Sharko pod:

- **The cluster connection job** keeps each cluster's ArgoCD Secret in step with Git. It runs once
  right at start, then every 30 seconds. It has no off switch.
- **The addon values job** keeps addon values secrets in step with your secrets store. It runs
  every 5 minutes by default. An admin can switch it off in **Settings > Addon Values Engine**.

## What you see

- The alert `SharkoReconcilerPassAge` fires. It fires when the cluster connection job has had no
  good pass for more than 5 minutes, or the addon values job for more than 30 minutes. It does not
  fire for a job an admin has switched off.
- In Prometheus, the time of the last good pass stops moving:

  ```promql
  time() - sharko_reconciler_last_success_timestamp
  ```

  Each job is one `engine` label: `cluster_connection` or `addon_values`.
- Passes still run but end badly. `sharko_reconciler_runs_total` keeps going up with
  `outcome="partial"` (one or more clusters failed) or `outcome="failure"` (the pass stopped early).
  A "partial" pass does not count as good, so the alert fires for that too.
- One of these lines shows up again and again in the Sharko log:

  ```text
  [clusterreconciler] git read failed — aborting tick (no state mutated)
  [clusterreconciler] managed-clusters file rejected — aborting tick (no state mutated)
  [clusterreconciler] listing managed cluster Secrets failed — aborting tick
  [clusterreconciler] vault GetCredentials failed — skipping cluster (others still reconcile)
  [clusterreconciler] no GitProvider getter configured, skipping reconcile
  [secrets] reconcile failed
  [secrets] no Git connection — skipping reconcile
  ```

- The Sharko pod itself keeps restarting. Then no job gets far enough to finish a pass.

## Why it happens

- **Sharko can't read Git.** The Git token expired, the Git host is down, or you hit a rate limit.
  The job stops the pass before it changes anything.
- **The managed-clusters file in Git is broken.** It does not match the format Sharko expects, so
  the job refuses to use it and changes nothing.
- **One cluster always fails.** For example, its sign-in details can't be read from the secrets
  store. The other clusters still get done, but every pass ends "partial".
- **There is no active connection.** Sharko has no Git or ArgoCD connection set up, so the jobs
  skip every pass.
- **The Sharko pod keeps restarting.** If the pod runs out of memory, or the program crashes, the
  whole pod restarts. The jobs run inside Sharko, so a crash in one of them restarts all of Sharko.

There is no kill switch for these jobs. Sharko does not read `SHARKO_RECONCILER_ENABLED`. If you
set it, Sharko will not start at all, because Sharko refuses to start when it sees a `SHARKO_`
setting it does not know.

## How to fix it

1. **See which job is stuck and what its last error was.**

   ```sh
   curl -s -H "Authorization: Bearer $SHARKO_TOKEN" \
     "$SHARKO_URL/api/v1/system/managed-secrets" | jq '.engines'
   ```

   For each job you get `wired`, `enabled`, `interval_seconds`, `last_run`, `last_error`,
   `last_error_cluster` and `last_error_at`. `last_error` tells you which of the causes below
   you have. The same facts are on the System page in the UI.

2. **Read the log lines from the last hour.**

   ```sh
   kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h \
     | grep -E '\[clusterreconciler\]|\[secrets\]' | grep -E '"level":"(ERROR|WARN)"'
   ```

3. **Fix the cause you found.**

   - **Git read failed.** Follow [Sharko can't reach Git](git-provider-unreachable.md). If the Git
     token is the problem, see
     [Put in a new Git token](git-provider-unreachable.md#put-in-a-new-git-token). If you hit a
     rate limit, see [git-provider-rate-limited.md](git-provider-rate-limited.md).
   - **Managed-clusters file rejected.** The log line says what is wrong. Fix the file in Git
     with a normal pull request. Until then the job changes nothing, which is the safe thing.
   - **One cluster keeps failing.** `last_error_cluster` names it. Follow
     [One cluster's sign-in details can't be read](single-cluster-credential-fetch-failed.md).
   - **No connection.** Open **Settings > Connection** in the UI, or run `sharko connect`, and
     set up the Git and ArgoCD connection.
   - **The addon values job fails.** Look at the `[secrets] reconcile failed` line for the reason.
     If you need it to stop while you fix the secrets store, switch it off in
     **Settings > Addon Values Engine**. Switch it back on when you are done.
   - **The pod keeps restarting.** Follow [Sharko keeps restarting](oom-restart-loop.md).

4. **If you only need Sharko to stop for a while**, scale it to 0 and back to 1. Scaling to 0 stops
   everything: the UI, the API and both jobs.

   ```sh
   kubectl -n "$SHARKO_NS" scale deploy/sharko --replicas=0
   # ... fix the cause ...
   kubectl -n "$SHARKO_NS" scale deploy/sharko --replicas=1
   ```

   Sharko must always run as one single pod. Do not scale it above 1.

## How to check it's fixed

- Run step 1 again. `last_error` is empty, and `last_run` is a recent time: within the last minute
  for the cluster connection job, within the last few intervals for the addon values job.
- In Prometheus, the age of the last good pass drops back near zero:

  ```promql
  time() - sharko_reconciler_last_success_timestamp{engine="cluster_connection"}
  ```

- New `sharko_reconciler_runs_total` counts come in with `outcome="success"`.
- The `SharkoReconcilerPassAge` alert clears on its own a few minutes later.
- The log shows no new `aborting tick` or `reconcile failed` lines.

## If that didn't work

Open an issue at <https://github.com/MoranWeissman/sharko/issues>. Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`)
- the log lines from the last hour that mention `[clusterreconciler]` or `[secrets]`
- the `.engines` part of `/api/v1/system/managed-secrets` (it has no secrets in it)
- what you tried from this page

Related pages:

- [The background job that keeps cluster secrets in step](cluster-reconciler.md)
- [Sharko keeps restarting](oom-restart-loop.md)
- [Metrics](metrics.md)

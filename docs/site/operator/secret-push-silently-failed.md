# An addon secret did not reach the cluster

Some addons need a Kubernetes Secret on the cluster, such as an API key. Sharko reads the value from your secrets store and writes the Secret onto the cluster. When that write fails, the addon starts without the Secret it needs and breaks, often some time later, when ArgoCD syncs it.

**How urgent:** high for the addon that is affected. It won't work until its Secret is there. Other addons and clusters are not affected.

Before you start, set up the shell variables from [First checks](troubleshooting.md#set-up-once).

## What you see

- In ArgoCD, the addon's Application is `Degraded`. Its pods say a Secret was not found, or they fail to sign in to the service the addon talks to.
- In the Sharko UI, open **Secrets**, then **Addon secrets**. The row for this cluster and addon is not in a good state, and it says why, for example:
  - `Sharko couldn't connect to one of the clusters. Check that Sharko can reach that cluster, then click Refresh.`
  - `Sharko couldn't fetch a secret's value from the secrets store. Click Refresh to try again.`
  - `Sharko tried to write a secret on a cluster and the write failed. Click Refresh to try again.`
  - `One of the clusters is set up to skip certificate checks, so Sharko refused to send it a secret. ...`
  - `One of the secret definitions in the catalog is incomplete. Fix it in the catalog, then click Refresh.`
- The logs show which cluster, addon and Secret failed:

  ```sh
  kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h | grep '\[secrets\] reconcile failed'
  ```

- The last run's totals show errors:

  ```sh
  curl -s -H "Authorization: Bearer $SHARKO_TOKEN" \
    "$SHARKO_URL/api/v1/secrets/status" | jq
  ```

  Look at `errors` and `last_run`.

- If you use the chart's Prometheus rules, the `SharkoManagedSecretsSustainedBadState` alert fires when a Secret stays missing or out of date for 30 minutes. The `sharko_reconciler_item_failures_total` counter, with `engine="addon_values"`, goes up on each failure.

## Why it happens

Sharko delivers addon secrets in the background. It reads what to deliver from the addon catalog in your Git repository, then writes each Secret onto each cluster that has the addon turned on. This runs every 5 minutes (`SHARKO_SECRET_RECONCILE_INTERVAL`), and right away after a signed Git webhook push.

A write can fail because:

- **Sharko can't reach the cluster,** or can't get the cluster's credentials.
- **The secrets store can't be read,** or the path in the catalog no longer exists there.
- **Sharko isn't allowed to write Secrets** in that namespace on the cluster, or the namespace does not exist yet.
- **A Secret with that name is already there and Sharko did not make it.** Sharko never overwrites a Secret it did not create. The log says `leaving this secret alone — it exists on the cluster and Sharko did not create it`.
- **The cluster's connection skips certificate checks.** Sharko won't send a secret over a connection it can't verify. The log says `refusing to deliver this secret — the cluster's connection is set up to skip certificate checks`.
- **The catalog entry for the secret is incomplete.**
- **Secret delivery is switched off.** The log then says `[secrets] addon-values engine is switched off — skipping reconcile`, and nothing is delivered until it is turned back on in Settings.

## How to fix it

### Find the reason

Read the row on the **Addon secrets** page, or the log line from above. The log line names the `cluster`, `addon` and `secret`. Then fix the cause:

- **Can't reach the cluster or get its credentials:** test the cluster connection with [Connection doctor](connection-doctor.md).
- **Secrets store problem:** see [Secrets provider unreachable](secrets-provider-unreachable.md). Check that the path in the catalog entry still exists in the store.
- **Not allowed to write, or the namespace is missing:** give Sharko's access on that cluster the right to create and update Secrets in that namespace, or create the namespace.
- **A Secret Sharko did not make is in the way:** check where it came from. If nobody needs it, delete it so Sharko can create its own:

  ```sh
  kubectl --context <cluster-context> -n <namespace> get secret <secret-name> -o yaml
  kubectl --context <cluster-context> -n <namespace> delete secret <secret-name>
  ```

- **Certificate checks are skipped:** fix the cluster's connection so its certificate can be checked.
- **Incomplete catalog entry:** fix the secret definition in the addon catalog in Git.

### Deliver the secrets again now

After you fix the cause, you don't have to wait for the next run. Ask Sharko to deliver this cluster's addon secrets right away:

```sh
curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" \
  "$SHARKO_URL/api/v1/clusters/<cluster-name>/secrets/refresh" | jq

# Only one addon:
curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" \
  "$SHARKO_URL/api/v1/clusters/<cluster-name>/secrets/refresh?addon=<addon-name>" | jq
```

- HTTP 200 with `secrets_refreshed` means every Secret was delivered.
- HTTP 207 means some failed. `failed_secrets` lists them, and the **Addon secrets** page shows the reason for each one.
- HTTP 404 means Git does not list this cluster, or does not define a secret for this addon on it.

Sharko takes what to deliver only from Git. Anything you put in the request body is ignored.

To run delivery for every cluster now:

```sh
curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" \
  "$SHARKO_URL/api/v1/secrets/reconcile" | jq
```

### Let the addon pick up the Secret

Some addons only read their Secret when they start. Once the Secret is there, sync the addon's Application in ArgoCD, or restart its pods.

## How to check it's fixed

- The Secret is on the cluster and Sharko owns it:

  ```sh
  kubectl --context <cluster-context> -n <namespace> get secret <secret-name> \
    -o jsonpath='{.metadata.labels.app\.kubernetes\.io/managed-by}{"\n"}'
  ```

  This prints `sharko`.

- The row on the **Addon secrets** page is in a good state.
- `errors` is `0` in the status after the next run:

  ```sh
  curl -s -H "Authorization: Bearer $SHARKO_TOKEN" \
    "$SHARKO_URL/api/v1/secrets/status" | jq '{errors, last_run}'
  ```

- The addon's Application in ArgoCD is `Healthy`.

## If that didn't work

Open an issue at <https://github.com/MoranWeissman/sharko/issues>. Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`)
- the log lines from the last hour that mention `[secrets]`
- the reason shown on the **Addon secrets** page for the row
- the answer from the refresh call (it holds Secret names, not values)
- what you tried from this page

Related pages:

- [Secrets provider unreachable](secrets-provider-unreachable.md)
- [Connection doctor](connection-doctor.md)
- [Metrics](metrics.md)
- [Sharko can't reach your Git host](git-provider-unreachable.md)

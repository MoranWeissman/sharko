# The cluster reconciler is running but skips every pass

Use this page when Sharko's cluster reconciler is alive but does no work,
because something it needs is not set up.

**How urgent:** fix it today. Clusters that are already in ArgoCD keep
working. But while this lasts, Sharko does not add, change or remove
ArgoCD cluster Secrets after a pull request merges, and it does not put
back a Secret that someone changed or deleted by hand.

Before you start, set up the shell variables from
[First checks](troubleshooting.md#set-up-once).

## What you see

The cluster reconciler runs a pass every 30 seconds. It keeps ArgoCD's
cluster Secrets in line with `managed-clusters.yaml` in Git. At the start
of each pass it checks that it has what it needs. If something is missing,
it writes one log line and stops that pass. It tries again 30 seconds
later.

Look for those lines:

```sh
kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h | grep 'skipping reconcile'
```

You will see one of these messages, about every 30 seconds:

| Log message | Level | What is missing |
|---|---|---|
| `[clusterreconciler] no Vault (cluster-credentials provider) configured, skipping reconcile` | WARN | The place Sharko reads cluster sign-in details from (the secrets provider) |
| `[clusterreconciler] no GitProvider getter configured, skipping reconcile` | WARN | Sharko was started without a way to reach Git. This is a Sharko bug |
| `[clusterreconciler] no ArgoClient (k8s clientset) configured, skipping reconcile` | WARN | Sharko was started without a Kubernetes client. This is a Sharko bug |
| `[clusterreconciler] no active git provider, skipping reconcile` | DEBUG | There is no active Git connection yet |

The first line is the one you will almost always see.

Other things you may notice:

- You merged a pull request that adds a cluster, but the cluster never
  shows up in ArgoCD.
- You removed a cluster, but its Secret is still in ArgoCD.
- The audit log has no `cluster_secret_reconcile_tick` entries. (A pass
  that changes nothing writes no entry either, so an empty list alone
  proves nothing.)
- If an earlier pass worked since Sharko started, the
  `SharkoReconcilerPassAge` alert fires after about 7 minutes. If no pass
  has worked since Sharko started, no alert fires at all. Skipped passes
  are not counted in any metric.

## Why it happens

**The secrets provider is missing (the `no Vault` line).** The
reconciler needs a secrets provider to read each cluster's sign-in
details. The provider is set on the active connection, under
**Settings > Secrets Provider** in the UI, in the field called
**Provider Type**. Sharko has no provider when:

- Sharko runs outside a Kubernetes cluster and the provider type is
  empty. (Inside a cluster, an empty type means "read from ArgoCD", and
  that works.)
- The provider type is `aws-sm`, but Sharko can't load its AWS settings.
- The provider type is `k8s-secrets`, but Sharko can't load its
  Kubernetes settings.

Sharko logs the reason at startup and every time you save the
connection:

```sh
kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h | grep 'no credentials provider configured'
```

The line starts with `[serve]` at startup and with `[startup]` after you
save the connection. Its `reason` field says what went wrong, for example
`no secrets provider configured — configure provider in Settings or via API`
or `loading AWS config: ...`.

**There is no active Git connection (the DEBUG line).** This happens on a
new install before anyone has set up a connection. At the default log
level (`SHARKO_LOG_LEVEL=info`) you will not see this line at all. The
reconciler just stays quiet.

**The other two WARN lines.** A normal Sharko server always gives the
reconciler a way to reach Git. It only starts the reconciler when it
has a Kubernetes client. So if you see either of these lines, Sharko
itself is wired wrong. There is nothing to fix in your settings. Go to
[If that didn't work](#if-that-didnt-work).

If you see none of these lines and the reconciler still does nothing,
check whether it started at all:

```sh
kubectl -n "$SHARKO_NS" logs deploy/sharko | grep 'cluster reconciler'
```

`cluster reconciler skipped: no in-cluster k8s client` or
`cluster reconciler skipped: no ConfigMap store (out-of-cluster or k8s client failure)`
means it never started. Sharko must run inside the cluster, with its
service account token mounted. If the reconciler keeps dying and
coming back, see [Sharko's background jobs stopped finishing their passes](reconciler-crash-loop.md).

## How to fix it

### Set the secrets provider

1. Open **Settings > Secrets Provider**.
2. Pick a **Provider Type**:
    - **ArgoCD (auto — reads cluster credentials from the ArgoCD Secret)**:
      Sharko reads sign-in details from ArgoCD's own cluster Secrets.
      This only works when Sharko runs inside the cluster.
    - **AWS Secrets Manager**: fill in the region and, if you use one,
      the name prefix (for example `clusters/`).
    - **Kubernetes Secrets**: fill in the namespace that holds the
      Secrets.
3. Save.

You can also set it through the API. This changes only the provider. It
keeps the Git, ArgoCD and GitOps settings on the connection:

```sh
CONN=$(curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/connections/" | jq -r .active_connection)

curl -s -X PUT -H "Authorization: Bearer $SHARKO_TOKEN" \
  -H "Content-Type: application/json" \
  "$SHARKO_URL/api/v1/connections/$CONN" \
  -d '{"provider":{"type":"aws-sm","region":"eu-west-1","prefix":"clusters/"}}' | jq
```

For `k8s-secrets`, send `{"provider":{"type":"k8s-secrets","namespace":"<namespace>"}}`
instead.

You do not need to restart Sharko. The reconciler looks for the provider
again at the start of every pass, so the next pass (within 30 seconds)
uses it.

If you manage the connection with Helm, the same settings are under
`connection.provider` (`type`, `region`, `prefix`, `namespace`,
`roleArn`). Helm passes them to Sharko as `SHARKO_CONN_PROVIDER_TYPE`,
`SHARKO_CONN_PROVIDER_REGION`, `SHARKO_CONN_PROVIDER_PREFIX`,
`SHARKO_CONN_PROVIDER_NAMESPACE` and `SHARKO_CONN_PROVIDER_ROLE_ARN`. A
value set in Helm wins over a value saved in the UI. These settings only
change a connection that already exists. On a new install with no
connection, they do nothing.

### If the provider type is set but still not working

The `reason` in the `no credentials provider configured` line tells you
which part failed:

- For AWS, check the role Sharko runs as. See
  [Check the AWS role](secrets-provider-unreachable.md#check-the-aws-role).
- For Kubernetes Secrets, see
  [Check the Kubernetes Secrets backend](secrets-provider-unreachable.md#check-the-kubernetes-secrets-backend).

### Set up a Git connection

If there is no active Git connection, finish the first setup in the UI,
or use `sharko connect`. To see what connections exist and which one is
active:

```sh
curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/connections/" \
  | jq '{active_connection, connections: [.connections[] | {name, is_active, git_provider, provider}]}'
```

To make a saved connection the active one:

```sh
curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" \
  -H "Content-Type: application/json" \
  "$SHARKO_URL/api/v1/connections/active" \
  -d '{"connection_name":"<name>"}' | jq
```

The reconciler picks up the active connection on its next pass. No
restart is needed.

## How to check it's fixed

1. The skip lines stop. This should print nothing:

    ```sh
    kubectl -n "$SHARKO_NS" logs deploy/sharko --since=2m | grep 'skipping reconcile'
    ```

2. After saving the connection, Sharko logs
   `[startup] credentials provider constructed`:

    ```sh
    kubectl -n "$SHARKO_NS" logs deploy/sharko --since=10m | grep 'credentials provider constructed'
    ```

3. A pass has worked. This metric only shows up after a real pass. Check
   that its value is a recent Unix time:

    ```sh
    curl -s "$SHARKO_URL/metrics" | grep 'sharko_reconciler_last_success_timestamp{engine="cluster_connection"}'
    ```

4. If a cluster was waiting, its Secret now shows up in ArgoCD, and the
   audit log has a `cluster_secret_reconcile_tick` entry:

    ```sh
    curl -s -H "Authorization: Bearer $SHARKO_TOKEN" \
      "$SHARKO_URL/api/v1/audit?source=reconciler&limit=20" \
      | jq '.entries[] | {timestamp, event, result, resource}'
    ```

## If that didn't work

Open an issue at <https://github.com/MoranWeissman/sharko/issues>. Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`)
- the log lines from the last hour that mention `clusterreconciler` or `credentials provider`
- what you tried from this page

Related pages:

- [Cluster reconciler](cluster-reconciler.md): what the reconciler does and how often
- [Sharko's background jobs stopped finishing their passes](reconciler-crash-loop.md)
- [Sharko can't reach its secrets backend](secrets-provider-unreachable.md)
- [Sharko can't read the sign-in details for one cluster](single-cluster-credential-fetch-failed.md)

# A cluster's ArgoCD Secret is broken

Use this page when one cluster fails its test because something inside its
ArgoCD cluster Secret is wrong: the server address is empty, the certificate
data is not valid, or the config is not valid JSON. This page is also the one
place that shows how to look at a cluster's ArgoCD Secret without printing the
token or password.

**How urgent:** Fix it soon. Only this one cluster is affected. Sharko can't
change addons on it until the Secret is fixed. Every other cluster keeps
working.

Before you start, set up the shell variables from
[First checks](troubleshooting.md#set-up-once).

## What you see

- **Test** on the cluster (in the UI, or `POST /api/v1/clusters/{name}/test`)
  fails at the first step, **Fetch credentials**. The answer has
  `"error_code": "ERR_AUTH"` and this fixed sentence:

    ```
    Sharko could not read this cluster's sign-in details from the configured credentials source. The server log for this request id says which step failed.
    ```

- The log has this line at level `ERROR`, with `step=fetch-credentials`:

    ```
    [cluster-test] failed
    ```

- Other clusters test fine.

Sharko does not say which field is wrong. On purpose, it never puts the
text of a sign-in error in the API answer or the log, because that text can
hold part of a secret. So you have to look at the Secret yourself. The next
section shows how.

Find the log line like this:

```sh
kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h \
  | grep -E '\[cluster-test\] failed|\[provider\] argocd cluster secret not found'
```

If the log says `[provider] argocd cluster secret not found`, there is no
Secret for this cluster at all. That is a different problem: see
[Sharko can't read the sign-in details for one cluster](single-cluster-credential-fetch-failed.md).

If instead the test answers `503` with an `error_code` like
`argocd_provider_iam_required`, `argocd_provider_exec_unsupported` or
`argocd_provider_unsupported_auth`, the Secret is readable but uses a sign-in
type Sharko can't use. Sharko shows the reason in full in that case. See
[AWS IAM cluster sign-in](aws-iam-cluster-auth.md) or
[Exec-plugin sign-in is not supported](argocd-exec-plugin-auth-unsupported.md).
(`argocd_provider_unsupported_auth` with the words `has no config field` means
the Secret has no `config` key at all. Fix it as shown below.)

If every cluster fails at once, this is not your page. Go to
[Secrets provider unreachable](secrets-provider-unreachable.md).

## Why it happens

Sharko builds a connection to the cluster from three keys in the ArgoCD
cluster Secret: `name`, `server` and `config`. The `config` key is JSON. Any
of these breaks the build:

- **`server` is empty or missing.**
- **`config` is not valid JSON**, for example after a hand edit.
- **Certificate data is not valid base64.** `tlsClientConfig.caData`,
  `certData` or `keyData` inside `config` must each be base64.
- **The token has a hidden character**, such as a newline at the end from a
  copy and paste. Sharko then can't build a working connection from it.

Most of the time the change came from outside Sharko: someone ran
`kubectl edit`, a script or another GitOps tool wrote the Secret with a wrong
value, or a migration tool left a field empty.

### Look at the cluster's ArgoCD Secret

These commands show you what is in the Secret without printing the token,
the password or the private key. Run them from your own machine.

Set the ArgoCD namespace (the Helm value `clusterTest.argocdNamespace`,
`argocd` by default) and the cluster name:

```sh
ARGOCD_NS=argocd
CLUSTER=<cluster-name>
```

Find the Secret. ArgoCD cluster Secrets have the label
`argocd.argoproj.io/secret-type=cluster`. Sharko matches the cluster by the
`name` key first, then by the `server` key:

```sh
SECRET_NAME=$(kubectl -n "$ARGOCD_NS" get secret \
  -l argocd.argoproj.io/secret-type=cluster -o json \
  | jq -r --arg c "$CLUSTER" \
    '.items[] | select((.data.name // "" | @base64d) == $c) | .metadata.name')
echo "$SECRET_NAME"
```

If nothing comes back, list every cluster Secret with its name and server:

```sh
kubectl -n "$ARGOCD_NS" get secret -l argocd.argoproj.io/secret-type=cluster -o json \
  | jq -r '.items[] | [.metadata.name, (.data.name // "" | @base64d), (.data.server // "" | @base64d)] | @tsv'
```

Show the safe fields: the name, the server, the labels, and only the
**names** of the annotations. (Do not print annotation values. The
`kubectl.kubernetes.io/last-applied-configuration` annotation can hold a full
copy of the Secret, token included.)

```sh
kubectl -n "$ARGOCD_NS" get secret "$SECRET_NAME" -o json | jq '{
  secret: .metadata.name,
  name: (.data.name // "" | @base64d),
  server: (.data.server // "" | @base64d),
  has_config: (.data.config != null),
  labels: .metadata.labels,
  annotation_names: ((.metadata.annotations // {}) | keys)
}'
```

Show what is inside `config`, without the values that are secret:

```sh
kubectl -n "$ARGOCD_NS" get secret "$SECRET_NAME" -o json | jq '
  .data.config | @base64d | fromjson | {
    keys: keys,
    tls_keys: ((.tlsClientConfig // {}) | keys),
    token_length: ((.bearerToken // "") | length),
    token_has_spaces_or_newlines: ((.bearerToken // "") | test("\\s")),
    password_set: (.password != null),
    exec_command: (.execProviderConfig.command // null)
  }'
```

If this command fails with a jq error such as `cannot be parsed`, the
`config` JSON itself is broken.

Check that the CA certificate decodes. (The CA is public, so printing its
first line is safe.)

```sh
kubectl -n "$ARGOCD_NS" get secret "$SECRET_NAME" -o json \
  | jq -r '.data.config | @base64d | fromjson | .tlsClientConfig.caData // empty' \
  | base64 --decode | head -1
```

Good: `-----BEGIN CERTIFICATE-----`. Bad: an error from `base64`, or nothing.

To check `certData` or `keyData`, test that they decode but send the result
nowhere. Never print `keyData`:

```sh
kubectl -n "$ARGOCD_NS" get secret "$SECRET_NAME" -o json \
  | jq -r '.data.config | @base64d | fromjson | .tlsClientConfig.keyData // empty' \
  | base64 --decode > /dev/null && echo "keyData decodes"
```

What to look for:

- `server` is empty: the server address is missing.
- `has_config` is `false`: the `config` key is missing.
- `token_has_spaces_or_newlines` is `true`: the token has a hidden character.
- the CA or key does not decode: the certificate data is broken.
- the label `app.kubernetes.io/managed-by: sharko` tells you Sharko wrote
  this Secret. If it is missing, or the Secret has the annotation
  `sharko.dev/adopted`, you (or another tool) look after it.

## How to fix it

1. **If Sharko owns the connection** (label `app.kubernetes.io/managed-by:
   sharko`, and not adopted), let Sharko rewrite it. Sharko reads the sign-in
   details fresh from your secrets backend, changes the Secret in place, and
   never prints a value. Look first, then repair:

    ```sh
    sharko repair-connection "$CLUSTER" --check-only
    sharko repair-connection "$CLUSTER"
    ```

    If the values in your secrets backend are the broken ones, fix them there
    first, then run the repair.

2. **If you look after the Secret yourself**, fix the broken field directly.
   Sharko only adds its addon labels to a connection like this. It does not
   rewrite your sign-in details.

    Put in a missing server address:

    ```sh
    CORRECT_SERVER=https://<cluster-api-server>:443
    kubectl -n "$ARGOCD_NS" patch secret "$SECRET_NAME" --type=merge \
      -p "{\"stringData\":{\"server\":\"$CORRECT_SERVER\"}}"
    ```

    Fix a field inside `config` (here, the token with a newline at the end).
    This rewrites `config` without printing it:

    ```sh
    NEW_CONFIG=$(kubectl -n "$ARGOCD_NS" get secret "$SECRET_NAME" -o json \
      | jq -c '.data.config | @base64d | fromjson
               | .bearerToken |= gsub("\\s"; "")')
    kubectl -n "$ARGOCD_NS" patch secret "$SECRET_NAME" --type=merge \
      -p "$(jq -n --arg c "$NEW_CONFIG" '{stringData: {config: $c}}')"
    ```

    The same pattern works for the CA: set `.tlsClientConfig.caData` to the
    base64 CA from the cluster's own kubeconfig
    (`certificate-authority-data`).

    If another tool (a GitOps repo, Helm, kustomize) writes this Secret, fix
    the value in that tool. Otherwise it puts the broken value back on its next
    run.

3. **If the `config` JSON is beyond repair** and you have a good copy of the
   Secret (in your GitOps repo or a backup), apply that copy. Check first that
   its token, CA and address are still current.

4. **Last try: remove the cluster from Sharko and add it again.** This
   rebuilds the connection from your secrets backend. ArgoCD loses the cluster
   for a short time in between, so prefer steps 1 to 3 for important clusters.

    ```sh
    sharko remove-cluster "$CLUSTER" --cleanup all -y
    sharko add-cluster "$CLUSTER"
    ```

    `--cleanup all` removes the ArgoCD connection only if Sharko owns it.

Sharko reads the Secret again on the next test. There is nothing to restart.

## How to check it's fixed

- Run **Test** on the cluster again. Every step passes, starting with
  **Fetch credentials**:

    ```sh
    curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" \
      "$SHARKO_URL/api/v1/clusters/$CLUSTER/test" | jq '{success, error_code, steps}'
    ```

    You want `"success": true`.

- Run the commands in
  [Look at the cluster's ArgoCD Secret](#look-at-the-clusters-argocd-secret)
  again. Every check comes back good.

- No new `[cluster-test] failed` lines for this cluster in the last few
  minutes.

## If that didn't work

Open an issue at <https://github.com/MoranWeissman/sharko/issues>. Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`)
- the `[cluster-test]` log lines for this cluster
- the output of the safe commands above (never the token, password or key)
- the value of the `app.kubernetes.io/managed-by` label
- what you tried from this page

Related pages:

- [Sharko can't read the sign-in details for one cluster](single-cluster-credential-fetch-failed.md)
- [AWS IAM cluster sign-in](aws-iam-cluster-auth.md)
- [Exec-plugin sign-in is not supported](argocd-exec-plugin-auth-unsupported.md)
- [Self-managed connections](self-managed-connections.md)
- [Cluster reconciler](cluster-reconciler.md)

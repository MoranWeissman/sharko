# Sharko can't reach ArgoCD

Use this page when everything in Sharko that needs ArgoCD fails at the same
time: adding clusters, turning addons on, upgrades, adopting clusters, and the
dashboard pages that read from ArgoCD.

**How urgent:** Fix it now. Nothing is lost, but no change reaches ArgoCD and
the dashboard shows nothing useful until you do.

Before you start, set up the shell variables from
[First checks](troubleshooting.md#set-up-once).

If only one call fails and the rest work, this is not your page. Look up the
error in the [failure mode index](failure-mode-index.md) instead.

## What you see

There are two different messages. They mean different things, so look at which
one you have.

**1. Sharko could not even build its ArgoCD connection.** Most API calls
answer `502` or `503` with this exact body:

```json
{"error":"Sharko has no usable ArgoCD connection. Open Settings and check the active connection: the ArgoCD server address and the ArgoCD token."}
```

(A few calls answer with a short body instead, such as
`{"error":"Service Unavailable","op":"get_active_argocd_client"}`.)

The log has this line at level `WARN`, with `method` and `route` saying which
call asked:

```
no usable ArgoCD connection for the active connection
```

This means there is no active connection, or the active connection has no
ArgoCD token. Sharko never tried to talk to ArgoCD.

**2. Sharko tried, and ArgoCD did not answer.** API calls that need ArgoCD fail
with a server error (`500`, `502` or `504`, depending on the call). The body
is short and names the step that failed, for example
`{"error":"Internal Server Error","op":"list_clusters"}`. **Test ArgoCD** in
**Settings > Connections** shows a message that starts with
`Sharko can't reach ArgoCD`. Where Sharko shows the reason in full, it reads:

```
Sharko could not get an answer from ArgoCD, so it has nothing to show for this. Check the ArgoCD server address and token in Settings, then try again. (code=argocd_no_answer call=GET /api/v1/clusters)
```

The `code` part tells you what kind of failure it was:

| `code` | What it means |
|---|---|
| `argocd_no_answer` | The address did not resolve, the connection was refused, or it dropped |
| `argocd_timed_out` | ArgoCD took too long to answer |
| `argocd_tls_untrusted` | Sharko does not trust ArgoCD's TLS certificate (see [Corporate TLS](corporate-mitm-tls.md)) |
| `argocd_canceled` | The caller gave up (for example, the browser tab closed). Not a problem on its own |

The log has this line at level `ERROR`, once per failed call, with `verb`,
`endpoint` and `error` (the `error` field names the kind of network error, not
the address):

```
argocd call got no answer
```

**If ArgoCD answers but says no**, the log line is instead
`argocd call failed` with a `status` field (`401` or `403`), and the message
says `invalid ArgoCD token` or `ArgoCD access denied`. That is a token problem:
go to [ArgoCD token expired or wrong](argocd-account-token-expired.md).

Other things you may see:

- The `SharkoClusterRegistrationFastBurn` or `SharkoDashboardReadFastBurn`
  alert firing (see [budget-burn-runbook.md](budget-burn-runbook.md)).
- A Kubernetes Warning event with reason `ArgoCDUnreachable` or
  `ArgoCDAuthFailed` in Sharko's namespace (see [sharko-events.md](sharko-events.md)).

Find the log lines like this:

```sh
kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h \
  | grep -E 'argocd call got no answer|argocd call failed|no usable ArgoCD connection'
```

## Why it happens

- **No ArgoCD token on the active connection, or no active connection.**
  Message 1 above. Someone deleted or changed the connection, or setup was
  never finished.
- **ArgoCD itself is down.** The `argocd-server` pod is crashing, not ready, or
  scaled to zero.
- **The address is wrong or out of date.** ArgoCD was moved, renamed or
  reinstalled, and the address Sharko uses no longer points at it.
- **The network blocks Sharko.** A NetworkPolicy in Sharko's namespace or in
  ArgoCD's namespace stops traffic between them. This often shows up after a
  "deny by default" policy is added.
- **TLS.** ArgoCD's certificate is signed by a company or self-made
  certificate authority that the Sharko pod does not trust.

How Sharko finds ArgoCD's address: if the connection has an ArgoCD server URL,
Sharko uses it. If it is empty, Sharko looks for the ArgoCD server Service in
the ArgoCD namespace and uses the first one that answers. Sharko keeps the
address it found until the connection is saved again or the pod restarts. So
after ArgoCD moves, Sharko can keep calling the old address.

## How to fix it

Go through these in order. Stop when the check in the next section passes.

1. **Check the connection in Sharko.** Open **Settings > Connections** in the
   UI and click **Test ArgoCD**. Or run:

    ```sh
    sharko connect test
    ```

    or:

    ```sh
    curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" \
      "$SHARKO_URL/api/v1/connections/test" | jq .argocd
    ```

    If it says there is no connection or no ArgoCD token, add the token in
    **Settings > Connections** (or with `sharko connect --argocd-token ...`).
    If the answer talks about an invalid token or access denied, go to
    [ArgoCD token expired or wrong](argocd-account-token-expired.md).

2. **Check that ArgoCD is running.**

    ```sh
    kubectl -n argocd get pods
    kubectl -n argocd logs deploy/argocd-server --tail=200
    ```

    Every `argocd-*` pod should be `Running` and not restarting. If
    `argocd-server` is not, fix ArgoCD first. Sharko starts working again by
    itself once ArgoCD is back. There is nothing to change in Sharko.

3. **Check the address.** Look at the ArgoCD Services:

    ```sh
    kubectl -n argocd get svc
    ```

    Then compare with the ArgoCD server URL in **Settings > Connections**.
    - If the URL is set and wrong, put in the right one, for example
      `https://argocd-server.argocd.svc.cluster.local`, and save.
    - If you set the URL with the Helm value `connection.argocd.serverURL`,
      change it there and run `helm upgrade`. A change made only in the UI
      is put back to the Helm value.
    - If the URL is empty, Sharko finds the address by itself, but keeps an
      old one until restarted. Restart it:

    ```sh
    kubectl -n "$SHARKO_NS" rollout restart deploy/sharko
    kubectl -n "$SHARKO_NS" rollout status deploy/sharko --timeout=120s
    ```

    If ArgoCD runs in a namespace other than `argocd`, check that the
    connection's ArgoCD namespace (Helm value `connection.argocd.namespace`)
    says so.

4. **Check the network.** List the NetworkPolicies on both sides:

    ```sh
    kubectl -n argocd get networkpolicy
    kubectl -n "$SHARKO_NS" get networkpolicy
    ```

    You can also test the path with a short-lived pod in Sharko's namespace.
    It runs on its own and is deleted when it ends:

    ```sh
    kubectl -n "$SHARKO_NS" run argocd-probe --rm -i --restart=Never \
      --image=curlimages/curl -- \
      curl -s --max-time 10 https://argocd-server.argocd.svc.cluster.local/api/version
    ```

    A JSON answer with `"Version"` in it means the path is open. A timeout or
    "connection refused" means something is in the way. (A NetworkPolicy that
    picks pods by label may treat this test pod differently from the Sharko
    pod, so use it as a hint, not proof.)

    If a policy blocks Sharko, allow it. The Sharko pod has the label
    `app.kubernetes.io/name: sharko`. The `argocd-server` container listens on
    port `8080` behind its Service:

    ```yaml
    apiVersion: networking.k8s.io/v1
    kind: NetworkPolicy
    metadata:
      name: allow-sharko-to-argocd
      namespace: argocd
    spec:
      podSelector:
        matchLabels:
          app.kubernetes.io/name: argocd-server
      ingress:
      - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: sharko   # Sharko's namespace
          podSelector:
            matchLabels:
              app.kubernetes.io/name: sharko
        ports:
        - protocol: TCP
          port: 8080
    ```

    See also the [Network Policy](security.md#network-policy) example for
    Sharko's own namespace.

5. **If the code is `argocd_tls_untrusted`**, follow
   [Corporate TLS](corporate-mitm-tls.md). Turning certificate checks off
   (the **Skip TLS certificate verification** box, or Helm value
   `connection.argocd.insecure=true`) works, but anyone on the network path
   could then read the ArgoCD token. Use it only for test setups.

6. **Last try: stop Sharko and start it again.** This clears anything the old
   pod kept in memory:

    ```sh
    kubectl -n "$SHARKO_NS" scale deploy/sharko --replicas=0
    kubectl -n "$SHARKO_NS" scale deploy/sharko --replicas=1
    kubectl -n "$SHARKO_NS" rollout status deploy/sharko
    ```

    Save the old pod's logs first if you want them for a bug report.

## How to check it's fixed

- **Test ArgoCD** in **Settings > Connections** is green, or the API test
  shows `"status": "ok"`:

    ```sh
    curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" \
      "$SHARKO_URL/api/v1/connections/test" | jq .argocd
    # {"status": "ok"}
    ```

- The clusters list loads again:

    ```sh
    curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/clusters" | jq '.clusters | length'
    ```

- No new log lines in the last few minutes:

    ```sh
    kubectl -n "$SHARKO_NS" logs deploy/sharko --since=5m \
      | grep -E 'argocd call got no answer|no usable ArgoCD connection'
    ```

    No output is good.

Note: `/api/v1/health` does not tell you about ArgoCD. It only says Sharko
itself is up.

## If that didn't work

Open an issue at <https://github.com/MoranWeissman/sharko/issues>. Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`)
- the log lines from the last hour that mention `argocd`
- the output of `sharko connect test`
- what you tried from this page

Related pages:

- [ArgoCD token expired or wrong](argocd-account-token-expired.md)
- [What Sharko needs from ArgoCD](security.md#what-sharko-needs-from-argocd)
- [Git provider unreachable](git-provider-unreachable.md), if Git fails at the same time
- [budget-burn-runbook.md](budget-burn-runbook.md)

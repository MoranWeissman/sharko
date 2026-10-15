# Sharko's ArgoCD token has expired or is wrong

This is the one page for the token Sharko uses to talk to ArgoCD. It
covers a token that expired or is wrong, how to make a new one in ArgoCD,
and how to put the new one into Sharko.

**How urgent:** fix it now. Without a working token, Sharko can't read or
change anything in ArgoCD. The dashboard, the cluster list, addon status,
registering clusters and `sharko init` all fail. The clusters and
applications that are already in ArgoCD keep running.

Before you start, set up the shell variables from
[First checks](troubleshooting.md#set-up-once).

## What you see

In the UI, on the cluster list (and the same way for applications):

> ArgoCD refused Sharko's token while listing clusters. The token may be
> expired or wrong — replace it on the ArgoCD connection in Settings and
> try again.

From the API, a call that needs ArgoCD returns HTTP 502 with a body like
this:

```json
{
  "error": "Bad Gateway",
  "cause": "invalid ArgoCD token — check that the token is correct and not expired",
  "hint": "check the ArgoCD token in Settings → Connections and replace it if it's expired.",
  "code": "ERR_AUTH"
}
```

When Sharko tries to change something in ArgoCD and the token is refused,
the error is this fixed sentence, followed by the code
`argocd_token_invalid`:

> ArgoCD refused this change. Sharko does not repeat ArgoCD's own reply
> here, because that reply quotes whatever ArgoCD was working on — which
> includes the repository address with its access token inside it. Open
> ArgoCD to read the full error.

In the logs, look for ArgoCD calls that failed with status 401 (wrong or
expired token) or 403 (not allowed):

```sh
kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h \
  | grep -E 'argocd call failed|argocd write call failed' \
  | grep -E '"status":(401|403)'
```

Reads log `argocd call failed` with `endpoint` and `status`. Changes log
`argocd write call failed` with `verb`, `endpoint` and `status`. ArgoCD's
own reply is not logged.

The connection test also shows it. Look at the `argocd` part of the
answer:

```sh
curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" \
  "$SHARKO_URL/api/v1/connections/test" | jq .argocd
```

A broken token gives `"status": "error"` and a `message` that starts with
`Sharko can't reach ArgoCD`. Watch out: `sharko connect test` prints "ok"
whenever the request itself worked, even when the ArgoCD part failed. Use
the `curl` command above instead.

### The token works but is not allowed to do something

If the status is 403, the token is fine but the ArgoCD account lacks a
permission. The API answer has the code `ERR_RBAC` and the hint
`grant the token the required permission in ArgoCD, or replace it in Settings → Connections.`
The UI says, for example:

> Sharko's ArgoCD account is not allowed to list clusters. Grant that
> account permission to list clusters in ArgoCD and try again.

Refused changes use the same fixed sentence as above, with the code
`argocd_permission_denied`.

## Why it happens

ArgoCD refuses the token (401) when:

- the token was made with an end date, and that date has passed
- someone deleted the token in ArgoCD, or turned the account off
- ArgoCD's signing key changed, which makes every old token stop working
- the wrong text was pasted in, or the new token went onto a connection
  that is not the active one

ArgoCD accepts the token but says no (403) when the account is not
allowed to do what Sharko asked.

**Where Sharko keeps the token.** The token is part of the active
connection. It is stored encrypted (AES-256-GCM) in the connection Secret,
`sharko-connections` by default (Helm `config.connectionSecretName`), and
decrypted with `SHARKO_ENCRYPTION_KEY`. It is not in the pod spec, and
Helm never sets it. There is no `SHARKO_ARGOCD_TOKEN` environment
variable: that name is not read by any build of Sharko.

The one other place is dev mode. With `SHARKO_DEV_MODE=true` and no token
on the connection, Sharko uses the plain `ARGOCD_TOKEN` environment
variable and logs `argocd: using ARGOCD_TOKEN env var (dev mode fallback)`.
That is for local work only. If there is no token anywhere, the error is
`ArgoCD token not configured. Provide it via Settings UI or set ARGOCD_TOKEN env var with SHARKO_DEV_MODE=true`.

## How to fix it

### Make a new token in ArgoCD

Use a separate ArgoCD account for Sharko, called `sharko`. Do not use the
admin token. If the account does not exist yet, add it to the
`argocd-cm` ConfigMap in ArgoCD's namespace:

```yaml
data:
  accounts.sharko: apiKey
```

Then give it the permissions Sharko needs in `argocd-rbac-cm`. Installing
Sharko does not change that ConfigMap for you. See
[Smallest ArgoCD policy for Sharko](security.md#smallest-argocd-policy-for-sharko)
for the exact policy and how to apply it.

Make the token:

```sh
argocd account generate-token --account sharko
```

To see the tokens the account has and when they end:

```sh
argocd account get --account sharko
```

If your rules say tokens must end, give it an end date with
`--expires-in` (for example `--expires-in 90d`), and put a reminder in
your calendar to make a new one before that date.

If the problem was a 403, you may not need a new token. Give the account
the missing permission in `argocd-rbac-cm` instead.

### Put the new token into Sharko in the UI

1. Open **Settings > Connections**.
2. In the ArgoCD part of the active connection, paste the new token into
   **Token**.
3. Click **Save**.

Sharko checks the connection, stores the token encrypted, and starts
using it at once. You do not need to restart Sharko.

### Put the new token into Sharko from the command line

This changes only the ArgoCD part of the connection. It keeps the Git
token, the secrets provider and the GitOps settings. You must send the
server address and namespace again, or they are cleared. You must also
send `insecure` again, or it goes back to `false`.

First, read the current ArgoCD settings of the active connection:

```sh
CONN=$(curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/connections/" | jq -r .active_connection)

curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/connections/" \
  | jq --arg c "$CONN" '.connections[] | select(.name == $c) | {argocd_server_url, argocd_namespace, argocd_insecure}'
```

Then send them back with the new token. Paste the token when `read`
waits, so it does not end up in your shell history:

```sh
read -rs NEW_ARGOCD_TOKEN

curl -s -X PUT -H "Authorization: Bearer $SHARKO_TOKEN" \
  -H "Content-Type: application/json" \
  "$SHARKO_URL/api/v1/connections/$CONN" \
  -d "$(jq -n --arg url 'https://argocd.example.com' --arg ns 'argocd' \
        --arg tok "$NEW_ARGOCD_TOKEN" --argjson insecure false \
        '{argocd: {server_url: $url, namespace: $ns, token: $tok, insecure: $insecure}}')" | jq
```

Put your own server address, namespace and `insecure` value in place of
the examples. The answer is `{"status":"updated","name":"..."}`. Sharko
reloads the connection at once.

### A warning about `sharko connect`

`sharko connect --name <name> ... --argocd-token <token>` saves the whole
connection again under that name and makes it active. Anything you leave
off the command line is lost. That includes the Git token, the secrets
provider, the GitOps settings and `insecure`. Use `sharko connect` to make
a new connection. To change only the token, use the UI or the `PUT`
command above.

## How to check it's fixed

1. The connection test says ok:

    ```sh
    curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" \
      "$SHARKO_URL/api/v1/connections/test" | jq .argocd
    ```

    You should see `"status": "ok"`.

2. The cluster list loads. This should print `200`:

    ```sh
    curl -s -o /dev/null -w '%{http_code}\n' \
      -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/clusters"
    ```

3. No new 401 or 403 lines show up in the logs:

    ```sh
    kubectl -n "$SHARKO_NS" logs deploy/sharko --since=5m \
      | grep -E 'argocd call failed|argocd write call failed' \
      | grep -E '"status":(401|403)'
    ```

## If that didn't work

Open an issue at <https://github.com/MoranWeissman/sharko/issues>. Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`)
- the log lines from the last hour that mention `argocd call failed` or `argocd write call failed`
- what you tried from this page

Do not paste the token itself into the issue.

Related pages:

- [Sharko can't reach ArgoCD](argocd-upstream-unreachable.md): for errors that are not 401 or 403
- [ArgoCD's own permissions](security.md#argocds-own-permissions): what Sharko is allowed to do in ArgoCD
- [Connections in the user guide](../user-guide/connections.md)

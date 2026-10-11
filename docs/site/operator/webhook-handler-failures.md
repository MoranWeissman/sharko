# Git webhook calls are refused

Your Git host sends a webhook call to Sharko each time someone pushes to the repository. Sharko is turning those calls away. Your Git host's webhook page shows failed deliveries.

**How urgent:** low. The webhook only makes Sharko act sooner. Without it, Sharko still finds the change on its own: addon secrets are delivered on the next run (every 5 minutes by default), and pull requests are checked every 30 seconds.

Before you start, set up the shell variables from [First checks](troubleshooting.md#set-up-once).

## What you see

On your Git host's webhook page (on GitHub: the repository's **Settings**, then **Webhooks**, then **Recent Deliveries**), deliveries fail with one of these answers:

| HTTP code | Body |
|---|---|
| 401 | `{"error":"the Git webhook accepts only a request signed with the shared secret set by an operator"}` |
| 400 | `{"error":"invalid push event payload"}` |
| 400 | `{"error":"could not read request body"}` |

The 401 answer is the same for every reason Sharko refuses a call: no shared secret is set on Sharko, the call has no signature, or the signature does not match. This is on purpose, so an outsider can't learn how Sharko is set up. You tell the reasons apart with the steps below, not from the answer.

When a call works, Sharko answers 200 and logs this for a push to the base branch:

```sh
kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h | grep 'External push detected'
```

If deliveries fail with 502, 503, 504 or a connection error, the call is not reaching Sharko at all. Check your Ingress and that the pod is running, using [First checks](troubleshooting.md).

## Why it happens

The webhook address is `POST /api/v1/webhooks/git`. It needs no Sharko login. The signature is the only thing that proves the call is real, so Sharko checks it every time:

- Sharko reads the shared secret from `SHARKO_WEBHOOK_SECRET`. You set it with the Helm value `secrets.webhookSecret`, and the chart puts it in its own Secret.
- If that value is empty, the webhook is closed and every call is refused. This is how a fresh install starts. It is not a fault.
- The Git host signs each call with the same shared secret and sends the result in the `X-Hub-Signature-256` header. Sharko works the signature out again and compares. The two secrets must match exactly.
- After the signature passes, a GitHub `ping` call (`X-GitHub-Event: ping`) gets `{"status":"pong"}`. Anything else must be JSON.

So the usual causes are:

- **The secret was changed on one side only.** Deliveries worked up to a point in time, then all fail with 401.
- **The webhook on the Git host has no secret.** Every delivery fails with 401.
- **`secrets.webhookSecret` was never set.** Every delivery fails with 401.
- **The webhook sends form data instead of JSON.** On GitHub, the webhook's **Content type** is set to `application/x-www-form-urlencoded`. Deliveries fail with 400 `invalid push event payload`.
- **A proxy or Ingress in front of Sharko changes the body.** The signature was made over the original body, so it no longer matches. Deliveries fail with 401.

Sharko reads GitHub's headers. Another Git host must send a signature in the same `X-Hub-Signature-256` header, or its calls are always refused.

## How to fix it

### Check whether Sharko has a shared secret

The chart's Secret is named after the Helm release (`sharko` when the release is `sharko`):

```sh
kubectl -n "$SHARKO_NS" get secret sharko -o json \
  | jq -r '.data.SHARKO_WEBHOOK_SECRET // "NOT SET"'
```

`NOT SET` means the webhook is closed. If a value comes back, decode it with `base64 --decode` to compare it with the Git host side.

### Set a new shared secret on both sides

This fixes every 401 cause except a proxy that changes the body.

1. Make a new random secret:

   ```sh
   NEW_SECRET=$(openssl rand -hex 32)
   echo "$NEW_SECRET"
   ```

2. Set it in your Helm values and upgrade the release:

   ```yaml
   secrets:
     webhookSecret: "<the new secret>"
   ```

   ```sh
   helm upgrade sharko <chart> -n "$SHARKO_NS" -f values.yaml
   ```

   The upgrade changes the chart's Secret, and the pod restarts on its own to pick it up. Don't patch the Secret by hand: the next `helm upgrade` puts the old value back.

3. On your Git host, edit the webhook and paste the same value into **Secret**. On GitHub, also check that **Content type** is `application/json`. Save.

4. On the Git host, pick a recent delivery and redeliver it.

### Fix the content type

If deliveries fail with 400 `invalid push event payload`, edit the webhook on your Git host and set **Content type** to `application/json`. Then redeliver.

### Test the signature yourself

This tells you whether Sharko's copy of the secret is the one you think it is, and whether a proxy is changing the body. Sign a small body with the secret and send it from your own machine:

```sh
read -rs SECRET   # paste the shared secret, then press Enter
BODY='{"ref":"refs/heads/not-a-real-branch","commits":[]}'
SIG="sha256=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$SECRET" -r | cut -d' ' -f1)"

curl -s -i -X POST "$SHARKO_URL/api/v1/webhooks/git" \
  -H "Content-Type: application/json" \
  -H "X-GitHub-Event: push" \
  -H "X-Hub-Signature-256: $SIG" \
  --data-binary "$BODY"
```

The branch name does not exist, so Sharko does nothing with the call. It only checks the signature.

- **200 with `{"status":"ok"}`:** Sharko's secret is right. If real deliveries still fail, the Git host's secret is different, or something between the Git host and Sharko changes the body.
- **401:** Sharko's secret is not the value you used. Set a new one on both sides as above.

### A proxy or Ingress changes the body

If your test works through `$SHARKO_URL` but real deliveries still fail with 401, check what sits between your Git host and Sharko. The body must reach Sharko exactly as the Git host sent it. Remove any rule that rewrites the body.

## How to check it's fixed

- The newest deliveries on your Git host's webhook page show 200.
- Sharko logs the push:

  ```sh
  kubectl -n "$SHARKO_NS" logs deploy/sharko --since=10m | grep 'External push detected'
  ```

- The audit log shows push entries from the webhook:

  ```sh
  curl -s -H "Authorization: Bearer $SHARKO_TOKEN" \
    "$SHARKO_URL/api/v1/audit?source=webhook&limit=5" \
    | jq '.entries[] | {timestamp, event, resource, result}'
  ```

## If that didn't work

Open an issue at <https://github.com/MoranWeissman/sharko/issues>. Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`)
- the log lines from the last hour that mention `webhook` or `External push detected`
- the HTTP code and body of a failed delivery from your Git host (never the shared secret)
- which Git host you use
- what you tried from this page

Related pages:

- [Sharko can't reach your Git host](git-provider-unreachable.md)
- [Configuration](configuration.md)
- [Security](security.md)
- [Audit log](audit-log.md)

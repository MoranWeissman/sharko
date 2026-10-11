# Someone may be getting into Sharko without a real sign-in

This page is for when you think Sharko lets someone in who should be refused: a wrong password
works, a revoked or made-up API token works, or a call with no token at all works.

**How urgent:** Act now. This is a security problem. Anyone who gets in can register and remove
clusters, turn addons on and off, and push secrets to your clusters.

Before you start, set up the shell variables from [First checks](troubleshooting.md#set-up-once).

**If you think this is real, do these four things first, in this order:**

1. Save the evidence (step 1 below). Sharko keeps its activity history in memory only, so it is
   gone the moment the pod stops.
2. Stop Sharko: scale it to 0 (step 2).
3. Report it privately as described in
   [SECURITY.md](https://github.com/MoranWeissman/sharko/blob/main/SECURITY.md). Do not open a
   public issue.
4. Change every password and token Sharko holds (step 4).

## What you see

What Sharko should do, and what tells you something is wrong:

| You send | Sharko should answer | Something is wrong if |
|---|---|---|
| A wrong password to `POST /api/v1/auth/login` | HTTP 401 `invalid credentials` (HTTP 429 after 5 tries in a minute from one address) | HTTP 200 with a `token` |
| A call to `/api/v1/clusters` with no token | HTTP 401 `unauthorized` | HTTP 200 |
| A made-up or revoked API token | HTTP 401 `unauthorized`, right away | HTTP 200 |
| An expired API token | HTTP 401 that names the token and says it expired | HTTP 200 |

Other signs:

- The activity history (the **Audit Log** page in the UI, or `GET /api/v1/audit`) shows `login`, `token_created`,
  `user_created`, `user_updated`, `password_reset` or `connection_updated` entries that nobody on
  your team did.
- Clusters were registered or removed, or addons turned on or off, and nobody claims the change.
- Pull requests from Sharko in your GitOps repo that nobody asked for.

Sharko has no metric that counts failed sign-ins. `sharko_auth_login_total` is listed in the code
but Sharko never adds to it, so it stays empty. Use the activity history instead.
`sharko_active_sessions` does work: it shows how many people are signed in right now.

## Why it happens

- **A bug in Sharko's sign-in checks.** This is a code problem, not a setting. Only a fixed Sharko
  version closes it.
- **A password or token leaked.** Someone has a real password or a real API token. Sharko is
  working as it should, but the wrong person holds the key.
- **Sharko is open to more of the network than you think.** For example, an Ingress with no
  limit on who can reach it.

Sharko itself refuses every call to `/api/` without a valid sign-in, except `/api/v1/health`,
`/api/v1/auth/login` and the Git webhook. The Git webhook refuses every call unless it carries a
valid signature made with the webhook secret.

## How to fix it

1. **Save the evidence before you change anything.** Keep these files somewhere safe and private.

   ```sh
   mkdir sharko-incident && cd sharko-incident
   kubectl -n "$SHARKO_NS" logs deploy/sharko --since=24h > sharko-logs.json
   curl -s -H "Authorization: Bearer $SHARKO_TOKEN" \
     "$SHARKO_URL/api/v1/audit?limit=1000" > audit.json
   curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/tokens" > tokens.json
   curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/users" > users.json
   ```

   The activity history holds the last 1000 entries and is lost when the pod stops. The token
   list has no token values in it. Do not save `kubectl get secrets -o yaml`: that prints every
   secret in clear text.

2. **Stop Sharko.** This ends every open session, because sessions live only in memory.

   ```sh
   kubectl -n "$SHARKO_NS" scale deploy/sharko --replicas=0
   ```

3. **Report it privately** as described in
   [SECURITY.md](https://github.com/MoranWeissman/sharko/blob/main/SECURITY.md). Say which check in
   the table above failed, your Sharko version, and what the activity history shows.

4. **Change every password and token Sharko holds.** Treat all of them as known to the attacker.

   - **The admin password.** This works while Sharko is stopped. It writes a new random password
     and prints it:

     ```sh
     sharko reset-admin --namespace "$SHARKO_NS" --secret sharko
     ```

   - **Other users' passwords.** Reset them in **Settings > Users** once Sharko is back. Remove
     users nobody knows (`sharko user list`, `sharko user delete`).
   - **API tokens.** These are saved, so a restart does not remove them. Once Sharko is back,
     revoke every one (`sharko token list`, then `sharko token revoke <name>`), and make new ones
     only for what you really need.
   - **The Git token** on the connection. Make a new token on your Git host, remove the old one
     there, then see [Put in a new Git token](git-provider-unreachable.md#put-in-a-new-git-token).
   - **The ArgoCD token** on the connection. See
     [argocd-account-token-expired.md](argocd-account-token-expired.md).
   - **The secrets store access**, for example the AWS role or keys Sharko uses. Change them in
     your cloud account.
   - **The webhook secret** (`secrets.webhookSecret` in your Helm values). Set a new one, then put
     the same value in the webhook settings on your Git host.
   - **The AI provider key** (`ai.apiKey`), if you use AI.

5. **Close the hole before you start Sharko again.**

   - If a wrong password, a missing token or a revoked token got in, this is a Sharko bug.
     Upgrade to a version that fixes it (see
     [upgrading-and-rollback.md](upgrading-and-rollback.md)). Until there is one, keep Sharko at 0.
   - If a real password or token leaked, step 4 already closes it.
   - Limit who can reach Sharko: a private Ingress, or no Ingress and only `kubectl port-forward`.

6. **Start Sharko again**, still with only one pod:

   ```sh
   kubectl -n "$SHARKO_NS" scale deploy/sharko --replicas=1
   kubectl -n "$SHARKO_NS" rollout status deploy/sharko --timeout=120s
   ```

7. **Undo what the attacker did.** Go through the saved `audit.json`, oldest first. For each
   cluster, addon, user or connection change nobody claims, put it back. Also check your own audit
   trails outside Sharko for the same time window: your Git host, ArgoCD, your cloud account and
   your Kubernetes clusters. Tell the teams whose clusters Sharko manages, so they can check their
   own audit trails too.

## How to check it's fixed

Run each check from the table above. All of them must answer 401.

```sh
# A wrong password
curl -s -o /dev/null -w '%{http_code}\n' -X POST "$SHARKO_URL/api/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"wrong-on-purpose"}'

# No token at all
curl -s -o /dev/null -w '%{http_code}\n' "$SHARKO_URL/api/v1/clusters"

# A made-up token
curl -s -o /dev/null -w '%{http_code}\n' \
  -H "Authorization: Bearer sharko_notarealtoken" "$SHARKO_URL/api/v1/clusters"

# A token you just revoked (there is no wait: revoking works at once)
TEST_TOKEN=$(curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" \
  -H 'Content-Type: application/json' -d '{"name":"revoke-check","role":"viewer"}' \
  "$SHARKO_URL/api/v1/tokens" | jq -r .token)
curl -s -X DELETE -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/tokens/revoke-check"
curl -s -o /dev/null -w '%{http_code}\n' \
  -H "Authorization: Bearer $TEST_TOKEN" "$SHARKO_URL/api/v1/clusters"
```

If any of them answers 200, scale Sharko back to 0 and add that to your private report.

Then, for the next few days, look at the activity history every day for `login` and
`token_created` entries you can't explain.

## If that didn't work

Do not open a public issue. Report it privately as described in
[SECURITY.md](https://github.com/MoranWeissman/sharko/blob/main/SECURITY.md). Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`)
- which check from the table failed, and what it answered
- the activity history entries you can't explain
- what you tried from this page

Never put a password, a token or the data of a Secret into the report.

Related pages:

- [A password or token showed up in the logs](credential-leak-in-logs.md)
- [Security](security.md)
- [Activity history](audit-log.md)

# A password or token showed up in Sharko's logs

This page is for when you find a real password, token, kubeconfig or other secret in Sharko's log
lines, in clear text.

**How urgent:** Act now. This is a security problem. Treat the secret as known to anyone who can
read those logs, in every place the logs were copied to.

Before you start, set up the shell variables from [First checks](troubleshooting.md#set-up-once).

**First, the one case that is expected.** On the very first start, when Sharko makes the admin
password for you, it prints it once to the log in a block like this:

```text
=== BOOTSTRAP ADMIN CREDENTIAL ===
username: admin
password: <the password>
...
=== END BOOTSTRAP ADMIN CREDENTIAL ===
```

(or `=== INITIAL ADMIN CREDENTIAL (GENERATED) ===`). This is on purpose, so you can sign in the
first time. If your logs are sent to a log store, that password is now stored there too. Change it
after your first sign-in with `sharko reset-admin` (see step 3). This is not a bug, and you do not
need to report it.

## What you see

Sharko replaces secret values in its log lines with `[REDACTED]`. It does this for:

- fields with names like `token`, `password`, `kubeconfig`, `secret`, `pat`, `authorization`,
  `api_key`, and any name ending in `_token`, `_password`, `_secret` or `_key`
- values that look like a JWT (they start with `eyJ`)
- long values (over 100 characters) made only of base64 characters
- web addresses with a user name or password in them (`https://<token>@host/...`); Sharko writes
  the address without that part
- error messages, which are replaced by a short description of the kind of error. Errors from a
  secrets store are replaced by one fixed sentence.

Something is wrong if a log line shows a real value instead. Search the last day of logs from your
own machine:

```sh
kubectl -n "$SHARKO_NS" logs deploy/sharko --since=24h > sharko-logs.txt

grep -nE 'eyJ[A-Za-z0-9_-]{20,}\.' sharko-logs.txt          # JWTs (ArgoCD tokens and others)
grep -nE '(ghp_|github_pat_|ghs_)[A-Za-z0-9_]{20,}' sharko-logs.txt   # GitHub tokens
grep -nE 'sharko_[0-9a-f]{32}' sharko-logs.txt              # Sharko API tokens
grep -nE 'k8s-aws-v1\.' sharko-logs.txt                     # EKS sign-in tokens
grep -nE 'apiVersion: v1|current-context:|client-key-data' sharko-logs.txt   # kubeconfigs
grep -nE 'https?://[^/ "]*:[^/ "]*@' sharko-logs.txt        # passwords inside web addresses
```

Leave out the bootstrap block above. Any other hit is a leak.

## Why it happens

- **A log line uses a field name that Sharko does not see as secret**, and the value has a shape
  none of the checks above catch. A short GitHub token in a field called `creds` is one example.
- **A secret sits inside a longer text**, for example a sentence that a library put together, so
  the field as a whole does not look like a secret.
- **A log line that is not JSON.** Sharko's own log lines are JSON. Text printed by other means
  (like the bootstrap block) does not go through these checks.

All three are bugs in Sharko except the bootstrap block. You can't fix them with a setting.

## How to fix it

1. **Save one example, without the secret.** Copy one bad log line and replace the secret part
   with `<REMOVED>`. Write down the `time`, `msg` and `request_id` fields, and the first and last
   time it shows up. Do not keep the real value anywhere.

2. **Work out what leaked**, from the field name or the shape of the value:

   | What you found | What it is | What to change |
   |---|---|---|
   | `ghp_...`, `github_pat_...`, a token in a Git address, or a Git token field | The Git token on the connection | Make a new token on your Git host, delete the old one there, then see [Put in a new Git token](git-provider-unreachable.md#put-in-a-new-git-token) |
   | `eyJ...` in an ArgoCD call, or an `argocd_token` field | The ArgoCD account token | See [argocd-account-token-expired.md](argocd-account-token-expired.md) |
   | `sharko_` followed by 32 letters and numbers | A Sharko API token | `sharko token list`, then `sharko token revoke <name>`, and make a new one |
   | A 64-character hex value in a `token` or `Authorization` field | A sign-in session | Restart Sharko (step 4). Sessions live only in memory, so a restart ends all of them |
   | A `password` field | A Sharko user's password | Step 3 |
   | `k8s-aws-v1....` | An EKS sign-in token | It stops working on its own about 15 minutes after it was made. Nothing to change, but report it |
   | A kubeconfig, `client-key-data`, a cluster bearer token | One cluster's sign-in details | Change them where they come from (your secrets store or cloud account). Then check Sharko can read the new ones: [One cluster's sign-in details can't be read](single-cluster-credential-fetch-failed.md) |
   | An addon secret value | The value in your secrets store | Change it there, then run `sharko refresh-secrets` |
   | An `api_key` near an AI call | The AI provider key | Make a new key with your AI provider and put it in `ai.apiKey` or **Settings > AI Provider** |
   | A webhook `secret` | The webhook secret | Set a new `secrets.webhookSecret` and put the same value on your Git host's webhook |

3. **To change a user's password:** for the admin user, run this from your own machine. It writes a
   new random password and prints it. Sharko does not need a restart.

   ```sh
   sharko reset-admin --namespace "$SHARKO_NS" --secret sharko
   ```

   You can read the new password again later:

   ```sh
   kubectl -n "$SHARKO_NS" get secret sharko-initial-admin-secret -o json \
     | jq -r '.data.password' | base64 --decode
   ```

   For other users, reset the password in **Settings > Users**.

4. **Restart Sharko if a session token leaked**, or once you have changed everything else:

   ```sh
   kubectl -n "$SHARKO_NS" rollout restart deploy/sharko
   ```

5. **Clean the logs where you can.** Sharko writes its logs to standard output only. Kubernetes keeps
   them on the node while the pod exists, and removes them when the pod is gone. Copies already sent
   to a log store (Loki, Elasticsearch, Splunk, Datadog, an S3 bucket ...) stay there. Ask the team
   that runs each store to delete the lines from that time window. This is only a best effort: the
   change in step 2 is what really protects you.

6. **Report it privately.** Do not open a public issue. Report it as described in
   [SECURITY.md](https://github.com/MoranWeissman/sharko/blob/main/SECURITY.md).

## How to check it's fixed

- Run the searches under "What you see" again on logs from after the restart:

  ```sh
  kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h > sharko-logs-after.txt
  ```

  Only `[REDACTED]` should show where the secret was. The leak may come back each time the same
  action runs, until a fixed Sharko version is out. Keep checking after each upgrade.
- Try the old secret where it was used (the Git host, ArgoCD, the Sharko API). It must be refused.
- Sharko still works with the new one: the connection test in **Settings > Connection** passes,
  and `sharko token list` shows only the tokens you meant to keep.

## If that didn't work

Do not open a public issue. Report it privately as described in
[SECURITY.md](https://github.com/MoranWeissman/sharko/blob/main/SECURITY.md). Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`)
- one bad log line with the secret replaced by `<REMOVED>`, and the first and last time it showed up
- what kind of secret it was (Git token, ArgoCD token, kubeconfig ...)
- what you tried from this page

Never put the real secret into the report.

Tell the teams whose clusters or secrets stores the leaked secret could reach, so they can check
on their side too.

Related pages:

- [Someone may be getting into Sharko without a real sign-in](auth-bypass.md)
- [Security](security.md)

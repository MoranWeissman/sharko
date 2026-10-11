# Sharko can't reach your Git host

Sharko keeps everything it manages in a Git repository. When it can't talk to your Git host, or the host turns its token away, every action that writes to Git fails and pages that read from Git stop loading.

**How urgent:** high. Adding clusters, turning addons on and off, and upgrades all stop until this is fixed. Clusters and addons that are already running keep running.

Before you start, set up the shell variables from [First checks](troubleshooting.md#set-up-once).

This page covers GitHub, Azure DevOps and Gitea, the three Git hosts Sharko supports.

## What you see

- The UI or the API answers with this message (HTTP 502, on some pages 503):

  ```text
  Sharko has no usable Git connection. Open Settings and check the active connection: the Git provider, the repository it points at, and the access token.
  ```

- Or an action fails part way with HTTP 502 and a message that starts with the step that failed, for example `creating branch "…": …` or `creating pull request: …`.
- Read pages fail with HTTP 502 (the host refused the connection or the name did not resolve), 504 (the call timed out) or 500.
- The logs show one of these lines:

  ```sh
  kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h | grep 'no usable Git connection for the active connection'
  kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h | grep '"msg":"server error"'
  kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h | grep '\[prtracker\] failed to poll PR'
  ```

  The first line only names the request (`method` and `route`). It does not say what is wrong with the connection, so use the test below to find out.

If the error mentions a rate limit, or the answer is HTTP 429, go to [Your Git host is limiting Sharko's calls](git-provider-rate-limited.md) instead.

## Why it happens

Sharko builds a Git client from the active connection each time it needs one. That fails when:

- there is no active connection, or its token is empty. The error then says `GitHub token not configured`, `Azure DevOps PAT not configured` or `Gitea token not configured`;
- the repository URL can't be read;
- the token has expired, was taken back, or does not have the rights it needs on the repository;
- your Git host is down, or Sharko's pod can't reach it over the network (a firewall, a proxy, a DNS problem, or a company proxy that swaps TLS certificates).

## How to fix it

### Find out what is wrong

Ask Sharko to test the active connection. Use `curl` and read the Git half of the answer. Don't rely on `sharko connect test` for this: it prints "ok" whenever the API answers, even when the Git test failed.

```sh
curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" \
  "$SHARKO_URL/api/v1/connections/test" | jq '.git'
```

`"status": "ok"` means the Git side works. Otherwise read `message`:

| The message says | What it means | What to do |
|---|---|---|
| `the Git host rejected the credentials (HTTP 401)` | The token is expired or wrong | [Put in a new Git token](#put-in-a-new-git-token) |
| `accepted the credentials but denied the requested operation` | The token works but can't do this on the repository | Give the token write rights on the repository, or put in a new one |
| `the host could not be reached` | Network or DNS problem | [Check the network path](#check-the-network-path) |
| `a certificate error occurred` | TLS problem, often a company proxy | See [Company proxy and TLS](corporate-mitm-tls.md) |
| `the request timed out` | Slow or blocked network | [Check the network path](#check-the-network-path) |
| `Check the connection settings and try again` | Something else | Check the provider and the repository URL in Settings |

### Check the token from your own machine

Test the same token straight against your Git host. Put the token in a shell variable first, so it never sits inside a URL.

```sh
read -rs GIT_TOKEN   # paste the token, then press Enter

# GitHub
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $GIT_TOKEN" \
  "https://api.github.com/repos/<owner>/<repo>"

# Azure DevOps
curl -s -o /dev/null -w '%{http_code}\n' -u ":$GIT_TOKEN" \
  "https://dev.azure.com/<org>/<project>/_apis/git/repositories/<repo>?api-version=7.0"

# Gitea
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: token $GIT_TOKEN" \
  "https://<gitea-host>/api/v1/repos/<owner>/<repo>"
```

`200` means the token can see the repository. `401` means the token is no good. `404` often means the token can't see this repository at all. Sharko also creates branches, opens pull requests and merges them, so the token needs write rights, not only read.

### Put in a new Git token

The Git token lives on the connection. Sharko stores it encrypted in the `sharko-connections` Secret. It is not a Helm value, so changing your Helm values does not change it.

**In the UI (works for all three hosts):**

1. Open **Settings**, then **Connection**.
2. Under **Git Repository**, paste the new token into **Token**. If you leave the field empty, the old token is kept.
3. Click **Test Git**. It should say **Connected**.
4. Click **Update Connection**. It should say **Saved**.

**With the API (changes only the token):**

```sh
# Find the name of the active connection.
CONN=$(curl -s -H "Authorization: Bearer $SHARKO_TOKEN" \
  "$SHARKO_URL/api/v1/connections/" | jq -r '.active_connection')

# GitHub or Gitea:
jq -n --arg t "$GIT_TOKEN" '{git: {token: $t}}' \
  | curl -s -X PUT -H "Authorization: Bearer $SHARKO_TOKEN" \
      -H "Content-Type: application/json" --data-binary @- \
      "$SHARKO_URL/api/v1/connections/$CONN" | jq

# Azure DevOps uses "pat" instead of "token":
jq -n --arg t "$GIT_TOKEN" '{git: {pat: $t}}' \
  | curl -s -X PUT -H "Authorization: Bearer $SHARKO_TOKEN" \
      -H "Content-Type: application/json" --data-binary @- \
      "$SHARKO_URL/api/v1/connections/$CONN" | jq
```

Every other field keeps its saved value. Sharko starts using the new token right away. You don't need to restart it.

**With the CLI:** `sharko connect` saves the whole connection again, so you have to pass every value, including the ArgoCD token:

```sh
sharko connect --name <connection-name> --git-provider github \
  --git-repo <repo-url> --git-token "$GIT_TOKEN" \
  --argocd-url <argocd-url> --argocd-token "$ARGOCD_TOKEN"
```

Use the CLI for GitHub only. For Azure DevOps and Gitea, use the UI or the API call above.

If the ArgoCD half of the test fails, see [ArgoCD account token expired](argocd-account-token-expired.md).

### Check the network path

Sharko's image has no shell tools, so you can't test from inside the pod. Check these from outside instead:

- Your Git host's status page, in case it is down.
- Any NetworkPolicy, firewall or egress proxy between the Sharko namespace and your Git host. Sharko needs outbound HTTPS (port 443) to the host.
- If your company proxy swaps TLS certificates, follow [Company proxy and TLS](corporate-mitm-tls.md).

If you changed something in the network, scale Sharko to 0 and back to 1 so it starts clean. Sharko runs as one copy only, so don't add more replicas.

```sh
kubectl -n "$SHARKO_NS" scale deploy/sharko --replicas=0
kubectl -n "$SHARKO_NS" scale deploy/sharko --replicas=1
```

## How to check it's fixed

- The connection test shows `ok` for Git:

  ```sh
  curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" \
    "$SHARKO_URL/api/v1/connections/test" | jq -r '.git.status'
  ```

- The action that failed works when you try it again.
- No new `no usable Git connection` or `failed to poll PR` lines appear:

  ```sh
  kubectl -n "$SHARKO_NS" logs deploy/sharko --since=10m | grep -E 'no usable Git connection|failed to poll PR'
  ```

## If that didn't work

Open an issue at <https://github.com/MoranWeissman/sharko/issues>. Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`)
- the log lines from the last hour that mention Git, the connection or `prtracker`
- the `.git` part of the connection test answer (it holds no token)
- what you tried from this page

Related pages:

- [Your Git host is limiting Sharko's calls](git-provider-rate-limited.md)
- [Auto-merge failed after the pull request opened](auto-merge-failed-after-pr-opened.md)
- [Connection doctor](connection-doctor.md)
- [Self-managed connections](self-managed-connections.md)

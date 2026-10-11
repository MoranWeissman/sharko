# Your Git host is limiting Sharko's calls

Git hosts cap how many API calls one token can make in an hour. When Sharko goes over that cap, the host turns its calls away until the hour resets. Reads and writes both fail, but nothing is lost, and things recover on their own once the cap resets.

**How urgent:** medium. Writes fail for a while, and background jobs skip their runs until the limit resets. Nothing already running on your clusters is changed.

Before you start, set up the shell variables from [First checks](troubleshooting.md#set-up-once).

## What you see

- API calls that read from Git answer with HTTP 429. Sharko uses 429 when the host's error mentions "rate limit", "too many requests" or "429".
- Actions that write to Git fail with HTTP 502, and the message holds the host's rate-limit error.
- Reading files from GitHub fails in the logs:

  ```sh
  kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h | grep 'github get file content failed'
  ```

- The cluster reconciler gives up on its run:

  ```sh
  kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h \
    | grep 'git read failed — aborting tick (no state mutated)'
  ```

- The audit log shows failed Git reads by the reconciler:

  ```sh
  curl -s -H "Authorization: Bearer $SHARKO_TOKEN" \
    "$SHARKO_URL/api/v1/audit?action=git_read&source=reconciler&result=failure&limit=20" \
    | jq '.entries[] | {timestamp, event, detail}'
  ```

### Why the log names a file you don't have

On a repository in the v4 layout, these errors often name `configuration/managed-clusters.yaml`, a file your repository does not have. That is expected. The cluster reconciler always asks for that older v3 path first. Only when the host says "not found" does it go on to `managed-clusters.yaml` at the root of the repository. When the host is limiting calls, the very first request is the one that fails, so the error is logged against the v3 path. The path is not the problem. The rate limit is.

## Why it happens

Sharko signs in to your Git host with one token. Every call it makes counts against that token's limit. On GitHub, a personal token gets 5,000 API calls an hour.

Sharko makes calls on its own, even when nobody is using it:

| What runs | How often |
|---|---|
| The cluster reconciler reads the managed clusters file | every 30 seconds (fixed, can't be changed) |
| The pull request tracker checks each open pull request | every 30 seconds (`SHARKO_PR_POLL_INTERVAL`) |
| The connection check | every 60 seconds |
| Addon secret delivery | every 5 minutes (`SHARKO_SECRET_RECONCILE_INTERVAL`) |
| The credential check | every 15 minutes |

On top of that come the calls people make: opening pages, adding clusters, turning addons on. Sharko makes its Git writes one at a time, so a large batch of work takes longer but doesn't make many calls at the same moment.

Common reasons for going over the limit:

- **Many clusters added at once.** Each one means branches, commits and a pull request.
- **Many open pull requests.** The tracker checks every open one every 30 seconds. Pull requests that stay open for a long time keep using calls.
- **The same token used somewhere else.** Another Sharko install, a CI job or a script using the same token shares the same limit.

## How to fix it

### Check how much of the limit is left

For GitHub, ask from your own machine using the same token Sharko uses. This call does not count against the limit.

```sh
read -rs GIT_TOKEN   # paste the token, then press Enter

curl -s -H "Authorization: Bearer $GIT_TOKEN" https://api.github.com/rate_limit \
  | jq '.resources.core | {limit, remaining, resets_at: (.reset | todate)}'
```

`resets_at` is the time (in UTC) when the limit fills up again. If `remaining` is close to 0, this page is the right one. If it is high, the problem is something else: see [Sharko can't reach your Git host](git-provider-unreachable.md).

Azure DevOps and Gitea limit calls in their own ways. Check your host's own admin pages or docs for how much is left.

### Wait for the reset

The simplest fix. When the limit resets, the background jobs pick up again on their next run and catch up. You don't need to restart Sharko. Hold off on large batches of work until then.

### Close or merge pull requests that have been open a long time

Every open pull request Sharko tracks costs calls every 30 seconds. List them:

```sh
sharko pr list --status open
```

Merge or close the ones you no longer need on your Git host. Then let Sharko see the change:

```sh
sharko pr refresh <id>
```

### Give Sharko its own token

If another tool uses the same token, make a new token just for Sharko and put it in. See [Put in a new Git token](git-provider-unreachable.md#put-in-a-new-git-token).

### Check the tracker more slowly

If you often have many open pull requests, you can make the tracker check less often. Set `SHARKO_PR_POLL_INTERVAL` (for example `2m`) under `extraEnv` in your Helm values, then upgrade the release:

```yaml
extraEnv:
  - name: SHARKO_PR_POLL_INTERVAL
    value: "2m"
```

 The new value takes effect when the pod restarts.

### Add clusters in smaller groups

When you add many clusters, add them in small groups and wait for each group's pull requests to merge before you start the next.

## How to check it's fixed

- `remaining` from the GitHub check above is going up, or is back near the full limit.
- No new rate-limit lines appear:

  ```sh
  kubectl -n "$SHARKO_NS" logs deploy/sharko --since=10m \
    | grep -E 'github get file content failed|git read failed — aborting tick'
  ```

- The action that failed works when you try it again.

## If that didn't work

Open an issue at <https://github.com/MoranWeissman/sharko/issues>. Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`)
- the log lines from the last hour that mention `github get file content failed`, `git read failed` or `prtracker`
- the `limit`, `remaining` and `resets_at` values from the rate limit check (they hold no secret)
- about how many clusters and open pull requests you have
- what you tried from this page

Related pages:

- [Sharko can't reach your Git host](git-provider-unreachable.md)
- [Cluster reconciler](cluster-reconciler.md)
- [Audit log](audit-log.md)

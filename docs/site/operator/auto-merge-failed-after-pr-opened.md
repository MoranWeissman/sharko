# Auto-merge failed after the pull request opened

Sharko opened a pull request in your Git repository, then tried to merge it on its own and the Git host said no. The pull request is still open. Nothing is lost, but the change does not take effect until someone merges it.

**How urgent:** medium. The change you asked for is waiting in an open pull request. Once it is merged, things carry on as normal.

Before you start, set up the shell variables from [First checks](troubleshooting.md#set-up-once).

## What you see

- The action answers with HTTP 207 and `"status": "partial"`. The message is one of these, with the pull request link at the end:
  - `Sharko opened the pull request that registers this cluster, but could not merge it. Merge it by hand to finish the registration. … Pull request: <url>` (adding a cluster). Between the two parts, the message says what Sharko already did: how many addon secrets it created or could not create, and whether it already saved the cluster's connection in ArgoCD.
  - `PR created but merge failed: <url>` (addon changes, removing, letting go of a cluster)
  - `PR created but merge failed for cluster <name>: <url>` (adopting a cluster)
  - `Pull request opened for cluster <name> but auto-merge failed. Merge manually: <url>` (changing a cluster's addons)
- The `error` field holds the Git host's reason, shaped like `PR created but merge failed: merge pull request #12: <reason>`.
- When adding a cluster, `failed_step` is `pr_merge` and the `git` part holds `pr_url`, `pr_id`, `branch` and `merged: false`.
- The logs show it:

  ```sh
  kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h | grep 'PR opened but auto-merge failed'
  kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h | grep 'merge failed'
  ```

- During first-time setup, the setup run is marked failed with `PR auto-merge failed: …`.

If `failed_step` is `git_commit` instead, the pull request was never opened. That is a Git connection problem: see [Sharko can't reach your Git host](git-provider-unreachable.md).

## Why it happens

Sharko asks the Git host to merge the pull request right after opening it. GitHub and Azure DevOps merges use squash. Gitea uses a normal merge. The host can refuse for these reasons:

- **Branch protection.** The base branch needs reviews or status checks to pass first, and Sharko's token is not allowed to skip them.
- **The token can't merge.** It can push branches and open pull requests, but it does not have the right to merge.
- **Squash merging is turned off** on a GitHub repository. Sharko's GitHub merge uses squash, so the host refuses it.
- **A merge conflict.** Someone changed the same file on the base branch at the same time.
- **The base branch moved.** On GitHub, Sharko already tries the merge again up to 3 times, 2 seconds apart, when the answer is "Base branch was modified". You will see `[git] PR merge got 405 base branch modified, will retry` in the logs. It only reaches you if all 3 tries failed.

## How to fix it

### Read the reason

Look at the `error` field from the answer, or find the pull request and its state:

```sh
sharko pr list --status open
sharko pr status <id>
```

Then open the pull request on your Git host. The host shows why it can't be merged: reviews needed, checks still running, a conflict, or a merge method that is turned off.

### Merge the pull request by hand

In most cases this is all you need. Merge it on your Git host the way you normally would. Then tell Sharko so it sees the merge right away instead of on its next check (every 30 seconds):

```sh
sharko pr refresh <id>
```

Or with the API:

```sh
curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" \
  "$SHARKO_URL/api/v1/prs/<id>/refresh" | jq
```

After a cluster was added this way, the message says ArgoCD registration is still needed. Sharko's cluster reconciler does that on its own within about 30 seconds of the merge. See [Cluster reconciler](cluster-reconciler.md).

### Fix the reason so it doesn't happen again

- **Branch protection:** let Sharko's token skip the rule on the base branch, or keep the rule and merge Sharko's pull requests by hand.
- **Token rights:** give the token the right to merge. See [Put in a new Git token](git-provider-unreachable.md#put-in-a-new-git-token) if you need a new one.
- **Squash turned off (GitHub):** turn on "Allow squash merging" in the repository settings.
- **Conflict:** fix the conflict on the pull request, or close it and run the action again.

### Turn auto-merge off if you want to review every change

If your team must review each change, turn auto-merge off. Sharko then opens pull requests and leaves the merge to you.

- In the UI: **Settings**, then **GitOps**, then the **Auto-merge PRs** switch.
- In Helm: set `connection.gitops.prAutoMerge`. When this value is set, Helm wins: a change made in the UI is put back the next time Sharko reads its settings. Leave it empty if you want to control it from the UI.
- For one action only, pass `--auto-merge=false` (or `--auto-merge`) to `sharko enable-addon`, `sharko disable-addon` or `sharko upgrade-clusters`.

### If first-time setup was the thing that failed

Merge the setup pull request by hand, then run setup again. If the setup run itself got stuck rather than failing, see [Setup got stuck](init-operation-deadlocked.md#the-client-stopped-watching).

## How to check it's fixed

- The pull request is gone from the open list, and shows as merged:

  ```sh
  sharko pr list --status open
  sharko pr status <id>
  ```

- The logs show the tracker saw the merge:

  ```sh
  kubectl -n "$SHARKO_NS" logs deploy/sharko --since=10m | grep '\[prtracker\] PR merged'
  ```

- The next action of the same kind merges on its own, with no `merge failed` message.

## If that didn't work

Open an issue at <https://github.com/MoranWeissman/sharko/issues>. Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`)
- the log lines from the last hour that mention `merge failed` or `prtracker`
- the `error` and `message` fields from the answer
- which Git host you use, and what its pull request page says about why it can't merge
- what you tried from this page

Related pages:

- [Sharko can't reach your Git host](git-provider-unreachable.md)
- [Your Git host is limiting Sharko's calls](git-provider-rate-limited.md)
- [The pull request merged but ArgoCD didn't pick it up](argocd-pr-merge-no-converge.md)
- [Setup got stuck](init-operation-deadlocked.md#the-client-stopped-watching)

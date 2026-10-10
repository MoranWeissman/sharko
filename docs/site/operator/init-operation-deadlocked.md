# Sharko init is stuck or stopped

This is the one page for `sharko init` (or the **Initialize** button in
the UI) when it never finishes, fails part way, or stops because the
screen or terminal watching it went away.

**How urgent:** fix it today. Until init finishes, Sharko can't register
clusters or turn on addons, because the Git repo and the ArgoCD
application they depend on are not in place. Clusters and applications
that already run in ArgoCD are not affected.

Before you start, set up the shell variables from
[First checks](troubleshooting.md#set-up-once).

## What you see

`POST /api/v1/init` answers at once with HTTP 202, an `operation_id` and
`"status": "pending"`. The work then goes on in the background, in these
steps:

1. Creating bootstrap files
2. Pushing to branch
3. Creating pull request
4. Waiting for PR merge
5. Bootstrapping ArgoCD
6. Waiting for sync

You can watch a run like this:

```sh
OP_ID=<operation_id>
curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/operations/$OP_ID" \
  | jq '{status, error, result, wait_payload, heartbeat_at, steps}'
```

`status` is one of `pending`, `running`, `waiting`, `completed`, `failed`
or `cancelled`. Each step has its own `status` and a `detail`. While the
run waits for you to merge the pull request, `status` is `waiting` and
`wait_payload` is the pull request's address.

Signs that something went wrong:

- `status` is `failed`, and `error` says why.
- `status` stays `waiting` and nobody merges the pull request.
- The CLI or the UI stopped showing progress.
- `GET /api/v1/operations/$OP_ID` returns 404 `operation not found`.

## Why it happens

**A step failed.** The `error` field says which one:

| Error starts with | What failed |
|---|---|
| `failed to collect bootstrap files:` | Sharko could not build the files for the repo |
| `failed to push bootstrap files:` | Sharko could not write the branch to Git |
| `failed to create pull request:` | Sharko could not open the pull request |
| `PR auto-merge failed:` | Sharko opened the pull request but could not merge it |
| `PR merge timed out or session abandoned` | Nobody merged the pull request in time, or the client stopped watching |
| `ArgoCD bootstrap failed:` | Sharko could not create the bootstrap application in ArgoCD |
| `argocd application "sharko-engine" did not reach synced state:` | The application was created, but ArgoCD did not sync it within 2 minutes |
| `cannot repair: no ArgoCD client configured` | There is no working ArgoCD connection |

**The run hit its time limit.** The whole run has a hard limit of 30
minutes. After that it is marked failed.

**The client stopped watching.** While Sharko waits for the pull request
to merge, the CLI or the UI must keep telling Sharko it is still there.
See [The client stopped watching](#the-client-stopped-watching).

**Sharko restarted.** Sharko keeps init runs only in memory. A restart
forgets all of them, so `GET /operations/{id}` returns 404. The work
already done in Git and ArgoCD stays.

**Init refused to run again.** When you run init on a repo that already
has the files, Sharko checks the ArgoCD application first. It refuses
when re-running would not help. The error is then one of:

- `repo initialized but ArgoCD cannot reach or evaluate the repository: ...`
  (ArgoCD can't read the Git repo)
- `... this ArgoCD application already exists, so re-running Initialize will not fix it. ...`
  (the application is there but out of sync or unhealthy)
- `repo initialized, but Sharko could not check whether the ArgoCD bootstrap application is healthy: ...`
- a message about the ArgoCD token or its permissions

## How to fix it

### Find the run and read its error

If you no longer have the `operation_id`, run `sharko init` again. If a
run is still running or waiting, Sharko does not start a second one. It
gives back the same `operation_id` with `"resumed": true`, and the CLI
prints `Resumed existing init operation: <id>`.

To see where the repo stands without starting anything:

```sh
curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/init/status" | jq
```

`state` is one of:

- `empty`: the bootstrap files are not on the base branch yet.
- `initialized`: the files are there and the ArgoCD application is
  Synced and Healthy. Init is done.
- `partial`: the files are there, but the ArgoCD application is missing
  or unhealthy. If `repairable` is `true`, the application is only
  missing, and running init again creates it with no new pull request.
- `unreachable`, `forbidden`, `auth_failed` or `unknown`: an ArgoCD
  problem. `detail` says what.

### Fix the cause, then run init again

| If the error is about | Go to |
|---|---|
| pushing, the branch or the pull request | [Sharko can't reach your Git host](git-provider-unreachable.md). To change the Git token, see [Put in a new Git token](git-provider-unreachable.md#put-in-a-new-git-token) |
| auto-merge | [Auto-merge failed after the pull request opened](auto-merge-failed-after-pr-opened.md) |
| the ArgoCD token or permissions | [Sharko's ArgoCD token has expired or is wrong](argocd-account-token-expired.md) |
| reaching ArgoCD | [Sharko can't reach ArgoCD](argocd-upstream-unreachable.md) |
| `did not reach synced state`, or an application that already exists | Open the `sharko-engine` application in ArgoCD and fix what it reports. See also [A merged pull request never reaches ArgoCD](argocd-pr-merge-no-converge.md) |

Running init again is safe:

- If the repo is set up and the ArgoCD application is healthy, it
  finishes at once with `repo already initialized — ArgoCD bootstrap detected and healthy`.
- If the files are there but the ArgoCD application is missing, it
  creates the application and finishes with
  `repaired — the ArgoCD bootstrap application was created`. It makes
  no Git changes.
- If the ArgoCD application exists but is unhealthy, it refuses. Fix the
  application in ArgoCD first.
- If the files are not on the base branch, it starts over and opens a
  new pull request.

### The client stopped watching

While a run waits for the pull request to merge, the client must send a
heartbeat (`POST /api/v1/operations/{id}/heartbeat`). The CLI and the UI
send one every 15 seconds. This stops when you press Ctrl-C, close the
terminal, close the browser tab, or the network drops.

After 2 minutes with no heartbeat, Sharko logs this at INFO:

```sh
kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h \
  | grep 'init operation abandoned — no heartbeat from client'
```

The line has a `session_id` field, which is the `operation_id`. Sharko
then marks the run failed with the error
`PR merge timed out or session abandoned`. The pull request stays open in
Git.

What to do:

- **Less than 2 minutes have passed:** run `sharko init` again right
  away. It picks up the same run and starts sending heartbeats again. Its
  first heartbeat goes out 15 seconds after it starts, so do this within
  about a minute and a half.
- **More than 2 minutes have passed:** find the open pull request. Its
  title is `sharko: initialize repository` and its branch starts with
  `sharko/initialize-repository-` (unless you changed the commit or
  branch prefix). Then pick one:
    - Merge it, then run `sharko init` again. Sharko sees the files and
      only creates the ArgoCD application.
    - Or close it and delete its branch, then run `sharko init` again for
      a clean start. If you skip this, the new run opens a second pull
      request next to the old one.

If you start init through the API yourself, send a heartbeat at least
once a minute until the run leaves `waiting`. To skip the wait for a
merge, start it with `sharko init --auto-merge`, or send
`{"auto_merge": true}` in the `POST /api/v1/init` body.

### Stop a run

```sh
curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" \
  "$SHARKO_URL/api/v1/operations/$OP_ID/cancel" | jq
```

This marks the run `cancelled`. A step that is already running may not
stop at once. The pull request, if one was opened, stays open.

### Sharko restarted during a run

The run is gone, and its `operation_id` returns 404. Check
`/api/v1/init/status`. If a pull request from the old run is still open,
merge it or close it as described under
[The client stopped watching](#the-client-stopped-watching). Then run
`sharko init` again.

## How to check it's fixed

1. The run finished:

    ```sh
    curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/operations/$OP_ID" \
      | jq '{status, result}'
    ```

    `status` is `completed`. `result` is `init complete`, or one of the
    "already initialized" or "repaired" messages above.

2. The repo state is `initialized`:

    ```sh
    curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/init/status" | jq .state
    ```

3. In ArgoCD, the `sharko-engine` application is Synced and Healthy.

## If that didn't work

Open an issue at <https://github.com/MoranWeissman/sharko/issues>. Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`)
- the log lines from the last hour that mention `init`
- what you tried from this page

Also include the `operation_id`, the `error` field, and the output of
`/api/v1/init/status`.

Related pages:

- [Installation](installation.md)
- [Sharko can't reach your Git host](git-provider-unreachable.md)
- [Sharko can't reach ArgoCD](argocd-upstream-unreachable.md)

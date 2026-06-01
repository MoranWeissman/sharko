# Init Operation Abandoned (Client Heartbeat Stopped)

**Severity:** P1

> **Verified:** Authored 2026-06-01 against `main` HEAD. The log line
> `"init operation abandoned — no heartbeat from client"` and the
> `IsAlive(2 * time.Minute)` heartbeat-deadline check are verified
> verbatim against `internal/api/init.go:383-386` as shipped. The
> 2-minute heartbeat window is hard-coded in the wait-for-merge loop;
> the session entry stays in `operations.StatusInProgress` after the
> server abandons polling (no terminal-state transition; the audit log
> shows `init_run` start with no completion). Currently logged at
> `Info` level; will be reclassified to `Warn` per the
> [logging audit punch list](../developer-guide/logging-audit-punchlist.md).
> Re-verify after the level reclassification ships, after the
> heartbeat window changes, or after the session-state model is
> updated.

The init operation (`POST /api/v1/init`) is the documented async
exception in Sharko's otherwise synchronous API surface. The handler
returns `202 Accepted` with an `operation_id`; the client is expected
to poll for status and, importantly, **send periodic heartbeats** so
the server knows the client is still alive and watching. When the
client crashes, gets backgrounded, or otherwise stops sending
heartbeats for 2 minutes, the server abandons its polling loop and
logs:

```
{"time":"...","level":"INFO","msg":"init operation abandoned — no heartbeat from client","session_id":"<id>"}
```

The bootstrap state at that moment is **partial-known**: a PR may
have been opened and merged (the repo is real), but the server-side
session is not in a terminal state — it just stopped polling. The
next `GET /api/v1/init/{operation_id}` will return the stale
in-progress state until a new init call manually progresses it.

This is **not** the same as
[`init-operation-deadlocked.md`](init-operation-deadlocked.md) (P0).
That runbook is for the case where the operation is stuck inside the
server (heartbeats are arriving but no progress is made). This
runbook is for the case where the client went away.

The blast radius is **bounded**: subsequent init calls can be made
(see Mitigation step 2). The cluster-secret reconciler and ArgoCD
sync are unaffected; only the bootstrap-init workflow is.

---

## Symptoms

What an operator sees when this fires:

- **`kubectl logs`** shows the abandonment line:

  ```
  {"time":"...","level":"INFO","msg":"init operation abandoned — no heartbeat from client","session_id":"op-<id>"}
  ```

  Currently logged at `Info` (the audit punch list flags this for
  reclassification to `Warn`). Grep:

  ```sh
  kubectl -n <sharko-ns> logs -l app=sharko --tail=5000 \
    | jq -c 'select(.msg == "init operation abandoned — no heartbeat from client")'
  ```

- **`GET /api/v1/init/{operation_id}`** returns the stale
  in-progress state for the abandoned session:

  ```sh
  curl -sS -H "Authorization: Bearer ${SHARKO_TOKEN}" \
    "http://sharko/api/v1/init/op-<id>" | jq
  ```

  Response carries `"status": "in_progress"` (or whichever state
  the operation was in at abandonment) with a `last_heartbeat`
  timestamp >2 minutes old. The operation never transitions to
  `success` or `failed` — it sits stale.

- **Audit log** shows `event=init_run` with `result=success` (the
  session was created) but no matching `event=init_completed` entry:

  ```sh
  curl -sS -H "Authorization: Bearer ${SHARKO_TOKEN}" \
    "http://sharko/api/v1/audit?event=init_run&limit=20" \
    | jq -r '.[] | "\(.time) \(.result) \(.resource)"'
  ```

  Manual cross-check via grep for the same session_id:

  ```sh
  curl -sS -H "Authorization: Bearer ${SHARKO_TOKEN}" \
    "http://sharko/api/v1/audit?limit=200" \
    | jq -r '.[] | select(.detail // "" | contains("op-<id>")) | "\(.time) \(.event) \(.result)"'
  ```

- **CLI symptom**: the operator ran `sharko init` from a terminal that
  got closed (SSH disconnect, laptop went to sleep, terminal tab
  closed) and the CLI did not gracefully cancel the operation. The CLI
  exited but the server-side session never transitioned.
- **UI symptom**: the operator opened the init wizard, clicked
  Initialize, then closed the browser tab before the wizard
  completed. The next time they reload the wizard they see no
  progress, but a fresh init attempt fails with the
  [`init-operation-deadlocked.md`](init-operation-deadlocked.md)
  shape (409 from `POST /api/v1/init` because the bootstrap repo
  already exists).
- **The bootstrap repo state is partially-known.** The PR may have
  been opened, may have been merged, or may not have been opened at
  all — depending on which step the operation was on when the client
  disconnected. Diagnosis step 2 inspects the actual repo state.
- **No specific Prometheus alert fires.** This is a V2-4.x
  follow-up — surfacing `sharko_init_abandoned_total` would let
  operators see operator-side disconnection patterns.

If the symptom is **`POST /api/v1/init` returning `409 Conflict`**
on a brand-new operator's first init attempt, this is the downstream
consequence — the previous session's partial state is still there.
Mitigation step 2 covers cleanup; the 409 itself isn't a separate
failure mode.

---

## Diagnosis

Three checks: confirm the abandonment, inspect the actual repo state
that resulted, decide whether the operation was effectively complete.

### 1. Confirm the operation was abandoned (not stuck)

The two failure modes look identical from the operator's perspective
(the operation is "not done") but require different mitigations.
Verify by checking the `last_heartbeat` timestamp:

```sh
SESSION_ID=op-<id>
curl -sS -H "Authorization: Bearer ${SHARKO_TOKEN}" \
  "http://sharko/api/v1/init/$SESSION_ID" \
  | jq '{status, last_heartbeat, last_action}'
```

Expected output for an abandoned session:

```json
{
  "status": "in_progress",
  "last_heartbeat": "2026-06-01T10:00:00Z",
  "last_action": "wait_for_pr_merge"
}
```

The `last_heartbeat` is more than 2 minutes old. Compute the gap:

```sh
gap_seconds=$(( $(date +%s) - $(date +%s -d "$LAST_HEARTBEAT") ))
echo "$gap_seconds seconds since last heartbeat"
```

If `gap_seconds > 120`, the server has stopped polling for this
session (per the `IsAlive(2 * time.Minute)` check). This runbook
applies.

If the heartbeat is fresh (<2 minutes) but the operation isn't
progressing, the server's polling loop is stuck inside an actual
step. Jump to
[`init-operation-deadlocked.md`](init-operation-deadlocked.md).

### 2. Inspect the actual repo state

Even though the server stopped polling, the work the server did
before stopping is **real and persisted**. The PR may have been
opened (and possibly merged). Check the bootstrap repo directly:

```sh
# List PRs Sharko opened with the init-flow operation code:
curl -sS -H "Authorization: token ${GITHUB_PAT}" \
  "https://api.github.com/repos/<org>/<repo>/pulls?state=all&per_page=20" \
  | jq -r '.[] | select(.title | contains("init")) | "\(.number) \(.state) \(.title)"'
```

Two cases:

- **The PR is `merged: true`**: the bootstrap is real. The server
  just didn't get to record the completion. The fleet may already
  be in a working state — verify via Mitigation step 3 below.
- **The PR is `open` or doesn't exist**: the bootstrap is incomplete.
  Mitigation step 2 cleans up; the operator must re-run init.

For an even more definitive check, look at whether the bootstrap
root Application exists in ArgoCD:

```sh
kubectl -n argocd get application sharko-root \
  -o jsonpath='{.status.sync.status}{"/"}{.status.health.status}'
```

Expected after a successful bootstrap: `Synced/Healthy`. If the
Application doesn't exist or shows `Unknown`, the bootstrap is
incomplete regardless of the PR state.

### 3. Decide whether the operation effectively completed

If Diagnosis step 2 shows the PR merged AND the root Application
is `Synced/Healthy`, the operation effectively completed. The
operator's perception of "incomplete" is wrong — the work landed,
just the session's `success` audit entry didn't. Mitigation step 1
(force the session to a terminal state) is the only cleanup needed.

If the PR didn't merge or the root Application isn't synced, the
operation actually failed and the operator must re-run init.
Mitigation step 2 handles the cleanup.

---

## Mitigation (try in order)

1. **If the work actually completed, declare the operation done.**
   When Diagnosis step 2 confirms the bootstrap landed (PR merged,
   root Application healthy), the only remaining issue is that
   `GET /api/v1/init/{operation_id}` returns stale in-progress state
   forever. The cleanup is purely cosmetic — the system is working.

   Tell the operator to refresh the UI / re-run their next intended
   action (e.g. `sharko add-cluster`). The init session-id is no
   longer relevant to any user-facing flow once the bootstrap is
   complete; it will eventually age out of the in-memory session
   store on the next pod restart.

   Success indicator: the operator can immediately proceed with
   `add-cluster` / `add-addon` / other post-init flows. Audit log
   shows successful subsequent operations.

2. **If the work was incomplete, ask the operator to re-run
   `POST /api/v1/init` with a stable heartbeat client.** Re-running
   init re-uses the existing repo / branch / PR if one was opened
   (per the `findOpenPRForCluster` idempotent-retry pattern at
   `internal/orchestrator/cluster.go`); a fresh PR is only opened
   if there is no open prior one.

   ```sh
   # CLI side, re-run with a session that won't disconnect:
   nohup sharko init > /tmp/sharko-init.log 2>&1 &
   tail -f /tmp/sharko-init.log
   ```

   Or, for a UI-driven flow, ask the operator to keep the browser tab
   focused until the wizard reports completion (the UI sends
   heartbeats from JavaScript while the tab is visible). If the tab
   must be backgrounded, the operator should switch to the CLI flow.

   Success indicator: the new `POST /api/v1/init` returns 202 with a
   fresh `operation_id`; polling that operation_id eventually shows
   `status: success` and `last_action: complete`.

3. **Clean up the abandoned session's partial state.** If the PR
   from Diagnosis step 2 is still `open` and there's no need to
   merge it (e.g. the operator made a mistake and wants to restart
   from scratch), close the PR manually:

   ```sh
   PR_NUMBER=<from Diagnosis step 2>

   curl -sS -X PATCH -H "Authorization: token ${GITHUB_PAT}" \
     "https://api.github.com/repos/<org>/<repo>/pulls/${PR_NUMBER}" \
     --data-binary '{"state":"closed"}'

   # And delete the branch:
   git -C <repo> push origin --delete sharko/init-<suffix>
   ```

   After cleanup, re-run Mitigation step 2 to start a fresh init.

   Success indicator: re-running `POST /api/v1/init` returns 202
   instead of 409 (the previous open PR no longer blocks).

4. **Last resort — restart the Sharko pod to age out the stale
   session.** The session store is in-memory; restarting the pod
   clears all sessions including the abandoned one. Use this when
   the operator wants a clean slate and Mitigation steps 1-3 are
   inconvenient.

   ```sh
   kubectl -n <sharko-ns> rollout restart deployment/sharko
   kubectl -n <sharko-ns> rollout status deployment/sharko --timeout=120s
   ```

   After restart, the abandoned session_id no longer exists; a fresh
   init call works normally. **Caveat:** any other in-flight
   operations from other operators are also dropped — only use this
   when the deployment is single-operator or during low-traffic
   windows.

   Success indicator: `GET /api/v1/init/op-<old-id>` returns 404; a
   fresh init call works normally.

---

## Root-cause patterns

Three common causes.

### CLI session terminated mid-init

The single most common cause. The operator ran `sharko init`,
then SSH disconnected, the laptop went to sleep, or they pressed
Ctrl-C without waiting for the wizard to confirm completion. The
CLI's heartbeat goroutine stopped when the CLI process exited.

Diagnostic signature: the operator can tell you exactly what they
were doing when they stepped away. The log line timestamp matches
when the SSH connection dropped or the terminal was closed.

Fix lane: Mitigation step 2 (re-run init from a stable session,
e.g. `nohup` or `tmux`).

### Browser tab closed during UI init

The UI's init wizard sends heartbeats via JavaScript while the tab
is visible and focused. When the tab is hidden, throttled (Chrome
throttles background tabs to ~1 invocation/minute), or closed, the
heartbeat stops or slows enough to trip the 2-minute deadline.

Diagnostic signature: the operator confirms they switched tabs or
closed the browser before the wizard finished. The UI's "progress"
bar froze at whatever step it was on; reloading shows no progress.

Fix lane: Mitigation step 2 plus instructions to keep the wizard
tab focused, or switch to the CLI flow for long-running inits.

### Network blip mid-operation

The CLI / UI was running normally, but a 30-90 second network blip
prevented heartbeats from reaching Sharko in time. If the client
auto-recovered before the 2-minute window expired, no problem; if
the blip lasted past the deadline, the server abandons polling
even though the client is still trying.

Diagnostic signature: the operator confirms there was no
intentional disconnect, but logs from the corp network or VPN
show a brief outage in the same window. The client is still
sending heartbeats but the server's session is in the
abandoned state.

Fix lane: Mitigation step 2 (re-run, ideally with a more stable
network connection). Long-term: the
[`init-operation-deadlocked.md`](init-operation-deadlocked.md)
runbook discusses extending the heartbeat tolerance to absorb
brief network blips — that work belongs there, not here.

---

## Rollback plan

If Mitigation step 3 (closing the PR) was a mistake — for example,
the PR was about to merge and you would have preferred to let it
complete — rollback path:

1. Re-open the PR via the GitHub UI or:

   ```sh
   curl -sS -X PATCH -H "Authorization: token ${GITHUB_PAT}" \
     "https://api.github.com/repos/<org>/<repo>/pulls/${PR_NUMBER}" \
     --data-binary '{"state":"open"}'
   ```

2. If the branch was deleted, restore it from the PR's commit history
   (GitHub UI provides a "Restore branch" button on closed PRs that
   were not force-merged).

3. Re-run Mitigation step 2 to drive the operation to completion.

Mitigation steps 1, 2, 4 are non-destructive (declaring done,
re-running, restarting the pod).

---

## Prevention

How to make this failure mode less likely going forward. Three
levers:

- **Monitoring — surface a per-deployment `init_abandoned` counter.**
  Wiring a counter into `internal/metrics/` for every abandonment
  event gives operators a signal: if abandonments are happening
  >1/day, the heartbeat window is too short for the deployment's
  network reality. Alert on sustained >0 per hour. V2-4.x
  follow-up.

- **Gating — UI must run init in foreground.** The UI's init wizard
  should refuse to start when the tab can't reliably send heartbeats
  (e.g. via the Page Visibility API). Display a banner: "Keep this
  tab focused until init completes; otherwise the operation will be
  abandoned after 2 minutes." A V2-4.x UI follow-up.

- **Scheduled work — periodic abandoned-session cleanup.** A
  scheduled task that walks `s.opsStore` and removes sessions whose
  `last_heartbeat` is >10 minutes old. Today these sessions sit in
  memory until pod restart; aging them out is cheap and prevents
  long-running pods from accumulating stale state. Wire into the
  reconciler tick (the reconciler already runs on a timer). V2-4.x
  follow-up.

---

## Related runbooks

- [`init-operation-deadlocked.md`](init-operation-deadlocked.md) —
  the P0 sibling. If the operation is genuinely stuck inside the
  server (heartbeats arriving, no progress), jump there.
- [`auto-merge-failed-after-pr-opened.md`](auto-merge-failed-after-pr-opened.md)
  — when the init flow's PR was opened but auto-merge failed; this
  causes the operation to wait for a merge that never comes, which
  eventually trips the heartbeat window.
- [`webhook-handler-failures.md`](webhook-handler-failures.md) — when
  webhook delivery is broken, the init flow's PR-merge detection
  falls back to polling, which is slower; if the heartbeat window
  is tight, this can contribute to abandonment.
- [`cluster-reconciler.md`](cluster-reconciler.md) — the safety-net
  tick that bounds the impact even when init flow is broken.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`../developer-guide/logging.md`](../developer-guide/logging.md) —
  `request_id` correlation pattern.
- [`../developer-guide/logging-audit-punchlist.md`](../developer-guide/logging-audit-punchlist.md)
  — flags this log line for reclassification from `Info` to `Warn`.

## Escalation

If the operator can't proceed after Mitigation step 2 (re-running
init returns 409 or fails in some other way), email the maintainer:
`moran.weissman@gmail.com`. Include:

- The runbook URL you used (this page)
- The original abandoned `operation_id`
- The output of Diagnosis step 1 + 2 (session state + repo state)
- The Sharko version (`sharko version` or the Helm chart version)
- 5 minutes of relevant logs filtered by the `session_id` or
  `request_id` per the
  [correlation pattern](../developer-guide/logging.md#correlation-ids)

The maintainer is a single human, not a 24x7 rotation. Expect a
business-day SLA. Because the failure is operator-disconnect-shaped
and the system stays healthy, abandonment incidents are not
pager-grade.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P1)
- [x] Verified-by-execution header + date
- [x] Symptoms section before Diagnosis
- [x] Symptoms include exact log lines / error messages / response shapes
- [x] Diagnosis has 3+ concrete checks
- [x] Mitigation uses numbered list
- [x] Mitigation has 3-5 steps in priority order (4 steps)
- [x] Root-cause patterns: 2+ named causes (3 named)
- [x] Prevention section present and non-empty
- [x] Related runbooks section present
- [x] Intro is operator-on-call voice
- [x] Length 300-800 lines (in range)
- [x] All cross-links resolve
- [x] No emoji / no internal Slack / employee email
- [x] (if applicable) Alert reference noted as V2-4.x follow-up
-->

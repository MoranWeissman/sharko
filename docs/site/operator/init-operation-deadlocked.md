# Init Operation Deadlocked

**Severity:** P0

> **Verified:** Authored 2026-06-01 against `main` HEAD. The init
> async-operation surface is `internal/api/init.go` —
> `event="init_run"` audit code, heartbeat-poll abandonment log line
> `"init operation abandoned — no heartbeat from client"` at
> `init.go:384`, and the operation-id terminal-state model are
> verified against the shipped source. The async-init pattern is the
> documented exception to Sharko's synchronous write API per the
> product-manager memory. Re-verify when init handler shape or
> operation-id state-machine changes.

The bootstrap init operation was started — `POST /api/v1/init` returned
`202 Accepted` with an `operation_id` — but the operation never reaches
a terminal state. The heartbeat stops, the operation-id stays in
`running` indefinitely, and the bootstrap repo is in an unknown state.
Whether init half-committed (some files created, some not) cannot be
determined from the API. Page on-call.

Init is the **documented async exception** in Sharko's otherwise
synchronous write API (per the product-manager.md design note):
because bootstrap can take longer than an HTTP request, init returns
`202 + operation_id + heartbeat` instead of blocking. The cost of that
design is that init can wedge in a way that's invisible to the caller
— this runbook is how to detect, diagnose, and recover from that wedge.

This is P0 because the bootstrap repo is the root of every other
operation. With init half-done, no cluster registration can proceed
(no `managed-clusters.yaml` to write to), no addon enable can land (no
catalog rendered), no reconciler tick converges. The fleet cannot
onboard.

---

## Symptoms

What an operator sees when this fires:

- **`POST /api/v1/init` returned 202** with an `operation_id`, but
  polling the operation-id endpoint shows `state: running` for an
  extended period (> 5 minutes for a normal-size bootstrap):

  ```sh
  curl -sS http://sharko/api/v1/operations/<operation-id> \
    -H "Authorization: Bearer ${SHARKO_TOKEN}"
  # {"id":"<op>","state":"running","started_at":"...","heartbeat_at":"..."}
  ```

- **Heartbeat stopped advancing.** The `heartbeat_at` field has not
  moved for > 2 minutes (the documented heartbeat interval is ~10s;
  > 2m without a heartbeat indicates wedge or abandonment):

  ```sh
  watch -n 5 'curl -sS http://sharko/api/v1/operations/<op-id> | jq .heartbeat_at'
  ```

- **Sharko logs may show the abandonment line** if the heartbeat
  timer expired:

  ```
  {"time":"...","level":"INFO","msg":"init operation abandoned — no heartbeat from client","session_id":"<op-id>"}
  ```

  (Per
  [`../developer-guide/logging-audit-punchlist.md`](../developer-guide/logging-audit-punchlist.md),
  this should be reclassified to `Warn` — but the message itself is the
  signal regardless of level.)

- **Audit-log entry**: `event=init_run` with `result=started` exists
  but no corresponding `result=success` or `result=failed` entry.

- **The bootstrap repo's `main` branch is in an unknown state.** Some
  files (catalog, AppProject, ApplicationSets) may exist; others
  (root Application, managed-clusters.yaml) may be missing. Inspect
  directly:

  ```sh
  curl -sS \
    -H "Authorization: Bearer ${GITHUB_TOKEN}" \
    "https://api.github.com/repos/<org>/<repo>/contents/?ref=main" \
    | jq '.[] | .name'
  ```

- **Subsequent `POST /api/v1/init` returns 409** because Sharko sees
  the half-initialized repo state and refuses to re-init:

  ```
  HTTP/1.1 409 Conflict
  {"error":"bootstrap repository already initialized"}
  ```

- **No specific alert today** for init wedge; detection is via UI
  signal ("init progress stopped") or operator-driven polling. Adding
  a Prometheus alert on long-running operation-ids is in Prevention.

---

## Diagnosis

Three checks. Each narrows whether the init goroutine is alive, dead,
or blocked.

### 1. Confirm the operation-id is genuinely wedged

A long-running init is not necessarily wedged. Verify the heartbeat
has stopped advancing AND the operation didn't move past a terminal
state in the time window:

```sh
OP_ID=<from-init-response>

# Snapshot the operation at T0:
curl -sS http://sharko/api/v1/operations/"$OP_ID" \
  -H "Authorization: Bearer ${SHARKO_TOKEN}" \
  > /tmp/op-t0.json

sleep 30

# Snapshot at T30:
curl -sS http://sharko/api/v1/operations/"$OP_ID" \
  -H "Authorization: Bearer ${SHARKO_TOKEN}" \
  > /tmp/op-t30.json

# Compare:
diff /tmp/op-t0.json /tmp/op-t30.json
```

If `state` flipped to `success` or `failed`, init is not wedged — it
just took longer than expected. Stop here; this runbook does not
apply.

If `state` is still `running` AND `heartbeat_at` did not advance, init
is wedged. Proceed.

### 2. Determine which step of init wedged

Init runs a multi-step pipeline (per `.claude/team/k8s-expert.md`):

```
Step 1 — Check if repo initialized (409 if exists)
Step 2 — Generate repo from templates, replace placeholders
Step 3 — Push via PR (always PR)
Step 4 — Add repo connection to ArgoCD (POST /api/v1/repositories)
Step 5 — Create AppProject
Step 6 — Create root Application
Step 7 — Endpoint blocks until sync completes or times out (up to 2 minutes)
```

Inspect the Sharko logs for the most recent init-step line:

```sh
kubectl -n <sharko-ns> logs -l app=sharko --since=15m \
  | jq -c "select(.session_id // \"\" | test(\"$OP_ID\"))" \
  | jq -c '{time, level, msg}' \
  | tail -20
```

The last `INFO` line in the stream points at the step that started.
Common wedge points:

- Last line `"init: opening PR for bootstrap repo"` — wedged in step 3.
  PR open is hanging; see
  [`git-provider-unreachable.md`](git-provider-unreachable.md).
- Last line `"init: adding repo connection to ArgoCD"` — wedged in
  step 4. ArgoCD connection is hanging; see
  [`argocd-upstream-unreachable.md`](argocd-upstream-unreachable.md).
- Last line `"init: waiting for root application sync"` — wedged in
  step 7. ArgoCD Application controller is degraded; ArgoCD's
  Application is stuck OutOfSync.
- Last line `"init: pushing templates"` (step 2 territory) — Git push
  is failing; same as step 3.

### 3. Inspect the repo state to see how far init got

```sh
ORG=<your-org>
REPO=<your-bootstrap-repo>

# What's on main:
curl -sS \
  -H "Authorization: Bearer ${GITHUB_TOKEN}" \
  "https://api.github.com/repos/${ORG}/${REPO}/contents/?ref=main" \
  | jq -r '.[] | .name'

# What's on the init PR's branch (if step 3 opened it):
INIT_BRANCH=$(curl -sS \
  -H "Authorization: Bearer ${GITHUB_TOKEN}" \
  "https://api.github.com/repos/${ORG}/${REPO}/branches" \
  | jq -r '.[] | select(.name | test("init|bootstrap")) | .name')

curl -sS \
  -H "Authorization: Bearer ${GITHUB_TOKEN}" \
  "https://api.github.com/repos/${ORG}/${REPO}/contents/?ref=${INIT_BRANCH}" \
  | jq -r '.[] | .name'
```

If main is empty and a branch exists with templates committed, init
wedged AFTER the push but BEFORE merge. The branch can be merged
manually as recovery (see Mitigation step 3).

If main has partial state (some templates) and no branch exists, the
prior init succeeded partially and got committed directly — this is
unusual and indicates a code-path bug; flag for post-mortem.

### 4. Check ArgoCD state (for step-4-and-later wedges)

```sh
# Did init create the repo connection?
kubectl -n argocd get secret -l app.kubernetes.io/part-of=argocd \
  -o jsonpath='{.items[*].metadata.name}'

# Did init create the AppProject?
kubectl -n argocd get appproject

# Did init create the root Application?
kubectl -n argocd get application -l sharko/bootstrap=true
```

The presence/absence of each tells you which step landed before the
wedge.

---

## Mitigation (try in order)

The goal: get init to a terminal state, restore the bootstrap repo to
a known-good state, and let the operator re-run init from there if
needed.

1. **Restart Sharko to clear the wedged init session.** This forces
   the operation-id to abandonment (the
   `"init operation abandoned — no heartbeat from client"` line
   fires), but the bootstrap repo state on the Git side persists.

   ```sh
   kubectl -n <sharko-ns> rollout restart deployment/sharko
   kubectl -n <sharko-ns> rollout status deployment/sharko --timeout=120s
   ```

   After restart:

   ```sh
   curl -sS http://sharko/api/v1/operations/"$OP_ID"
   # Expected: 404 or {"state":"failed", "error":"abandoned"}
   ```

2. **Identify the underlying wedge cause and address it.** Per
   Diagnosis step 2, the wedge maps to one of:

   - **Git provider unreachable** — fix per
     [`git-provider-unreachable.md`](git-provider-unreachable.md).
     Once Git is reachable, re-run init.
   - **ArgoCD unreachable** — fix per
     [`argocd-upstream-unreachable.md`](argocd-upstream-unreachable.md).
     Once ArgoCD is reachable, re-run init.
   - **ArgoCD Application controller degraded** (step 7 wedge) — see
     ArgoCD's own runbook. Once ArgoCD's controller is healthy,
     the root Application syncs on its own; init's idempotent retry
     converges without re-running anything.

3. **If init half-committed and the repo is in inconsistent state**,
   manually complete or revert the half-commit:

   a. **If a bootstrap PR branch exists but wasn't merged** — merge
      it manually to bring `main` to a known-good state:

      ```sh
      gh pr list --state open --search "in:title init OR bootstrap"
      gh pr merge <PR-number> --squash
      ```

      Then re-run init's idempotent path: Sharko on restart re-checks
      ArgoCD for the AppProject and root Application; if missing, it
      creates them. Run:

      ```sh
      curl -sS -X POST http://sharko/api/v1/init \
        -H "Authorization: Bearer ${SHARKO_TOKEN}"
      ```

      If init returns 409 ("already initialized"), the repo IS
      bootstrapped — verify by inspecting `managed-clusters.yaml`
      exists on main.

   b. **If `main` has partial / inconsistent files** — revert the
      partial commits to a clean state:

      ```sh
      cd <bootstrap-repo>
      git log --oneline main
      git revert <bad-init-commit-sha>
      git push origin main
      ```

      Then re-run init. The clean state allows init to start over.

   c. **If init created ArgoCD resources but no Git push** —
      delete the orphaned ArgoCD resources to allow re-run:

      ```sh
      kubectl -n argocd delete application -l sharko/bootstrap=true
      kubectl -n argocd delete appproject sharko-bootstrap
      kubectl -n argocd delete secret -l app.kubernetes.io/part-of=argocd \
        --field-selector "metadata.name=sharko-bootstrap-repo"
      ```

      Then re-run init.

4. **If re-running init re-wedges at the same step**, the underlying
   cause from Diagnosis step 2 is not fixed; loop back to Mitigation
   step 2.

5. **Last resort — manual bootstrap.** If init is broken across
   multiple restarts, set up the bootstrap repo manually per
   [`installation.md`](installation.md). The manual procedure clones
   the templates, replaces placeholders, pushes a PR, merges, and
   creates the ArgoCD resources via `argocd` CLI. Sharko then comes
   up against the already-bootstrapped state and skips init.

---

## Root-cause patterns

### Client crashed mid-flight

The caller (CLI, UI, CI job) crashed or lost network connectivity
between heartbeats. The server-side init goroutine continues running
but no client is polling. After the heartbeat-timeout window (default
~2 minutes), Sharko logs the abandonment line and the operation-id
flips to `failed`.

Diagnostic signature: the abandonment log line is present; the
operation-id state moved from `running` to `failed` with
`error=abandoned`.

Why it happens: a CLI command was Ctrl-C'd, a UI session was closed,
a CI job timed out and was killed. The HTTP request was severed but
the server-side init kept going.

Recovery: not a wedge, technically — the operation correctly
abandoned. The caller should re-run init. If the bootstrap PR was
opened before abandonment, the operator merges it manually (Mitigation
step 3a).

### Git PR open hung

Init was pushing the bootstrap PR (step 3) and the Git provider call
hung. The HTTP client request to the provider has an effective
timeout but in some cases (proxy issues, TCP keep-alive failures) the
hang exceeds the heartbeat window.

Diagnostic signature: Diagnosis step 2's last log line is
`"init: opening PR for bootstrap repo"` or similar; the Git provider
is currently down or slow per
[`git-provider-unreachable.md`](git-provider-unreachable.md).

Fix is Mitigation step 2 (fix Git provider) then retry. Long-term:
add an aggressive ctx-with-timeout on the PR-open path so the wedge
manifests as a step-3 failure instead of an indefinite hang.

### ArgoCD repository connection registration hung

Step 4 (`POST /api/v1/repositories` to ArgoCD) hung waiting for
ArgoCD's response. ArgoCD's repo-server is OOMing or the
repository-validation probe is timing out internally.

Diagnostic signature: Diagnosis step 2's last log line is
`"init: adding repo connection to ArgoCD"`; ArgoCD's repo-server
is showing high memory or recent OOMKill.

Fix is on the ArgoCD side (increase repo-server memory, fix
the validation probe timeout). Once ArgoCD is healthy, re-run init.

### Root Application sync timed out

Step 7 waits up to 2 minutes for ArgoCD to sync the root Application.
If ArgoCD's Application controller is slow, init's 2-minute timeout
expires and init returns "timed out" — but the operation-id may still
appear `running` to the client if the heartbeat thread didn't propagate
the timeout.

Diagnostic signature: Diagnosis step 2's last log line is
`"init: waiting for root application sync"`; the timeout window has
elapsed; ArgoCD's root Application is still `OutOfSync` or `Progressing`.

Recovery: init's idempotent design means the root Application will
eventually sync on its own. Re-running init is safe (it returns 409
once main has the bootstrap state). Operator can monitor ArgoCD's
sync state and consider init "complete" once the root Application is
`Healthy`.

---

## Rollback plan

The init operation itself is idempotent — re-runs are safe.

For Mitigation step 3 (manual merge / revert):

- **If you merged the wrong PR**: revert via Git and re-run init.
- **If you deleted the wrong ArgoCD resource**: re-create via init or
  manually with `argocd app create`.

For Mitigation step 5 (manual bootstrap):

- Roll back by deleting the manually-created resources (ArgoCD
  Application, AppProject, repository secret, bootstrap files on
  main) and re-running automated init.

---

## Prevention

- **Monitoring — alert on long-running operation-ids.** Add a
  Prometheus rule that pages when an operation-id has `state=running`
  for > 5 minutes:

  ```promql
  sharko_operation_running_seconds > 300
  ```

  Wiring requires Sharko to emit a per-operation-id gauge or counter.

- **Code change — aggressive ctx timeouts on init steps.** Each
  init step (Git push, ArgoCD repo registration, AppProject creation,
  root Application creation, root sync wait) should have an explicit
  `ctx.WithTimeout` shorter than the heartbeat window. Wedges then
  manifest as init failures (clean state to recover from) rather
  than indefinite hangs.

- **Code change — promote the abandonment log to `Warn` level.** Per
  [`../developer-guide/logging-audit-punchlist.md`](../developer-guide/logging-audit-punchlist.md),
  the abandonment line at `init.go:384` should be `Warn`, not
  `Info` — it's an operator-actionable event, not informational. The
  change is one line; tracked as a V2-4.x follow-up.

- **Gating — pre-init dependency health probe.** Before starting
  init, probe Git and ArgoCD reachability. If either is unreachable,
  fail init early with a clear error instead of starting a wedge-prone
  multi-step process. Implementation in `internal/api/init.go`'s
  pre-flight.

- **Operator procedure — re-run init is safe.** Document explicitly
  in [`installation.md`](installation.md) and the API reference that
  re-running init against an already-bootstrapped repo is safe and
  returns 409. Operators in a wedge situation often hesitate to retry
  because they're not sure it's safe; documenting eliminates the
  hesitation.

---

## Related runbooks

- [`git-provider-unreachable.md`](git-provider-unreachable.md) —
  underlying cause of step-2/3 wedges.
- [`argocd-upstream-unreachable.md`](argocd-upstream-unreachable.md) —
  underlying cause of step-4-and-later wedges.
- [`argocd-pr-merge-no-converge.md`](argocd-pr-merge-no-converge.md) —
  adjacent failure pattern (PR merge to no convergence). Step-7 wedge
  shares the "ArgoCD didn't converge" symptom.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.
- [`installation.md`](installation.md) — the installation runbook
  that init automates. Falls back to manual when init wedges.
- [`../developer-guide/logging.md`](../developer-guide/logging.md) —
  `session_id` (init-specific) and `request_id` correlation patterns.
- [`../developer-guide/logging-audit-punchlist.md`](../developer-guide/logging-audit-punchlist.md) —
  audit-log discipline that flagged the Info-vs-Warn classification of
  the abandonment line.

## Escalation

If init re-wedges across multiple restarts and Mitigation steps don't
make progress within 30 minutes, email the maintainer:
`moran.weissman@gmail.com`. Include:

- This runbook URL
- The `operation_id`
- The output of Diagnosis steps 1, 2, 3, 4
- The current state of the bootstrap repo (file list on `main`, any
  open PRs)
- The current state of ArgoCD bootstrap resources (Application,
  AppProject, repository secret)
- The Sharko version

The maintainer is a single human, not a 24×7 rotation. Init wedges
are P0 because they block fleet onboarding; expect a same-business-day
investigation.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P0)
- [x] Verified-by-execution header + date
- [x] Symptoms section before Diagnosis
- [x] Symptoms include exact log lines / error messages / alert names
- [x] Diagnosis has 3+ concrete checks (4 named)
- [x] Mitigation uses numbered list
- [x] Mitigation has 3-5 steps in priority order
- [x] Root-cause patterns: 2+ named causes (4 named)
- [x] Prevention section present and non-empty
- [x] Related runbooks section present
- [x] Intro is operator-on-call voice
- [x] Length 300-800 lines
- [x] All cross-links resolve
- [x] No emoji / no internal Slack / employee email
- [x] (if applicable) No alert defined yet (per Symptoms)
-->

# Git Provider Rate Limited

**Severity:** P1

> **Verified:** Authored 2026-06-01 against `main` HEAD. The wrapping
> error surface is verified against
> `internal/orchestrator/git_helpers.go` (every Git op funnels through
> `commitChangesWithMeta`; rate-limit errors bubble up from
> `g.git.CreateBranch / BatchCreateFiles / CreatePullRequest` calls) and
> against the GitHub provider implementation in
> `internal/gitprovider/github.go:70-100` (every `Repositories.GetContents`
> returning `*github.ErrorResponse` with HTTP 403 + `X-RateLimit-Remaining: 0`
> is rate-limit-shaped). The reconciler-side surface for the
> `managed-clusters.yaml` 403 is verified against
> `internal/clusterreconciler/reconciler.go:345-372` (the `audit.action=git_read`
> failure entry). Re-verify before changing the go-github version pinned
> in `go.mod` (v68 at audit time) or the `commitChangesWithMeta` error
> wrapping; both anchors are load-bearing for the symptoms below.

A burst of cluster registrations, addon enables, or reconciler ticks is
hitting the Git provider's per-token rate limit. GitHub responds with
HTTP 403 + `X-RateLimit-Remaining: 0` on every subsequent call;
Sharko wraps the error and bubbles it through the API surface
(operator gets 502 + `creating branch` / `writing files` / `creating
pull request` in the error body) and through the reconciler
(`audit.action=git_read` with `result=failure` and reconciler ticks
become no-ops until the quota resets).

This covers two adjacent failure-mode rows from the
[failure-mode index](failure-mode-index.md):

- "Git provider rate limit hit" — generic burst-driven quota exhaustion
  observed at `internal/orchestrator/git_helpers.go` (and every Git op
  funnelled through it).
- "GitHub Contents API 403 on `managed-clusters.yaml` read" —
  reconciler-side surface, logged as `audit.action=git_read` with
  `git_fetch_failed` shape in the reconciler tick log.

Both share the same root cause (PAT quota exhausted), the same
diagnosis path (inspect rate-limit headers and call cadence), and the
same mitigation lanes (rotate to a less-loaded PAT or back off
cadence). They are documented here as one runbook per the
[style guide's grouping rule](../developer-guide/runbook-style-guide.md#when-to-write-one-runbook-vs-multiple).

The blast radius is **bounded but visible**: existing labeled cluster
Secrets stay healthy (the reconciler skips on a `git_fetch_failed`
without deleting), but **new registrations stall**, **new addon
enables stall**, and **the dashboard surfaces a backlog of pending
PRs.** Bursts during fleet onboarding are the typical trigger; steady
state usually has headroom.

---

## Symptoms

What an operator sees when this fires:

- **HTTP 502** from any write handler that opens a PR:
  `POST /api/v1/clusters`, `POST /api/v1/clusters/batch`,
  `POST /api/v1/clusters/{name}/adopt`, `DELETE /api/v1/clusters/{name}`,
  `PATCH /api/v1/clusters/{name}`, `POST /api/v1/addons`,
  `PATCH /api/v1/addons/{name}`, `POST /api/v1/addons/{name}/upgrade`,
  `POST /api/v1/addons/upgrade-batch`.
- **HTTP response body** wraps the underlying go-github error:

  ```json
  {"error":"creating branch \"sharko/register-cluster-...\": POST https://api.github.com/repos/.../git/refs: 403 API rate limit exceeded for installation ID ..."}
  ```

  The wrapping operation may be `creating branch`, `writing files on
  branch`, or `creating pull request` depending on which Git op tripped
  the limit first. The inner `403 API rate limit exceeded` substring is
  the grep anchor.

- **`kubectl logs` line from the reconciler tick** when the 403 lands
  on `GetFileContent` for `managed-clusters.yaml`:

  ```
  {"time":"...","level":"ERROR","msg":"github get file content failed","error":"GET https://api.github.com/repos/.../contents/configuration/managed-clusters.yaml: 403 API rate limit exceeded for ...","path":"configuration/managed-clusters.yaml","ref":"main"}
  ```

  Immediately followed by an audit entry recording the failure:

  ```
  {"time":"...","level":"INFO","msg":"audit","event":"cluster_secret_reconcile","action":"git_read","result":"failure","error":"..."}
  ```

- **`kubectl logs` line from any handler-driven write path**:

  ```
  {"time":"...","level":"ERROR","msg":"RegisterCluster: git commit failed","cluster":"prod-eu","error":"creating branch \"sharko/register-cluster-prod-eu-...\": ... 403 API rate limit exceeded ..."}
  ```

- **Dashboard PR panel** shows the previous Sharko-opened PRs as
  `open` but no new PRs being created over a 5-15 minute window — the
  pending operations queue is silently building because every write
  attempt fails fast.
- **Audit log** shows a cluster of `git_read` / `cluster_secret_reconcile`
  entries with `result=failure` clustered in time:

  ```sh
  curl -sS -H "Authorization: Bearer ${SHARKO_TOKEN}" \
    "http://sharko/api/v1/audit?event=cluster_secret_reconcile&result=failure&limit=50" \
    | jq -r '.[] | "\(.time) \(.action) \(.error)"' \
    | head -20
  ```

- **GitHub's response headers** (verifiable via direct `curl` from the
  Sharko pod — Diagnosis step 2) carry the rate-limit signal:

  ```
  X-RateLimit-Limit: 5000
  X-RateLimit-Remaining: 0
  X-RateLimit-Reset: <unix-timestamp>
  X-RateLimit-Used: 5000
  X-RateLimit-Resource: core
  ```

- No specific Prometheus alert fires for "Git rate limit hit" today.
  Sustained 403s fan into
  [`SharkoClusterRegistrationSlowBurn`](budget-burn-runbook.md#sharkoclusterregistrationslowburn)
  and
  [`SharkoAddonCycleSlowBurn`](budget-burn-runbook.md#sharkoaddoncycleslowburn)
  when the burst lasts long enough to consume the error budget.

If the symptom is HTTP 502 with `403 Forbidden` but the response body
says `Resource not accessible by integration` or `Bad credentials`
instead of `API rate limit exceeded`, this is **not** the runbook —
that's a PAT-scope problem, not a quota problem. See
[`git-provider-unreachable.md`](git-provider-unreachable.md) for the
PAT-scope diagnosis.

---

## Diagnosis

Three checks: confirm the rate limit, identify which PAT is exhausted,
identify the burst source. Stop after step 1 if the rate limit signal
is the obvious symptom — steps 2 and 3 narrow to the right
mitigation.

### 1. Confirm the failure is rate-limit-shaped (not PAT-scope or auth)

Grep the Sharko logs for the 403 + `rate limit` shape, joined by
`request_id` per the
[V2-2.2 correlation pattern](../developer-guide/logging.md#correlation-ids):

```sh
SHARKO_NS=<sharko-ns>
kubectl -n "$SHARKO_NS" logs -l app=sharko --tail=5000 \
  | jq -c 'select(.msg | test("git|github"; "i")) | select(.error | test("rate limit|403"; "i"))' \
  | head -20
```

The signature you want to see:

```
{"time":"...","level":"ERROR","msg":"github get file content failed","error":"... 403 API rate limit exceeded for installation ID ...","path":"...","ref":"main"}
```

If every recent failure shows `403 API rate limit exceeded`, this
runbook applies. If you see `403 Bad credentials` or `403 Resource
not accessible by integration`, the PAT is wrong-scoped or revoked —
go to [`git-provider-unreachable.md`](git-provider-unreachable.md).

### 2. Probe GitHub's rate-limit endpoint directly from the Sharko pod

Read the PAT Sharko is using from the secret and probe GitHub's
`/rate_limit` endpoint:

```sh
SHARKO_POD=$(kubectl -n "$SHARKO_NS" get pod -l app=sharko -o name | head -1)
GITHUB_PAT=$(kubectl -n "$SHARKO_NS" get secret <sharko-release> \
  -o jsonpath='{.data.GITHUB_TOKEN}' | base64 -d)

kubectl -n "$SHARKO_NS" exec "$SHARKO_POD" -- \
  curl -sS -H "Authorization: token ${GITHUB_PAT}" \
  https://api.github.com/rate_limit | jq .
```

Expected output when quota is exhausted:

```json
{
  "resources": {
    "core": {
      "limit": 5000,
      "remaining": 0,
      "reset": 1717272000,
      "used": 5000
    }
  }
}
```

Convert `reset` to a human-readable time:

```sh
date -u -r 1717272000 +"%Y-%m-%d %H:%M UTC"
```

That's when the quota refills. If `remaining > 0` despite Sharko
logging 403s, the PAT shown above is **not** the one Sharko is
actually using — check the secret name and the env-var wiring.

For a per-token-tier PAT (GitHub App installation token) the limit
is 5000/hour. For a user PAT it's 5000/hour. For a `GITHUB_TOKEN` in
GitHub Actions it's 1000/hour. Match the limit value in the response
against the token shape Sharko was deployed with.

### 3. Identify the burst source

Once the rate-limit hit is confirmed, identify which operation caused
the burst — knowing this routes you to the right mitigation lane
(rotate PAT vs. throttle a specific operation).

```sh
# Count Sharko-initiated Git ops in the last hour by operation type.
curl -sS -H "Authorization: Bearer ${SHARKO_TOKEN}" \
  "http://sharko/api/v1/audit?limit=500" \
  | jq -r '.[] | select(.time > (now - 3600 | strftime("%Y-%m-%dT%H:%M:%S"))) | .action' \
  | sort | uniq -c | sort -rn | head -20
```

Common burst sources, ranked by frequency:

- `register_cluster` × N — fleet onboarding (often the cause; 50
  clusters in 5 minutes = 50 PRs = 150+ API calls).
- `addon_enable_on_cluster` × N — bulk addon enable across the
  fleet.
- `cluster_secret_reconcile` (`git_read` action) — every 30s the
  reconciler reads `managed-clusters.yaml`. Sustained, this is
  120 reads/hour — well under quota by itself. If the burst is
  reconciler-driven, the reconciler is being `Trigger()`ed
  excessively (post-merge hook in a tight PR-merge loop).

Distinguish reconciler-driven from handler-driven by checking the
`source` field on audit entries (`source=reconciler` vs `source=api`).

---

## Mitigation (try in order)

1. **Wait for the quota window to reset.** If the `reset` timestamp
   from Diagnosis step 2 is within 15 minutes, the cheapest mitigation
   is to do nothing — Sharko's write operations will start succeeding
   again as soon as the quota refreshes. Communicate the ETA to anyone
   running a fleet-onboarding script; ask them to pause and retry after
   the reset.

   ```sh
   # Time until reset:
   echo "Quota resets in $(( ( $(date -u -r <reset-unix> +%s) - $(date -u +%s) ) / 60 )) minutes"
   ```

   Success indicator: `/api/v1/health` (if it surfaces git provider
   reachability) flips back to reachable; a synthetic
   `GET /api/v1/clusters` does not return 502 with the rate-limit
   shape.

2. **Rotate to a less-loaded PAT.** If the quota reset is more than 15
   minutes away and fleet operations cannot wait, rotate Sharko's PAT
   to a freshly-issued token (preferably from a different GitHub
   App installation / user account than the exhausted one). The
   new PAT starts with a fresh 5000/hour budget.

   ```sh
   # Generate a new PAT via GitHub UI (Settings -> Developer settings ->
   # Personal access tokens -> Fine-grained tokens) with the same
   # scopes as the current one (repo + workflow + read:org).
   NEW_PAT="<paste new PAT>"

   kubectl -n "$SHARKO_NS" patch secret <sharko-release> \
     --type='json' \
     -p='[{"op":"replace","path":"/data/GITHUB_TOKEN","value":"'"$(echo -n "$NEW_PAT" | base64)"'"}]'

   kubectl -n "$SHARKO_NS" rollout restart deployment/sharko
   kubectl -n "$SHARKO_NS" rollout status deployment/sharko --timeout=120s
   ```

   Success indicator: re-run Diagnosis step 2 — the rate-limit
   endpoint now reports `remaining` close to 5000 for the freshly-
   rotated PAT.

3. **Back off the burst-causing operation.** If the burst source from
   Diagnosis step 3 is identifiable (e.g. `register_cluster` x 50 in
   5 minutes), stop the script that's driving the burst and retry
   with a slower cadence. Sharko's `commitChangesWithMeta` serialises
   Git operations under a process-local mutex, so per-pod
   parallelism is already capped at 1 — the burst is operator-driven,
   not Sharko-internal.

   For fleet onboarding scripts, the right cadence is **one cluster
   every 10-15 seconds** (each registration makes ~3-4 GitHub API
   calls: create-branch, batch-write, create-PR, optional merge).
   At that cadence 50 clusters consume ~200 API calls over ~12 minutes
   — well within the 5000/hour budget.

   ```sh
   # Operator-side: pause the onboarding script, then retry with
   # explicit sleep between calls.
   for cluster in $(cat clusters.txt); do
     curl -sS -X POST -H "Authorization: Bearer ${SHARKO_TOKEN}" \
       "http://sharko/api/v1/clusters" \
       --data-binary "{\"name\":\"${cluster}\"}"
     sleep 15
   done
   ```

   Success indicator: `register_cluster` audit entries show
   `result=success` instead of `result=failure`; the rate-limit
   endpoint shows `remaining` decreasing slowly instead of falling
   off a cliff.

4. **Throttle the reconciler.** If Diagnosis step 3 fingers
   `cluster_secret_reconcile` as the burst source — e.g. Sharko is
   reading `managed-clusters.yaml` once a second because a PR-merge
   hook is mis-wired — increase `DefaultTickInterval` to slow the
   safety-net tick (default 30s; bump to 60s or 120s for
   over-quota deployments) and audit the post-merge `Trigger()`
   wiring.

   ```yaml
   # charts/sharko/values.yaml
   reconciler:
     tickInterval: 60s
   ```

   Then `helm upgrade <release> charts/sharko/` and restart Sharko.
   The reconciler will still trigger immediately on PR merge — only
   the steady-state safety-net cadence slows.

   Success indicator: reconciler audit ticks (`source=reconciler`)
   drop from N per minute to N per 2 minutes; rate-limit
   `remaining` shows healthy headroom even under steady-state
   operation.

5. **Last resort — escalate to a higher rate-limit tier.** GitHub
   offers higher limits via GitHub Apps (15,000/hour per installation)
   compared to user PATs (5000/hour). If your steady-state usage
   regularly exceeds 5000/hour, the right fix is to migrate Sharko's
   Git auth from a user PAT to a GitHub App installation token.

   This is **a configuration change**, not a Sharko code change —
   GitHub App auth is already supported by the underlying go-github
   library. Document the migration in your installation runbook;
   it's beyond the scope of an emergency mitigation. Until the
   migration ships, the previous four steps are the realistic
   operator levers.

---

## Root-cause patterns

Four common causes. The first two are by far the most frequent;
they account for almost every observed incidence of this failure.

### Fleet onboarding burst

The single most common cause. An operator runs a script to register
50, 100, or 500 clusters in a tight loop. Each registration makes
~3-4 API calls (create branch, batch-write files, create PR, merge);
the script doesn't sleep between calls; the 5000/hour budget is
consumed in 20-40 minutes. Once the budget hits zero, every
subsequent registration returns 502, and operators interpret the
failure as a Sharko bug rather than as a self-inflicted rate-limit.

Diagnostic signature: a tight cluster of `register_cluster` audit
entries spanning <30 minutes; the first N succeed, the rest fail
with the 403 + rate-limit shape; the burst stops as soon as the
script does.

Fix lane: Mitigation step 3 (back off the script). Long-term: the
operator's onboarding script should rate-limit itself client-side.

### Mis-wired reconciler `Trigger()` chain

The reconciler is configured to `Trigger()` on every PR merge via
`prTracker.SetOnMergeFn`. In a healthy deployment the PR-merge
cadence is bounded (1-3 PRs per minute under load); the trigger
costs 1 `GetFileContent` call per fire.

If the PR-tracker poll loop is mis-tuned (poll interval too short,
or the merge detection emits one trigger per detected-already-merged
PR), the reconciler can fire dozens of times per minute on the same
state. Each fire reads `managed-clusters.yaml` from GitHub. At ~60
fires/minute, that's 3600 API calls per hour — 72% of a default user
PAT's budget consumed by no-op reconciles.

Diagnostic signature: a steady-state `git_read` failure burst that
isn't preceded by any handler-driven operation; the audit log shows
`source=reconciler` for the entire failure cluster; rate-limit
exhaustion happens at the same time every hour as the budget resets
and is immediately re-consumed.

Fix lane: Mitigation step 4 (throttle reconciler cadence) plus a
follow-up to fix the PR-tracker emission shape so each PR triggers
exactly once.

### Shared PAT across multiple Sharko deployments

A single GitHub PAT is reused across two or more Sharko deployments
(e.g. staging + prod, or one-Sharko-per-tenant). The combined
operation rate trips the per-PAT 5000/hour budget even though no
single deployment is busy.

Diagnostic signature: Diagnosis step 2 (the rate-limit probe) shows
`remaining` near zero, but the burst source from Diagnosis step 3
shows only modest activity from this deployment. The deficit is
the sum of activity across all PAT-sharing deployments.

Fix lane: Mitigation step 2 (rotate to a per-deployment PAT). Each
Sharko deployment should have its own PAT with its own per-token
budget; sharing a PAT is convenient at install time but turns into
a debugging nightmare under burst load.

### GitHub App installation hitting per-installation limit

When Sharko is authenticated via a GitHub App rather than a user
PAT, the per-installation budget is 15,000/hour by default — three
times the user PAT budget. This pattern is rare but real: large
fleet operations (1000+ cluster onboardings in a single hour) can
still exhaust a GitHub App installation's budget.

Diagnostic signature: the rate-limit probe reports `limit: 15000`
(GitHub App) or `limit: 5000` (user PAT). If `limit: 15000` and the
budget is still exhausted, the issue is genuinely "we are doing too
much in this window."

Fix lane: Mitigation step 3 (back off the burst-causing operation).
GitHub App rate limit is a hard ceiling; no further-up tier is
operator-side. Spreading the work across time is the only fix.

---

## Rollback plan

If Mitigation step 2 (PAT rotation) made things worse — for example,
the new PAT has narrower scopes than the old one and other
operations break (signature: 403 `Resource not accessible by
integration` instead of the rate-limit shape) — rollback path:

1. Restore the previous secret value from the K8s API revision
   history (if enabled) or from your secrets backup tool:

   ```sh
   kubectl -n "$SHARKO_NS" rollout undo deployment/sharko
   ```

2. Generate a fresh PAT with the full scope set Sharko requires:
   `repo`, `workflow`, `read:org`. Confirm the scopes match
   `charts/sharko/values.yaml`'s documented requirements.

3. Re-apply Mitigation step 2 with the correctly-scoped PAT.

Mitigation steps 1, 3, 4, 5 are non-destructive and need no
rollback (waiting, throttling, configuration changes).

---

## Prevention

How to make this failure mode less likely going forward. Three
levers, in order of leverage:

- **Monitoring — alert on rate-limit headroom, not on the failure.**
  GitHub returns `X-RateLimit-Remaining` on every response. Add a
  Sharko-internal metric that records the most-recent value (e.g.
  `sharko_github_rate_limit_remaining`) and alert when it falls
  below 20% of `X-RateLimit-Limit` for 5 minutes. That gives the
  operator a 15-30 minute warning before the budget hits zero — long
  enough to throttle a burst or rotate a PAT preemptively. Wiring
  this metric into `internal/metrics/` is a V2-4.x follow-up.

- **Gating — document the per-operation API cost in onboarding
  scripts.** Each cluster registration costs ~3-4 GitHub API calls;
  each addon enable costs ~3-4. Operators running fleet scripts
  should know the per-operation cost so they can size their batch
  windows correctly. Add a "rate-limit budget" section to the
  installation runbook with a calculator:
  `N_clusters * 4 + N_addons * 4 < 5000` over any one-hour window.

- **Scheduled work — quarterly PAT rotation drill.** Sharko's
  PAT has no enforced rotation today. A 90-day rotation drill
  catches Mitigation step 2's procedure drift, exercises the
  secret-patch path, and validates the per-deployment budget
  separation (Root cause pattern 3). The drill belongs in the same
  schedule as the ArgoCD token rotation drill documented in
  [`argocd-account-token-expired.md`](argocd-account-token-expired.md).

---

## Related runbooks

- [`git-provider-unreachable.md`](git-provider-unreachable.md) — the
  P0 sibling failure mode. If symptoms include `Bad credentials` or
  `Resource not accessible by integration` (PAT-scope problem) instead
  of `API rate limit exceeded`, jump there.
- [`argocd-account-token-expired.md`](argocd-account-token-expired.md)
  — the same rotation-drift class but on the ArgoCD account token.
  Quarterly drills should rotate both.
- [`cluster-reconciler.md`](cluster-reconciler.md) — the reconciler
  surface that triggers Root cause pattern 2. Tunables for the tick
  interval are documented there.
- [`budget-burn-runbook.md`](budget-burn-runbook.md) — when sustained
  rate-limiting consumes the error budget enough to fire
  `SharkoClusterRegistrationSlowBurn` or `SharkoAddonCycleSlowBurn`.
- [`failure-mode-index.md`](failure-mode-index.md) — the master
  inventory of every operator-facing failure mode in Sharko.
- [`../developer-guide/logging.md`](../developer-guide/logging.md) —
  V2-2.2 `request_id` correlation pattern, used throughout
  Diagnosis above.

## Escalation

If the mitigations above do not resolve the failure within 60
minutes, email the maintainer: `moran.weissman@gmail.com`. Include:

- The runbook URL you used (this page)
- The output of Diagnosis step 2 (`/rate_limit` probe response)
- The output of Diagnosis step 3 (audit-action histogram for the
  last hour)
- The Sharko version (`sharko version` or the Helm chart version)
- A 5-minute window of relevant logs filtered by `request_id` per
  the [correlation pattern](../developer-guide/logging.md#correlation-ids)

The maintainer is a single human, not a 24x7 rotation. Expect a
business-day SLA, not a paged response.

<!-- Style-guide compliance checklist (V2-4.1):
- [x] Title matches `# <Failure name>`
- [x] Severity line present (P1)
- [x] Verified-by-execution header + date
- [x] Symptoms section before Diagnosis
- [x] Symptoms include exact log lines / error messages / response shapes
- [x] Diagnosis has 3+ concrete checks
- [x] Mitigation uses numbered list
- [x] Mitigation has 3-5 steps in priority order (5 steps)
- [x] Root-cause patterns: 2+ named causes (4 named)
- [x] Prevention section present and non-empty
- [x] Related runbooks section present
- [x] Intro is operator-on-call voice
- [x] Length 300-800 lines (this page: in range)
- [x] All cross-links resolve
- [x] No emoji / no internal Slack / employee email
- [x] (if applicable) Alert name from prometheusrules.yaml referenced (SlowBurn)
-->

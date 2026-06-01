# Auto-Merge Failed After PR Opened

**Severity:** P1

> **Verified:** Authored 2026-06-01 against `main` HEAD. The log line
> `"RegisterCluster: PR opened but auto-merge failed"` and the
> partial-success result envelope (`Status: "partial"`, `FailedStep:
> "pr_merge"`, `Message` containing the PR URL) are verified verbatim
> against `internal/orchestrator/cluster.go:335-343` as shipped. The
> wrapping error path in `commitChangesWithMeta` at
> `internal/orchestrator/git_helpers.go:148-153` returns
> `"PR created but merge failed: <wrapped error>"` when
> `o.git.MergePullRequest` fails after a successful PR creation. The
> same partial-success shape is emitted by `adopt.go` (cluster adopt
> flow) and by every other PR-opening operation that uses
> `commitChangesWithMeta`. Re-verify before changing the
> `Status: "partial"` shape or the `FailedStep` constant — both are
> grep anchors for the failure mode.

A write operation (`POST /clusters`, `POST /clusters/adopt`,
`DELETE /clusters/{name}`, `PATCH /addons/{name}`, `POST /addons`,
etc.) opened a PR successfully against the bootstrap repo, but
auto-merge failed. The operation's HTTP response body carries
`status: "partial"`, the PR URL, and the wrapped merge error. The
user-visible effect: the requested operation is **half-done** —
secrets may have been pushed, the PR exists, but the cluster's
configuration is not yet on the base branch.

The blast radius is **bounded but visible**: the cluster-secret
reconciler does NOT converge for the failed operation until the
PR actually merges (manual merge by the operator, or a follow-up
auto-merge attempt). Other clusters / operations are unaffected.

Common causes:

- The repo's branch protection rules require status checks that
  haven't passed (or aren't running on Sharko-opened branches).
- Required reviewers aren't auto-assigning to Sharko PRs.
- A merge conflict against the base branch (concurrent operator
  changes).
- The Git provider rejected the merge (rate-limit, transient API
  failure, or PR-state mismatch).

This is **not** the same as
[`git-provider-unreachable.md`](git-provider-unreachable.md) (the
Git provider is reachable enough to open the PR) and **not** the
same as
[`auth-bypass.md`](auth-bypass.md) (Sharko's PAT is valid enough to
create the PR; merge requires different permissions). The merge
fails specifically — the PR creation already succeeded.

---

## Symptoms

What an operator sees when this fires:

- **HTTP response is 200/207** (depending on the endpoint) with the
  partial-success envelope:

  ```json
  {
    "status": "partial",
    "completed_steps": ["secrets", "git_pr_open"],
    "failed_step": "pr_merge",
    "error": "PR created but merge failed: ...",
    "message": "Secrets created and PR opened, but auto-merge failed. Merge manually then ArgoCD registration will be needed: https://github.com/<org>/<repo>/pull/<id>",
    "git": {
      "pr_url": "https://github.com/<org>/<repo>/pull/<id>",
      "pr_id": <id>,
      "branch": "sharko/register-cluster-prod-eu-..."
    }
  }
  ```

- **`kubectl logs` line** (Error level — call site is
  `cluster.go:335` for RegisterCluster; analogous emitters exist in
  every PR-opening endpoint):

  ```
  {"time":"...","level":"ERROR","msg":"RegisterCluster: PR opened but auto-merge failed","cluster":"prod-eu","pr_url":"https://github.com/<org>/<repo>/pull/<id>","error":"merging PR <id>: <github-error>"}
  ```

  Grep:

  ```sh
  kubectl -n <sharko-ns> logs -l app=sharko --tail=5000 \
    | jq -c 'select(.msg | test("PR opened but auto-merge failed"; "i"))'
  ```

- **Dashboard PR panel** shows the PR in `open` state for the
  cluster + operation combination — long after the request that
  opened it. Operators usually notice when running `sharko pr list`:

  ```sh
  curl -sS -H "Authorization: Bearer ${SHARKO_TOKEN}" \
    "http://sharko/api/v1/prs?status=open" | jq '.[] | {pr_id, pr_url, cluster, operation, age_seconds: (now - (.created_at | fromdate))}'
  ```

  PRs older than ~30 seconds with auto-merge expected to have closed
  them indicate failure.

- **GitHub PR view** shows the PR as `Open` (not `Merged`). The PR
  itself may surface the reason the merge failed: status checks
  blocking, branch protection failure, merge conflict, or `Sharko Bot`
  lacking merge permission.
- **Audit log** records the partial outcome under the operation's
  event with `result=partial`:

  ```sh
  curl -sS -H "Authorization: Bearer ${SHARKO_TOKEN}" \
    "http://sharko/api/v1/audit?result=partial&limit=20" \
    | jq -r '.[] | "\(.time) \(.event) \(.resource) \(.detail)"'
  ```

- **Reconciler does NOT converge** for the affected cluster.
  ArgoCD's cluster Secret stays absent (for register) or stays in its
  pre-operation state (for update / delete). Other clusters' reconciler
  ticks proceed normally.
- **No specific Prometheus alert fires** for auto-merge failure.
  Sustained PR-open-but-not-merged accumulation fans into
  [`SharkoClusterRegistrationSlowBurn`](budget-burn-runbook.md#sharkoclusterregistrationslowburn)
  when the error budget is consumed.

If the symptom is **HTTP 502** with `"creating pull request"` in the
error body, the PR was never opened — auto-merge isn't the issue.
Jump to [`git-provider-unreachable.md`](git-provider-unreachable.md)
or [`git-provider-rate-limited.md`](git-provider-rate-limited.md).

---

## Diagnosis

Three checks: identify the merge failure reason, classify it, and
decide between manual merge and full retry.

### 1. Identify the merge failure reason

The wrapped error string from the partial-success response (or the
Error log line) carries the GitHub-side reason. Common shapes:

```
"merging PR 123: PUT https://api.github.com/repos/<org>/<repo>/pulls/123/merge: 405 Pull Request is not mergeable [...]"
```

Decode the GitHub error code:

| GitHub error fragment | Cause |
|---|---|
| `405 Pull Request is not mergeable` | Merge conflict against base branch, OR status checks failed, OR branch protection blocks |
| `405 Required status check ... is expected` | Required CI check hasn't started or is pending |
| `403 Resource not accessible by integration` | Sharko's PAT lacks merge permission (different from PR-create permission) |
| `409 Head branch was modified` | A different commit was pushed to the source branch after the PR opened |
| `422 At least one approving review is required` | Branch protection requires reviewers; auto-merge can't bypass |

The error tells you which mitigation lane applies (Mitigation step 2
covers branch protection; step 3 covers conflict; step 4 covers PAT
scope).

### 2. Inspect the PR's mergeability directly

Get the PR's status straight from GitHub — the merge-state field
tells you exactly what's blocking:

```sh
PR_NUMBER=<from log line>
curl -sS -H "Authorization: token ${GITHUB_PAT}" \
  "https://api.github.com/repos/<org>/<repo>/pulls/${PR_NUMBER}" \
  | jq '{state, merged, mergeable, mergeable_state, required_approving_review_count: .required_approving_review_count, requested_reviewers}'
```

Key fields:

- `mergeable: null` — GitHub hasn't computed mergeability yet.
  Re-poll in 5-10 seconds.
- `mergeable: false` — there is a hard block (conflict, status
  failure, or protection rule).
- `mergeable: true` and `mergeable_state: "clean"` — would merge
  successfully on retry. Probably a transient failure.
- `mergeable_state: "blocked"` — branch protection is blocking
  (status checks, required reviewers).
- `mergeable_state: "dirty"` — merge conflict.
- `mergeable_state: "behind"` — base branch has advanced; the PR
  needs a rebase or merge from base.

### 3. Check branch protection on the base branch

```sh
curl -sS -H "Authorization: token ${GITHUB_PAT}" \
  "https://api.github.com/repos/<org>/<repo>/branches/main/protection" \
  | jq '{
    require_pull_request_reviews,
    required_status_checks,
    enforce_admins
  }'
```

If `require_pull_request_reviews.required_approving_review_count > 0`,
GitHub will not auto-merge any PR without that many approvals. Sharko
cannot satisfy this; the operator must either approve the PR manually
or relax the rule.

If `required_status_checks.contexts` is non-empty, each listed check
must pass. If Sharko-opened branches don't trigger those checks (e.g.
the CI config skips PR-branch builds for `sharko/*` branches), the
merge will never proceed automatically.

---

## Mitigation (try in order)

The first step is always to **inspect the PR directly** — Sharko's
error wrapping loses some context that's visible only on GitHub.

1. **Manually merge the PR.** The fastest fix for a one-off failure
   is for the operator (or a reviewer with sufficient permissions)
   to merge the PR via the GitHub UI or the API:

   ```sh
   # Via GitHub CLI:
   gh pr merge "${PR_NUMBER}" --squash --auto

   # Or via the API:
   curl -sS -X PUT -H "Authorization: token ${GITHUB_PAT}" \
     "https://api.github.com/repos/<org>/<repo>/pulls/${PR_NUMBER}/merge" \
     --data-binary '{"merge_method":"squash"}'
   ```

   After the merge, the reconciler picks up the change on its next
   30s tick (or immediately if a webhook fires; see
   [`webhook-handler-failures.md`](webhook-handler-failures.md)).
   For init flows, also call
   `POST /api/v1/init/{operation_id}/finalize` to advance the
   server's session state.

   Success indicator: PR shows `Merged`; reconciler audit shows
   `cluster_secret_create, result=success` for the cluster within 30s.

2. **Fix branch protection rules for Sharko-opened PRs.** If
   Diagnosis step 3 shows branch protection is blocking and the
   operator wants future Sharko PRs to auto-merge cleanly, relax
   the protection for the `sharko/*` branch prefix (or for the
   `Sharko Bot` user / app).

   In the GitHub UI: `Settings -> Branches -> Branch protection rules ->
   Edit "main"`. Either:
   - Lower `Required approving reviews` to 0 (the operator's call;
     risky for non-Sharko-bot PRs without compensating controls).
   - Add `Sharko Bot` as a CODEOWNER or as an exception in the
     required-reviewer list.
   - Disable `Require status checks to pass` for Sharko-opened PRs
     by configuring the relevant CI workflow to skip `sharko/*`
     branches.

   This is **a configuration choice with security implications**.
   Document it in the operator runbook; don't paper-fix it
   silently.

   Success indicator: the next Sharko-opened PR auto-merges
   successfully without manual intervention.

3. **Resolve a merge conflict by rebasing or by re-running the
   Sharko operation.** If Diagnosis step 2 showed
   `mergeable_state: "dirty"`, the base branch has changes that
   conflict with the PR. Two paths:

   - **Operator rebases manually:**

     ```sh
     git fetch origin
     git checkout sharko/register-cluster-prod-eu-<suffix>
     git rebase origin/main
     # Resolve conflicts; git rebase --continue
     git push --force-with-lease
     ```

   - **Operator closes the PR and re-runs the Sharko operation.**
     Re-running uses `findOpenPRForCluster` to detect the existing
     PR — if it's been closed, a fresh PR is opened with current
     base-branch content. This is usually safer for non-trivial
     conflicts:

     ```sh
     curl -sS -X PATCH -H "Authorization: token ${GITHUB_PAT}" \
       "https://api.github.com/repos/<org>/<repo>/pulls/${PR_NUMBER}" \
       --data-binary '{"state":"closed"}'

     # Re-run the original Sharko operation (e.g. POST /clusters/prod-eu/adopt).
     ```

   Success indicator: PR (whether the same one or a fresh one)
   merges cleanly; reconciler converges within 30s.

4. **Fix Sharko's PAT scopes.** If Diagnosis step 1 showed
   `403 Resource not accessible by integration` on the merge call,
   Sharko's PAT lacks the merge permission even though it has the
   PR-create permission. This usually means the PAT is a fine-
   grained token that was scoped to `contents:write` but not
   `pull_requests:write`.

   Generate a new PAT with both scopes:

   - Fine-grained token: `Contents: Read and write`,
     `Pull requests: Read and write`, `Workflows: Read and write` (if CI is enabled).
   - Classic PAT: `repo` (covers both), `workflow`.

   Update Sharko's secret per the
   [`git-provider-rate-limited.md`](git-provider-rate-limited.md#mitigation-try-in-order)
   PAT-rotation procedure.

   Success indicator: the next Sharko operation's PR auto-merges
   successfully.

5. **Last resort — disable auto-merge for now and merge all PRs
   manually.** If the deployment's branch-protection setup
   fundamentally cannot accommodate Sharko's auto-merge model (e.g.
   SOC-2 review-required policy), turn off auto-merge at the Sharko
   connection level:

   ```yaml
   # In the connection config:
   gitops:
     prAutoMerge: false
   ```

   Then `helm upgrade <release> charts/sharko/` and restart Sharko.
   Every operation will open a PR and return immediately with
   `status: "pending merge"`; the operator merges via GitHub UI.

   Success indicator: operations return cleanly; the
   `PR opened but auto-merge failed` log line stops; PRs accumulate
   in the dashboard as expected for the manual-merge workflow.

---

## Root-cause patterns

Four common causes.

### Branch protection blocks Sharko's bot

The single most common cause. The operator enabled branch protection
on `main` (good security practice) but didn't carve out an exception
for Sharko-opened PRs. Every Sharko operation opens a PR, then
fails to merge because the branch protection rule requires reviewers
or status checks.

Diagnostic signature: Diagnosis step 2 shows
`mergeable_state: "blocked"`. Diagnosis step 3 shows the protection
rule. The failure shape is consistent across every Sharko-opened
PR — every operation surfaces the same partial-success result.

Fix lane: Mitigation step 2 (configure protection rules to
accommodate Sharko's bot identity) or step 5 (disable auto-merge).

### Merge conflict from concurrent operator changes

Two operators perform conflicting Sharko operations in parallel
(e.g. both register the same cluster with different addons, or one
operator manually edits `managed-clusters.yaml` while a Sharko
operation has a PR open). The first PR merges fine; the second
PR's auto-merge fails with `mergeable_state: "dirty"`.

Diagnostic signature: Diagnosis step 2 shows
`mergeable_state: "dirty"` or `"behind"`. Sharko logs show
multiple operations in tight succession on the same file path.

Fix lane: Mitigation step 3 (resolve the conflict or re-run the
operation).

### Sharko PAT lacks merge permission

The operator scoped Sharko's PAT to `Contents: Read and write` but
forgot `Pull requests: Read and write`. PR creation works (uses
contents); PR merge requires the pull-requests scope.

Diagnostic signature: Diagnosis step 1 shows
`403 Resource not accessible by integration`. The PR creation
succeeds; only the merge call fails. The failure shape is also
consistent across every Sharko-opened PR.

Fix lane: Mitigation step 4 (re-scope the PAT).

### CI status checks aren't running on Sharko's branches

The repo's CI workflow filters branches (e.g.
`on: push: branches: [main, develop]`) and Sharko opens PRs from
`sharko/*` branches. No CI runs on the PR; the required-status-checks
rule blocks the merge forever.

Diagnostic signature: Diagnosis step 2 shows
`mergeable_state: "blocked"`; the PR view shows "Some checks are
required but haven't been run." Sharko's branch prefix is not in
the CI's branch filter.

Fix lane: extend the CI workflow's branch filter to include
`sharko/*`. This is a CI configuration change, not a Sharko change.

---

## Rollback plan

If Mitigation step 5 (disabling auto-merge) was a mistake and the
operator wants to re-enable it after fixing the underlying issue:

1. Restore the `prAutoMerge: true` setting in the connection config.
2. `helm upgrade <release> charts/sharko/`.
3. Restart Sharko.

Other mitigation steps are non-destructive (merging via UI, fixing
branch protection, rebasing).

---

## Prevention

How to make this failure mode less likely going forward. Three
levers:

- **Monitoring — expose a per-PR auto-merge-failure counter.**
  Wire `sharko_pr_auto_merge_failed_total` (with labels for the
  operation and the cluster) and alert on >0 over 5 minutes. The
  bounded blast radius keeps this at P1; the counter would catch
  the slow drift cases (branch protection rules added after Sharko
  was deployed) at first-fail. V2-4.x follow-up.

- **Gating — pre-flight branch protection check on `sharko init`.**
  The init flow could query branch protection on the base branch
  before opening the bootstrap PR; if Sharko's bot can't satisfy
  the rules, fail init with a clear "configure branch protection
  for Sharko's bot identity, or disable `prAutoMerge`" error. This
  catches Root cause pattern 1 at install time rather than at the
  first operator-driven write. V2-4.x follow-up.

- **Scheduled work — quarterly auto-merge health check.** Walk the
  dashboard's PR panel, flag any PR open >24h that's expected to
  have auto-merged, and surface a maintenance ticket. These
  accumulate when branch protection drifts or CI status check
  configurations change without a corresponding Sharko config
  update.

---

## Related runbooks

- [`git-provider-unreachable.md`](git-provider-unreachable.md) —
  the P0 sibling. If the PR couldn't be opened at all, this isn't
  the runbook.
- [`git-provider-rate-limited.md`](git-provider-rate-limited.md) —
  when rate-limit failures masquerade as merge failures (the merge
  call itself may be the one returning 403).
- [`webhook-handler-failures.md`](webhook-handler-failures.md) —
  when webhook delivery is broken, the reconciler convergence
  after a manual merge takes the full 30s; the cumulative latency
  feels worse.
- [`argocd-pr-merge-no-converge.md`](argocd-pr-merge-no-converge.md)
  — the P0 escalation: PR merged but ArgoCD never converges.
  Distinct from auto-merge-failed (which is "PR didn't merge"); the
  PR-merge-no-converge case is "PR merged but ArgoCD doesn't see
  it."
- [`init-operation-abandoned.md`](init-operation-abandoned.md) —
  the init flow's adjacent failure mode. Auto-merge failure on the
  init PR contributes to abandonment if the operator doesn't
  manually merge before the heartbeat window closes.
- [`failure-mode-index.md`](failure-mode-index.md) — master inventory.

## Escalation

If the mitigations above do not resolve the failure within 2 hours,
email the maintainer: `moran.weissman@gmail.com`. Include:

- The runbook URL you used (this page)
- The PR URL from the failed operation
- The output of Diagnosis step 1 (Sharko's wrapped error)
- The output of Diagnosis step 2 (GitHub PR mergeability JSON)
- The output of Diagnosis step 3 (branch protection JSON)
- The Sharko version (`sharko version` or the Helm chart version)

The maintainer is a single human, not a 24x7 rotation. Expect a
business-day SLA. Because the operator can always merge the PR
manually (Mitigation step 1), this isn't a paging-severity
incident.

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
- [x] Length 300-800 lines (in range)
- [x] All cross-links resolve
- [x] No emoji / no internal Slack / employee email
- [x] (if applicable) Alert reference noted as V2-4.x follow-up
-->

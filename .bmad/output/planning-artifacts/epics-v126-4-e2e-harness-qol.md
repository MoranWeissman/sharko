---
stepsCompleted: [1, 2, 3]
inputDocuments:
  - task #187 (e2e progress output)
  - task #188 (kind cleanup hardening)
  - task #189 (seedActiveConnection URL validator — real product bug)
  - tests/e2e/lifecycle/cluster_helpers.go:289-294 (gitfake RepoURL site)
  - tests/e2e/harness (agent locates via Serena)
  - internal/api/connections.go (validator site — agent confirms)
workflowType: 'epics-and-stories'
project_name: 'sharko'
version: 'v126-4-e2e-harness-qol'
user_name: 'Moran'
date: '2026-05-21'
---

# V126-4 — e2e harness QoL bundle — Epic Breakdown

## Sprint goal (verbatim, locked 2026-05-21)

> "Standing e2e + sharko-dev.sh QoL items (#187-191). e2e bundle: progress output (#187) + cleanup hardening (#188) + validator fix (#189 — REAL product bug)." — Moran

3 tasks → 1 multi-surface story → 1 agent → 1 PR. Auto-merge per `feedback_auto_merge_when_green`.

## Branch + release strategy

- **Branch:** `dev/v126-4-e2e-harness-qol` from current `main` HEAD (commit `0a746410`)
- Single worktree, single agent
- Single PR; auto-merge when CI green
- NO release / NO tag

## Quality gates

- `go build ./...`, `go vet ./...`
- `go test ./internal/api/... -race -count=1` (validator unit test must pass)
- `go vet ./tests/e2e/...` + `go build ./tests/e2e/...` (harness compiles)
- `make test-e2e-clean` (new target — verify it runs cleanly even with zero leaks)
- DO NOT run `make test-e2e` from worktree (kind is slow — full e2e validation happens in PR CI)
- Swagger regen ONLY if `@Router` annotations change (validator-internal fix shouldn't trigger; agent confirms)
- NO `Co-Authored-By`, NO `--no-verify`, NO `git push` from worktree

## Role files

- `.claude/team/tech-lead.md`
- `.claude/team/go-expert.md` (validator fix + heartbeat goroutine + test seam)
- `.claude/team/test-engineer.md` (regression test + harness pattern)

## MCP tools

- **Serena MCP** — navigate `tests/e2e/harness/`, `tests/e2e/lifecycle/cluster_helpers.go`, `internal/api/connections.go`
- **Sequential-thinking MCP** — optional for the heartbeat goroutine lifecycle (start/stop with t.Cleanup)

## Scope guardrails

- ONLY: `tests/e2e/**` + `internal/api/connections.go` (validator) + `Makefile` (one new target)
- NO bash script. NO UI. NO Chart.yaml. NO release notes.
- NO SIGINT handler work (out of scope; standard `go test` can't reliably catch)

---

## Epic V126-4: e2e harness QoL bundle

### Story V126-4.1 — Progress heartbeat + cleanup hardening + validator fix (multi-surface, single agent)

**Subtasks:**

#### #187 — Progress heartbeat (cosmetic, anxiety-reducer)

1. Locate the kind harness (likely `tests/e2e/harness/kind.go` or `kind_harness.go` — agent confirms via Serena).
2. Add `t.Logf("[harness] starting kind cluster %s (typical: 60-90s)…", name)` at the start of every kind-touching test (cluster create, argocd apply, cluster register).
3. Add a background goroutine in the harness that emits `t.Logf("[harness] waiting for cluster %s: %ds elapsed…", name, elapsed)` every 30s while cluster create / argocd apply is in flight. Goroutine MUST be stopped via `t.Cleanup` (defer ticker.Stop + close done channel).
4. NO change to gotestsum format. NO Makefile change for this part.

#### #188 — Cleanup hardening

5. Audit every kind-touching test in `tests/e2e/**`. Confirm each has the pre-flight scan currently in `addon_cluster_test.go:148` ("harness: no kind clusters present, no stale cleanup needed"). Add where missing.
6. Make `kind delete cluster --name X` resilient: if it fails, fall back to `docker rm -f X-control-plane` (best-effort; log + continue).
7. New `Makefile` target `test-e2e-clean`:
   ```makefile
   test-e2e-clean:
   	@kind get clusters 2>/dev/null | grep -E '^sharko-e2e-' | xargs -I{} kind delete cluster --name {} || true
   	@docker container prune -f --filter "label=io.x-k8s.kind.cluster" >/dev/null
   	@echo "e2e cleanup complete"
   ```
   Confirm it works with zero leaks (echoes "complete", exit 0).

#### #189 — seedActiveConnection URL validator fix (REAL product bug, Option A)

8. Locate the validator. Search: `grep -rn "Git URL must contain owner/repo" internal/` — likely `internal/api/connections.go` or `internal/git/validator.go`. Agent confirms via Serena.
9. Fix shape (Option A): when `RepoURL` path doesn't contain owner/repo segments AND explicit `Owner` + `Repo` fields are both populated → use the explicit fields. Validator continues to reject only when BOTH path-parse fails AND explicit fields missing.
10. Add unit test in the validator's `*_test.go` covering:
    - Path-with-owner-repo + no explicit fields → accept (existing behavior preserved)
    - Path-without-owner-repo + explicit fields populated → accept (new behavior, fixes BUG)
    - Path-without-owner-repo + explicit fields empty → reject with friendly error (existing behavior preserved)
    - GitHub URL + explicit fields populated → accept (existing behavior preserved)
11. Confirm CI run https://github.com/MoranWeissman/sharko/actions/runs/25783650876 failure body `{"error":"invalid git URL: validation failed: Git URL must contain owner/repo (got: /sharko-e2e)"}` would now pass.
12. Test: `tests/e2e/lifecycle/cluster_helpers.go:289-294` does NOT need changes — once the validator accepts explicit-fields-override, the existing gitfake URL works as-is.

**Acceptance criteria:**

- Heartbeat log lines visible in `go test -v ./tests/e2e/...` output every 30s during cluster create
- `make test-e2e-clean` target exists + runs successfully with zero leaks present
- Validator unit test covers all 4 cases + passes
- `internal/api/...` unit tests pass with `-race -count=1`
- Harness compiles + vets clean
- No `@Router` annotation changes (no swagger regen needed) — agent verifies

**Commit:**

```bash
git commit -m "$(cat <<'EOF'
feat(e2e,api): harness heartbeat + cleanup hardening + Git URL validator explicit-fields override (V126-4.1)

Three e2e QoL items bundled into one story:

#187 — Heartbeat: t.Logf at start of every kind-touching test + 30s
"still waiting" goroutine in harness (stopped via t.Cleanup). gotestsum
no longer goes silent for 5+ min between fast-tests-done and first
kind-test-completes.

#188 — Cleanup hardening: pre-flight scan in every kind test (was only
in addon_cluster_test.go:148); resilient delete falls back to
docker rm -f X-control-plane when kind delete cluster fails; new
make test-e2e-clean for manual recovery.

#189 — Real product bug: seedActiveConnection's Git URL validator
demanded owner/repo in RepoURL path even when explicit Owner+Repo
fields were populated. Anyone with self-hosted Gitea on a non-standard
path or a corporate proxy hit a 400. Validator now accepts explicit
fields as override when path-parse fails. e2e test passes without
modification.

NOT in scope: SIGINT handler (standard go test can't reliably catch).
Deferred to a future sprint if Ctrl+C leaks remain a problem.

Refs: tasks #187 #188 #189, epics-v126-4-e2e-harness-qol.md V126-4.1
EOF
)"
```

---

## Sequencing + dispatch order

| Wave | Stories | Mode |
|------|---------|------|
| A | V126-4.1 | Serial (single multi-surface story, one agent) |

Total: 1 story, ~75-105 min agent runtime (validator fix is fast; harness audit + heartbeat goroutine is the bulk).

## Parallel-to-V126-5

This bundle dispatches in parallel with V126-5 (sharko-dev.sh DX). Surfaces are independent (Go test harness + Go API + Makefile vs. bash script) — no merge conflict.

---

## All OQs PRE-RESOLVED

Per `feedback_decide_dont_ask_technical_oqs`:

1. Branch → `dev/v126-4-e2e-harness-qol`
2. SIGINT handler → OUT of scope
3. Validator fix shape → Option A (explicit fields override path-parse fallback)
4. Heartbeat cadence → 30s
5. Cleanup fallback → `docker rm -f` best-effort
6. `make test-e2e` run from worktree → NO (CI handles it; ~10+ min locally)
7. Swagger regen → only if `@Router` changes (unlikely; agent confirms)
8. Auto-merge → YES once CI green

## Done definition

- [ ] V126-4.1 shipped on `dev/v126-4-e2e-harness-qol`
- [ ] All quality gates green
- [ ] PR opened
- [ ] CI green (including full e2e run)
- [ ] PR auto-merged
- [ ] Tasks #187, #188, #189 marked done

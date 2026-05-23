---
stepsCompleted: [1, 2, 3]
inputDocuments:
  - User dispatch 2026-05-23 — "i want NO gaps, at all. make a plan to address all of them.. now"
  - Orchestrator gap triage (post-v1.25.0-pre.0): #265 (pre-existing test failure), #264 (catalog-side line-level mutators), #82 (V2 hardening epic plan), GH release page for v1.25.0-pre.0, sprint-status.yaml flip, stale memory cluster_secrets_gap
  - Main HEAD: abbd72a7 — release: v1.25.0-pre.0 — schema envelope + cluster reconciler + DX bundle (#351)
workflowType: 'cleanup-sprint'
project_name: 'sharko'
version: 'zg1-zero-gaps-cleanup'
user_name: 'Moran'
date: '2026-05-23'
---

# ZG-1 — Zero-gaps cleanup (post-v1.25.0-pre.0)

## Sprint goal (verbatim, locked 2026-05-23)

> "i want NO gaps, at all. make a plan to address all of them.. now" — Moran

Close every gap enumerated post-v1.25.0-pre.0 in three parallel-then-serial waves: orchestrator-only tinies (Wave Z0) ‖ two code-fix agents (Wave A) → planning deliverable for V2 hardening (Wave B, separate skill invocation).

## Branch + release strategy

- **Wave Z0:** `chore/zg1-release-page-and-status-flip-2026-05-23` from main HEAD `abbd72a7`
- **Wave A.265:** `dev/zg1-265-harness-decode-fix` from main HEAD `abbd72a7`
- **Wave A.264:** `dev/zg1-264-catalog-mutator-retirement` from main HEAD `abbd72a7`
- All three PRs land independently; auto-merge per `feedback_auto_merge_when_green`
- **NO new tag this sprint** — cleanup work threads into the next release

## Quality gates (universal)

- NO `Co-Authored-By` trailers (CLAUDE.md hard rule)
- NO `--no-verify`
- NO `git push` from worktree, NO `git tag` from agent
- Edit-to-main-repo drift protocol: `$(git rev-parse --show-toplevel)` prefix discipline + post-batch `git status -s` self-check on main

## Forbidden

- NO sprint creep beyond locked gap items
- NO Chart.yaml bump (cleanup, not release)
- NO release notes update (threads into next release section)
- DO NOT touch `.claude/team/*.md` (refreshed in PR #350 2026-05-21)
- DO NOT touch `docs/design/` (implementation cleanup, not design)

---

## Wave Z0 — Orchestrator-only tinies (single chore PR, no agents)

Folded into ONE PR: `chore/zg1-release-page-and-status-flip-2026-05-23`.

### Z0.1 — GH release page for v1.25.0-pre.0

- `gh release create v1.25.0-pre.0 --prerelease --title "v1.25.0-pre.0 — schema envelope + cluster reconciler + DX bundle" --notes-file <notes>`
- Notes body: extract v1.25.0-pre.0 section from `docs/site/release-notes.md` (the consolidated section)
- Mirror v1.23.0-pre.0 release-page shape (isPrerelease=true)
- NO assets attachment (goreleaser binaries already attached automatically by release.yml; image + chart live in ghcr.io OCI not release-page assets)

### Z0.2 — sprint-status.yaml flip

Edit `.bmad/output/implementation-artifacts/sprint-status.yaml`:
- `epic-v1-25-0-pre-0-release: backlog` → `done   # tag v1.25.0-pre.0 (commit abbd72a7) shipped 2026-05-22; cosign verified; ghcr.io image + helm OCI chart published`
- `v1-25-0-pre-0-1-chart-bump-and-release-notes-consolidation: backlog` → `done   # PR #351 squash-merged 2026-05-22, commit abbd72a7`
- `v1-25-0-pre-0-2-tag-cut-and-pipeline-watch-and-smoke: backlog` → `done   # tag pushed; CI on tag #26277969875 green; Release workflow #26278085154 green; smoke: docker pull + helm pull + cosign verify all passed (Issuer: token.actions.githubusercontent.com)`
- `epic-v1-25-0-pre-0-release-retrospective: optional` (unchanged)

### Z0.3 — Memory cleanup (cluster_secrets_gap stale)

Edit `~/.claude/projects/-Users-weissmmo-projects-github-moran-sharko/memory/project_cluster_secrets_gap.md`:
- Prepend RESOLVED header: `**RESOLVED 2026-05-21:** V125-1-8 cluster reconciler shipped in PR #348 (commit 5966e244). Gap closed. Memory preserved below for historical context.`
- Leave original content intact below the header
- Update `MEMORY.md` index entry to read: `- [Cluster-secrets gap (resolved)](project_cluster_secrets_gap.md) — V125-1-8 reconciler shipped; kept for historical context`

### Chore-PR commit message

```
chore(zg1): GH release page + sprint-status flip + stale memory marker (Z0)

Wave Z0 of the zero-gaps cleanup sprint:
- Create GH release page for v1.25.0-pre.0 (isPrerelease=true)
- Flip sprint-status entries for v1.25.0-pre.0 release to done with
  tag/SHA/cosign annotations
- Mark cluster_secrets_gap memory RESOLVED (V125-1-8 shipped the
  reconciler in PR #348)

Refs: .bmad/output/planning-artifacts/zg1-zero-gaps-cleanup.md Z0
```

---

## Wave A — Parallel agent dispatches

### Story ZG1-A.265 — TestHarnessSharkoInProcess decode mismatch fix

**Source:** task #265. Pre-existing failure, not v1.25-caused (confirmed during V125-1-8 work).

**The bug:** `tests/e2e/harness/sharko_test.go:62-` decodes the health endpoint response as `map[string]string`, but the handler returns a struct with `cluster_test_available: bool` (and likely other typed fields). Decode fails on the bool→string mismatch.

**Fix shape (agent picks once they see the actual handler):**
- Find the handler (agent uses Serena `find_referencing_symbols` for `/api/v1/health` or `cluster_test_available`)
- Either define a small typed struct in the test that mirrors the handler's response shape, OR decode as `map[string]any` and assert on the bool field
- Pick whichever leads to the smaller, clearer diff
- Add ONE regression test that asserts the response shape contract so future renames break loudly

**Files in scope:**
- `tests/e2e/harness/sharko_test.go` (the failing test)
- POSSIBLY `internal/api/health.go` or wherever the handler lives — **only to read, not modify**. Handler is correct; test is wrong.

**NOT in scope:** changing the handler shape, refactoring the test harness, fixing other tests.

**Acceptance:**
- `go test ./tests/e2e/harness/... -run TestHarnessSharkoInProcess -race -count=1 -v` — PASS
- `go test ./tests/e2e/harness/... -race -count=1` — full surface PASS
- `go test ./internal/api/... -race -count=1` — no regression in handler tests
- `go build ./...` clean

**Role files (CLAUDE.md every-dispatch rule):**
- `.claude/team/tech-lead.md` (always)
- `.claude/team/go-expert.md` (test seam, encoding/json)
- `.claude/team/test-engineer.md` (every fix needs the test that previously failed to now pass)

**MCP tools:** Serena for handler navigation. Skip context7 (well-understood encoding/json).

**Branch:** `dev/zg1-265-harness-decode-fix` from main HEAD `abbd72a7`

**Estimated:** 30-45 min

**Commit message:**
```
fix(tests): align TestHarnessSharkoInProcess decode shape to handler contract (ZG1-A.265)

Pre-existing failure: handler returns cluster_test_available as bool;
test decoded the response as map[string]string and panicked on the
bool→string mismatch.

Fix: <variant agent picks — typed struct or map[string]any>. Added
regression test for the response-shape contract so future handler
renames break loudly.

Refs: task #265, .bmad/output/planning-artifacts/zg1-zero-gaps-cleanup.md A.265
```

---

### Story ZG1-A.264 — Catalog-side line-level mutator retirement (V125-1-8.3 mirror)

**Source:** task #264. V125-1-8.3 retired cluster-side mutators by replacing with envelope reader/writer (`models.LoadManagedClusters` + `models.SaveManagedClusters`). Catalog side still uses brittle line-level mutators with hard-coded 2-space indent assumptions — same risk as the bug that motivated 8.3.

**Fix shape (mirror V125-1-8.3 exactly):**
1. Identify all catalog-side line-level mutators in `internal/gitops/yaml_mutator.go` + `yaml_mutator_catalog_test.go` family (agent inventories via Serena)
2. Replace each mutator's body with envelope-aware parse-mutate-marshal via `models.LoadAddonCatalog` + `models.SaveAddonCatalog` (per V125-1-9.2's `MarshalAddonCatalog` writer)
3. Preserve idempotency semantics where callers depend on them (V125-1-8.3 kept silent-skip-on-duplicate for `AddClusterEntry` because `adoptSingleCluster` retry logic depended on it — agent does the same audit for catalog-side callers; if `AddCatalogEntry`/sibling has callers depending on silent-skip, preserve)
4. Update tests in `yaml_mutator_catalog_test.go` to assert envelope shape rather than line-level position
5. Run `./sharko validate-config configuration/` as a final regression check on V125-1-9 envelope surface

**Files likely in scope (agent verifies):**
- `internal/gitops/yaml_mutator.go` — catalog-side mutators (`AddCatalogEntry`, `RemoveCatalogEntry`, `UpdateCatalogEntry`, possibly `setCatalogLabel`)
- `internal/gitops/yaml_mutator_catalog_test.go` — test updates
- POSSIBLY `internal/gitops/yaml_mutator_envelope_test.go` — extend envelope coverage
- POSSIBLY callers in `internal/catalog/` + `internal/orchestrator/` if they relied on side-effects beyond the documented API

**NOT in scope:**
- Cluster-side mutators (already done in V125-1-8.3)
- Refactoring `internal/catalog/loader.go` (curated marketplace catalog ≠ user's deployed catalog; agent must NOT confuse the two — same lesson from V125-1-8.3 dispatch correction)
- New endpoints, schema changes, swagger regen

**Acceptance:**
- `go build ./...` clean
- `go vet ./...` clean
- `go test ./internal/gitops/... -race -count=1` PASS (existing + new envelope tests)
- `go test ./internal/catalog/... -race -count=1` PASS (no regression in caller tests)
- `go test ./internal/orchestrator/... -race -count=1` PASS (catalog-touching orchestrator paths)
- `./sharko validate-config configuration/` exits 0
- No 2-space-indent string literals remain in catalog-side mutator code (grep gate)

**Role files (CLAUDE.md every-dispatch rule):**
- `.claude/team/tech-lead.md` (always)
- `.claude/team/go-expert.md` (envelope refactor pattern)
- `.claude/team/test-engineer.md` (test updates)
- `.claude/team/code-reviewer.md` (mirror-pattern correctness check)

**MCP tools:** Serena for code navigation. Skip context7 (envelope writer already understood from V125-1-8.3).

**Branch:** `dev/zg1-264-catalog-mutator-retirement` from main HEAD `abbd72a7`

**Estimated:** 1.5-2hr

**Commit message:**
```
refactor(gitops): retire catalog-side line-level mutators in favor of envelope reader/writer (ZG1-A.264)

V125-1-8.3 mirror for the catalog side. Replaces AddCatalogEntry /
RemoveCatalogEntry / UpdateCatalogEntry / <sibling list from agent>
in internal/gitops/yaml_mutator.go with parse-mutate-marshal via
models.LoadAddonCatalog + models.SaveAddonCatalog (the V125-1-9.2
envelope writer).

Eliminates 2-space indent assumptions that broke against enveloped YAML
(same brittleness class as the cluster-side bug fixed in V125-1-8.3).

Idempotency preserved for AddCatalogEntry per existing caller retry
semantics in <agent-identified caller list>.

Closes #264. Tests updated to assert envelope shape.

Refs: .bmad/output/planning-artifacts/zg1-zero-gaps-cleanup.md A.264
```

---

## Wave B — V2 hardening planning (separate skill invocation)

**NOT dispatched from THIS plan.** After Wave A merges, orchestrator invokes `bmad-create-epics-and-stories` for task #82 with this scope frame:

**V2 hardening = production-launch readiness for v2.0.0. INCLUDES:**
- Perf baselines + SLO targets per critical path (cluster reg, addon deploy, catalog scan, PR open→merge convergence)
- Logging hardening: slog adoption sweep for remaining `log.*` callers; structured field consistency; correlation IDs across reconciler + prtracker + orchestrator
- Error-budget telemetry: prometheus metrics for the SLO surfaces; runbook for breaching budgets
- Runbook gaps audit: which operator-facing failure modes don't have a `docs/site/operator/*.md` runbook yet
- Deprecation pass: surface anything left from V125-1-11 compat shim era; confirm typed ProviderConfig is the only path; remove dead compat code
- V1.x → v2.0.0 migration shim audit: zero shims means clean v2.0.0 cut
- CNCF maturity gap closure: per `project_attribution_design` ~40% to incubation — list the specific gates we haven't met

**EXCLUDES (V3+ backlog per `project_v3_backlog`):**
- Fine-grained RBAC
- SSO
- Multi-ArgoCD
- Operator mode (CRDs + admission webhook + binary split)
- Rule-based auto-merge
- Advanced metrics beyond SLO surfaces

**Inputs Wave B will load:**
- Memory `project_v3_backlog`
- Memory `project_attribution_design`
- Memory `project_sharko_roadmap`
- `.claude/team/product-manager.md`, `architect.md`, `project-manager.md` (refreshed PR #350)

**Output:** `.bmad/output/planning-artifacts/epics-v2-hardening.md`

---

## Sequencing + dispatch order

| Wave | Stories | Mode | Time |
|------|---------|------|------|
| Z0   | 3 tinies in ONE chore PR | Orchestrator-only | ~10 min total |
| A    | ZG1-A.265 ‖ ZG1-A.264 | Parallel agent dispatches | 30-45 min ‖ 1.5-2hr (gated by .264) |
| B    | #82 V2 hardening plan | Separate `bmad-create-epics-and-stories` invocation | ~20 min skill run |

Best case wall-clock: ~2.5 hr to all-gaps-closed (Z0 + A.264 + B serial).

## Risk register

| Risk | Likelihood | Mitigation |
|------|-----------|-----------|
| #264 catalog-side has more callers depending on side-effects than expected | Med | Agent embeds code-reviewer role; mirror-pattern audit against V125-1-8.3 commits before refactor |
| #265 handler shape isn't the actual root cause | Low | Agent confirms by reading handler first, only then fixes test |
| GH release page CLI flags differ from v1.23 expectations (e.g., wrong notes format) | Low | Mirror v1.23.0-pre.0 release directly via `gh release view v1.23.0-pre.0` as reference |

## All OQs PRE-RESOLVED

Per `feedback_decide_dont_ask_technical_oqs` (count: 9 locked, 0 surfaced to maintainer):

1. Wave order → Z0 ‖ A.265 + A.264 (parallel); B is post-Wave-A separate invocation
2. PR #309 catalog-scan → OUT of scope (content review ≠ product cleanup)
3. V2 hardening scope → production readiness; V3+ excluded
4. #265 fix shape → align test to handler (handler correct)
5. #264 fix shape → mirror V125-1-8.3 exactly
6. Memory cleanup → RESOLVED header + preserve history (don't delete)
7. Auto-merge → YES on all 3 PRs
8. Stop point → after #82 plan lands (V2 hardening itself is future sprints)
9. Z0 bundling → ONE chore PR (cohesive)

## Done definition

- [ ] Wave Z0 chore PR shipped: GH release page exists + sprint-status flipped + memory marked RESOLVED
- [ ] Wave A.265 PR shipped: `TestHarnessSharkoInProcess` passes
- [ ] Wave A.264 PR shipped: catalog-side mutators retired; envelope writer used
- [ ] Wave B planning artifact shipped: `epics-v2-hardening.md` exists with locked V2 scope
- [ ] All ZG-1 sprint-status entries flipped to `done`

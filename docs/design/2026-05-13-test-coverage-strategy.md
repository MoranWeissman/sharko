# Test Coverage Strategy — Sharko E2E

> **Status:** Discussion synthesis (2026-05-13). Captures the planning conversation after V125-1-7.x cascade + V124-5.3 (smoke.sh orphan-cascade Phase 6) shipped. Maintainer pushed: "in our e2e tests we need to cover all functionalities — every. little. function. api. has."
>
> **Trigger:** Live smoke run shows 57/57 PASS, but honest coverage is ~15-20% of API surface. Maintainer wants comprehensive coverage. Question: smoke.sh vs proper Go e2e harness, and at what scope.
>
> **Outcome:** Recommendation laid out below — incremental smoke phases now, V2 Epic 7-1 in parallel for the long-term home.

---

## 1. The framing that matters — three tiers, not one

| Tier | Speed | What it catches | Sharko's current state |
|---|---|---|---|
| **Unit tests** (per-package `_test.go`) | ms | Isolated logic, edge cases, mocked deps | 300+ tests, healthy |
| **Smoke** (`./scripts/smoke.sh`) | ~20s | Gross regressions across many endpoints | 57 checks today; covers ~6 endpoints + 1 lifecycle |
| **Functional E2E** (`tests/e2e/` Go) | minutes | Full lifecycle flows with real kind+ArgoCD+git | 1 cached test today (basically a placeholder) |

**Key insight:** "Every function" doesn't have to live in smoke.sh — too fat = too slow = nobody runs it. The right home for most lifecycle coverage is **tests/e2e in Go** (the harness V2 Epic 7-1 already exists for; currently stub-status).

## 2. What's NOT covered today (honest gap analysis)

| Surface | Endpoints | Coverage today |
|---|---|---|
| Cluster register | POST /clusters | ✅ orphan path only (Phase 6); managed path = no |
| Cluster remove | DELETE /clusters/{name} | ❌ |
| Cluster batch / discover / adopt / unadopt | 4 endpoints | ❌ |
| Cluster test / refresh | 2 endpoints | ❌ |
| Addon catalog CRUD | POST/PATCH/DELETE /addons | ❌ |
| Addon enable/disable per cluster | POST .../enable, .../disable | ❌ |
| Addon upgrade (global + per-cluster) | 2 endpoints | ❌ |
| Values editor (global + per-cluster) | PUT /addons.../values | ❌ |
| Marketplace add | POST /catalog/validate + add | ❌ |
| AI annotate / summary | 2 endpoints | ❌ |
| Init / bootstrap | POST /init | ❌ |
| Connections CRUD | POST/PATCH/DELETE /connections | ❌ |
| User CRUD + RBAC | 4 endpoints + role enforcement | ❌ |
| API keys CRUD | 2 endpoints | ❌ |
| PR tracking | GET /prs + polling | ❌ |
| Notifications | GET /notifications | ✅ read only (Phase 3); lifecycle = no |

**Total: ~30 untested endpoints, ~12 lifecycle flows.** Production-product bar this isn't yet. Pre-V2.0.0 is the gap-closing window.

## 3. Two paths

### Path A — Incremental: extend smoke.sh, one lifecycle per story

~6-8 more phases gets full lifecycle coverage. Each story: 30-60 min agent dispatch, 5-15 added assertions, ~doubles smoke runtime per phase added. Lands in days each.

- **Pro:** Continuous coverage gain, easy to ship, no upfront investment, learns as we go
- **Con:** smoke.sh becomes fat (~150 assertions in a single bash file = harder to maintain), bash isn't ideal for complex assertions (no proper subtest grouping, no helpers without macros, hard to mock things)

### Path B — Proper functional E2E in Go (V2 Epic 7-1)

Build `tests/e2e/lifecycle_test.go`, `addon_test.go`, etc. — Go subtests with helpers for "register cluster", "wait for PR", "assert addon healthy", etc. Each test is a real Go function with `t.Run("..." func(t *testing.T) {...})` proper assertions.

- **Pro:** Maintainable forever, runnable in CI (with appropriate kind setup), the natural home, Go's testing framework gives subtests/parallelism/cleanup, much easier to add coverage incrementally once harness exists
- **Con:** 1-2 weeks of upfront work; coverage doesn't grow until harness is mostly built; harness needs `kindprovisioner`, `gitfaker`/`live-gh`, mocks-vs-real toggles

## 4. Recommendation — Both, sequenced

1. **NOW — add 2-3 high-value phases to smoke.sh** (cluster lifecycle, addon lifecycle, marketplace flow). Cheap, immediate. Covers the most-used surfaces. Each is one V124-5.N story. This unblocks confidence in NEW work without waiting for the Go suite.
2. **In parallel / next sprint — start V2 Epic 7-1 scoping** for the comprehensive Go e2e suite. The smoke phases inform what helpers the Go suite needs (provisioning, polling, cleanup primitives). When Epic 7-1 lands, retire the heaviest smoke phases — keep smoke as quick-feedback-loop only.

### Why both rather than just B

Building Epic 7-1 takes weeks. In the meantime we'll keep shipping fixes and features. Every shipped change without regression coverage compounds risk. Smoke phases close that gap in days; Epic 7-1 closes it permanently in weeks.

### Why both rather than just A

smoke.sh in bash will hit a wall around 150 assertions — file becomes unmaintainable, test interactions get complex, no proper isolation between phases. Go e2e is the long-term home regardless. The smoke work isn't wasted — it's a forcing function for the helpers that Epic 7-1 needs.

## 5. Concrete next stories (Path A)

**V124-5.7: smoke.sh Phase 7 — full cluster lifecycle**
- POST /clusters with kubeconfig + auto-merge → expect cluster appears in managed list
- GET /clusters/{name} → assert returned details (server URL, K8s version, addons)
- POST /clusters/{name}/test → expect 200 + reachable=true
- DELETE /clusters/{name} → expect cleanup (Secret gone, managed-clusters.yaml entry removed)
- ~8 assertions, ~45s additional smoke runtime
- Pattern mirrors Phase 6 (orphan-cascade): register → assert → action → assert cleanup

**V124-5.8: smoke.sh Phase 8 — addon lifecycle**
- POST /addons (add catalog entry) → assert in catalog
- POST /clusters/{name}/addons/{addon}/enable → assert label on cluster Secret
- POST /addons/{name}/upgrade → assert version bumped + PR opened
- POST /clusters/{name}/addons/{addon}/disable → assert cleanup
- DELETE /addons/{name} → assert removed from catalog
- ~10 assertions

**V124-5.9: marketplace add flow**
- POST /catalog/validate {url:...} → expect schema valid
- POST /catalog/sources/refresh → expect new entries
- GET /catalog/addons → assert new addon visible
- ~5 assertions

**V124-5.10: connections / init flow**
- POST /connections (configure ArgoCD + git) → expect saved
- POST /connections/{name}/test → expect ok
- POST /init → expect operation_id + poll to completion
- ~6 assertions

**V124-5.11: user / RBAC flow**
- POST /users {role:viewer} → expect created
- Login as viewer → try write endpoint → expect 403
- DELETE /users/{name} as admin → expect cleanup
- ~6 assertions

Each story is incremental, ~30-60 min agent dispatch, follows the V124-5.3 pattern (one phase, skip-if-prereq-missing, idempotent).

## 6. Concrete plan for Epic 7-1 (Path B, when ready)

**Goal:** Replace smoke.sh phases 6+ with proper Go subtests under `tests/e2e/`. Keep smoke.sh phases 1-5 (the lightweight ones — pre-flight, CLI sweep, read sweep, validation pins, cached Go test) as a fast-feedback loop.

**Structure (rough):**
```
tests/e2e/
  harness/            # provisioning, helpers, fixtures
    kind.go           # spin up / tear down kind cluster
    sharko.go         # boot Sharko Pod against the kind
    gitfake.go        # in-memory git server OR live GH org
    fixtures.go       # kubeconfig builder, SA helpers
  lifecycle/
    cluster_test.go   # register-managed-remove
    addon_test.go     # catalog CRUD + enable/disable + upgrade
    values_test.go    # editor + per-cluster overrides
    orphan_test.go    # the V125-1-7 cascade (port from Phase 6)
  flows/
    marketplace_test.go
    init_test.go
    rbac_test.go
    pr_tracking_test.go
  go.mod              # separate module? or share root?
```

**Open questions for Epic 7-1 scoping:**
- Real GH vs in-memory git? (live GH gives true integration; in-memory is faster, more reproducible)
- Should this run in CI on every PR, or nightly only? (full e2e is minutes, CI cost is real)
- Kind provisioning: per-test cluster (clean state) or per-suite (faster)?
- Sharko Pod boot strategy: helm install vs `go run ./cmd/sharko serve` direct?

Defer answering these until incremental smoke phases stabilize and the harness needs become concrete.

## 7. Decision summary

| Decision | Status |
|---|---|
| Current smoke coverage is insufficient for V2.0.0 | ✅ agreed |
| Need both smoke (quick) AND proper Go e2e (comprehensive) | ✅ agreed |
| Start with incremental smoke phases (V124-5.7..V124-5.11) | ✅ proposed |
| Epic 7-1 (Go e2e suite) becomes home for everything heavier | ✅ proposed |
| Retire smoke phases 6+ once Epic 7-1 ships | ✅ proposed |
| Specific scope of V124-5.7 (next dispatch) | ⏳ awaiting maintainer green-light |

---

## 8. Cross-references

- `scripts/smoke.sh` — V124-5.6 (always-refresh auth + Phase 6 orphan-cascade), `dev/v1.24-cleanup` HEAD `c27cde6e`
- `tests/e2e/` — stub Go e2e suite (currently 1 cached test)
- `.bmad/output/implementation-artifacts/sprint-status.yaml` — V2 Epic 7 (4 stories backlog including 7-1)
- `docs/design/2026-05-12-v125-architectural-todos.md` — V125-1-7-fix epic closed (3-story cascade verified live)
- Memory: `feedback_fix_bugs_dont_stop.md` — fix-and-ship-without-asking standing rule (broadened 2026-05-13)
- Memory: `feedback_agent_dispatch_worktree_isolation.md` — agent worktree safety (Layer 1 of V124-5.5 incident prevention)

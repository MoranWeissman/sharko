---
stepsCompleted: [1, 2, 3]
inputDocuments:
  - docs/design/2026-05-11-cluster-secret-reconciler-and-gitops-stance.md (primary, all 17 sections)
  - docs/design/section-3-template-and-secrets.md (the "PR Is the Gate" design this partially supersedes)
  - .bmad/output/planning-artifacts/epics-v125-1-9.md (paired predecessor — sets read-contract V125-1-8 depends on)
  - .planning task #257 (yaml_mutator.go indent assumptions flagged by V125-1-9.1 agent)
workflowType: 'epics-and-stories'
project_name: 'sharko'
version: 'v125-1-8'
user_name: 'Moran'
date: '2026-05-21'
---

# V125-1-8 — Cluster reconciler + ownership label + GitOps stance fix — Epic Breakdown

## Overview

V125-1-8 is the **second of two paired V125 architectural epics**. V125-1-9 (shipped 2026-05-21 in PR #346) made `managed-clusters.yaml` and `addon-catalog.yaml` self-describing + schema-validated. V125-1-8 builds the **stateless reconciler goroutine** that turns those YAML files into the actual source of truth for ArgoCD cluster Secret state.

The reconciler closes a real bug class observed in production-like testing: today's pre-merge ArgoCD Secret creation orphans Secrets when a registration PR is closed-without-merge. The architectural fix is **Sharko-as-reconciler** (per design doc §7 Option E): a goroutine pattern mirroring `internal/prtracker/tracker.go` that reads `managed-clusters.yaml` from git via REST API (no clone), lists Sharko-labeled Secrets in argocd ns, computes the diff, and converges.

It also closes V125-1-7's safety gap: today, "Delete cluster Secret" can silently destroy user-managed Secrets because Sharko-created Secrets carry no ownership label. V125-1-8 adds `app.kubernetes.io/managed-by: sharko` as the universal gate.

After V125-1-8 lands → cut `v1.25.0-pre.0` tag (separate sprint).

## Driving sources

- **`docs/design/2026-05-11-cluster-secret-reconciler-and-gitops-stance.md`** — primary scope. §12 enumerates V125-1-8's exact concrete deltas. §13 settles the operator-vs-stateless-reconciler architectural question with a definitive recommendation. §7 Option E details the chosen pattern. §8 + §10 explain how the goroutine reconciles without being a real K8s operator. §9 defines the two-direction policy.
- **`docs/design/section-3-template-and-secrets.md`** — "The PR Is the Gate" design that V125-1-8 partially supersedes. §12 note: "Lands cleanly before V125-1-8 so the reconciler reads against a stable, validated contract" — describing V125-1-9's role. V125-1-8 partially supersedes the pre-creation pattern.
- **`epics-v125-1-9.md`** — paired predecessor. The validator (`schema.DefaultValidator()` in `internal/schema/`), envelope-aware reader paths (`models.LoadManagedClusters` + `config.ParseClusterAddons`), and `sharko validate-config` CLI all become reconciler dependencies.
- **Task #257** — `internal/gitops/yaml_mutator.go` indent assumptions flagged during V125-1-9.1. Disposition: **in-scope for Story 8.3** (replace with parse-mutate-marshal pattern using V125-1-9's envelope-aware `SaveManagedClusters`). Not a separate retirement story.

## Scope frame (verbatim from design doc §12 V125-1-8)

### Concrete deltas (design doc §12)

1. New package `internal/clusterreconciler` — ~200 LoC mirroring prtracker structure
2. Add ownership label `app.kubernetes.io/managed-by: sharko` on every Sharko-created Secret. Pre-prod product (no operators in field) → no one-shot migration of existing Secrets needed (per `feedback_realistic_framing`)
3. Wire `reconciler.Start(ctx)` into `cmd/sharko/serve.go` near the prtracker bootstrap
4. Wire `prTracker.SetOnMergeFn` to call `reconciler.Trigger()` for immediate post-merge reconcile
5. Remove pre-merge `argoSecretManager.Ensure` and direct-API `RegisterCluster` calls from `internal/orchestrator/cluster.go`
6. Remove `argoSecretManager` interface entirely if no remaining caller uses it

### Plus tightening (design doc §12 V125-1-7 sub-bullet)

- Add ownership label check to `handleDeleteOrphanCluster` (refuses delete without `app.kubernetes.io/managed-by: sharko`)
- Orphan resolver only surfaces Secrets with sharko label (others = V125-2 Adopt territory)
- Rename UI button "Delete cluster Secret" → "Discard cancelled registration" or "Clean up orphan registration"

### Locked-in architectural decisions (design doc §7, §8, §9, §10, §13)

1. Pattern: stateless reconciler goroutine (NOT real K8s operator) — design §13
2. Ownership label: `app.kubernetes.io/managed-by: sharko` — design §5, §7-E, §12
3. Cadence: 30s default poll + onMergeFn trigger for low-latency post-merge — design §8, §12
4. Direction: one automatic (git → ArgoCD), one human-initiated (ArgoCD → git via Adopt) — design §9
5. Self-healing: yes for labeled-Secret accidental deletion — design §9
6. Single-pod assumption: yes; no HA / leader election in V125 — design §13
7. Git access: REST API (no clone, no disk) — design §10
8. No webhook in V125: 30s poll + prTracker `onMergeFn` is sufficient — design §10, §14
9. Vault retry: "fail this tick, log, try again next tick" — design §14
10. Adopt UI: out of V125-1-8 scope (V125-2 backlog) — design §12
11. Single Secret per cluster: keep — design §14
12. Migration approach: greenfield (no one-shot migration; pre-prod product) — `feedback_realistic_framing`
13. yaml_mutator retirement: REPLACE line-level mutators with parse-mutate-marshal using V125-1-9's `SaveManagedClusters` — handles task #257

## Branch + release strategy

- **Branch:** `dev/v125-1-8-cluster-reconciler` cut from current `main` HEAD `57c01a5a` (post V125-1-9 chore-merge)
- All stories merge into this branch via per-story FF (same pattern as V125-1-9)
- **Single sprint PR at the end:** `dev/v125-1-8-cluster-reconciler → main`
- **Auto-merge** per `feedback_auto_merge_when_green` when CI green
- **V125 release coupling:** after this sprint lands → separate sprint cuts `v1.25.0-pre.0` tag (Chart.yaml bump + release notes covering V125-1-9 + V125-1-8 + interim V125-1-10/11/13.x/13.y + V126-1..5). NOT in this sprint.

## Lean-workflow expectations

- Every dispatch embeds `.claude/team/tech-lead.md` + `.claude/team/architect.md` (architectural epic)
- Implementation stories add `.claude/team/go-expert.md`
- Stories touching the orchestrator add `.claude/team/k8s-expert.md` (cluster Secret CRUD, ArgoCD API conventions)
- Story 8.2 (V125-1-7 UI tightening) adds `.claude/team/frontend-expert.md`
- Final docs story adds `.claude/team/docs-writer.md`
- Worktree-isolated per `feedback_agent_dispatch_worktree_isolation`
- **Edit-to-main-repo drift warning** carried forward from V125-1-9 (bit 4 of 7 agents) — every dispatch prompt includes the `$(git rev-parse --show-toplevel)` guidance + post-batch `git status -s` self-check on main
- Single commit per story; commit prefixes `feat(reconciler):` / `refactor(orchestrator):` / `fix(ui):` / `docs(reconciler):`

## Quality gates (per story baseline)

- `go build ./...`
- `go vet ./...`
- `go test ./... -race -count=1`
- `make test-e2e` for stories touching cluster-lifecycle surfaces (kind harness from V125-1-13)
- `make generate-schemas` zero-diff (envelope types unchanged this sprint; regression check)
- `./sharko validate-config templates/bootstrap/configuration/` exits 0 (V125-1-9 regression check)
- `swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal` ONLY if `@Router` annotations change (unlikely; only Story 8.5 might if adopting endpoint changes)
- `mkdocs build --strict` for Story 8.5 (docs)
- NO `Co-Authored-By` trailers
- NO `--no-verify` / hook skipping
- NO `git push` from worktree, NO `git branch -f`, NO `git update-ref`

## Forbidden

- NO frontend work beyond Story 8.2's V125-1-7 button rename (no new screens, no new components)
- NO Helm chart values changes (reconciler tick + label-key are hardcoded for V125; configurability deferred)
- NO version bump / NO release tag / NO Chart.yaml edit (next sprint)
- NO CRD installation / admission webhook / operator-mode binary (V3+)
- NO leader election / HA work (V2 backlog per design §14)
- NO webhook for sub-30s reconcile latency (V2 backlog per design §14)
- NO Adopt UI work (V125-2 backlog)

---

## Epic V125-1-8: Cluster reconciler + ownership label + GitOps stance fix

### Story 8.0 — Scaffold reconciler package + ownership label utilities (Wave A0)

Mirrors A0 pattern from V125-1-9: small, scaffold-only, single agent, ~30 min. Sets shared types + helpers that Stories 8.1-8.4 consume.

**Files to create:**

1. `internal/clusterreconciler/reconciler.go` — `Reconciler` struct skeleton (NOT the PollOnce logic — that's Story 8.1):
   - Fields: `cmStore cmstore.Store`, `gitProvider func() gitprovider.GitProvider`, `argoClient argocd.Client`, `vault credprovider.Provider`, `auditFn func(audit.Entry)`, `tickInterval time.Duration`, `triggerCh chan struct{}`, `stopCh chan struct{}` (mirror prtracker shapes)
   - Methods: `New(deps...) *Reconciler`, `Start(ctx context.Context)`, `Stop()`, `Trigger()`, private `PollOnce(ctx)` returning `error` — stub implementation that logs "reconciler tick (not yet implemented)" and returns nil
   - `defaultTickInterval = 30 * time.Second` constant
2. `internal/clusterreconciler/labels.go` — ownership label utilities:
   - `const LabelManagedBy = "app.kubernetes.io/managed-by"`
   - `const LabelValueSharko = "sharko"`
   - `func IsManagedBySharko(secret *corev1.Secret) bool`
   - `func ApplyManagedBySharkoLabel(secret *corev1.Secret)` (idempotent — no-op if already labeled)
3. `internal/clusterreconciler/reconciler_test.go` + `labels_test.go` — minimal tests:
   - `TestReconciler_Lifecycle_StartStop_NoTickFires` — start, immediately stop, no panics
   - `TestReconciler_Trigger_QueuesPoll` — trigger pre-start does NOT panic; trigger post-start does NOT block (channel buffer = 1)
   - `TestLabels_IsManagedBySharko_FalseWhenAbsent`
   - `TestLabels_ApplyManagedBySharkoLabel_Idempotent`

**Acceptance:**
- `go build/vet/test ./internal/clusterreconciler/...` clean
- No new dependencies in `go.mod`
- Scaffold compiles even though `PollOnce` is stub

**Effort:** S (~30-60 min agent)

**Commit:** `feat(reconciler): scaffold internal/clusterreconciler package + ownership label utilities (V125-1-8.0)`

---

### Story 8.1 — Reconciler core: git → argocd diff + act (Wave A)

Real `PollOnce()` implementation. Depends on 8.0 scaffold. Single agent.

**Files to modify:**

- `internal/clusterreconciler/reconciler.go` — implement `PollOnce(ctx)`:
  1. Read `managed-clusters.yaml` via gitProvider (use `models.LoadManagedClusters` from V125-1-9 — already envelope-aware + schema-validated)
  2. List Secrets in argocd namespace with label `app.kubernetes.io/managed-by=sharko`
  3. Compute set diff: `in-git ∖ in-argocd` (need to create); `in-argocd ∖ in-git` (need to delete)
  4. For each cluster in `in-git ∖ in-argocd`: fetch creds from vault → call internal helper `createArgoSecret(name, creds)` → label with sharko ownership before writing
  5. For each Secret in `in-argocd ∖ in-git` (already-labeled): delete via argocd client
  6. Audit-log every action (creation + deletion) via `auditFn`
  7. Idempotent: re-running with no changes produces zero mutations
- `internal/clusterreconciler/reconciler.go` — wire `Start(ctx)` to call `PollOnce` every `tickInterval` AND on every `triggerCh` signal (select pattern from prtracker)
- New test file `internal/clusterreconciler/poll_test.go` with fake k8s client (`k8s.io/client-go/kubernetes/fake`) + fake git provider:
  - `TestPollOnce_NewClusterInGit_CreatesLabeledSecret`
  - `TestPollOnce_ClusterRemovedFromGit_DeletesLabeledSecret`
  - `TestPollOnce_UnlabeledSecret_LeftAlone` (V125-2 Adopt territory — reconciler doesn't touch)
  - `TestPollOnce_NoChanges_Idempotent` (run twice → zero mutations on 2nd run)
  - `TestPollOnce_GitFetchFails_PreservesState_LogsAudit` (vault/git transient failure handling per §14)
  - `TestPollOnce_VaultFailsForOneCluster_OthersStillReconcile` (per-cluster error isolation)
  - `TestPollOnce_InvalidYAML_RejectedNotApplied` (V125-1-9 validator integration — bad YAML returns error, no partial reconcile)

**Acceptance:**
- All 7 tests pass
- `go test ./internal/clusterreconciler/... -race -count=1` clean
- No new dependencies beyond `k8s.io/client-go/kubernetes/fake` (test-only)

**Effort:** M (~2-3 hours agent runtime)

**Commit:** `feat(reconciler): reconciler core git-to-argocd diff + act (V125-1-8.1)`

---

### Story 8.2 — V125-1-7 tightening: label gate + UI button rename (Wave B, parallel with 8.3)

Adds the ownership-label check to delete paths + renames the confusing UI button. Independent surface from 8.3 (UI + orphan API vs orchestrator pre-merge code).

**Files to modify (backend):**

- `internal/api/clusters_orphan_delete.go` (`handleDeleteOrphanCluster`) — REFUSE delete if Secret lacks `app.kubernetes.io/managed-by=sharko`. Return 400 with friendly error: `"this Secret was not created by Sharko (no managed-by label); refusing to delete. If you want to bring it under management, use the Adopt action."`
- `internal/api/clusters_orphans.go` — orphan resolver filters by sharko label (don't surface unlabeled Secrets — those are V125-2 Adopt territory)
- Tests in `internal/api/clusters_orphan_delete_test.go` + `clusters_orphans_test.go`:
  - `TestHandleDeleteOrphan_UnlabeledSecret_Reject400`
  - `TestHandleDeleteOrphan_LabeledSecret_DeletesAsBeforeShipped`
  - `TestOrphanResolver_OnlySurfacesLabeledSecrets`

**Files to modify (UI):**

- `ui/src/views/Clusters*.tsx` (find via Serena — current button text is "Delete cluster Secret"). Rename to **"Discard cancelled registration"** (per maintainer's earlier feedback per V125-1-7-fix sprint context). Update accompanying confirmation modal copy + tests.
- `ui/src/components/...` confirmation modal text — point at the new copy
- Vitest tests for the rename

**Acceptance:**
- Unlabeled Secrets → orphan API returns 400 on delete, never surfaces in orphan list
- Labeled Secrets → delete path works as today (no regression)
- UI button text matches the new label everywhere
- `cd ui && npm run build && npm test --run` clean
- `go test ./internal/api/... -race -count=1` clean
- `swag init ...` if validation error shape changes the handler's `@Failure` annotations (verify — likely YES, regen needed)

**Effort:** S (~1-2 hours agent runtime)

**Commit:** `fix(api,ui): V125-1-7 label gate + rename "Delete cluster Secret" → "Discard cancelled registration" (V125-1-8.2)`

---

### Story 8.3 — Retire pre-merge Secret paths + replace yaml_mutator (Wave B, parallel with 8.2)

The "hardest part" per design doc §12: removing the dead pre-merge code paths AND replacing the line-level YAML mutators with V125-1-9's envelope-aware parse-mutate-marshal pattern (handles task #257).

**Files to modify:**

- `internal/orchestrator/cluster.go`:
  - Remove lines 227-262 (`argoSecretManager.Ensure` pre-merge path + branch around it)
  - Remove lines 408-450 (direct ArgoCD API `RegisterCluster` pre-merge path)
  - Cluster registration flow becomes: fetch creds → connectivity probe → open PR (with managed-clusters.yaml entry added via parse-mutate-marshal) → done. Reconciler picks up post-merge.
  - Connectivity probe MUST stay (UX win — fails fast on bad kubeconfig before even opening a PR)
- `internal/orchestrator/cluster.go::DeregisterCluster` (line 492) + `UpdateClusterAddons` (line 592) — same retirement of direct-API `RegisterCluster` paths
- `internal/argosecrets/` (or wherever `argoSecretManager` interface lives — find via Serena) — DELETE the interface if no remaining caller; otherwise leave with `// Deprecated:` comment + plan for V125-2 cleanup
- `internal/gitops/yaml_mutator.go`:
  - REPLACE `AddClusterEntry` / `RemoveClusterEntry` / `setAddonLabel` / `UpdateClusterSecretPath` with parse-mutate-marshal pattern using V125-1-9's `models.LoadManagedClusters` + `models.SaveManagedClusters`. The new functions accept the file bytes, return mutated bytes.
  - Old line-level mutators DELETED (not just stubbed). Callers updated to the new pattern.
  - Round-trip test: load enveloped YAML → AddClusterEntry → save → load → verify entry present + envelope shape preserved + schema header preserved
  - Handles task #257 — the indent assumption bug becomes moot when we go through the YAML library
- Tests:
  - `internal/orchestrator/cluster_test.go` — DRY out / update existing register-path tests to assert: NO Secret created by orchestrator; PR opened with managed-clusters.yaml mutation; reconciler-trigger fired (mock `reconciler.Trigger()` injected via test seam)
  - **Behavioral parity tests** — design doc says this is the hardest part. Cover every register path:
    - `TestRegisterCluster_Kubeconfig_NoPreCreateSecret`
    - `TestRegisterCluster_EKSDirect_NoPreCreateSecret`
    - `TestRegisterCluster_EKSDiscovery_NoPreCreateSecret`
    - `TestRegisterCluster_Batch_NoPreCreateSecret`
    - `TestRegisterCluster_Adopt_NoPreCreateSecret`
    - All must assert: PR opened, no direct argocd Secret create, no direct argocd API RegisterCluster call

**Acceptance:**
- All behavioral parity tests pass
- `go test ./internal/orchestrator/... ./internal/gitops/... -race -count=1` clean
- `make test-e2e` clean (kind harness covers register flow)
- yaml_mutator's old line-level functions removed (verified via grep — zero callers)
- argoSecretManager interface either fully removed OR has zero callers + `// Deprecated:` comment

**Effort:** L (~3-5 hours agent runtime — biggest story; test parity work dominates)

**Commit:** `refactor(orchestrator,gitops): retire pre-merge Secret paths + replace yaml_mutator with envelope-aware writer (V125-1-8.3 / closes #257)`

---

### Story 8.4 — Wire reconciler into serve.go + e2e smoke (Wave C, serial)

Bootstrap wiring + end-to-end proof. Depends on 8.1 + 8.2 + 8.3 all merged.

**Files to modify:**

- `cmd/sharko/serve.go` — near prtracker bootstrap (find via Serena, grep for `prtracker.New`):
  ```go
  // Construct + start reconciler (V125-1-8)
  recon := clusterreconciler.New(clusterreconciler.Deps{
      CMStore:    cmStore,
      GitProvider: func() gitprovider.GitProvider { return /* same lazy access as prtracker */ },
      ArgoClient: argocdClient,
      Vault:      credProvider,
      AuditFn:    auditLogger.Log,
      TickInterval: 30 * time.Second,
  })
  prTracker.SetOnMergeFn(func(pr prtracker.PRInfo) {
      recon.Trigger() // immediate post-merge convergence
  })
  recon.Start(ctx)
  ```
- New end-to-end test in `tests/e2e/lifecycle/` (find via Serena pattern from V125-1-13):
  - `TestE2E_RegisterCluster_PostMergeReconcile_CreatesSecret` — register a cluster via Sharko API, merge the PR, assert ArgoCD Secret appears within 5s (immediate via trigger; 30s poll as safety net)
  - `TestE2E_ClusterRemovedFromGit_ReconcilerDeletes` — delete entry from managed-clusters.yaml via PR, merge, assert Secret disappears
  - `TestE2E_AccidentalSecretDeletion_SelfHealing` — kubectl-delete a labeled Secret, assert it reappears on next tick (or trigger)

**Acceptance:**
- Wiring compiles + e2e suite passes
- `make test-e2e` clean (full kind harness)
- `swag init ...` if any `@Router` annotations changed (verify — unlikely)
- Reconciler logs visible in `make test-e2e-report` output via Story V126-4.1's heartbeat pattern

**Effort:** M (~2-3 hours agent runtime; bulk is e2e wiring)

**Commit:** `feat(reconciler,e2e): wire reconciler into serve.go + add e2e smoke tests (V125-1-8.4)`

---

### Story 8.5 — Docs + design-doc annotation (Wave D, final, docs-only)

**Files to modify/create:**

- `docs/design/2026-05-11-cluster-secret-reconciler-and-gitops-stance.md` — annotate §12 V125-1-8 + V125-1-7 tightening sub-bullets with `✅ shipped V1.25` markers and pointer to PR. Add a top-of-doc status line: `**STATUS: V125-1-8 shipped 2026-05-21** — see PR (TBD, fill in when sprint PR opens)`
- `docs/design/section-3-template-and-secrets.md` — add a callout at the top of "The PR Is the Gate" section: `> **V125-1-8 update (2026-05-21):** The pre-creation pattern described below has been replaced by the reconciler model. See [cluster-secret-reconciler-and-gitops-stance.md](./2026-05-11-cluster-secret-reconciler-and-gitops-stance.md) for the current architecture.`
- New `docs/site/operator/cluster-reconciler.md` (~200-300 lines, operator-facing):
  - What the reconciler does (1-paragraph plain-English summary)
  - Ownership label `app.kubernetes.io/managed-by: sharko` — why it matters, who creates it, what happens if a user removes it manually
  - Reconciliation cadence (30s default + immediate post-merge trigger)
  - Two-direction policy table (git → ArgoCD auto, ArgoCD → git via Adopt manual)
  - Recovery scenarios:
    - "What if Sharko is down?" — registration blocked; reads still work; existing Secrets unchanged
    - "What if a labeled Secret is accidentally deleted?" — self-healing on next tick
    - "What if vault is transiently down?" — reconcile fails this tick, audit-log, retries next tick
  - Coexistence with manual / external clusters (unlabeled Secrets are never touched)
  - Troubleshooting: audit-log search examples, reconciler-trigger CLI command if exposed (otherwise: "wait up to 30s")
- `docs/site/operator/configuration.md` — banner near top: `> **V125-1-8 update (2026-05-21):** Cluster Secret lifecycle is now managed by an in-Pod reconciler. See [cluster reconciler](./cluster-reconciler.md) for ownership semantics + recovery scenarios.`
- `mkdocs.yml` — add nav entry for the new operator page under Operator Manual
- `docs/site/release-notes.md` — add `<!-- V125-1-8 cluster reconciler merged to dev/v125-1-8-cluster-reconciler — release note pending v1.25.0-pre.0 cut -->` marker (NO v1.25 entry yet; that comes with the version tag in a follow-up sprint)

**Acceptance:**
- `mkdocs build --strict` clean
- All cross-links resolve
- Light test smoke: `go build ./... && go vet ./...` clean (paranoia; docs-only shouldn't break Go but verify)

**Effort:** S (~1-2 hours agent runtime)

**Commit:** `docs(reconciler): V125-1-8 operator runbook + design-doc annotation (V125-1-8.5)`

---

## Sequencing + dispatch order

| Wave | Stories | Mode | Depends on |
|------|---------|------|------------|
| **A0** | 8.0 scaffold + label utilities | Serial (single small agent, ~30-60 min) | — |
| **A** | 8.1 reconciler core | Serial (single agent, M) | A0 |
| **B** | 8.2 + 8.3 in parallel | Parallel (2 agents — independent surfaces) | A |
| **C** | 8.4 wiring + e2e smoke | Serial (single agent, M) | B |
| **D** | 8.5 docs | Serial (single agent, S) | C |

**Total:** 5 waves, 6 stories (8.0–8.5). Estimated wall-clock: 1-2 dev days with mostly-parallel agents. Aggressive but the V125-1-9 cadence proved this shape works (6 stories shipped in one day).

## Pre-req fixture / test infrastructure

- **Fake k8s client:** `k8s.io/client-go/kubernetes/fake` — already used in `internal/argosecrets/` tests (verify via Serena). Story 8.1 reuses pattern.
- **Fake git provider:** `internal/demo/mock_git.go` extended in V125-1-9.1 for envelope shape. Story 8.1 reuses for `PollOnce` testing.
- **e2e harness:** `tests/e2e/lifecycle/` with kind multi-cluster (shipped V125-1-13). Story 8.4 extends the existing register-flow tests with reconciler assertions.
- **No new test infrastructure** to build.

## ALL OQs LOCKED 2026-05-21 (per `feedback_decide_dont_ask_technical_oqs`)

Zero open questions to surface. All 13 design-doc decisions itemized in the "Locked-in architectural decisions" section above are pre-committed by the design doc itself. Migration approach (greenfield, no one-shot) and yaml_mutator disposition (replace via parse-mutate-marshal) are the only two not explicitly in the design doc — both locked here.

## Out-of-scope reminders

- CRD installation (operator mode, V3+)
- Server-side admission webhook (operator mode, V3+)
- Operator-mode binary split (V3+)
- HA / leader election (V2)
- Webhook for sub-30s reconcile latency (V2)
- Adopt UI (V125-2)
- Schema versioning beyond v1 (V126+ when v2 actually needed)
- Vault retry/backoff tuning beyond "fail this tick, log, retry next" (V2 if production load reveals need)
- Multi-replica Sharko concurrent reconciliation race (V2 — see design §10 failure modes)
- Configurable tick interval via Helm values (V125-2 polish — hardcoded 30s for V125)

## Done definition

- [ ] All 6 stories (8.0–8.5) shipped on `dev/v125-1-8-cluster-reconciler`
- [ ] All quality gates green per story
- [ ] Single sprint PR `dev/v125-1-8-cluster-reconciler → main` opened
- [ ] CI green (incl. full e2e + schemas drift + sharko validate-config gate)
- [ ] PR auto-merged
- [ ] Task #257 closed (yaml_mutator follow-up resolved)
- [ ] sprint-status.yaml flipped to done
- [ ] V125-1-9 follow-up risks discharged

---
stepsCompleted: [1, 2, 3]
inputDocuments:
  - docs/design/2026-05-12-v125-architectural-todos.md (lines 144-191)
  - docs/design/2026-05-11-cluster-secret-reconciler-and-gitops-stance.md (§11)
workflowType: 'epics-and-stories'
project_name: 'sharko'
version: 'v1.25'
user_name: 'Moran'
date: '2026-05-13'
---

# V125-1-9 — Schema envelope + JSON Schema — Epic Breakdown

## Overview

V125-1-9 is the FIRST of two paired V125 architectural epics (V125-1-9 schema envelope → V125-1-8 cluster reconciler → V125-1-7 tightening → V125-2 cleanup). It wraps `managed-clusters.yaml` and `addons-catalog.yaml` in a self-describing, schema-validated `apiVersion/kind/spec` envelope so that V125-1-8's reconciler reads against a stable validated contract instead of a typed Go struct that silently swallows malformed entries.

This is **operational safety infrastructure**, not a feature. From the gitops-stance doc (§11):

> "Why it matters more once V125-1-8 lands: A reconciler-driven design makes the YAML the authoritative source of truth. Schema integrity goes from 'nice to have' to 'operationally critical': bad YAML = silent reconcile failures = potential incidents."

## Driving sources

- **`docs/design/2026-05-12-v125-architectural-todos.md` §4** (lines 144-191) — primary scope doc. Every story acceptance criterion derives from this section.
- **`docs/design/2026-05-11-cluster-secret-reconciler-and-gitops-stance.md` §11** — explains why V125-1-9 must precede V125-1-8 and what failure modes it prevents.
- The new envelope shape is already shown in the architectural-todos doc at lines 100-114 — that example is the **exact target output** of every Sharko writer after this epic.

## Scope frame (verbatim from design doc §4)

### Goal
"Make `managed-clusters.yaml` and `addons-catalog.yaml` self-describing, schema-validated, editor-friendly. Bridge to operator mode in V3+."

### Locked-in decisions
1. Envelope shape:
   ```yaml
   apiVersion: sharko.io/v1
   kind: ManagedClusters    # or AddonCatalog
   metadata:
     name: managed-clusters
   spec: { ... }
   ```
2. JSON Schema generated from Go struct definitions, emitted to `docs/schemas/managed-clusters.v1.json` + `docs/schemas/addon-catalog.v1.json`, **committed to the repo** (not gitignored).
3. Schema header in every Sharko-written YAML: `# yaml-language-server: $schema=https://sharko.io/schemas/managed-clusters.v1.json`
4. Validation on PR (Sharko CLI or CI hook) AND validation on read (reconciler).
5. Reader accepts BOTH old (no envelope) and new (enveloped) formats during transition.
6. Writer always emits enveloped shape after this epic ships.
7. Filename rename: `addons-catalog.yaml` → `addon-catalog.yaml` (singular). Keep old name as alias for back-compat.
8. Deprecate legacy reader in **V126+** (next architectural release after V125).
9. Single-doc-with-array, NOT multi-doc YAML.
10. CLI subcommand: `sharko validate-config <file>` (exit 0/1).

### Explicitly out of scope (per design doc)
- CRD installation (operator mode, V3+)
- Server-side validation webhook (operator mode, V3+)
- Multi-version schema migration framework (just have v1 for now)

## Branch + release strategy

- **Branch:** `dev/v125-1-9-schema-envelope` cut from `main` (currently `5addc25d` after PR #321 merge). All stories merge into this branch via per-story FF.
- **Single PR at the end:** `dev/v125-1-9-schema-envelope → main`.
- **NO release / NO tag / NO version bump** — V125 architectural work ships when V125-1-9 + V125-1-8 are BOTH done; the version bump happens then, not now.
- **Coordination with PR #319 (`dev/v1.24-cleanup`):** PR #319 is still open per session context. V125-1-9 touches `internal/models/cluster.go`, `internal/catalog/loader.go`, `internal/demo/mock_git.go`, the bootstrap templates, and adds NEW files (`cmd/schema-gen/`, `internal/schema/`, `docs/schemas/`). Conflict surface with #319 should be small but agent dispatches must rebase if #319 lands first.

## Lean-workflow expectations

- Per `.claude/team/` role files: every story dispatch embeds the relevant role file(s) and includes test-engineer + (where touched) docs-writer alongside the implementation agent.
- Architect role file embedded in **every** story (this is an architectural epic).
- One agent per story (lean workflow). Parallel-dispatchable bundles called out below.
- Wave-based dispatch: never dispatch story N+1 before story N is FF-merged AND quality gates green.

## Quality gates (per story)

- `go build ./...`
- `go vet ./...`
- `go test ./...` (race detector off; existing convention)
- `swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal` ONLY if the story modifies `@Router` annotations. (V125-1-9 likely doesn't touch handlers; CLI subcommand 9.5 might if it exposes an HTTP endpoint, which it doesn't per current scope.)
- For 9.3+: `make generate-schemas` MUST be re-runnable and produce identical output (idempotent).
- For 9.6: CI workflow YAML lints clean (`actionlint` or equivalent).
- For 9.8: `mkdocs build --strict` passes.

## Forbidden during dispatches

- NO `Co-Authored-By` trailers
- NO `--no-verify` / hook skipping
- Worktree-isolated agent dispatch with explicit "stay on worktree branch + DO NOT push" guard (per `feedback_agent_dispatch_worktree_isolation` memory)
- NO new product features beyond the design doc's scope
- NO frontend work (UI doesn't see the envelope directly)
- NO release / NO tag / NO version bump

---

## Epic V125-1-9: Schema envelope + JSON Schema

### Story 9.1: Envelope types + managed-clusters reader/writer compat

Define the shared envelope shape as a Go generic (or twin types — agent decides) in a new `internal/schema/` package. Apply to `internal/models/cluster.go`:

- New `ManagedClustersDoc` envelope type with `APIVersion`, `Kind`, `Metadata`, `Spec` fields.
- Reader (existing `LoadManagedClusters` or equivalent) accepts BOTH legacy bare-YAML AND enveloped YAML. Detection: presence of `apiVersion: sharko.io/v1` at the top level.
- Writer always emits enveloped shape after this story.
- Update `templates/bootstrap/configuration/managed-clusters.yaml` to enveloped shape with header.
- Update `internal/demo/mock_git.go` seed to enveloped shape.

**Acceptance criteria:**
- Round-trip test: read legacy → write → read → verify enveloped output matches legacy semantics
- Round-trip test: read enveloped → write → read → verify lossless
- Existing tests in `internal/models/`, `internal/service/`, `internal/orchestrator/` still pass (legacy fixtures stay readable)
- Demo mode boots cleanly with the new template
- `go build/vet/test ./...` clean

**Effort:** M

### Story 9.2: Addon-catalog envelope + filename rename (alias preserved)

Mirror 9.1 for `internal/catalog/loader.go`:

- New `AddonCatalogDoc` envelope (using the shared infrastructure landed in 9.1).
- Reader accepts BOTH `addons-catalog.yaml` (legacy filename) AND `addon-catalog.yaml` (new singular filename). Reader prefers new filename if both exist (defensive).
- Writer always emits `addon-catalog.yaml` (singular) with envelope shape.
- Update `templates/bootstrap/configuration/addons-catalog.yaml` → `addon-catalog.yaml` with envelope + header. Keep a stub at the old path that just `# moved to addon-catalog.yaml` if any deployed installs reference it directly.
- Update `internal/demo/mock_git.go` to seed at the new path with envelope shape.

**Acceptance criteria:**
- Reader compat: legacy bare YAML at old filename, legacy bare YAML at new filename, enveloped YAML at new filename — all work
- Writer always produces new filename + enveloped shape
- Demo mode boots cleanly
- Catalog tests in `internal/catalog/...` and `tests/e2e/lifecycle/catalog_test.go` still pass

**Effort:** M (parallel-safe with 9.1 once 9.1's shared envelope infrastructure lands)

### Story 9.3: JSON Schema generator (`cmd/schema-gen/`)

New binary `cmd/schema-gen/main.go` that introspects the Go envelope types defined in 9.1 + 9.2 and emits JSON Schema:

- Output: `docs/schemas/managed-clusters.v1.json` + `docs/schemas/addon-catalog.v1.json` (committed to repo, NOT gitignored).
- Makefile target: `make generate-schemas` runs the binary.
- CI guard: similar to existing "Swagger Up To Date" check — fail CI if schemas drift from the Go types.
- Use a proven library (recommend `invopop/jsonschema`); resolve via context7 for current API.

**Acceptance criteria:**
- `make generate-schemas` produces idempotent output (running twice produces no diff)
- Generated schemas validate the example YAML in the design doc (the line 100-114 example)
- Generated schemas reject obvious malformed input (missing required field, wrong type)
- New CI check fails if Go types changed without re-running the generator
- Schemas committed to `docs/schemas/`

**Effort:** M (depends on 9.1 + 9.2 — needs stable type definitions to introspect)

### Story 9.4: Read-time validation in reader paths

Wire the JSON Schema validator into the read paths landed in 9.1 + 9.2:

- New `internal/schema/validator.go` with `Validate(filename, body []byte) error` function — auto-detects schema by `kind:` field.
- Reader code in `internal/models/cluster.go` + `internal/catalog/loader.go` calls `Validate` BEFORE returning the parsed doc.
- On validation failure: audit-logged error (use existing audit infrastructure), return error to caller — no silent swallowing.
- Per design doc §4 line 167: "rejects malformed file with audit-logged error rather than silent reconcile failure."
- Pick the schema validator library (recommend `santhosh-tekuri/jsonschema` v5 — fast, JSON-Schema-2020-12 support, pure Go); resolve via context7.

**Acceptance criteria:**
- Valid enveloped YAML loads cleanly
- Invalid enveloped YAML (wrong type, missing required, extra forbidden field) returns error + audit-log entry
- Legacy bare YAML still loads (validation skipped for legacy shape — the `apiVersion` detection from 9.1 routes it past validation)
- Performance: validation adds < 5ms per file load (load-once cache the compiled validator)
- Reader unit tests cover happy path + each failure mode

**Effort:** M (depends on 9.3 — needs the schemas to validate against)

### Story 9.5: `sharko validate-config <file>` CLI + CI/PR hook

Two-part story (kept as one because they're tightly coupled):

**Part A — CLI subcommand:**
- New `cmd/sharko/validate.go` (or extend existing CLI structure)
- `sharko validate-config <file>` — auto-detects schema by `kind:` field, validates, prints user-friendly errors (line numbers when YAML library supports them), exits 0/1
- `sharko validate-config <dir>` — validates all `*.yaml` files in dir matching known kinds
- Help text + examples

**Part B — CI/PR hook:**
- New GitHub Actions step (in `.github/workflows/ci.yml` or new dedicated workflow): runs `sharko validate-config` on every changed YAML file in PR diff matching `managed-clusters.yaml`, `addon-catalog.yaml`, or `addons-catalog.yaml`
- Validation failure → CI fails with link to the schema URL for self-service fix
- Test by adding a fixture-break test PR (or manual validation that the CI step catches a bad envelope)

**Acceptance criteria:**
- CLI validates a known-good file → exit 0
- CLI validates a known-bad file → exit 1 with helpful error
- CI step rejects a PR that adds an invalid enveloped YAML
- CI step ignores YAML files NOT matching known schema kinds (non-Sharko config files)
- CLI documented in `docs/site/operator/cli-reference.md` (or wherever existing CLI commands are documented)

**Effort:** S (depends on 9.4 for the validation library)

### Story 9.6: Documentation + migration runbook

- Update `docs/design/2026-05-12-v125-architectural-todos.md` §4 with shipped state (annotate "✅ shipped V1.25" on each scope item).
- New `docs/site/operator/yaml-schema-migration.md`:
  - What changed (envelope shape, schema header, filename rename for addon-catalog)
  - Why (operational safety prep for V125-1-8 reconciler)
  - User impact: the reader still understands old shape; first write after upgrade emits new shape; no manual migration required
  - Deprecation timeline: legacy reader removed in V126
  - Editor setup (yaml-language-server in VS Code / IntelliJ — auto-completion + inline validation thanks to schema header)
  - Manual migration steps for users who want to adopt the new shape immediately (run `sharko validate-config` against their repo)
- Add a section to existing operator docs (probably `docs/site/operator/configuration.md` or similar) cross-linking to the new migration page.
- mkdocs strict build passes.

**Acceptance criteria:**
- Design doc annotated with shipped state (any deviations from design noted)
- Migration runbook covers all six bullets above
- Cross-links from existing operator/configuration docs work
- `mkdocs build --strict` passes

**Effort:** S

---

## Sequencing + dispatch order

Hard dependencies:

- 9.1 + 9.2 can run **in parallel** (Wave A) — independent files, but both define types the schema generator needs
- 9.3 (schema generator) waits for 9.1 AND 9.2 (Wave B)
- 9.4 (read-time validation) waits for 9.3 (Wave C)
- 9.5 (CLI + CI hook) waits for 9.4 (Wave D)
- 9.6 (docs) waits for 9.5 (Wave E, last)

Recommended dispatch:

| Wave | Stories | Mode | Notes |
|------|---------|------|-------|
| A | 9.1, 9.2 | Parallel (2 worktrees) | Coordinate on shared envelope helper — agents must agree on `internal/schema/` package name + interface before either commits |
| B | 9.3 | Serial | After Wave A FF-merged |
| C | 9.4 | Serial | After 9.3 FF-merged |
| D | 9.5 | Serial | After 9.4 FF-merged |
| E | 9.6 | Serial | After 9.5 FF-merged |

Total: 5 wall-clock waves. ~3-5 dev days end-to-end per design doc estimate ("This is mechanical work (~3-5 days)" — §11 line 421 of gitops-stance doc).

## Pre-req fixture / test infrastructure

- Existing fixtures in `internal/api/testdata/`, `internal/orchestrator/testdata/`, `internal/catalog/testdata/`: sufficient for legacy-shape coverage (proves 9.1/9.2's reader compat).
- New fixtures needed: enveloped variants — agents add `*.v1.yaml` siblings to existing fixtures as part of 9.1/9.2.
- e2e suite (`tests/e2e/`): no new e2e tests required for V125-1-9 — the existing harness already loads via the standard reader paths, so 9.1/9.2 reader-compat work transparently makes e2e tests cover the migration path. If e2e gaps surface during dispatch, file as backlog (don't expand scope).
- BUG-018/019/020 in V124 backlog: not envelope precursors per the V124-13/V124-23 task history — they're cleanup items closed during V124. No carryover.

## Open questions for maintainer (BLOCK dispatch until resolved)

1. **JSON Schema generator library:** recommend `invopop/jsonschema` (proven, handles Go struct tags well). Confirm or pick alternative (handwritten emission, `xeipuuv/gojsonschema`, etc.).
2. **Validation library:** recommend `santhosh-tekuri/jsonschema` v5 (pure Go, fast, JSON-Schema-2020-12). Confirm or pick alternative.
3. **Filename rename window:** doc says "keep alias for back-compat" but doesn't quantify how long. Recommend: alias removed in V126 along with legacy reader. Confirm.
4. **Schema URL hosting:** `https://sharko.io/schemas/managed-clusters.v1.json` per the design doc. Is `sharko.io` a domain you control / want to register, or do we host on `moranweissman.github.io/sharko/` (or similar GH Pages) with the `sharko.io` URL as aspirational? Affects 9.6's docs.
5. **CI validation file-match scope:** validate only known filenames (`managed-clusters.yaml`, `addon-catalog.yaml`), or every YAML in PR diff that has `apiVersion: sharko.io/*`? Recommend the latter — more permissive, catches user-renamed files too.
6. **Coordination with PR #319:** PR #319 is still open. Should V125-1-9 wait for #319 to merge to main first, or proceed in parallel and rebase on conflicts? Recommend: proceed in parallel (low conflict surface), rebase if needed.

## Out-of-scope reminders (do NOT expand into these)

- CRD installation
- Server-side validation webhook
- Multi-version schema migration framework (no `v2` work — only `v1`)
- Breaking-change handling for future schema versions (deferred until V126+ when `v2` is actually needed)
- Concurrent-write handling (the gitops "all writes via PR" stance handles this — no envelope-level locking needed)
- Backup-on-migrate (no migration happens automatically — first WRITE after upgrade emits new shape; reader handles old shape forever until V126 removes it)
- Partial validation (entire file validates or fails — no per-cluster-entry granularity in V125-1-9)

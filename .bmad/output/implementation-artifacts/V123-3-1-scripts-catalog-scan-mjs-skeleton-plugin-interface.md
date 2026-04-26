---
story_key: V123-3-1-scripts-catalog-scan-mjs-skeleton-plugin-interface
epic: V123-3 (Trusted-source scanning bot)
status: review
effort: M
dispatched: 2026-04-26
opens_epic: V123-3
---

# Story V123-3.1 — `scripts/catalog-scan.mjs` skeleton + plugin interface

## Brief (from epics-v1.23.md §V123-3.1)

As a **Sharko maintainer**, I want a modular scanner script where each
upstream source is a plugin so that new sources can be added without
rewriting the bot.

**This story OPENS Epic V123-3 (Trusted-source scanning bot).** It ships
the skeleton + plugin contract; concrete scanner plugins (V123-3.2 CNCF
Landscape, V123-3.3 EKS Blueprints) layer on top; PR-opening logic +
GitHub workflow are V123-3.4; reviewer runbook is V123-3.5.

## Acceptance Criteria

**Given** `scripts/catalog-scan.mjs` exists
**Then** it loads the current `catalog/addons.yaml`, iterates registered
scanner plugins, diffs proposals, and emits a single changeset JSON for
PR-opening logic (V123-3.4 will consume this).

**Given** the plugin interface
**Then** each plugin exports `{ name, fetch() → normalized[] }` and lives
under `scripts/catalog-scan/plugins/`.

**Given** no plugins produce any diff
**When** the script runs
**Then** it exits 0 with a "no changes" log and does not open a PR.

**Given** the script runs locally
**When** `node scripts/catalog-scan.mjs --dry-run` is invoked
**Then** proposals print to stdout with no PR side effect.

## Key existing context

- `scripts/` already hosts ESM `.mjs` scripts (e.g.
  `scripts/docs-screenshots.mjs`) — same node/ESM idiom applies here.
- `scripts/package.json` does **not** exist yet. Per the brief's tech
  notes, add one only if a runtime dep is needed.
- `ui/package.json` already has `yaml@^2.8.2` (the `eemeli/yaml` v2 lib).
  The brief explicitly allows reusing that. Two pragmatic choices:
  - **Option A (preferred):** add a tiny `scripts/package.json`
    declaring `"yaml": "^2.8.2"` (matching ui pin) so the script can be
    invoked from the repo root via `npm install --prefix scripts && node scripts/catalog-scan.mjs`
    without polluting the UI install graph.
  - **Option B:** use only Node built-ins — `node:fs/promises` reads the
    file, but parsing YAML in pure stdlib is hostile. Reject Option B
    unless adding `scripts/package.json` is somehow blocked.
  - **Option C (rejected):** import `yaml` from `ui/node_modules` via a
    relative path — fragile, breaks when the ui workspace is reinstalled.
- `catalog/addons.yaml` is the live curated list. Schema is enforced by
  `internal/catalog/loader.go` (Go side). The scanner only needs to
  read names + categories + versions for diffing; it does NOT need to
  validate the schema (that's the loader's job at PR-merge time, via
  the existing `Swagger Up To Date` / `Go Build & Test` CI checks
  catching schema breaks).
- Catalog YAML uses `addons:` as the top-level key (per loader's
  `yamlRoot.Addons` field — see `internal/catalog/loader.go:259`).
- `scripts/docs-screenshots.mjs` shows the in-repo conventions: shebang
  line, JSDoc-style top comment with usage block, env-var documentation,
  no TypeScript transpile, deterministic stdout for CI capture.

## Scope (Tier-ordered)

### Tier 1 — Skeleton + plugin contract (REQUIRED)

1. **`scripts/catalog-scan.mjs`** (NEW) — entry point. Responsibilities:
   - Parse CLI args: `--dry-run`, `--catalog <path>` (default
     `catalog/addons.yaml`), `--out <path>` (default `-` = stdout when
     `--dry-run`, else `_dist/catalog-scan/changeset.json`).
   - Load and parse the catalog YAML via `yaml@^2.8.2`.
   - Discover plugins by reading `scripts/catalog-scan/plugins/*.mjs`
     (alphabetical glob); import each via dynamic `await import()`.
   - For each plugin, call `await plugin.fetch()` with a per-plugin
     timeout (default 60s) and a context object carrying
     `{ logger, abortSignal, http: fetchWithRetry }` so plugins don't
     each hand-roll the same boilerplate.
   - Diff plugin proposals against the current catalog using the
     diff-helper module (#3 below).
   - Aggregate per-plugin proposals into a single changeset JSON shape
     (#4 below).
   - **No-changes branch:** when the aggregated changeset has zero
     `adds` and zero `updates`, log "no changes" and exit 0 — do NOT
     write the output file (V123-3.4's GH Action will key its
     "open PR" decision on the file's existence).
   - **`--dry-run` branch:** print the aggregated changeset to stdout
     as pretty-printed JSON; do NOT write any file.
   - **Default branch:** write the changeset to `--out`, ensuring the
     parent directory exists.
   - Top-level error handling: any plugin error logs the plugin name +
     error and continues with the rest (so one upstream blip doesn't
     blackhole the run); a global error (catalog YAML missing/parse
     failure) exits 1 with a clear message.

2. **`scripts/catalog-scan/plugins/` directory** (NEW, with a
   `README.md` documenting the contract). The README is the contract
   spec the scanner plugins (V123-3.2 / V123-3.3) read when they're
   implemented next. Contract:
   ```
   Plugin module shape (ESM):
     export const name = '<short-id>';                // unique per plugin
     export async function fetch(ctx) {
       // ctx.logger.info('...')
       // ctx.http(url, opts) — fetch with retries + timeout
       // ctx.abortSignal — honor it for cancellation
       return [
         {
           name: '<addon-name>',     // catalog primary key
           repo: '<helm-repo-url>',  // optional; informational
           chart: '<chart-name>',    // optional; informational
           version: '<chart-ver>',   // optional; informational
           category: '<category>',   // optional; map to schema enum
           trust_score: 0..100,      // plugin-defined heuristic
           // ...other normalized fields per schema
         },
         ...
       ];
     }
   ```
   The plugin may also export `annotate(entry)` if it wants to enrich
   an existing catalog entry instead of replacing it (V123-3.2/3.3 may
   choose either pattern). The scanner ignores absent helpers.

3. **`scripts/catalog-scan/lib/diff.mjs`** (NEW) — pure-function diff
   helper. Inputs: `current: CatalogEntry[]`, `proposed: ScannerEntry[]`.
   Output:
   ```
   {
     adds:    [{plugin, entry}],
     updates: [{plugin, entry, diff: {field: {from, to}}}],
   }
   ```
   - "Add" means the proposed `name` doesn't exist in current.
   - "Update" means the name exists and at least one normalized field
     (chart, version, category, repo) differs.
   - **Deletes are NOT proposed by this scanner** — removal is a human
     decision (CODEOWNERS-gated catalog edits). Same rationale as the
     existing curation policy.
   - Field-level diffs use deep-equal on the comparable subset; ignore
     fields that scanners don't populate (security_score, license,
     curated_by — those stay human-curated).

4. **`scripts/catalog-scan/lib/changeset.mjs`** (NEW) — aggregator + JSON
   shape. The single output object:
   ```
   {
     schema_version: '1.0',
     generated_at:   '<RFC3339>',
     scanner_runs:   [{plugin, fetched_count, error?}],
     adds:           [{plugin, entry}],
     updates:        [{plugin, entry, diff}],
   }
   ```
   This shape is the contract V123-3.4's PR-opener consumes. Document
   it in the plugins/README.md alongside the plugin contract.

5. **`scripts/catalog-scan/lib/http.mjs`** (NEW) — thin retry-wrapped
   `fetch()` helper. 3 retries with exponential backoff (1s, 2s, 4s);
   honors AbortSignal; sets `User-Agent: sharko-catalog-scan/1.0`.
   Uses Node 18+'s built-in `fetch` (no node-fetch dep needed). Plugins
   call `ctx.http(url, opts)` instead of bare `fetch()` so retries +
   UA are uniform.

6. **`scripts/catalog-scan/lib/logger.mjs`** (NEW) — tiny structured
   logger with `info/warn/error` levels writing JSON-line to stderr
   (so stdout stays clean for `--dry-run` JSON capture). Each line:
   `{ts, level, msg, ...attrs}`. Plugins receive this via `ctx.logger`.

7. **A trivial built-in plugin for tests** — `scripts/catalog-scan/plugins/_example.mjs`
   that returns a fixed `[]` so the discovery loop can be exercised
   without external network calls. Prefix `_` so V123-3.2 / V123-3.3
   skip it via the alphabetical glob? — actually NO: include it in the
   glob, name it `_example.mjs`, and skip it via an `_` prefix
   convention (document this in plugins/README.md). Cleaner than
   special-casing one filename.

8. **`scripts/package.json`** (NEW) — minimal:
   ```
   {
     "name": "sharko-scripts",
     "private": true,
     "type": "module",
     "engines": { "node": ">=18.0.0" },
     "dependencies": { "yaml": "^2.8.2" }
   }
   ```
   No devDeps; no test framework — see Tier 2 below.

### Tier 2 — Tests (REQUIRED)

The brief calls this an M-effort skeleton story; tests must be
proportionate. Use Node's built-in `node:test` runner (zero new
deps; ships with Node ≥18). Test files live next to the modules with
`.test.mjs` suffix.

9. **`scripts/catalog-scan/lib/diff.test.mjs`** — 6 cases:
   - empty current + empty proposed → empty changeset
   - empty current + 1 proposed → 1 add, 0 updates
   - 1 current + same name + same fields → 0 adds, 0 updates
   - 1 current + same name + different version → 0 adds, 1 update with
     `diff.version.{from,to}`
   - 1 current + missing name in proposed (deletion case) → 0 adds,
     0 updates (assert delete is NOT proposed)
   - 2 plugins propose same name → 2 entries in adds (each tagged with
     plugin name); the resolution policy is "let humans pick" — the
     scanner aggregates, doesn't dedupe semantically.

10. **`scripts/catalog-scan/lib/changeset.test.mjs`** — 3 cases:
    - aggregates 0-plugin run → empty adds/updates with `scanner_runs: []`
    - aggregates 2-plugin run with mixed results → correct shape
    - serializes to deterministic JSON (snapshot via stringify with
      sorted keys). This catches accidental field-order regressions.

11. **`scripts/catalog-scan.test.mjs`** — top-level integration, 4 cases:
    - **no-changes-no-output:** load a catalog with one entry, run
      with a stub plugin returning the same entry → exits 0, no file
      written, "no changes" log line emitted.
    - **dry-run-stdout:** load the same catalog, stub plugin proposes
      one new entry, run with `--dry-run` → stdout contains valid JSON
      matching the changeset shape, no file written.
    - **default-writes-output:** as above without `--dry-run` → output
      file created at `_dist/catalog-scan/changeset.json` containing
      the changeset.
    - **plugin-error-isolated:** two stub plugins, one throws → other
      plugin's results still appear in the changeset; the throwing
      plugin is recorded in `scanner_runs` with `error: <message>`;
      exit code 0 (the run itself succeeded; the upstream blip is
      reported, not fatal).

    The test uses a fixture catalog under
    `scripts/catalog-scan/__tests__/fixtures/addons.tiny.yaml`
    (NEW) and stub plugins under `scripts/catalog-scan/__tests__/stubs/`.
    The integration test invokes the script as a child process via
    `node:child_process spawnSync` so CLI parsing + exit codes are
    actually exercised.

### Tier 3 — Convenience wiring (RECOMMENDED, low cost)

12. **Root `Makefile`** (existing) — add a `catalog-scan` target that
    runs `npm install --prefix scripts --silent && node scripts/catalog-scan.mjs --dry-run`.
    Maintainer-facing only; not a CI gate yet (V123-3.4 owns CI).

13. **`scripts/package.json`** also gets a `"scripts": { "test": "node --test scripts/catalog-scan/" }`
    entry so `npm test --prefix scripts` runs the full suite.

### Out of scope — explicit non-goals

- **Concrete scanner plugins.** V123-3.2 (CNCF Landscape) and V123-3.3
  (EKS Blueprints) are separate stories. This story ships only the
  skeleton + the trivial `_example` plugin. Do NOT scaffold either
  upstream-specific plugin here.
- **PR-opening logic + GitHub Action.** V123-3.4 owns the
  `catalog-bot` workflow + auto-merge policy (open question §7.3).
  Do NOT add `.github/workflows/catalog-scan.yml`.
- **Reviewer runbook docs.** V123-3.5 owns
  `docs/site/developer-guide/catalog-scan-runbook.md`. Do NOT add
  docs to the `docs/site/` tree.
- **Schema validation in the script.** The Go loader is the
  authoritative validator at PR-merge time; the scanner emits a
  "proposal" — humans + CI gate accept/reject. Do NOT reimplement
  schema enforcement in JS.
- **Trust-score computation.** The contract allows plugins to set
  `trust_score`, but this skeleton doesn't compute one. The
  `_example` plugin returns `[]`, so trust-score plumbing is exercised
  only at the type level (the JSON aggregator passes it through).
- **Cosign verification of upstream sources.** Out of scope for
  V123-3 — the scanning bot opens *PRs* which a human then reviews;
  signing of catalog entries is V123-2's territory.
- **Network calls in tests.** All tests use stubs; no real
  github.com / cncf landscape URLs hit. Hermetic.

## Implementation plan

### Files

- `scripts/catalog-scan.mjs` (NEW, ~150 lines) — entry point.
- `scripts/catalog-scan/plugins/README.md` (NEW) — plugin contract spec.
- `scripts/catalog-scan/plugins/_example.mjs` (NEW, ~10 lines) —
  test-only sentinel plugin.
- `scripts/catalog-scan/lib/diff.mjs` (NEW) — pure diff function.
- `scripts/catalog-scan/lib/changeset.mjs` (NEW) — aggregator + JSON
  shape helpers.
- `scripts/catalog-scan/lib/http.mjs` (NEW) — fetch-with-retries.
- `scripts/catalog-scan/lib/logger.mjs` (NEW) — JSON-line stderr logger.
- `scripts/catalog-scan/lib/diff.test.mjs` (NEW) — 6 cases.
- `scripts/catalog-scan/lib/changeset.test.mjs` (NEW) — 3 cases.
- `scripts/catalog-scan.test.mjs` (NEW) — 4 integration cases.
- `scripts/catalog-scan/__tests__/fixtures/addons.tiny.yaml` (NEW).
- `scripts/catalog-scan/__tests__/stubs/empty.mjs` (NEW).
- `scripts/catalog-scan/__tests__/stubs/proposes-add.mjs` (NEW).
- `scripts/catalog-scan/__tests__/stubs/throws.mjs` (NEW).
- `scripts/package.json` (NEW) — single-dep manifest + test script.
- `Makefile` — add `catalog-scan` target.
- `.bmad/output/implementation-artifacts/sprint-status.yaml` — flip
  V123-3-1 backlog → in-progress → review; flip epic-V123-3 backlog →
  in-progress (auto-transition rule).
- `.bmad/output/implementation-artifacts/REMAINING-STORIES.md` — note
  V123-3.1 in flight.
- `.bmad/output/implementation-artifacts/V123-3-1-...md` (this file) —
  retrospective sections appended.

### Quality gates (run order)

1. `cd /Users/weissmmo/projects/github-moran/sharko && npm install --prefix scripts --silent`
2. `node --check scripts/catalog-scan.mjs` — syntax check (catches
   parse errors before tests).
3. `node --test scripts/catalog-scan/` — full unit + integration suite
   (built-in test runner, zero deps).
4. `node scripts/catalog-scan.mjs --dry-run --catalog catalog/addons.yaml`
   — smoke run against the real catalog with only `_example` plugin
   loaded; expect "no changes" log, exit 0, no file written.
5. `make catalog-scan` — proves the Makefile target runs end-to-end.
6. **Skip** the Go/UI build — this story touches neither.

### Anti-gold-plating reminders

- **Do NOT scaffold CNCF / EKS plugins.** V123-3.2 / V123-3.3.
- **Do NOT add `.github/workflows/catalog-scan.yml`.** V123-3.4.
- **Do NOT add a vitest dep.** Node's built-in `node:test` is
  sufficient — and matches "no new runtime deps beyond what's in
  ui/package.json" (yaml is the only new addition; Node test runner
  is built in).
- **Do NOT reimplement YAML parsing in stdlib.** Use `yaml`.
- **Do NOT touch the Go loader or catalog/addons.yaml.** Read-only.
- **Do NOT add eslint config for scripts/.** Out of scope; the
  existing repo doesn't enforce eslint on root-level mjs files.
- **Do NOT add trust-score computation logic.** Plugins own that;
  the scanner is plugin-agnostic.
- **Do NOT validate against the catalog schema in JS.** The Go
  loader is authoritative.

## Dependencies

- **None per epic.** V123-1.x stabilized the catalog YAML shape
  (V123-1.2 fetcher already reads `catalog/addons.yaml` via the same
  loader). The script reads the same file via the `yaml` lib — no
  new schema work required.

## Gotchas

1. **Node test runner result-reporting:** `node --test` exits non-zero
   on any failure but its output is TAP. Run with `--test-reporter=spec`
   for human-readable output during development. CI can use the
   default TAP.

2. **`scripts/package.json` introduces a second `node_modules`.** Add
   `scripts/node_modules/` to `.gitignore` if it isn't already covered
   by a wildcard (check the existing `.gitignore`).

3. **Top-level await in CLI scripts** is fine in ESM (Node ≥14.8) but
   if any helper module uses TLA, parents must also be ESM. All `.mjs`
   already are.

4. **`yaml@^2.x` API is `parse()` not `safeLoad()`.** The v2 lib has
   no security-relevant unsafe modes (no anchors-execute-code), so
   `parse(text)` is fine. Don't import the v1 API.

5. **Plugin discovery via dynamic import** must use absolute paths or
   `pathToFileURL` — relative imports inside the script don't work for
   dynamically discovered files in a sibling dir. Use:
   ```js
   import {pathToFileURL} from 'node:url';
   const mod = await import(pathToFileURL(absPath).href);
   ```

6. **Stdout vs stderr:** the `--dry-run` branch must write JSON to
   stdout and logs to stderr. The `info/warn/error` logger always uses
   stderr; the JSON.stringify call uses `process.stdout.write`. Don't
   mix them — pipe-friendliness matters for V123-3.4's CI integration.

7. **Test fixture YAML must be schema-valid** so future schema
   tightening doesn't break the fixture. Keep it minimal: 1-2 entries
   with only the required fields.

8. **`spawnSync` in the integration test** — pass `{cwd: repoRoot}`
   so relative paths in the script work; pass `{env: {...process.env, NO_COLOR: '1'}}`
   to keep log output deterministic for assertions.

9. **The `_example` plugin** is part of the deliverable (exercises the
   discovery loop) but has zero runtime impact. Document it in the
   plugins/README.md so V123-3.2's implementer doesn't accidentally
   delete it.

10. **The `_` prefix convention** for "skip in production but ship in
    repo" is a soft convention — implement it as
    `if (file.startsWith('_')) skip`. Don't engineer a manifest-driven
    plugin registry for a 1-file edge case.

## Role files (MUST embed in dispatch)

- `.claude/team/devops-agent.md` — primary (CI-adjacent automation,
  scripts conventions, Makefile patterns, Node tooling choices).
- `.claude/team/architect.md` — secondary (plugin contract design;
  shape of `{name, fetch(ctx) → normalized[]}` and the changeset JSON
  schema needs to be extensible without breaking V123-3.4's consumer).

## PR plan

- **Branch:** `dev/v1.23-scanner-skeleton` off `main` (already created).
- **Commits:**
  1. `feat(scripts): catalog-scan skeleton + plugin contract (V123-3.1)`
     — `scripts/catalog-scan.mjs` + `plugins/README.md` + `plugins/_example.mjs`
     + `scripts/package.json`.
  2. `feat(scripts): diff + changeset + http + logger libs (V123-3.1)`
     — the four `lib/*.mjs` modules.
  3. `test(scripts): unit + integration tests for catalog-scan (V123-3.1)`
     — `lib/*.test.mjs` + top-level integration test + fixtures + stubs.
  4. `chore(make): add catalog-scan target (V123-3.1)` — single-line
     Makefile entry.
  5. `chore(bmad): mark V123-3.1 for review (opens Epic V123-3)`
     — sprint-status.yaml + REMAINING-STORIES.md + this file's
     status flip.
- **PR body** must call out:
  - "Opens Epic V123-3 (1/5 in review). V123-3.2 (CNCF Landscape
    plugin) is the natural next story."
  - "Skeleton-only — no real upstream scanners and no GitHub workflow.
    Smoke-tested via `_example` plugin returning `[]`."
  - Test plan checklist with the 5 quality-gate commands.
- **NO TAG.** v1.23.0 cut belongs to V123-4.5.

## Next story

**V123-3.2** — CNCF Landscape scanner plugin. Implements
`scripts/catalog-scan/plugins/cncf-landscape.mjs` against the contract
this story ships. Filters by Helm-only + maturity ≥ incubating; maps
landscape categories to Sharko schema enum; proposes adds/updates.

## Tasks completed

1. **Skeleton + plugin contract (commit 1).** Wrote
   `scripts/catalog-scan.mjs` (~250 lines) covering CLI parsing, catalog
   YAML load via `yaml@^2.8.2`, plugin discovery glob, per-plugin
   timeout via AbortController, dry-run vs. default output branches,
   no-changes-no-output rule, and per-plugin error isolation. Added
   the plugin contract README + `_example` sentinel plugin + minimal
   `scripts/package.json` + `.gitignore` entry for
   `scripts/node_modules/`.
2. **Helper libs (commit 2).** Wrote the four `lib/*.mjs` modules:
   pure-function `diff()`, changeset aggregator + deterministic JSON
   serializer, fetch-with-retry helper (3 retries, 1s/2s/4s backoff,
   AbortSignal honor, `User-Agent: sharko-catalog-scan/1.0`), and a
   JSON-line stderr structured logger with `child(attrs)` for plugin
   scoping.
3. **Tests (commit 3).** 13 cases via Node's built-in `node:test`:
   6 diff cases, 3 changeset cases, 4 integration cases via
   `spawnSync`. Hermetic — no real network calls. Stubs are copied
   into a per-test temp dir which is passed as `--plugin-dir` so the
   production discovery path stays untouched. Updated
   `scripts/package.json`'s `test` script to use an explicit glob
   (Node `--test <dir>` does not recurse for nested `*.test.mjs`).
4. **Makefile (commit 4).** Added `catalog-scan` target that
   idempotently runs `npm install --prefix scripts --silent` and
   invokes the script in `--dry-run`. Added to the `.PHONY` list.
5. **BMAD tracking (commit 5).** Flipped V123-3.1 to `review`,
   updated `last_updated`, moved the story into a new "In review"
   subsection in `REMAINING-STORIES.md`, and filled the retrospective
   sections of this file.

## Files touched

- `scripts/catalog-scan.mjs` (NEW, ~250 lines) — entry point.
- `scripts/catalog-scan/plugins/README.md` (NEW) — plugin contract spec
  + changeset JSON shape doc.
- `scripts/catalog-scan/plugins/_example.mjs` (NEW, ~13 lines) —
  test-only sentinel.
- `scripts/catalog-scan/lib/diff.mjs` (NEW, ~80 lines).
- `scripts/catalog-scan/lib/changeset.mjs` (NEW, ~55 lines).
- `scripts/catalog-scan/lib/http.mjs` (NEW, ~65 lines).
- `scripts/catalog-scan/lib/logger.mjs` (NEW, ~30 lines).
- `scripts/catalog-scan/lib/diff.test.mjs` (NEW, 6 cases).
- `scripts/catalog-scan/lib/changeset.test.mjs` (NEW, 3 cases).
- `scripts/catalog-scan.test.mjs` (NEW, 4 integration cases).
- `scripts/catalog-scan/__tests__/fixtures/addons.tiny.yaml` (NEW).
- `scripts/catalog-scan/__tests__/stubs/empty.mjs` (NEW).
- `scripts/catalog-scan/__tests__/stubs/proposes-add.mjs` (NEW).
- `scripts/catalog-scan/__tests__/stubs/throws.mjs` (NEW).
- `scripts/package.json` (NEW) — manifest + npm test entry.
- `scripts/package-lock.json` (NEW, generated by `npm install`).
- `.gitignore` — added `scripts/node_modules/`.
- `Makefile` — added `catalog-scan` target.
- `.bmad/output/implementation-artifacts/sprint-status.yaml` —
  V123-3.1 flipped `in-progress` → `review`; comment headers updated.
- `.bmad/output/implementation-artifacts/REMAINING-STORIES.md` —
  added new "In review" subsection under Epic V123-3.
- `.bmad/output/implementation-artifacts/V123-3-1-...md` (this file) —
  status flipped, retrospective sections filled.

No Go files touched. No UI files touched. No
`.github/workflows/` files touched. No swagger regeneration (no API
surface added).

## Tests

Quality gates run in the brief's documented order:

1. `cd /Users/weissmmo/projects/github-moran/sharko && npm install --prefix scripts --silent`
   — clean install. 1 dep (`yaml@^2.8.2`).
2. `node --check scripts/catalog-scan.mjs` — syntax OK.
3. `node --check scripts/catalog-scan/lib/{diff,changeset,http,logger}.mjs`
   — all four syntax OK.
4. `node --test 'scripts/catalog-scan/**/*.test.mjs' 'scripts/catalog-scan.test.mjs'`
   — **13 / 13 pass** (~340 ms): 6 diff + 3 changeset + 4 integration.
5. `node scripts/catalog-scan.mjs --dry-run --catalog catalog/addons.yaml`
   — smoke run against the real 45-entry catalog with no plugins
   loaded (production default skips `_example`). Logs:
   ```
   {"level":"info","msg":"loaded catalog","catalog":"catalog/addons.yaml","entries":45}
   {"level":"info","msg":"no plugins discovered","plugin_dir":"scripts/catalog-scan/plugins","include_hidden":false}
   {"level":"info","msg":"no changes proposed by any plugin","plugins":0}
   ```
   Exit 0. No file written (no-changes path). Empty changeset emitted to
   stdout because `--dry-run` was set.
6. `make catalog-scan` — runs end-to-end via the new Makefile target.
   Same logs as gate 5.
7. `npm test --prefix scripts` — 13 / 13 pass via the npm script entry.

Skipped per brief: Go/UI builds (story touches neither).

## Decisions

1. **Adopted the prompt's `--plugin-dir` and `--include-hidden`
   corrections.** The brief's Tier 1 #7 originally suggested loading
   `_example.mjs` via the alphabetical glob with an `_`-prefix skip
   inside the script — but that conflicts with the brief's own intent
   ("ship in repo, skip in production"). The dispatch prompt resolved
   this by making `_`-prefix a hard skip in production and adding two
   opt-in mechanisms for tests:
   `--include-hidden` (CLI) and `SHARKO_SCAN_LOAD_HIDDEN=1` (env). To
   keep tests hermetic AND avoid mutating the production plugin
   directory at all, `--plugin-dir <path>` is also accepted; the
   integration tests build a temp dir of stub plugins (no leading `_`)
   and point the script at it. Production callers never need either
   flag — the defaults route discovery to
   `scripts/catalog-scan/plugins/` and skip `_example`.

2. **Built-in `node:test` runner instead of vitest.** The brief
   explicitly forbids new runtime deps beyond `yaml@^2.8.2`. Node 18+
   ships `node:test` and `node:assert/strict`. CI emits standard TAP;
   developers can run with `--test-reporter=spec` for human output.

3. **Glob-based npm test script.** Node's `--test <dir>` arg in
   v24 tries to load `<dir>` as a module rather than recursing — it
   does NOT discover nested `*.test.mjs` files. The fix is an
   explicit glob (`'catalog-scan/**/*.test.mjs'`) which Node's test
   runner expands. Documented in the commit message.

4. **`stringifyDeterministic` uses a `JSON.stringify` replacer that
   sorts keys for objects, leaves arrays alone.** Arrays in the
   changeset (`scanner_runs`, `adds`, `updates`) carry semantic order
   (audit trail), so re-sorting them would lie. Only object key order
   gets normalized — exactly the scope the snapshot test asserts on.

5. **`diff()` ignores fields the proposal didn't populate.** A
   missing `chart` key in a proposal is treated as "scanner has no
   opinion", not as "set to undefined". This matches the brief's
   intent that scanners propose updates only for fields they
   actively monitor — security_score / license / curated_by /
   github_stars stay human-curated and the diff never proposes
   blanking them.

6. **Per-plugin failures isolated, never fatal.** Brief AC: "one
   upstream blip doesn't blackhole the run". Implementation records
   the failure in `scanner_runs` with `error: <message>` and continues
   with the remaining plugins. Exit code stays 0 unless a global
   error (catalog YAML missing/parse failure, --out write failure)
   fires — those exit 1.

7. **`spawnSync` for integration tests.** Per the brief gotcha #8,
   pass `{cwd: REPO_ROOT}` so relative defaults like `catalog/addons.yaml`
   resolve, plus `{env: {...process.env, NO_COLOR: '1'}}` for
   deterministic output. Each test gets its own temp dir for
   `--plugin-dir` and `--out`, cleaned via `t.after()`.

8. **Log discipline.** `process.stderr.write` for all info/warn/error
   records (JSON-line); `process.stdout.write` only in `--dry-run`
   for the changeset JSON. Pipe-friendliness matters for V123-3.4's
   future CI integration (the GH Action will redirect stdout to a
   file).

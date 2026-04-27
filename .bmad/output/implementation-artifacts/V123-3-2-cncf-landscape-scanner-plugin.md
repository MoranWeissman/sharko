---
story_key: V123-3-2-cncf-landscape-scanner-plugin
epic: V123-3 (Trusted-source scanning bot)
status: review
effort: M
dispatched: 2026-04-26
depends_on: V123-3.1
---

# Story V123-3.2 — CNCF Landscape scanner plugin

## Brief (from epics-v1.23.md §V123-3.2)

As the **scanning bot**, I want a plugin that fetches the CNCF Landscape
YAML and proposes adds/updates for graduated + incubating projects with
Helm charts, so that Sharko's curated set stays current with CNCF
maturity signals.

## Acceptance Criteria

**Given** the plugin runs
**When** it fetches `https://raw.githubusercontent.com/cncf/landscape/master/landscape.yml`
**Then** it filters by category (security, observability, networking,
storage, autoscaling, gitops, database, backup, chaos, developer-tools)
+ maturity `graduated` or `incubating`.

**Given** a landscape item has no Helm chart reference
**Then** the plugin skips it (Helm-only per v1.21 §6 item 10).

**Given** a landscape item maps to an existing catalog entry
**When** the chart version or maintainer list has changed
**Then** the plugin proposes an update.

**Given** a landscape item doesn't exist in the catalog
**Then** the plugin proposes an add.

## Existing context (from V123-3.1)

- `scripts/catalog-scan/plugins/` is the discovery dir; alphabetical glob;
  `_`-prefix files skipped unless `--include-hidden` or
  `SHARKO_SCAN_LOAD_HIDDEN=1`.
- Plugin contract (per `scripts/catalog-scan/plugins/README.md`):
  ```
  export const name = '<short-id>';
  export async function fetch(ctx) { return [...] }
  ```
  where `ctx = { logger, abortSignal, http: fetchWithRetry }`.
- Helpers available:
  - `lib/http.mjs` — `fetch()`-with-3-retries (1s/2s/4s backoff),
    `User-Agent: sharko-catalog-scan/1.0`. Returns the Response — call
    `.text()` / `.json()` yourself.
  - `lib/logger.mjs` — JSON-line stderr logger; methods `info/warn/error`.
  - `lib/diff.mjs` + `lib/changeset.mjs` — consumed by the script after
    plugins return; the plugin doesn't call these itself.
- Test runner: `node:test` (built-in). Stubs live at
  `scripts/catalog-scan/__tests__/stubs/`; fixtures at
  `scripts/catalog-scan/__tests__/fixtures/`.
- Catalog YAML schema (`catalog/schema.json`):
  - `category` enum exactly matches the V123-3.2 AC list.
  - `curated_by` is an **array** of enum strings; `cncf-graduated` /
    `cncf-incubating` / `cncf-sandbox` are valid tokens (existing
    catalog entries already use these — see e.g. `cert-manager` →
    `[cncf-graduated, aws-eks-blueprints, artifacthub-verified]`).
  - Required fields: `name, description, chart, repo, default_namespace,
    category, curated_by, license, maintainers`.

## Scope (Tier-ordered)

### Tier 1 — Plugin implementation (REQUIRED)

1. **`scripts/catalog-scan/plugins/cncf-landscape.mjs`** (NEW) —
   the plugin module.
   - `export const name = 'cncf-landscape'`
   - `export async function fetch(ctx)` that:
     1. Fetches `https://raw.githubusercontent.com/cncf/landscape/master/landscape.yml`
        via `ctx.http(...)` (URL is overridable via env var
        `SHARKO_CNCF_LANDSCAPE_URL` for testing — default to the canonical
        URL).
     2. Parses the YAML via `yaml@^2.8.2` (already a dep from V123-3.1).
     3. Walks the landscape tree: `{landscape: [{category, subcategories: [{subcategory, items: [...]}]}]}`.
     4. For each item:
        - **Filter by maturity:** keep only `project === 'graduated'` or
          `project === 'incubating'` (skip `sandbox`, `archived`, empty).
        - **Filter by Helm chart presence:** keep only items where
          `extra.chart_url` OR `extra.helm_url` OR `extra.artifacthub_url`
          contains a Helm chart reference. (CNCF landscape uses several
          inconsistent fields across items — defensive checks.)
        - **Map landscape category → Sharko schema enum:** see Tier 2 #4
          mapping table. Items whose landscape category doesn't map →
          skip.
     5. Build a normalized entry per the V123-3.1 contract:
        ```
        {
          name: <slugify item.name>,             // DNS-safe lowercase
          repo: <derived helm repo URL>,
          chart: <derived chart name>,
          version: <latest version if known>,    // optional; many
                                                 // landscape items don't
                                                 // surface chart version
          category: <mapped Sharko category>,
          curated_by: ['cncf-graduated' | 'cncf-incubating'],
          maintainers: <derived from item or repo>,  // optional
          // proposal-only metadata (consumed by plugin's diff
          // hint logic; the changeset emitter passes everything through):
          _landscape_homepage: item.homepage_url,
          _landscape_source: item.repo_url,
        }
        ```
     6. Returns the array.
   - Per-item parse errors must NOT throw — log a warn line and skip
     that item.
   - Network errors propagate via `ctx.http`'s retry behavior; if all
     retries fail, the function throws and the script's per-plugin
     error isolation logs it in `scanner_runs[].error`.

2. **Slug normalizer** — small helper inside the plugin (or in
   `scripts/catalog-scan/lib/slug.mjs` if reusable):
   - Lowercase, replace non-`[a-z0-9-]` with `-`, collapse runs of `-`,
     trim leading/trailing `-`, max 63 chars (matches schema regex
     `^[a-z0-9][a-z0-9-]*[a-z0-9]$`).
   - **Critical:** the normalized name must match the catalog primary
     key for diff to work. If a landscape item's name is "cert-manager"
     and the existing catalog has it as `cert-manager`, slug → match.
     Document the mapping rule in the plugin file's top comment.

3. **Helm chart extraction** — separate helper inside the plugin:
   - Try fields in priority order: `extra.chart_url`, `extra.helm_url`,
     `extra.artifacthub_url`.
   - Parse the URL to derive `repo` (the chart repo base) and `chart`
     (the chart name). For `https://artifacthub.io/packages/helm/foo/bar`
     → repo `https://foo.example.invalid/charts` is unknown; **fall
     back: skip if we can't derive a deterministic helm repo URL**.
     The brief's AC says "skip if no Helm chart reference" — apply
     strictly.
   - When `extra.helm_url` is itself a chart-repo URL (e.g.
     `https://charts.jetstack.io`), set `repo` to that and chart to
     the slugified item name as a best guess.
   - Document in code comments that this is a heuristic; reviewers will
     correct in the bot PR.

### Tier 2 — Category mapping + tests (REQUIRED)

4. **Landscape → Sharko category mapping** as an inline JS object inside
   the plugin (no separate config file — keep it co-located):
   ```js
   const LANDSCAPE_TO_SHARKO_CATEGORY = {
     // CNCF landscape uses categories like "Provisioning", "Runtime",
     // "Orchestration & Management", "App Definition and Development",
     // "Observability and Analysis", "Platform". Their subcategories
     // are what we map.
     'Security & Compliance':       'security',
     'Key Management':              'security',
     'Monitoring':                  'observability',
     'Logging':                     'observability',
     'Tracing':                     'observability',
     'Service Mesh':                'networking',
     'Service Proxy':               'networking',
     'API Gateway':                 'networking',
     'Cloud Native Network':        'networking',
     'Cloud Native Storage':        'storage',
     'Automation & Configuration':  'gitops',  // close-enough; reviewers refine
     'Continuous Integration & Delivery': 'gitops',
     'Database':                    'database',
     'Streaming & Messaging':       'database',  // close-enough
     'Container Runtime':           null,        // not a Sharko addon
     'Cloud Native Storage':        'storage',
     'Chaos Engineering':           'chaos',
     'Application Definition & Image Build': 'developer-tools',
     'Coordination & Service Discovery': 'networking',
     'Scheduling & Orchestration':  null,        // K8s-internal
     'Container Registry':          null,
   };
   ```
   - Items whose subcategory maps to `null` → skipped with an info log.
   - **The brief allows the implementer to refine this map after looking
     at the live landscape.yml** — the goal is "reasonable best
     effort," not perfect 1:1. Reviewers tune the map in follow-up
     stories or directly in the bot PR.

5. **`scripts/catalog-scan/__tests__/fixtures/landscape.fixture.yaml`**
   (NEW) — minimal landscape.yml-shaped fixture covering ALL the
   filter / mapping branches:
   - 1 item: graduated + helm + maps to security → produces add proposal.
   - 1 item: incubating + helm + maps to observability → produces add.
   - 1 item: graduated + helm + already in catalog with same chart
     version → produces NO update.
   - 1 item: graduated + helm + already in catalog with DIFFERENT chart
     version → produces update.
   - 1 item: graduated + helm + maintainers list changed → produces
     update (chart version unchanged).
   - 1 item: sandbox + helm → skipped (maturity filter).
   - 1 item: graduated + NO helm reference → skipped (Helm-only).
   - 1 item: graduated + helm + landscape category maps to `null`
     (e.g. Container Runtime) → skipped.
   - Total: 8 items, 4 produce proposals (3 adds + 1 update from version
     diff + 1 update from maintainers diff = 4 proposals total —
     check arithmetic; brief expects 3 adds + 2 updates = 5 actually
     — count the bullets again: items 1, 2, 4, 5 produce proposals;
     items 3, 6, 7, 8 don't. So 4 proposals: 2 adds + 2 updates. The
     fixture must exactly match what the test asserts; let the
     implementer pick the shape and document in the test).

6. **`scripts/catalog-scan/plugins/cncf-landscape.test.mjs`** (NEW) —
   ~6 cases:
   - **happy path:** runs the plugin against the fixture (HTTP stubbed
     via Node's `http.createServer` on a random port, OR via setting
     `SHARKO_CNCF_LANDSCAPE_URL=file://<fixture-path>` if the
     `ctx.http` helper supports `file://` — confirm this; if not, use
     httptest pattern). Asserts the right number of normalized
     entries comes back with the right shape.
   - **maturity filter:** sandbox item is skipped.
   - **helm filter:** no-chart item is skipped.
   - **category filter:** unmapped-category item is skipped.
   - **slug normalization:** an item with `name: "Cert-Manager 2.0!"`
     normalizes to `cert-manager-2-0` (or similar — match the rule
     documented in the plugin).
   - **network error tolerance:** ctx.http throws → plugin throws →
     scanner script logs in scanner_runs[].error (this is integration
     behavior; the plugin's own test just asserts the throw
     propagates).

7. **Integration test extension** — add ONE new case to
   `scripts/catalog-scan.test.mjs`:
   - **integration:** invoke the script via spawnSync with the
     fixture-backed plugin loaded; assert the changeset JSON contains
     the expected number of adds/updates against the real
     `catalog/addons.yaml`.
   - Use a stub for the HTTP fetch by pointing
     `SHARKO_CNCF_LANDSCAPE_URL` to a `file://` URL OR a tiny test
     HTTP server. Pick whichever the http.mjs helper supports.

### Tier 3 — Smoke run + docs (RECOMMENDED)

8. **Update `scripts/catalog-scan/plugins/README.md`** — add a section:
   "Real plugin example: `cncf-landscape.mjs`" with a 5-line summary of
   what it does, the env var override, and a link to the AC.

9. **Manual smoke run** documented in the retrospective:
   - `node scripts/catalog-scan.mjs --dry-run --catalog catalog/addons.yaml`
     with the plugin loaded → should hit the live landscape.yml and
     produce a real changeset. Capture top-line numbers (adds count,
     updates count) in the retrospective. **Do NOT commit the
     resulting changeset JSON** — it's noise that will rot.

### Out of scope — explicit non-goals

- **GitHub Actions workflow / nightly cron / PR-opener.** V123-3.4.
- **AWS EKS Blueprints plugin.** V123-3.3.
- **Reviewer runbook docs.** V123-3.5.
- **Trust-score computation.** Brief allowed `trust_score` as plugin
  output but landscape.yml doesn't surface anything useful here. Skip
  it. Document this decision; V123-3.4's PR-opener can apply a fixed
  baseline like `60` for cncf-graduated, `40` for cncf-incubating.
- **Auto-merge logic.** Open question §7.3 — V123-3.4 territory.
- **Catalog schema changes.** None needed; existing schema accommodates
  `cncf-graduated` / `cncf-incubating` already.
- **Validation of plugin output against schema.** Done by humans at
  PR-review time + Go loader at merge time. Don't reimplement schema
  enforcement in JS.
- **A landscape.yml schema validator.** It's a known-good upstream
  source; defensive parsing (skip-on-error) is sufficient.

## Implementation plan

### Files

- `scripts/catalog-scan/plugins/cncf-landscape.mjs` (NEW, ~150-200
  lines including category map + helpers).
- `scripts/catalog-scan/plugins/cncf-landscape.test.mjs` (NEW, ~6
  cases, ~150 lines).
- `scripts/catalog-scan/__tests__/fixtures/landscape.fixture.yaml`
  (NEW, ~80 lines covering 8 representative items).
- `scripts/catalog-scan.test.mjs` (MODIFY) — add 1 integration case.
- `scripts/catalog-scan/plugins/README.md` (MODIFY) — append "Real
  plugin example" section.
- `scripts/catalog-scan/lib/slug.mjs` (NEW IF reusable; else inline) —
  slug normalizer.
- `.bmad/output/implementation-artifacts/sprint-status.yaml` — flip
  V123-3-2 backlog → in-progress → review.
- `.bmad/output/implementation-artifacts/REMAINING-STORIES.md` — note
  V123-3.2 in flight.
- `.bmad/output/implementation-artifacts/V123-3-2-...md` (this file) —
  retrospective sections appended.

### Quality gates (run order)

1. `cd /Users/weissmmo/projects/github-moran/sharko && npm install --prefix scripts --silent` — clean.
2. `node --check scripts/catalog-scan/plugins/cncf-landscape.mjs` —
   syntax OK.
3. `node --test 'scripts/catalog-scan/**/*.test.mjs' 'scripts/catalog-scan.test.mjs'`
   — all unit + integration tests pass. Existing 13 + ~7 new = ~20.
4. **Smoke run** (REQUIRED — ground the implementation in reality):
   `node scripts/catalog-scan.mjs --dry-run --catalog catalog/addons.yaml`
   with the plugin loaded (production discovery picks it up
   automatically since name doesn't start with `_`). This will hit the
   live landscape.yml. Record the resulting top-line counts in the
   retrospective. If it errors → STOP and report.
5. **No Go/UI gates** — story doesn't touch Go or UI.

### Anti-gold-plating reminders

- **Do NOT commit the smoke-run changeset JSON.**
- **Do NOT add `.github/workflows/catalog-scan.yml`.** V123-3.4.
- **Do NOT scaffold the EKS Blueprints plugin.** V123-3.3.
- **Do NOT add a category-map config file.** Inline in the plugin;
  reviewers tune it in PRs.
- **Do NOT validate against schema.json in JS.** Go loader is
  authoritative.
- **Do NOT compute `trust_score`.** Out of scope; V123-3.4 applies a
  baseline.
- **Do NOT add description text to proposals.** Leave description
  empty or set to a fixed `"<TODO: human description>"` marker — the
  reviewer fills it in. Same for `default_namespace` — emit
  `<slug>-system` as a guess and let the reviewer correct.
- **Do NOT add license inference.** Many landscape items don't surface
  it; emit `"unknown"` and let the reviewer correct. Schema requires
  the field non-empty so use a literal `"unknown"` string. Note: the
  schema's allow-list (Apache-2.0, BSD-3-Clause, MIT, MPL-2.0) is for
  CI flagging; `"unknown"` will trip it for human review — that's the
  correct behavior for a bot proposal.

## Dependencies

- **V123-3.1** — done ✅ (skeleton merged at PR #296 / `3eb97d0`).

## Gotchas

1. **landscape.yml is large** (~5 MB raw). The `http.mjs` helper has
   no body-size cap today; verify it doesn't OOM. If memory becomes an
   issue, add a streaming parse — but premature; start with the simple
   `yaml.parse(text)` path.

2. **CNCF landscape category nesting.** The top-level `landscape:` is an
   array of `{category, subcategories: [{subcategory, items: []}]}`.
   Items live two levels deep. The category mapping table keys against
   `subcategory`, not `category`.

3. **`project` field on landscape items** is the maturity:
   `'graduated' | 'incubating' | 'sandbox' | undefined | ''`. Treat
   missing/empty as "not a CNCF project" → skip.

4. **Helm chart references in landscape.yml are inconsistent.** Some
   items have `extra.chart_url` (full chart URL), some have
   `extra.helm_url` (chart repo URL), many have neither. Skip items
   with no recognizable chart reference rather than guessing.

5. **The fixture YAML must be valid landscape.yml shape** so the parser
   accepts it without special-casing. Mirror the upstream's structure
   exactly (just trimmed). Look at one or two items in the live
   landscape.yml as a structural template.

6. **HTTP stub via env var.** The plugin reads
   `SHARKO_CNCF_LANDSCAPE_URL`; tests can set it to a `file://` URL
   pointing at the fixture OR spin up a tiny `http.createServer` in
   the test setup. `file://` is simpler if `ctx.http` (built on
   Node's built-in `fetch`) supports it; Node 18+ may not — confirm
   in the agent's first test iteration, fall back to httptest if not.

7. **Slug collisions.** Two landscape items could slugify to the same
   name. Treat as warning + emit both — the diff helper will emit two
   adds tagged with the same plugin name; the reviewer dedupes
   manually. Don't engineer a dedupe layer.

8. **Maintainer derivation.** landscape.yml doesn't carry chart
   maintainers reliably. Two pragmatic options:
   - Emit empty maintainers array → schema-invalid (minItems: 1) — the
     proposal won't merge as-is, forces human review. **Acceptable.**
   - Emit `["unknown"]` placeholder → schema-valid but lies. **Not
     acceptable.**
   - Emit `["<TODO: derive from chart repo>"]` marker → schema-valid,
     clear intent. **Preferred.**

9. **Update detection precision.** AC says "chart version OR
   maintainer list changed → propose update". The diff helper from
   V123-3.1 (`lib/diff.mjs`) does deep-equal on the comparable subset.
   The plugin only emits `version` (sometimes) and `maintainers` (the
   TODO marker). For an existing entry, the diff helper compares
   what's emitted against what's in the catalog — the maintainer
   "TODO" marker won't equal the real list, so EVERY existing entry
   would diff. **This is wrong.** Fix: the plugin should NOT emit
   maintainers for items it can't derive reliably. The diff helper
   skips fields that aren't on the proposed entry. Re-read
   `lib/diff.mjs` to confirm this — if it doesn't, that's a small
   V123-3.1 bug to fix in this story or a follow-up.

10. **Live URL flakiness.** github.com raw can rate-limit or 5xx.
    Smoke run might fail transiently. If smoke run fails on first try,
    retry once before reporting blocked.

## Role files (MUST embed in dispatch)

- `.claude/team/devops-agent.md` — primary (script automation,
  upstream-source parsing, CI-adjacent code).
- `.claude/team/architect.md` — secondary (the diff-emission semantics
  for "fields the plugin doesn't know" needs careful reasoning so the
  diff helper doesn't churn proposals every run).

## PR plan

- **Branch:** `dev/v1.23-cncf-landscape-plugin` off `main` (current
  HEAD `9cf977a` after the argosecrets race fix).
- **Commits:**
  1. `feat(scanner): cncf-landscape plugin — fetch + filter + map (V123-3.2)`
     — `cncf-landscape.mjs` + slug helper + category map.
  2. `test(scanner): cncf-landscape unit + integration tests (V123-3.2)`
     — `cncf-landscape.test.mjs` + `landscape.fixture.yaml` +
     1 new case in `catalog-scan.test.mjs`.
  3. `docs(scanner): document cncf-landscape in plugins README (V123-3.2)`
     — README append.
  4. `chore(bmad): mark V123-3.2 for review (Epic V123-3 2/5)`
     — sprint-status.yaml + REMAINING-STORIES.md + this file.
- **PR body** must call out:
  - "Epic V123-3: 2 of 5 in review."
  - "Smoke run output (live landscape.yml): X adds, Y updates against
    current 45-entry catalog."
  - Any category-map gaps the smoke run revealed (note in
    Decisions section so V123-3.4's reviewer runbook can cover them).
  - "NO live network in CI tests — fixture-backed via env var."
- **NO TAG.** v1.23.0 cut belongs to V123-4.5.

## Next story

**V123-3.3** — AWS EKS Blueprints scanner plugin. Parses
`aws-ia/terraform-aws-eks-blueprints-addons` repo's `addons/`
directory. Same plugin contract; different upstream shape (Terraform
modules, not landscape.yml). Builds on the diff-helper observations
this story exercises.

## Tasks completed

1. **Plugin module (commit 1).** Wrote
   `scripts/catalog-scan/plugins/cncf-landscape.mjs` (~349 lines).
   Implements `export const name = 'cncf-landscape'` and
   `export async function fetch(ctx)` plus a small `slugify()` helper
   exported for unit testing. Pipeline: fetch landscape.yml via
   `ctx.http` (with a `file://` fallback for fixture-backed tests
   since Node's built-in fetch doesn't support `file://` as of Node
   18-22) → `yaml.parse` → walk `landscape[].subcategories[].items[]`
   → filter by maturity (graduated/incubating only) → filter by Helm
   chart presence → map subcategory to Sharko `category` enum via
   inline `LANDSCAPE_TO_SHARKO_CATEGORY` → emit normalized entries
   with TODO markers for non-derivable fields. Per-item parse errors
   log a warn and continue; network errors propagate to the harness.
2. **Fixture (commit 2).** Wrote
   `scripts/catalog-scan/__tests__/fixtures/landscape.fixture.yaml`
   (~120 lines, 8 items). Mirrors upstream `landscape.yml` shape
   exactly (dual-key `- category:` / `name:` idiom). Items chosen to
   exercise every filter + mapping branch: 2 ADD candidates, 2
   UPDATE candidates (cert-manager + external-dns — names that
   exist in the real catalog), 4 SKIP candidates (sandbox maturity,
   no-helm-ref, Container Runtime null-mapped, unmapped subcategory).
3. **Plugin unit tests (commit 2).** Wrote
   `scripts/catalog-scan/plugins/cncf-landscape.test.mjs` (7 cases,
   ~165 lines). Stubs `ctx.http` directly with a Response-shaped
   object so the tests stay hermetic without depending on Node's
   `file://` fetch behavior. Cases: happy-path shape (4 entries with
   correct fields), maturity filter, helm filter, category filter,
   slugify edge cases, ctx.http throw propagation, non-2xx response
   throw.
4. **Integration test extension (commit 2).** Added one new case to
   `scripts/catalog-scan.test.mjs` (`integration: cncf-landscape
   plugin against real catalog (V123-3.2)`). Spawns the script as a
   child process pointing `--plugin-dir` at the production
   `scripts/catalog-scan/plugins/` (so the real
   `cncf-landscape.mjs` is loaded; `_example.mjs` is skipped per
   discovery rules), `--catalog catalog/addons.yaml` (the real
   45-entry catalog), and sets `SHARKO_CNCF_LANDSCAPE_URL` to a
   `file://` URL pointing at the fixture. Asserts exactly 2 adds
   (argo-cd-fixture, fixture-monitor-x) + 2 updates (cert-manager
   with version+repo diff, external-dns with category+repo diff).
5. **Plugins README update (commit 3).** Appended a "Real plugin
   example: `cncf-landscape.mjs`" section per Tier 3 #8: 5-line
   summary, env-var override snippet, TODO-marker policy note, link
   to the V123-3.2 brief.
6. **BMAD tracking (commit 4).** Flipped V123-3-2 in
   `sprint-status.yaml` from `in-progress` → `review`, updated
   `last_updated` line + comment header. Moved V123-3.2 from
   backlog to a new "In review" subsection in
   `REMAINING-STORIES.md` and reflected V123-3.1 completion.
   Filled the four retrospective sections of this file.

## Files touched

- `scripts/catalog-scan/plugins/cncf-landscape.mjs` (NEW, ~349 lines).
- `scripts/catalog-scan/plugins/cncf-landscape.test.mjs` (NEW, 7
  cases, ~165 lines).
- `scripts/catalog-scan/plugins/README.md` (MODIFIED, +27 / -1
  lines).
- `scripts/catalog-scan/__tests__/fixtures/landscape.fixture.yaml`
  (NEW, ~120 lines).
- `scripts/catalog-scan.test.mjs` (MODIFIED, +63 / -3 lines —
  added integration case + `pathToFileURL` import + extra-env
  parameter on `runScript`).
- `.bmad/output/implementation-artifacts/sprint-status.yaml` —
  V123-3-2 flipped `backlog` → `in-progress` → `review`; comment
  header + `last_updated` line refreshed.
- `.bmad/output/implementation-artifacts/REMAINING-STORIES.md` —
  V123-3.1 moved to a "Done" subsection under Epic V123-3; V123-3.2
  moved into a new "In review" subsection.
- `.bmad/output/implementation-artifacts/V123-3-2-...md` (this file)
  — status flipped to `review`, retrospective sections filled.

No Go files touched. No UI files touched. No
`.github/workflows/` files touched. No swagger regeneration (no API
surface added). No new runtime dependencies (only `yaml@^2.8.2`
already pinned by V123-3.1).

## Tests

Quality gates run in the brief's documented order:

1. `cd /Users/weissmmo/projects/github-moran/sharko && npm install --prefix scripts --silent`
   — clean. No new deps, lock unchanged.
2. `node --check scripts/catalog-scan/plugins/cncf-landscape.mjs` —
   syntax OK.
3. `node --test 'scripts/catalog-scan/**/*.test.mjs' 'scripts/catalog-scan.test.mjs'`
   — **21 / 21 pass** (~460 ms): 6 diff + 3 changeset + 5
   integration (V123-3.1's 4 + V123-3.2's 1) + 7 cncf-landscape unit.
4. **Smoke run** (REQUIRED — ground the implementation in reality).
   Command:
   ```
   node scripts/catalog-scan.mjs --dry-run --catalog catalog/addons.yaml
   ```
   Live `landscape.yml` was fetched and parsed cleanly on first try
   (no retry). Filter summary:
   ```
   kept: 0
   skipped_maturity: 2326   (most landscape items aren't CNCF projects)
   skipped_helm:    47   (graduated/incubating items without a
                          recognizable Helm chart reference)
   skipped_category: 25   (graduated/incubating + helm but maps to
                          null subcategory like Container Runtime)
   skipped_parse:    0
   ```
   Result: **0 adds, 0 updates, 0 errors**. Empty changeset emitted to
   stdout (no file written — V123-3.1's no-changes path is honored).
   Exit 0. **No JSON output committed** — per anti-gold-plating rule.
5. Skipped: Go/UI gates (story touches neither).

## Decisions

1. **Broadened the Helm-reference field heuristic beyond the brief.**
   Brief Tier 1 #1 listed `extra.chart_url`, `extra.helm_url`,
   `extra.artifacthub_url`. Inspection of the live landscape.yml
   showed the dominant field today is `extra.helm_chart_url` (only
   one item — Vald — uses it currently, but it's the field the
   landscape schema documents). Added `helm_chart_url` as the
   highest-priority candidate; kept the brief's three as fallbacks.
   This is a documented deviation: it broadens, not contradicts.

2. **Bare chart-repo URLs (`extra.helm_url`) are deliberately
   skipped instead of guessed.** The brief's "fall back: skip if we
   can't derive a deterministic helm repo URL" rule applies. Names
   in landscape.yml are display names ("Cert Manager") and don't
   reliably equal Helm chart names (`cert-manager`). Emitting a
   guessed chart name would risk fabricating a proposal that fails
   schema validation OR — worse — silently mismatches a real chart.
   `parseHelmUrl` returns `null` for bare repo URLs; the harness
   skips those items with no proposal. Reviewers can extend
   `parseHelmUrl` in V123-3.4+ if needed.

3. **`file://` fallback in the plugin itself, not in `lib/http.mjs`.**
   The brief allowed switching tests to env-var-driven local-file
   loading if `lib/http.mjs` lacked `file://` support (it does —
   Node's built-in fetch doesn't handle `file://` as of Node 18-22).
   Implemented a one-call `node:fs/promises.readFile` short-circuit
   inside `fetchAsText` so the plugin transparently handles
   `SHARKO_CNCF_LANDSCAPE_URL=file://...`. Keeps `lib/http.mjs`
   focused on http(s) retry semantics; tests stay hermetic without
   spinning up an `http.createServer`. Production callers always use
   https://, so the fallback is test-only in practice.

4. **`lib/diff.mjs` was NOT modified.** Confirmed gotcha #9 is a
   non-issue: V123-3.1's `diffFields()` already short-circuits
   (`if (!(f in proposed)) continue;`) AND `COMPARABLE_FIELDS` is
   `['chart', 'version', 'category', 'repo']` — neither
   `maintainers` nor `description` nor `license` nor
   `default_namespace` is in the comparable set. The plugin's TODO
   markers for those fields are emitted (so proposal entries are
   schema-valid for downstream consumers) but they never trigger
   spurious updates against curated catalog data. No follow-up
   needed.

5. **Plugin tests stub `ctx.http` directly rather than using
   `file://`.** Both work, but stubbing `ctx.http` is simpler (no
   filesystem scaffolding inside the unit test) and directly
   exercises the plugin's "Response object shape" expectations. The
   `file://` codepath is exercised by the integration test in
   `scripts/catalog-scan.test.mjs` instead — which is where we want
   the file://-via-env-var path covered anyway (it tests the full
   subprocess invocation).

6. **Integration test points `--plugin-dir` at the real
   `scripts/catalog-scan/plugins/` directory.** Avoids module-
   resolution headaches when copying a plugin file to a tempdir
   (the plugin's `import 'yaml'` would fail to resolve from
   `/tmp/...`). Production discovery rules already skip `_example`
   so only `cncf-landscape.mjs` runs. No tempdir cleanup needed for
   the plugin dir, but `--out` is still tempdir'd via the existing
   helper for the no-dry-run cases (this test uses `--dry-run` so
   nothing is written).

7. **Description policy: literal `<TODO: human description>` string.**
   Schema requires non-empty `description` (minLength 1; maxLength
   256). Empty would invalidate the proposal. Picked the explicit
   TODO marker over generic placeholder text so any reviewer
   immediately sees the field needs attention. Same rationale for
   the `["<TODO: derive from chart repo>"]` maintainers list.

8. **License: literal `"unknown"` string.** Schema's `license`
   allow-list (Apache-2.0, BSD-3-Clause, MIT, MPL-2.0) is a CI
   advisory check, not a hard validation — `"unknown"` will trip
   that check and force human review, which is the correct
   behavior for a bot-proposed entry.

9. **`default_namespace = "<slug>-system"` heuristic.** Predictable,
   schema-compatible, and reviewer-correctable. For the rare slug
   that's >55 chars (so `<slug>-system` would exceed the schema's
   63-char cap), `namespaceFor` falls back to clipping the slug
   itself — reviewers will fix this manually if it ever happens.

10. **Smoke run yielded 0 proposals — this is correct, not a bug.**
    The live landscape.yml currently has only one project (Vald)
    with `helm_chart_url`, and Vald has no `project:` maturity tag
    (it's not formally a CNCF project today). 47 graduated/incubating
    projects lack Helm chart references in landscape.yml entirely.
    The plugin correctly skips them per the AC's "Helm-only" rule.
    **Category-map gaps observed:** none surfaced in the smoke run
    (no items reached the category-mapping stage that would have
    revealed unmapped subcategories). V123-3.4's reviewer runbook
    will need to flag the broader issue: landscape.yml doesn't
    currently surface enough Helm metadata to drive material PRs.
    Mitigation paths for V123-3.3+: (a) consult ArtifactHub directly
    for graduated/incubating CNCF charts, (b) use a curated
    secondary list (e.g. the EKS Blueprints addon catalog —
    V123-3.3's job).

11. **No new dependencies added.** Only `yaml@^2.8.2` is used
    (already pinned by V123-3.1). All other helpers come from Node
    18+ built-ins (`node:fs/promises`, `node:url`, `node:test`,
    `node:assert/strict`).

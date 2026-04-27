---
story_key: V123-3-4-pr-opening-logic-github-workflow-open-question-7-3
epic: V123-3 (Trusted-source scanning bot)
status: review
effort: M
dispatched: 2026-04-27
depends_on: V123-3.2, V123-3.3
resolves: OQ §7.3
---

# Story V123-3.4 — PR-opening logic + GitHub workflow + resolve OQ §7.3

## Brief (from epics-v1.23.md §V123-3.4)

As the **scanning bot**, I want one PR per scan run with a rich body
and appropriate labels, so that reviewers can act on proposals quickly.

**This story is the END-USER VALUE delivery of Epic V123-3** — the
prior three stories (skeleton + 2 scanners) are infrastructure; this
story turns scanner output into actionable PRs on a daily cadence.

## Acceptance Criteria

**Given** the open question §7.3 is resolved (draft-to-main vs
`catalog-updates` branch)
**When** the decision lands
**Then** it is recorded; **leaning draft-to-main with the
`catalog-scan` label** (per epic AC).

**Given** a scan run produces a changeset
**When** the workflow's PR-opening step runs
**Then** a branch `catalog-scan/<YYYY-MM-DD>` is created, the catalog
is updated, and a PR is opened with labels `catalog-scan` +
`needs-review`.

**Given** the PR body
**Then** it contains a markdown table for each proposed change
including: action (add/update), Scorecard pre-computed, license
pre-checked (allow-list), chart resolvability (index.yaml check), and
the upstream source that proposed it.

**Given** `.github/workflows/catalog-scan.yml`
**When** the cron `0 4 * * *` fires
**Then** the workflow runs the script and opens PRs.

**Given** the workflow never auto-merges (NFR-V123-7)
**Then** the `PULL_REQUESTS_AUTOMERGE` permission is not granted.

**Given** rate limits
**When** upstream returns 429 / 5xx
**Then** exponential backoff retries up to 3 times.

## Open Question §7.3 — Resolution

**Decision: draft-to-main + label `catalog-scan` + label `needs-review`.**

Rationale (record in the brief retrospective + design doc update):
- **Draft-to-main keeps the review surface small** — one PR open at a
  time vs an ever-growing branch with stacked commits no human triages.
- **`catalog-scan` label** lets repo CODEOWNERS rules + branch
  protection treat these PRs distinctly from human contributions.
- **`needs-review` label** is the universal "human eyes required"
  signal; pairs with the existing repo CODEOWNERS for catalog/.
- **NEVER auto-merge** (NFR-V123-7) — the `automerge` permission is
  explicitly NOT granted in the workflow YAML.
- A `catalog-updates` long-lived branch was rejected because:
  - Stacking proposals creates merge-conflict churn the bot would
    have to resolve.
  - Every fresh scan would need to rebase onto main (more workflow code).
  - Single-PR-per-day is easier to triage; close-without-merge is the
    natural reject path.

This decision must also be added to the design doc
(`docs/design/2026-04-20-v1.23-catalog-extensibility.md` §7.3) — the
brief includes a §Documentation update task.

## Existing context

- V123-3.1 + V123-3.2 + V123-3.3 all merged. Pipeline:
  `node scripts/catalog-scan.mjs --catalog catalog/addons.yaml` →
  writes `_dist/catalog-scan/changeset.json` (or no-op exit 0 + no
  file when zero proposals).
- Changeset shape (from `scripts/catalog-scan/lib/changeset.mjs`):
  ```
  {
    schema_version: '1.0',
    generated_at:   '<RFC3339>',
    scanner_runs:   [{ plugin, fetched_count, error? }],
    adds:           [{ plugin, entry }],
    updates:        [{ plugin, entry, diff }],
  }
  ```
- `entry` is the normalized scanner-output shape: `{name, repo, chart,
  version?, category, curated_by, default_namespace, description,
  license, maintainers, _<plugin-specific-meta>}`.
- `diff` (updates only) is `{ field: { from, to } }`.
- Existing `catalog/addons.yaml` is the file to edit. Schema enforces
  required fields; the scanner's TODO-marker conventions
  (`description: '<TODO: human description>'`, `license: 'unknown'`,
  `maintainers: ['<TODO: derive from chart repo>']`) keep the resulting
  YAML schema-valid + clearly intent-marking for reviewers.
- `gh` CLI is already used in the repo — `gh pr create` with
  `--body-file` is the canonical pattern.
- `lib/http.mjs` (V123-3.1) ships fetch-with-3-retries + 1s/2s/4s
  backoff + AbortSignal — reusable for signal pre-compute.
- No `internal/scorecard/` Go package exists today. Scorecard signal
  is computed via `https://api.securityscorecards.dev/projects/github.com/<owner>/<repo>`
  HTTP API (anonymous; rate-limited; non-fatal on failure).
- `scripts/package.json` has `yaml@^2.8.2` — used to edit the catalog.
- `scripts/Makefile` target `catalog-scan` runs `npm install --prefix scripts`
  + the scanner; this story adds a sibling `catalog-scan-pr` target.

## Scope (Tier-ordered)

### Tier 1 — PR-opener script + signal pre-compute (REQUIRED)

1. **`scripts/catalog-scan/pr-open.mjs`** (NEW, ~250-350 lines).
   - Shebang + JSDoc top comment matching V123-3.1 / V123-3.2 style.
   - CLI args:
     - `--changeset <path>` (default `_dist/catalog-scan/changeset.json`)
     - `--catalog <path>` (default `catalog/addons.yaml`)
     - `--dry-run` — print the PR body markdown to stdout + skip all
       git/gh side effects
     - `--branch <name>` (default `catalog-scan/<YYYY-MM-DD>` based on
       UTC today; override allows for manual/test runs)
   - Reads the changeset JSON. If `adds.length + updates.length === 0`
     → log "no proposals" and exit 0 (no PR).
   - **Concurrency guard:**
     - Skip if `gh pr list --label catalog-scan --state open --json number`
       returns a non-empty array — already-open PR; let humans triage
       it before opening another.
     - Skip if the target branch already exists locally OR remotely
       (`git ls-remote --heads origin catalog-scan/<date>` non-empty).
     - On skip: log "open PR or branch already exists" + exit 0.
   - **Signal pre-compute** (per proposal — see Tier 1 #2 below).
     Run sequentially per entry; failures emit "unknown" — never abort.
   - **YAML editing** (per proposal — see Tier 1 #3 below).
   - **Branching + commit:**
     - `git checkout -b catalog-scan/<YYYY-MM-DD>` (fail clean if exists)
     - Write the edited YAML.
     - `git add catalog/addons.yaml`
     - `git commit -m "catalog-scan: <N> adds, <M> updates from <plugins>"`
   - **PR opening:**
     - Push: `git push -u origin catalog-scan/<YYYY-MM-DD>`
     - `gh pr create --title "..." --body-file <tmp> --label catalog-scan --label needs-review --draft`
       (draft mode — reinforces "human review required" per OQ §7.3).
   - **Top-level error handling:** any unrecoverable error logs the
     reason + exits 1 (the workflow YAML continues / fails accordingly).

2. **Signal pre-compute helpers** in `scripts/catalog-scan/lib/signals.mjs`
   (NEW).
   - `async function scorecardForRepo(repoUrl, ctx)` — derives
     `<owner>/<repo>` from a github.com URL; calls
     `https://api.securityscorecards.dev/projects/github.com/<owner>/<repo>`;
     returns `{score: 0..10, updated: '<date>'}` or `'unknown'` on
     any failure (404 = never-scored, also "unknown"). Logs
     warn on failure with the URL fingerprint.
   - `async function chartIndexResolves(repoUrl, chartName, ctx)` —
     fetches `<repoUrl>/index.yaml` (only for `https://` repos —
     `oci://` returns `'oci-not-checked'`); parses; checks if
     `entries[chartName]` exists. Returns `'ok' | 'missing' | 'unknown'`.
     Caches per-repo so multi-add proposals don't hammer the same URL.
   - `async function licenseFromChart(chartIndex, chartName)` — when
     the index.yaml entry includes a license field, return it; else
     return `'unknown'`. Allow-list check: if license is in
     `[Apache-2.0, BSD-3-Clause, MIT, MPL-2.0]` mark `ok`; else
     `flagged`.
   - All helpers are pure-ish (only side effect = HTTP via the
     reusable `lib/http.mjs`) and exported individually for testing.

3. **YAML editing** in `scripts/catalog-scan/lib/yaml-edit.mjs` (NEW).
   - `applyChangeset(yamlText, changeset) → newYamlText` —
     - For each add: append a new `addons:` entry (or insert
       alphabetically by name to keep diff stable).
     - For each update: locate the entry by name; apply `diff`
       field-by-field.
     - Use `yaml@^2.8.2` AST mode (`yaml.parseDocument`) to preserve
       comments and field order on existing entries — crucial for
       small diffs that reviewers can read.
     - Validation: after editing, re-parse + assert root has
       `addons:` array + each entry has `name`. Don't reimplement
       schema validation (Go loader does that on PR merge).
   - Pure function — no I/O. Caller writes the result back to disk.

### Tier 2 — Workflow YAML + Makefile target (REQUIRED)

4. **`.github/workflows/catalog-scan.yml`** (NEW).
   - `name: Catalog Scan`
   - Triggers:
     - `schedule: - cron: '0 4 * * *'` (daily 04:00 UTC per epic AC)
     - `workflow_dispatch:` — manual trigger for testing.
   - **Permissions** (CRITICAL — NFR-V123-7):
     ```yaml
     permissions:
       contents: write       # push branch + edit catalog/addons.yaml
       pull-requests: write  # open PR
       # NO actions: write
       # NO automerge / merge-queue permissions
     ```
   - Concurrency:
     ```yaml
     concurrency:
       group: catalog-scan
       cancel-in-progress: false  # let in-flight runs finish
     ```
   - One job, sequential steps:
     1. `actions/checkout@v4` with `fetch-depth: 0` (so we can push a
        new branch).
     2. `actions/setup-node@v4` with `node-version: 20` + `cache: 'npm'`
        + `cache-dependency-path: 'scripts/package-lock.json'`.
     3. `npm ci --prefix scripts`
     4. **Configure git author:** `git config user.name 'Moran Weissman'` +
        `git config user.email 'moran.weissman@gmail.com'` — matches
        the repo's git-author convention (CLAUDE.md).
     5. **Run scanner** with `GITHUB_TOKEN` set:
        ```yaml
        - run: node scripts/catalog-scan.mjs --catalog catalog/addons.yaml
          env:
            GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        ```
     6. **Conditional PR-open** — only if changeset file exists:
        ```yaml
        - run: |
            if [ -s _dist/catalog-scan/changeset.json ]; then
              node scripts/catalog-scan/pr-open.mjs
            else
              echo "no proposals — skipping PR open"
            fi
          env:
            GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        ```
   - Top of file: comment block explaining "first cron firing after
     this lands is the integration test; consider triggering once via
     `gh workflow run Catalog\ Scan` after merge".

5. **`Makefile` target `catalog-scan-pr`** (MODIFY existing Makefile):
   ```make
   .PHONY: catalog-scan-pr
   catalog-scan-pr: catalog-scan
       @node scripts/catalog-scan/pr-open.mjs --dry-run
   ```
   Maintainer-facing only; reviewers run this locally to preview the
   PR body before the bot opens one.

### Tier 3 — Tests (REQUIRED)

6. **`scripts/catalog-scan/lib/yaml-edit.test.mjs`** (NEW, ~6 cases):
   - apply 1 add → yamlText gains the new entry
   - apply 1 update → existing entry's field changes; other fields
     untouched; comments preserved
   - apply 0 ops → input === output (idempotent)
   - apply update for non-existent entry → throws OR records error
     in changeset (pick + document; throwing is simpler)
   - apply 2 updates to same entry → both fields change
   - alphabetical-insertion order for adds → yamlText shows new entry
     in sorted position

7. **`scripts/catalog-scan/lib/signals.test.mjs`** (NEW, ~6 cases):
   - scorecardForRepo happy path — stubbed http returns score JSON →
     returns `{score, updated}`
   - scorecardForRepo 404 → returns `'unknown'`
   - scorecardForRepo non-github URL → returns `'unknown'` (don't even
     try the API)
   - chartIndexResolves happy → `'ok'`
   - chartIndexResolves chart missing → `'missing'`
   - chartIndexResolves oci:// → `'oci-not-checked'`

8. **`scripts/catalog-scan/pr-open.test.mjs`** (NEW, ~5 cases):
   - dry-run mode: synthetic changeset → markdown body printed to
     stdout containing the right number of table rows + all
     required columns (action / Scorecard / license / chart-resolves
     / source).
   - empty changeset → exits 0, no body, "no proposals" log line.
   - concurrency skip: simulate `gh pr list` returning a non-empty
     array (via `child_process.execFile` stub) → exits 0, "open PR
     exists" log line.
   - branch-exists skip: simulate the branch existing locally OR
     remotely.
   - body shape: assert the PR title, label list, and `--draft` flag
     are passed correctly to `gh pr create` via spy on the gh CLI
     invocation.
   - All tests use stubbed `child_process.execFile` (so no real `git`
     or `gh` calls) and stubbed `lib/http.mjs` (so no real HTTP).
   - Use the recorded-logger pattern V123-3.2/3.3 established
     (in-memory recorder).

9. **No live GitHub workflow validation in CI.** Document the
   workflow file got `actionlint` (or `yq` syntax check) only.
   Real validation is the first cron firing after merge.

### Tier 4 — Smoke run + design doc update (RECOMMENDED)

10. **Manual smoke run** (documented in retrospective):
    - Pre-condition: a non-empty changeset on disk. Use the V123-3.3
      smoke run output: `GITHUB_TOKEN=$(gh auth token) node scripts/catalog-scan.mjs --catalog catalog/addons.yaml`
      writes `_dist/catalog-scan/changeset.json`.
    - `node scripts/catalog-scan/pr-open.mjs --dry-run` — capture the
      first ~30 lines of the markdown body in the retrospective.
    - **DO NOT actually open a PR from the smoke run** — branch +
      label name pollution. Dry-run only.

11. **Design doc update** —
    `docs/design/2026-04-20-v1.23-catalog-extensibility.md`. Add a
    short paragraph under §7.3 recording: "Resolution: draft-to-main +
    label `catalog-scan` + label `needs-review`. NEVER auto-merge per
    NFR-V123-7. Implemented in V123-3.4 (PR #<this story's PR>)."

### Out of scope — explicit non-goals

- **Reviewer runbook docs.** V123-3.5.
- **Auto-merge logic.** NFR-V123-7 forbids.
- **Schema validation in JS.** Go loader is authoritative.
- **Caching / persistence between runs.** NFR-V123-1: stateless.
  Each cron run is fully independent.
- **A separate "approved sources" allowlist.** Curators (`curated_by`
  values) are the allowlist; scanner plugins map to those tokens.
- **Slack / chat notifications.** Bot opens a PR — that's the signal.
- **Editing existing licenses / descriptions on `update` proposals.**
  Updates only touch the diff fields the scanner emitted.
- **Auto-retry the workflow on failure.** Workflow exits non-zero;
  daily cron runs again tomorrow.
- **An "I'm working" status check on existing PRs.** Concurrency guard
  is the simpler answer.

## Implementation plan

### Files

- `scripts/catalog-scan/pr-open.mjs` (NEW, ~250-350 lines).
- `scripts/catalog-scan/pr-open.test.mjs` (NEW, ~150-200 lines, 5 cases).
- `scripts/catalog-scan/lib/signals.mjs` (NEW, ~120 lines).
- `scripts/catalog-scan/lib/signals.test.mjs` (NEW, ~120 lines, 6 cases).
- `scripts/catalog-scan/lib/yaml-edit.mjs` (NEW, ~100 lines).
- `scripts/catalog-scan/lib/yaml-edit.test.mjs` (NEW, ~150 lines, 6 cases).
- `.github/workflows/catalog-scan.yml` (NEW, ~80 lines).
- `Makefile` (MODIFY) — add `catalog-scan-pr` target.
- `docs/design/2026-04-20-v1.23-catalog-extensibility.md` (MODIFY) —
  §7.3 resolution paragraph.
- `scripts/catalog-scan/plugins/README.md` (MODIFY) — append
  "Workflow integration" section pointing at the new YAML.
- `.bmad/output/implementation-artifacts/sprint-status.yaml` — flip
  V123-3-4 backlog → in-progress → review.
- `.bmad/output/implementation-artifacts/REMAINING-STORIES.md` — note.
- This brief file — retrospective sections appended.

### Quality gates (run order)

1. `cd /Users/weissmmo/projects/github-moran/sharko && npm install --prefix scripts --silent` — clean.
2. `node --check scripts/catalog-scan/pr-open.mjs` + 2 lib files — syntax OK.
3. `node --test 'scripts/catalog-scan/**/*.test.mjs' 'scripts/catalog-scan.test.mjs'`
   — existing 33 + new ~17 = ~50 tests pass.
4. **Workflow lint** — install actionlint locally OR visual review:
   `actionlint .github/workflows/catalog-scan.yml`. If actionlint is
   not available, manual check that the YAML parses and permissions
   are exactly `contents: write` + `pull-requests: write` (no more,
   no less). Document the choice.
5. **Smoke run** (REQUIRED, dry-run only):
   - First, regenerate a changeset:
     `GITHUB_TOKEN=$(gh auth token) node scripts/catalog-scan.mjs --catalog catalog/addons.yaml`
   - Then: `node scripts/catalog-scan/pr-open.mjs --dry-run`
   - Capture the first 30-50 lines of the markdown body in the
     retrospective. Verify the table has the 5 required columns.
   - **DO NOT commit changeset.json.** **DO NOT open a real PR.**
6. **Skip Go/UI gates** — story doesn't touch them.

### Anti-gold-plating reminders

- **Do NOT add the reviewer runbook.** V123-3.5.
- **Do NOT add auto-merge.** NFR-V123-7.
- **Do NOT add Slack notifications.**
- **Do NOT add CODEOWNERS edits.** Out of scope; existing CODEOWNERS
  for `catalog/` already gates merges.
- **Do NOT cache scorecard / index.yaml results across runs.**
  Stateless principle.
- **Do NOT add a label-creation step.** Repo labels are managed
  separately; the workflow just applies them. If they don't exist,
  `gh pr create --label` fails — which is a one-time human action
  to create the labels (document in the workflow YAML's top comment).
- **Do NOT extract the gh-CLI invocation to a wrapper module.**
  Direct `child_process.execFile` is fine; reviewers can refactor
  later if the surface grows.
- **Do NOT add a `--no-draft` flag.** Draft is the default for OQ §7.3.
- **Do NOT add a self-test that opens a real PR.** Dry-run smoke
  only.

## Dependencies

- **V123-3.1** done ✅ (skeleton + lib helpers).
- **V123-3.2** done ✅ (CNCF plugin).
- **V123-3.3** done ✅ (EKS plugin — first plugin producing real
  proposals).

## Gotchas

1. **`gh pr create --draft` requires the repo to support drafts.**
   Public repos support drafts; private repos may not (depending on
   GH plan). MoranWeissman/sharko is public — fine.

2. **`actions/checkout@v4` `fetch-depth: 0`** is needed because
   `git ls-remote` against a shallow clone may not see remote
   branches. Don't shallow-clone for this workflow.

3. **`GITHUB_TOKEN` from GitHub Actions** has a default
   ~5000 req/hr rate limit (matching authenticated user). Sufficient
   for a daily scan. Don't add `secrets.PERSONAL_TOKEN` unless
   absolutely necessary.

4. **`yaml.parseDocument` AST mode** preserves comments and field
   order, but inserting new top-level array items is finicky. The
   simplest reliable approach: parse → modify the JS object → use
   `doc.set('addons', ...)` to write back. Test the comment-preservation
   case explicitly — gotcha #1 of #6.

5. **Concurrency guard race condition.** Two scheduled runs (e.g.
   manual workflow_dispatch + cron firing within seconds) could both
   pass the `gh pr list` check before either opens a PR. Acceptable
   risk — duplicate PRs are reviewer-visible and the second one will
   be closed immediately. Don't engineer a distributed lock.

6. **`git push -u origin <branch>`** from inside the workflow uses
   the default `${{ secrets.GITHUB_TOKEN }}` — works for `contents:
   write` permission. No SSH key config needed.

7. **Git author identity.** Workflow runs as `github-actions[bot]` by
   default, but to keep the commit history consistent with the
   project's "Moran Weissman authors all commits" rule (CLAUDE.md),
   we explicitly set `git config user.name/user.email` in step 4. The
   PR will still show "github-actions[bot] opened this PR" but the
   commit author will be Moran. This is what the user wants.

8. **Branch name collision when running the same day twice.** If a
   manual `workflow_dispatch` ran earlier today, the cron run later
   will hit "branch exists" → exit 0. Acceptable — the earlier PR is
   live; reviewer sees one PR per day.

9. **Markdown table escaping.** Plugin entry fields can contain
   pipes (`|`) which break GitHub-flavored markdown tables. Sanitize
   with `value.replace(/\|/g, '\\|')` in the table-row builder.

10. **`gh` CLI may not be available in node_modules.** The GitHub
    Actions runner has `gh` pre-installed; locally, the user has it.
    Don't try to npm-install gh.

## Role files (MUST embed in dispatch)

- `.claude/team/devops-agent.md` — primary (workflow YAML, GitHub
  Actions, gh CLI, rate-limit hygiene).
- `.claude/team/architect.md` — secondary (the changeset → PR-body
  contract spans concerns; a clean signals.mjs / yaml-edit.mjs split
  is worth getting right since V123-4.4's reviewer sweep will
  re-read this code).

## PR plan

- **Branch:** `dev/v1.23-pr-opener` off `main` (current HEAD `f9aa20c`).
- **Commits:**
  1. `feat(scanner): pr-open.mjs + signal pre-compute + yaml-edit (V123-3.4)`
     — `pr-open.mjs` + `lib/signals.mjs` + `lib/yaml-edit.mjs`.
  2. `test(scanner): pr-open + signals + yaml-edit unit tests (V123-3.4)`
     — three test files.
  3. `feat(ci): catalog-scan workflow YAML + Makefile target (V123-3.4)`
     — `.github/workflows/catalog-scan.yml` + `Makefile` edit +
     plugins/README append.
  4. `docs(design): record OQ §7.3 resolution (V123-3.4)`
     — design doc paragraph.
  5. `chore(bmad): mark V123-3.4 for review (Epic V123-3 4/5)`
     — sprint-status + REMAINING-STORIES + this brief.
- **PR body** must call out:
  - "Epic V123-3: 4 of 5 in review."
  - "OQ §7.3 resolved: draft-to-main + `catalog-scan` label + NEVER
    auto-merge (NFR-V123-7)."
  - "Smoke run output (dry-run): `<paste first 30 lines of markdown
    body>`."
  - "Workflow can NOT be validated end-to-end without a real cron
    firing. After merge, trigger once via `gh workflow run 'Catalog
    Scan'` to confirm; or wait for the daily 04:00 UTC cron."
  - "Required one-time setup before first run: ensure `catalog-scan`
    + `needs-review` labels exist in the repo (otherwise
    `gh pr create --label` fails). Create via:
    `gh label create catalog-scan --color BFD4F2` +
    `gh label create needs-review --color FBCA04`."
- **NO TAG.** v1.23.0 cut belongs to V123-4.5.

## Next story

**V123-3.5** — Reviewer runbook. Documents how to read these PRs,
when to close-without-merge vs edit-and-merge, how to add a new
scanner plugin. Closes Epic V123-3.

## Tasks completed

1. **Tier 1 — PR-opener + helpers (commit 1).** Wrote three new
   modules:
   - `scripts/catalog-scan/pr-open.mjs` (~340 lines): CLI parser,
     concurrency guards via `gh pr list --label catalog-scan` +
     `git rev-parse` / `git ls-remote`, signal pre-compute via
     `lib/signals.mjs`, YAML edit via `lib/yaml-edit.mjs`, branch
     `catalog-scan/<UTC YYYY-MM-DD>` (overridable via `--branch`),
     commit + push + `gh pr create --draft --base main` with both
     labels. Markdown body with the 5 required columns (action /
     name / Scorecard / license / chart-resolves / source). Pipe-
     escaping. Body also includes a per-update diff table + a
     reviewer checklist. Top-level `run()` accepts `deps` (logger,
     execFile, fetcher, readFile, writeFile) for full testability.
     Default `execFile = util.promisify(child_process.execFile)`.
     `--dry-run` prints body to stdout and skips git/gh entirely
     (also skips concurrency guards because there's nothing to
     conflict with).
   - `scripts/catalog-scan/lib/signals.mjs` (~180 lines):
     `scorecardForRepo(url, ctx)` calls
     `https://api.securityscorecards.dev/projects/github.com/<owner>/<repo>`
     and returns `{score, updated}` or `'unknown'`; non-github URLs
     short-circuit without an HTTP call. `chartIndexResolves(repoUrl,
     chartName, ctx)` fetches `<repo>/index.yaml`, parses, returns
     `'ok' | 'missing' | 'oci-not-checked' | 'unknown'`; supports a
     shared `_cache` Map so multi-add proposals against the same
     Helm repo don't hammer the URL. `licenseFromChart(chartIndex,
     chartName)` classifies against the schema allow-list (Apache-
     2.0 / BSD-3-Clause / MIT / MPL-2.0). All three are defensive —
     network blip / 404 returns the sentinel, never throws.
   - `scripts/catalog-scan/lib/yaml-edit.mjs` (~180 lines):
     `applyChangeset(yamlText, changeset)` uses `yaml.parseDocument`
     AST mode so existing entries' comments + flow style + field
     order survive untouched. Adds insert at the alphabetical
     position by `name` (stable diffs). Updates apply each
     `diff[field].to` value. Throws on update for a non-existent
     entry name. Idempotent on empty changeset (returns input
     verbatim). Strips scanner-internal underscore-prefixed fields
     (`_eks_blueprints_path`, etc.) before insert. Re-validates the
     post-edit document has `addons:` array + each entry has `name`.

2. **Tier 3 — unit tests (commit 2).** Added 26 new test cases.
   - `lib/yaml-edit.test.mjs` (7 cases): all 6 brief cases (1 add,
     1 update with comment preservation, idempotent no-op, throw on
     non-existent update target, 2 updates same entry, alphabetical
     insert) plus a 7th case asserting scanner-internal underscore
     fields are stripped on add.
   - `lib/signals.test.mjs` (12 cases): 4 scorecard cases (happy /
     404 / non-github short-circuit / error→warn-log), 4
     chartIndexResolves cases (happy / missing / oci-not-checked /
     `_cache` shared across calls), 4 licenseFromChart cases (allow-
     list ok / non-allow-list flagged / absent unknown / artifacthub
     annotation precedence over plain license field).
   - `pr-open.test.mjs` (7 cases): parseArgs (defaults + flag-set),
     dry-run body shape (5-column header + correct add/update row
     counts), empty changeset path, both concurrency-skip paths
     (open PR exists, branch exists locally), full flow asserting
     `gh pr create` args (`--draft`, `--base main`, `--head <branch>`,
     both `--label` flags, title format), and the missing-changeset-
     file safety path (ENOENT → exit 0 + "nothing to do" log).
     Stubbed `child_process.execFile` (no real git/gh) and an offline
     fetcher (no real HTTP). Recorded-logger pattern duplicated
     inline per V123-3.2 / V123-3.3 convention.
   Total scanner suite: 33 → 59 passing (~7.7s on local).

3. **Tier 2 — workflow YAML + Makefile (commit 3).**
   - `.github/workflows/catalog-scan.yml` (89 lines): triggers
     `schedule: '0 4 * * *'` + `workflow_dispatch`. Permissions
     EXACTLY `contents: write` + `pull-requests: write` per NFR-V123-7.
     Concurrency `group: catalog-scan, cancel-in-progress: false`.
     Steps: actions/checkout@v4 (`fetch-depth: 0`), actions/setup-node@v4
     (Node 20, npm cache keyed on `scripts/package-lock.json`), `npm
     ci --prefix scripts`, git config `Moran Weissman <moran.weissman@
     gmail.com>`, scanner with `GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN
     }}`, conditional `pr-open.mjs` only when `_dist/catalog-scan/
     changeset.json` exists. Top comment block documents the one-
     time label-creation step + first-run validation note.
   - `Makefile`: added `.PHONY: catalog-scan-pr` after the existing
     `catalog-scan` target. Runs `pr-open.mjs --dry-run` for local
     PR-body preview.
   - `scripts/catalog-scan/plugins/README.md`: appended a "Workflow
     integration" section describing the 2-step scanner → pr-open
     flow + the `make catalog-scan-pr` local preview command.

4. **Tier 4 — design doc (commit 4).** Updated
   `docs/design/2026-04-20-v1.23-catalog-extensibility.md` §7.3 with
   the resolution paragraph: draft-to-main + label `catalog-scan` +
   label `needs-review` + NEVER auto-merge per NFR-V123-7. Records
   why the `catalog-updates` long-lived branch alternative was
   rejected and notes the concurrency guard. References the
   implementation files + V123-3.5 for the runbook.

5. **BMAD tracking (commit 5).** Flipped V123-3-4 in
   `sprint-status.yaml` from `in-progress` → `review`; refreshed
   `last_updated` line + comment header. Moved V123-3.4 from
   "Backlog" to "In review" in `REMAINING-STORIES.md`; updated count
   to "5 stories, 3 done, 1 in review" + V123-3.3 to "Done". Filled
   the four retrospective sections of this file.

## Files touched

- `scripts/catalog-scan/pr-open.mjs` (NEW, ~340 lines).
- `scripts/catalog-scan/pr-open.test.mjs` (NEW, 7 cases).
- `scripts/catalog-scan/lib/signals.mjs` (NEW, ~180 lines).
- `scripts/catalog-scan/lib/signals.test.mjs` (NEW, 12 cases).
- `scripts/catalog-scan/lib/yaml-edit.mjs` (NEW, ~180 lines).
- `scripts/catalog-scan/lib/yaml-edit.test.mjs` (NEW, 7 cases).
- `.github/workflows/catalog-scan.yml` (NEW, 89 lines).
- `Makefile` (MODIFIED, +5 / -1 lines — `catalog-scan-pr` target).
- `scripts/catalog-scan/plugins/README.md` (MODIFIED, +33 / -0
  lines — Workflow integration section).
- `docs/design/2026-04-20-v1.23-catalog-extensibility.md` (MODIFIED,
  +2 / -1 lines — OQ §7.3 resolution paragraph).
- `.bmad/output/implementation-artifacts/sprint-status.yaml` —
  V123-3-4 flipped `in-progress` → `review`; comment header +
  `last_updated` line refreshed.
- `.bmad/output/implementation-artifacts/REMAINING-STORIES.md` —
  V123-3.3 moved from "In review" to "Done"; V123-3.4 moved from
  "Backlog" to "In review" with smoke-run summary.
- `.bmad/output/implementation-artifacts/V123-3-4-...md` (this file)
  — status flipped to `review`, retrospective sections filled.

No Go files touched. No UI files touched. No swagger regeneration
(no API surface added). No new runtime dependencies (`yaml@^2.8.2`
already pinned by V123-3.1 in `scripts/package.json`). No
`_dist/catalog-scan/changeset.json` committed (used during smoke
run, removed before commit 5 per the brief's anti-gold-plating
rules).

## Tests

Quality gates run in the brief's documented order:

1. `cd /Users/weissmmo/projects/github-moran/sharko && npm install
   --prefix scripts --silent` — clean, no new deps, lock unchanged.
2. `node --check` on the three new source files
   (`scripts/catalog-scan/pr-open.mjs`,
   `scripts/catalog-scan/lib/signals.mjs`,
   `scripts/catalog-scan/lib/yaml-edit.mjs`) — all pass.
3. `node --test 'scripts/catalog-scan/**/*.test.mjs'
   'scripts/catalog-scan.test.mjs'` — **59 / 59 pass** (~7.7s):
   33 pre-existing (V123-3.1 + V123-3.2 + V123-3.3) + 7 yaml-edit
   + 12 signals + 7 pr-open = 59. No skips, no flakes.
4. **Workflow lint method:** `actionlint` was NOT installed locally
   (`which actionlint` → not found). Fallback per the brief's Tier 3
   #9: parsed the workflow with `yaml.parse` to confirm well-formed
   YAML, then visual review of structure: `permissions` is exactly
   `contents: write` + `pull-requests: write`, `actions: write` is
   absent, no `automerge` config, `fetch-depth: 0` on checkout,
   Node 20 with npm cache, `GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}`
   set on both `run` steps that need it.
5. **Smoke run (REQUIRED, dry-run only).**
   - Step 1 — `GITHUB_TOKEN=$(gh auth token) node scripts/catalog-
     scan.mjs --catalog catalog/addons.yaml`. Result: scan completed
     in ~9s; EKS plugin enumerated 64 dirs, fetched 30 entries,
     34 skipped for missing chart constants. Output: 19 adds + 11
     updates against the 45-entry catalog. cncf-landscape returned
     0 (sparse Helm metadata in upstream landscape.yml — same
     behavior V123-3.2 documented).
   - Step 2 — `node scripts/catalog-scan/pr-open.mjs --dry-run`.
     Result (exit 0): markdown body printed to stdout, all 5
     columns rendered correctly. Per-add chart-resolves classification
     spread: 16 `ok` + 1 `missing` (apache-airflow's chart name
     mismatch in upstream Helm repo) + 1 `oci-not-checked` (kro on
     `oci://`) + 1 `unknown` (grafana-operator's repo URL fetch
     failed). License values pulled correctly from index.yaml when
     present (`Apache-2.0` for prometheus-node-exporter, backstage,
     cert-manager, kube-state-metrics; `unknown` elsewhere).
     Scorecard column was `unknown` for every row because EKS plugin
     entries don't carry a `source_url` — falls back to the Helm repo
     URL which `parseGithubSlug` correctly rejects (it's not on
     github.com). One stderr warn line fired for grafana-operator
     where the repo URL contained a literal `<TODO: chart repo>`
     placeholder; this is the defensive-fallback path working as
     designed (caught the error, emitted warn, returned `'unknown'`).
   - **`_dist/catalog-scan/changeset.json` was NOT committed** —
     deleted between smoke run and commit 5 per anti-gold-plating
     rules. **No real PR opened.**

   **First 30 lines of the dry-run markdown body (captured for the
   PR description):**

   ```
   # Catalog scan — automated proposal

   This PR was opened by the **catalog-scan bot** (V123-3.4 of the v1.23 catalog-extensibility epic).

   - **Generated:** `2026-04-27T12:23:35.919Z`
   - **Sources:** `aws-eks-blueprints`
   - **Adds:** 19 · **Updates:** 11

   Per [Open Question §7.3 resolution](../docs/design/2026-04-20-v1.23-catalog-extensibility.md#7-open-questions-to-resolve-during-v123-execution): this is a **draft PR**. Labels `catalog-scan` + `needs-review` are applied so CODEOWNERS treat it distinctly. **NEVER auto-merged** per NFR-V123-7. Reviewer is expected to edit/close — see the runbook (V123-3.5).

   ## Scanner runs

   | Plugin | Fetched | Error |
   |---|---|---|
   | aws-eks-blueprints | 30 |  |
   | cncf-landscape | 0 |  |

   ## Proposals

   | Action | Name | Scorecard | License | Chart resolves | Source |
   |---|---|---|---|---|---|
   | add | `apache-airflow` | unknown | unknown | missing | aws-eks-blueprints |
   | add | `appmesh` | unknown | unknown | ok | aws-eks-blueprints |
   | add | `aws-for-fluent-bit` | unknown | unknown | ok | aws-eks-blueprints |
   | add | `aws-node-termination-handler` | unknown | unknown | ok | aws-eks-blueprints |
   | add | `aws-privateca-issuer` | unknown | unknown | ok | aws-eks-blueprints |
   | add | `calico-operator` | unknown | unknown | ok | aws-eks-blueprints |
   | add | `calico` | unknown | unknown | ok | aws-eks-blueprints |
   | add | `container-insights` | unknown | unknown | ok | aws-eks-blueprints |
   ```
6. Skipped: Go/UI gates (story touches neither). No swagger regen
   (no API surface added).

## Decisions

1. **AST-mode YAML editing via `yaml.parseDocument` confirmed the
   right call.** Probed against the real `catalog/addons.yaml` early
   in the implementation: the curated file uses inline flow-style
   for `maintainers: [jetstack]` and `curated_by: [...]`,
   per-section comments (`# -- Security --`), and inline trailing
   comments on `repo:` lines. AST mode preserved every one of these
   on entries the changeset DIDN'T touch, while letting us insert
   new entries + apply `diff.field.to` values on entries it did
   touch. Plain-object round-trip via `yaml.parse` + `yaml.stringify`
   would have erased all of this and produced a churn-heavy
   diff that reviewers couldn't read at a glance.

2. **Insert-alphabetically (not append-at-end) on adds.** Two
   reasons: the curated catalog is loosely category-grouped
   (`# -- Security --` etc.) and an alphabetical-by-name insert
   lands the new entry near related entries, AND a stable insert
   position means the bot's diffs are smaller and stable across
   runs (reviewers can scan a single new `- name: ...` block, not
   a churn at the bottom).

3. **`source_url` over `repo` for Scorecard input.** Scorecard is
   github-only, but Helm `repo:` URLs are mostly
   `https://charts.<vendor>.com` shapes that are not github.com and
   would fail `parseGithubSlug`. Plugin-emitted `source_url` (which
   the EKS plugin doesn't currently set, but the CNCF plugin does
   via `extra.repo_url`) is the better signal. The PR-opener tries
   `entry.source_url ?? entry.repo` — when neither is github.com,
   Scorecard returns `'unknown'` cleanly. Smoke-run output confirms
   this works as designed: every EKS-Blueprints add showed
   `Scorecard: unknown` (correct — no source_url emitted) without
   an HTTP call (verified via the no-call assertion in the
   `non-github URL` unit test).

4. **Concurrency guards skipped in `--dry-run` mode.** The brief
   describes the guards generically; in dry-run there's no PR or
   branch to conflict with, AND CI contexts where `gh` isn't
   authenticated would always trip the guard for the wrong reason
   (auth failure looks the same as "no PR open"). Skipping in
   `--dry-run` keeps the local preview path (`make catalog-scan-pr`)
   working for any developer who hasn't `gh auth login`'d.

5. **Defensive `licenseFromChart` annotation precedence.** The Helm
   chart-index spec lets either the top-level `license` field or the
   `annotations['artifacthub.io/license']` annotation carry the SPDX
   value. The actual prometheus-node-exporter index.yaml uses the
   annotation, NOT the top-level field — confirmed by the smoke run
   output. Prefer the annotation if present (it's the more recent
   convention).

6. **No `lib/github.mjs` extraction.** Per the V123-3.3 retrospective
   decision #2 — the gh CLI invocation is direct in `pr-open.mjs`
   and the GitHub HTTP client lives in the EKS plugin only.
   Reviewers can extract a shared wrapper if a third use site lands.

7. **No retries / state recording for "update-on-non-existent-entry"
   path; throw instead.** Brief Tier 3 #6 noted this as a choice;
   throwing was selected for the simpler semantics. The diff helper
   guarantees an update target exists in the current catalog (the
   current-by-name map is the source of truth). If it ever regresses
   the throw surfaces it in the workflow log, and the cron run fails
   loudly rather than emitting a half-applied change.

---
story_key: V123-3-3-aws-eks-blueprints-scanner-plugin
epic: V123-3 (Trusted-source scanning bot)
status: review
effort: M
dispatched: 2026-04-27
depends_on: V123-3.1
---

# Story V123-3.3 — AWS EKS Blueprints scanner plugin

## Brief (from epics-v1.23.md §V123-3.3)

As the **scanning bot**, I want a plugin enumerating `lib/addons/*` in
`aws-quickstart/cdk-eks-blueprints`, so that AWS-curated addons are kept
current.

## Acceptance Criteria

**Given** the plugin runs
**When** it queries the GitHub API for `aws-quickstart/cdk-eks-blueprints`
contents under `lib/addons/`
**Then** it enumerates directories and extracts addon metadata from each
(name, chart, repo).

**Given** an addon is new vs the catalog
**Then** the plugin proposes an add.

**Given** the plugin respects `GITHUB_TOKEN`
**Then** API calls authenticate; rate-limit headroom is logged.

**Note on scope:** AC requires only `add` proposals — NOT updates. The
plugin emits adds and the diff helper will surface them; existing
catalog entries with `aws-eks-blueprints` in `curated_by` (there are
18 today) won't generate update churn. This is narrower than V123-3.2.

## Existing context

- V123-3.1 + V123-3.2 are merged. The `cncf-landscape.mjs` plugin
  (~349 lines) is the closest analogue — same shape, different upstream.
  Mirror its file layout: top JSDoc, env-var URL override, slug
  helper, defensive parse-and-skip, ctx.http for network.
- `ctx.http` does NOT support `file://` URLs (V123-3.2 hit this and
  stubbed `ctx.http` directly in unit tests). Same approach here:
  unit tests stub the http function in-memory; integration test in
  `scripts/catalog-scan.test.mjs` uses an env-var URL pointing at a
  fixture (loaded via `fetchAsText` helper inside the plugin, same
  `file://` fallback `cncf-landscape.mjs` has).
- Sharko catalog has 18 entries with `aws-eks-blueprints` in
  `curated_by`. Smoke run should produce a meaningful but bounded list
  of new adds (likely 5-15 — Blueprints' addon set is bigger than
  what we curate; not all overlap).
- `curated_by` token: `aws-eks-blueprints` is already a valid enum
  (per `catalog/schema.json` line 102).
- Maintainer / description / license / default_namespace policy:
  same TODO-marker convention V123-3.2 established —
  `["<TODO: derive from chart repo>"]` for maintainers,
  `"<TODO: human description>"` for description, `"unknown"` for
  license, `"<slug>-system"` for default_namespace. These are
  schema-valid + CI-flagged for human review.

## GitHub API specifics

- **Repo:** `aws-quickstart/cdk-eks-blueprints` (TypeScript / CDK).
- **Confirmed via curl:** the repo exists; `lib/addons/` exists;
  GitHub's Contents API redirects from name-based to ID-based URL
  (HTTP 301, follow normally).
- **Endpoints used:**
  - `GET /repos/aws-quickstart/cdk-eks-blueprints/contents/lib/addons` —
    returns `[{name, type, path, ...}, ...]`. Filter `type === "dir"`
    to enumerate addon directories.
  - `GET /repos/aws-quickstart/cdk-eks-blueprints/contents/lib/addons/<addon>` —
    returns the directory's contents as another array. Look for
    files like `index.ts`, `<addon>-addon.ts`, or any `.ts` whose
    name matches the addon. Pick the first `.ts` (alphabetically) as
    the candidate; the source URL field on the response is the raw
    file URL.
  - `GET <download_url>` — fetches raw `.ts` file content. Use the
    `download_url` field returned by the contents API directly (it
    already points to `raw.githubusercontent.com`).
- **Auth:** `Authorization: Bearer ${GITHUB_TOKEN}` if env var is
  present. Without auth: 60 req/hr; with auth: 5000 req/hr.
- **Rate limit:** log `X-RateLimit-Remaining` after each Contents API
  call. Treat sub-10 remaining as a WARN log (operator should
  configure GITHUB_TOKEN). Do NOT abort the scan on rate-limit
  exhaustion — let the Bad Status Code from a 403 propagate via
  ctx.http's retry, then surface as plugin error in scanner_runs.

## Metadata extraction policy

CDK addons declare Helm metadata as TS constants in their `.ts` files:

```typescript
const HELM_CHART_NAME = 'aws-cloudwatch-metrics';
const HELM_CHART_REPO = 'https://aws.github.io/eks-charts';
const HELM_CHART_VERSION = '0.0.10';
const HELM_CHART_NAMESPACE = 'amazon-cloudwatch';
```

But naming varies across addons. Defensive extractor:
- Try regex patterns in priority order, looking for first match per
  field:
  - `chart`: `/(?:helm[_]?chart[_]?name|chartName)\s*[:=]\s*['"`]([^'"`]+)['"`]/i`
  - `repo`:  `/(?:helm[_]?chart[_]?repo|chartRepo)\s*[:=]\s*['"`]([^'"`]+)['"`]/i`
  - `version`: `/(?:helm[_]?chart[_]?version|chartVersion)\s*[:=]\s*['"`]([^'"`]+)['"`]/i`
  - `namespace`: `/(?:helm[_]?chart[_]?namespace|chartNamespace|namespace)\s*[:=]\s*['"`]([^'"`]+)['"`]/i`
- If `chart` AND `repo` are missing → log `info` and skip the addon
  (matches V123-3.2's "skip if no Helm chart reference" rule).
- Document in plugin comments: this is a heuristic; reviewers verify
  in the bot PR.

## Scope (Tier-ordered)

### Tier 1 — Plugin implementation (REQUIRED)

1. **`scripts/catalog-scan/plugins/aws-eks-blueprints.mjs`** (NEW,
   ~250-300 lines).
   - `export const name = 'aws-eks-blueprints'`
   - `export async function fetch(ctx)` with the pipeline:
     1. Read `process.env.SHARKO_EKS_BLUEPRINTS_API_BASE` (default
        `https://api.github.com/repos/aws-quickstart/cdk-eks-blueprints/contents`)
        — env override lets tests point at a fixture-server URL.
     2. Read `process.env.GITHUB_TOKEN` (optional). Build a
        `headersFor(extraHeaders)` helper that always sets
        `User-Agent: sharko-catalog-scan/1.0` and `Accept: application/vnd.github+json`,
        plus `Authorization: Bearer ${GITHUB_TOKEN}` when present.
     3. Wrap `ctx.http` with a thin GitHub-aware wrapper that:
        - Adds the standard headers.
        - Reads `X-RateLimit-Remaining` from each response and logs
          via `ctx.logger.info` when ≥ 10, `ctx.logger.warn` when
          < 10.
        - Returns the parsed JSON body for Contents-API calls; raw
          text for `download_url` fetches.
     4. List dirs under `lib/addons/`. Filter `type === "dir"`.
     5. For each dir (sequential — keeps rate-limit behaviour
        predictable; don't parallelize):
        - List the dir contents.
        - Pick the first `.ts` file alphabetically (excluding
          `index.test.ts`, `*.spec.ts`, `*.types.ts`).
        - Fetch the raw content via the file's `download_url`.
        - Run the regex extractors. If `chart` AND `repo` are
          missing, log `info` and skip.
        - Build a normalized entry (same TODO-marker conventions
          from V123-3.2):
          ```js
          {
            name: <slugify dir.name>,
            chart: <extracted chart>,
            repo: <extracted repo>,
            version: <extracted version | undefined>,
            category: <inferred from name OR 'developer-tools' as fallback>,
            curated_by: ['aws-eks-blueprints'],
            default_namespace: <extracted namespace | <slug>-system>,
            description: '<TODO: human description>',
            license: 'unknown',
            maintainers: ['<TODO: derive from chart repo>'],
            _eks_blueprints_path: dir.path,
            _eks_blueprints_source: <raw .ts URL>,
          }
          ```
     6. Returns the array.
   - Per-addon errors must NOT throw — log `warn` and skip.
   - Network errors (e.g. 403 rate-limit on the dir-list call)
     propagate; scanner script logs in `scanner_runs[].error`.
   - **Optional category inference:** simple keyword map inside the
     plugin (e.g. addon names containing "monitoring/observability/
     metrics/logs/grafana/prometheus" → `observability`; "ingress/
     loadbalancer/networking" → `networking`; "secret/cert/auth/
     security" → `security`; "csi/storage" → `storage`; etc.).
     Fallback: `developer-tools` (close-enough; reviewers refine).
     Keep the keyword map small (~20 entries) — same anti-gold-plate
     stance as V123-3.2's category map.

2. **Slug normalizer** — reuse the helper. If V123-3.2 inlined it in
   its plugin, copy the function into `aws-eks-blueprints.mjs` (still
   < 30 lines duplicated); ONLY extract to `lib/slug.mjs` if you
   touch a third use site (you won't here). Brief explicitly allows
   inline duplication to keep diffs small.

### Tier 2 — Tests (REQUIRED)

3. **`scripts/catalog-scan/__tests__/fixtures/eks-blueprints/`** (NEW
   directory) — fixture set mimicking GitHub Contents API responses.
   Layout:
   - `eks-blueprints/contents-lib-addons.json` — top-level dir
     listing, ~5 entries:
     ```json
     [
       {"name": "aws-load-balancer-controller", "type": "dir", "path": "lib/addons/aws-load-balancer-controller", ...},
       {"name": "cert-manager",                 "type": "dir", "path": "lib/addons/cert-manager", ...},
       {"name": "external-dns",                 "type": "dir", "path": "lib/addons/external-dns", ...},
       {"name": "secrets-store-csi-driver",     "type": "dir", "path": "lib/addons/secrets-store-csi-driver", ...},
       {"name": "fancy-new-addon",              "type": "dir", "path": "lib/addons/fancy-new-addon", ...},
       {"name": "broken-addon",                 "type": "dir", "path": "lib/addons/broken-addon", ...},
       {"name": "index.ts",                     "type": "file", "path": "lib/addons/index.ts", ...}
     ]
     ```
     Mix of: 3 already-curated names (cert-manager, external-dns,
     aws-load-balancer-controller — verify against
     `catalog/addons.yaml`), 1 new add (`fancy-new-addon`), 1 broken
     (no chart constants), 1 file (filtered out by `type==="dir"`).
   - One JSON file per addon dir (5 dirs × 1 = 5 files):
     `eks-blueprints/contents-<addon>.json` — addon-dir listing
     with one entry: `[{name: "<addon>-addon.ts", type: "file",
     download_url: "<test-server-url>/raw/<addon>.ts", ...}]`.
   - One TS file per addon (5 .ts files): mock `lib/addons/<addon>.ts`
     content with the HELM_CHART_* constants (or missing them for
     `broken-addon`).

4. **`scripts/catalog-scan/plugins/aws-eks-blueprints.test.mjs`**
   (NEW, ~6-8 cases):
   - **happy path:** stubbed ctx.http returns the fixture chain →
     plugin returns 5 normalized entries (skips broken + filters `index.ts`).
   - **maturity / type filter:** asserts `index.ts` (type=file) is filtered out.
   - **broken extraction:** asserts `broken-addon` is skipped + a warn log emitted.
   - **slug normalization:** an addon dir named "Aws-Load-Balancer-Controller!"
     normalizes to `aws-load-balancer-controller`.
   - **GITHUB_TOKEN propagates:** when `process.env.GITHUB_TOKEN` is
     set, the wrapped http function passes `Authorization: Bearer ...`
     in headers; assert via spy on the stubbed http function.
   - **rate-limit logging:** when the stubbed response's
     `headers.get('x-ratelimit-remaining')` returns "5", a `warn` log
     line is emitted; "5000" → `info`. Use the recorded-logger
     pattern V123-3.2 established (`logger.records.find(r => r.level === 'warn')`).
   - **network error propagation:** stubbed http throws on the
     dir-list call → plugin throws (scanner script's error isolation
     handles it).
   - Use `t.Cleanup` for any test-scoped state (e.g. `delete process.env.GITHUB_TOKEN`).

5. **Integration test extension** — add ONE new sub-case to
   `scripts/catalog-scan.test.mjs`:
   - `eks-blueprints integration` — start a tiny `http.createServer`
     that serves the fixture set under predictable URLs; set
     `SHARKO_EKS_BLUEPRINTS_API_BASE=<server URL>`; spawn the
     scanner script; assert the changeset JSON has the expected
     count of adds against the real `catalog/addons.yaml`.
   - Use `t.Cleanup(server.close)`.
   - **Note:** V123-3.2 used a `file://` URL via `fetchAsText`'s
     fallback. The EKS plugin needs to make MULTIPLE calls (dir-list
     + per-addon dir-list + per-addon raw-fetch) — `file://` won't
     handle the API-shape requirement. Use `http.createServer`
     instead. Document this in the test file's top comment.

### Tier 3 — Smoke run + docs (RECOMMENDED)

6. **`scripts/catalog-scan/plugins/README.md`** (MODIFY) — append
   "Real plugin example: `aws-eks-blueprints.mjs`" section after the
   V123-3.2 section. 5-line summary + GITHUB_TOKEN env var note + AC link.

7. **Manual smoke run** documented in retrospective:
   - `GITHUB_TOKEN=<personal-token> node scripts/catalog-scan.mjs --dry-run --catalog catalog/addons.yaml`
     → hits live GitHub API. Capture top-line counts (adds count,
     errors). 60 req/hr unauth might exhaust against the live repo
     (it has > 60 addon dirs); a token raises this to 5000.
   - **Test without GITHUB_TOKEN as well** to confirm the rate-limit
     warning path fires. Capture the WARN log line in the
     retrospective.
   - **DO NOT commit the smoke-run JSON.** Counts only.

### Out of scope — explicit non-goals

- **Update proposals.** AC says "proposes an add" — only adds. The
  plugin emits new entries; the diff helper handles the diff. Don't
  attempt update detection (chart-version drift, etc.). V123-3.4 may
  later add this if scope permits, but not here.
- **GitHub Actions workflow / nightly cron / PR-opener.** V123-3.4.
- **Reviewer runbook docs.** V123-3.5.
- **Trust-score computation.** V123-3.4 baseline applies.
- **`aws-ia/terraform-aws-eks-blueprints-addons` (Terraform repo).**
  Roadmap memory mentioned it but the epic AC is authoritative — use
  the CDK repo only. Document in Decisions section that the
  Terraform repo could be a future plugin (no story exists for it).
- **Parallel HTTP fetches.** Sequential per addon dir keeps rate-limit
  semantics predictable + simplifies error handling. Don't engineer a
  pool.
- **Catalog schema changes.** None needed.
- **Validation of plugin output against schema.** Done by humans at
  PR-review time + Go loader at merge time.

## Implementation plan

### Files

- `scripts/catalog-scan/plugins/aws-eks-blueprints.mjs` (NEW, ~250-300 lines).
- `scripts/catalog-scan/plugins/aws-eks-blueprints.test.mjs` (NEW, ~250 lines, 6-8 cases).
- `scripts/catalog-scan/__tests__/fixtures/eks-blueprints/` (NEW directory).
  - `contents-lib-addons.json` (~30 lines).
  - 5 × `contents-<addon>.json` (~10 lines each).
  - 5 × `<addon>.ts` (~15 lines each, mock TS source).
- `scripts/catalog-scan.test.mjs` (MODIFY) — add 1 integration case
  (~50 lines including http.createServer setup).
- `scripts/catalog-scan/plugins/README.md` (MODIFY) — append section.
- `.bmad/output/implementation-artifacts/sprint-status.yaml` — flip
  V123-3-3 backlog → in-progress → review.
- `.bmad/output/implementation-artifacts/REMAINING-STORIES.md` — note
  V123-3.3 in flight.
- `.bmad/output/implementation-artifacts/V123-3-3-...md` (this file) —
  retrospective sections appended.

### Quality gates (run order)

1. `cd /Users/weissmmo/projects/github-moran/sharko && npm install --prefix scripts --silent` — clean.
2. `node --check scripts/catalog-scan/plugins/aws-eks-blueprints.mjs` — syntax OK.
3. `node --test 'scripts/catalog-scan/**/*.test.mjs' 'scripts/catalog-scan.test.mjs'` —
   all pass. Existing 21 + ~7-9 new = ~28-30 tests.
4. **Smoke run (required, two passes):**
   - **Pass 1 unauth:** `node scripts/catalog-scan.mjs --dry-run --catalog catalog/addons.yaml`
     — confirm WARN log on rate limit OR partial scan completes; record outcome.
   - **Pass 2 with token (if available):**
     `GITHUB_TOKEN=$(gh auth token) node scripts/catalog-scan.mjs --dry-run --catalog catalog/addons.yaml`
     — full scan; record adds count + errors. (`gh auth token` works
     since `gh` is already in use for PR ops.)
   - **DO NOT commit the JSON output.** Counts go in retrospective.

   If Pass 2 errors → STOP and report. Pass 1 hitting rate-limit is
   expected and confirms the warning code path.

5. **Skip Go/UI gates** — story doesn't touch them.

### Anti-gold-plating reminders

- **Do NOT add update-detection logic.** AC is adds-only.
- **Do NOT parallelize HTTP fetches.** Sequential is correct here.
- **Do NOT add a TS parser.** Regex extractors are sufficient.
- **Do NOT commit smoke-run JSON.**
- **Do NOT add the Terraform repo plugin.** Out of scope.
- **Do NOT add a separate `lib/github.mjs` helper.** GitHub-specific
  wrapper lives inline in the plugin; reviewers can extract later if
  V123-3.4 needs the same wrapper.
- **Do NOT compute trust_score.** V123-3.4 applies a baseline.
- **Do NOT add `description`/`license`/`maintainers` derivation.**
  TODO markers per V123-3.2 convention.

## Dependencies

- **V123-3.1** — done ✅ (skeleton merged at PR #296 / `3eb97d0`).
- **V123-3.2** — done ✅ (cncf-landscape plugin merged at PR #299 /
  `99341ef`) — provides the pattern + reusable conventions.

## Gotchas

1. **GitHub Contents API max pagination.** A directory with > 1000
   entries paginates. `lib/addons/` has fewer than 100 today, but
   defensive code: if response array length is exactly 1000, log a
   warn that pagination might be needed (don't implement it now —
   scope creep).

2. **301 redirects.** `aws-quickstart/cdk-eks-blueprints` returned 301
   to a permanent `/repositories/<id>/...` URL on test (curl
   confirmed). Node's built-in `fetch` follows redirects by default;
   no special handling needed. Just don't override `redirect: 'manual'`.

3. **Rate limit at 60 req/hr unauth, 5000 req/hr auth.** With ~70-80
   addon dirs in the live `lib/addons/` + 1 dir-list + 1 file-fetch
   per dir = ~150 calls per scan. Unauth → exhausts in 1 minute.
   With token → fine. Document this in the plugin's top comment so
   operators know they NEED `GITHUB_TOKEN` for the nightly bot.

4. **Test-server URL stability.** `http.createServer` listens on a
   random port; the test must read the actual port (`server.address().port`)
   and template it into all the fixture URLs. The dir-listing JSON
   needs to be served with the correct `download_url` pointing at
   `http://localhost:<port>/raw/<addon>.ts`. The simplest approach:
   the fixture JSONs use a placeholder like `${BASE_URL}` and the
   test substitutes at startup.

5. **`download_url` field is HTTPS to raw.githubusercontent.com.** The
   plugin should follow it as-is — it returns plain text. The test
   fixture's `download_url` should point at the local httptest server
   instead.

6. **Empty `lib/addons/` response.** Defensive: if the dir-list
   returns `[]`, log `info` and return `[]` from the plugin.

7. **Node ESM dynamic `process.env` reads.** The plugin reads env vars
   at the top of `fetch(ctx)` (not at module load) — so unit tests
   can `process.env.GITHUB_TOKEN = '...'` before calling `fetch()`
   and the value is picked up. Don't cache them at module scope.

8. **Slug collision risk.** Two addons with names that slugify the
   same (e.g. "Foo Bar" and "foo-bar") emit duplicates — same
   behavior as V123-3.2 — the diff helper passes both through with
   the plugin name; reviewer dedupes manually.

9. **Smoke-run pass-1 may emit a WARN log AND error.** That's fine —
   confirms the rate-limit code path works. Don't fail the story
   because of it.

10. **`gh auth token`** prints a token if you're logged in to gh CLI;
    use it to provide GITHUB_TOKEN for the smoke run without exposing
    the user's PAT in shell history. Document this command in the
    retrospective so reviewers can reproduce.

## Role files (MUST embed in dispatch)

- `.claude/team/devops-agent.md` — primary (script automation, GitHub
  API, rate-limit hygiene, env-var-driven config).
- `.claude/team/architect.md` — secondary (the GitHub-API wrapper +
  rate-limit policy is a small abstraction worth getting right;
  reviewers may want to extract it for a future ArtifactHub plugin).

## PR plan

- **Branch:** `dev/v1.23-eks-blueprints-plugin` off `main` (current
  HEAD `0e76521`).
- **Commits:**
  1. `feat(scanner): aws-eks-blueprints plugin — list + extract + propose adds (V123-3.3)`
     — plugin module + GitHub-API wrapper + extractors.
  2. `test(scanner): aws-eks-blueprints unit + integration tests (V123-3.3)`
     — plugin test + fixture set + 1 integration case.
  3. `docs(scanner): document aws-eks-blueprints in plugins README (V123-3.3)`
     — README append.
  4. `chore(bmad): mark V123-3.3 for review (Epic V123-3 3/5)`
     — sprint-status.yaml + REMAINING-STORIES.md + this file.
- **PR body** must call out:
  - "Epic V123-3: 3 of 5 in review."
  - "Smoke run results (Pass 1 unauth + Pass 2 with `gh auth token`):
    adds=X, errors=Y. WARN-on-low-rate-limit code path confirmed in
    Pass 1."
  - "GITHUB_TOKEN required for nightly bot — documented in plugin top
    comment + README."
  - "NO live GitHub in CI tests — fixture-backed via env var +
    httptest."
- **NO TAG.** v1.23.0 cut belongs to V123-4.5.

## Next story

**V123-3.4** — PR-opening logic + GitHub workflow. This is the
nightly cron + PR-opener that consumes the changeset JSON the
scanner emits. Resolves open question §7.3 (auto-merge policy —
recommended: label `catalog-bot` + human review required).

## Tasks completed

1. **Plugin module (commit 1).** Wrote
   `scripts/catalog-scan/plugins/aws-eks-blueprints.mjs` (~487
   lines). Implements `export const name = 'aws-eks-blueprints'`
   and `export async function fetch(ctx)` plus a `slugify()` helper
   exported for unit testing. Pipeline: read
   `SHARKO_EKS_BLUEPRINTS_API_BASE` (defaulted to the live
   `aws-quickstart/cdk-eks-blueprints` Contents API root) → build
   inline `makeGitHubClient(ctx, token)` wrapper that injects
   `User-Agent`, `Accept`, optional `Authorization: Bearer
   $GITHUB_TOKEN`, logs `X-RateLimit-Remaining` (warn < 10, info ≥
   10), throws on non-2xx with descriptive error → list dirs under
   `lib/addons/` (filter `type === "dir"`) → sequential per-addon:
   list dir, pick first non-test `.ts` alphabetically, fetch raw
   source via `download_url`, run regex extractors → if `chart`
   AND `repo` are both missing, log info and skip → emit normalized
   entry with TODO markers for non-derivable fields. Per-addon
   errors log warn and continue; top-level dir-list errors throw to
   the harness.
2. **Fixture set (commit 2).** Built
   `scripts/catalog-scan/__tests__/fixtures/eks-blueprints/` with:
   one `contents-lib-addons.json` (6 entries: 3 already-curated
   names — karpenter, cert-manager, external-dns; 1 new add —
   fancy-new-addon; 1 broken — broken-addon; 1 file — index.ts);
   five `contents-<addon>.json` per-addon listings; five `<addon>.ts`
   mock TS sources covering BOTH the brief's documented
   `HELM_CHART_*` constant style (fancy-new-addon) AND the live
   repo's `defaultProps = { chart, repository, version, namespace }`
   form (cert-manager, external-dns, karpenter). All URL fields use
   `${BASE_URL}` placeholders that the test substitutes at runtime.
3. **Plugin unit tests (commit 2).** Wrote
   `scripts/catalog-scan/plugins/aws-eks-blueprints.test.mjs` (11
   cases). Stubs `ctx.http` directly with a chain-of-fixtures
   `buildHttpStub` helper (records every call's URL + headers for
   spying). Recorded-logger helper duplicated inline per the brief.
   Cases cover: happy path (4 entries with right shape), top-level
   type=file filter, broken extraction skip, slugify edge cases,
   GITHUB_TOKEN propagation (Authorization: Bearer header), no-token
   path (no auth header), rate-limit < 10 warn, rate-limit ≥ 10
   info, network error propagation, non-2xx 403 propagation, empty
   `lib/addons` returns [].
4. **Integration test extension (commit 2).** Added one new case to
   `scripts/catalog-scan.test.mjs`: `integration:
   aws-eks-blueprints plugin against real catalog (V123-3.3)`.
   Spins up an in-process `http.createServer` on a random port,
   serves the fixture set under predictable routes
   (`/contents/lib/addons`, `/contents/lib/addons/<addon>`,
   `/raw/<file>`) with `${BASE_URL}` substituted to the actual
   server URL. Sets `SHARKO_EKS_BLUEPRINTS_API_BASE` to the server
   URL. Asserts exactly 1 add (fancy-new-addon) against the real
   `catalog/addons.yaml`. Uses async `spawn` (not `spawnSync`)
   because the http server runs on the test event loop —
   `spawnSync` would block the main thread for the entire script
   run and starve the server. Also updated the V123-3.2 cncf
   integration test to override
   `SHARKO_EKS_BLUEPRINTS_API_BASE=http://127.0.0.1:1/contents` so
   the EKS plugin fails fast (per-plugin error isolation) rather
   than skewing assertions by hitting live GitHub.
5. **Plugins README update (commit 3).** Appended a "Real plugin
   example: `aws-eks-blueprints.mjs`" section per Tier 3 #6. Notes
   the GITHUB_TOKEN requirement for production scans (60 → 5000
   req/hr), the env-var override, the TODO-marker policy, and an
   updated out-of-scope list (Terraform sibling repo, ArtifactHub
   plugin).
6. **BMAD tracking (commit 4).** Flipped V123-3-3 in
   `sprint-status.yaml` from `in-progress` → `review`, updated
   `last_updated` line + comment header. Moved V123-3.3 from
   backlog to "In review" subsection in `REMAINING-STORIES.md`,
   updated count to "5 stories, 2 done, 1 in review". Filled the
   four retrospective sections of this file.

## Files touched

- `scripts/catalog-scan/plugins/aws-eks-blueprints.mjs` (NEW, 487
  lines).
- `scripts/catalog-scan/plugins/aws-eks-blueprints.test.mjs` (NEW,
  11 cases).
- `scripts/catalog-scan/plugins/README.md` (MODIFIED, +57 / -5
  lines).
- `scripts/catalog-scan/__tests__/fixtures/eks-blueprints/` (NEW
  directory): `contents-lib-addons.json`,
  `contents-{karpenter,cert-manager,external-dns,fancy-new-addon,broken-addon}.json`
  + `{karpenter,cert-manager,external-dns,fancy-new-addon,broken-addon}.ts`
  (11 files total).
- `scripts/catalog-scan.test.mjs` (MODIFIED, +95 / -4 lines —
  added EKS integration case + async-spawn helper + extra-env
  override on the cncf-landscape integration case).
- `.bmad/output/implementation-artifacts/sprint-status.yaml` —
  V123-3-3 flipped `in-progress` → `review`; comment header +
  `last_updated` line refreshed.
- `.bmad/output/implementation-artifacts/REMAINING-STORIES.md` —
  V123-3.2 moved from "In review" to "Done"; V123-3.3 moved into
  "In review" with smoke-run summary.
- `.bmad/output/implementation-artifacts/V123-3-3-...md` (this file)
  — status flipped to `review`, retrospective sections filled.

No Go files touched. No UI files touched. No
`.github/workflows/` files touched. No swagger regeneration (no API
surface added). No new runtime dependencies (only `yaml@^2.8.2`
already pinned by V123-3.1, and even that is unused by this plugin
which parses JSON).

## Tests

Quality gates run in the brief's documented order:

1. `cd /Users/weissmmo/projects/github-moran/sharko && npm install --prefix scripts --silent`
   — clean. No new deps, lock unchanged.
2. `node --check scripts/catalog-scan/plugins/aws-eks-blueprints.mjs`
   — syntax OK. Same for the test file and the modified
   `catalog-scan.test.mjs`.
3. `node --test 'scripts/catalog-scan/**/*.test.mjs' 'scripts/catalog-scan.test.mjs'`
   — **33 / 33 pass** (~7.6s): 6 diff + 3 changeset + 6
   integration (V123-3.1's 4 + V123-3.2's 1 + V123-3.3's 1) + 11
   aws-eks-blueprints unit + 7 cncf-landscape unit.
4. **Smoke run (REQUIRED, two passes).**

   **Pass 1 — unauth (`unset GITHUB_TOKEN`):**
   ```
   node scripts/catalog-scan.mjs --dry-run --catalog catalog/addons.yaml
   ```
   Result (stderr excerpt):
   ```
   warn: GitHub API rate-limit low; configure GITHUB_TOKEN to raise budget
         {remaining:0, url:.../lib/addons}
   warn: plugin failed (isolated, run continues)
         {plugin:aws-eks-blueprints,
          error:GitHub API 403 rate limit exceeded for .../lib/addons}
   info: cncf-landscape plugin returned 0 (live landscape.yml — sparse Helm metadata)
   info: no changes proposed by any plugin
   ```
   Exit 0. **Both expected behaviors confirmed**: WARN log fired
   on low rate-limit, AND 403 propagated as isolated plugin error
   (not silently truncated). Pass.

   **Pass 2 — with `gh auth token`:**
   ```
   GITHUB_TOKEN=$(gh auth token) node scripts/catalog-scan.mjs --dry-run --catalog catalog/addons.yaml
   ```
   Result: scan completed in ~54 seconds. EKS plugin enumerated 64
   dirs (one `index.ts` file filtered), fetched 30 entries, 34
   skipped for missing chart constants. Output: **19 adds, 11
   updates, 0 errors.**

   - **19 adds** (real EKS Blueprints addons not in the curated 45):
     apache-airflow, appmesh, aws-for-fluent-bit,
     aws-node-termination-handler, aws-privateca-issuer, calico,
     calico-operator, container-insights, gpu-operator,
     grafana-operator, istio-addons, jupyterhub, kro, kuberay,
     kubevious, nginx, opa-gatekeeper, prometheus-node-exporter,
     upbound-universal-crossplane.
   - **11 updates** (existing curated entries whose chart/repo/
     version drifted): backstage, cert-manager, cluster-autoscaler,
     external-dns (also category drift), external-secrets, falco,
     ingress-nginx, keda, kube-state-metrics, metrics-server (repo
     drift too), velero (repo drift too).
   - cncf-landscape ran in parallel and returned 0 (same as
     V123-3.2's smoke result — landscape.yml lacks Helm metadata
     for most projects).
   - **NO JSON output committed** — counts captured here per the
     anti-gold-plate rule.
5. Skipped: Go/UI gates (story touches neither).

## Decisions

1. **Broadened the metadata-extractor regex set beyond the brief.**
   Brief's metadata-extraction policy specified `HELM_CHART_NAME` /
   `HELM_CHART_REPO` etc. constants. Inspection of the live
   `aws-quickstart/cdk-eks-blueprints` repo showed the dominant
   pattern today is `defaultProps = { chart, repository, version,
   namespace }` — camelCase property names inside an object literal,
   NOT all-caps constants. The plugin tries the brief's pattern
   FIRST (preserves coverage for any legacy convention), then falls
   back to the camelCase literal. Same kind of documented broadening
   V123-3.2 did for `helm_chart_url` (Decision #1 in that brief).
   Smoke-run validation: 30 / 64 addons extracted cleanly using the
   broadened pattern; the brief's narrower regex would have matched
   ~0 (no addon in the live repo uses `HELM_CHART_*`).

2. **Inline GitHub-aware HTTP wrapper, NOT extracted to
   `lib/github.mjs`.** Per the brief's anti-gold-plate rules — only
   extract when a second use site lands. V123-3.4 (PR-opening +
   workflow) may want a similar wrapper; reviewers can extract it
   then with a 2-call refactor.

3. **Sequential per-addon iteration, no parallelism.** Brief
   explicit non-goal — keeps rate-limit semantics predictable +
   simplifies error handling. With auth (5000 req/hr) the 130-call
   scan is well within budget; without auth it exhausts in <60s
   either way (parallelism wouldn't help).

4. **Adds-only — no update-detection logic added.** AC restricts
   scope to "proposes an add". The diff helper still records
   incidental updates on already-curated names when chart/version/
   repo drift (smoke run produced 11 such updates) — that's a
   property of the diff helper, not extra plugin logic.

5. **Fixture uses `karpenter` (not `aws-load-balancer-controller`)
   as one of the 3 already-curated names.** The catalog has
   `ingress-nginx` and `haproxy-ingress` for ingress but does NOT
   carry `aws-load-balancer-controller`. `karpenter` IS curated,
   IS in EKS Blueprints, and slugs to itself — perfect fit. This
   choice keeps the integration assertion "exactly 1 add
   (fancy-new-addon)" deterministic.

6. **Async `spawn` for the EKS integration test, NOT `spawnSync`.**
   The test needs an in-process `http.createServer` running on the
   same event loop. `spawnSync` blocks the main thread for the
   subprocess's entire run, starving the server — observed first
   hand: a `spawnSync`-based draft timed out at 122s (= 2 plugins
   × 60s timeout each) because the server never accepted any
   connection. Replaced with a Promise-wrapped `spawn`. The
   V123-3.2 cncf integration test still uses `spawnSync` since
   `file://` doesn't need a server.

7. **Hermetic guard on the cncf-landscape integration test.**
   Adding the EKS plugin caused that test to fail because the EKS
   plugin (with its default URL = live GitHub API) hit live
   network and contributed unexpected adds. Fix: explicitly
   override `SHARKO_EKS_BLUEPRINTS_API_BASE=http://127.0.0.1:1/contents`
   so the EKS plugin gets connection-refused, fails fast, and is
   isolated by the harness. Cost: ~7s of `fetchWithRetry` 1+2+4s
   backoffs (the cncf integration test went from <1s to ~7s).
   Acceptable. Could be further reduced by lowering retry budget
   via env var if it ever becomes annoying.

8. **`developer-tools` fallback for unmatched category-keyword
   inference.** Same stance as V123-3.2: small inline keyword
   map, conservative default; reviewers refine in the bot PR.
   Smoke-run observation: the 19 adds covered every category in
   the schema except `chaos` and `gitops` (no chaos/gitops adds
   landed because the existing catalog already covers them well).

9. **Pass-through `_eks_blueprints_path` and `_eks_blueprints_source`
   metadata fields on each entry.** Mirror V123-3.2's
   `_landscape_homepage` / `_landscape_source` convention. Not
   consumed by `diff.mjs`'s COMPARABLE_FIELDS so no churn risk.
   Provides reviewer convenience: a click-through to the upstream
   TS source so a reviewer can verify the chart/version values
   without leaving the bot PR.

10. **No CHANGELOG entry, no version bump.** Per the brief — v1.23.0
    cut belongs to V123-4.5. This is one of three plugin-shaped
    intermediate stories; tagging would be premature.

11. **Repo URL fallback `<TODO: chart repo>` placeholder for the
    rare case where extractor finds chart but not repo.** Schema
    requires `repo` non-empty + URL-format. Gating earlier on
    `if (!meta.chart && !meta.repo)` skip means we already filter
    the most common no-extract case; the placeholder covers a
    weird edge where chart was extracted but repo wasn't (didn't
    happen in the smoke run). The placeholder is schema-valid via
    the `https://example.invalid/...` URL pattern → forces human
    review.

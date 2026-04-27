# `catalog-scan` plugin contract

This directory holds the scanner plugins consumed by
[`scripts/catalog-scan.mjs`](../../catalog-scan.mjs). The skeleton lands
in **V123-3.1**; concrete plugins arrive in:

- **V123-3.2** — `cncf-landscape.mjs`
- **V123-3.3** — `aws-eks-blueprints.mjs`

The GitHub workflow + PR-opener that consumes the changeset JSON is
**V123-3.4**.

## Plugin module shape (ESM)

A plugin is a `.mjs` file that exports a `name` and an `async fetch(ctx)`
function. Optional `annotate(entry)` lets a plugin enrich a curated
entry instead of replacing it.

```js
// scripts/catalog-scan/plugins/my-source.mjs
export const name = 'my-source';

/**
 * @param {object} ctx
 * @param {{ info: Function, warn: Function, error: Function, child: Function }} ctx.logger
 *        Structured logger writing JSON-lines to stderr. Use `child({plugin: ...})`
 *        for nested context.
 * @param {AbortSignal} ctx.abortSignal Cancellation signal honored by ctx.http.
 * @param {(url: string, opts?: object) => Promise<Response>} ctx.http
 *        fetch-with-3-retries (1s/2s/4s) + UA `sharko-catalog-scan/1.0`.
 *        Plugins MUST use ctx.http instead of bare fetch() so retries +
 *        UA stay uniform across sources.
 * @returns {Promise<NormalizedEntry[]>}
 */
export async function fetch(ctx) {
  ctx.logger.info('starting fetch');
  // const res = await ctx.http('https://example.com/data.json');
  return [
    // {
    //   name: 'my-addon',         // catalog primary key (REQUIRED)
    //   repo: 'https://example.com/charts',
    //   chart: 'my-addon',
    //   version: '1.2.3',
    //   category: 'security',     // map to schema enum if known
    //   trust_score: 80,          // 0..100, plugin-defined heuristic
    //   // ...other normalized fields from catalog/schema.json
    // },
  ];
}

// Optional: enrich a curated entry instead of proposing a replacement.
// The scanner ignores absent helpers.
//
// export function annotate(entry) { return { ...entry, /* extras */ }; }
```

## Discovery rules

The scanner discovers plugins by alphabetical glob of `*.mjs` in this
directory. **Files whose name starts with `_` are skipped in production**
— they exist only to exercise the discovery loop in tests. Tests opt in
via either the `--include-hidden` CLI flag or the
`SHARKO_SCAN_LOAD_HIDDEN=1` env var.

`*.test.mjs` files are also skipped (the scanner never runs unit tests
as scanner plugins).

The current `_example.mjs` plugin returns `[]` and has zero runtime
impact. **Do not delete it** — it's the regression test for the
"discovery loop loads at least one plugin" path.

## Changeset JSON shape

A scan produces a single JSON document conforming to this shape (the
shape itself lives in [`../lib/changeset.mjs`](../lib/changeset.mjs)):

```json
{
  "schema_version": "1.0",
  "generated_at": "2026-04-26T00:00:00.000Z",
  "scanner_runs": [
    { "plugin": "my-source", "fetched_count": 3 },
    { "plugin": "broken-source", "fetched_count": 0, "error": "upstream 503" }
  ],
  "adds": [
    { "plugin": "my-source", "entry": { "name": "new-addon", "...": "..." } }
  ],
  "updates": [
    {
      "plugin": "my-source",
      "entry": { "name": "existing-addon", "version": "2.0.0" },
      "diff": { "version": { "from": "1.0.0", "to": "2.0.0" } }
    }
  ]
}
```

**Deletes are NOT proposed** — removal of a curated catalog entry is a
human decision, gated by CODEOWNERS. Same rationale as the existing
catalog curation policy.

When two plugins propose the same `name`, both entries appear in
`adds` (each tagged with its `plugin`). The scanner aggregates; humans
pick. This is intentional — the scanner is plugin-agnostic.

## Failure isolation

If a plugin throws or returns a non-array, the run continues with the
remaining plugins. The failing plugin appears in `scanner_runs` with an
`error` field; downstream tooling (V123-3.4's PR opener) decides
whether to surface the partial result or skip it.

## Real plugin example: `cncf-landscape.mjs`

Shipped in V123-3.2. Pulls the canonical CNCF landscape YAML
(`https://raw.githubusercontent.com/cncf/landscape/master/landscape.yml`)
and proposes catalog adds/updates for **graduated + incubating**
projects that surface a Helm chart reference (`extra.helm_chart_url`,
`extra.chart_url`, `extra.helm_url`, or `extra.artifacthub_url`).
Subcategories that don't map to a Sharko `category` schema enum (e.g.
`Container Runtime`, `Scheduling & Orchestration`) are skipped.

URL override for tests + local development:

```bash
SHARKO_CNCF_LANDSCAPE_URL=file:///path/to/fixture.yaml \
  node scripts/catalog-scan.mjs --dry-run --catalog catalog/addons.yaml
```

The plugin emits `<TODO: ...>` markers for fields it cannot derive
reliably (description, maintainers) and `"unknown"` for license —
schema-valid but obviously synthetic so reviewers correct in the bot
PR. Diff comparison only covers `chart`, `version`, `category`, `repo`
(see `lib/diff.mjs`), so synthetic fields never churn updates.

Acceptance criteria + design notes live in
`.bmad/output/implementation-artifacts/V123-3-2-cncf-landscape-scanner-plugin.md`.

## Real plugin example: `aws-eks-blueprints.mjs`

Shipped in V123-3.3. Walks the GitHub Contents API tree under
`lib/addons/` in
[`aws-quickstart/cdk-eks-blueprints`](https://github.com/aws-quickstart/cdk-eks-blueprints)
and proposes catalog adds for any addon that surfaces a Helm chart
reference and isn't already curated. Adds-only per V123-3.3 AC (the
diff helper still records incidental updates on already-curated names
when chart/version/repo drift, but the plugin doesn't engineer
update-detection logic).

Endpoints used per scan (~130 calls for the current 65-addon set):

  1. `GET <BASE>/lib/addons` — top-level dir listing.
  2. `GET <BASE>/lib/addons/<addon>` — per-addon dir listing.
  3. `GET <download_url>` — raw `.ts` file content.

**`GITHUB_TOKEN` is required for production scans.** Unauthenticated
GitHub API allows only 60 req/hr; with token the budget rises to
5000 req/hr. The plugin reads `X-RateLimit-Remaining` from each
Contents API response — `info` log when ≥ 10, `warn` log when < 10
(operator should configure `GITHUB_TOKEN`). HTTP 403 from rate-limit
exhaustion propagates as a plugin error in `scanner_runs[].error`,
not silently truncated.

URL override for tests + local development:

```bash
SHARKO_EKS_BLUEPRINTS_API_BASE=http://127.0.0.1:8080/contents \
  GITHUB_TOKEN=$(gh auth token) \
  node scripts/catalog-scan.mjs --dry-run --catalog catalog/addons.yaml
```

Same TODO-marker policy as `cncf-landscape.mjs`:

  - `description` → `"<TODO: human description>"`
  - `default_namespace` → extracted namespace OR `"<slug>-system"`
  - `license` → `"unknown"` (CI flags for human review)
  - `maintainers` → `["<TODO: derive from chart repo>"]`

These are schema-valid but obviously synthetic so reviewers correct
in the bot PR. The diff helper only compares `chart, version,
category, repo` so synthetic placeholders never trigger spurious
updates against curated catalog data.

Acceptance criteria + design notes live in
`.bmad/output/implementation-artifacts/V123-3-3-aws-eks-blueprints-scanner-plugin.md`.

## Workflow integration

The plugins are invoked daily by
[`.github/workflows/catalog-scan.yml`](../../../.github/workflows/catalog-scan.yml)
(V123-3.4). The workflow:

1. Checks out the repo with `fetch-depth: 0`.
2. Runs `node scripts/catalog-scan.mjs --catalog catalog/addons.yaml`,
   which produces `_dist/catalog-scan/changeset.json` (or no file
   when zero proposals).
3. If the changeset is non-empty, runs
   `node scripts/catalog-scan/pr-open.mjs`, which:
   - Checks `gh pr list --label catalog-scan --state open` and skips
     when an open bot PR already exists.
   - Pre-computes Scorecard + chart-resolves + license signals per
     proposal (failures degrade to `unknown`, never abort).
   - Edits `catalog/addons.yaml` via `lib/yaml-edit.mjs` (AST mode
     preserves comments + per-entry style).
   - Commits + pushes to `catalog-scan/<UTC YYYY-MM-DD>` and opens a
     **draft** PR with labels `catalog-scan` + `needs-review`.

The workflow's permissions are exactly `contents: write` +
`pull-requests: write`. **Auto-merge is forbidden by NFR-V123-7** —
the bot opens a draft PR and human reviewers merge or close it.

Local preview:

```bash
GITHUB_TOKEN=$(gh auth token) node scripts/catalog-scan.mjs --catalog catalog/addons.yaml
make catalog-scan-pr   # prints the PR body markdown to stdout
```

## Out of scope for this directory

- **Additional upstream scanners** — Terraform-flavored
  `aws-ia/terraform-aws-eks-blueprints-addons` (could be a future
  plugin; no story exists today). ArtifactHub plugin (no story).
- **Schema validation** — the Go loader at
  `internal/catalog/loader.go` is authoritative; the scanner emits a
  *proposal*, not a validated entry.
- **Trust-score computation** — plugins own that heuristic; V123-3.4
  applies a baseline.
- **Cosign verification** — V123-2 ships per-entry signatures at the
  embedded-catalog level; the scanner is one hop earlier.

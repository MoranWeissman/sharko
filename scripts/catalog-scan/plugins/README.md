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

## Out of scope for this directory

- Real upstream scanners — V123-3.2 / V123-3.3.
- Schema validation — the Go loader at `internal/catalog/loader.go` is
  authoritative; the scanner emits a *proposal*, not a validated entry.
- Trust-score computation — plugins own that heuristic.
- Cosign verification — V123-2 ships per-entry signatures at the
  embedded-catalog level; the scanner is one hop earlier.

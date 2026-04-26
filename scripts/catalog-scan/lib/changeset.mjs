/**
 * changeset.mjs — aggregator + JSON shape for the scanner output.
 *
 * The shape is the contract V123-3.4's PR-opener consumes:
 *
 *   {
 *     schema_version: '1.0',
 *     generated_at:   '<RFC3339>',
 *     scanner_runs:   [{ plugin, fetched_count, error? }],
 *     adds:           [{ plugin, entry }],
 *     updates:        [{ plugin, entry, diff }],
 *   }
 */

export const CHANGESET_SCHEMA_VERSION = '1.0';

/** Build an empty changeset with placeholder timestamp. */
export function newChangeset() {
  return {
    schema_version: CHANGESET_SCHEMA_VERSION,
    generated_at: new Date(0).toISOString(),
    scanner_runs: [],
    adds: [],
    updates: [],
  };
}

/** Append one scanner run record. */
export function recordRun(changeset, { plugin, fetched_count, error }) {
  const rec = { plugin, fetched_count };
  if (error !== undefined) rec.error = error;
  changeset.scanner_runs.push(rec);
}

/** Stamp the generated_at field with the current time. Idempotent. */
export function finalizeChangeset(changeset) {
  changeset.generated_at = new Date().toISOString();
  return changeset;
}

/**
 * Deterministic JSON: stable key ordering at every nested object so
 * snapshots don't churn on field-insertion-order regressions. Arrays
 * keep their order (semantically meaningful — adds/updates are an
 * audit trail).
 */
export function stringifyDeterministic(obj) {
  return JSON.stringify(obj, sortedReplacer, 2);
}

function sortedReplacer(_key, value) {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    const sorted = {};
    for (const k of Object.keys(value).sort()) sorted[k] = value[k];
    return sorted;
  }
  return value;
}

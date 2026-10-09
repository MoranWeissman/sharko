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
 *     duplicates:     [{ plugin, entry, _duplicate_proposal: true,
 *                        first_plugin: string }],
 *   }
 *
 * V123-PR-F3 / M3: `duplicates` carries cross-plugin slug collisions
 * — two plugins proposing the same NEW addon name. Pre-fix, the
 * second occurrence reached `yaml-edit.applyChangeset` and threw,
 * aborting the entire scan run for ONE collision. Post-fix, the first
 * occurrence stays in `adds` (yaml-edit writes it normally) and every
 * subsequent occurrence lands in `duplicates` so the PR body renders
 * it as a "Duplicate proposals — review needed" section instead of
 * silently dropping it.
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
    duplicates: [],
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
 * Dedup `changeset.adds` by entry name. The FIRST occurrence wins
 * (stays in `adds`); subsequent occurrences move to `changeset.duplicates`
 * with `_duplicate_proposal: true` and `first_plugin` set to the plugin
 * that won. Returns the same changeset (mutated in place) for a fluent
 * call style.
 *
 * V123-PR-F3 / M3: pre-fix, two plugins proposing the same new addon
 * slug both landed in `adds` and the second one tripped a defensive
 * throw inside `yaml-edit.applyChangeset`, killing the entire scan
 * run. Post-fix, that case is reviewer-visible (PR body renders the
 * `duplicates` array) and the rest of the run completes cleanly.
 *
 * The function is idempotent: calling it twice on the same changeset
 * is a no-op on the second pass (no entry in `adds` shares a name
 * after the first pass).
 *
 * @param {object} changeset — must have `.adds` array and may have
 *   `.duplicates` array (created if missing).
 * @returns {object} the same changeset, mutated.
 */
export function dedupAdds(changeset) {
  if (!changeset || !Array.isArray(changeset.adds)) return changeset;
  if (!Array.isArray(changeset.duplicates)) changeset.duplicates = [];

  const seen = new Map(); // name → plugin that won the slot
  const kept = [];
  for (const a of changeset.adds) {
    const name = a?.entry?.name;
    if (typeof name !== 'string' || name.length === 0) {
      kept.push(a);
      continue;
    }
    if (!seen.has(name)) {
      seen.set(name, a.plugin);
      kept.push(a);
      continue;
    }
    changeset.duplicates.push({
      plugin: a.plugin,
      entry: a.entry,
      _duplicate_proposal: true,
      first_plugin: seen.get(name),
    });
  }
  changeset.adds = kept;
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

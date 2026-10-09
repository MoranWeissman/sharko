/**
 * diff.mjs — pure-function diff between current catalog entries and
 * scanner-proposed entries.
 *
 * Inputs:
 *   current:    CatalogEntry[]                              (parsed from catalog/addons.yaml)
 *   proposals:  Array<{ plugin: string, entries: ScannerEntry[] }>
 *
 * Output:
 *   {
 *     adds:    [{ plugin, entry }],
 *     updates: [{ plugin, entry, diff: { field: { from, to } } }],
 *   }
 *
 * Rules:
 *   - "Add" — proposed `name` doesn't exist in current.
 *   - "Update" — name exists AND at least one comparable field differs.
 *   - Deletes are NOT proposed by this scanner (curation is a human
 *     decision, CODEOWNERS-gated).
 *   - Comparable fields: chart, version, category, repo. Other fields
 *     (security_score, license, curated_by, github_stars, etc.) stay
 *     human-curated and are intentionally ignored by this diff.
 *   - When two plugins propose the same name, both entries appear in
 *     `adds` (each tagged with its plugin). The scanner aggregates;
 *     humans pick.
 */

const COMPARABLE_FIELDS = ['chart', 'version', 'category', 'repo'];

/**
 * @param {Array<object>} current  catalog entries (must have `.name`)
 * @param {Array<{plugin:string, entries:object[]}>} proposals
 * @returns {{adds: Array<{plugin:string, entry:object}>, updates: Array<{plugin:string, entry:object, diff:object}>}}
 */
export function diff(current, proposals) {
  const byName = new Map();
  for (const e of current) {
    if (e && typeof e.name === 'string') byName.set(e.name, e);
  }
  const adds = [];
  const updates = [];
  for (const { plugin, entries } of proposals) {
    if (!Array.isArray(entries)) continue;
    for (const entry of entries) {
      if (!entry || typeof entry.name !== 'string') continue;
      const existing = byName.get(entry.name);
      if (!existing) {
        adds.push({ plugin, entry });
        continue;
      }
      const fieldDiff = diffFields(existing, entry);
      if (fieldDiff && Object.keys(fieldDiff).length > 0) {
        updates.push({ plugin, entry, diff: fieldDiff });
      }
    }
  }
  return { adds, updates };
}

/**
 * Compute per-field {from, to} diff over the comparable subset. Only
 * fields the proposal *populated* are considered — a missing key in
 * the proposal is treated as "scanner has no opinion", not as "set
 * to undefined".
 */
function diffFields(current, proposed) {
  const out = {};
  for (const f of COMPARABLE_FIELDS) {
    if (!(f in proposed)) continue;
    const a = current[f];
    const b = proposed[f];
    if (!deepEqual(a, b)) {
      out[f] = { from: a, to: b };
    }
  }
  return out;
}

function deepEqual(a, b) {
  if (a === b) return true;
  if (a == null || b == null) return false;
  if (typeof a !== typeof b) return false;
  if (typeof a !== 'object') return false;
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  if (Array.isArray(a)) {
    if (a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) if (!deepEqual(a[i], b[i])) return false;
    return true;
  }
  const ak = Object.keys(a);
  const bk = Object.keys(b);
  if (ak.length !== bk.length) return false;
  for (const k of ak) if (!deepEqual(a[k], b[k])) return false;
  return true;
}

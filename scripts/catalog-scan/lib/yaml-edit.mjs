/**
 * yaml-edit.mjs — apply a scanner changeset to `catalog/addons.yaml`
 * while preserving comments + field order on existing entries.
 *
 * V123-3.4 Tier 1 #3. The PR-opener (V123-3.4) drives this module:
 * a single call to `applyChangeset(yamlText, changeset)` returns the
 * edited YAML text ready to write back to disk. The caller does I/O.
 *
 * ## Why AST mode (`yaml.parseDocument`) instead of plain `parse`
 *
 * Plain `yaml.parse` returns a JS object — round-tripping it through
 * `yaml.stringify` would erase all comments + field-order conventions
 * that the curated `catalog/addons.yaml` relies on (the file has
 * `# -- Security --` section markers + grouped fields per entry that
 * reviewers read at a glance). AST mode keeps every node's source
 * position + leading/trailing comments intact for entries we DON'T
 * touch; we only mutate the nodes the changeset names.
 *
 * ## Add semantics
 *
 * New entries are inserted **alphabetically by `name`** so the diff
 * reviewers see is stable and small (one new `- name: ...` block in
 * the right spot, not a churn at the bottom). The new node is built
 * from the scanner's normalized entry shape (per
 * `scripts/catalog-scan/plugins/README.md`) — `name`, `chart`, `repo`,
 * `version?`, `category`, `default_namespace`, `description`,
 * `license`, `maintainers`, `curated_by`. Plugin-specific underscore-
 * prefixed metadata (e.g. `_eks_blueprints_path`) is stripped — those
 * fields are scanner internals, not catalog schema.
 *
 * ## Update semantics
 *
 * Per `scripts/catalog-scan/lib/diff.mjs`, only COMPARABLE_FIELDS
 * (`chart`, `version`, `category`, `repo`) are compared. The diff
 * payload `{ field: { from, to } }` lists exactly those fields that
 * drifted; this module applies the `to` values into the existing AST
 * node, preserving every other key + every comment on that entry.
 *
 * Updates for non-existent entry names throw — the scanner shouldn't
 * be proposing an update for something that isn't in the catalog
 * (the upstream diff helper guarantees this), but if anything ever
 * regresses, throwing is the simpler/louder failure than recording
 * a soft error in the changeset (V123-3.4 brief Tier 3 #6 explicitly
 * documents this choice).
 *
 * ## Empty changeset
 *
 * `adds.length + updates.length === 0` → return the input text
 * unchanged (idempotent, no formatting churn).
 *
 * @see scripts/catalog-scan/lib/yaml-edit.test.mjs for the 6-case suite.
 */

import yamlPkg from 'yaml';
const { parseDocument, isMap, isSeq } = yamlPkg;

/** Fields that scanner plugins emit but are not part of the catalog schema. */
const SCANNER_INTERNAL_FIELD_PREFIX = '_';

/**
 * Canonical key order for new entries — matches the curated style in
 * `catalog/addons.yaml`. Fields not in this list are appended in
 * insertion order at the end. Keep in sync with the dominant pattern
 * in the curated file.
 */
const PREFERRED_KEY_ORDER = [
  'name',
  'description',
  'chart',
  'repo',
  'version',
  'default_namespace',
  'default_sync_wave',
  'docs_url',
  'homepage',
  'source_url',
  'maintainers',
  'license',
  'category',
  'curated_by',
  'security_score',
  'github_stars',
  'min_kubernetes_version',
];

/**
 * Apply a scanner changeset to a YAML text. Pure function: no I/O.
 *
 * @param {string} yamlText - Original `catalog/addons.yaml` contents.
 * @param {object} changeset - Object with `adds` + `updates` arrays
 *   per `scripts/catalog-scan/lib/changeset.mjs`. Other top-level
 *   fields (`generated_at`, `scanner_runs`, `schema_version`) are
 *   ignored here — they live in the PR body, not the catalog.
 * @returns {string} edited YAML text.
 */
export function applyChangeset(yamlText, changeset) {
  const adds = Array.isArray(changeset?.adds) ? changeset.adds : [];
  const updates = Array.isArray(changeset?.updates) ? changeset.updates : [];

  if (adds.length === 0 && updates.length === 0) {
    return yamlText;
  }

  const doc = parseDocument(yamlText);
  const addonsNode = doc.get('addons', true /* keepNode */);
  if (!isSeq(addonsNode)) {
    throw new Error("yaml-edit: catalog is missing top-level 'addons:' sequence");
  }

  for (const { entry, diff: fieldDiff } of updates) {
    if (!entry || typeof entry.name !== 'string') {
      throw new Error('yaml-edit: update payload missing entry.name');
    }
    const existingIdx = findEntryIndex(addonsNode, entry.name);
    if (existingIdx < 0) {
      throw new Error(`yaml-edit: update for '${entry.name}' but no such entry in catalog`);
    }
    applyDiff(addonsNode.items[existingIdx], fieldDiff, entry.name);
  }

  for (const { entry } of adds) {
    if (!entry || typeof entry.name !== 'string') {
      throw new Error('yaml-edit: add payload missing entry.name');
    }
    if (findEntryIndex(addonsNode, entry.name) >= 0) {
      // Defensive — diff helper shouldn't propose an "add" for a name
      // already in the catalog, but if it ever does, surface loudly.
      throw new Error(`yaml-edit: add for '${entry.name}' but entry already exists`);
    }
    insertAlphabetically(doc, addonsNode, entry);
  }

  // Re-validate — the loader (Go side) is authoritative on schema, but
  // a sanity check here means we never produce a YAML missing the
  // top-level `addons:` array or with malformed entry shapes.
  const reparsed = doc.toJS();
  if (!reparsed || !Array.isArray(reparsed.addons)) {
    throw new Error("yaml-edit: post-edit catalog missing top-level 'addons:' array");
  }
  for (const e of reparsed.addons) {
    if (!e || typeof e.name !== 'string') {
      throw new Error('yaml-edit: post-edit catalog has entry without name field');
    }
  }

  return String(doc);
}

/* ------------------------------------------------------------------ */
/* Helpers                                                              */
/* ------------------------------------------------------------------ */

function findEntryIndex(seqNode, name) {
  for (let i = 0; i < seqNode.items.length; i++) {
    const item = seqNode.items[i];
    if (!isMap(item)) continue;
    const nameVal = item.get('name');
    if (nameVal === name) return i;
  }
  return -1;
}

function applyDiff(itemNode, fieldDiff, entryName) {
  if (!fieldDiff || typeof fieldDiff !== 'object') return;
  if (!isMap(itemNode)) {
    throw new Error(`yaml-edit: entry '${entryName}' is not a map node`);
  }
  for (const [field, change] of Object.entries(fieldDiff)) {
    if (!change || typeof change !== 'object' || !('to' in change)) continue;
    itemNode.set(field, change.to);
  }
}

/**
 * Insert a new entry into the addons sequence at the alphabetical
 * position (by `name`). Builds an ordered map node so the new entry
 * looks consistent with curated entries.
 */
function insertAlphabetically(doc, seqNode, entry) {
  // Strip scanner-internal fields (`_eks_blueprints_path`, etc.).
  const clean = {};
  for (const [k, v] of Object.entries(entry)) {
    if (k.startsWith(SCANNER_INTERNAL_FIELD_PREFIX)) continue;
    clean[k] = v;
  }

  // Build a JS object respecting the preferred key order, then
  // append any remaining keys in insertion order.
  const ordered = {};
  for (const k of PREFERRED_KEY_ORDER) {
    if (k in clean) ordered[k] = clean[k];
  }
  for (const [k, v] of Object.entries(clean)) {
    if (!(k in ordered)) ordered[k] = v;
  }

  // doc.createNode produces a YAMLMap matching the document's flow style.
  const newItem = doc.createNode(ordered);

  // Find insertion index — first existing item whose name is > entry.name.
  let insertAt = seqNode.items.length;
  for (let i = 0; i < seqNode.items.length; i++) {
    const item = seqNode.items[i];
    if (!isMap(item)) continue;
    const existingName = item.get('name');
    if (typeof existingName === 'string' && existingName.localeCompare(entry.name) > 0) {
      insertAt = i;
      break;
    }
  }
  seqNode.items.splice(insertAt, 0, newItem);
}

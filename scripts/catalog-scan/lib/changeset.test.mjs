/**
 * changeset.test.mjs — unit tests for the changeset aggregator + JSON
 * shape helpers.
 *
 * Cases mirror V123-3.1 brief Tier 2 #10:
 *   1. aggregates 0-plugin run → empty adds/updates with scanner_runs: []
 *   2. aggregates 2-plugin run with mixed results → correct shape
 *   3. serializes to deterministic JSON (snapshot via stringify with
 *      sorted keys) — catches accidental field-order regressions
 */
import test from 'node:test';
import assert from 'node:assert/strict';

import {
  newChangeset,
  recordRun,
  finalizeChangeset,
  stringifyDeterministic,
  CHANGESET_SCHEMA_VERSION,
} from './changeset.mjs';

test('changeset: 0-plugin run → empty shape', () => {
  const cs = newChangeset();
  finalizeChangeset(cs);
  assert.equal(cs.schema_version, CHANGESET_SCHEMA_VERSION);
  assert.deepEqual(cs.scanner_runs, []);
  assert.deepEqual(cs.adds, []);
  assert.deepEqual(cs.updates, []);
  assert.match(cs.generated_at, /^\d{4}-\d{2}-\d{2}T/);
});

test('changeset: 2-plugin run with mixed results → correct shape', () => {
  const cs = newChangeset();
  recordRun(cs, { plugin: 'p1', fetched_count: 3 });
  recordRun(cs, { plugin: 'p2', fetched_count: 0, error: 'upstream 503' });
  cs.adds = [{ plugin: 'p1', entry: { name: 'a' } }];
  cs.updates = [{ plugin: 'p1', entry: { name: 'b' }, diff: { version: { from: '1', to: '2' } } }];
  finalizeChangeset(cs);

  assert.equal(cs.scanner_runs.length, 2);
  assert.deepEqual(cs.scanner_runs[0], { plugin: 'p1', fetched_count: 3 });
  assert.deepEqual(cs.scanner_runs[1], { plugin: 'p2', fetched_count: 0, error: 'upstream 503' });
  assert.equal(cs.adds.length, 1);
  assert.equal(cs.updates.length, 1);
});

test('changeset: deterministic JSON has sorted top-level keys', () => {
  // Build a changeset with intentionally out-of-alphabetical insertion
  // order; the stringifyDeterministic should still emit sorted keys.
  const cs = newChangeset();
  recordRun(cs, { plugin: 'z-source', fetched_count: 1 });
  recordRun(cs, { plugin: 'a-source', fetched_count: 1 });
  cs.adds = [{ plugin: 'z-source', entry: { name: 'foo' } }];
  cs.updates = [];
  // Pin generated_at so the snapshot is byte-stable.
  cs.generated_at = '2026-04-26T00:00:00.000Z';

  const json = stringifyDeterministic(cs);
  // Top-level keys appear in alphabetical order.
  const expectedTop = ['adds', 'generated_at', 'scanner_runs', 'schema_version', 'updates'];
  for (let i = 0; i < expectedTop.length - 1; i++) {
    const a = json.indexOf(`"${expectedTop[i]}"`);
    const b = json.indexOf(`"${expectedTop[i + 1]}"`);
    assert.ok(a > -1 && b > -1, `expected both keys present: ${expectedTop[i]}, ${expectedTop[i + 1]}`);
    assert.ok(a < b, `expected '${expectedTop[i]}' before '${expectedTop[i + 1]}'`);
  }
  // scanner_runs preserves insertion order (semantically meaningful).
  const idxZ = json.indexOf('"z-source"');
  const idxA = json.indexOf('"a-source"');
  assert.ok(idxZ > -1 && idxA > -1);
  assert.ok(idxZ < idxA, 'scanner_runs array preserves insertion order');

  // Inside an entry, keys also appear sorted: structured assertion via
  // the parsed output. Re-parse the JSON and inspect Object.keys order
  // (Node preserves insertion order for string keys, and the
  // deterministic stringify inserted them sorted).
  const parsed = JSON.parse(json);
  const runKeys = Object.keys(parsed.scanner_runs[0]);
  assert.deepEqual(runKeys, ['fetched_count', 'plugin'], 'object keys are sorted alphabetically');
  const addKeys = Object.keys(parsed.adds[0]);
  assert.deepEqual(addKeys, ['entry', 'plugin'], 'add-record keys are sorted alphabetically');
});

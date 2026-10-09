/**
 * diff.test.mjs — unit tests for the pure diff() helper.
 *
 * Cases mirror V123-3.1 brief Tier 2 #9:
 *   1. empty current + empty proposed → empty changeset
 *   2. empty current + 1 proposed → 1 add, 0 updates
 *   3. 1 current + same name + same fields → 0 adds, 0 updates
 *   4. 1 current + same name + different version → 0 adds, 1 update
 *      with diff.version.{from, to}
 *   5. 1 current + missing name in proposed (deletion case) → 0 adds,
 *      0 updates (assert delete is NOT proposed)
 *   6. 2 plugins propose same name → 2 entries in adds (each tagged
 *      with plugin name); the resolution policy is "let humans pick".
 */
import test from 'node:test';
import assert from 'node:assert/strict';

import { diff } from './diff.mjs';

test('diff: empty current + empty proposed → empty', () => {
  const result = diff([], []);
  assert.deepEqual(result, { adds: [], updates: [] });
});

test('diff: empty current + 1 proposed → 1 add, 0 updates', () => {
  const proposals = [
    { plugin: 'p1', entries: [{ name: 'foo', version: '1.0.0' }] },
  ];
  const result = diff([], proposals);
  assert.equal(result.adds.length, 1);
  assert.equal(result.updates.length, 0);
  assert.equal(result.adds[0].plugin, 'p1');
  assert.equal(result.adds[0].entry.name, 'foo');
});

test('diff: same name + same fields → 0 adds, 0 updates', () => {
  const current = [{ name: 'foo', version: '1.0.0', chart: 'foo', repo: 'https://x', category: 'security' }];
  const proposals = [
    { plugin: 'p1', entries: [{ name: 'foo', version: '1.0.0', chart: 'foo', repo: 'https://x', category: 'security' }] },
  ];
  const result = diff(current, proposals);
  assert.deepEqual(result, { adds: [], updates: [] });
});

test('diff: same name + different version → 1 update with from/to', () => {
  const current = [{ name: 'foo', version: '1.0.0', chart: 'foo', repo: 'https://x' }];
  const proposals = [
    { plugin: 'p1', entries: [{ name: 'foo', version: '1.1.0' }] },
  ];
  const result = diff(current, proposals);
  assert.equal(result.adds.length, 0);
  assert.equal(result.updates.length, 1);
  const u = result.updates[0];
  assert.equal(u.plugin, 'p1');
  assert.equal(u.entry.name, 'foo');
  assert.deepEqual(u.diff, { version: { from: '1.0.0', to: '1.1.0' } });
});

test('diff: deletion case (current has entry not in proposals) → no delete proposed', () => {
  // 'foo' is in current but no proposal mentions it.
  const current = [{ name: 'foo', version: '1.0.0' }];
  const proposals = [
    { plugin: 'p1', entries: [] },
  ];
  const result = diff(current, proposals);
  assert.deepEqual(result, { adds: [], updates: [] }, 'scanner must NOT propose deletes');
});

test('diff: 2 plugins propose same name → both appear in adds (each tagged)', () => {
  const proposals = [
    { plugin: 'p1', entries: [{ name: 'newaddon', version: '1.0.0' }] },
    { plugin: 'p2', entries: [{ name: 'newaddon', version: '1.0.1' }] },
  ];
  const result = diff([], proposals);
  assert.equal(result.adds.length, 2);
  const plugins = result.adds.map((a) => a.plugin).sort();
  assert.deepEqual(plugins, ['p1', 'p2']);
  assert.equal(result.updates.length, 0);
});

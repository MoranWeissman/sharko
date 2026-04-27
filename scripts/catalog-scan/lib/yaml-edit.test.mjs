/**
 * yaml-edit.test.mjs — V123-3.4 Tier 3 #6.
 *
 * Six cases per the V123-3.4 brief:
 *   1. apply 1 add → yaml gains the new entry.
 *   2. apply 1 update → existing entry's field changes; other fields
 *      untouched; comments preserved.
 *   3. apply 0 ops → input === output (idempotent).
 *   4. apply update for non-existent entry → throws.
 *   5. apply 2 updates to same entry → both fields change.
 *   6. alphabetical-insertion order — adds land in the sorted spot.
 */

import test from 'node:test';
import assert from 'node:assert/strict';

import { applyChangeset } from './yaml-edit.mjs';

const SAMPLE_YAML = `# top-level comment kept
addons:

  - name: alpha
    description: Alpha thing
    chart: alpha
    repo: https://charts.alpha.test  # alpha repo comment
    default_namespace: alpha
    maintainers: [alpha-team]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated]

  - name: charlie
    description: Charlie thing
    chart: charlie
    repo: https://charts.charlie.test
    default_namespace: charlie
    maintainers: [charlie-team]
    license: MIT
    category: networking
    curated_by: [cncf-incubating]
`;

function makeAdd(name, extra = {}) {
  return {
    plugin: 'test-plugin',
    entry: {
      name,
      description: '<TODO: human description>',
      chart: name,
      repo: `https://charts.${name}.test`,
      version: '0.1.0',
      default_namespace: `${name}-system`,
      maintainers: ['<TODO: derive from chart repo>'],
      license: 'unknown',
      category: 'security',
      curated_by: ['cncf-incubating'],
      ...extra,
    },
  };
}

test('yaml-edit: apply 1 add — new entry appears in output YAML', () => {
  const cs = { adds: [makeAdd('beta')], updates: [] };
  const out = applyChangeset(SAMPLE_YAML, cs);
  assert.match(out, /- name: beta/);
  assert.match(out, /chart: beta/);
  // Existing entries still present.
  assert.match(out, /- name: alpha/);
  assert.match(out, /- name: charlie/);
});

test('yaml-edit: apply 1 update — only the diff fields change; comments preserved', () => {
  const cs = {
    adds: [],
    updates: [
      {
        plugin: 'test-plugin',
        entry: { name: 'alpha' },
        diff: { version: { from: undefined, to: '2.0.0' } },
      },
    ],
  };
  const out = applyChangeset(SAMPLE_YAML, cs);
  // Top comment kept.
  assert.match(out, /# top-level comment kept/);
  // alpha repo comment kept (was inline on the repo: line).
  assert.match(out, /# alpha repo comment/);
  // Other alpha fields unchanged.
  assert.match(out, /chart: alpha/);
  assert.match(out, /license: Apache-2.0/);
  // version field added.
  assert.match(out, /version: 2\.0\.0/);
  // charlie untouched.
  assert.match(out, /chart: charlie/);
});

test('yaml-edit: apply 0 ops → input === output (idempotent, no formatting churn)', () => {
  const out = applyChangeset(SAMPLE_YAML, { adds: [], updates: [] });
  assert.equal(out, SAMPLE_YAML);
});

test('yaml-edit: apply update for non-existent entry throws', () => {
  const cs = {
    adds: [],
    updates: [
      {
        plugin: 'test-plugin',
        entry: { name: 'does-not-exist' },
        diff: { version: { to: '9.9.9' } },
      },
    ],
  };
  assert.throws(() => applyChangeset(SAMPLE_YAML, cs), /no such entry/);
});

test('yaml-edit: apply 2 updates to same entry — both fields change', () => {
  const cs = {
    adds: [],
    updates: [
      {
        plugin: 'test-plugin',
        entry: { name: 'charlie' },
        diff: {
          chart: { from: 'charlie', to: 'charlie-v2' },
          version: { to: '3.0.0' },
        },
      },
    ],
  };
  const out = applyChangeset(SAMPLE_YAML, cs);
  assert.match(out, /chart: charlie-v2/);
  assert.match(out, /version: 3\.0\.0/);
  // Untouched fields still there.
  assert.match(out, /license: MIT/);
  assert.match(out, /repo: https:\/\/charts\.charlie\.test/);
});

test('yaml-edit: adds insert in alphabetical position by name', () => {
  // "bravo" should land between alpha and charlie.
  const cs = { adds: [makeAdd('bravo')], updates: [] };
  const out = applyChangeset(SAMPLE_YAML, cs);
  const idxAlpha = out.indexOf('- name: alpha');
  const idxBravo = out.indexOf('- name: bravo');
  const idxCharlie = out.indexOf('- name: charlie');
  assert.ok(idxAlpha >= 0 && idxBravo >= 0 && idxCharlie >= 0, 'all three names must appear');
  assert.ok(idxAlpha < idxBravo, 'alpha must precede bravo');
  assert.ok(idxBravo < idxCharlie, 'bravo must precede charlie');
});

test('yaml-edit: scanner-internal underscore fields are stripped on add', () => {
  const cs = {
    adds: [makeAdd('zulu', { _eks_blueprints_path: 'lib/addons/zulu', _scanner_meta: 'foo' })],
    updates: [],
  };
  const out = applyChangeset(SAMPLE_YAML, cs);
  // zulu present.
  assert.match(out, /- name: zulu/);
  // Underscore fields NOT in output.
  assert.ok(!out.includes('_eks_blueprints_path'));
  assert.ok(!out.includes('_scanner_meta'));
});

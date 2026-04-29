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

import { applyChangeset, assertValidName, VALID_NAME_RE } from './yaml-edit.mjs';

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

/* ------------------------------------------------------------------ */
/* M3 — duplicate adds no longer throw (V123-PR-F3)                     */
/* ------------------------------------------------------------------ */

test('yaml-edit: duplicate add for an existing entry name is silently no-op (M3)', () => {
  // Pre-fix this threw "add for 'alpha' but entry already exists" and
  // aborted the entire scan run. Post-fix: the duplicate is dropped
  // (the upstream `dedupAdds()` in changeset.mjs is the visibility
  // path; this is the belt-and-braces fallback). The first occurrence
  // — the curated `alpha` already in the YAML — survives unchanged.
  const cs = { adds: [makeAdd('alpha')], updates: [] };
  const out = applyChangeset(SAMPLE_YAML, cs);
  // No throw → reach this line. Output equals input modulo formatting.
  // alpha entry is still present (one occurrence; not duplicated).
  const occurrences = (out.match(/^\s*-\s*name:\s*alpha\s*$/gm) || []).length;
  assert.equal(occurrences, 1, 'alpha must appear exactly once after duplicate-add no-op');
  // No spurious "version: 0.1.0" leak from the fake add (the curated
  // alpha didn't have a version field, and the no-op must not have
  // injected one).
  assert.doesNotMatch(out, /version: 0\.1\.0/);
});

/* ------------------------------------------------------------------ */
/* L2 — name regex validation (V123-PR-F3)                              */
/* ------------------------------------------------------------------ */

test('yaml-edit: assertValidName accepts curated catalog names', () => {
  // Sample of names from `catalog/addons.yaml` — verified against
  // `^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$` at story-prep time.
  const ok = [
    'cert-manager', 'external-dns', 'argo-workflows', 'kube-prometheus-stack',
    'cloudnative-pg', 'metrics-server', 'aws-load-balancer-controller',
    'k8sgpt', 'opentelemetry-operator',
  ];
  for (const n of ok) {
    assert.doesNotThrow(() => assertValidName(n), `name '${n}' should pass`);
  }
});

test('yaml-edit: assertValidName rejects uppercase letters', () => {
  assert.throws(
    () => assertValidName('Foo-Bar'),
    /invalid entry name 'Foo-Bar'/,
  );
});

test('yaml-edit: assertValidName rejects spaces', () => {
  assert.throws(
    () => assertValidName('foo bar'),
    /invalid entry name 'foo bar'/,
  );
});

test('yaml-edit: assertValidName rejects leading dot', () => {
  assert.throws(
    () => assertValidName('.foo'),
    /invalid entry name '\.foo'/,
  );
});

test('yaml-edit: assertValidName rejects trailing dash', () => {
  assert.throws(
    () => assertValidName('foo-'),
    /invalid entry name 'foo-'/,
  );
});

test('yaml-edit: assertValidName rejects empty / non-string', () => {
  assert.throws(() => assertValidName(''), /invalid entry name/);
  assert.throws(() => assertValidName(null), /invalid entry name/);
  assert.throws(() => assertValidName(undefined), /invalid entry name/);
  assert.throws(() => assertValidName(42), /invalid entry name/);
});

test('yaml-edit: assertValidName accepts single character', () => {
  // The relaxed shape `^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$` is required so
  // a hypothetical 1-char addon name would not break the regex. None
  // currently exist in the catalog, but the relaxation is defensive.
  assert.doesNotThrow(() => assertValidName('x'));
  assert.doesNotThrow(() => assertValidName('1'));
});

test('yaml-edit: applyChangeset rejects invalid name on add (L2)', () => {
  const cs = { adds: [makeAdd('Bad-Name')], updates: [] };
  assert.throws(
    () => applyChangeset(SAMPLE_YAML, cs),
    /invalid entry name 'Bad-Name'/,
  );
});

test('yaml-edit: applyChangeset rejects invalid name on update (L2)', () => {
  const cs = {
    adds: [],
    updates: [
      {
        plugin: 'test-plugin',
        entry: { name: 'has spaces' },
        diff: { version: { to: '1.0.0' } },
      },
    ],
  };
  assert.throws(
    () => applyChangeset(SAMPLE_YAML, cs),
    /invalid entry name 'has spaces'/,
  );
});

test('yaml-edit: VALID_NAME_RE constant is exported', () => {
  assert.ok(VALID_NAME_RE instanceof RegExp);
  assert.ok(VALID_NAME_RE.test('cert-manager'));
  assert.ok(!VALID_NAME_RE.test('Cert-Manager'));
});

/**
 * cncf-landscape.test.mjs — unit tests for the V123-3.2 plugin.
 *
 * These tests run the plugin directly (no spawnSync) with a stubbed
 * ctx — the integration test in scripts/catalog-scan.test.mjs covers
 * the file://-URL path through the real scanner harness.
 *
 * Test cases (per the V123-3.2 brief Tier 2 #6):
 *   1. happy path — fixture produces 4 normalized entries with the
 *      right shape (per the fixture's expected mapping).
 *   2. maturity filter — sandbox item is skipped.
 *   3. helm filter — no-chart item is skipped.
 *   4. category filter — Container Runtime + unmapped subcategory
 *      both skipped.
 *   5. slug normalization — exercises slugify() across edge cases.
 *   6. network error tolerance — ctx.http throws → plugin throws
 *      (the scanner harness's per-plugin error isolation handles it
 *      at the integration level).
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

import { fetch as cncfFetch, slugify } from './cncf-landscape.mjs';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const FIXTURE_PATH = resolve(
  __dirname,
  '..',
  '__tests__',
  'fixtures',
  'landscape.fixture.yaml',
);

/** Build a Response-like wrapper around the fixture's text body. */
async function fixtureResponse() {
  const body = await readFile(FIXTURE_PATH, 'utf8');
  return {
    ok: true,
    status: 200,
    statusText: 'OK',
    text: async () => body,
  };
}

/** Build a stub ctx that records logger output for assertions. */
function makeCtx({ httpImpl } = {}) {
  const logs = [];
  function record(level) {
    return (msg, attrs) => logs.push({ level, msg, ...(attrs ?? {}) });
  }
  const logger = {
    info: record('info'),
    warn: record('warn'),
    error: record('error'),
    child: () => logger,
  };
  return {
    logs,
    ctx: {
      logger,
      abortSignal: undefined,
      http: httpImpl ?? (async () => fixtureResponse()),
    },
  };
}

/* ------------------------------------------------------------------ */
/* Happy path                                                           */
/* ------------------------------------------------------------------ */

test('cncf-landscape: happy path produces 4 proposals from fixture', async () => {
  const { ctx, logs } = makeCtx();
  const out = await cncfFetch(ctx);
  assert.equal(out.length, 4, `expected 4 entries; got ${out.length}: ${out.map((e) => e.name).join(', ')}`);

  const byName = new Map(out.map((e) => [e.name, e]));
  // Adds (new names not in tiny.yaml).
  assert.ok(byName.has('argo-cd-fixture'), 'argo-cd-fixture should be proposed');
  assert.ok(byName.has('fixture-monitor-x'), 'fixture-monitor-x should be proposed');
  // "Updates" are surfaced as proposals here too — diff happens later
  // in the harness; the plugin just emits normalized entries.
  assert.ok(byName.has('cert-manager'), 'cert-manager should be proposed');
  assert.ok(byName.has('external-dns'), 'external-dns should be proposed');

  // Shape check on one entry — the gitops one with version.
  const argo = byName.get('argo-cd-fixture');
  assert.equal(argo.category, 'gitops');
  assert.deepEqual(argo.curated_by, ['cncf-graduated']);
  assert.equal(argo.chart, 'argo-cd');
  // repo is the chart-repo parent path (without the chart name); the
  // reviewer typically replaces with the upstream chart-repo URL.
  assert.equal(argo.repo, 'https://artifacthub.io/packages/helm/argo');
  assert.equal(argo.version, '7.6.10');
  assert.equal(argo.license, 'unknown');
  assert.equal(argo.default_namespace, 'argo-cd-fixture-system');
  assert.deepEqual(argo.maintainers, ['<TODO: derive from chart repo>']);
  assert.equal(argo.description, '<TODO: human description>');

  // Incubating maturity → cncf-incubating tag.
  const monitor = byName.get('fixture-monitor-x');
  assert.deepEqual(monitor.curated_by, ['cncf-incubating']);
  assert.equal(monitor.category, 'observability');
  // No version field on this fixture entry → no `version` emitted.
  assert.equal('version' in monitor, false, 'no version → no key in entry');

  // Filter summary log was emitted.
  const summary = logs.find((l) => l.msg === 'landscape filter summary');
  assert.ok(summary, 'expected landscape filter summary log');
  assert.equal(summary.kept, 4);
  assert.equal(summary.skipped_maturity, 1, 'sandbox-skipped counted once');
  assert.equal(summary.skipped_helm, 1, 'graduated-nohelm counted once');
  assert.equal(summary.skipped_category, 2, 'runtime-skipped + unmapped-category');
});

/* ------------------------------------------------------------------ */
/* Filters                                                              */
/* ------------------------------------------------------------------ */

test('cncf-landscape: maturity filter skips sandbox', async () => {
  const { ctx } = makeCtx();
  const out = await cncfFetch(ctx);
  assert.ok(
    !out.some((e) => e.name === 'sandbox-skipped'),
    'sandbox item must NOT appear in proposals',
  );
});

test('cncf-landscape: helm filter skips items without chart reference', async () => {
  const { ctx } = makeCtx();
  const out = await cncfFetch(ctx);
  assert.ok(
    !out.some((e) => e.name === 'graduated-nohelm'),
    'item without helm reference must NOT appear',
  );
});

test('cncf-landscape: category filter skips null-mapped + unmapped subcategories', async () => {
  const { ctx } = makeCtx();
  const out = await cncfFetch(ctx);
  assert.ok(
    !out.some((e) => e.name === 'runtime-skipped'),
    'Container Runtime (mapped to null) must NOT appear',
  );
  assert.ok(
    !out.some((e) => e.name === 'unmapped-category'),
    'unknown subcategory must NOT appear',
  );
});

/* ------------------------------------------------------------------ */
/* Slug normalization                                                   */
/* ------------------------------------------------------------------ */

test('cncf-landscape: slugify normalizes per schema regex', () => {
  assert.equal(slugify('cert-manager'), 'cert-manager');
  assert.equal(slugify('Cert-Manager 2.0!'), 'cert-manager-2-0');
  assert.equal(slugify('  CNCF Project  '), 'cncf-project');
  assert.equal(slugify('Foo___Bar'), 'foo-bar');
  assert.equal(slugify('multiple---dashes'), 'multiple-dashes');
  assert.equal(slugify('-leading-and-trailing-'), 'leading-and-trailing');
  assert.equal(slugify(''), '');
  assert.equal(slugify(null), '');
  assert.equal(slugify(undefined), '');
  // 70-char input clipped to 63 + trailing-dash re-trimmed.
  const long = 'a'.repeat(70);
  const clipped = slugify(long);
  assert.equal(clipped.length, 63);
  assert.match(clipped, /^[a-z0-9][a-z0-9-]*[a-z0-9]$/, 'matches schema regex');
  // Pure-symbol input → empty (no usable slug).
  assert.equal(slugify('!!!'), '');
});

/* ------------------------------------------------------------------ */
/* Network error tolerance                                              */
/* ------------------------------------------------------------------ */

test('cncf-landscape: network error propagates to harness (per-plugin isolation)', async () => {
  const { ctx } = makeCtx({
    httpImpl: async () => {
      throw new Error('upstream blip');
    },
  });
  await assert.rejects(() => cncfFetch(ctx), /upstream blip/);
});

test('cncf-landscape: non-2xx HTTP response throws', async () => {
  const { ctx } = makeCtx({
    httpImpl: async () => ({
      ok: false,
      status: 503,
      statusText: 'Service Unavailable',
      text: async () => '',
    }),
  });
  await assert.rejects(() => cncfFetch(ctx), /HTTP 503/);
});

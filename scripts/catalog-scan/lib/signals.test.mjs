/**
 * signals.test.mjs — V123-3.4 Tier 3 #7.
 *
 * Six cases per the V123-3.4 brief:
 *   1. scorecardForRepo happy path — stubbed http returns score JSON
 *      → returns {score, updated}.
 *   2. scorecardForRepo 404 → 'unknown'.
 *   3. scorecardForRepo non-github URL → 'unknown' (no API call).
 *   4. chartIndexResolves happy → 'ok'.
 *   5. chartIndexResolves chart missing → 'missing'.
 *   6. chartIndexResolves oci:// → 'oci-not-checked'.
 * Plus: licenseFromChart classification (allow-list ok / non-allow-list
 * flagged / absent unknown) — fast, parallel to the brief intent.
 */

import test from 'node:test';
import assert from 'node:assert/strict';

import { scorecardForRepo, chartIndexResolves, licenseFromChart } from './signals.mjs';

function makeJsonResponse(body, { ok = true, status = 200 } = {}) {
  return {
    ok,
    status,
    statusText: ok ? 'OK' : 'Not OK',
    json: async () => body,
    text: async () => JSON.stringify(body),
    headers: { get: () => null },
  };
}

function makeTextResponse(text, { ok = true, status = 200 } = {}) {
  return {
    ok,
    status,
    statusText: ok ? 'OK' : 'Not OK',
    text: async () => text,
    json: async () => JSON.parse(text),
    headers: { get: () => null },
  };
}

function makeRecordedLogger() {
  const records = [];
  function record(level) {
    return (msg, attrs) => records.push({ level, msg, ...(attrs ?? {}) });
  }
  const logger = {
    records,
    info: record('info'),
    warn: record('warn'),
    error: record('error'),
    child: () => logger,
  };
  return logger;
}

/* ------------------------------------------------------------------ */
/* scorecardForRepo                                                     */
/* ------------------------------------------------------------------ */

test('scorecardForRepo: happy path — github URL returns {score, updated}', async () => {
  const calls = [];
  const ctx = {
    http: async (url) => {
      calls.push(url);
      return makeJsonResponse({ score: 8.4, date: '2026-04-20', repo: { name: 'foo/bar' } });
    },
  };
  const out = await scorecardForRepo('https://github.com/foo/bar', ctx);
  assert.deepEqual(out, { score: 8.4, updated: '2026-04-20' });
  assert.equal(calls.length, 1);
  assert.match(calls[0], /api\.securityscorecards\.dev\/projects\/github\.com\/foo\/bar/);
});

test('scorecardForRepo: 404 → "unknown" (never-scored repo)', async () => {
  const ctx = {
    http: async () => makeJsonResponse({}, { ok: false, status: 404 }),
  };
  const out = await scorecardForRepo('https://github.com/foo/bar', ctx);
  assert.equal(out, 'unknown');
});

test('scorecardForRepo: non-github URL → "unknown" without HTTP call', async () => {
  let called = false;
  const ctx = {
    http: async () => { called = true; return makeJsonResponse({}); },
  };
  const out = await scorecardForRepo('https://gitlab.example/foo/bar', ctx);
  assert.equal(out, 'unknown');
  assert.equal(called, false, 'http must NOT be invoked for non-github URLs');
});

test('scorecardForRepo: thrown error → "unknown" + warn log', async () => {
  const logger = makeRecordedLogger();
  const ctx = {
    http: async () => { throw new Error('econnreset'); },
    logger,
  };
  const out = await scorecardForRepo('https://github.com/foo/bar', ctx);
  assert.equal(out, 'unknown');
  assert.ok(logger.records.find((r) => r.level === 'warn'), 'expected warn log on http error');
});

/* ------------------------------------------------------------------ */
/* chartIndexResolves                                                   */
/* ------------------------------------------------------------------ */

test('chartIndexResolves: happy path — chart present in index → "ok"', async () => {
  const indexYaml = `apiVersion: v1
entries:
  cert-manager:
    - name: cert-manager
      version: v1.20.2
      license: Apache-2.0
generated: 2026-04-26T00:00:00Z
`;
  const ctx = {
    http: async () => makeTextResponse(indexYaml),
    _cache: new Map(),
  };
  const out = await chartIndexResolves('https://charts.jetstack.io', 'cert-manager', ctx);
  assert.equal(out, 'ok');
});

test('chartIndexResolves: chart missing from index → "missing"', async () => {
  const indexYaml = `apiVersion: v1
entries:
  some-other-chart:
    - name: some-other-chart
      version: 1.0.0
`;
  const ctx = {
    http: async () => makeTextResponse(indexYaml),
    _cache: new Map(),
  };
  const out = await chartIndexResolves('https://charts.example.test', 'cert-manager', ctx);
  assert.equal(out, 'missing');
});

test('chartIndexResolves: oci:// URL → "oci-not-checked" without HTTP', async () => {
  let called = false;
  const ctx = {
    http: async () => { called = true; return makeTextResponse(''); },
  };
  const out = await chartIndexResolves('oci://public.ecr.aws/karpenter', 'karpenter', ctx);
  assert.equal(out, 'oci-not-checked');
  assert.equal(called, false, 'http must NOT be invoked for oci:// URLs');
});

test('chartIndexResolves: shares parsed index across calls via _cache', async () => {
  let calls = 0;
  const indexYaml = `apiVersion: v1
entries:
  one:
    - name: one
      version: 1.0.0
  two:
    - name: two
      version: 2.0.0
`;
  const ctx = {
    http: async () => { calls += 1; return makeTextResponse(indexYaml); },
    _cache: new Map(),
  };
  const a = await chartIndexResolves('https://charts.example.test', 'one', ctx);
  const b = await chartIndexResolves('https://charts.example.test', 'two', ctx);
  assert.equal(a, 'ok');
  assert.equal(b, 'ok');
  assert.equal(calls, 1, 'only one HTTP call expected when _cache is shared');
});

/* ------------------------------------------------------------------ */
/* licenseFromChart                                                     */
/* ------------------------------------------------------------------ */

test('licenseFromChart: allow-list license → status "ok"', () => {
  const idx = { entries: { 'cert-manager': [{ name: 'cert-manager', license: 'Apache-2.0' }] } };
  const out = licenseFromChart(idx, 'cert-manager');
  assert.deepEqual(out, { value: 'Apache-2.0', status: 'ok' });
});

test('licenseFromChart: non-allow-list license → status "flagged"', () => {
  const idx = { entries: { foo: [{ name: 'foo', license: 'GPL-3.0-only' }] } };
  const out = licenseFromChart(idx, 'foo');
  assert.deepEqual(out, { value: 'GPL-3.0-only', status: 'flagged' });
});

test('licenseFromChart: missing license → status "unknown"', () => {
  const idx = { entries: { foo: [{ name: 'foo' }] } };
  const out = licenseFromChart(idx, 'foo');
  assert.deepEqual(out, { value: 'unknown', status: 'unknown' });
});

test('licenseFromChart: artifacthub.io/license annotation takes precedence', () => {
  const idx = {
    entries: {
      foo: [{ name: 'foo', annotations: { 'artifacthub.io/license': 'MIT' }, license: 'GPL-3.0-only' }],
    },
  };
  const out = licenseFromChart(idx, 'foo');
  assert.deepEqual(out, { value: 'MIT', status: 'ok' });
});

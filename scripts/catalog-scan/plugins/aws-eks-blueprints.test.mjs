/**
 * aws-eks-blueprints.test.mjs — unit tests for the V123-3.3 plugin.
 *
 * These tests run the plugin directly (no spawnSync) with a stubbed
 * ctx — the integration test in scripts/catalog-scan.test.mjs covers
 * the full HTTP-server-backed path through the real scanner harness.
 *
 * Test cases (per the V123-3.3 brief Tier 2 #4):
 *   1. happy path — fixture chain produces 4 normalized entries
 *      (broken-addon skipped + index.ts file filtered).
 *   2. type filter — `index.ts` (type=file) at the top-level listing
 *      is filtered out (not enumerated as a dir).
 *   3. broken extraction — `broken-addon` with no chart constants is
 *      skipped + an info log is emitted.
 *   4. slug normalization — exercises slugify() edge cases identical
 *      to V123-3.2 (intentional inline duplication).
 *   5. GITHUB_TOKEN propagation — when env is set, the wrapped http
 *      function passes `Authorization: Bearer <token>` in headers.
 *   6. rate-limit logging — `x-ratelimit-remaining=5` warns,
 *      `=5000` infos.
 *   7. network error propagation — top-level dir-list throw → plugin
 *      throws (per-plugin isolation lives in the harness).
 *   8. empty dir — top-level returns [] → plugin returns [] cleanly.
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

import { fetch as eksFetch, slugify } from './aws-eks-blueprints.mjs';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const FIXTURE_DIR = resolve(
  __dirname,
  '..',
  '__tests__',
  'fixtures',
  'eks-blueprints',
);

const BASE_URL = 'http://example.test';
// Mirror the env var the plugin reads — using a stable BASE_URL keeps
// the fixture substitutions deterministic across cases.
const API_BASE = `${BASE_URL}/contents`;

/* ------------------------------------------------------------------ */
/* Fixture loader — substitutes ${BASE_URL} into JSON files            */
/* ------------------------------------------------------------------ */

async function loadFixture(filename) {
  const text = await readFile(resolve(FIXTURE_DIR, filename), 'utf8');
  return text.replaceAll('${BASE_URL}', BASE_URL);
}

async function loadJsonFixture(filename) {
  return JSON.parse(await loadFixture(filename));
}

/* ------------------------------------------------------------------ */
/* Recorded-logger helper (copied inline from cncf-landscape.test.mjs   */
/* per V123-3.3 brief — small enough that duplication is preferred     */
/* over yet another lib module).                                       */
/* ------------------------------------------------------------------ */

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
/* Default httpStub — chained fixture responses                         */
/* ------------------------------------------------------------------ */

/**
 * Build a stub `ctx.http` that resolves URLs against the fixture set.
 * Tracks every call's URL + headers so tests can spy on auth/rate
 * behavior. Optional overrides:
 *   - `errorFor(url)` — return an Error to throw for matching URLs
 *   - `rateLimit` — value used for `x-ratelimit-remaining` header on
 *     contents API responses (default '4900')
 */
function buildHttpStub({ rateLimit = '4900', errorOn } = {}) {
  const calls = [];
  async function http(url, opts = {}) {
    calls.push({ url, opts });
    if (errorOn && errorOn === url) {
      throw new Error('upstream connection blip');
    }
    // Map URL → fixture file.
    const path = url.startsWith(API_BASE) ? url.slice(API_BASE.length) : url;
    if (path === '/lib/addons') {
      return jsonResponse(await loadFixture('contents-lib-addons.json'), { rateLimit });
    }
    if (path.startsWith('/lib/addons/')) {
      const addon = path.slice('/lib/addons/'.length);
      return jsonResponse(await loadFixture(`contents-${addon}.json`), { rateLimit });
    }
    // raw .ts files served from BASE_URL/raw/<addon>.ts (no rate-limit
    // header — matches GitHub's raw.githubusercontent.com behavior).
    if (url.startsWith(`${BASE_URL}/raw/`)) {
      const fileName = url.slice(`${BASE_URL}/raw/`.length);
      const text = await loadFixture(fileName);
      return textResponse(text);
    }
    throw new Error(`unmocked URL in test stub: ${url}`);
  }
  return { http, calls };
}

function jsonResponse(bodyText, { rateLimit } = {}) {
  return {
    ok: true,
    status: 200,
    statusText: 'OK',
    headers: {
      get(name) {
        if (name.toLowerCase() === 'x-ratelimit-remaining') return rateLimit ?? null;
        return null;
      },
    },
    json: async () => JSON.parse(bodyText),
    text: async () => bodyText,
  };
}

function textResponse(text) {
  return {
    ok: true,
    status: 200,
    statusText: 'OK',
    headers: {
      get() {
        return null;
      },
    },
    json: async () => JSON.parse(text),
    text: async () => text,
  };
}

/* ------------------------------------------------------------------ */
/* Common ctx + env reset                                               */
/* ------------------------------------------------------------------ */

function withEnv(t, key, value) {
  const prior = process.env[key];
  if (value === undefined) delete process.env[key];
  else process.env[key] = value;
  t.after(() => {
    if (prior === undefined) delete process.env[key];
    else process.env[key] = prior;
  });
}

function makeCtx(httpStub) {
  const logger = makeRecordedLogger();
  return {
    logger,
    ctx: {
      logger,
      abortSignal: undefined,
      http: httpStub.http,
    },
  };
}

/* ------------------------------------------------------------------ */
/* Happy path                                                           */
/* ------------------------------------------------------------------ */

test('aws-eks-blueprints: happy path produces 4 entries from fixture', async (t) => {
  withEnv(t, 'SHARKO_EKS_BLUEPRINTS_API_BASE', API_BASE);
  withEnv(t, 'GITHUB_TOKEN', undefined);
  const stub = buildHttpStub();
  const { ctx, logger } = makeCtx(stub);

  const out = await eksFetch(ctx);

  // Top fixture has 5 dirs (3 curated names: karpenter, cert-manager,
  // external-dns + 1 new: fancy-new-addon + 1 broken: broken-addon)
  // + 1 file (index.ts). The file is filtered by type. Of the 5 dirs,
  // 4 produce entries; broken-addon is skipped (no chart constants).
  assert.equal(out.length, 4, `expected 4 entries; got ${out.length}: ${out.map((e) => e.name).join(', ')}`);

  const byName = new Map(out.map((e) => [e.name, e]));
  assert.ok(byName.has('karpenter'), 'karpenter must be proposed');
  assert.ok(byName.has('cert-manager'), 'cert-manager must be proposed');
  assert.ok(byName.has('external-dns'), 'external-dns must be proposed');
  assert.ok(byName.has('fancy-new-addon'), 'fancy-new-addon must be proposed');
  assert.ok(!byName.has('broken-addon'), 'broken-addon must be skipped');

  // Shape check on cert-manager — defaultProps style fixture.
  const cm = byName.get('cert-manager');
  assert.equal(cm.chart, 'cert-manager');
  assert.equal(cm.repo, 'https://charts.jetstack.io');
  assert.equal(cm.version, 'v1.19.4');
  assert.equal(cm.default_namespace, 'cert-manager');
  assert.equal(cm.category, 'security'); // 'cert' keyword
  assert.deepEqual(cm.curated_by, ['aws-eks-blueprints']);
  assert.equal(cm.license, 'unknown');
  assert.deepEqual(cm.maintainers, ['<TODO: derive from chart repo>']);
  assert.equal(cm.description, '<TODO: human description>');

  // Shape check on fancy-new-addon — HELM_CHART_* style fixture.
  const fancy = byName.get('fancy-new-addon');
  assert.equal(fancy.chart, 'fancy-new-addon');
  assert.equal(fancy.repo, 'https://charts.fancy.example.invalid');
  assert.equal(fancy.version, '0.4.2');
  assert.equal(fancy.default_namespace, 'fancy-system');
  assert.equal(fancy.category, 'developer-tools'); // no keyword match
  assert.equal(fancy._eks_blueprints_path, 'lib/addons/fancy-new-addon');

  // Category inference exercised: external-dns matches the 'dns' keyword.
  assert.equal(byName.get('external-dns').category, 'networking');
  // karpenter slug matches the 'karpenter' keyword → autoscaling.
  assert.equal(
    byName.get('karpenter').category,
    'autoscaling',
    'karpenter should map to autoscaling via karpenter keyword',
  );

  // Filter summary log was emitted.
  const summary = logger.records.find((l) => l.msg === 'aws-eks-blueprints scan summary');
  assert.ok(summary, 'expected aws-eks-blueprints scan summary log');
  assert.equal(summary.kept, 4);
  assert.equal(summary.dirs, 5, '5 dirs enumerated (file filtered out)');
  assert.equal(summary.skipped_no_extract, 1, 'broken-addon counted once');
});

/* ------------------------------------------------------------------ */
/* Type filter                                                          */
/* ------------------------------------------------------------------ */

test('aws-eks-blueprints: top-level type=file entry is filtered out', async (t) => {
  withEnv(t, 'SHARKO_EKS_BLUEPRINTS_API_BASE', API_BASE);
  withEnv(t, 'GITHUB_TOKEN', undefined);
  const stub = buildHttpStub();
  const { ctx, logger } = makeCtx(stub);
  await eksFetch(ctx);

  // The plugin must NOT have called the dir-listing endpoint for `index.ts`.
  const indexTsFetched = stub.calls.find((c) => c.url.includes('/lib/addons/index.ts'));
  assert.equal(indexTsFetched, undefined, 'index.ts (type=file) must not be enumerated as a dir');

  // The discovery log notes the file got filtered.
  const discovery = logger.records.find((l) => l.msg === 'discovered addon dirs');
  assert.ok(discovery, 'discovered addon dirs log must fire');
  assert.equal(discovery.dirs, 5);
  assert.equal(discovery.files_skipped, 1);
});

/* ------------------------------------------------------------------ */
/* Broken extraction                                                    */
/* ------------------------------------------------------------------ */

test('aws-eks-blueprints: broken extraction skipped with info log', async (t) => {
  withEnv(t, 'SHARKO_EKS_BLUEPRINTS_API_BASE', API_BASE);
  withEnv(t, 'GITHUB_TOKEN', undefined);
  const stub = buildHttpStub();
  const { ctx, logger } = makeCtx(stub);
  const out = await eksFetch(ctx);

  assert.ok(!out.some((e) => e.name === 'broken-addon'), 'broken-addon must NOT appear in output');
  const skipLog = logger.records.find(
    (r) =>
      r.level === 'info' &&
      r.msg === 'addon source lacks chart+repo references; skipping' &&
      r.addon === 'broken-addon',
  );
  assert.ok(skipLog, 'expected info-level skip log for broken-addon');
});

/* ------------------------------------------------------------------ */
/* Slug normalization                                                   */
/* ------------------------------------------------------------------ */

test('aws-eks-blueprints: slugify normalizes per schema regex', () => {
  assert.equal(slugify('cert-manager'), 'cert-manager');
  assert.equal(slugify('Aws-Load-Balancer-Controller!'), 'aws-load-balancer-controller');
  assert.equal(slugify('  Some Addon  '), 'some-addon');
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
  assert.equal(slugify('!!!'), '');
});

/* ------------------------------------------------------------------ */
/* GITHUB_TOKEN propagation                                             */
/* ------------------------------------------------------------------ */

test('aws-eks-blueprints: GITHUB_TOKEN propagates to Authorization header', async (t) => {
  withEnv(t, 'SHARKO_EKS_BLUEPRINTS_API_BASE', API_BASE);
  withEnv(t, 'GITHUB_TOKEN', 'ghp_fixture_test_token_xxx');
  const stub = buildHttpStub();
  const { ctx } = makeCtx(stub);
  await eksFetch(ctx);

  // Every Contents API call must carry the bearer header.
  const apiCalls = stub.calls.filter((c) => c.url.startsWith(API_BASE));
  assert.ok(apiCalls.length > 0, 'expected at least one contents API call');
  for (const call of apiCalls) {
    const headers = call.opts?.headers ?? {};
    assert.equal(
      headers.Authorization,
      'Bearer ghp_fixture_test_token_xxx',
      `call to ${call.url} must include bearer auth`,
    );
    assert.equal(headers['User-Agent'], 'sharko-catalog-scan/1.0');
    assert.equal(headers.Accept, 'application/vnd.github+json');
  }
});

test('aws-eks-blueprints: GITHUB_TOKEN does NOT propagate to download_url fetches (M2)', async (t) => {
  // V123-PR-F3 / M2: pre-fix, the bearer token attached to every call
  // including the raw .ts file download. That token works at
  // raw.githubusercontent.com today but would leak to a third-party
  // origin if a future plugin pointed `download_url` elsewhere.
  // Post-fix: only api.github.com / configured apiBase URLs get the
  // Authorization header; raw downloads get a no-auth header set.
  withEnv(t, 'SHARKO_EKS_BLUEPRINTS_API_BASE', API_BASE);
  withEnv(t, 'GITHUB_TOKEN', 'ghp_fixture_test_token_xxx');
  const stub = buildHttpStub();
  const { ctx } = makeCtx(stub);
  await eksFetch(ctx);

  // Partition calls by URL prefix.
  const apiCalls = stub.calls.filter((c) => c.url.startsWith(API_BASE));
  const rawCalls = stub.calls.filter((c) => c.url.startsWith(`${BASE_URL}/raw/`));
  assert.ok(apiCalls.length > 0, 'expected at least one Contents API call');
  assert.ok(rawCalls.length > 0, 'expected at least one raw download_url call');

  // API calls carry auth.
  for (const call of apiCalls) {
    assert.equal(
      call.opts?.headers?.Authorization,
      'Bearer ghp_fixture_test_token_xxx',
      `API call ${call.url} MUST carry bearer auth`,
    );
  }

  // download_url calls MUST NOT carry auth — this is the bug fix.
  for (const call of rawCalls) {
    assert.equal(
      call.opts?.headers?.Authorization,
      undefined,
      `download_url call ${call.url} MUST NOT carry bearer auth (M2 token-leak guard)`,
    );
    // Sanity: User-Agent + Accept still present (the no-auth set is
    // identical EXCEPT for the missing Authorization).
    assert.equal(call.opts?.headers?.['User-Agent'], 'sharko-catalog-scan/1.0');
    assert.equal(call.opts?.headers?.Accept, 'application/vnd.github+json');
  }
});

test('aws-eks-blueprints: no GITHUB_TOKEN → no Authorization header', async (t) => {
  withEnv(t, 'SHARKO_EKS_BLUEPRINTS_API_BASE', API_BASE);
  withEnv(t, 'GITHUB_TOKEN', undefined);
  const stub = buildHttpStub();
  const { ctx } = makeCtx(stub);
  await eksFetch(ctx);

  const firstCall = stub.calls[0];
  assert.ok(firstCall, 'expected a stub call to be made');
  const auth = firstCall.opts?.headers?.Authorization;
  assert.equal(auth, undefined, 'no auth header when GITHUB_TOKEN is unset');
});

/* ------------------------------------------------------------------ */
/* Rate-limit logging                                                   */
/* ------------------------------------------------------------------ */

test('aws-eks-blueprints: rate-limit < 10 emits warn log', async (t) => {
  withEnv(t, 'SHARKO_EKS_BLUEPRINTS_API_BASE', API_BASE);
  withEnv(t, 'GITHUB_TOKEN', undefined);
  const stub = buildHttpStub({ rateLimit: '5' });
  const { ctx, logger } = makeCtx(stub);
  await eksFetch(ctx);

  const warnLog = logger.records.find(
    (r) => r.level === 'warn' && r.msg.includes('rate-limit low'),
  );
  assert.ok(warnLog, 'expected warn log when remaining < 10');
  assert.equal(warnLog.remaining, 5);
});

test('aws-eks-blueprints: rate-limit >= 10 emits info log', async (t) => {
  withEnv(t, 'SHARKO_EKS_BLUEPRINTS_API_BASE', API_BASE);
  withEnv(t, 'GITHUB_TOKEN', undefined);
  const stub = buildHttpStub({ rateLimit: '5000' });
  const { ctx, logger } = makeCtx(stub);
  await eksFetch(ctx);

  const infoLog = logger.records.find(
    (r) => r.level === 'info' && r.msg === 'GitHub API rate-limit headroom',
  );
  assert.ok(infoLog, 'expected info log when remaining >= 10');
  assert.equal(infoLog.remaining, 5000);

  const warnLog = logger.records.find(
    (r) => r.level === 'warn' && r.msg.includes('rate-limit low'),
  );
  assert.equal(warnLog, undefined, 'no warn log expected when remaining >= 10');
});

/* ------------------------------------------------------------------ */
/* Network error propagation                                            */
/* ------------------------------------------------------------------ */

test('aws-eks-blueprints: network error on dir-list propagates', async (t) => {
  withEnv(t, 'SHARKO_EKS_BLUEPRINTS_API_BASE', API_BASE);
  withEnv(t, 'GITHUB_TOKEN', undefined);
  const stub = buildHttpStub({ errorOn: `${API_BASE}/lib/addons` });
  const { ctx } = makeCtx(stub);
  await assert.rejects(() => eksFetch(ctx), /upstream connection blip/);
});

test('aws-eks-blueprints: non-2xx on dir-list throws with descriptive error', async (t) => {
  withEnv(t, 'SHARKO_EKS_BLUEPRINTS_API_BASE', API_BASE);
  withEnv(t, 'GITHUB_TOKEN', undefined);
  const calls = [];
  const ctx = {
    logger: makeRecordedLogger(),
    abortSignal: undefined,
    http: async (url, opts) => {
      calls.push({ url, opts });
      return {
        ok: false,
        status: 403,
        statusText: 'rate limit exceeded',
        headers: {
          get(name) {
            if (name.toLowerCase() === 'x-ratelimit-remaining') return '0';
            return null;
          },
        },
      };
    },
  };
  await assert.rejects(() => eksFetch(ctx), /GitHub API 403/);
});

/* ------------------------------------------------------------------ */
/* Empty top-level dir                                                  */
/* ------------------------------------------------------------------ */

/* ------------------------------------------------------------------ */
/* M7 — default_namespace extraction (V123-PR-F3)                       */
/* ------------------------------------------------------------------ */

/**
 * V123-PR-F3 / M7: pre-fix, the loose `\bnamespace\s*:\s*['"`]` regex
 * fired on ANY object literal anywhere in the source (including the
 * `defaultProps` block where the value was the placeholder string
 * `"namespace"`, which made apache-airflow render as
 * `default_namespace: "namespace"`).
 *
 * Post-fix: extraction is anchored to the `defaultProps` block AND
 * the placeholder string `"namespace"` is filtered (extractor falls
 * through to the next match or returns undefined).
 */

const APACHE_AIRFLOW_TS_FIXTURE = `
import { Construct } from 'constructs';
import { ClusterInfo, Values } from '../../spi';
import { HelmAddOn, HelmAddOnUserProps, HelmAddOnProps } from '../helm-addon';

/**
 * Configuration options for the add-on.
 */
export interface AirflowAddOnProps extends HelmAddOnUserProps {
    /**
     * Namespace where Airflow will be installed
     * @default namespace will be created if it doesn't exist
     */
    namespace?: string;
    airflowVersion?: string;
}

/**
 * Defaults options for the add-on.
 *
 * NOTE: The literal string "namespace" below is intentional — the
 * upstream cdk-eks-blueprints repo ships several addons (apache-airflow,
 * external-secrets, etc.) with a placeholder value that the runtime
 * overrides. The scanner used to capture this placeholder verbatim
 * and emit \`default_namespace: "namespace"\` in proposals, which is
 * obviously wrong. (V123-PR-F3 / M7 fix.)
 */
const defaultProps: HelmAddOnProps = {
    name: 'apache-airflow',
    namespace: "namespace",
    chart: 'airflow',
    release: 'release-name',
    repository: 'https://airflow.apache.org',
    version: '1.11.0',
    values: {
        airflowVersion: '2.5.1',
    },
};

/**
 * Implementation of the Airflow add-on.
 */
export class AirflowAddOn extends HelmAddOn {
    constructor(props?: AirflowAddOnProps) {
        super({...defaultProps as HelmAddOnUserProps, ...props});
    }

    deploy(clusterInfo: ClusterInfo): Promise<Construct> {
        const dependable = utils.dependable(\`namespace: "namespace-foo"\`);
        return Promise.resolve();
    }
}
`;

const CLOUDWATCH_TS_FIXTURE = `
import { HelmAddOn, HelmAddOnUserProps } from '../helm-addon';

const defaultProps = {
    name: 'aws-cloudwatch-metrics',
    chart: 'aws-cloudwatch-metrics',
    namespace: "amazon-cloudwatch",
    release: 'aws-cloudwatch-metrics',
    repository: 'https://aws.github.io/eks-charts',
    version: '0.0.10',
    values: {},
};

// A regex literal that incidentally contains the word namespace.
const namespaceRegex = /\\bnamespace\\s*:\\s*['"\\\`]([^'"\\\`]+)['"\\\`]/i;
`;

test('aws-eks-blueprints: default_namespace ignores literal "namespace" placeholder (M7)', async (t) => {
  // Build a single-addon stub: top-level lists ONE addon, the dir-list
  // returns a single .ts file, and the raw download serves the
  // apache-airflow fixture above. We assert that the resulting entry
  // does NOT have default_namespace="namespace".
  withEnv(t, 'SHARKO_EKS_BLUEPRINTS_API_BASE', API_BASE);
  withEnv(t, 'GITHUB_TOKEN', undefined);

  const calls = [];
  const ctx = {
    logger: makeRecordedLogger(),
    abortSignal: undefined,
    http: async (url, opts = {}) => {
      calls.push({ url, opts });
      if (url === `${API_BASE}/lib/addons`) {
        return jsonResponse(JSON.stringify([
          { name: 'apache-airflow', type: 'dir', path: 'lib/addons/apache-airflow' },
        ]), { rateLimit: '4990' });
      }
      if (url === `${API_BASE}/lib/addons/apache-airflow`) {
        return jsonResponse(JSON.stringify([
          {
            name: 'index.ts',
            type: 'file',
            download_url: `${BASE_URL}/raw/apache-airflow.ts`,
            html_url: `${BASE_URL}/blob/apache-airflow.ts`,
          },
        ]), { rateLimit: '4990' });
      }
      if (url === `${BASE_URL}/raw/apache-airflow.ts`) {
        return textResponse(APACHE_AIRFLOW_TS_FIXTURE);
      }
      throw new Error(`unmocked URL: ${url}`);
    },
  };

  const out = await eksFetch(ctx);
  assert.equal(out.length, 1);
  const e = out[0];
  assert.equal(e.name, 'apache-airflow');
  // The placeholder "namespace" must NOT survive into default_namespace.
  // Acceptable post-fix outcomes:
  //   - extractor returned undefined → fallback `apache-airflow-system`
  //   - extractor matched some OTHER namespace value, NOT "namespace"
  // Either way, the literal string "namespace" is not the result.
  assert.notEqual(
    e.default_namespace,
    'namespace',
    `default_namespace must not be the placeholder "namespace"; got: ${e.default_namespace}`,
  );
  // The fallback path produces `<slug>-system`; assert the shape so we
  // catch a future regression where a different bad value sneaks in.
  assert.equal(
    e.default_namespace,
    'apache-airflow-system',
    `expected fallback default_namespace; got ${e.default_namespace}`,
  );
});

test('aws-eks-blueprints: default_namespace extracts real value from defaultProps (M7)', async (t) => {
  // The CLOUDWATCH fixture has BOTH a real `namespace: "amazon-cloudwatch"`
  // value AND a regex literal that incidentally contains the word
  // namespace. The post-fix extractor must capture "amazon-cloudwatch"
  // (the real value) and NOT trip on the regex literal.
  withEnv(t, 'SHARKO_EKS_BLUEPRINTS_API_BASE', API_BASE);
  withEnv(t, 'GITHUB_TOKEN', undefined);

  const ctx = {
    logger: makeRecordedLogger(),
    abortSignal: undefined,
    http: async (url) => {
      if (url === `${API_BASE}/lib/addons`) {
        return jsonResponse(JSON.stringify([
          { name: 'aws-cloudwatch-metrics', type: 'dir', path: 'lib/addons/aws-cloudwatch-metrics' },
        ]), { rateLimit: '4990' });
      }
      if (url === `${API_BASE}/lib/addons/aws-cloudwatch-metrics`) {
        return jsonResponse(JSON.stringify([
          {
            name: 'index.ts',
            type: 'file',
            download_url: `${BASE_URL}/raw/cloudwatch.ts`,
            html_url: `${BASE_URL}/blob/cloudwatch.ts`,
          },
        ]), { rateLimit: '4990' });
      }
      if (url === `${BASE_URL}/raw/cloudwatch.ts`) {
        return textResponse(CLOUDWATCH_TS_FIXTURE);
      }
      throw new Error(`unmocked URL: ${url}`);
    },
  };

  const out = await eksFetch(ctx);
  assert.equal(out.length, 1);
  assert.equal(out[0].default_namespace, 'amazon-cloudwatch');
});

test('aws-eks-blueprints: empty lib/addons returns [] cleanly', async (t) => {
  withEnv(t, 'SHARKO_EKS_BLUEPRINTS_API_BASE', API_BASE);
  withEnv(t, 'GITHUB_TOKEN', undefined);
  const ctx = {
    logger: makeRecordedLogger(),
    abortSignal: undefined,
    http: async () =>
      jsonResponse('[]', { rateLimit: '4990' }),
  };
  const out = await eksFetch(ctx);
  assert.deepEqual(out, []);
});

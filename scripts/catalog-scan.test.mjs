/**
 * catalog-scan.test.mjs — integration tests for the top-level
 * catalog-scan.mjs entry point. Invokes the script as a child process
 * via node:child_process spawnSync so CLI parsing + exit codes + the
 * stdout/stderr split are actually exercised.
 *
 * Cases mirror V123-3.1 brief Tier 2 #11:
 *   1. no-changes-no-output     — stub returns same entry as catalog;
 *                                 exits 0, no file written, "no
 *                                 changes" log line emitted.
 *   2. dry-run-stdout           — stub proposes a new entry; stdout is
 *                                 valid JSON of the changeset shape,
 *                                 no file written.
 *   3. default-writes-output    — same plugin, no --dry-run; output
 *                                 file written under _dist/...
 *   4. plugin-error-isolated    — two plugins, one throws; sibling
 *                                 plugin's results still appear in
 *                                 the changeset; throwing plugin is
 *                                 recorded with error; exit code 0.
 *
 * Hermetic: no real network calls. Plugin discovery uses the
 * --plugin-dir override so scripts/catalog-scan/__tests__/stubs/ is
 * scanned in place of the production scripts/catalog-scan/plugins/.
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync, spawn } from 'node:child_process';
import { createServer } from 'node:http';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { dirname, resolve } from 'node:path';
import { mkdtemp, rm, readFile, writeFile, mkdir, stat, access } from 'node:fs/promises';
import { tmpdir } from 'node:os';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const REPO_ROOT = resolve(__dirname, '..');
const SCRIPT = resolve(REPO_ROOT, 'scripts/catalog-scan.mjs');
const FIXTURE = resolve(REPO_ROOT, 'scripts/catalog-scan/__tests__/fixtures/addons.tiny.yaml');
const STUBS_DIR = resolve(REPO_ROOT, 'scripts/catalog-scan/__tests__/stubs');
const REAL_CATALOG = resolve(REPO_ROOT, 'catalog/addons.yaml');
const REAL_PLUGIN_DIR = resolve(REPO_ROOT, 'scripts/catalog-scan/plugins');
const LANDSCAPE_FIXTURE = resolve(
  REPO_ROOT,
  'scripts/catalog-scan/__tests__/fixtures/landscape.fixture.yaml',
);
const EKS_FIXTURE_DIR = resolve(
  REPO_ROOT,
  'scripts/catalog-scan/__tests__/fixtures/eks-blueprints',
);

/**
 * Build a temp working directory containing only the plugins this
 * test wants discovered. spawnSync runs the script with this dir as
 * --plugin-dir; on any failure the dir is cleaned in `t.after`.
 */
async function makeTempPluginDir(t, pluginNames) {
  const dir = await mkdtemp(resolve(tmpdir(), 'sharko-scan-test-'));
  for (const name of pluginNames) {
    const src = resolve(STUBS_DIR, name);
    const dst = resolve(dir, name);
    const text = await readFile(src, 'utf8');
    await writeFile(dst, text, 'utf8');
  }
  t.after(() => rm(dir, { recursive: true, force: true }));
  return dir;
}

async function makeTempOutDir(t) {
  const dir = await mkdtemp(resolve(tmpdir(), 'sharko-scan-out-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  return dir;
}

function runScript(args, extraEnv) {
  return spawnSync('node', [SCRIPT, ...args], {
    cwd: REPO_ROOT,
    env: { ...process.env, NO_COLOR: '1', ...(extraEnv ?? {}) },
    encoding: 'utf8',
  });
}

async function exists(p) {
  try {
    await access(p);
    return true;
  } catch {
    return false;
  }
}

test('integration: no-changes-no-output (stub returns nothing new)', async (t) => {
  const pluginDir = await makeTempPluginDir(t, ['empty.mjs']);
  const outDir = await makeTempOutDir(t);
  const outFile = resolve(outDir, 'changeset.json');

  const res = runScript([
    '--catalog', FIXTURE,
    '--plugin-dir', pluginDir,
    '--out', outFile,
  ]);

  assert.equal(res.status, 0, `expected exit 0, got ${res.status}; stderr:\n${res.stderr}`);
  assert.equal(await exists(outFile), false, 'no-changes path must NOT write output file');
  assert.match(res.stderr, /no changes proposed/);
});

test('integration: dry-run-stdout emits valid JSON, no file written', async (t) => {
  const pluginDir = await makeTempPluginDir(t, ['proposes-add.mjs']);
  const outDir = await makeTempOutDir(t);
  const outFile = resolve(outDir, 'changeset.json');

  const res = runScript([
    '--dry-run',
    '--catalog', FIXTURE,
    '--plugin-dir', pluginDir,
    '--out', outFile,
  ]);

  assert.equal(res.status, 0, `expected exit 0, got ${res.status}; stderr:\n${res.stderr}`);
  assert.equal(await exists(outFile), false, '--dry-run must NOT write output file');

  // stdout must parse as JSON and match the changeset shape.
  const cs = JSON.parse(res.stdout);
  assert.equal(cs.schema_version, '1.0');
  assert.ok(Array.isArray(cs.scanner_runs));
  assert.ok(Array.isArray(cs.adds));
  assert.ok(Array.isArray(cs.updates));
  assert.equal(cs.adds.length, 1);
  assert.equal(cs.adds[0].entry.name, 'argo-cd');
});

test('integration: default-writes-output creates the changeset file', async (t) => {
  const pluginDir = await makeTempPluginDir(t, ['proposes-add.mjs']);
  const outDir = await makeTempOutDir(t);
  const outFile = resolve(outDir, 'changeset.json');

  const res = runScript([
    '--catalog', FIXTURE,
    '--plugin-dir', pluginDir,
    '--out', outFile,
  ]);

  assert.equal(res.status, 0, `expected exit 0, got ${res.status}; stderr:\n${res.stderr}`);
  assert.equal(await exists(outFile), true, 'default branch must write output file');
  const text = await readFile(outFile, 'utf8');
  const cs = JSON.parse(text);
  assert.equal(cs.adds.length, 1);
  assert.equal(cs.adds[0].entry.name, 'argo-cd');
  assert.equal(cs.scanner_runs.length, 1);
  assert.equal(cs.scanner_runs[0].fetched_count, 1);
  assert.equal(cs.scanner_runs[0].error, undefined, 'happy path: no error field');
});

test('integration: cncf-landscape plugin against real catalog (V123-3.2)', async () => {
  // Discovery uses the production plugins directory so the real
  // cncf-landscape.mjs is loaded. The fixture URL is fed via the
  // SHARKO_CNCF_LANDSCAPE_URL env override (file:// path so no
  // network call). Catalog is the real catalog/addons.yaml — both
  // cert-manager and external-dns appear in it, so the fixture
  // produces 2 adds + 2 updates.
  //
  // Hermetic guard for V123-3.3 plugin: point the eks-blueprints
  // base URL at an unreachable `127.0.0.1:1` so the plugin fails
  // fast (no live GitHub API call) and gets isolated by the
  // per-plugin error isolation in the scanner harness. Without
  // this override the eks plugin would hit the live API and skew
  // the assertions on adds/updates.
  const fileUrl = pathToFileURL(LANDSCAPE_FIXTURE).href;
  const res = runScript(
    [
      '--dry-run',
      '--catalog', REAL_CATALOG,
      '--plugin-dir', REAL_PLUGIN_DIR,
    ],
    {
      SHARKO_CNCF_LANDSCAPE_URL: fileUrl,
      SHARKO_EKS_BLUEPRINTS_API_BASE: 'http://127.0.0.1:1/contents',
    },
  );
  assert.equal(res.status, 0, `expected exit 0, got ${res.status}; stderr:\n${res.stderr}`);
  const cs = JSON.parse(res.stdout);
  assert.equal(cs.schema_version, '1.0');

  // Filter to cncf-landscape's adds/updates — the eks plugin errored
  // out (unreachable URL) and contributes 0 entries to the diff.
  const cncfAdds = cs.adds.filter((a) => a.plugin === 'cncf-landscape');
  const addNames = cncfAdds.map((a) => a.entry.name).sort();
  assert.deepEqual(
    addNames,
    ['argo-cd-fixture', 'fixture-monitor-x'],
    `expected exactly 2 cncf adds (argo-cd-fixture, fixture-monitor-x); got ${addNames.join(', ')}`,
  );

  const cncfUpdates = cs.updates.filter((u) => u.plugin === 'cncf-landscape');
  const updateNames = cncfUpdates.map((u) => u.entry.name).sort();
  assert.deepEqual(
    updateNames,
    ['cert-manager', 'external-dns'],
    `expected exactly 2 cncf updates (cert-manager, external-dns); got ${updateNames.join(', ')}`,
  );

  // Diff details — confirms COMPARABLE_FIELDS detection.
  const certUpdate = cncfUpdates.find((u) => u.entry.name === 'cert-manager');
  assert.ok('version' in certUpdate.diff, 'cert-manager update should diff version');
  assert.equal(certUpdate.diff.version.to, '9.9.9');
  assert.ok('repo' in certUpdate.diff, 'cert-manager update should diff repo');

  const dnsUpdate = cncfUpdates.find((u) => u.entry.name === 'external-dns');
  assert.ok('category' in dnsUpdate.diff, 'external-dns update should diff category');
  assert.equal(dnsUpdate.diff.category.from, 'security');
  assert.equal(dnsUpdate.diff.category.to, 'networking');

  // Plugin-run record is correct for cncf-landscape.
  const run = cs.scanner_runs.find((r) => r.plugin === 'cncf-landscape');
  assert.ok(run, 'cncf-landscape must appear in scanner_runs');
  assert.equal(run.fetched_count, 4, 'plugin should have returned 4 normalized entries');
  assert.equal(run.error, undefined, 'happy path: no error field');
});

/**
 * Async helper: spawn the script and resolve with {status, stdout,
 * stderr}. Required for tests that run an in-process http.createServer
 * because spawnSync would block the event loop and starve the server.
 */
function runScriptAsync(args, extraEnv) {
  return new Promise((resolveRun, rejectRun) => {
    const child = spawn('node', [SCRIPT, ...args], {
      cwd: REPO_ROOT,
      env: { ...process.env, NO_COLOR: '1', ...(extraEnv ?? {}) },
    });
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', (d) => (stdout += d.toString('utf8')));
    child.stderr.on('data', (d) => (stderr += d.toString('utf8')));
    child.on('error', rejectRun);
    child.on('close', (code) => resolveRun({ status: code, stdout, stderr }));
  });
}

/**
 * Integration test for the V123-3.3 aws-eks-blueprints plugin.
 *
 * Unlike the cncf-landscape integration test (which uses a `file://`
 * URL via the plugin's fetchAsText fallback), this plugin makes
 * MULTIPLE chained API calls (top-level dir-list, per-addon dir-list,
 * raw .ts download) — `file://` would not handle that surface. We
 * spin up a real `http.createServer` on a random port, serve fixture
 * files with `${BASE_URL}` substituted to the actual server URL, and
 * point `SHARKO_EKS_BLUEPRINTS_API_BASE` at it.
 *
 * Note: this test uses async `spawn` (not `spawnSync`) because the
 * http server runs on the test's event loop. spawnSync would block
 * the main thread for the script's entire run, starving the server.
 *
 * The catalog used is the real `catalog/addons.yaml`. The fixture
 * top-level listing intentionally includes 3 already-curated names
 * (cert-manager, external-dns, aws-load-balancer-controller). Per the
 * adds-only AC, those names should NOT generate adds (they exist).
 * The single NEW name is `fancy-new-addon`, which must produce
 * exactly 1 add.
 */
test('integration: aws-eks-blueprints plugin against real catalog (V123-3.3)', async (t) => {
  // Start a tiny HTTP server that serves the fixture set. The server
  // listens on a random port; we read the actual port at startup and
  // template it into the fixture URLs via ${BASE_URL} substitution.
  const server = createServer(async (req, res) => {
    try {
      const url = new URL(req.url, `http://localhost`);
      // Routes:
      //   GET /contents/lib/addons                       → contents-lib-addons.json
      //   GET /contents/lib/addons/<addon>               → contents-<addon>.json
      //   GET /raw/<addon>.ts                            → <addon>.ts
      const pathname = url.pathname;
      let fixtureFile = null;
      if (pathname === '/contents/lib/addons') {
        fixtureFile = 'contents-lib-addons.json';
      } else if (pathname.startsWith('/contents/lib/addons/')) {
        const addon = pathname.slice('/contents/lib/addons/'.length);
        if (addon === 'index.ts') {
          res.writeHead(404);
          res.end('not a dir');
          return;
        }
        fixtureFile = `contents-${addon}.json`;
      } else if (pathname.startsWith('/raw/')) {
        fixtureFile = pathname.slice('/raw/'.length);
      }
      if (!fixtureFile) {
        res.writeHead(404);
        res.end('not found');
        return;
      }
      const text = await readFile(resolve(EKS_FIXTURE_DIR, fixtureFile), 'utf8');
      const baseUrl = `http://127.0.0.1:${server.address().port}`;
      const body = text.replaceAll('${BASE_URL}', baseUrl);
      const isJson = fixtureFile.endsWith('.json');
      res.writeHead(200, {
        'Content-Type': isJson ? 'application/json' : 'text/plain',
        'X-RateLimit-Remaining': '4999',
      });
      res.end(body);
    } catch (err) {
      res.writeHead(500);
      res.end(err?.message ?? 'fixture error');
    }
  });

  await new Promise((resolveListen) => server.listen(0, '127.0.0.1', resolveListen));
  t.after(() => new Promise((r) => server.close(r)));
  const port = server.address().port;
  const apiBase = `http://127.0.0.1:${port}/contents`;

  // Discovery uses the production plugins directory so the real
  // aws-eks-blueprints.mjs is loaded. We must keep ALL plugins
  // hermetic — point cncf-landscape at the EKS server's `/landscape`
  // route (which 404s) so it errors out rather than hitting the live
  // CNCF landscape.yml URL. Per-plugin failure isolation (V123-3.1)
  // ensures the harness still emits the changeset.
  const res = await runScriptAsync(
    [
      '--dry-run',
      '--catalog', REAL_CATALOG,
      '--plugin-dir', REAL_PLUGIN_DIR,
    ],
    {
      SHARKO_EKS_BLUEPRINTS_API_BASE: apiBase,
      SHARKO_CNCF_LANDSCAPE_URL: `http://127.0.0.1:${port}/landscape-not-found.yaml`,
    },
  );
  assert.equal(res.status, 0, `expected exit 0, got ${res.status}; stderr:\n${res.stderr}`);
  const cs = JSON.parse(res.stdout);
  assert.equal(cs.schema_version, '1.0');

  // Filter to the eks plugin's adds — cncf-landscape errored out and
  // contributes 0 entries.
  const eksAdds = cs.adds.filter((a) => a.plugin === 'aws-eks-blueprints');
  const eksAddNames = eksAdds.map((a) => a.entry.name).sort();
  assert.deepEqual(
    eksAddNames,
    ['fancy-new-addon'],
    `expected exactly 1 eks add (fancy-new-addon); got ${eksAddNames.join(', ')}`,
  );

  // The fancy-new-addon entry should have all required schema fields
  // populated (TODO markers where the plugin can't derive).
  const fancy = eksAdds[0].entry;
  assert.equal(fancy.chart, 'fancy-new-addon');
  assert.equal(fancy.repo, 'https://charts.fancy.example.invalid');
  assert.equal(fancy.version, '0.4.2');
  assert.deepEqual(fancy.curated_by, ['aws-eks-blueprints']);
  assert.equal(fancy.license, 'unknown');
  assert.equal(fancy.description, '<TODO: human description>');

  // Plugin-run record exists for the eks plugin.
  const eksRun = cs.scanner_runs.find((r) => r.plugin === 'aws-eks-blueprints');
  assert.ok(eksRun, 'aws-eks-blueprints must appear in scanner_runs');
  assert.equal(eksRun.fetched_count, 4, 'plugin should have returned 4 entries');
  assert.equal(eksRun.error, undefined, 'happy path: no error field');
});

test('integration: plugin-error-isolated (one throws, sibling still contributes)', async (t) => {
  const pluginDir = await makeTempPluginDir(t, ['proposes-add.mjs', 'throws.mjs']);
  const outDir = await makeTempOutDir(t);
  const outFile = resolve(outDir, 'changeset.json');

  const res = runScript([
    '--dry-run',
    '--catalog', FIXTURE,
    '--plugin-dir', pluginDir,
    '--out', outFile,
  ]);

  assert.equal(res.status, 0, `per-plugin failures must NOT abort the run; stderr:\n${res.stderr}`);
  const cs = JSON.parse(res.stdout);
  // sibling plugin's add survived.
  assert.equal(cs.adds.length, 1);
  assert.equal(cs.adds[0].entry.name, 'argo-cd');
  // both plugins recorded.
  assert.equal(cs.scanner_runs.length, 2);
  const throwsRun = cs.scanner_runs.find((r) => r.plugin === 'stub-throws');
  assert.ok(throwsRun, 'throwing plugin appears in scanner_runs');
  assert.match(throwsRun.error, /upstream blip/, 'error message captured');
  // warning was logged on stderr.
  assert.match(res.stderr, /plugin failed/);
});

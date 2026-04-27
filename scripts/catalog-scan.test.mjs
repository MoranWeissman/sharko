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
import { spawnSync } from 'node:child_process';
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
  const fileUrl = pathToFileURL(LANDSCAPE_FIXTURE).href;
  const res = runScript(
    [
      '--dry-run',
      '--catalog', REAL_CATALOG,
      '--plugin-dir', REAL_PLUGIN_DIR,
    ],
    { SHARKO_CNCF_LANDSCAPE_URL: fileUrl },
  );
  assert.equal(res.status, 0, `expected exit 0, got ${res.status}; stderr:\n${res.stderr}`);
  const cs = JSON.parse(res.stdout);
  assert.equal(cs.schema_version, '1.0');

  // Two ADDs: argo-cd-fixture, fixture-monitor-x.
  const addNames = cs.adds.map((a) => a.entry.name).sort();
  assert.deepEqual(
    addNames,
    ['argo-cd-fixture', 'fixture-monitor-x'],
    `expected exactly 2 adds (argo-cd-fixture, fixture-monitor-x); got ${addNames.join(', ')}`,
  );

  // Two UPDATEs: cert-manager (version+repo diff), external-dns (category+repo diff).
  const updateNames = cs.updates.map((u) => u.entry.name).sort();
  assert.deepEqual(
    updateNames,
    ['cert-manager', 'external-dns'],
    `expected exactly 2 updates (cert-manager, external-dns); got ${updateNames.join(', ')}`,
  );

  // Diff details — confirms COMPARABLE_FIELDS detection.
  const certUpdate = cs.updates.find((u) => u.entry.name === 'cert-manager');
  assert.ok('version' in certUpdate.diff, 'cert-manager update should diff version');
  assert.equal(certUpdate.diff.version.to, '9.9.9');
  assert.ok('repo' in certUpdate.diff, 'cert-manager update should diff repo');

  const dnsUpdate = cs.updates.find((u) => u.entry.name === 'external-dns');
  assert.ok('category' in dnsUpdate.diff, 'external-dns update should diff category');
  assert.equal(dnsUpdate.diff.category.from, 'security');
  assert.equal(dnsUpdate.diff.category.to, 'networking');

  // Plugin-run record is correct.
  const run = cs.scanner_runs.find((r) => r.plugin === 'cncf-landscape');
  assert.ok(run, 'cncf-landscape must appear in scanner_runs');
  assert.equal(run.fetched_count, 4, 'plugin should have returned 4 normalized entries');
  assert.equal(run.error, undefined, 'happy path: no error field');
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

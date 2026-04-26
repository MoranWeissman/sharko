#!/usr/bin/env node
/**
 * catalog-scan.mjs — skeleton scanner that lets pluggable upstream
 * sources propose additions/updates against `catalog/addons.yaml`. The
 * skeleton lands in V123-3.1; concrete plugins (CNCF Landscape =
 * V123-3.2, EKS Blueprints = V123-3.3) layer on top; the GitHub
 * workflow + PR-opener (V123-3.4) consumes the changeset JSON this
 * script emits.
 *
 * Usage:
 *   node scripts/catalog-scan.mjs                       # write changeset to default --out
 *   node scripts/catalog-scan.mjs --dry-run             # print pretty JSON to stdout
 *   node scripts/catalog-scan.mjs --catalog path/to.yaml
 *   node scripts/catalog-scan.mjs --out path/to/changeset.json
 *   node scripts/catalog-scan.mjs --plugin-dir scripts/catalog-scan/__tests__/stubs
 *   node scripts/catalog-scan.mjs --include-hidden      # also load `_*.mjs` plugins
 *
 * Optional env vars:
 *   SHARKO_SCAN_LOAD_HIDDEN=1   same as --include-hidden (used by tests)
 *   SHARKO_SCAN_TIMEOUT_MS      per-plugin timeout in ms (default 60000)
 *
 * Logging discipline: stdout stays clean for `--dry-run` JSON capture;
 * all human-readable logs (info/warn/error) go to stderr as JSON-lines.
 *
 * Exit codes:
 *   0  — success (including the "no changes" path and per-plugin errors
 *        that were isolated per the brief)
 *   1  — global error (catalog YAML missing/parse failure, --out write
 *        failure, internal CLI error)
 */
import { mkdir, readFile, writeFile, readdir } from 'node:fs/promises';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { dirname, resolve, basename } from 'node:path';
import { parse as yamlParse } from 'yaml';

import { diff } from './catalog-scan/lib/diff.mjs';
import { newChangeset, recordRun, finalizeChangeset, stringifyDeterministic } from './catalog-scan/lib/changeset.mjs';
import { fetchWithRetry } from './catalog-scan/lib/http.mjs';
import { newLogger } from './catalog-scan/lib/logger.mjs';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const REPO_ROOT = resolve(__dirname, '..');

const DEFAULT_CATALOG = 'catalog/addons.yaml';
const DEFAULT_OUT = '_dist/catalog-scan/changeset.json';
const DEFAULT_PLUGIN_DIR = 'scripts/catalog-scan/plugins';
const DEFAULT_TIMEOUT_MS = 60_000;

/** Parse argv into a small options object. */
export function parseArgs(argv) {
  const opts = {
    dryRun: false,
    catalog: DEFAULT_CATALOG,
    out: DEFAULT_OUT,
    pluginDir: DEFAULT_PLUGIN_DIR,
    includeHidden: false,
  };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    switch (a) {
      case '--dry-run':
        opts.dryRun = true;
        break;
      case '--include-hidden':
        opts.includeHidden = true;
        break;
      case '--catalog':
        opts.catalog = argv[++i];
        break;
      case '--out':
        opts.out = argv[++i];
        break;
      case '--plugin-dir':
        opts.pluginDir = argv[++i];
        break;
      case '-h':
      case '--help':
        opts.help = true;
        break;
      default:
        throw new Error(`unknown argument: ${a}`);
    }
  }
  if (process.env.SHARKO_SCAN_LOAD_HIDDEN === '1') {
    opts.includeHidden = true;
  }
  return opts;
}

const HELP_TEXT = `Usage: catalog-scan.mjs [options]

  --dry-run               Print changeset JSON to stdout, do not write file
  --catalog <path>        Path to catalog YAML (default: ${DEFAULT_CATALOG})
  --out <path>            Output changeset path (default: ${DEFAULT_OUT})
  --plugin-dir <path>     Directory to scan for plugins (default: ${DEFAULT_PLUGIN_DIR})
  --include-hidden        Also load plugins whose filename starts with '_'
  -h, --help              Show this help
`;

/** Resolve a path relative to repo root if not absolute. */
function resolveRepoPath(p) {
  return resolve(REPO_ROOT, p);
}

/**
 * Load the catalog YAML and return the array of current entries.
 * Throws on missing/invalid YAML — the caller treats this as a global error.
 */
export async function loadCatalog(absPath) {
  let text;
  try {
    text = await readFile(absPath, 'utf8');
  } catch (err) {
    throw new Error(`failed to read catalog at ${absPath}: ${err.message}`);
  }
  let parsed;
  try {
    parsed = yamlParse(text);
  } catch (err) {
    throw new Error(`failed to parse catalog YAML at ${absPath}: ${err.message}`);
  }
  if (!parsed || !Array.isArray(parsed.addons)) {
    throw new Error(`catalog at ${absPath} is missing the top-level 'addons:' list`);
  }
  return parsed.addons;
}

/**
 * Discover plugins in the given absolute directory. Returns an
 * alphabetical list of {name, modulePath} where modulePath is an
 * absolute file path. Files starting with '_' are skipped unless
 * includeHidden is true. Non-`.mjs` files and `*.test.mjs` siblings
 * are always skipped.
 */
export async function discoverPlugins(absDir, { includeHidden = false } = {}) {
  let entries;
  try {
    entries = await readdir(absDir, { withFileTypes: true });
  } catch (err) {
    if (err.code === 'ENOENT') return [];
    throw err;
  }
  const out = [];
  for (const e of entries) {
    if (!e.isFile()) continue;
    const fname = e.name;
    if (!fname.endsWith('.mjs')) continue;
    if (fname.endsWith('.test.mjs')) continue;
    if (fname === 'README.md') continue;
    if (fname.startsWith('_') && !includeHidden) continue;
    out.push({ filename: fname, modulePath: resolve(absDir, fname) });
  }
  out.sort((a, b) => a.filename.localeCompare(b.filename));
  return out;
}

/**
 * Run a single plugin with a per-plugin timeout. Returns either
 *   { ok: true, name, fetched }  on success, or
 *   { ok: false, name, error }   on per-plugin failure.
 * Per-plugin failures are recorded in the changeset and do NOT abort
 * the run.
 */
async function runPlugin(modulePath, { logger, timeoutMs }) {
  let mod;
  let pluginName = basename(modulePath, '.mjs');
  try {
    mod = await import(pathToFileURL(modulePath).href);
  } catch (err) {
    return { ok: false, name: pluginName, error: `import failed: ${err.message}` };
  }
  if (!mod || typeof mod.fetch !== 'function') {
    return { ok: false, name: pluginName, error: 'plugin missing required export: fetch()' };
  }
  if (typeof mod.name === 'string' && mod.name.length > 0) {
    pluginName = mod.name;
  }
  const ac = new AbortController();
  const timer = setTimeout(() => ac.abort(new Error(`plugin '${pluginName}' exceeded timeout ${timeoutMs}ms`)), timeoutMs);
  try {
    const ctx = {
      logger: logger.child({ plugin: pluginName }),
      abortSignal: ac.signal,
      http: (url, opts = {}) => fetchWithRetry(url, { ...opts, signal: opts.signal ?? ac.signal }),
    };
    const fetched = await mod.fetch(ctx);
    if (!Array.isArray(fetched)) {
      return { ok: false, name: pluginName, error: 'plugin fetch() did not return an array' };
    }
    return { ok: true, name: pluginName, fetched };
  } catch (err) {
    return { ok: false, name: pluginName, error: err.message ?? String(err) };
  } finally {
    clearTimeout(timer);
  }
}

/**
 * Top-level run: returns { changeset, exitCode }. Exits 1 only on
 * global errors; per-plugin errors stay isolated per the brief.
 */
export async function run(opts, { logger } = {}) {
  const log = logger ?? newLogger();
  const catalogPath = resolveRepoPath(opts.catalog);
  const pluginDir = resolveRepoPath(opts.pluginDir);
  const timeoutMs = Number(process.env.SHARKO_SCAN_TIMEOUT_MS) || DEFAULT_TIMEOUT_MS;

  const current = await loadCatalog(catalogPath);
  log.info('loaded catalog', { catalog: opts.catalog, entries: current.length });

  const plugins = await discoverPlugins(pluginDir, { includeHidden: opts.includeHidden });
  if (plugins.length === 0) {
    log.info('no plugins discovered', { plugin_dir: opts.pluginDir, include_hidden: opts.includeHidden });
  }

  const changeset = newChangeset();
  const allProposals = [];

  for (const p of plugins) {
    const result = await runPlugin(p.modulePath, { logger: log, timeoutMs });
    if (result.ok) {
      log.info('plugin returned proposals', { plugin: result.name, fetched: result.fetched.length });
      recordRun(changeset, { plugin: result.name, fetched_count: result.fetched.length });
      allProposals.push({ plugin: result.name, entries: result.fetched });
    } else {
      log.warn('plugin failed (isolated, run continues)', { plugin: result.name, error: result.error });
      recordRun(changeset, { plugin: result.name, fetched_count: 0, error: result.error });
    }
  }

  const aggregated = diff(current, allProposals);
  changeset.adds = aggregated.adds;
  changeset.updates = aggregated.updates;
  finalizeChangeset(changeset);

  if (changeset.adds.length === 0 && changeset.updates.length === 0) {
    log.info('no changes proposed by any plugin', { plugins: plugins.length });
    if (opts.dryRun) {
      // Still emit the (empty) changeset to stdout so pipelines can
      // observe shape; matches the "--dry-run prints to stdout" AC.
      process.stdout.write(stringifyDeterministic(changeset) + '\n');
    }
    // No file written on the no-changes path (V123-3.4 keys "open PR" on existence).
    return { changeset, exitCode: 0, wrote: false };
  }

  if (opts.dryRun) {
    process.stdout.write(stringifyDeterministic(changeset) + '\n');
    return { changeset, exitCode: 0, wrote: false };
  }

  const outAbs = resolveRepoPath(opts.out);
  await mkdir(dirname(outAbs), { recursive: true });
  await writeFile(outAbs, stringifyDeterministic(changeset) + '\n', 'utf8');
  log.info('wrote changeset', { out: opts.out, adds: changeset.adds.length, updates: changeset.updates.length });
  return { changeset, exitCode: 0, wrote: true };
}

/* ------------------------------------------------------------------ */
/* Entry point                                                         */
/* ------------------------------------------------------------------ */

const isMain = process.argv[1] && resolve(process.argv[1]) === __filename;

if (isMain) {
  const log = newLogger();
  let opts;
  try {
    opts = parseArgs(process.argv.slice(2));
  } catch (err) {
    log.error(err.message);
    process.stderr.write(HELP_TEXT);
    process.exit(1);
  }
  if (opts.help) {
    process.stdout.write(HELP_TEXT);
    process.exit(0);
  }
  try {
    const { exitCode } = await run(opts, { logger: log });
    process.exit(exitCode);
  } catch (err) {
    log.error('global error', { error: err.message ?? String(err) });
    process.exit(1);
  }
}

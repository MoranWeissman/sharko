#!/usr/bin/env node
/**
 * pr-open.mjs — V123-3.4 PR-opener for the catalog-scan bot.
 *
 * Consumes the changeset JSON written by `scripts/catalog-scan.mjs`,
 * pre-computes per-proposal signals, edits `catalog/addons.yaml`, and
 * opens ONE GitHub draft PR per scan run with the labels
 * `catalog-scan` + `needs-review`. Resolves Open Question §7.3
 * (draft-to-main + label gating + NEVER auto-merge per NFR-V123-7).
 *
 * Usage:
 *   node scripts/catalog-scan/pr-open.mjs                     # default paths
 *   node scripts/catalog-scan/pr-open.mjs --dry-run           # print body, skip git/gh
 *   node scripts/catalog-scan/pr-open.mjs --changeset path.json --catalog catalog/addons.yaml
 *   node scripts/catalog-scan/pr-open.mjs --branch catalog-scan/2026-04-30
 *
 * Optional env vars:
 *   GITHUB_TOKEN           Authenticates the `gh` CLI. The workflow
 *                          provides `${{ secrets.GITHUB_TOKEN }}`.
 *   SHARKO_PR_OPEN_TODAY   Override the YYYY-MM-DD branch suffix
 *                          (test seam; otherwise UTC today).
 *
 * Exit codes:
 *   0 — success (PR opened OR concurrency-skipped OR empty changeset).
 *   1 — global error (missing changeset on disk, git/gh failure,
 *       YAML edit threw, etc.).
 *
 * ## Concurrency guard
 *
 * Two checks before any git work:
 *   1. `gh pr list --label catalog-scan --state open --json number`
 *      returns non-empty → another bot PR is open; skip + exit 0
 *      (the human triages it before the bot opens another).
 *   2. The target branch already exists locally OR remotely → skip.
 *
 * Race condition: two simultaneous workflow runs (manual dispatch +
 * cron firing in the same second) could both pass the check and both
 * open a PR. Acceptable risk per V123-3.4 brief gotcha #5 — a
 * duplicate PR is reviewer-visible; close one. Don't engineer a
 * distributed lock.
 *
 * ## PR body shape (per V123-3.4 AC)
 *
 * One markdown table per scan with EXACTLY 5 columns:
 *   | Action | Name | Scorecard | License | Chart resolves | Source |
 *
 * Pipe-escaping: any value containing `|` is replaced by `\|` per
 * GitHub-flavored markdown.
 *
 * ## Anti-gold-plating
 *
 * - No auto-merge logic (NFR-V123-7 forbids).
 * - No Slack / chat notifications — the PR IS the signal.
 * - No CODEOWNERS edits.
 * - No caching / persistence between runs (NFR-V123-1: stateless).
 * - No label-creation step in this script (operator one-time setup;
 *   `gh label create catalog-scan` etc. lives in the workflow YAML
 *   top comment).
 */

import { execFile as execFileCb } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdtemp, readFile, writeFile, rm } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { dirname, resolve, join } from 'node:path';
import { tmpdir } from 'node:os';

import { newLogger } from './lib/logger.mjs';
import { applyChangeset } from './lib/yaml-edit.mjs';
import { scorecardForRepo, chartIndexResolves, licenseFromChart } from './lib/signals.mjs';
import { fetchWithRetry } from './lib/http.mjs';

const execFile = promisify(execFileCb);

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const REPO_ROOT = resolve(__dirname, '..', '..');

const DEFAULT_CHANGESET = '_dist/catalog-scan/changeset.json';
const DEFAULT_CATALOG = 'catalog/addons.yaml';
const PR_LABELS = ['catalog-scan', 'needs-review'];

/** Parse argv into a small options object. */
export function parseArgs(argv) {
  const opts = {
    changeset: DEFAULT_CHANGESET,
    catalog: DEFAULT_CATALOG,
    dryRun: false,
    branch: null, // computed at run-time when null
  };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    switch (a) {
      case '--dry-run':
        opts.dryRun = true;
        break;
      case '--changeset':
        opts.changeset = argv[++i];
        break;
      case '--catalog':
        opts.catalog = argv[++i];
        break;
      case '--branch':
        opts.branch = argv[++i];
        break;
      case '-h':
      case '--help':
        opts.help = true;
        break;
      default:
        throw new Error(`unknown argument: ${a}`);
    }
  }
  return opts;
}

const HELP_TEXT = `Usage: pr-open.mjs [options]

  --changeset <path>      Changeset JSON path (default: ${DEFAULT_CHANGESET})
  --catalog <path>        Catalog YAML to edit (default: ${DEFAULT_CATALOG})
  --branch <name>         Override branch name (default: catalog-scan/<UTC YYYY-MM-DD>)
  --dry-run               Print PR body to stdout; skip all git/gh side effects
  -h, --help              Show this help
`;

function resolveRepoPath(p) {
  return resolve(REPO_ROOT, p);
}

/** Today in UTC YYYY-MM-DD, with env-var override for tests. */
function todayUtcDate() {
  const override = process.env.SHARKO_PR_OPEN_TODAY;
  if (override && /^\d{4}-\d{2}-\d{2}$/.test(override)) return override;
  return new Date().toISOString().slice(0, 10);
}

/* ------------------------------------------------------------------ */
/* Process wrappers (replaceable for testing via opts)                  */
/* ------------------------------------------------------------------ */

/**
 * Default execFile wrapper used in production. Tests replace this via
 * `opts.execFile` to spy on git/gh invocations.
 */
async function runProcess(file, args, options) {
  return execFile(file, args, options);
}

/* ------------------------------------------------------------------ */
/* Concurrency guards                                                   */
/* ------------------------------------------------------------------ */

/** Returns true when an open PR with the catalog-scan label exists.
 *
 * V123-PR-B (H5): pre-fix this swallowed `gh pr list` failures and returned
 * false, so a transient gh outage on a daily cron run let the bot push a
 * duplicate PR (the real concurrency guard relies on this probe). The
 * asymmetric badness — one missed cron run vs. a duplicate PR humans must
 * close — makes "fail loud" the right call. See pr-open.test.mjs for the
 * matching unit test.
 */
async function openPrExists(exec, logger) {
  try {
    const { stdout } = await exec('gh', ['pr', 'list', '--label', 'catalog-scan', '--state', 'open', '--json', 'number']);
    const arr = JSON.parse(stdout || '[]');
    return Array.isArray(arr) && arr.length > 0;
  } catch (err) {
    // Log + rethrow. The caller (run()) treats any throw from this fn as a
    // fatal run failure — that's exactly the behaviour we want on a daily
    // cron because a quiet "skip" can hide a duplicate-PR-producing bug
    // for days.
    const msg = err?.message ?? String(err);
    logger.error('open-PR check failed', { error: msg });
    throw new Error(`openPrExists: gh pr list failed: ${msg}`);
  }
}

/** Returns true when the branch exists locally or remotely. */
async function branchExists(exec, branch, logger) {
  // Local check.
  try {
    await exec('git', ['rev-parse', '--verify', '--quiet', branch]);
    return true;
  } catch {
    // ignore — branch not local
  }
  // Remote check.
  try {
    const { stdout } = await exec('git', ['ls-remote', '--heads', 'origin', branch]);
    return stdout.trim().length > 0;
  } catch (err) {
    logger.warn('remote branch probe failed (treating as not present)', { branch, error: err.message ?? String(err) });
    return false;
  }
}

/* ------------------------------------------------------------------ */
/* Signal pre-compute                                                   */
/* ------------------------------------------------------------------ */

/**
 * Compute Scorecard / license / chart-resolves signals for one
 * proposal. Sequential; failures degrade to "unknown" — never abort.
 *
 * @returns {Promise<{scorecard: string, chartResolves: string, license: string}>}
 */
async function computeSignalsFor(entry, ctx) {
  const sourceUrl = entry?.source_url ?? entry?.repo;

  // Scorecard
  let scorecard = 'unknown';
  const sc = await scorecardForRepo(sourceUrl, ctx);
  if (sc !== 'unknown' && typeof sc === 'object') {
    scorecard = `${sc.score.toFixed(1)} (${sc.updated})`;
  }

  // Chart resolves + license (share the index-yaml fetch via _cache)
  let chartResolves = 'unknown';
  let license = entry?.license ?? 'unknown';
  let licenseStatus = license === 'unknown' ? 'unknown' : 'ok';
  if (typeof entry?.repo === 'string' && typeof entry?.chart === 'string') {
    chartResolves = await chartIndexResolves(entry.repo, entry.chart, ctx);
    // Pull cached parsed index back out for license lookup.
    if (ctx._cache instanceof Map && ctx._cache.has(entry.repo)) {
      const idx = ctx._cache.get(entry.repo);
      if (idx && idx !== 'unknown') {
        const lic = licenseFromChart(idx, entry.chart);
        if (lic.status !== 'unknown') {
          license = lic.value;
          licenseStatus = lic.status;
        }
      }
    }
  }

  return {
    scorecard,
    chartResolves,
    license: licenseStatus === 'flagged' ? `${license} (flagged)` : license,
  };
}

/* ------------------------------------------------------------------ */
/* Markdown body builder                                                */
/* ------------------------------------------------------------------ */

/** Escape pipe characters so they don't break GFM tables. */
function escTableCell(value) {
  if (value == null) return '';
  return String(value).replace(/\|/g, '\\|');
}

/**
 * Render the PR body markdown.
 *
 * @param {object} args.changeset - Full changeset object.
 * @param {Array<{kind:'add'|'update', plugin:string, entry:object,
 *   diff?:object, signals:object}>} args.rows - Pre-computed rows.
 * @returns {string}
 */
function renderBody({ changeset, rows }) {
  const adds = rows.filter((r) => r.kind === 'add');
  const updates = rows.filter((r) => r.kind === 'update');
  const plugins = Array.from(new Set(rows.map((r) => r.plugin))).sort();
  const lines = [];

  lines.push('# Catalog scan — automated proposal');
  lines.push('');
  lines.push('This PR was opened by the **catalog-scan bot** (V123-3.4 of the v1.23 catalog-extensibility epic).');
  lines.push('');
  lines.push(`- **Generated:** \`${changeset.generated_at}\``);
  lines.push(`- **Sources:** ${plugins.length > 0 ? plugins.map((p) => `\`${p}\``).join(', ') : '_none_'}`);
  lines.push(`- **Adds:** ${adds.length} · **Updates:** ${updates.length}`);
  lines.push('');
  lines.push('Per [Open Question §7.3 resolution](../docs/design/2026-04-20-v1.23-catalog-extensibility.md#7-open-questions-to-resolve-during-v123-execution): this is a **draft PR**. Labels `catalog-scan` + `needs-review` are applied so CODEOWNERS treat it distinctly. **NEVER auto-merged** per NFR-V123-7. Reviewer is expected to edit/close — see the runbook (V123-3.5).');
  lines.push('');

  // Scanner runs summary
  lines.push('## Scanner runs');
  lines.push('');
  lines.push('| Plugin | Fetched | Error |');
  lines.push('|---|---|---|');
  for (const run of changeset.scanner_runs ?? []) {
    lines.push(`| ${escTableCell(run.plugin)} | ${escTableCell(run.fetched_count)} | ${escTableCell(run.error ?? '')} |`);
  }
  lines.push('');

  // Proposals table — single combined table per AC.
  lines.push('## Proposals');
  lines.push('');
  if (rows.length === 0) {
    lines.push('_No add/update proposals — empty changeset._');
  } else {
    lines.push('| Action | Name | Scorecard | License | Chart resolves | Source |');
    lines.push('|---|---|---|---|---|---|');
    for (const r of rows) {
      const action = r.kind === 'add' ? 'add' : 'update';
      const name = escTableCell(r.entry?.name ?? '');
      const scorecard = escTableCell(r.signals.scorecard);
      const license = escTableCell(r.signals.license);
      const chart = escTableCell(r.signals.chartResolves);
      const source = escTableCell(r.plugin);
      lines.push(`| ${action} | \`${name}\` | ${scorecard} | ${license} | ${chart} | ${source} |`);
    }
  }
  lines.push('');

  // Per-update diff details — reviewers want to see what fields drifted.
  if (updates.length > 0) {
    lines.push('## Update diffs');
    lines.push('');
    for (const u of updates) {
      const name = u.entry?.name ?? '';
      lines.push(`### \`${escTableCell(name)}\` (from \`${escTableCell(u.plugin)}\`)`);
      lines.push('');
      const fields = u.diff && typeof u.diff === 'object' ? Object.keys(u.diff) : [];
      if (fields.length === 0) {
        lines.push('_No field-level diff payload._');
      } else {
        lines.push('| Field | From | To |');
        lines.push('|---|---|---|');
        for (const f of fields) {
          const from = escTableCell(u.diff[f]?.from ?? '');
          const to = escTableCell(u.diff[f]?.to ?? '');
          lines.push(`| \`${f}\` | ${from} | ${to} |`);
        }
      }
      lines.push('');
    }
  }

  lines.push('---');
  lines.push('');
  lines.push('## Reviewer checklist');
  lines.push('');
  lines.push('- [ ] License values are sane for each proposal (allow-list: `Apache-2.0`, `BSD-3-Clause`, `MIT`, `MPL-2.0`).');
  lines.push('- [ ] Chart-resolves column shows `ok` (or `oci-not-checked` for OCI repos).');
  lines.push('- [ ] Scorecard score (where present) is acceptable; investigate scores < 5.');
  lines.push('- [ ] TODO markers in the diff (e.g. `<TODO: human description>`, `<TODO: derive from chart repo>`) are replaced with real values OR the proposal is closed.');
  lines.push('- [ ] If closing without merging: leave a comment explaining why so the next scan does not re-propose.');
  lines.push('');
  lines.push('NEVER auto-merge — per NFR-V123-7.');
  lines.push('');

  return lines.join('\n');
}

/* ------------------------------------------------------------------ */
/* Main runner                                                          */
/* ------------------------------------------------------------------ */

/**
 * Top-level run. Returns `{ exitCode, body, branch }` for testability.
 *
 * @param {object} opts - Parsed CLI options.
 * @param {object} deps - Optional injectables for tests:
 *   - `logger`
 *   - `execFile(file, args, options) -> Promise<{stdout, stderr}>`
 *   - `fetcher` — replaces lib/http.mjs's fetchWithRetry
 *   - `readFile`, `writeFile` — fs override
 */
export async function run(opts, deps = {}) {
  const log = deps.logger ?? newLogger();
  const exec = deps.execFile ?? runProcess;
  const fetcher = deps.fetcher ?? fetchWithRetry;
  const read = deps.readFile ?? readFile;
  const write = deps.writeFile ?? writeFile;

  const branch = opts.branch ?? `catalog-scan/${todayUtcDate()}`;
  const changesetAbs = resolveRepoPath(opts.changeset);
  const catalogAbs = resolveRepoPath(opts.catalog);

  // 1. Read changeset.
  let changeset;
  try {
    const text = await read(changesetAbs, 'utf8');
    changeset = JSON.parse(text);
  } catch (err) {
    if (err.code === 'ENOENT') {
      log.info('no changeset file on disk — nothing to do', { changeset: opts.changeset });
      return { exitCode: 0, body: '', branch };
    }
    log.error('failed to read/parse changeset', { changeset: opts.changeset, error: err.message ?? String(err) });
    return { exitCode: 1, body: '', branch };
  }

  const adds = Array.isArray(changeset.adds) ? changeset.adds : [];
  const updates = Array.isArray(changeset.updates) ? changeset.updates : [];
  if (adds.length + updates.length === 0) {
    log.info('changeset has zero proposals — no PR to open', { changeset: opts.changeset });
    return { exitCode: 0, body: '', branch };
  }

  // 2. Concurrency guards (skipped in dry-run since we never act anyway,
  //    and CI where there's no `gh` config would always trip "open PR").
  if (!opts.dryRun) {
    if (await openPrExists(exec, log)) {
      log.info('open catalog-scan PR already exists — skipping', {});
      return { exitCode: 0, body: '', branch };
    }
    if (await branchExists(exec, branch, log)) {
      log.info('target branch already exists (local or remote) — skipping', { branch });
      return { exitCode: 0, body: '', branch };
    }
  }

  // 3. Pre-compute signals — sequential; per-proposal failures
  //    degrade to "unknown" inside computeSignalsFor (never throw).
  const ctx = {
    http: fetcher,
    logger: log.child({ component: 'signals' }),
    _cache: new Map(),
  };
  const rows = [];
  for (const a of adds) {
    const signals = await computeSignalsFor(a.entry, ctx);
    rows.push({ kind: 'add', plugin: a.plugin, entry: a.entry, signals });
  }
  for (const u of updates) {
    const signals = await computeSignalsFor(u.entry, ctx);
    rows.push({ kind: 'update', plugin: u.plugin, entry: u.entry, diff: u.diff, signals });
  }

  // 4. Render the PR body.
  const body = renderBody({ changeset, rows });

  if (opts.dryRun) {
    process.stdout.write(body + '\n');
    log.info('dry-run: skipped git/gh side effects', { adds: adds.length, updates: updates.length });
    return { exitCode: 0, body, branch };
  }

  // 5. Edit the catalog YAML.
  let editedYaml;
  try {
    const yamlText = await read(catalogAbs, 'utf8');
    editedYaml = applyChangeset(yamlText, changeset);
  } catch (err) {
    log.error('YAML edit failed', { error: err.message ?? String(err) });
    return { exitCode: 1, body, branch };
  }

  // 6. Branch + commit + push + open PR.
  let tmpDir;
  try {
    await exec('git', ['checkout', '-b', branch], { cwd: REPO_ROOT });
    await write(catalogAbs, editedYaml, 'utf8');
    await exec('git', ['add', opts.catalog], { cwd: REPO_ROOT });
    const commitSubject = `catalog-scan: ${adds.length} adds, ${updates.length} updates`;
    await exec('git', ['commit', '-m', commitSubject], { cwd: REPO_ROOT });
    await exec('git', ['push', '-u', 'origin', branch], { cwd: REPO_ROOT });

    tmpDir = await mkdtemp(join(tmpdir(), 'catalog-scan-pr-'));
    const bodyFile = join(tmpDir, 'pr-body.md');
    await write(bodyFile, body, 'utf8');

    const ghArgs = [
      'pr', 'create',
      '--title', `catalog-scan: ${adds.length} adds, ${updates.length} updates (${todayUtcDate()})`,
      '--body-file', bodyFile,
      '--base', 'main',
      '--head', branch,
      '--draft',
    ];
    for (const label of PR_LABELS) {
      ghArgs.push('--label', label);
    }
    const { stdout } = await exec('gh', ghArgs, { cwd: REPO_ROOT });
    const prUrl = stdout.trim();
    log.info('opened PR', { url: prUrl, branch, adds: adds.length, updates: updates.length });
    return { exitCode: 0, body, branch, prUrl };
  } catch (err) {
    log.error('git/gh step failed', { branch, error: err.message ?? String(err), stderr: err.stderr });
    return { exitCode: 1, body, branch };
  } finally {
    if (tmpDir) {
      try { await rm(tmpDir, { recursive: true, force: true }); } catch { /* best-effort */ }
    }
  }
}

/* ------------------------------------------------------------------ */
/* Entry point                                                          */
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

/**
 * pr-open.test.mjs — V123-3.4 Tier 3 #8.
 *
 * Five cases per the V123-3.4 brief:
 *   1. dry-run with synthetic changeset → markdown body printed to
 *      stdout containing the right number of table rows + 5 columns.
 *   2. empty changeset → exits 0, no body, "no proposals" log line.
 *   3. concurrency skip on open PR — `gh pr list` returns non-empty
 *      array → exits 0, "already exists" log line.
 *   4. branch-exists skip — local branch `git rev-parse` succeeds.
 *   5. body shape (non-dry-run) — assert title, label list, --draft
 *      flag passed to `gh pr create`.
 *
 * All cases stub `execFile` so no real `git`/`gh` runs, and stub the
 * fetcher so no real HTTP. Recorded-logger duplicated inline.
 */

import test from 'node:test';
import assert from 'node:assert/strict';

import { run, parseArgs, escTableCell } from './pr-open.mjs';

/* ------------------------------------------------------------------ */
/* Recorded logger                                                       */
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
/* Stub execFile                                                         */
/* ------------------------------------------------------------------ */

/**
 * Build a stub execFile that consults a route table.
 *
 * @param {object} routes - Map of `<file>:<arg0>:<arg1>...` → handler
 *   `(args, options) => {stdout, stderr}` (or throws).
 *   Falls back to a pass-through "no PRs / branch absent" default.
 */
function buildExecStub(routes = {}) {
  const calls = [];
  async function exec(file, args, options) {
    calls.push({ file, args, options });
    const key = `${file} ${args.join(' ')}`;
    for (const [pattern, handler] of Object.entries(routes)) {
      if (key.startsWith(pattern)) {
        return handler(args, options);
      }
    }
    // Defaults that pass concurrency guard.
    if (file === 'gh' && args[0] === 'pr' && args[1] === 'list') {
      return { stdout: '[]', stderr: '' };
    }
    if (file === 'git' && args[0] === 'rev-parse') {
      const err = new Error('not a branch');
      throw err;
    }
    if (file === 'git' && args[0] === 'ls-remote') {
      return { stdout: '', stderr: '' };
    }
    if (file === 'git' && (args[0] === 'checkout' || args[0] === 'add' || args[0] === 'commit' || args[0] === 'push')) {
      return { stdout: '', stderr: '' };
    }
    if (file === 'gh' && args[0] === 'pr' && args[1] === 'create') {
      return { stdout: 'https://github.com/foo/bar/pull/123\n', stderr: '' };
    }
    throw new Error(`unmocked exec call: ${key}`);
  }
  return { exec, calls };
}

/* ------------------------------------------------------------------ */
/* Test fixtures                                                          */
/* ------------------------------------------------------------------ */

function makeChangeset({ adds = [], updates = [] } = {}) {
  return {
    schema_version: '1.0',
    generated_at: '2026-04-27T04:00:00.000Z',
    scanner_runs: [
      { plugin: 'cncf-landscape', fetched_count: adds.length },
      { plugin: 'aws-eks-blueprints', fetched_count: 0, error: 'rate-limit' },
    ],
    adds,
    updates,
  };
}

function makeAdd(name) {
  return {
    plugin: 'aws-eks-blueprints',
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
      curated_by: ['aws-eks-blueprints'],
      source_url: `https://github.com/example/${name}`,
    },
  };
}

function makeUpdate(name) {
  return {
    plugin: 'cncf-landscape',
    entry: { name, version: '2.0.0' },
    diff: { version: { from: '1.0.0', to: '2.0.0' } },
  };
}

/**
 * Build a fetcher that always returns "scorecard 404 + index missing"
 * — keeps signals all "unknown" so the body shape is deterministic
 * across machines (no live HTTP).
 */
function offlineFetcher() {
  return async () => ({
    ok: false,
    status: 404,
    text: async () => '',
    json: async () => ({}),
    headers: { get: () => null },
  });
}

/**
 * Build deps that route file reads to fixtures + writes to a Map.
 */
function buildDeps({ changeset, exec, logger }) {
  const fsWrites = new Map();
  return {
    deps: {
      logger,
      execFile: exec,
      fetcher: offlineFetcher(),
      readFile: async (path) => {
        if (String(path).endsWith('changeset.json')) return JSON.stringify(changeset);
        if (String(path).endsWith('addons.yaml')) {
          return `# fixture
addons:
  - name: alpha
    description: Alpha thing
    chart: alpha
    repo: https://charts.alpha.test
    default_namespace: alpha
    maintainers: [alpha]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated]
`;
        }
        const err = new Error(`ENOENT: ${path}`);
        err.code = 'ENOENT';
        throw err;
      },
      writeFile: async (path, data) => { fsWrites.set(String(path), data); },
    },
    fsWrites,
  };
}

/* ------------------------------------------------------------------ */
/* Test cases                                                            */
/* ------------------------------------------------------------------ */

test('parseArgs: defaults + flags', () => {
  const opts = parseArgs([]);
  assert.equal(opts.dryRun, false);
  assert.equal(opts.changeset, '_dist/catalog-scan/changeset.json');
  assert.equal(opts.catalog, 'catalog/addons.yaml');
  assert.equal(opts.branch, null);

  const opts2 = parseArgs(['--dry-run', '--branch', 'foo', '--changeset', 'a.json', '--catalog', 'b.yaml']);
  assert.equal(opts2.dryRun, true);
  assert.equal(opts2.branch, 'foo');
  assert.equal(opts2.changeset, 'a.json');
  assert.equal(opts2.catalog, 'b.yaml');
});

test('pr-open: dry-run prints body with 5-column table + correct row count', async (t) => {
  const logger = makeRecordedLogger();
  const { exec } = buildExecStub();
  const cs = makeChangeset({ adds: [makeAdd('beta'), makeAdd('zulu')], updates: [makeUpdate('alpha')] });
  const { deps } = buildDeps({ changeset: cs, exec, logger });

  // Capture stdout writes.
  const origWrite = process.stdout.write.bind(process.stdout);
  let captured = '';
  process.stdout.write = (chunk, ...rest) => {
    captured += String(chunk);
    return origWrite(chunk, ...rest);
  };
  t.after(() => { process.stdout.write = origWrite; });

  const opts = { changeset: '_dist/catalog-scan/changeset.json', catalog: 'catalog/addons.yaml', dryRun: true, branch: 'catalog-scan/test' };
  const result = await run(opts, deps);

  assert.equal(result.exitCode, 0);
  assert.match(captured, /Catalog scan — automated proposal/);
  // 5-column table header.
  assert.match(captured, /\| Action \| Name \| Scorecard \| License \| Chart resolves \| Source \|/);
  // 3 proposals → 3 data rows. Count `| add |` and `| update |` rows.
  const addRowCount = (captured.match(/^\| add \|/gm) || []).length;
  const updRowCount = (captured.match(/^\| update \|/gm) || []).length;
  assert.equal(addRowCount, 2);
  assert.equal(updRowCount, 1);
});

test('pr-open: empty changeset exits 0, logs "zero proposals", no body', async () => {
  const logger = makeRecordedLogger();
  const { exec } = buildExecStub();
  const cs = makeChangeset({ adds: [], updates: [] });
  const { deps } = buildDeps({ changeset: cs, exec, logger });

  const opts = { changeset: '_dist/catalog-scan/changeset.json', catalog: 'catalog/addons.yaml', dryRun: false, branch: 'catalog-scan/test' };
  const result = await run(opts, deps);
  assert.equal(result.exitCode, 0);
  assert.equal(result.body, '');
  assert.ok(logger.records.find((r) => r.msg.includes('zero proposals')), 'expected zero-proposals info log');
});

test('pr-open: concurrency skip — open PR exists → exits 0, no git work', async () => {
  const logger = makeRecordedLogger();
  const { exec, calls } = buildExecStub({
    'gh pr list': async () => ({ stdout: JSON.stringify([{ number: 99 }]), stderr: '' }),
  });
  const cs = makeChangeset({ adds: [makeAdd('beta')], updates: [] });
  const { deps } = buildDeps({ changeset: cs, exec, logger });

  const opts = { changeset: '_dist/catalog-scan/changeset.json', catalog: 'catalog/addons.yaml', dryRun: false, branch: 'catalog-scan/test' };
  const result = await run(opts, deps);
  assert.equal(result.exitCode, 0);
  // No git/gh-create call — only the gh pr list probe.
  const ghCreateCalls = calls.filter((c) => c.file === 'gh' && c.args[1] === 'create');
  assert.equal(ghCreateCalls.length, 0, 'gh pr create must NOT be invoked');
  const gitCalls = calls.filter((c) => c.file === 'git');
  assert.equal(gitCalls.length, 0, 'no git work expected');
  assert.ok(logger.records.find((r) => r.msg.includes('already exists')), 'expected already-exists log');
});

test('pr-open: branch-exists skip (local branch present) → exits 0, no PR opened', async () => {
  const logger = makeRecordedLogger();
  const { exec, calls } = buildExecStub({
    'git rev-parse': async () => ({ stdout: 'abc123\n', stderr: '' }), // branch exists
  });
  const cs = makeChangeset({ adds: [makeAdd('beta')], updates: [] });
  const { deps } = buildDeps({ changeset: cs, exec, logger });

  const opts = { changeset: '_dist/catalog-scan/changeset.json', catalog: 'catalog/addons.yaml', dryRun: false, branch: 'catalog-scan/test' };
  const result = await run(opts, deps);
  assert.equal(result.exitCode, 0);
  const ghCreateCalls = calls.filter((c) => c.file === 'gh' && c.args[1] === 'create');
  assert.equal(ghCreateCalls.length, 0);
  assert.ok(
    logger.records.find((r) => r.msg.includes('target branch already exists')),
    'expected target-branch-exists log',
  );
});

test('pr-open: full flow — gh pr create called with --draft, both labels, --base main', async () => {
  const logger = makeRecordedLogger();
  const { exec, calls } = buildExecStub();
  const cs = makeChangeset({ adds: [makeAdd('beta')], updates: [makeUpdate('alpha')] });
  const { deps, fsWrites } = buildDeps({ changeset: cs, exec, logger });

  const opts = { changeset: '_dist/catalog-scan/changeset.json', catalog: 'catalog/addons.yaml', dryRun: false, branch: 'catalog-scan/2026-04-30' };
  const result = await run(opts, deps);
  assert.equal(result.exitCode, 0, `expected exit 0; logger records: ${JSON.stringify(logger.records)}`);

  const ghCreate = calls.find((c) => c.file === 'gh' && c.args[1] === 'create');
  assert.ok(ghCreate, 'gh pr create must be invoked');
  const args = ghCreate.args;
  assert.ok(args.includes('--draft'), 'must include --draft');
  assert.ok(args.includes('--base'), 'must specify --base');
  assert.equal(args[args.indexOf('--base') + 1], 'main');
  assert.ok(args.includes('--head'));
  assert.equal(args[args.indexOf('--head') + 1], 'catalog-scan/2026-04-30');
  // Both labels present.
  const labelArgs = [];
  for (let i = 0; i < args.length; i++) {
    if (args[i] === '--label') labelArgs.push(args[i + 1]);
  }
  assert.deepEqual(labelArgs.sort(), ['catalog-scan', 'needs-review']);
  // Title format.
  const titleIdx = args.indexOf('--title');
  assert.ok(titleIdx >= 0);
  assert.match(args[titleIdx + 1], /catalog-scan: 1 adds, 1 updates/);

  // Catalog YAML was written with the edit applied.
  const catalogWrite = Array.from(fsWrites.entries()).find(([k]) => k.endsWith('addons.yaml'));
  assert.ok(catalogWrite, 'catalog YAML must be written');
  assert.match(catalogWrite[1], /- name: beta/);
});

test('pr-open: openPrExists rethrows on gh pr list failure (V123-PR-B / H5)', async () => {
  // Pre-fix the helper logged a warning and returned false, which let a
  // transient `gh` outage on the daily cron run pass the concurrency
  // guard and open a duplicate PR. Rethrowing turns that into a loud
  // failed run — one missed cron is recoverable, a duplicate PR is not.
  const logger = makeRecordedLogger();
  const ghErr = new Error('gh: not authenticated');
  const { exec, calls } = buildExecStub({
    'gh pr list': async () => { throw ghErr; },
  });
  const cs = makeChangeset({ adds: [makeAdd('beta')], updates: [] });
  const { deps } = buildDeps({ changeset: cs, exec, logger });

  const opts = { changeset: '_dist/catalog-scan/changeset.json', catalog: 'catalog/addons.yaml', dryRun: false, branch: 'catalog-scan/test' };

  await assert.rejects(
    () => run(opts, deps),
    (err) => {
      assert.match(err.message, /openPrExists: gh pr list failed/);
      assert.match(err.message, /not authenticated/);
      return true;
    },
  );

  // Defensive: no git/gh-create work happened — failure must short-circuit.
  const ghCreateCalls = calls.filter((c) => c.file === 'gh' && c.args[1] === 'create');
  assert.equal(ghCreateCalls.length, 0, 'gh pr create must NOT run after the throw');
  const gitMutateCalls = calls.filter((c) =>
    c.file === 'git' && (c.args[0] === 'checkout' || c.args[0] === 'commit' || c.args[0] === 'push'),
  );
  assert.equal(gitMutateCalls.length, 0, 'no git mutating calls after the throw');

  // The error log was emitted before the throw so a maintainer reading
  // workflow logs sees the underlying gh error message.
  const errLog = logger.records.find((r) => r.level === 'error' && r.msg.includes('open-PR check failed'));
  assert.ok(errLog, 'expected open-PR-check error log');
});

/* ------------------------------------------------------------------ */
/* M4 — escTableCell: pipe + backtick + backslash + newline (V123-PR-F3) */
/* ------------------------------------------------------------------ */

test('escTableCell: escapes pipe (existing behavior preserved)', () => {
  assert.equal(escTableCell('foo|bar'), 'foo\\|bar');
});

test('escTableCell: escapes backtick (M4)', () => {
  // Pre-fix, an entry name containing `code-spans` rendered as code
  // inside the table cell, breaking the visual column layout for the
  // following cells.
  assert.equal(escTableCell('foo`bar'), 'foo\\`bar');
});

test('escTableCell: escapes backslash FIRST so subsequent escapes are not double-escaped (M4)', () => {
  // Order matters: if `|` is escaped before `\\`, the `\\|` inserted by
  // pipe-escape would itself be double-escaped to `\\\\|` and render
  // visibly. The implementation runs `\\` -> `\\\\` first.
  assert.equal(escTableCell('a\\b'), 'a\\\\b');
  // Combined: backslash + pipe in the same cell.
  assert.equal(escTableCell('a\\b|c'), 'a\\\\b\\|c');
});

test('escTableCell: collapses newlines into a single space (M4)', () => {
  // Embedded newlines either break the table row or render as <br>
  // depending on the markdown flavor — collapsing to a space is the
  // conservative, deterministic choice.
  assert.equal(escTableCell('foo\nbar'), 'foo bar');
  assert.equal(escTableCell('foo\r\nbar'), 'foo bar');
  assert.equal(escTableCell('foo\n\n\nbar'), 'foo bar');
});

test('escTableCell: handles all four special characters together (M4)', () => {
  // A pathological cell value: backslash, backtick, pipe, newline.
  // Escape order ensures none of them double-escape.
  assert.equal(
    escTableCell('a\\b`c|d\ne'),
    'a\\\\b\\`c\\|d e',
  );
});

test('escTableCell: passes plain text through unchanged', () => {
  assert.equal(escTableCell('cert-manager'), 'cert-manager');
  assert.equal(escTableCell('1.18.0'), '1.18.0');
});

test('escTableCell: handles null/undefined/numbers without throwing', () => {
  assert.equal(escTableCell(null), '');
  assert.equal(escTableCell(undefined), '');
  assert.equal(escTableCell(42), '42');
  assert.equal(escTableCell(0), '0');
});

/* ------------------------------------------------------------------ */
/* M3 — duplicate proposals render in PR body (V123-PR-F3)              */
/* ------------------------------------------------------------------ */

test('pr-open: duplicates section renders when changeset.duplicates is non-empty (M3)', async (t) => {
  const logger = makeRecordedLogger();
  const { exec } = buildExecStub();
  const cs = makeChangeset({ adds: [makeAdd('beta')], updates: [] });
  // Inject a duplicate-proposal record (simulating dedupAdds upstream).
  cs.duplicates = [{
    plugin: 'aws-eks-blueprints',
    entry: { name: 'beta', version: '0.2.0', chart: 'beta', repo: 'https://other.test' },
    _duplicate_proposal: true,
    first_plugin: 'cncf-landscape',
  }];
  const { deps } = buildDeps({ changeset: cs, exec, logger });

  // Capture stdout (dry-run prints body).
  const origWrite = process.stdout.write.bind(process.stdout);
  let captured = '';
  process.stdout.write = (chunk, ...rest) => {
    captured += String(chunk);
    return origWrite(chunk, ...rest);
  };
  t.after(() => { process.stdout.write = origWrite; });

  const opts = {
    changeset: '_dist/catalog-scan/changeset.json',
    catalog: 'catalog/addons.yaml',
    dryRun: true,
    branch: 'catalog-scan/test',
  };
  const result = await run(opts, deps);
  assert.equal(result.exitCode, 0);

  // Section heading visible.
  assert.match(captured, /## Duplicate proposals — review needed/);
  // Three-column table header.
  assert.match(captured, /\| Name \| Duplicate plugin \| First-winner plugin \|/);
  // The losing-plugin record + winner pointer both rendered.
  assert.match(captured, /\| `beta` \| aws-eks-blueprints \| cncf-landscape \|/);
});

test('pr-open: duplicates section is omitted when changeset.duplicates is empty', async (t) => {
  const logger = makeRecordedLogger();
  const { exec } = buildExecStub();
  const cs = makeChangeset({ adds: [makeAdd('beta')], updates: [] });
  // duplicates left at default empty array.
  const { deps } = buildDeps({ changeset: cs, exec, logger });

  const origWrite = process.stdout.write.bind(process.stdout);
  let captured = '';
  process.stdout.write = (chunk, ...rest) => {
    captured += String(chunk);
    return origWrite(chunk, ...rest);
  };
  t.after(() => { process.stdout.write = origWrite; });

  const opts = {
    changeset: '_dist/catalog-scan/changeset.json',
    catalog: 'catalog/addons.yaml',
    dryRun: true,
    branch: 'catalog-scan/test',
  };
  await run(opts, deps);

  assert.doesNotMatch(captured, /Duplicate proposals/);
});

test('pr-open: missing changeset file → exit 0 with "nothing to do" log (workflow safety)', async () => {
  const logger = makeRecordedLogger();
  const { exec } = buildExecStub();
  const deps = {
    logger,
    execFile: exec,
    fetcher: offlineFetcher(),
    readFile: async () => {
      const err = new Error('ENOENT');
      err.code = 'ENOENT';
      throw err;
    },
    writeFile: async () => {},
  };
  const opts = { changeset: '_dist/catalog-scan/changeset.json', catalog: 'catalog/addons.yaml', dryRun: false, branch: 'catalog-scan/test' };
  const result = await run(opts, deps);
  assert.equal(result.exitCode, 0);
  assert.ok(logger.records.find((r) => r.msg.includes('nothing to do')), 'expected nothing-to-do log');
});

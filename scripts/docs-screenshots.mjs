#!/usr/bin/env node
/**
 * docs-screenshots.mjs — captures the docs-site screenshots from a
 * running Sharko demo server. Designed for V122-4 ("docs site has no
 * screenshots of the actual UI"); produces deterministic PNGs under
 * docs/site/assets/screenshots/.
 *
 * Usage:
 *   1. Start the demo server in another terminal:
 *        make demo            # http://localhost:8080
 *   2. In a third terminal:
 *        npm install --no-save playwright   # (one-time, ~150 MB)
 *        npx playwright install chromium    # (one-time, ~120 MB)
 *   3. Run the script:
 *        node scripts/docs-screenshots.mjs
 *      (or `cd ui && npm run docs:screenshots`)
 *
 * Optional env vars:
 *   SHARKO_URL    base URL (default http://localhost:8080)
 *   SHARKO_USER   demo username (default admin)
 *   SHARKO_PASS   demo password (default admin)
 *   OUT_DIR       output dir   (default docs/site/assets/screenshots)
 *
 * Why this isn't wired into CI:
 *   The demo backend is mocked, but it's not deterministic enough to
 *   commit screenshot diffs as CI artefacts (the cluster cards re-rank
 *   on every load). Run the script locally before a docs release; the
 *   PNGs land in git so the docs site renders the latest UI even when
 *   no demo is reachable.
 */
import { mkdirSync } from 'node:fs';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { dirname, resolve } from 'node:path';
import { createRequire } from 'node:module';

// Resolve `playwright` from the caller's CWD (typically `ui/node_modules`)
// rather than from the script location, so the script works whether you
// run it from the repo root or from `ui/`. Falls back to a clear error
// instead of Node's default ERR_MODULE_NOT_FOUND.
const require = createRequire(`${process.cwd()}/`);
let chromium;
try {
  const pwPath = require.resolve('playwright');
  // playwright's package main is CJS, so dynamic import returns
  // { default: <module>, 'module.exports': <module> } — the chromium
  // export lives on `.default` (or directly on the namespace under
  // ESM-aware setups). Try both for forward-compat.
  const ns = await import(pathToFileURL(pwPath).href);
  chromium = ns.chromium ?? ns.default?.chromium;
  if (!chromium) {
    throw new Error(
      'imported module does not export `chromium` (got keys: ' +
        Object.keys(ns).join(', ') +
        ')',
    );
  }
} catch (err) {
  console.error(
    'playwright is not installed (or load failed). Install it once with:\n' +
      '  cd ui && npm install --no-save playwright && npx playwright install chromium\n' +
      `(underlying error: ${err.message})`,
  );
  process.exit(1);
}

const __dirname = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(__dirname, '..');

const BASE_URL = process.env.SHARKO_URL || 'http://localhost:8080';
const USER = process.env.SHARKO_USER || 'admin';
const PASS = process.env.SHARKO_PASS || 'admin';
const OUT_DIR =
  process.env.OUT_DIR || resolve(REPO_ROOT, 'docs', 'site', 'assets', 'screenshots');

mkdirSync(OUT_DIR, { recursive: true });

// Each shot: route + filename + optional pre-snap callback (e.g. wait for a
// specific element, click into a tab). The label is the page heading we
// expect to see before snapping — used as a render-complete signal.
const SHOTS = [
  {
    name: 'dashboard.png',
    route: '/dashboard',
    waitFor: 'h1, h2',
  },
  {
    name: 'marketplace-browse.png',
    route: '/addons?tab=marketplace',
    waitFor: 'h1, h2',
  },
  {
    name: 'marketplace-detail.png',
    route: '/addons?tab=marketplace',
    // Open the first marketplace card so we capture the in-page detail
    // view rather than the grid.
    after: async (page) => {
      const card = page.getByRole('button', { name: /Open /i }).first();
      if (await card.count()) {
        await card.click();
        await page.waitForTimeout(800);
      }
    },
  },
  {
    name: 'cluster-detail.png',
    // Demo seeds at least one managed cluster; pick the first one.
    route: '/clusters',
    after: async (page) => {
      const link = page.getByRole('link').filter({ hasText: /Open|View/ }).first();
      if (await link.count()) {
        await link.click();
        await page.waitForLoadState('networkidle');
      } else {
        // Fall back to the first card with a heading.
        const card = page.locator('[role="article"], a[href^="/clusters/"]').first();
        if (await card.count()) {
          await card.click();
          await page.waitForLoadState('networkidle');
        }
      }
    },
  },
  {
    name: 'audit-log.png',
    route: '/audit',
    waitFor: 'h1',
  },
];

async function login(page) {
  await page.goto(`${BASE_URL}/login`, { waitUntil: 'networkidle' });
  // Wait for the username field to actually mount (the login bundle is
  // lazy-loaded) before typing into it.
  try {
    await page.waitForSelector('#username', { timeout: 10_000 });
  } catch {
    // Already past login (sessionStorage token survived) — nothing to do.
    return;
  }
  await page.fill('#username', USER);
  await page.fill('#password', PASS);
  await page.getByRole('button', { name: /sign in|log in|login/i }).click();
  // The SPA doesn't actually navigate after login — Login.tsx is replaced
  // in-place by the Layout once useAuth().isAuthenticated flips to true.
  // Wait for the username input to disappear (the clearest signal the
  // Login view has been swapped out for the Layout).
  await page.waitForSelector('#username', { state: 'detached', timeout: 15_000 });

  // Demo mode lands on the FirstRunWizard at Step 4 because the in-memory
  // demo Git provider isn't bootstrapped. Skip past the wizard so the
  // screenshots capture real pages, not setup overlays.
  await dismissWizardIfPresent(page);
}

async function dismissWizardIfPresent(page) {
  const closeButton = page.getByTitle('Skip to Dashboard');
  if (await closeButton.count()) {
    await closeButton.first().click();
    await page.waitForTimeout(500);
  }
}

async function capture(page, shot) {
  await page.goto(`${BASE_URL}${shot.route}`, { waitUntil: 'networkidle' });
  await dismissWizardIfPresent(page);
  if (shot.waitFor) {
    // Best-effort: don't fail the whole shot if the selector never
    // appears — we still want a screenshot of the rendered page.
    try {
      await page.waitForSelector(shot.waitFor, { timeout: 10_000 });
    } catch {
      console.warn(`  (waitFor "${shot.waitFor}" timed out — capturing anyway)`);
    }
  }
  if (shot.after) {
    await shot.after(page);
  }
  // Small settle so animations finish.
  await page.waitForTimeout(800);
  const out = resolve(OUT_DIR, shot.name);
  await page.screenshot({ path: out, fullPage: true });
  console.log(`✓ ${shot.name}  ←  ${shot.route}`);
}

async function main() {
  console.log(`Connecting to ${BASE_URL} …`);
  const browser = await chromium.launch();
  const context = await browser.newContext({
    viewport: { width: 1920, height: 1080 },
    deviceScaleFactor: 2, // retina-quality PNGs
    colorScheme: 'light',
  });
  const page = await context.newPage();

  try {
    await login(page);
    for (const shot of SHOTS) {
      try {
        await capture(page, shot);
      } catch (err) {
        console.error(`✗ ${shot.name} failed: ${err.message}`);
      }
    }
  } finally {
    await browser.close();
  }
  console.log(`\nWrote screenshots to ${OUT_DIR}`);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});

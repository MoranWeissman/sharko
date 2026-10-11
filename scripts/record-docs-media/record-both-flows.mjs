#!/usr/bin/env node
/**
 * Record both flows for Sharko documentation.
 * Strict fail-hard version following exact UI paths.
 *
 * Prerequisites:
 * - Sharko demo server with version 4.0.5 on http://localhost:8080
 * - Playwright installed
 */

import { chromium } from 'playwright';
import { mkdir } from 'fs/promises';
import { join, dirname } from 'path';
import { fileURLToPath } from 'url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const OUTPUT_DIR = join(__dirname, '../../_video-recordings-final');
const FRAMES_DIR = '/tmp/sharko-gif-frames-v3';
const VIEWPORT = { width: 1280, height: 800 };
const BASE_URL = 'http://localhost:8080';

const ADMIN_USER = 'admin';
const ADMIN_PASS = 'admin';

// Pacing: key frames 5-6s, medium 3s, short transitions 1.5s, very short 0.8s
const HOLD_KEY = 6000;
const HOLD_MEDIUM = 3000;
const HOLD_SHORT = 1500;
const HOLD_VERY_SHORT = 800;
const TYPING_DELAY = 80;

let frameCounter = 0;

async function setupPage(browser) {
  const context = await browser.newContext({
    viewport: VIEWPORT,
    recordVideo: {
      dir: OUTPUT_DIR,
      size: VIEWPORT,
    },
  });
  const page = await context.newPage();
  return { context, page };
}

async function saveFrame(page, flowName, stepName) {
  frameCounter++;
  const framePath = join(FRAMES_DIR, flowName, `${String(frameCounter).padStart(2, '0')}-${stepName}.png`);
  await mkdir(dirname(framePath), { recursive: true });
  await page.screenshot({ path: framePath });
  console.log(`  📸 ${stepName}`);
  return framePath;
}

async function loginFast(page) {
  await page.goto(BASE_URL);
  await page.waitForLoadState('networkidle');
  await page.locator('input[type="text"]').waitFor({ state: 'visible', timeout: 10000 });
  await page.fill('input[type="text"]', ADMIN_USER, { delay: TYPING_DELAY });
  await page.fill('input[type="password"]', ADMIN_PASS, { delay: TYPING_DELAY });
  await page.click('button[type="submit"]');
  await page.waitForURL('**/dashboard', { timeout: 10000 });
  await page.waitForLoadState('networkidle');
  await page.waitForTimeout(HOLD_VERY_SHORT);
}

/**
 * Flow A: Add kyverno from Marketplace.
 * Path: Marketplace → search → detail → form → Preview → [preview] → Add to catalog → PR opened
 */
async function recordFlowA(browser) {
  console.log('\n📹 Flow A: Add kyverno from Marketplace');
  frameCounter = 0;
  const { context, page } = await setupPage(browser);

  try {
    // Login (will be in the video but we'll trim it later)
    await loginFast(page);

    console.log('  → Opening Marketplace');
    await page.click('text=Addons');
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(HOLD_VERY_SHORT);

    const marketplaceTab = page.locator('button[role="tab"]', { hasText: 'Marketplace' });
    await marketplaceTab.waitFor({ state: 'visible', timeout: 10000 });
    await marketplaceTab.click();
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(HOLD_SHORT);
    await saveFrame(page, 'add-addon', 'marketplace');

    console.log('  → Searching kyverno');
    const searchInput = page.locator('input[placeholder*="Name, description"]');
    await searchInput.waitFor({ state: 'visible', timeout: 10000 });
    await searchInput.fill('kyverno', { delay: TYPING_DELAY });
    await page.waitForTimeout(HOLD_SHORT);
    await saveFrame(page, 'add-addon', 'search');

    console.log('  → Opening kyverno detail');
    const kyvernoCard = page.locator('.ring-2').filter({ hasText: 'kyverno' }).first();
    await kyvernoCard.waitFor({ state: 'visible', timeout: 10000 });
    await kyvernoCard.click();
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(HOLD_MEDIUM);
    await saveFrame(page, 'add-addon', 'detail');

    console.log('  → Clicking Preview');
    const previewButton = page.locator('button', { hasText: 'Preview' }).first();
    await previewButton.waitFor({ state: 'visible', timeout: 10000 });
    await previewButton.scrollIntoViewIfNeeded();
    await previewButton.click();
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(HOLD_KEY);
    await saveFrame(page, 'add-addon', 'preview');

    console.log('  → Clicking Add to catalog');
    const addButton = page.locator('button', { hasText: /Add to catalog/i }).first();
    await addButton.waitFor({ state: 'visible', timeout: 10000 });
    await addButton.click();
    await page.waitForTimeout(HOLD_SHORT);

    console.log('  → Waiting for PR result');
    await page.locator('text=/PR (created|opened)/i').waitFor({ state: 'visible', timeout: 10000 });
    await page.waitForTimeout(HOLD_KEY);
    await saveFrame(page, 'add-addon', 'result');

    console.log('  ✓ Flow A complete');
  } catch (error) {
    console.error(`  ❌ ${error.message}`);
    await saveFrame(page, 'add-addon', 'error');
    throw error;
  } finally {
    await context.close();
  }
}

/**
 * Flow B: Enable addon on cluster.
 * Path: /clusters/dev-us → Add addon → pick addon → preview → submit → result → show cluster
 */
async function recordFlowB(browser) {
  console.log('\n📹 Flow B: Enable addon on cluster');
  frameCounter = 0;
  const { context, page } = await setupPage(browser);

  try {
    // Login (will be in the video but we'll trim it later)
    await loginFast(page);

    console.log('  → Opening cluster dev-us');
    await page.goto(`${BASE_URL}/clusters/dev-us`);
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(HOLD_MEDIUM);
    await saveFrame(page, 'enable-addon', 'cluster-detail');

    console.log('  → Clicking Add addon');
    const addAddonBtn = page.locator('[data-testid="manage-addons-enable-btn"]');
    await addAddonBtn.waitFor({ state: 'visible', timeout: 10000 });
    await addAddonBtn.click();
    await page.waitForTimeout(HOLD_SHORT);
    await saveFrame(page, 'enable-addon', 'picker-open');

    console.log('  → Selecting datadog');
    // dev-us has cert-manager, metrics-server, vault
    // Pick datadog which is in catalog but not on this cluster
    // Wait for the picker list to load
    await page.locator('text=datadog').waitFor({ state: 'visible', timeout: 10000 });
    await page.waitForTimeout(HOLD_SHORT);
    await saveFrame(page, 'enable-addon', 'picker-with-options');
    await page.click('text=datadog');
    await page.waitForTimeout(HOLD_SHORT);
    await saveFrame(page, 'enable-addon', 'addon-selected');

    console.log('  → Clicking Preview');
    const previewBtn = page.locator('button', { hasText: 'Preview' });
    await previewBtn.waitFor({ state: 'visible', timeout: 10000 });
    await previewBtn.click();
    await page.waitForTimeout(HOLD_SHORT);

    console.log('  → Waiting for preview');
    // Wait for DryRunPreview to render (it shows file changes with PR Title)
    await page.locator('text=PR Title:').waitFor({ state: 'visible', timeout: 10000 });
    await page.waitForTimeout(HOLD_KEY);
    await saveFrame(page, 'enable-addon', 'preview');

    console.log('  → Clicking Confirm');
    const confirmBtn = page.locator('[data-testid="v4-confirm"]');
    await confirmBtn.waitFor({ state: 'visible', timeout: 10000 });
    await confirmBtn.click();
    await page.waitForTimeout(HOLD_SHORT);

    console.log('  → Waiting for result');
    // Wait for PR result banner
    await page.locator('text=/PR (merged|opened)/i').waitFor({ state: 'visible', timeout: 10000 });
    await page.waitForTimeout(HOLD_KEY);
    await saveFrame(page, 'enable-addon', 'result');

    console.log('  → Closing dialog');
    const doneBtn = page.locator('button', { hasText: 'Done' });
    await doneBtn.waitFor({ state: 'visible', timeout: 10000 });
    await doneBtn.click();
    await page.waitForTimeout(HOLD_MEDIUM);
    await saveFrame(page, 'enable-addon', 'cluster-updated');

    console.log('  ✓ Flow B complete');
  } catch (error) {
    console.error(`  ❌ ${error.message}`);
    await saveFrame(page, 'enable-addon', 'error');
    throw error;
  } finally {
    await context.close();
  }
}

async function main() {
  console.log('🦈 Sharko Documentation GIF Recorder\n');
  console.log(`Videos: ${OUTPUT_DIR}`);
  console.log(`Frames: ${FRAMES_DIR}\n`);

  await mkdir(OUTPUT_DIR, { recursive: true });
  await mkdir(FRAMES_DIR, { recursive: true });

  const browser = await chromium.launch({
    headless: false,
    slowMo: 25,
  });

  try {
    await recordFlowA(browser);
    await recordFlowB(browser);

    console.log('\n✅ Both flows complete!');
    console.log(`\nFrames: ${FRAMES_DIR}`);
    console.log('Videos: see _video-recordings-final/\n');
  } catch (error) {
    console.error('\n❌ Recording failed:', error);
    process.exit(1);
  } finally {
    await browser.close();
  }
}

main().catch(console.error);

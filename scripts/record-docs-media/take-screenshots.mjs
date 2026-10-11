#!/usr/bin/env node
/**
 * Take screenshots for Sharko documentation.
 * Takes 5 screenshots at 1280x800 resolution.
 *
 * Usage: node scripts/record-docs-media/take-screenshots.mjs
 *
 * Prerequisites:
 * - Sharko demo server running on http://localhost:8080
 * - Playwright installed (npx playwright install chromium)
 */

import { chromium } from 'playwright';
import { join, dirname } from 'path';
import { fileURLToPath } from 'url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const OUTPUT_DIR = join(__dirname, '../../docs/site/assets/screenshots');
const VIEWPORT = { width: 1280, height: 800 };
const BASE_URL = 'http://localhost:8080';

// Demo credentials
const ADMIN_USER = 'admin';
const ADMIN_PASS = 'admin';

async function takeScreenshots() {
  console.log('🦈 Sharko Documentation Screenshot Tool\n');
  console.log(`Output directory: ${OUTPUT_DIR}\n`);

  const browser = await chromium.launch({
    headless: true,
  });

  const context = await browser.newContext({
    viewport: VIEWPORT,
  });

  const page = await context.newPage();

  try {
    // Login
    console.log('Logging in...');
    await page.goto(BASE_URL);
    await page.waitForLoadState('networkidle');
    await page.fill('input[type="text"]', ADMIN_USER);
    await page.fill('input[type="password"]', ADMIN_PASS);
    await page.click('button[type="submit"]');
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(2000);

    // 1. Dashboard
    console.log('📸 Taking screenshot: dashboard.png');
    await page.goto(`${BASE_URL}/dashboard`);
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(2000);
    await page.screenshot({ path: join(OUTPUT_DIR, 'dashboard.png'), fullPage: false });

    // 2. Marketplace browse
    console.log('📸 Taking screenshot: marketplace-browse.png');
    await page.goto(`${BASE_URL}/addons`);
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(2000);
    await page.screenshot({ path: join(OUTPUT_DIR, 'marketplace-browse.png'), fullPage: false });

    // 3. Marketplace detail (cert-manager)
    console.log('📸 Taking screenshot: marketplace-detail.png');
    await page.waitForSelector('text=cert-manager', { timeout: 10000 });
    await page.click('text=cert-manager');
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(2000);
    await page.screenshot({ path: join(OUTPUT_DIR, 'marketplace-detail.png'), fullPage: false });

    // 4. Clusters view and then cluster detail
    console.log('📸 Taking screenshot: cluster-detail.png');
    await page.goto(`${BASE_URL}/clusters`);
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(2000);

    // Try to click on first visible cluster card or link
    const possibleSelectors = [
      'a[href*="/clusters/"]',
      '[data-testid="cluster-card"] a',
      '.cluster-card a',
      'text=/prod|staging|dev|cluster/i',
    ];

    let clusterFound = false;
    for (const selector of possibleSelectors) {
      try {
        const element = page.locator(selector).first();
        if (await element.isVisible({ timeout: 1000 })) {
          await element.click();
          await page.waitForLoadState('networkidle');
          await page.waitForTimeout(2000);
          await page.screenshot({ path: join(OUTPUT_DIR, 'cluster-detail.png'), fullPage: false });
          clusterFound = true;
          break;
        }
      } catch (e) {
        // Try next selector
      }
    }

    if (!clusterFound) {
      // Try direct navigation to common cluster names
      const commonNames = ['prod', 'staging', 'dev', 'prod-eu', 'prod-us'];
      for (const name of commonNames) {
        try {
          await page.goto(`${BASE_URL}/clusters/${name}`);
          await page.waitForLoadState('networkidle');
          await page.waitForTimeout(2000);

          // Check if we got a real page (not 404)
          const content = await page.content();
          if (!content.includes('404') && !content.includes('Not Found')) {
            await page.screenshot({ path: join(OUTPUT_DIR, 'cluster-detail.png'), fullPage: false });
            clusterFound = true;
            break;
          }
        } catch (e) {
          // Try next name
        }
      }
    }

    if (!clusterFound) {
      console.log('  ⚠️  No cluster found, skipping cluster-detail screenshot');
    }

    // 5. Audit log
    console.log('📸 Taking screenshot: audit-log.png');
    // Try different possible routes for audit log
    const auditRoutes = ['/audit', '/observability'];
    let auditFound = false;

    for (const route of auditRoutes) {
      try {
        await page.goto(`${BASE_URL}${route}`);
        await page.waitForLoadState('networkidle');
        await page.waitForTimeout(2000);

        // Check if we're on an actual page (not 404)
        const content = await page.content();
        if (!content.includes('404') && !content.includes('Not Found')) {
          await page.screenshot({ path: join(OUTPUT_DIR, 'audit-log.png'), fullPage: false });
          auditFound = true;
          break;
        }
      } catch (e) {
        // Try next route
      }
    }

    if (!auditFound) {
      console.log('  ⚠️  Audit log page not found, skipping screenshot');
    }

    console.log('\n✅ All screenshots taken!');
    console.log(`\nScreenshots saved to: ${OUTPUT_DIR}`);
  } finally {
    await browser.close();
  }
}

takeScreenshots().catch(console.error);

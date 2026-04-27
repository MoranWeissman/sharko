/**
 * signals.mjs — best-effort signal pre-compute helpers for the
 * scanner PR-opener (V123-3.4).
 *
 * Three signals — Scorecard score, Helm chart resolvability, license
 * SPDX allow-list classification — are computed per scanner proposal
 * and pasted into the PR body table so reviewers can decide quickly
 * whether to merge / edit / close the proposal.
 *
 * ## Design intent
 *
 * - **Best-effort, non-fatal.** Every helper returns either a useful
 *   structured value OR the sentinel `'unknown'`. A network blip or
 *   404 must NEVER abort the PR-opener — the scanner ran, the
 *   proposal is real, and the reviewer can compute the signal by
 *   hand if needed.
 * - **No persistence between runs.** Per NFR-V123-1 (stateless): each
 *   scan recomputes from scratch. The chart-index cache is per-call
 *   (Map keyed by repo URL inside `chartIndexResolves`) so multi-add
 *   proposals against the same Helm repo don't hammer the URL.
 * - **HTTP via `lib/http.mjs`.** Reuses the V123-3.1 retry/backoff
 *   wrapper (3 retries, 1s/2s/4s backoff, UA `sharko-catalog-scan/1.0`).
 *   Pure callers can stub by passing a `ctx.http` field.
 *
 * ## Allow-list (license)
 *
 * Mirrored from `catalog/schema.json` — the four SPDX values the
 * curated catalog accepts without flagging:
 *   `Apache-2.0`, `BSD-3-Clause`, `MIT`, `MPL-2.0`.
 * Anything else returns `flagged` so reviewers see it visually in
 * the table; `unknown` (literally the string `"unknown"` or absent)
 * stays `unknown`.
 *
 * @see scripts/catalog-scan/lib/signals.test.mjs for the 6-case suite.
 */

import yamlPkg from 'yaml';
const { parse: yamlParse } = yamlPkg;

import { fetchWithRetry } from './http.mjs';

const SCORECARD_API = 'https://api.securityscorecards.dev/projects/github.com';
const LICENSE_ALLOWLIST = new Set(['Apache-2.0', 'BSD-3-Clause', 'MIT', 'MPL-2.0']);

/**
 * Look up an OpenSSF Scorecard for a github.com repo URL. Anonymous
 * API call — generous rate limit, but non-fatal on any failure.
 *
 * @param {string} repoUrl - Source repo URL. Only `https://github.com/owner/repo`
 *   shapes are queried; anything else returns `'unknown'`.
 * @param {object} ctx - Plugin-style context. Optional `http` override
 *   for tests; otherwise uses lib/http.mjs's fetchWithRetry. Optional
 *   `logger` for warn on failure.
 * @returns {Promise<{score: number, updated: string} | 'unknown'>}
 */
export async function scorecardForRepo(repoUrl, ctx = {}) {
  const repoSlug = parseGithubSlug(repoUrl);
  if (!repoSlug) return 'unknown';
  const url = `${SCORECARD_API}/${repoSlug}`;
  try {
    const http = ctx.http ?? fetchWithRetry;
    const res = await http(url);
    if (!res.ok) {
      // 404 = never-scored; other 4xx/5xx = transient or upstream issue.
      // Either way, "unknown" is the right answer for the PR body.
      return 'unknown';
    }
    const body = await res.json();
    const score = typeof body?.score === 'number' ? body.score : null;
    const updated = typeof body?.date === 'string' ? body.date : '';
    if (score === null) return 'unknown';
    return { score, updated };
  } catch (err) {
    if (ctx.logger?.warn) {
      ctx.logger.warn('scorecardForRepo failed (non-fatal)', { repo: repoSlug, error: err.message ?? String(err) });
    }
    return 'unknown';
  }
}

/**
 * Check whether a Helm chart name resolves in the upstream repo's
 * index.yaml. OCI registries return a sentinel — `helm pull oci://`
 * is a different verification path the scanner doesn't attempt here.
 *
 * @param {string} repoUrl - Helm repo URL (`https://...` or `oci://...`).
 * @param {string} chartName - Chart name to look for in `entries.<chartName>`.
 * @param {object} ctx - Optional `{http, logger, _cache?}`. The
 *   `_cache` (Map) is supplied by the caller so multiple proposals
 *   against the same repo URL share parsed index.yaml.
 * @returns {Promise<'ok' | 'missing' | 'oci-not-checked' | 'unknown'>}
 */
export async function chartIndexResolves(repoUrl, chartName, ctx = {}) {
  if (typeof repoUrl !== 'string' || repoUrl.length === 0) return 'unknown';
  if (repoUrl.startsWith('oci://')) return 'oci-not-checked';
  if (!repoUrl.startsWith('http://') && !repoUrl.startsWith('https://')) return 'unknown';

  const cache = ctx._cache instanceof Map ? ctx._cache : null;
  let parsedIndex;
  if (cache && cache.has(repoUrl)) {
    parsedIndex = cache.get(repoUrl);
  } else {
    parsedIndex = await fetchIndex(repoUrl, ctx);
    if (cache) cache.set(repoUrl, parsedIndex);
  }

  if (parsedIndex === 'unknown') return 'unknown';
  const entries = parsedIndex?.entries;
  if (entries && Object.prototype.hasOwnProperty.call(entries, chartName)) {
    return 'ok';
  }
  return 'missing';
}

/**
 * Inspect a parsed Helm index for the chart's license + classify
 * against the schema allow-list.
 *
 * @param {object|null} chartIndex - Parsed `index.yaml` object (the
 *   `_cache.get(repoUrl)` value), or null when no index is available.
 * @param {string} chartName - Chart name to look up.
 * @returns {{value: string, status: 'ok' | 'flagged' | 'unknown'}}
 *   - `value`: the license string from the index (or `'unknown'`).
 *   - `status`: `ok` if in allow-list; `flagged` for any other named
 *     license; `unknown` if absent.
 */
export function licenseFromChart(chartIndex, chartName) {
  if (!chartIndex || typeof chartIndex !== 'object') {
    return { value: 'unknown', status: 'unknown' };
  }
  const versions = chartIndex.entries?.[chartName];
  if (!Array.isArray(versions) || versions.length === 0) {
    return { value: 'unknown', status: 'unknown' };
  }
  // Pick the first listed version's license — Helm puts newest first
  // by convention; we don't try to find "the latest" because licenses
  // virtually never change between chart versions of the same project.
  const license = versions[0]?.annotations?.['artifacthub.io/license']
    ?? versions[0]?.license
    ?? 'unknown';
  if (license === 'unknown' || license === '' || license == null) {
    return { value: 'unknown', status: 'unknown' };
  }
  if (LICENSE_ALLOWLIST.has(license)) {
    return { value: license, status: 'ok' };
  }
  return { value: license, status: 'flagged' };
}

/* ------------------------------------------------------------------ */
/* Helpers                                                              */
/* ------------------------------------------------------------------ */

function parseGithubSlug(repoUrl) {
  if (typeof repoUrl !== 'string') return null;
  let url;
  try {
    url = new URL(repoUrl);
  } catch {
    return null;
  }
  if (url.hostname !== 'github.com') return null;
  const parts = url.pathname.split('/').filter(Boolean);
  if (parts.length < 2) return null;
  // Strip a trailing `.git` suffix if present (cosmetic; rarely used
  // for `source_url` but cheap to handle).
  const repo = parts[1].replace(/\.git$/, '');
  return `${parts[0]}/${repo}`;
}

async function fetchIndex(repoUrl, ctx) {
  const http = ctx.http ?? fetchWithRetry;
  const indexUrl = repoUrl.endsWith('/') ? `${repoUrl}index.yaml` : `${repoUrl}/index.yaml`;
  try {
    const res = await http(indexUrl);
    if (!res.ok) {
      if (ctx.logger?.warn) {
        ctx.logger.warn('chart index fetch non-2xx', { url: indexUrl, status: res.status });
      }
      return 'unknown';
    }
    const text = await res.text();
    return yamlParse(text);
  } catch (err) {
    if (ctx.logger?.warn) {
      ctx.logger.warn('chart index fetch failed (non-fatal)', { url: indexUrl, error: err.message ?? String(err) });
    }
    return 'unknown';
  }
}

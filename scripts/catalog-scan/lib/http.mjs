/**
 * http.mjs — thin retry-wrapped fetch helper for scanner plugins.
 *
 * - 3 retries with exponential backoff (1s, 2s, 4s); honors AbortSignal.
 * - Sets `User-Agent: sharko-catalog-scan/1.0` so upstreams can identify
 *   the scanner traffic.
 * - Uses Node 18+'s built-in `fetch` — no node-fetch dep needed.
 *
 * Plugins call `ctx.http(url, opts)` instead of bare `fetch()` so
 * retries + UA stay uniform across sources.
 */

const USER_AGENT = 'sharko-catalog-scan/1.0';
const DEFAULT_RETRIES = 3;
const BACKOFF_MS = [1000, 2000, 4000];

/**
 * @param {string|URL} url
 * @param {RequestInit & {retries?: number, sleeper?: (ms:number)=>Promise<void>}} opts
 * @returns {Promise<Response>}
 */
export async function fetchWithRetry(url, opts = {}) {
  const retries = opts.retries ?? DEFAULT_RETRIES;
  const sleep = opts.sleeper ?? defaultSleep;
  const signal = opts.signal;
  const headers = mergeHeaders(opts.headers, { 'User-Agent': USER_AGENT });
  const requestInit = { ...opts, headers };
  delete requestInit.retries;
  delete requestInit.sleeper;

  let lastErr;
  for (let attempt = 0; attempt <= retries; attempt++) {
    if (signal?.aborted) {
      throw signal.reason ?? new Error('aborted');
    }
    try {
      const res = await fetch(url, requestInit);
      // Retry on 5xx; accept 4xx as a definitive answer the caller can act on.
      if (res.status >= 500 && attempt < retries) {
        lastErr = new Error(`upstream ${res.status} ${res.statusText}`);
      } else {
        return res;
      }
    } catch (err) {
      if (err?.name === 'AbortError') throw err;
      lastErr = err;
      if (attempt >= retries) break;
    }
    const delay = BACKOFF_MS[Math.min(attempt, BACKOFF_MS.length - 1)];
    await sleep(delay);
  }
  throw lastErr ?? new Error('fetchWithRetry: exhausted retries with no error');
}

function mergeHeaders(existing, extra) {
  const h = new Headers(existing ?? {});
  for (const [k, v] of Object.entries(extra)) {
    if (!h.has(k)) h.set(k, v);
  }
  return h;
}

function defaultSleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

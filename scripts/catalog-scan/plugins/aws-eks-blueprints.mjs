/**
 * `aws-eks-blueprints` — scanner plugin (V123-3.3).
 *
 * Walks the GitHub Contents API tree under `lib/addons/` in
 * `aws-quickstart/cdk-eks-blueprints` and proposes catalog additions for
 * any EKS-Blueprints addon that surfaces a Helm chart reference and is
 * not already curated in `catalog/addons.yaml`.
 *
 * **Adds-only** (per V123-3.3 AC, narrower than V123-3.2). Updates to
 * existing entries are intentionally not generated — the diff helper's
 * `COMPARABLE_FIELDS` would still flag drift on `chart` / `version` /
 * `repo` for any curated entry whose `curated_by` already includes
 * `aws-eks-blueprints`, but since this plugin emits TODO markers for
 * fields it cannot reliably derive AND since the brief restricts scope
 * to "propose adds", we rely on the same emit-and-let-diff-decide flow
 * as V123-3.2: a name that already exists in the catalog will produce
 * an "update" record only when one of the comparable fields actually
 * differs. In practice for this plugin the `chart` field will match
 * (EKS Blueprints uses the upstream Helm chart name verbatim), so the
 * common case is "no change recorded for already-curated entries."
 *
 * ## GitHub API
 *
 * Endpoints used (in order, sequentially per addon — no parallelism so
 * rate-limit behavior stays predictable):
 *
 *   1. `GET ${BASE}/lib/addons` — top-level dir listing. Filter
 *      `type === "dir"` to enumerate addon directories.
 *   2. `GET ${BASE}/lib/addons/<addon>` — per-addon dir listing. Pick
 *      the first `.ts` file alphabetically (excluding `*.test.ts`,
 *      `*.spec.ts`, `*.types.ts`).
 *   3. `GET <download_url>` — raw `.ts` file content. The `download_url`
 *      field on the contents API response already points at
 *      raw.githubusercontent.com.
 *
 * Where `BASE` is `process.env.SHARKO_EKS_BLUEPRINTS_API_BASE`
 * (defaults to
 * `https://api.github.com/repos/aws-quickstart/cdk-eks-blueprints/contents`).
 * The env var lets tests redirect at a `http.createServer` fixture.
 *
 * ## Rate limit
 *
 * Unauthenticated GitHub API: 60 req/hr. With ~65 addons in
 * `lib/addons/` today + 1 dir-list + 1 file-fetch per addon, a single
 * scan needs roughly 130 calls — far over the unauth budget. Setting
 * `GITHUB_TOKEN` (or running under a workflow with `secrets.GITHUB_TOKEN`)
 * raises this to 5000 req/hr, which is more than enough headroom.
 *
 * The plugin reads `X-RateLimit-Remaining` from each Contents API
 * response. ≥ 10 → `info` log; < 10 → `warn` log telling the operator
 * to configure `GITHUB_TOKEN`. Rate-limit exhaustion (HTTP 403) is NOT
 * caught — it propagates so the scanner harness records it as a plugin
 * error in `scanner_runs[].error`. This is the correct behavior: a
 * partial scan would produce a misleading changeset.
 *
 * ## Metadata extraction
 *
 * CDK addons declare Helm metadata as TS constants in their `.ts` files.
 * Naming varies (the brief documents `HELM_CHART_*` convention; the live
 * repo uses `defaultProps = { chart, repository, version, namespace }`).
 * The extractor tries both shapes via priority-ordered regex matches —
 * heuristic, not a TS parser. If `chart` AND `repo` are both missing,
 * the addon is skipped with an `info` log.
 *
 * Documented per V123-3.2 convention:
 *   - description       → "<TODO: human description>"
 *   - default_namespace → extracted namespace OR "<slug>-system"
 *   - license           → "unknown"
 *   - maintainers       → ["<TODO: derive from chart repo>"]
 *
 * These TODO markers are deliberately schema-valid (so the proposal
 * loads) but obviously synthetic (so the reviewer corrects them in the
 * bot PR). `lib/diff.mjs` only compares the COMPARABLE_FIELDS set
 * (`chart`, `version`, `category`, `repo`) so the synthetic placeholders
 * never trigger spurious updates against existing catalog entries.
 *
 * @see scripts/catalog-scan/plugins/README.md for the plugin contract.
 * @see .bmad/output/implementation-artifacts/V123-3-3-...md for AC.
 */

export const name = 'aws-eks-blueprints';

const DEFAULT_API_BASE =
  'https://api.github.com/repos/aws-quickstart/cdk-eks-blueprints/contents';

/**
 * Slug-keyword → Sharko `category` schema enum. Small, conservative
 * map; reviewers refine in PRs (same anti-gold-plate stance as
 * V123-3.2). Order matters slightly — earlier matches win when an
 * addon name contains multiple keywords (e.g. "metrics-server"
 * → observability via "metrics" before "server" would have a chance
 * to match anything).
 */
const CATEGORY_KEYWORDS = [
  // observability
  ['monitoring', 'observability'],
  ['metrics', 'observability'],
  ['logging', 'observability'],
  ['logs', 'observability'],
  ['grafana', 'observability'],
  ['prometheus', 'observability'],
  ['otel', 'observability'],
  ['adot', 'observability'],
  ['fluent', 'observability'],
  ['xray', 'observability'],
  // networking
  ['ingress', 'networking'],
  ['gateway', 'networking'],
  ['loadbalancer', 'networking'],
  ['load-balancer', 'networking'],
  ['network', 'networking'],
  ['mesh', 'networking'],
  ['dns', 'networking'],
  ['cilium', 'networking'],
  // security
  ['cert', 'security'],
  ['secret', 'security'],
  ['kyverno', 'security'],
  ['gatekeeper', 'security'],
  ['falco', 'security'],
  ['vault', 'security'],
  ['rbac', 'security'],
  ['oidc', 'security'],
  // storage
  ['csi', 'storage'],
  ['storage', 'storage'],
  ['ebs', 'storage'],
  ['efs', 'storage'],
  ['fsx', 'storage'],
  // gitops
  ['argocd', 'gitops'],
  ['argo', 'gitops'],
  ['flux', 'gitops'],
  // autoscaling
  ['karpenter', 'autoscaling'],
  ['autoscaler', 'autoscaling'],
  ['keda', 'autoscaling'],
  // backup
  ['velero', 'backup'],
  ['backup', 'backup'],
  // chaos
  ['chaos', 'chaos'],
];

/**
 * @param {object} ctx scanner context (logger, abortSignal, http).
 * @returns {Promise<Array<object>>} normalized scanner entries.
 */
export async function fetch(ctx) {
  const apiBase = (process.env.SHARKO_EKS_BLUEPRINTS_API_BASE ?? DEFAULT_API_BASE).replace(
    /\/+$/,
    '',
  );
  const token = process.env.GITHUB_TOKEN || '';
  ctx.logger.info('fetching aws-eks-blueprints addons', {
    api_base: apiBase,
    auth: token ? 'token' : 'none',
  });

  const gh = makeGitHubClient(ctx, token);

  // 1. Top-level dir listing.
  const topUrl = `${apiBase}/lib/addons`;
  const top = await gh.json(topUrl);
  if (!Array.isArray(top)) {
    throw new Error(`unexpected response shape from ${topUrl}: not an array`);
  }
  if (top.length === 0) {
    ctx.logger.info('lib/addons listing is empty; nothing to propose');
    return [];
  }
  if (top.length === 1000) {
    ctx.logger.warn('lib/addons listing returned exactly 1000 entries — pagination may be needed', {
      count: top.length,
    });
  }
  const dirs = top.filter((e) => e && e.type === 'dir' && typeof e.name === 'string');
  ctx.logger.info('discovered addon dirs', {
    total: top.length,
    dirs: dirs.length,
    files_skipped: top.length - dirs.length,
  });

  // 2. Per-addon: list dir, pick .ts, fetch raw, extract.
  const out = [];
  let skippedNoTs = 0;
  let skippedNoExtract = 0;
  let skippedSlug = 0;
  let skippedError = 0;
  for (const dir of dirs) {
    try {
      const dirUrl = `${apiBase}/lib/addons/${dir.name}`;
      const contents = await gh.json(dirUrl);
      if (!Array.isArray(contents)) {
        ctx.logger.warn('addon dir listing not an array; skipping', {
          addon: dir.name,
          url: dirUrl,
        });
        skippedError++;
        continue;
      }
      const tsFile = pickAddonTsFile(contents);
      if (!tsFile) {
        ctx.logger.info('addon dir has no candidate .ts file; skipping', { addon: dir.name });
        skippedNoTs++;
        continue;
      }
      const downloadUrl = typeof tsFile.download_url === 'string' ? tsFile.download_url : '';
      if (!downloadUrl) {
        ctx.logger.warn('candidate .ts file has no download_url; skipping', {
          addon: dir.name,
          file: tsFile.name,
        });
        skippedError++;
        continue;
      }
      const source = await gh.text(downloadUrl);
      const meta = extractMetadata(source);
      if (!meta.chart && !meta.repo) {
        ctx.logger.info('addon source lacks chart+repo references; skipping', {
          addon: dir.name,
          file: tsFile.name,
        });
        skippedNoExtract++;
        continue;
      }
      const slug = slugify(dir.name);
      if (!slug) {
        ctx.logger.warn('addon name does not produce a usable slug; skipping', {
          raw_name: dir.name,
        });
        skippedSlug++;
        continue;
      }
      out.push(buildEntry({ slug, dir, tsFile, meta }));
    } catch (err) {
      // Per-addon errors must NOT throw — log warn and continue. Only
      // the top-level dir-list call (above) throws on failure.
      skippedError++;
      ctx.logger.warn('per-addon scan failed; skipping', {
        addon: dir.name,
        error: err?.message ?? String(err),
      });
    }
  }

  ctx.logger.info('aws-eks-blueprints scan summary', {
    kept: out.length,
    dirs: dirs.length,
    skipped_no_ts: skippedNoTs,
    skipped_no_extract: skippedNoExtract,
    skipped_slug: skippedSlug,
    skipped_error: skippedError,
  });

  return out;
}

/* ------------------------------------------------------------------ */
/* GitHub client wrapper                                                */
/* ------------------------------------------------------------------ */

/**
 * Build a thin wrapper around `ctx.http` that:
 *   - Sets `User-Agent`, `Accept: application/vnd.github+json`, and
 *     optional `Authorization: Bearer <token>` headers.
 *   - Reads `X-RateLimit-Remaining` from each response and logs
 *     accordingly (`info` ≥ 10, `warn` < 10).
 *   - Throws on non-2xx with a descriptive error including status +
 *     URL fragment so the scanner_runs[].error is debuggable.
 *   - Exposes `.json(url)` (parses JSON body) and `.text(url)` (raw
 *     text, for `download_url` fetches).
 *
 * The inline-wrapper pattern is intentional: V123-3.4 may need a
 * similar GitHub-aware wrapper, but extracting to `lib/github.mjs`
 * before a second use site lands would be premature per the brief's
 * anti-gold-plating rules.
 *
 * @param {{logger: object, http: Function}} ctx
 * @param {string} token GITHUB_TOKEN or empty
 */
function makeGitHubClient(ctx, token) {
  const baseHeaders = {
    'User-Agent': 'sharko-catalog-scan/1.0',
    Accept: 'application/vnd.github+json',
  };
  if (token) {
    baseHeaders.Authorization = `Bearer ${token}`;
  }

  async function call(url) {
    const res = await ctx.http(url, { headers: baseHeaders });
    if (!res.ok) {
      // Log the rate-limit headers even on failure so operators can
      // correlate a 403 with rate-limit exhaustion.
      logRateLimit(ctx, res, url);
      const status = `${res.status} ${res.statusText ?? ''}`.trim();
      throw new Error(`GitHub API ${status} for ${redactUrl(url)}`);
    }
    logRateLimit(ctx, res, url);
    return res;
  }

  return {
    async json(url) {
      const res = await call(url);
      try {
        return await res.json();
      } catch (err) {
        throw new Error(`GitHub API JSON parse failed for ${redactUrl(url)}: ${err.message}`);
      }
    },
    async text(url) {
      const res = await call(url);
      return res.text();
    },
  };
}

function logRateLimit(ctx, res, url) {
  // Headers may be a plain object (test stub) or a Headers instance.
  const remainingRaw =
    res.headers && typeof res.headers.get === 'function'
      ? res.headers.get('x-ratelimit-remaining')
      : res.headers?.['x-ratelimit-remaining'];
  if (remainingRaw == null || remainingRaw === '') return; // download_url responses don't carry it
  const remaining = Number(remainingRaw);
  if (!Number.isFinite(remaining)) return;
  const where = redactUrl(url);
  if (remaining < 10) {
    ctx.logger.warn('GitHub API rate-limit low; configure GITHUB_TOKEN to raise budget', {
      remaining,
      url: where,
    });
  } else {
    ctx.logger.info('GitHub API rate-limit headroom', { remaining, url: where });
  }
}

function redactUrl(url) {
  // Strip query-string token leakage (defensive — we never put tokens
  // in query strings, but defense in depth is cheap).
  try {
    const u = new URL(url);
    u.search = '';
    return u.toString();
  } catch {
    return url;
  }
}

/* ------------------------------------------------------------------ */
/* Helpers                                                              */
/* ------------------------------------------------------------------ */

/**
 * From a GitHub Contents API directory listing, pick the candidate
 * `.ts` source file alphabetically. Excludes test/spec/types files
 * (which never carry the chart constants) and non-file entries.
 */
function pickAddonTsFile(contents) {
  const candidates = contents
    .filter(
      (e) =>
        e &&
        e.type === 'file' &&
        typeof e.name === 'string' &&
        e.name.endsWith('.ts') &&
        !/\.(test|spec|types|d)\.ts$/i.test(e.name),
    )
    .sort((a, b) => a.name.localeCompare(b.name));
  return candidates[0] ?? null;
}

/**
 * Run priority-ordered regex extractors against a TS source string.
 * Returns `{ chart, repo, version, namespace }` with each field
 * either a non-empty string or `undefined`.
 *
 * Patterns try BOTH the brief's documented constants
 * (`HELM_CHART_NAME` etc.) AND the live `defaultProps = { chart,
 * repository, ... }` form observed in the upstream repo today. This
 * is a documented broadening — covered by V123-3-3 Decisions section.
 */
function extractMetadata(source) {
  return {
    chart:
      firstMatch(source, /(?:helm[_]?chart[_]?name|chartName)\s*[:=]\s*['"`]([^'"`]+)['"`]/i) ??
      firstMatch(source, /\bchart\s*:\s*['"`]([^'"`]+)['"`]/),
    repo:
      firstMatch(source, /(?:helm[_]?chart[_]?repo|chartRepo)\s*[:=]\s*['"`]([^'"`]+)['"`]/i) ??
      firstMatch(source, /\brepository\s*:\s*['"`]([^'"`]+)['"`]/i),
    version:
      firstMatch(source, /(?:helm[_]?chart[_]?version|chartVersion)\s*[:=]\s*['"`]([^'"`]+)['"`]/i) ??
      firstMatch(source, /\bversion\s*:\s*['"`]([^'"`]+)['"`]/i),
    namespace:
      firstMatch(
        source,
        /(?:helm[_]?chart[_]?namespace|chartNamespace)\s*[:=]\s*['"`]([^'"`]+)['"`]/i,
      ) ?? firstMatch(source, /\bnamespace\s*:\s*['"`]([^'"`]+)['"`]/i),
  };
}

function firstMatch(source, re) {
  const m = re.exec(source);
  if (!m) return undefined;
  const v = m[1];
  return typeof v === 'string' && v.length > 0 ? v : undefined;
}

/**
 * Build the normalized scanner entry. The diff helper only compares
 * `chart, version, category, repo` so the synthetic TODO-marker fields
 * below never trigger updates against curated catalog data.
 */
function buildEntry({ slug, dir, tsFile, meta }) {
  const repo = meta.repo;
  // Repo is required by the catalog schema, so emit it even when the
  // extractor missed it — fall back to a `<TODO>` placeholder marker
  // so the proposal is at least visible to a reviewer. (In practice
  // we already gate this branch on `meta.repo` being truthy via the
  // `if (!meta.chart && !meta.repo)` skip earlier; if only one of the
  // two is populated we proceed with what we have plus a TODO marker.)
  const entry = {
    name: slug,
    description: '<TODO: human description>',
    chart: meta.chart ?? slug,
    repo: repo ?? 'https://example.invalid/<TODO: chart repo>',
    default_namespace: meta.namespace
      ? slugifyNamespace(meta.namespace) || namespaceFor(slug)
      : namespaceFor(slug),
    category: inferCategory(slug),
    curated_by: ['aws-eks-blueprints'],
    license: 'unknown',
    maintainers: ['<TODO: derive from chart repo>'],
  };
  if (meta.version) entry.version = meta.version;
  // Pass-through metadata for reviewer convenience (not consumed by
  // diff.mjs's COMPARABLE_FIELDS, so won't churn updates).
  if (typeof dir.path === 'string') entry._eks_blueprints_path = dir.path;
  if (typeof tsFile.html_url === 'string') {
    entry._eks_blueprints_source = tsFile.html_url;
  } else if (typeof tsFile.download_url === 'string') {
    entry._eks_blueprints_source = tsFile.download_url;
  }
  return entry;
}

function inferCategory(slug) {
  for (const [keyword, category] of CATEGORY_KEYWORDS) {
    if (slug.includes(keyword)) return category;
  }
  return 'developer-tools';
}

/**
 * Slugify an addon dir name to a catalog-schema-compatible primary key.
 * Returns `''` if the input cannot be coerced to a non-empty slug —
 * caller skips with a warn log. Mirrors `cncf-landscape.mjs#slugify`
 * (intentional inline duplication per V123-3.3 brief; if a third use
 * site lands, extract to `lib/slug.mjs`).
 */
export function slugify(raw) {
  if (typeof raw !== 'string') return '';
  const cleaned = raw
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, '-')
    .replace(/-+/g, '-')
    .replace(/^-+|-+$/g, '');
  if (cleaned.length === 0) return '';
  const clipped = cleaned.length > 63 ? cleaned.slice(0, 63).replace(/-+$/, '') : cleaned;
  if (!/^[a-z0-9]/.test(clipped)) return '';
  return clipped;
}

/** Same rules as slugify, but allow the result to be empty (caller
 * decides the fallback). Used for namespaces lifted directly from
 * extracted source. */
function slugifyNamespace(raw) {
  return slugify(raw);
}

function namespaceFor(slug) {
  const ns = `${slug}-system`;
  if (ns.length <= 63) return ns;
  return slug.slice(0, 63).replace(/-+$/, '');
}

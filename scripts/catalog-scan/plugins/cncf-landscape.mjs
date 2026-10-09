/**
 * `cncf-landscape` — scanner plugin (V123-3.2).
 *
 * Pulls the canonical CNCF landscape YAML
 * (https://raw.githubusercontent.com/cncf/landscape/master/landscape.yml)
 * and proposes catalog adds/updates for graduated + incubating projects
 * that surface a Helm chart reference.
 *
 * Filters applied (per V123-3.2 acceptance criteria):
 *
 *   1. `project` ∈ { 'graduated', 'incubating' } — sandbox / archived /
 *      empty are skipped. Maturity is the strongest curation signal
 *      landscape.yml carries.
 *   2. Helm-only — items without a recognizable Helm chart reference
 *      (`extra.helm_chart_url`, `extra.chart_url`, `extra.helm_url`,
 *      `extra.artifacthub_url`) are skipped. Sharko deploys via Helm
 *      only (v1.21 design §6 item 10).
 *   3. Subcategory must map to a Sharko `category` schema enum value
 *      via LANDSCAPE_TO_SHARKO_CATEGORY below. Subcategories that map
 *      to `null` (Container Runtime, Scheduling & Orchestration, etc.)
 *      are skipped — those aren't Sharko-deployable addons.
 *
 * Per-item parse errors are logged at warn level and skipped — they
 * never throw. Network errors propagate to the scanner harness which
 * isolates per-plugin failures (V123-3.1 contract).
 *
 * URL override: tests can point this plugin at a fixture by setting
 * `SHARKO_CNCF_LANDSCAPE_URL` to a `file://` URL or a local
 * `http.createServer` URL.
 *
 * Field-derivation policy (intentionally minimal — humans curate the
 * rest in the bot PR):
 *
 *   - `description`        → "<TODO: human description>"   (placeholder)
 *   - `default_namespace`  → "<slug>-system"               (heuristic)
 *   - `license`            → "unknown"                     (CI flags this for human review)
 *   - `maintainers`        → ["<TODO: derive from chart repo>"]  (schema-valid + intent-marking)
 *
 * These TODO markers are deliberately schema-valid (so the proposal
 * loads) but obviously synthetic (so the reviewer corrects them).
 * Crucially, `lib/diff.mjs` only compares the COMPARABLE_FIELDS set
 * (`chart`, `version`, `category`, `repo`) — these synthetic fields
 * never trigger spurious updates against existing catalog entries.
 *
 * Slug normalization rule: lowercase + replace non-`[a-z0-9-]` with
 * `-` + collapse runs of `-` + trim leading/trailing `-` + clip to
 * 63 chars. Matches the catalog schema's primary-key regex
 * `^[a-z0-9][a-z0-9-]*[a-z0-9]$`. Document collision: two items that
 * slugify to the same name will both appear in the changeset; the
 * reviewer dedupes (per V123-3.1 plugin contract).
 *
 * @see scripts/catalog-scan/plugins/README.md for the plugin contract.
 * @see .bmad/output/implementation-artifacts/V123-3-2-...md for AC.
 */
import { parse as yamlParse } from 'yaml';

export const name = 'cncf-landscape';

const DEFAULT_LANDSCAPE_URL =
  'https://raw.githubusercontent.com/cncf/landscape/master/landscape.yml';

/**
 * Landscape subcategory → Sharko `category` schema enum.
 *
 * Anything mapped to `null` is intentionally skipped (not a deployable
 * Sharko addon). Reviewers may extend this map in follow-up PRs as
 * the landscape adds new subcategories or as Sharko's category enum
 * grows. The map is intentionally conservative — better to skip
 * borderline items than to mis-categorize them.
 */
const LANDSCAPE_TO_SHARKO_CATEGORY = {
  // Provisioning
  'Automation & Configuration': 'gitops',
  'Container Registry': null,
  'Security & Compliance': 'security',
  'Key Management': 'security',
  // Runtime
  'Cloud Native Storage': 'storage',
  'Container Runtime': null,
  'Cloud Native Network': 'networking',
  // Orchestration & Management
  'Scheduling & Orchestration': null,
  'Coordination & Service Discovery': 'networking',
  'Remote Procedure Call': 'networking',
  'Service Proxy': 'networking',
  'API Gateway': 'networking',
  'Service Mesh': 'networking',
  // App Definition and Development
  'Database': 'database',
  'Streaming & Messaging': 'database',
  'Application Definition & Image Build': 'developer-tools',
  'Continuous Integration & Delivery': 'gitops',
  // Observability and Analysis
  'Monitoring': 'observability',
  'Logging': 'observability',
  'Tracing': 'observability',
  'Chaos Engineering': 'chaos',
  // Platform / Serverless / Special
  'Certified Kubernetes - Distribution': null,
  'Certified Kubernetes - Hosted': null,
  'Certified Kubernetes - Installer': null,
  'PaaS/Container Service': null,
  'Hosted Platform': null,
  'Installable Platform': null,
};

/**
 * @param {object} ctx scanner context (logger, abortSignal, http).
 * @returns {Promise<Array<object>>} normalized scanner entries.
 */
export async function fetch(ctx) {
  const url = process.env.SHARKO_CNCF_LANDSCAPE_URL ?? DEFAULT_LANDSCAPE_URL;
  ctx.logger.info('fetching landscape', { url });

  const text = await fetchAsText(url, ctx);
  const parsed = yamlParse(text);
  if (!parsed || !Array.isArray(parsed.landscape)) {
    throw new Error("landscape YAML missing top-level 'landscape:' array");
  }

  const out = [];
  let kept = 0;
  let skippedMaturity = 0;
  let skippedHelm = 0;
  let skippedCategory = 0;
  let skippedParse = 0;

  for (const cat of parsed.landscape) {
    const subs = Array.isArray(cat?.subcategories) ? cat.subcategories : [];
    for (const sub of subs) {
      const subName = typeof sub?.name === 'string' ? sub.name : '';
      const sharkoCategory = LANDSCAPE_TO_SHARKO_CATEGORY[subName];
      // null → explicit skip; undefined → unmapped subcategory (also skip).
      const items = Array.isArray(sub?.items) ? sub.items : [];
      for (const item of items) {
        try {
          const maturity = typeof item?.project === 'string' ? item.project : '';
          if (maturity !== 'graduated' && maturity !== 'incubating') {
            skippedMaturity++;
            continue;
          }
          if (sharkoCategory == null) {
            skippedCategory++;
            continue;
          }
          const helm = extractHelm(item, ctx);
          if (!helm) {
            skippedHelm++;
            continue;
          }
          const slug = slugify(item?.name);
          if (!slug) {
            skippedParse++;
            ctx.logger.warn('item missing usable name; skipping', { raw_name: item?.name });
            continue;
          }
          out.push(buildEntry({ slug, item, sharkoCategory, maturity, helm }));
          kept++;
        } catch (err) {
          skippedParse++;
          ctx.logger.warn('per-item parse error; skipping', {
            error: err?.message ?? String(err),
            raw_name: item?.name,
          });
        }
      }
    }
  }

  ctx.logger.info('landscape filter summary', {
    kept,
    skipped_maturity: skippedMaturity,
    skipped_helm: skippedHelm,
    skipped_category: skippedCategory,
    skipped_parse: skippedParse,
  });

  return out;
}

/* ------------------------------------------------------------------ */
/* Helpers                                                              */
/* ------------------------------------------------------------------ */

/**
 * Fetch via ctx.http (3-retry + UA) when the URL is http(s); fall back
 * to direct file read for `file://` URLs (Node's built-in fetch does
 * NOT support `file://` as of Node 18-22, so tests that point us at a
 * fixture would otherwise fail). The file-read path is test-only — in
 * production the URL is always https.
 */
async function fetchAsText(url, ctx) {
  if (url.startsWith('file://')) {
    const { readFile } = await import('node:fs/promises');
    const { fileURLToPath } = await import('node:url');
    return readFile(fileURLToPath(url), 'utf8');
  }
  const res = await ctx.http(url);
  if (!res.ok) {
    throw new Error(`landscape fetch failed: HTTP ${res.status} ${res.statusText}`);
  }
  return res.text();
}

/**
 * Try every known landscape Helm-reference field in priority order.
 * Returns `{ repo, chart, version }` or `null` if the item has no
 * deterministic Helm reference. The "deterministic" bar is deliberately
 * high — guesses based on item.name alone are NOT acceptable here
 * because the reviewer can't tell a guess apart from a verified URL.
 *
 * Field priority (broader than the brief because the live landscape
 * uses `helm_chart_url` as the dominant field today, with `chart_url`
 * and `helm_url` appearing on a handful of entries):
 *
 *   1. extra.helm_chart_url   — most common in current landscape.yml
 *   2. extra.chart_url        — alternate field on a few items
 *   3. extra.helm_url         — chart-repo URL (no chart name)
 *   4. extra.artifacthub_url  — pointer to ArtifactHub package
 */
function extractHelm(item, ctx) {
  const extra = item?.extra ?? {};
  const candidates = [
    { field: 'helm_chart_url', value: extra.helm_chart_url },
    { field: 'chart_url', value: extra.chart_url },
    { field: 'helm_url', value: extra.helm_url },
    { field: 'artifacthub_url', value: extra.artifacthub_url },
  ];
  for (const { field, value } of candidates) {
    if (typeof value !== 'string' || value.length === 0) continue;
    const parsed = parseHelmUrl(value, field);
    if (parsed) return parsed;
    ctx.logger.warn('unparseable helm reference; skipping field', {
      raw_name: item?.name,
      field,
      value,
    });
  }
  return null;
}

/**
 * Parse a Helm-reference URL into `{ repo, chart, version }`.
 *
 * Supported shapes:
 *   - https://artifacthub.io/packages/helm/<repo>/<chart>[/<version>]
 *   - https://artifacthub.io/packages/chart/<repo>/<chart>[/<version>]
 *     → repo is the chart-repo URL on artifacthub itself (the reviewer
 *       will replace with the upstream chart repo URL during PR
 *       review). We DO emit the proposal because schema accepts the
 *       https://artifacthub.io URL as a valid `repo`.
 *   - Bare chart-repo URL (e.g. https://charts.jetstack.io) → repo
 *     set verbatim, chart left undefined (no deterministic chart name)
 *     → return `null` instead so the diff helper doesn't propose an
 *     update with a missing `chart`.
 */
function parseHelmUrl(value, field) {
  let u;
  try {
    u = new URL(value);
  } catch {
    return null;
  }
  const host = u.hostname.toLowerCase();

  if (host === 'artifacthub.io') {
    // /packages/helm/<repo>/<chart>[/<version>]
    // /packages/chart/<repo>/<chart>[/<version>]
    const segs = u.pathname.split('/').filter(Boolean);
    if (segs.length >= 4 && segs[0] === 'packages') {
      const chart = segs[3];
      const version = segs[4]; // optional
      // The artifacthub URL itself is a valid `repo` per schema (https://).
      // Reviewer will substitute the upstream chart-repo URL during PR review.
      const repo = `${u.protocol}//${u.host}/packages/${segs[1]}/${segs[2]}`;
      return version
        ? { repo, chart, version }
        : { repo, chart };
    }
    return null;
  }

  // Bare chart-repo URL — no deterministic chart name. Skip.
  // (Reviewers prefer no-proposal over a name-guessing one; the
  // landscape entry's `name` field is rarely the actual Helm chart
  // name, e.g. "Cert Manager" vs chart `cert-manager`.)
  if (field === 'helm_url') return null;

  // Unrecognized shape (`chart_url` pointing somewhere non-artifacthub,
  // for example) → skip rather than fabricate.
  return null;
}

/**
 * Build the normalized scanner entry. Only emits fields the plugin can
 * reliably populate; the diff helper ignores absent fields ("scanner
 * has no opinion") so the synthetic placeholders below never churn
 * updates against curated catalog data.
 */
function buildEntry({ slug, item, sharkoCategory, maturity, helm }) {
  const entry = {
    name: slug,
    description: '<TODO: human description>',
    chart: helm.chart ?? slug,
    repo: helm.repo,
    default_namespace: namespaceFor(slug),
    category: sharkoCategory,
    curated_by: [maturity === 'graduated' ? 'cncf-graduated' : 'cncf-incubating'],
    license: 'unknown',
    maintainers: ['<TODO: derive from chart repo>'],
  };
  if (helm.version) entry.version = helm.version;
  // Pass-through metadata for reviewer convenience (not consumed by
  // diff.mjs's COMPARABLE_FIELDS, so won't churn updates).
  if (typeof item?.homepage_url === 'string') entry._landscape_homepage = item.homepage_url;
  if (typeof item?.repo_url === 'string') entry._landscape_source = item.repo_url;
  return entry;
}

/**
 * Slugify a landscape item name to a catalog-schema-compatible
 * primary key. Returns `''` if the input cannot be coerced to a
 * non-empty slug — caller skips with a warn log.
 */
export function slugify(raw) {
  if (typeof raw !== 'string') return '';
  const cleaned = raw
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, '-')
    .replace(/-+/g, '-')
    .replace(/^-+|-+$/g, '');
  if (cleaned.length === 0) return '';
  // Schema requires first + last chars be `[a-z0-9]`; the trim above
  // handles trailing `-`. Clip to 63 then re-trim trailing `-` for
  // the rare case the clip lands on a hyphen boundary.
  const clipped = cleaned.length > 63 ? cleaned.slice(0, 63).replace(/-+$/, '') : cleaned;
  if (!/^[a-z0-9]/.test(clipped)) return '';
  return clipped;
}

function namespaceFor(slug) {
  // Heuristic: `<slug>-system` is reviewer-correctable and stays
  // within the schema's 63-char limit for any reasonable slug.
  const ns = `${slug}-system`;
  if (ns.length <= 63) return ns;
  // Schema cap — drop the suffix entirely if too long; reviewer must
  // pick something else. (No realistic slug hits this branch today.)
  return slug.slice(0, 63).replace(/-+$/, '');
}

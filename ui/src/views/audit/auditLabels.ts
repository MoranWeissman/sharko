// Friendly presentation helpers for the audit log (V2-cleanup-25).
//
// The backend records ~60 distinct snake_case event codes (see the `Event:`
// literals across internal/api/*.go) plus a handful of generated fallback
// codes from deriveEventName (e.g. "<resource>_created"). The map below turns
// the known codes into plain-English phrases. ANY code that is not in the map
// is de-snake-cased so the Action column is NEVER blank.

/**
 * EVENT_LABELS maps a snake_case audit event code to a human phrase.
 * Phrases read as the verb portion of a sentence ("enabled cert-manager on a
 * cluster"), so they compose nicely after a subject ("Alice").
 *
 * Keep this in sync with the `Event:` literals in internal/api/*.go.
 */
export const EVENT_LABELS: Record<string, string> = {
  // --- Clusters ---
  cluster_registered: 'registered a cluster',
  cluster_deregistered: 'removed a cluster',
  cluster_updated: 'updated a cluster',
  cluster_adopted: 'adopted a cluster',
  cluster_unadopted: 'released a cluster from management',
  cluster_tested: 'tested cluster connectivity',
  cluster_diagnosed: 'ran cluster diagnostics',
  cluster_discovery_run: 'discovered clusters from the provider',
  cluster_credentials_refreshed: 'refreshed cluster credentials',
  cluster_orphan_deleted: 'deleted an orphaned cluster secret',
  cluster_orphan_delete_rejected: 'blocked an orphaned-cluster deletion',
  cluster_secret_sync: 'synced a cluster secret',
  cluster_secret_synced: 'synced a cluster secret',
  cluster_addon_values_edited: 'edited per-cluster addon values',

  // --- Addons ---
  addon_added: 'added an addon to the catalog',
  addon_removed: 'removed an addon from the catalog',
  addon_configured: 'changed addon configuration',
  addon_upgraded: 'upgraded an addon',
  addon_enabled_on_cluster: 'enabled an addon on a cluster',
  addon_disabled_on_cluster: 'disabled an addon on a cluster',
  addon_secret_set: 'set an addon secret',
  addon_secret_deleted: 'deleted an addon secret',

  // --- Catalog ---
  catalog_reprobe: 'rechecked a catalog source',
  catalog_sources_refreshed: 'refreshed catalog sources',
  values_globals_unwrapped: 'unwrapped global values',

  // --- Connections ---
  connection_created: 'created a connection',
  connection_updated: 'updated a connection',
  connection_deleted: 'deleted a connection',
  connection_tested: 'tested a connection',
  active_connection_changed: 'switched the active connection',
  provider_tested: 'tested a provider',

  // --- Init / lifecycle ---
  init: 'initialized the repository',
  init_run: 'initialized the repository',
  reconcile_triggered: 'triggered a reconcile',
  operation_cancelled: 'cancelled an operation',

  // --- Pull requests / Git ---
  pr_deleted: 'deleted a pull request',
  pr_refreshed: 'refreshed a pull request',
  push: 'pushed a commit',

  // --- Auth & users ---
  login: 'signed in',
  login_failed: 'failed to sign in',
  logout: 'signed out',
  password_changed: 'changed a password',
  password_reset: 'reset a password',
  token_created: 'created an API token',
  token_revoked: 'revoked an API token',
  user_created: 'created a user',
  user_updated: 'updated a user',
  user_deleted: 'deleted a user',
  user_github_token_set: 'set their Git token',
  user_github_token_cleared: 'cleared their Git token',
  user_github_token_tested: 'tested their Git token',

  // --- AI ---
  ai_config_updated: 'updated the AI configuration',
  ai_tested: 'tested the AI provider',
  ai_chat_reset: 'reset an AI chat',
  ai_annotate_run: 'ran an AI annotation',
  ai_annotate_blocked: 'was blocked from an AI annotation',
  ai_opt_out_toggled: 'changed their AI opt-out setting',

  // --- Misc / security ---
  upgrade_analyzed: 'analyzed an upgrade',
  dashboards_saved: 'saved dashboards',
  secret_leak_blocked: 'was blocked for a possible secret leak',
};

/** The number of explicitly-mapped event codes (handy for tests/docs). */
export const MAPPED_EVENT_COUNT = Object.keys(EVENT_LABELS).length;

/**
 * deSnakeCase turns a snake_case (or dotted, or hyphenated) code into a
 * capitalized human phrase. The MANDATORY fallback — nothing renders blank.
 *   "addon_enabled_on_cluster" -> "Addon enabled on cluster"
 *   "cluster.test"             -> "Cluster test"
 */
export function deSnakeCase(code: string): string {
  const cleaned = code.replace(/[._-]+/g, ' ').trim().replace(/\s+/g, ' ');
  if (!cleaned) return '';
  return cleaned.charAt(0).toUpperCase() + cleaned.slice(1);
}

/**
 * eventPhrase returns the friendly verb-phrase for an event code, falling back
 * to the de-snake-cased form for any unmapped code. Never returns blank for a
 * non-empty code.
 */
export function eventPhrase(code: string | undefined | null): string {
  if (!code) return 'Performed an action';
  return EVENT_LABELS[code] ?? deSnakeCase(code);
}

/**
 * parseResource extracts a readable target from the audit `resource` field.
 * The backend records resources like "cluster:prod-eu addon:cert-manager",
 * "addon:cert-manager", "clusters:3", or a bare name. Returns the most
 * human-friendly target string it can, or '' when there's nothing useful.
 */
export function parseResource(resource: string | undefined | null): string {
  if (!resource) return '';
  const r = resource.trim();
  if (!r) return '';

  // Pull out "key:value" pairs, preferring addon + cluster when both present.
  const pairs: Record<string, string> = {};
  const re = /([a-zA-Z_]+):([^\s]+)/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(r)) !== null) {
    pairs[m[1].toLowerCase()] = m[2];
  }

  const addon = pairs['addon'];
  const cluster = pairs['cluster'];
  if (addon && cluster) return `${addon} on ${cluster}`;
  if (addon) return addon;
  if (cluster) return cluster;

  // "clusters:3" / "addons:2" style counts.
  if (pairs['clusters']) return `${pairs['clusters']} clusters`;
  if (pairs['addons']) return `${pairs['addons']} addons`;

  // Any other single key:value — show the value.
  const values = Object.values(pairs);
  if (values.length === 1) return values[0];
  if (values.length > 1) return values.join(', ');

  // Bare resource string with no key:value structure.
  return r;
}

/**
 * actionSentence composes the human-readable Action sentence for a row:
 *   "<Who> <event phrase>[ — <target>]"
 * e.g. "alice enabled an addon on a cluster — cert-manager on prod-eu".
 * The target (parsed resource) is appended when it adds information.
 */
export function actionSentence(opts: {
  user?: string | null;
  event?: string | null;
  resource?: string | null;
}): string {
  const who = opts.user && opts.user !== 'anonymous' ? opts.user : 'Someone';
  const phrase = eventPhrase(opts.event);
  const target = parseResource(opts.resource);
  const base = `${who} ${phrase}`;
  return target ? `${base} — ${target}` : base;
}

export type AuditResult = 'success' | 'partial' | 'rejected' | 'failure';

/**
 * Plain-English words for each result value the backend records
 * (internal/api/audit_middleware.go's resultFromStatus, V2-cleanup-85.2):
 *   - 2xx           -> "success"  -> "Succeeded"
 *   - 207 (partial) -> "partial"  -> "Partly done"
 *   - 4xx           -> "rejected" -> "Rejected" (the caller's request was refused)
 *   - 5xx / other   -> "failure"  -> "Failed"
 * "error" is kept as a synonym for "failure" — a legacy value that may still
 * sit in an already-buffered entry.
 */
const RESULT_LABELS: Record<string, string> = {
  success: 'Succeeded',
  partial: 'Partly done',
  rejected: 'Rejected',
  failure: 'Failed',
  error: 'Failed',
};

/** The REAL result values the backend records (resultFromStatus). */
export const RESULT_OPTIONS: { value: string; label: string }[] = [
  { value: '', label: 'All results' },
  { value: 'success', label: 'Succeeded' },
  { value: 'partial', label: 'Partly done' },
  { value: 'rejected', label: 'Rejected' },
  { value: 'failure', label: 'Failed' },
];

/** Tailwind classes for the result badge, keyed by result value. */
export function resultBadgeClass(result: string | undefined): string {
  switch (result) {
    case 'success':
      return 'bg-green-50 text-green-700 dark:bg-green-900/30 dark:text-green-400';
    case 'partial':
      return 'bg-amber-50 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400';
    case 'rejected':
      return 'bg-orange-50 text-orange-700 dark:bg-orange-900/30 dark:text-orange-400';
    case 'failure':
    case 'error': // legacy value, just in case an old entry is still buffered
      return 'bg-red-50 text-red-700 dark:bg-red-900/30 dark:text-red-400';
    default:
      return 'bg-[#d6eeff] text-[#1a4a6a] dark:bg-gray-700 dark:text-gray-300';
  }
}

/** Human label for a result value — one of the plain words in RESULT_LABELS, never blank. */
export function resultLabel(result: string | undefined): string {
  if (!result) return '—';
  return RESULT_LABELS[result] ?? result.charAt(0).toUpperCase() + result.slice(1);
}

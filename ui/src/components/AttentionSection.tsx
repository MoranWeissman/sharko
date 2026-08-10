import { Link } from 'react-router-dom';
import type { AddonState } from '@/hooks/useAddonStates';
import { isSettling, deepLinkToAddonOnCluster } from '@/hooks/useAddonStates';
import { getClusterConnectionState } from '@/lib/clusterStatus';

// dashboard UX review 2026-08-01: this list used to be the ONLY place on
// the Dashboard allowed to show red or amber. WQ-3 (attention-move-badges)
// relocates the detailed rows to Observability — the Dashboard keeps only
// a thin one-line count that links here. This module is the single place
// the row model, the row-building logic, and the settling-window honesty
// live, so both pages (and the nav badges) read the exact same numbers.
//
// Every problem — a disconnected cluster, a confirmed-degraded app, an app
// still settling, an app ArgoCD isn't reporting on, an addon with version
// drift — appears here exactly once, with a name, a plain reason, and a
// link straight to where you'd go to look at it.
//
// Maintainer's walk-day findings on the Observability "Health" section
// (formerly "Fleet Health") retired the chip-click-to-reveal panel this
// module used to export as OpenIssuesBlock, and retired the double-listing
// where a problem addon showed up both as a flat row here AND as its own
// addon-group card below. The per-addon health groups in Observability.tsx
// are now the ONLY place addon problems render; this module still owns the
// row model (AttentionRow, AttentionRowView) for the two problem kinds that
// have no addon group to live in — cluster connection problems and version
// drift — plus the classification helpers Observability uses to compose a
// problem addon group's inline reason line.

export interface AttentionRow {
  key: string;
  severity: 'problem' | 'attention';
  title: string;
  reason: string;
  link: string;
  badge?: string;
  /** Extra explanation for a hover title, when the visible `reason` was
   * trimmed down and shouldn't carry the full explanation inline (e.g. the
   * grace-period rows — see GRACE_PERIOD_TOOLTIP). Falls back to `reason`
   * itself so a long reason still shows in full on hover. */
  tooltip?: string;
}

export function AttentionRowView({ row }: { row: AttentionRow }) {
  const dot = row.severity === 'problem' ? 'bg-red-500' : 'bg-amber-500';
  const badgeClass =
    row.severity === 'problem'
      ? 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'
      : 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400';
  return (
    <div className="flex items-start gap-3 rounded-lg bg-card px-3 py-2 text-sm">
      <div className={`mt-0.5 h-2.5 w-2.5 shrink-0 rounded-full ${dot}`} />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2 flex-wrap">
          <Link
            to={row.link}
            className="font-medium text-card-foreground hover:text-teal-600 hover:underline dark:hover:text-teal-400"
          >
            {row.title}
          </Link>
          {row.badge && (
            <span className={`rounded px-1.5 py-0.5 text-xs font-medium ${badgeClass}`}>{row.badge}</span>
          )}
        </div>
        <p className="mt-1 text-muted-foreground" title={row.tooltip ?? row.reason}>
          {row.reason.length > 140 ? row.reason.slice(0, 140) + '...' : row.reason}
        </p>
      </div>
    </div>
  );
}

export function minutesAgo(ms: number): number {
  return Math.max(0, Math.floor((Date.now() - ms) / 60_000));
}

function addonLink(item: { addonName: string; cluster: string }): string {
  return item.cluster
    ? deepLinkToAddonOnCluster(item.addonName, item.cluster)
    : `/addons/${encodeURIComponent(item.addonName)}`;
}

// Plain-words vocabulary (maintainer's walk-day lock): a freshly-bad addon
// isn't "settling" and it isn't "needs attention" — it's new, and Sharko is
// still confirming it's a real problem. Maintainer's follow-up finding
// (live-bug fix, 2026-08-02): the visible row/inline text doesn't need to
// teach the grace-period concept — the amber color already says "not
// confirmed yet". The explanation now lives ONLY in a hover tooltip, shared
// here so the row builder below and describeAddonProblem (used for the
// addon-group header line) attach the exact same tooltip text.
export const GRACE_PERIOD_TOOLTIP =
  'Started less than 10 minutes ago. Sharko waits a few minutes before counting a new problem as a confirmed issue.';

// --- Splitting the addon-state map into severity buckets --------------

export interface AddonAttentionBuckets {
  confirmed: AddonState[];
  settling: AddonState[];
  unknown: AddonState[];
  progressing: AddonState[];
}

/**
 * splitAddonStates — the ONE place that turns the raw useAddonStates() map
 * into the severity buckets every consumer (Dashboard's thin line, the nav
 * badges, Observability's detail rows) reads from. Keeping this here means
 * the settling-window rule (isSettling) is applied exactly once.
 */
export function splitAddonStates(byApp: Map<string, AddonState>): AddonAttentionBuckets {
  const all = Array.from(byApp.values());
  const bad = all.filter((s) => s.displayState === 'degraded' || s.displayState === 'missing');
  return {
    confirmed: bad.filter((s) => !isSettling(s)),
    settling: bad.filter((s) => isSettling(s)),
    unknown: all.filter((s) => s.displayState === 'unknown'),
    progressing: all.filter((s) => s.displayState === 'progressing-advisory'),
  };
}

/**
 * getConfirmedAddonProblemCount — confirmed (past the settling window)
 * addon problems only. Does NOT include cluster connectivity problems —
 * add those on top via getConfirmedProblemCount below.
 */
export function getConfirmedAddonProblemCount(byApp: Map<string, AddonState>): number {
  return splitAddonStates(byApp).confirmed.length;
}

/**
 * getConfirmedProblemCount — the ONE number shown on the Dashboard's thin
 * attention line and mirrored verbatim on the Observability nav badge
 * (one truth, two mirrors). Confirmed addon problems plus failed/missing
 * cluster connections — the same settling-window count the old Dashboard
 * banner used.
 */
export function getConfirmedProblemCount(byApp: Map<string, AddonState>, clusterProblemCount: number): number {
  return getConfirmedAddonProblemCount(byApp) + clusterProblemCount;
}

// --- Row builders -------------------------------------------------------

export function buildConfirmedAddonRows(items: AddonState[]): AttentionRow[] {
  return items.map((item) => ({
    key: `addon-${item.addonName}@${item.cluster}`,
    severity: 'problem',
    title: item.appName || item.addonName,
    reason: item.advisoryMessage
      ? `${item.errorType ? item.errorType + ': ' : ''}${item.advisoryMessage}`
      : `Health: ${item.healthStatus}${item.cluster ? ` on ${item.cluster}` : ''}`,
    link: addonLink(item),
    badge: item.healthStatus,
  }));
}

export function buildSettlingAddonRows(items: AddonState[]): AttentionRow[] {
  return items.map((item) => {
    const state = item.healthStatus || 'Unknown';
    const capitalizedState = state.charAt(0).toUpperCase() + state.slice(1);
    return {
      key: `settling-${item.addonName}@${item.cluster}`,
      severity: 'attention',
      title: item.appName || item.addonName,
      // Maintainer's finding: the row doesn't need to teach the grace-period
      // concept — the amber color already signals "not confirmed yet". Just
      // state + where + when it started (e.g. "Degraded on spoke-eu —
      // started 1m ago"); the full explanation lives in the tooltip only.
      reason: `${capitalizedState}${item.cluster ? ` on ${item.cluster}` : ''} — started ${minutesAgo(item.badSince ?? Date.now())}m ago`,
      tooltip: GRACE_PERIOD_TOOLTIP,
      link: addonLink(item),
      badge: item.healthStatus,
    };
  });
}

export function buildUnknownAddonRows(items: AddonState[]): AttentionRow[] {
  return items.map((item) => ({
    key: `unknown-${item.addonName}@${item.cluster}`,
    severity: 'attention',
    title: item.appName || item.addonName,
    reason: `No status reported${item.cluster ? ` on ${item.cluster}` : ''}. ArgoCD isn't reporting a health status for this app.`,
    link: addonLink(item),
    badge: item.healthStatus,
  }));
}

// Cluster problem rows — failed/missing only, reason text straight from
// clusterStatus.ts (the single source of truth for this vocabulary).
export function buildClusterAttentionRows(
  clusters: { name: string; connectionStatus: string }[],
): AttentionRow[] {
  return clusters.map((c) => ({
    key: `cluster-${c.name}`,
    severity: 'problem',
    title: c.name,
    reason: getClusterConnectionState(c.connectionStatus).meaning,
    link: `/clusters/${encodeURIComponent(c.name)}`,
  }));
}

export function buildDriftRows(versionDrifts: { addon: string; count: number }[]): AttentionRow[] {
  return versionDrifts.map((d) => ({
    key: `drift-${d.addon}`,
    severity: 'attention',
    title: d.addon,
    reason: `Different versions deployed across ${d.count} cluster${d.count !== 1 ? 's' : ''}.`,
    link: `/addons/${encodeURIComponent(d.addon)}`,
  }));
}

// --- Whether there's anything open at all --------------------------------

export interface OpenIssuesCounts {
  clusterAttentionRows: AttentionRow[];
  confirmedAddonRows: AttentionRow[];
  settlingAddonRows: AttentionRow[];
  unknownAddonRows: AttentionRow[];
  driftRows: AttentionRow[];
  /** stats.clusters.failed + stats.clusters.missing — the server's single
   * classification, not a re-derivation from the named rows above. */
  clusterProblemCount: number;
}

/**
 * hasOpenIssues — same OR-of-lengths every caller needs before deciding
 * whether to render the "Open issues (N)" line or the "No open issues."
 * zero-state. Exported so Observability doesn't re-derive it a second way.
 */
export function hasOpenIssues(props: OpenIssuesCounts): boolean {
  return (
    props.clusterProblemCount > 0 ||
    props.confirmedAddonRows.length > 0 ||
    props.settlingAddonRows.length > 0 ||
    props.unknownAddonRows.length > 0 ||
    props.driftRows.length > 0
  );
}

/**
 * countOpenIssues — the total N behind the "Open issues (N)" line at the
 * top of Observability's Health section. Sums every problem kind the
 * section can show: cluster connection problems (server-classified count,
 * not a re-derivation of the named rows), confirmed/new/no-status addon
 * problems, and version drift. This is a display total for that one line —
 * it is NOT the same number as getConfirmedProblemCount (which the
 * Dashboard's thin line and the nav badges read, and which deliberately
 * excludes new/no-status/drift). Keeping the two separate is intentional:
 * this file still owns both, so neither can drift out of sync by accident.
 */
export function countOpenIssues(props: OpenIssuesCounts): number {
  return (
    props.clusterProblemCount +
    props.confirmedAddonRows.length +
    props.settlingAddonRows.length +
    props.unknownAddonRows.length +
    props.driftRows.length
  );
}

// --- Composing a problem addon group's inline reason line ---------------
//
// Walk-day lock: the per-addon health groups in Observability.tsx are the
// ONLY place addon problems render (no more flat row + full-width group
// duplicating the same information). These two helpers turn a group's
// child apps plus the live AddonState map into "which tier is this group
// in" and "what does the header line say", built from the same fields
// (healthStatus, advisoryMessage, errorType, badSince) the row builders
// above use — just composed into one inline sentence instead of a row.

export type AddonGroupProblemTier = 'confirmed' | 'grace' | 'unknown' | 'none';

export interface AddonGroupProblem {
  tier: AddonGroupProblemTier;
  /** The worst child app driving this group's tier, or null when tier is 'none'. */
  state: AddonState | null;
  /** Other child apps (any cluster, any problem tier) beyond the one named in `state`. */
  extraCount: number;
}

const TIER_RANK: Record<Exclude<AddonGroupProblemTier, 'none'>, number> = {
  confirmed: 3,
  grace: 2,
  unknown: 1,
};

/**
 * classifyAddonGroupProblem — walks one addon group's child apps, looks
 * each one up in the live AddonState map by `<addon>@<cluster>`, and picks
 * the worst tier present. Only apps the map actually knows about (i.e.
 * ones the /dashboard/attention feed currently lists as degraded/missing/
 * unknown) count — healthy and progressing-advisory apps never do.
 *
 * `byAppName` is an optional second index (feed items keyed by their real
 * ArgoCD app name) used as a fallback lookup when the primary
 * `<addon>@<cluster>` key doesn't line up. Maintainer's live-bug finding:
 * /dashboard/attention currently ships the full ArgoCD app name in its
 * addon_name field instead of the catalog addon name (internal/api/
 * dashboard.go — tracked as a separate Go-side fix), so the primary key
 * can miss even though the app name itself matches this child exactly. See
 * `isOrphan` in Observability.tsx for the identical divergence.
 */
export function classifyAddonGroupProblem(
  group: { addon_name: string; child_apps: { cluster_name: string; app_name?: string }[] },
  byApp: Map<string, AddonState>,
  byAppName?: Map<string, AddonState>,
): AddonGroupProblem {
  let best: { tier: Exclude<AddonGroupProblemTier, 'none'>; state: AddonState } | null = null;
  let count = 0;

  for (const child of group.child_apps ?? []) {
    const state =
      byApp.get(`${group.addon_name}@${child.cluster_name}`) ??
      (child.app_name ? byAppName?.get(child.app_name) : undefined);
    if (!state) continue;

    let tier: Exclude<AddonGroupProblemTier, 'none'> | null = null;
    if (state.displayState === 'degraded' || state.displayState === 'missing') {
      tier = isSettling(state) ? 'grace' : 'confirmed';
    } else if (state.displayState === 'unknown') {
      tier = 'unknown';
    }
    if (!tier) continue;

    count++;
    if (!best || TIER_RANK[tier] > TIER_RANK[best.tier]) {
      best = { tier, state };
    }
  }

  if (!best) return { tier: 'none', state: null, extraCount: 0 };
  return { tier: best.tier, state: best.state, extraCount: Math.max(0, count - 1) };
}

export interface AddonProblemDescription {
  /** Plain-words inline sentence, e.g. "degraded on spoke-eu — pod not ready · seen 4m ago". */
  text: string;
  /** Present only for the grace tier — the full grace-period explanation, for a title/tooltip. */
  tooltip?: string;
}

/**
 * describeAddonProblem — turns one AddonState + its tier into the plain
 * sentence a problem addon group's header shows. Vocabulary lock: the
 * unknown tier says "no status reported" (never "not reporting" jargon in
 * the headline). Maintainer's finding: the grace tier's amber color already
 * signals "not confirmed yet" on its own, so the visible text is just
 * state + where + detail + when — the "new — confirming" phrase and the
 * grace-period explanation only ever show up in the tooltip (see
 * GRACE_PERIOD_TOOLTIP). Neither "settling" nor "needs attention" appears
 * anywhere in this output.
 */
export function describeAddonProblem(
  state: AddonState,
  tier: Exclude<AddonGroupProblemTier, 'none'>,
): AddonProblemDescription {
  const clusterPart = state.cluster ? ` on ${state.cluster}` : '';

  if (tier === 'unknown') {
    return { text: `no status reported${clusterPart}` };
  }

  const badge = (state.healthStatus || 'unknown').toLowerCase();
  const detail = state.advisoryMessage
    ? `${state.errorType ? state.errorType + ': ' : ''}${state.advisoryMessage}`
    : null;
  const mins = state.badSince !== undefined ? minutesAgo(state.badSince) : null;
  const seenPart = mins !== null ? ` · seen ${mins}m ago` : '';
  const base = `${badge}${clusterPart}${detail ? ` — ${detail}` : ''}${seenPart}`;

  if (tier === 'grace') {
    return { text: base, tooltip: GRACE_PERIOD_TOOLTIP };
  }
  return { text: base };
}

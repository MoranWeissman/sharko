import { useState } from 'react';
import { Link } from 'react-router-dom';
import { AlertTriangle, ChevronRight } from 'lucide-react';
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

export interface AttentionRow {
  key: string;
  severity: 'problem' | 'attention';
  title: string;
  reason: string;
  link: string;
  badge?: string;
}

export function AttentionRowView({ row }: { row: AttentionRow }) {
  const dot = row.severity === 'problem' ? 'bg-red-500' : 'bg-amber-500';
  const badgeClass =
    row.severity === 'problem'
      ? 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'
      : 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400';
  return (
    <div className="flex items-start gap-3 rounded-lg bg-card px-3 py-2 text-xs">
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
        <p className="mt-1 text-muted-foreground" title={row.reason}>
          {row.reason.length > 140 ? row.reason.slice(0, 140) + '...' : row.reason}
        </p>
      </div>
    </div>
  );
}

function minutesAgo(ms: number): number {
  return Math.max(0, Math.floor((Date.now() - ms) / 60_000));
}

function addonLink(item: { addonName: string; cluster: string }): string {
  return item.cluster
    ? deepLinkToAddonOnCluster(item.addonName, item.cluster)
    : `/addons/${encodeURIComponent(item.addonName)}`;
}

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
  return items.map((item) => ({
    key: `settling-${item.addonName}@${item.cluster}`,
    severity: 'attention',
    title: item.appName || item.addonName,
    reason: `Settling — degraded for ${minutesAgo(item.badSince ?? Date.now())}m. Given a grace window before counting as a confirmed problem.`,
    link: addonLink(item),
    badge: item.healthStatus,
  }));
}

export function buildUnknownAddonRows(items: AddonState[]): AttentionRow[] {
  return items.map((item) => ({
    key: `unknown-${item.addonName}@${item.cluster}`,
    severity: 'attention',
    title: item.appName || item.addonName,
    reason: `ArgoCD is not reporting a health status for this app${item.cluster ? ` on ${item.cluster}` : ''}.`,
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

// --- The composite detail block (moved verbatim from Dashboard, WQ-3) ---

export interface AttentionDetailProps {
  onNavigate: (path: string) => void;
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
 * AttentionDetail — the full "Needs Attention" surface, unchanged from its
 * original Dashboard incarnation, now hosted on Observability. Same
 * severity honesty, same settling window, same per-row deep links.
 */
export function AttentionDetail({
  onNavigate,
  clusterAttentionRows,
  confirmedAddonRows,
  settlingAddonRows,
  unknownAddonRows,
  driftRows,
  clusterProblemCount,
}: AttentionDetailProps) {
  const [showAttention, setShowAttention] = useState(false);

  const redAppCount = confirmedAddonRows.length;
  const settlingCount = settlingAddonRows.length;
  const unknownCount = unknownAddonRows.length;
  const driftCount = driftRows.length;
  const hasIssues =
    clusterProblemCount > 0 || redAppCount > 0 || settlingCount > 0 || unknownCount > 0 || driftCount > 0;

  if (!hasIssues) return null;

  return (
    <div className="rounded-xl border-2 border-amber-300 bg-amber-50 dark:border-amber-700 dark:bg-amber-900/20">
      <div className="flex items-center justify-between p-5 pb-3">
        <div className="flex items-center gap-2 text-amber-700 dark:text-amber-400">
          <AlertTriangle className="h-5 w-5" />
          <h3 className="text-sm font-semibold">Needs Attention</h3>
        </div>
        <div className="flex flex-wrap gap-2">
          {redAppCount > 0 && (
            <button
              onClick={() => setShowAttention(!showAttention)}
              aria-expanded={showAttention}
              className="flex items-center gap-2 rounded-lg border border-red-200 bg-[#f0f7ff] px-3 py-1.5 text-xs text-red-700 hover:bg-red-50 dark:border-red-800 dark:bg-gray-800 dark:text-red-400"
            >
              <div className="h-2 w-2 rounded-full bg-red-500" />
              {redAppCount} app{redAppCount !== 1 ? 's' : ''} with issues
              <ChevronRight className={`h-3 w-3 transition-transform ${showAttention ? 'rotate-90' : ''}`} />
            </button>
          )}
          {clusterProblemCount > 0 && (
            <button
              onClick={() => setShowAttention(!showAttention)}
              aria-expanded={showAttention}
              className="flex items-center gap-2 rounded-lg border border-red-200 bg-[#f0f7ff] px-3 py-1.5 text-xs text-red-700 hover:bg-red-50 dark:border-red-800 dark:bg-gray-800 dark:text-red-400"
            >
              <div className="h-2 w-2 rounded-full bg-red-500" />
              {clusterProblemCount} disconnected cluster{clusterProblemCount !== 1 ? 's' : ''}
              <ChevronRight className={`h-3 w-3 transition-transform ${showAttention ? 'rotate-90' : ''}`} />
            </button>
          )}
          {settlingCount > 0 && (
            <button
              onClick={() => setShowAttention(!showAttention)}
              aria-expanded={showAttention}
              className="flex items-center gap-2 rounded-lg border border-amber-200 bg-[#f0f7ff] px-3 py-1.5 text-xs text-amber-700 hover:bg-amber-50 dark:border-amber-800 dark:bg-gray-800 dark:text-amber-400"
            >
              <div className="h-2 w-2 rounded-full bg-amber-500" />
              {settlingCount} settling
            </button>
          )}
          {unknownCount > 0 && (
            <button
              onClick={() => setShowAttention(!showAttention)}
              aria-expanded={showAttention}
              className="flex items-center gap-2 rounded-lg border border-amber-200 bg-[#f0f7ff] px-3 py-1.5 text-xs text-amber-700 hover:bg-amber-50 dark:border-amber-800 dark:bg-gray-800 dark:text-amber-400"
            >
              <div className="h-2 w-2 rounded-full bg-amber-500" />
              {unknownCount} app{unknownCount !== 1 ? 's' : ''} not reporting
            </button>
          )}
          {driftCount > 0 && (
            <button
              onClick={() => setShowAttention(!showAttention)}
              aria-expanded={showAttention}
              className="flex items-center gap-2 rounded-lg border border-amber-200 bg-[#f0f7ff] px-3 py-1.5 text-sm text-amber-700 hover:bg-amber-50 dark:border-amber-800 dark:bg-gray-800 dark:text-amber-400"
            >
              <div className="h-2 w-2 rounded-full bg-amber-500" />
              {driftCount} addon{driftCount !== 1 ? 's' : ''} with drift
            </button>
          )}
        </div>
      </div>
      {/* Expanded detail — cluster/addon names route to their own detail
          pages for quick debug + AI-assisted investigation. */}
      {showAttention && (
        <div className="border-t border-amber-200 p-4 dark:border-amber-700 space-y-4">
          {clusterAttentionRows.length > 0 && (
            <div>
              <div className="mb-1.5 flex items-center justify-between">
                <h4 className="text-xs font-semibold uppercase tracking-wide text-[#3a6a8a] dark:text-gray-400">Clusters</h4>
                <button
                  onClick={() => onNavigate('/clusters?status=disconnected')}
                  className="text-xs text-teal-600 hover:text-teal-700 dark:text-teal-400"
                >
                  View in Clusters
                </button>
              </div>
              <div className="space-y-1.5">
                {clusterAttentionRows.map((row) => (
                  <AttentionRowView key={row.key} row={row} />
                ))}
              </div>
            </div>
          )}
          {(confirmedAddonRows.length > 0 || settlingAddonRows.length > 0 || unknownAddonRows.length > 0) && (
            <div>
              <h4 className="mb-1.5 text-xs font-semibold uppercase tracking-wide text-[#3a6a8a] dark:text-gray-400">Apps</h4>
              <div className="max-h-64 overflow-y-auto space-y-1.5">
                {[...confirmedAddonRows, ...settlingAddonRows, ...unknownAddonRows].map((row) => (
                  <AttentionRowView key={row.key} row={row} />
                ))}
              </div>
            </div>
          )}
          {driftRows.length > 0 && (
            <div>
              <div className="mb-1.5 flex items-center justify-between">
                <h4 className="text-xs font-semibold uppercase tracking-wide text-[#3a6a8a] dark:text-gray-400">Version drift</h4>
                <button
                  onClick={() => onNavigate('/version-matrix?drift=true')}
                  className="text-xs text-teal-600 hover:text-teal-700 dark:text-teal-400"
                >
                  View matrix
                </button>
              </div>
              <div className="space-y-1.5">
                {driftRows.map((row) => (
                  <AttentionRowView key={row.key} row={row} />
                ))}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

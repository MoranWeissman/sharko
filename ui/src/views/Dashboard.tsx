import { useState, useEffect, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Server, AlertTriangle,
  Clock, ChevronRight, ShieldAlert, RefreshCw,
  Store, PlusCircle, Loader2
} from 'lucide-react';
import { api } from '@/services/api';
import type { DashboardStats, SyncActivityEntry, ClustersResponse } from '@/services/models';
import { getCached, setCached } from '@/lib/viewCache';
import { WaveDecoration } from '@/components/WaveDecoration';
import { LoadingState } from '@/components/LoadingState';
import { ErrorState } from '@/components/ErrorState';
import { ArgoCDStatusBanner } from '@/components/ArgoCDStatusBanner';
import { MigrationBanner } from '@/components/MigrationBanner';
import { PullRequestsPanel } from '@/components/PullRequestsPanel';
import { DriftAlertsPanel } from '@/components/DriftAlertsPanel';
import { EmptyState } from '@/components/EmptyState';
import { showToast } from '@/components/ToastNotification';
import {
  FleetStatusStrip,
  summarizeBehindCatalog,
  type BehindCatalogSummary,
} from '@/components/FleetStatusStrip';
import { splitAddonStates } from '@/components/AttentionSection';
import { prettyOperation } from '@/lib/utils';
import type { TrackedPR } from '@/services/models';
import { useAddonStates } from '@/hooks/useAddonStates';

// --- Time ago helpers ---

function timeAgo(timestamp: string): string {
  const secs = Math.floor((Date.now() - new Date(timestamp).getTime()) / 1000);
  if (secs < 60) return 'just now';
  if (secs < 3600) return `${Math.floor(secs / 60)}m ago`;
  if (secs < 86400) return `${Math.floor(secs / 3600)}h ago`;
  return `${Math.floor(secs / 86400)}d ago`;
}

// activityVerb picks the Recent Activity row's leading word from the
// server's action field (S3, walk day 5 ride-along): "installed " for an
// app's first recorded deploy, "updated " for every later one. Falls back
// to "deployed " when action is absent — an older cached response, or a
// server build that predates this field.
function activityVerb(action?: string): string {
  if (action === 'installed') return 'installed ';
  if (action === 'updated') return 'updated ';
  return 'deployed ';
}

// --- Bootstrap Health Banner ---

// connhealth-2: the inline bootstrap banner now renders ONLY for bootstrap
// states that genuinely BLOCK addon deploys. Softer / transient states
// (Unknown, Progressing, or anything not listed here) are surfaced through
// the notification bell instead (Story 1's "ArgoCD can't sync the repo"
// alert), so they no longer get a redundant inline banner.
//   Blocking set: Error, Missing, Degraded.
//   - Error / Missing: the bootstrap Application failed or doesn't exist —
//     nothing can deploy.
//   - Degraded: the bootstrap Application is unhealthy enough to stall child
//     addon syncs.
// Anything else (Unknown = ArgoCD hasn't reported yet, Progressing =
// mid-sync, Healthy) is bell-only / no banner.
export const BOOTSTRAP_BLOCKING_HEALTH = ['Error', 'Missing', 'Degraded'] as const;

export function isBootstrapBlocking(health: string | undefined | null): boolean {
  return !!health && (BOOTSTRAP_BLOCKING_HEALTH as readonly string[]).includes(health);
}

interface BootstrapHealthBannerProps {
  health: string;
  sync: string;
}

function BootstrapHealthBanner({ health, sync }: BootstrapHealthBannerProps) {
  const [dismissed, setDismissed] = useState(false);
  if (dismissed) return null;

  // The render gate (isBootstrapBlocking) only passes Error/Missing/Degraded
  // through to this banner now, so these are always "critical" (red). The
  // amber branch is kept for defensive styling in case the banner is ever
  // shown for a softer state.
  const isCritical = health === 'Degraded' || health === 'Missing' || health === 'Error' || health === 'Unknown';
  const borderClass = isCritical
    ? 'border-red-300 dark:border-red-700'
    : 'border-amber-300 dark:border-amber-700';
  const bgClass = isCritical
    ? 'bg-red-50 dark:bg-red-900/20'
    : 'bg-amber-50 dark:bg-amber-900/20';
  const textClass = isCritical
    ? 'text-red-800 dark:text-red-300'
    : 'text-amber-800 dark:text-amber-300';
  const iconClass = isCritical
    ? 'text-red-500 dark:text-red-400'
    : 'text-amber-500 dark:text-amber-400';
  const badgeBg = isCritical
    ? 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300'
    : 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300';
  const dismissClass = isCritical
    ? 'text-red-600 hover:bg-red-100 dark:text-red-400 dark:hover:bg-red-800'
    : 'text-amber-600 hover:bg-amber-100 dark:text-amber-400 dark:hover:bg-amber-800';

  return (
    <div className={`flex items-start justify-between rounded-lg border-2 ${borderClass} ${bgClass} px-4 py-3`}>
      <div className={`flex items-start gap-3 text-sm ${textClass}`}>
        <ShieldAlert className={`h-5 w-5 shrink-0 mt-0.5 ${iconClass}`} />
        <div>
          <p className="font-semibold">Sharko Engine Issue</p>
          <p className="mt-0.5 text-xs opacity-90">
            The <code className="rounded bg-black/10 px-1 dark:bg-white/10">sharko-engine</code> application
            is the foundation of all addon deployments. An unhealthy engine app may prevent addons from syncing.
          </p>
          <div className="mt-2 flex items-center gap-2">
            <span className={`rounded px-2 py-0.5 text-xs font-medium ${badgeBg}`}>
              Health: {health}
            </span>
            <span className={`rounded px-2 py-0.5 text-xs font-medium ${badgeBg}`}>
              Sync: {sync}
            </span>
          </div>
        </div>
      </div>
      <button
        type="button"
        onClick={() => setDismissed(true)}
        className={`ml-4 mt-0.5 rounded p-0.5 ${dismissClass}`}
        aria-label="Dismiss"
      >
        <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" />
        </svg>
      </button>
    </div>
  );
}

// --- Main Dashboard ---

// perf S2 — everything the page renders from its reads, snapshotted
// into one cache entry once a load finishes. A revisit within the same
// browser session hydrates all of this in one shot (instant paint, no
// spinner) before kicking a background refresh — see the mount effect
// below and fetchData's setCached call at the end.
interface DashboardSnapshot {
  stats: DashboardStats;
  recentSyncs: SyncActivityEntry[];
  argoCDUnreachable: boolean;
  behindCatalog: BehindCatalogSummary;
}

// Exported so tests can prime/inspect the same key the component uses.
export const DASHBOARD_CACHE_KEY = 'dashboard';

export function Dashboard() {
  const navigate = useNavigate();
  const [stats, setStats] = useState<DashboardStats | null>(null);
  const [recentSyncs, setRecentSyncs] = useState<SyncActivityEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [isRefreshing, setIsRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Addon health/sync state is sourced from the unified AddonStatesProvider
  // so the Dashboard's attention count agrees with Observability / AddonDetail
  // / ClusterDetail. The provider polls /dashboard/attention once for the
  // whole app.
  const { byApp: addonStateMap, refresh: refreshAddonStates } = useAddonStates();
  // ArgoCD-unreachable is the ONLY signal the machinery banner needs — the
  // detailed cluster attention rows live on Observability now (WQ-3).
  const [argoCDUnreachable, setArgoCDUnreachable] = useState(false);
  // Behind-catalog stat (walk day 5 finding) — derived from the version
  // matrix's per-cell drift_from_catalog flag, not the upstream
  // newest_available comparison the old "N outdated" stat used.
  const [behindCatalog, setBehindCatalog] = useState<BehindCatalogSummary>({ behindCount: 0 });

  // S3 — progressive paint: every read still fires at once (unchanged
  // parallelism), but the page no longer waits for Promise.all on all reads
  // before showing anything. /dashboard/stats alone clears the spinner and
  // paints the frame + stat cards; every other panel fills in on its own,
  // whenever its own fetch lands, using the .catch(()=>null) degrade it
  // already had. A never-resolving observability call, say, no longer
  // holds up the stat cards.
  const fetchData = useCallback(async (background = false) => {
    if (background) {
      setIsRefreshing(true);
    } else {
      setLoading(true);
    }
    setError(null);

    const obsPromise = api.getObservability().catch(() => null);
    const matrixPromise = api.getVersionMatrix().catch(() => null);
    const clustersPromise = api.getClusters().catch(() => null);

    let statsData: DashboardStats;
    try {
      statsData = await api.getDashboardStats();
    } catch (e: unknown) {
      if (!background) {
        setError(e instanceof Error ? e.message : 'Failed to load dashboard');
      }
      setLoading(false);
      setIsRefreshing(false);
      return;
    }

    setStats(statsData);
    setLoading(false);
    // Trigger an extra refresh of the unified state so Dashboard reflects
    // any attention items that arrived between the provider's polls.
    refreshAddonStates();

    // Local accumulators for the perf S2 write-through cache — filled in
    // as each read below lands, written once everything has settled so the
    // NEXT visit this session can paint the whole page instantly.
    let snapRecentSyncs: SyncActivityEntry[] = [];
    let snapArgoCDUnreachable = false;
    let snapBehindCatalog: BehindCatalogSummary = { behindCount: 0 };

    obsPromise.then((obsData) => {
      // Recent syncs (last 5)
      if (obsData?.recent_syncs) {
        snapRecentSyncs = obsData.recent_syncs.slice(0, 5);
        setRecentSyncs(snapRecentSyncs);
      }
    });

    matrixPromise.then((matrixData) => {
      snapBehindCatalog = summarizeBehindCatalog(matrixData);
      setBehindCatalog(snapBehindCatalog);
    });

    clustersPromise.then((clustersData) => {
      // ArgoCD unreachable: the ONLY honest signal is the getClusters()
      // fetch itself failing (caught to null above) — NOT a heuristic over
      // connection_status values. The old check looked for a lowercase
      // 'unknown' the server never sends, AND its logic was backwards:
      // every cluster reading Failed actually PROVES ArgoCD is reachable
      // (it answered with a real status), not the opposite.
      const typedClusters = clustersData as ClustersResponse | null;
      snapArgoCDUnreachable = typedClusters === null;
      setArgoCDUnreachable(snapArgoCDUnreachable);
      // Named cluster attention rows (with reasons + deep links) now live
      // on Observability (WQ-3) — this fetch here only needs to prove
      // ArgoCD answered at all.
    });

    await Promise.allSettled([obsPromise, matrixPromise, clustersPromise]);

    setIsRefreshing(false);

    // Write-through: next mount this session paints all of this instantly.
    setCached<DashboardSnapshot>(DASHBOARD_CACHE_KEY, {
      stats: statsData,
      recentSyncs: snapRecentSyncs,
      argoCDUnreachable: snapArgoCDUnreachable,
      behindCatalog: snapBehindCatalog,
    });
  }, [refreshAddonStates]);

  const handleRefresh = useCallback(() => {
    void fetchData(true);
  }, [fetchData]);

  const handlePRMerged = useCallback((pr: TrackedPR) => {
    showToast(`Merged PR #${pr.pr_id}: ${prettyOperation(pr.operation)}${pr.cluster ? ` on ${pr.cluster}` : ''}.`)
    // Refresh dashboard data when a PR merges
    void fetchData(true)
  }, [fetchData])

  // perf S2 — stale-while-refresh: a cache hit paints every card instantly
  // (no spinner) from this session's last successful load, then a normal
  // background fetch quietly brings it current.
  useEffect(() => {
    const cached = getCached<DashboardSnapshot>(DASHBOARD_CACHE_KEY);
    if (cached) {
      setStats(cached.data.stats);
      setRecentSyncs(cached.data.recentSyncs);
      setArgoCDUnreachable(cached.data.argoCDUnreachable);
      setBehindCatalog(cached.data.behindCatalog);
      setLoading(false);
      void fetchData(true);
    } else {
      void fetchData();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fetchData]);

  // Auto-refresh every 30s
  useEffect(() => {
    const interval = setInterval(() => {
      void fetchData(true);
    }, 30_000);
    return () => clearInterval(interval);
  }, [fetchData]);

  if (loading) return <LoadingState message="Loading dashboard..." />;
  if (error) return <ErrorState message={error} onRetry={fetchData} />;
  if (!stats) return null;

  // Top deployed addons (from stats)
  const appTotal = stats.applications.total;
  const healthyCount = stats.applications.by_health_status.healthy;

  // Confirmed (non-settling) addon problems — the same split Observability's
  // detail rows use (components/AttentionSection.tsx), so the Dashboard's
  // issues line agrees with the detail page byte-for-byte. Settling/unknown/
  // drift rows and their names+links are Observability's job now (WQ-3) —
  // Dashboard only needs the confirmed COUNT. Progressing apps are not an
  // issue (walk finding) — they no longer appear on this page at all; see
  // Observability for that state.
  const { confirmed: confirmedAddonItems } = splitAddonStates(addonStateMap);
  const redAppCount = confirmedAddonItems.length;
  // The Dashboard's own "how many clusters are actually disconnected"
  // number comes from the server's single classification (Package 1), not
  // a browser-side re-derivation — every surface on this page that states
  // the count reads stats.clusters.
  const clusterProblemCount = stats.clusters.failed + stats.clusters.missing;
  // The ONE number on the thin attention line, mirrored verbatim by the
  // Observability nav badge (components/AttentionSection.tsx's
  // getConfirmedProblemCount — same shared computation, one truth, two
  // mirrors).
  const confirmedProblemCount = redAppCount + clusterProblemCount;

  // V2-cleanup-61.3 (B1): a fresh install with nothing registered used to
  // show green "All systems operational" + "0/0 healthy" — a false-positive
  // reading of "everything's fine" when actually nothing has happened yet.
  // This is also where the first-run wizard lands (every exit path —
  // "Go to Dashboard", "Skip, go to Dashboard", and the X-button escape —
  // navigates to /dashboard), so this neutral state doubles as the
  // post-wizard next-step guidance the wizard itself doesn't provide.
  const noClustersYet = stats.clusters.total === 0;

  const heroSection = (
    <div className="relative overflow-hidden rounded-2xl bg-gradient-to-r from-teal-700 to-blue-800 px-8 py-8 text-white shadow-lg dark:from-teal-900 dark:to-blue-950">
      <div className="flex items-center gap-6">
        <img
          src="/sharko-banner.png"
          alt="Sharko"
          className="hidden h-32 w-auto sm:block"
        />
        <div className="flex-1">
          <h1 className="text-2xl font-bold tracking-tight sm:text-3xl" style={{ fontFamily: '"Quicksand", sans-serif', fontWeight: 700 }}>
            Sharko
          </h1>
          <p className="mt-1 max-w-2xl text-sm text-teal-100 sm:text-base">
            Addon management across all your Kubernetes clusters.
          </p>
        </div>
        <button
          type="button"
          onClick={handleRefresh}
          disabled={isRefreshing}
          title="Refresh"
          data-testid="dashboard-refresh"
          className="inline-flex items-center gap-1.5 rounded-lg border border-white/20 bg-white/10 px-3 py-1.5 text-xs font-medium text-white hover:bg-white/20 disabled:cursor-not-allowed disabled:opacity-60"
        >
          {isRefreshing ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
          Refresh
        </button>
      </div>
      <WaveDecoration />
    </div>
  );

  if (noClustersYet) {
    return (
      <div className="mx-auto max-w-screen-xl space-y-6">
        {heroSection}
        <div className="flex flex-col items-center gap-4 rounded-2xl border border-border bg-card px-6 py-14 text-center shadow-sm">
          <Server className="h-10 w-10 text-muted-foreground" />
          <div>
            <h2 className="text-lg font-semibold text-card-foreground">
              Nothing connected yet
            </h2>
            <p className="mx-auto mt-1 max-w-md text-sm text-muted-foreground">
              Register a cluster to start deploying addons, or browse the Marketplace to see
              what&rsquo;s available first.
            </p>
          </div>
          <div className="flex flex-wrap items-center justify-center gap-3">
            <button
              onClick={() => navigate('/clusters')}
              className="inline-flex items-center gap-2 rounded-lg bg-[#0a2a4a] px-5 py-2.5 text-sm font-semibold text-white shadow-sm hover:bg-[#0d3558] dark:bg-blue-700 dark:hover:bg-blue-600"
            >
              <PlusCircle className="h-4 w-4" />
              Register a Cluster
            </button>
            <button
              onClick={() => navigate('/addons?tab=marketplace')}
              className="inline-flex items-center gap-2 rounded-lg border border-border bg-muted px-5 py-2.5 text-sm font-medium text-card-foreground hover:bg-muted/70"
            >
              <Store className="h-4 w-4" />
              Browse the Marketplace
            </button>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-screen-xl space-y-6">
      {/* Hero Section */}
      {heroSection}

      {/* ArgoCD Status Banner */}
      <ArgoCDStatusBanner visible={argoCDUnreachable} />

      {/* v3 -> v4 repo migration notice (migration-ui) — renders nothing
          unless the connected repo is still on the v3 file layout. */}
      <MigrationBanner />

      {/* Bootstrap App Health Banner — inline only for genuinely BLOCKING
          states. Per the "bell absorbs everything" decision (connhealth-2),
          the ArgoCD repo connection now also surfaces in the notification
          bell (Story 1's "ArgoCD can't sync the repo" alert), so softer /
          transient bootstrap states (e.g. Unknown, Progressing) are
          bell-only and do NOT get an inline banner. Only Error, Missing,
          and Degraded actually prevent addon deploys, so only those keep
          the banner. See BOOTSTRAP_BLOCKING_HEALTH. */}
      {isBootstrapBlocking(stats.bootstrap_app_health) && (
        <BootstrapHealthBanner
          health={stats.bootstrap_app_health!}
          sync={stats.bootstrap_app_sync ?? 'Unknown'}
        />
      )}

      {/* Fleet Status Strip — reads FIRST now (S1, walk day 7): the
          numbers-are-doors summary of the whole estate before anything
          else. Replaces the three stat cards (Total Clusters /
          Applications / Upgrades). Maintainer's verdict on the old cards:
          "small fonts, boring looks"; 16-product research says nobody
          lands on a stats poster. One slim row, numbers are doors: clicking
          a segment goes straight to Clusters, Observability's Addon Health
          section, or the version matrix. Each segment carries a short
          title saying what it's counting — managed clusters vs. addon
          applications/versions (walk day 5 finding). */}
      <FleetStatusStrip
        clusters={stats.clusters}
        appsTotal={appTotal}
        appsHealthy={healthyCount}
        behindCatalog={behindCatalog}
      />

      {/* Pull Requests — second content surface (dashboard-purpose
          decision, WQ-1; S1 walk day 7 keeps it right after the pies): a
          Tier-2 bordered panel with the larger title tier, so it carries
          the most visual weight below the fleet strip. Pending/Merged
          toggle. */}
      <PullRequestsPanel onMergeDetected={handlePRMerged} />

      {/* Bottom row (S1, walk day 7): Recent Activity and Issues now sit
          side by side instead of Recent Activity running the full page
          width on its own — its real content (a handful of short rows) was
          narrow and the full width was wasted (maintainer's walk
          complaint). Stacks to one column on narrow screens. */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        {/* Recent Activity. Rows read "installed/updated <addon> on
            <cluster> · rev <sha> · <time>" (S3, walk day 5 ride-along —
            every row used to say "deployed" even for an app that has been
            upgrading for months; falls back to "deployed" if the server
            hasn't sent action yet) — no status dot, since the server
            always reports Succeeded here and a permanently-green dot says
            nothing. */}
        <div className="rounded-xl border border-border bg-card p-5 shadow-sm">
          <div className="mb-3 flex items-center justify-between">
            <div className="flex items-center gap-2">
              <Clock className="h-4 w-4 text-muted-foreground" />
              <h3 className="text-base font-semibold text-card-foreground">Recent Activity</h3>
            </div>
            <button onClick={() => navigate('/observability')} className="text-xs text-teal-600 hover:text-teal-700 dark:text-teal-400">
              View all
            </button>
          </div>
          {recentSyncs.length === 0 ? (
            <EmptyState compact title="No recent sync activity" />
          ) : (
            <div className="space-y-2">
              {recentSyncs.map((sync, i) => (
                <div key={i} className="flex items-center gap-3 text-xs">
                  <div className="min-w-0 flex-1 truncate">
                    <span className="text-muted-foreground">{activityVerb(sync.action)}</span>
                    <span className="font-medium text-card-foreground">{sync.addon_name}</span>
                    <span className="text-muted-foreground"> on {sync.cluster_name}</span>
                    {sync.revision && (
                      <span className="text-muted-foreground"> · rev {sync.revision.slice(0, 7)}</span>
                    )}
                  </div>
                  <span className="shrink-0 text-muted-foreground flex items-center gap-1">
                    <Clock className="h-3 w-3" />
                    {timeAgo(sync.timestamp)}
                  </span>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* Issues — folds the two former standalone cards ("N apps
            unhealthy", "M clusters unreachable") into one box (S1, walk day
            7) so the bottom row pairs cleanly with Recent Activity. Same
            underlying numbers and links as before (confirmedProblemCount ==
            redAppCount + clusterProblemCount, components/AttentionSection.tsx's
            getConfirmedProblemCount) — only the presentation changed. At
            zero, a quiet one-line "No open issues" replaces the cards
            instead of the box disappearing, so the bottom row keeps its
            two-box shape either way.

            Progressing is not an issue (maintainer's verdict) — it never
            renders here. It's still visible on Observability. */}
        <div className="rounded-xl border border-border bg-card p-5 shadow-sm">
          <div className="mb-3 flex items-center gap-2">
            <AlertTriangle className="h-4 w-4 text-muted-foreground" />
            <h3 className="text-base font-semibold text-card-foreground">Issues</h3>
          </div>
          {confirmedProblemCount > 0 ? (
            <div className="space-y-2">
              {redAppCount > 0 && (
                <button
                  onClick={() => navigate('/observability#addon-health')}
                  className="flex w-full items-center gap-2 rounded-lg border border-amber-300 bg-amber-50 px-4 py-2.5 text-left text-sm font-medium text-amber-800 transition-colors hover:bg-amber-100 dark:border-amber-700 dark:bg-amber-900/20 dark:text-amber-300 dark:hover:bg-amber-900/30"
                >
                  <AlertTriangle className="h-4 w-4 shrink-0" />
                  {redAppCount} app{redAppCount !== 1 ? 's' : ''} unhealthy
                  <ChevronRight className="ml-auto h-4 w-4 shrink-0" />
                </button>
              )}
              {clusterProblemCount > 0 && (
                <button
                  onClick={() => navigate('/clusters?status=disconnected')}
                  className="flex w-full items-center gap-2 rounded-lg border border-amber-300 bg-amber-50 px-4 py-2.5 text-left text-sm font-medium text-amber-800 transition-colors hover:bg-amber-100 dark:border-amber-700 dark:bg-amber-900/20 dark:text-amber-300 dark:hover:bg-amber-900/30"
                >
                  <AlertTriangle className="h-4 w-4 shrink-0" />
                  {clusterProblemCount} cluster{clusterProblemCount !== 1 ? 's' : ''} unreachable
                  <ChevronRight className="ml-auto h-4 w-4 shrink-0" />
                </button>
              )}
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">No open issues</p>
          )}
        </div>
      </div>

      {/* GitOps corrections (audit-derived orphan/drift cleanup — distinct
          from the addon version-drift rows that now live on Observability).
          Unchanged by WQ-3 — this is a different surface (GitOps self-heal
          corrections), not part of the attention-detail move. */}
      <DriftAlertsPanel />
    </div>
  );
}
export default Dashboard

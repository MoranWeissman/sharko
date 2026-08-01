import { useState, useEffect, useCallback } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import {
  Server, AlertTriangle,
  Clock, ChevronRight, ShieldAlert, RefreshCw,
  Hourglass, Store, PlusCircle
} from 'lucide-react';
import { api, extractArgocdVersionString } from '@/services/api';
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
import { HomeClusterCard } from '@/components/HomeClusterCard';
import { showToast } from '@/components/ToastNotification';
import {
  FleetStatusStrip,
  summarizeUpgrades,
  type UpgradesSummary,
} from '@/components/FleetStatusStrip';
import { prettyOperation } from '@/lib/utils';
import type { TrackedPR } from '@/services/models';
import {
  useAddonStates,
  deepLinkToAddonOnCluster,
  isSettling,
} from '@/hooks/useAddonStates';
import { isClusterNeedsAttention, getClusterConnectionState } from '@/lib/clusterStatus';

// --- Time ago helpers ---

function timeAgo(timestamp: string): string {
  const secs = Math.floor((Date.now() - new Date(timestamp).getTime()) / 1000);
  if (secs < 60) return 'just now';
  if (secs < 3600) return `${Math.floor(secs / 60)}m ago`;
  if (secs < 86400) return `${Math.floor(secs / 3600)}h ago`;
  return `${Math.floor(secs / 86400)}d ago`;
}

function timeAgoMs(ms: number): string {
  return timeAgo(new Date(ms).toISOString());
}

function minutesAgo(ms: number): number {
  return Math.max(0, Math.floor((Date.now() - ms) / 60_000));
}

// --- The one attention surface ---
//
// dashboard UX review 2026-08-01: this list is the ONLY place on the page
// allowed to show red or amber. Every problem — a disconnected cluster, a
// confirmed-degraded app, an app still settling, an app ArgoCD isn't
// reporting on, an addon with version drift — appears here exactly once,
// with a name, a plain reason, and a link straight to where you'd go to
// look at it.
interface AttentionRow {
  key: string;
  severity: 'problem' | 'attention';
  title: string;
  reason: string;
  link: string;
  badge?: string;
}

function AttentionRowView({ row }: { row: AttentionRow }) {
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
          <p className="font-semibold">ArgoCD Bootstrap Application Issue</p>
          <p className="mt-0.5 text-xs opacity-90">
            The <code className="rounded bg-black/10 px-1 dark:bg-white/10">sharko-engine</code> application
            is the foundation of all addon deployments. An unhealthy bootstrap app may prevent addons from syncing.
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

type HomeClusterInfo = {
  available: boolean;
  message?: string;
  kubernetes_version?: string;
  node_count?: number;
  nodes_ready?: number;
  nodes_not_ready?: number;
} | null;

// perf S2 — everything the page renders from its 8 fetches, snapshotted
// into one cache entry once a load finishes. A revisit within the same
// browser session hydrates all of this in one shot (instant paint, no
// spinner) before kicking a background refresh — see the mount effect
// below and fetchData's setCached call at the end.
interface DashboardSnapshot {
  stats: DashboardStats;
  recentSyncs: SyncActivityEntry[];
  versionDrifts: { addon: string; count: number }[];
  clusters: { name: string; connectionStatus: string }[];
  argoCDUnreachable: boolean;
  homeCluster: HomeClusterInfo;
  sharkoVersion: string | undefined;
  argocdVersion: string | undefined;
  argocdConnected: boolean;
  uptime: string | undefined;
  upgrades: UpgradesSummary;
}

// Exported so tests can prime/inspect the same key the component uses.
export const DASHBOARD_CACHE_KEY = 'dashboard';

export function Dashboard() {
  const navigate = useNavigate();
  const [stats, setStats] = useState<DashboardStats | null>(null);
  const [recentSyncs, setRecentSyncs] = useState<SyncActivityEntry[]>([]);
  const [versionDrifts, setVersionDrifts] = useState<{ addon: string; count: number }[]>([]);
  const [loading, setLoading] = useState(true);
  const [isRefreshing, setIsRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Addon health/sync state is sourced from the unified AddonStatesProvider
  // so the Dashboard's attention list agrees with AddonDetail / ClusterDetail.
  // The provider polls /dashboard/attention once for the whole app.
  const { byApp: addonStateMap, refresh: refreshAddonStates } = useAddonStates();
  const [showAttention, setShowAttention] = useState(false);
  const [showProgressing, setShowProgressing] = useState(false);
  // Cluster problem rows — failed/missing ONLY (dashboard UX review
  // 2026-08-01: cluster rows in the attention list are for connectivity
  // problems, not a duplicate of addon health under a cluster heading).
  const [clusters, setClusters] = useState<{ name: string; connectionStatus: string }[]>([]);
  const [argoCDUnreachable, setArgoCDUnreachable] = useState(false);
  const [homeCluster, setHomeCluster] = useState<HomeClusterInfo>(null);
  // Home-cluster identity card (Package 3) — three more thin reads, each
  // degrading independently to undefined ("—" in the card) rather than
  // failing the whole dashboard fetch.
  const [sharkoVersion, setSharkoVersion] = useState<string | undefined>(undefined);
  const [argocdVersion, setArgocdVersion] = useState<string | undefined>(undefined);
  const [argocdConnected, setArgocdConnected] = useState(false);
  const [uptime, setUptime] = useState<string | undefined>(undefined);
  // Upgrades stat (Package 2 #1) — derived from the version matrix.
  const [upgrades, setUpgrades] = useState<UpgradesSummary>({ withUpgrade: 0, checked: 0, namesWithUpgrade: [] });
  // Wall-clock time of the last successful (or attempted) fetch — powers
  // the calm all-fine line's "last checked" timestamp.
  const [lastChecked, setLastChecked] = useState<number | null>(null);

  // S3 — progressive paint: every read still fires at once (unchanged
  // parallelism), but the page no longer waits for Promise.all on all 8
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
    const homeClusterPromise = api.getHomeCluster().catch(() => null);
    const healthPromise = api.health().catch(() => null);
    const configPromise = api.getConfig().catch(() => null);
    const fleetPromise = api.getFleetStatus().catch(() => null);

    let statsData: DashboardStats;
    try {
      statsData = await api.getDashboardStats();
    } catch (e: unknown) {
      if (!background) {
        setError(e instanceof Error ? e.message : 'Failed to load dashboard');
      }
      setLoading(false);
      setIsRefreshing(false);
      setLastChecked(Date.now());
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
    let snapVersionDrifts: { addon: string; count: number }[] = [];
    let snapClusters: { name: string; connectionStatus: string }[] = [];
    let snapArgoCDUnreachable = false;
    let snapHomeCluster: HomeClusterInfo = null;
    let snapSharkoVersion: string | undefined;
    let snapArgocdVersion: string | undefined;
    let snapArgocdConnected = false;
    let snapUptime: string | undefined;
    let snapUpgrades: UpgradesSummary = { withUpgrade: 0, checked: 0, namesWithUpgrade: [] };

    obsPromise.then((obsData) => {
      // Recent syncs (last 5)
      if (obsData?.recent_syncs) {
        snapRecentSyncs = obsData.recent_syncs.slice(0, 5);
        setRecentSyncs(snapRecentSyncs);
      }
    });

    matrixPromise.then((matrixData) => {
      snapUpgrades = summarizeUpgrades(matrixData);
      setUpgrades(snapUpgrades);

      if (matrixData?.addons) {
        // Version drifts
        const drifts: { addon: string; count: number }[] = [];
        for (const row of matrixData.addons) {
          let driftCount = 0;
          for (const cell of Object.values(row.cells || {})) {
            if (cell?.drift_from_catalog) driftCount++;
          }
          if (driftCount > 0) {
            drifts.push({ addon: row.addon_name, count: driftCount });
          }
        }
        snapVersionDrifts = drifts;
        setVersionDrifts(drifts);
      } else {
        snapVersionDrifts = [];
        setVersionDrifts([]);
      }
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

      // Cluster attention rows — failed/missing only, built from the
      // per-cluster /clusters list (the aggregate /dashboard/stats has
      // counts but not names/links). A cluster with an open (not yet
      // merged) registration PR is excluded — it's mid-lifecycle, not
      // broken.
      if (typedClusters?.clusters) {
        const pendingNames = new Set(
          (typedClusters.pending_registrations ?? []).map((p) => p.cluster_name),
        );
        snapClusters = typedClusters.clusters
          .filter((c) => !pendingNames.has(c.name) && isClusterNeedsAttention(c.connection_status))
          .map((c) => ({ name: c.name, connectionStatus: c.connection_status || 'Unknown' }));
      } else {
        snapClusters = [];
      }
      setClusters(snapClusters);
    });

    homeClusterPromise.then((homeClusterData) => {
      snapHomeCluster = homeClusterData;
      setHomeCluster(homeClusterData);
    });

    healthPromise.then((healthData) => {
      snapSharkoVersion = healthData?.version;
      setSharkoVersion(snapSharkoVersion);
    });

    configPromise.then((configData) => {
      snapArgocdConnected = !!configData?.argocd?.connected;
      // Defensive: api.getConfig() already normalizes argocd.version to a
      // plain string (the server sends ArgoCD's full version-info object,
      // not a string — see extractArgocdVersionString in services/api.ts).
      // Re-normalizing here too means a non-string ever reaching this line
      // (e.g. a mocked/older api client) still can't crash the render — it
      // becomes "—" via HomeClusterCard's own fallback instead of a raw
      // object hitting JSX.
      snapArgocdVersion = extractArgocdVersionString(configData?.argocd?.version);
      setArgocdConnected(snapArgocdConnected);
      setArgocdVersion(snapArgocdVersion);
    });

    fleetPromise.then((fleetData) => {
      snapUptime = fleetData?.uptime;
      setUptime(snapUptime);
    });

    await Promise.allSettled([
      obsPromise,
      matrixPromise,
      clustersPromise,
      homeClusterPromise,
      healthPromise,
      configPromise,
      fleetPromise,
    ]);

    setIsRefreshing(false);
    setLastChecked(Date.now());

    // Write-through: next mount this session paints all of this instantly.
    setCached<DashboardSnapshot>(DASHBOARD_CACHE_KEY, {
      stats: statsData,
      recentSyncs: snapRecentSyncs,
      versionDrifts: snapVersionDrifts,
      clusters: snapClusters,
      argoCDUnreachable: snapArgoCDUnreachable,
      homeCluster: snapHomeCluster,
      sharkoVersion: snapSharkoVersion,
      argocdVersion: snapArgocdVersion,
      argocdConnected: snapArgocdConnected,
      uptime: snapUptime,
      upgrades: snapUpgrades,
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
      setVersionDrifts(cached.data.versionDrifts);
      setClusters(cached.data.clusters);
      setArgoCDUnreachable(cached.data.argoCDUnreachable);
      setHomeCluster(cached.data.homeCluster);
      setSharkoVersion(cached.data.sharkoVersion);
      setArgocdVersion(cached.data.argocdVersion);
      setArgocdConnected(cached.data.argocdConnected);
      setUptime(cached.data.uptime);
      setUpgrades(cached.data.upgrades);
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

  // Split addon states into: confirmed problems (red), settling problems
  // (amber, degraded/missing but inside the settling window), apps ArgoCD
  // isn't reporting on (amber, never red — a Suspended or Unknown health
  // status is not a failure), and Progressing (its own calm blue widget,
  // never counted here at all).
  const allAddonStates = Array.from(addonStateMap.values());
  const badAddonStates = allAddonStates.filter(
    (s) => s.displayState === 'degraded' || s.displayState === 'missing',
  );
  const confirmedAddonItems = badAddonStates.filter((s) => !isSettling(s));
  const settlingAddonItems = badAddonStates.filter((s) => isSettling(s));
  const unknownAddonItems = allAddonStates.filter((s) => s.displayState === 'unknown');
  const progressingItems = allAddonStates.filter((s) => s.displayState === 'progressing-advisory');

  const addonLink = (item: { addonName: string; cluster: string }) =>
    item.cluster ? deepLinkToAddonOnCluster(item.addonName, item.cluster) : `/addons/${encodeURIComponent(item.addonName)}`;

  const confirmedAddonRows: AttentionRow[] = confirmedAddonItems.map((item) => ({
    key: `addon-${item.addonName}@${item.cluster}`,
    severity: 'problem',
    title: item.appName || item.addonName,
    reason: item.advisoryMessage
      ? `${item.errorType ? item.errorType + ': ' : ''}${item.advisoryMessage}`
      : `Health: ${item.healthStatus}${item.cluster ? ` on ${item.cluster}` : ''}`,
    link: addonLink(item),
    badge: item.healthStatus,
  }));

  const settlingAddonRows: AttentionRow[] = settlingAddonItems.map((item) => ({
    key: `settling-${item.addonName}@${item.cluster}`,
    severity: 'attention',
    title: item.appName || item.addonName,
    reason: `Settling — degraded for ${minutesAgo(item.badSince ?? Date.now())}m. Given a grace window before counting as a confirmed problem.`,
    link: addonLink(item),
    badge: item.healthStatus,
  }));

  const unknownAddonRows: AttentionRow[] = unknownAddonItems.map((item) => ({
    key: `unknown-${item.addonName}@${item.cluster}`,
    severity: 'attention',
    title: item.appName || item.addonName,
    reason: `ArgoCD is not reporting a health status for this app${item.cluster ? ` on ${item.cluster}` : ''}.`,
    link: addonLink(item),
    badge: item.healthStatus,
  }));

  // Cluster problem rows — failed/missing only, reason text straight from
  // clusterStatus.ts (the single source of truth for this vocabulary).
  const clusterAttentionRows: AttentionRow[] = clusters.map((c) => ({
    key: `cluster-${c.name}`,
    severity: 'problem',
    title: c.name,
    reason: getClusterConnectionState(c.connectionStatus).meaning,
    link: `/clusters/${encodeURIComponent(c.name)}`,
  }));

  const driftRows: AttentionRow[] = versionDrifts.map((d) => ({
    key: `drift-${d.addon}`,
    severity: 'attention',
    title: d.addon,
    reason: `Different versions deployed across ${d.count} cluster${d.count !== 1 ? 's' : ''}.`,
    link: `/addons/${encodeURIComponent(d.addon)}`,
  }));

  // The Dashboard's own "how many clusters are actually disconnected"
  // number comes from the server's single classification (Package 1),
  // not a browser-side re-derivation — every surface on this page that
  // states the COUNT reads stats.clusters. The named clusterAttentionRows
  // above exist only because the aggregate endpoint has no per-cluster
  // names/links to give the expanded list; in the ordinary case both
  // describe the same clusters.
  const clusterProblemCount = stats.clusters.failed + stats.clusters.missing;
  const redAppCount = confirmedAddonRows.length;
  const settlingCount = settlingAddonRows.length;
  const unknownCount = unknownAddonRows.length;
  const driftCount = driftRows.length;
  const hasIssues = clusterProblemCount > 0 || redAppCount > 0 || settlingCount > 0 || unknownCount > 0 || driftCount > 0;

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
          onClick={handleRefresh}
          className="rounded-md p-2 text-white/70 hover:bg-white/10 hover:text-white"
          title="Refresh"
        >
          <RefreshCw className={`h-4 w-4 ${isRefreshing ? 'animate-spin' : ''}`} />
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

      {/* Needs Attention — the ONE surface allowed red/amber on this page
          (dashboard UX review 2026-08-01). Every problem appears once,
          with a name, a plain reason, and a deep link. */}
      {hasIssues ? (
        <div className="rounded-xl border-2 border-amber-300 bg-amber-50 dark:border-amber-700 dark:bg-amber-900/20">
          <div className="flex items-center justify-between p-5 pb-3">
            <div className="flex items-center gap-2 text-amber-700 dark:text-amber-400">
              <AlertTriangle className="h-5 w-5" />
              <h3 className="text-sm font-semibold">Needs Attention</h3>
            </div>
            <div className="flex flex-wrap gap-2">
              {redAppCount > 0 && (
                <button onClick={() => setShowAttention(!showAttention)}
                  aria-expanded={showAttention}
                  className="flex items-center gap-2 rounded-lg border border-red-200 bg-[#f0f7ff] px-3 py-1.5 text-xs text-red-700 hover:bg-red-50 dark:border-red-800 dark:bg-gray-800 dark:text-red-400">
                  <div className="h-2 w-2 rounded-full bg-red-500" />
                  {redAppCount} app{redAppCount !== 1 ? 's' : ''} with issues
                  <ChevronRight className={`h-3 w-3 transition-transform ${showAttention ? 'rotate-90' : ''}`} />
                </button>
              )}
              {clusterProblemCount > 0 && (
                <button onClick={() => setShowAttention(!showAttention)}
                  aria-expanded={showAttention}
                  className="flex items-center gap-2 rounded-lg border border-red-200 bg-[#f0f7ff] px-3 py-1.5 text-xs text-red-700 hover:bg-red-50 dark:border-red-800 dark:bg-gray-800 dark:text-red-400">
                  <div className="h-2 w-2 rounded-full bg-red-500" />
                  {clusterProblemCount} disconnected cluster{clusterProblemCount !== 1 ? 's' : ''}
                  <ChevronRight className={`h-3 w-3 transition-transform ${showAttention ? 'rotate-90' : ''}`} />
                </button>
              )}
              {settlingCount > 0 && (
                <button onClick={() => setShowAttention(!showAttention)}
                  aria-expanded={showAttention}
                  className="flex items-center gap-2 rounded-lg border border-amber-200 bg-[#f0f7ff] px-3 py-1.5 text-xs text-amber-700 hover:bg-amber-50 dark:border-amber-800 dark:bg-gray-800 dark:text-amber-400">
                  <div className="h-2 w-2 rounded-full bg-amber-500" />
                  {settlingCount} settling
                </button>
              )}
              {unknownCount > 0 && (
                <button onClick={() => setShowAttention(!showAttention)}
                  aria-expanded={showAttention}
                  className="flex items-center gap-2 rounded-lg border border-amber-200 bg-[#f0f7ff] px-3 py-1.5 text-xs text-amber-700 hover:bg-amber-50 dark:border-amber-800 dark:bg-gray-800 dark:text-amber-400">
                  <div className="h-2 w-2 rounded-full bg-amber-500" />
                  {unknownCount} app{unknownCount !== 1 ? 's' : ''} not reporting
                </button>
              )}
              {driftCount > 0 && (
                <button onClick={() => setShowAttention(!showAttention)}
                  aria-expanded={showAttention}
                  className="flex items-center gap-2 rounded-lg border border-amber-200 bg-[#f0f7ff] px-3 py-1.5 text-sm text-amber-700 hover:bg-amber-50 dark:border-amber-800 dark:bg-gray-800 dark:text-amber-400">
                  <div className="h-2 w-2 rounded-full bg-amber-500" />
                  {driftCount} addon{driftCount !== 1 ? 's' : ''} with drift
                </button>
              )}
            </div>
          </div>
          {/* Expanded detail — cluster/addon names route to their own
              detail pages for quick debug + AI-assisted investigation. */}
          {showAttention && (
            <div className="border-t border-amber-200 p-4 dark:border-amber-700 space-y-4">
              {clusterAttentionRows.length > 0 && (
                <div>
                  <div className="mb-1.5 flex items-center justify-between">
                    <h4 className="text-xs font-semibold uppercase tracking-wide text-[#3a6a8a] dark:text-gray-400">Clusters</h4>
                    <button onClick={() => navigate('/clusters?status=disconnected')} className="text-xs text-teal-600 hover:text-teal-700 dark:text-teal-400">
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
                    <button onClick={() => navigate('/version-matrix?drift=true')} className="text-xs text-teal-600 hover:text-teal-700 dark:text-teal-400">
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
      ) : (
        <div className="flex items-center gap-2 rounded-xl border border-green-200 bg-green-50 px-5 py-3 dark:border-green-800 dark:bg-green-900/20">
          <div className="h-2 w-2 shrink-0 rounded-full bg-green-500" />
          <span className="text-sm font-medium text-green-700 dark:text-green-400">All systems operational</span>
          <span className="text-xs text-green-600/80 dark:text-green-400/70">
            · last checked {lastChecked ? timeAgoMs(lastChecked) : 'just now'}
          </span>
        </div>
      )}

      {/* Progressing widget — apps can still be healthy while progressing;
          subtle blue styling so it doesn't read as a hard error. Click an
          addon name to land on the cluster's addon row for debugging. */}
      {progressingItems.length > 0 && (
        <div className="rounded-xl border border-blue-200 bg-blue-50/60 dark:border-blue-800 dark:bg-blue-900/10">
          <div className="flex items-center justify-between p-4 pb-2">
            <div className="flex items-center gap-2 text-blue-700 dark:text-blue-300">
              <Hourglass className="h-4 w-4" />
              <h3 className="text-sm font-semibold">Progressing — usually temporary</h3>
            </div>
            <button
              onClick={() => setShowProgressing(!showProgressing)}
              aria-expanded={showProgressing}
              className="flex items-center gap-2 rounded-lg border border-blue-200 bg-white/70 px-3 py-1.5 text-sm text-blue-700 hover:bg-white dark:border-blue-700 dark:bg-gray-800 dark:text-blue-300"
            >
              <div className="h-2 w-2 rounded-full bg-blue-500" />
              {progressingItems.length} app{progressingItems.length !== 1 ? 's' : ''} progressing
              <ChevronRight className={`h-3 w-3 transition-transform ${showProgressing ? 'rotate-90' : ''}`} />
            </button>
          </div>
          {showProgressing && (
            <div className="border-t border-blue-200/70 p-3 dark:border-blue-800/60">
              <p className="mb-2 text-sm text-blue-700/80 dark:text-blue-300/80">
                Apps in this state are still considered healthy. ArgoCD is rolling out a change or waiting on a workload.
                Click an addon to investigate on its cluster page.
              </p>
              <div className="max-h-64 overflow-y-auto space-y-1.5">
                {progressingItems.map((item, i) => (
                  <div key={`prog-${item.addonName}@${item.cluster}-${i}`} className="flex items-center gap-3 rounded-lg bg-white/70 px-3 py-2 text-xs dark:bg-gray-800">
                    <div className="h-2.5 w-2.5 shrink-0 rounded-full bg-blue-500" />
                    <Link
                      to={item.cluster ? deepLinkToAddonOnCluster(item.addonName, item.cluster) : `/addons/${encodeURIComponent(item.addonName)}`}
                      className="flex-1 truncate font-medium text-card-foreground hover:text-teal-600 hover:underline dark:hover:text-teal-400"
                      title="Investigate on cluster page"
                    >
                      {item.appName || item.addonName}
                      {item.cluster && <span className="ml-1 text-muted-foreground font-normal">on {item.cluster}</span>}
                    </Link>
                    <span className="rounded bg-blue-100 px-1.5 py-0.5 text-xs font-medium text-blue-700 dark:bg-blue-900/40 dark:text-blue-300">
                      Progressing
                    </span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      )}

      {/* Pull Requests — promoted to the first content surface after the
          attention layer (dashboard-purpose decision, WQ-1): a Tier-2
          bordered panel with the larger title tier, so it carries the most
          visual weight of anything below Needs Attention. Pending/Merged
          toggle. */}
      <PullRequestsPanel onMergeDetected={handlePRMerged} />

      {/* Fleet Status Strip — replaces the three stat cards (Total
          Clusters / Applications / Upgrades). Maintainer's verdict on the
          old cards: "small fonts, boring looks"; 16-product research says
          nobody lands on a stats poster. One slim row, numbers are doors:
          clicking a segment goes straight to Clusters, Observability's
          Addon Health section, or the version matrix. */}
      <FleetStatusStrip
        clusters={stats.clusters}
        appsTotal={appTotal}
        appsHealthy={healthyCount}
        upgrades={upgrades}
      />

      {/* Recent Activity — moved up from the old bottom row (Package 2:
          new page order). Rows read "deployed <addon> on <cluster> · rev
          <sha> · <time>" (panel-lens finding) — no status dot, since the
          server always reports Succeeded here and a permanently-green dot
          says nothing. */}
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
                  <span className="text-muted-foreground">deployed </span>
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

      {/* GitOps corrections (audit-derived orphan/drift cleanup — distinct
          from the addon version-drift rows in Needs Attention above). */}
      <DriftAlertsPanel />

      {/* Bottom row: home-cluster identity card only (Quick Actions and
          Available Addons are gone — maintainer's call). A 3-column grid
          with two empty cells reads like a layout bug, so this renders as
          a standalone, width-capped block instead of a grid — Tier 3
          ("compact, quieter") stays compact rather than stretching to
          fill a row it doesn't need. */}
      {homeCluster && (
        <HomeClusterCard
          homeCluster={homeCluster}
          sharkoVersion={sharkoVersion}
          argocdVersion={argocdVersion}
          argocdConnected={argocdConnected}
          uptime={uptime}
        />
      )}
    </div>
  );
}
export default Dashboard

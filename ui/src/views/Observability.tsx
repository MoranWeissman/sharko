import { useState, useEffect, useCallback, useMemo } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  CartesianGrid,
  Legend,
  LineChart,
  Line,
} from 'recharts';
import { DistributionPie, type DistributionSlice } from '@/components/DistributionPie';
import {
  Activity,
  Clock,
  Server,
  CheckCircle,
  AlertTriangle,
  RefreshCw,
  ChevronDown,
  ChevronUp,
  ShieldAlert,
  ExternalLink,
  Heart,
  LayoutGrid,
  LayoutList,
  Search,
  X,
} from 'lucide-react';
import { api } from '@/services/api';
import type {
  ObservabilityOverviewResponse,
  AddonGroupHealth,
  ResourceAlert,
  SyncActivityEntry,
  DashboardStats,
} from '@/services/models';
import { LoadingState } from '@/components/LoadingState';
import { ErrorState } from '@/components/ErrorState';
import { EmptyState } from '@/components/EmptyState';
import { PaginationControls, PageSizeSelector, type PageSize } from '@/components/PaginationControls';
import {
  AttentionRowView,
  countOpenIssues,
  splitAddonStates,
  buildConfirmedAddonRows,
  buildSettlingAddonRows,
  buildUnknownAddonRows,
  buildClusterAttentionRows,
  buildDriftRows,
  classifyAddonGroupProblem,
  describeAddonProblem,
  type AttentionRow,
  type AddonGroupProblemTier,
} from '@/components/AttentionSection';
import { useAddonStates, deepLinkToAddonOnCluster, type AddonState } from '@/hooks/useAddonStates';
import { isClusterNeedsAttention } from '@/lib/clusterStatus';

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function timeAgo(dateStr: string): string {
  const now = Date.now();
  const then = new Date(dateStr).getTime();
  const diffSecs = Math.max(0, Math.floor((now - then) / 1000));

  if (diffSecs < 60) return `${diffSecs}s ago`;
  const mins = Math.floor(diffSecs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

function healthBadgeCls(health: string): string {
  const h = health.toLowerCase();
  if (h === 'healthy')
    return 'bg-green-100 text-green-800 dark:bg-green-900/40 dark:text-green-300';
  if (h === 'degraded')
    return 'bg-red-100 text-red-800 dark:bg-red-900/40 dark:text-red-300';
  if (h === 'progressing')
    return 'bg-blue-100 text-blue-800 dark:bg-blue-900/40 dark:text-blue-300';
  return 'bg-[#d6eeff] text-[#1a4a6a] dark:bg-gray-800 dark:text-gray-400';
}

function syncBadgeCls(sync: string): string {
  const s = (sync ?? '').toLowerCase();
  if (s === 'synced')
    return 'bg-green-100 text-green-800 dark:bg-green-900/40 dark:text-green-300';
  if (s === 'outofsync' || s === 'out_of_sync')
    return 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-300';
  return 'bg-[#d6eeff] text-[#1a4a6a] dark:bg-gray-800 dark:text-gray-400';
}

const HEALTH_COLORS: Record<string, string> = {
  Healthy: '#22c55e',
  Degraded: '#ef4444',
  Progressing: '#3b82f6',
  Missing: '#f59e0b',
  Unknown: '#9ca3af',
  Suspended: '#9ca3af',
};

const SYNC_COLORS: Record<string, string> = {
  Synced: '#22c55e',
  OutOfSync: '#f59e0b',
  Unknown: '#9ca3af',
};

function statusIcon(status: string) {
  const s = status.toLowerCase();
  if (s === 'succeeded' || s === 'synced' || s === 'healthy')
    return <CheckCircle className="h-4 w-4 text-green-500" />;
  if (s === 'failed' || s === 'degraded')
    return <AlertTriangle className="h-4 w-4 text-red-500" />;
  return <RefreshCw className="h-4 w-4 text-blue-400" />;
}

// ---------------------------------------------------------------------------
// Section 1: Control Plane Health
// ---------------------------------------------------------------------------

function ControlPlaneSection({
  data,
  stats,
  argoCDUrl,
}: {
  data: ObservabilityOverviewResponse['control_plane'];
  stats: DashboardStats | null;
  argoCDUrl: string | null;
}) {
  // v4 slim (maintainer's lock: "go with the slim version") — this panel is
  // now ONE machinery-of-deployment header: version chips + the Sharko
  // Engine app health, folded in from the old standalone
  // "Sharko Engine" section. The four v3 tiles (Applications, ApplicationSets,
  // Clusters configured, In ArgoCD) and the health-summary bar are gone —
  // they either mislabeled addon data as control-plane data, or duplicated
  // the fleet 5-state model / the addon-health section below.
  const health = stats?.bootstrap_app_health ?? 'Unknown';
  const sync = stats?.bootstrap_app_sync ?? 'Unknown';
  const { dot, badge } = bootstrapHealthColor(health);
  const appExists = health !== 'Missing' && health !== 'Unknown' && health !== '';
  const argoCDLink =
    appExists && argoCDUrl
      ? `${argoCDUrl.replace(/\/$/, '')}/applications/sharko-engine`
      : null;

  return (
    <section className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-6 shadow-sm dark:ring-gray-700 dark:bg-gray-900">
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-center gap-3">
          <h2 className="text-lg font-semibold text-[#0a2a4a] dark:text-gray-100">
            ArgoCD Control Plane
          </h2>
          <span className="rounded-full bg-teal-100 px-2.5 py-0.5 text-xs font-medium text-teal-800 dark:bg-teal-900/40 dark:text-teal-300">
            ArgoCD {data.argocd_version}
          </span>
          <span className="rounded-full bg-[#d6eeff] px-2.5 py-0.5 text-xs font-medium text-[#1a4a6a] dark:bg-gray-800 dark:text-gray-400">
            Helm {data.helm_version}
          </span>
          <span className="rounded-full bg-[#d6eeff] px-2.5 py-0.5 text-xs font-medium text-[#1a4a6a] dark:bg-gray-800 dark:text-gray-400">
            kubectl {data.kubectl_version}
          </span>
        </div>
        {argoCDLink && (
          <a
            href={argoCDLink}
            target="_blank"
            rel="noopener noreferrer"
            className="flex items-center gap-1.5 rounded-md bg-[#d6eeff] px-3 py-1 text-xs font-medium text-[#1a4a6a] transition-colors hover:bg-[#bee0ff] dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
          >
            <ExternalLink className="h-3.5 w-3.5" />
            View in ArgoCD
          </a>
        )}
      </div>

      <div className="flex flex-wrap items-center gap-6 border-t border-[#6aade0] pt-4 dark:border-gray-700">
        <div className="flex items-center gap-2">
          <Heart className="h-4 w-4 text-teal-500" />
          <span className="text-sm font-medium text-[#0a2a4a] dark:text-gray-100">
            Sharko Engine
          </span>
          <span className="text-xs text-[#5a8aaa] dark:text-gray-500">
            the ArgoCD application that deploys your addons from git
          </span>
        </div>
        <div className="flex items-center gap-2">
          <span className={`inline-block h-3 w-3 rounded-full ${dot}`} />
          <span className={`rounded-full px-2.5 py-0.5 text-xs font-semibold uppercase ${badge}`}>
            {health || 'Unknown'}
          </span>
        </div>
        <div className="flex items-center gap-2">
          <span className="text-xs font-medium uppercase tracking-wide text-[#5a8aaa] dark:text-gray-500">
            Sync
          </span>
          <span className={`rounded-full px-2.5 py-0.5 text-xs font-semibold uppercase ${syncBadgeCls(sync)}`}>
            {sync || 'Unknown'}
          </span>
        </div>
      </div>

      {health && health.toLowerCase() !== 'healthy' && health.toLowerCase() !== 'unknown' && health !== '' && (
        <div className="mt-4 flex items-start gap-2 rounded-lg bg-amber-50 p-3 ring-1 ring-amber-300 dark:bg-amber-950/30 dark:ring-amber-700">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-600 dark:text-amber-400" />
          <p className="text-sm text-amber-800 dark:text-amber-300">
            The engine app is <strong>{health}</strong>. Check ArgoCD for sync errors or missing resources.
            Addon installations and updates may not work until this is resolved.
          </p>
        </div>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------------
// Section 2: Health and Sync Distribution Donuts
// ---------------------------------------------------------------------------

// Both pies below read the SAME source — addon_groups[].child_apps[] — so
// "All addons" and any single-addon filter are computed the same way, and
// the "All addons" total always matches control_plane.health_summary (both
// come from the same server-side app list; see Observability.test.tsx for
// the parity check). Walk-day finding: the old titles just said
// "Application ..." with no hint of WHICH applications — these are the
// addon applications running on managed clusters, not ArgoCD itself.

function computeHealthSlices(addonGroups: AddonGroupHealth[]): DistributionSlice[] {
  const counts: Record<string, number> = {};
  for (const group of addonGroups ?? []) {
    for (const child of group.child_apps ?? []) {
      const health = child.health || 'Unknown';
      counts[health] = (counts[health] ?? 0) + 1;
    }
  }
  return Object.entries(counts).map(([name, value]) => ({
    key: name,
    label: name,
    value,
    color: HEALTH_COLORS[name] ?? '#9ca3af',
  }));
}

function computeSyncSlices(addonGroups: AddonGroupHealth[]): DistributionSlice[] {
  const counts: Record<string, number> = {
    Synced: 0,
    OutOfSync: 0,
    Unknown: 0,
  };

  for (const group of addonGroups ?? []) {
    for (const child of group.child_apps ?? []) {
      const status = (child.sync_status ?? '').toLowerCase();
      if (status === 'synced') {
        counts.Synced++;
      } else if (status === 'outofsync' || status === 'out_of_sync') {
        counts.OutOfSync++;
      } else {
        counts.Unknown++;
      }
    }
  }

  return Object.entries(counts).map(([name, value]) => ({
    key: name,
    label: name,
    value,
    color: SYNC_COLORS[name] ?? '#9ca3af',
  }));
}

function HealthDistributionSection({
  addonGroups,
  subtitle,
}: {
  addonGroups: AddonGroupHealth[];
  subtitle: string;
}) {
  const healthSlices: DistributionSlice[] = useMemo(
    () => computeHealthSlices(addonGroups),
    [addonGroups],
  );

  const total = healthSlices.reduce((sum, d) => sum + d.value, 0);

  if (total === 0) {
    return (
      <div
        data-testid="health-distribution-card"
        className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-6 dark:ring-gray-700 dark:bg-gray-800"
      >
        <h3 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
          Addon Application Health
        </h3>
        <p className="mb-2 text-xs text-[#5a8aaa] dark:text-gray-500">{subtitle}</p>
        <p className="text-sm text-[#2a5a7a] dark:text-gray-400">
          No applications yet
        </p>
      </div>
    );
  }

  return (
    <div
      data-testid="health-distribution-card"
      className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:ring-gray-700 dark:bg-gray-800"
    >
      <h3 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
        Addon Application Health
      </h3>
      <p className="mb-3 text-xs text-[#5a8aaa] dark:text-gray-500">{subtitle}</p>
      <DistributionPie
        slices={healthSlices}
        ariaPrefix="Addon application health"
        legendOrientation="horizontal"
        testId="health-distribution-pie"
        legendTestId="health-distribution-legend"
      />
      <div className="mt-2 text-center text-sm font-medium text-[#2a5a7a] dark:text-gray-400">
        Total: {total} application{total !== 1 ? 's' : ''}
      </div>
    </div>
  );
}

function SyncDistributionSection({
  addonGroups,
  subtitle,
}: {
  addonGroups: AddonGroupHealth[];
  subtitle: string;
}) {
  const syncSlices: DistributionSlice[] = useMemo(
    () => computeSyncSlices(addonGroups),
    [addonGroups],
  );

  const total = syncSlices.reduce((sum, d) => sum + d.value, 0);

  if (total === 0) {
    return (
      <div
        data-testid="sync-distribution-card"
        className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-6 dark:ring-gray-700 dark:bg-gray-800"
      >
        <h3 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
          Addon Application Sync
        </h3>
        <p className="mb-2 text-xs text-[#5a8aaa] dark:text-gray-500">{subtitle}</p>
        <p className="text-sm text-[#2a5a7a] dark:text-gray-400">
          No applications yet
        </p>
      </div>
    );
  }

  return (
    <div
      data-testid="sync-distribution-card"
      className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:ring-gray-700 dark:bg-gray-800"
    >
      <h3 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
        Addon Application Sync
      </h3>
      <p className="mb-3 text-xs text-[#5a8aaa] dark:text-gray-500">{subtitle}</p>
      <DistributionPie
        slices={syncSlices}
        ariaPrefix="Addon application sync"
        legendOrientation="horizontal"
        testId="sync-distribution-pie"
        legendTestId="sync-distribution-legend"
      />
      <div className="mt-2 text-center text-sm font-medium text-[#2a5a7a] dark:text-gray-400">
        Total: {total} application{total !== 1 ? 's' : ''}
      </div>
    </div>
  );
}

// Shared per-addon filter for both distribution pies (walk-day finding: the
// maintainer wants to answer "health/sync FOR WHICH addon", not just the
// aggregate). appProject filtering is explicitly out of scope — the project
// name isn't in this wire response.
function AddonDistributionSection({ addonGroups }: { addonGroups: AddonGroupHealth[] }) {
  const [selectedAddon, setSelectedAddon] = useState('all');

  const addonNames = useMemo(
    () => [...new Set((addonGroups ?? []).map((g) => g.addon_name))].sort(),
    [addonGroups],
  );

  const filteredGroups = useMemo(() => {
    if (selectedAddon === 'all') return addonGroups ?? [];
    return (addonGroups ?? []).filter((g) => g.addon_name === selectedAddon);
  }, [addonGroups, selectedAddon]);

  const subtitle =
    selectedAddon === 'all'
      ? 'All addon applications across your managed clusters'
      : `${selectedAddon} applications across your managed clusters`;

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <label
          htmlFor="addon-distribution-filter"
          className="text-sm font-medium text-[#2a5a7a] dark:text-gray-400"
        >
          Addon
        </label>
        <select
          id="addon-distribution-filter"
          value={selectedAddon}
          onChange={(e) => setSelectedAddon(e.target.value)}
          aria-label="Filter distribution charts by addon"
          className="rounded-md ring-2 ring-[#6aade0] bg-[#f0f7ff] px-2 py-1 text-xs text-[#0a3a5a] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300"
        >
          <option value="all">All addons</option>
          {addonNames.map((name) => (
            <option key={name} value={name}>
              {name}
            </option>
          ))}
        </select>
      </div>
      <div className="grid grid-cols-1 gap-6 md:grid-cols-2">
        <HealthDistributionSection addonGroups={filteredGroups} subtitle={subtitle} />
        <SyncDistributionSection addonGroups={filteredGroups} subtitle={subtitle} />
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Section 3: Resource Alerts
// ---------------------------------------------------------------------------

function ResourceAlertsSection({ alerts }: { alerts: ResourceAlert[] }) {
  if (!alerts || alerts.length === 0) return null;

  return (
    <section className="rounded-xl border border-amber-300 bg-amber-50 p-6 shadow-sm dark:border-amber-700 dark:bg-amber-950/30">
      <div className="mb-4 flex items-center gap-2">
        <ShieldAlert className="h-5 w-5 text-amber-600 dark:text-amber-400" />
        <h2 className="text-lg font-semibold text-amber-800 dark:text-amber-200">
          Resource Configuration Alerts
        </h2>
        <span className="rounded-full bg-amber-200 px-2 py-0.5 text-xs font-medium text-amber-800 dark:bg-amber-800 dark:text-amber-200">
          {alerts.length}
        </span>
      </div>
      <p className="mb-4 text-sm text-amber-700 dark:text-amber-300">
        The following addons are missing resource requests or limits in their
        global values. This can lead to unpredictable resource usage and
        scheduling issues.
      </p>
      <div className="space-y-2">
        {alerts.map((alert) => (
          <div
            key={`${alert.addon_name}-${alert.alert_type}`}
            className="flex items-center justify-between rounded-lg bg-[#f0f7ff] px-4 py-3 dark:bg-gray-900"
          >
            <div className="flex items-center gap-3">
              <AlertTriangle className="h-4 w-4 shrink-0 text-amber-500" />
              <div>
                <span className="text-sm font-medium text-[#0a2a4a] dark:text-gray-100">
                  {alert.addon_name}
                </span>
                <p className="text-sm text-[#2a5a7a] dark:text-gray-400">
                  {alert.details}
                </p>
              </div>
            </div>
            <span className="rounded-full bg-amber-100 px-2 py-0.5 text-xs font-semibold uppercase text-amber-700 dark:bg-amber-900/40 dark:text-amber-300">
              {alert.alert_type.replace(/_/g, ' ')}
            </span>
          </div>
        ))}
      </div>
    </section>
  );
}

// ---------------------------------------------------------------------------
// Section 4: Addon Health Groups
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Resource usage bar: green (usage<=request), yellow (request<usage<=limit), red (>limit), gray (remaining)
// ---------------------------------------------------------------------------


type GroupBy = 'addon' | 'cluster';

// Problem-tier-first ordering (walk-day lock): confirmed problems, then
// grace-window/new ones, then no-status, healthy groups last.
const TIER_ORDER: Record<AddonGroupProblemTier, number> = {
  confirmed: 0,
  grace: 1,
  unknown: 2,
  none: 3,
};

interface ClusterGroup {
  cluster_name: string;
  addons: Array<{
    addon_name: string;
    health: string;
    sync_status: string;
    reconciled_at?: string;
    resource_summary: { total_pods: number; running_pods: number; total_containers: number };
    app_name: string;
  }>;
  health_counts: Record<string, number>;
}

interface AddonGroupsSectionProps {
  groups: AddonGroupHealth[];
  /** Live addon state map (keyed `<addon>@<cluster>`), used to classify each
   * group's problem tier and compose its inline reason line. */
  addonStateMap: Map<string, AddonState>;
  /** Cluster connection problems and version-drift rows — neither has an
   * addon group to live inside, so they render as compact rows above the
   * list (walk-day finding: no more double-listing an addon problem as
   * both a flat row and a full-width group). */
  clusterAttentionRows: AttentionRow[];
  driftRows: AttentionRow[];
  /** Addon problems whose addon isn't present in `groups` at all (a
   * mismatch between the attention feed and the observability snapshot) —
   * shown as compact rows too, so a problem never silently disappears. */
  orphanAddonRows: AttentionRow[];
  /** The "Open issues (N)" line's total — see countOpenIssues doc comment
   * in AttentionSection.tsx. Already includes cluster connection problems,
   * so this component doesn't need the raw cluster count separately. */
  totalOpenIssues: number;
  /** S5 (scale-walk) — the dashboard's "not healthy" donut row deep-links
   * here with `?health=issues`. When true, the addon/cluster lists below
   * narrow to problem entries only, with a dismissible chip to clear it. */
  issuesOnly?: boolean;
  onClearIssuesOnly?: () => void;
}

function AddonGroupsSection({
  groups,
  addonStateMap,
  clusterAttentionRows,
  driftRows,
  orphanAddonRows,
  totalOpenIssues,
  issuesOnly = false,
  onClearIssuesOnly,
}: AddonGroupsSectionProps) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [sortMode, setSortMode] = useState<'issues' | 'alpha'>('issues');
  const [groupBy, setGroupBy] = useState<GroupBy>('addon');
  const [viewMode, setViewMode] = useState<'list' | 'grid'>('list');
  const [searchTerm, setSearchTerm] = useState('');
  const [pageSize, setPageSize] = useState<PageSize>(20);
  const [page, setPage] = useState(1);

  // Pivot data: group by cluster instead of addon
  const clusterGroups = useMemo((): ClusterGroup[] => {
    const map = new Map<string, ClusterGroup>();
    for (const group of groups ?? []) {
      for (const child of group.child_apps ?? []) {
        let cg = map.get(child.cluster_name);
        if (!cg) {
          cg = { cluster_name: child.cluster_name, addons: [], health_counts: {} };
          map.set(child.cluster_name, cg);
        }
        cg.addons.push({
          addon_name: group.addon_name,
          health: child.health,
          sync_status: child.sync_status,
          reconciled_at: child.reconciled_at,
          resource_summary: child.resource_summary,
          app_name: child.app_name,
        });
        const h = child.health || 'Unknown';
        cg.health_counts[h] = (cg.health_counts[h] ?? 0) + 1;
      }
    }
    return Array.from(map.values());
  }, [groups]);

  // Filter by search term
  const filteredAddonGroups = useMemo(() => {
    const copy = [...(groups ?? [])];
    if (!searchTerm) return copy;
    const term = searchTerm.toLowerCase();
    return copy.filter((g) => g.addon_name.toLowerCase().includes(term));
  }, [groups, searchTerm]);

  // Classify every group's problem tier once (walk-day lock: confirmed
  // problems first, then grace-window/new ones, then no-status, healthy
  // last — this is the primary sort key regardless of which sort mode
  // below is selected; "Most Issues"/"A-Z" only order groups WITHIN the
  // same tier, they never let a healthy group outrank a problem one).
  //
  // byAppName (live-bug fix): a fallback index of the feed's states keyed
  // by their real ArgoCD app name, alongside the primary addon@cluster map.
  // /dashboard/attention currently ships the full app name in its
  // addon_name field (internal/api/dashboard.go), so the primary key alone
  // can miss a child whose app name matches exactly — see isOrphan below
  // for the same divergence.
  const groupProblems = useMemo(() => {
    const byAppName = new Map<string, AddonState>();
    for (const s of addonStateMap.values()) {
      if (s.appName) byAppName.set(s.appName, s);
    }
    const map = new Map<string, ReturnType<typeof classifyAddonGroupProblem>>();
    for (const g of groups ?? []) {
      map.set(g.addon_name, classifyAddonGroupProblem(g, addonStateMap, byAppName));
    }
    return map;
  }, [groups, addonStateMap]);

  // Sort filtered groups — problem tier first (see groupProblems above),
  // then the selected secondary order within each tier.
  const sortedAddonGroups = useMemo(() => {
    const copy = [...filteredAddonGroups];
    copy.sort((a, b) => {
      const tierA = TIER_ORDER[groupProblems.get(a.addon_name)?.tier ?? 'none'];
      const tierB = TIER_ORDER[groupProblems.get(b.addon_name)?.tier ?? 'none'];
      if (tierA !== tierB) return tierA - tierB;
      if (sortMode === 'alpha') {
        return a.addon_name.localeCompare(b.addon_name);
      }
      const aIssues = Object.entries(a.health_counts ?? {}).filter(([k]) => k.toLowerCase() !== 'healthy').reduce((s, [, v]) => s + v, 0);
      const bIssues = Object.entries(b.health_counts ?? {}).filter(([k]) => k.toLowerCase() !== 'healthy').reduce((s, [, v]) => s + v, 0);
      return bIssues - aIssues;
    });
    return copy;
  }, [filteredAddonGroups, sortMode, groupProblems]);

  // Filter cluster groups by search term
  const filteredClusterGroups = useMemo(() => {
    const copy = [...clusterGroups];
    if (!searchTerm) return copy;
    const term = searchTerm.toLowerCase();
    return copy.filter((cg) => cg.cluster_name.toLowerCase().includes(term));
  }, [clusterGroups, searchTerm]);

  // Sort filtered cluster groups
  const sortedClusterGroups = useMemo(() => {
    const copy = [...filteredClusterGroups];
    if (sortMode === 'alpha') {
      copy.sort((a, b) => a.cluster_name.localeCompare(b.cluster_name));
    } else {
      // Most issues first
      copy.sort((a, b) => {
        const aIssues = a.addons.filter(x => x.health.toLowerCase() !== 'healthy').length;
        const bIssues = b.addons.filter(x => x.health.toLowerCase() !== 'healthy').length;
        return bIssues - aIssues;
      });
    }
    return copy;
  }, [filteredClusterGroups, sortMode]);

  // S5 — health=issues narrows to problem entries only, applied after sort
  // so relative order is unaffected. Addon view reuses the same tier
  // classification as the sort key above (groupProblems); cluster view
  // reuses the same non-healthy count already used for its own sorting.
  const visibleAddonGroups = useMemo(() => {
    if (!issuesOnly) return sortedAddonGroups;
    return sortedAddonGroups.filter((g) => (groupProblems.get(g.addon_name)?.tier ?? 'none') !== 'none');
  }, [sortedAddonGroups, issuesOnly, groupProblems]);

  const visibleClusterGroups = useMemo(() => {
    if (!issuesOnly) return sortedClusterGroups;
    return sortedClusterGroups.filter((cg) => cg.addons.some((a) => a.health.toLowerCase() !== 'healthy'));
  }, [sortedClusterGroups, issuesOnly]);

  const toggle = (name: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(name)) {
        next.delete(name);
      } else {
        next.add(name);
      }
      return next;
    });
  };

  // Reset expanded state when switching group mode
  const handleGroupByChange = (mode: GroupBy) => {
    setGroupBy(mode);
    setExpanded(new Set());
    setPage(1);
  };

  // Reset page when search/sort/pageSize/issues-filter changes
  useEffect(() => {
    setPage(1);
  }, [searchTerm, sortMode, pageSize, groupBy, issuesOnly]);

  // Paginate the sorted (and, when active, issues-only-filtered) groups
  const totalPages = Math.ceil(
    (groupBy === 'addon' ? visibleAddonGroups.length : visibleClusterGroups.length) / pageSize
  );
  const paginatedAddonGroups = useMemo(() => {
    const start = (page - 1) * pageSize;
    return visibleAddonGroups.slice(start, start + pageSize);
  }, [visibleAddonGroups, page, pageSize]);
  const paginatedClusterGroups = useMemo(() => {
    const start = (page - 1) * pageSize;
    return visibleClusterGroups.slice(start, start + pageSize);
  }, [visibleClusterGroups, page, pageSize]);

  const hasGroups = !!groups && groups.length > 0;
  const hasIssues = totalOpenIssues > 0;

  if (!hasGroups && !hasIssues) {
    return null;
  }

  return (
    // id="addon-health" — the Dashboard's Applications card (and its thin
    // attention line) deep-link here (walk finding #2: "which apps are
    // unhealthy" is answered by this section, which already sorts problem
    // groups first and lists per-app-per-cluster health, not just a
    // per-addon aggregate). Walk-day lock (maintainer's three findings on
    // the old "Fleet Health" section): "fleet" doesn't appear on screen —
    // this section is just "Health"; the per-addon groups below are the
    // ONLY place addon problems render (no separate expandable issue list,
    // no chips); a problem group's header carries its reason inline.
    <section id="addon-health">
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <h2 className="flex items-center gap-2 text-lg font-semibold text-[#0a2a4a] dark:text-gray-100">
          <Server className="h-5 w-5 text-teal-500" />
          Health
        </h2>
        <div className="flex flex-wrap items-center gap-3">
          {/* Search */}
          <div className="relative">
            <Search className="absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-[#3a6a8a]" />
            <input
              type="text"
              placeholder={groupBy === 'addon' ? 'Search addons...' : 'Search clusters...'}
              value={searchTerm}
              onChange={(e) => setSearchTerm(e.target.value)}
              className="w-48 rounded-md ring-2 ring-[#6aade0] bg-[#f0f7ff] py-1 pl-8 pr-8 text-sm text-[#0a3a5a] placeholder:text-[#5a8aaa] focus:outline-none focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300"
            />
            {searchTerm && (
              <button
                type="button"
                onClick={() => setSearchTerm('')}
                className="absolute right-2 top-1/2 -translate-y-1/2 text-[#3a6a8a] hover:text-[#0a2a4a]"
              >
                <X className="h-4 w-4" />
              </button>
            )}
          </div>
          {/* View mode toggle */}
          <div className="flex gap-1 rounded-md border border-[#5a9dd0] dark:border-gray-600">
            <button
              type="button"
              onClick={() => setViewMode('list')}
              className={`rounded-l-md p-1.5 transition-colors ${
                viewMode === 'list'
                  ? 'bg-teal-600 text-white'
                  : 'text-[#2a5a7a] hover:bg-[#d6eeff] dark:text-gray-400 dark:hover:bg-gray-800'
              }`}
              aria-label="List view"
            >
              <LayoutList className="h-4 w-4" />
            </button>
            <button
              type="button"
              onClick={() => setViewMode('grid')}
              className={`rounded-r-md p-1.5 transition-colors ${
                viewMode === 'grid'
                  ? 'bg-teal-600 text-white'
                  : 'text-[#2a5a7a] hover:bg-[#d6eeff] dark:text-gray-400 dark:hover:bg-gray-800'
              }`}
              aria-label="Grid view"
            >
              <LayoutGrid className="h-4 w-4" />
            </button>
          </div>
          {/* Group by toggle */}
          <div className="flex rounded-md border border-[#5a9dd0] dark:border-gray-600">
            {(['addon', 'cluster'] as const).map((m) => (
              <button
                key={m}
                onClick={() => handleGroupByChange(m)}
                className={`px-2.5 py-1 text-xs font-medium transition-colors ${
                  groupBy === m
                    ? 'bg-teal-600 text-white'
                    : 'text-[#2a5a7a] hover:bg-[#d6eeff] dark:text-gray-400 dark:hover:bg-gray-800'
                } ${m === 'addon' ? 'rounded-l-md' : 'rounded-r-md'}`}
              >
                By {m === 'addon' ? 'Addon' : 'Cluster'}
              </button>
            ))}
          </div>
          {/* Sort toggle */}
          <div className="flex gap-1">
            {(['issues', 'alpha'] as const).map((m) => (
              <button
                key={m}
                onClick={() => setSortMode(m)}
                className={`rounded-md px-2.5 py-1 text-xs font-medium transition-colors ${
                  sortMode === m
                    ? 'bg-teal-100 text-teal-800 dark:bg-teal-900/40 dark:text-teal-300'
                    : 'text-[#2a5a7a] hover:bg-[#d6eeff] dark:text-gray-400 dark:hover:bg-gray-800'
                }`}
              >
                {m === 'issues' ? 'Most Issues' : 'A-Z'}
              </button>
            ))}
          </div>
          {/* Page size selector */}
          <PageSizeSelector pageSize={pageSize} onChange={setPageSize} />
        </div>
      </div>

      {/* Open issues — a plain one-line count, always the first thing in
          this section (walk-day lock: no chips, no separate expandable
          list — the per-addon groups below already show every problem).
          The one exception (S5, scale-walk): the dashboard's "not healthy"
          donut row deep-links here with ?health=issues — when that's
          active, a dismissible chip says so and clears back to the full
          list, same pattern as the version matrix's filter chips. */}
      <div className="mb-4 flex flex-wrap items-center gap-2">
        {hasIssues ? (
          <h3 className="text-sm font-semibold text-amber-700 dark:text-amber-400">
            Open issues ({totalOpenIssues})
          </h3>
        ) : (
          <p className="text-sm text-[#2a5a7a] dark:text-gray-400">No open issues.</p>
        )}
        {issuesOnly && (
          <span
            data-testid="health-issues-chip"
            className="inline-flex items-center gap-1.5 rounded-full bg-amber-50 px-3 py-1 text-xs font-medium text-amber-700 ring-1 ring-amber-200 dark:bg-amber-900/30 dark:text-amber-400 dark:ring-amber-800"
          >
            problem apps only
            <button
              type="button"
              onClick={onClearIssuesOnly}
              aria-label="Clear the problem-apps filter"
              className="rounded-full hover:text-amber-900 dark:hover:text-amber-200"
            >
              <X className="h-3 w-3" />
            </button>
          </span>
        )}
      </div>

      {/* Cluster connection problems and version drift have no addon group
          to live inside, so they render as compact rows here — modest
          height, not full-width one-item containers. Orphan addon rows
          (an addon the attention feed flags that isn't in the groups below
          at all) render here too, so a problem never silently disappears. */}
      {(clusterAttentionRows.length > 0 || driftRows.length > 0 || orphanAddonRows.length > 0) && (
        <div className="mb-6 space-y-1.5">
          {clusterAttentionRows.map((row) => (
            <AttentionRowView key={row.key} row={row} />
          ))}
          {driftRows.map((row) => (
            <AttentionRowView key={row.key} row={row} />
          ))}
          {orphanAddonRows.map((row) => (
            <AttentionRowView key={row.key} row={row} />
          ))}
        </div>
      )}

      {!hasGroups ? null : (
      <div className={viewMode === 'grid' ? 'grid grid-cols-1 gap-3 md:grid-cols-2 lg:grid-cols-3' : 'space-y-3'}>
        {groupBy === 'addon' ? (
          /* ---- Group by Addon ---- */
          paginatedAddonGroups.map((group) => {
            const isExpanded = expanded.has(group.addon_name);
            const healthEntries = Object.entries(group.health_counts);
            const total = group.total_apps;
            // Problem-group header (walk-day lock): one addon group's
            // header carries its reason inline, in plain words, composed
            // from the live addon-state map — no separate flat row for
            // this same problem exists anywhere else on the page.
            const problem = groupProblems.get(group.addon_name);
            const tier = problem?.tier ?? 'none';
            const description =
              problem?.state && tier !== 'none'
                ? describeAddonProblem(problem.state, tier as Exclude<AddonGroupProblemTier, 'none'>)
                : null;

            return (
              <div
                key={group.addon_name}
                className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] shadow-sm transition-shadow hover:shadow-md dark:ring-gray-700 dark:bg-gray-900"
              >
                <button
                  onClick={() => toggle(group.addon_name)}
                  className="flex w-full items-center gap-4 p-4 text-left"
                  aria-expanded={isExpanded}
                >
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-3">
                      {tier !== 'none' && (
                        <span
                          className={`h-2.5 w-2.5 shrink-0 rounded-full ${tier === 'confirmed' ? 'bg-red-500' : 'bg-amber-500'}`}
                          aria-hidden="true"
                        />
                      )}
                      <span className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
                        {group.addon_name}
                      </span>
                      <span className="rounded-full bg-[#d6eeff] px-2 py-0.5 text-xs text-[#2a5a7a] dark:bg-gray-800 dark:text-gray-400">
                        {total} Application{total !== 1 ? 's' : ''}
                      </span>
                    </div>
                    <div className="mt-2 flex h-2 overflow-hidden rounded-full">
                      {healthEntries.map(([status, count]) => (
                        <div
                          key={status}
                          style={{
                            width: total > 0 ? `${(count / total) * 100}%` : '0%',
                            backgroundColor: HEALTH_COLORS[status] ?? '#9ca3af',
                          }}
                          title={`${status}: ${count}`}
                        />
                      ))}
                    </div>
                    <div className="mt-1.5 flex flex-wrap gap-3">
                      {healthEntries.map(([status, count]) => (
                        <span
                          key={status}
                          className="flex items-center gap-1 text-sm text-[#2a5a7a] dark:text-gray-400"
                        >
                          <span
                            className="inline-block h-2 w-2 rounded-full"
                            style={{ backgroundColor: HEALTH_COLORS[status] ?? '#9ca3af' }}
                          />
                          {status}: {count}
                        </span>
                      ))}
                    </div>
                  </div>
                  {isExpanded ? (
                    <ChevronUp className="h-4 w-4 shrink-0 text-[#3a6a8a]" />
                  ) : (
                    <ChevronDown className="h-4 w-4 shrink-0 text-[#3a6a8a]" />
                  )}
                </button>

                {/* Reason line — a sibling of the toggle button (not nested
                    inside it) so the deep link below is valid markup and
                    still lands exactly where the old flat row's link did. */}
                {description && problem?.state && (
                  <div className="-mt-2 px-4 pb-3">
                    <p
                      className={`text-sm ${tier === 'confirmed' ? 'text-red-700 dark:text-red-400' : 'text-amber-700 dark:text-amber-400'}`}
                      title={description.tooltip}
                    >
                      <Link
                        to={deepLinkToAddonOnCluster(group.addon_name, problem.state.cluster)}
                        className="hover:underline"
                      >
                        {description.text}
                      </Link>
                      {problem.extraCount > 0 && (
                        <span className="text-[#3a6a8a] dark:text-gray-500">
                          {' '}(+{problem.extraCount} more)
                        </span>
                      )}
                    </p>
                  </div>
                )}

                {isExpanded && group.child_apps && group.child_apps.length > 0 && (
                  <div className="border-t border-[#6aade0] px-4 pb-4 dark:border-gray-700">
                    <table className="mt-3 w-full text-xs">
                      <thead>
                        <tr className="text-left text-[#2a5a7a] dark:text-gray-400">
                          <th className="pb-2 font-medium">Cluster</th>
                          <th className="pb-2 font-medium">Health</th>
                          <th className="pb-2 font-medium">Sync</th>
                          <th className="pb-2 font-medium">Resources</th>
                          <th className="pb-2 font-medium">Last Synced</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-[#d6eeff] dark:divide-gray-800">
                        {group.child_apps.map((child) => {
                          return (
                            <tr
                              key={child.app_name}
                              className="hover:bg-[#d6eeff] dark:hover:bg-gray-800"
                            >
                              <td className="py-2 pr-3 font-medium text-[#0a3a5a] dark:text-gray-200">
                                {child.cluster_name}
                              </td>
                              <td className="py-2 pr-3">
                                <span className={`inline-block rounded-full px-2 py-0.5 text-xs font-semibold uppercase ${healthBadgeCls(child.health)}`}>
                                  {child.health || 'Unknown'}
                                </span>
                              </td>
                              <td className="py-2 pr-3">
                                <span className={`inline-block rounded-full px-2 py-0.5 text-xs font-semibold uppercase ${syncBadgeCls(child.sync_status)}`}>
                                  {child.sync_status || 'Unknown'}
                                </span>
                              </td>
                              <td className="py-2 pr-3 text-[#2a5a7a] dark:text-gray-400">
                                {child.resource_summary?.total_pods > 0 && (
                                  <span>{child.resource_summary.running_pods}/{child.resource_summary.total_pods} pods</span>
                                )}
                                {(child.resource_summary?.total_pods ?? 0) === 0 && (child.resource_summary?.total_containers ?? 0) > 0 && (
                                  <span>{child.resource_summary.total_containers} workloads</span>
                                )}
                                {(child.resource_summary?.total_pods ?? 0) === 0 && (child.resource_summary?.total_containers ?? 0) === 0 && (
                                  <span className="text-[#3a6a8a]">--</span>
                                )}
                              </td>
                              <td className="py-2 text-[#3a6a8a]">
                                {child.reconciled_at ? timeAgo(child.reconciled_at) : '--'}
                              </td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>
                )}
              </div>
            );
          })
        ) : (
          /* ---- Group by Cluster ---- */
          paginatedClusterGroups.length === 0 ? (
            <div className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-8 text-center text-sm text-[#2a5a7a] dark:ring-gray-700 dark:bg-gray-800 dark:text-gray-400">
              {searchTerm ? `No clusters matching "${searchTerm}"` : 'No cluster data available.'}
            </div>
          ) : (
            paginatedClusterGroups.map((cg) => {
              const isExpanded = expanded.has(cg.cluster_name);
              const healthEntries = Object.entries(cg.health_counts);
              const total = cg.addons.length;

              return (
                <div
                  key={cg.cluster_name}
                  className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] shadow-sm transition-shadow hover:shadow-md dark:ring-gray-700 dark:bg-gray-900"
                >
                  <button
                    onClick={() => toggle(cg.cluster_name)}
                    className="flex w-full items-center gap-4 p-4 text-left"
                    aria-expanded={isExpanded}
                  >
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-3">
                        <span className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
                          {cg.cluster_name}
                        </span>
                        <span className="rounded-full bg-[#d6eeff] px-2 py-0.5 text-xs text-[#2a5a7a] dark:bg-gray-800 dark:text-gray-400">
                          {total} addon{total !== 1 ? 's' : ''}
                        </span>
                      </div>
                      <div className="mt-2 flex h-2 overflow-hidden rounded-full">
                        {healthEntries.map(([status, count]) => (
                          <div
                            key={status}
                            style={{
                              width: total > 0 ? `${(count / total) * 100}%` : '0%',
                              backgroundColor: HEALTH_COLORS[status] ?? '#9ca3af',
                            }}
                            title={`${status}: ${count}`}
                          />
                        ))}
                      </div>
                      <div className="mt-1.5 flex flex-wrap gap-3">
                        {healthEntries.map(([status, count]) => (
                          <span
                            key={status}
                            className="flex items-center gap-1 text-sm text-[#2a5a7a] dark:text-gray-400"
                          >
                            <span
                              className="inline-block h-2 w-2 rounded-full"
                              style={{ backgroundColor: HEALTH_COLORS[status] ?? '#9ca3af' }}
                            />
                            {status}: {count}
                          </span>
                        ))}
                      </div>
                    </div>
                    {isExpanded ? (
                      <ChevronUp className="h-4 w-4 shrink-0 text-[#3a6a8a]" />
                    ) : (
                      <ChevronDown className="h-4 w-4 shrink-0 text-[#3a6a8a]" />
                    )}
                  </button>

                  {isExpanded && (
                    <div className="border-t border-[#6aade0] px-4 pb-4 dark:border-gray-700">
                      <table className="mt-3 w-full text-xs">
                        <thead>
                          <tr className="text-left text-[#2a5a7a] dark:text-gray-400">
                            <th className="pb-2 font-medium">Addon</th>
                            <th className="pb-2 font-medium">Health</th>
                            <th className="pb-2 font-medium">Sync</th>
                            <th className="pb-2 font-medium">Resources</th>
                            <th className="pb-2 font-medium">Last Synced</th>
                          </tr>
                        </thead>
                        <tbody className="divide-y divide-[#d6eeff] dark:divide-gray-800">
                          {cg.addons.map((addon) => {
                            return (
                              <tr
                                key={addon.app_name}
                                className="hover:bg-[#d6eeff] dark:hover:bg-gray-800"
                              >
                                <td className="py-2 pr-3 font-medium text-[#0a3a5a] dark:text-gray-200">
                                  {addon.addon_name}
                                </td>
                                <td className="py-2 pr-3">
                                  <span className={`inline-block rounded-full px-2 py-0.5 text-xs font-semibold uppercase ${healthBadgeCls(addon.health)}`}>
                                    {addon.health || 'Unknown'}
                                  </span>
                                </td>
                                <td className="py-2 pr-3">
                                  <span className={`inline-block rounded-full px-2 py-0.5 text-xs font-semibold uppercase ${syncBadgeCls(addon.sync_status)}`}>
                                    {addon.sync_status || 'Unknown'}
                                  </span>
                                </td>
                                <td className="py-2 pr-3 text-[#2a5a7a] dark:text-gray-400">
                                  {addon.resource_summary?.total_pods > 0 && (
                                    <span>{addon.resource_summary.running_pods}/{addon.resource_summary.total_pods} pods</span>
                                  )}
                                  {(addon.resource_summary?.total_pods ?? 0) === 0 && (addon.resource_summary?.total_containers ?? 0) > 0 && (
                                    <span>{addon.resource_summary.total_containers} workloads</span>
                                  )}
                                  {(addon.resource_summary?.total_pods ?? 0) === 0 && (addon.resource_summary?.total_containers ?? 0) === 0 && (
                                    <span className="text-[#3a6a8a]">--</span>
                                  )}
                                </td>
                                <td className="py-2 text-[#3a6a8a]">
                                  {addon.reconciled_at ? timeAgo(addon.reconciled_at) : '--'}
                                </td>
                              </tr>
                            );
                          })}
                        </tbody>
                      </table>
                    </div>
                  )}
                </div>
              );
            })
          )
        )}
      </div>
      )}

      {/* Pagination controls */}
      {hasGroups && totalPages > 1 && (
        <div className="mt-4 flex flex-wrap items-center justify-between gap-4 rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-3 dark:ring-gray-700 dark:bg-gray-900">
          <div className="text-sm text-[#2a5a7a] dark:text-gray-400">
            Showing{' '}
            {(page - 1) * pageSize + 1}-
            {Math.min(
              page * pageSize,
              groupBy === 'addon' ? sortedAddonGroups.length : sortedClusterGroups.length
            )}{' '}
            of{' '}
            {groupBy === 'addon' ? sortedAddonGroups.length : sortedClusterGroups.length}
          </div>
          <PaginationControls page={page} totalPages={totalPages} onPageChange={setPage} />
        </div>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------------
// Section 5: Sync Activity Timeline
// ---------------------------------------------------------------------------

function SyncActivitySection({
  syncs,
}: {
  syncs: SyncActivityEntry[];
}) {
  const [addonFilter, setAddonFilter] = useState('');
  const [clusterFilter, setClusterFilter] = useState('');

  const addonNames = useMemo(
    () => [...new Set((syncs ?? []).map((s) => s.addon_name))].sort(),
    [syncs],
  );
  const clusterNames = useMemo(
    () => [...new Set((syncs ?? []).map((s) => s.cluster_name))].sort(),
    [syncs],
  );

  const filtered = useMemo(
    () =>
      (syncs ?? []).filter(
        (s) =>
          (!addonFilter || s.addon_name === addonFilter) &&
          (!clusterFilter || s.cluster_name === clusterFilter),
      ),
    [syncs, addonFilter, clusterFilter],
  );

  // Bar chart: syncs per hour over the last 24h
  const hourlyData = useMemo(() => {
    const now = Date.now();
    const buckets: Record<number, number> = {};
    for (let i = 0; i < 24; i++) buckets[i] = 0;
    for (const s of syncs ?? []) {
      const hoursAgo = Math.floor((now - new Date(s.timestamp).getTime()) / 3600000);
      if (hoursAgo >= 0 && hoursAgo < 24) {
        buckets[hoursAgo]++;
      }
    }
    return Array.from({ length: 24 }, (_, i) => ({
      label: i === 0 ? 'now' : `${i}h`,
      count: buckets[i],
    })).reverse();
  }, [syncs]);

  return (
    <section className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-6 shadow-sm dark:ring-gray-700 dark:bg-gray-900">
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <h2 className="flex items-center gap-2 text-lg font-semibold text-[#0a2a4a] dark:text-gray-100">
          <Activity className="h-5 w-5 text-teal-500" />
          Sync Activity
        </h2>
        <div className="flex gap-2">
          <select
            value={addonFilter}
            onChange={(e) => setAddonFilter(e.target.value)}
            className="rounded-md ring-2 ring-[#6aade0] bg-[#f0f7ff] px-2 py-1 text-xs text-[#0a3a5a] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300"
            aria-label="Filter by addon"
          >
            <option value="">All Addons</option>
            {addonNames.map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
          <select
            value={clusterFilter}
            onChange={(e) => setClusterFilter(e.target.value)}
            className="rounded-md ring-2 ring-[#6aade0] bg-[#f0f7ff] px-2 py-1 text-xs text-[#0a3a5a] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300"
            aria-label="Filter by cluster"
          >
            <option value="">All Clusters</option>
            {clusterNames.map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        </div>
      </div>

      {/* Sync frequency chart */}
      {(syncs ?? []).length > 0 && (
        <div className="mb-5 h-32" style={{ minWidth: 0, minHeight: 0 }}>
          <ResponsiveContainer width="100%" height={128} minWidth={100}>
            <BarChart data={hourlyData}>
              <CartesianGrid strokeDasharray="3 3" stroke="#374151" opacity={0.3} />
              <XAxis dataKey="label" tick={{ fontSize: 10 }} interval={3} />
              <YAxis allowDecimals={false} tick={{ fontSize: 10 }} width={30} />
              <Tooltip />
              <Bar dataKey="count" fill="#06b6d4" radius={[2, 2, 0, 0]} />
            </BarChart>
          </ResponsiveContainer>
        </div>
      )}

      {/* Timeline feed */}
      <div className="max-h-80 space-y-1 overflow-y-auto">
        {filtered.length === 0 && (
          <p className="py-4 text-center text-sm text-[#2a5a7a]">
            No sync activity found.
          </p>
        )}
        {filtered.map((s, idx) => (
          <div
            key={`${s.timestamp}-${s.app_name}-${idx}`}
            className="flex items-center gap-3 rounded-lg px-3 py-2 transition-colors hover:bg-[#d6eeff] dark:hover:bg-gray-800"
          >
            {statusIcon(s.status)}
            <span className="w-16 shrink-0 text-sm text-[#3a6a8a]">
              {timeAgo(s.timestamp)}
            </span>
            <span className="min-w-0 flex-1 truncate text-sm font-medium text-[#0a2a4a] dark:text-gray-100">
              {s.addon_name}
            </span>
            <span className="hidden truncate text-xs text-[#2a5a7a] sm:inline dark:text-gray-400">
              {s.cluster_name}
            </span>
            <span className="flex items-center gap-1 text-sm text-[#3a6a8a]">
              <Clock className="h-3 w-3" />
              {s.duration}
            </span>
          </div>
        ))}
      </div>
    </section>
  );
}

// ---------------------------------------------------------------------------
// Section 6: Deployment Frequency (stacked bar: Succeeded/Failed over time)
// ---------------------------------------------------------------------------

function DeploymentFrequencySection({
  syncs,
}: {
  syncs: SyncActivityEntry[];
}) {
  const frequencyData = useMemo(() => {
    if (!syncs || syncs.length === 0) return [];

    const now = Date.now();
    const buckets: Record<number, { succeeded: number; failed: number }> = {};
    for (let i = 0; i < 24; i++) buckets[i] = { succeeded: 0, failed: 0 };

    for (const s of syncs) {
      const hoursAgo = Math.floor((now - new Date(s.timestamp).getTime()) / 3600000);
      if (hoursAgo >= 0 && hoursAgo < 24) {
        const status = (s.status ?? '').toLowerCase();
        if (status === 'succeeded') {
          buckets[hoursAgo].succeeded++;
        } else if (status === 'failed') {
          buckets[hoursAgo].failed++;
        }
      }
    }

    return Array.from({ length: 24 }, (_, i) => ({
      label: i === 0 ? 'now' : `${i}h`,
      succeeded: buckets[i].succeeded,
      failed: buckets[i].failed,
    })).reverse();
  }, [syncs]);

  const hasSyncs = syncs && syncs.length > 0;

  return (
    <section className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:ring-gray-700 dark:bg-gray-800">
      <h3 className="mb-3 text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
        Deployment frequency
      </h3>
      {!hasSyncs ? (
        <p className="py-8 text-center text-sm text-[#2a5a7a] dark:text-gray-400">
          No sync activity yet
        </p>
      ) : (
        <ResponsiveContainer width="100%" height={200}>
          <BarChart data={frequencyData}>
            <CartesianGrid strokeDasharray="3 3" stroke="#374151" opacity={0.3} />
            <XAxis dataKey="label" tick={{ fontSize: 12 }} interval={3} />
            <YAxis allowDecimals={false} tick={{ fontSize: 12 }} width={35} />
            <Tooltip
              contentStyle={{
                backgroundColor: '#f0f7ff',
                border: '2px solid #6aade0',
                borderRadius: '8px',
                fontSize: '14px',
              }}
            />
            <Legend wrapperStyle={{ fontSize: '14px' }} iconType="square" />
            <Bar dataKey="succeeded" stackId="a" fill="#22c55e" name="Succeeded" radius={[0, 0, 0, 0]} />
            <Bar dataKey="failed" stackId="a" fill="#ef4444" name="Failed" radius={[2, 2, 0, 0]} />
          </BarChart>
        </ResponsiveContainer>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------------
// Section 7: Sync Duration (line chart over time)
// ---------------------------------------------------------------------------

function SyncDurationSection({
  syncs,
}: {
  syncs: SyncActivityEntry[];
}) {
  const durationData = useMemo(() => {
    if (!syncs || syncs.length === 0) return [];

    // Sort by timestamp ascending (oldest first) for a proper timeline
    const sorted = [...syncs].sort(
      (a, b) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime()
    );

    return sorted.map((s, idx) => ({
      index: idx + 1,
      duration_secs: s.duration_secs,
      duration: s.duration,
      timestamp: s.timestamp,
    }));
  }, [syncs]);

  const hasSyncs = syncs && syncs.length > 0;

  return (
    <section className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:ring-gray-700 dark:bg-gray-800">
      <h3 className="mb-3 text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
        Sync duration
      </h3>
      {!hasSyncs ? (
        <p className="py-8 text-center text-sm text-[#2a5a7a] dark:text-gray-400">
          No sync activity yet
        </p>
      ) : (
        <ResponsiveContainer width="100%" height={200}>
          <LineChart data={durationData}>
            <CartesianGrid strokeDasharray="3 3" stroke="#374151" opacity={0.3} />
            <XAxis
              dataKey="index"
              tick={{ fontSize: 12 }}
              label={{ value: 'Sync events (ordered by time)', position: 'insideBottom', offset: -5, fontSize: 12 }}
            />
            <YAxis
              tick={{ fontSize: 12 }}
              width={45}
              label={{ value: 'seconds', angle: -90, position: 'insideLeft', fontSize: 12 }}
            />
            <Tooltip
              contentStyle={{
                backgroundColor: '#f0f7ff',
                border: '2px solid #6aade0',
                borderRadius: '8px',
                fontSize: '14px',
              }}
              labelFormatter={(value) => `Sync #${value}`}
              formatter={(_val, _name, item) => {
                const dur = item?.payload?.duration;
                return [dur ?? `${item?.value ?? ''}s`, 'Duration'];
              }}
            />
            <Line
              type="monotone"
              dataKey="duration_secs"
              stroke="#06b6d4"
              strokeWidth={2}
              dot={{ r: 3 }}
              activeDot={{ r: 5 }}
            />
          </LineChart>
        </ResponsiveContainer>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------------
// Sharko Engine health color helper — used inline in ControlPlaneSection
// ---------------------------------------------------------------------------

function bootstrapHealthColor(health: string): { dot: string; badge: string } {
  const h = (health ?? '').toLowerCase();
  if (h === 'healthy')
    return {
      dot: 'bg-green-500',
      badge: 'bg-green-100 text-green-800 dark:bg-green-900/40 dark:text-green-300',
    };
  if (h === 'degraded')
    return {
      dot: 'bg-amber-500',
      badge: 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-300',
    };
  if (h === 'missing' || h === 'error')
    return {
      dot: 'bg-red-500',
      badge: 'bg-red-100 text-red-800 dark:bg-red-900/40 dark:text-red-300',
    };
  return {
    dot: 'bg-[#9ca3af]',
    badge: 'bg-[#d6eeff] text-[#1a4a6a] dark:bg-gray-800 dark:text-gray-400',
  };
}

// ---------------------------------------------------------------------------
// Main View
// ---------------------------------------------------------------------------

export function Observability() {
  const [searchParams, setSearchParams] = useSearchParams();
  // S5 (scale-walk) — the dashboard's addon-applications donut deep-links
  // its "not healthy" row here as ?health=issues (kept before the #hash so
  // it actually lands in location.search — a hash-then-query URL would put
  // it inside the fragment instead, invisible to useSearchParams). Narrows
  // the Health section below to problem apps/clusters only.
  const [issuesOnly, setIssuesOnly] = useState(searchParams.get('health') === 'issues');
  const clearIssuesOnly = useCallback(() => {
    setIssuesOnly(false);
    const params = new URLSearchParams(searchParams.toString());
    params.delete('health');
    setSearchParams(params, { replace: true });
  }, [searchParams, setSearchParams]);
  const [data, setData] = useState<ObservabilityOverviewResponse | null>(null);
  const [stats, setStats] = useState<DashboardStats | null>(null);
  const [argoCDUrl, setArgoCDUrl] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // Attention-detail rows (WQ-3, attention-move-badges) — moved here
  // verbatim from the Dashboard. Cluster connectivity problems and version
  // drift need their own reads (the aggregate /observability/overview and
  // /dashboard/stats calls above don't carry per-cluster names or drift
  // detail); addon problem/settling/unknown rows come from the app-wide
  // AddonStatesProvider (useAddonStates below) — no extra fetch for those.
  const [attentionClusters, setAttentionClusters] = useState<{ name: string; connectionStatus: string }[]>([]);
  const [versionDrifts, setVersionDrifts] = useState<{ addon: string; count: number }[]>([]);
  const { byApp: addonStateMap } = useAddonStates();

  const fetchData = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const [result, dashStats, connections, clustersData, matrixData] = await Promise.all([
        api.getObservability(),
        api.getDashboardStats().catch(() => null),
        api.getConnections().catch(() => null),
        api.getClusters().catch(() => null),
        api.getVersionMatrix().catch(() => null),
      ]);
      setData(result);
      setStats(dashStats);
      const activeConn = (connections?.connections ?? []).find(
        (c: { name: string; is_active?: boolean; argocd_server_url?: string }) => c.is_active,
      );
      setArgoCDUrl(activeConn?.argocd_server_url ?? null);

      // Cluster attention rows — failed/missing only, same filtering the
      // Dashboard used to do. A cluster with an open (not yet merged)
      // registration PR is excluded — it's mid-lifecycle, not broken.
      if (clustersData?.clusters) {
        const pendingNames = new Set(
          (clustersData.pending_registrations ?? []).map((p) => p.cluster_name),
        );
        setAttentionClusters(
          clustersData.clusters
            .filter((c) => !pendingNames.has(c.name) && isClusterNeedsAttention(c.connection_status))
            .map((c) => ({ name: c.name, connectionStatus: c.connection_status || 'Unknown' })),
        );
      } else {
        setAttentionClusters([]);
      }

      // Version drifts — same-addon-different-version across clusters.
      if (matrixData?.addons) {
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
        setVersionDrifts(drifts);
      } else {
        setVersionDrifts([]);
      }
    } catch (e: unknown) {
      setError(
        e instanceof Error ? e.message : 'Failed to load observability data',
      );
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetchData();
  }, [fetchData]);

  // Dashboard deep-link (walk finding #2): the Applications card navigates
  // to /observability#addon-health. A client-side route change doesn't
  // auto-scroll to a hash the way a full page load does, so once the
  // section has real content to scroll to, do it ourselves.
  useEffect(() => {
    if (loading || !data) return;
    if (window.location.hash !== '#addon-health') return;
    const el = document.getElementById('addon-health');
    el?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }, [loading, data]);

  if (loading) return <LoadingState message="Loading observability data..." />;
  if (error) return <ErrorState message={error} onRetry={fetchData} />;
  if (!data) return null;

  // Open-issues rows (walk day 3 lock, formerly WQ-3's standalone
  // "Needs Attention" block) — same severity honesty, same settling window,
  // same deep links, now folded into the Health section (AddonGroupsSection)
  // instead of rendered as its own surface. Cluster problem count reads
  // stats.clusters (the server's single classification, same source the
  // old Dashboard banner and the nav badge both use) rather than
  // re-deriving it from the named row list above.
  const { confirmed, settling, unknown } = splitAddonStates(addonStateMap);
  const clusterAttentionRows = buildClusterAttentionRows(attentionClusters);
  const driftRows = buildDriftRows(versionDrifts);
  const clusterProblemCount = (stats?.clusters?.failed ?? 0) + (stats?.clusters?.missing ?? 0);

  // Addon problems whose addon appears inside data.addon_groups render
  // ONLY as that group's inline reason line (walk-day lock: one surface,
  // no double-listing). Anything left over — an addon@cluster the
  // attention feed flags that the observability snapshot doesn't have a
  // matching group+child for — still needs to be seen, so it renders as a
  // compact row above the list instead of silently disappearing.
  //
  // Live-bug fix (maintainer's finding): /dashboard/attention currently
  // ships the full ArgoCD app name (e.g. "metrics-server-spoke-eu") in its
  // addon_name field instead of the catalog addon name (internal/api/
  // dashboard.go sets `addonName := app.Name` directly — a Go-side fix is
  // tracked separately; this lane only fixes the UI-side matching). That
  // mismatch made a genuine group member fail the addon@cluster check and
  // fall through to this fallback row, doubling it up with the group's own
  // inline reason line. Match on three signals so a real group member never
  // falls through:
  //   1. addon@cluster — the intended primary key.
  //   2. the feed item's real app name equals a child's app_name directly —
  //      catches the case above, since the app name itself still lines up
  //      with the group's child even when addon_name doesn't.
  //   3. an empty-cluster feed item whose addon name matches a group at
  //      all — an item with nowhere to report its cluster shouldn't
  //      auto-orphan just because cluster is blank.
  const groupChildKeys = new Set<string>();
  const groupChildAppNames = new Set<string>();
  const groupAddonNames = new Set<string>();
  for (const g of data.addon_groups ?? []) {
    groupAddonNames.add(g.addon_name);
    for (const c of g.child_apps ?? []) {
      groupChildKeys.add(`${g.addon_name}@${c.cluster_name}`);
      if (c.app_name) groupChildAppNames.add(c.app_name);
    }
  }
  const isOrphan = (s: { addonName: string; cluster: string; appName?: string }) => {
    if (groupChildKeys.has(`${s.addonName}@${s.cluster}`)) return false;
    if (s.appName && groupChildAppNames.has(s.appName)) return false;
    if (!s.cluster && groupAddonNames.has(s.addonName)) return false;
    return true;
  };
  const orphanAddonRows = [
    ...buildConfirmedAddonRows(confirmed.filter(isOrphan)),
    ...buildSettlingAddonRows(settling.filter(isOrphan)),
    ...buildUnknownAddonRows(unknown.filter(isOrphan)),
  ];

  // The "Open issues (N)" line's total — every problem kind the section
  // can show, not just the confirmed-only count the Dashboard's thin line
  // and nav badges read (see countOpenIssues doc comment).
  const totalOpenIssues = countOpenIssues({
    clusterAttentionRows,
    confirmedAddonRows: buildConfirmedAddonRows(confirmed),
    settlingAddonRows: buildSettlingAddonRows(settling),
    unknownAddonRows: buildUnknownAddonRows(unknown),
    driftRows,
    clusterProblemCount,
  });

  // Check if there's any meaningful data to display
  const hasControlPlane = data.control_plane && (data.control_plane.total_apps > 0 || data.control_plane.total_clusters > 0);
  const hasAlerts = (data.resource_alerts ?? []).length > 0;
  const hasAddonGroups = (data.addon_groups ?? []).length > 0;
  const hasSyncs = (data.recent_syncs ?? []).length > 0;
  const hasAnyData = hasControlPlane || hasAlerts || hasAddonGroups || hasSyncs;

  if (!hasAnyData) {
    return (
      <div className="space-y-6">
        <div>
          <h1 className="text-2xl font-bold text-[#0a2a4a] dark:text-gray-100">Observability</h1>
          <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-400">
            ArgoCD control plane health, addon health per cluster, resource alerts, and sync activity timeline.
          </p>
        </div>
        <AddonGroupsSection
          groups={data.addon_groups ?? []}
          addonStateMap={addonStateMap}
          clusterAttentionRows={clusterAttentionRows}
          driftRows={driftRows}
          orphanAddonRows={orphanAddonRows}
          totalOpenIssues={totalOpenIssues}
          issuesOnly={issuesOnly}
          onClearIssuesOnly={clearIssuesOnly}
        />
        <ControlPlaneSection data={data.control_plane} stats={stats} argoCDUrl={argoCDUrl} />
        <EmptyState
          title="No observability data available yet"
          description="Deploy addons to clusters to see sync status, health metrics, and version information."
        />
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold text-[#0a2a4a] dark:text-gray-100">Observability</h1>
        <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-400">
          ArgoCD control plane health, addon health per cluster, resource alerts, and sync activity timeline.
        </p>
      </div>
      <ControlPlaneSection data={data.control_plane} stats={stats} argoCDUrl={argoCDUrl} />
      <AddonDistributionSection addonGroups={data.addon_groups ?? []} />
      <ResourceAlertsSection alerts={data.resource_alerts ?? []} />
      <AddonGroupsSection
        groups={data.addon_groups ?? []}
        addonStateMap={addonStateMap}
        clusterAttentionRows={clusterAttentionRows}
        driftRows={driftRows}
        orphanAddonRows={orphanAddonRows}
        totalOpenIssues={totalOpenIssues}
        issuesOnly={issuesOnly}
        onClearIssuesOnly={clearIssuesOnly}
      />
      <SyncActivitySection syncs={data.recent_syncs ?? []} />
      <div className="grid grid-cols-1 gap-6 md:grid-cols-2">
        <DeploymentFrequencySection syncs={data.recent_syncs ?? []} />
        <SyncDurationSection syncs={data.recent_syncs ?? []} />
      </div>
    </div>
  );
}
export default Observability

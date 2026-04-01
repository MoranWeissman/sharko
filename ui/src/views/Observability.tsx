import { useState, useEffect, useCallback, useMemo } from 'react';
import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  CartesianGrid,
} from 'recharts';
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
} from 'lucide-react';
import { api } from '@/services/api';
import type {
  ObservabilityOverviewResponse,
  AddonGroupHealth,
  ResourceAlert,
  SyncActivityEntry,
  AddonMetricsData,
  ClusterMetricsData,
} from '@/services/models';
import { LoadingState } from '@/components/LoadingState';
import { ErrorState } from '@/components/ErrorState';

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
  return 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400';
}

function syncBadgeCls(sync: string): string {
  const s = (sync ?? '').toLowerCase();
  if (s === 'synced')
    return 'bg-green-100 text-green-800 dark:bg-green-900/40 dark:text-green-300';
  if (s === 'outofsync' || s === 'out_of_sync')
    return 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-300';
  return 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400';
}

const HEALTH_COLORS: Record<string, string> = {
  Healthy: '#22c55e',
  Degraded: '#ef4444',
  Progressing: '#3b82f6',
  Missing: '#f59e0b',
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
}: {
  data: ObservabilityOverviewResponse['control_plane'];
}) {
  const healthData = useMemo(
    () =>
      Object.entries(data.health_summary).map(([name, value]) => ({
        name,
        value,
      })),
    [data.health_summary],
  );

  const total = healthData.reduce((sum, d) => sum + d.value, 0);

  return (
    <section className="rounded-xl border border-gray-200 bg-white p-6 shadow-sm dark:border-gray-700 dark:bg-gray-900">
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
          Control Plane
        </h2>
        <span className="rounded-full bg-cyan-100 px-2.5 py-0.5 text-xs font-medium text-cyan-800 dark:bg-cyan-900/40 dark:text-cyan-300">
          ArgoCD {data.argocd_version}
        </span>
        <span className="rounded-full bg-gray-100 px-2.5 py-0.5 text-xs font-medium text-gray-600 dark:bg-gray-800 dark:text-gray-400">
          Helm {data.helm_version}
        </span>
        <span className="rounded-full bg-gray-100 px-2.5 py-0.5 text-xs font-medium text-gray-600 dark:bg-gray-800 dark:text-gray-400">
          kubectl {data.kubectl_version}
        </span>
      </div>

      <div className="mb-5 grid grid-cols-3 gap-4">
        <StatBlock label="Total Apps" value={data.total_apps} />
        <StatBlock label="Total Clusters" value={data.total_clusters} />
        <StatBlock
          label="Connected"
          value={data.connected_clusters}
          sub={`/ ${data.total_clusters}`}
        />
      </div>

      {/* Health bar */}
      <div>
        <p className="mb-2 text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
          Health Summary
        </p>
        <div className="flex h-4 overflow-hidden rounded-full">
          {healthData.map((d) => (
            <div
              key={d.name}
              style={{
                width: total > 0 ? `${(d.value / total) * 100}%` : '0%',
                backgroundColor: HEALTH_COLORS[d.name] ?? '#9ca3af',
              }}
              title={`${d.name}: ${d.value}`}
            />
          ))}
        </div>
        <div className="mt-2 flex flex-wrap gap-4">
          {healthData.map((d) => (
            <span
              key={d.name}
              className="flex items-center gap-1.5 text-xs text-gray-600 dark:text-gray-400"
            >
              <span
                className="inline-block h-2.5 w-2.5 rounded-full"
                style={{ backgroundColor: HEALTH_COLORS[d.name] ?? '#9ca3af' }}
              />
              {d.name}: {d.value}
            </span>
          ))}
        </div>
      </div>
    </section>
  );
}

function StatBlock({
  label,
  value,
  sub,
}: {
  label: string;
  value: number;
  sub?: string;
}) {
  return (
    <div className="rounded-lg border border-gray-100 bg-gray-50 p-4 dark:border-gray-700 dark:bg-gray-800">
      <p className="text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
        {label}
      </p>
      <p className="mt-1 text-2xl font-bold text-gray-900 dark:text-gray-100">
        {value}
        {sub && (
          <span className="text-base font-normal text-gray-400"> {sub}</span>
        )}
      </p>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Section 2: Resource Alerts
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
            className="flex items-center justify-between rounded-lg bg-white px-4 py-3 dark:bg-gray-900"
          >
            <div className="flex items-center gap-3">
              <AlertTriangle className="h-4 w-4 shrink-0 text-amber-500" />
              <div>
                <span className="text-sm font-medium text-gray-900 dark:text-gray-100">
                  {alert.addon_name}
                </span>
                <p className="text-xs text-gray-500 dark:text-gray-400">
                  {alert.details}
                </p>
              </div>
            </div>
            <span className="rounded-full bg-amber-100 px-2 py-0.5 text-[10px] font-semibold uppercase text-amber-700 dark:bg-amber-900/40 dark:text-amber-300">
              {alert.alert_type.replace(/_/g, ' ')}
            </span>
          </div>
        ))}
      </div>
    </section>
  );
}

// ---------------------------------------------------------------------------
// Section 3: Addon Health Groups
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Resource usage bar: green (usage<=request), yellow (request<usage<=limit), red (>limit), gray (remaining)
// ---------------------------------------------------------------------------

function ResourceUsageBar({
  usage,
  request,
  limit,
  label,
  unit,
  decimals = 2,
}: {
  usage: number;
  request: number;
  limit: number;
  label: string;
  unit: string;
  decimals?: number;
}) {
  const max = Math.max(limit, usage, request, 0.001);
  const usagePct = Math.min((usage / max) * 100, 100);
  const requestPct = Math.min((request / max) * 100, 100);

  // Determine bar color based on usage vs request vs limit
  let barColor = '#22c55e'; // green: usage <= request
  if (usage > request && request > 0) barColor = '#f59e0b'; // yellow: above request
  if (usage > limit && limit > 0) barColor = '#ef4444'; // red: above limit

  return (
    <div className="flex items-center gap-2" title={`${label}: ${usage.toFixed(decimals)} / ${request.toFixed(decimals)} / ${limit.toFixed(decimals)} ${unit}`}>
      <div className="relative h-2.5 w-20 overflow-hidden rounded-full bg-gray-200 dark:bg-gray-700">
        {/* Usage bar */}
        <div
          className="absolute left-0 top-0 h-full rounded-full transition-all"
          style={{ width: `${usagePct}%`, backgroundColor: barColor }}
        />
        {/* Request marker line */}
        {request > 0 && (
          <div
            className="absolute top-0 h-full border-r-2 border-dashed border-gray-500 dark:border-gray-400"
            style={{ left: `${requestPct}%` }}
          />
        )}
      </div>
      <span className="whitespace-nowrap text-[10px] text-gray-500 dark:text-gray-400">
        {usage.toFixed(decimals)}<span className="text-gray-400 dark:text-gray-500"> / {request.toFixed(decimals)} / {limit.toFixed(decimals)}</span> {unit}
      </span>
    </div>
  );
}

type GroupBy = 'addon' | 'cluster';

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

function AddonGroupsSection({ groups }: { groups: AddonGroupHealth[] }) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [sortMode, setSortMode] = useState<'issues' | 'alpha'>('issues');
  const [groupBy, setGroupBy] = useState<GroupBy>('addon');
  const [visibleCount, setVisibleCount] = useState(10);
  const [ddEnabled, setDdEnabled] = useState<boolean | null>(null);
  // Cache: clusterName -> data (or null if fetch failed/empty)
  const [clusterMetricsCache, setClusterMetricsCache] = useState<Record<string, ClusterMetricsData | null>>({});
  const [loadingClusters, setLoadingClusters] = useState<Set<string>>(new Set());

  useEffect(() => {
    api.getDatadogStatus().then((s) => setDdEnabled(s.enabled)).catch(() => setDdEnabled(false));
  }, []);

  // Pivot data: group by cluster instead of addon
  const clusterGroups = useMemo((): ClusterGroup[] => {
    const map = new Map<string, ClusterGroup>();
    for (const group of groups) {
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

  const sortedAddonGroups = useMemo(() => {
    const copy = [...(groups ?? [])];
    if (sortMode === 'alpha') {
      copy.sort((a, b) => a.addon_name.localeCompare(b.addon_name));
    }
    return copy;
  }, [groups, sortMode]);

  const sortedClusterGroups = useMemo(() => {
    const copy = [...clusterGroups];
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
  }, [clusterGroups, sortMode]);

  const fetchClusterMetrics = useCallback((clusterNames: string[]) => {
    if (!ddEnabled) return;
    for (const cn of clusterNames) {
      if (cn in clusterMetricsCache || loadingClusters.has(cn)) continue;
      setLoadingClusters((prev) => new Set(prev).add(cn));
      api.getClusterMetrics(cn).then((data) => {
        setClusterMetricsCache((prev) => ({ ...prev, [cn]: data }));
      }).catch(() => {
        // Mark as fetched-but-empty so we don't retry and don't show "..."
        setClusterMetricsCache((prev) => ({ ...prev, [cn]: null }));
      }).finally(() => {
        setLoadingClusters((prev) => {
          const next = new Set(prev);
          next.delete(cn);
          return next;
        });
      });
    }
  }, [ddEnabled, clusterMetricsCache, loadingClusters]);

  const toggle = (name: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(name)) {
        next.delete(name);
      } else {
        next.add(name);
        if (groupBy === 'addon') {
          const group = groups.find((g) => g.addon_name === name);
          if (group?.child_apps) {
            const clusters = [...new Set(group.child_apps.map((c) => c.cluster_name))];
            fetchClusterMetrics(clusters);
          }
        } else {
          fetchClusterMetrics([name]);
        }
      }
      return next;
    });
  };

  // Reset expanded state when switching group mode
  const handleGroupByChange = (mode: GroupBy) => {
    setGroupBy(mode);
    setExpanded(new Set());
  };

  const getAddonMetrics = (clusterName: string, addonName: string): AddonMetricsData | undefined => {
    const cm = clusterMetricsCache[clusterName];
    if (!cm?.addons) return undefined;
    return cm.addons.find((a) => a.namespace === addonName || a.addon_name === addonName);
  };

  if (!groups || groups.length === 0) {
    return null;
  }

  return (
    <section>
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <h2 className="flex items-center gap-2 text-lg font-semibold text-gray-900 dark:text-gray-100">
          <Server className="h-5 w-5 text-cyan-500" />
          Addon Health
        </h2>
        <div className="flex items-center gap-3">
          {/* Group by toggle */}
          <div className="flex rounded-md border border-gray-300 dark:border-gray-600">
            {(['addon', 'cluster'] as const).map((m) => (
              <button
                key={m}
                onClick={() => handleGroupByChange(m)}
                className={`px-2.5 py-1 text-xs font-medium transition-colors ${
                  groupBy === m
                    ? 'bg-cyan-600 text-white'
                    : 'text-gray-500 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-800'
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
                    ? 'bg-cyan-100 text-cyan-800 dark:bg-cyan-900/40 dark:text-cyan-300'
                    : 'text-gray-500 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-800'
                }`}
              >
                {m === 'issues' ? 'Most Issues' : 'A-Z'}
              </button>
            ))}
          </div>
        </div>
      </div>

      <div className="space-y-3">
        {groupBy === 'addon' ? (
          /* ---- Group by Addon ---- */
          sortedAddonGroups.slice(0, visibleCount).map((group) => {
            const isExpanded = expanded.has(group.addon_name);
            const healthEntries = Object.entries(group.health_counts);
            const total = group.total_apps;

            return (
              <div
                key={group.addon_name}
                className="rounded-xl border border-gray-200 bg-white shadow-sm transition-shadow hover:shadow-md dark:border-gray-700 dark:bg-gray-900"
              >
                <button
                  onClick={() => toggle(group.addon_name)}
                  className="flex w-full items-center gap-4 p-4 text-left"
                  aria-expanded={isExpanded}
                >
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-3">
                      <span className="text-sm font-semibold text-gray-900 dark:text-gray-100">
                        {group.addon_name}
                      </span>
                      <span className="rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-500 dark:bg-gray-800 dark:text-gray-400">
                        {total} app{total !== 1 ? 's' : ''}
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
                          className="flex items-center gap-1 text-[11px] text-gray-500 dark:text-gray-400"
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
                    <ChevronUp className="h-4 w-4 shrink-0 text-gray-400" />
                  ) : (
                    <ChevronDown className="h-4 w-4 shrink-0 text-gray-400" />
                  )}
                </button>

                {isExpanded && group.child_apps && group.child_apps.length > 0 && (
                  <div className="border-t border-gray-100 px-4 pb-4 dark:border-gray-700">
                    <table className="mt-3 w-full text-xs">
                      <thead>
                        <tr className="text-left text-gray-500 dark:text-gray-400">
                          <th className="pb-2 font-medium">Cluster</th>
                          <th className="pb-2 font-medium">Health</th>
                          <th className="pb-2 font-medium">Sync</th>
                          {ddEnabled ? (
                            <>
                              <th className="pb-2 font-medium">CPU (use / req / lim)</th>
                              <th className="pb-2 font-medium">Memory (use / req / lim)</th>
                              <th className="pb-2 font-medium">Pods</th>
                            </>
                          ) : (
                            <th className="pb-2 font-medium">Resources</th>
                          )}
                          <th className="pb-2 font-medium">Last Reconciled</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-gray-50 dark:divide-gray-800">
                        {group.child_apps.map((child) => {
                          const am = ddEnabled ? getAddonMetrics(child.cluster_name, group.addon_name) : undefined;
                          return (
                            <tr
                              key={child.app_name}
                              className="hover:bg-gray-50 dark:hover:bg-gray-800"
                            >
                              <td className="py-2 pr-3 font-medium text-gray-800 dark:text-gray-200">
                                {child.cluster_name}
                              </td>
                              <td className="py-2 pr-3">
                                <span className={`inline-block rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase ${healthBadgeCls(child.health)}`}>
                                  {child.health || 'Unknown'}
                                </span>
                              </td>
                              <td className="py-2 pr-3">
                                <span className={`inline-block rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase ${syncBadgeCls(child.sync_status)}`}>
                                  {child.sync_status || 'Unknown'}
                                </span>
                              </td>
                              {ddEnabled && am ? (
                                <>
                                  <td className="py-2 pr-3">
                                    <ResourceUsageBar usage={am.cpu_usage_cores} request={am.cpu_request_cores} limit={am.cpu_limit_cores} label="CPU" unit="cores" decimals={3} />
                                  </td>
                                  <td className="py-2 pr-3">
                                    <ResourceUsageBar usage={am.mem_usage_mb} request={am.mem_request_mb} limit={am.mem_limit_mb} label="Mem" unit="MB" decimals={0} />
                                  </td>
                                  <td className="py-2 pr-3 text-gray-600 dark:text-gray-300">{am.pod_count}</td>
                                </>
                              ) : ddEnabled && !am ? (
                                <>
                                  <td className="py-2 pr-3 text-gray-400">{loadingClusters.has(child.cluster_name) ? '...' : '--'}</td>
                                  <td className="py-2 pr-3 text-gray-400">{loadingClusters.has(child.cluster_name) ? '...' : '--'}</td>
                                  <td className="py-2 pr-3 text-gray-400">{loadingClusters.has(child.cluster_name) ? '...' : '--'}</td>
                                </>
                              ) : (
                                <td className="py-2 pr-3 text-gray-500 dark:text-gray-400">
                                  {child.resource_summary.total_pods > 0 && (
                                    <span>{child.resource_summary.running_pods}/{child.resource_summary.total_pods} pods</span>
                                  )}
                                  {child.resource_summary.total_pods === 0 && child.resource_summary.total_containers > 0 && (
                                    <span>{child.resource_summary.total_containers} workloads</span>
                                  )}
                                  {child.resource_summary.total_pods === 0 && child.resource_summary.total_containers === 0 && (
                                    <span className="text-gray-400">--</span>
                                  )}
                                </td>
                              )}
                              <td className="py-2 text-gray-400">
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
          sortedClusterGroups.length === 0 ? (
            <div className="rounded-lg border border-gray-200 bg-white p-8 text-center text-sm text-gray-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-400">
              No cluster data available.
            </div>
          ) : (
            sortedClusterGroups.map((cg) => {
              const isExpanded = expanded.has(cg.cluster_name);
              const healthEntries = Object.entries(cg.health_counts);
              const total = cg.addons.length;

              return (
                <div
                  key={cg.cluster_name}
                  className="rounded-xl border border-gray-200 bg-white shadow-sm transition-shadow hover:shadow-md dark:border-gray-700 dark:bg-gray-900"
                >
                  <button
                    onClick={() => toggle(cg.cluster_name)}
                    className="flex w-full items-center gap-4 p-4 text-left"
                    aria-expanded={isExpanded}
                  >
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-3">
                        <span className="text-sm font-semibold text-gray-900 dark:text-gray-100">
                          {cg.cluster_name}
                        </span>
                        <span className="rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-500 dark:bg-gray-800 dark:text-gray-400">
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
                            className="flex items-center gap-1 text-[11px] text-gray-500 dark:text-gray-400"
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
                      <ChevronUp className="h-4 w-4 shrink-0 text-gray-400" />
                    ) : (
                      <ChevronDown className="h-4 w-4 shrink-0 text-gray-400" />
                    )}
                  </button>

                  {isExpanded && (
                    <div className="border-t border-gray-100 px-4 pb-4 dark:border-gray-700">
                      <table className="mt-3 w-full text-xs">
                        <thead>
                          <tr className="text-left text-gray-500 dark:text-gray-400">
                            <th className="pb-2 font-medium">Addon</th>
                            <th className="pb-2 font-medium">Health</th>
                            <th className="pb-2 font-medium">Sync</th>
                            {ddEnabled ? (
                              <>
                                <th className="pb-2 font-medium">CPU (use / req / lim)</th>
                                <th className="pb-2 font-medium">Memory (use / req / lim)</th>
                                <th className="pb-2 font-medium">Pods</th>
                              </>
                            ) : (
                              <th className="pb-2 font-medium">Resources</th>
                            )}
                            <th className="pb-2 font-medium">Last Reconciled</th>
                          </tr>
                        </thead>
                        <tbody className="divide-y divide-gray-50 dark:divide-gray-800">
                          {cg.addons.map((addon) => {
                            const am = ddEnabled ? getAddonMetrics(cg.cluster_name, addon.addon_name) : undefined;
                            return (
                              <tr
                                key={addon.app_name}
                                className="hover:bg-gray-50 dark:hover:bg-gray-800"
                              >
                                <td className="py-2 pr-3 font-medium text-gray-800 dark:text-gray-200">
                                  {addon.addon_name}
                                </td>
                                <td className="py-2 pr-3">
                                  <span className={`inline-block rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase ${healthBadgeCls(addon.health)}`}>
                                    {addon.health || 'Unknown'}
                                  </span>
                                </td>
                                <td className="py-2 pr-3">
                                  <span className={`inline-block rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase ${syncBadgeCls(addon.sync_status)}`}>
                                    {addon.sync_status || 'Unknown'}
                                  </span>
                                </td>
                                {ddEnabled && am ? (
                                  <>
                                    <td className="py-2 pr-3">
                                      <ResourceUsageBar usage={am.cpu_usage_cores} request={am.cpu_request_cores} limit={am.cpu_limit_cores} label="CPU" unit="cores" decimals={3} />
                                    </td>
                                    <td className="py-2 pr-3">
                                      <ResourceUsageBar usage={am.mem_usage_mb} request={am.mem_request_mb} limit={am.mem_limit_mb} label="Mem" unit="MB" decimals={0} />
                                    </td>
                                    <td className="py-2 pr-3 text-gray-600 dark:text-gray-300">{am.pod_count}</td>
                                  </>
                                ) : ddEnabled && !am ? (
                                  <>
                                    <td className="py-2 pr-3 text-gray-400">{loadingClusters.has(cg.cluster_name) ? '...' : '--'}</td>
                                    <td className="py-2 pr-3 text-gray-400">{loadingClusters.has(cg.cluster_name) ? '...' : '--'}</td>
                                    <td className="py-2 pr-3 text-gray-400">{loadingClusters.has(cg.cluster_name) ? '...' : '--'}</td>
                                  </>
                                ) : (
                                  <td className="py-2 pr-3 text-gray-500 dark:text-gray-400">
                                    {addon.resource_summary.total_pods > 0 && (
                                      <span>{addon.resource_summary.running_pods}/{addon.resource_summary.total_pods} pods</span>
                                    )}
                                    {addon.resource_summary.total_pods === 0 && addon.resource_summary.total_containers > 0 && (
                                      <span>{addon.resource_summary.total_containers} workloads</span>
                                    )}
                                    {addon.resource_summary.total_pods === 0 && addon.resource_summary.total_containers === 0 && (
                                      <span className="text-gray-400">--</span>
                                    )}
                                  </td>
                                )}
                                <td className="py-2 text-gray-400">
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

      {/* Show more / less for addon groups */}
      {groupBy === 'addon' && sortedAddonGroups.length > visibleCount && (
        <button
          onClick={() => setVisibleCount(v => v + 10)}
          className="w-full rounded-lg border border-gray-200 bg-white py-2 text-center text-sm text-cyan-600 hover:bg-gray-50 dark:border-gray-700 dark:bg-gray-800 dark:text-cyan-400"
        >
          Show more ({sortedAddonGroups.length - visibleCount} remaining)
        </button>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------------
// Section 4: Sync Activity Timeline
// ---------------------------------------------------------------------------

function SyncActivitySection({
  syncs,
}: {
  syncs: SyncActivityEntry[];
}) {
  const [addonFilter, setAddonFilter] = useState('');
  const [clusterFilter, setClusterFilter] = useState('');

  const addonNames = useMemo(
    () => [...new Set(syncs.map((s) => s.addon_name))].sort(),
    [syncs],
  );
  const clusterNames = useMemo(
    () => [...new Set(syncs.map((s) => s.cluster_name))].sort(),
    [syncs],
  );

  const filtered = useMemo(
    () =>
      syncs.filter(
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
    for (const s of syncs) {
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
    <section className="rounded-xl border border-gray-200 bg-white p-6 shadow-sm dark:border-gray-700 dark:bg-gray-900">
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <h2 className="flex items-center gap-2 text-lg font-semibold text-gray-900 dark:text-gray-100">
          <Activity className="h-5 w-5 text-cyan-500" />
          Sync Activity
        </h2>
        <div className="flex gap-2">
          <select
            value={addonFilter}
            onChange={(e) => setAddonFilter(e.target.value)}
            className="rounded-md border border-gray-200 bg-white px-2 py-1 text-xs text-gray-700 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300"
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
            className="rounded-md border border-gray-200 bg-white px-2 py-1 text-xs text-gray-700 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300"
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
      {syncs.length > 0 && (
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
          <p className="py-4 text-center text-sm text-gray-500">
            No sync activity found.
          </p>
        )}
        {filtered.map((s, idx) => (
          <div
            key={`${s.timestamp}-${s.app_name}-${idx}`}
            className="flex items-center gap-3 rounded-lg px-3 py-2 transition-colors hover:bg-gray-50 dark:hover:bg-gray-800"
          >
            {statusIcon(s.status)}
            <span className="w-16 shrink-0 text-xs text-gray-400">
              {timeAgo(s.timestamp)}
            </span>
            <span className="min-w-0 flex-1 truncate text-sm font-medium text-gray-900 dark:text-gray-100">
              {s.addon_name}
            </span>
            <span className="hidden truncate text-xs text-gray-500 sm:inline dark:text-gray-400">
              {s.cluster_name}
            </span>
            <span className="flex items-center gap-1 text-xs text-gray-400">
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
// Main View
// ---------------------------------------------------------------------------

export function Observability() {
  const [data, setData] = useState<ObservabilityOverviewResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchData = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const result = await api.getObservability();
      setData(result);
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

  if (loading) return <LoadingState message="Loading observability data..." />;
  if (error) return <ErrorState message={error} onRetry={fetchData} />;
  if (!data) return null;

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold text-gray-900 dark:text-gray-100">Observability</h1>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
          ArgoCD control plane health, add-on health per cluster, resource alerts, and sync activity timeline.
        </p>
      </div>
      <ControlPlaneSection data={data.control_plane} />
      <ResourceAlertsSection alerts={data.resource_alerts} />
      <AddonGroupsSection groups={data.addon_groups} />
      <SyncActivitySection syncs={data.recent_syncs} />
    </div>
  );
}

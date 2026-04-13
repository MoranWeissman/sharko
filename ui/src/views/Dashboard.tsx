import { useState, useEffect, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Server, AppWindow, Package, Rocket, AlertTriangle, CheckCircle2,
  ArrowUpCircle, Activity, Clock, ChevronRight
} from 'lucide-react';
import { api } from '@/services/api';
import type { DashboardStats, SyncActivityEntry, ClustersResponse } from '@/services/models';
import { StatCard } from '@/components/StatCard';
import { ClusterCard } from '@/components/ClusterCard';
import { WaveDecoration } from '@/components/WaveDecoration';
import { LoadingState } from '@/components/LoadingState';
import { ErrorState } from '@/components/ErrorState';
import { ArgoCDStatusBanner } from '@/components/ArgoCDStatusBanner';
import { PendingPRsPanel } from '@/components/PendingPRsPanel';
import { showToast } from '@/components/ToastNotification';
import type { TrackedPR } from '@/services/models';

// --- Health Bar with totals ---

interface HealthBarProps {
  title: string;
  subtitle: string;
  segments: { label: string; value: number; color: string }[];
}

function HealthBar({ title, subtitle, segments }: HealthBarProps) {
  const total = segments.reduce((sum, s) => sum + s.value, 0);
  if (total === 0) return null;

  return (
    <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5 shadow-sm dark:border-gray-700 dark:bg-gray-800">
      <h3 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">{title}</h3>
      <p className="mb-3 text-xs text-[#2a5a7a] dark:text-gray-400">{subtitle}</p>
      <div className="mb-3 flex h-3 overflow-hidden rounded-full bg-[#d6eeff] dark:bg-gray-700">
        {segments.filter(s => s.value > 0).map((seg) => (
          <div
            key={seg.label}
            className="transition-all duration-500"
            style={{ width: `${(seg.value / total) * 100}%`, backgroundColor: seg.color }}
            title={`${seg.label}: ${seg.value}`}
          />
        ))}
      </div>
      <div className="flex flex-wrap gap-x-4 gap-y-1">
        {segments.filter(s => s.value > 0).map((seg) => (
          <div key={seg.label} className="flex items-center gap-1.5 text-xs">
            <div className="h-2.5 w-2.5 rounded-full" style={{ backgroundColor: seg.color }} />
            <span className="font-medium text-[#0a3a5a] dark:text-gray-300">{seg.value}/{total}</span>
            <span className="text-[#2a5a7a] dark:text-gray-400">{seg.label}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

// --- Time ago helper ---

function timeAgo(timestamp: string): string {
  const secs = Math.floor((Date.now() - new Date(timestamp).getTime()) / 1000);
  if (secs < 60) return 'just now';
  if (secs < 3600) return `${Math.floor(secs / 60)}m ago`;
  if (secs < 86400) return `${Math.floor(secs / 3600)}h ago`;
  return `${Math.floor(secs / 86400)}d ago`;
}

// --- Main Dashboard ---

export function Dashboard() {
  const navigate = useNavigate();
  const [stats, setStats] = useState<DashboardStats | null>(null);
  const [recentSyncs, setRecentSyncs] = useState<SyncActivityEntry[]>([]);
  const [versionDrifts, setVersionDrifts] = useState<{ addon: string; count: number }[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [attentionItems, setAttentionItems] = useState<{ app_name: string; addon_name: string; cluster: string; health: string; sync: string; error?: string; error_type?: string }[]>([]);
  const [showAttention, setShowAttention] = useState(false);
  const [clusters, setClusters] = useState<{ name: string; connectionStatus: string; addons: { name: string; health: string }[]; healthy: number; total: number }[]>([]);
  const [argoCDUnreachable, setArgoCDUnreachable] = useState(false);

  const fetchData = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const [statsData, obsData, matrixData, attention, clustersData] = await Promise.all([
        api.getDashboardStats(),
        api.getObservability().catch(() => null),
        api.getVersionMatrix().catch(() => null),
        api.getAttentionItems().catch(() => []),
        api.getClusters().catch(() => null),
      ]);
      setStats(statsData);
      setAttentionItems(attention || []);

      // Recent syncs (last 5)
      if (obsData?.recent_syncs) {
        setRecentSyncs(obsData.recent_syncs.slice(0, 5));
      }

      // Version drifts
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
      }

      // Detect ArgoCD unreachable
      const typedClustersCheck = clustersData as ClustersResponse | null
      if (typedClustersCheck?.clusters && typedClustersCheck.clusters.length > 0) {
        const allFailed = typedClustersCheck.clusters.every(
          (c) => !c.connection_status || c.connection_status === 'Failed' || c.connection_status === 'unknown'
        );
        setArgoCDUnreachable(allFailed);
      }

      // Build cluster cards
      const typedClusters = clustersData as ClustersResponse | null
      if (typedClusters?.clusters && matrixData?.addons) {
        const cards = typedClusters.clusters.map(c => {
          const addons: { name: string; health: string }[] = []
          let healthy = 0
          let total = 0
          for (const row of matrixData.addons) {
            const cell = row.cells?.[c.name]
            if (cell) {
              total++
              const health = cell.health || 'Unknown'
              if (health === 'Healthy') healthy++
              addons.push({ name: row.addon_name, health })
            }
          }
          return { name: c.name, connectionStatus: c.connection_status || 'Unknown', addons, healthy, total }
        })
        const problemClusters = cards.filter(c =>
          (c.connectionStatus !== 'Successful' && c.connectionStatus !== 'Connected') ||
          c.healthy < c.total
        )
        setClusters(problemClusters)
      }
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to load dashboard');
    } finally {
      setLoading(false);
    }
  }, []);

  const handlePRMerged = useCallback((pr: TrackedPR) => {
    showToast(`PR #${pr.pr_id} merged -- ${pr.cluster ?? ''} ${pr.operation}`)
    // Refresh dashboard data when a PR merges
    void fetchData()
  }, [fetchData])

  useEffect(() => {
    void fetchData();
  }, [fetchData]);

  if (loading) return <LoadingState message="Loading dashboard..." />;
  if (error) return <ErrorState message={error} onRetry={fetchData} />;
  if (!stats) return null;

  const degradedCount = stats.applications.by_health_status.degraded;
  const disconnectedCount = stats.clusters.disconnected_from_argocd;
  const hasIssues = degradedCount > 0 || disconnectedCount > 0;

  // Top deployed addons (from stats — we can derive from total_deployments vs enabled)
  const appTotal = stats.applications.total;
  const healthyCount = stats.applications.by_health_status.healthy;

  return (
    <div className="mx-auto max-w-screen-xl space-y-6">
      {/* Hero Section */}
      <div className="relative overflow-hidden rounded-2xl bg-gradient-to-r from-teal-700 to-blue-800 px-8 py-8 text-white shadow-lg dark:from-teal-900 dark:to-blue-950">
        <div className="flex items-center gap-6">
          <img
            src="/sharko-banner.png"
            alt="Sharko"
            className="hidden h-32 w-auto sm:block"
          />
          <div>
            <h1 className="text-2xl font-bold tracking-tight sm:text-3xl" style={{ fontFamily: '"Quicksand", sans-serif', fontWeight: 700 }}>
              Sharko
            </h1>
            <p className="mt-1 max-w-2xl text-sm text-teal-100 sm:text-base">
              Addon management across all your Kubernetes clusters.
            </p>
          </div>
        </div>
        <WaveDecoration />
      </div>

      {/* ArgoCD Status Banner */}
      <ArgoCDStatusBanner visible={argoCDUnreachable} />

      {/* Needs Attention */}
      {hasIssues || attentionItems.length > 0 ? (
        <div className="rounded-xl border-2 border-amber-300 bg-amber-50 dark:border-amber-700 dark:bg-amber-900/20">
          <div className="flex items-center justify-between p-5 pb-3">
            <div className="flex items-center gap-2 text-amber-700 dark:text-amber-400">
              <AlertTriangle className="h-5 w-5" />
              <h3 className="text-sm font-semibold">Needs Attention</h3>
            </div>
            <div className="flex flex-wrap gap-2">
              {attentionItems.length > 0 && (
                <button onClick={() => setShowAttention(!showAttention)}
                  className="flex items-center gap-2 rounded-lg border border-red-200 bg-[#f0f7ff] px-3 py-1.5 text-xs text-red-700 hover:bg-red-50 dark:border-red-800 dark:bg-gray-800 dark:text-red-400">
                  <div className="h-2 w-2 rounded-full bg-red-500" />
                  {attentionItems.length} app{attentionItems.length !== 1 ? 's' : ''} with issues
                  <ChevronRight className={`h-3 w-3 transition-transform ${showAttention ? 'rotate-90' : ''}`} />
                </button>
              )}
              {disconnectedCount > 0 && (
                <button onClick={() => navigate('/clusters?status=disconnected')}
                  className="flex items-center gap-2 rounded-lg border border-red-200 bg-[#f0f7ff] px-3 py-1.5 text-xs text-red-700 hover:bg-red-50 dark:border-red-800 dark:bg-gray-800 dark:text-red-400">
                  <div className="h-2 w-2 rounded-full bg-red-500" />
                  {disconnectedCount} disconnected cluster{disconnectedCount !== 1 ? 's' : ''}
                </button>
              )}
              {versionDrifts.length > 0 && (
                <button onClick={() => navigate('/version-matrix?drift=true')}
                  className="flex items-center gap-2 rounded-lg border border-amber-200 bg-[#f0f7ff] px-3 py-1.5 text-xs text-amber-700 hover:bg-amber-50 dark:border-amber-800 dark:bg-gray-800 dark:text-amber-400">
                  <div className="h-2 w-2 rounded-full bg-amber-500" />
                  {versionDrifts.length} addon{versionDrifts.length !== 1 ? 's' : ''} with drift
                </button>
              )}
            </div>
          </div>
          {/* Expandable detail panel */}
          {showAttention && attentionItems.length > 0 && (
            <div className="border-t border-amber-200 p-4 dark:border-amber-700">
              <div className="max-h-64 overflow-y-auto space-y-1.5">
                {attentionItems.map((item, i) => (
                  <div key={i} className="flex items-start gap-3 rounded-lg bg-[#f0f7ff] px-3 py-2 text-xs dark:bg-gray-800">
                    <div className={`mt-0.5 h-2.5 w-2.5 shrink-0 rounded-full ${
                      item.health === 'Error' ? 'bg-red-500' : item.health === 'Degraded' ? 'bg-red-400' : 'bg-amber-500'
                    }`} />
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <span className="font-medium text-[#0a2a4a] dark:text-gray-100">{item.app_name}</span>
                        <span className={`rounded px-1.5 py-0.5 text-[10px] font-medium ${
                          item.health === 'Error' ? 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'
                            : item.health === 'Degraded' ? 'bg-red-100 text-red-600 dark:bg-red-900/30 dark:text-red-400'
                              : 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400'
                        }`}>{item.health}</span>
                        {item.cluster && <span className="text-[#3a6a8a]">on {item.cluster}</span>}
                      </div>
                      {item.error && (
                        <p className="mt-1 truncate text-[#2a5a7a] dark:text-gray-400" title={item.error}>
                          {item.error_type && <span className="font-medium text-red-600 dark:text-red-400">{item.error_type}: </span>}
                          {item.error.length > 120 ? item.error.slice(0, 120) + '...' : item.error}
                        </p>
                      )}
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      ) : (
        <div className="flex items-center gap-2 rounded-xl border border-green-200 bg-green-50 px-5 py-4 dark:border-green-800 dark:bg-green-900/20">
          <CheckCircle2 className="h-5 w-5 text-green-600 dark:text-green-400" />
          <span className="text-sm font-medium text-green-700 dark:text-green-400">All systems operational</span>
        </div>
      )}

      {/* Stats Cards */}
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <StatCard title="Total Clusters" value={stats.clusters.total} icon={<Server className="h-6 w-6" />}
          color="default" onClick={() => navigate('/clusters')} />
        <StatCard title="Applications" value={`${healthyCount}/${appTotal} healthy`} icon={<AppWindow className="h-6 w-6" />}
          color={degradedCount > 0 ? 'error' : 'success'} onClick={() => navigate('/addons')} />
        <StatCard title="Available Addons" value={stats.addons.total_available} icon={<Package className="h-6 w-6" />}
          color="default" onClick={() => navigate('/addons')} />
        <StatCard title="Active Deployments" value={`${stats.addons.enabled_deployments}/${stats.addons.total_deployments}`}
          icon={<Rocket className="h-6 w-6" />} color="warning" onClick={() => navigate('/version-matrix')} />
      </div>

      {/* Cluster Cards — problem clusters only */}
      {clusters.length > 0 && (
        <div>
          <div className="mb-3 flex items-center justify-between">
            <h2 className="text-lg font-semibold text-[#0a2a4a] dark:text-gray-100">Clusters Needing Attention</h2>
            <button
              onClick={() => navigate('/clusters?status=issues')}
              className="text-sm text-teal-600 hover:text-teal-700 dark:text-teal-400"
            >
              View all {clusters.length} clusters
            </button>
          </div>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {clusters.slice(0, 5).map((cluster) => (
              <ClusterCard
                key={cluster.name}
                name={cluster.name}
                connectionStatus={cluster.connectionStatus}
                addonSummary={cluster.addons}
                healthyCount={cluster.healthy}
                totalCount={cluster.total}
              />
            ))}
          </div>
        </div>
      )}

      {/* Health Bars */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <HealthBar title="Application Health" subtitle="Operational health of deployed applications"
          segments={[
            { label: 'Healthy', value: stats.applications.by_health_status.healthy, color: '#22c55e' },
            { label: 'Progressing', value: stats.applications.by_health_status.progressing, color: '#3b82f6' },
            { label: 'Degraded', value: stats.applications.by_health_status.degraded, color: '#ef4444' },
            { label: 'Unknown', value: stats.applications.by_health_status.unknown, color: '#9ca3af' },
          ]} />
        <HealthBar title="Cluster Connectivity" subtitle="Cluster connection status to ArgoCD"
          segments={[
            { label: 'Connected', value: stats.clusters.connected_to_argocd, color: '#22c55e' },
            { label: 'Disconnected', value: stats.clusters.disconnected_from_argocd, color: '#ef4444' },
          ]} />
      </div>

      {/* Pending PRs */}
      <PendingPRsPanel onMergeDetected={handlePRMerged} />

      {/* Bottom row: Quick Actions + Recent Activity + Version Drift */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        {/* Quick Actions */}
        <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5 shadow-sm dark:border-gray-700 dark:bg-gray-800">
          <h3 className="mb-3 text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Quick Actions</h3>
          <div className="space-y-2">
            <button onClick={() => navigate('/upgrade')}
              className="flex w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left text-sm text-[#0a3a5a] transition-colors hover:bg-[#d6eeff] dark:text-gray-300 dark:hover:bg-gray-700">
              <ArrowUpCircle className="h-4 w-4 text-teal-500" />
              <span>Check Upgrade Impact</span>
              <ChevronRight className="ml-auto h-4 w-4 text-[#3a6a8a]" />
            </button>
            <button onClick={() => navigate('/observability')}
              className="flex w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left text-sm text-[#0a3a5a] transition-colors hover:bg-[#d6eeff] dark:text-gray-300 dark:hover:bg-gray-700">
              <Activity className="h-4 w-4 text-green-500" />
              <span>View Observability</span>
              <ChevronRight className="ml-auto h-4 w-4 text-[#3a6a8a]" />
            </button>
          </div>
        </div>

        {/* Recent Sync Activity */}
        <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5 shadow-sm dark:border-gray-700 dark:bg-gray-800">
          <div className="mb-3 flex items-center justify-between">
            <h3 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Recent Activity</h3>
            <button onClick={() => navigate('/observability')} className="text-xs text-teal-600 hover:text-teal-700 dark:text-teal-400">
              View all
            </button>
          </div>
          {recentSyncs.length === 0 ? (
            <p className="py-4 text-center text-xs text-[#2a5a7a]">No recent sync activity</p>
          ) : (
            <div className="space-y-2">
              {recentSyncs.map((sync, i) => (
                <div key={i} className="flex items-center gap-3 text-xs">
                  <div className={`h-2 w-2 shrink-0 rounded-full ${sync.status === 'Synced' || sync.status === 'Succeeded' ? 'bg-green-500' : 'bg-amber-500'}`} />
                  <div className="min-w-0 flex-1">
                    <span className="font-medium text-[#0a3a5a] dark:text-gray-300">{sync.addon_name}</span>
                    <span className="text-[#3a6a8a]"> on </span>
                    <span className="text-[#2a5a7a] dark:text-gray-400">{sync.cluster_name}</span>
                  </div>
                  <span className="shrink-0 text-[#3a6a8a] flex items-center gap-1">
                    <Clock className="h-3 w-3" />
                    {timeAgo(sync.timestamp)}
                  </span>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* Version Drift */}
        <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5 shadow-sm dark:border-gray-700 dark:bg-gray-800">
          <div className="mb-3 flex items-center justify-between">
            <h3 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Version Drift</h3>
            <button onClick={() => navigate('/version-matrix')} className="text-xs text-teal-600 hover:text-teal-700 dark:text-teal-400">
              View matrix
            </button>
          </div>
          {versionDrifts.length === 0 ? (
            <div className="flex items-center gap-2 py-4 text-center text-xs text-green-600 dark:text-green-400">
              <CheckCircle2 className="mx-auto h-4 w-4" />
              <span>No version drift detected</span>
            </div>
          ) : (
            <div className="space-y-2">
              {versionDrifts.slice(0, 5).map((d) => (
                <div key={d.addon}
                  onClick={() => navigate(`/addons/${d.addon}`)}
                  className="flex cursor-pointer items-center justify-between rounded-lg px-3 py-2 text-xs transition-colors hover:bg-[#d6eeff] dark:hover:bg-gray-700">
                  <span className="font-medium text-[#0a3a5a] dark:text-gray-300">{d.addon}</span>
                  <span className="rounded-full bg-amber-100 px-2 py-0.5 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400">
                    {d.count} cluster{d.count !== 1 ? 's' : ''}
                  </span>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
export default Dashboard

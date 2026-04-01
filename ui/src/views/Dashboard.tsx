import { useState, useEffect, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Server, AppWindow, Package, Rocket, AlertTriangle, CheckCircle2,
  ArrowUpCircle, GitPullRequest, Activity, Clock, ChevronRight
} from 'lucide-react';
import { api } from '@/services/api';
import type { DashboardStats, SyncActivityEntry } from '@/services/models';
import { StatCard } from '@/components/StatCard';
import { LoadingState } from '@/components/LoadingState';
import { ErrorState } from '@/components/ErrorState';

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
    <div className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm dark:border-gray-700 dark:bg-gray-800">
      <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">{title}</h3>
      <p className="mb-3 text-xs text-gray-500 dark:text-gray-400">{subtitle}</p>
      <div className="mb-3 flex h-3 overflow-hidden rounded-full bg-gray-100 dark:bg-gray-700">
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
            <span className="font-medium text-gray-700 dark:text-gray-300">{seg.value}/{total}</span>
            <span className="text-gray-500 dark:text-gray-400">{seg.label}</span>
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

  const fetchData = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const [statsData, obsData, matrixData, attention] = await Promise.all([
        api.getDashboardStats(),
        api.getObservability().catch(() => null),
        api.getVersionMatrix().catch(() => null),
        api.getAttentionItems().catch(() => []),
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
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to load dashboard');
    } finally {
      setLoading(false);
    }
  }, []);

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
      <div className="rounded-2xl bg-gradient-to-r from-cyan-600 to-blue-700 px-8 py-10 text-white shadow-lg dark:from-cyan-800 dark:to-blue-900">
        <h1 className="text-3xl font-bold tracking-tight sm:text-4xl">
          ArgoCD Addons Platform
        </h1>
        <p className="mt-2 max-w-2xl text-lg text-cyan-100">
          Centralized visibility into add-on deployments, health status, and
          configurations across all your Kubernetes clusters.
        </p>
      </div>

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
                  className="flex items-center gap-2 rounded-lg border border-red-200 bg-white px-3 py-1.5 text-xs text-red-700 hover:bg-red-50 dark:border-red-800 dark:bg-gray-800 dark:text-red-400">
                  <div className="h-2 w-2 rounded-full bg-red-500" />
                  {attentionItems.length} app{attentionItems.length !== 1 ? 's' : ''} with issues
                  <ChevronRight className={`h-3 w-3 transition-transform ${showAttention ? 'rotate-90' : ''}`} />
                </button>
              )}
              {disconnectedCount > 0 && (
                <button onClick={() => navigate('/clusters?status=disconnected')}
                  className="flex items-center gap-2 rounded-lg border border-red-200 bg-white px-3 py-1.5 text-xs text-red-700 hover:bg-red-50 dark:border-red-800 dark:bg-gray-800 dark:text-red-400">
                  <div className="h-2 w-2 rounded-full bg-red-500" />
                  {disconnectedCount} disconnected cluster{disconnectedCount !== 1 ? 's' : ''}
                </button>
              )}
              {versionDrifts.length > 0 && (
                <button onClick={() => navigate('/version-matrix?drift=true')}
                  className="flex items-center gap-2 rounded-lg border border-amber-200 bg-white px-3 py-1.5 text-xs text-amber-700 hover:bg-amber-50 dark:border-amber-800 dark:bg-gray-800 dark:text-amber-400">
                  <div className="h-2 w-2 rounded-full bg-amber-500" />
                  {versionDrifts.length} add-on{versionDrifts.length !== 1 ? 's' : ''} with drift
                </button>
              )}
            </div>
          </div>
          {/* Expandable detail panel */}
          {showAttention && attentionItems.length > 0 && (
            <div className="border-t border-amber-200 p-4 dark:border-amber-700">
              <div className="max-h-64 overflow-y-auto space-y-1.5">
                {attentionItems.map((item, i) => (
                  <div key={i} className="flex items-start gap-3 rounded-lg bg-white px-3 py-2 text-xs dark:bg-gray-800">
                    <div className={`mt-0.5 h-2.5 w-2.5 shrink-0 rounded-full ${
                      item.health === 'Error' ? 'bg-red-500' : item.health === 'Degraded' ? 'bg-red-400' : 'bg-amber-500'
                    }`} />
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <span className="font-medium text-gray-900 dark:text-gray-100">{item.app_name}</span>
                        <span className={`rounded px-1.5 py-0.5 text-[10px] font-medium ${
                          item.health === 'Error' ? 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'
                            : item.health === 'Degraded' ? 'bg-red-100 text-red-600 dark:bg-red-900/30 dark:text-red-400'
                              : 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400'
                        }`}>{item.health}</span>
                        {item.cluster && <span className="text-gray-400">on {item.cluster}</span>}
                      </div>
                      {item.error && (
                        <p className="mt-1 truncate text-gray-500 dark:text-gray-400" title={item.error}>
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
        <StatCard title="Available Add-ons" value={stats.addons.total_available} icon={<Package className="h-6 w-6" />}
          color="default" onClick={() => navigate('/addons')} />
        <StatCard title="Active Deployments" value={`${stats.addons.enabled_deployments}/${stats.addons.total_deployments}`}
          icon={<Rocket className="h-6 w-6" />} color="warning" onClick={() => navigate('/version-matrix')} />
      </div>

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

      {/* Bottom row: Quick Actions + Recent Activity + Version Drift */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        {/* Quick Actions */}
        <div className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm dark:border-gray-700 dark:bg-gray-800">
          <h3 className="mb-3 text-sm font-semibold text-gray-900 dark:text-gray-100">Quick Actions</h3>
          <div className="space-y-2">
            <button onClick={() => navigate('/upgrade')}
              className="flex w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left text-sm text-gray-700 transition-colors hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-gray-700">
              <ArrowUpCircle className="h-4 w-4 text-cyan-500" />
              <span>Check Upgrade Impact</span>
              <ChevronRight className="ml-auto h-4 w-4 text-gray-400" />
            </button>
            <button onClick={() => navigate('/migration')}
              className="flex w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left text-sm text-gray-700 transition-colors hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-gray-700">
              <GitPullRequest className="h-4 w-4 text-violet-500" />
              <span>Start Migration</span>
              <ChevronRight className="ml-auto h-4 w-4 text-gray-400" />
            </button>
            <button onClick={() => navigate('/observability')}
              className="flex w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left text-sm text-gray-700 transition-colors hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-gray-700">
              <Activity className="h-4 w-4 text-green-500" />
              <span>View Observability</span>
              <ChevronRight className="ml-auto h-4 w-4 text-gray-400" />
            </button>
          </div>
        </div>

        {/* Recent Sync Activity */}
        <div className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm dark:border-gray-700 dark:bg-gray-800">
          <div className="mb-3 flex items-center justify-between">
            <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">Recent Syncs</h3>
            <button onClick={() => navigate('/observability')} className="text-xs text-cyan-600 hover:text-cyan-700 dark:text-cyan-400">
              View all
            </button>
          </div>
          {recentSyncs.length === 0 ? (
            <p className="py-4 text-center text-xs text-gray-500">No recent sync activity</p>
          ) : (
            <div className="space-y-2">
              {recentSyncs.map((sync, i) => (
                <div key={i} className="flex items-center gap-3 text-xs">
                  <div className={`h-2 w-2 shrink-0 rounded-full ${sync.status === 'Synced' || sync.status === 'Succeeded' ? 'bg-green-500' : 'bg-amber-500'}`} />
                  <div className="min-w-0 flex-1">
                    <span className="font-medium text-gray-700 dark:text-gray-300">{sync.addon_name}</span>
                    <span className="text-gray-400"> on </span>
                    <span className="text-gray-500 dark:text-gray-400">{sync.cluster_name}</span>
                  </div>
                  <span className="shrink-0 text-gray-400 flex items-center gap-1">
                    <Clock className="h-3 w-3" />
                    {timeAgo(sync.timestamp)}
                  </span>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* Version Drift */}
        <div className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm dark:border-gray-700 dark:bg-gray-800">
          <div className="mb-3 flex items-center justify-between">
            <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">Version Drift</h3>
            <button onClick={() => navigate('/version-matrix')} className="text-xs text-cyan-600 hover:text-cyan-700 dark:text-cyan-400">
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
                  className="flex cursor-pointer items-center justify-between rounded-lg px-3 py-2 text-xs transition-colors hover:bg-gray-50 dark:hover:bg-gray-700">
                  <span className="font-medium text-gray-700 dark:text-gray-300">{d.addon}</span>
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

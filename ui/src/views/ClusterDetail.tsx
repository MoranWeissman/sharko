import { useState, useEffect, useMemo, useCallback } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import {
  ArrowLeft,
  List,
  CheckCircle,
  AlertTriangle,
  CloudOff,
  Eye,
  Ban,
  ChevronDown,
  ChevronUp,
  ExternalLink,
  Server,
  Cpu,
  WifiOff,
  MessageSquare,
  Tag,
} from 'lucide-react';
import { api } from '@/services/api';
import type { ClusterComparisonResponse, AddonComparisonStatus, ConfigDiffResponse } from '@/services/models';
import { StatCard } from '@/components/StatCard';
import { StatusBadge } from '@/components/StatusBadge';
import { LoadingState } from '@/components/LoadingState';
import { ErrorState } from '@/components/ErrorState';
import { YamlViewer } from '@/components/YamlViewer';

type StatusFilter =
  | 'all'
  | 'healthy'
  | 'with_issues'
  | 'missing_in_argocd'
  | 'untracked'
  | 'disabled_in_git';

function capitalizeAddonName(name: string): string {
  return name.charAt(0).toUpperCase() + name.slice(1);
}

function shouldTruncateIssues(issues: string[]): boolean {
  return issues.join(' ').length > 100;
}

export function ClusterDetail() {
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const [data, setData] = useState<ClusterComparisonResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [expandedRows, setExpandedRows] = useState<Set<string>>(new Set());
  const [activeTab, setActiveTab] = useState<'comparison' | 'config-overrides'>('comparison');
  const [configDiff, setConfigDiff] = useState<ConfigDiffResponse | null>(null);
  const [configDiffLoading, setConfigDiffLoading] = useState(false);
  const [configDiffError, setConfigDiffError] = useState<string | null>(null);
  const [clusterValuesYaml, setClusterValuesYaml] = useState<string | null>(null);
  const [nodeInfo, setNodeInfo] = useState<{ total: number; ready: number; not_ready: number } | null>(null);
  const [argocdBaseURL, setArgocdBaseURL] = useState<string>('');

  const fetchData = useCallback(async () => {
    if (!name) return;
    try {
      setLoading(true);
      setError(null);
      const [result, nodes, connections] = await Promise.all([
        api.getClusterComparison(name),
        api.getNodeInfo().catch(() => null),
        api.getConnections().catch(() => null),
      ]);
      setData(result);
      if (nodes && typeof nodes === 'object' && 'total' in nodes) {
        setNodeInfo(nodes as { total: number; ready: number; not_ready: number });
      }
      if (connections) {
        const active = connections.connections.find(
          c => c.name === connections.active_connection || c.is_active
        );
        if (active?.argocd_server_url) {
          setArgocdBaseURL(active.argocd_server_url.replace(/\/$/, ''));
        }
      }
    } catch (e: unknown) {
      setError(
        e instanceof Error
          ? e.message
          : `Failed to load comparison for cluster: ${name}`,
      );
    } finally {
      setLoading(false);
    }
  }, [name]);

  const fetchConfigDiff = useCallback(async () => {
    if (!name) return;
    try {
      setConfigDiffLoading(true);
      setConfigDiffError(null);
      const result = await api.getConfigDiff(name);
      setConfigDiff(result);
    } catch (e: unknown) {
      setConfigDiffError(
        e instanceof Error ? e.message : 'Failed to load config diff',
      );
    } finally {
      setConfigDiffLoading(false);
    }
  }, [name]);

  useEffect(() => {
    void fetchData();
  }, [fetchData]);

  useEffect(() => {
    if (activeTab === 'config-overrides' && !configDiff && !configDiffLoading) {
      void fetchConfigDiff();
    }
    if (activeTab === 'config-overrides' && clusterValuesYaml === null && name) {
      api
        .getClusterValues(name)
        .then((res) => setClusterValuesYaml(res.values_yaml))
        .catch(() => {
          // Cluster values file may not exist — that's OK
        });
    }
  }, [activeTab, configDiff, configDiffLoading, fetchConfigDiff, clusterValuesYaml, name]);

  const filteredAddons = useMemo(() => {
    if (!data) return [];
    const addons = data.addon_comparisons;
    if (statusFilter === 'all') return addons;

    return addons.filter((addon) => {
      switch (statusFilter) {
        case 'healthy':
          return addon.status === 'healthy';
        case 'with_issues':
          return ['progressing', 'unknown_health', 'unhealthy', 'unknown_state'].includes(
            addon.status ?? '',
          );
        case 'missing_in_argocd':
          return addon.status === 'missing_in_argocd';
        case 'untracked':
          return addon.status === 'untracked_in_argocd';
        case 'disabled_in_git':
          return addon.status === 'disabled_in_git';
        default:
          return true;
      }
    });
  }, [data, statusFilter]);

  const handleStatusFilter = (filter: StatusFilter) => {
    setStatusFilter(statusFilter === filter ? 'all' : filter);
  };

  const toggleExpanded = (addonName: string) => {
    setExpandedRows((prev) => {
      const next = new Set(prev);
      if (next.has(addonName)) {
        next.delete(addonName);
      } else {
        next.add(addonName);
      }
      return next;
    });
  };

  if (loading) {
    return <LoadingState message="Loading cluster details..." />;
  }

  if (error) {
    return <ErrorState message={error} onRetry={fetchData} />;
  }

  if (!data) return null;

  const allCount =
    data.total_healthy +
    data.total_with_issues +
    data.total_missing_in_argocd +
    data.total_untracked_in_argocd +
    data.total_disabled_in_git;

  const statItems: Array<{
    key: StatusFilter;
    title: string;
    value: number;
    color: 'default' | 'success' | 'error' | 'warning';
    icon: React.ReactNode;
  }> = [
    {
      key: 'all',
      title: 'All Addons',
      value: allCount,
      color: 'default',
      icon: <List className="h-5 w-5" />,
    },
    {
      key: 'healthy',
      title: 'Healthy',
      value: data.total_healthy,
      color: 'success',
      icon: <CheckCircle className="h-5 w-5" />,
    },
    {
      key: 'with_issues',
      title: 'With Issues',
      value: data.total_with_issues,
      color: 'error',
      icon: <AlertTriangle className="h-5 w-5" />,
    },
    {
      key: 'missing_in_argocd',
      title: 'Not Deployed',
      value: data.total_missing_in_argocd,
      color: 'warning',
      icon: <CloudOff className="h-5 w-5" />,
    },
    {
      key: 'untracked',
      title: 'Unmanaged',
      value: data.total_untracked_in_argocd,
      color: 'warning',
      icon: <Eye className="h-5 w-5" />,
    },
    {
      key: 'disabled_in_git',
      title: 'Not Enabled',
      value: data.total_disabled_in_git,
      color: 'default',
      icon: <Ban className="h-5 w-5" />,
    },
  ];

  return (
    <div className="space-y-6">
      {/* Back button */}
      <button
        type="button"
        onClick={() => navigate('/clusters')}
        className="inline-flex items-center gap-1.5 text-sm font-medium text-cyan-600 hover:text-cyan-800 dark:text-cyan-400 dark:hover:text-cyan-300"
      >
        <ArrowLeft className="h-4 w-4" />
        Back to Clusters Overview
      </button>

      {/* Heading + cluster meta */}
      <div>
        <h2 className="text-2xl font-bold text-gray-900 dark:text-gray-100">{data.cluster.name}</h2>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
          Kubernetes cluster managed by ArgoCD — deployed add-ons, health, and configuration overrides.
        </p>
      </div>

      {/* Cluster info stat cards */}
      <div className="flex flex-wrap gap-3">
        {data.cluster.server_version && (
          <div className="flex items-center gap-2 rounded-lg border border-gray-200 bg-white px-4 py-3 shadow-sm dark:border-gray-700 dark:bg-gray-800">
            <Tag className="h-4 w-4 text-cyan-500" />
            <div>
              <p className="text-xs text-gray-500 dark:text-gray-400">Cluster Version</p>
              <p className="font-mono text-sm font-semibold text-gray-900 dark:text-gray-100">{data.cluster.server_version}</p>
            </div>
          </div>
        )}
        {nodeInfo && nodeInfo.total > 0 && (
          <div className={`flex items-center gap-2 rounded-lg border px-4 py-3 shadow-sm ${
            nodeInfo.not_ready > 0
              ? 'border-red-200 bg-red-50 dark:border-red-700 dark:bg-red-900/20'
              : 'border-green-200 bg-green-50 dark:border-green-700 dark:bg-green-900/20'
          }`}>
            <Cpu className={`h-4 w-4 ${nodeInfo.not_ready > 0 ? 'text-red-500' : 'text-green-500'}`} />
            <div>
              <p className="text-xs text-gray-500 dark:text-gray-400">Nodes Ready</p>
              <p className={`text-sm font-semibold ${nodeInfo.not_ready > 0 ? 'text-red-700 dark:text-red-400' : 'text-green-700 dark:text-green-400'}`}>
                {nodeInfo.ready} / {nodeInfo.total}
                {nodeInfo.not_ready > 0 && (
                  <span className="ml-1.5 text-xs font-normal">({nodeInfo.not_ready} not ready)</span>
                )}
              </p>
            </div>
          </div>
        )}
        <div className="flex items-center gap-2 rounded-lg border border-gray-200 bg-white px-4 py-3 shadow-sm dark:border-gray-700 dark:bg-gray-800">
          <Server className="h-4 w-4 text-cyan-500" />
          <div>
            <p className="text-xs text-gray-500 dark:text-gray-400">Connection</p>
            <p className="text-sm font-semibold text-gray-900 dark:text-gray-100">
              {data.cluster_connection_state || 'Unknown'}
            </p>
          </div>
        </div>
      </div>

      {/* Connection status banner */}
      {data.cluster_connection_state && data.cluster_connection_state !== 'Successful' && (
        <div className="flex items-center justify-between rounded-xl border-2 border-red-300 bg-red-50 px-5 py-3 dark:border-red-700 dark:bg-red-900/20">
          <div className="flex items-center gap-2 text-red-700 dark:text-red-400">
            <WifiOff className="h-5 w-5 shrink-0" />
            <div>
              <span className="text-sm font-semibold">Cluster unreachable</span>
              <span className="ml-2 text-xs text-red-600 dark:text-red-400">({data.cluster_connection_state})</span>
              <p className="text-xs text-red-600 dark:text-red-400">Add-on health data below reflects the last known state and may be stale.</p>
            </div>
          </div>
          <button
            onClick={() => window.dispatchEvent(new CustomEvent('open-assistant', { detail: `Cluster ${name} is unreachable (${data.cluster_connection_state}). What could be wrong and how can I fix it?` }))}
            className="flex shrink-0 items-center gap-1.5 rounded-lg border border-red-200 bg-white px-3 py-1.5 text-xs text-red-700 hover:bg-red-50 dark:border-red-800 dark:bg-gray-800 dark:text-red-400"
          >
            <MessageSquare className="h-3.5 w-3.5" />
            Ask AI
          </button>
        </div>
      )}

      {/* Tabs */}
      <div className="flex gap-1 border-b border-gray-200 dark:border-gray-700">
        <button
          type="button"
          onClick={() => setActiveTab('comparison')}
          className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
            activeTab === 'comparison'
              ? 'border-cyan-500 text-cyan-600 dark:text-cyan-400'
              : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300'
          }`}
        >
          Add-ons
        </button>
        <button
          type="button"
          onClick={() => setActiveTab('config-overrides')}
          className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
            activeTab === 'config-overrides'
              ? 'border-cyan-500 text-cyan-600 dark:text-cyan-400'
              : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300'
          }`}
        >
          Values: Global vs. Cluster
        </button>
      </div>

      {activeTab === 'comparison' && (
        <>
          {/* Status filter cards — hide zero-count categories */}
          <div className="flex flex-wrap gap-4">
            {statItems
              .filter((item) => item.key === 'all' || item.value > 0)
              .map((item) => (
                <div key={item.key} className="min-w-[140px] flex-1">
                  <StatCard
                    title={item.title}
                    value={item.value}
                    icon={item.icon}
                    color={item.color}
                    selected={statusFilter === item.key}
                    onClick={() => handleStatusFilter(item.key)}
                  />
                </div>
              ))}
          </div>

          {/* Comparison table */}
          <div className="overflow-x-auto rounded-xl border border-gray-200 bg-white shadow-sm dark:border-gray-700 dark:bg-gray-800">
            <table className="w-full text-left text-sm">
              <thead className="border-b border-gray-200 bg-gray-50 text-xs uppercase text-gray-500 dark:border-gray-700 dark:bg-gray-900 dark:text-gray-400">
                <tr>
                  <th className="px-4 py-3">Status</th>
                  <th className="px-4 py-3">Addon Name</th>
                  <th className="px-4 py-3">Git Version</th>
                  <th className="px-4 py-3">ArgoCD Version</th>
                  <th className="px-4 py-3">Namespace</th>
                  <th className="px-4 py-3">Issues</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-200 dark:divide-gray-700">
                {filteredAddons.map((addon) => (
                  <ComparisonRow
                    key={addon.addon_name}
                    addon={addon}
                    isExpanded={expandedRows.has(addon.addon_name)}
                    onToggleExpand={() => toggleExpanded(addon.addon_name)}
                    argocdBaseURL={argocdBaseURL}
                  />
                ))}
                {filteredAddons.length === 0 && (
                  <tr>
                    <td
                      colSpan={6}
                      className="px-6 py-8 text-center text-gray-400 dark:text-gray-500"
                    >
                      No addons match the current filter.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </>
      )}

      {activeTab === 'config-overrides' && (
        <>
          {clusterValuesYaml && (
            <YamlViewer yaml={clusterValuesYaml} title="Cluster Values" />
          )}
          <ConfigOverridesPanel
            data={configDiff}
            loading={configDiffLoading}
            error={configDiffError}
            onRetry={fetchConfigDiff}
          />
        </>
      )}
    </div>
  );
}

/* ------------------------------------------------------------------ */

interface ComparisonRowProps {
  addon: AddonComparisonStatus;
  isExpanded: boolean;
  onToggleExpand: () => void;
  argocdBaseURL: string;
}

function ComparisonRow({ addon, isExpanded, onToggleExpand, argocdBaseURL }: ComparisonRowProps) {
  const allIssues = addon.issues;
  const isTruncated = shouldTruncateIssues(allIssues);
  const displayedIssues = isExpanded ? allIssues : allIssues.slice(0, 2);

  // An app is NOT OK if health is non-healthy OR there are issues
  const hasProblems = allIssues.length > 0
    || addon.argocd_health_status === 'Error'
    || addon.argocd_health_status === 'Degraded'
    || addon.status === 'with_issues'
    || addon.status === 'unknown_health'
    || addon.status === 'unknown_state';

  return (
    <tr className="hover:bg-gray-50 dark:hover:bg-gray-700">
      <td className="px-4 py-3">
        {addon.status ? (
          <StatusBadge status={addon.status} />
        ) : (
          <span className="text-gray-400 dark:text-gray-500">--</span>
        )}
      </td>
      <td className="px-4 py-3 font-medium text-gray-900 dark:text-gray-100">
        <div className="flex items-center gap-2">
          {capitalizeAddonName(addon.addon_name)}
          {addon.argocd_application_name && argocdBaseURL && (
            <a
              href={`${argocdBaseURL}/applications/${addon.argocd_application_name}`}
              target="_blank"
              rel="noopener noreferrer"
              onClick={(e) => e.stopPropagation()}
              className="text-gray-400 hover:text-cyan-600 dark:hover:text-cyan-400"
              title={`Open ${addon.argocd_application_name} in ArgoCD`}
            >
              <ExternalLink className="h-3.5 w-3.5" />
            </a>
          )}
        </div>
      </td>
      <td className="px-4 py-3 font-mono text-xs text-gray-600 dark:text-gray-400">
        {addon.has_version_override
          ? (addon.custom_version ?? addon.environment_version ?? addon.git_version ?? '--')
          : (addon.environment_version ?? addon.git_version ?? '--')}
      </td>
      <td className="px-4 py-3 font-mono text-xs text-gray-600 dark:text-gray-400">
        {addon.argocd_deployed_version ?? '--'}
      </td>
      <td className="px-4 py-3 text-gray-600 dark:text-gray-400">
        {addon.argocd_namespace ?? '--'}
      </td>
      <td className="px-4 py-3">
        {allIssues.length > 0 ? (
          <div>
            <ul className="space-y-0.5 text-xs text-gray-600 dark:text-gray-400">
              {displayedIssues.map((issue, i) => (
                <li key={i}>{issue}</li>
              ))}
            </ul>
            {isTruncated && (
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  onToggleExpand();
                }}
                className="mt-1 inline-flex items-center gap-0.5 text-xs font-medium text-cyan-600 hover:text-cyan-800 dark:text-cyan-400 dark:hover:text-cyan-300"
              >
                {isExpanded ? (
                  <>
                    <ChevronUp className="h-3 w-3" /> Show less
                  </>
                ) : (
                  <>
                    <ChevronDown className="h-3 w-3" /> Show more
                  </>
                )}
              </button>
            )}
          </div>
        ) : hasProblems ? (
          <span className="text-xs text-amber-600 dark:text-amber-400">
            {addon.argocd_health_status || addon.status || 'Unknown'}
          </span>
        ) : (
          <span className="text-xs text-green-600 dark:text-green-400">OK</span>
        )}
      </td>
    </tr>
  );
}

/* ------------------------------------------------------------------ */

interface ConfigOverridesPanelProps {
  data: ConfigDiffResponse | null;
  loading: boolean;
  error: string | null;
  onRetry: () => void;
}

function ConfigOverridesPanel({ data, loading, error, onRetry }: ConfigOverridesPanelProps) {
  if (loading) {
    return <LoadingState message="Loading config overrides..." />;
  }

  if (error) {
    return <ErrorState message={error} onRetry={onRetry} />;
  }

  if (!data) return null;

  const overriddenAddons = data.addon_diffs.filter((d) => d.has_overrides);

  if (overriddenAddons.length === 0) {
    return (
      <div className="rounded-xl border border-gray-200 bg-white p-8 text-center shadow-sm dark:border-gray-700 dark:bg-gray-800">
        <p className="text-gray-500 dark:text-gray-400">
          This cluster uses all global defaults — no per-cluster overrides found.
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Per-addon diffs — tree view only */}
      {overriddenAddons.map((entry) => (
        <div
          key={entry.addon_name}
          className="rounded-xl border border-gray-200 bg-white shadow-sm dark:border-gray-700 dark:bg-gray-800"
        >
          <div className="flex items-center gap-2 border-b border-gray-200 px-4 py-3 dark:border-gray-700">
            <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">
              {capitalizeAddonName(entry.addon_name)}
            </h3>
            <span className="inline-flex items-center rounded-full bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-800 dark:bg-amber-900/30 dark:text-amber-400">
              Custom overrides
            </span>
          </div>
          <div className="grid grid-cols-1 divide-y md:grid-cols-2 md:divide-x md:divide-y-0 divide-gray-200 dark:divide-gray-700">
            <div className="p-4">
              <YamlViewer yaml={entry.global_values || ''} title="Global Default" defaultView="tree" />
            </div>
            <div className="p-4">
              <YamlViewer yaml={entry.cluster_values || ''} title="Cluster Override" defaultView="tree" />
            </div>
          </div>
        </div>
      ))}
    </div>
  );
}

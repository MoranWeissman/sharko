import { useState, useEffect, useMemo, useCallback } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import {
  Server,
  CheckCircle,
  XCircle,
  HelpCircle,
  AlertTriangle,
  Search,
  X,
  Info,
  LayoutList,
  LayoutGrid,
} from 'lucide-react';
import { api } from '@/services/api';
import type { Cluster, ClusterHealthStats, ClustersResponse } from '@/services/models';
import { StatCard } from '@/components/StatCard';
import { ConnectionStatus } from '@/components/ConnectionStatus';
import { LoadingState } from '@/components/LoadingState';
import { ErrorState } from '@/components/ErrorState';

type StatusFilter =
  | 'all'
  | 'connected'
  | 'failed'
  | 'missing_from_argocd'
  | 'not_in_git';

interface Filters {
  name: string;
  versions: string[];
  connectionTypes: string[];
}

export function ClustersOverview() {
  const [allClusters, setAllClusters] = useState<Cluster[]>([]);
  const [healthStats, setHealthStats] = useState<ClusterHealthStats | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [searchParams] = useSearchParams();
  const initialStatus = searchParams.get('status');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>(
    initialStatus === 'disconnected' ? 'failed' : 'all'
  );
  const [filters, setFilters] = useState<Filters>({
    name: '',
    versions: [],
    connectionTypes: [],
  });
  const [viewMode, setViewMode] = useState<'list' | 'grid'>('list');
  const [versionDropdownOpen, setVersionDropdownOpen] = useState(false);
  const [connectionDropdownOpen, setConnectionDropdownOpen] = useState(false);
  const navigate = useNavigate();

  const fetchData = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const response: ClustersResponse = await api.getClusters();
      setAllClusters(response.clusters);
      setHealthStats(response.health_stats ?? null);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to load clusters');
      setAllClusters([]);
      setHealthStats(null);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetchData();
  }, [fetchData]);

  const availableVersions = useMemo(() => {
    const versions = new Set(
      allClusters.map((c) => c.server_version).filter(Boolean) as string[],
    );
    return Array.from(versions).sort();
  }, [allClusters]);

  const availableConnectionTypes = useMemo(() => {
    const types = new Set(
      allClusters.map((c) => c.connection_status).filter(Boolean) as string[],
    );
    return Array.from(types).sort();
  }, [allClusters]);

  const filteredClusters = useMemo(
    () =>
      allClusters
        .filter((cluster) => {
          if (statusFilter === 'all') return true;
          const cs = cluster.connection_status?.toLowerCase() ?? '';
          switch (statusFilter) {
            case 'connected':
              return cs === 'connected' || cs === 'successful';
            case 'failed':
              return cs === 'failed';
            case 'missing_from_argocd':
              return cs === 'missing';
            case 'not_in_git':
              return cs === 'not_in_git';
            default:
              return true;
          }
        })
        .filter((cluster) => {
          const nameMatch = cluster.name
            .toLowerCase()
            .includes(filters.name.toLowerCase());
          const versionMatch =
            filters.versions.length === 0 ||
            (cluster.server_version &&
              filters.versions.includes(cluster.server_version));
          const connectionMatch =
            filters.connectionTypes.length === 0 ||
            (cluster.connection_status &&
              filters.connectionTypes.includes(cluster.connection_status));
          return nameMatch && versionMatch && connectionMatch;
        }),
    [allClusters, statusFilter, filters],
  );

  const handleStatusFilter = (filter: StatusFilter) => {
    setStatusFilter(statusFilter === filter ? 'all' : filter);
  };

  const toggleVersion = (version: string) => {
    setFilters((prev) => ({
      ...prev,
      versions: prev.versions.includes(version)
        ? prev.versions.filter((v) => v !== version)
        : [...prev.versions, version],
    }));
  };

  const toggleConnectionType = (type: string) => {
    setFilters((prev) => ({
      ...prev,
      connectionTypes: prev.connectionTypes.includes(type)
        ? prev.connectionTypes.filter((t) => t !== type)
        : [...prev.connectionTypes, type],
    }));
  };

  const clearFilters = () => {
    setFilters({ name: '', versions: [], connectionTypes: [] });
    setStatusFilter('all');
  };

  if (loading) {
    return <LoadingState message="Loading clusters..." />;
  }

  if (error) {
    return <ErrorState message={error} onRetry={fetchData} />;
  }

  const totalClusters = healthStats
    ? healthStats.total_in_git + healthStats.not_in_git
    : allClusters.length;

  const statItems: Array<{
    key: StatusFilter;
    title: string;
    value: number;
    color: 'default' | 'success' | 'error' | 'warning';
    icon: React.ReactNode;
  }> = [
    {
      key: 'all',
      title: 'All Clusters',
      value: totalClusters,
      color: 'default',
      icon: <Server className="h-5 w-5" />,
    },
    {
      key: 'connected',
      title: 'Connected',
      value: healthStats?.connected ?? 0,
      color: 'success',
      icon: <CheckCircle className="h-5 w-5" />,
    },
    {
      key: 'failed',
      title: 'Failed',
      value: healthStats?.failed ?? 0,
      color: 'error',
      icon: <XCircle className="h-5 w-5" />,
    },
    {
      key: 'missing_from_argocd',
      title: 'Not Deployed',
      value: healthStats?.missing_from_argocd ?? 0,
      color: 'warning',
      icon: <HelpCircle className="h-5 w-5" />,
    },
    {
      key: 'not_in_git',
      title: 'Unmanaged',
      value: healthStats?.not_in_git ?? 0,
      color: 'warning',
      icon: <AlertTriangle className="h-5 w-5" />,
    },
  ];

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-bold text-gray-900 dark:text-gray-100">Clusters</h2>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
          All Kubernetes clusters managed by ArgoCD. Click a cluster to see deployed add-ons, health status, and configuration.
        </p>
      </div>

      {/* Health stat cards */}
      <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5">
        {statItems.map((item) => (
          <StatCard
            key={item.key}
            title={item.title}
            value={item.value}
            icon={item.icon}
            color={item.color}
            selected={statusFilter === item.key}
            onClick={() => handleStatusFilter(item.key)}
          />
        ))}
      </div>

      {/* Advanced filters */}
      <div className="rounded-lg border border-gray-200 bg-gray-50 p-4 dark:border-gray-700 dark:bg-gray-900">
        <div className="flex flex-wrap items-center gap-3">
          {/* Name search */}
          <div className="relative min-w-[200px] flex-1">
            <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
            <input
              type="text"
              placeholder="Filter by name..."
              value={filters.name}
              onChange={(e) =>
                setFilters((prev) => ({ ...prev, name: e.target.value }))
              }
              className="w-full rounded-md border border-gray-300 py-2 pl-10 pr-4 text-sm focus:border-cyan-500 focus:outline-none focus:ring-1 focus:ring-cyan-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:placeholder-gray-500"
            />
          </div>

          {/* Version multi-select */}
          <div className="relative">
            <button
              type="button"
              onClick={() => {
                setVersionDropdownOpen(!versionDropdownOpen);
                setConnectionDropdownOpen(false);
              }}
              className="rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:hover:bg-gray-700"
            >
              Version{filters.versions.length > 0 ? ` (${filters.versions.length})` : ''}
            </button>
            {versionDropdownOpen && (
              <div className="absolute left-0 top-full z-10 mt-1 min-w-[180px] rounded-md border border-gray-200 bg-white py-1 shadow-lg dark:border-gray-600 dark:bg-gray-800">
                {availableVersions.map((version) => (
                  <label
                    key={version}
                    className="flex cursor-pointer items-center gap-2 px-3 py-1.5 text-sm hover:bg-gray-50 dark:text-gray-200 dark:hover:bg-gray-700"
                  >
                    <input
                      type="checkbox"
                      checked={filters.versions.includes(version)}
                      onChange={() => toggleVersion(version)}
                      className="rounded border-gray-300 dark:border-gray-600"
                    />
                    {version}
                  </label>
                ))}
                {availableVersions.length === 0 && (
                  <p className="px-3 py-2 text-sm text-gray-400">No versions</p>
                )}
              </div>
            )}
          </div>

          {/* Connection status multi-select */}
          <div className="relative">
            <button
              type="button"
              onClick={() => {
                setConnectionDropdownOpen(!connectionDropdownOpen);
                setVersionDropdownOpen(false);
              }}
              className="rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:hover:bg-gray-700"
            >
              Connection Status{filters.connectionTypes.length > 0 ? ` (${filters.connectionTypes.length})` : ''}
            </button>
            {connectionDropdownOpen && (
              <div className="absolute left-0 top-full z-10 mt-1 min-w-[200px] rounded-md border border-gray-200 bg-white py-1 shadow-lg dark:border-gray-600 dark:bg-gray-800">
                {availableConnectionTypes.map((type) => (
                  <label
                    key={type}
                    className="flex cursor-pointer items-center gap-2 px-3 py-1.5 text-sm hover:bg-gray-50 dark:text-gray-200 dark:hover:bg-gray-700"
                  >
                    <input
                      type="checkbox"
                      checked={filters.connectionTypes.includes(type)}
                      onChange={() => toggleConnectionType(type)}
                      className="rounded border-gray-300 dark:border-gray-600"
                    />
                    {type}
                  </label>
                ))}
                {availableConnectionTypes.length === 0 && (
                  <p className="px-3 py-2 text-sm text-gray-400">No statuses</p>
                )}
              </div>
            )}
          </div>

          {/* Clear button */}
          {(filters.name || filters.versions.length > 0 || filters.connectionTypes.length > 0 || statusFilter !== 'all') && (
            <button
              type="button"
              onClick={clearFilters}
              className="rounded-md border border-gray-300 bg-white px-3 py-2 text-sm text-gray-600 hover:bg-gray-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
            >
              Clear all
            </button>
          )}

          {/* View mode toggle */}
          <div className="ml-auto flex items-center rounded-md border border-gray-300 dark:border-gray-600">
            <button
              type="button"
              onClick={() => setViewMode('list')}
              className={`rounded-l-md p-2 ${
                viewMode === 'list'
                  ? 'bg-cyan-600 text-white'
                  : 'bg-white text-gray-500 hover:bg-gray-50 dark:bg-gray-800 dark:text-gray-400 dark:hover:bg-gray-700'
              }`}
              aria-label="List view"
              title="List view"
            >
              <LayoutList className="h-4 w-4" />
            </button>
            <button
              type="button"
              onClick={() => setViewMode('grid')}
              className={`rounded-r-md p-2 ${
                viewMode === 'grid'
                  ? 'bg-cyan-600 text-white'
                  : 'bg-white text-gray-500 hover:bg-gray-50 dark:bg-gray-800 dark:text-gray-400 dark:hover:bg-gray-700'
              }`}
              aria-label="Grid view"
              title="Grid view"
            >
              <LayoutGrid className="h-4 w-4" />
            </button>
          </div>
        </div>

        {/* Active filter chips */}
        {(filters.versions.length > 0 || filters.connectionTypes.length > 0) && (
          <div className="mt-3 flex flex-wrap gap-2">
            {filters.versions.map((version) => (
              <span
                key={`v-${version}`}
                className="inline-flex items-center gap-1 rounded-full bg-cyan-100 px-2.5 py-0.5 text-xs font-medium text-cyan-700 dark:bg-cyan-900/30 dark:text-cyan-400"
              >
                {version}
                <button
                  type="button"
                  onClick={() => toggleVersion(version)}
                  className="ml-0.5 rounded-full p-0.5 hover:bg-cyan-200 dark:hover:bg-cyan-800"
                  aria-label={`Remove version filter ${version}`}
                >
                  <X className="h-3 w-3" />
                </button>
              </span>
            ))}
            {filters.connectionTypes.map((type) => (
              <span
                key={`c-${type}`}
                className="inline-flex items-center gap-1 rounded-full bg-purple-100 px-2.5 py-0.5 text-xs font-medium text-purple-700 dark:bg-purple-900/30 dark:text-purple-400"
              >
                {type}
                <button
                  type="button"
                  onClick={() => toggleConnectionType(type)}
                  className="ml-0.5 rounded-full p-0.5 hover:bg-purple-200 dark:hover:bg-purple-800"
                  aria-label={`Remove connection filter ${type}`}
                >
                  <X className="h-3 w-3" />
                </button>
              </span>
            ))}
          </div>
        )}
      </div>

      {/* Cluster list / grid */}
      {viewMode === 'list' ? (
        <div className="overflow-x-auto rounded-xl border border-gray-200 bg-white shadow-sm dark:border-gray-700 dark:bg-gray-800">
          <table className="w-full text-left text-sm">
            <thead className="border-b border-gray-200 bg-gray-50 text-xs uppercase text-gray-500 dark:border-gray-700 dark:bg-gray-900 dark:text-gray-400">
              <tr>
                <th className="px-6 py-3">Name</th>
                <th className="px-6 py-3">Connection Status</th>
                <th className="px-6 py-3">Cluster Version</th>
                <th className="px-6 py-3">Addons Installed</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-200 dark:divide-gray-700">
              {filteredClusters.map((cluster) => {
                const isInCluster = cluster.name === 'in-cluster';
                return (
                  <tr
                    key={cluster.name}
                    onClick={
                      isInCluster
                        ? undefined
                        : () => navigate(`/clusters/${cluster.name}`)
                    }
                    className={
                      isInCluster
                        ? 'cursor-not-allowed bg-gray-50 opacity-70 dark:bg-gray-900'
                        : 'cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-700'
                    }
                    title={
                      isInCluster
                        ? 'This is the local cluster where ArgoCD is running. It is managed directly and not defined in the Git repository.'
                        : undefined
                    }
                  >
                    <td className="px-6 py-3 font-medium text-gray-900 dark:text-gray-100">
                      <span className="inline-flex items-center gap-1.5">
                        {cluster.name}
                        {isInCluster && (
                          <Info className="h-4 w-4 text-blue-400" />
                        )}
                      </span>
                    </td>
                    <td className="px-6 py-3">
                      <ConnectionStatus
                        status={cluster.connection_status ?? 'unknown'}
                      />
                    </td>
                    <td className="px-6 py-3 font-mono text-xs text-gray-500 dark:text-gray-400">
                      {cluster.server_version ?? '--'}
                    </td>
                    <td className="px-6 py-3 text-gray-500 dark:text-gray-400">
                      {Object.values(cluster.labels).filter((v) => v === 'enabled').length}
                    </td>
                  </tr>
                );
              })}
              {filteredClusters.length === 0 && (
                <tr>
                  <td
                    colSpan={4}
                    className="px-6 py-8 text-center text-gray-400 dark:text-gray-500"
                  >
                    No clusters match the current filters.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {filteredClusters.map((cluster) => {
            const isInCluster = cluster.name === 'in-cluster';
            const addonCount = Object.values(cluster.labels).filter((v) => v === 'enabled').length;
            return (
              <div
                key={cluster.name}
                onClick={
                  isInCluster
                    ? undefined
                    : () => navigate(`/clusters/${cluster.name}`)
                }
                className={`rounded-lg border border-gray-200 bg-white p-4 shadow-sm transition-all dark:border-gray-700 dark:bg-gray-800 ${
                  isInCluster
                    ? 'cursor-not-allowed opacity-70'
                    : 'cursor-pointer hover:-translate-y-0.5 hover:border-cyan-400 hover:shadow-md dark:hover:border-cyan-500'
                }`}
                title={
                  isInCluster
                    ? 'This is the local cluster where ArgoCD is running. It is managed directly and not defined in the Git repository.'
                    : undefined
                }
              >
                <div className="mb-3 flex items-start justify-between">
                  <h3 className="text-lg font-bold text-gray-900 dark:text-gray-100">
                    <span className="inline-flex items-center gap-1.5">
                      {cluster.name}
                      {isInCluster && (
                        <Info className="h-4 w-4 text-blue-400" />
                      )}
                    </span>
                  </h3>
                </div>
                <div className="mb-2">
                  <ConnectionStatus
                    status={cluster.connection_status ?? 'unknown'}
                  />
                </div>
                <p className="mb-2 font-mono text-xs text-gray-500 dark:text-gray-400">
                  {cluster.server_version ? `v${cluster.server_version}` : '--'}
                </p>
                {addonCount > 0 && (
                  <span className="inline-flex items-center rounded-full bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-600 dark:bg-gray-700 dark:text-gray-300">
                    {addonCount} addon{addonCount !== 1 ? 's' : ''}
                  </span>
                )}
              </div>
            );
          })}
          {filteredClusters.length === 0 && (
            <div className="col-span-full rounded-lg border border-gray-200 bg-white p-8 text-center text-gray-400 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-500">
              No clusters match the current filters.
            </div>
          )}
        </div>
      )}
    </div>
  );
}

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
  Plus,
  Loader2,
  Wifi,
  WifiOff,
  GitMerge,
} from 'lucide-react';
import { api, registerCluster, testClusterConnection } from '@/services/api';
import type { Cluster, ClusterHealthStats, ClustersResponse, AddonCatalogResponse } from '@/services/models';
import { StatCard } from '@/components/StatCard';
import { ConnectionStatus } from '@/components/ConnectionStatus';
import { LoadingState } from '@/components/LoadingState';
import { ErrorState } from '@/components/ErrorState';
import { RoleGuard } from '@/components/RoleGuard';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog';

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

  // Test connection state per cluster
  const [testResults, setTestResults] = useState<Record<string, { reachable: boolean; server_version?: string; platform?: string; error?: string } | 'testing'>>({});

  // Adopt (start managing) state per cluster
  const [manageStatus, setManageStatus] = useState<Record<string, { loading?: boolean; success?: string; error?: string }>>({});

  // Add Cluster dialog state
  const [addClusterOpen, setAddClusterOpen] = useState(false);
  const [addClusterName, setAddClusterName] = useState('');
  const [addClusterRegion, setAddClusterRegion] = useState('');
  const [addClusterSubmitting, setAddClusterSubmitting] = useState(false);
  const [addClusterError, setAddClusterError] = useState<string | null>(null);
  const [addClusterResult, setAddClusterResult] = useState<string | null>(null);
  const [catalogAddons, setCatalogAddons] = useState<AddonCatalogResponse | null>(null);
  const [selectedAddons, setSelectedAddons] = useState<Record<string, boolean>>({});

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

  const openAddCluster = useCallback(() => {
    setAddClusterOpen(true);
    setAddClusterError(null);
    setAddClusterResult(null);
    setAddClusterName('');
    setAddClusterRegion('');
    setSelectedAddons({});
    // Fetch catalog for addon multi-select
    if (!catalogAddons) {
      api.getAddonCatalog().then(setCatalogAddons).catch(() => {});
    }
  }, [catalogAddons]);

  const handleAddCluster = useCallback(async () => {
    if (!addClusterName.trim()) return;
    setAddClusterSubmitting(true);
    setAddClusterError(null);
    setAddClusterResult(null);
    try {
      const result = await registerCluster({
        name: addClusterName.trim(),
        region: addClusterRegion.trim() || undefined,
        addons: Object.keys(selectedAddons).length > 0 ? selectedAddons : undefined,
      });
      const prUrl = result?.git?.pr_url || result?.pr_url || result?.pull_request_url;
      const merged = result?.git?.merged;
      const statusMsg = merged === false && prUrl
        ? `Cluster registered. PR pending merge: ${prUrl}`
        : prUrl
          ? `Cluster registered. PR: ${prUrl}`
          : 'Cluster registered successfully.';
      setAddClusterOpen(false);
      void fetchData();
      setAddClusterResult(statusMsg);
    } catch (e: unknown) {
      setAddClusterError(e instanceof Error ? e.message : 'Failed to register cluster');
    } finally {
      setAddClusterSubmitting(false);
    }
  }, [addClusterName, addClusterRegion, selectedAddons, fetchData]);

  const handleTestCluster = useCallback(async (name: string, e: React.MouseEvent) => {
    e.stopPropagation();
    setTestResults((prev) => ({ ...prev, [name]: 'testing' }));
    try {
      const result = await testClusterConnection(name);
      setTestResults((prev) => ({ ...prev, [name]: result }));
    } catch (err) {
      setTestResults((prev) => ({ ...prev, [name]: { reachable: false, error: err instanceof Error ? err.message : 'Failed' } }));
    }
  }, []);

  const handleAdoptCluster = useCallback(async (name: string, e: React.MouseEvent) => {
    e.stopPropagation();
    setManageStatus(prev => ({ ...prev, [name]: { loading: true } }));
    try {
      const result = await registerCluster({ name, addons: {} });
      const prUrl = result?.git?.pr_url || result?.pr_url || result?.pull_request_url;
      setManageStatus(prev => ({ ...prev, [name]: { success: prUrl || 'Cluster adopted' } }));
      void fetchData();
    } catch (err) {
      setManageStatus(prev => ({ ...prev, [name]: { error: err instanceof Error ? err.message : 'Failed to adopt cluster' } }));
    }
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

  // Split into managed (in git) and discovered (ArgoCD-only / unmanaged)
  const managedClusters = useMemo(
    () => filteredClusters.filter((c) => c.managed !== false && c.connection_status !== 'not_in_git'),
    [filteredClusters],
  );

  const discoveredClusters = useMemo(
    () => filteredClusters.filter((c) => c.managed === false || c.connection_status === 'not_in_git'),
    [filteredClusters],
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
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="text-2xl font-bold text-[#0a2a4a] dark:text-gray-100">Clusters</h2>
          <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-400">
            All Kubernetes clusters managed by ArgoCD. Click a cluster to see deployed addons, health status, and configuration.
          </p>
        </div>
        <RoleGuard adminOnly>
          <button
            type="button"
            onClick={openAddCluster}
            className="inline-flex shrink-0 items-center gap-2 rounded-lg bg-[#0a2a4a] px-5 py-2.5 text-sm font-semibold text-white shadow-sm hover:bg-[#0d3558] dark:bg-blue-700 dark:hover:bg-blue-600"
          >
            <Plus className="h-4 w-4" />
            Add Cluster
          </button>
        </RoleGuard>
      </div>

      {/* Add Cluster Dialog */}
      <Dialog open={addClusterOpen} onOpenChange={(v) => { if (!v) setAddClusterOpen(false) }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Register New Cluster</DialogTitle>
            <DialogDescription>Add a new cluster to the Git catalog.</DialogDescription>
          </DialogHeader>
          <div className="space-y-4 py-2">
            <div>
              <label className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300">
                Cluster Name <span className="text-red-500">*</span>
              </label>
              <input
                type="text"
                value={addClusterName}
                onChange={(e) => setAddClusterName(e.target.value)}
                placeholder="e.g. prod-us-east-1"
                className="w-full rounded-md border border-[#5a9dd0] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 dark:placeholder-[#5a8aaa]"
              />
            </div>
            <div>
              <label className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300">
                Region (optional)
              </label>
              <input
                type="text"
                value={addClusterRegion}
                onChange={(e) => setAddClusterRegion(e.target.value)}
                placeholder="e.g. us-east-1"
                className="w-full rounded-md border border-[#5a9dd0] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 dark:placeholder-[#5a8aaa]"
              />
            </div>
            {catalogAddons && catalogAddons.addons.length > 0 && (
              <div>
                <label className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300">
                  Enable Addons (optional)
                </label>
                <div className="max-h-40 space-y-1 overflow-y-auto rounded-md ring-2 ring-[#6aade0] p-2 dark:border-gray-700">
                  {catalogAddons.addons.map((addon) => (
                    <label
                      key={addon.addon_name}
                      className="flex cursor-pointer items-center gap-2 rounded px-1 py-0.5 text-sm hover:bg-[#d6eeff] dark:text-gray-300 dark:hover:bg-gray-700"
                    >
                      <input
                        type="checkbox"
                        checked={!!selectedAddons[addon.addon_name]}
                        onChange={(e) =>
                          setSelectedAddons((prev) => ({
                            ...prev,
                            [addon.addon_name]: e.target.checked,
                          }))
                        }
                        className="rounded border-[#5a9dd0] dark:border-gray-600"
                      />
                      <span className="capitalize">{addon.addon_name}</span>
                    </label>
                  ))}
                </div>
              </div>
            )}
            {addClusterError && (
              <p className="text-sm text-red-600 dark:text-red-400">{addClusterError}</p>
            )}
            {addClusterResult && (
              <p className="text-sm text-green-600 dark:text-green-400">{addClusterResult}</p>
            )}
          </div>
          <DialogFooter>
            <button
              type="button"
              onClick={() => setAddClusterOpen(false)}
              disabled={addClusterSubmitting}
              className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
            >
              {addClusterResult ? 'Close' : 'Cancel'}
            </button>
            {!addClusterResult && (
              <button
                type="button"
                onClick={handleAddCluster}
                disabled={!addClusterName.trim() || addClusterSubmitting}
                className="inline-flex items-center gap-2 rounded-md bg-teal-600 px-4 py-2 text-sm font-medium text-white hover:bg-teal-700 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-teal-700 dark:hover:bg-teal-600"
              >
                {addClusterSubmitting && <Loader2 className="h-4 w-4 animate-spin" />}
                Register Cluster
              </button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Registration success banner */}
      {addClusterResult && (
        <div className="flex items-center justify-between rounded-md border border-green-300 bg-green-50 px-4 py-2 text-sm text-green-800 dark:border-green-700 dark:bg-green-900/30 dark:text-green-300">
          <span>{addClusterResult}</span>
          <button
            type="button"
            onClick={() => setAddClusterResult(null)}
            className="ml-4 rounded p-0.5 hover:bg-green-100 dark:hover:bg-green-800"
            aria-label="Dismiss"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
      )}

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
      <div className="rounded-lg ring-2 ring-[#6aade0] bg-[#d0e8f8] p-4 dark:border-gray-700 dark:bg-gray-900">
        <div className="flex flex-wrap items-center gap-3">
          {/* Name search */}
          <div className="relative min-w-[200px] flex-1">
            <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[#3a6a8a]" />
            <input
              type="text"
              placeholder="Filter by name..."
              value={filters.name}
              onChange={(e) =>
                setFilters((prev) => ({ ...prev, name: e.target.value }))
              }
              className="w-full rounded-md border border-[#5a9dd0] bg-[#f0f7ff] py-2 pl-10 pr-4 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:placeholder-[#5a8aaa]"
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
              className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-2 text-sm text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:hover:bg-gray-700"
            >
              Version{filters.versions.length > 0 ? ` (${filters.versions.length})` : ''}
            </button>
            {versionDropdownOpen && (
              <div className="absolute left-0 top-full z-10 mt-1 min-w-[180px] rounded-md ring-2 ring-[#6aade0] bg-[#f0f7ff] py-1 shadow-lg dark:border-gray-600 dark:bg-gray-800">
                {availableVersions.map((version) => (
                  <label
                    key={version}
                    className="flex cursor-pointer items-center gap-2 px-3 py-1.5 text-sm hover:bg-[#d6eeff] dark:text-gray-200 dark:hover:bg-gray-700"
                  >
                    <input
                      type="checkbox"
                      checked={filters.versions.includes(version)}
                      onChange={() => toggleVersion(version)}
                      className="rounded border-[#5a9dd0] dark:border-gray-600"
                    />
                    {version}
                  </label>
                ))}
                {availableVersions.length === 0 && (
                  <p className="px-3 py-2 text-sm text-[#3a6a8a]">No versions</p>
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
              className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-2 text-sm text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:hover:bg-gray-700"
            >
              Connection Status{filters.connectionTypes.length > 0 ? ` (${filters.connectionTypes.length})` : ''}
            </button>
            {connectionDropdownOpen && (
              <div className="absolute left-0 top-full z-10 mt-1 min-w-[200px] rounded-md ring-2 ring-[#6aade0] bg-[#f0f7ff] py-1 shadow-lg dark:border-gray-600 dark:bg-gray-800">
                {availableConnectionTypes.map((type) => (
                  <label
                    key={type}
                    className="flex cursor-pointer items-center gap-2 px-3 py-1.5 text-sm hover:bg-[#d6eeff] dark:text-gray-200 dark:hover:bg-gray-700"
                  >
                    <input
                      type="checkbox"
                      checked={filters.connectionTypes.includes(type)}
                      onChange={() => toggleConnectionType(type)}
                      className="rounded border-[#5a9dd0] dark:border-gray-600"
                    />
                    {type}
                  </label>
                ))}
                {availableConnectionTypes.length === 0 && (
                  <p className="px-3 py-2 text-sm text-[#3a6a8a]">No statuses</p>
                )}
              </div>
            )}
          </div>

          {/* Clear button */}
          {(filters.name || filters.versions.length > 0 || filters.connectionTypes.length > 0 || statusFilter !== 'all') && (
            <button
              type="button"
              onClick={clearFilters}
              className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-2 text-sm text-[#1a4a6a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
            >
              Clear all
            </button>
          )}

          {/* View mode toggle */}
          <div className="ml-auto flex items-center rounded-md border border-[#5a9dd0] dark:border-gray-600">
            <button
              type="button"
              onClick={() => setViewMode('list')}
              className={`rounded-l-md p-2 ${
                viewMode === 'list'
                  ? 'bg-teal-600 text-white'
                  : 'bg-[#f0f7ff] text-[#2a5a7a] hover:bg-[#d6eeff] dark:bg-gray-800 dark:text-gray-400 dark:hover:bg-gray-700'
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
                  ? 'bg-teal-600 text-white'
                  : 'bg-[#f0f7ff] text-[#2a5a7a] hover:bg-[#d6eeff] dark:bg-gray-800 dark:text-gray-400 dark:hover:bg-gray-700'
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
                className="inline-flex items-center gap-1 rounded-full bg-teal-100 px-2.5 py-0.5 text-xs font-medium text-teal-700 dark:bg-teal-900/30 dark:text-teal-400"
              >
                {version}
                <button
                  type="button"
                  onClick={() => toggleVersion(version)}
                  className="ml-0.5 rounded-full p-0.5 hover:bg-teal-200 dark:hover:bg-teal-800"
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

      {/* Managed Clusters */}
      <div className="space-y-3">
        <h3 className="flex items-center gap-2 text-sm font-semibold text-[#0a2a4a] dark:text-gray-200">
          <Server className="h-4 w-4 text-teal-600" />
          Managed Clusters
          <span className="rounded-full bg-teal-100 px-2 py-0.5 text-xs font-medium text-teal-700 dark:bg-teal-900/30 dark:text-teal-400">
            {managedClusters.length}
          </span>
        </h3>

        {viewMode === 'list' ? (
          <div className="overflow-x-auto rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] shadow-sm dark:border-gray-700 dark:bg-gray-800">
            <table className="w-full text-left text-sm">
              <thead className="border-b border-[#6aade0] bg-[#d0e8f8] text-xs uppercase text-[#2a5a7a] dark:border-gray-700 dark:bg-gray-900 dark:text-gray-400">
                <tr>
                  <th className="px-6 py-3">Name</th>
                  <th className="px-6 py-3">Connection Status</th>
                  <th className="px-6 py-3">Cluster Version</th>
                  <th className="px-6 py-3">Addons</th>
                  <th className="px-6 py-3">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[#6aade0] dark:divide-gray-700">
                {managedClusters.map((cluster) => {
                  const isInCluster = cluster.name === 'in-cluster';
                  const testResult = testResults[cluster.name];
                  return (
                    <tr
                      key={cluster.name}
                      onClick={isInCluster ? undefined : () => navigate(`/clusters/${cluster.name}`)}
                      className={isInCluster
                        ? 'cursor-not-allowed bg-[#d0e8f8] opacity-70 dark:bg-gray-900'
                        : 'cursor-pointer hover:bg-[#d6eeff] dark:hover:bg-gray-700'}
                      title={isInCluster ? 'This is the local cluster where ArgoCD is running.' : undefined}
                    >
                      <td className="px-6 py-3 font-medium text-[#0a2a4a] dark:text-gray-100">
                        <span className="inline-flex items-center gap-1.5">
                          {cluster.name}
                          {isInCluster && <Info className="h-4 w-4 text-blue-400" />}
                        </span>
                      </td>
                      <td className="px-6 py-3">
                        <ConnectionStatus status={cluster.connection_status ?? 'unknown'} />
                      </td>
                      <td className="px-6 py-3 font-mono text-xs text-[#2a5a7a] dark:text-gray-400">
                        {cluster.server_version ?? '--'}
                      </td>
                      <td className="px-6 py-3 text-[#2a5a7a] dark:text-gray-400">
                        {Object.values(cluster.labels).filter((v) => v === 'enabled').length}
                      </td>
                      <td className="px-6 py-3" onClick={(e) => e.stopPropagation()}>
                        <div className="flex items-center gap-2">
                          <button
                            type="button"
                            onClick={(e) => handleTestCluster(cluster.name, e)}
                            disabled={testResult === 'testing'}
                            className="inline-flex items-center gap-1 rounded border border-[#5a9dd0] px-2 py-1 text-xs text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
                          >
                            {testResult === 'testing'
                              ? <Loader2 className="h-3 w-3 animate-spin" />
                              : <Wifi className="h-3 w-3" />}
                            Test
                          </button>
                          {testResult && testResult !== 'testing' && (
                            testResult.reachable
                              ? <span className="flex items-center gap-1 text-xs text-green-600 dark:text-green-400">
                                  <CheckCircle className="h-3 w-3" />
                                  {[testResult.server_version, testResult.platform].filter(Boolean).join(' — ') || 'Reachable'}
                                </span>
                              : <span className="flex items-center gap-1 text-xs text-red-500 dark:text-red-400">
                                  <WifiOff className="h-3 w-3" />
                                  Error: {testResult.error ?? 'Unreachable'}
                                </span>
                          )}
                        </div>
                      </td>
                    </tr>
                  );
                })}
                {managedClusters.length === 0 && (
                  <tr>
                    <td colSpan={5} className="px-6 py-8 text-center text-[#3a6a8a] dark:text-gray-500">
                      No managed clusters match the current filters.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        ) : (
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
            {managedClusters.map((cluster) => {
              const isInCluster = cluster.name === 'in-cluster';
              const addonCount = Object.values(cluster.labels).filter((v) => v === 'enabled').length;
              const testResult = testResults[cluster.name];
              return (
                <div
                  key={cluster.name}
                  onClick={isInCluster ? undefined : () => navigate(`/clusters/${cluster.name}`)}
                  className={`rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 shadow-sm transition-all dark:border-gray-700 dark:bg-gray-800 ${
                    isInCluster ? 'cursor-not-allowed opacity-70' : 'cursor-pointer hover:-translate-y-0.5 hover:shadow-md'
                  }`}
                >
                  <div className="mb-3 flex items-start justify-between">
                    <h3 className="text-sm font-bold text-[#0a2a4a] dark:text-gray-100">
                      <span className="inline-flex items-center gap-1.5">
                        {cluster.name}
                        {isInCluster && <Info className="h-4 w-4 text-blue-400" />}
                      </span>
                    </h3>
                    <button
                      type="button"
                      onClick={(e) => handleTestCluster(cluster.name, e)}
                      disabled={testResult === 'testing'}
                      className="inline-flex items-center gap-1 rounded border border-[#5a9dd0] px-2 py-1 text-xs text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
                    >
                      {testResult === 'testing' ? <Loader2 className="h-3 w-3 animate-spin" /> : <Wifi className="h-3 w-3" />}
                      Test
                    </button>
                  </div>
                  {testResult && testResult !== 'testing' && (
                    <div className="mb-2">
                      {testResult.reachable
                        ? <span className="flex items-center gap-1 text-xs text-green-600 dark:text-green-400">
                            <CheckCircle className="h-3 w-3" />
                            {[testResult.server_version, testResult.platform].filter(Boolean).join(' — ') || 'Reachable'}
                          </span>
                        : <span className="flex items-center gap-1 text-xs text-red-500 dark:text-red-400">
                            <WifiOff className="h-3 w-3" />
                            Error: {testResult.error ?? 'Unreachable'}
                          </span>
                      }
                    </div>
                  )}
                  <div className="mb-2">
                    <ConnectionStatus status={cluster.connection_status ?? 'unknown'} />
                  </div>
                  <p className="mb-2 font-mono text-xs text-[#2a5a7a] dark:text-gray-400">
                    {cluster.server_version ? `v${cluster.server_version}` : '--'}
                  </p>
                  {addonCount > 0 && (
                    <span className="inline-flex items-center rounded-full bg-[#d6eeff] px-2 py-0.5 text-xs font-medium text-[#1a4a6a] dark:bg-gray-700 dark:text-gray-300">
                      {addonCount} addon{addonCount !== 1 ? 's' : ''}
                    </span>
                  )}
                </div>
              );
            })}
            {managedClusters.length === 0 && (
              <div className="col-span-full rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-8 text-center text-[#3a6a8a] dark:border-gray-700 dark:bg-gray-800 dark:text-gray-500">
                No managed clusters match the current filters.
              </div>
            )}
          </div>
        )}
      </div>

      {/* Discovered (ArgoCD-only) Clusters */}
      {discoveredClusters.length > 0 && (
        <div className="space-y-3">
          <h3 className="flex items-center gap-2 text-sm font-semibold text-[#0a2a4a] dark:text-gray-200">
            <AlertTriangle className="h-4 w-4 text-amber-500" />
            Discovered (ArgoCD)
            <span className="rounded-full bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-400">
              {discoveredClusters.length}
            </span>
            <span className="text-xs font-normal text-[#3a6a8a] dark:text-gray-500">
              — present in ArgoCD but not yet managed by Sharko
            </span>
          </h3>

          {viewMode === 'list' ? (
            <div className="overflow-x-auto rounded-xl ring-2 ring-amber-200 bg-[#fffbf0] shadow-sm dark:border-amber-800 dark:bg-gray-800">
              <table className="w-full text-left text-sm">
                <thead className="border-b border-amber-200 bg-amber-50 text-xs uppercase text-amber-700 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-500">
                  <tr>
                    <th className="px-6 py-3">Name</th>
                    <th className="px-6 py-3">Status</th>
                    <th className="px-6 py-3">Version</th>
                    <th className="px-6 py-3">Actions</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-amber-100 dark:divide-amber-900/30">
                  {discoveredClusters.map((cluster) => {
                    const testResult = testResults[cluster.name];
                    const ms = manageStatus[cluster.name];
                    return (
                      <tr key={cluster.name} className="hover:bg-amber-50/60 dark:hover:bg-amber-950/20">
                        <td className="px-6 py-3 font-medium text-[#0a2a4a] dark:text-gray-100">
                          {cluster.name}
                        </td>
                        <td className="px-6 py-3">
                          <ConnectionStatus status={cluster.connection_status ?? 'unknown'} />
                        </td>
                        <td className="px-6 py-3 font-mono text-xs text-[#2a5a7a] dark:text-gray-400">
                          {cluster.server_version ?? '--'}
                        </td>
                        <td className="px-6 py-3">
                          <div className="flex flex-col gap-1">
                            <div className="flex items-center gap-2">
                              <button
                                type="button"
                                onClick={(e) => handleTestCluster(cluster.name, e)}
                                disabled={testResult === 'testing'}
                                className="inline-flex items-center gap-1 rounded border border-[#5a9dd0] px-2 py-1 text-xs text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
                              >
                                {testResult === 'testing' ? <Loader2 className="h-3 w-3 animate-spin" /> : <Wifi className="h-3 w-3" />}
                                Test
                              </button>
                              {testResult && testResult !== 'testing' && (
                                testResult.reachable
                                  ? <span className="flex items-center gap-1 text-xs text-green-600 dark:text-green-400">
                                      <CheckCircle className="h-3 w-3" />
                                      {[testResult.server_version, testResult.platform].filter(Boolean).join(' — ') || 'Reachable'}
                                    </span>
                                  : <span className="flex items-center gap-1 text-xs text-red-500 dark:text-red-400">
                                      <WifiOff className="h-3 w-3" />
                                      Error: {testResult.error ?? 'Unreachable'}
                                    </span>
                              )}
                              <RoleGuard adminOnly>
                                <button
                                  type="button"
                                  onClick={(e) => handleAdoptCluster(cluster.name, e)}
                                  disabled={!!ms?.loading}
                                  className="inline-flex items-center gap-1 rounded bg-teal-600 px-2 py-1 text-xs font-medium text-white hover:bg-teal-700 disabled:opacity-50"
                                >
                                  {ms?.loading ? <Loader2 className="h-3 w-3 animate-spin" /> : <GitMerge className="h-3 w-3" />}
                                  Start Managing
                                </button>
                              </RoleGuard>
                            </div>
                            {ms?.success && (
                              <span className="text-xs text-green-700 dark:text-green-400">
                                Cluster adopted!{ms.success !== 'Cluster adopted' ? <> PR: <a href={ms.success} target="_blank" rel="noopener noreferrer" className="underline">{ms.success}</a></> : ''}
                              </span>
                            )}
                            {ms?.error && (
                              <span className="text-xs text-red-600 dark:text-red-400">{ms.error}</span>
                            )}
                          </div>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          ) : (
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
              {discoveredClusters.map((cluster) => {
                const testResult = testResults[cluster.name];
                const ms = manageStatus[cluster.name];
                return (
                  <div key={cluster.name} className="rounded-lg ring-2 ring-amber-200 bg-[#fffbf0] p-4 shadow-sm dark:border-amber-800 dark:bg-gray-800">
                    <div className="mb-2 flex items-center justify-between">
                      <h3 className="text-sm font-bold text-[#0a2a4a] dark:text-gray-100">{cluster.name}</h3>
                      <button
                        type="button"
                        onClick={(e) => handleTestCluster(cluster.name, e)}
                        disabled={testResult === 'testing'}
                        className="inline-flex items-center gap-1 rounded border border-[#5a9dd0] px-2 py-1 text-xs text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:text-gray-300"
                      >
                        {testResult === 'testing' ? <Loader2 className="h-3 w-3 animate-spin" /> : <Wifi className="h-3 w-3" />}
                        Test
                      </button>
                    </div>
                    {testResult && testResult !== 'testing' && (
                      <div className="mb-2">
                        {testResult.reachable
                          ? <span className="flex items-center gap-1 text-xs text-green-600 dark:text-green-400">
                              <CheckCircle className="h-3 w-3" />
                              {[testResult.server_version, testResult.platform].filter(Boolean).join(' — ') || 'Reachable'}
                            </span>
                          : <span className="flex items-center gap-1 text-xs text-red-500 dark:text-red-400">
                              <WifiOff className="h-3 w-3" />
                              Error: {testResult.error ?? 'Unreachable'}
                            </span>
                        }
                      </div>
                    )}
                    <div className="mb-3">
                      <ConnectionStatus status={cluster.connection_status ?? 'unknown'} />
                    </div>
                    <p className="mb-3 font-mono text-xs text-[#2a5a7a] dark:text-gray-400">
                      {cluster.server_version ? `v${cluster.server_version}` : '--'}
                    </p>
                    <RoleGuard adminOnly>
                      <button
                        type="button"
                        onClick={(e) => handleAdoptCluster(cluster.name, e)}
                        disabled={!!ms?.loading}
                        className="inline-flex w-full items-center justify-center gap-1 rounded bg-teal-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-teal-700 disabled:opacity-50"
                      >
                        {ms?.loading ? <Loader2 className="h-3 w-3 animate-spin" /> : <GitMerge className="h-3 w-3" />}
                        Start Managing
                      </button>
                    </RoleGuard>
                    {ms?.success && (
                      <p className="mt-2 text-xs text-green-700 dark:text-green-400">
                        Cluster adopted!{ms.success !== 'Cluster adopted' ? <> PR: <a href={ms.success} target="_blank" rel="noopener noreferrer" className="underline">{ms.success}</a></> : ''}
                      </p>
                    )}
                    {ms?.error && (
                      <p className="mt-2 text-xs text-red-600 dark:text-red-400">{ms.error}</p>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
export default ClustersOverview

import { useState, useEffect, useMemo, useCallback, useContext } from 'react';
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
  Eye,
  ScanSearch,
  Unlink,
} from 'lucide-react';
import { api, registerCluster, discoverEKSClusters, testClusterConnection, unadoptCluster } from '@/services/api';
import type {
  Cluster,
  ClusterHealthStats,
  ClusterProvider,
  ClustersResponse,
  AddonCatalogResponse,
  DiscoveredClusterItem,
  DryRunResult,
  RegisterClusterResult,
} from '@/services/models';
import { AuthContext } from '@/hooks/useAuth';
import { StatCard } from '@/components/StatCard';
import { ConnectionStatus } from '@/components/ConnectionStatus';
import { LoadingState } from '@/components/LoadingState';
import { ErrorState } from '@/components/ErrorState';
import { RoleGuard } from '@/components/RoleGuard';
import { StatusBadge, isClusterStatus } from '@/components/StatusBadge';
import { ClusterStatusLegend } from '@/components/ClusterStatusLegend';
import { DiagnoseModal } from '@/components/DiagnoseModal';
import { ArgoCDStatusBanner } from '@/components/ArgoCDStatusBanner';
import { AdoptClustersDialog } from '@/components/AdoptClustersDialog';
import { ConfirmationModal } from '@/components/ConfirmationModal';
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
  const [testResults, setTestResults] = useState<Record<string, { reachable?: boolean; success?: boolean; server_version?: string; platform?: string; error?: string; error_message?: string; suggestions?: string[] } | 'testing'>>({});

  // Adopt (start managing) state per cluster (populated by AdoptClustersDialog via refresh)
  const [manageStatus] = useState<Record<string, { loading?: boolean; success?: string; error?: string }>>({});

  // Diagnose modal state
  const [diagnoseCluster, setDiagnoseCluster] = useState<string | null>(null);

  // Adopt dialog state
  const [adoptDialogOpen, setAdoptDialogOpen] = useState(false);
  const [adoptDialogClusters, setAdoptDialogClusters] = useState<Cluster[]>([]);
  const [selectedDiscoveredForAdopt, setSelectedDiscoveredForAdopt] = useState<Record<string, boolean>>({});

  // Un-adopt state
  const [unadoptTarget, setUnadoptTarget] = useState<string | null>(null);
  const [unadoptLoading, setUnadoptLoading] = useState(false);
  const [unadoptResult, setUnadoptResult] = useState<{ success?: string; error?: string } | null>(null);

  // ArgoCD unreachable detection
  const [argoCDUnreachable, setArgoCDUnreachable] = useState(false);

  // Auth context for role-based auto-merge logic
  const authCtx = useContext(AuthContext);

  // Add Cluster dialog state
  const [addClusterOpen, setAddClusterOpen] = useState(false);
  const [addClusterName, setAddClusterName] = useState('');
  const [addClusterRegion, setAddClusterRegion] = useState('');
  const [addClusterRoleArn, setAddClusterRoleArn] = useState('');
  const [addClusterSecretPath, setAddClusterSecretPath] = useState('');
  const [addClusterSubmitting, setAddClusterSubmitting] = useState(false);
  const [addClusterError, setAddClusterError] = useState<string | null>(null);
  const [addClusterResult, setAddClusterResult] = useState<RegisterClusterResult | null>(null);
  const [addClusterResultMsg, setAddClusterResultMsg] = useState<string | null>(null);
  const [catalogAddons, setCatalogAddons] = useState<AddonCatalogResponse | null>(null);
  const [selectedAddons, setSelectedAddons] = useState<Record<string, boolean>>({});

  // Provider selection
  const [provider, setProvider] = useState<ClusterProvider>('eks');

  // Discovery mode
  const [registrationMode, setRegistrationMode] = useState<'direct' | 'discovery'>('direct');
  const [discoveryRoleArns, setDiscoveryRoleArns] = useState('');
  const [discoveryRegion, setDiscoveryRegion] = useState('');
  const [discovering, setDiscovering] = useState(false);
  const [discoveryError, setDiscoveryError] = useState<string | null>(null);
  const [discoveredItems, setDiscoveredItems] = useState<DiscoveredClusterItem[]>([]);
  const [selectedDiscovered, setSelectedDiscovered] = useState<Record<string, boolean>>({});

  // Auto-merge checkbox
  const [autoMerge, setAutoMerge] = useState(false);

  // Dry-run preview
  const [dryRunResult, setDryRunResult] = useState<DryRunResult | null>(null);
  const [dryRunLoading, setDryRunLoading] = useState(false);

  const fetchData = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const response: ClustersResponse = await api.getClusters();
      setAllClusters(response.clusters);
      setHealthStats(response.health_stats ?? null);
      // Detect ArgoCD unreachable: if all clusters have failed/unknown status or response is empty
      const hasArgoError = response.clusters.length === 0 ||
        (response.clusters.length > 0 && response.clusters.every(
          (c) => !c.connection_status || c.connection_status === 'Failed' || c.connection_status === 'unknown'
        ));
      setArgoCDUnreachable(hasArgoError && response.clusters.length > 0);
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
    setAddClusterResultMsg(null);
    setAddClusterName('');
    setAddClusterRegion('');
    setAddClusterRoleArn('');
    setAddClusterSecretPath('');
    setSelectedAddons({});
    setProvider('eks');
    setRegistrationMode('direct');
    setDiscoveryRoleArns('');
    setDiscoveryRegion('');
    setDiscovering(false);
    setDiscoveryError(null);
    setDiscoveredItems([]);
    setSelectedDiscovered({});
    setAutoMerge(false);
    setDryRunResult(null);
    setDryRunLoading(false);
    // Fetch catalog for addon multi-select
    if (!catalogAddons) {
      api.getAddonCatalog().then(setCatalogAddons).catch(() => {});
    }
  }, [catalogAddons]);

  // Determine if auto-merge checkbox should be disabled
  const isAutoMergeDisabled = authCtx?.role === 'operator' || authCtx?.role === 'viewer';

  const handleDiscoverClusters = useCallback(async () => {
    const arns = discoveryRoleArns.split(',').map(a => a.trim()).filter(Boolean);
    if (arns.length === 0) return;
    setDiscovering(true);
    setDiscoveryError(null);
    setDiscoveredItems([]);
    setSelectedDiscovered({});
    try {
      const result = await discoverEKSClusters({
        role_arns: arns,
        region: discoveryRegion.trim() || undefined,
      });
      setDiscoveredItems(result.clusters || []);
      if (result.errors && result.errors.length > 0) {
        setDiscoveryError(result.errors.join('; '));
      }
    } catch (e: unknown) {
      setDiscoveryError(e instanceof Error ? e.message : 'Discovery failed');
    } finally {
      setDiscovering(false);
    }
  }, [discoveryRoleArns, discoveryRegion]);

  const handleDryRun = useCallback(async () => {
    const clusterName = registrationMode === 'direct' ? addClusterName.trim() : '';
    if (registrationMode === 'direct' && !clusterName) return;
    setDryRunLoading(true);
    setDryRunResult(null);
    setAddClusterError(null);
    try {
      const result = await registerCluster({
        name: clusterName || 'dry-run-preview',
        region: addClusterRegion.trim() || undefined,
        secret_path: addClusterSecretPath.trim() || undefined,
        provider,
        role_arn: addClusterRoleArn.trim() || undefined,
        auto_merge: autoMerge,
        addons: Object.keys(selectedAddons).length > 0 ? selectedAddons : undefined,
        dry_run: true,
      });
      if (result?.dry_run) {
        setDryRunResult(result.dry_run);
      }
    } catch (e: unknown) {
      setAddClusterError(e instanceof Error ? e.message : 'Dry run failed');
    } finally {
      setDryRunLoading(false);
    }
  }, [registrationMode, addClusterName, addClusterRegion, addClusterRoleArn, addClusterSecretPath, provider, autoMerge, selectedAddons]);

  const handleAddCluster = useCallback(async () => {
    if (registrationMode === 'direct' && !addClusterName.trim()) return;
    setAddClusterSubmitting(true);
    setAddClusterError(null);
    setAddClusterResult(null);
    setAddClusterResultMsg(null);
    try {
      if (registrationMode === 'discovery') {
        // Register all selected discovered clusters
        const selected = discoveredItems.filter(c => selectedDiscovered[c.name]);
        if (selected.length === 0) return;
        const errors: string[] = [];
        let lastResult: RegisterClusterResult | null = null;
        for (const cluster of selected) {
          try {
            lastResult = await registerCluster({
              name: cluster.name,
              region: cluster.region,
              provider,
              role_arn: cluster.arn || undefined,
              auto_merge: autoMerge,
              addons: Object.keys(selectedAddons).length > 0 ? selectedAddons : undefined,
            });
          } catch (e: unknown) {
            errors.push(`${cluster.name}: ${e instanceof Error ? e.message : 'Failed'}`);
          }
        }
        if (errors.length > 0 && errors.length < selected.length) {
          // Partial success
          setAddClusterResultMsg(`Registered ${selected.length - errors.length} of ${selected.length} clusters. Errors: ${errors.join('; ')}`);
          setAddClusterResult({ status: 'partial', partial: true, errors });
        } else if (errors.length > 0) {
          setAddClusterError(errors.join('; '));
          return;
        } else {
          setAddClusterResultMsg(`${selected.length} cluster(s) registered successfully.`);
          setAddClusterResult(lastResult);
        }
        setAddClusterOpen(false);
        void fetchData();
      } else {
        // Direct registration
        const result = await registerCluster({
          name: addClusterName.trim(),
          region: addClusterRegion.trim() || undefined,
          secret_path: addClusterSecretPath.trim() || undefined,
          provider,
          role_arn: addClusterRoleArn.trim() || undefined,
          auto_merge: autoMerge,
          addons: Object.keys(selectedAddons).length > 0 ? selectedAddons : undefined,
        });
        const prUrl = result?.git?.pr_url || result?.pr_url || result?.pull_request_url;
        const merged = result?.git?.merged ?? autoMerge;
        if (merged && !prUrl) {
          setAddClusterResultMsg('Cluster registered successfully.');
        } else if (prUrl) {
          setAddClusterResultMsg(prUrl);
        } else {
          setAddClusterResultMsg('Cluster registered successfully.');
        }
        setAddClusterResult(result);
        setAddClusterOpen(false);
        void fetchData();
      }
    } catch (e: unknown) {
      setAddClusterError(e instanceof Error ? e.message : 'Failed to register cluster');
    } finally {
      setAddClusterSubmitting(false);
    }
  }, [addClusterName, addClusterRegion, addClusterRoleArn, addClusterSecretPath, provider, autoMerge, selectedAddons, fetchData, registrationMode, discoveredItems, selectedDiscovered]);

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

  const handleOpenAdoptDialog = useCallback((clusters: Cluster[]) => {
    setAdoptDialogClusters(clusters);
    setAdoptDialogOpen(true);
  }, []);

  const handleAdoptSuccess = useCallback(() => {
    setSelectedDiscoveredForAdopt({});
    void fetchData();
  }, [fetchData]);

  const handleUnadopt = useCallback(async () => {
    if (!unadoptTarget) return;
    setUnadoptLoading(true);
    setUnadoptResult(null);
    try {
      const result = await unadoptCluster(unadoptTarget);
      setUnadoptResult({ success: result.pr_url || 'Cluster un-adopted successfully.' });
      setUnadoptTarget(null);
      void fetchData();
    } catch (err) {
      setUnadoptResult({ error: err instanceof Error ? err.message : 'Un-adopt failed' });
      setUnadoptTarget(null);
    } finally {
      setUnadoptLoading(false);
    }
  }, [unadoptTarget, fetchData]);

  const toggleDiscoveredSelection = useCallback((name: string) => {
    setSelectedDiscoveredForAdopt((prev) => ({
      ...prev,
      [name]: !prev[name],
    }));
  }, []);

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

  const handleAdoptSelected = useCallback(() => {
    const selected = discoveredClusters.filter((c) => selectedDiscoveredForAdopt[c.name]);
    if (selected.length === 0) return;
    handleOpenAdoptDialog(selected);
  }, [discoveredClusters, selectedDiscoveredForAdopt, handleOpenAdoptDialog]);

  const toggleAllDiscovered = useCallback((checked: boolean) => {
    const next: Record<string, boolean> = {};
    discoveredClusters.forEach((c) => { next[c.name] = checked; });
    setSelectedDiscoveredForAdopt(next);
  }, [discoveredClusters]);

  const selectedDiscoveredCount = useMemo(
    () => discoveredClusters.filter((c) => selectedDiscoveredForAdopt[c.name]).length,
    [discoveredClusters, selectedDiscoveredForAdopt],
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

      {/* ArgoCD Status Banner */}
      <ArgoCDStatusBanner visible={argoCDUnreachable} />

      {/* Cluster Status Legend */}
      <ClusterStatusLegend />

      {/* Diagnose Modal */}
      <DiagnoseModal
        clusterName={diagnoseCluster ?? ''}
        open={diagnoseCluster !== null}
        onClose={() => setDiagnoseCluster(null)}
      />

      {/* Add Cluster Dialog */}
      <Dialog open={addClusterOpen} onOpenChange={(v) => { if (!v) setAddClusterOpen(false) }}>
        <DialogContent className="max-w-2xl max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>Register New Cluster</DialogTitle>
            <DialogDescription>Add a new cluster to the Git catalog.</DialogDescription>
          </DialogHeader>
          <div className="space-y-4 py-2">
            {/* Provider Selection */}
            <div>
              <label className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300">
                Provider
              </label>
              <select
                value={provider}
                onChange={(e) => setProvider(e.target.value as ClusterProvider)}
                className="w-full rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100"
              >
                <option value="eks">Amazon EKS</option>
                <option value="gke" disabled>Google GKE (coming soon)</option>
                <option value="aks" disabled>Azure AKS (coming soon)</option>
                <option value="generic" disabled>Generic K8s (coming soon)</option>
              </select>
            </div>

            {/* Registration Mode Toggle */}
            <div>
              <label className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300">
                Registration Mode
              </label>
              <div className="flex rounded-md ring-2 ring-[#6aade0] dark:ring-gray-700">
                <button
                  type="button"
                  onClick={() => setRegistrationMode('direct')}
                  className={`flex-1 rounded-l-md px-4 py-2 text-sm font-medium transition-colors ${
                    registrationMode === 'direct'
                      ? 'bg-teal-600 text-white'
                      : 'bg-[#f0f7ff] text-[#2a5a7a] hover:bg-[#d6eeff] dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700'
                  }`}
                >
                  Direct
                </button>
                <button
                  type="button"
                  onClick={() => setRegistrationMode('discovery')}
                  className={`flex-1 rounded-r-md px-4 py-2 text-sm font-medium transition-colors ${
                    registrationMode === 'discovery'
                      ? 'bg-teal-600 text-white'
                      : 'bg-[#f0f7ff] text-[#2a5a7a] hover:bg-[#d6eeff] dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700'
                  }`}
                >
                  <span className="inline-flex items-center gap-1"><ScanSearch className="h-4 w-4" /> Discovery</span>
                </button>
              </div>
            </div>

            {registrationMode === 'direct' ? (
              <>
                {/* Direct mode fields */}
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
                <div>
                  <label className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300">
                    Role ARN (optional)
                  </label>
                  <input
                    type="text"
                    value={addClusterRoleArn}
                    onChange={(e) => setAddClusterRoleArn(e.target.value)}
                    placeholder="e.g. arn:aws:iam::123456789012:role/sharko-access"
                    className="w-full rounded-md border border-[#5a9dd0] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 dark:placeholder-[#5a8aaa]"
                  />
                </div>
                <div>
                  <label className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300">
                    Secret Path (optional)
                  </label>
                  <input
                    type="text"
                    value={addClusterSecretPath}
                    onChange={(e) => setAddClusterSecretPath(e.target.value)}
                    placeholder="Override AWS SM secret name (e.g., k8s-my-cluster)"
                    className="w-full rounded-md border border-[#5a9dd0] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 dark:placeholder-[#5a8aaa]"
                  />
                  <p className="mt-1 text-xs text-[#5a8aaa] dark:text-gray-500">
                    Leave empty to use cluster name as the secret key
                  </p>
                </div>
              </>
            ) : (
              <>
                {/* Discovery mode */}
                <div>
                  <label className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300">
                    Role ARNs <span className="text-red-500">*</span>
                  </label>
                  <input
                    type="text"
                    value={discoveryRoleArns}
                    onChange={(e) => setDiscoveryRoleArns(e.target.value)}
                    placeholder="Comma-separated: arn:aws:iam::111:role/a, arn:aws:iam::222:role/b"
                    className="w-full rounded-md border border-[#5a9dd0] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 dark:placeholder-[#5a8aaa]"
                  />
                  <p className="mt-1 text-xs text-[#5a8aaa] dark:text-gray-500">
                    Enter one or more AWS IAM Role ARNs to scan for EKS clusters
                  </p>
                </div>
                <div>
                  <label className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300">
                    Region (optional)
                  </label>
                  <input
                    type="text"
                    value={discoveryRegion}
                    onChange={(e) => setDiscoveryRegion(e.target.value)}
                    placeholder="e.g. us-east-1 (leave empty for all regions)"
                    className="w-full rounded-md border border-[#5a9dd0] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 dark:placeholder-[#5a8aaa]"
                  />
                </div>
                <button
                  type="button"
                  onClick={handleDiscoverClusters}
                  disabled={discovering || !discoveryRoleArns.trim()}
                  className="inline-flex items-center gap-2 rounded-md bg-[#0a2a4a] px-4 py-2 text-sm font-medium text-white hover:bg-[#0d3558] disabled:cursor-not-allowed disabled:opacity-50 dark:bg-blue-700 dark:hover:bg-blue-600"
                >
                  {discovering ? <Loader2 className="h-4 w-4 animate-spin" /> : <ScanSearch className="h-4 w-4" />}
                  Scan
                </button>
                {discoveryError && (
                  <p className="text-sm text-amber-600 dark:text-amber-400">{discoveryError}</p>
                )}
                {discoveredItems.length > 0 && (
                  <div>
                    <label className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300">
                      Discovered Clusters ({discoveredItems.length})
                    </label>
                    <div className="max-h-48 overflow-y-auto rounded-md ring-2 ring-[#6aade0] dark:ring-gray-700">
                      <table className="w-full text-left text-sm">
                        <thead className="sticky top-0 border-b border-[#6aade0] bg-[#d0e8f8] text-xs uppercase text-[#2a5a7a] dark:border-gray-700 dark:bg-gray-900 dark:text-gray-400">
                          <tr>
                            <th className="px-3 py-2 w-8">
                              <input
                                type="checkbox"
                                checked={discoveredItems.filter(c => !c.already_managed).length > 0 && discoveredItems.filter(c => !c.already_managed).every(c => selectedDiscovered[c.name])}
                                onChange={(e) => {
                                  const checked = e.target.checked;
                                  const next: Record<string, boolean> = {};
                                  discoveredItems.forEach(c => {
                                    if (!c.already_managed) next[c.name] = checked;
                                  });
                                  setSelectedDiscovered(next);
                                }}
                                className="rounded border-[#5a9dd0] dark:border-gray-600"
                              />
                            </th>
                            <th className="px-3 py-2">Name</th>
                            <th className="px-3 py-2">Region</th>
                            <th className="px-3 py-2">Version</th>
                            <th className="px-3 py-2">Status</th>
                          </tr>
                        </thead>
                        <tbody className="divide-y divide-[#6aade0] dark:divide-gray-700 bg-[#f0f7ff] dark:bg-gray-800">
                          {discoveredItems.map((cluster) => (
                            <tr key={cluster.name} className={cluster.already_managed ? 'opacity-50' : ''}>
                              <td className="px-3 py-2">
                                <input
                                  type="checkbox"
                                  checked={!!selectedDiscovered[cluster.name]}
                                  disabled={cluster.already_managed}
                                  onChange={(e) => setSelectedDiscovered(prev => ({ ...prev, [cluster.name]: e.target.checked }))}
                                  className="rounded border-[#5a9dd0] dark:border-gray-600"
                                />
                              </td>
                              <td className="px-3 py-2 font-medium text-[#0a2a4a] dark:text-gray-100">
                                {cluster.name}
                              </td>
                              <td className="px-3 py-2 text-[#2a5a7a] dark:text-gray-400">{cluster.region}</td>
                              <td className="px-3 py-2 font-mono text-xs text-[#2a5a7a] dark:text-gray-400">{cluster.version ?? '--'}</td>
                              <td className="px-3 py-2">
                                {cluster.already_managed
                                  ? <span className="text-xs text-[#5a8aaa]">Already managed</span>
                                  : <span className="text-xs text-teal-600 dark:text-teal-400">{cluster.status ?? 'Available'}</span>
                                }
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  </div>
                )}
              </>
            )}

            {/* Addons selection - shared between modes */}
            {catalogAddons && catalogAddons.addons.length > 0 && (
              <div>
                <label className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300">
                  Enable Addons (optional)
                </label>
                <div className="max-h-40 space-y-1 overflow-y-auto rounded-md ring-2 ring-[#6aade0] p-2 dark:ring-gray-700">
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

            {/* Auto-merge checkbox */}
            <div className="flex items-center gap-2">
              <input
                type="checkbox"
                id="auto-merge-checkbox"
                checked={autoMerge}
                disabled={isAutoMergeDisabled}
                onChange={(e) => setAutoMerge(e.target.checked)}
                className="rounded border-[#5a9dd0] dark:border-gray-600 disabled:opacity-50"
              />
              <label
                htmlFor="auto-merge-checkbox"
                className={`text-sm font-medium ${isAutoMergeDisabled ? 'text-[#5a8aaa] dark:text-gray-500' : 'text-[#0a3a5a] dark:text-gray-300'}`}
              >
                Merge PR automatically
              </label>
              {isAutoMergeDisabled && (
                <span className="text-xs text-[#5a8aaa] dark:text-gray-500">(admin only)</span>
              )}
            </div>

            {/* Dry-run preview panel */}
            {dryRunResult && (
              <div className="rounded-md ring-2 ring-[#6aade0] bg-[#e8f4ff] p-3 dark:ring-gray-700 dark:bg-gray-900">
                <h4 className="mb-2 text-sm font-semibold text-[#0a2a4a] dark:text-gray-200">Dry Run Preview</h4>
                <div className="space-y-2 text-xs text-[#2a5a7a] dark:text-gray-400">
                  <div>
                    <span className="font-medium text-[#0a3a5a] dark:text-gray-300">PR Title:</span>{' '}
                    {dryRunResult.pr_title}
                  </div>
                  {dryRunResult.effective_addons.length > 0 && (
                    <div>
                      <span className="font-medium text-[#0a3a5a] dark:text-gray-300">Effective Addons:</span>{' '}
                      {dryRunResult.effective_addons.join(', ')}
                    </div>
                  )}
                  {dryRunResult.files.length > 0 && (
                    <div>
                      <span className="font-medium text-[#0a3a5a] dark:text-gray-300">Files:</span>
                      <ul className="mt-1 space-y-0.5 font-mono">
                        {dryRunResult.files.map((f) => (
                          <li key={f.path}>
                            <span className={f.action === 'create' ? 'text-green-600 dark:text-green-400' : 'text-amber-600 dark:text-amber-400'}>
                              {f.action === 'create' ? '+' : '~'}
                            </span>{' '}
                            {f.path}
                          </li>
                        ))}
                      </ul>
                    </div>
                  )}
                  {dryRunResult.secrets_to_create.length > 0 && (
                    <div>
                      <span className="font-medium text-[#0a3a5a] dark:text-gray-300">Secrets to Create:</span>{' '}
                      {dryRunResult.secrets_to_create.join(', ')}
                    </div>
                  )}
                </div>
              </div>
            )}

            {addClusterError && (
              <p className="text-sm text-red-600 dark:text-red-400">{addClusterError}</p>
            )}
          </div>
          <DialogFooter className="flex-wrap gap-2">
            <button
              type="button"
              onClick={() => setAddClusterOpen(false)}
              disabled={addClusterSubmitting}
              className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={handleDryRun}
              disabled={
                dryRunLoading ||
                addClusterSubmitting ||
                (registrationMode === 'direct' && !addClusterName.trim()) ||
                (registrationMode === 'discovery' && !Object.values(selectedDiscovered).some(Boolean))
              }
              className="inline-flex items-center gap-2 rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
            >
              {dryRunLoading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Eye className="h-4 w-4" />}
              Preview
            </button>
            <button
              type="button"
              onClick={handleAddCluster}
              disabled={
                addClusterSubmitting ||
                (registrationMode === 'direct' && !addClusterName.trim()) ||
                (registrationMode === 'discovery' && !Object.values(selectedDiscovered).some(Boolean))
              }
              className="inline-flex items-center gap-2 rounded-md bg-teal-600 px-4 py-2 text-sm font-medium text-white hover:bg-teal-700 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-teal-700 dark:hover:bg-teal-600"
            >
              {addClusterSubmitting && <Loader2 className="h-4 w-4 animate-spin" />}
              Register{registrationMode === 'discovery' && Object.values(selectedDiscovered).filter(Boolean).length > 1 ? ` (${Object.values(selectedDiscovered).filter(Boolean).length})` : ''}
            </button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Registration success banner */}
      {addClusterResultMsg && (
        <div className={`flex items-center justify-between rounded-md px-4 py-2 text-sm ${
          addClusterResult?.partial
            ? 'border border-amber-300 bg-amber-50 text-amber-800 dark:border-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
            : 'border border-green-300 bg-green-50 text-green-800 dark:border-green-700 dark:bg-green-900/30 dark:text-green-300'
        }`}>
          <span>
            {addClusterResult?.partial
              ? addClusterResultMsg
              : addClusterResultMsg.startsWith('http')
                ? <>Cluster registered. PR: <a href={addClusterResultMsg} target="_blank" rel="noopener noreferrer" className="underline font-medium">{addClusterResultMsg}</a></>
                : addClusterResultMsg
            }
          </span>
          <button
            type="button"
            onClick={() => { setAddClusterResultMsg(null); setAddClusterResult(null); }}
            className={`ml-4 rounded p-0.5 ${addClusterResult?.partial ? 'hover:bg-amber-100 dark:hover:bg-amber-800' : 'hover:bg-green-100 dark:hover:bg-green-800'}`}
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
                        {isClusterStatus(cluster.connection_status ?? 'unknown')
                          ? <StatusBadge status={cluster.connection_status ?? 'unknown'} />
                          : <ConnectionStatus status={cluster.connection_status ?? 'unknown'} />
                        }
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
                          <button
                            type="button"
                            onClick={(e) => { e.stopPropagation(); setDiagnoseCluster(cluster.name); }}
                            className="inline-flex items-center gap-1 rounded border border-[#5a9dd0] px-2 py-1 text-xs text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
                          >
                            <HelpCircle className="h-3 w-3" />
                            Diagnose
                          </button>
                          {cluster.adopted && (
                            <RoleGuard adminOnly>
                              <button
                                type="button"
                                onClick={(e) => { e.stopPropagation(); setUnadoptTarget(cluster.name); }}
                                className="inline-flex items-center gap-1 rounded border border-red-300 px-2 py-1 text-xs text-red-600 hover:bg-red-50 dark:border-red-700 dark:text-red-400 dark:hover:bg-red-900/20"
                              >
                                <Unlink className="h-3 w-3" />
                                Un-adopt
                              </button>
                            </RoleGuard>
                          )}
                          {testResult && testResult !== 'testing' && (
                            testResult.reachable !== false && testResult.success !== false
                              ? <span className="flex items-center gap-1 text-xs text-green-600 dark:text-green-400">
                                  <CheckCircle className="h-3 w-3" />
                                  {[testResult.server_version, testResult.platform].filter(Boolean).join(' — ') || 'Reachable'}
                                </span>
                              : <div className="flex flex-col gap-1">
                                  <span className="flex items-center gap-1 text-xs text-red-500 dark:text-red-400">
                                    <WifiOff className="h-3 w-3" />
                                    Error: {testResult.error ?? testResult.error_message ?? 'Unreachable'}
                                  </span>
                                  {testResult.suggestions && testResult.suggestions.length > 0 && (
                                    <button
                                      type="button"
                                      onClick={(e) => { e.stopPropagation(); navigate(`/clusters/${cluster.name}`); }}
                                      className="inline-flex items-center gap-1 text-xs text-[#0a3a5a] underline hover:text-[#2a5a7a] dark:text-blue-400 dark:hover:text-blue-300"
                                    >
                                      {testResult.suggestions.length} similar secret{testResult.suggestions.length > 1 ? 's' : ''} found — click to fix
                                    </button>
                                  )}
                                </div>
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
                    <div className="flex items-center gap-1">
                      <button
                        type="button"
                        onClick={(e) => handleTestCluster(cluster.name, e)}
                        disabled={testResult === 'testing'}
                        className="inline-flex items-center gap-1 rounded border border-[#5a9dd0] px-2 py-1 text-xs text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
                      >
                        {testResult === 'testing' ? <Loader2 className="h-3 w-3 animate-spin" /> : <Wifi className="h-3 w-3" />}
                        Test
                      </button>
                      <button
                        type="button"
                        onClick={(e) => { e.stopPropagation(); setDiagnoseCluster(cluster.name); }}
                        className="inline-flex items-center gap-1 rounded border border-[#5a9dd0] px-2 py-1 text-xs text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
                      >
                        <HelpCircle className="h-3 w-3" />
                      </button>
                    </div>
                  </div>
                  {testResult && testResult !== 'testing' && (
                    <div className="mb-2">
                      {testResult.reachable !== false && testResult.success !== false
                        ? <span className="flex items-center gap-1 text-xs text-green-600 dark:text-green-400">
                            <CheckCircle className="h-3 w-3" />
                            {[testResult.server_version, testResult.platform].filter(Boolean).join(' — ') || 'Reachable'}
                          </span>
                        : <div className="flex flex-col gap-1">
                            <span className="flex items-center gap-1 text-xs text-red-500 dark:text-red-400">
                              <WifiOff className="h-3 w-3" />
                              Error: {testResult.error ?? testResult.error_message ?? 'Unreachable'}
                            </span>
                            {testResult.suggestions && testResult.suggestions.length > 0 && (
                              <button
                                type="button"
                                onClick={(e) => { e.stopPropagation(); navigate(`/clusters/${cluster.name}`); }}
                                className="inline-flex items-center gap-1 text-xs text-[#0a3a5a] underline hover:text-[#2a5a7a] dark:text-blue-400 dark:hover:text-blue-300"
                              >
                                {testResult.suggestions.length} similar secret{testResult.suggestions.length > 1 ? 's' : ''} found — click to fix
                              </button>
                            )}
                          </div>
                      }
                    </div>
                  )}
                  <div className="mb-2">
                    {isClusterStatus(cluster.connection_status ?? 'unknown')
                      ? <StatusBadge status={cluster.connection_status ?? 'unknown'} />
                      : <ConnectionStatus status={cluster.connection_status ?? 'unknown'} />
                    }
                  </div>
                  <p className="mb-2 font-mono text-xs text-[#2a5a7a] dark:text-gray-400">
                    {cluster.server_version ? `v${cluster.server_version}` : '--'}
                  </p>
                  <div className="flex items-center justify-between">
                    {addonCount > 0 && (
                      <span className="inline-flex items-center rounded-full bg-[#d6eeff] px-2 py-0.5 text-xs font-medium text-[#1a4a6a] dark:bg-gray-700 dark:text-gray-300">
                        {addonCount} addon{addonCount !== 1 ? 's' : ''}
                      </span>
                    )}
                    {cluster.adopted && (
                      <RoleGuard adminOnly>
                        <button
                          type="button"
                          onClick={(e) => { e.stopPropagation(); setUnadoptTarget(cluster.name); }}
                          className="inline-flex items-center gap-1 rounded border border-red-300 px-2 py-1 text-xs text-red-600 hover:bg-red-50 dark:border-red-700 dark:text-red-400 dark:hover:bg-red-900/20"
                        >
                          <Unlink className="h-3 w-3" />
                          Un-adopt
                        </button>
                      </RoleGuard>
                    )}
                  </div>
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
          <div className="flex items-center justify-between">
            <h3 className="flex items-center gap-2 text-sm font-semibold text-[#0a2a4a] dark:text-gray-200">
              <AlertTriangle className="h-4 w-4 text-amber-500" />
              Discovered Clusters
              <span className="rounded-full bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-400">
                {discoveredClusters.length}
              </span>
              <span className="text-xs font-normal text-[#3a6a8a] dark:text-gray-500">
                — present in ArgoCD but not yet managed by Sharko
              </span>
            </h3>
            <RoleGuard adminOnly>
              {selectedDiscoveredCount > 0 && (
                <button
                  type="button"
                  onClick={handleAdoptSelected}
                  className="inline-flex items-center gap-1.5 rounded-md bg-teal-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-teal-700 dark:bg-teal-700 dark:hover:bg-teal-600"
                >
                  <GitMerge className="h-3.5 w-3.5" />
                  Adopt Selected ({selectedDiscoveredCount})
                </button>
              )}
            </RoleGuard>
          </div>

          {viewMode === 'list' ? (
            <div className="overflow-x-auto rounded-xl ring-2 ring-amber-200 bg-[#fffbf0] shadow-sm dark:border-amber-800 dark:bg-gray-800">
              <table className="w-full text-left text-sm">
                <thead className="border-b border-amber-200 bg-amber-50 text-xs uppercase text-amber-700 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-500">
                  <tr>
                    <th className="px-3 py-3 w-8">
                      <RoleGuard adminOnly>
                        <input
                          type="checkbox"
                          checked={discoveredClusters.length > 0 && discoveredClusters.every((c) => selectedDiscoveredForAdopt[c.name])}
                          onChange={(e) => toggleAllDiscovered(e.target.checked)}
                          className="rounded border-amber-300 dark:border-gray-600"
                          aria-label="Select all discovered clusters"
                        />
                      </RoleGuard>
                    </th>
                    <th className="px-6 py-3">Name</th>
                    <th className="px-6 py-3">Server URL</th>
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
                        <td className="px-3 py-3">
                          <RoleGuard adminOnly>
                            <input
                              type="checkbox"
                              checked={!!selectedDiscoveredForAdopt[cluster.name]}
                              onChange={() => toggleDiscoveredSelection(cluster.name)}
                              className="rounded border-amber-300 dark:border-gray-600"
                            />
                          </RoleGuard>
                        </td>
                        <td className="px-6 py-3 font-medium text-[#0a2a4a] dark:text-gray-100">
                          {cluster.name}
                        </td>
                        <td className="px-6 py-3 font-mono text-xs text-[#3a6a8a] dark:text-gray-400 max-w-[200px] truncate" title={cluster.server_url}>
                          {cluster.server_url ?? '--'}
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
                                testResult.reachable !== false && testResult.success !== false
                                  ? <span className="flex items-center gap-1 text-xs text-green-600 dark:text-green-400">
                                      <CheckCircle className="h-3 w-3" />
                                      {[testResult.server_version, testResult.platform].filter(Boolean).join(' — ') || 'Reachable'}
                                    </span>
                                  : <span className="flex items-center gap-1 text-xs text-red-500 dark:text-red-400">
                                      <WifiOff className="h-3 w-3" />
                                      Error: {testResult.error ?? testResult.error_message ?? 'Unreachable'}
                                    </span>
                              )}
                              <RoleGuard adminOnly>
                                <button
                                  type="button"
                                  onClick={() => handleOpenAdoptDialog([cluster])}
                                  disabled={!!ms?.loading}
                                  className="inline-flex items-center gap-1 rounded bg-teal-600 px-2 py-1 text-xs font-medium text-white hover:bg-teal-700 disabled:opacity-50"
                                >
                                  {ms?.loading ? <Loader2 className="h-3 w-3 animate-spin" /> : <GitMerge className="h-3 w-3" />}
                                  Adopt
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
                      <div className="flex items-center gap-2">
                        <RoleGuard adminOnly>
                          <input
                            type="checkbox"
                            checked={!!selectedDiscoveredForAdopt[cluster.name]}
                            onChange={() => toggleDiscoveredSelection(cluster.name)}
                            className="rounded border-amber-300 dark:border-gray-600"
                          />
                        </RoleGuard>
                        <h3 className="text-sm font-bold text-[#0a2a4a] dark:text-gray-100">{cluster.name}</h3>
                      </div>
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
                    {cluster.server_url && (
                      <p className="mb-2 font-mono text-xs text-[#3a6a8a] dark:text-gray-400 truncate" title={cluster.server_url}>
                        {cluster.server_url}
                      </p>
                    )}
                    {testResult && testResult !== 'testing' && (
                      <div className="mb-2">
                        {testResult.reachable !== false && testResult.success !== false
                          ? <span className="flex items-center gap-1 text-xs text-green-600 dark:text-green-400">
                              <CheckCircle className="h-3 w-3" />
                              {[testResult.server_version, testResult.platform].filter(Boolean).join(' — ') || 'Reachable'}
                            </span>
                          : <span className="flex items-center gap-1 text-xs text-red-500 dark:text-red-400">
                              <WifiOff className="h-3 w-3" />
                              Error: {testResult.error ?? testResult.error_message ?? 'Unreachable'}
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
                        onClick={() => handleOpenAdoptDialog([cluster])}
                        disabled={!!ms?.loading}
                        className="inline-flex w-full items-center justify-center gap-1 rounded bg-teal-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-teal-700 disabled:opacity-50"
                      >
                        {ms?.loading ? <Loader2 className="h-3 w-3 animate-spin" /> : <GitMerge className="h-3 w-3" />}
                        Adopt
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

      {/* Adopt Clusters Dialog */}
      <AdoptClustersDialog
        open={adoptDialogOpen}
        onClose={() => setAdoptDialogOpen(false)}
        clusters={adoptDialogClusters}
        onSuccess={handleAdoptSuccess}
        onDiagnose={(name) => { setAdoptDialogOpen(false); setDiagnoseCluster(name); }}
      />

      {/* Un-adopt Confirmation Modal */}
      <ConfirmationModal
        open={unadoptTarget !== null}
        onClose={() => setUnadoptTarget(null)}
        onConfirm={handleUnadopt}
        title="Un-adopt Cluster"
        description={`This will remove "${unadoptTarget}" from Sharko management. The ArgoCD connection will remain, but Sharko will no longer manage addons for this cluster.`}
        confirmText="Un-adopt"
        typeToConfirm={unadoptTarget ?? ''}
        destructive
        loading={unadoptLoading}
      />

      {/* Un-adopt result banner */}
      {unadoptResult && (
        <div className={`flex items-center justify-between rounded-md px-4 py-2 text-sm ${
          unadoptResult.error
            ? 'border border-red-300 bg-red-50 text-red-800 dark:border-red-700 dark:bg-red-900/30 dark:text-red-300'
            : 'border border-green-300 bg-green-50 text-green-800 dark:border-green-700 dark:bg-green-900/30 dark:text-green-300'
        }`}>
          <span>
            {unadoptResult.error
              ? unadoptResult.error
              : unadoptResult.success?.startsWith('http')
                ? <>Cluster un-adopted. PR: <a href={unadoptResult.success} target="_blank" rel="noopener noreferrer" className="underline font-medium">{unadoptResult.success}</a></>
                : unadoptResult.success
            }
          </span>
          <button
            type="button"
            onClick={() => setUnadoptResult(null)}
            className={`ml-4 rounded p-0.5 ${unadoptResult.error ? 'hover:bg-red-100 dark:hover:bg-red-800' : 'hover:bg-green-100 dark:hover:bg-green-800'}`}
            aria-label="Dismiss"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
      )}
    </div>
  );
}
export default ClustersOverview

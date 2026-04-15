import { useState, useEffect, useMemo, useCallback } from 'react';
import { useParams, useNavigate, useSearchParams } from 'react-router-dom';
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
  Loader2,
  LayoutGrid,
  Package,
  FileCode,
  Clock,
  GitPullRequest,
  Wifi,
  ScanSearch,
  Pencil,
  KeyRound,
  Plus,
} from 'lucide-react';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog';
import type { AddonCatalogItem } from '@/services/models';
import { api, deregisterCluster, updateClusterAddons, updateClusterSettings, testClusterConnection } from '@/services/api';
import type { ClusterComparisonResponse, AddonComparisonStatus, ConfigDiffResponse, SyncActivityEntry, VerifyStep } from '@/services/models';
import { StatCard } from '@/components/StatCard';
import { StatusBadge } from '@/components/StatusBadge';
import { LoadingState } from '@/components/LoadingState';
import { ErrorState } from '@/components/ErrorState';
import { EmptyState } from '@/components/EmptyState';
import { YamlViewer } from '@/components/YamlViewer';
import { RoleGuard } from '@/components/RoleGuard';
import { ConfirmationModal } from '@/components/ConfirmationModal';
import { DetailNavPanel } from '@/components/DetailNavPanel';
import { DiagnoseModal } from '@/components/DiagnoseModal';
import { PendingPRsPanel } from '@/components/PendingPRsPanel';
import { showToast } from '@/components/ToastNotification';
import type { TrackedPR } from '@/services/models';

type StatusFilter =
  | 'all'
  | 'healthy'
  | 'with_issues'
  | 'missing_in_argocd'
  | 'untracked'
  | 'disabled_in_git';

function ClusterHistorySection({ clusterName }: { clusterName: string }) {
  const [history, setHistory] = useState<SyncActivityEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api.getClusterHistory(clusterName)
      .then(data => setHistory(data.history ?? []))
      .catch(e => setError(e instanceof Error ? e.message : 'Failed to load history'))
      .finally(() => setLoading(false));
  }, [clusterName]);

  if (loading) return <LoadingState message="Loading history..." />;
  if (error) return <ErrorState message={error} />;

  if (history.length === 0) {
    return (
      <EmptyState
        title="No history yet"
        description="Sync activity for this cluster will appear here."
      />
    );
  }

  return (
    <div className="space-y-4">
      <h3 className="text-lg font-semibold text-[#0a2a4a]">Change History</h3>
      <div className="space-y-2">
        {history.map((entry, i) => (
          <div key={i} className="flex items-center gap-3 rounded-lg bg-[#f0f7ff] ring-2 ring-[#6aade0] px-4 py-3">
            <div className={`h-2.5 w-2.5 shrink-0 rounded-full ${
              entry.status === 'Synced' || entry.status === 'Succeeded' ? 'bg-green-500' : 'bg-amber-500'
            }`} />
            <div className="min-w-0 flex-1">
              <span className="font-medium text-[#0a2a4a]">{entry.addon_name}</span>
              <span className="text-[#3a6a8a]"> — {entry.status}</span>
            </div>
            <span className="shrink-0 text-xs text-[#3a6a8a]">
              {new Date(entry.timestamp).toLocaleDateString()} {new Date(entry.timestamp).toLocaleTimeString()}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}

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
  const [searchParams, setSearchParams] = useSearchParams();
  const activeSection = searchParams.get('section') || 'overview';
  const setActiveSection = (s: string) => setSearchParams({ section: s }, { replace: true });
  const [configDiff, setConfigDiff] = useState<ConfigDiffResponse | null>(null);
  const [configDiffLoading, setConfigDiffLoading] = useState(false);
  const [configDiffError, setConfigDiffError] = useState<string | null>(null);
  const [clusterValuesYaml, setClusterValuesYaml] = useState<string | null>(null);
  const [configFetched, setConfigFetched] = useState(false);
  const [nodeInfo, setNodeInfo] = useState<{ total: number; ready: number; not_ready: number } | null>(null);
  const [argocdBaseURL, setArgocdBaseURL] = useState<string>('');

  // Remove cluster
  const [removeModalOpen, setRemoveModalOpen] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [removeError, setRemoveError] = useState<string | null>(null);

  // Test connection
  const [testResult, setTestResult] = useState<{ reachable?: boolean; success?: boolean; server_version?: string; error?: string; error_message?: string; suggestions?: string[]; steps?: VerifyStep[] } | 'testing' | null>(null);
  const [diagnoseOpen, setDiagnoseOpen] = useState(false);

  // Secret path editing
  const [editingSecretPath, setEditingSecretPath] = useState(false);
  const [secretPathValue, setSecretPathValue] = useState('');
  const [secretPathSaving, setSecretPathSaving] = useState(false);
  const [secretPathResult, setSecretPathResult] = useState<string | null>(null);

  // Addon toggles
  const [addonToggles, setAddonToggles] = useState<Record<string, boolean>>({});
  const [originalToggles, setOriginalToggles] = useState<Record<string, boolean>>({});
  const [applyingToggles, setApplyingToggles] = useState(false);
  const [toggleError, setToggleError] = useState<string | null>(null);
  const [toggleResult, setToggleResult] = useState<string | null>(null);

  // Deploy Addon dialog
  const [deployDialogOpen, setDeployDialogOpen] = useState(false);
  const [catalogAddons, setCatalogAddons] = useState<AddonCatalogItem[]>([]);
  const [catalogLoading, setCatalogLoading] = useState(false);
  const [catalogError, setCatalogError] = useState<string | null>(null);
  const [selectedAddon, setSelectedAddon] = useState<AddonCatalogItem | null>(null);
  const [deploying, setDeploying] = useState(false);
  const [deployResult, setDeployResult] = useState<{ prUrl?: string; error?: string } | null>(null);

  // Compute display status from test result + server state
  const computedStatus = useMemo((): string => {
    if (testResult && testResult !== 'testing') {
      if (testResult.reachable || testResult.success) return 'connected';
      return 'unreachable';
    }
    if (data?.cluster_connection_state) {
      const state = data.cluster_connection_state.toLowerCase();
      if (state === 'successful' || state === 'connected') return 'connected';
      if (state === 'unreachable' || state === 'failed') return 'unreachable';
    }
    // If cluster has healthy addons, show operational
    if (data && data.total_healthy > 0) return 'operational';
    return 'unknown';
  }, [testResult, data]);

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
      // Initialize addon toggles from cluster data
      const toggleMap: Record<string, boolean> = {};
      result.addon_comparisons.forEach((a: { addon_name: string; git_enabled: boolean }) => {
        toggleMap[a.addon_name] = a.git_enabled;
      });
      setAddonToggles(toggleMap);
      setOriginalToggles(toggleMap);
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

  const handleRemoveCluster = useCallback(async () => {
    if (!name) return;
    setRemoving(true);
    setRemoveError(null);
    try {
      await deregisterCluster(name);
      navigate('/clusters');
    } catch (e: unknown) {
      setRemoveError(e instanceof Error ? e.message : 'Failed to remove cluster');
      setRemoving(false);
    }
  }, [name, navigate]);

  const hasToggleChanges = useMemo(() => {
    return Object.keys(addonToggles).some((k) => addonToggles[k] !== originalToggles[k]);
  }, [addonToggles, originalToggles]);

  const handleApplyToggles = useCallback(async () => {
    if (!name) return;
    setApplyingToggles(true);
    setToggleError(null);
    setToggleResult(null);
    try {
      const result = await updateClusterAddons(name, addonToggles);
      const prUrl = result?.pr_url || result?.pull_request_url;
      setToggleResult(prUrl ? `Changes applied. PR: ${prUrl}` : 'Changes applied successfully.');
      setOriginalToggles({ ...addonToggles });
    } catch (e: unknown) {
      setToggleError(e instanceof Error ? e.message : 'Failed to apply changes');
    } finally {
      setApplyingToggles(false);
    }
  }, [name, addonToggles]);

  const handleTestConnection = useCallback(async () => {
    if (!name) return;
    setTestResult('testing');
    try {
      const result = await testClusterConnection(name);
      setTestResult(result);
      // Refetch cluster data so server-side computed status is up to date
      if (result.reachable || result.success) {
        void fetchData();
      }
    } catch (err) {
      setTestResult({ reachable: false, error: err instanceof Error ? err.message : 'Failed' });
    }
  }, [name, fetchData]);

  const handleOpenDeployDialog = useCallback(async () => {
    setDeployDialogOpen(true);
    setSelectedAddon(null);
    setDeployResult(null);
    setCatalogError(null);
    setCatalogLoading(true);
    try {
      const catalog = await api.getAddonCatalog();
      // Only show addons that are NOT currently enabled (git_enabled = true) on this cluster
      const enabledNames = new Set(
        (data?.addon_comparisons ?? [])
          .filter((a) => a.git_enabled)
          .map((a) => a.addon_name),
      );
      setCatalogAddons(catalog.addons.filter((a) => !enabledNames.has(a.addon_name)));
    } catch (e: unknown) {
      setCatalogError(e instanceof Error ? e.message : 'Failed to load addon catalog');
    } finally {
      setCatalogLoading(false);
    }
  }, [data]);

  const handleDeployAddon = useCallback(async () => {
    if (!name || !selectedAddon) return;
    setDeploying(true);
    setDeployResult(null);
    try {
      const result = await api.enableAddonOnCluster(name, selectedAddon.addon_name);
      const prUrl = result?.pr_url || result?.pull_request_url;
      setDeployResult({ prUrl });
      void fetchData();
    } catch (e: unknown) {
      setDeployResult({ error: e instanceof Error ? e.message : 'Failed to deploy addon' });
    } finally {
      setDeploying(false);
    }
  }, [name, selectedAddon, fetchData]);

  const handleSelectSuggestion = useCallback(async (suggestion: string) => {
    if (!name) return;
    try {
      await updateClusterSettings(name, { secret_path: suggestion });
      showToast(`Secret path updated to: ${suggestion}`, 'success');
      // Auto-retry the test with the new secret path
      setTestResult('testing');
      const result = await testClusterConnection(name);
      setTestResult(result);
    } catch (err) {
      setTestResult({ reachable: false, error: err instanceof Error ? err.message : 'Failed to update secret path' });
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
    if (activeSection === 'config' && !configFetched && name) {
      setConfigFetched(true);
      void fetchConfigDiff();
      api
        .getClusterValues(name)
        .then((res) => setClusterValuesYaml(res.values_yaml))
        .catch(() => {
          // Cluster values file may not exist — that's OK
        });
    }
  }, [activeSection, configFetched, name, fetchConfigDiff]);

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

  const navSections = [
    {
      items: [
        { key: 'overview', label: 'Overview', icon: LayoutGrid },
        { key: 'addons', label: 'Addons', badge: data ? data.addon_comparisons.length : undefined, icon: Package },
        { key: 'prs', label: 'Pull Requests', icon: GitPullRequest },
        { key: 'config', label: 'Config', icon: FileCode },
        { key: 'history', label: 'History', icon: Clock },
      ],
    },
    {
      items: [
        { key: 'remove', label: 'Remove Cluster', destructive: true },
      ],
    },
  ];

  return (
    <div className="space-y-6">
      {/* Back button */}
      <button
        type="button"
        onClick={() => navigate('/clusters')}
        className="inline-flex items-center gap-1.5 text-sm font-medium text-teal-600 hover:text-teal-800 dark:text-teal-400 dark:hover:text-teal-300"
      >
        <ArrowLeft className="h-4 w-4" />
        Back to Clusters Overview
      </button>

      {/* Heading + cluster meta + actions */}
      <div className="flex items-start justify-between">
        <div>
          <div className="flex items-center gap-3">
            <h2 className="text-2xl font-bold text-[#0a2a4a] dark:text-gray-100">{data.cluster.name}</h2>
            <StatusBadge status={computedStatus} size="sm" />
          </div>
          <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-400">
            Kubernetes cluster managed by ArgoCD — deployed addons, health, and configuration overrides.
          </p>
          {testResult && testResult !== 'testing' && (
            <div className="mt-2">
              {/* Step-by-step test results */}
              {testResult.steps && testResult.steps.length > 0 && (
                <div className="mb-2 rounded-lg bg-[#f8fbff] p-3 ring-1 ring-[#d0e4f5] dark:bg-gray-800 dark:ring-gray-700">
                  <p className="mb-2 text-xs font-semibold text-[#0a2a4a] dark:text-gray-200">Test Results:</p>
                  <div className="space-y-1">
                    {testResult.steps.map((step, i) => (
                      <div key={i} className="flex items-center gap-2 text-xs">
                        {step.status === 'pass' && (
                          <span className="text-green-600 dark:text-green-400">&#10003;</span>
                        )}
                        {step.status === 'fail' && (
                          <span className="text-red-600 dark:text-red-400">&#10007;</span>
                        )}
                        {step.status === 'skipped' && (
                          <span className="text-gray-400 dark:text-gray-500">&#9675;</span>
                        )}
                        <span className={
                          step.status === 'pass'
                            ? 'text-[#0a2a4a] dark:text-gray-200'
                            : step.status === 'fail'
                              ? 'text-red-700 dark:text-red-400'
                              : 'text-gray-400 dark:text-gray-500'
                        }>
                          {step.name}
                          {step.detail && step.status !== 'skipped' && (
                            <span className="ml-1 text-[#3a6a8a] dark:text-gray-400">
                              {step.status === 'fail' ? ` \u2014 ${step.detail}` : ` (${step.detail})`}
                            </span>
                          )}
                          {step.status === 'skipped' && (
                            <span className="ml-1 text-gray-400 dark:text-gray-500">(skipped)</span>
                          )}
                        </span>
                      </div>
                    ))}
                  </div>
                </div>
              )}
              {/* Summary badge */}
              <div className={`inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-medium ${
                testResult.reachable || testResult.success
                  ? 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400'
                  : 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'
              }`}>
                {testResult.reachable || testResult.success
                  ? `Connected${testResult.server_version ? ` \u2014 ${testResult.server_version}` : ''}`
                  : testResult.error || testResult.error_message || 'Unreachable'}
              </div>
              {!testResult.reachable && !testResult.success && testResult.suggestions && testResult.suggestions.length > 0 && (
                <div className="mt-2 rounded-lg bg-[#e8f4ff] p-3 ring-2 ring-[#6aade0] dark:bg-gray-800 dark:ring-gray-700">
                  <p className="text-xs font-semibold text-[#0a2a4a] dark:text-gray-200">Similar secrets found:</p>
                  <div className="mt-1.5 flex flex-wrap gap-1.5">
                    {testResult.suggestions.map((s) => (
                      <button
                        key={s}
                        onClick={() => handleSelectSuggestion(s)}
                        className="inline-flex items-center gap-1 rounded-md bg-[#f0f7ff] px-2.5 py-1 text-xs font-medium text-[#0a3a5a] ring-1 ring-[#5a9dd0] hover:bg-[#d6eeff] dark:bg-gray-700 dark:text-gray-200 dark:ring-gray-600 dark:hover:bg-gray-600"
                      >
                        <KeyRound className="h-3 w-3" />
                        {s}
                      </button>
                    ))}
                  </div>
                </div>
              )}
              {!testResult.reachable && !testResult.success && (!testResult.suggestions || testResult.suggestions.length === 0) && (
                <p className="mt-1.5 text-xs text-[#3a6a8a] dark:text-gray-400">
                  Set the secret path manually in cluster settings.
                </p>
              )}
            </div>
          )}
        </div>
        <RoleGuard roles={['admin', 'operator']}>
          <div className="flex items-center gap-2">
            <button
              onClick={handleTestConnection}
              disabled={testResult === 'testing'}
              className="inline-flex items-center gap-1.5 rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-1.5 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
            >
              {testResult === 'testing' ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Wifi className="h-3.5 w-3.5" />}
              Test
            </button>
            <button
              onClick={() => setDiagnoseOpen(true)}
              className="inline-flex items-center gap-1.5 rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-1.5 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
            >
              <ScanSearch className="h-3.5 w-3.5" />
              Diagnose
            </button>
          </div>
        </RoleGuard>
      </div>

      <ConfirmationModal
        open={removeModalOpen}
        onClose={() => setRemoveModalOpen(false)}
        onConfirm={handleRemoveCluster}
        title={`Remove cluster "${name}"?`}
        description="This will remove the cluster from the Git catalog. This action creates a pull request and cannot be undone."
        confirmText="Remove"
        destructive
        loading={removing}
      />
      <DiagnoseModal
        clusterName={name ?? ''}
        open={diagnoseOpen}
        onClose={() => setDiagnoseOpen(false)}
      />
      {removeError && (
        <p className="text-sm text-red-600 dark:text-red-400">{removeError}</p>
      )}

      {/* Main layout: nav panel + content */}
      <div className="flex gap-6">
        <RoleGuard
          adminOnly
          fallback={
            <DetailNavPanel
              sections={[navSections[0]]}
              activeKey={activeSection}
              onSelect={(key) => {
                setActiveSection(key);
              }}
            />
          }
        >
          <DetailNavPanel
            sections={navSections}
            activeKey={activeSection}
            onSelect={(key) => {
              if (key === 'remove') {
                setRemoveError(null);
                setRemoveModalOpen(true);
              } else {
                setActiveSection(key);
              }
            }}
          />
        </RoleGuard>

        <div className="flex-1 space-y-6">
          {/* Overview section */}
          {activeSection === 'overview' && (
            <>
              {/* Cluster info stat cards */}
              <div className="flex flex-wrap gap-3">
                {data.cluster.server_version && (
                  <div className="flex items-center gap-2 rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] px-4 py-3 shadow-sm dark:border-gray-700 dark:bg-gray-800">
                    <Tag className="h-4 w-4 text-teal-500" />
                    <div>
                      <p className="text-xs text-[#2a5a7a] dark:text-gray-400">Cluster Version</p>
                      <p className="font-mono text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">{data.cluster.server_version}</p>
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
                      <p className="text-xs text-[#2a5a7a] dark:text-gray-400">Nodes Ready</p>
                      <p className={`text-sm font-semibold ${nodeInfo.not_ready > 0 ? 'text-red-700 dark:text-red-400' : 'text-green-700 dark:text-green-400'}`}>
                        {nodeInfo.ready} / {nodeInfo.total}
                        {nodeInfo.not_ready > 0 && (
                          <span className="ml-1.5 text-xs font-normal">({nodeInfo.not_ready} not ready)</span>
                        )}
                      </p>
                    </div>
                  </div>
                )}
                <div className="flex items-center gap-2 rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] px-4 py-3 shadow-sm dark:border-gray-700 dark:bg-gray-800">
                  <Server className="h-4 w-4 text-teal-500" />
                  <div>
                    <p className="text-xs text-[#2a5a7a] dark:text-gray-400">Connection</p>
                    <StatusBadge status={computedStatus} size="sm" />
                  </div>
                </div>
                {/* Secret Path */}
                <div className="flex items-center gap-2 rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] px-4 py-3 shadow-sm dark:border-gray-700 dark:bg-gray-800">
                  <KeyRound className="h-4 w-4 text-teal-500" />
                  <div className="min-w-0 flex-1">
                    <p className="text-xs text-[#2a5a7a] dark:text-gray-400">Secret Path</p>
                    {editingSecretPath ? (
                      <div className="flex items-center gap-2 mt-0.5">
                        <input
                          type="text"
                          value={secretPathValue}
                          onChange={(e) => setSecretPathValue(e.target.value)}
                          placeholder="e.g. k8s-my-cluster"
                          className="w-40 rounded border border-[#5a9dd0] bg-white px-2 py-0.5 text-xs focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-900 dark:text-gray-100"
                        />
                        <button
                          type="button"
                          disabled={secretPathSaving}
                          onClick={async () => {
                            if (!name) return;
                            setSecretPathSaving(true);
                            setSecretPathResult(null);
                            try {
                              const result = await updateClusterSettings(name, { secret_path: secretPathValue });
                              setSecretPathResult(result?.pr_url || result?.message || 'Secret path updated');
                              setEditingSecretPath(false);
                            } catch (e: unknown) {
                              setSecretPathResult(e instanceof Error ? e.message : 'Failed to update');
                            } finally {
                              setSecretPathSaving(false);
                            }
                          }}
                          className="rounded bg-teal-600 px-2 py-0.5 text-xs text-white hover:bg-teal-700 disabled:opacity-50"
                        >
                          {secretPathSaving ? <Loader2 className="h-3 w-3 animate-spin" /> : 'Save'}
                        </button>
                        <button
                          type="button"
                          onClick={() => setEditingSecretPath(false)}
                          className="text-xs text-[#3a6a8a] hover:text-[#0a2a4a] dark:text-gray-400"
                        >
                          Cancel
                        </button>
                      </div>
                    ) : (
                      <div className="flex items-center gap-1.5">
                        <p className="font-mono text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
                          {data.cluster.secret_path || '(cluster name)'}
                        </p>
                        <RoleGuard adminOnly>
                          <button
                            type="button"
                            onClick={() => {
                              setSecretPathValue(data.cluster.secret_path || '');
                              setEditingSecretPath(true);
                              setSecretPathResult(null);
                            }}
                            className="text-[#5a8aaa] hover:text-[#0a2a4a] dark:text-gray-400 dark:hover:text-white"
                            aria-label="Edit secret path"
                          >
                            <Pencil className="h-3 w-3" />
                          </button>
                        </RoleGuard>
                      </div>
                    )}
                    {secretPathResult && (
                      <p className="mt-0.5 text-xs text-teal-600 dark:text-teal-400">{secretPathResult}</p>
                    )}
                  </div>
                </div>
              </div>

              {/* Connection status banner */}
              {computedStatus === 'unreachable' && (() => {
                const hasArgoCDError = data.argocd_connection_status && data.argocd_connection_status !== 'Successful';
                if (hasArgoCDError) {
                  // Consolidated banner: ArgoCD error IS the root cause
                  return (
                    <div className="flex items-start justify-between gap-3 rounded-xl border-2 border-red-300 bg-red-50 px-5 py-4 dark:border-red-700 dark:bg-red-900/20">
                      <div className="flex items-start gap-3 text-red-700 dark:text-red-400">
                        <WifiOff className="h-5 w-5 shrink-0 mt-0.5" />
                        <div>
                          <span className="text-sm font-semibold">Cluster Unreachable — ArgoCD Connection Failed</span>
                          {data.argocd_connection_message && (
                            <p className="mt-0.5 text-xs text-red-600 dark:text-red-400">{data.argocd_connection_message}</p>
                          )}
                          <p className="mt-1 text-xs text-red-600 dark:text-red-400">Addon health data below reflects the last known state and may be stale.</p>
                        </div>
                      </div>
                      <button
                        onClick={() => window.dispatchEvent(new CustomEvent('open-assistant', { detail: `ArgoCD cannot connect to cluster ${name}. Error: ${data.argocd_connection_message}. What could cause this and how do I fix it?` }))}
                        className="flex shrink-0 items-center gap-1.5 rounded-lg border border-red-200 bg-[#f0f7ff] px-3 py-1.5 text-xs text-red-700 hover:bg-red-50 dark:border-red-800 dark:bg-gray-800 dark:text-red-400"
                      >
                        <MessageSquare className="h-3.5 w-3.5" />
                        Ask AI
                      </button>
                    </div>
                  );
                }
                // Generic unreachable banner (no ArgoCD-specific error)
                return (
                  <div className="flex items-center justify-between rounded-xl border-2 border-red-300 bg-red-50 px-5 py-3 dark:border-red-700 dark:bg-red-900/20">
                    <div className="flex items-center gap-2 text-red-700 dark:text-red-400">
                      <WifiOff className="h-5 w-5 shrink-0" />
                      <div>
                        <span className="text-sm font-semibold">Cluster unreachable</span>
                        {data.cluster_connection_state && (
                          <span className="ml-2 text-xs text-red-600 dark:text-red-400">({data.cluster_connection_state})</span>
                        )}
                        <p className="text-xs text-red-600 dark:text-red-400">Addon health data below reflects the last known state and may be stale.</p>
                      </div>
                    </div>
                    <button
                      onClick={() => window.dispatchEvent(new CustomEvent('open-assistant', { detail: `Cluster ${name} is unreachable (${data.cluster_connection_state}). What could be wrong and how can I fix it?` }))}
                      className="flex shrink-0 items-center gap-1.5 rounded-lg border border-red-200 bg-[#f0f7ff] px-3 py-1.5 text-xs text-red-700 hover:bg-red-50 dark:border-red-800 dark:bg-gray-800 dark:text-red-400"
                    >
                      <MessageSquare className="h-3.5 w-3.5" />
                      Ask AI
                    </button>
                  </div>
                );
              })()}
              {computedStatus === 'connected' && (
                <div className="flex items-center gap-2 rounded-xl border-2 border-green-300 bg-green-50 px-5 py-3 dark:border-green-700 dark:bg-green-900/20">
                  <Wifi className="h-5 w-5 shrink-0 text-green-600 dark:text-green-400" />
                  <div>
                    <span className="text-sm font-semibold text-green-700 dark:text-green-400">Cluster connected</span>
                    {testResult && testResult !== 'testing' && (testResult.server_version) && (
                      <span className="ml-2 text-xs text-green-600 dark:text-green-400">({testResult.server_version})</span>
                    )}
                  </div>
                </div>
              )}

              {/* ArgoCD connection error banner — only shown when cluster is NOT unreachable (edge case: connected but ArgoCD error) */}
              {computedStatus !== 'unreachable' && data.argocd_connection_status && data.argocd_connection_status !== 'Successful' && (
                <div className="flex items-start justify-between gap-3 rounded-xl ring-2 ring-red-300 bg-red-50 px-5 py-4 dark:ring-red-700 dark:bg-red-950/30">
                  <div className="flex items-start gap-3">
                    <AlertTriangle className="h-5 w-5 shrink-0 text-red-600 dark:text-red-400 mt-0.5" />
                    <div>
                      <p className="text-sm font-semibold text-red-700 dark:text-red-400">ArgoCD Connection Failed</p>
                      {data.argocd_connection_message && (
                        <p className="mt-0.5 text-xs text-red-600 dark:text-red-400">{data.argocd_connection_message}</p>
                      )}
                    </div>
                  </div>
                  <button
                    onClick={() => window.dispatchEvent(new CustomEvent('open-assistant', { detail: `ArgoCD cannot connect to cluster ${name}. Error: ${data.argocd_connection_message}. What could cause this and how do I fix it?` }))}
                    className="flex shrink-0 items-center gap-1.5 rounded-lg border border-red-200 bg-[#f0f7ff] px-3 py-1.5 text-xs text-red-700 hover:bg-red-50 dark:border-red-800 dark:bg-gray-800 dark:text-red-400"
                  >
                    <MessageSquare className="h-3.5 w-3.5" />
                    Ask AI
                  </button>
                </div>
              )}
            </>
          )}

          {/* Addons section */}
          {activeSection === 'addons' && (
            <>
              {/* Section header with Deploy Addon button */}
              <div className="flex items-center justify-between">
                <h3 className="text-lg font-semibold text-[#0a2a4a] dark:text-gray-100">Addons</h3>
                <RoleGuard roles={['admin', 'operator']}>
                  <button
                    type="button"
                    onClick={handleOpenDeployDialog}
                    className="inline-flex items-center gap-1.5 rounded-lg bg-teal-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-teal-700 dark:bg-teal-700 dark:hover:bg-teal-600"
                  >
                    <Plus className="h-4 w-4" />
                    Deploy Addon
                  </button>
                </RoleGuard>
              </div>

              {/* Deploy Addon Dialog */}
              <Dialog open={deployDialogOpen} onOpenChange={(open) => { setDeployDialogOpen(open); if (!open) { setSelectedAddon(null); setDeployResult(null); } }}>
                <DialogContent className="max-w-lg bg-[#f0f7ff] dark:bg-gray-800">
                  <DialogHeader>
                    <DialogTitle className="text-[#0a2a4a] dark:text-gray-100">Deploy Addon to {name}</DialogTitle>
                    <DialogDescription className="text-[#3a6a8a] dark:text-gray-400">
                      Select an addon from the catalog to enable on this cluster. A pull request will be created.
                    </DialogDescription>
                  </DialogHeader>

                  {catalogLoading && (
                    <div className="flex items-center justify-center py-8">
                      <Loader2 className="h-6 w-6 animate-spin text-teal-600" />
                      <span className="ml-2 text-sm text-[#3a6a8a] dark:text-gray-400">Loading catalog...</span>
                    </div>
                  )}

                  {catalogError && (
                    <p className="text-sm text-red-600 dark:text-red-400">{catalogError}</p>
                  )}

                  {!catalogLoading && !catalogError && catalogAddons.length === 0 && (
                    <p className="py-4 text-center text-sm text-[#3a6a8a] dark:text-gray-400">
                      All catalog addons are already enabled on this cluster.
                    </p>
                  )}

                  {!catalogLoading && !catalogError && catalogAddons.length > 0 && !deployResult && (
                    <div className="max-h-64 space-y-1.5 overflow-y-auto">
                      {catalogAddons.map((addon) => (
                        <button
                          key={addon.addon_name}
                          type="button"
                          onClick={() => setSelectedAddon(addon)}
                          className={`w-full rounded-lg px-3 py-2.5 text-left text-sm ring-2 transition-colors ${
                            selectedAddon?.addon_name === addon.addon_name
                              ? 'bg-teal-50 ring-teal-500 dark:bg-teal-900/20 dark:ring-teal-400'
                              : 'bg-[#f0f7ff] ring-[#6aade0] hover:bg-[#d6eeff] dark:bg-gray-700 dark:ring-gray-600 dark:hover:bg-gray-600'
                          }`}
                        >
                          <span className="font-medium capitalize text-[#0a2a4a] dark:text-gray-100">{addon.addon_name}</span>
                          <span className="ml-2 text-xs text-[#5a8aaa] dark:text-gray-400">v{addon.version}</span>
                          {addon.namespace && (
                            <span className="ml-2 text-xs text-[#5a8aaa] dark:text-gray-400">({addon.namespace})</span>
                          )}
                        </button>
                      ))}
                    </div>
                  )}

                  {deployResult && (
                    <div className={`rounded-lg p-3 text-sm ${deployResult.error ? 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-400' : 'bg-green-50 text-green-700 dark:bg-green-900/20 dark:text-green-400'}`}>
                      {deployResult.error ? (
                        <p>{deployResult.error}</p>
                      ) : (
                        <div>
                          <p className="font-medium">Addon deploy requested successfully.</p>
                          {deployResult.prUrl && (
                            <a
                              href={deployResult.prUrl}
                              target="_blank"
                              rel="noopener noreferrer"
                              className="mt-1 inline-flex items-center gap-1 text-xs underline"
                            >
                              <ExternalLink className="h-3 w-3" />
                              View Pull Request
                            </a>
                          )}
                        </div>
                      )}
                    </div>
                  )}

                  <DialogFooter>
                    {!deployResult ? (
                      <>
                        <button
                          type="button"
                          onClick={() => setDeployDialogOpen(false)}
                          className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
                        >
                          Cancel
                        </button>
                        <button
                          type="button"
                          disabled={!selectedAddon || deploying}
                          onClick={handleDeployAddon}
                          className="inline-flex items-center gap-2 rounded-md bg-teal-600 px-4 py-2 text-sm font-medium text-white hover:bg-teal-700 disabled:opacity-50 dark:bg-teal-700 dark:hover:bg-teal-600"
                        >
                          {deploying && <Loader2 className="h-4 w-4 animate-spin" />}
                          Deploy
                        </button>
                      </>
                    ) : (
                      <button
                        type="button"
                        onClick={() => { setDeployDialogOpen(false); setDeployResult(null); setSelectedAddon(null); }}
                        className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
                      >
                        Close
                      </button>
                    )}
                  </DialogFooter>
                </DialogContent>
              </Dialog>

              {/* Admin: Addon Enable/Disable Toggles */}
              <RoleGuard adminOnly>
                <div className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:border-gray-700 dark:bg-gray-800">
                  <h3 className="mb-3 text-base font-semibold text-[#0a2a4a] dark:text-gray-100">Manage Addons</h3>
                  {Object.keys(addonToggles).length === 0 ? (
                    <p className="text-sm text-[#3a6a8a] dark:text-gray-500">No addons in catalog.</p>
                  ) : (
                    <div className="grid grid-cols-2 gap-x-6 gap-y-2 sm:grid-cols-3 lg:grid-cols-4">
                      {Object.keys(addonToggles).sort().map((addonName) => (
                        <label key={addonName} className="flex cursor-pointer items-center gap-2 text-sm">
                          <div
                            role="switch"
                            aria-checked={addonToggles[addonName]}
                            onClick={() =>
                              setAddonToggles((prev) => ({ ...prev, [addonName]: !prev[addonName] }))
                            }
                            className={`relative inline-flex h-5 w-9 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors focus:outline-none ${
                              addonToggles[addonName]
                                ? 'bg-teal-600'
                                : 'bg-[#c0ddf0] dark:bg-gray-600'
                            }`}
                          >
                            <span
                              className={`pointer-events-none inline-block h-4 w-4 rounded-full bg-[#f0f7ff] shadow-lg transition-transform ${
                                addonToggles[addonName] ? 'translate-x-4' : 'translate-x-0'
                              }`}
                            />
                          </div>
                          <span className={`capitalize ${addonToggles[addonName] !== originalToggles[addonName] ? 'font-semibold text-teal-600 dark:text-teal-400' : 'text-[#0a3a5a] dark:text-gray-300'}`}>
                            {addonName}
                          </span>
                        </label>
                      ))}
                    </div>
                  )}
                  {hasToggleChanges && (
                    <div className="mt-4 flex items-center gap-3">
                      <button
                        type="button"
                        onClick={handleApplyToggles}
                        disabled={applyingToggles}
                        className="inline-flex items-center gap-2 rounded-md bg-teal-600 px-4 py-2 text-sm font-medium text-white hover:bg-teal-700 disabled:opacity-50 dark:bg-teal-700 dark:hover:bg-teal-600"
                      >
                        {applyingToggles && <Loader2 className="h-4 w-4 animate-spin" />}
                        Apply Changes
                      </button>
                      <button
                        type="button"
                        onClick={() => { setAddonToggles({ ...originalToggles }); setToggleError(null); setToggleResult(null); }}
                        disabled={applyingToggles}
                        className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
                      >
                        Discard
                      </button>
                    </div>
                  )}
                  {toggleError && <p className="mt-2 text-sm text-red-600 dark:text-red-400">{toggleError}</p>}
                  {toggleResult && <p className="mt-2 text-sm text-green-600 dark:text-green-400">{toggleResult}</p>}
                </div>
              </RoleGuard>

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
              <div className="overflow-x-auto rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] shadow-sm dark:border-gray-700 dark:bg-gray-800">
                <table className="w-full text-left text-sm">
                  <thead className="border-b border-[#6aade0] bg-[#d0e8f8] text-xs uppercase text-[#2a5a7a] dark:border-gray-700 dark:bg-gray-900 dark:text-gray-400">
                    <tr>
                      <th className="px-4 py-3">Status</th>
                      <th className="px-4 py-3">Addon Name</th>
                      <th className="px-4 py-3">Git Version</th>
                      <th className="px-4 py-3">ArgoCD Version</th>
                      <th className="px-4 py-3">Namespace</th>
                      <th className="px-4 py-3">Issues</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-[#6aade0] dark:divide-gray-700">
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
                          className="px-6 py-8 text-center text-[#3a6a8a] dark:text-gray-500"
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

          {/* Config section */}
          {activeSection === 'config' && (
            <div className="space-y-6">
              {configDiffLoading && <LoadingState message="Loading config..." />}
              {configDiffError && (
                <ErrorState
                  message={configDiffError}
                  onRetry={() => {
                    setConfigDiffError(null);
                    setConfigFetched(false);
                  }}
                />
              )}
              {clusterValuesYaml && (
                <YamlViewer yaml={clusterValuesYaml} title="Cluster Values" />
              )}
              {configDiff && (
                <ConfigOverridesPanel
                  data={configDiff}
                  loading={false}
                  error={null}
                  onRetry={fetchConfigDiff}
                />
              )}
              {!configDiffLoading && !configDiffError && !configDiff && !clusterValuesYaml && (
                <p className="text-sm text-[#2a5a7a]">No configuration overrides for this cluster.</p>
              )}
            </div>
          )}

          {/* Pull Requests section */}
          {activeSection === 'prs' && (
            <PendingPRsPanel
              cluster={name}
              onMergeDetected={(pr: TrackedPR) => {
                showToast(`PR #${pr.pr_id} merged -- ${pr.cluster ?? ''} ${pr.operation}`)
                void fetchData()
              }}
            />
          )}

          {/* History section */}
          {activeSection === 'history' && (
            <ClusterHistorySection clusterName={name!} />
          )}
        </div>
      </div>
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
    <tr className="hover:bg-[#d6eeff] dark:hover:bg-gray-700">
      <td className="px-4 py-3">
        {addon.status ? (
          <StatusBadge status={addon.status} />
        ) : (
          <span className="text-[#3a6a8a] dark:text-gray-500">--</span>
        )}
      </td>
      <td className="px-4 py-3 font-medium text-[#0a2a4a] dark:text-gray-100">
        <div className="flex items-center gap-2">
          {capitalizeAddonName(addon.addon_name)}
          {addon.argocd_application_name && argocdBaseURL && (
            <a
              href={`${argocdBaseURL}/applications/${addon.argocd_application_name}`}
              target="_blank"
              rel="noopener noreferrer"
              onClick={(e) => e.stopPropagation()}
              className="text-[#3a6a8a] hover:text-teal-600 dark:hover:text-teal-400"
              title={`Open ${addon.argocd_application_name} in ArgoCD`}
            >
              <ExternalLink className="h-3.5 w-3.5" />
            </a>
          )}
        </div>
      </td>
      <td className="px-4 py-3 font-mono text-xs text-[#1a4a6a] dark:text-gray-400">
        {addon.has_version_override
          ? (addon.custom_version ?? addon.environment_version ?? addon.git_version ?? '--')
          : (addon.environment_version ?? addon.git_version ?? '--')}
      </td>
      <td className="px-4 py-3 font-mono text-xs text-[#1a4a6a] dark:text-gray-400">
        {addon.argocd_deployed_version ?? '--'}
      </td>
      <td className="px-4 py-3 text-[#1a4a6a] dark:text-gray-400">
        {addon.argocd_namespace ?? '--'}
      </td>
      <td className="px-4 py-3">
        {allIssues.length > 0 ? (
          <div>
            <ul className="space-y-0.5 text-xs text-[#1a4a6a] dark:text-gray-400">
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
                className="mt-1 inline-flex items-center gap-0.5 text-xs font-medium text-teal-600 hover:text-teal-800 dark:text-teal-400 dark:hover:text-teal-300"
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
      <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-8 text-center shadow-sm dark:border-gray-700 dark:bg-gray-800">
        <p className="text-[#2a5a7a] dark:text-gray-400">
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
          className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] shadow-sm dark:border-gray-700 dark:bg-gray-800"
        >
          <div className="flex items-center gap-2 border-b border-[#6aade0] px-4 py-3 dark:border-gray-700">
            <h3 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
              {capitalizeAddonName(entry.addon_name)}
            </h3>
            <span className="inline-flex items-center rounded-full bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-800 dark:bg-amber-900/30 dark:text-amber-400">
              Custom overrides
            </span>
          </div>
          <div className="grid grid-cols-1 divide-y md:grid-cols-2 md:divide-x md:divide-y-0 divide-[#6aade0] dark:divide-gray-700">
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
export default ClusterDetail

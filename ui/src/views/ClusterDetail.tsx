import { useState, useEffect, useMemo, useCallback, useRef } from 'react';
import { Link, useParams, useNavigate, useSearchParams } from 'react-router-dom';
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
  WifiOff,
  MessageSquare,
  Tag,
  Loader2,
  Package,
  FileCode,
  Clock,
  GitPullRequest,
  Wifi,
  ScanSearch,
  Pencil,
  KeyRound,
  Plus,
  RefreshCw,
  RotateCcw,
  X,
  ShieldCheck,
  Sparkles,
  Settings,
} from 'lucide-react';
import { api, deregisterCluster, updateClusterAddons, updateClusterSettings, testClusterConnection, isTestClusterUnavailable, fetchTrackedPRs } from '@/services/api';
import type { TestClusterUnavailable, PRWriteResult } from '@/services/api';
import { PRResultBanner, extractPR } from '@/components/PRFeedback';
import { EnableAddonPicker } from '@/components/EnableAddonPicker';
import type { ClusterComparisonResponse, AddonComparisonStatus, ConfigDiffResponse, SyncActivityEntry, VerifyStep } from '@/services/models';
import { StatCard } from '@/components/StatCard';
import { StatusBadge } from '@/components/StatusBadge';
import { ConnectivityBadge } from '@/components/ConnectivityBadge';
import { SHARKO_CONN_LABEL, SHARKO_CONN_TOOLTIP } from '@/components/WhoseConnectionLabel';
import {
  TEST_CONNECTION_LABEL,
  TEST_CONNECTION_HINT,
  CHECK_PERMISSIONS_LABEL,
  CHECK_PERMISSIONS_HINT,
} from '@/components/ClusterActionHints';
import { ClusterTypeBadge } from '@/components/ClusterTypeBadge';
import { InfoHint } from '@/components/InfoHint';
import { LoadingState } from '@/components/LoadingState';
import { ErrorState } from '@/components/ErrorState';
import { EmptyState } from '@/components/EmptyState';
import { YamlViewer } from '@/components/YamlViewer';
import { RoleGuard } from '@/components/RoleGuard';
import { ConfirmationModal } from '@/components/ConfirmationModal';
import { DetailNavPanel } from '@/components/DetailNavPanel';
import { DiagnoseModal } from '@/components/DiagnoseModal';
import { PendingPRsPanel } from '@/components/PendingPRsPanel';
import { PerClusterAddonOverridesEditor } from '@/components/PerClusterAddonOverridesEditor';
import { showToast } from '@/components/ToastNotification';
import { prettyOperation } from '@/lib/utils';
import type { ConnectionsListResponse, TrackedPR } from '@/services/models';

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

// Per-error-code copy + optional action link for the Test-unavailable
// banner. Production targets are self-hosted K8s + AWS-managed clusters;
// kind/minikube are dev-only and must not anchor production-facing copy.
// The aws-iam-cluster-auth docs link points at an in-app placeholder
// today; it will resolve once the operator-docs page lands.
function TestUnavailableBanner({ result }: { result: TestClusterUnavailable }) {
  let title: string;
  let body: string;
  let actionTo: string | null = null;
  let actionLabel: string | null = null;

  switch (result.error_code) {
    case 'no_secrets_backend':
      title = 'Cluster test unavailable';
      body = result.error;
      actionTo = '/settings?section=connections';
      actionLabel = 'Open Settings → Connections';
      break;
    case 'argocd_provider_iam_required':
      title = 'AWS IAM authentication required';
      body =
        "This cluster uses AWS IAM authentication. Configure AWS credentials for the Sharko pod's role (IRSA, EC2 instance profile, or Pod Identity) to enable Test connection for AWS-managed clusters.";
      actionTo = '/docs/operator/aws-iam-cluster-auth';
      actionLabel = 'Open IAM setup guide';
      break;
    case 'argocd_provider_exec_unsupported':
      title = 'Exec-plugin authentication not supported';
      body =
        'This cluster uses exec-plugin auth (e.g. gcloud, azure-cli, aws-iam-authenticator). Exec plugins are not supported in Sharko v1.x — tracked for v2.';
      // No action link — surface the limitation; there is no in-app fix path.
      break;
    case 'argocd_provider_unsupported_auth':
      title = 'Unrecognized cluster authentication';
      body =
        "Unrecognized authentication shape in this cluster's ArgoCD Secret. Inspect the Secret manually in the argocd namespace (kubectl -n argocd get secret <name> -o yaml).";
      // No action link — manual inspection is the only path.
      break;
  }

  return (
    <div
      role="alert"
      data-testid="test-unavailable-banner"
      data-error-code={result.error_code}
      className="mt-2 rounded-lg ring-2 ring-amber-300 bg-amber-50 px-3 py-2 dark:ring-amber-700 dark:bg-amber-950/30"
    >
      <p className="text-xs font-semibold text-amber-800 dark:text-amber-300">{title}</p>
      <p className="mt-0.5 text-xs text-amber-700 dark:text-amber-300">{body}</p>
      {actionTo && actionLabel && (
        <Link
          to={actionTo}
          className="mt-1 inline-block text-xs font-medium text-amber-800 underline hover:text-amber-900 dark:text-amber-300 dark:hover:text-amber-200"
        >
          {actionLabel}
        </Link>
      )}
    </div>
  );
}

function shouldTruncateIssues(issues: string[]): boolean {
  return issues.join(' ').length > 100;
}

export function ClusterDetail() {
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const [data, setData] = useState<ClusterComparisonResponse | null>(null);
  // Pending PRs scoped to this cluster, indexed by addon name. We fetch
  // ALL open PRs for this cluster once and bucket per-addon in the FE so
  // the addons table can render an inline pending-PR badge without N
  // round-trips. PRs that didn't attribute an `addon` (e.g. cluster
  // register/deregister) are dropped from this map — they belong to the
  // cluster's separate PR panel.
  const [pendingPRsByAddon, setPendingPRsByAddon] = useState<Record<string, TrackedPR[]>>({});
  const [loading, setLoading] = useState(true);
  const [isRefreshing, setIsRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [expandedRows, setExpandedRows] = useState<Set<string>>(new Set());
  const [searchParams, setSearchParams] = useSearchParams();
  const activeSection = searchParams.get('section') || 'addons';
  // When switching section, preserve other query params (notably ?addon=…
  // which drives the deep-link scroll + highlight for the addons section).
  const setActiveSection = (s: string) => {
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.set('section', s);
        return next;
      },
      { replace: true },
    );
  };
  // Deep-link: /clusters/X?section=addons&addon=Y → scroll to + briefly ring
  // the addon row. Read once; the useEffect below consumes it.
  const highlightAddon = searchParams.get('addon') || '';
  const [highlightedAddon, setHighlightedAddon] = useState<string>('');

  // When the page loads (or the addon query param changes) on the addons
  // section, turn the highlight on. Fade it out after 2s by clearing the
  // state — ComparisonRow removes its ring class. We intentionally DON'T
  // strip the ?addon= from the URL so the browser back-button returns to
  // the addon-page cleanly.
  useEffect(() => {
    if (!highlightAddon) return;
    if (activeSection !== 'addons') {
      // Moran landed here with ?addon=X but on a different section; switch
      // into addons so the highlight can actually run.
      setActiveSection('addons');
      return;
    }
    setHighlightedAddon(highlightAddon);
    const t = setTimeout(() => setHighlightedAddon(''), 2000);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [highlightAddon, activeSection]);
  const [configDiff, setConfigDiff] = useState<ConfigDiffResponse | null>(null);
  const [configDiffLoading, setConfigDiffLoading] = useState(false);
  const [configDiffError, setConfigDiffError] = useState<string | null>(null);
  const [clusterValuesYaml, setClusterValuesYaml] = useState<string | null>(null);
  const [configFetched, setConfigFetched] = useState(false);
  const [argocdBaseURL, setArgocdBaseURL] = useState<string>('');
  // Values editor — derive a GitHub deep-link from the active
  // connection so users can pop into github.com to see the file in context.
  const [gitRepoBase, setGitRepoBase] = useState<string>('');
  const [gitDefaultBranch, setGitDefaultBranch] = useState<string>('main');

  // Remove cluster
  const [removeModalOpen, setRemoveModalOpen] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [removeError, setRemoveError] = useState<string | null>(null);

  // Test connection
  const [testResult, setTestResult] = useState<
    | { reachable?: boolean; success?: boolean; server_version?: string; error?: string; error_message?: string; suggestions?: string[]; steps?: VerifyStep[] }
    | TestClusterUnavailable
    | 'testing'
    | null
  >(null);
  const [diagnoseOpen, setDiagnoseOpen] = useState(false);

  // Secret path editing
  const [editingSecretPath, setEditingSecretPath] = useState(false);
  const [secretPathValue, setSecretPathValue] = useState('');
  const [secretPathSaving, setSecretPathSaving] = useState(false);
  // Defect 2.2: secret-path save now keeps the PR result so we can render a
  // clickable PR link (PRResultBanner) instead of dumping the raw URL as text.
  // `message` carries the non-PR / error fallback.
  const [secretPathResult, setSecretPathResult] = useState<{ pr?: PRWriteResult; message?: string } | null>(null);

  // AI-enabled state — fetched once on mount so the "Ask AI" button on
  // sync_failing rows knows whether to render.
  const [aiEnabled, setAiEnabled] = useState<boolean>(false);

  // Addon toggles
  const [addonToggles, setAddonToggles] = useState<Record<string, boolean>>({});
  const [originalToggles, setOriginalToggles] = useState<Record<string, boolean>>({});
  const [applyingToggles, setApplyingToggles] = useState(false);
  const [toggleError, setToggleError] = useState<string | null>(null);
  // Defect 2.2: apply-toggles keeps the PR result so the success line is a
  // clickable PR link (PRResultBanner) instead of "Changes applied. PR: <url>".
  const [toggleResult, setToggleResult] = useState<{ pr?: PRWriteResult; message?: string } | null>(null);

  // Enable-addon picker (Manage Addons card)
  const [pickerOpen, setPickerOpen] = useState(false);
  // Catalog names fetched lazily when the picker opens.
  const [pickerCatalogNames, setPickerCatalogNames] = useState<string[]>([]);
  const [pickerCatalogLoading, setPickerCatalogLoading] = useState(false);
  const [pickerCatalogError, setPickerCatalogError] = useState<string | null>(null);

  // Compute display status from test result + server state
  const computedStatus = useMemo((): string => {
    if (testResult && testResult !== 'testing') {
      // A "test unavailable" result (no secrets backend on the active
      // connection) does NOT mean the cluster is unreachable — it means
      // the test feature itself is unavailable. Fall through to the
      // server-reported state instead of marking the cluster red.
      if (!isTestClusterUnavailable(testResult)) {
        if (testResult.reachable || testResult.success) return 'connected';
        return 'unreachable';
      }
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

  const fetchData = useCallback(async (background = false) => {
    if (!name) return;
    try {
      if (background) {
        setIsRefreshing(true);
      } else {
        setLoading(true);
      }
      setError(null);
      // Fetch cluster-scoped open PRs alongside the comparison so the
      // addons table can render per-row pending-PR badges. The .catch
      // keeps the page rendering when the PR-tracker is disabled.
      const [result, connections, prsResp] = await Promise.all([
        api.getClusterComparison(name),
        api.getConnections().catch(() => null),
        fetchTrackedPRs({ status: 'open', cluster: name }).catch(() => null),
      ]);
      setData(result);

      // Bucket pending PRs by addon so ComparisonRow can look up its own
      // row in O(1). PRs without an `addon` field (cluster register/
      // deregister, init) are dropped — they belong on the cluster's PRs
      // section, not in the addon row.
      if (prsResp?.prs) {
        const byAddon: Record<string, TrackedPR[]> = {};
        for (const pr of prsResp.prs) {
          if (!pr.addon) continue;
          if (pr.last_status.toLowerCase() !== 'open') continue;
          if (!byAddon[pr.addon]) byAddon[pr.addon] = [];
          byAddon[pr.addon].push(pr);
        }
        setPendingPRsByAddon(byAddon);
      } else {
        setPendingPRsByAddon({});
      }
      // Initialize addon toggles from cluster data. Only include rows that are
      // genuine catalog addons (git_configured === true). Rows with status
      // 'untracked_in_argocd' or 'sharko_system' are NOT catalog addons and
      // must never enter the toggle map — if they did, they'd appear in the
      // picker and could be sent to the PATCH endpoint as labels, producing
      // an inconsistent gitops state (V2-cleanup-32 fix).
      const toggleMap: Record<string, boolean> = {};
      result.addon_comparisons.forEach((a: { addon_name: string; git_enabled: boolean; git_configured: boolean; status?: string }) => {
        if (!a.git_configured) return;
        if (a.status === 'untracked_in_argocd' || a.status === 'sharko_system') return;
        toggleMap[a.addon_name] = a.git_enabled;
      });
      setAddonToggles(toggleMap);
      setOriginalToggles(toggleMap);
      if (connections) {
        const active = (connections as ConnectionsListResponse).connections.find(
          (c) => c.name === (connections as ConnectionsListResponse).active_connection || c.is_active
        );
        if (active?.argocd_server_url) {
          setArgocdBaseURL(active.argocd_server_url.replace(/\/$/, ''));
        }
        if (active?.git_provider === 'github' && active.git_repo_identifier) {
          setGitRepoBase(`https://github.com/${active.git_repo_identifier}`);
        }
        if (active?.gitops?.base_branch) {
          setGitDefaultBranch(active.gitops.base_branch);
        }
      }
    } catch (e: unknown) {
      if (!background) {
        setError(
          e instanceof Error
            ? e.message
            : `Failed to load comparison for cluster: ${name}`,
        );
      }
    } finally {
      setLoading(false);
      setIsRefreshing(false);
    }
  }, [name]);

  const handleRefresh = useCallback(() => {
    void fetchData(true);
  }, [fetchData]);

  // Stable onSaved for the per-cluster overrides editor — passing a fresh
  // arrow function on every render would defeat the editor's React.memo
  // prop-equality check and re-trigger its useEffects every parent tick.
  const handlePerClusterOverridesSaved = useCallback(() => {
    setConfigFetched(false);
  }, []);

  const handleRemoveCluster = useCallback(async () => {
    if (!name) return;
    setRemoving(true);
    setRemoveError(null);
    try {
      // Let the global GitOps auto-merge setting decide — don't pass an override.
      const result = await deregisterCluster(name);
      const git = result?.git;
      const merged = git?.merged ?? false;
      const prUrl = git?.pr_url || git?.pull_request_url;
      const prId = git?.pr_id;
      if (merged) {
        showToast(`Cluster "${name}" removed.`, 'success');
        navigate('/clusters');
        return;
      }
      // Manual path: PR opened, cluster stays listed until it merges. Close
      // the dialog and surface the PR so it doesn't look like nothing happened.
      setRemoveModalOpen(false);
      setRemoving(false);
      showToast(
        'Removal PR opened for review. The cluster stays listed until it merges.',
        'success',
        prUrl ? { url: prUrl, id: prId } : undefined,
      );
      // Refresh so any pending-PR indicator picks up the new open PR.
      void fetchData(true);
    } catch (e: unknown) {
      setRemoveError(e instanceof Error ? e.message : 'Failed to remove cluster');
      setRemoving(false);
    }
  }, [name, navigate, fetchData]);

  const hasToggleChanges = useMemo(() => {
    return Object.keys(addonToggles).some((k) => addonToggles[k] !== originalToggles[k]);
  }, [addonToggles, originalToggles]);

  const handleApplyToggles = useCallback(async () => {
    if (!name) return;
    setApplyingToggles(true);
    setToggleError(null);
    setToggleResult(null);
    try {
      // Send only keys that are currently enabled OR being staged for a change
      // (enabled→disabled or never-enabled→enabled). Do NOT send keys for addons
      // that are disabled-in-git with no pending change — those are catalog addons
      // the operator never touched on this cluster. Sending them as `false` would
      // add spurious labels to managed-clusters.yaml (V2-cleanup-32 fix).
      const payload: Record<string, boolean> = {};
      for (const [k, v] of Object.entries(addonToggles)) {
        const wasEnabled = originalToggles[k] === true;
        const isEnabled = v === true;
        // Include if currently enabled, was enabled (being removed), or is newly staged
        if (wasEnabled || isEnabled) {
          payload[k] = v;
        }
      }
      const result = await updateClusterAddons(name, payload);
      const { prUrl } = extractPR(result);
      setToggleResult(prUrl ? { pr: result } : { message: 'Changes applied successfully.' });
      setOriginalToggles({ ...addonToggles });
    } catch (e: unknown) {
      setToggleError(e instanceof Error ? e.message : 'Failed to apply changes');
    } finally {
      setApplyingToggles(false);
    }
  }, [name, addonToggles, originalToggles]);

  const handleTestConnection = useCallback(async () => {
    if (!name) return;
    setTestResult('testing');
    try {
      const result = await testClusterConnection(name);
      setTestResult(result);
      // Skip the refetch when the test came back as "unavailable" —
      // there's no new server-side state to observe.
      if (isTestClusterUnavailable(result)) {
        return;
      }
      // Refetch cluster data so server-side computed status is up to date
      if (result.reachable || result.success) {
        void fetchData();
      }
    } catch (err) {
      setTestResult({ reachable: false, error: err instanceof Error ? err.message : 'Failed' });
    }
  }, [name, fetchData]);

  // Open the enable-addon picker and lazily fetch the real catalog so the
  // picker offers every catalog addon, not just the ones already in
  // addonToggles. Reuses api.getAddonCatalog() — the same call AddonCatalog
  // view uses (no new endpoint). Available = catalog names minus currently
  // enabled+staged, which the picker computes from pickerEnabledNames.
  const handleOpenPicker = useCallback(async () => {
    setPickerOpen(true);
    setPickerCatalogError(null);
    if (pickerCatalogNames.length > 0) return; // already fetched
    setPickerCatalogLoading(true);
    try {
      const catalog = await api.getAddonCatalog();
      setPickerCatalogNames(catalog.addons.map((a) => a.addon_name));
    } catch (e: unknown) {
      setPickerCatalogError(e instanceof Error ? e.message : 'Failed to load catalog');
    } finally {
      setPickerCatalogLoading(false);
    }
  }, [pickerCatalogNames]);

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

  // Fetch AI-enabled status once on mount. The AI assistant is OPT-IN and
  // hidden by default (V2-cleanup-55.4, master gate in Layout.tsx): every
  // "Ask AI" affordance on this page — the connection banners and the
  // sync_failing rows — renders only when an AI provider is configured.
  useEffect(() => {
    api
      .getAIStatus()
      .then((res) => setAiEnabled(res.enabled))
      .catch(() => setAiEnabled(false));
  }, []);

  // Adaptive polling: 10s while any addon is actively changing (deploying or
  // sync_failing), 30s otherwise. The interval is recreated whenever the
  // "active" state flips so the cadence adjusts immediately.
  const hasActiveAddon = useMemo(() => {
    if (!data) return false;
    return data.addon_comparisons.some(
      (a) => a.status === 'deploying' || a.status === 'sync_failing',
    );
  }, [data]);

  useEffect(() => {
    const intervalMs = hasActiveAddon ? 10_000 : 30_000;
    const interval = setInterval(() => {
      void fetchData(true);
    }, intervalMs);
    return () => clearInterval(interval);
  }, [fetchData, hasActiveAddon]);

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
          // sync_failing is a real issue — show it in the with_issues filter.
          // deploying is informational (active rollout with no error) — not an issue.
          return ['progressing', 'unknown_health', 'unhealthy', 'unknown_state', 'sync_failing'].includes(
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

  // Defensive guard for the pending-PR pseudo-row case. A "discovered"
  // cluster whose registration PR hasn't merged yet will 404 here.
  // We can't reliably distinguish "404 because pending PR" from "404
  // because misspelled URL" without the pending-registrations list cached.
  // Show an empty state with a link back to /clusters — the operator gets
  // one click to the surface that DOES know about pending PRs.
  if (error) {
    const lower = error.toLowerCase();
    const looksLikeNotFound =
      lower.includes('not found') ||
      lower.includes('404') ||
      lower.includes('cluster not found');
    if (looksLikeNotFound) {
      return (
        <EmptyState
          title={`Cluster "${name}" not found`}
          description={
            'This cluster is not in managed-clusters.yaml. ' +
            'It may have been registered via a pending PR that has not yet merged. ' +
            'Open the Clusters page and look under "Pending Registrations" for an open PR.'
          }
          action={
            <button
              type="button"
              onClick={() => navigate('/clusters')}
              className="inline-flex items-center gap-2 rounded-md bg-[#0a2a4a] px-4 py-2 text-sm font-semibold text-white hover:bg-[#0d3558] dark:bg-blue-700 dark:hover:bg-blue-600"
            >
              <ArrowLeft className="h-4 w-4" />
              Back to Clusters
            </button>
          }
        />
      );
    }
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
    // Canonical vocabulary (V2-cleanup-61.2, D1+D2): "Missing from ArgoCD"
    // is the problem state (red); "Not managed" is the attention state.
    {
      key: 'missing_in_argocd',
      title: 'Missing from ArgoCD',
      value: data.total_missing_in_argocd,
      color: 'error',
      icon: <CloudOff className="h-5 w-5" />,
    },
    {
      key: 'untracked',
      title: 'Not managed',
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
        { key: 'addons', label: 'Addons', badge: data ? data.addon_comparisons.length : undefined, icon: Package },
        { key: 'config', label: 'Config', icon: FileCode },
        { key: 'history', label: 'History', icon: Clock },
        { key: 'settings', label: 'Settings', icon: Settings },
      ],
    },
    {
      items: [
        { key: 'remove', label: 'Remove Cluster', destructive: true },
      ],
    },
  ];

  // Manage-Addons list state (V2-cleanup-81.1) — computed once here so both
  // the "+ Enable addon" button in the Addons section header and the
  // enabled-list body below can share it without duplicating the logic.
  // Visible-row source: only catalog rows (git_configured=true) — junk
  // (untracked/sharko_system) was filtered out at toggle-map seeding time.
  const allCatalogNames = Object.keys(addonToggles).sort();
  // noCatalog: true when addonToggles has no entries AND no catalog was
  // fetched for the picker yet. After the picker fetch we have
  // pickerCatalogNames, which is the authoritative source for what's
  // available to enable. If even that is empty, there's nothing in the catalog.
  const noCatalog = allCatalogNames.length === 0 && pickerCatalogNames.length === 0;
  // Which addons are currently desired-true (original + staged enables)?
  // Excludes addons staged for removal (still in list, but they retain
  // their row with a pending-removal mark).
  const enabledRows = allCatalogNames.filter((n) => addonToggles[n]);
  const removedRows = allCatalogNames.filter(
    (n) => originalToggles[n] && !addonToggles[n],
  );
  // Rows to show: currently enabled OR staged for removal.
  const visibleRows = Array.from(
    new Set([...enabledRows, ...removedRows]),
  ).sort();
  // The picker must not show addons that are already enabled OR staged for
  // enable (i.e. addonToggles[n] === true).
  const pickerEnabledNames = new Set(
    Object.entries(addonToggles)
      .filter(([, v]) => v)
      .map(([k]) => k),
  );
  // Connectivity-check system row visibility.
  // Values: 'verified_check' | 'check_pending' | 'check_failed'
  const connStatus = data?.cluster?.connectivity_status ?? '';
  const showCheckRow =
    connStatus === 'verified_check' ||
    connStatus === 'check_pending' ||
    connStatus === 'check_failed';

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

      {/* Heading + actions. Cluster name, type, version, and connection
        * status live in the vitals ribbon below (V2-cleanup-78.1) so they
        * appear once, consistently, on every section instead of duplicated
        * here and again in the old Overview stat cards. */}
      <div className="flex items-start justify-between">
        <div>
          <p className="text-sm text-[#2a5a7a] dark:text-gray-400">
            Kubernetes cluster managed by ArgoCD — deployed addons, health, and configuration overrides.
          </p>
          {testResult && testResult !== 'testing' && isTestClusterUnavailable(testResult) && (
            // The Test endpoint can be unavailable for several reasons —
            // see the typed `error_code` values on TestClusterUnavailable.
            // Branch-specific copy + an action link per code gives the
            // operator a clear next step instead of a generic "test
            // failed" message. The cluster itself is NOT classified as
            // "Unreachable" in any of these branches — only the test
            // feature is unavailable. See computedStatus above.
            <TestUnavailableBanner result={testResult} />
          )}
          {testResult && testResult !== 'testing' && !isTestClusterUnavailable(testResult) && (
            <div className="mt-2">
              {/* Step-by-step test results */}
              {testResult.steps && testResult.steps.length > 0 && (
                <div className="mb-2 rounded-lg bg-[#f8fbff] p-3 ring-1 ring-[#d0e4f5] dark:bg-gray-800 dark:ring-gray-700">
                  {/* The Test connection flow is Sharko's own connection — say so (V2-cleanup-55.3). */}
                  <div className="mb-2 flex items-center gap-1">
                    <p className="cursor-help text-xs font-semibold text-[#0a2a4a] dark:text-gray-200" title={SHARKO_CONN_TOOLTIP}>
                      Connection test results ({SHARKO_CONN_LABEL}):
                    </p>
                    {/* V2-cleanup-61.4 (G3): click/focus affordance for the
                      * explanation above — a hover-only title never reaches
                      * touch or keyboard users. V2-cleanup-65.1: echoes the
                      * same "what does Test connection do" one-liner shown
                      * next to the button, plus the whose-connection note. */}
                    <InfoHint text={`${TEST_CONNECTION_HINT} ${SHARKO_CONN_TOOLTIP}`} label="What does this mean?" />
                  </div>
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
              {/* Summary badge — Sharko's own connection result (V2-cleanup-55.3). */}
              <div className="flex items-center gap-1">
                <div title={SHARKO_CONN_TOOLTIP} className={`inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-medium ${
                  testResult.reachable || testResult.success
                    ? 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400'
                    : 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'
                }`}>
                  {testResult.reachable || testResult.success
                    ? `Connected${testResult.server_version ? ` \u2014 ${testResult.server_version}` : ''}`
                    : testResult.error || testResult.error_message || 'Unreachable'}
                </div>
                <InfoHint text={SHARKO_CONN_TOOLTIP} label="What does this mean?" />
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
        <div className="flex items-center gap-2">
          <button
            onClick={handleRefresh}
            className="rounded-md p-2 text-[#3a6a8a] hover:bg-[#d6eeff] dark:text-gray-400 dark:hover:bg-gray-700"
            title="Refresh"
          >
            <RefreshCw className={`h-4 w-4 ${isRefreshing ? 'animate-spin' : ''}`} />
          </button>
          <RoleGuard roles={['admin', 'operator']}>
            <button
              onClick={handleTestConnection}
              disabled={testResult === 'testing'}
              title={SHARKO_CONN_TOOLTIP}
              className="inline-flex items-center gap-1.5 rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-1.5 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
            >
              {testResult === 'testing' ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Wifi className="h-3.5 w-3.5" />}
              {TEST_CONNECTION_LABEL}
            </button>
            <InfoHint text={TEST_CONNECTION_HINT} label="What does Test connection do?" />
            <button
              onClick={() => setDiagnoseOpen(true)}
              className="inline-flex items-center gap-1.5 rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-1.5 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
            >
              <ScanSearch className="h-3.5 w-3.5" />
              {CHECK_PERMISSIONS_LABEL}
            </button>
            <InfoHint text={CHECK_PERMISSIONS_HINT} label="What does Check permissions do?" />
          </RoleGuard>
        </div>
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
        extraContent={
          <p className="text-xs text-[#5a8aaa] dark:text-gray-500">
            Auto-merge follows your{' '}
            <a href="/settings?section=gitops" className="underline hover:text-[#0a2a4a] dark:hover:text-gray-300">
              global GitOps setting
            </a>
            .
          </p>
        }
      />
      <DiagnoseModal
        clusterName={name ?? ''}
        open={diagnoseOpen}
        onClose={() => setDiagnoseOpen(false)}
      />
      {removeError && (
        <p className="text-sm text-red-600 dark:text-red-400">{removeError}</p>
      )}

      {/* Vitals ribbon — persistent across every section (V2-cleanup-78.1).
        * Opening a cluster now leads with its addons, so the identity +
        * connection info that used to live in the Overview tab's stat cards
        * is condensed into a slim strip that stays visible no matter which
        * tab is active. */}
      <div className="flex flex-wrap items-center gap-x-5 gap-y-2 rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] px-4 py-2.5 dark:ring-gray-700 dark:bg-gray-800">
        <div className="flex items-center gap-2">
          <h2 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">{data.cluster.name}</h2>
          <ClusterTypeBadge server={data.cluster.server_url} />
        </div>
        {data.cluster.server_version && (
          <div className="flex items-center gap-1.5 text-xs text-[#2a5a7a] dark:text-gray-400">
            <Tag className="h-3.5 w-3.5 text-teal-500" />
            <span className="text-[10px] uppercase tracking-wide text-[#5a8aaa] dark:text-gray-500">Cluster Version</span>
            <span className="font-mono font-medium text-[#0a2a4a] dark:text-gray-200">{data.cluster.server_version}</span>
          </div>
        )}
        <div className="flex items-center gap-2">
          <StatusBadge status={computedStatus} size="sm" />
          <ConnectivityBadge
            connectivityStatus={data.cluster.connectivity_status}
            connectivityDetail={data.cluster.connectivity_detail}
            sharkoStatus={data.cluster.sharko_status}
            lastTestAt={data.cluster.last_test_at}
            testFailing={data.cluster.test_failing}
            testErrorCode={data.cluster.test_error_code}
          />
        </div>
      </div>

      {/* Connection health banners — relocated from the old Overview tab so
        * a stale-data warning stays visible on every section, especially the
        * new default (Addons). The redundant green "Cluster connected"
        * banner was dropped here since the ribbon above already shows
        * connection status at a glance. */}
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
              {/* Ask AI — hidden unless an AI provider is configured (opt-in, V2-cleanup-55.4) */}
              {aiEnabled && (
                <button
                  onClick={() => window.dispatchEvent(new CustomEvent('open-assistant', { detail: { message: `ArgoCD cannot connect to cluster ${name}. Error: ${data.argocd_connection_message}. What could cause this and how do I fix it?`, nonce: crypto.randomUUID() } }))}
                  className="flex shrink-0 items-center gap-1.5 rounded-lg border border-red-200 bg-[#f0f7ff] px-3 py-1.5 text-xs text-red-700 hover:bg-red-50 dark:border-red-800 dark:bg-gray-800 dark:text-red-400"
                >
                  <MessageSquare className="h-3.5 w-3.5" />
                  Ask AI
                </button>
              )}
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
            {/* Ask AI — hidden unless an AI provider is configured (opt-in, V2-cleanup-55.4) */}
            {aiEnabled && (
              <button
                onClick={() => window.dispatchEvent(new CustomEvent('open-assistant', { detail: { message: `Cluster ${name} is unreachable (${data.cluster_connection_state}). What could be wrong and how can I fix it?`, nonce: crypto.randomUUID() } }))}
                className="flex shrink-0 items-center gap-1.5 rounded-lg border border-red-200 bg-[#f0f7ff] px-3 py-1.5 text-xs text-red-700 hover:bg-red-50 dark:border-red-800 dark:bg-gray-800 dark:text-red-400"
              >
                <MessageSquare className="h-3.5 w-3.5" />
                Ask AI
              </button>
            )}
          </div>
        );
      })()}

      {/* ArgoCD connection banner — distinguishes three states so a
        * cluster Sharko hasn't yet probed isn't mis-labelled as
        * "Connection Failed":
        *
        *   - argocd_connection_status missing / "Unknown" → neutral banner below
        *   - argocd_connection_status === "Successful"    → no banner (happy path)
        *   - anything else                                 → red "Connection Failed" banner
        *
        * The !== 'unreachable' guard prevents double-rendering when
        * the consolidated "Cluster Unreachable" banner is already
        * shown.
        */}
      {(() => {
        if (computedStatus === 'unreachable') return null;
        const argoStatus = data.argocd_connection_status;
        if (!argoStatus) return null;
        if (argoStatus === 'Successful') return null;
        const lowered = argoStatus.toLowerCase();
        // "Unknown" is not a failure — it's the absence of an
        // observation. Render a neutral "status unknown" banner
        // instead of the red Connection Failed copy.
        if (lowered === 'unknown' || lowered === '') {
          return (
            <div className="flex items-start gap-3 rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] px-5 py-3 dark:ring-gray-700 dark:bg-gray-800">
              <AlertTriangle className="h-5 w-5 shrink-0 text-[#3a6a8a] dark:text-gray-300 mt-0.5" />
              <div>
                <p className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Status unknown</p>
                <p className="mt-0.5 text-xs text-[#3a6a8a] dark:text-gray-400">Sharko has not yet observed an ArgoCD response for this cluster.</p>
              </div>
            </div>
          );
        }
        return (
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
            {/* Ask AI — hidden unless an AI provider is configured (opt-in, V2-cleanup-55.4) */}
            {aiEnabled && (
              <button
                onClick={() => window.dispatchEvent(new CustomEvent('open-assistant', { detail: { message: `ArgoCD cannot connect to cluster ${name}. Error: ${data.argocd_connection_message}. What could cause this and how do I fix it?`, nonce: crypto.randomUUID() } }))}
                className="flex shrink-0 items-center gap-1.5 rounded-lg border border-red-200 bg-[#f0f7ff] px-3 py-1.5 text-xs text-red-700 hover:bg-red-50 dark:border-red-800 dark:bg-gray-800 dark:text-red-400"
              >
                <MessageSquare className="h-3.5 w-3.5" />
                Ask AI
              </button>
            )}
          </div>
        );
      })()}

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
          {/* Addons section */}
          {activeSection === 'addons' && (
            <>
              {/* Section header with the single add-addon entry point
                * (V2-cleanup-81.1). "Deploy Addon" and the nested "Manage
                * Addons > Enable addon" both enabled a catalog addon on this
                * cluster — redundant, and a box-in-a-box under a section
                * already titled "Addons". Collapsed to this one button; the
                * enabled-addons list (still admin-only) sits directly below,
                * no card chrome. */}
              <div className="flex items-center justify-between">
                <h3 className="text-lg font-semibold text-[#0a2a4a] dark:text-gray-100">Addons</h3>
                <RoleGuard adminOnly>
                  {!noCatalog && (
                    <button
                      type="button"
                      data-testid="manage-addons-enable-btn"
                      onClick={() => { void handleOpenPicker(); }}
                      className="inline-flex items-center gap-1.5 rounded-lg bg-teal-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-teal-700 dark:bg-teal-700 dark:hover:bg-teal-600"
                    >
                      <Plus className="h-4 w-4" />
                      Enable addon
                    </button>
                  )}
                </RoleGuard>
              </div>

              {/* Admin: enabled-addons list + searchable enable picker —
                * sits directly under the Addons header, no separate card. */}
              <RoleGuard adminOnly>
                {/* Empty catalog */}
                {noCatalog && (
                  <p className="text-sm text-[#3a6a8a] dark:text-gray-500">
                    No addons in catalog.
                  </p>
                )}

                {/* Row list */}
                {!noCatalog && (
                  <div className="space-y-1">
                    {/* Connectivity-check system row */}
                    {showCheckRow && (
                      <div
                        data-testid="connectivity-check-row"
                        className="flex items-start gap-3 rounded-md bg-[#e8f4ff] px-3 py-2.5 opacity-80 dark:bg-gray-700/60"
                      >
                        <ShieldCheck className="mt-0.5 h-4 w-4 shrink-0 text-[#5a8aaa] dark:text-gray-400" />
                        <div className="min-w-0 flex-1">
                          <div className="flex flex-wrap items-center gap-2">
                            <span className="text-sm font-medium text-[#3a6a8a] dark:text-gray-300">
                              Connectivity check
                            </span>
                            <span className="rounded-full bg-[#c0ddf0] px-2 py-0.5 text-xs font-semibold uppercase tracking-wide text-[#2a5a7a] dark:bg-gray-600 dark:text-gray-300">
                              Sharko system — automatic
                            </span>
                          </div>
                          <p className="mt-0.5 text-xs text-[#5a8aaa] dark:text-gray-400">
                            A tiny test app Sharko deploys through ArgoCD to prove this cluster can receive deployments. It removes itself when the first addon is enabled.
                          </p>
                        </div>
                      </div>
                    )}

                    {/* Enabled / pending addon rows */}
                    {visibleRows.length === 0 ? (
                      <div className="py-2">
                        <p className="text-sm text-[#3a6a8a] dark:text-gray-400">
                          No addons enabled on this cluster yet.
                        </p>
                      </div>
                    ) : (
                      visibleRows.map((addonName) => {
                        const isPendingEnable =
                          !originalToggles[addonName] && addonToggles[addonName];
                        const isPendingRemove =
                          originalToggles[addonName] && !addonToggles[addonName];

                        return (
                          <div
                            key={addonName}
                            data-testid={`manage-addon-row-${addonName}`}
                            className={`flex items-center justify-between gap-3 rounded-md px-3 py-2 ${
                              isPendingEnable
                                ? 'bg-teal-50 ring-1 ring-teal-300 dark:bg-teal-900/20 dark:ring-teal-700'
                                : isPendingRemove
                                ? 'bg-[#e8f4ff] opacity-60 ring-1 ring-[#6aade0] dark:bg-gray-700/40'
                                : 'bg-[#e8f4ff] dark:bg-gray-700/40'
                            }`}
                          >
                            <div className="flex min-w-0 flex-1 items-center gap-2">
                              <span
                                className={`truncate text-sm font-medium ${
                                  isPendingRemove
                                    ? 'line-through text-[#5a8aaa] dark:text-gray-500'
                                    : 'text-[#0a2a4a] dark:text-gray-200'
                                }`}
                              >
                                {addonName}
                              </span>
                              {(isPendingEnable || isPendingRemove) && (
                                <span className="shrink-0 rounded-full bg-teal-600 px-2 py-0.5 text-xs font-semibold uppercase tracking-wide text-white dark:bg-teal-700">
                                  {isPendingEnable ? 'pending' : 'removing'}
                                </span>
                              )}
                            </div>
                            {/* Remove button — not available when already pending-remove */}
                            {!isPendingRemove && (
                              <button
                                type="button"
                                data-testid={`manage-addon-remove-${addonName}`}
                                aria-label={`Remove ${addonName}`}
                                onClick={() =>
                                  setAddonToggles((prev) => ({ ...prev, [addonName]: false }))
                                }
                                className="shrink-0 rounded p-0.5 text-[#5a8aaa] hover:bg-[#c0ddf0] hover:text-[#0a2a4a] dark:hover:bg-gray-600 dark:hover:text-gray-200"
                              >
                                <X className="h-4 w-4" />
                              </button>
                            )}
                            {/* Undo-remove button when pending-remove */}
                            {isPendingRemove && (
                              <button
                                type="button"
                                data-testid={`manage-addon-undo-${addonName}`}
                                aria-label={`Undo remove ${addonName}`}
                                onClick={() =>
                                  setAddonToggles((prev) => ({ ...prev, [addonName]: true }))
                                }
                                className="shrink-0 rounded p-0.5 text-teal-600 hover:bg-teal-100 dark:hover:bg-teal-900/30"
                              >
                                Undo
                              </button>
                            )}
                          </div>
                        );
                      })
                    )}
                  </div>
                )}

                {/* Apply / Discard footer */}
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
                      onClick={() => {
                        setAddonToggles({ ...originalToggles });
                        setToggleError(null);
                        setToggleResult(null);
                      }}
                      disabled={applyingToggles}
                      className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
                    >
                      Discard
                    </button>
                  </div>
                )}
                {toggleError && (
                  <p className="mt-2 text-sm text-red-600 dark:text-red-400">{toggleError}</p>
                )}
                {toggleResult?.pr && (
                  <div className="mt-2">
                    <PRResultBanner
                      result={toggleResult.pr}
                      mergedMessage="PR merged — addon changes applied"
                      openMessage="PR opened — addon changes apply once it merges"
                    />
                  </div>
                )}
                {toggleResult?.message && (
                  <p className="mt-2 text-sm text-green-600 dark:text-green-400">
                    {toggleResult.message}
                  </p>
                )}

                {/* Enable-addon picker dialog */}
                <EnableAddonPicker
                  open={pickerOpen}
                  allAddonNames={pickerCatalogNames.length > 0 ? pickerCatalogNames : allCatalogNames}
                  enabledNames={pickerEnabledNames}
                  loading={pickerCatalogLoading}
                  error={pickerCatalogError}
                  onEnable={(addonName) =>
                    setAddonToggles((prev) => ({ ...prev, [addonName]: true }))
                  }
                  onClose={() => setPickerOpen(false)}
                  onRetry={() => {
                    setPickerCatalogError(null);
                    setPickerCatalogNames([]);
                    void handleOpenPicker();
                  }}
                />
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
              <div className="overflow-x-auto rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] shadow-sm dark:ring-gray-700 dark:bg-gray-800">
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
                        clusterName={name ?? ''}
                        isExpanded={expandedRows.has(addon.addon_name)}
                        onToggleExpand={() => toggleExpanded(addon.addon_name)}
                        argocdBaseURL={argocdBaseURL}
                        highlighted={highlightedAddon === addon.addon_name}
                        pendingPRs={pendingPRsByAddon[addon.addon_name] ?? []}
                        onRefresh={() => void fetchData(true)}
                        aiEnabled={aiEnabled}
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
              {/* Per-cluster overrides editor (Tier 2) */}
              <RoleGuard roles={['admin', 'operator']}>
                <PerClusterAddonOverridesEditor
                  clusterName={name!}
                  addons={data.addon_comparisons}
                  gitRepoBase={gitRepoBase}
                  gitDefaultBranch={gitDefaultBranch}
                  onSaved={handlePerClusterOverridesSaved}
                />
              </RoleGuard>

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

          {/* History section — every cluster change is a PR, so the old
            * standalone "Pull Requests" tab was just a duplicate log
            * (V2-cleanup-81.1). Open PRs now lead History as a "pending"
            * group, with the change timeline underneath. */}
          {activeSection === 'history' && (
            <div className="space-y-6">
              <div className="space-y-3">
                <h3 className="text-lg font-semibold text-[#0a2a4a] dark:text-gray-100">Open pull requests</h3>
                <PendingPRsPanel
                  cluster={name}
                  onMergeDetected={(pr: TrackedPR) => {
                    showToast(`Merged PR #${pr.pr_id}: ${prettyOperation(pr.operation)}${pr.cluster ? ` on ${pr.cluster}` : ''}.`)
                    void fetchData()
                  }}
                />
              </div>
              <ClusterHistorySection clusterName={name!} />
            </div>
          )}

          {/* Settings section — houses admin troubleshooting controls that
            * used to sit among the Overview stat cards (V2-cleanup-78.1).
            * Secret Path is viewable by everyone (it's not sensitive — it's
            * the lookup key into the secrets backend) but only admins can
            * edit it, same as before the move. */}
          {activeSection === 'settings' && (
            <div className="space-y-4">
              <h3 className="text-lg font-semibold text-[#0a2a4a] dark:text-gray-100">Cluster Settings</h3>
              <div className="flex items-center gap-2 rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] px-4 py-3 shadow-sm dark:ring-gray-700 dark:bg-gray-800">
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
                            const { prUrl } = extractPR(result);
                            setSecretPathResult(
                              prUrl
                                ? { pr: result }
                                : { message: result?.message || 'Secret path updated' },
                            );
                            setEditingSecretPath(false);
                          } catch (e: unknown) {
                            setSecretPathResult({ message: e instanceof Error ? e.message : 'Failed to update' });
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
                  {secretPathResult?.pr && (
                    <div className="mt-1">
                      <PRResultBanner
                        result={secretPathResult.pr}
                        mergedMessage="PR merged — secret path updated"
                        openMessage="PR opened — secret path updates once it merges"
                      />
                    </div>
                  )}
                  {secretPathResult?.message && (
                    <p className="mt-0.5 text-xs text-teal-600 dark:text-teal-400">{secretPathResult.message}</p>
                  )}
                </div>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

/* ------------------------------------------------------------------ */

interface ComparisonRowProps {
  addon: AddonComparisonStatus;
  clusterName: string;
  isExpanded: boolean;
  onToggleExpand: () => void;
  argocdBaseURL: string;
  highlighted?: boolean;
  // Pending PRs targeting this addon on the current cluster. Rendered as
  // inline badges (newest-first) on the addon-name cell so operators see
  // "PR open" without leaving the addons sub-page.
  pendingPRs?: TrackedPR[];
  // Called after a successful restart-sync so the parent immediately refetches
  // the cluster status instead of waiting for the next poll cycle.
  onRefresh?: () => void;
  // When true the "Ask AI" button is rendered next to "Restart sync" on
  // sync_failing rows.  False/absent → button is absent.
  aiEnabled?: boolean;
}

function ComparisonRow({ addon, clusterName, isExpanded, onToggleExpand, argocdBaseURL, highlighted, pendingPRs = [], onRefresh, aiEnabled = false }: ComparisonRowProps) {
  const [restartLoading, setRestartLoading] = useState(false);
  const allIssues = addon.issues;
  const isTruncated = shouldTruncateIssues(allIssues);
  const displayedIssues = isExpanded ? allIssues : allIssues.slice(0, 2);

  const handleRestartSync = async () => {
    setRestartLoading(true);
    try {
      await api.restartAddonSync(clusterName, addon.addon_name);
      showToast(`Sync restarted for ${addon.addon_name} on ${clusterName}.`, 'success');
      // Immediately refetch cluster status so the UI reflects the new sync state
      // without waiting for the next poll cycle.
      onRefresh?.();
    } catch (err) {
      showToast(`Failed to restart sync: ${err instanceof Error ? err.message : String(err)}`, 'error');
    } finally {
      setRestartLoading(false);
    }
  };
  const rowRef = useRef<HTMLTableRowElement>(null);

  // Deep-link effect: when highlighted flips true, scroll into view and
  // briefly pulse the row. Runs once per highlight flip. The `highlighted`
  // flag fades to false after 2s in the parent — we deliberately do NOT
  // apply pointer-events-intercepting styles here so the addon link +
  // ArgoCD icon stay clickable during and after the highlight window.
  useEffect(() => {
    if (!highlighted) return;
    rowRef.current?.scrollIntoView({ behavior: 'smooth', block: 'center' });
  }, [highlighted]);

  // An app is NOT OK if health is non-healthy OR there are issues
  const hasProblems = allIssues.length > 0
    || addon.argocd_health_status === 'Error'
    || addon.argocd_health_status === 'Degraded'
    || addon.status === 'with_issues'
    || addon.status === 'unknown_health'
    || addon.status === 'unknown_state'
    || addon.status === 'sync_failing';

  return (
    <tr
      ref={rowRef}
      className={`hover:bg-[#d6eeff] dark:hover:bg-gray-700 ${
        highlighted ? 'ring-2 ring-inset ring-blue-500 bg-blue-50/60 dark:bg-blue-950/30 transition-colors duration-500' : ''
      }`}
    >
      <td className="px-4 py-3">
        {addon.status ? (
          <StatusBadge status={addon.status} />
        ) : (
          <span className="text-[#3a6a8a] dark:text-gray-500">--</span>
        )}
      </td>
      <td className="px-4 py-3 font-medium text-[#0a2a4a] dark:text-gray-100">
        <div className="flex flex-wrap items-center gap-2">
          {addon.status === 'sharko_system' ? (
            <span className="text-[#0a2a4a] dark:text-gray-100">Connectivity check</span>
          ) : (
            <Link
              to={`/addons/${encodeURIComponent(addon.addon_name)}`}
              className="hover:text-teal-600 hover:underline dark:hover:text-teal-400"
              title={`Open ${addon.addon_name} details`}
            >
              {addon.addon_name}
            </Link>
          )}
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
          {/* Pending-PR badges (one per open PR targeting this addon on
              this cluster). Renders inline next to the addon name so the
              operator can tell "this addon's state is in flight" without
              scrolling to the cluster PRs section. data-testid stays
              stable so per-row tests can locate it. */}
          {pendingPRs.map((pr) => (
            <a
              key={pr.pr_id}
              href={pr.pr_url}
              target="_blank"
              rel="noopener noreferrer"
              onClick={(e) => e.stopPropagation()}
              data-testid="addon-pending-pr-badge"
              title={`Open PR #${pr.pr_id} — ${pr.pr_title}`}
              className="inline-flex items-center gap-1 rounded-full bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-800 hover:bg-amber-200 dark:bg-amber-900/30 dark:text-amber-300 dark:hover:bg-amber-900/60"
            >
              <GitPullRequest className="h-3 w-3" />
              PR #{pr.pr_id}
              {pr.operation && (
                <span className="rounded bg-amber-200/70 px-1 py-px text-xs capitalize text-amber-900 dark:bg-amber-800/50 dark:text-amber-200">
                  {pr.operation.replace(/^addon-/, '')}
                </span>
              )}
              <ExternalLink className="h-3 w-3" />
            </a>
          ))}
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
        {addon.status === 'sharko_system' ? (
          <span className="text-xs text-[#3a6a8a] dark:text-gray-400">
            A tiny test app Sharko deploys through ArgoCD to prove this cluster can receive
            deployments. It removes itself when the first addon is enabled.
          </span>
        ) : allIssues.length > 0 ? (
          <div>
            <ul className="space-y-0.5 text-xs text-[#1a4a6a] dark:text-gray-400">
              {displayedIssues.map((issue, i) => (
                <li key={i}>{issue}</li>
              ))}
            </ul>
            {/* When the row is expanded AND there is a full operation message,
                show it as a scrollable monospace block. The full message comes
                from argocd_operation_message (up to 4000 chars, full text
                including all lines) — the issues[] list above only has the
                short first-line version. */}
            {isExpanded && addon.argocd_operation_message && (
              <pre
                data-testid="full-operation-message"
                className="mt-2 max-h-48 overflow-y-auto whitespace-pre-wrap break-words rounded-md bg-[#e8f4ff] p-2 font-mono text-xs text-[#0a2a4a] ring-2 ring-[#6aade0] dark:bg-gray-900 dark:text-gray-200"
              >
                {addon.argocd_operation_message}
              </pre>
            )}
            {(isTruncated || (addon.argocd_operation_message && !isExpanded)) && (
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
            {addon.status === 'sync_failing' && (
              <div className="mt-2 flex flex-wrap gap-1">
                <RoleGuard roles={['admin', 'operator']}>
                  <button
                    type="button"
                    data-testid="restart-sync-btn"
                    onClick={(e) => { e.stopPropagation(); void handleRestartSync(); }}
                    disabled={restartLoading}
                    className="inline-flex items-center gap-1 rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-2 py-0.5 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
                  >
                    {restartLoading
                      ? <Loader2 className="h-3 w-3 animate-spin" />
                      : <RotateCcw className="h-3 w-3" />}
                    Restart sync
                  </button>
                </RoleGuard>
                {aiEnabled && (
                  <button
                    type="button"
                    data-testid="ask-ai-btn"
                    onClick={(e) => {
                      e.stopPropagation();
                      const message = `Addon "${addon.addon_name}" on cluster "${clusterName}" is failing to sync in ArgoCD. Here is the error:\n\n${addon.argocd_operation_message ?? '(no operation message available)'}\n\nWhat's wrong and how do I fix it?`;
                      window.dispatchEvent(new CustomEvent('open-assistant', { detail: { message, nonce: crypto.randomUUID() } }));
                    }}
                    className="inline-flex items-center gap-1 rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-2 py-0.5 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
                  >
                    <Sparkles className="h-3 w-3" />
                    Ask AI
                  </button>
                )}
              </div>
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
      <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-8 text-center shadow-sm dark:ring-gray-700 dark:bg-gray-800">
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
          className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] shadow-sm dark:ring-gray-700 dark:bg-gray-800"
        >
          <div className="flex items-center gap-2 border-b border-[#6aade0] px-4 py-3 dark:border-gray-700">
            <h3 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
              {entry.addon_name}
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

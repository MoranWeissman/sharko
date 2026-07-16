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
  XCircle,
  ShieldCheck,
  Sparkles,
  Settings,
  Stethoscope,
  Activity,
} from 'lucide-react';
import { api, deregisterCluster, updateClusterAddons, updateClusterSettings, testClusterConnection, isTestClusterUnavailable, fetchTrackedPRs, previewEnableAddon, reconcileCluster, diagnoseCluster, doctorCluster } from '@/services/api';
import type { TestClusterUnavailable, PRWriteResult } from '@/services/api';
import { PRResultBanner, extractPR } from '@/components/PRFeedback';
import { DryRunPreview } from '@/components/AddAddonFlow';
import { EnableAddonPicker } from '@/components/EnableAddonPicker';
import type { ClusterChange, ClusterComparisonResponse, AddonComparisonStatus, ConfigDiffResponse, VerifyStep, DryRunResult } from '@/services/models';
import { StatCard } from '@/components/StatCard';
import { StatusBadge } from '@/components/StatusBadge';
import { ConnectivityBadge } from '@/components/ConnectivityBadge';
import { SHARKO_CONN_TOOLTIP } from '@/components/WhoseConnectionLabel';
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
import { DiagnoseResultView } from '@/components/DiagnoseModal';
import { DoctorResultView, DOCTOR_LABEL, DOCTOR_HINT } from '@/components/DoctorModal';
import { TestConnectionModal } from '@/components/TestConnectionModal';
import { PendingPRsPanel } from '@/components/PendingPRsPanel';
import { CompletedChangesPanel } from '@/components/CompletedChangesPanel';
import { PerClusterAddonOverridesEditor } from '@/components/PerClusterAddonOverridesEditor';
import { HelperText } from '@/components/HelperText';
import { showToast } from '@/components/ToastNotification';
import { prettyOperation } from '@/lib/utils';
import type { ConnectionsListResponse, TrackedPR, DiagnosticReport, DoctorClusterResponse } from '@/services/models';

type StatusFilter =
  | 'all'
  | 'healthy'
  | 'with_issues'
  | 'missing_in_argocd'
  | 'untracked'
  | 'disabled_in_git';

// ClusterChangesSection — the unified "Changes" tab content (V2-cleanup-84.2).
// Pending changes (open PRs, via PendingPRsPanel) render on top; completed
// changes (GET /clusters/{name}/changes, via CompletedChangesPanel) render
// below. This wrapper tracks whether each half has loaded and whether it's
// empty, purely so it can collapse both into a single friendly empty state
// when the cluster genuinely has no changes at all — otherwise each panel
// shows its own loading/error/empty state independently.
function ClusterChangesSection({
  clusterName,
  onMergeDetected,
}: {
  clusterName: string;
  onMergeDetected: (pr: TrackedPR) => void;
}) {
  const [pendingPRs, setPendingPRs] = useState<TrackedPR[] | null>(null);
  const [completedChanges, setCompletedChanges] = useState<ClusterChange[] | null>(null);
  const [completedRefreshKey, setCompletedRefreshKey] = useState(0);

  const handlePendingData = useCallback((prs: TrackedPR[]) => setPendingPRs(prs), []);
  const handleCompletedData = useCallback((changes: ClusterChange[]) => setCompletedChanges(changes), []);

  const handleMergeDetected = useCallback(
    (pr: TrackedPR) => {
      onMergeDetected(pr);
      // A pending change just became a completed one — refetch the
      // completed-changes list so it shows up without a manual reload.
      setCompletedRefreshKey((n) => n + 1);
    },
    [onMergeDetected],
  );

  const bothLoaded = pendingPRs !== null && completedChanges !== null;
  const bothEmpty = bothLoaded && pendingPRs.length === 0 && completedChanges.length === 0;

  if (bothEmpty) {
    return (
      <EmptyState
        title="No changes yet"
        description="A change is something you do to this cluster — enabling or disabling an addon, or editing an addon's values. Each one goes out as a pull request for you to review, and shows up here once it's merged."
      />
    );
  }

  return (
    <div className="space-y-6">
      <div className="space-y-3">
        <h3 className="text-lg font-semibold text-[#0a2a4a] dark:text-gray-100">Pending changes</h3>
        <PendingPRsPanel
          cluster={clusterName}
          onMergeDetected={handleMergeDetected}
          onDataChange={handlePendingData}
        />
      </div>
      <CompletedChangesPanel
        cluster={clusterName}
        refreshKey={completedRefreshKey}
        onDataChange={handleCompletedData}
      />
    </div>
  );
}

function shouldTruncateIssues(issues: string[]): boolean {
  return issues.join(' ').length > 100;
}

// Format a UTC ISO-8601 timestamp as a relative "X ago" string. Mirrors the
// helper in ConnectivityBadge.tsx / ClusterStatusSummary.tsx — kept local
// here too rather than introducing a shared util for a three-line function.
function relativeTime(isoString: string): string {
  const then = new Date(isoString).getTime();
  const now = Date.now();
  const diffMs = now - then;
  if (diffMs < 0) return 'just now';
  const secs = Math.floor(diffMs / 1000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  return `${days}d ago`;
}

// "Sync now" bounded-poll parameters (V2-cleanup-90.4 / L1): at most 4
// refetches, 2s apart, stopping early once last_reconcile.time changes.
const SYNC_POLL_MAX_ATTEMPTS = 4;
const SYNC_POLL_INTERVAL_MS = 2000;

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
  const [removePreview, setRemovePreview] = useState<DryRunResult | null>(null);
  const [removePreviewLoading, setRemovePreviewLoading] = useState(false);
  const [removePreviewError, setRemovePreviewError] = useState<string | null>(null);

  // Test connection
  const [testResult, setTestResult] = useState<
    | { reachable?: boolean; success?: boolean; server_version?: string; error?: string; error_message?: string; suggestions?: string[]; steps?: VerifyStep[] }
    | TestClusterUnavailable
    | 'testing'
    | null
  >(null);
  // Check permissions (HD1, V3) — result now lives in the Diagnostics
  // section and PERSISTS until re-run or leaving, instead of a fading modal.
  const [diagnoseReport, setDiagnoseReport] = useState<DiagnosticReport | null>(null);
  const [diagnoseLoading, setDiagnoseLoading] = useState(false);
  const [diagnoseError, setDiagnoseError] = useState<string | null>(null);
  // Connection doctor (V2-cleanup-88.4/88.5, persisted in-section HD1) — the
  // six real-attempt checks against this cluster's connection. Result
  // persists in the Diagnostics section.
  const [doctorResult, setDoctorResult] = useState<DoctorClusterResponse | null>(null);
  const [doctorLoading, setDoctorLoading] = useState(false);
  const [doctorError, setDoctorError] = useState<string | null>(null);
  // Test connection modal (V2-cleanup-91.1/F5) — replaces inline results panel.
  const [testOpen, setTestOpen] = useState(false);

  // Manual "sync now" (V2-cleanup-89.4) — nudges the cluster-secret
  // reconciler instead of waiting for its periodic tick.
  const [syncingNow, setSyncingNow] = useState(false);
  // V2-cleanup-90.4 (L1) — bounded-poll state for the "sync now" refetch.
  // syncPollTimerRef holds the currently-scheduled setTimeout so it can be
  // cleared on unmount; syncPollCancelledRef is flipped in that same
  // cleanup so an in-flight fetchData that resolves just after unmount
  // never calls setState on a gone component.
  const syncPollTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const syncPollCancelledRef = useRef(false);

  useEffect(() => {
    return () => {
      syncPollCancelledRef.current = true;
      if (syncPollTimerRef.current) {
        clearTimeout(syncPollTimerRef.current);
        syncPollTimerRef.current = null;
      }
    };
  }, []);

  // Secret path editing
  const [editingSecretPath, setEditingSecretPath] = useState(false);
  const [secretPathValue, setSecretPathValue] = useState('');
  const [secretPathSaving, setSecretPathSaving] = useState(false);
  // Defect 2.2: secret-path save now keeps the PR result so we can render a
  // clickable PR link (PRResultBanner) instead of dumping the raw URL as text.
  // `message` carries the non-PR / error fallback.
  const [secretPathResult, setSecretPathResult] = useState<{ pr?: PRWriteResult; message?: string } | null>(null);
  const [secretPathPreview, setSecretPathPreview] = useState<DryRunResult | null>(null);
  const [secretPathPreviewLoading, setSecretPathPreviewLoading] = useState(false);
  const [secretPathPreviewError, setSecretPathPreviewError] = useState<string | null>(null);

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
  const [togglePreview, setTogglePreview] = useState<DryRunResult | null>(null);
  const [togglePreviewLoading, setTogglePreviewLoading] = useState(false);
  const [togglePreviewError, setTogglePreviewError] = useState<string | null>(null);

  // Enable-addon picker (Manage Addons card)
  const [pickerOpen, setPickerOpen] = useState(false);
  // Catalog names fetched eagerly on mount (V3-BUG-2 fix).
  const [pickerCatalogNames, setPickerCatalogNames] = useState<string[]>([]);
  const [pickerCatalogLoading, setPickerCatalogLoading] = useState(false);
  const [pickerCatalogError, setPickerCatalogError] = useState<string | null>(null);
  const [catalogFetched, setCatalogFetched] = useState(false);

  // addon_secrets_ready pre-warn (V2-cleanup-88.5, L4): when this cluster
  // has no resolvable connection credentials, staging a secret-bearing
  // addon for enable fires a background dry-run of the exact same
  // pre-flight gate EnableAddon applies, so a would-be 422 shows up here as
  // an inline warning INSTEAD of surprising the user after "Apply
  // Changes". Keyed by addon name; the message is the backend's own
  // MissingClusterCredentialsError text, rendered verbatim.
  const [addonSecretWarnings, setAddonSecretWarnings] = useState<Record<string, string>>({});

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

  // "Not connected yet" (V2-cleanup-85.1): before this fix, an un-probed
  // cluster showed the same "Unknown" fact three separate times — the type
  // badge, the status badge, and a banner — because each one independently
  // falls back to "no information" when Sharko has never heard from ArgoCD
  // about this cluster. Collapse them into a single signal: no ArgoCD
  // connection status has been observed AND there's no server URL to derive
  // a type from. Deliberately narrow (both conditions, not either) so a
  // cluster that resolves a type from its server URL, or has a real
  // argocd_connection_status, keeps its normal badges — this only touches
  // the case where every signal is simultaneously empty.
  const notConnected = useMemo((): boolean => {
    const argoStatus = (data?.argocd_connection_status ?? '').trim().toLowerCase();
    const serverUrl = (data?.cluster?.server_url ?? '').trim();
    return (argoStatus === '' || argoStatus === 'unknown') && serverUrl === '';
  }, [data]);

  const fetchData = useCallback(async (background = false): Promise<ClusterComparisonResponse | undefined> => {
    if (!name) return undefined;
    try {
      if (!background) {
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
      //
      // V3-AP1: DO NOT reseed toggles on a background refetch when there are
      // unsaved changes or an open preview — the background poll would silently
      // discard the user's pending toggle edit and strand the preview. Only
      // seed toggles on the foreground/first load (background === false).
      if (!background) {
        const toggleMap: Record<string, boolean> = {};
        result.addon_comparisons.forEach((a: { addon_name: string; git_enabled: boolean; git_configured: boolean; status?: string }) => {
          if (!a.git_configured) return;
          if (a.status === 'untracked_in_argocd' || a.status === 'sharko_system') return;
          toggleMap[a.addon_name] = a.git_enabled;
        });
        setAddonToggles(toggleMap);
        setOriginalToggles(toggleMap);
      }
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
      // V2-cleanup-90.4 (L1): return the freshly-fetched comparison so
      // callers (the "Sync now" bounded poll) can read the new
      // last_reconcile.time without waiting for a render + effect round
      // trip through `data` state.
      return result;
    } catch (e: unknown) {
      if (!background) {
        setError(
          e instanceof Error
            ? e.message
            : `Failed to load comparison for cluster: ${name}`,
        );
      }
      return undefined;
    } finally {
      setLoading(false);
    }
  }, [name]);

  // Stable onSaved for the per-cluster overrides editor — passing a fresh
  // arrow function on every render would defeat the editor's React.memo
  // prop-equality check and re-trigger its useEffects every parent tick.
  const handlePerClusterOverridesSaved = useCallback(() => {
    setConfigFetched(false);
  }, []);


  const handlePreviewRemoveCluster = useCallback(async () => {
    if (!name) return;
    setRemovePreviewLoading(true);
    setRemovePreviewError(null);
    try {
      const result = await deregisterCluster(name, undefined, true);
      setRemovePreview(result);
    } catch (e: unknown) {
      setRemovePreviewError(e instanceof Error ? e.message : 'Failed to generate preview');
    } finally {
      setRemovePreviewLoading(false);
    }
  }, [name]);

  const handleRemoveCluster = useCallback(async () => {
    if (!name) return;
    setRemoving(true);
    setRemoveError(null);
    try {
      // Let the global GitOps auto-merge setting decide — don't pass an override.
      const result = await deregisterCluster(name);
      // deregisterCluster wraps its PR fields under `git` — hand THAT to the
      // banner (extractPR reads pr_url/pr_id/merged off the object it's given).
      const git = result?.git ?? null;
      const merged = git?.merged ?? false;

      // V3-D5: on successful removal navigate to Clusters list with the removal
      // PR in router state — the Clusters page shows it as a dismissible note.
      // The detail page is soon-dead for auto-merge, and for manual the cluster
      // stays in the list with its open-PR badge, so better to land where the
      // action lives. On failure: stay on the page + show the error (unchanged).
      setRemoveModalOpen(false);
      setRemoving(false);
      navigate('/clusters', {
        state: {
          removalPR: {
            cluster: name,
            pr_url: git?.pr_url,
            pr_id: git?.pr_id,
            merged: merged,
          },
        },
      });
    } catch (e: unknown) {
      setRemoveError(e instanceof Error ? e.message : 'Failed to remove cluster');
      setRemoving(false);
    }
  }, [name, navigate]);

  const hasToggleChanges = useMemo(() => {
    return Object.keys(addonToggles).some((k) => addonToggles[k] !== originalToggles[k]);
  }, [addonToggles, originalToggles]);

  // Builds the enabled/staged-only toggle payload shared by preview + apply
  // so the two paths can never diverge (V2-cleanup-32 fix logic).
  const buildTogglePayload = useCallback(() => {
    const payload: Record<string, boolean> = {};
    for (const [k, v] of Object.entries(addonToggles)) {
      const wasEnabled = originalToggles[k] === true;
      const isEnabled = v === true;
      // Include if currently enabled, was enabled (being removed), or is newly staged
      if (wasEnabled || isEnabled) {
        payload[k] = v;
      }
    }
    return payload;
  }, [addonToggles, originalToggles]);

  const handlePreviewToggles = useCallback(async () => {
    if (!name) return;
    setTogglePreviewLoading(true);
    setTogglePreviewError(null);
    try {
      const payload = buildTogglePayload();
      const result = await updateClusterAddons(name, payload, true);
      setTogglePreview(result);
    } catch (e: unknown) {
      setTogglePreviewError(e instanceof Error ? e.message : 'Failed to generate preview');
    } finally {
      setTogglePreviewLoading(false);
    }
  }, [name, buildTogglePayload]);

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
      const payload = buildTogglePayload();
      const result = await updateClusterAddons(name, payload);
      const { prUrl } = extractPR(result);
      setToggleResult(prUrl ? { pr: result } : { message: 'Changes applied successfully.' });
      setOriginalToggles({ ...addonToggles });
      setTogglePreview(null);
      setTogglePreviewError(null);
    } catch (e: unknown) {
      setToggleError(e instanceof Error ? e.message : 'Failed to apply changes');
    } finally {
      setApplyingToggles(false);
    }
  }, [name, addonToggles, buildTogglePayload]);

  const handlePreviewSecretPath = useCallback(async () => {
    if (!name) return;
    setSecretPathPreviewLoading(true);
    setSecretPathPreviewError(null);
    try {
      const result = await updateClusterSettings(name, { secret_path: secretPathValue, dry_run: true });
      setSecretPathPreview(result);
    } catch (e: unknown) {
      setSecretPathPreviewError(e instanceof Error ? e.message : 'Failed to generate preview');
    } finally {
      setSecretPathPreviewLoading(false);
    }
  }, [name, secretPathValue]);

  // handleSyncNow triggers a manual reconcile (V2-cleanup-89.4) instead of
  // waiting for the reconciler's periodic tick. The endpoint returns 202 as
  // soon as the trigger is accepted — the reconcile itself runs
  // asynchronously server-side, so a single blind refetch could easily land
  // before it finished.
  //
  // V2-cleanup-90.4 (L1): replaced the single blind 1.5s refetch with a
  // bounded poll — up to SYNC_POLL_MAX_ATTEMPTS refetches,
  // SYNC_POLL_INTERVAL_MS apart, stopping as soon as last_reconcile.time
  // changes from the value observed right before the click. The timer is
  // tracked in a ref so it can be cleared on unmount (no setState-after-
  // unmount), and the spinner stays lit through the FIRST refetch landing,
  // not just until the 202 is accepted.
  const handleSyncNow = useCallback(async () => {
    if (!name) return;
    setSyncingNow(true);
    const preClickTime = data?.cluster?.last_reconcile?.time;
    try {
      await reconcileCluster(name);
      showToast(`Sync triggered for cluster "${name}".`, 'success');

      let attempt = 0;
      const pollOnce = async () => {
        attempt += 1;
        const result = await fetchData(true);
        if (syncPollCancelledRef.current) return;
        // Spinner stays lit only through the first refetch landing —
        // whether or not that refetch detected a change.
        if (attempt === 1) {
          setSyncingNow(false);
        }
        const newTime = result?.cluster?.last_reconcile?.time;
        const changed = newTime !== undefined && newTime !== preClickTime;
        if (changed || attempt >= SYNC_POLL_MAX_ATTEMPTS) {
          syncPollTimerRef.current = null;
          return;
        }
        syncPollTimerRef.current = setTimeout(() => {
          void pollOnce();
        }, SYNC_POLL_INTERVAL_MS);
      };

      syncPollTimerRef.current = setTimeout(() => {
        void pollOnce();
      }, SYNC_POLL_INTERVAL_MS);
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to trigger sync', 'error');
      setSyncingNow(false);
    }
  }, [name, fetchData, data]);

  const handleTestConnection = useCallback(async () => {
    if (!name) return;
    // F5 (V2-cleanup-91.1): open the modal instead of rendering inline.
    setTestOpen(true);
  }, [name]);

  // HD1 (V3): run Check permissions and render the result in the Diagnostics
  // section. Same POST /clusters/{name}/diagnose call the old modal made; only
  // WHERE the result renders (in-section) and its LIFETIME (persists) changed.
  const handleCheckPermissions = useCallback(async () => {
    if (!name) return;
    setDiagnoseLoading(true);
    setDiagnoseError(null);
    try {
      const report = await diagnoseCluster(name);
      setDiagnoseReport(report);
    } catch (e: unknown) {
      setDiagnoseError(e instanceof Error ? e.message : 'Diagnosis failed');
    } finally {
      setDiagnoseLoading(false);
    }
  }, [name]);

  // HD1 (V3): run the connection doctor and render its six checks in the
  // Diagnostics section (persists). Same POST /clusters/{name}/doctor call.
  const handleRunDoctor = useCallback(async () => {
    if (!name) return;
    setDoctorLoading(true);
    setDoctorError(null);
    try {
      const result = await doctorCluster(name);
      setDoctorResult(result);
    } catch (e: unknown) {
      setDoctorError(e instanceof Error ? e.message : 'Connection doctor failed');
    } finally {
      setDoctorLoading(false);
    }
  }, [name]);

  // Open the enable-addon picker. The catalog is now fetched eagerly on mount
  // (V3-BUG-2 fix) so this just opens the picker; the lazy-fetch fallback is
  // kept as a no-op (the early-return when pickerCatalogNames is populated) for
  // safety — it never runs in normal flow since the mount effect beat it.
  const handleOpenPicker = useCallback(async () => {
    setPickerOpen(true);
    setPickerCatalogError(null);
    if (pickerCatalogNames.length > 0) return; // already fetched (eagerly or fallback)
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

  // addon_secrets_ready pre-warn (V2-cleanup-88.5, L4). Only worth the
  // round-trip when this cluster has no resolvable connection credentials
  // (addon_secrets_ready === false) — a ready cluster's dry-run always
  // succeeds regardless of the addon, so there's nothing to warn about.
  // A secret-LESS addon's dry-run also always succeeds (EnableAddon's
  // pre-flight gate is a no-op for it) — this only fires for the real
  // "secret-bearing addon + cred-less cluster" combination, and the
  // message rendered is the backend's own text, verbatim.
  const checkAddonSecretWarning = useCallback(async (addonName: string) => {
    if (!name) return;
    if (data?.cluster.addon_secrets_ready !== false) return;
    try {
      await previewEnableAddon(name, addonName);
      // Dry-run succeeded — no secrets gate applies to this addon; clear
      // any stale warning from a previous selection of the same addon.
      setAddonSecretWarnings((prev) => {
        if (!(addonName in prev)) return prev;
        const next = { ...prev };
        delete next[addonName];
        return next;
      });
    } catch (e: unknown) {
      setAddonSecretWarnings((prev) => ({
        ...prev,
        [addonName]: e instanceof Error ? e.message : 'Sharko cannot confirm this addon can be enabled on this cluster yet.',
      }));
    }
  }, [name, data]);

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

  // V3-BUG-2 fix: fetch the addon catalog eagerly on mount so `pickerCatalogNames`
  // is populated independent of whether the "+ Enable addon" picker was ever opened.
  // Before this, a 0-addon cluster hid the "+ Enable addon" button because `noCatalog`
  // was true (derived from the empty per-cluster `addonToggles` map) — but the button's
  // click was the only trigger for the catalog fetch, creating a catch-22. Now the
  // catalog fetch runs once on load, and `noCatalog` is recomputed off the REAL catalog.
  useEffect(() => {
    if (catalogFetched) return;
    setCatalogFetched(true);
    setPickerCatalogLoading(true);
    setPickerCatalogError(null);
    api
      .getAddonCatalog()
      .then((catalog) => {
        setPickerCatalogNames(catalog.addons.map((a) => a.addon_name));
      })
      .catch((e: unknown) => {
        setPickerCatalogError(e instanceof Error ? e.message : 'Failed to load catalog');
      })
      .finally(() => {
        setPickerCatalogLoading(false);
      });
  }, [catalogFetched]);

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
        { key: 'history', label: 'Changes', icon: Clock },
        { key: 'diagnostics', label: 'Diagnostics', icon: Activity },
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
  // noCatalog (V3-BUG-2 fix): true ONLY when the REAL catalog — fetched eagerly
  // on mount — is genuinely empty. Before this fix, `noCatalog` keyed off
  // `allCatalogNames` (seeded from per-cluster enabled addons), which was empty
  // for a 0-addon cluster even when the catalog had addons available. That hid
  // the "+ Enable addon" button whose click fetches the catalog (catch-22).
  // Now: the catalog is the authoritative source. If the catalog fetch hasn't
  // completed yet, fall back to the old logic (show button if cluster has addons)
  // to avoid breaking existing behavior — but once the catalog loads, key off
  // the real catalog count regardless of per-cluster enabled state.
  const noCatalog = catalogFetched && !pickerCatalogLoading
    ? pickerCatalogNames.length === 0
    : allCatalogNames.length === 0 && pickerCatalogNames.length === 0;
  // V3-AM1: The top manage strip now shows ONLY pending changes
  // (staged enables + staged removes), not all enabled addons.
  // A pending-enable = addon wasn't originally enabled but is now toggled true.
  // A pending-remove = addon was originally enabled but is now toggled false.
  const pendingEnableRows = allCatalogNames.filter(
    (n) => !originalToggles[n] && addonToggles[n],
  );
  const pendingRemoveRows = allCatalogNames.filter(
    (n) => originalToggles[n] && !addonToggles[n],
  );
  // Rows to show in the top strip: only addons with pending deltas.
  const visibleRows = Array.from(
    new Set([...pendingEnableRows, ...pendingRemoveRows]),
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
          {/* Last sync (V2-cleanup-89.4) — Sharko's own reconcile result for
            * this cluster's ArgoCD secret. ArgoCD shows a failed apply;
            * before this, a failed reconcile here was server-log-only. */}
          {data?.cluster?.last_reconcile && (
            <p className="mt-1 text-xs text-[#5a8aaa] dark:text-gray-500">
              Last sync: {relativeTime(data.cluster.last_reconcile.time)}
              {data.cluster.last_reconcile.outcome === 'succeeded' && ' — succeeded'}
              {data.cluster.last_reconcile.outcome === 'skipped' && ' — skipped'}
              {data.cluster.last_reconcile.outcome === 'failed' && (
                <span className="text-red-600 dark:text-red-400"> — failed</span>
              )}
              {data.cluster.last_reconcile.message && (
                <span className="block text-[#5a8aaa] dark:text-gray-500">{data.cluster.last_reconcile.message}</span>
              )}
            </p>
          )}
          {/* Migrate nudge (V2-cleanup-89.6) — this cluster was registered
            * via the "Paste a kubeconfig" path, whose availability an admin
            * can now turn off install-wide. A light nudge toward the
            * GitOps-clean alternative; not a warning or a blocker. */}
          {data?.cluster?.creds_source === 'inline-kubeconfig' && (
            <p className="mt-1 text-xs text-[#5a8aaa] dark:text-gray-500">
              Registered with pasted credentials — consider migrating to a secret-store pointer.
            </p>
          )}
          {/* HD1 (V3): header redesign — dropped the bare refresh button (auto-poll
            * + Sync-now's refetch cover it), moved Check permissions + Diagnose to
            * the new Diagnostics section. Test connection stays light here.
            * G4 (V3): Sync-related controls moved to dedicated GitOps area below. */}
        </div>
        <div className="flex items-center gap-3">
          <RoleGuard roles={['admin', 'operator']}>
            {/* Test connection — light button */}
            <button
              onClick={handleTestConnection}
              title={SHARKO_CONN_TOOLTIP}
              className="inline-flex items-center gap-1.5 rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-1.5 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
            >
              <Wifi className="h-3.5 w-3.5" />
              {TEST_CONNECTION_LABEL}
            </button>
            <InfoHint text={TEST_CONNECTION_HINT} label="What does Test connection do?" />
          </RoleGuard>
        </div>
      </div>

      <ConfirmationModal
        open={removeModalOpen}
        onClose={() => {
          setRemoveModalOpen(false);
          setRemovePreview(null);
          setRemovePreviewError(null);
        }}
        onConfirm={handleRemoveCluster}
        title={`Remove cluster "${name}"?`}
        description="This will remove the cluster from the Git catalog. This action creates a pull request and cannot be undone."
        confirmText="Remove"
        destructive
        loading={removing}
        extraContent={
          <div className="space-y-3">
            {!removePreview && !removePreviewLoading && (
              <button
                type="button"
                onClick={handlePreviewRemoveCluster}
                className="inline-flex items-center gap-2 rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-1.5 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
              >
                <Eye className="h-4 w-4" />
                Preview changes
              </button>
            )}
            {removePreviewLoading && (
              <div
                role="status"
                className="flex items-center gap-2 rounded-md ring-2 ring-[#6aade0] bg-[#f0f7ff] p-3 text-sm text-[#0a3a5a] dark:ring-gray-700 dark:bg-gray-900 dark:text-gray-300"
              >
                <Loader2 className="h-4 w-4 shrink-0 animate-spin" aria-hidden="true" />
                <span>Generating preview...</span>
              </div>
            )}
            {removePreview && <DryRunPreview result={removePreview} />}
            {removePreviewError && (
              <div
                role="alert"
                className="flex items-start gap-2 rounded-md border border-red-300 bg-red-50 p-3 text-sm text-red-900 dark:border-red-700 dark:bg-red-950/40 dark:text-red-200"
              >
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
                <p>{removePreviewError}</p>
              </div>
            )}
            <p className="text-xs text-[#5a8aaa] dark:text-gray-500">
              Auto-merge follows your{' '}
              <a href="/settings?section=gitops" className="underline hover:text-[#0a2a4a] dark:hover:text-gray-300">
                global GitOps setting
              </a>
              .
            </p>
          </div>
        }
      />
      <TestConnectionModal
        clusterName={name ?? ''}
        open={testOpen}
        onClose={() => setTestOpen(false)}
        onSuggestionSelect={handleSelectSuggestion}
        onResult={(r) => setTestResult(r)}
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
          {/* Type badge is suppressed in the "not connected yet" state — the
            * banner below already says so once, in plain words (V2-cleanup-85.1). */}
          {!notConnected && <ClusterTypeBadge server={data.cluster.server_url} />}
        </div>
        {data.cluster.server_version && (
          <div className="flex items-center gap-1.5 text-xs text-[#2a5a7a] dark:text-gray-400">
            <Tag className="h-3.5 w-3.5 text-teal-500" />
            <span className="text-[10px] uppercase tracking-wide text-[#5a8aaa] dark:text-gray-500">Cluster Version</span>
            <span className="font-mono font-medium text-[#0a2a4a] dark:text-gray-200">{data.cluster.server_version}</span>
          </div>
        )}
        <div className="flex items-center gap-2">
          {/* Status badge is suppressed in the "not connected yet" state —
            * same reason as the type badge above (V2-cleanup-85.1). */}
          {!notConnected && <StatusBadge status={computedStatus} size="sm" />}
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

      {/* ArgoCD connection banner — distinguishes the states so a cluster
        * Sharko hasn't yet probed isn't mis-labelled as "Connection Failed":
        *
        *   - not connected at all (see `notConnected` above) → single
        *     "Not connected yet" banner (V2-cleanup-85.1); this also
        *     replaces the old "Status unknown" copy for this case, since
        *     the type + status badges are hidden here too and this banner
        *     is now the one place that says so.
        *   - argocd_connection_status missing / "Unknown", but the cluster
        *     otherwise has a server URL → neutral "Status unknown" banner
        *     (rare: e.g. server URL known from Git but ArgoCD hasn't
        *     reported back yet)
        *   - argocd_connection_status === "Successful"    → no banner (happy path)
        *   - anything else                                 → red "Connection Failed" banner
        *
        * The !== 'unreachable' guard prevents double-rendering when
        * the consolidated "Cluster Unreachable" banner is already
        * shown.
        */}
      {(() => {
        if (computedStatus === 'unreachable') return null;
        if (notConnected) {
          return (
            <div className="flex items-start gap-3 rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] px-5 py-3 dark:ring-gray-700 dark:bg-gray-800">
              <AlertTriangle className="h-5 w-5 shrink-0 text-[#3a6a8a] dark:text-gray-300 mt-0.5" />
              <div>
                <p className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Not connected yet</p>
                <p className="mt-0.5 text-xs text-[#3a6a8a] dark:text-gray-400">Sharko hasn't reached this cluster through ArgoCD. Once it connects, its type, status and health will appear here.</p>
              </div>
            </div>
          );
        }
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

      {/* GitOps Sync Area (V3 G4) — dedicated section consolidating sync status,
        * sync action, and live drift diff. Uses ArgoCD-familiar vocabulary:
        * Synced / OutOfSync / Sync failed / Reconciling. Colors from clusterStatus.ts. */}
      <RoleGuard roles={['admin', 'operator']}>
        <div className="space-y-3 rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] px-5 py-4 dark:ring-gray-700 dark:bg-gray-800">
          <div className="flex items-center justify-between">
            <h3 className="text-base font-semibold text-[#0a2a4a] dark:text-gray-100">
              GitOps Sync
            </h3>
            {/* Sync status pill (ArgoCD-familiar words) */}
            {(() => {
              const lastRec = data?.cluster?.last_reconcile;
              const drift = lastRec?.label_drift;
              const hasDrift = drift && (drift.added?.length || drift.removed?.length || drift.changed?.length);
              let label = 'Not synced yet';
              let bgClass = 'bg-[#5a8aaa] dark:bg-gray-600';
              let textClass = 'text-white';
              if (syncingNow) {
                label = 'Reconciling';
                bgClass = 'bg-[#3a6a8a] dark:bg-gray-500';
              } else if (lastRec?.outcome === 'succeeded') {
                label = hasDrift ? 'OutOfSync' : 'Synced';
                bgClass = hasDrift ? 'bg-amber-500' : 'bg-green-600 dark:bg-green-700';
              } else if (lastRec?.outcome === 'failed') {
                label = 'Sync failed';
                bgClass = 'bg-red-600 dark:bg-red-700';
              } else if (lastRec?.outcome === 'skipped') {
                label = 'Synced';
                bgClass = 'bg-green-600 dark:bg-green-700';
              }
              return (
                <span className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${bgClass} ${textClass}`}>
                  {label}
                </span>
              );
            })()}
          </div>

          {/* Sync-now action */}
          <div className="flex items-center gap-2">
            <button
              onClick={handleSyncNow}
              disabled={syncingNow}
              className="inline-flex items-center gap-1.5 rounded-lg bg-teal-600 px-4 py-2 text-sm font-medium text-white hover:bg-teal-700 disabled:opacity-50 dark:bg-teal-700 dark:hover:bg-teal-600"
            >
              {syncingNow ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}
              Sync now
            </button>
            <InfoHint
              text="Re-applies the desired state from Git to this cluster's connection labels in ArgoCD. Sharko keeps a label listing which addons belong here. Out of sync means those labels drifted from what Git says (rare)."
              label="What does Sync now do?"
            />
          </div>

          {/* Drift diff — integrated into the GitOps area (V3 G2 + G4) */}
          {(() => {
            const lastRec = data?.cluster?.last_reconcile;
            const drift = lastRec?.label_drift;
            const hasDrift = drift && (drift.added?.length || drift.removed?.length || drift.changed?.length);

            if (!lastRec) return null; // No reconcile data yet

            if (!hasDrift) {
              // Synced — show brief confirmation
              return (
                <div className="flex items-center gap-2 rounded-md bg-green-50 px-3 py-2 dark:bg-green-950/20">
                  <CheckCircle className="h-4 w-4 text-green-600 dark:text-green-400" />
                  <p className="text-sm font-medium text-green-700 dark:text-green-400">
                    Labels in sync — Git matches live cluster state
                  </p>
                </div>
              );
            }

            // OutOfSync — show the drift diff
            return (
              <div className="space-y-2 rounded-md bg-amber-50 px-3 py-2.5 dark:bg-amber-950/20">
                <div className="flex items-start gap-2">
                  <AlertTriangle className="h-5 w-5 shrink-0 text-amber-600 dark:text-amber-400 mt-0.5" />
                  <div className="flex-1">
                    <p className="text-sm font-semibold text-amber-700 dark:text-amber-400">
                      Label Drift Detected
                    </p>
                    <p className="mt-0.5 text-xs text-amber-600 dark:text-amber-400">
                      This cluster's addon labels drifted from Git. The diff below shows what changed.
                    </p>
                  </div>
                </div>
                <div className="rounded-md bg-white ring-2 ring-[#6aade0] p-3 dark:bg-gray-900 dark:ring-gray-700">
                  <h4 className="mb-2 text-xs font-semibold text-[#0a2a4a] dark:text-gray-200">
                    Git vs Live Label Diff
                  </h4>
                  <div className="space-y-1 text-xs font-mono text-[#2a5a7a] dark:text-gray-400">
                    {drift.added && drift.added.length > 0 && (
                      <div>
                        <span className="font-semibold text-green-600 dark:text-green-400">Added in Git (missing on cluster):</span>
                        {drift.added.map((key) => (
                          <div key={key} className="ml-4 text-green-600 dark:text-green-400">
                            + {key}
                          </div>
                        ))}
                      </div>
                    )}
                    {drift.removed && drift.removed.length > 0 && (
                      <div>
                        <span className="font-semibold text-red-600 dark:text-red-400">Removed in Git (present on cluster):</span>
                        {drift.removed.map((key) => (
                          <div key={key} className="ml-4 text-red-600 dark:text-red-400">
                            - {key}
                          </div>
                        ))}
                      </div>
                    )}
                    {drift.changed && drift.changed.length > 0 && (
                      <div>
                        <span className="font-semibold text-amber-600 dark:text-amber-400">Changed (values differ):</span>
                        {drift.changed.map((key) => (
                          <div key={key} className="ml-4 text-amber-600 dark:text-amber-400">
                            ~ {key}
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                </div>
              </div>
            );
          })()}
        </div>
      </RoleGuard>

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
                  {/* V3-BUG-2 fix: show the "+ Enable addon" button whenever the
                    * catalog is non-empty (keying off the real eagerly-fetched catalog
                    * once loaded), regardless of whether this cluster has enabled addons. */}
                  {!noCatalog && (
                    <button
                      type="button"
                      data-testid="manage-addons-enable-btn"
                      onClick={() => { void handleOpenPicker(); }}
                      className="inline-flex items-center gap-1.5 rounded-lg bg-teal-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-teal-700 dark:bg-teal-700 dark:hover:bg-teal-600"
                    >
                      <Plus className="h-4 w-4" />
                      Manage addons
                    </button>
                  )}
                </RoleGuard>
              </div>

              {/* Admin: enabled-addons list + searchable enable picker —
                * sits directly under the Addons header, no separate card. */}
              <RoleGuard adminOnly>
                {/* V3-BUG-2 fix: show "No addons in catalog." only when the real
                  * catalog (eagerly fetched) is genuinely empty. Before the catalog
                  * loads, `noCatalog` falls back to the old per-cluster logic to
                  * avoid flashing empty-state on clusters with enabled addons. */}
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

                    {/* V3-AM1: Pending addon rows (staged enables + staged removes).
                        When no pending changes, this strip is empty — the "No addons"
                        message does NOT appear here (enabled addons are in the comparison
                        table, not duplicated here). */}
                    {visibleRows.length > 0 && (
                      visibleRows.map((addonName) => {
                        const isPendingEnable =
                          !originalToggles[addonName] && addonToggles[addonName];
                        const isPendingRemove =
                          originalToggles[addonName] && !addonToggles[addonName];
                        // addon_secrets_ready pre-warn (V2-cleanup-88.5, L4)
                        // — only relevant while the addon is still staged
                        // to be enabled (not once it's removed again).
                        const secretWarning = isPendingEnable ? addonSecretWarnings[addonName] : undefined;

                        return (
                          <div key={addonName} className="flex flex-col gap-1">
                          <div
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
                          {secretWarning && (
                            <p
                              data-testid={`manage-addon-secret-warning-${addonName}`}
                              className="mx-1 rounded-md bg-amber-50 px-2.5 py-1.5 text-xs text-amber-800 ring-1 ring-amber-200 dark:bg-amber-900/20 dark:text-amber-300 dark:ring-amber-800"
                            >
                              {secretWarning}
                            </p>
                          )}
                          </div>
                        );
                      })
                    )}
                  </div>
                )}

                {/* Apply / Discard footer — V3-AP1: render whenever there are pending
                    changes OR an open preview so a shown preview ALWAYS carries its Apply
                    + Discard buttons (even if a background poll tried to reseed toggles). */}
                {(hasToggleChanges || togglePreview) && (
                  <div className="mt-4 flex items-center gap-3">
                    <button
                      type="button"
                      onClick={handlePreviewToggles}
                      disabled={applyingToggles || togglePreviewLoading}
                      className="inline-flex items-center gap-2 rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
                    >
                      {togglePreviewLoading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Eye className="h-4 w-4" />}
                      Preview changes
                    </button>
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
                        setTogglePreview(null);
                        setTogglePreviewError(null);
                      }}
                      disabled={applyingToggles}
                      className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
                    >
                      Discard
                    </button>
                  </div>
                )}
                {togglePreview && (
                  <div className="mt-3">
                    <DryRunPreview result={togglePreview} />
                  </div>
                )}
                {togglePreviewError && (
                  <p className="mt-2 text-sm text-red-600 dark:text-red-400">{togglePreviewError}</p>
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
                  onEnable={(addonName) => {
                    setAddonToggles((prev) => ({ ...prev, [addonName]: true }));
                    // Pre-warn (V2-cleanup-88.5) instead of letting the
                    // user hit the 422 blind on Apply Changes.
                    void checkAddonSecretWarning(addonName);
                  }}
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
                        onStageRemove={(addonName) => {
                          setAddonToggles((prev) => ({ ...prev, [addonName]: false }));
                        }}
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

          {/* Changes section — every cluster change is a PR, so this is one
            * unified change story: pending changes (open PRs) on top,
            * completed changes (GET /clusters/{name}/changes) below. The
            * ArgoCD sync-activity timeline that used to render here was
            * replaced by the durable PR-based change log (V2-cleanup-84.2).
            * Section key stays 'history' to preserve existing deep links. */}
          {activeSection === 'history' && (
            <ClusterChangesSection
              clusterName={name!}
              onMergeDetected={(pr: TrackedPR) => {
                showToast(`Merged PR #${pr.pr_id}: ${prettyOperation(pr.operation)}${pr.cluster ? ` on ${pr.cluster}` : ''}.`)
                void fetchData()
              }}
            />
          )}

          {/* Diagnostics section (HD1, V3) — persistent diagnostic results.
            * Check permissions, Diagnose connection (the doctor), and Test
            * connection all show their LAST result here until re-run or leaving.
            * Replaces the fading modals that used to live in the header. */}
          {activeSection === 'diagnostics' && (
            <div className="space-y-4">
              <h3 className="text-lg font-semibold text-[#0a2a4a] dark:text-gray-100">Diagnostics</h3>
              <HelperText>
                Diagnostics tests whether <strong>Sharko itself</strong> can reach and operate on this cluster, using the credentials you registered. This is not testing ArgoCD's connection — it's testing Sharko's ability to read addons, write secrets, and manage the cluster. (Two checks read ArgoCD-side state to verify configuration consistency.)
              </HelperText>
              <HelperText className="text-xs">
                Run these checks when troubleshooting connectivity issues or after rotating credentials. Results persist here until you re-run.
              </HelperText>
              <RoleGuard roles={['admin', 'operator']}>
                <div className="space-y-3">
                  {/* Test connection result (also triggered from header) */}
                  {testResult && testResult !== 'testing' && (
                    <div className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:ring-gray-700 dark:bg-gray-800">
                      <div className="flex items-center gap-2 mb-2">
                        <Wifi className="h-4 w-4 text-teal-600 dark:text-teal-400" />
                        <h4 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Test Connection Result</h4>
                      </div>
                      {isTestClusterUnavailable(testResult) ? (
                        <p className="text-sm text-amber-700 dark:text-amber-400">
                          {testResult.error}
                        </p>
                      ) : testResult.reachable || testResult.success ? (
                        <div className="flex items-center gap-2 text-sm text-green-700 dark:text-green-400">
                          <CheckCircle className="h-4 w-4" />
                          <span>Cluster reachable{testResult.server_version ? ` — ${testResult.server_version}` : ''}</span>
                        </div>
                      ) : (
                        <div className="space-y-2">
                          <div className="flex items-center gap-2 text-sm text-red-700 dark:text-red-400">
                            <XCircle className="h-4 w-4" />
                            <span>Connection failed</span>
                          </div>
                          {testResult.error_message && (
                            <p className="text-xs text-[#2a5a7a] dark:text-gray-400">{testResult.error_message}</p>
                          )}
                        </div>
                      )}
                    </div>
                  )}
                  {/* Check permissions — runs POST /clusters/{name}/diagnose;
                    * the report renders below and PERSISTS until re-run (HD1). */}
                  <div className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:ring-gray-700 dark:bg-gray-800">
                    <div className="flex items-center justify-between gap-2 mb-2">
                      <div className="flex items-center gap-2">
                        <ScanSearch className="h-4 w-4 text-teal-600 dark:text-teal-400" />
                        <h4 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">{CHECK_PERMISSIONS_LABEL}</h4>
                      </div>
                      <button
                        data-testid="run-check-permissions"
                        onClick={() => { void handleCheckPermissions(); }}
                        disabled={diagnoseLoading}
                        className="inline-flex items-center gap-1.5 rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-1.5 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
                      >
                        {diagnoseLoading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <ScanSearch className="h-3.5 w-3.5" />}
                        {diagnoseReport || diagnoseError ? 'Re-run' : 'Run'}
                      </button>
                    </div>
                    <HelperText className="mt-1">{CHECK_PERMISSIONS_HINT}</HelperText>
                    {diagnoseError && (
                      <div className="mt-3 rounded-md bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-400">
                        {diagnoseError}
                      </div>
                    )}
                    {diagnoseReport && !diagnoseLoading && (
                      <div className="mt-3">
                        <DiagnoseResultView report={diagnoseReport} />
                      </div>
                    )}
                  </div>

                  {/* Diagnose connection (the doctor) — runs POST /clusters/{name}/doctor;
                    * the six checks render below and PERSIST until re-run (HD1). */}
                  <div className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:ring-gray-700 dark:bg-gray-800">
                    <div className="flex items-center justify-between gap-2 mb-2">
                      <div className="flex items-center gap-2">
                        <Stethoscope className="h-4 w-4 text-teal-600 dark:text-teal-400" />
                        <h4 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">{DOCTOR_LABEL}</h4>
                      </div>
                      <button
                        data-testid="run-connection-doctor"
                        onClick={() => { void handleRunDoctor(); }}
                        disabled={doctorLoading}
                        className="inline-flex items-center gap-1.5 rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-1.5 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
                      >
                        {doctorLoading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Stethoscope className="h-3.5 w-3.5" />}
                        {doctorResult || doctorError ? 'Re-run' : 'Run'}
                      </button>
                    </div>
                    <HelperText className="mt-1">{DOCTOR_HINT}</HelperText>
                    {doctorError && (
                      <div className="mt-3 rounded-md bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-400">
                        {doctorError}
                      </div>
                    )}
                    {doctorResult && !doctorLoading && (
                      <div className="mt-3">
                        <DoctorResultView result={doctorResult} />
                      </div>
                    )}
                  </div>
                </div>
              </RoleGuard>
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
                  <HelperText className="text-xs">Secret Path</HelperText>
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
                        disabled={secretPathSaving || secretPathPreviewLoading}
                        onClick={handlePreviewSecretPath}
                        className="inline-flex items-center gap-1 rounded border border-[#5a9dd0] bg-[#f0f7ff] px-2 py-0.5 text-xs text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
                      >
                        {secretPathPreviewLoading ? <Loader2 className="h-3 w-3 animate-spin" /> : <Eye className="h-3 w-3" />}
                        Preview changes
                      </button>
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
                            setSecretPathPreview(null);
                            setSecretPathPreviewError(null);
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
                        onClick={() => {
                          setEditingSecretPath(false);
                          setSecretPathPreview(null);
                          setSecretPathPreviewError(null);
                        }}
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
                  {secretPathPreview && (
                    <div className="mt-2">
                      <DryRunPreview result={secretPathPreview} />
                    </div>
                  )}
                  {secretPathPreviewError && (
                    <p className="mt-1 text-xs text-red-600 dark:text-red-400">{secretPathPreviewError}</p>
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
  // V3-AM1: Optional callback to stage a pending-remove. When present + addon
  // is enabled/managed (git_enabled && not untracked/system), a labeled
  // "Remove" control is rendered.
  onStageRemove?: (addonName: string) => void;
}

function ComparisonRow({ addon, clusterName, isExpanded, onToggleExpand, argocdBaseURL, highlighted, pendingPRs = [], onRefresh, aiEnabled = false, onStageRemove }: ComparisonRowProps) {
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
        {/* V3-AM1: Remove control for enabled/managed addons. Rendered when
            onStageRemove is provided + addon is git_enabled + not untracked/system.
            Clicking stages a pending-remove (setAddonToggles false). Placed outside
            the issues conditional so it's visible on ALL enabled rows. */}
        {onStageRemove && addon.git_enabled && addon.status !== 'untracked_in_argocd' && addon.status !== 'sharko_system' && (
          <div className="mt-2">
            <RoleGuard adminOnly>
              <button
                type="button"
                data-testid="comparison-row-remove-btn"
                onClick={(e) => {
                  e.stopPropagation();
                  onStageRemove(addon.addon_name);
                }}
                className="inline-flex items-center gap-1 rounded-md border border-red-400 bg-[#f0f7ff] px-2 py-0.5 text-xs font-medium text-red-600 hover:bg-red-50 dark:border-red-700 dark:bg-gray-700 dark:text-red-400 dark:hover:bg-red-900/20"
              >
                <X className="h-3 w-3" />
                Remove
              </button>
            </RoleGuard>
          </div>
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

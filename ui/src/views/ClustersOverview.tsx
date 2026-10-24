import { useState, useEffect, useMemo, useCallback, useRef, type ReactNode } from 'react';
import { useNavigate, useSearchParams, useLocation } from 'react-router-dom';
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
  Unlink,
  ChevronDown,
  ChevronUp,
  RefreshCw,
  Trash2,
  ExternalLink,
  Shield,
} from 'lucide-react';
import { isCredentialLookupFailure } from '@/lib/verifyStage';
import { api, registerCluster, testClusterConnection, unadoptCluster, deleteOrphanCluster, isTestClusterUnavailable, getSystemCapabilities, type PRWriteResult } from '@/services/api';
import { PRLifecycleProgress, extractPR, writeFailureMessage } from '@/components/PRFeedback';
import { DryRunPreview } from '@/components/AddAddonFlow';
import type { TestClusterUnavailable } from '@/services/api';
import type {
  AvailableCluster,
  Cluster,
  ClusterHealthStats,
  ClusterProvider,
  CredsSource,
  ClustersResponse,
  DryRunResult,
  PendingRegistration,
  OrphanRegistration,
  RegisterClusterResult,
  SystemCapabilitiesResponse,
  VerifyStep,
} from '@/services/models';
import { StatCard } from '@/components/StatCard';
import { PaginationControls, PageSizeSelector, type PageSize } from '@/components/PaginationControls';
import { ClusterStatusSummary, type ClusterLatestTest } from '@/components/ClusterStatusSummary';
import { LoadingState } from '@/components/LoadingState';
import { ErrorState } from '@/components/ErrorState';
import { ErrorDetail } from '@/components/ErrorDetail';
import { getCached, setCached } from '@/lib/viewCache';
import { RoleGuard } from '@/components/RoleGuard';
import {
  WhoseConnectionLabel,
  ARGOCD_CONN_TOOLTIP,
  SHARKO_CONN_TOOLTIP,
} from '@/components/WhoseConnectionLabel';
import { ClusterStatusLegend } from '@/components/ClusterStatusLegend';
import { InfoHint } from '@/components/InfoHint';
import {
  TEST_CONNECTION_LABEL,
  TEST_CONNECTION_HINT,
} from '@/components/ClusterActionHints';
import { PRModelExplainer } from '@/components/PRFeedback';
import { DiagnoseModal } from '@/components/DiagnoseModal';
import { withV } from '@/lib/versionLabel';
import { ClusterIdentityStrip } from '@/components/ClusterIdentityStrip';
import { ArgoCDStatusBanner } from '@/components/ArgoCDStatusBanner';
import { showToast } from '@/components/ToastNotification';
import { AdoptClustersDialog } from '@/components/AdoptClustersDialog';
import { TakeoverDialog } from '@/components/TakeoverDialog';
import { ConfirmationModal } from '@/components/ConfirmationModal';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog';
import { AutoMergeNote } from '@/components/AutoMergeNote';

type StatusFilter =
  | 'all'
  | 'connected'
  // 'disconnected' means failed + missing ONLY (dashboard UX review
  // 2026-08-01: red is reserved for failed/missing; pending/untested
  // clusters are neutral, not a problem, so they no longer count as
  // "disconnected" here either). Mirrors Dashboard's
  // `stats.clusters.failed + stats.clusters.missing` headline count. The
  // deep-link `?status=disconnected` from the Dashboard's banner relies on
  // this staying in sync with that definition.
  | 'disconnected'
  | 'failed'
  | 'missing_from_argocd'
  | 'not_in_git';

// Every real StatusFilter value — used to validate a `?status=` deep-link
// param before trusting it as the initial filter (see the useState below).
const VALID_STATUS_FILTERS: readonly StatusFilter[] = [
  'all',
  'connected',
  'disconnected',
  'failed',
  'missing_from_argocd',
  'not_in_git',
];

interface Filters {
  name: string;
  versions: string[];
  connectionTypes: string[];
}

// S1 (maintainer's 50-cluster walk, day 7): real sort controls for the
// managed clusters list. "Most addons" reuses the exact enabled-addon-count
// signal the Addons column already renders (Object.values(labels).filter
// 'enabled') — there is no separate addon_count field on Cluster, so this
// is the only client-side data the sort can honestly use. No age/"oldest
// first" option exists — there is no creation-date field anywhere in the
// cluster data (locked decision, do not add one).
type ClusterSortOption = 'name-asc' | 'name-desc' | 'problems-first' | 'k8s-version' | 'most-addons';

const CLUSTER_SORT_OPTIONS: Array<{ value: ClusterSortOption; label: string }> = [
  { value: 'name-asc', label: 'Name A→Z' },
  { value: 'name-desc', label: 'Name Z→A' },
  { value: 'problems-first', label: 'Problems first' },
  { value: 'k8s-version', label: 'Kubernetes version' },
  { value: 'most-addons', label: 'Most addons' },
];

// Same enabled-addon-count signal the table/grid rows already compute
// inline (see rowAddonCount / addonCount below) — pulled out here so the
// sort comparator and the row renderers read one definition.
function getEnabledAddonCount(cluster: Cluster): number {
  return Object.values(cluster.labels).filter((v) => v === 'enabled').length;
}

// "Problems first" groups failed/missing clusters ahead of everything
// else. Mirrors the exact definition the `disconnected` StatusFilter (and
// the Dashboard's headline count) already use for "problem" — see the
// StatusFilter type's comment above — so the sort agrees with the filter
// and stat card on what counts as a problem.
function isProblemCluster(cluster: Cluster): boolean {
  const cs = cluster.connection_status?.toLowerCase() ?? '';
  return cs === 'failed' || cs === 'missing';
}

// Loose semver-ish compare for the "Kubernetes version" sort — descending
// (newest first), missing versions always sort last regardless of
// direction. Cluster versions are plain dotted strings (e.g. "1.28",
// "v1.29.3"); this is intentionally forgiving rather than a strict semver
// parser.
function compareServerVersionsDesc(a?: string, b?: string): number {
  if (!a && !b) return 0;
  if (!a) return 1;
  if (!b) return -1;
  const toParts = (v: string) => v.replace(/^v/i, '').split('.').map((n) => parseInt(n, 10) || 0);
  const pa = toParts(a);
  const pb = toParts(b);
  const len = Math.max(pa.length, pb.length);
  for (let i = 0; i < len; i++) {
    const na = pa[i] ?? 0;
    const nb = pb[i] ?? 0;
    if (na !== nb) return nb - na;
  }
  return 0;
}

// Empty Clusters page: one sentence when nothing is registered, another when a filter hides every row.
export const EMPTY_CLUSTERS_MESSAGE =
  'No clusters are registered yet. Click Add Cluster to register your first one.';
export const EMPTY_CLUSTERS_FILTERED_MESSAGE =
  'No managed clusters match the current filters.';

// V2-cleanup-55.3: one plain-English line per credential-source option,
// shown under the dropdown so a non-expert knows what each choice means
// before the option-specific fields appear.
// V3-CC2: clarified to state what-you-provide + expiry + AWS/IAM requirement.
const CREDS_SOURCE_HINTS: Record<CredsSource, string> = {
  'inline-kubeconfig':
    'Paste the file once — Sharko stores it. Works for any cluster. The token inside can expire; re-paste when it does.',
  'secret-kubeconfig':
    'Sharko fetches the kubeconfig from your secrets backend by name/path. Works for any cluster; token lifetime is whatever you stored.',
  'eks-token':
    'Sharko generates a short-lived AWS token on every connection using its own AWS identity — nothing to store or rotate. EKS only; Sharko needs AWS access to the cluster.',
};

// V2-cleanup-57.2: connection-ownership choice, in plain English. This is
// the "Sharko never owns my connections" escape hatch: with "I do", Sharko
// never creates, changes, or deletes the ArgoCD cluster secret — you make
// it yourself (the operator guide shows the exact YAML) and Sharko only
// keeps the addon labels on it in sync.
export type ConnOwnership = 'sharko' | 'user';
export const CONN_OWNERSHIP_HINTS: Record<ConnOwnership, ReactNode> = {
  // WHAT THIS SAID BEFORE, AND WHY IT CHANGED. It read "Sharko creates the
  // ArgoCD cluster secret and keeps its credentials up to date." Both halves
  // are the browser making a promise about what the server does — the
  // "keeps … up to date" half is a claim of ongoing work this form has no way
  // to know is happening — and it used the retired word "cluster secret"
  // where the product now says "connection". The product owner ruled it out
  // under "zero browser-authored promises about server behaviour".
  //
  // A hint under a radio button describes THE CHOICE, which is a fact about
  // this form and nothing else. That is what it says now.
  sharko: (
    <>
      Hand this cluster&apos;s connection to Sharko. The usual choice.
    </>
  ),
  user: (
    <>
      You manage the cluster connection yourself — Sharko only updates its addon labels.{' '}
      <a
        href="https://sharko.readthedocs.io/en/latest/operator/self-managed-connections/"
        target="_blank"
        rel="noopener noreferrer"
        className="text-teal-600 hover:underline dark:text-teal-400"
      >
        Operator Manual: Managing cluster connections yourself
      </a>
    </>
  ),
};

// perf S2 — everything fetchData renders, snapshotted into one cache entry
// once a load finishes. A revisit within the same browser session hydrates
// all of this in one shot (instant paint, no spinner) before kicking a
// background refresh — see the mount effect and fetchData's setCached call.
interface ClustersSnapshot {
  allClusters: Cluster[];
  healthStats: ClusterHealthStats | null;
  pendingRegistrations: PendingRegistration[];
  orphanRegistrations: OrphanRegistration[];
  argoCDUnreachable: boolean;
}

// Exported so tests can prime/inspect the same key the component uses.
export const CLUSTERS_CACHE_KEY = 'clusters';

/** Shown when un-adopt failed and the server sent no sentence of its own. */
export const UNADOPT_FAILED_FALLBACK =
  'Un-adopt did not finish: Sharko could not write the change to Git.';

// S4: mirrors services/api.ts's CLUSTERS_PAGE_SIZE. Kept as a local
// constant (rather than importing the one in api.ts) so this component
// doesn't depend on a named export existing on every test file's
// `vi.mock('@/services/api', ...)` factory — those factories return a
// plain object literal with only the functions each test needs, and an
// unmocked named export reads as undefined at call time, not a helpful
// type error.
const CLUSTERS_PAGE_SIZE = 100;

// Shown when a registration stopped part-way and the server sent no
// sentence of its own (LO-8 R6). Pinned by exact text in
// ClustersOverview.registerpartial.test.tsx.
const REGISTER_DID_NOT_FINISH = 'Sharko did not finish registering this cluster.';

// Shown when the server says the registration worked, the pull request is
// not merged, and the answer has no pull request link (LO-9 R9). A
// "success" answer only comes back once the pull request is open (Sharko
// opened it, or found one already open), so the sentence says that and
// nothing more. Before, it said "submitted as a pull request ... Check the
// open PR list", a guess. Pinned by exact text in
// ClustersOverview.registerpartial.test.tsx.
const REGISTER_PR_NO_LINK =
  "There is an open pull request that registers this cluster, but Sharko's answer had no link to it. Find it in your GitOps repo and merge it to finish the registration.";

// The popover's Sharko line reads the test the user just ran on this page.
// A test that is still running, or could not run at all, says nothing yet.
export function latestTestOf(r: unknown, at?: string): ClusterLatestTest | undefined {
  const t = latestTestResultOf(r);
  return t && at ? { ...t, at } : t;
}
function latestTestResultOf(r: unknown): ClusterLatestTest | undefined {
  if (!r || r === 'testing' || typeof r !== 'object') return undefined;
  if (isTestClusterUnavailable(r as never)) return undefined;
  // Sharko could not read the cluster's sign-in details: nothing was
  // tested. The Adopt dialog calls the same answer "Not verified", so this
  // page says so too, not "Test failing".
  if (isCredentialLookupFailure(r as never)) return { success: false, notVerified: true };
  const t = r as { reachable?: boolean; success?: boolean; steps?: VerifyStep[] };
  const steps = t.steps ?? [];
  return {
    // A pass only when the answer says so. A missing field is not a pass.
    success: t.success === true || (t.success === undefined && t.reachable === true),
    passed: steps.length > 0 ? steps.filter((x) => x.status === 'pass').length : undefined,
    total: steps.length > 0 ? steps.length : undefined,
    failedStep: steps.find((x) => x.status === 'fail')?.name,
  };
}

// Shown when Sharko could not ask ArgoCD which other clusters it knows.
// Pinned by exact text in ClustersOverview.roundr.test.tsx.
export const AVAILABLE_CHECK_FAILED =
  "Sharko could not check which other clusters ArgoCD knows. Refresh to try again.";

// The two sentences the server sends when Sharko has no usable Git or ArgoCD
// connection (internal/credsafe, pinned there by exact text). A refresh does
// not fix that, so the page does not say "Refresh to try again" for it.
const NO_USABLE_CONNECTION_ERRORS = [
  'Sharko has no usable Git connection. Open Settings and check the active connection: the Git provider, the repository it points at, and the access token.',
  'Sharko has no usable ArgoCD connection. Open Settings and check the active connection: the ArgoCD server address and the ArgoCD token.',
];

export type AvailableStatus = 'pending' | 'ok' | 'failed' | 'skipped';

/**
 * Sorts a failed GET /clusters/available into "say it could not check"
 * ('failed') or "say nothing" ('skipped'). A viewer is not allowed to ask
 * (403), and a set-up with no usable connection is not fixed by a refresh:
 * neither is a failure the user can fix from this page (EP3 round S, S27).
 */
export function availableFailureKind(err: unknown): 'failed' | 'skipped' {
  // Read the fields, not `instanceof`: many tests mock the api module.
  const status = (err as { status?: unknown } | null)?.status;
  if (status === 401 || status === 403) return 'skipped';
  const message = err instanceof Error ? err.message : '';
  if (typeof status === 'number' && NO_USABLE_CONNECTION_ERRORS.includes(message)) return 'skipped';
  return 'failed';
}

// The button that lets go of a picked cluster, so the fields for a cluster
// typed by hand show again.
export const PICKER_ENTER_BY_HAND = 'Enter a different cluster by hand';

// What an open pull request in the pending table is for.
const PENDING_OPERATION_LABEL: Record<string, string> = {
  register: 'Register',
  adopt: 'Adopt',
  takeover: 'Take ownership',
  remove: 'Remove',
};
// An operation this page does not know yet (a newer server).
const PENDING_OPERATION_OTHER = 'Change';

export function ClustersOverview() {
  const [allClusters, setAllClusters] = useState<Cluster[]>([]);
  // Mirror the latest allClusters in a ref so fetchData's catch block can read
  // the current length without (a) closing over stale state and (b) putting
  // allClusters in fetchData's dep array (which would cause the fetch effect
  // to re-fire on every state update).
  const allClustersRef = useRef<Cluster[]>([]);
  // True while the Adopt or Take ownership window is open on this page. The
  // error screen takes the whole page, and with it the open window, so a
  // quiet reload that fails while a window is open must not bring it up
  // (EP3 round S, S34). The reload runs again when the window closes.
  const actionDialogOpenRef = useRef(false);
  const reloadFailedWhileDialogOpenRef = useRef(false);
  // True once a load has worked. From then on every reload is quiet: the page
  // stays on screen and any open dialog (like the Adopt success screen with
  // its PR link) is not thrown away by a full-page loading swap.
  const hasLoadedRef = useRef(false);
  // S4: server-side pagination — api.getClusters() already requests
  // CLUSTERS_PAGE_SIZE (100), which covers any realistic estate in one
  // call. clusterTotal stays null until a page-aware call
  // (api.getClustersPage) has actually read X-Total-Count; loadingMore
  // guards the "Load more clusters" button below against double-clicks.
  const [clusterTotal, setClusterTotal] = useState<number | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const [loadMoreError, setLoadMoreError] = useState<string | null>(null);
  // Cluster-registration PRs that have NOT yet merged. The BE returns
  // these via /api/v1/clusters.pending_registrations. Surfaced as a
  // dedicated "Pending Registrations" section AND filtered out of the
  // Managed + Discovered sections so the same cluster never appears
  // twice.
  const [pendingRegistrations, setPendingRegistrations] = useState<PendingRegistration[]>([]);
  // ArgoCD cluster Secrets with no managed-clusters.yaml entry AND no
  // open registration PR (typically left over from a manual-mode register
  // PR closed without merging). Surfaced in a dedicated amber/orange
  // "Leftovers from cancelled registrations" section with a "Discard
  // cancelled registration" button — registration cleanup, not Secret
  // management.
  const [orphanRegistrations, setOrphanRegistrations] = useState<OrphanRegistration[]>([]);
  // Per-cluster orphan-delete state. `null` = no action; `pending` = the
  // confirm dialog is open for this name; `deleting` = the API call is in
  // flight. We use a single piece of state because only one orphan delete
  // can be initiated at a time from the dialog flow.
  const [orphanDeleteTarget, setOrphanDeleteTarget] = useState<string | null>(null);
  const [orphanDeleteLoading, setOrphanDeleteLoading] = useState(false);
  const [orphanDeleteResult, setOrphanDeleteResult] = useState<{ name: string; success?: string; error?: string } | null>(null);
  const [healthStats, setHealthStats] = useState<ClusterHealthStats | null>(null);
  const [loading, setLoading] = useState(true);
  const [isRefreshing, setIsRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [searchParams] = useSearchParams();
  const initialStatus = searchParams.get('status');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>(
    // Keep the `?status=disconnected` deep-link from the Dashboard intact
    // so the headline count and the resulting row list refer to the same
    // set of clusters (everything in managed-clusters.yaml that isn't
    // currently "Connected" / "Successful" in ArgoCD).
    //
    // V2-cleanup-61.1 (A1): the Dashboard's "Needs Attention" panel also
    // links here with `?status=issues` ("View all {n} clusters"). This
    // page has no per-cluster addon-health signal to reproduce the
    // Dashboard's exact "issues" definition (connection failure OR
    // unhealthy addons) — that would need the version-matrix data the
    // Dashboard fetches separately. Honest mechanical mapping: `issues`
    // aliases to the existing `disconnected` problem-subset filter
    // (connection failures), the closest real filter this page has,
    // rather than silently ignoring the param.
    //
    // Bug fix (S5, scale-walk grounding): every other StatusFilter value
    // (`connected`, `failed`, `missing_from_argocd`, `not_in_git`) is a
    // real filter the row-list switch below already understands, but this
    // initial-state mapping only ever recognized `disconnected`/`issues` —
    // a deep link like `?status=connected` (the dashboard donut's
    // "connected" row, S5) silently fell through to `all` instead of
    // actually filtering. Recognize any valid StatusFilter value directly.
    initialStatus === 'disconnected' || initialStatus === 'issues'
      ? 'disconnected'
      : (VALID_STATUS_FILTERS as readonly string[]).includes(initialStatus ?? '')
        ? (initialStatus as StatusFilter)
        : 'all'
  );
  const [filters, setFilters] = useState<Filters>({
    name: '',
    versions: [],
    connectionTypes: [],
  });
  const [viewMode, setViewMode] = useState<'list' | 'grid'>('list');
  // S1: sort + pagination for the managed clusters list. Page resets to 1
  // whenever search/filters/sort/page-size change (effect below) so a
  // narrowed result set never leaves the user stranded on an empty page.
  const [sortOption, setSortOption] = useState<ClusterSortOption>('name-asc');
  const [managedPage, setManagedPage] = useState(1);
  const [managedPageSize, setManagedPageSize] = useState<PageSize>(20);
  // V2-cleanup-61.3 (B3): below the collapse threshold the legend is
  // on-demand — this tracks whether the user has opened it.
  // V2-cleanup-92.1 (F3): shown by default (legendOpen starts true).
  const [legendOpen, setLegendOpen] = useState(true);
  const [versionDropdownOpen, setVersionDropdownOpen] = useState(false);
  const [connectionDropdownOpen, setConnectionDropdownOpen] = useState(false);
  const navigate = useNavigate();
  const location = useLocation();
  // S3 (walk day 7): clicking a stat card jumps down to the managed
  // clusters list instead of leaving the user scrolled at the top with no
  // sign anything happened. Scrolled only from the click handler
  // (handleStatusFilter below) — never from an effect, so an unrelated
  // re-render (a 30s poll, a filter typed elsewhere) can never yank the
  // page down on its own.
  const managedSectionRef = useRef<HTMLDivElement>(null);

  // V3-D5: cluster removal PR note carried via router state from ClusterDetail
  // after a successful removal. Read on mount, show as dismissible banner,
  // clear state so a refresh drops the note.
  const [removalNote, setRemovalNote] = useState<{
    cluster: string;
    pr_url?: string;
    pr_id?: number;
    merged: boolean;
  } | null>(() => location.state?.removalPR ?? null);

  // Test connection state per cluster
  // When each test on this page finished, by cluster name. The popover
  // uses it to tell this run from a newer one the server remembers.
  const testAtRef = useRef<Record<string, string>>({});
  const [testResults, setTestResults] = useState<Record<string, { reachable?: boolean; success?: boolean; server_version?: string; platform?: string; error?: string; error_message?: string; suggestions?: string[]; steps?: VerifyStep[] } | TestClusterUnavailable | 'testing'>>({});

  // Expanded test steps per cluster
  const [expandedTestSteps, setExpandedTestSteps] = useState<Record<string, boolean>>({});

  // Diagnose modal state
  const [diagnoseCluster, setDiagnoseCluster] = useState<string | null>(null);

  // Adopt dialog state
  const [adoptDialogOpen, setAdoptDialogOpen] = useState(false);
  const [adoptDialogClusters, setAdoptDialogClusters] = useState<Cluster[]>([]);
  // Clusters ArgoCD already knows that Sharko does not manage yet, from
  // GET /clusters/available. Empty until the server answers (or when it
  // cannot): the page then falls back to what /clusters itself lists.
  const [availableClusters, setAvailableClusters] = useState<AvailableCluster[]>([]);
  // How the last /clusters/available call went:
  //   'pending' — no answer yet;
  //   'ok'      — ArgoCD's list came back;
  //   'failed'  — a failure a refresh can fix (network, ArgoCD or Git not
  //               answering). The page keeps the last good list and says, in
  //               one line, that it could not check;
  //   'skipped' — the user may not ask (a viewer gets 403), or Sharko has no
  //               usable connection yet. Nothing to say about it here.
  // Only 'ok' lets the Add Cluster dialog say "Sharko checked ArgoCD".
  const [availableStatus, setAvailableStatus] = useState<AvailableStatus>('pending');
  const availableFailed = availableStatus === 'failed';
  // Each call gets a number. Only the answer to the newest call is used, so
  // a slow, older answer never replaces a newer one.
  const availableSeqRef = useRef(0);
  // The cluster the Take ownership dialog is open for, from the Clusters page.
  const [takeoverTarget, setTakeoverTarget] = useState<string | null>(null);
  // V2-cleanup-89.3: which discovered (ArgoCD-only) cluster is picked in the
  // Register dialog's "Pick from what ArgoCD already has" block, keyed by
  // name. Single-select — adopting is a one-cluster-at-a-time decision made
  // from inside the dialog now, not a standing bulk-select list.
  const [pickedDiscovered, setPickedDiscovered] = useState<string | null>(null);
  // True once the user let go of the picked cluster to type one by hand.
  // Stops the one-cluster auto-pick from picking it again.
  const userLetGoOfPickRef = useRef(false);
  // V2-cleanup-92.1 (F13): search filter for discovered-clusters picker.
  const [discoveredFilter, setDiscoveredFilter] = useState('');

  // Un-adopt state
  const [unadoptTarget, setUnadoptTarget] = useState<string | null>(null);
  const [unadoptLoading, setUnadoptLoading] = useState(false);
  // Un-adopt result: a PR-bearing result (clickable PRResultBanner), a plain
  // success message (no PR), or an error (V2-cleanup-24).
  const [unadoptResult, setUnadoptResult] = useState<{ pr?: PRWriteResult; success?: string; error?: string } | null>(null);
  const [unadoptPreview, setUnadoptPreview] = useState<DryRunResult | null>(null);
  const [unadoptPreviewLoading, setUnadoptPreviewLoading] = useState(false);
  const [unadoptPreviewError, setUnadoptPreviewError] = useState<string | null>(null);

  // ArgoCD unreachable detection
  const [argoCDUnreachable, setArgoCDUnreachable] = useState(false);

  // Capability flag for the per-cluster Test button. Driven by
  // /api/v1/health.cluster_test_available (true when a secrets backend —
  // Vault / AWS Secrets Manager / file-store / ArgoCDProvider auto-default
  // — is configured on the active connection). When false, the backend
  // returns 503 + error_code=no_secrets_backend for POST
  // /clusters/{name}/test, so we render the button disabled with a
  // tooltip explaining how to enable it. Defaults to `true` so the first
  // render before /health resolves doesn't flash a disabled state for
  // installs that DO have a backend.
  const [clusterTestAvailable, setClusterTestAvailable] = useState(true);
  const TEST_BUTTON_DISABLED_TOOLTIP =
    'Cluster connectivity test is unavailable: no secrets backend (Vault / AWS Secrets Manager / file-store) is configured on the active connection. Configure one in Settings → Connections to enable.';

  // Legacy escape hatch for the "Paste a kubeconfig" registration path
  // (V2-cleanup-89.6; default flipped off by the connection-reconciliation
  // epic, product correction 5). Starts hidden and only appears once the
  // server confirms the setting is on — matches the fail-closed polarity
  // used server-side, and the server refuses pasted credentials with a 403
  // when the setting is off regardless.
  const [allowInlineCredentials, setAllowInlineCredentials] = useState(false);

  // Add Cluster dialog state
  const [addClusterOpen, setAddClusterOpen] = useState(false);
  const [addClusterName, setAddClusterName] = useState('');
  const [addClusterRegion, setAddClusterRegion] = useState('');
  const [addClusterRoleArn, setAddClusterRoleArn] = useState('');
  const [addClusterSecretPath, setAddClusterSecretPath] = useState('');
  // Kubeconfig YAML pasted by the user when provider === 'kubeconfig'.
  const [addClusterKubeconfig, setAddClusterKubeconfig] = useState('');
  const [addClusterSubmitting, setAddClusterSubmitting] = useState(false);
  // Holds whatever the register/dry-run catch received (an ApiError from
  // api.ts, a bare Error, or null) so ErrorDetail can render the server's
  // cause/hint alongside the headline instead of a flattened string
  // (error review package 2).
  const [addClusterError, setAddClusterError] = useState<unknown>(null);
  const [addClusterResult, setAddClusterResult] = useState<RegisterClusterResult | null>(null);
  const [addClusterResultMsg, setAddClusterResultMsg] = useState<string | null>(null);

  // Credential-source selection (creds-reframe-2). This is the PRIMARY
  // question the Direct-mode form asks: "How should Sharko get this
  // cluster's credentials?" It drives which inputs are shown and is sent
  // to the backend as `creds_source`.
  //
  // NO silent default (V2-cleanup-60.4): the dialog used to open on
  // 'eks-token', which quietly routed a registration to the AWS token path
  // when the user never made a choice — the exact trap the maintainer fell
  // into. '' means "not chosen yet"; the Preview/Register buttons stay
  // disabled until the user picks explicitly.
  const [credsSource, setCredsSource] = useState<CredsSource | ''>('');

  // Connection-ownership choice (V2-cleanup-57.2): who manages the ArgoCD
  // cluster secret. 'sharko' (default) = today's behavior; 'user' = the
  // user creates the secret by hand, Sharko only syncs addon labels onto
  // it, and the credential inputs become optional (verification only).
  const [connManagedBy, setConnManagedBy] = useState<ConnOwnership>('sharko');

  // V2-cleanup-91.2 (F2): toggle for optional connection credentials in the
  // "I do" + nothing-to-adopt case. When false, the selector is collapsed;
  // when true, it expands.
  const [showOptionalCreds, setShowOptionalCreds] = useState(false);

  // Legacy `provider` value, kept in sync with `credsSource` so anything
  // that still reads `provider` (audit trails, persisted state) sees a
  // sensible value. The backend keys on `creds_source` (it wins), so this
  // is metadata only — we just never send a value that contradicts the
  // chosen creds source:
  //   inline-kubeconfig → 'kubeconfig'   (today's inline-paste ELSE branch)
  //   secret-kubeconfig → 'eks'          (non-kubeconfig; secret-backed)
  //   eks-token         → 'eks'          (the AWS token path)
  const providerForCredsSource = (cs: CredsSource): ClusterProvider =>
    cs === 'inline-kubeconfig' ? 'kubeconfig' : 'eks';
  // Before an explicit choice exists ('' state) the payload builder is
  // unreachable (buttons disabled), so the fallback here is display-only.
  const provider: ClusterProvider = credsSource === '' ? 'eks' : providerForCredsSource(credsSource);

  // Dry-run preview
  const [dryRunResult, setDryRunResult] = useState<DryRunResult | null>(null);
  const [dryRunLoading, setDryRunLoading] = useState(false);

  // Layer 1 — Identity (V2-cleanup-88.5): what Sharko has auto-detected
  // about its own AWS identity, fetched once when the dialog opens. `null`
  // after loading means the fetch failed (or the endpoint isn't reachable)
  // — ClusterIdentityPanel treats that the same as "not detected", which is
  // truthful either way and never blocks the form.
  const [capabilities, setCapabilities] = useState<SystemCapabilitiesResponse | null>(null);
  const [capabilitiesLoading, setCapabilitiesLoading] = useState(false);

  const fetchData = useCallback(async (requestedBackground = false) => {
    const background = requestedBackground || hasLoadedRef.current;
    try {
      if (background) {
        setIsRefreshing(true);
      } else {
        setLoading(true);
      }
      const response: ClustersResponse = await api.getClusters();
      // Only clear `error` once we actually have fresh data in hand.
      // Clearing pre-emptively would let a failed background refresh kick
      // the prior ErrorState off-screen and leave an empty cluster list
      // with no error chrome.
      setError(null);
      hasLoadedRef.current = true;
      // Best effort: the hint line and the picker only gain from this.
      // Wrapped because a test's partial API mock has no such function, and
      // a failed answer must never break the page.
      const seq = ++availableSeqRef.current;
      try {
        Promise.resolve(api.getAvailableClusters())
          .then((list) => {
            if (seq !== availableSeqRef.current) return;
            setAvailableClusters(Array.isArray(list) ? list : []);
            setAvailableStatus('ok');
          })
          .catch((err: unknown) => {
            if (seq !== availableSeqRef.current) return;
            setAvailableStatus(availableFailureKind(err));
          });
      } catch {
        /* no available list: the page uses what /clusters listed */
        setAvailableStatus('ok');
      }
      setAllClusters(response.clusters);
      // A fresh load — any earlier "Load more" progress no longer applies.
      setClusterTotal(null);
      setLoadMoreError(null);
      const healthStatsValue = response.health_stats ?? null;
      setHealthStats(healthStatsValue);
      // Default to [] so a server that omits these fields doesn't crash
      // this view (forward-compat).
      const pendingRegistrationsValue = response.pending_registrations ?? [];
      const orphanRegistrationsValue = response.orphan_registrations ?? [];
      setPendingRegistrations(pendingRegistrationsValue);
      setOrphanRegistrations(orphanRegistrationsValue);
      // ArgoCD unreachable: the same rule as the Dashboard and the menu badge.
      // The only honest sign is the clusters request itself failing (see the
      // catch below). A request that answered means ArgoCD answered, whatever
      // the cluster statuses say.
      const argoCDUnreachableValue = false;
      setArgoCDUnreachable(argoCDUnreachableValue);

      // perf S2 write-through — every mutation flow (register/adopt/unadopt/
      // delete-orphan) ends by calling fetchData(), so this one write-through
      // site keeps the cache honest without a separate invalidation system.
      setCached<ClustersSnapshot>(CLUSTERS_CACHE_KEY, {
        allClusters: response.clusters,
        healthStats: healthStatsValue,
        pendingRegistrations: pendingRegistrationsValue,
        orphanRegistrations: orphanRegistrationsValue,
        argoCDUnreachable: argoCDUnreachableValue,
      });
    } catch (e: unknown) {
      const message = e instanceof Error ? e.message : 'Failed to load clusters';
      setArgoCDUnreachable(true);
      if (!background) {
        setError(message);
        setAllClusters([]);
        setHealthStats(null);
      } else {
        // A background refresh that fails MUST surface an error when
        // there is no prior data to show — otherwise the page would
        // render an empty stat grid with no indication anything went
        // wrong. If we already have data, keep it on screen and let the
        // next refresh try to recover. Read the prior length via the ref
        // (NOT inside a state updater, which would violate purity in
        // StrictMode).
        if (actionDialogOpenRef.current) {
          // Keep the page, and the open window, as it is. Try again on close.
          reloadFailedWhileDialogOpenRef.current = true;
        } else if (allClustersRef.current.length === 0) {
          setError(message);
          setHealthStats(null);
          // Nothing to keep on screen: let the retry show the loading state.
          hasLoadedRef.current = false;
        }
        // Intentionally do NOT call setAllClusters — prior data stays on screen.
      }
    } finally {
      setLoading(false);
      setIsRefreshing(false);
    }
  }, []);

  const handleRefresh = useCallback(() => {
    void fetchData(true);
  }, [fetchData]);

  // S4: fetch the next page beyond what api.getClusters() already loaded.
  // Only reachable once allClusters.length has hit a full CLUSTERS_PAGE_SIZE
  // page — see mayHaveMoreClusters below — so this never fires for any
  // estate that fits in one request (every demo size up to 100 clusters).
  const handleLoadMoreClusters = useCallback(async () => {
    setLoadingMore(true);
    setLoadMoreError(null);
    try {
      const loadedSoFar = allClustersRef.current.length;
      const nextPage = Math.floor(loadedSoFar / CLUSTERS_PAGE_SIZE) + 1;
      const { data, total } = await api.getClustersPage(nextPage, CLUSTERS_PAGE_SIZE);
      setAllClusters((prev) => [...prev, ...data.clusters]);
      // total == null means the server didn't send X-Total-Count (should
      // never happen against the real API, but degrade safely): treat
      // "loaded everything this call returned" as the total so the button
      // disappears rather than risking an unbounded fetch loop.
      setClusterTotal(total ?? loadedSoFar + data.clusters.length);
    } catch (e: unknown) {
      setLoadMoreError(e instanceof Error ? e.message : 'Failed to load more clusters');
    } finally {
      setLoadingMore(false);
    }
  }, []);

  // Keep allClustersRef in sync with allClusters so fetchData's catch
  // block can check "did we have prior data on screen?" without depending
  // on allClusters in its dep array.
  useEffect(() => {
    allClustersRef.current = allClusters;
  }, [allClusters]);

  // perf S2 — stale-while-refresh: a cache hit paints the cluster list
  // instantly (no spinner) from this session's last successful load, then
  // a normal background fetch quietly brings it current.
  useEffect(() => {
    const cached = getCached<ClustersSnapshot>(CLUSTERS_CACHE_KEY);
    if (cached) {
      setAllClusters(cached.data.allClusters);
      setHealthStats(cached.data.healthStats);
      setPendingRegistrations(cached.data.pendingRegistrations);
      setOrphanRegistrations(cached.data.orphanRegistrations);
      setArgoCDUnreachable(cached.data.argoCDUnreachable);
      setLoading(false);
      void fetchData(true);
    } else {
      void fetchData();
    }
  }, [fetchData]);

  // V3-D5: clear the removalPR state from the router after reading it on mount
  // so a manual browser refresh drops the note.
  useEffect(() => {
    if (location.state?.removalPR) {
      navigate('.', { replace: true, state: {} });
    }
  }, [location.state?.removalPR, navigate]);

  // Fetch /health once to learn whether the cluster-connectivity test
  // endpoint is available (depends on a secrets backend being configured).
  // The flag does not change at runtime once a backend is wired, so we
  // don't poll. If /health fails we keep the optimistic default — better
  // to let the operator click and see the structured 503 than to silently
  // disable a feature.
  useEffect(() => {
    let cancelled = false;
    void api
      .health()
      .then((h) => {
        if (cancelled) return;
        if (typeof h?.cluster_test_available === 'boolean') {
          setClusterTestAvailable(h.cluster_test_available);
        }
      })
      .catch(() => {
        /* keep optimistic default */
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // Fetch the admin-level allow_inline_credentials kill switch
  // (V2-cleanup-89.6) whenever the Register dialog OPENS, not just once on
  // mount (V2-cleanup-90.4): an admin flipping the setting mid-session
  // wasn't reflected until a full page reload — refetching on every open
  // means the very next open sees the current value. Every ClustersOverview
  // test suite's '@/services/api' mock now includes this method, so the
  // old defensive `typeof api.getAllowInlineCredentials !== 'function'`
  // skip (which masked stale mocks instead of the mocks being fixed) is
  // gone.
  useEffect(() => {
    if (!addClusterOpen) return;
    let cancelled = false;
    void api
      .getAllowInlineCredentials()
      .then((res) => {
        if (cancelled) return;
        if (typeof res?.allow_inline_credentials === 'boolean') {
          setAllowInlineCredentials(res.allow_inline_credentials);
        }
      })
      .catch(() => {
        /* keep the fail-closed default (hidden) — the server refuses
         * pasted credentials anyway when the setting is off */
      });
    return () => {
      cancelled = true;
    };
  }, [addClusterOpen]);

  // Auto-refresh every 30s, but not while the Adopt or Take ownership window
  // is open (S34): the same rule as the cluster page.
  const actionDialogOpen = adoptDialogOpen || takeoverTarget !== null;
  useEffect(() => {
    actionDialogOpenRef.current = actionDialogOpen;
    if (actionDialogOpen) return;
    if (reloadFailedWhileDialogOpenRef.current) {
      reloadFailedWhileDialogOpenRef.current = false;
      void fetchData(true);
    }
    const interval = setInterval(() => {
      void fetchData(true);
    }, 30_000);
    return () => clearInterval(interval);
  }, [fetchData, actionDialogOpen]);

  // `presetOwnership` (V2-cleanup-89.3): the collapsed Discovered-clusters
  // hint opens this same dialog pre-set to "I do", since that's the only
  // ownership mode where adopting an ArgoCD-known cluster makes sense. The
  // plain "Add Cluster" button still opens on the Sharko-managed default.
  const openAddCluster = useCallback((presetOwnership?: ConnOwnership) => {
    setAddClusterOpen(true);
    setAddClusterError(null);
    setAddClusterResult(null);
    setAddClusterResultMsg(null);
    setAddClusterName('');
    setAddClusterRegion('');
    setAddClusterRoleArn('');
    setAddClusterSecretPath('');
    setAddClusterKubeconfig('');
    // No silent creds-source default — the user must choose (V2-cleanup-60.4).
    setCredsSource('');
    setConnManagedBy(presetOwnership ?? 'sharko');
    setPickedDiscovered(null);
    userLetGoOfPickRef.current = false;
    setDiscoveredFilter('');
    // V2-cleanup-91.2 (F2): reset optional-creds toggle when dialog reopens.
    setShowOptionalCreds(false);
    setDryRunResult(null);
    setDryRunLoading(false);
    // Fetch Layer 1 identity info once per dialog session (V2-cleanup-88.5).
    // Wrapped in try/catch (not just a Promise .catch) because a strict
    // `vi.mock('@/services/api', ...)` factory that doesn't declare this
    // export throws synchronously on the property read itself, not just on
    // the call — the panel just falls back to its "not detected" copy,
    // same as it would for a real fetch failure.
    if (!capabilities) {
      setCapabilitiesLoading(true);
      try {
        Promise.resolve(getSystemCapabilities())
          .then((c) => { if (c) setCapabilities(c); })
          .catch(() => {})
          .finally(() => setCapabilitiesLoading(false));
      } catch {
        setCapabilitiesLoading(false);
      }
    }
  }, [capabilities]);

  // Build the Direct-mode register payload for the chosen credential source
  // (creds-reframe-2). `creds_source` is the authoritative signal; we also
  // send a consistent `provider` for backward-compat. Each source sends a
  // DISJOINT field set so the backend's per-source validation is happy:
  //   inline-kubeconfig → name + creds_source + provider + kubeconfig
  //   secret-kubeconfig → name + creds_source + provider + secret_path (+region)
  //   eks-token         → name + creds_source + provider + region/role_arn/secret_path
  const buildRegisterPayload = useCallback(
    (name: string, extra?: { dry_run?: boolean }): Parameters<typeof registerCluster>[0] => {
      // Registration is gated until an explicit creds-source choice exists
      // (directRequiredMissing), so '' here should not normally happen —
      // eks-token is a safe fallback shape.
      const source: CredsSource = credsSource === '' ? 'eks-token' : credsSource;
      // connection_managed_by is only sent for the non-default 'user'
      // choice — omitting it for 'sharko' keeps the request byte-compatible
      // with pre-57.2 servers.
      const base = {
        name,
        creds_source: source,
        provider,
        ...(connManagedBy === 'user' ? { connection_managed_by: 'user' as const } : {}),
        ...extra,
      };
      switch (source) {
        case 'inline-kubeconfig':
          // Inline paste — server rejects AWS-shaped fields here, so omit them.
          return { ...base, kubeconfig: addClusterKubeconfig };
        case 'secret-kubeconfig':
          // Stored kubeconfig — the secret name/path is the key input. Region
          // is optional metadata.
          return {
            ...base,
            secret_path: addClusterSecretPath.trim() || undefined,
            region: addClusterRegion.trim() || undefined,
          };
        case 'eks-token':
        default:
          // EKS token from cloud identity — AWS-shaped fields.
          return {
            ...base,
            region: addClusterRegion.trim() || undefined,
            secret_path: addClusterSecretPath.trim() || undefined,
            role_arn: addClusterRoleArn.trim() || undefined,
          };
      }
    },
    [credsSource, provider, connManagedBy, addClusterKubeconfig, addClusterSecretPath, addClusterRegion, addClusterRoleArn],
  );

  // A preview describes the values it was made from. When the user changes a
  // field, the old preview no longer matches what Register would do, so it is
  // removed, and an answer still on its way for the old values is dropped.
  const dryRunSeq = useRef(0);
  useEffect(() => {
    dryRunSeq.current += 1;
    setDryRunResult(null);
    setDryRunLoading(false);
  }, [
    addClusterName,
    addClusterKubeconfig,
    addClusterSecretPath,
    addClusterRegion,
    addClusterRoleArn,
    credsSource,
    connManagedBy,
    pickedDiscovered,
  ]);

  const handleDryRun = useCallback(async () => {
    const clusterName = addClusterName.trim();
    if (!clusterName) return;
    setDryRunLoading(true);
    setDryRunResult(null);
    setAddClusterError(null);
    const seq = ++dryRunSeq.current;
    try {
      const result = await registerCluster(
        buildRegisterPayload(clusterName, { dry_run: true }),
      );
      if (seq !== dryRunSeq.current) return;
      if (result?.dry_run) {
        setDryRunResult(result.dry_run);
      } else if (result?.status === 'partial') {
        // The preview stopped part-way (for example Sharko could not read
        // the cluster registry from Git). Show the server's sentence.
        setAddClusterError(new Error(result.message || 'Sharko could not build the preview.'));
      }
    } catch (e: unknown) {
      if (seq !== dryRunSeq.current) return;
      setAddClusterError(e);
    } finally {
      if (seq === dryRunSeq.current) setDryRunLoading(false);
    }
  }, [addClusterName, buildRegisterPayload]);

  const handleAddCluster = useCallback(async () => {
    if (!addClusterName.trim()) return;
    setAddClusterSubmitting(true);
    setAddClusterError(null);
    setAddClusterResult(null);
    setAddClusterResultMsg(null);
    try {
      // The chosen creds source (creds-reframe-2) decides which disjoint
      // field set is sent — see buildRegisterPayload. `creds_source` is
      // authoritative; `provider` rides along for backward-compat.
      const result = await registerCluster(buildRegisterPayload(addClusterName.trim()));
      const prUrl = result?.git?.pr_url || result?.pr_url || result?.pull_request_url;
      const merged = result?.git?.merged ?? false;
      const clusterName = addClusterName.trim();
      // The registration stopped before any pull request was opened (for
      // example Sharko could not read the cluster registry from Git, or
      // the Git commit failed). Show the server's sentence in the dialog,
      // the same way the preview does, and keep what the user typed so
      // they can try again (LO-8 R6). Before, this fell through to the
      // "submitted as a pull request" sentence, which was not true.
      const stoppedMsg = writeFailureMessage(result, REGISTER_DID_NOT_FINISH);
      if (stoppedMsg) {
        setAddClusterError(new Error(stoppedMsg));
        void fetchData();
        return;
      }
      // Manual-mode register opens a PR but the cluster is NOT actually
      // registered until merge. Branch on `merged` so the toast tells
      // the user the truth.
      if (result?.status === 'partial') {
        // A pull request exists, but a step did not finish (auto-merge
        // failed, or some addon secrets were not created). The banner
        // shows the server's sentence and the PR link (LO-8 R6).
        setAddClusterResultMsg(result.message || REGISTER_DID_NOT_FINISH);
        if (merged) {
          void fetchData();
        }
      } else if (merged) {
        // Auto-merge succeeded (or PR-merge was implicit). Cluster is
        // truly registered.
        setAddClusterResultMsg(prUrl
          ? `__merged__|${prUrl}`
          : 'Cluster registered successfully.');
        // V3-CC1: auto-test the just-registered cluster now that it's
        // truly connected (merged=true path ONLY — not the PR-pending path,
        // which would test an unwired cluster → false failure).
        void fetchData().then(() => {
          testClusterConnection(clusterName)
            .then((testResult) => {
              testAtRef.current[clusterName] = new Date().toISOString();
              setTestResults((prev) => ({ ...prev, [clusterName]: testResult }));
            })
            .catch((err) => {
              testAtRef.current[clusterName] = new Date().toISOString();
              setTestResults((prev) => ({
                ...prev,
                [clusterName]: {
                  reachable: false,
                  error: err instanceof Error ? err.message : 'Connection test failed',
                },
              }));
            });
        });
      } else if (prUrl) {
        // Manual mode (or auto-merge requested but not yet merged):
        // values-file PR is open. The cluster won't appear as managed
        // until the PR is merged. We tag the message with a `__pending__`
        // prefix so the renderer picks the "PR opened — merge to
        // activate" wording rather than the legacy "Cluster registered"
        // wording.
        setAddClusterResultMsg(`__pending__|${prUrl}`);
      } else {
        // The server said "success" but sent no PR link and no merge.
        // Stay truthful — don't claim "registered" without evidence, and
        // don't guess more than the answer says (LO-9 R9).
        setAddClusterResultMsg(REGISTER_PR_NO_LINK);
      }
      setAddClusterResult(result);
      setAddClusterOpen(false);
      if (!merged) {
        void fetchData();
      }
    } catch (e: unknown) {
      setAddClusterError(e);
    } finally {
      setAddClusterSubmitting(false);
    }
  }, [addClusterName, buildRegisterPayload, fetchData]);

  const handleTestCluster = useCallback(async (name: string, e: React.MouseEvent) => {
    e.stopPropagation();
    setTestResults((prev) => ({ ...prev, [name]: 'testing' }));
    try {
      const result = await testClusterConnection(name);
      testAtRef.current[name] = new Date().toISOString();
      setTestResults((prev) => ({ ...prev, [name]: result }));
    } catch (err) {
      testAtRef.current[name] = new Date().toISOString();
      setTestResults((prev) => ({ ...prev, [name]: { reachable: false, error: err instanceof Error ? err.message : 'Failed' } }));
    }
  }, []);

  const toggleTestSteps = useCallback((name: string, e: React.MouseEvent) => {
    e.stopPropagation();
    setExpandedTestSteps((prev) => ({ ...prev, [name]: !prev[name] }));
  }, []);

  /** Compact test result summary with expandable steps */
  const renderTestResult = useCallback((clusterName: string, testResult: typeof testResults[string], opts?: { showSuggestions?: boolean }) => {
    if (!testResult || testResult === 'testing') return null;
    // Render "test unavailable" distinctly — do NOT show the cluster as
    // "Unreachable" when only the test feature itself is unavailable.
    if (isTestClusterUnavailable(testResult)) {
      return (
        <span className="inline-flex items-center gap-1 text-sm text-amber-700 dark:text-amber-300" title={testResult.error}>
          <AlertTriangle className="h-3 w-3" />
          Test unavailable
        </span>
      );
    }
    const isSuccess = testResult.reachable !== false && testResult.success !== false;
    const steps = testResult.steps;
    const passedCount = steps?.filter((s) => s.status === 'pass').length ?? 0;
    const totalCount = steps?.length ?? 0;
    const failedStep = steps?.find((s) => s.status === 'fail');
    const expanded = expandedTestSteps[clusterName];

    return (
      <div className="flex flex-col gap-1">
        {/* The Test flow is Sharko's own connection — say so (V2-cleanup-55.3). */}
        <WhoseConnectionLabel who="sharko" />
        <button
          type="button"
          onClick={(e) => steps && steps.length > 0 ? toggleTestSteps(clusterName, e) : e.stopPropagation()}
          className={`inline-flex items-center gap-1 text-xs ${steps && steps.length > 0 ? 'cursor-pointer hover:underline' : 'cursor-default'}`}
        >
          {isSuccess ? (
            <span className="flex items-center gap-1 text-green-600 dark:text-green-400">
              <CheckCircle className="h-3 w-3" />
              {steps && steps.length > 0
                ? `Connected (${passedCount}/${totalCount} checks passed)`
                : [testResult.server_version, testResult.platform].filter(Boolean).join(' \u2014 ') || 'Reachable'}
            </span>
          ) : (
            <span className="flex items-center gap-1 text-red-500 dark:text-red-400">
              <WifiOff className="h-3 w-3" />
              {failedStep
                ? `Failed at: ${failedStep.name}`
                : `Error: ${testResult.error ?? testResult.error_message ?? 'Unreachable'}`}
            </span>
          )}
          {steps && steps.length > 0 && (
            expanded
              ? <ChevronUp className="h-3 w-3 text-[#3a6a8a] dark:text-gray-400" />
              : <ChevronDown className="h-3 w-3 text-[#3a6a8a] dark:text-gray-400" />
          )}
        </button>
        {expanded && steps && steps.length > 0 && (
          <div className="mt-1 rounded-md bg-[#f8fbff] p-2 ring-1 ring-[#d0e4f5] dark:bg-gray-800 dark:ring-gray-700" onClick={(e) => e.stopPropagation()}>
            {steps.map((step, i) => (
              <div key={i} className="flex items-start gap-1.5 py-0.5 text-xs">
                {step.status === 'pass' && <span className="text-green-600 dark:text-green-400">&#10003;</span>}
                {step.status === 'fail' && <span className="text-red-500 dark:text-red-400">&#10007;</span>}
                {step.status === 'skipped' && <span className="text-[#5a8aaa] dark:text-gray-500">&#8211;</span>}
                <span className={step.status === 'fail' ? 'text-red-600 dark:text-red-400' : 'text-[#2a5a7a] dark:text-gray-300'}>
                  {step.name}
                  {step.detail && <span className="ml-1 text-[#5a8aaa] dark:text-gray-500">({step.detail})</span>}
                </span>
              </div>
            ))}
          </div>
        )}
        {!isSuccess && opts?.showSuggestions && testResult.suggestions && testResult.suggestions.length > 0 && (
          <button
            type="button"
            onClick={(e) => { e.stopPropagation(); navigate(`/clusters/${clusterName}`); }}
            className="inline-flex items-center gap-1 text-xs text-[#0a3a5a] underline hover:text-[#2a5a7a] dark:text-blue-400 dark:hover:text-blue-300"
          >
            {testResult.suggestions.length} similar secret{testResult.suggestions.length > 1 ? 's' : ''} found — click to fix
          </button>
        )}
      </div>
    );
  }, [expandedTestSteps, toggleTestSteps, navigate]);

  const handleOpenAdoptDialog = useCallback((clusters: Cluster[]) => {
    setAdoptDialogClusters(clusters);
    setAdoptDialogOpen(true);
  }, []);

  const handleAdoptSuccess = useCallback(() => {
    void fetchData();
  }, [fetchData]);

  const handlePreviewUnadopt = useCallback(async () => {
    if (!unadoptTarget) return;
    setUnadoptPreviewLoading(true);
    setUnadoptPreviewError(null);
    try {
      const result = await unadoptCluster(unadoptTarget, true);
      setUnadoptPreview(result);
    } catch (err) {
      setUnadoptPreviewError(err instanceof Error ? err.message : 'Failed to generate preview');
    } finally {
      setUnadoptPreviewLoading(false);
    }
  }, [unadoptTarget]);

  const handleUnadopt = useCallback(async () => {
    if (!unadoptTarget) return;
    setUnadoptLoading(true);
    setUnadoptResult(null);
    try {
      const result = await unadoptCluster(unadoptTarget);
      const { prUrl } = extractPR(result);
      // v4.0.4 review fix: the server answers 200 with status "failed" when
      // the Git step did not happen — that is not a success.
      const failure = writeFailureMessage(result, UNADOPT_FAILED_FALLBACK);
      if (failure) {
        setUnadoptResult({ error: failure });
      } else {
        setUnadoptResult(prUrl ? { pr: result } : { success: 'Cluster un-adopted successfully.' });
      }
      setUnadoptTarget(null);
      void fetchData();
    } catch (err) {
      setUnadoptResult({ error: err instanceof Error ? err.message : 'Un-adopt failed' });
      setUnadoptTarget(null);
    } finally {
      setUnadoptLoading(false);
    }
  }, [unadoptTarget, fetchData]);

  // Orphan cluster Secret cleanup. Confirms via ConfirmationModal (same
  // destructive-action pattern as unadopt + remove cluster). On success,
  // refetch to drop the orphan row; on failure, surface the BE error
  // message (the BE returns 400 with a remediation hint if the cluster
  // turns out to be managed/pending in a TOCTOU race).
  const handleDeleteOrphan = useCallback(async () => {
    if (!orphanDeleteTarget) return;
    setOrphanDeleteLoading(true);
    setOrphanDeleteResult(null);
    const target = orphanDeleteTarget;
    try {
      await deleteOrphanCluster(target);
      setOrphanDeleteResult({ name: target, success: `Cancelled registration for "${target}" discarded.` });
      setOrphanDeleteTarget(null);
      void fetchData();
    } catch (err) {
      setOrphanDeleteResult({
        name: target,
        error: err instanceof Error ? err.message : 'Orphan delete failed',
      });
      setOrphanDeleteTarget(null);
    } finally {
      setOrphanDeleteLoading(false);
    }
  }, [orphanDeleteTarget, fetchData]);

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

  // The status filter, the search box and the version and connection
  // filters, as one test. The clusters that only /clusters/available lists
  // go through it too, so a filter hides them like any other row.
  const passesFilters = useCallback(
    (cluster: Cluster): boolean => {
      if (statusFilter !== 'all') {
        const cs = cluster.connection_status?.toLowerCase() ?? '';
        let statusMatch: boolean;
        switch (statusFilter) {
          case 'connected':
            statusMatch = cs === 'connected' || cs === 'successful';
            break;
          case 'disconnected':
            // Failed or missing ONLY — same definition the Dashboard
            // uses for its red "disconnected clusters" count. Pending
            // ("" / Unknown) and discovered / not_in_git clusters are
            // NOT counted here; they aren't a problem.
            statusMatch = cs === 'failed' || cs === 'missing';
            break;
          case 'failed':
            statusMatch = cs === 'failed';
            break;
          case 'missing_from_argocd':
            statusMatch = cs === 'missing';
            break;
          case 'not_in_git':
            statusMatch = cs === 'not_in_git';
            break;
          default:
            statusMatch = true;
        }
        if (!statusMatch) return false;
      }
      const nameMatch = cluster.name
        .toLowerCase()
        .includes(filters.name.toLowerCase());
      const versionMatch =
        filters.versions.length === 0 ||
        (!!cluster.server_version &&
          filters.versions.includes(cluster.server_version));
      const connectionMatch =
        filters.connectionTypes.length === 0 ||
        (!!cluster.connection_status &&
          filters.connectionTypes.includes(cluster.connection_status));
      return nameMatch && versionMatch && connectionMatch;
    },
    [statusFilter, filters],
  );
  const filteredClusters = useMemo(
    () => allClusters.filter(passesFilters),
    [allClusters, passesFilters],
  );

  // Cluster names that have an open registration PR but whose values-file
  // changes have NOT yet merged. They belong only in the "Pending
  // Registrations" surface. The BE strips them from `not_in_git`; we
  // re-apply the filter so a stale BE response or slow refresh can't
  // surface the cluster in two places.
  // Only a REGISTER PR keeps a cluster out of the managed list: that cluster
  // is not managed until the PR merges. An open adopt, take over or remove PR
  // is about a cluster that is already on screen somewhere. A row with no
  // `operation` (an older server) is a register.
  const pendingNames = useMemo(
    () =>
      new Set(
        pendingRegistrations
          .filter((p) => (p.operation ?? 'register') === 'register')
          .map((p) => p.cluster_name),
      ),
    [pendingRegistrations],
  );

  const pendingOnlyRegisters = useMemo(
    () => pendingRegistrations.every((p) => (p.operation ?? 'register') === 'register'),
    [pendingRegistrations],
  );

  // Any open PR at all: the cluster is already being dealt with, so it is not
  // offered again as "ArgoCD knows this one".
  const anyPendingNames = useMemo(
    () => new Set(pendingRegistrations.map((p) => p.cluster_name)),
    [pendingRegistrations],
  );

  // Same defence-in-depth for orphans — they belong only in the
  // "Leftovers from cancelled registrations" section.
  const orphanNames = useMemo(
    () => new Set(orphanRegistrations.map((o) => o.cluster_name)),
    [orphanRegistrations],
  );

  // Split into managed (in git) and discovered (ArgoCD-only / unmanaged)
  const managedClusters = useMemo(
    () => filteredClusters.filter(
      (c) => c.managed !== false && c.connection_status !== 'not_in_git' && !pendingNames.has(c.name) && !orphanNames.has(c.name),
    ),
    [filteredClusters, pendingNames, orphanNames],
  );

  // A filter is hiding rows only when one is actually set. With no filter
  // and no rows, nothing is registered yet, so say that instead.
  const clusterFilterActive =
    statusFilter !== 'all' ||
    filters.name !== '' ||
    filters.versions.length > 0 ||
    filters.connectionTypes.length > 0;
  const emptyManagedMessage = clusterFilterActive
    ? EMPTY_CLUSTERS_FILTERED_MESSAGE
    : EMPTY_CLUSTERS_MESSAGE;

  // Everything ArgoCD knows that Sharko does not manage: what /clusters lists
  // plus what /clusters/available adds. Built from the whole list, with no
  // page filter: the Add Cluster picker, its count and its one-cluster pick
  // use this, so a word typed in the page search never hides a cluster from
  // the picker (EP3 round S, S29).
  const allDiscoveredClusters = useMemo(() => {
    const fromList = allClusters.filter(
      (c) => (c.managed === false || c.connection_status === 'not_in_git') && !anyPendingNames.has(c.name) && !orphanNames.has(c.name),
    );
    // Any name /clusters already lists is handled there. It never comes back
    // as an extra.
    const listed = new Set(allClusters.map((c) => c.name));
    const extra = availableClusters
      .filter(
        (a) =>
          a.name &&
          !listed.has(a.name) &&
          !anyPendingNames.has(a.name) &&
          !orphanNames.has(a.name),
      )
      .map((a): Cluster => ({
        name: a.name,
        labels: {},
        server_url: a.server_url,
        managed: false,
        // For the status filter: ArgoCD knows it, Sharko does not manage it.
        connection_status: 'not_in_git',
      }));
    return [...fromList, ...extra];
  }, [availableClusters, allClusters, anyPendingNames, orphanNames]);

  // The same list with the page's search and filters on. Only the hint line
  // under the page title uses it, so the hint follows what the page shows.
  const discoveredClusters = useMemo(
    () => allDiscoveredClusters.filter(passesFilters),
    [allDiscoveredClusters, passesFilters],
  );

  // S1: sort, THEN paginate — filters/search already ran above in
  // filteredClusters/managedClusters. Sorted via a decorate-sort-undecorate
  // pass (rather than relying on Array.sort's stability) so "Problems
  // first" keeps a deterministic, engine-independent order within each
  // group.
  const sortedManagedClusters = useMemo(() => {
    const decorated = managedClusters.map((cluster, index) => ({ cluster, index }));
    decorated.sort((x, y) => {
      let cmp = 0;
      switch (sortOption) {
        case 'name-asc':
          cmp = x.cluster.name.localeCompare(y.cluster.name);
          break;
        case 'name-desc':
          cmp = y.cluster.name.localeCompare(x.cluster.name);
          break;
        case 'problems-first':
          cmp = Number(isProblemCluster(y.cluster)) - Number(isProblemCluster(x.cluster));
          break;
        case 'k8s-version':
          cmp = compareServerVersionsDesc(x.cluster.server_version, y.cluster.server_version);
          break;
        case 'most-addons':
          cmp = getEnabledAddonCount(y.cluster) - getEnabledAddonCount(x.cluster);
          break;
      }
      return cmp !== 0 ? cmp : x.index - y.index;
    });
    return decorated.map((d) => d.cluster);
  }, [managedClusters, sortOption]);

  const managedTotalPages = Math.max(1, Math.ceil(sortedManagedClusters.length / managedPageSize));
  // Clamp rather than trust `managedPage` directly — a background refresh
  // that shrinks the result set (or a page-size bump) can otherwise leave
  // the view pointed at a page that no longer exists.
  const clampedManagedPage = Math.min(managedPage, managedTotalPages);
  const pagedManagedClusters = useMemo(
    () => sortedManagedClusters.slice(
      (clampedManagedPage - 1) * managedPageSize,
      clampedManagedPage * managedPageSize,
    ),
    [sortedManagedClusters, clampedManagedPage, managedPageSize],
  );

  // Page resets to 1 whenever search, filters, status filter, sort, or
  // page size change — never leaves the user stranded on a now-empty page.
  useEffect(() => {
    setManagedPage(1);
  }, [filters.name, filters.versions, filters.connectionTypes, statusFilter, sortOption, managedPageSize]);

  // V2-cleanup-89.3: confirming a pick in the Register dialog's "Pick from
  // what ArgoCD already has" block closes the dialog and reuses the EXACT
  // same adopt flow the (now-collapsed) standing Discovered section used —
  // same verify-then-confirm dialog, same Git-PR success banner.
  const handleAdoptFromPicker = useCallback(() => {
    const cluster = allDiscoveredClusters.find((c) => c.name === pickedDiscovered);
    if (!cluster) {
      // V2-cleanup-90.4: the picked cluster can disappear between the pick
      // and the confirm click — the picker's 30s auto-refresh may have
      // dropped it from ArgoCD's discovered list. That used to be a silent
      // no-op (the button looked like it did nothing). Name the cluster and
      // reset the selection so the picker returns to its unpicked state.
      if (pickedDiscovered) {
        showToast(`"${pickedDiscovered}" is no longer discoverable — refresh the list.`, 'error');
        setPickedDiscovered(null);
      }
      return;
    }
    setAddClusterOpen(false);
    handleOpenAdoptDialog([cluster]);
  }, [allDiscoveredClusters, pickedDiscovered, handleOpenAdoptDialog]);

  // Take ownership of the picked cluster: same dialog as on the cluster page.
  const handleTakeOwnershipFromPicker = useCallback(() => {
    const cluster = allDiscoveredClusters.find((c) => c.name === pickedDiscovered);
    if (!cluster) {
      if (pickedDiscovered) {
        showToast(`"${pickedDiscovered}" is no longer discoverable — refresh the list.`, 'error');
        setPickedDiscovered(null);
      }
      return;
    }
    setAddClusterOpen(false);
    setTakeoverTarget(cluster.name);
  }, [allDiscoveredClusters, pickedDiscovered]);

  // Only one cluster to pick from: pick it, so Adopt is not greyed until the
  // user clicks a radio button that has no other choice. Picking hides the
  // fields for a cluster typed by hand, so the user can let go of it
  // ("Enter a different cluster by hand"), and then it is not picked again
  // until the dialog opens again.
  useEffect(() => {
    if (
      addClusterOpen &&
      connManagedBy === 'user' &&
      pickedDiscovered === null &&
      !userLetGoOfPickRef.current &&
      allDiscoveredClusters.length === 1
    ) {
      setPickedDiscovered(allDiscoveredClusters[0].name);
    }
  }, [addClusterOpen, connManagedBy, pickedDiscovered, allDiscoveredClusters]);

  // Client-side gate for the Preview/Register buttons.
  //
  // Connection credentials are OPTIONAL at registration, for every
  // connection-source choice and every ownership mode (V2-cleanup-88.3 —
  // lazy credentials; the backend accepts an empty kubeconfig / secret path
  // for every creds_source, see internal/orchestrator/cluster.go's
  // skip_credentials step). Sharko's one ongoing need for a cluster's own
  // connection credentials is pushing an addon's addon secrets, and that
  // need is enforced later, at enable-addon time — not here.
  //
  // The creds-source CHOICE itself is still always required — there is no
  // silent default to fall into (V2-cleanup-60.4) — but which field, if
  // any, is filled in underneath it no longer gates submission.
  //
  // V2-cleanup-91.2 (F2): EXCEPT in the "I do" + nothing-to-adopt case where
  // the creds-source selector is optional (collapsed behind a toggle) — then
  // an empty credsSource is allowed (the user never clicked the toggle to add
  // optional credentials).
  // With a connection ArgoCD already has, Sharko does not need to be told how
  // to connect, so the choice stays folded away behind an optional link.
  const userManagedWithNothingToAdopt = connManagedBy === 'user';
  const directRequiredMissing = credsSource === '' && !userManagedWithNothingToAdopt;

  const handleStatusFilter = (filter: StatusFilter) => {
    setStatusFilter(statusFilter === filter ? 'all' : filter);
    // S3: jump to the managed clusters list so the filter change is
    // visible right away, instead of leaving the user scrolled at the top
    // wondering if the click did anything.
    managedSectionRef.current?.scrollIntoView({ behavior: 'smooth' });
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
    return <ErrorState message={error} onRetry={() => void fetchData(false)} />;
  }

  // LW-10: the backend now excludes pending/orphan registrations from
  // healthStats.not_in_git at the source (internal/api/clusters.go), so the
  // FE consumes the value directly — no client-side subtraction. Fixing
  // not_in_git at the backend also fixes this total (total_in_git +
  // not_in_git).
  const totalClusters = healthStats
    ? healthStats.total_in_git + healthStats.not_in_git
    : allClusters.length;

  // S4: show "Load more" once we've confirmed (via clusterTotal, read from
  // X-Total-Count on a page-aware call) that more clusters exist than are
  // loaded, OR — before that first confirmation — once the initial
  // api.getClusters() call came back completely full (a strong hint there
  // may be more beyond CLUSTERS_PAGE_SIZE). Never true for any estate that
  // fits in one page, so this stays hidden for every size up to 100
  // clusters (covers every demo size, including the 50-cluster preset).
  const mayHaveMoreClusters = clusterTotal !== null
    ? allClusters.length < clusterTotal
    : allClusters.length > 0 && allClusters.length % CLUSTERS_PAGE_SIZE === 0;

  // V2-cleanup-61.3 (B3): at n=1 (or any small fleet) the 5 stat cards + the
  // full filter bar + view toggle are ~5 rows of controls for 1 row of data.
  // Below the locked threshold of 5 clusters, hide the stat-card row and the
  // filter bar entirely, and make the legend on-demand. At >= 5 everything
  // appears automatically, as before. The register/add-cluster button stays
  // prominent regardless of cluster count.
  const showFullControls = totalClusters >= 5;

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
    // Titles and colors follow the canonical "ArgoCD → cluster" vocabulary
    // in lib/clusterStatus.ts (V2-cleanup-61.2, findings D2 + D3):
    // Connected (green) / Disconnected (red) / Connecting (neutral — the
    // normal post-registration wait, not a warning) / Not managed (amber).
    {
      key: 'connected',
      title: 'Connected',
      value: healthStats?.connected ?? 0,
      color: 'success',
      icon: <CheckCircle className="h-5 w-5" />,
    },
    {
      key: 'failed',
      title: 'Disconnected',
      value: healthStats?.failed ?? 0,
      color: 'error',
      icon: <XCircle className="h-5 w-5" />,
    },
    {
      // v4 walk-findings W2, item 2: this lane counts clusters in the
      // canonical 'missing' state (clusterStatus.ts), whose one true label
      // is "Not connected" (amber, needs attention) — "Connecting" implied
      // a transient wait that this state is not.
      key: 'missing_from_argocd',
      title: 'Not connected',
      value: healthStats?.missing_from_argocd ?? 0,
      color: 'default',
      icon: <HelpCircle className="h-5 w-5" />,
    },
    {
      key: 'not_in_git',
      title: 'Available to manage',
      value: healthStats?.not_in_git ?? 0,
      color: 'warning',
      icon: <AlertTriangle className="h-5 w-5" />,
    },
  ];

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          {/* Page title stays "Clusters" — the page already has a "Managed
              Clusters" vs "Discovered" section split below; the nav/
              breadcrumb/page-jump wording is what decision 10 renames
              ("Managed Clusters"), so this in-page title is left distinct
              to avoid two identical headings on the same page. */}
          <h2 className="text-2xl font-bold text-[#0a2a4a] dark:text-gray-100">Clusters</h2>
          {/* S4 (walk day 7, missing-truth finding a): a quiet one-liner
              saying what these clusters actually are and who owns the
              connection — the maintainer's walk found the page never said
              this plainly. */}
          <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-400">
            Clusters ArgoCD deploys to — Sharko manages their connections from git.
          </p>
          {discoveredClusters.length > 0 && (
            <p data-testid="argocd-known-hint" className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-400">
              <RoleGuard
                adminOnly
                fallback={
                  <span>
                    {discoveredClusters.length} cluster{discoveredClusters.length === 1 ? '' : 's'} ArgoCD already knows
                  </span>
                }
              >
                <button
                  type="button"
                  onClick={() => openAddCluster('user')}
                  className="font-medium text-teal-700 underline hover:text-teal-900 dark:text-teal-400 dark:hover:text-teal-300"
                >
                  {discoveredClusters.length} cluster{discoveredClusters.length === 1 ? '' : 's'} ArgoCD already knows
                </button>
              </RoleGuard>
              <span> that Sharko doesn&apos;t manage yet.</span>
            </p>
          )}
          {availableFailed && (
            <p data-testid="argocd-known-failed" className="mt-1 text-sm text-amber-700 dark:text-amber-400">
              {AVAILABLE_CHECK_FAILED}
            </p>
          )}
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <button
            onClick={handleRefresh}
            className="rounded-md p-2 text-[#3a6a8a] hover:bg-[#d6eeff] dark:text-gray-400 dark:hover:bg-gray-700"
            title="Refresh"
          >
            <RefreshCw className={`h-4 w-4 ${isRefreshing ? 'animate-spin' : ''}`} />
          </button>
          <RoleGuard adminOnly>
            <button
              type="button"
              onClick={() => openAddCluster()}
              className="inline-flex shrink-0 items-center gap-2 rounded-lg bg-[#0a2a4a] px-5 py-2.5 text-sm font-semibold text-white shadow-sm hover:bg-[#0d3558] dark:bg-blue-700 dark:hover:bg-blue-600"
            >
              <Plus className="h-4 w-4" />
              Add Cluster
            </button>
          </RoleGuard>
        </div>
      </div>

      {/* ArgoCD Status Banner */}
      <ArgoCDStatusBanner visible={argoCDUnreachable} />

      {/* Cluster Status Legend — automatic at >= 5 clusters (locked
          threshold); below it, an on-demand "what do these mean?" toggle
          instead of a permanently-visible legend (V2-cleanup-61.3, B3). */}
      {showFullControls ? (
        <ClusterStatusLegend />
      ) : (
        <div>
          <button
            type="button"
            onClick={() => setLegendOpen((o) => !o)}
            aria-expanded={legendOpen}
            className="inline-flex items-center gap-1.5 text-sm text-[#3a6a8a] hover:text-[#0a3a5a] dark:text-gray-400 dark:hover:text-gray-200"
          >
            <HelpCircle className="h-3.5 w-3.5" />
            {legendOpen ? 'Hide status legend' : 'Status legend'}
          </button>
          {legendOpen && (
            <div className="mt-2">
              <ClusterStatusLegend />
            </div>
          )}
        </div>
      )}

      {/* Diagnose Modal */}
      <DiagnoseModal
        clusterName={diagnoseCluster ?? ''}
        open={diagnoseCluster !== null}
        onClose={() => setDiagnoseCluster(null)}
      />

      {/* Add Cluster Dialog */}
      <Dialog open={addClusterOpen} onOpenChange={(v) => { if (!v && !addClusterSubmitting) setAddClusterOpen(false) }}>
        {/* A flex column, not the base grid: with only a max height, a grid
          * row grows to fit its content, so a long preview pushed the footer
          * below the dialog's cut-off edge. In a flex column the body
          * (min-h-0) shrinks and scrolls, and the header and footer keep
          * their size, so Cancel / Preview / Register stay on screen.
          * max-h-[90%] and not 90vh: under CSS zoom, vh units grow with the
          * zoom (90vh at 125% is taller than the screen), a percentage of
          * the screen does not. */}
        <DialogContent className="sm:max-w-3xl max-h-[90%] flex flex-col">
          <DialogHeader className="shrink-0">
            <DialogTitle>Register New Cluster</DialogTitle>
            <DialogDescription>
              Connection details and cluster location.
            </DialogDescription>
          </DialogHeader>
          <div data-testid="register-dialog-body" className="space-y-4 py-2 min-h-0 overflow-y-auto">
            {/* Layer 1 — Identity (V2-cleanup-88.5, shrunk to one line by
              * V2-cleanup-89.2). Purely informational: tells the user what
              * Sharko already knows about its own AWS identity instead of
              * asking them to know it. The full explainer (ARN, method,
              * "how it works") now lives on the System page — this dialog
              * only needs the one-line summary + a link there. */}
            {/* Sharko's own AWS identity only matters when Sharko makes the
              * connection. With a cluster ArgoCD already connects to, it is
              * noise (and on a laptop cluster it points at EKS). */}
            {(connManagedBy !== 'user' || showOptionalCreds) && (
              <ClusterIdentityStrip capabilities={capabilities} loading={capabilitiesLoading} />
            )}

            <div>
              <p className="text-xs font-semibold uppercase tracking-wide text-[#3a6a8a] dark:text-gray-400">
                Cluster location
              </p>
              <p className="mt-0.5 text-sm text-[#2a5a7a] dark:text-gray-500">
                You can add the connection details later, when an addon needs secrets on this cluster.
              </p>
            </div>

            {/* Connection ownership — WHO manages the ArgoCD cluster secret
              * (V2-cleanup-57.2). Asked before the credentials question
              * because it changes what the credentials are FOR: with "I
              * do", Sharko never writes the secret and the credential
              * inputs below become optional (verification only). */}
            <div>
              <label className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300">
                Connection managed by
              </label>
              <select
                value={connManagedBy}
                onChange={(e) => {
                  setConnManagedBy(e.target.value as ConnOwnership);
                  setPickedDiscovered(null);
                  userLetGoOfPickRef.current = false;
                  setDiscoveredFilter('');
                }}
                className="w-full rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100"
              >
                <option value="sharko">Sharko (default)</option>
                <option value="user">Manage a cluster ArgoCD already connects to</option>
              </select>
              <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-400">
                {CONN_OWNERSHIP_HINTS[connManagedBy]}
              </p>
            </div>

            {/* "Pick from what ArgoCD already has" (V2-cleanup-89.3). Only
              * relevant with "I do": if the user is about to type coordinates
              * for a self-managed connection by hand, but ArgoCD already
              * knows this cluster, adopting it is strictly less work — and
              * it's the same data GET /clusters already returns as
              * managed=false. Hidden entirely when there's nothing to pick
              * from (today's behavior is unchanged in that case).
              * V2-cleanup-92.1 (F13): search filter + showing X of Y count. */}
            {connManagedBy === 'user' && allDiscoveredClusters.length > 0 && (() => {
              const filteredDiscovered = allDiscoveredClusters.filter((c) => {
                const filterLower = discoveredFilter.toLowerCase();
                return (
                  c.name.toLowerCase().includes(filterLower) ||
                  (c.server_url && c.server_url.toLowerCase().includes(filterLower))
                );
              });
              return (
              <div
                data-testid="discovered-picker"
                className="rounded-lg ring-2 ring-teal-300 bg-teal-50 p-3 dark:ring-teal-800 dark:bg-teal-950/20"
              >
                <p className="text-sm font-medium text-[#0a3a5a] dark:text-gray-200">
                  Discovered clusters
                </p>
                <p className="mt-0.5 text-sm text-[#2a5a7a] dark:text-gray-500">
                  ArgoCD already knows about {allDiscoveredClusters.length} cluster{allDiscoveredClusters.length === 1 ? '' : 's'} Sharko doesn't manage yet. Pick one to adopt {allDiscoveredClusters.length === 1 ? 'it' : 'them'} instead of typing its coordinates by hand.
                </p>
                <div className="mt-2 relative">
                  <Search className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-[#3a6a8a] dark:text-gray-500" />
                  <input
                    type="text"
                    placeholder="Search by name or server URL..."
                    value={discoveredFilter}
                    onChange={(e) => setDiscoveredFilter(e.target.value)}
                    className="w-full rounded-md border border-teal-200 bg-white py-1.5 pl-8 pr-3 text-xs focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-teal-900 dark:bg-gray-900 dark:text-gray-100"
                  />
                </div>
                {filteredDiscovered.length > 0 ? (
                  <>
                    <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-500">
                      Showing {filteredDiscovered.length} of {allDiscoveredClusters.length}
                    </p>
                    <div className="mt-1 max-h-40 space-y-1 overflow-y-auto rounded-md ring-1 ring-teal-200 bg-white p-2 dark:ring-teal-900 dark:bg-gray-900">
                      {filteredDiscovered.map((cluster) => (
                        <label
                          key={cluster.name}
                          className="flex cursor-pointer items-center gap-2 rounded px-1.5 py-1 text-sm hover:bg-teal-50 dark:hover:bg-gray-800"
                        >
                          <input
                            type="radio"
                            name="discovered-cluster-pick"
                            checked={pickedDiscovered === cluster.name}
                            onChange={() => setPickedDiscovered(cluster.name)}
                            className="border-teal-400 dark:border-gray-600"
                          />
                          <span className="font-medium text-[#0a2a4a] dark:text-gray-100">{cluster.name}</span>
                          <span
                            className="ml-auto max-w-[220px] truncate font-mono text-sm text-[#3a6a8a] dark:text-gray-400"
                            title={cluster.server_url}
                          >
                            {cluster.server_url ?? '--'}
                          </span>
                        </label>
                      ))}
                    </div>
                  </>
                ) : (
                  <p className="mt-2 rounded-md bg-white px-3 py-2 text-sm text-[#3a6a8a] dark:bg-gray-900 dark:text-gray-500">
                    No clusters match your search.
                  </p>
                )}
                <div className="mt-2 flex flex-wrap items-center gap-2">
                  <button
                    type="button"
                    onClick={handleAdoptFromPicker}
                    disabled={!pickedDiscovered}
                    className="inline-flex items-center gap-1.5 rounded-md bg-teal-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-teal-700 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-teal-700 dark:hover:bg-teal-600"
                  >
                    <GitMerge className="h-3.5 w-3.5" />
                    {pickedDiscovered ? `Adopt ${pickedDiscovered}` : 'Adopt cluster'}
                  </button>
                  <button
                    type="button"
                    data-testid="picker-take-ownership"
                    onClick={handleTakeOwnershipFromPicker}
                    disabled={!pickedDiscovered}
                    className="inline-flex items-center gap-1.5 rounded-md border border-teal-600 px-3 py-1.5 text-xs font-medium text-teal-700 hover:bg-teal-100 disabled:cursor-not-allowed disabled:opacity-50 dark:border-teal-500 dark:text-teal-300 dark:hover:bg-teal-900/30"
                  >
                    <Shield className="h-3.5 w-3.5" />
                    {pickedDiscovered ? `Take ownership of ${pickedDiscovered}` : 'Take ownership'}
                  </button>
                </div>
                <p className="mt-2 text-sm text-[#2a5a7a] dark:text-gray-500">
                  Or enter details manually below for a cluster ArgoCD doesn't have yet.
                </p>
                {pickedDiscovered && (
                  <button
                    type="button"
                    data-testid="picker-clear"
                    onClick={() => {
                      userLetGoOfPickRef.current = true;
                      setPickedDiscovered(null);
                    }}
                    className="mt-1 text-sm font-medium text-teal-700 underline hover:text-teal-800 dark:text-teal-300"
                  >
                    {PICKER_ENTER_BY_HAND}
                  </button>
                )}
              </div>
              );
            })()}

            {/* Empty-state line for "I do" + nothing to adopt (V2-cleanup-89.8).
              * Maintainer walkthrough finding: with "I do" chosen and no
              * discovered clusters, the picker block above was hidden
              * entirely — indistinguishable from the feature not existing.
              * Gated on `!loading` (the page's initial GET /clusters fetch)
              * so this never flashes before clusters have actually loaded.
              * Also gated on `!argoCDUnreachable` (V2-cleanup-90.4): when
              * ArgoCD itself can't be reached, "no other clusters there to
              * adopt" is misleading — Sharko didn't check, it couldn't. The
              * ArgoCDStatusBanner already covers that case, so this line
              * stays silent rather than saying something false. */}
            {connManagedBy === 'user' && !loading && !argoCDUnreachable && availableStatus === 'ok' && allDiscoveredClusters.length === 0 && (
              <p
                data-testid="discovered-empty"
                className="text-sm text-[#2a5a7a] dark:text-gray-500"
              >
                Sharko checked ArgoCD — no other clusters there to adopt.
              </p>
            )}
            {/* The check failed: say so, never "no other clusters" (S36). */}
            {connManagedBy === 'user' && !loading && availableFailed && allDiscoveredClusters.length === 0 && (
              <p
                data-testid="discovered-check-failed"
                className="text-sm text-amber-700 dark:text-amber-400"
              >
                {AVAILABLE_CHECK_FAILED}
              </p>
            )}

            {/* Connection source — the PRIMARY Layer-2 question
              * (creds-reframe-2, reframed as "coordinates" for
              * V2-cleanup-88.5). We ask HOW Sharko should get the
              * cluster's connection credentials, and that choice drives
              * which inputs show below. Cluster platform (EKS/GKE/AKS) is
              * now implied metadata, not the gate.
              *
              * V2-cleanup-91.2 (F2): in the "I do" path with nothing to
              * adopt, credentials are OPTIONAL (connectivity test only) and
              * the selector is gated — collapsed behind an expand toggle
              * instead of rendering as a required choice under the "nothing
              * to adopt" empty-state. Credentials stay required where they
              * genuinely apply: Sharko-managed connections, or when there ARE
              * discovered clusters to adopt in the "I do" path.
              *
              * V2-cleanup-92.1 (F12): hide this entire Connection source
              * selector when adopting — the picked discovered cluster already
              * supplies its connection from ArgoCD. */}
            {(() => {
              const adoptingDiscovered = connManagedBy === 'user' && pickedDiscovered;

              if (adoptingDiscovered) {
                // Adopting an ArgoCD-known cluster — Connection + Name come
                // from the picked cluster, so hide these fields entirely.
                return null;
              }

              if (userManagedWithNothingToAdopt && !showOptionalCreds) {
                return (
                  <button
                    type="button"
                    onClick={() => setShowOptionalCreds(true)}
                    className="text-xs text-teal-600 hover:underline dark:text-teal-400"
                  >
                    + Add connection credentials (optional — used only to test connectivity)
                  </button>
                );
              }

              return (
                <div>
                  <label className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300">
                    Connection source{userManagedWithNothingToAdopt && <span className="font-normal text-[#2a5a7a] dark:text-gray-500"> (optional)</span>}
                  </label>
                  <select
                    value={credsSource}
                    onChange={(e) => setCredsSource(e.target.value as CredsSource)}
                    className="w-full rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100"
                  >
                    {/* No silent default (V2-cleanup-60.4): the placeholder is
                      * not selectable, and Preview/Register stay disabled until
                      * the user makes an explicit choice. */}
                    <option value="" disabled>Choose where this cluster's credentials come from…</option>
                    {/* Hidden when an admin has turned off allow_inline_credentials
                      * (V2-cleanup-89.6) — the server rejects it anyway, so don't
                      * offer an option that will only 403. */}
                    {allowInlineCredentials && (
                      <option value="inline-kubeconfig">Paste kubeconfig (stored)</option>
                    )}
                    <option value="secret-kubeconfig">Kubeconfig from a secrets backend</option>
                    <option value="eks-token">Amazon EKS — Use a short-lived token from AWS</option>
                  </select>
                  {/* Plain-English hint for the selected option (V2-cleanup-55.3).
                    * V2-cleanup-91.2 (F2): "Required" only fires when the selector
                    * is genuinely required (not in the "I do" + nothing-to-adopt
                    * case where credentials are optional). */}
                  <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-500">
                    {credsSource === ''
                      ? userManagedWithNothingToAdopt
                        ? 'Pick a credential source if you want to test connectivity now.'
                        : 'Required — choose one of the options in the list before registering.'
                      : CREDS_SOURCE_HINTS[credsSource]}
                  </p>
                </div>
              );
            })()}

            {/* Cluster Name is required for every provider.
              * V2-cleanup-92.1 (F12): hide when adopting a discovered cluster
              * — its name comes from the picker. */}
            {!(connManagedBy === 'user' && pickedDiscovered) && (
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
            )}

            {/* The fields for the chosen connection source sit right under the
              * choice, so the one the user has to fill is always in view. The
              * secret name is called "Secret name" for both sources. */}
            {credsSource === 'inline-kubeconfig' && (
              <>
                {/* Paste a kubeconfig — inline YAML. Optional for every
                  * connection-ownership mode (V2-cleanup-88.3/88.5 —
                  * connection credentials are lazy): you can register
                  * with connection-only info and add this later. */}
                <div>
                  <label className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300">
                    Kubeconfig{' '}
                    <span className="font-normal text-[#2a5a7a] dark:text-gray-500">(optional)</span>
                  </label>
                  <textarea
                    value={addClusterKubeconfig}
                    onChange={(e) => setAddClusterKubeconfig(e.target.value)}
                    rows={12}
                    placeholder={'apiVersion: v1\nkind: Config\nclusters:\n- name: my-cluster\n  cluster:\n    server: https://...\n    certificate-authority-data: ...\nusers:\n- name: my-user\n  user:\n    token: ...'}
                    className="w-full rounded-md border border-[#5a9dd0] px-3 py-2 font-mono text-xs focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 dark:placeholder-[#5a8aaa]"
                  />
                  <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-500">
                    Sharko reads the server address, the CA certificate, and how to sign in: a bearer token or an inline client certificate both work. For kind, make a token with <code className="font-mono">kubectl create token &lt;serviceaccount&gt; --duration=24h</code>.
                  </p>
                </div>
              </>
            )}

            {credsSource === 'secret-kubeconfig' && (
              <>
                {/* Use a stored kubeconfig — the secret name/path is the
                  * key input (promoted from the old buried "Secret Path"
                  * field to a first-class field). Optional for every
                  * connection-ownership mode (V2-cleanup-88.3/88.5). */}
                <div>
                  <label className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300">
                    Secret name{' '}
                    <span className="font-normal text-[#2a5a7a] dark:text-gray-500">(optional)</span>
                  </label>
                  <input
                    type="text"
                    value={addClusterSecretPath}
                    onChange={(e) => setAddClusterSecretPath(e.target.value)}
                    placeholder="e.g. k8s-my-cluster or secret/data/clusters/my-cluster"
                    className="w-full rounded-md border border-[#5a9dd0] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 dark:placeholder-[#5a8aaa]"
                  />
                  <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-500">
                    The secret in your configured backend holds this cluster's kubeconfig.
                  </p>
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
              </>
            )}

            {credsSource === 'eks-token' && (
              <>
                {/* Amazon EKS — generate a token from cloud identity
                  * (IRSA / role assumption). The AWS-shaped fields. */}
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
                  <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-500">
                    Cross-account override — leave empty to use the identity shown above.
                  </p>
                </div>
                <div>
                  <label className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300">
                    Secret name{' '}
                        <span className="font-normal text-[#2a5a7a] dark:text-gray-500">(optional)</span>
                  </label>
                  <input
                    type="text"
                    value={addClusterSecretPath}
                    onChange={(e) => setAddClusterSecretPath(e.target.value)}
                    placeholder="e.g. k8s-my-cluster"
                    className="w-full rounded-md border border-[#5a9dd0] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 dark:placeholder-[#5a8aaa]"
                  />
                  <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-500">
                    Leave empty to use cluster name as the secret key
                  </p>
                </div>
              </>
            )}


            {/* Auto-merge is a global setting — no per-flow checkbox. The note says what it is set to (story G2). */}
            <AutoMergeNote className="text-sm" />

            {dryRunResult && <DryRunPreview result={dryRunResult} />}

            {addClusterError != null && (
              <ErrorDetail error={addClusterError} fallbackMessage="Failed to register cluster" />
            )}
          </div>
          {/* Footer buttons. Plain `title=` tooltips (not shadcn
            * <Tooltip>) to stay consistent with the rest of this dialog
            * and avoid portal/provider plumbing for one-line hints. */}
          <DialogFooter className="shrink-0 flex-wrap gap-2">
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
                !addClusterName.trim() ||
                directRequiredMissing
              }
              title="Show the pull request and the changes it would make, without changing anything."
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
                !addClusterName.trim() ||
                directRequiredMissing
              }
              title="Open a pull request that adds this cluster. Whether it merges by itself is set in Settings → GitOps."
              className="inline-flex items-center gap-2 rounded-md bg-teal-600 px-4 py-2 text-sm font-medium text-white hover:bg-teal-700 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-teal-700 dark:hover:bg-teal-600"
            >
              {addClusterSubmitting && <Loader2 className="h-4 w-4 animate-spin" />}
              Register
            </button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Registration success banner */}
      {addClusterResultMsg && (() => {
        // Pick banner styling + copy based on the tagged message marker
        // (__merged__|<url> vs __pending__|<url>). The tag is set in
        // handleAddCluster; we strip it here so external callers never
        // see the marker characters.
        const isMergedTag = addClusterResultMsg.startsWith('__merged__|');
        const isPendingTag = addClusterResultMsg.startsWith('__pending__|');
        const taggedURL = isMergedTag
          ? addClusterResultMsg.slice('__merged__|'.length)
          : isPendingTag
            ? addClusterResultMsg.slice('__pending__|'.length)
            : '';
        const isPartial = addClusterResult?.status === 'partial' || addClusterResult?.partial;
        const partialPrUrl = isPartial
          ? addClusterResult?.git?.pr_url || addClusterResult?.pr_url || addClusterResult?.pull_request_url
          : undefined;
        // Three tones (V2-cleanup-61.3, F1a):
        //   - "warn" (amber)   — a genuine partial failure, needs attention.
        //   - "pending" (blue) — the expected "PR opened, not merged yet"
        //     state. This used to share the amber "warn" treatment, which
        //     reads as a problem even though it's the normal, in-progress
        //     outcome of a PR-only write. Per the status-vocabulary color
        //     law, "a change is in progress" is blue, not amber.
        //   - "success" (green) — the PR already merged.
        const tone: 'success' | 'warn' | 'pending' = isPartial
          ? 'warn'
          : isPendingTag || addClusterResultMsg === REGISTER_PR_NO_LINK
            ? 'pending'
            : 'success';
        const toneClasses =
          tone === 'warn'
            ? 'border border-amber-300 bg-amber-50 text-amber-800 dark:border-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
            : tone === 'pending'
              ? 'border border-blue-300 bg-blue-50 text-blue-800 dark:border-blue-700 dark:bg-blue-900/30 dark:text-blue-300'
              : 'border border-green-300 bg-green-50 text-green-800 dark:border-green-700 dark:bg-green-900/30 dark:text-green-300';
        const dismissHoverClasses =
          tone === 'warn'
            ? 'hover:bg-amber-100 dark:hover:bg-amber-800'
            : tone === 'pending'
              ? 'hover:bg-blue-100 dark:hover:bg-blue-800'
              : 'hover:bg-green-100 dark:hover:bg-green-800';
        // L8 (GitOps story on-screen): registration always lands as a PR
        // against managed-clusters.yaml — say so, as a second line under
        // the main outcome, so the existing outcome wording (asserted
        // exactly by ClustersOverview.pending.test.tsx) stays untouched.
        // Skipped for the untagged no-PR-URL fallback message, which
        // already says "pull request" and "GitOps repo" itself.
        const showGitOpsLine = !isPartial && (isPendingTag || isMergedTag || addClusterResultMsg.startsWith('http'));
        return (
        <div className={`flex items-center justify-between rounded-md px-4 py-2 text-sm ${toneClasses}`}>
          <div className="flex flex-col gap-0.5">
          <span>
            {isPartial
              ? <>{addClusterResultMsg}
                  {partialPrUrl && <>{' '}
                    <a href={partialPrUrl} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-0.5 underline font-medium hover:no-underline">
                      View PR <ExternalLink className="h-3 w-3" />
                    </a>
                  </>}
                </>
              : isPendingTag
                ? <>Cluster registration PR opened — merge to activate.{' '}
                    <a href={taggedURL} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-0.5 underline font-medium hover:no-underline">
                      View PR <ExternalLink className="h-3 w-3" />
                    </a>
                  </>
                : isMergedTag
                  ? <>Cluster registered.{' '}
                      <a href={taggedURL} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-0.5 underline font-medium hover:no-underline">
                        View PR <ExternalLink className="h-3 w-3" />
                      </a>
                    </>
                  : addClusterResultMsg.startsWith('http')
                    ? <>Cluster registered.{' '}
                        <a href={addClusterResultMsg} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-0.5 underline font-medium hover:no-underline">
                          View PR <ExternalLink className="h-3 w-3" />
                        </a>
                      </>
                    : addClusterResultMsg
            }
          </span>
          {showGitOpsLine && (
            <span className="text-xs opacity-80">
              This registration opens a pull request in your GitOps repo that adds the cluster.
            </span>
          )}
          </div>
          <button
            type="button"
            onClick={() => { setAddClusterResultMsg(null); setAddClusterResult(null); }}
            className={`ml-4 rounded p-0.5 ${dismissHoverClasses}`}
            aria-label="Dismiss"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        );
      })()}

      {/* One-time PR-model explainer (V2-cleanup-61.3, F1b) — shown the
          first time a cluster-registration PR completes; dismissing it here
          also hides it on the Addons page (shared localStorage flag). */}
      {addClusterResultMsg && <PRModelExplainer />}

      {/* Health stat cards + advanced filter bar — both hidden below the
          collapse threshold (V2-cleanup-61.3, B3): 5 stat cards + a full
          filter bar + view toggle is control overload for 1-4 clusters.
          They reappear automatically once the fleet reaches 5. */}
      {showFullControls && (
        <>
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
      <div className="rounded-lg ring-2 ring-[#6aade0] bg-[#d0e8f8] p-4 dark:ring-gray-700 dark:bg-gray-900">
        <div className="flex flex-wrap items-center gap-3">
          {/* Name search */}
          <div className="relative min-w-[200px] flex-1">
            <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[#3a6a8a]" />
            <input
              type="text"
              placeholder="Search clusters by name..."
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
              title={ARGOCD_CONN_TOOLTIP}
              className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-2 text-sm text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:hover:bg-gray-700"
            >
              ArgoCD Connection{filters.connectionTypes.length > 0 ? ` (${filters.connectionTypes.length})` : ''}
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

          {/* Sort (S1) */}
          <div className="flex items-center gap-1.5">
            <label htmlFor="cluster-sort" className="text-sm text-[#2a5a7a] dark:text-gray-400">
              Sort
            </label>
            <select
              id="cluster-sort"
              value={sortOption}
              onChange={(e) => setSortOption(e.target.value as ClusterSortOption)}
              className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-2 py-2 text-sm text-[#0a3a5a] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200"
            >
              {CLUSTER_SORT_OPTIONS.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </select>
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
        </>
      )}

      {/* Pending registration PRs. The wizard closes after submitting
          and the values-file PR is opened in Git but NOT merged; this
          surface lets the user see which clusters are mid-registration
          and links straight to the open PR. */}
      {pendingRegistrations.length > 0 && (
        <div className="space-y-3">
          <h3 className="flex items-center gap-2 text-sm font-semibold text-[#0a2a4a] dark:text-gray-200">
            <GitMerge className="h-4 w-4 text-blue-600" />
            {pendingOnlyRegisters ? 'Pending Registrations' : 'Pending Changes'}
            <span className="rounded-full bg-blue-100 px-2 py-0.5 text-xs font-medium text-blue-700 dark:bg-blue-900/30 dark:text-blue-400">
              {pendingRegistrations.length}
            </span>
            <span className="text-xs font-normal text-[#3a6a8a] dark:text-gray-500">
              {pendingOnlyRegisters
                ? '— registration PR open, will appear as managed once merged'
                : '— a pull request is open for each, the change happens once it is merged'}
            </span>
          </h3>
          <div className="overflow-x-auto rounded-xl ring-2 ring-blue-200 bg-[#f0f7ff] shadow-sm dark:ring-blue-900/40 dark:bg-gray-800">
            <table className="w-full text-left text-sm">
              <thead className="border-b border-blue-200 bg-blue-50 text-xs uppercase text-blue-700 dark:border-blue-900/40 dark:bg-blue-950/30 dark:text-blue-400">
                <tr>
                  <th className="px-6 py-3">Cluster Name</th>
                  {!pendingOnlyRegisters && <th className="px-6 py-3">Change</th>}
                  <th className="px-6 py-3">Branch</th>
                  <th className="px-6 py-3">Opened</th>
                  <th className="px-6 py-3">Action</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-blue-100 dark:divide-blue-900/40">
                {pendingRegistrations.map((p) => (
                  <tr key={`${p.cluster_name}-${p.pr_url}`}>
                    <td className="px-6 py-3 font-medium text-[#0a2a4a] dark:text-gray-100">
                      {p.cluster_name}
                    </td>
                    {!pendingOnlyRegisters && (
                      <td className="px-6 py-3 text-sm text-[#2a5a7a] dark:text-gray-400">
                        {PENDING_OPERATION_LABEL[p.operation ?? 'register'] ?? PENDING_OPERATION_OTHER}
                      </td>
                    )}
                    <td className="px-6 py-3 font-mono text-sm text-[#2a5a7a] dark:text-gray-400">
                      {p.branch || '--'}
                    </td>
                    <td className="px-6 py-3 text-sm text-[#2a5a7a] dark:text-gray-400">
                      {p.opened_at || '--'}
                    </td>
                    <td className="px-6 py-3">
                      {p.pr_url ? (
                        <a
                          href={p.pr_url}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="inline-flex items-center gap-1 rounded border border-blue-300 px-2 py-1 text-xs font-medium text-blue-700 hover:bg-blue-50 dark:border-blue-700 dark:text-blue-300 dark:hover:bg-blue-900/20"
                        >
                          <Eye className="h-3 w-3" />
                          View PR{typeof p.pr_number === 'number' && p.pr_number > 0 ? ` #${p.pr_number}` : ''}
                        </a>
                      ) : (
                        <span className="text-sm text-[#3a6a8a] dark:text-gray-500">PR URL unavailable</span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Leftovers from cancelled registrations — ArgoCD cluster Secrets
          with NO managed-clusters.yaml entry AND no open registration PR
          (typically left over when a manual-mode register PR was closed
          without merging; the orchestrator pre-creates the Secret before
          the PR opens). S4 (walk day 7, missing-truth finding b): the day-7
          body (S2) explained the deletion gate but skipped the first beat —
          WHY a secret exists in ArgoCD before the PR is even merged — and
          that confused the maintainer. The body now opens with that beat
          (register → secret created immediately + PR opens in git → PR
          closed without merging → secret stranded) before the ownership/
          not-in-git/no-open-PR gate. Per-row "Discard cancelled
          registration" button deletes the Secret via DELETE
          /api/v1/clusters/{name}/orphan. Amber/orange tint signals "needs
          cleanup attention". */}
      {orphanRegistrations.length > 0 && (
        <div className="space-y-3">
          <h3 className="flex items-center gap-2 text-sm font-semibold text-[#0a2a4a] dark:text-gray-200">
            <AlertTriangle className="h-4 w-4 text-amber-600" />
            Leftovers from cancelled registrations
            <span className="rounded-full bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-400">
              {orphanRegistrations.length}
            </span>
          </h3>
          <p className="text-sm text-[#3a6a8a] dark:text-gray-400">
            Registering a cluster creates its connection secret in ArgoCD right away — that&apos;s how Sharko tests the connection — while the PR records the cluster in Git. If that PR is closed without merging, the secret stays behind with nothing using it. Everything listed here carries Sharko&apos;s own ownership label, is not in Git, and has no open PR. Discard removes just this leftover secret.
          </p>
          <div className="overflow-x-auto rounded-xl ring-2 ring-amber-200 bg-amber-50/40 shadow-sm dark:ring-amber-900/40 dark:bg-gray-800">
            <table className="w-full text-left text-sm">
              <thead className="border-b border-amber-200 bg-amber-100/60 text-xs uppercase text-amber-800 dark:border-amber-900/40 dark:bg-amber-950/30 dark:text-amber-400">
                <tr>
                  <th className="px-6 py-3">Cluster Name</th>
                  <th className="px-6 py-3">Server URL</th>
                  <th className="px-6 py-3">Last Seen</th>
                  <th className="px-6 py-3">Action</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-amber-100 dark:divide-amber-900/40">
                {orphanRegistrations.map((o) => (
                  <tr key={`${o.cluster_name}-${o.server_url}`}>
                    <td className="px-6 py-3 font-medium text-[#0a2a4a] dark:text-gray-100">
                      {o.cluster_name}
                    </td>
                    <td className="px-6 py-3 font-mono text-sm text-[#2a5a7a] dark:text-gray-400">
                      {o.server_url || '--'}
                    </td>
                    <td className="px-6 py-3 text-sm text-[#2a5a7a] dark:text-gray-400">
                      {o.last_seen_at || '--'}
                    </td>
                    <td className="px-6 py-3">
                      <RoleGuard adminOnly>
                        <button
                          type="button"
                          onClick={() => setOrphanDeleteTarget(o.cluster_name)}
                          disabled={orphanDeleteLoading && orphanDeleteTarget === o.cluster_name}
                          className="inline-flex items-center gap-1 rounded border border-red-300 bg-white px-2 py-1 text-xs font-medium text-red-700 hover:bg-red-50 disabled:opacity-50 dark:border-red-700 dark:bg-gray-800 dark:text-red-300 dark:hover:bg-red-900/20"
                          aria-label={`Discard cancelled registration for ${o.cluster_name}`}
                        >
                          <Trash2 className="h-3 w-3" />
                          Discard cancelled registration
                        </button>
                      </RoleGuard>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Orphan delete result banner — rendered OUTSIDE the orphan
          section so it survives the refetch that empties the orphan list
          on success. Without this, the success message would unmount
          immediately when orphanRegistrations.length flips back to 0. */}
      {orphanDeleteResult && (
        <div className={`flex items-center justify-between rounded-md px-4 py-2 text-sm ${
          orphanDeleteResult.error
            ? 'border border-red-300 bg-red-50 text-red-800 dark:border-red-700 dark:bg-red-900/30 dark:text-red-300'
            : 'border border-green-300 bg-green-50 text-green-800 dark:border-green-700 dark:bg-green-900/30 dark:text-green-300'
        }`}>
          <span>{orphanDeleteResult.error || orphanDeleteResult.success}</span>
          <button
            type="button"
            onClick={() => setOrphanDeleteResult(null)}
            className="ml-3 text-xs underline opacity-80 hover:opacity-100"
          >
            Dismiss
          </button>
        </div>
      )}

      {/* Managed Clusters */}
      <div id="managed-clusters-section" ref={managedSectionRef} className="space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h3 className="flex items-center gap-2 text-sm font-semibold text-[#0a2a4a] dark:text-gray-200">
            <Server className="h-4 w-4 text-teal-600" />
            Managed Clusters
            <span className="rounded-full bg-teal-100 px-2 py-0.5 text-xs font-medium text-teal-700 dark:bg-teal-900/30 dark:text-teal-400">
              {managedClusters.length}
            </span>
          </h3>
          <div className="flex flex-wrap items-center gap-3">
            {/* S3 (walk day 7): the list's own status control, two-way
                synced with the stat cards above — reuses the same
                StatusFilter values and titles (statItems), so picking a
                status here or up in the cards always agrees with the
                other. Gated by showFullControls like the stat cards
                themselves (V2-cleanup-61.3, B3) — below the 5-cluster
                threshold there's no stat-card row to sync with either. */}
            {showFullControls && (
              <div className="flex items-center gap-1.5">
                <label htmlFor="managed-status-filter" className="text-sm text-[#2a5a7a] dark:text-gray-400">
                  Status
                </label>
                <select
                  id="managed-status-filter"
                  value={statusFilter}
                  onChange={(e) => setStatusFilter(e.target.value as StatusFilter)}
                  className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-2 py-2 text-sm text-[#0a3a5a] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200"
                >
                  {statItems.map((item) => (
                    <option key={item.key} value={item.key}>
                      {item.title}
                    </option>
                  ))}
                </select>
              </div>
            )}
            {/* S1: page-size selector — 5/10/20/50/100, default 20 for this
                page. Always visible so the control is discoverable even for
                a small list; PaginationControls itself hides once everything
                fits on one page. */}
            <PageSizeSelector
              pageSize={managedPageSize}
              onChange={setManagedPageSize}
              sizes={[5, 10, 20, 50, 100]}
            />
          </div>
        </div>

        {viewMode === 'list' ? (
          <div className="overflow-x-auto rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] shadow-sm dark:ring-gray-700 dark:bg-gray-800">
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
                {pagedManagedClusters.map((cluster) => {
                  const isInCluster = cluster.name === 'in-cluster';
                  const testResult = testResults[cluster.name];
                  // v4 walk-findings W2, item 2: same enabled-addon-count
                  // signal already used for the "Addons" column, reused so
                  // the connection pill can tell a zero-addon cluster apart
                  // from a genuinely mid-registration one.
                  const rowAddonCount = getEnabledAddonCount(cluster);
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
                        {/* ONE composite status pill per row (V2-cleanup-61.2,
                            D4). The pill shows the worst of the parts; the
                            popover breaks down ArgoCD connection, deploy
                            check, Sharko test, and connection ownership. */}
                        <ClusterStatusSummary
                          connectionStatus={cluster.connection_status}
                          connectivityStatus={cluster.connectivity_status}
                          connectivityDetail={cluster.connectivity_detail}
                          sharkoStatus={cluster.sharko_status}
                          lastTestAt={cluster.last_test_at}
                          testFailing={cluster.test_failing}
                          testErrorCode={cluster.test_error_code}
                          connectionManagedBy={cluster.connection_managed_by}
                          enabledAddonCount={rowAddonCount}
                          latestTest={latestTestOf(testResults[cluster.name], testAtRef.current[cluster.name])}
                        />
                      </td>
                      <td className="px-6 py-3 font-mono text-sm text-[#2a5a7a] dark:text-gray-400">
                        {cluster.server_version ?? '--'}
                      </td>
                      <td className="px-6 py-3 text-[#2a5a7a] dark:text-gray-400">
                        {rowAddonCount}
                      </td>
                      <td className="px-6 py-3" onClick={(e) => e.stopPropagation()}>
                        <div className="flex items-center gap-2">
                          <button
                            type="button"
                            onClick={(e) => handleTestCluster(cluster.name, e)}
                            disabled={testResult === 'testing' || !clusterTestAvailable}
                            title={!clusterTestAvailable ? TEST_BUTTON_DISABLED_TOOLTIP : SHARKO_CONN_TOOLTIP}
                            aria-label={!clusterTestAvailable ? TEST_BUTTON_DISABLED_TOOLTIP : TEST_CONNECTION_LABEL}
                            className="inline-flex items-center gap-1 rounded border border-[#5a9dd0] px-2 py-1 text-xs text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 disabled:cursor-not-allowed dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
                          >
                            {testResult === 'testing'
                              ? <Loader2 className="h-3 w-3 animate-spin" />
                              : <Wifi className="h-3 w-3" />}
                            {TEST_CONNECTION_LABEL}
                          </button>
                          {/* V2-cleanup-61.4 (G3): the disabled reason lived
                            * only in `title`/`aria-label` — invisible to
                            * touch/keyboard. V2-cleanup-65.1: when the button
                            * IS enabled, the same slot explains what it does
                            * instead, so there's always a click/focus
                            * affordance next to it. */}
                          <InfoHint
                            text={!clusterTestAvailable ? TEST_BUTTON_DISABLED_TOOLTIP : TEST_CONNECTION_HINT}
                            label={!clusterTestAvailable ? 'Why is Test connection disabled?' : 'What does Test connection do?'}
                          />
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
                          {renderTestResult(cluster.name, testResult, { showSuggestions: true })}
                        </div>
                      </td>
                    </tr>
                  );
                })}
                {managedClusters.length === 0 && (
                  <tr>
                    <td colSpan={5} className="px-6 py-8 text-center text-[#3a6a8a] dark:text-gray-500">
                      {emptyManagedMessage}
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        ) : (
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
            {pagedManagedClusters.map((cluster) => {
              const isInCluster = cluster.name === 'in-cluster';
              const addonCount = getEnabledAddonCount(cluster);
              const testResult = testResults[cluster.name];
              return (
                <div
                  key={cluster.name}
                  onClick={isInCluster ? undefined : () => navigate(`/clusters/${cluster.name}`)}
                  className={`rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 shadow-sm transition-all dark:ring-gray-700 dark:bg-gray-800 ${
                    isInCluster ? 'cursor-not-allowed opacity-70' : 'cursor-pointer hover:-translate-y-0.5 hover:shadow-md'
                  }`}
                >
                  <div className="mb-3 flex items-start justify-between">
                    <h3 className="text-sm font-bold text-[#0a2a4a] dark:text-gray-100">
                      <span className="inline-flex flex-wrap items-center gap-1.5">
                        {cluster.name}
                        {isInCluster && <Info className="h-4 w-4 text-blue-400" />}
                      </span>
                    </h3>
                    <div className="flex items-center gap-1">
                      <button
                        type="button"
                        onClick={(e) => handleTestCluster(cluster.name, e)}
                        disabled={testResult === 'testing' || !clusterTestAvailable}
                        title={!clusterTestAvailable ? TEST_BUTTON_DISABLED_TOOLTIP : SHARKO_CONN_TOOLTIP}
                        aria-label={!clusterTestAvailable ? TEST_BUTTON_DISABLED_TOOLTIP : TEST_CONNECTION_LABEL}
                        className="inline-flex items-center gap-1 rounded border border-[#5a9dd0] px-2 py-1 text-xs text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 disabled:cursor-not-allowed dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
                      >
                        {testResult === 'testing' ? <Loader2 className="h-3 w-3 animate-spin" /> : <Wifi className="h-3 w-3" />}
                        {TEST_CONNECTION_LABEL}
                      </button>
                      <InfoHint
                        text={!clusterTestAvailable ? TEST_BUTTON_DISABLED_TOOLTIP : TEST_CONNECTION_HINT}
                        label={!clusterTestAvailable ? 'Why is Test connection disabled?' : 'What does Test connection do?'}
                      />
                    </div>
                  </div>
                  {testResult && testResult !== 'testing' && (
                    <div className="mb-2">
                      {renderTestResult(cluster.name, testResult, { showSuggestions: true })}
                    </div>
                  )}
                  <div className="mb-2 flex flex-col gap-1">
                    {/* ONE composite status pill per card (V2-cleanup-61.2,
                        D4) — details live in the accessible popover. */}
                    <ClusterStatusSummary
                      connectionStatus={cluster.connection_status}
                      connectivityStatus={cluster.connectivity_status}
                      connectivityDetail={cluster.connectivity_detail}
                      sharkoStatus={cluster.sharko_status}
                      lastTestAt={cluster.last_test_at}
                      testFailing={cluster.test_failing}
                      testErrorCode={cluster.test_error_code}
                      connectionManagedBy={cluster.connection_managed_by}
                      enabledAddonCount={addonCount}
                      latestTest={latestTestOf(testResults[cluster.name], testAtRef.current[cluster.name])}
                    />
                  </div>
                  <p className="mb-2 font-mono text-sm text-[#2a5a7a] dark:text-gray-400">
                    {cluster.server_version ? withV(cluster.server_version) : '--'}
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
              <div className="col-span-full rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-8 text-center text-[#3a6a8a] dark:ring-gray-700 dark:bg-gray-800 dark:text-gray-500">
                {emptyManagedMessage}
              </div>
            )}
          </div>
        )}

        {/* S1: pagination over the loaded+filtered+sorted managed list.
            Renders nothing when everything fits on one page. This pages
            through what's already in memory — "Load more clusters" below
            (unchanged) is still how more gets fetched from the server. */}
        {managedTotalPages > 1 && (
          <div className="flex items-center justify-between gap-2 pt-1">
            <span className="text-xs text-[#3a6a8a] dark:text-gray-500">
              Page {clampedManagedPage} of {managedTotalPages}
            </span>
            <PaginationControls
              page={clampedManagedPage}
              totalPages={managedTotalPages}
              onPageChange={setManagedPage}
            />
          </div>
        )}

        {mayHaveMoreClusters && (
          <div className="flex flex-col items-center gap-2 pt-2">
            <button
              type="button"
              data-testid="clusters-load-more"
              onClick={() => void handleLoadMoreClusters()}
              disabled={loadingMore}
              className="inline-flex items-center gap-2 rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:cursor-not-allowed disabled:opacity-60 dark:ring-gray-700 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
            >
              {loadingMore ? (
                <>
                  <Loader2 className="h-4 w-4 animate-spin" />
                  Loading more clusters...
                </>
              ) : (
                <>Load more clusters ({allClusters.length}{clusterTotal !== null ? ` of ${clusterTotal}` : '+'} loaded)</>
              )}
            </button>
            {loadMoreError && (
              <p className="text-xs text-red-600 dark:text-red-400">{loadMoreError}</p>
            )}
          </div>
        )}
      </div>


      {/* Adopt Clusters Dialog */}
      <AdoptClustersDialog
        open={adoptDialogOpen}
        onClose={() => setAdoptDialogOpen(false)}
        clusters={adoptDialogClusters}
        onSuccess={handleAdoptSuccess}
        onDiagnose={(name) => { setAdoptDialogOpen(false); setDiagnoseCluster(name); }}
      />

      {/* Take ownership, from the Clusters page. Same dialog as the cluster page. */}
      {takeoverTarget && (
        <TakeoverDialog
          clusterName={takeoverTarget}
          open
          onClose={() => setTakeoverTarget(null)}
          onDone={() => void fetchData()}
        />
      )}

      {/* Un-adopt Confirmation Modal */}
      <ConfirmationModal
        open={unadoptTarget !== null}
        onClose={() => {
          setUnadoptTarget(null);
          setUnadoptPreview(null);
          setUnadoptPreviewError(null);
        }}
        onConfirm={handleUnadopt}
        title="Un-adopt Cluster"
        description={`This will remove "${unadoptTarget}" from Sharko management. The ArgoCD connection will remain, but Sharko will no longer manage addons for this cluster.`}
        confirmText="Un-adopt"
        typeToConfirm={unadoptTarget ?? ''}
        destructive
        loading={unadoptLoading}
        extraContent={
          <div className="space-y-3">
            {!unadoptPreview && !unadoptPreviewLoading && (
              <button
                type="button"
                onClick={handlePreviewUnadopt}
                className="inline-flex items-center gap-2 rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-1.5 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
              >
                <Eye className="h-4 w-4" />
                Preview changes
              </button>
            )}
            {unadoptPreviewLoading && (
              <div
                role="status"
                className="flex items-center gap-2 rounded-md ring-2 ring-[#6aade0] bg-[#f0f7ff] p-3 text-sm text-[#0a3a5a] dark:ring-gray-700 dark:bg-gray-900 dark:text-gray-300"
              >
                <Loader2 className="h-4 w-4 shrink-0 animate-spin" aria-hidden="true" />
                <span>Generating preview...</span>
              </div>
            )}
            {unadoptPreview && <DryRunPreview result={unadoptPreview} />}
            {unadoptPreviewError && (
              <div
                role="alert"
                className="flex items-start gap-2 rounded-md border border-red-300 bg-red-50 p-3 text-sm text-red-900 dark:border-red-700 dark:bg-red-950/40 dark:text-red-200"
              >
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
                <p>{unadoptPreviewError}</p>
              </div>
            )}
          </div>
        }
      />

      {/* Orphan Delete Confirmation Modal */}
      <ConfirmationModal
        open={orphanDeleteTarget !== null}
        onClose={() => setOrphanDeleteTarget(null)}
        onConfirm={handleDeleteOrphan}
        title="Discard this leftover secret?"
        description={`This removes only the leftover ArgoCD connection secret Sharko created for "${orphanDeleteTarget}" when its registration PR was opened. That PR was closed without merging, so nothing is using this secret. The cluster itself is not touched — this only cleans up the leftover secret.`}
        confirmText="Discard"
        destructive
        loading={orphanDeleteLoading}
      />

      {/* Un-adopt result: init-style lifecycle progress. */}
      {unadoptResult?.pr && (
        <div className="relative">
          <PRLifecycleProgress
            result={unadoptResult.pr}
            autoMergeExpected={(unadoptResult.pr?.merged ?? unadoptResult.pr?.result?.merged) === true}
            mergedLabel="PR merged — cluster un-adopted"
            openLabel="PR open for review — cluster is un-adopted once it merges"
          />
          <button
            type="button"
            onClick={() => setUnadoptResult(null)}
            className="absolute right-2 top-2 rounded p-0.5 hover:bg-green-100 dark:hover:bg-green-800"
            aria-label="Dismiss"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
      )}
      {unadoptResult && !unadoptResult.pr && (
        <div className={`flex items-center justify-between rounded-md px-4 py-2 text-sm ${
          unadoptResult.error
            ? 'border border-red-300 bg-red-50 text-red-800 dark:border-red-700 dark:bg-red-900/30 dark:text-red-300'
            : 'border border-green-300 bg-green-50 text-green-800 dark:border-green-700 dark:bg-green-900/30 dark:text-green-300'
        }`}>
          <span>{unadoptResult.error ? unadoptResult.error : unadoptResult.success}</span>
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

      {/* V3-D5: cluster removal PR note carried from ClusterDetail on successful removal */}
      {removalNote?.pr_url && (
        <div className="relative">
          <PRLifecycleProgress
            result={{ pr_url: removalNote.pr_url, pr_id: removalNote.pr_id, merged: removalNote.merged }}
            autoMergeExpected={removalNote.merged}
            mergedLabel={`Cluster "${removalNote.cluster}" removed`}
            openLabel={`Removal PR opened for "${removalNote.cluster}"`}
          />
          <button
            type="button"
            onClick={() => setRemovalNote(null)}
            className="absolute right-2 top-2 rounded p-0.5 hover:bg-green-100 dark:hover:bg-green-800"
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

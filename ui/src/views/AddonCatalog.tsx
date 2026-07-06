import { useState, useEffect, useMemo, useCallback } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import {
  Search,
  Filter,
  ArrowUpDown,
  ChevronDown,
  ChevronUp,
  Package,
  CheckCircle,
  XCircle,
  AlertTriangle,
  ExternalLink,
  Eye,
  GitPullRequest,
  LayoutGrid,
  LayoutList,
  Plus,
  Loader2,
  RefreshCw,
  X,
  Boxes,
  Store,
} from 'lucide-react'
import { api, addAddon, type AddAddonResponse } from '@/services/api'
import type {
  AddonCatalogItem,
  AddonCatalogResponse,
  CatalogRepoChartsResponse,
  CatalogValidateResponse,
  CatalogVersionsResponse,
  DryRunResult,
} from '@/services/models'
import { StatCard } from '@/components/StatCard'
import { StatusBadge } from '@/components/StatusBadge'
import { LoadingState } from '@/components/LoadingState'
import { ErrorState } from '@/components/ErrorState'
import { RoleGuard } from '@/components/RoleGuard'
import { MarketplaceTab } from '@/components/MarketplaceTab'
import { VersionPicker } from '@/components/VersionPicker'
import {
  DryRunPreview,
  SubmitPhaseBanner,
  type SubmitPhase,
} from '@/components/AddAddonFlow'
import { PRModelExplainer } from '@/components/PRFeedback'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'

// The tab value is `'catalog'` (not `'installed'`) because an addon listed
// in the catalog is not necessarily running on a cluster. The tile badge
// sources its copy from deployed_cluster_count /
// total_target_cluster_count (see DeploymentBadge).
type AddonsView = 'catalog' | 'marketplace'

type FilterType = 'all' | 'healthy' | 'unhealthy' | 'git-only' | 'drifted'

// True when any enabled application runs a version different from the
// catalog version. Shared by the card drift chip, the `drifted` filter,
// and the `?drift=true` deep-link from the Dashboard (V2-cleanup-61.2).
function countDriftedApps(addon: AddonCatalogItem): number {
  return addon.applications.filter(
    (a) => a.enabled && a.deployed_version && a.deployed_version !== addon.version,
  ).length
}
type SortBy = 'name' | 'applications'
type PageSize = 15 | 30 | 60

/**
 * Top-of-page tab control switching between the user's "Catalog" and the
 * curated "Marketplace" tab. Implemented as a real WAI-ARIA tablist so
 * keyboard users get arrow-key navigation for free via the browser's
 * default radio-group behaviour on the underlying buttons.
 */
function AddonsTabBar({
  tab,
  onChange,
}: {
  tab: AddonsView
  onChange: (next: AddonsView) => void
}) {
  const items: { value: AddonsView; label: string; icon: React.ReactNode }[] = [
    { value: 'catalog', label: 'Catalog', icon: <Boxes className="h-4 w-4" /> },
    { value: 'marketplace', label: 'Marketplace', icon: <Store className="h-4 w-4" /> },
  ]
  return (
    <div
      role="tablist"
      aria-label="Addons view"
      className="inline-flex w-fit gap-1 rounded-lg bg-[#d0e8f8] p-1 dark:bg-gray-900"
    >
      {items.map((item) => {
        const active = tab === item.value
        return (
          <button
            key={item.value}
            type="button"
            role="tab"
            id={`addons-tab-${item.value}`}
            aria-selected={active}
            aria-controls={`addons-panel-${item.value}`}
            tabIndex={active ? 0 : -1}
            onClick={() => onChange(item.value)}
            onKeyDown={(e) => {
              if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') {
                e.preventDefault()
                onChange(tab === 'catalog' ? 'marketplace' : 'catalog')
              }
            }}
            className={`inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-[#6aade0] ${
              active
                ? 'bg-white text-[#0a2a4a] shadow-sm dark:bg-gray-700 dark:text-white'
                : 'text-[#2a5a7a] hover:bg-[#e0f0ff] dark:text-gray-400 dark:hover:bg-gray-800'
            }`}
          >
            {item.icon}
            {item.label}
          </button>
        )
      })}
    </div>
  )
}

function HealthProgressBar({ healthy, total }: { healthy: number; total: number }) {
  if (total === 0) return null
  const pct = (healthy / total) * 100
  const barColor =
    pct === 100 ? 'bg-green-500' : pct > 50 ? 'bg-yellow-500' : 'bg-red-500'

  return (
    <div>
      <div className="h-2 w-full overflow-hidden rounded-full bg-[#c0ddf0] dark:bg-gray-700">
        <div
          className={`h-full rounded-full transition-all ${barColor}`}
          style={{ width: `${pct}%` }}
        />
      </div>
      <p className="mt-1 text-xs text-[#2a5a7a] dark:text-gray-400">
        {healthy}/{total} healthy ({Math.round(pct)}%)
      </p>
    </div>
  )
}

function StatusChip({
  label,
  count,
  color,
}: {
  label: string
  count: number
  color: 'green' | 'red' | 'yellow'
}) {
  if (count === 0) return null
  const colors = {
    green: 'bg-green-50 text-green-700 border-green-200 dark:bg-green-900/30 dark:text-green-400 dark:border-green-700',
    red: 'bg-red-50 text-red-700 border-red-200 dark:bg-red-900/30 dark:text-red-400 dark:border-red-700',
    yellow: 'bg-yellow-50 text-yellow-700 border-yellow-200 dark:bg-yellow-900/30 dark:text-yellow-400 dark:border-yellow-700',
  }
  return (
    <span
      className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-medium ${colors[color]}`}
    >
      {count} {label}
    </span>
  )
}

/**
 * Tile badge that distinguishes "in catalog" from "running on N clusters".
 * Renders one of these copies based on (deployed, target) — names follow
 * the V2-cleanup-61.2 vocabulary (one name per state, one state per name):
 *
 *   target = 0           → "Not deployed yet"        (neutral — in the catalog,
 *                                                     not enabled on any cluster;
 *                                                     benign, was "Catalog Only")
 *   deployed = 0, M > 0  → "Waiting to deploy"       (amber — enabled on clusters
 *                                                     but nothing running yet)
 *   0 < N < M            → "Running on N/M clusters" (project-blue, partial)
 *   N == M, M > 0        → "Running on N clusters"   (green, fully covered)
 *
 * Reads deployed_cluster_count + total_target_cluster_count from the addon
 * row; both default to 0 when missing, degrading gracefully to "Not
 * deployed yet" rather than crashing.
 */
function DeploymentBadge({ addon }: { addon: AddonCatalogItem }) {
  const deployed = addon.deployed_cluster_count ?? 0
  const target = addon.total_target_cluster_count ?? 0

  // Check for applications in a failing sync state — surfaces before the
  // running-count logic so operators see a warning before the count story.
  const hasSyncFailing = addon.applications.some((a) => a.enabled && a.status === 'sync_failing')
  const hasDeploying = addon.applications.some((a) => a.enabled && a.status === 'deploying')

  let label: string
  let tone: 'neutral' | 'amber' | 'blue' | 'green' | 'red'

  if (hasSyncFailing) {
    label = 'Sync failing'
    tone = 'red'
  } else if (target === 0) {
    // Benign: nothing has opted in yet. Neutral, not a warning.
    label = 'Not deployed yet'
    tone = 'neutral'
  } else if (deployed === 0 && hasDeploying) {
    // Active first rollout — no completed deploys yet but something is Deploying.
    label = 'Deploying…'
    tone = 'blue'
  } else if (deployed === 0) {
    // Clusters opted in but ArgoCD hasn't produced a synced+healthy app yet.
    label = 'Waiting to deploy'
    tone = 'amber'
  } else if (deployed === target) {
    // N == M, M > 0 — cleaner copy than "Running on N/N clusters"
    label = `Running on ${deployed} ${deployed === 1 ? 'cluster' : 'clusters'}`
    tone = 'green'
  } else {
    // 0 < N < M — partial coverage
    label = `Running on ${deployed}/${target} clusters`
    tone = 'blue'
  }

  const toneClasses: Record<typeof tone, string> = {
    neutral:
      // Neutral blue-tinted "inactive" tokens — matches the Unknown /
      // Connecting… styling elsewhere.
      'bg-[#d6eeff] text-[#1a4a6a] ring-[#a0d0f0] dark:bg-gray-700 dark:text-gray-300 dark:ring-gray-600',
    amber:
      'bg-amber-50 text-amber-700 ring-amber-200 dark:bg-amber-900/30 dark:text-amber-400 dark:ring-amber-700',
    blue:
      // Sharko project-blue tokens, matches the existing tile chrome.
      'bg-[#d0e8f8] text-[#0a3a5a] ring-[#6aade0] dark:bg-blue-900/30 dark:text-blue-300 dark:ring-blue-700',
    green:
      'bg-green-50 text-green-700 ring-green-200 dark:bg-green-900/30 dark:text-green-400 dark:ring-green-700',
    red:
      'bg-red-50 text-red-700 ring-red-200 dark:bg-red-900/30 dark:text-red-400 dark:ring-red-700',
  }

  return (
    <span
      data-testid="addon-deployment-badge"
      className={`mt-1 inline-flex w-fit items-center rounded-full px-2.5 py-0.5 text-xs font-semibold ring-1 ${toneClasses[tone]}`}
    >
      {label}
    </span>
  )
}

function AddonCard({ addon }: { addon: AddonCatalogItem }) {
  const [expanded, setExpanded] = useState(false)
  const navigate = useNavigate()

  const enabledApps = addon.applications.filter((a) => a.enabled).length
  const namespace =
    addon.applications.find((a) => a.enabled && a.namespace)?.namespace ??
    addon.namespace ??
    addon.addon_name

  const handleCardClick = (e: React.MouseEvent) => {
    if ((e.target as HTMLElement).closest('button, a')) return
    navigate(`/addons/${addon.addon_name}`)
  }

  return (
    <div
      onClick={handleCardClick}
      className="group flex cursor-pointer flex-col rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] shadow-sm transition-all duration-150 hover:-translate-y-0.5 hover:border-teal-400 hover:shadow-md dark:ring-gray-700 dark:bg-gray-800 dark:hover:border-teal-500"
    >
      <div className="flex flex-1 flex-col p-4">
        {/* Header */}
        <div className="mb-2 flex items-start justify-between">
          <div className="min-w-0 flex-1">
            <h3 className="truncate text-lg font-bold text-teal-700 dark:text-teal-400">
              {addon.addon_name}
            </h3>
            <p className="truncate text-xs text-[#2a5a7a] dark:text-gray-400">
              Namespace: {namespace}
            </p>
            <DeploymentBadge addon={addon} />
          </div>
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation()
              setExpanded((v) => !v)
            }}
            className="ml-2 shrink-0 rounded p-1 text-[#3a6a8a] hover:bg-[#d6eeff] hover:text-[#1a4a6a] dark:hover:bg-gray-700 dark:hover:text-[#5a8aaa]"
            title={expanded ? 'Collapse details' : 'Expand details'}
          >
            {expanded ? (
              <ChevronUp className="h-5 w-5" />
            ) : (
              <ChevronDown className="h-5 w-5" />
            )}
          </button>
        </div>

        <HealthProgressBar healthy={addon.healthy_applications} total={enabledApps} />

        {/* Status chips. Problem states are red (V2-cleanup-61.2, D1+D3):
            Degraded matches its red rendering everywhere else, and
            "Missing from ArgoCD" is the PROBLEM name — enabled in the
            catalog but ArgoCD has no Application for it (distinct from the
            benign "Not deployed yet"). */}
        <div className="mt-2 flex flex-wrap gap-1">
          <StatusChip label="Healthy" count={addon.healthy_applications} color="green" />
          <StatusChip label="Degraded" count={addon.degraded_applications} color="red" />
          <StatusChip label="Missing from ArgoCD" count={addon.missing_applications} color="red" />
        </div>

        {/* Version drift indicator */}
        {(() => {
          const driftCount = countDriftedApps(addon)
          if (driftCount > 0) {
            return (
              <div className="mt-2">
                <span className="inline-flex items-center gap-1 rounded-full bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-700">
                  ⚠ {driftCount} drifted
                </span>
              </div>
            )
          }
          return null
        })()}

        {/* View Details button */}
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation()
            navigate(`/addons/${addon.addon_name}`)
          }}
          className="mt-3 flex w-full items-center justify-center gap-1.5 rounded-md bg-teal-600 px-3 py-1.5 text-sm font-medium text-white transition-colors hover:bg-teal-700 dark:bg-teal-700 dark:hover:bg-teal-600"
        >
          <ExternalLink className="h-3.5 w-3.5" />
          View Details
        </button>

        {/* Expanded section */}
        {expanded && (
          <div className="mt-3 border-t border-[#6aade0] pt-3 dark:border-gray-700">
            <h4 className="mb-2 text-xs font-semibold text-[#0a3a5a] dark:text-gray-300">
              Cluster Deployments
            </h4>
            <div className="max-h-60 overflow-auto rounded border text-xs dark:border-gray-700">
              <table className="w-full">
                <thead className="sticky top-0 bg-[#d0e8f8] dark:bg-gray-900">
                  <tr>
                    <th className="px-2 py-1 text-left font-medium text-[#1a4a6a] dark:text-gray-400">
                      Cluster
                    </th>
                    <th className="px-2 py-1 text-left font-medium text-[#1a4a6a] dark:text-gray-400">
                      Env
                    </th>
                    <th className="px-2 py-1 text-left font-medium text-[#1a4a6a] dark:text-gray-400">
                      Health
                    </th>
                    <th className="px-2 py-1 text-left font-medium text-[#1a4a6a] dark:text-gray-400">
                      Version
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-[#a0d0f0] dark:divide-gray-700">
                  {addon.applications
                    .filter((a) => a.enabled)
                    .map((app) => (
                      <tr key={app.cluster_name}>
                        <td className="px-2 py-1 font-medium dark:text-gray-200">
                          {app.cluster_name}
                        </td>
                        <td className="px-2 py-1 text-[#2a5a7a] dark:text-gray-400">
                          {app.cluster_environment ?? 'unknown'}
                        </td>
                        <td className="px-2 py-1">
                          <StatusBadge status={app.health_status ?? app.status} />
                        </td>
                        <td className="px-2 py-1 font-mono dark:text-gray-300">
                          {app.deployed_version ?? app.configured_version ?? 'N/A'}
                        </td>
                      </tr>
                    ))}
                </tbody>
              </table>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

function PaginationControls({
  page,
  totalPages,
  onPageChange,
}: {
  page: number
  totalPages: number
  onPageChange: (p: number) => void
}) {
  if (totalPages <= 1) return null
  return (
    <div className="flex items-center gap-2">
      <button
        type="button"
        disabled={page <= 1}
        onClick={() => onPageChange(page - 1)}
        className="rounded border px-3 py-1 text-sm font-medium text-[#0a3a5a] transition-colors hover:bg-[#d6eeff] disabled:cursor-not-allowed disabled:opacity-40 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
      >
        Prev
      </button>
      {Array.from({ length: totalPages }, (_, i) => i + 1)
        .filter(
          (p) =>
            p === 1 ||
            p === totalPages ||
            Math.abs(p - page) <= 1,
        )
        .reduce<(number | 'ellipsis')[]>((acc, p, idx, arr) => {
          if (idx > 0 && p - (arr[idx - 1] as number) > 1) {
            acc.push('ellipsis')
          }
          acc.push(p)
          return acc
        }, [])
        .map((item, idx) =>
          item === 'ellipsis' ? (
            <span key={`e-${idx}`} className="px-1 text-[#3a6a8a]">
              ...
            </span>
          ) : (
            <button
              key={item}
              type="button"
              onClick={() => onPageChange(item)}
              className={`rounded px-3 py-1 text-sm font-medium transition-colors ${
                item === page
                  ? 'bg-teal-600 text-white'
                  : 'text-[#0a3a5a] hover:bg-[#d6eeff] dark:text-gray-300 dark:hover:bg-gray-700'
              }`}
            >
              {item}
            </button>
          ),
        )}
      <button
        type="button"
        disabled={page >= totalPages}
        onClick={() => onPageChange(page + 1)}
        className="rounded border px-3 py-1 text-sm font-medium text-[#0a3a5a] transition-colors hover:bg-[#d6eeff] disabled:cursor-not-allowed disabled:opacity-40 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
      >
        Next
      </button>
    </div>
  )
}

function AddonListTable({ addons }: { addons: AddonCatalogItem[] }) {
  const navigate = useNavigate()
  return (
    <div className="overflow-x-auto rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] shadow-sm dark:ring-gray-700 dark:bg-gray-800">
      <table className="w-full text-left text-sm">
        <thead className="border-b border-[#6aade0] bg-[#d0e8f8] text-xs uppercase text-[#2a5a7a] dark:border-gray-700 dark:bg-gray-900 dark:text-gray-400">
          <tr>
            <th className="px-6 py-3">Addon Name</th>
            <th className="px-6 py-3">Version</th>
            <th className="px-6 py-3">Deployed</th>
            <th className="px-6 py-3">Healthy</th>
            <th className="px-6 py-3">Degraded</th>
            {/* The PROBLEM column (V2-cleanup-61.2, D1): enabled in the
                catalog but ArgoCD has no Application. Was "Catalog Only",
                which collided with the benign not-deployed badge. */}
            <th className="px-6 py-3">Missing from ArgoCD</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-[#6aade0] dark:divide-gray-700">
          {addons.map((addon) => (
            <tr
              key={addon.addon_name}
              onClick={() => navigate(`/addons/${addon.addon_name}`)}
              className="cursor-pointer hover:bg-[#d6eeff] dark:hover:bg-gray-700"
            >
              <td className="px-6 py-3 font-medium text-[#0a2a4a] dark:text-gray-100">
                {addon.addon_name}
              </td>
              <td className="px-6 py-3 font-mono text-xs text-[#2a5a7a] dark:text-gray-400">
                {addon.version}
              </td>
              <td className="px-6 py-3 text-[#0a3a5a] dark:text-gray-300">
                {addon.enabled_clusters === 0 ? (
                  // Benign state (V2-cleanup-61.2, D1): in the catalog,
                  // not enabled on any cluster yet. Neutral tone.
                  <span className="inline-flex items-center rounded-full bg-[#d6eeff] px-2 py-0.5 text-xs font-medium text-[#1a4a6a] dark:bg-gray-700 dark:text-gray-300">
                    Not deployed yet
                  </span>
                ) : (
                  <span className="inline-flex items-center rounded-full bg-green-50 px-2 py-0.5 text-xs font-medium text-green-700 dark:bg-green-900/30 dark:text-green-400">
                    {addon.enabled_clusters} {addon.enabled_clusters === 1 ? 'cluster' : 'clusters'}
                  </span>
                )}
              </td>
              <td className="px-6 py-3">
                {addon.healthy_applications > 0 && (
                  <span className="inline-flex items-center rounded-full bg-green-50 px-2 py-0.5 text-xs font-medium text-green-700 dark:bg-green-900/30 dark:text-green-400">
                    {addon.healthy_applications}
                  </span>
                )}
              </td>
              <td className="px-6 py-3">
                {addon.degraded_applications > 0 && (
                  <span className="inline-flex items-center rounded-full bg-yellow-50 px-2 py-0.5 text-xs font-medium text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-400">
                    {addon.degraded_applications}
                  </span>
                )}
              </td>
              <td className="px-6 py-3">
                {addon.missing_applications > 0 && (
                  <span className="inline-flex items-center rounded-full bg-red-50 px-2 py-0.5 text-xs font-medium text-red-700 dark:bg-red-900/30 dark:text-red-400">
                    {addon.missing_applications}
                  </span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export function AddonCatalog() {
  // Tab state — kept in URL so Marketplace deep links survive a refresh.
  // Default tab is "catalog". Stale `?tab=installed` links from old
  // bookmarks are normalised to the new value via a one-shot redirect
  // effect so they don't crash and the URL stays canonical.
  // Marketplace is opt-in via ?tab=marketplace or the tab control.
  const [searchParams, setSearchParams] = useSearchParams()
  const navigate = useNavigate()
  const initialTab: AddonsView =
    searchParams.get('tab') === 'marketplace' ? 'marketplace' : 'catalog'
  const [tab, setTab] = useState<AddonsView>(initialTab)
  // Stale `?tab=installed` redirect — strip the legacy param so the URL
  // stays canonical. Runs once on mount; subsequent navigation goes through
  // switchTab below.
  useEffect(() => {
    if (searchParams.get('tab') === 'installed') {
      const params = new URLSearchParams(searchParams.toString())
      params.delete('tab')
      setSearchParams(params, { replace: true })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  const switchTab = useCallback(
    (next: AddonsView) => {
      setTab(next)
      const params = new URLSearchParams(searchParams.toString())
      if (next === 'marketplace') params.set('tab', 'marketplace')
      else params.delete('tab')
      setSearchParams(params, { replace: true })
    },
    [searchParams, setSearchParams],
  )

  const [catalogData, setCatalogData] = useState<AddonCatalogResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [isRefreshing, setIsRefreshing] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [viewMode, setViewMode] = useState<'grid' | 'list'>('grid')
  const [search, setSearch] = useState('')
  // The Dashboard's "addons with drift" button deep-links here with
  // `?drift=true` (carried through the /version-matrix redirect by 61.1's
  // RedirectPreservingQuery). Honor it: land pre-filtered on the drifted
  // addons (V2-cleanup-61.2 handover).
  const [filterType, setFilterType] = useState<FilterType>(
    searchParams.get('drift') === 'true' ? 'drifted' : 'all',
  )
  const [sortBy, setSortBy] = useState<SortBy>('name')
  const [pageSize, setPageSize] = useState<PageSize>(15)
  const [page, setPage] = useState(1)

  // Add Addon dialog state. Advanced options (sync options, ignore
  // differences, additional sources) are set on the addon's Deployment
  // Options tab after the addon exists. The dialog auto-validates the
  // repo URL and offers chart/version dropdowns.
  const [addAddonOpen, setAddAddonOpen] = useState(false)
  const [addonForm, setAddonForm] = useState({
    name: '',
    chart: '',
    repo_url: '',
    version: '',
    namespace: '',
  })
  const [addAddonSubmitting, setAddAddonSubmitting] = useState(false)
  const [addAddonError, setAddAddonError] = useState<string | null>(null)

  // V2-cleanup-15 — parity with the Marketplace add-addon flow (#397). The
  // catalog "Register addon" dialog now offers a dry-run preview,
  // branch→commit→PR→merge progress, a clickable PR link, and an HONEST
  // merged-vs-open outcome. Auto-merge is now controlled globally.
  const [addAddonDryRun, setAddAddonDryRun] = useState<DryRunResult | null>(null)
  const [addAddonPreviewing, setAddAddonPreviewing] = useState(false)
  const [addAddonPhase, setAddAddonPhase] = useState<SubmitPhase>('idle')
  const [addAddonResult, setAddAddonResult] =
    useState<AddAddonResponse | null>(null)
  // Captured at submit time so the "View addon" terminal button still knows
  // the name after the form is reset (V2-cleanup-66.1).
  const [addAddonSubmittedName, setAddAddonSubmittedName] = useState('')

  // Repo URL validation lifecycle. Debounced auto-fire on blur or after
  // 500ms of typing pause. We only set validRepo=true after a successful
  // chart-listing call so the chart dropdown can rely on it.
  const [repoValidating, setRepoValidating] = useState(false)
  const [repoValidState, setRepoValidState] = useState<'idle' | 'valid' | 'invalid'>('idle')
  const [repoValidError, setRepoValidError] = useState<string | null>(null)
  const [repoCharts, setRepoCharts] = useState<string[]>([])

  // Chart version state. Populated when the user picks (or types) a chart
  // and we successfully validate the (repo, chart) pair via /catalog/validate.
  const [chartVersionsResp, setChartVersionsResp] = useState<CatalogVersionsResponse | null>(null)
  const [chartVersionsLoading, setChartVersionsLoading] = useState(false)
  const [chartVersionsError, setChartVersionsError] = useState<string | null>(null)
  const [chartShowPrereleases, setChartShowPrereleases] = useState(false)

  // Toast notification state (shown after successful addon registration)
  const [toast, setToast] = useState<{ message: string; prUrl?: string } | null>(null)

  const fetchCatalog = useCallback((background = false) => {
    if (background) {
      setIsRefreshing(true)
    }
    return api
      .getAddonCatalog()
      .then((data) => setCatalogData(data))
      .catch((e: unknown) => {
        if (!background) {
          setError(e instanceof Error ? e.message : 'Failed to load addon catalog')
        }
      })
      .finally(() => {
        setLoading(false)
        setIsRefreshing(false)
      })
  }, [])

  const handleRefresh = useCallback(() => {
    void fetchCatalog(true)
  }, [fetchCatalog])

  useEffect(() => {
    void fetchCatalog()
  }, [fetchCatalog])

  // Auto-refresh every 60s (less critical page)
  useEffect(() => {
    const interval = setInterval(() => {
      void fetchCatalog(true)
    }, 60_000)
    return () => clearInterval(interval)
  }, [fetchCatalog])

  const resetAddonFormState = useCallback(() => {
    setAddAddonError(null)
    setAddonForm({ name: '', chart: '', repo_url: '', version: '', namespace: '' })
    setRepoValidating(false)
    setRepoValidState('idle')
    setRepoValidError(null)
    setRepoCharts([])
    setChartVersionsResp(null)
    setChartVersionsLoading(false)
    setChartVersionsError(null)
    setChartShowPrereleases(false)
    setAddAddonDryRun(null)
    setAddAddonPreviewing(false)
    setAddAddonPhase('idle')
    setAddAddonResult(null)
    setAddAddonSubmittedName('')
  }, [])

  const openAddAddon = useCallback(() => {
    setAddAddonOpen(true)
    resetAddonFormState()
  }, [resetAddonFormState])

  // Trigger repo validation. Called on blur and after a 500ms typing
  // debounce. Hits /catalog/repo-charts so we get both "is this URL OK"
  // and "what charts can I offer in the dropdown" in one round-trip.
  const validateRepoUrl = useCallback(async (rawUrl: string) => {
    const url = rawUrl.trim()
    if (!url) {
      setRepoValidState('idle')
      setRepoValidError(null)
      setRepoCharts([])
      return
    }
    if (!url.startsWith('http://') && !url.startsWith('https://')) {
      setRepoValidState('invalid')
      setRepoValidError('Repo URL must start with http:// or https://')
      setRepoCharts([])
      return
    }
    setRepoValidating(true)
    setRepoValidError(null)
    try {
      const resp: CatalogRepoChartsResponse = await api.listRepoCharts(url)
      if (resp.valid && resp.charts) {
        setRepoValidState('valid')
        setRepoCharts(resp.charts)
        setRepoValidError(null)
      } else {
        setRepoValidState('invalid')
        setRepoValidError(resp.message ?? 'Could not list charts at this URL')
        setRepoCharts([])
      }
    } catch (e: unknown) {
      setRepoValidState('invalid')
      setRepoValidError(e instanceof Error ? e.message : 'Repo validation failed')
      setRepoCharts([])
    } finally {
      setRepoValidating(false)
    }
  }, [])

  // Debounce: when the user types a new repo URL, wait 500ms before firing
  // validation. Covers paste-and-pause as well as continuous typing.
  useEffect(() => {
    if (!addAddonOpen) return
    const url = addonForm.repo_url.trim()
    if (!url) return
    const t = setTimeout(() => {
      void validateRepoUrl(url)
    }, 500)
    return () => clearTimeout(t)
  }, [addAddonOpen, addonForm.repo_url, validateRepoUrl])

  // When the chart name changes (and the repo is valid), fetch versions
  // via /catalog/validate so the version picker can populate. Also acts as
  // confirmation that the (repo, chart) pair is real.
  useEffect(() => {
    if (!addAddonOpen) return
    if (repoValidState !== 'valid') return
    const repo = addonForm.repo_url.trim()
    const chart = addonForm.chart.trim()
    if (!repo || !chart) {
      setChartVersionsResp(null)
      setChartVersionsError(null)
      return
    }
    let cancelled = false
    const t = setTimeout(async () => {
      setChartVersionsLoading(true)
      setChartVersionsError(null)
      try {
        const resp: CatalogValidateResponse = await api.validateCatalogChart(
          repo,
          chart,
        )
        if (cancelled) return
        if (resp.valid && resp.versions) {
          const versionsResp: CatalogVersionsResponse = {
            addon: chart,
            chart,
            repo: resp.repo,
            versions: resp.versions,
            latest_stable: resp.latest_stable,
            cached_at: resp.cached_at ?? new Date().toISOString(),
          }
          setChartVersionsResp(versionsResp)
          // Auto-select latest stable when no version chosen yet, so the
          // form is submittable in one step.
          if (resp.latest_stable && !addonForm.version.trim()) {
            setAddonForm((prev) => ({ ...prev, version: resp.latest_stable! }))
          }
        } else {
          setChartVersionsResp(null)
          setChartVersionsError(resp.message ?? 'Chart not found in repo')
        }
      } catch (e: unknown) {
        if (!cancelled) {
          setChartVersionsError(
            e instanceof Error ? e.message : 'Failed to load chart versions',
          )
        }
      } finally {
        if (!cancelled) setChartVersionsLoading(false)
      }
    }, 400)
    return () => {
      cancelled = true
      clearTimeout(t)
    }
    // We deliberately omit addonForm.version from deps so picking a version
    // doesn't refire the validate call.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [addAddonOpen, addonForm.chart, addonForm.repo_url, repoValidState])

  // Shared request payload for both the preview and the real submit so the
  // dry-run previews exactly what the real call will write. `source: manual`
  // marks this as the legacy direct "Register addon" path (not Marketplace).
  const buildAddRequest = useCallback(
    (dryRun: boolean) => ({
      name: addonForm.name.trim(),
      chart: addonForm.chart.trim(),
      repo_url: addonForm.repo_url.trim(),
      version: addonForm.version.trim(),
      namespace: addonForm.namespace.trim() || undefined,
      source: 'manual' as const,
      // auto_merge omitted — falls back to the global GitOps setting.
      dry_run: dryRun,
    }),
    [addonForm],
  )

  const addAddonFormValid =
    !!addonForm.name.trim() &&
    !!addonForm.chart.trim() &&
    !!addonForm.repo_url.trim() &&
    !!addonForm.version.trim() &&
    repoValidState === 'valid'

  // Editing any form field invalidates a stale dry-run preview so the operator
  // never confirms against an out-of-date file list.
  useEffect(() => {
    setAddAddonDryRun(null)
  }, [
    addonForm.name,
    addonForm.chart,
    addonForm.repo_url,
    addonForm.version,
    addonForm.namespace,
  ])

  // Preview step: dry-run the add and render the files it would write. No PR,
  // no commit. Re-runnable — the operator can tweak the form and preview again.
  const handlePreviewAddon = useCallback(async () => {
    if (!addAddonFormValid) return
    setAddAddonPreviewing(true)
    setAddAddonError(null)
    setAddAddonDryRun(null)
    try {
      const res = await addAddon(buildAddRequest(true))
      if (res.dry_run) setAddAddonDryRun(res.dry_run)
    } catch (e: unknown) {
      setAddAddonError(e instanceof Error ? e.message : 'Failed to preview')
    } finally {
      setAddAddonPreviewing(false)
    }
  }, [addAddonFormValid, buildAddRequest])

  const handleAddAddon = useCallback(async () => {
    if (!addAddonFormValid) return
    setAddAddonSubmitting(true)
    setAddAddonPhase('submitting')
    setAddAddonError(null)
    const addonName = addonForm.name.trim()
    setAddAddonSubmittedName(addonName)
    try {
      const result = await addAddon(buildAddRequest(false))
      setAddAddonResult(result)
      const prUrl = result.pr_url || result.result?.pr_url || result.pull_request_url
      const prId = result.pr_id ?? result.result?.pr_id
      const wasMerged = result.merged ?? result.result?.merged ?? false
      const label = prId ? `PR #${prId}` : 'PR'

      // Branch STRICTLY on `merged` so an open PR is never presented as
      // already-cataloged ("not really in git" was the v2.0.2 smoke-test bug):
      //   - merged === true  → the addon really landed. KEEP the dialog open
      //     (V2-cleanup-66.1) showing the lifecycle window's terminal
      //     "Merged ✓" state instead of closing instantly — the user moves
      //     on via the explicit "View addon" button. Refresh the catalog in
      //     the background now so it's ready when they do.
      //   - merged === false → a PR is awaiting review → DO NOT refresh the
      //     catalog (it isn't in git yet). Keep the dialog open showing the
      //     honest "PR open — merge to apply" banner with the clickable PR.
      if (wasMerged) {
        setAddAddonPhase('merged')
        setToast({
          message: `\`${addonName}\` added to your catalog.`,
          prUrl: prUrl || undefined,
        })
        setTimeout(() => setToast(null), 6000)
        void fetchCatalog()
      } else {
        setAddAddonPhase('opened')
        // Leave the dialog open so the SubmitResultBanner shows the clickable
        // PR. The catalog is NOT refreshed — the addon isn't in git until the
        // PR merges. A toast also points at the pending-PR dashboard.
        setToast({
          message: `${label} opened — merge it to apply. Track it on the Dashboard.`,
          prUrl: prUrl || undefined,
        })
        setTimeout(() => setToast(null), 8000)
      }
    } catch (e: unknown) {
      setAddAddonPhase('idle')
      setAddAddonError(e instanceof Error ? e.message : 'Failed to add addon')
    } finally {
      setAddAddonSubmitting(false)
    }
  }, [addAddonFormValid, buildAddRequest, addonForm, fetchCatalog])

  // Reset page on filter/search/sort/pageSize change
  useEffect(() => {
    setPage(1)
  }, [search, filterType, sortBy, pageSize])

  const filteredAddons = useMemo(() => {
    if (!catalogData) return []

    let result = catalogData.addons

    // Search
    if (search) {
      const q = search.toLowerCase()
      result = result.filter(
        (a) =>
          a.addon_name.toLowerCase().includes(q) ||
          a.chart.toLowerCase().includes(q) ||
          a.namespace?.toLowerCase().includes(q),
      )
    }

    // Filter
    if (filterType !== 'all') {
      result = result.filter((a) => {
        switch (filterType) {
          case 'healthy':
            return (
              a.enabled_clusters > 0 &&
              a.degraded_applications === 0 &&
              a.missing_applications === 0
            )
          case 'unhealthy':
            return a.degraded_applications > 0 || a.missing_applications > 0
          case 'git-only':
            return a.enabled_clusters === 0
          case 'drifted':
            return countDriftedApps(a) > 0
          default:
            return true
        }
      })
    }

    // Sort
    result = [...result].sort((a, b) => {
      if (sortBy === 'applications') return b.enabled_clusters - a.enabled_clusters
      return a.addon_name.localeCompare(b.addon_name)
    })

    return result
  }, [catalogData, search, filterType, sortBy])

  const totalPages = Math.ceil(filteredAddons.length / pageSize)
  const paginatedAddons = useMemo(() => {
    const start = (page - 1) * pageSize
    return filteredAddons.slice(start, start + pageSize)
  }, [filteredAddons, page, pageSize])

  const healthyCount = catalogData
    ? catalogData.addons.filter(
        (a) =>
          a.enabled_clusters > 0 &&
          a.degraded_applications === 0 &&
          a.missing_applications === 0,
      ).length
    : 0

  const unhealthyCount = catalogData
    ? catalogData.addons.filter(
        (a) => a.degraded_applications > 0 || a.missing_applications > 0,
      ).length
    : 0

  const handleStatFilter = (filter: FilterType) => {
    setFilterType(filterType === filter ? 'all' : filter)
  }

  // The page header + tabs render unconditionally so the Marketplace tab is
  // reachable even while the installed catalog is loading or errored.
  const renderPageHeader = () => (
    <div className="space-y-4">
      <div>
        <h2 className="text-2xl font-bold text-[#0a2a4a] dark:text-gray-100">Addons</h2>
        <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-400">
          {tab === 'catalog'
            ? 'All addons defined in your Git catalog. See deployment coverage, health, and version per addon.'
            : 'Browse Sharko\u2019s curated catalog and configure a new addon for your Git repo.'}
        </p>
      </div>
      <AddonsTabBar tab={tab} onChange={switchTab} />
    </div>
  )

  if (tab === 'marketplace') {
    return (
      <div className="space-y-6">
        {renderPageHeader()}
        <MarketplaceTab />
      </div>
    )
  }

  if (loading) {
    return (
      <div className="space-y-6">
        {renderPageHeader()}
        <LoadingState message="Loading addon catalog..." />
      </div>
    )
  }

  if (error) {
    return (
      <div className="space-y-6">
        {renderPageHeader()}
        <ErrorState message={error} />
      </div>
    )
  }

  if (!catalogData) {
    return (
      <div className="space-y-6">
        {renderPageHeader()}
        <p className="text-[#2a5a7a] dark:text-gray-400">No addon catalog data available.</p>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      {renderPageHeader()}
      {/* Toast notification */}
      {toast && (
        <div className="flex items-start justify-between gap-3 rounded-lg bg-green-50 px-4 py-3 ring-1 ring-green-300 dark:bg-green-950/30 dark:ring-green-700">
          <div className="flex items-center gap-2 min-w-0 flex-1">
            <CheckCircle className="h-4 w-4 shrink-0 text-green-600 dark:text-green-400" />
            <p className="text-sm text-green-800 dark:text-green-300 break-all">
              {toast.message}
              {toast.prUrl && (
                <>
                  {' '}
                  <a
                    href={toast.prUrl}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex items-center gap-0.5 font-medium underline hover:no-underline"
                  >
                    View PR <ExternalLink className="h-3 w-3" />
                  </a>
                </>
              )}
            </p>
          </div>
          <button
            type="button"
            onClick={() => setToast(null)}
            className="shrink-0 rounded-md p-0.5 text-green-600 hover:bg-green-100 dark:text-green-400 dark:hover:bg-green-900/40"
            aria-label="Dismiss"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
      )}

      {/* One-time PR-model explainer (V2-cleanup-61.3, F1b) — shown the
          first time an addon-add PR completes; dismissing it here also
          hides it on the Clusters page (shared localStorage flag). */}
      {toast && <PRModelExplainer />}

      {/* Action bar — refresh + Add Addon */}
      <div className="flex items-center justify-end gap-2">
        <p className="mr-auto text-xs text-[#3a6a8a] dark:text-gray-500">
          <span className="font-medium text-[#1a4a6a] dark:text-gray-300">Not deployed yet</span>{' '}
          = in your catalog, not enabled on any cluster.{' '}
          <span className="font-medium text-red-600 dark:text-red-400">Missing from ArgoCD</span>{' '}
          = enabled, but ArgoCD has no matching app — needs a look.
        </p>
        <button
          onClick={handleRefresh}
          className="rounded-md p-2 text-[#3a6a8a] hover:bg-[#d6eeff] dark:text-gray-400 dark:hover:bg-gray-700"
          title="Refresh"
        >
          <RefreshCw className={`h-4 w-4 ${isRefreshing ? 'animate-spin' : ''}`} />
        </button>
        {/* Primary CTA is the Marketplace (V2-cleanup-61.3, finding A2) — it's
            the recommended path for adding an addon. The manual chart-URL
            dialog below is the advanced/secondary path, demoted to an
            outline button; it stays fully reachable, just not the default
            affordance. */}
        <button
          type="button"
          onClick={() => switchTab('marketplace')}
          className="inline-flex shrink-0 items-center gap-2 rounded-lg bg-[#0a2a4a] px-5 py-2.5 text-sm font-semibold text-white shadow-sm hover:bg-[#0d3558] dark:bg-blue-700 dark:hover:bg-blue-600"
        >
          <Store className="h-4 w-4" />
          Browse Marketplace
        </button>
        <RoleGuard adminOnly>
          <button
            type="button"
            onClick={openAddAddon}
            title="Advanced: register a Helm chart that isn't in the Marketplace"
            className="inline-flex shrink-0 items-center gap-2 rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] px-4 py-2.5 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] dark:ring-gray-700 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
          >
            <Plus className="h-4 w-4" />
            Add addon manually
          </button>
        </RoleGuard>
      </div>

      {/* Add Addon Dialog — manual flow for charts NOT in the curated
          Marketplace. Form auto-validates the repo URL, then offers a
          chart-name dropdown and a version dropdown. No sync-wave field;
          operators set it on the addon page after creation. */}
      <Dialog
        open={addAddonOpen}
        onOpenChange={(v) => {
          if (!v) {
            setAddAddonOpen(false)
            resetAddonFormState()
          }
        }}
      >
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>Register New Addon</DialogTitle>
            <DialogDescription>
              For Helm charts <strong>not in the Marketplace</strong>. For
              curated addons, browse the{' '}
              <button
                type="button"
                onClick={() => {
                  setAddAddonOpen(false)
                  resetAddonFormState()
                  switchTab('marketplace')
                }}
                className="font-semibold text-teal-600 underline hover:no-underline dark:text-teal-400"
              >
                Marketplace tab
              </button>{' '}
              instead.
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-3 py-2">
            {/* Display name + Namespace — simple text inputs. */}
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <div>
                <label
                  htmlFor="add-addon-name"
                  className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300"
                >
                  Name <span className="text-red-500">*</span>
                </label>
                <input
                  id="add-addon-name"
                  type="text"
                  value={addonForm.name}
                  onChange={(e) =>
                    setAddonForm((prev) => ({ ...prev, name: e.target.value }))
                  }
                  placeholder="e.g. my-addon"
                  className="w-full rounded-md border border-[#5a9dd0] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 dark:placeholder-[#5a8aaa]"
                />
              </div>
              <div>
                <label
                  htmlFor="add-addon-namespace"
                  className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300"
                >
                  Namespace
                </label>
                <input
                  id="add-addon-namespace"
                  type="text"
                  value={addonForm.namespace}
                  onChange={(e) =>
                    setAddonForm((prev) => ({ ...prev, namespace: e.target.value }))
                  }
                  placeholder="optional, defaults to addon name"
                  className="w-full rounded-md border border-[#5a9dd0] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 dark:placeholder-[#5a8aaa]"
                />
              </div>
            </div>

            {/* Repo URL with auto-validation. */}
            <div>
              <label
                htmlFor="add-addon-repo"
                className="mb-1 flex items-center gap-2 text-sm font-medium text-[#0a3a5a] dark:text-gray-300"
              >
                Repo URL <span className="text-red-500">*</span>
                {repoValidating && (
                  <Loader2
                    className="h-3.5 w-3.5 animate-spin text-[#3a6a8a]"
                    aria-label="Validating repo URL"
                  />
                )}
                {repoValidState === 'valid' && !repoValidating && (
                  <span className="inline-flex items-center gap-1 rounded-full bg-green-100 px-1.5 py-0.5 text-xs font-semibold text-green-700 dark:bg-green-900/30 dark:text-green-400">
                    <CheckCircle className="h-3 w-3" aria-hidden="true" />
                    Reachable · {repoCharts.length} chart{repoCharts.length === 1 ? '' : 's'}
                  </span>
                )}
              </label>
              <input
                id="add-addon-repo"
                type="url"
                value={addonForm.repo_url}
                onChange={(e) =>
                  setAddonForm((prev) => ({ ...prev, repo_url: e.target.value }))
                }
                onBlur={() => void validateRepoUrl(addonForm.repo_url)}
                placeholder="https://helm.example.com"
                aria-invalid={repoValidState === 'invalid' || undefined}
                className="w-full rounded-md border border-[#5a9dd0] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 dark:placeholder-[#5a8aaa]"
              />
              {repoValidState === 'invalid' && repoValidError && (
                <p
                  role="alert"
                  className="mt-1 flex items-center gap-1 text-xs text-red-600 dark:text-red-400"
                >
                  <AlertTriangle className="h-3 w-3" aria-hidden="true" />
                  {repoValidError}
                </p>
              )}
            </div>

            {/* Chart name — dropdown of repo charts + free-text autocomplete. */}
            <div>
              <label
                htmlFor="add-addon-chart"
                className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300"
              >
                Chart <span className="text-red-500">*</span>
              </label>
              <input
                id="add-addon-chart"
                type="text"
                list="add-addon-chart-list"
                value={addonForm.chart}
                onChange={(e) =>
                  setAddonForm((prev) => ({ ...prev, chart: e.target.value }))
                }
                disabled={repoValidState !== 'valid'}
                placeholder={
                  repoValidState !== 'valid'
                    ? 'Validate the repo URL first'
                    : 'Pick or type a chart name'
                }
                className="w-full rounded-md border border-[#5a9dd0] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 dark:placeholder-[#5a8aaa]"
              />
              <datalist id="add-addon-chart-list">
                {repoCharts.map((c) => (
                  <option key={c} value={c} />
                ))}
              </datalist>
              {repoValidState === 'valid' && repoCharts.length > 0 && (
                <ul
                  role="listbox"
                  aria-label="Available charts in this repo"
                  className="mt-1 flex max-h-24 flex-wrap gap-1 overflow-y-auto rounded-md border border-dashed border-[#c0ddf0] bg-[#f7fbff] p-1.5 dark:border-gray-700 dark:bg-gray-900"
                >
                  {repoCharts.slice(0, 50).map((c) => {
                    const selected = c === addonForm.chart.trim()
                    return (
                      <li key={c} className="contents">
                        <button
                          type="button"
                          role="option"
                          aria-selected={selected}
                          onClick={() =>
                            setAddonForm((prev) => ({ ...prev, chart: c }))
                          }
                          className={`rounded-full px-2 py-0.5 text-xs font-mono transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-teal-500 ${
                            selected
                              ? 'bg-teal-600 text-white hover:bg-teal-700'
                              : 'bg-white text-[#0a3a5a] ring-1 ring-[#c0ddf0] hover:bg-[#d6eeff] dark:bg-gray-800 dark:text-gray-200 dark:ring-gray-700 dark:hover:bg-gray-700'
                          }`}
                        >
                          {c}
                        </button>
                      </li>
                    )
                  })}
                  {repoCharts.length > 50 && (
                    <li className="px-2 py-0.5 text-xs italic text-[#3a6a8a] dark:text-gray-500">
                      +{repoCharts.length - 50} more — type to filter
                    </li>
                  )}
                </ul>
              )}
            </div>

            {/* Version — shared VersionPicker so the UX matches the Marketplace
                Configure modal exactly. */}
            <div>
              <label
                htmlFor="add-addon-version"
                className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300"
              >
                Version <span className="text-red-500">*</span>
              </label>
              <VersionPicker
                inputId="add-addon-version"
                value={addonForm.version}
                onChange={(v) =>
                  setAddonForm((prev) => ({ ...prev, version: v }))
                }
                versionsResp={chartVersionsResp}
                loading={chartVersionsLoading}
                error={chartVersionsError}
                showPrereleases={chartShowPrereleases}
                onShowPrereleasesChange={setChartShowPrereleases}
                placeholder={
                  repoValidState !== 'valid'
                    ? 'Validate the repo URL first'
                    : !addonForm.chart.trim()
                      ? 'Select a chart first'
                      : 'e.g. 1.20.0'
                }
              />
            </div>

            {/* Note: where to set advanced options after creation. */}
            <div className="rounded-md bg-[#e8f4ff] p-3 text-xs text-[#2a5a7a] ring-1 ring-[#c0ddf0] dark:bg-gray-800 dark:text-gray-300 dark:ring-gray-700">
              After adding, advanced options like sync options, ignore
              differences, and additional sources are available on the
              addon&rsquo;s <strong>Deployment Options</strong> tab.
            </div>

            {/* Auto-merge is now a global setting — no per-flow checkbox. */}
            {!addAddonResult && (
              <p className="text-xs text-[#5a8aaa] dark:text-gray-500">
                Auto-merge follows your{' '}
                <a href="/settings?section=gitops" className="underline hover:text-[#0a2a4a] dark:hover:text-gray-300">
                  global GitOps setting
                </a>
                .
              </p>
            )}

            {/* Dry-run preview (shared AddAddonFlow render) — the files the
                real submit would write, no PR, no commit. */}
            {addAddonDryRun && !addAddonResult && (
              <DryRunPreview result={addAddonDryRun} />
            )}

            {/* PR lifecycle — init-style step list from submitting to terminal.
                SubmitPhaseBanner upgrades to PRLifecycleProgress once the
                POST resolves; SubmitResultBanner kept below as a fallback for
                callers that haven't yet migrated to the lifecycle approach. */}
            <SubmitPhaseBanner phase={addAddonPhase} result={addAddonResult} />

            {addAddonError && (
              <p className="text-sm text-red-600 dark:text-red-400">{addAddonError}</p>
            )}
          </div>
          <DialogFooter>
            {addAddonResult ? (
              // Terminal state (V2-cleanup-66.1) — keep the dialog open
              // through both outcomes and hand the user an explicit button
              // instead of an automatic jump.
              addAddonPhase === 'merged' ? (
                <>
                  <button
                    type="button"
                    onClick={() => {
                      setAddAddonOpen(false)
                      resetAddonFormState()
                    }}
                    className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
                  >
                    Close
                  </button>
                  <button
                    type="button"
                    onClick={() => resetAddonFormState()}
                    className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
                  >
                    Add another
                  </button>
                  <button
                    type="button"
                    onClick={() => {
                      setAddAddonOpen(false)
                      resetAddonFormState()
                      navigate(`/addons/${encodeURIComponent(addAddonSubmittedName)}`)
                    }}
                    className="inline-flex items-center gap-2 rounded-md bg-teal-600 px-4 py-2 text-sm font-medium text-white hover:bg-teal-700 dark:bg-teal-700 dark:hover:bg-teal-600"
                  >
                    <ExternalLink className="h-4 w-4" />
                    View addon
                  </button>
                </>
              ) : (
                // PR opened for review, auto-merge off (or timed out). The
                // catalog is NOT refreshed — the addon isn't in git yet.
                <>
                  <button
                    type="button"
                    onClick={() => {
                      setAddAddonOpen(false)
                      resetAddonFormState()
                    }}
                    className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
                  >
                    Close
                  </button>
                  <button
                    type="button"
                    onClick={() => {
                      setAddAddonOpen(false)
                      resetAddonFormState()
                      navigate('/dashboard?prs_state=pending')
                    }}
                    className="inline-flex items-center gap-2 rounded-md bg-teal-600 px-4 py-2 text-sm font-medium text-white hover:bg-teal-700 dark:bg-teal-700 dark:hover:bg-teal-600"
                  >
                    <GitPullRequest className="h-4 w-4" />
                    Track on Dashboard
                  </button>
                </>
              )
            ) : (
              <>
                <button
                  type="button"
                  onClick={() => {
                    setAddAddonOpen(false)
                    resetAddonFormState()
                  }}
                  disabled={addAddonSubmitting || addAddonPreviewing}
                  className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
                >
                  Cancel
                </button>
                <button
                  type="button"
                  onClick={handlePreviewAddon}
                  disabled={
                    !addAddonFormValid ||
                    addAddonPreviewing ||
                    addAddonSubmitting
                  }
                  title="Preview: show the PR title and the files that would be committed — without opening a PR."
                  className="inline-flex items-center gap-2 rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
                >
                  {addAddonPreviewing ? (
                    <Loader2 className="h-4 w-4 animate-spin" />
                  ) : (
                    <Eye className="h-4 w-4" />
                  )}
                  Preview
                </button>
                <button
                  type="button"
                  onClick={handleAddAddon}
                  disabled={!addAddonFormValid || addAddonSubmitting}
                  className="inline-flex items-center gap-2 rounded-md bg-teal-600 px-4 py-2 text-sm font-medium text-white hover:bg-teal-700 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-teal-700 dark:hover:bg-teal-600"
                >
                  {addAddonSubmitting && <Loader2 className="h-4 w-4 animate-spin" />}
                  Register Addon
                </button>
              </>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Summary stat cards — click to filter */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          title="All Addons"
          value={catalogData.total_addons}
          icon={<Package className="h-5 w-5" />}
          onClick={() => handleStatFilter('all')}
          selected={filterType === 'all'}
        />
        <StatCard
          title="Healthy"
          value={healthyCount}
          icon={<CheckCircle className="h-5 w-5" />}
          color="success"
          onClick={() => handleStatFilter('healthy')}
          selected={filterType === 'healthy'}
        />
        <StatCard
          title="Unhealthy"
          value={unhealthyCount}
          icon={<XCircle className="h-5 w-5" />}
          color="error"
          onClick={() => handleStatFilter('unhealthy')}
          selected={filterType === 'unhealthy'}
        />
        <StatCard
          title="Not deployed yet"
          value={catalogData.addons_only_in_git}
          icon={<Boxes className="h-5 w-5" />}
          color="default"
          onClick={() => handleStatFilter('git-only')}
          selected={filterType === 'git-only'}
          subtitle="In your catalog, not enabled on any cluster yet"
        />
      </div>

      {/* Search & filter controls */}
      <div className="flex flex-wrap items-center gap-3">
        <div className="relative flex-1" style={{ minWidth: 220, maxWidth: 350 }}>
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[#3a6a8a]" />
          <input
            type="text"
            placeholder="Search addons by name, chart, or namespace..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="w-full rounded-lg border border-[#5a9dd0] py-2 pl-10 pr-4 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:placeholder-[#5a8aaa]"
          />
        </div>

        <div className="flex items-center gap-1">
          <Filter className="h-4 w-4 text-[#3a6a8a]" />
          <select
            value={filterType}
            onChange={(e) => setFilterType(e.target.value as FilterType)}
            className="rounded-lg border border-[#5a9dd0] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200"
          >
            <option value="all">All Addons</option>
            <option value="healthy">Healthy Only</option>
            <option value="unhealthy">Not Healthy</option>
            <option value="git-only">Not deployed yet</option>
            <option value="drifted">With version drift</option>
          </select>
        </div>

        <div className="flex items-center gap-1">
          <ArrowUpDown className="h-4 w-4 text-[#3a6a8a]" />
          <select
            value={sortBy}
            onChange={(e) => setSortBy(e.target.value as SortBy)}
            className="rounded-lg border border-[#5a9dd0] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200"
          >
            <option value="name">A-Z</option>
            <option value="applications">Most Apps</option>
          </select>
        </div>

        <select
          value={pageSize}
          onChange={(e) => setPageSize(Number(e.target.value) as PageSize)}
          className="rounded-lg border border-[#5a9dd0] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200"
        >
          <option value={15}>15 per page</option>
          <option value={30}>30 per page</option>
          <option value={60}>60 per page</option>
        </select>

        {/* View mode toggle */}
        <div className="ml-auto flex items-center rounded-lg border border-[#5a9dd0] dark:border-gray-600">
          <button
            type="button"
            onClick={() => setViewMode('grid')}
            className={`rounded-l-lg p-2 ${
              viewMode === 'grid'
                ? 'bg-teal-600 text-white'
                : 'bg-[#f0f7ff] text-[#2a5a7a] hover:bg-[#d6eeff] dark:bg-gray-800 dark:text-gray-400 dark:hover:bg-gray-700'
            }`}
            aria-label="Grid view"
            title="Grid view"
          >
            <LayoutGrid className="h-4 w-4" />
          </button>
          <button
            type="button"
            onClick={() => setViewMode('list')}
            className={`rounded-r-lg p-2 ${
              viewMode === 'list'
                ? 'bg-teal-600 text-white'
                : 'bg-[#f0f7ff] text-[#2a5a7a] hover:bg-[#d6eeff] dark:bg-gray-800 dark:text-gray-400 dark:hover:bg-gray-700'
            }`}
            aria-label="List view"
            title="List view"
          >
            <LayoutList className="h-4 w-4" />
          </button>
        </div>
      </div>

      {/* Results count + top pagination */}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-sm text-[#2a5a7a] dark:text-gray-400">
          {search
            ? `Showing ${filteredAddons.length} of ${catalogData.total_addons} addons`
            : `Showing ${filteredAddons.length} addons`}
          {totalPages > 1 && (
            <span>
              {' '}
              &middot; Page {page} of {totalPages} (
              {(page - 1) * pageSize + 1}-
              {Math.min(page * pageSize, filteredAddons.length)} of{' '}
              {filteredAddons.length})
            </span>
          )}
        </p>
        <PaginationControls
          page={page}
          totalPages={totalPages}
          onPageChange={setPage}
        />
      </div>

      {/* Addon grid / list */}
      {paginatedAddons.length === 0 ? (
        <div className="rounded-lg border border-teal-200 bg-teal-50 p-6 text-center text-sm text-teal-700 dark:border-teal-700 dark:bg-teal-900/30 dark:text-teal-400">
          {search ? (
            `No addons found matching "${search}"`
          ) : (
            // V2-cleanup-61.3 (B2): the catalog empty state used to be a
            // dead end that never mentioned the Marketplace. Point to it.
            <div className="space-y-3">
              <p>Your catalog is empty — nothing has been added yet.</p>
              <button
                type="button"
                onClick={() => switchTab('marketplace')}
                className="inline-flex items-center gap-2 rounded-lg bg-[#0a2a4a] px-4 py-2 text-sm font-semibold text-white shadow-sm hover:bg-[#0d3558] dark:bg-blue-700 dark:hover:bg-blue-600"
              >
                <Store className="h-4 w-4" />
                Browse the Marketplace
              </button>
            </div>
          )}
        </div>
      ) : viewMode === 'grid' ? (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {paginatedAddons.map((addon) => (
            <AddonCard key={addon.addon_name} addon={addon} />
          ))}
        </div>
      ) : (
        <AddonListTable addons={paginatedAddons} />
      )}

      {/* Bottom pagination */}
      {totalPages > 1 && (
        <div className="flex justify-center">
          <PaginationControls
            page={page}
            totalPages={totalPages}
            onPageChange={setPage}
          />
        </div>
      )}
    </div>
  )
}
export default AddonCatalog

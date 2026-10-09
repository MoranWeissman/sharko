/**
 * Dashboard PR panel with Pending/Merged tabs. Universal pending-PR surface
 * for every PR-creating operation (register-cluster, adopt-cluster, init-
 * repo, addon-*, values-edit, ai-annotate, ai-tool-*).
 *
 * Scannability features for orgs with many in-flight PRs:
 *   - Per-row category badge (Clusters / Addons / Init / AI), colour-coded
 *   - Filter chip row above the list (rendered on both tabs)
 *   - Free-text search across title + cluster + addon + branch (client-side)
 *   - "Showing N of M PRs" + "View all on GitHub →" escape hatch when the
 *     server response is at the cap (default 100).
 *
 *   - Tabs: Pending | Merged. Selection persists in the URL via
 *     `?prs_state=pending|merged` so deep-links work.
 *   - `cluster` prop scopes the panel for ClusterDetail reuse.
 *   - WCAG 2.1 AA: tab toggle uses role=tablist + role=tab + aria-selected,
 *     keyboard arrow-keys cycle.
 */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import {
  Clock,
  ExternalLink,
  GitMerge,
  GitPullRequest,
  Loader2,
  RefreshCw,
  Search,
} from 'lucide-react'
import {
  fetchMergedPRs,
  fetchTrackedPRs,
  refreshPR,
  type MergedPRItem,
} from '@/services/api'
import { useConnectionsOptional } from '@/hooks/useConnections'
import type { TrackedPR } from '@/services/models'
import { EmptyState } from '@/components/EmptyState'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { PaginationControls, PageSizeSelector, type PageSize } from '@/components/PaginationControls'

const POLL_INTERVAL = 30_000

// PRs are fetched at most this many at a time. Mirrors the server-side
// default (prsDefaultLimit in internal/api/prs.go) so the FE never hits
// the hard cap accidentally — the cap is a safety net, the default is
// the actual UX contract.
const PR_FETCH_LIMIT = 100

// Both tabs page their already-fetched list with real page numbers (same
// PaginationControls the clusters page and Observability use), rather than
// dumping up to 100 rows on screen or growing a "Show more" list forever.
// This is purely a display slice over the list that's already in memory —
// it does NOT change PR_FETCH_LIMIT or the merged fetch's own limit (still
// 100 either way). Default page size keeps the original first-screen
// density (10 rows).
const DEFAULT_PAGE_SIZE: PageSize = 10

type TabKey = 'pending' | 'merged'
type CategoryKey = 'all' | 'clusters' | 'addons' | 'init' | 'ai'

interface CategoryBucket {
  key: CategoryKey
  label: string
  // Operation codes that map into this bucket. Empty for the "all"
  // bucket (no operation filter sent to the BE).
  operations: string[]
}

// Bucket definitions used by the filter chips. The server-side filter
// receives the operations slice as a CSV; an empty slice means "no
// filter" → all PRs.
//
// Keep in sync with the canonical Op* constants in
// internal/prtracker/types.go. Adding a new operation? Decide which
// bucket it belongs to here AND make sure the badge() function knows
// about it (otherwise it falls back to the gray default badge).
const CATEGORY_BUCKETS: CategoryBucket[] = [
  { key: 'all', label: 'All', operations: [] },
  {
    key: 'clusters',
    label: 'Clusters',
    operations: ['register-cluster', 'remove-cluster', 'update-cluster', 'adopt-cluster', 'unadopt-cluster'],
  },
  {
    key: 'addons',
    label: 'Addons',
    operations: [
      'addon-add',
      'addon-remove',
      'addon-enable',
      'addon-disable',
      'addon-configure',
      'addon-upgrade',
      'values-edit',
      'ai-annotate',
      'catalog-add',
      'catalog-add-enable',
    ],
  },
  { key: 'init', label: 'Init', operations: ['init-repo'] },
  { key: 'ai', label: 'AI', operations: ['ai-tool-enable', 'ai-tool-disable', 'ai-tool-update'] },
]

// categoryForOperation — exact bucket lookup for a PENDING PR's canonical
// `operation` code (e.g. "addon-add"). TrackedPR carries the real code, so
// this is a plain membership check against CATEGORY_BUCKETS.
function categoryForOperation(operation: string): CategoryKey | null {
  for (const bucket of CATEGORY_BUCKETS) {
    if (bucket.key === 'all') continue
    if (bucket.operations.includes(operation)) return bucket.key
  }
  return null
}

// categorizeMergedTitle — best-effort category classifier for a MERGED PR,
// mirroring the same CATEGORY_BUCKETS groups. The merged-PRs endpoint
// (GET /prs/merged, internal/api/prs.go) does return an `operation` field,
// but the backend fills it via inferOperation() — literally the title's
// FIRST WORD. Every Sharko-authored PR title is prefixed with the git
// connection's commit prefix (default "sharko:" — internal/models/connection.go),
// so that first word is "sharko:" for nearly every operation, which makes
// the field useless for bucketing here. Matching on phrases actually
// present in the title is the same best-effort trick the backend already
// plays for inferCluster/inferAddon in that file — substring matching, not
// an exact field, same honesty tradeoff, just done client-side since the
// wire doesn't carry anything more precise. A title matching nothing falls
// through to null — it only ever shows up under "All", same as an unknown
// operation code does on the Pending tab's gray badge.
function categorizeMergedTitle(title: string): CategoryKey | null {
  const t = (title || '').toLowerCase()

  // AI assistant tool ops (internal/ai/tools_write.go) use a distinct
  // "[Sharko] <Verb> ..." title shape that no other operation produces —
  // a reliable signal, checked first.
  if (t.startsWith('[sharko]')) return 'ai'

  if (t.includes('initialize repository')) return 'init'

  // Cluster ops. Check "unadopt" before "adopt" — "unadopt cluster x"
  // contains "adopt cluster" as a substring.
  if (t.includes('unadopt cluster')) return 'clusters'
  if (t.includes('adopt cluster')) return 'clusters'
  if (t.includes('register cluster')) return 'clusters'
  if (t.includes('remove cluster')) return 'clusters'
  if (t.includes('update addons for cluster')) return 'clusters'

  // Addon ops — includes values-edit, ai-annotate, and catalog adds, all
  // bucketed under "Addons" per CATEGORY_BUCKETS above.
  if (t.includes('add addon')) return 'addons'
  if (t.includes('remove addon')) return 'addons'
  if (t.includes('enable addon')) return 'addons'
  if (t.includes('disable addon')) return 'addons'
  if (t.includes('configure addon')) return 'addons'
  if (t.includes('upgrade addon')) return 'addons'
  if (t.includes('overrides on cluster')) return 'addons'
  if (t.includes('global values for')) return 'addons'
  if (t.includes('ai annotate')) return 'addons'
  if (t.includes('ai opt-out')) return 'addons'
  if (t.includes('to the catalog')) return 'addons'

  return null
}

interface OperationBadgeMeta {
  label: string
  // Tailwind background + text + dark-mode tuple. We use `inline-flex`
  // utility classes elsewhere; this is just the colour tuple.
  classes: string
}

// Per-row category badge. The colour groups operations into the same
// buckets as the filter chips so a user filtering by "Addons" sees a wall
// of teal badges and can spot the outlier (a register-cluster PR that
// snuck in via a label).
function operationBadge(op: string): OperationBadgeMeta {
  switch (op) {
    case 'register-cluster':
      return { label: 'Register cluster', classes: 'bg-blue-50 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400' }
    case 'remove-cluster':
      return { label: 'Remove cluster', classes: 'bg-blue-50 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400' }
    case 'update-cluster':
      return { label: 'Update cluster', classes: 'bg-blue-50 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400' }
    case 'adopt-cluster':
      return { label: 'Adopt cluster', classes: 'bg-blue-50 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400' }
    case 'unadopt-cluster':
      return { label: 'Unadopt cluster', classes: 'bg-blue-50 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400' }
    case 'addon-add':
      return { label: 'Add addon', classes: 'bg-teal-50 text-teal-700 dark:bg-teal-900/30 dark:text-teal-400' }
    case 'addon-remove':
      return { label: 'Remove addon', classes: 'bg-teal-50 text-teal-700 dark:bg-teal-900/30 dark:text-teal-400' }
    case 'addon-enable':
      return { label: 'Enable addon', classes: 'bg-teal-50 text-teal-700 dark:bg-teal-900/30 dark:text-teal-400' }
    case 'addon-disable':
      return { label: 'Disable addon', classes: 'bg-teal-50 text-teal-700 dark:bg-teal-900/30 dark:text-teal-400' }
    case 'addon-configure':
      return { label: 'Configure addon', classes: 'bg-teal-50 text-teal-700 dark:bg-teal-900/30 dark:text-teal-400' }
    case 'addon-upgrade':
      return { label: 'Upgrade addon', classes: 'bg-teal-50 text-teal-700 dark:bg-teal-900/30 dark:text-teal-400' }
    case 'values-edit':
      return { label: 'Values', classes: 'bg-teal-50 text-teal-700 dark:bg-teal-900/30 dark:text-teal-400' }
    case 'ai-annotate':
      return { label: 'AI annotate', classes: 'bg-teal-50 text-teal-700 dark:bg-teal-900/30 dark:text-teal-400' }
    case 'catalog-add':
      return { label: 'Catalog: add addon(s)', classes: 'bg-teal-50 text-teal-700 dark:bg-teal-900/30 dark:text-teal-400' }
    case 'catalog-add-enable':
      return { label: 'Catalog: add + enable', classes: 'bg-teal-50 text-teal-700 dark:bg-teal-900/30 dark:text-teal-400' }
    case 'init-repo':
      return { label: 'Init repo', classes: 'bg-amber-50 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400' }
    case 'ai-tool-enable':
    case 'ai-tool-disable':
    case 'ai-tool-update':
      return { label: 'AI assistant', classes: 'bg-purple-50 text-purple-700 dark:bg-purple-900/30 dark:text-purple-400' }
    default:
      // Unknown operations land in a gray bucket so the panel doesn't
      // visually break for upgrades-in-progress.
      return { label: op || 'Other', classes: 'bg-gray-100 text-gray-700 dark:bg-gray-700 dark:text-gray-300' }
  }
}

function CategoryBadge({ operation }: { operation: string }) {
  const { label, classes } = operationBadge(operation)
  return (
    <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${classes}`}>
      {label}
    </span>
  )
}

function timeAgo(timestamp?: string): string {
  if (!timestamp) return '—'
  const d = new Date(timestamp)
  if (Number.isNaN(d.getTime())) return timestamp
  const secs = Math.floor((Date.now() - d.getTime()) / 1000)
  if (secs < 60) return 'just now'
  if (secs < 3600) return `${Math.floor(secs / 60)}m ago`
  if (secs < 86400) return `${Math.floor(secs / 3600)}h ago`
  return `${Math.floor(secs / 86400)}d ago`
}

// Build a GitHub-flavoured "pulls?q=is:pr+is:open" URL from the active
// connection's git_repo_identifier (e.g. "MoranWeissman/sharko"). Only
// ever call this for a GitHub connection — see EscapeHatchLink below.
function gitHubAllPRsURL(repoIdentifier: string, state: 'open' | 'closed'): string {
  const q = state === 'open' ? 'is%3Apr+is%3Aopen' : 'is%3Apr+is%3Aclosed'
  return `https://github.com/${repoIdentifier}/pulls?q=${q}`
}

// The "view the rest" escape hatch used to always build a github.com URL,
// even for Gitea/GitLab/self-hosted connections — a link that 404s for
// every non-GitHub provider. We only know how to build a working URL for
// GitHub (the identifier is an "org/repo" pair we can point at
// github.com); for anything else, say so in plain words instead of
// guessing a URL.
function EscapeHatchLink({
  repoIdentifier,
  gitProvider,
  state,
}: {
  repoIdentifier: string | undefined
  gitProvider: string | undefined
  state: 'open' | 'closed'
}) {
  if (!repoIdentifier) return null
  if (gitProvider === 'github') {
    return (
      <a
        href={gitHubAllPRsURL(repoIdentifier, state)}
        target="_blank"
        rel="noopener noreferrer"
        className="inline-flex items-center gap-1 text-teal-600 hover:underline dark:text-teal-400"
      >
        View all on GitHub <ExternalLink className="h-3 w-3" />
      </a>
    )
  }
  return <span className="text-muted-foreground">View the rest on your Git host</span>
}

// FilterControls renders the chip row + search input shared between the
// Pending and Merged tab bodies. Keeping it in one place ensures the
// chips stay visually identical across tabs (a small but real source
// of UX friction otherwise).
//
// The whole control row is clutter on a short list — it only renders once
// the tab's (unfiltered/baseline) row count is above 10. Callers gate this
// with `showPendingFilters` / `showMergedFilters` in <PullRequestsPanel>.
//
// Both tabs show the same category chips now (S2, scale-walk): Pending
// filters server-side via the real operation code; Merged filters
// client-side via categorizeMergedTitle's best-effort title match (see that
// function's comment for why the merge endpoint's own `operation` field
// isn't usable here). `hideAiChip` drops just the AI chip when nothing in
// the current tab's fetched list matches it (S4) — the other chips always
// show, same as before.
function FilterControls({
  category,
  onCategoryChange,
  search,
  onSearchChange,
  hideAiChip = false,
}: {
  category: CategoryKey
  onCategoryChange: (next: CategoryKey) => void
  search: string
  onSearchChange: (next: string) => void
  hideAiChip?: boolean
}) {
  const visibleBuckets = CATEGORY_BUCKETS.filter((b) => !(hideAiChip && b.key === 'ai'))
  return (
    <div className="mb-3 flex flex-wrap items-center gap-2">
      <div role="group" aria-label="Filter by category" className="inline-flex flex-wrap gap-1">
        {visibleBuckets.map((b) => {
          const active = category === b.key
          const chip = (
            <button
              key={b.key}
              type="button"
              onClick={() => onCategoryChange(b.key)}
              aria-pressed={active}
              className={`rounded-full px-2.5 py-0.5 text-sm font-medium transition-colors ${
                active
                  ? 'bg-teal-600 text-white shadow-sm'
                  : 'bg-muted text-card-foreground hover:bg-muted/70'
              }`}
            >
              {b.label}
            </button>
          )
          if (b.key !== 'ai') return chip
          // AI chip carries a plain tooltip explaining what it filters to
          // (S4) — the label alone ("AI") doesn't say whose PRs these are.
          return (
            <TooltipProvider key={b.key}>
              <Tooltip>
                <TooltipTrigger asChild>{chip}</TooltipTrigger>
                <TooltipContent>
                  <p>Pull requests opened by the AI assistant</p>
                </TooltipContent>
              </Tooltip>
            </TooltipProvider>
          )
        })}
      </div>
      <div className="ml-auto flex items-center gap-1 rounded-md border border-border bg-background px-2 py-0.5 text-sm">
        <Search className="h-3 w-3 text-muted-foreground" />
        <input
          type="search"
          value={search}
          onChange={(e) => onSearchChange(e.target.value)}
          placeholder="Search title, cluster, addon…"
          aria-label="Search PRs"
          className="w-44 bg-transparent py-0.5 text-card-foreground placeholder:text-muted-foreground focus:outline-none"
        />
      </div>
    </div>
  )
}

function PendingTabBody({
  cluster,
  category,
  search,
  repoIdentifier,
  gitProvider,
  onMergeDetected,
  onBaselineChange,
  onCurrentCountChange,
}: {
  cluster?: string
  category: CategoryKey
  search: string
  repoIdentifier: string | undefined
  gitProvider: string | undefined
  onMergeDetected?: (pr: TrackedPR) => void
  /**
   * Fires only when `category === 'all'`, i.e. the true unfiltered fetch for
   * this tab — the full list, not just its count. Used to decide whether
   * the filter controls are worth showing at all (gating on the
   * currently-filtered count would let a narrow category selection hide the
   * very controls needed to get back to "all" — findings, panel lens) and
   * to know whether a category NOT currently selected has any match at all
   * (S4's AI-chip visibility when AI isn't the active category).
   */
  onBaselineChange?: (prs: TrackedPR[]) => void
  /**
   * Fires on every fetch with the CURRENTLY selected category's real count
   * (the server already filtered by operation, so this IS the live count
   * for whatever category is active — no baseline staleness). Used
   * specifically for S4's fallback: when the ACTIVE category is 'ai' and
   * its own live count drops to 0 (its last PR merged away), the baseline
   * won't refresh on its own (it only updates while category === 'all'),
   * so the fallback can't rely on it — this callback is what actually
   * detects the disappearance in time.
   */
  onCurrentCountChange?: (count: number) => void
}) {
  const [prs, setPrs] = useState<TrackedPR[]>([])
  const [serverLimit, setServerLimit] = useState<number>(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [refreshingId, setRefreshingId] = useState<number | null>(null)
  const previousStatusRef = useRef<Record<number, string>>({})
  // Real pagination over the already-fetched list — this tab's own page
  // and page-size state, independent of the Merged tab's. Resets to page 1
  // whenever the active filters (or the page size itself) change so the
  // view never stays pointed at a page from a different, differently-sized
  // filtered set.
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState<PageSize>(DEFAULT_PAGE_SIZE)
  useEffect(() => {
    setPage(1)
  }, [category, search, cluster, pageSize])

  const operationCSV = useMemo(() => {
    const bucket = CATEGORY_BUCKETS.find((b) => b.key === category)
    if (!bucket || bucket.operations.length === 0) return ''
    return bucket.operations.join(',')
  }, [category])

  const fetchPRs = useCallback(
    async (showLoading = false) => {
      try {
        if (showLoading) setLoading(true)
        setError(null)
        const filters: { cluster?: string; operation?: string; limit?: number } = {
          limit: PR_FETCH_LIMIT,
        }
        if (cluster) filters.cluster = cluster
        if (operationCSV) filters.operation = operationCSV
        const result = await fetchTrackedPRs(filters)
        const newPrs = result.prs || []
        setServerLimit(result.limit ?? 0)

        const prev = previousStatusRef.current
        for (const pr of newPrs) {
          const old = prev[pr.pr_id]
          if (old?.toLowerCase() === 'open' && pr.last_status.toLowerCase() === 'merged') {
            onMergeDetected?.(pr)
          }
        }
        const next: Record<number, string> = {}
        for (const pr of newPrs) next[pr.pr_id] = pr.last_status
        previousStatusRef.current = next

        setPrs(newPrs)
        if (category === 'all') onBaselineChange?.(newPrs)
        onCurrentCountChange?.(newPrs.length)
      } catch (e: unknown) {
        setError(e instanceof Error ? e.message : 'Failed to load tracked PRs')
      } finally {
        setLoading(false)
      }
    },
    [cluster, onMergeDetected, onBaselineChange, onCurrentCountChange, operationCSV, category],
  )

  useEffect(() => {
    void fetchPRs(true)
    const id = setInterval(() => void fetchPRs(false), POLL_INTERVAL)
    return () => clearInterval(id)
  }, [fetchPRs])

  const handleRefreshPR = async (id: number) => {
    setRefreshingId(id)
    try {
      await refreshPR(id)
      await fetchPRs(false)
    } catch {
      // silent — PR row keeps the cached status
    } finally {
      setRefreshingId(null)
    }
  }

  // Apply the client-side free-text search ON TOP of the server-side
  // category filter. Search is case-insensitive and matches anywhere in
  // title, cluster, addon, or branch.
  const visiblePrs = useMemo(() => {
    if (!search.trim()) return prs
    const needle = search.trim().toLowerCase()
    return prs.filter((pr) => {
      const haystack = `${pr.pr_title} ${pr.cluster ?? ''} ${pr.addon ?? ''} ${pr.pr_branch ?? ''}`.toLowerCase()
      return haystack.includes(needle)
    })
  }, [prs, search])

  if (loading) {
    return (
      <div className="flex items-center justify-center py-6 text-muted-foreground">
        <Loader2 className="mr-2 h-4 w-4 animate-spin" />
        <span className="text-sm">Loading PRs…</span>
      </div>
    )
  }
  if (error) {
    return <p className="text-sm text-red-600 dark:text-red-400">{error}</p>
  }
  if (prs.length === 0) {
    return (
      <EmptyState
        compact
        title="No tracked PRs"
        description={
          cluster
            ? 'No pull requests tracked for this cluster.'
            : category !== 'all'
              ? 'No pending PRs in the selected category.'
              : 'No pull requests currently being tracked.'
        }
      />
    )
  }

  // serverLimit > 0 AND prs.length === serverLimit → server is at the
  // cap; surface the escape hatch so the user can pivot to the GitHub
  // PR page for the full list.
  const atCap = serverLimit > 0 && prs.length >= serverLimit
  // Page the (already category+search filtered) list. Clamp rather than
  // trust `page` directly — a background poll that shrinks the result set
  // (or a page-size bump) can otherwise leave the view pointed at a page
  // that no longer exists.
  const totalPages = Math.max(1, Math.ceil(visiblePrs.length / pageSize))
  const clampedPage = Math.min(page, totalPages)
  const pagedPrs = visiblePrs.slice((clampedPage - 1) * pageSize, clampedPage * pageSize)

  return (
    <div className="space-y-2">
      <div className="flex items-baseline justify-between text-sm text-muted-foreground">
        <span>
          Showing {visiblePrs.length} of {prs.length} open PR{prs.length === 1 ? '' : 's'}
          {atCap ? ` (server cap)` : ''}
        </span>
        {atCap && (
          <EscapeHatchLink repoIdentifier={repoIdentifier} gitProvider={gitProvider} state="open" />
        )}
      </div>
      <div className="overflow-x-auto">
        <table className="w-full text-left text-sm">
          <thead>
            <tr className="border-b border-border">
              <th className="pb-2 pr-3 font-semibold text-card-foreground">Title</th>
              <th className="pb-2 pr-3 font-semibold text-card-foreground">Category</th>
              {!cluster && <th className="pb-2 pr-3 font-semibold text-card-foreground">Cluster</th>}
              <th className="pb-2 pr-3 font-semibold text-card-foreground">User</th>
              <th className="pb-2 pr-3 font-semibold text-card-foreground">Created</th>
              <th className="pb-2 font-semibold text-card-foreground text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            {pagedPrs.map((pr) => (
              <tr
                key={pr.pr_id}
                className="border-b border-border/60 last:border-0"
              >
                <td className="py-2 pr-3">
                  <a
                    href={pr.pr_url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="font-medium text-card-foreground hover:text-teal-600 hover:underline"
                    title={pr.pr_title}
                  >
                    {pr.pr_title.length > 50 ? pr.pr_title.slice(0, 50) + '…' : pr.pr_title}
                  </a>
                </td>
                <td className="py-2 pr-3">
                  <CategoryBadge operation={pr.operation} />
                </td>
                {!cluster && (
                  <td className="py-2 pr-3 text-muted-foreground">{pr.cluster || '—'}</td>
                )}
                <td className="py-2 pr-3 text-muted-foreground">{pr.user}</td>
                <td className="py-2 pr-3 text-muted-foreground whitespace-nowrap">
                  <span className="flex items-center gap-1">
                    <Clock className="h-3 w-3" />
                    {timeAgo(pr.created_at)}
                  </span>
                </td>
                <td className="py-2 text-right">
                  <div className="flex items-center justify-end gap-1">
                    <a
                      href={pr.pr_url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="rounded p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-card-foreground"
                      title="Open PR in browser"
                      aria-label={`Open PR #${pr.pr_id} in browser`}
                    >
                      <ExternalLink className="h-3.5 w-3.5" />
                    </a>
                    <button
                      onClick={() => void handleRefreshPR(pr.pr_id)}
                      disabled={refreshingId === pr.pr_id}
                      className="rounded p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-card-foreground disabled:opacity-50"
                      title="Refresh PR status"
                      aria-label={`Refresh status of PR #${pr.pr_id}`}
                    >
                      {refreshingId === pr.pr_id ? (
                        <Loader2 className="h-3.5 w-3.5 animate-spin" />
                      ) : (
                        <RefreshCw className="h-3.5 w-3.5" />
                      )}
                    </button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="flex flex-wrap items-center justify-between gap-2 pt-1">
        <div className="flex items-center gap-3">
          {totalPages > 1 && (
            <span className="text-xs text-muted-foreground">
              Page {clampedPage} of {totalPages}
            </span>
          )}
          <PageSizeSelector pageSize={pageSize} onChange={setPageSize} sizes={[5, 10, 20, 50, 100]} />
        </div>
        <PaginationControls page={clampedPage} totalPages={totalPages} onPageChange={setPage} />
      </div>
    </div>
  )
}

function MergedTabBody({
  cluster,
  category,
  search,
  repoIdentifier,
  gitProvider,
  onDataChange,
}: {
  cluster?: string
  category: CategoryKey
  search: string
  repoIdentifier: string | undefined
  gitProvider: string | undefined
  onDataChange?: (prs: MergedPRItem[]) => void
}) {
  const [prs, setPrs] = useState<MergedPRItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  // Same real pagination as the Pending tab — its own page and page-size
  // state, independent of the Pending tab's. Resets to page 1 on filter or
  // page-size change.
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState<PageSize>(DEFAULT_PAGE_SIZE)
  useEffect(() => {
    setPage(1)
  }, [category, search, cluster, pageSize])

  const fetchMerged = useCallback(
    async (showLoading = false) => {
      try {
        if (showLoading) setLoading(true)
        setError(null)
        const filters: { cluster?: string; limit?: number } = { limit: 100 }
        if (cluster) filters.cluster = cluster
        const res = await fetchMergedPRs(filters)
        const newPrs = res.prs || []
        setPrs(newPrs)
        onDataChange?.(newPrs)
      } catch (e: unknown) {
        setError(e instanceof Error ? e.message : 'Failed to load merged PRs')
      } finally {
        setLoading(false)
      }
    },
    [cluster, onDataChange],
  )

  useEffect(() => {
    void fetchMerged(true)
    // Less aggressive polling on merged — backend cache is 60s and the
    // data changes only when something actually merges.
    const id = setInterval(() => void fetchMerged(false), POLL_INTERVAL * 2)
    return () => clearInterval(id)
  }, [fetchMerged])

  // S2 — category filter, best-effort (see categorizeMergedTitle's comment
  // for why: the merge endpoint's own `operation` field is a near-useless
  // guess, so this classifies off the title text instead, same as
  // inferCluster/inferAddon already do server-side). Applied before the
  // free-text search, which stays unconditional — it never drops a row it
  // can't classify.
  const categoryFiltered = useMemo(() => {
    if (category === 'all') return prs
    return prs.filter((pr) => categorizeMergedTitle(pr.pr_title) === category)
  }, [prs, category])

  const visiblePrs = useMemo(() => {
    if (!search.trim()) return categoryFiltered
    const needle = search.trim().toLowerCase()
    return categoryFiltered.filter((pr) => {
      const haystack = `${pr.pr_title ?? ''} ${pr.cluster ?? ''} ${pr.addon ?? ''} ${pr.pr_branch ?? ''}`.toLowerCase()
      return haystack.includes(needle)
    })
  }, [categoryFiltered, search])

  if (loading) {
    return (
      <div className="flex items-center justify-center py-6 text-muted-foreground">
        <Loader2 className="mr-2 h-4 w-4 animate-spin" />
        <span className="text-sm">Loading merged PRs…</span>
      </div>
    )
  }
  if (error) {
    return <p className="text-sm text-red-600 dark:text-red-400">{error}</p>
  }
  if (prs.length === 0) {
    return (
      <EmptyState
        compact
        title="No merged PRs yet"
        description="Sharko-authored PRs that have been merged in your Git repo will appear here."
      />
    )
  }
  if (categoryFiltered.length === 0) {
    return (
      <EmptyState
        compact
        title="No merged PRs"
        description="No merged PRs in the selected category."
      />
    )
  }

  // Page the (already category+search filtered) list. Clamp rather than
  // trust `page` directly — same reasoning as the Pending tab above.
  const totalPages = Math.max(1, Math.ceil(visiblePrs.length / pageSize))
  const clampedPage = Math.min(page, totalPages)
  const pagedPrs = visiblePrs.slice((clampedPage - 1) * pageSize, clampedPage * pageSize)

  return (
    <div className="space-y-2">
      <div className="flex items-baseline justify-between text-sm text-muted-foreground">
        <span>
          Showing {visiblePrs.length} of {prs.length} merged PR{prs.length === 1 ? '' : 's'}
        </span>
        <EscapeHatchLink repoIdentifier={repoIdentifier} gitProvider={gitProvider} state="closed" />
      </div>
      <div className="overflow-x-auto">
        <table className="w-full text-left text-sm">
          <thead>
            <tr className="border-b border-border">
              <th className="pb-2 pr-3 font-semibold text-card-foreground">Title</th>
              {!cluster && <th className="pb-2 pr-3 font-semibold text-card-foreground">Cluster</th>}
              <th className="pb-2 pr-3 font-semibold text-card-foreground">Author</th>
              <th className="pb-2 pr-3 font-semibold text-card-foreground">Merged</th>
              <th className="pb-2 font-semibold text-card-foreground text-right">Link</th>
            </tr>
          </thead>
          <tbody>
            {pagedPrs.map((pr) => (
              <tr
                key={pr.pr_id}
                className="border-b border-border/60 last:border-0"
              >
                <td className="py-2 pr-3">
                  <a
                    href={pr.pr_url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="font-medium text-card-foreground hover:text-teal-600 hover:underline"
                    title={pr.description ? `${pr.pr_title}\n\n${pr.description}` : pr.pr_title}
                  >
                    #{pr.pr_id} {pr.pr_title.length > 60 ? pr.pr_title.slice(0, 60) + '…' : pr.pr_title}
                  </a>
                </td>
                {!cluster && (
                  <td className="py-2 pr-3 text-muted-foreground">{pr.cluster || '—'}</td>
                )}
                <td className="py-2 pr-3 text-muted-foreground">{pr.author || '—'}</td>
                <td className="py-2 pr-3 text-muted-foreground whitespace-nowrap">
                  <span className="flex items-center gap-1">
                    <GitMerge className="h-3 w-3" />
                    {timeAgo(pr.merged_at)}
                  </span>
                </td>
                <td className="py-2 text-right">
                  <a
                    href={pr.pr_url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex items-center gap-1 rounded p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-card-foreground"
                    aria-label={`Open merged PR #${pr.pr_id} in browser`}
                    title="Open in browser"
                  >
                    <ExternalLink className="h-3.5 w-3.5" />
                  </a>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="flex flex-wrap items-center justify-between gap-2 pt-1">
        <div className="flex items-center gap-3">
          {totalPages > 1 && (
            <span className="text-xs text-muted-foreground">
              Page {clampedPage} of {totalPages}
            </span>
          )}
          <PageSizeSelector pageSize={pageSize} onChange={setPageSize} sizes={[5, 10, 20, 50, 100]} />
        </div>
        <PaginationControls page={clampedPage} totalPages={totalPages} onPageChange={setPage} />
      </div>
    </div>
  )
}

export interface PullRequestsPanelProps {
  /** When set, scope the panel to this cluster (for ClusterDetail page). */
  cluster?: string
  /** Callback fired the moment we observe an Open→Merged transition. */
  onMergeDetected?: (pr: TrackedPR) => void
}

export function PullRequestsPanel({ cluster, onMergeDetected }: PullRequestsPanelProps) {
  const [searchParams, setSearchParams] = useSearchParams()
  // Determine initial tab: honor explicit URL selection, otherwise default to pending
  const explicitUrlSelection = searchParams.has('prs_state')
  const initial: TabKey = searchParams.get('prs_state') === 'merged' ? 'merged' : 'pending'
  const [tab, setTab] = useState<TabKey>(initial)
  const [category, setCategory] = useState<CategoryKey>('all')
  const [search, setSearch] = useState('')
  // Track whether we've applied the auto-default logic (to avoid loops)
  const [autoDefaultApplied, setAutoDefaultApplied] = useState(false)
  // Baseline (category==='all') fetches per tab — the FULL list, not just
  // a count. null = not loaded yet, so anything gated on these stays
  // hidden/inactive until we actually know. Two things read off these:
  //   - showPendingFilters / showMergedFilters (panel-lens finding: a
  //     short list doesn't need a filter bar) — gating on the baseline
  //     count, never the currently-filtered count, so picking a narrow
  //     category can't hide the controls needed to get back to "all"
  //     (S2 fix #3).
  //   - whether the AI chip has anything to show (S4) — computed from
  //     whichever tab is active below.
  // Pending's baseline only updates while category === 'all' (see
  // PendingTabBody's onBaselineChange); Merged's list IS always the
  // baseline (the merge endpoint has no server-side operation filter).
  const [pendingBaselinePRs, setPendingBaselinePRs] = useState<TrackedPR[] | null>(null)
  const [mergedPRs, setMergedPRs] = useState<MergedPRItem[] | null>(null)
  // The Pending tab's CURRENTLY selected category's live count (S4
  // fallback) — see PendingTabBody's onCurrentCountChange doc comment for
  // why this exists separately from pendingBaselinePRs: while category is
  // 'ai', the baseline (category==='all' only) goes stale and can't tell
  // us the AI PR count dropped to zero; this always reflects the live,
  // currently-active-category count instead.
  const [pendingCurrentCount, setPendingCurrentCount] = useState<number | null>(null)
  // Optional context — the panel renders fine without a connection
  // provider (e.g. in unit tests that mount it under MemoryRouter only).
  const connCtx = useConnectionsOptional()

  // Pick the active connection's git_repo_identifier + provider for the
  // "view the rest" escape hatch. When no active connection (test mode /
  // first-run), the link is hidden.
  const activeConn = useMemo(() => {
    if (!connCtx) return undefined
    return connCtx.connections.find((c) => c.name === connCtx.activeConnection)
  }, [connCtx])
  const repoIdentifier = activeConn?.git_repo_identifier
  const gitProvider = activeConn?.git_provider

  const pendingBaseline = pendingBaselinePRs?.length ?? null
  const mergedCount = mergedPRs?.length ?? null
  const showPendingFilters = (pendingBaseline ?? 0) > 10
  const showMergedFilters = (mergedCount ?? 0) > 10

  // S4 — whether the AI chip has anything to show. Merged is always
  // computed off its baseline (categorizeMergedTitle's best-effort title
  // match) since that list is never category-filtered server-side, so it's
  // always live regardless of which category is selected. Pending splits
  // in two:
  //   - category === 'ai' already active: use the LIVE current-category
  //     count (onCurrentCountChange) — the server already filtered to
  //     exactly the AI bucket, so this is the real-time answer, and it's
  //     the one that matters for detecting "the last AI PR just merged
  //     away" (the whole point of the S4 fallback below).
  //   - any other category active: use the baseline (real operation codes)
  //     to decide whether the chip is even worth showing in the first
  //     place — the current fetch is filtered to a DIFFERENT category, so
  //     it can't answer "does AI have anything" at all.
  const activeHasAI = useMemo(() => {
    if (tab === 'pending') {
      if (category === 'ai') return (pendingCurrentCount ?? 0) > 0
      return (pendingBaselinePRs ?? []).some((pr) => categoryForOperation(pr.operation) === 'ai')
    }
    return (mergedPRs ?? []).some((pr) => categorizeMergedTitle(pr.pr_title) === 'ai')
  }, [tab, category, pendingCurrentCount, pendingBaselinePRs, mergedPRs])

  // S4 — if the selected category is AI and it's no longer real for the
  // active tab (its last matching PR merged/disappeared), fall back to
  // "all" gracefully rather than showing a hidden chip as "selected".
  useEffect(() => {
    if (category === 'ai' && !activeHasAI) {
      setCategory('all')
    }
  }, [category, activeHasAI])

  // Auto-default to Merged when pending=0 and no explicit selection. Keys
  // ONLY on the baseline (category === 'all') pending count — S2 fix #1.
  // A category-filtered zero (e.g. "AI" selected with nothing pending in
  // that bucket) must never flip the tab; only "there is truly nothing
  // pending" should.
  useEffect(() => {
    if (pendingBaseline === 0 && !explicitUrlSelection && !autoDefaultApplied && tab === 'pending') {
      setTab('merged')
      setAutoDefaultApplied(true)
    }
  }, [pendingBaseline, explicitUrlSelection, autoDefaultApplied, tab])

  // Keep ?prs_state= in sync when the user clicks. We use replace so back
  // navigation goes to the previous page rather than the previous tab.
  const switchTab = useCallback(
    (next: TabKey) => {
      setTab(next)
      // Mark that an explicit selection has been made
      setAutoDefaultApplied(true)
      setSearchParams(
        (prev) => {
          const np = new URLSearchParams(prev)
          if (next === 'merged') np.set('prs_state', 'merged')
          else np.delete('prs_state')
          return np
        },
        { replace: true },
      )
    },
    [setSearchParams],
  )

  // Keyboard navigation: arrow keys cycle tabs (WCAG 2.1 AA expectation
  // for role=tablist).
  const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') {
      e.preventDefault()
      switchTab(tab === 'pending' ? 'merged' : 'pending')
    }
  }

  return (
    <div className="rounded-xl border border-border bg-card p-5 shadow-sm">
      <div className="mb-3 flex items-center justify-between">
        <div className="flex items-center gap-2">
          <GitPullRequest className="h-4 w-4 text-teal-500" />
          <h3 className="section-heading">
            {cluster ? 'Cluster PRs' : 'Pull Requests'}
          </h3>
        </div>
        <div
          role="tablist"
          aria-label="PR state filter"
          onKeyDown={onKeyDown}
          className="inline-flex rounded-lg border border-border bg-background p-0.5 text-sm"
        >
          <button
            role="tab"
            aria-selected={tab === 'pending'}
            tabIndex={tab === 'pending' ? 0 : -1}
            onClick={() => switchTab('pending')}
            className={`rounded px-3 py-1 font-medium transition-colors ${
              tab === 'pending'
                ? 'bg-teal-600 text-white shadow-sm'
                : 'text-card-foreground hover:bg-muted'
            }`}
          >
            Pending
          </button>
          <button
            role="tab"
            aria-selected={tab === 'merged'}
            tabIndex={tab === 'merged' ? 0 : -1}
            onClick={() => switchTab('merged')}
            className={`rounded px-3 py-1 font-medium transition-colors ${
              tab === 'merged'
                ? 'bg-teal-600 text-white shadow-sm'
                : 'text-card-foreground hover:bg-muted'
            }`}
          >
            Merged
          </button>
        </div>
      </div>

      {tab === 'pending' && showPendingFilters && (
        <FilterControls
          category={category}
          onCategoryChange={setCategory}
          search={search}
          onSearchChange={setSearch}
          hideAiChip={!activeHasAI}
        />
      )}
      {tab === 'merged' && showMergedFilters && (
        <FilterControls
          category={category}
          onCategoryChange={setCategory}
          search={search}
          onSearchChange={setSearch}
          hideAiChip={!activeHasAI}
        />
      )}

      <div role="tabpanel" aria-label={tab === 'pending' ? 'Pending PRs' : 'Merged PRs'}>
        {tab === 'pending' ? (
          <PendingTabBody
            cluster={cluster}
            category={category}
            search={showPendingFilters ? search : ''}
            repoIdentifier={repoIdentifier}
            gitProvider={gitProvider}
            onMergeDetected={onMergeDetected}
            onBaselineChange={setPendingBaselinePRs}
            onCurrentCountChange={setPendingCurrentCount}
          />
        ) : (
          <MergedTabBody
            cluster={cluster}
            category={category}
            search={showMergedFilters ? search : ''}
            repoIdentifier={repoIdentifier}
            gitProvider={gitProvider}
            onDataChange={setMergedPRs}
          />
        )}
      </div>
    </div>
  )
}

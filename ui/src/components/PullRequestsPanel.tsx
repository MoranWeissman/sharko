/**
 * PullRequestsPanel — dashboard PR panel with Pending/Merged tabs.
 *
 * Maintainer feedback (v1.21 Bundle 3, item 5):
 *   "in dashboard we have 'Pending PRs' but i want also to be able to switch
 *    from there to see merged PRs with the PR description/user and etc and
 *    a link to github"
 *
 * V125-1-6 (BUG-056/057):
 *   The dashboard PR panel previously only saw addon-from-UI PRs because
 *   only those write-paths called TrackPR. The orchestrator funnel now
 *   tracks every PR-creating operation (register-cluster, adopt-cluster,
 *   init-repo, addon-*, values-edit, ai-annotate, ai-tool-*), so the
 *   panel is the universal pending-PR surface for the org.
 *
 *   To keep the panel scannable when an org has many in-flight PRs:
 *     - Per-row category badge (Clusters / Addons / Init / AI) — colour-
 *       coded so the eye picks the relevant rows out of a list of dozens.
 *     - Filter chip row above the list (also rendered on the Merged tab)
 *       so the user can narrow by operation category.
 *     - Free-text search across title + cluster + addon + branch
 *       (client-side over the already-filtered server response).
 *     - "Showing N of M PRs" count + "View all on GitHub →" escape hatch
 *       when the server response is at the limit cap (default 100).
 *
 * Design carried over from V121:
 *   - Tabs: Pending | Merged. Selection persists in the URL via
 *     `?prs_state=pending|merged` so deep-links work.
 *   - Cluster prop scopes the panel for ClusterDetail reuse.
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

const POLL_INTERVAL = 30_000

// PRs are fetched at most this many at a time. Mirrors the server-side
// default (prsDefaultLimit in internal/api/prs.go) so the FE never hits
// the hard cap accidentally — the cap is a safety net, the default is
// the actual UX contract.
const PR_FETCH_LIMIT = 100

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
    ],
  },
  { key: 'init', label: 'Init', operations: ['init-repo'] },
  { key: 'ai', label: 'AI', operations: ['ai-tool-enable', 'ai-tool-disable', 'ai-tool-update'] },
]

interface OperationBadgeMeta {
  label: string
  // Tailwind background + text + dark-mode tuple. We use `inline-flex`
  // utility classes elsewhere; this is just the colour tuple.
  classes: string
}

// V125-1-6: per-row category badge. The colour groups operations into
// the same buckets as the filter chips so a user filtering by "Addons"
// sees a wall of teal badges and can spot the outlier (a register-
// cluster PR that snuck in via a label).
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
    case 'init-repo':
      return { label: 'Init repo', classes: 'bg-amber-50 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400' }
    case 'ai-tool-enable':
    case 'ai-tool-disable':
    case 'ai-tool-update':
      return { label: 'AI assistant', classes: 'bg-purple-50 text-purple-700 dark:bg-purple-900/30 dark:text-purple-400' }
    default:
      // Unknown / pre-V125-1-6 operations land in a gray bucket so the
      // panel doesn't visually break for upgrades-in-progress.
      return { label: op || 'Other', classes: 'bg-gray-100 text-gray-700 dark:bg-gray-700 dark:text-gray-300' }
  }
}

function CategoryBadge({ operation }: { operation: string }) {
  const { label, classes } = operationBadge(operation)
  return (
    <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-medium ${classes}`}>
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
// connection's git_repo_identifier (e.g. "MoranWeissman/sharko"). When
// the identifier is missing we fall back to a no-op `#` link — the
// caller hides the escape hatch in that case anyway.
function gitHubAllPRsURL(repoIdentifier: string | undefined, state: 'open' | 'closed'): string {
  if (!repoIdentifier) return '#'
  const q = state === 'open' ? 'is%3Apr+is%3Aopen' : 'is%3Apr+is%3Aclosed'
  // Best-effort GitHub URL. Other providers (GitLab, Azure DevOps) get
  // the same /pulls path which won't 404, but the query string is
  // ignored — that's acceptable for the V125 MVP.
  return `https://github.com/${repoIdentifier}/pulls?q=${q}`
}

// FilterControls renders the chip row + search input shared between the
// Pending and Merged tab bodies. Keeping it in one place ensures the
// chips stay visually identical across tabs (a small but real source
// of UX friction otherwise).
function FilterControls({
  category,
  onCategoryChange,
  search,
  onSearchChange,
}: {
  category: CategoryKey
  onCategoryChange: (next: CategoryKey) => void
  search: string
  onSearchChange: (next: string) => void
}) {
  return (
    <div className="mb-3 flex flex-wrap items-center gap-2">
      <div role="group" aria-label="Filter by category" className="inline-flex flex-wrap gap-1">
        {CATEGORY_BUCKETS.map((b) => {
          const active = category === b.key
          return (
            <button
              key={b.key}
              type="button"
              onClick={() => onCategoryChange(b.key)}
              aria-pressed={active}
              className={`rounded-full px-2.5 py-0.5 text-[11px] font-medium transition-colors ${
                active
                  ? 'bg-teal-600 text-white shadow-sm'
                  : 'bg-[#e8f4ff] text-[#0a3a5a] hover:bg-[#d6eeff] dark:bg-gray-700 dark:text-gray-200 dark:hover:bg-gray-600'
              }`}
            >
              {b.label}
            </button>
          )
        })}
      </div>
      <div className="ml-auto flex items-center gap-1 rounded-md ring-1 ring-[#6aade0] bg-white px-2 py-0.5 text-xs dark:ring-gray-700 dark:bg-gray-900">
        <Search className="h-3 w-3 text-[#5a8aaa] dark:text-gray-400" />
        <input
          type="search"
          value={search}
          onChange={(e) => onSearchChange(e.target.value)}
          placeholder="Search title, cluster, addon…"
          aria-label="Search PRs"
          className="w-44 bg-transparent py-0.5 text-[#0a2a4a] placeholder:text-[#5a8aaa] focus:outline-none dark:text-gray-200 dark:placeholder:text-gray-500"
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
  onMergeDetected,
}: {
  cluster?: string
  category: CategoryKey
  search: string
  repoIdentifier: string | undefined
  onMergeDetected?: (pr: TrackedPR) => void
}) {
  const [prs, setPrs] = useState<TrackedPR[]>([])
  const [serverLimit, setServerLimit] = useState<number>(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [refreshingId, setRefreshingId] = useState<number | null>(null)
  const previousStatusRef = useRef<Record<number, string>>({})

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
      } catch (e: unknown) {
        setError(e instanceof Error ? e.message : 'Failed to load tracked PRs')
      } finally {
        setLoading(false)
      }
    },
    [cluster, onMergeDetected, operationCSV],
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
      <div className="flex items-center justify-center py-6 text-[#3a6a8a] dark:text-gray-400">
        <Loader2 className="mr-2 h-4 w-4 animate-spin" />
        <span className="text-xs">Loading PRs…</span>
      </div>
    )
  }
  if (error) {
    return <p className="text-xs text-red-600 dark:text-red-400">{error}</p>
  }
  if (prs.length === 0) {
    return (
      <EmptyState
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

  return (
    <div className="space-y-2">
      <div className="flex items-baseline justify-between text-[11px] text-[#3a6a8a] dark:text-gray-400">
        <span>
          Showing {visiblePrs.length} of {prs.length} open PR{prs.length === 1 ? '' : 's'}
          {atCap ? ` (server cap)` : ''}
        </span>
        {atCap && repoIdentifier && (
          <a
            href={gitHubAllPRsURL(repoIdentifier, 'open')}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1 text-teal-600 hover:underline dark:text-teal-400"
          >
            View all on GitHub <ExternalLink className="h-3 w-3" />
          </a>
        )}
      </div>
      <div className="overflow-x-auto">
        <table className="w-full text-left text-xs">
          <thead>
            <tr className="border-b border-[#6aade0] dark:border-gray-700">
              <th className="pb-2 pr-3 font-semibold text-[#0a2a4a] dark:text-gray-100">Title</th>
              <th className="pb-2 pr-3 font-semibold text-[#0a2a4a] dark:text-gray-100">Category</th>
              {!cluster && <th className="pb-2 pr-3 font-semibold text-[#0a2a4a] dark:text-gray-100">Cluster</th>}
              <th className="pb-2 pr-3 font-semibold text-[#0a2a4a] dark:text-gray-100">User</th>
              <th className="pb-2 pr-3 font-semibold text-[#0a2a4a] dark:text-gray-100">Status</th>
              <th className="pb-2 pr-3 font-semibold text-[#0a2a4a] dark:text-gray-100">Created</th>
              <th className="pb-2 font-semibold text-[#0a2a4a] dark:text-gray-100 text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            {visiblePrs.map((pr) => (
              <tr
                key={pr.pr_id}
                className="border-b border-[#d6eeff] last:border-0 dark:border-gray-700/50"
              >
                <td className="py-2 pr-3">
                  <span className="font-medium text-[#0a3a5a] dark:text-gray-300" title={pr.pr_title}>
                    {pr.pr_title.length > 50 ? pr.pr_title.slice(0, 50) + '…' : pr.pr_title}
                  </span>
                </td>
                <td className="py-2 pr-3">
                  <CategoryBadge operation={pr.operation} />
                </td>
                {!cluster && (
                  <td className="py-2 pr-3 text-[#2a5a7a] dark:text-gray-400">{pr.cluster || '—'}</td>
                )}
                <td className="py-2 pr-3 text-[#2a5a7a] dark:text-gray-400">{pr.user}</td>
                <td className="py-2 pr-3">
                  <span className="inline-flex items-center gap-1 rounded-full bg-green-50 px-2 py-0.5 text-xs font-medium text-green-700 dark:bg-green-900/30 dark:text-green-400">
                    <span className="inline-block h-2 w-2 rounded-full bg-green-500" />
                    Open
                  </span>
                </td>
                <td className="py-2 pr-3 text-[#3a6a8a] dark:text-gray-400 whitespace-nowrap">
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
                      className="rounded p-1 text-[#3a6a8a] transition-colors hover:bg-[#d6eeff] hover:text-[#0a2a4a] dark:text-gray-400 dark:hover:bg-gray-700 dark:hover:text-white"
                      title="Open PR in browser"
                      aria-label={`Open PR #${pr.pr_id} on GitHub`}
                    >
                      <ExternalLink className="h-3.5 w-3.5" />
                    </a>
                    <button
                      onClick={() => void handleRefreshPR(pr.pr_id)}
                      disabled={refreshingId === pr.pr_id}
                      className="rounded p-1 text-[#3a6a8a] transition-colors hover:bg-[#d6eeff] hover:text-[#0a2a4a] disabled:opacity-50 dark:text-gray-400 dark:hover:bg-gray-700 dark:hover:text-white"
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
    </div>
  )
}

function MergedTabBody({
  cluster,
  category,
  search,
  repoIdentifier,
}: {
  cluster?: string
  category: CategoryKey
  search: string
  repoIdentifier: string | undefined
}) {
  const [prs, setPrs] = useState<MergedPRItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const fetchMerged = useCallback(
    async (showLoading = false) => {
      try {
        if (showLoading) setLoading(true)
        setError(null)
        const filters: { cluster?: string; limit?: number } = { limit: 100 }
        if (cluster) filters.cluster = cluster
        const res = await fetchMergedPRs(filters)
        setPrs(res.prs || [])
      } catch (e: unknown) {
        setError(e instanceof Error ? e.message : 'Failed to load merged PRs')
      } finally {
        setLoading(false)
      }
    },
    [cluster],
  )

  useEffect(() => {
    void fetchMerged(true)
    // Less aggressive polling on merged — backend cache is 60s and the
    // data changes only when something actually merges.
    const id = setInterval(() => void fetchMerged(false), POLL_INTERVAL * 2)
    return () => clearInterval(id)
  }, [fetchMerged])

  // Merged PRs don't have a stored Operation field (the prtracker
  // dropped them on merge — see /api/v1/prs/merged). The handler does
  // a best-effort `inferOperation()` from the title, but that won't
  // line up with the canonical Op codes. So for the Merged tab the
  // category filter is applied as a best-effort title-substring match
  // — perfect for register-cluster (titles always start with "Register
  // cluster"), looser for upgrade-addon. Unknowns pass through.
  const filteredByCategory = useMemo(() => {
    const bucket = CATEGORY_BUCKETS.find((b) => b.key === category)
    if (!bucket || bucket.operations.length === 0) return prs
    const tokens = bucket.operations
      .map((op) => op.replace(/-/g, ' '))
      .map((t) => t.toLowerCase())
    return prs.filter((pr) => {
      const hay = `${pr.pr_title ?? ''} ${pr.operation ?? ''} ${pr.pr_branch ?? ''}`.toLowerCase()
      return tokens.some((t) => hay.includes(t))
    })
  }, [prs, category])

  const visiblePrs = useMemo(() => {
    if (!search.trim()) return filteredByCategory
    const needle = search.trim().toLowerCase()
    return filteredByCategory.filter((pr) => {
      const haystack = `${pr.pr_title ?? ''} ${pr.cluster ?? ''} ${pr.addon ?? ''} ${pr.pr_branch ?? ''}`.toLowerCase()
      return haystack.includes(needle)
    })
  }, [filteredByCategory, search])

  if (loading) {
    return (
      <div className="flex items-center justify-center py-6 text-[#3a6a8a] dark:text-gray-400">
        <Loader2 className="mr-2 h-4 w-4 animate-spin" />
        <span className="text-xs">Loading merged PRs…</span>
      </div>
    )
  }
  if (error) {
    return <p className="text-xs text-red-600 dark:text-red-400">{error}</p>
  }
  if (prs.length === 0) {
    return (
      <EmptyState
        title="No merged PRs yet"
        description="Sharko-authored PRs that have been merged on GitHub will appear here."
      />
    )
  }

  return (
    <div className="space-y-2">
      <div className="flex items-baseline justify-between text-[11px] text-[#3a6a8a] dark:text-gray-400">
        <span>
          Showing {visiblePrs.length} of {prs.length} merged PR{prs.length === 1 ? '' : 's'}
        </span>
        {repoIdentifier && (
          <a
            href={gitHubAllPRsURL(repoIdentifier, 'closed')}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1 text-teal-600 hover:underline dark:text-teal-400"
          >
            View all on GitHub <ExternalLink className="h-3 w-3" />
          </a>
        )}
      </div>
      <div className="overflow-x-auto">
        <table className="w-full text-left text-xs">
          <thead>
            <tr className="border-b border-[#6aade0] dark:border-gray-700">
              <th className="pb-2 pr-3 font-semibold text-[#0a2a4a] dark:text-gray-100">Title</th>
              {!cluster && <th className="pb-2 pr-3 font-semibold text-[#0a2a4a] dark:text-gray-100">Cluster</th>}
              <th className="pb-2 pr-3 font-semibold text-[#0a2a4a] dark:text-gray-100">Author</th>
              <th className="pb-2 pr-3 font-semibold text-[#0a2a4a] dark:text-gray-100">Merged</th>
              <th className="pb-2 font-semibold text-[#0a2a4a] dark:text-gray-100 text-right">Link</th>
            </tr>
          </thead>
          <tbody>
            {visiblePrs.map((pr) => (
              <tr
                key={pr.pr_id}
                className="border-b border-[#d6eeff] last:border-0 dark:border-gray-700/50"
              >
                <td className="py-2 pr-3">
                  <a
                    href={pr.pr_url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="font-medium text-[#0a3a5a] hover:text-teal-600 hover:underline dark:text-gray-300 dark:hover:text-teal-400"
                    title={pr.description ? `${pr.pr_title}\n\n${pr.description}` : pr.pr_title}
                  >
                    #{pr.pr_id} {pr.pr_title.length > 60 ? pr.pr_title.slice(0, 60) + '…' : pr.pr_title}
                  </a>
                </td>
                {!cluster && (
                  <td className="py-2 pr-3 text-[#2a5a7a] dark:text-gray-400">{pr.cluster || '—'}</td>
                )}
                <td className="py-2 pr-3 text-[#2a5a7a] dark:text-gray-400">{pr.author || '—'}</td>
                <td className="py-2 pr-3 text-[#3a6a8a] dark:text-gray-400 whitespace-nowrap">
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
                    className="inline-flex items-center gap-1 rounded p-1 text-[#3a6a8a] transition-colors hover:bg-[#d6eeff] hover:text-[#0a2a4a] dark:text-gray-400 dark:hover:bg-gray-700 dark:hover:text-white"
                    aria-label={`Open merged PR #${pr.pr_id} on GitHub`}
                    title="Open on GitHub"
                  >
                    <ExternalLink className="h-3.5 w-3.5" />
                  </a>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
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
  const initial: TabKey = searchParams.get('prs_state') === 'merged' ? 'merged' : 'pending'
  const [tab, setTab] = useState<TabKey>(initial)
  const [category, setCategory] = useState<CategoryKey>('all')
  const [search, setSearch] = useState('')
  // Optional context — the panel renders fine without a connection
  // provider (e.g. in unit tests that mount it under MemoryRouter only).
  const connCtx = useConnectionsOptional()

  // Pick the active connection's git_repo_identifier for the "View all
  // on GitHub →" escape hatch. When no active connection (test mode /
  // first-run), the link is hidden.
  const repoIdentifier = useMemo(() => {
    if (!connCtx) return undefined
    const conn = connCtx.connections.find((c) => c.name === connCtx.activeConnection)
    return conn?.git_repo_identifier
  }, [connCtx])

  // Keep ?prs_state= in sync when the user clicks. We use replace so back
  // navigation goes to the previous page rather than the previous tab.
  const switchTab = useCallback(
    (next: TabKey) => {
      setTab(next)
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
    <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5 shadow-sm dark:ring-gray-700 dark:bg-gray-800">
      <div className="mb-3 flex items-center justify-between">
        <div className="flex items-center gap-2">
          <GitPullRequest className="h-4 w-4 text-teal-500" />
          <h3 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
            {cluster ? 'Cluster PRs' : 'Pull Requests'}
          </h3>
        </div>
        <div
          role="tablist"
          aria-label="PR state filter"
          onKeyDown={onKeyDown}
          className="inline-flex rounded-lg border border-[#6aade0] bg-white p-0.5 text-xs dark:border-gray-700 dark:bg-gray-900"
        >
          <button
            role="tab"
            aria-selected={tab === 'pending'}
            tabIndex={tab === 'pending' ? 0 : -1}
            onClick={() => switchTab('pending')}
            className={`rounded px-3 py-1 font-medium transition-colors ${
              tab === 'pending'
                ? 'bg-teal-600 text-white shadow-sm'
                : 'text-[#0a2a4a] hover:bg-[#d6eeff] dark:text-gray-300 dark:hover:bg-gray-700'
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
                : 'text-[#0a2a4a] hover:bg-[#d6eeff] dark:text-gray-300 dark:hover:bg-gray-700'
            }`}
          >
            Merged
          </button>
        </div>
      </div>

      <FilterControls
        category={category}
        onCategoryChange={setCategory}
        search={search}
        onSearchChange={setSearch}
      />

      <div role="tabpanel" aria-label={tab === 'pending' ? 'Pending PRs' : 'Merged PRs'}>
        {tab === 'pending' ? (
          <PendingTabBody
            cluster={cluster}
            category={category}
            search={search}
            repoIdentifier={repoIdentifier}
            onMergeDetected={onMergeDetected}
          />
        ) : (
          <MergedTabBody cluster={cluster} category={category} search={search} repoIdentifier={repoIdentifier} />
        )}
      </div>
    </div>
  )
}

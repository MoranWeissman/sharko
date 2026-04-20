/**
 * PullRequestsPanel — replaces the dashboard's old "Pending PRs" panel with
 * a Pending/Merged tab toggle.
 *
 * Maintainer feedback (v1.21 Bundle 3, item 5):
 *   "in dashboard we have 'Pending PRs' but i want also to be able to switch
 *    from there to see merged PRs with the PR description/user and etc and
 *    a link to github"
 *
 * Design:
 *   - Tabs: Pending | Merged. Selection persists in the URL via
 *     `?prs_state=pending|merged` so deep-links work.
 *   - Pending tab reuses the existing PendingPRsPanel rendering pattern
 *     (kept as a separate component for the per-cluster page).
 *   - Merged tab fetches /api/v1/prs/merged (cap 20 by default, server-side
 *     cached 60s). Shows author + merged-at + cluster/addon if inferable +
 *     external GitHub link.
 *   - WCAG 2.1 AA: tab toggle uses role=tablist + role=tab + aria-selected,
 *     keyboard arrow-keys cycle.
 */
import { useCallback, useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import {
  Clock,
  ExternalLink,
  GitMerge,
  GitPullRequest,
  Loader2,
  RefreshCw,
} from 'lucide-react'
import {
  fetchMergedPRs,
  fetchTrackedPRs,
  refreshPR,
  type MergedPRItem,
} from '@/services/api'
import type { TrackedPR } from '@/services/models'
import { EmptyState } from '@/components/EmptyState'

const POLL_INTERVAL = 30_000

type TabKey = 'pending' | 'merged'

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

function PendingTabBody({
  cluster,
  onMergeDetected,
}: {
  cluster?: string
  onMergeDetected?: (pr: TrackedPR) => void
}) {
  const [prs, setPrs] = useState<TrackedPR[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [refreshingId, setRefreshingId] = useState<number | null>(null)
  const previousStatusRef = useRef<Record<number, string>>({})

  const fetchPRs = useCallback(
    async (showLoading = false) => {
      try {
        if (showLoading) setLoading(true)
        setError(null)
        const filters: { cluster?: string } = {}
        if (cluster) filters.cluster = cluster
        const result = await fetchTrackedPRs(filters)
        const newPrs = result.prs || []

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
    [cluster, onMergeDetected],
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
        description={cluster ? 'No pull requests tracked for this cluster.' : 'No pull requests currently being tracked.'}
      />
    )
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left text-xs">
        <thead>
          <tr className="border-b border-[#6aade0] dark:border-gray-700">
            <th className="pb-2 pr-3 font-semibold text-[#0a2a4a] dark:text-gray-100">Title</th>
            {!cluster && <th className="pb-2 pr-3 font-semibold text-[#0a2a4a] dark:text-gray-100">Cluster</th>}
            <th className="pb-2 pr-3 font-semibold text-[#0a2a4a] dark:text-gray-100">Operation</th>
            <th className="pb-2 pr-3 font-semibold text-[#0a2a4a] dark:text-gray-100">User</th>
            <th className="pb-2 pr-3 font-semibold text-[#0a2a4a] dark:text-gray-100">Status</th>
            <th className="pb-2 pr-3 font-semibold text-[#0a2a4a] dark:text-gray-100">Created</th>
            <th className="pb-2 font-semibold text-[#0a2a4a] dark:text-gray-100 text-right">Actions</th>
          </tr>
        </thead>
        <tbody>
          {prs.map((pr) => (
            <tr
              key={pr.pr_id}
              className="border-b border-[#d6eeff] last:border-0 dark:border-gray-700/50"
            >
              <td className="py-2 pr-3">
                <span className="font-medium text-[#0a3a5a] dark:text-gray-300" title={pr.pr_title}>
                  {pr.pr_title.length > 50 ? pr.pr_title.slice(0, 50) + '…' : pr.pr_title}
                </span>
              </td>
              {!cluster && (
                <td className="py-2 pr-3 text-[#2a5a7a] dark:text-gray-400">{pr.cluster || '—'}</td>
              )}
              <td className="py-2 pr-3 text-[#2a5a7a] dark:text-gray-400 capitalize">{pr.operation}</td>
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
  )
}

function MergedTabBody({ cluster }: { cluster?: string }) {
  const [prs, setPrs] = useState<MergedPRItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const fetchMerged = useCallback(
    async (showLoading = false) => {
      try {
        if (showLoading) setLoading(true)
        setError(null)
        const filters: { cluster?: string; limit?: number } = { limit: 20 }
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
    // Less aggressive polling on merged — backend cache is 60s and the data
    // changes only when something actually merges.
    const id = setInterval(() => void fetchMerged(false), POLL_INTERVAL * 2)
    return () => clearInterval(id)
  }, [fetchMerged])

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
          {prs.map((pr) => (
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

      <div role="tabpanel" aria-label={tab === 'pending' ? 'Pending PRs' : 'Merged PRs'}>
        {tab === 'pending' ? (
          <PendingTabBody cluster={cluster} onMergeDetected={onMergeDetected} />
        ) : (
          <MergedTabBody cluster={cluster} />
        )}
      </div>
    </div>
  )
}

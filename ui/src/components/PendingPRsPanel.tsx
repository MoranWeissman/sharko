import { useState, useEffect, useCallback, useRef } from 'react'
import {
  ExternalLink,
  RefreshCw,
  GitPullRequest,
  Clock,
  Loader2,
} from 'lucide-react'
import { fetchTrackedPRs, refreshPR } from '@/services/api'
import type { TrackedPR } from '@/services/models'
import { EmptyState } from '@/components/EmptyState'

interface PendingPRsPanelProps {
  cluster?: string
  onMergeDetected?: (pr: TrackedPR) => void
}

const PR_POLL_INTERVAL = 30_000

function timeAgo(timestamp: string): string {
  const secs = Math.floor((Date.now() - new Date(timestamp).getTime()) / 1000)
  if (secs < 60) return 'just now'
  if (secs < 3600) return `${Math.floor(secs / 60)}m ago`
  if (secs < 86400) return `${Math.floor(secs / 3600)}h ago`
  return `${Math.floor(secs / 86400)}d ago`
}

function prStatusBadge(status: string) {
  const s = status.toLowerCase()
  if (s === 'open') {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-green-50 px-2 py-0.5 text-xs font-medium text-green-700 dark:bg-green-900/30 dark:text-green-400">
        <span className="inline-block h-2 w-2 rounded-full bg-green-500" />
        Open
      </span>
    )
  }
  if (s === 'merged') {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-purple-50 px-2 py-0.5 text-xs font-medium text-purple-700 dark:bg-purple-900/30 dark:text-purple-400">
        <span className="inline-block h-2 w-2 rounded-full bg-purple-500" />
        Merged
      </span>
    )
  }
  if (s === 'closed') {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-red-50 px-2 py-0.5 text-xs font-medium text-red-700 dark:bg-red-900/30 dark:text-red-400">
        <span className="inline-block h-2 w-2 rounded-full bg-red-500" />
        Closed
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1 rounded-full bg-[#d6eeff] px-2 py-0.5 text-xs font-medium text-[#1a4a6a] dark:bg-gray-700 dark:text-gray-400">
      <span className="inline-block h-2 w-2 rounded-full bg-[#3a6a8a]" />
      {status}
    </span>
  )
}

export function PendingPRsPanel({ cluster, onMergeDetected }: PendingPRsPanelProps) {
  const [prs, setPrs] = useState<TrackedPR[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [refreshingId, setRefreshingId] = useState<number | null>(null)
  const previousStatusRef = useRef<Record<number, string>>({})

  const fetchPRs = useCallback(async (showLoading = false) => {
    try {
      if (showLoading) setLoading(true)
      setError(null)
      const filters: { cluster?: string } = {}
      if (cluster) filters.cluster = cluster
      const result = await fetchTrackedPRs(filters)
      const newPrs = result.prs || []

      // Detect merge transitions
      const prevStatuses = previousStatusRef.current
      for (const pr of newPrs) {
        const prev = prevStatuses[pr.pr_id]
        if (prev && prev.toLowerCase() === 'open' && pr.last_status.toLowerCase() === 'merged') {
          onMergeDetected?.(pr)
        }
      }

      // Update previous statuses
      const newStatuses: Record<number, string> = {}
      for (const pr of newPrs) {
        newStatuses[pr.pr_id] = pr.last_status
      }
      previousStatusRef.current = newStatuses

      setPrs(newPrs)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to load tracked PRs')
    } finally {
      setLoading(false)
    }
  }, [cluster, onMergeDetected])

  useEffect(() => {
    void fetchPRs(true)
    const interval = setInterval(() => {
      void fetchPRs(false)
    }, PR_POLL_INTERVAL)
    return () => clearInterval(interval)
  }, [fetchPRs])

  const handleRefreshPR = async (id: number) => {
    setRefreshingId(id)
    try {
      await refreshPR(id)
      await fetchPRs(false)
    } catch {
      // refresh failed silently — the PR list will still show the cached status
    } finally {
      setRefreshingId(null)
    }
  }

  if (loading) {
    return (
      <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5 shadow-sm dark:ring-gray-700 dark:bg-gray-800">
        <div className="flex items-center gap-2 mb-3">
          <GitPullRequest className="h-4 w-4 text-teal-500" />
          <h3 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
            {cluster ? 'Cluster PRs' : 'Pending PRs'}
          </h3>
        </div>
        <div className="flex items-center justify-center py-6 text-[#3a6a8a] dark:text-gray-400">
          <Loader2 className="mr-2 h-4 w-4 animate-spin" />
          <span className="text-xs">Loading PRs...</span>
        </div>
      </div>
    )
  }

  if (error) {
    return (
      <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5 shadow-sm dark:ring-gray-700 dark:bg-gray-800">
        <div className="flex items-center gap-2 mb-3">
          <GitPullRequest className="h-4 w-4 text-teal-500" />
          <h3 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
            {cluster ? 'Cluster PRs' : 'Pending PRs'}
          </h3>
        </div>
        <p className="text-xs text-red-600 dark:text-red-400">{error}</p>
      </div>
    )
  }

  if (prs.length === 0) {
    return (
      <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5 shadow-sm dark:ring-gray-700 dark:bg-gray-800">
        <div className="flex items-center gap-2 mb-3">
          <GitPullRequest className="h-4 w-4 text-teal-500" />
          <h3 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
            {cluster ? 'Cluster PRs' : 'Pending PRs'}
          </h3>
        </div>
        <EmptyState
          title="No tracked PRs"
          description={cluster ? 'No pull requests tracked for this cluster.' : 'No pull requests currently being tracked.'}
        />
      </div>
    )
  }

  const openCount = prs.filter(pr => pr.last_status.toLowerCase() === 'open').length

  return (
    <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5 shadow-sm dark:ring-gray-700 dark:bg-gray-800">
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2">
          <GitPullRequest className="h-4 w-4 text-teal-500" />
          <h3 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
            {cluster ? 'Cluster PRs' : 'Pending PRs'}
          </h3>
          {openCount > 0 && (
            <span className="inline-flex items-center justify-center rounded-full bg-teal-100 px-2 py-0.5 text-xs font-semibold text-teal-700 dark:bg-teal-900/30 dark:text-teal-400">
              {openCount} open
            </span>
          )}
        </div>
        <button
          onClick={() => void fetchPRs(false)}
          className="rounded-lg p-1.5 text-[#3a6a8a] transition-colors hover:bg-[#d6eeff] hover:text-[#0a2a4a] dark:text-gray-400 dark:hover:bg-gray-700 dark:hover:text-white"
          aria-label="Refresh PRs"
          title="Refresh PR list"
        >
          <RefreshCw className="h-3.5 w-3.5" />
        </button>
      </div>

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
                    {pr.pr_title.length > 50 ? pr.pr_title.slice(0, 50) + '...' : pr.pr_title}
                  </span>
                </td>
                {!cluster && (
                  <td className="py-2 pr-3 text-[#2a5a7a] dark:text-gray-400">
                    {pr.cluster || '-'}
                  </td>
                )}
                <td className="py-2 pr-3 text-[#2a5a7a] dark:text-gray-400 capitalize">
                  {pr.operation}
                </td>
                <td className="py-2 pr-3 text-[#2a5a7a] dark:text-gray-400">
                  {pr.user}
                </td>
                <td className="py-2 pr-3">
                  {prStatusBadge(pr.last_status)}
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
                    >
                      <ExternalLink className="h-3.5 w-3.5" />
                    </a>
                    <button
                      onClick={() => void handleRefreshPR(pr.pr_id)}
                      disabled={refreshingId === pr.pr_id}
                      className="rounded p-1 text-[#3a6a8a] transition-colors hover:bg-[#d6eeff] hover:text-[#0a2a4a] disabled:opacity-50 dark:text-gray-400 dark:hover:bg-gray-700 dark:hover:text-white"
                      title="Refresh PR status"
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

import { useEffect, useState } from 'react'
import { ExternalLink, GitMerge, Loader2 } from 'lucide-react'
import type { RecentPRsResponse } from '@/services/models'

/**
 * RecentPRsPanel — small "Recent changes (last 5)" card rendered beneath a
 * ValuesEditor. Shows recently-merged PRs that touched the matching values
 * file. Backed by a 5-minute backend cache; no client-side polling.
 *
 * Intentionally minimal:
 *  - No pagination. Five rows is the bar.
 *  - Read-only. No refresh button — `View all on GitHub →` takes the user to
 *    the full PR search if they want history.
 *  - Gracefully invisible when the backend returns zero entries (an empty
 *    state would be noise for brand-new addons with no edit history).
 */
export interface RecentPRsPanelProps {
  /** A loader that returns the response. Caller picks the addon / cluster. */
  load: () => Promise<RecentPRsResponse>
  /** Title shown in the panel header; defaults to "Recent changes". */
  title?: string
}

export function RecentPRsPanel({ load, title = 'Recent changes' }: RecentPRsPanelProps) {
  const [data, setData] = useState<RecentPRsResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    load()
      .then((res) => {
        if (!cancelled) setData(res)
      })
      .catch((e) => {
        if (!cancelled) setError(e instanceof Error ? e.message : 'Failed to load recent PRs')
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [load])

  if (loading) {
    return (
      <div className="rounded-xl ring-2 ring-[#6aade0] bg-white p-4 text-xs text-[#3a6a8a] dark:ring-gray-700 dark:bg-gray-800 dark:text-gray-400">
        <div className="flex items-center gap-2">
          <Loader2 className="h-3.5 w-3.5 animate-spin" />
          Loading recent PRs…
        </div>
      </div>
    )
  }

  // Quiet failure — the editor is the primary UX, so a failure here shouldn't
  // bubble up as a red banner. Log via console.warn for dev visibility only.
  if (error) {
    console.warn('RecentPRsPanel load failed:', error)
    return null
  }
  if (!data || data.entries.length === 0) {
    return null
  }

  return (
    <div className="rounded-xl ring-2 ring-[#6aade0] bg-white p-4 dark:ring-gray-700 dark:bg-gray-800">
      <div className="mb-2 flex items-center justify-between">
        <h4 className="flex items-center gap-1.5 text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
          <GitMerge className="h-4 w-4 text-[#3a6a8a] dark:text-gray-400" />
          {title}
        </h4>
        {data.view_all_url && (
          <a
            href={data.view_all_url}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-0.5 text-xs font-medium text-teal-600 hover:underline dark:text-teal-400"
          >
            View all on GitHub
            <ExternalLink className="h-3 w-3" />
          </a>
        )}
      </div>
      <ul className="divide-y divide-[#e0f0ff] dark:divide-gray-700">
        {data.entries.map((pr) => (
          <li key={pr.pr_id} className="flex items-center gap-3 py-1.5 text-xs">
            <a
              href={pr.url}
              target="_blank"
              rel="noopener noreferrer"
              className="flex-1 truncate font-medium text-[#0a2a4a] hover:text-teal-600 hover:underline dark:text-gray-100 dark:hover:text-teal-400"
              title={pr.title}
            >
              #{pr.pr_id} {pr.title}
            </a>
            <span className="shrink-0 text-[#3a6a8a] dark:text-gray-500" title={pr.merged_at}>
              {pr.author || '—'} · {formatMergedAt(pr.merged_at)}
            </span>
          </li>
        ))}
      </ul>
    </div>
  )
}

function formatMergedAt(raw: string): string {
  if (!raw) return 'merged'
  const d = new Date(raw)
  if (Number.isNaN(d.getTime())) return raw
  const diffMs = Date.now() - d.getTime()
  const days = Math.floor(diffMs / (1000 * 60 * 60 * 24))
  if (days < 1) return 'today'
  if (days === 1) return 'yesterday'
  if (days < 7) return `${days}d ago`
  if (days < 30) return `${Math.floor(days / 7)}w ago`
  return d.toLocaleDateString()
}

import { useEffect, useState } from 'react'
import { AlertTriangle, ExternalLink, GitMerge, Loader2, RefreshCw } from 'lucide-react'
import type { RecentPRsResponse } from '@/services/models'

/**
 * RecentPRsPanel — small "Recent changes (last 5)" card rendered beneath a
 * ValuesEditor. Shows recently-merged PRs that touched the matching values
 * file. Backed by a 5-minute backend cache; no client-side polling.
 *
 * v1.20.2: always renders a visible card (empty-state included). Previously
 * the panel collapsed to null when zero entries, which made users think it
 * was broken. It also now accepts a `refreshKey` that forces a re-fetch
 * after the editor submits — needed so the user sees their own new PR.
 */
export interface RecentPRsPanelProps {
  /** A loader that returns the response. Caller picks the addon / cluster. */
  load: () => Promise<RecentPRsResponse>
  /** Title shown in the panel header; defaults to "Recent changes". */
  title?: string
  /**
   * Bump to force a re-fetch — e.g. after a successful submit on the editor
   * above. Changes to `refreshKey` run the loader again (bypassing the
   * panel's own cache; the backend cache is still authoritative).
   */
  refreshKey?: number
}

export function RecentPRsPanel({ load, title = 'Recent changes', refreshKey = 0 }: RecentPRsPanelProps) {
  const [data, setData] = useState<RecentPRsResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [manualRefresh, setManualRefresh] = useState(0)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    load()
      .then((res) => {
        if (!cancelled) setData(res)
      })
      .catch((e) => {
        if (!cancelled) {
          const msg = e instanceof Error ? e.message : 'Failed to load recent PRs'
          setError(msg)
          // Surface to devtools for debugging — the panel itself still
          // renders an inline error strip below.
          console.error('RecentPRsPanel load failed:', msg)
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
    // `load` is a closure redefined on every parent render; we deliberately
    // key on refreshKey / manualRefresh only.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refreshKey, manualRefresh])

  return (
    <div className="rounded-xl ring-2 ring-[#6aade0] bg-white p-4 dark:ring-gray-700 dark:bg-gray-800">
      <div className="mb-2 flex items-center justify-between">
        <h4 className="flex items-center gap-1.5 text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
          <GitMerge className="h-4 w-4 text-[#3a6a8a] dark:text-gray-400" />
          {title}
        </h4>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => setManualRefresh((n) => n + 1)}
            disabled={loading}
            title="Refresh"
            className="inline-flex items-center gap-1 rounded text-xs text-[#3a6a8a] hover:text-teal-600 disabled:opacity-50 dark:text-gray-400 dark:hover:text-teal-400"
          >
            <RefreshCw className={`h-3 w-3 ${loading ? 'animate-spin' : ''}`} />
          </button>
          {data?.view_all_url && (
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
      </div>

      {loading && !data && (
        <div className="flex items-center gap-2 py-2 text-xs text-[#3a6a8a] dark:text-gray-400">
          <Loader2 className="h-3.5 w-3.5 animate-spin" />
          Loading recent PRs…
        </div>
      )}

      {error && (
        <div className="flex items-start gap-2 rounded border border-amber-300 bg-amber-50 px-2 py-1.5 text-xs text-amber-800 dark:border-amber-700 dark:bg-amber-950/30 dark:text-amber-300">
          <AlertTriangle className="mt-0.5 h-3 w-3 shrink-0" />
          <span className="flex-1 truncate" title={error}>
            Couldn't load recent PRs — {error.slice(0, 80)}
          </span>
        </div>
      )}

      {!loading && !error && (!data || data.entries.length === 0) && (
        <p className="py-1.5 text-xs italic text-[#3a6a8a] dark:text-gray-500">
          No recent PRs touching this values file yet. Submit a change and it will appear here.
        </p>
      )}

      {data && data.entries.length > 0 && (
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
      )}
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

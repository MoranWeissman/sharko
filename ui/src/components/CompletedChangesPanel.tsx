import { useState, useEffect, useCallback } from 'react'
import {
  CheckCircle2,
  ChevronDown,
  ChevronUp,
  Clock,
  ExternalLink,
  HelpCircle,
  History,
  Loader2,
  XCircle,
} from 'lucide-react'
import { api } from '@/services/api'
import type { ClusterChange } from '@/services/models'
import { EmptyState } from '@/components/EmptyState'
import { timeAgo, prStatusBadge } from '@/components/PendingPRsPanel'
import { prettyOperation } from '@/lib/utils'

interface CompletedChangesPanelProps {
  cluster: string
  /** Bump to force a re-fetch — e.g. right after a pending PR merges. */
  refreshKey?: number
  /** Fired every time the completed-changes list is (re)loaded. */
  onDataChange?: (changes: ClusterChange[]) => void
}

/**
 * The "Completed changes" half of the cluster Changes tab (V2-cleanup-84.2).
 * Backed by GET /clusters/{name}/changes — the durable, capped change log
 * populated from the PR tracker (V2-cleanup-84.1). Each row is a completed
 * (merged or closed) pull request; clicking a row expands it to show the
 * full detail and a link out to the PR on GitHub — that's the path to the
 * diff and discussion, not a second in-app fetch.
 */
export function CompletedChangesPanel({ cluster, refreshKey = 0, onDataChange }: CompletedChangesPanelProps) {
  const [changes, setChanges] = useState<ClusterChange[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [expandedId, setExpandedId] = useState<number | null>(null)

  const fetchChanges = useCallback(async () => {
    try {
      setLoading(true)
      setError(null)
      const result = await api.getClusterChanges(cluster)
      const newChanges = result.changes || []
      setChanges(newChanges)
      onDataChange?.(newChanges)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to load completed changes')
    } finally {
      setLoading(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cluster])

  useEffect(() => {
    void fetchChanges()
    // refreshKey deliberately re-triggers this effect without changing
    // fetchChanges' identity.
  }, [fetchChanges, refreshKey])

  if (loading && changes.length === 0) {
    return (
      <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5 shadow-sm dark:ring-gray-700 dark:bg-gray-800">
        <div className="flex items-center gap-2 mb-3">
          <History className="h-4 w-4 text-teal-500" />
          <h3 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Completed changes</h3>
        </div>
        <div className="flex items-center justify-center py-6 text-[#3a6a8a] dark:text-gray-400">
          <Loader2 className="mr-2 h-4 w-4 animate-spin" />
          <span className="text-xs">Loading completed changes...</span>
        </div>
      </div>
    )
  }

  if (error) {
    return (
      <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5 shadow-sm dark:ring-gray-700 dark:bg-gray-800">
        <div className="flex items-center gap-2 mb-3">
          <History className="h-4 w-4 text-teal-500" />
          <h3 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Completed changes</h3>
        </div>
        <p className="text-xs text-red-600 dark:text-red-400">{error}</p>
      </div>
    )
  }

  if (changes.length === 0) {
    return (
      <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5 shadow-sm dark:ring-gray-700 dark:bg-gray-800">
        <div className="flex items-center gap-2 mb-3">
          <History className="h-4 w-4 text-teal-500" />
          <h3 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Completed changes</h3>
        </div>
        <EmptyState
          title="No completed changes yet"
          description="Once a pull request for this cluster — like enabling or disabling an addon, or editing its values — merges or closes, it'll show up here with its result."
        />
      </div>
    )
  }

  return (
    <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5 shadow-sm dark:ring-gray-700 dark:bg-gray-800">
      <div className="flex items-center gap-2 mb-3">
        <History className="h-4 w-4 text-teal-500" />
        <h3 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Completed changes</h3>
      </div>

      <div className="space-y-2">
        {changes.map((change) => {
          const isExpanded = expandedId === change.pr_id
          return (
            <div
              key={change.pr_id}
              className="rounded-lg bg-white ring-2 ring-[#6aade0] dark:ring-gray-700 dark:bg-gray-900"
            >
              <button
                type="button"
                onClick={() => setExpandedId(isExpanded ? null : change.pr_id)}
                aria-expanded={isExpanded}
                className="flex w-full flex-wrap items-center gap-2 px-4 py-3 text-left"
              >
                <span className="font-medium text-[#0a2a4a] dark:text-gray-100 capitalize">
                  {prettyOperation(change.operation)}
                </span>
                {change.addon && (
                  <span className="text-[#3a6a8a] dark:text-gray-400">— {change.addon}</span>
                )}
                <span className="ml-auto flex flex-wrap items-center gap-2">
                  {prStatusBadge(change.status)}
                  {deployOutcomeBadge(change.deploy_outcome)}
                  <span className="flex items-center gap-1 text-xs text-[#3a6a8a] dark:text-gray-400">
                    <Clock className="h-3 w-3" />
                    {timeAgo(change.completed_at)}
                  </span>
                  {isExpanded ? (
                    <ChevronUp className="h-4 w-4 text-[#3a6a8a] dark:text-gray-400" />
                  ) : (
                    <ChevronDown className="h-4 w-4 text-[#3a6a8a] dark:text-gray-400" />
                  )}
                </span>
              </button>

              {isExpanded && (
                <div className="space-y-1.5 border-t border-[#d6eeff] px-4 py-3 text-xs text-[#2a5a7a] dark:border-gray-700 dark:text-gray-400">
                  <div>
                    <span className="font-semibold text-[#0a2a4a] dark:text-gray-200">Operation: </span>
                    {prettyOperation(change.operation)}
                  </div>
                  {change.addon && (
                    <div>
                      <span className="font-semibold text-[#0a2a4a] dark:text-gray-200">Addon: </span>
                      {change.addon}
                    </div>
                  )}
                  <div>
                    <span className="font-semibold text-[#0a2a4a] dark:text-gray-200">Status: </span>
                    <span className="capitalize">{change.status}</span>
                  </div>
                  <div>
                    <span className="font-semibold text-[#0a2a4a] dark:text-gray-200">Deploy outcome: </span>
                    {deployOutcomeLabel(change.deploy_outcome)}
                  </div>
                  <div>
                    <span className="font-semibold text-[#0a2a4a] dark:text-gray-200">Opened: </span>
                    {new Date(change.opened_at).toLocaleString()}
                  </div>
                  <div>
                    <span className="font-semibold text-[#0a2a4a] dark:text-gray-200">Completed: </span>
                    {new Date(change.completed_at).toLocaleString()}
                  </div>
                  <a
                    href={change.pr_url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="mt-1 inline-flex items-center gap-1 rounded-md bg-teal-50 px-2.5 py-1.5 font-semibold text-teal-700 hover:bg-teal-100 dark:bg-teal-900/30 dark:text-teal-400 dark:hover:bg-teal-900/50"
                  >
                    View pull request on GitHub
                    <ExternalLink className="h-3.5 w-3.5" />
                  </a>
                </div>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}

function deployOutcomeLabel(outcome: string): string {
  if (outcome === 'healthy') return 'Deployed & healthy'
  if (outcome === 'failed') return 'Sync failed'
  return 'Deploy status unknown'
}

function deployOutcomeBadge(outcome: string) {
  if (outcome === 'healthy') {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-green-50 px-2 py-0.5 text-xs font-medium text-green-700 dark:bg-green-900/30 dark:text-green-400">
        <CheckCircle2 className="h-3 w-3" />
        Deployed & healthy
      </span>
    )
  }
  if (outcome === 'failed') {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-red-50 px-2 py-0.5 text-xs font-medium text-red-700 dark:bg-red-900/30 dark:text-red-400">
        <XCircle className="h-3 w-3" />
        Sync failed
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1 rounded-full bg-[#d6eeff] px-2 py-0.5 text-xs font-medium text-[#1a4a6a] dark:bg-gray-700 dark:text-gray-400">
      <HelpCircle className="h-3 w-3" />
      Deploy status unknown
    </span>
  )
}

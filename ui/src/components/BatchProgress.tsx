import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { CheckCircle2, Circle, Loader2, XCircle, ArrowRight } from 'lucide-react'
import { api } from '@/services/api'
import type { MigrationBatch, Migration } from '@/services/api'

interface BatchProgressProps {
  batch: MigrationBatch
  onUpdate?: (batch: MigrationBatch) => void
}

export function BatchProgress({ batch, onUpdate }: BatchProgressProps) {
  const navigate = useNavigate()
  const [migrations, setMigrations] = useState<Map<string, Migration>>(new Map())
  const [liveBatch, setLiveBatch] = useState(batch)

  // Poll for updates
  useEffect(() => {
    const fetchStatus = async () => {
      try {
        const updated = await api.getActiveBatch()
        if (updated) {
          setLiveBatch(updated)
          onUpdate?.(updated)
        }
        // Fetch individual migration statuses
        const map = new Map<string, Migration>()
        for (const id of (updated || liveBatch).migration_ids) {
          try {
            const m = await api.getMigration(id)
            map.set(id, m)
          } catch { /* ignore */ }
        }
        setMigrations(map)
      } catch { /* ignore */ }
    }

    void fetchStatus()
    const interval = setInterval(fetchStatus, 5000)
    return () => clearInterval(interval)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [batch.id])

  const completed = liveBatch.addons.filter((_, i) => i < liveBatch.current_index).length
  const total = liveBatch.addons.length
  const current = liveBatch.current_index < total ? liveBatch.addons[liveBatch.current_index] : null
  const progress = total > 0 ? Math.round((completed / total) * 100) : 0
  const isDone = liveBatch.status === 'completed'

  return (
    <div className="rounded-xl border-2 border-violet-500 bg-violet-50 p-5 dark:border-violet-600 dark:bg-violet-900/20">
      {/* Header */}
      <div className="mb-3 flex items-center justify-between">
        <div>
          <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
            {isDone ? 'Batch Migration Complete' : 'Batch Migration in Progress'}
          </h3>
          <p className="text-sm text-gray-500 dark:text-gray-400">
            {liveBatch.cluster_name} &middot; {liveBatch.mode.toUpperCase()} mode &middot; {completed}/{total} addons
          </p>
        </div>
        {current && !isDone && (
          <span className="flex items-center gap-2 rounded-full bg-violet-100 px-3 py-1 text-sm font-medium text-violet-700 dark:bg-violet-800 dark:text-violet-200">
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
            Migrating {current}
          </span>
        )}
        {isDone && (
          <span className="flex items-center gap-2 rounded-full bg-green-100 px-3 py-1 text-sm font-medium text-green-700 dark:bg-green-800 dark:text-green-200">
            <CheckCircle2 className="h-3.5 w-3.5" />
            All done
          </span>
        )}
      </div>

      {/* Progress bar */}
      <div className="mb-4 h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-gray-700">
        <div
          className="h-full rounded-full bg-violet-500 transition-all duration-500"
          style={{ width: `${isDone ? 100 : progress}%` }}
        />
      </div>

      {/* Addon queue */}
      <div className="space-y-1.5">
        {liveBatch.addons.map((addon, i) => {
          const migId = liveBatch.migration_ids[i]
          const mig = migrations.get(migId)
          const isCurrent = i === liveBatch.current_index && !isDone
          const isCompleted = i < liveBatch.current_index || isDone
          const isFailed = mig?.status === 'failed'
          return (
            <div
              key={addon}
              onClick={() => migId && navigate(`/migration/${migId}`)}
              className={`flex cursor-pointer items-center gap-3 rounded-lg px-3 py-2 text-sm transition-colors ${
                isCurrent
                  ? 'bg-violet-100 font-medium text-violet-900 dark:bg-violet-800/40 dark:text-violet-100'
                  : isCompleted
                    ? 'text-gray-500 dark:text-gray-400'
                    : 'text-gray-400 dark:text-gray-500'
              } hover:bg-gray-100 dark:hover:bg-gray-700/50`}
            >
              {/* Status icon */}
              {isFailed ? (
                <XCircle className="h-4 w-4 shrink-0 text-red-500" />
              ) : isCompleted ? (
                <CheckCircle2 className="h-4 w-4 shrink-0 text-green-500" />
              ) : isCurrent ? (
                <Loader2 className="h-4 w-4 shrink-0 animate-spin text-violet-500" />
              ) : (
                <Circle className="h-4 w-4 shrink-0 text-gray-300 dark:text-gray-600" />
              )}

              {/* Addon name */}
              <span className={isCompleted && !isCurrent ? 'line-through' : ''}>
                {addon}
              </span>

              {/* Status text */}
              {isCurrent && mig && (
                <span className="ml-auto text-xs text-violet-500">
                  Step {mig.current_step}/10
                </span>
              )}
              {isFailed && mig?.error && (
                <span className="ml-auto truncate text-xs text-red-500" title={mig.error}>
                  {mig.error.slice(0, 50)}
                </span>
              )}

              {/* Link arrow */}
              {(isCurrent || isFailed) && (
                <ArrowRight className="ml-auto h-3.5 w-3.5 shrink-0 text-gray-400" />
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}

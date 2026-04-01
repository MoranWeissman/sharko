import { useState, useEffect, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { Plus, GitPullRequest, Play } from 'lucide-react'
import { api } from '@/services/api'
import type { Migration, MigrationBatch } from '@/services/api'
import { MigrationSettings } from '@/components/MigrationSettings'
import { useAuth } from '@/hooks/useAuth'
import { NewMigrationDialog } from '@/components/NewMigrationDialog'
import { BatchProgress } from '@/components/BatchProgress'
import { StatusBadge } from '@/components/StatusBadge'
import { LoadingState } from '@/components/LoadingState'
import { ErrorState } from '@/components/ErrorState'
import { Button } from '@/components/ui/button'

export default function MigrationPage() {
  const navigate = useNavigate()
  const { isAdmin } = useAuth()
  const [configured, setConfigured] = useState(false)
  const [migrations, setMigrations] = useState<Migration[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [activeBatch, setActiveBatch] = useState<MigrationBatch | null>(null)

  const fetchMigrations = useCallback(async () => {
    try {
      setError(null)
      const [data, batch] = await Promise.all([
        api.listMigrations(),
        api.getActiveBatch().catch(() => null),
      ])
      setMigrations(data ?? [])
      setActiveBatch(batch)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to load migrations')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (configured || !isAdmin) {
      // Non-admins always try to load (they can't see settings to configure)
      void fetchMigrations()
    } else {
      setLoading(false)
    }
  }, [configured, fetchMigrations])

  const handleStarted = (migration: Migration) => {
    setDialogOpen(false)
    navigate(`/migration/${migration.id}`)
  }

  const hasActive = migrations.some((m) =>
    ['running', 'waiting', 'paused', 'gated'].includes(m.status)
  )
  const activeMigration = migrations.find((m) =>
    ['running', 'waiting', 'paused', 'gated'].includes(m.status)
  )

  const handleResume = (migrationId: string) => {
    navigate(`/migration/${migrationId}`)
  }

  const formatDate = (dateStr: string) => {
    try {
      return new Date(dateStr).toLocaleString()
    } catch {
      return dateStr
    }
  }

  const stepLabel = (m: Migration) => {
    if (!m.steps || m.steps.length === 0) return `${m.current_step}`
    const total = m.steps.length
    return `${m.current_step} / ${total}`
  }

  return (
    <div className="space-y-8">
      {/* Hero */}
      <div className="rounded-2xl bg-gradient-to-r from-violet-600 to-purple-700 px-8 py-10 text-white shadow-lg dark:from-violet-800 dark:to-purple-900">
        <div className="flex items-center gap-3">
          <GitPullRequest className="h-8 w-8" />
          <h1 className="text-3xl font-bold tracking-tight sm:text-4xl">
            Migration Wizard
          </h1>
        </div>
        <p className="mt-2 max-w-2xl text-lg text-violet-100">
          Migrate addons from your old platform to the new ArgoCD Addons Platform with guided, step-by-step automation.
        </p>
      </div>

      {/* Settings — admin only */}
      {isAdmin && <MigrationSettings onConfigured={() => setConfigured(true)} />}

      {/* Batch Progress */}
      {activeBatch && activeBatch.status === 'running' && (
        <BatchProgress batch={activeBatch} onUpdate={setActiveBatch} />
      )}

      {/* Migrations List */}
      <div className="rounded-xl border border-gray-200 bg-white p-6 shadow-sm dark:border-gray-700 dark:bg-gray-800">
        <div className="mb-4 flex items-center justify-between">
          <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
            Migrations
          </h3>
          <Button onClick={() => setDialogOpen(true)} disabled={!configured || hasActive}>
            <Plus className="h-4 w-4" />
            {hasActive ? 'Migration in Progress' : 'New Migration'}
          </Button>
        </div>

        {/* Active migration banner */}
        {activeMigration && (
          <div className="mb-4 rounded-lg border-2 border-cyan-500 bg-cyan-50 p-4 dark:bg-cyan-900/20">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-3">
                <span className="font-semibold text-gray-900 dark:text-gray-100">
                  {activeMigration.addon_name}
                </span>
                <span className="text-gray-500 dark:text-gray-400">
                  on {activeMigration.cluster_name}
                </span>
                <StatusBadge status={activeMigration.status} />
              </div>
              <Button size="sm" onClick={() => navigate(`/migration/${activeMigration.id}`)}>
                View Progress
              </Button>
            </div>
          </div>
        )}

        {loading ? (
          <LoadingState message="Loading migrations..." />
        ) : error ? (
          <ErrorState message={error} onRetry={fetchMigrations} />
        ) : migrations.length === 0 ? (
          <div className="py-12 text-center text-gray-500 dark:text-gray-400">
            <GitPullRequest className="mx-auto mb-3 h-10 w-10 text-gray-300 dark:text-gray-600" />
            <p className="text-lg font-medium">No migrations yet</p>
            <p className="mt-1 text-sm">Start your first migration above.</p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-left text-sm">
              <thead>
                <tr className="border-b border-gray-200 dark:border-gray-700">
                  <th className="pb-3 pr-4 font-medium text-gray-500 dark:text-gray-400">Addon</th>
                  <th className="pb-3 pr-4 font-medium text-gray-500 dark:text-gray-400">Cluster</th>
                  <th className="pb-3 pr-4 font-medium text-gray-500 dark:text-gray-400">Status</th>
                  <th className="pb-3 pr-4 font-medium text-gray-500 dark:text-gray-400">Current Step</th>
                  <th className="pb-3 pr-4 font-medium text-gray-500 dark:text-gray-400">Started At</th>
                  <th className="pb-3 font-medium text-gray-500 dark:text-gray-400"></th>
                </tr>
              </thead>
              <tbody>
                {migrations.map((m) => (
                  <tr
                    key={m.id}
                    onClick={() => navigate(`/migration/${m.id}`)}
                    className="cursor-pointer border-b border-gray-100 transition-colors hover:bg-gray-50 dark:border-gray-700/50 dark:hover:bg-gray-700/50"
                  >
                    <td className="py-3 pr-4 font-medium text-gray-900 dark:text-gray-100">
                      {m.addon_name}
                    </td>
                    <td className="py-3 pr-4 text-gray-600 dark:text-gray-300">
                      {m.cluster_name}
                    </td>
                    <td className="py-3 pr-4">
                      <StatusBadge status={m.status} />
                    </td>
                    <td className="py-3 pr-4 text-gray-600 dark:text-gray-300">
                      {stepLabel(m)}
                    </td>
                    <td className="py-3 pr-4 text-gray-500 dark:text-gray-400">
                      {formatDate(m.created_at)}
                    </td>
                    <td className="py-3">
                      {['paused', 'cancelled'].includes(m.status) && (
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={(e) => {
                            e.stopPropagation()
                            handleResume(m.id)
                          }}
                        >
                          <Play className="h-3 w-3" />
                          Resume
                        </Button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <NewMigrationDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        onStarted={handleStarted}
      />
    </div>
  )
}

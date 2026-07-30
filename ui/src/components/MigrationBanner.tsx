import { useState, useEffect, useCallback } from 'react'
import { AlertTriangle, ChevronRight, ExternalLink, Loader2, CheckCircle, FileEdit, FilePlus, FileMinus } from 'lucide-react'
import { api } from '@/services/api'
import type { MigrationPlan } from '@/services/models'
import { RoleGuard } from '@/components/RoleGuard'

// MigrationBanner (v4-wave2 migration-ui) — tells the operator, in plain
// words, that the connected repo still uses the older v3 file layout, and
// walks them through the one-pull-request migration:
//
//   1. Preview migration  — POST /migration/preview, zero side effects
//   2. Open migration PR  — POST /migration/migrate, opens exactly one PR
//
// The banner is self-contained: it fetches its own status
// (GET /migration/status) and renders nothing when the repo has no active
// connection, is already on v4, or has never been set up. Once the
// migration PR merges and the connected repo re-reports format "v4", a
// background poll picks that up and the banner disappears on its own —
// there is no manual dismiss button by design (a v3 repo blocking on a
// merged PR should not be hideable by accident).
//
// Dropped into both the Dashboard (top-level surface) and FirstRunWizard
// (so a person resuming setup on an already-connected v3 repo learns about
// it too) — both call sites just render <MigrationBanner />.
export function MigrationBanner() {
  const [format, setFormat] = useState<'v3' | 'v4' | 'empty' | null>(null)
  const [loading, setLoading] = useState(true)

  const [showPreview, setShowPreview] = useState(false)
  const [plan, setPlan] = useState<MigrationPlan | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)
  const [previewError, setPreviewError] = useState<string | null>(null)

  const [migrating, setMigrating] = useState(false)
  const [migrateError, setMigrateError] = useState<string | null>(null)
  const [prUrl, setPrUrl] = useState<string | null>(null)

  const fetchStatus = useCallback(() => {
    api
      .getMigrationStatus()
      .then((res) => setFormat(res.format))
      // No active connection, not authenticated yet, etc. — say nothing;
      // this banner is a bonus notice, never a hard requirement to render.
      .catch(() => setFormat(null))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    fetchStatus()
  }, [fetchStatus])

  // Once a migration PR is open, keep checking in the background so the
  // banner clears itself the moment the PR merges — no refresh, no
  // dismiss button needed.
  useEffect(() => {
    if (format !== 'v3') return
    const interval = setInterval(fetchStatus, 30000)
    return () => clearInterval(interval)
  }, [format, fetchStatus])

  async function handlePreview() {
    setShowPreview((v) => !v)
    if (plan || previewLoading) return
    setPreviewLoading(true)
    setPreviewError(null)
    try {
      const p = await api.previewMigration()
      setPlan(p)
    } catch (err) {
      setPreviewError(err instanceof Error ? err.message : 'Could not load the migration preview.')
    } finally {
      setPreviewLoading(false)
    }
  }

  async function handleMigrate() {
    setMigrating(true)
    setMigrateError(null)
    try {
      const result = await api.migrateRepo({ yes: true })
      if (result.git?.pr_url) setPrUrl(result.git.pr_url)
      fetchStatus()
    } catch (err) {
      setMigrateError(err instanceof Error ? err.message : 'The migration pull request could not be opened.')
    } finally {
      setMigrating(false)
    }
  }

  if (loading || format !== 'v3') return null

  return (
    <div
      data-testid="migration-banner"
      className="rounded-xl ring-2 ring-amber-300 bg-amber-50 p-4 shadow-sm dark:ring-amber-700 dark:bg-amber-900/20"
    >
      <div className="flex items-start gap-3">
        <AlertTriangle className="mt-0.5 h-5 w-5 shrink-0 text-amber-600 dark:text-amber-400" />
        <div className="min-w-0 flex-1">
          <p className="text-sm font-semibold text-amber-800 dark:text-amber-300">
            This repo uses the older v3 layout
          </p>
          <p className="mt-0.5 text-sm text-amber-700 dark:text-amber-400">
            One pull request migrates it. Reads keep working until then.
          </p>

          {prUrl ? (
            <div className="mt-3 flex flex-wrap items-center gap-2 text-sm text-green-700 dark:text-green-400">
              <CheckCircle className="h-4 w-4 shrink-0" />
              <span>Migration pull request open.</span>
              <a
                href={prUrl}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1 underline hover:text-green-900 dark:hover:text-green-300"
              >
                View it <ExternalLink className="h-3 w-3" />
              </a>
            </div>
          ) : (
            <RoleGuard roles={['admin']}>
              <div className="mt-3 flex flex-wrap items-center gap-3">
                <button
                  type="button"
                  onClick={() => { void handlePreview() }}
                  className="inline-flex items-center gap-1.5 rounded-lg border border-amber-400 bg-amber-50 px-3 py-1.5 text-sm font-medium text-amber-800 hover:bg-amber-100 dark:border-amber-600 dark:bg-amber-900/30 dark:text-amber-300 dark:hover:bg-amber-900/50"
                >
                  <ChevronRight className={`h-3.5 w-3.5 transition-transform ${showPreview ? 'rotate-90' : ''}`} />
                  Preview migration
                </button>
                <button
                  type="button"
                  onClick={() => { void handleMigrate() }}
                  disabled={migrating}
                  data-testid="open-migration-pr"
                  className="inline-flex items-center gap-1.5 rounded-lg bg-amber-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-amber-700 disabled:opacity-50 dark:bg-amber-700 dark:hover:bg-amber-600"
                >
                  {migrating && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                  Open migration PR
                </button>
              </div>
            </RoleGuard>
          )}

          {migrateError && (
            <p className="mt-2 text-sm text-red-600 dark:text-red-400">{migrateError}</p>
          )}

          {showPreview && !prUrl && (
            <div className="mt-3 rounded-lg bg-amber-100/70 p-3 dark:bg-amber-950/30">
              {previewLoading && (
                <div className="flex items-center gap-2 text-sm text-amber-700 dark:text-amber-400">
                  <Loader2 className="h-4 w-4 animate-spin" />
                  Building the migration plan…
                </div>
              )}
              {previewError && (
                <p className="text-sm text-red-600 dark:text-red-400">{previewError}</p>
              )}
              {plan && !previewLoading && (
                <MigrationPlanView plan={plan} />
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

function MigrationPlanView({ plan }: { plan: MigrationPlan }) {
  const rows: Array<{ kind: 'add' | 'convert' | 'remove'; path: string; fromPath?: string }> = [
    ...plan.add.map((f) => ({ kind: 'add' as const, path: f.path })),
    ...plan.convert.map((f) => ({ kind: 'convert' as const, path: f.path, fromPath: f.from_path })),
    ...plan.remove.map((f) => ({ kind: 'remove' as const, path: f.path })),
  ]

  return (
    <div className="space-y-3">
      <p className="text-xs font-semibold uppercase tracking-wide text-amber-700 dark:text-amber-400">
        Files this pull request would touch ({rows.length})
      </p>
      <ul className="max-h-64 space-y-1 overflow-y-auto text-sm">
        {rows.map((row, i) => (
          <li key={`${row.kind}-${row.path}-${i}`} className="flex items-center gap-2 text-amber-900 dark:text-amber-200">
            {row.kind === 'add' && <FilePlus className="h-3.5 w-3.5 shrink-0 text-green-600 dark:text-green-400" />}
            {row.kind === 'convert' && <FileEdit className="h-3.5 w-3.5 shrink-0 text-amber-600 dark:text-amber-400" />}
            {row.kind === 'remove' && <FileMinus className="h-3.5 w-3.5 shrink-0 text-red-600 dark:text-red-400" />}
            <span className="font-mono text-xs">
              {row.kind === 'convert' && row.fromPath ? `${row.fromPath} → ${row.path}` : row.path}
            </span>
          </li>
        ))}
      </ul>
      {plan.notes.length > 0 && (
        <div>
          <p className="text-xs font-semibold uppercase tracking-wide text-amber-700 dark:text-amber-400">
            Worth reading before you merge
          </p>
          <ul className="mt-1 list-disc space-y-1 pl-4 text-sm text-amber-800 dark:text-amber-300">
            {plan.notes.map((note, i) => (
              <li key={i}>{note}</li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

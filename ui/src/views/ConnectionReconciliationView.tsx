// ConnectionReconciliationView — Story 2+3 of the connection-reconciliation
// epic: the cluster connection page as a GitOps reconciliation view, the way
// an ArgoCD Application detail page reads, for a resource reconciled by
// Sharko.
//
// ONE read: GET /clusters/{name}/connection-reconciliation (plus the
// existing audit fetch for the activity feed). The server owns every verdict
// and every promise — this file renders the server's sentences VERBATIM and
// computes none of its own. In particular:
//   - The synchronization invariant (synced ⇒ verification full) is enforced
//     in the API builder. This page renders the combination it is given.
//   - plan.automatic is the ONLY automatic-fix promise on the page. There is
//     no UI-computed "Sharko will fix this on the next pass" anywhere.
//   - Actions render BESIDE the condition or drift group they resolve,
//     driven by plan.action. There is no permanent row of write buttons; a
//     healthy connection shows no repair action at all.
//   - During an in-flight request the INITIATING control says Checking… /
//     Applying… / Repairing… — request-local only, gone on completion, never
//     persisted or inferred anywhere else (product ruling 4).
//
// SECURITY RULES (binding): a sensitive drift entry arrives as path + status
// + sensitive:true and NOTHING else — both sides render the fixed text
// "<redacted>", the same fixed text every time, never a mask whose length
// hints at the value. No secret value, hash, length, fragment or encoding
// exists anywhere in the response, so there is nothing here to leak.

import { useContext, useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import {
  AlertTriangle,
  CheckCircle,
  ChevronDown,
  ChevronRight,
  Loader2,
  OctagonX,
  RefreshCw,
  RotateCcw,
  Wrench,
} from 'lucide-react'
import { api, fetchAuditLog, ApiError } from '@/services/api'
import type { AuditEntry, ConnectionReconciliation } from '@/services/models'
import { CREDS_SOURCE_EKS_TOKEN } from '@/services/models'
import { AuthContext } from '@/hooks/useAuth'
import { ConfirmationModal } from '@/components/ConfirmationModal'
import { showToast } from '@/components/ToastNotification'
import { TimeChip } from '@/components/resource/TimeChip'
import { TakeoverDialog } from '@/components/TakeoverDialog'
import {
  ACTIVITY_EMPTY_SENTENCE,
  ACTIVITY_FETCH_LIMIT,
  mapActivityEntries,
  NO_CHANGES_MADE,
  RECENT_ACTIVITY_LABEL,
  type ActivityFeedItem,
} from './connectionActivity'

// ─────────────────────────────────────────────────────────────────────────────
// Pinned display words and sentences. The sync headline names its scope
// (product correction 4): bare "Synced" never renders for any connection
// whose full owned scope was not verified.
// ─────────────────────────────────────────────────────────────────────────────

export const HEADLINE_CONNECTION_SYNCED = 'Connection synced'
export const HEADLINE_ADDON_LABELS_SYNCED = 'Addon labels synced'
export const HEADLINE_ADDON_LABELS_OUT_OF_SYNC = 'Addon labels out of sync'
export const HEADLINE_OUT_OF_SYNC = 'Out of sync'
export const HEADLINE_OUT_OF_SYNC_APPROVAL = 'Out of sync — approval required'
export const HEADLINE_OUT_OF_SYNC_CANNOT_RESTORE = 'Out of sync — cannot be restored from Git'
export const HEADLINE_BLOCKED = 'Blocked'
export const HEADLINE_VERIFICATION_INCOMPLETE = 'Verification incomplete'
export const HEADLINE_EKS_PARTIAL = 'Configuration matches; credential content not compared'
export const HEADLINE_UNKNOWN_CHECK_FAILED = 'Unknown — check failed'
export const HEADLINE_NOT_CHECKED_YET = 'Not checked yet'

/** The qualifier beside a partial verification — part of what Sharko owns was not compared. */
export const QUALIFIER_CREDENTIAL_NOT_COMPARED = 'Credential content not compared'
/** The qualifier for a self-managed connection (product ruling 2). */
export const QUALIFIER_SELF_MANAGED = 'Connection data managed outside Sharko'
/** The qualifier for a legacy inline connection (product correction 3, exact ruled sentence). */
export const QUALIFIER_LEGACY_INLINE =
  "The credential exists only in the live Secret. Sharko can check the connection's health and some non-sensitive fields, but it cannot verify or rebuild the complete connection from Git."

/** The three health words — independent of sync in every state. Showing Connected beside an unverifiable git state is correct, not a contradiction. */
export const HEALTH_WORDS: Record<string, string> = {
  connected: 'Connected',
  unavailable: 'Unavailable',
  not_checked: 'Not checked',
}

/** The calm access sentence a viewer sees — the reconciliation read needs operator access. */
export const RECONCILIATION_NEEDS_OPERATOR = 'Reading this connection needs operator access.'

/** The fixed redaction text for sensitive drift, both sides, every time. */
const REDACTED_TEXT = '<redacted>'

/** What stays untouched whenever anything on this page writes — a scope statement, not a promise of any automatic fix. */
export const PLAN_UNTOUCHED_SENTENCE = 'Foreign labels, other annotations and other data keys are never touched.'

/** The documented migration path for a legacy inline credential (Story 4 publishes the page at this stable path). */
export const MIGRATE_CREDENTIALS_DOC_PATH = '/operator/migrate-inline-credentials/'
// The site serves under /en/latest/ (readthedocs language/version prefix) —
// the unprefixed form of a published page hard-404s. Same prefix every other
// UI doc link uses (AuditViewer, ClusterIdentityPanel,
// PerClusterAddonOverridesEditor).
export const MIGRATE_CREDENTIALS_DOC_URL = `https://sharko.readthedocs.io/en/latest${MIGRATE_CREDENTIALS_DOC_PATH}`
export const MIGRATE_CREDENTIALS_LINK_LABEL = 'How to move this credential to a supported provider'

/** Existing action names — unchanged by this redesign. */
export const ACTION_REPAIR_CONNECTION = 'Repair connection'
export const ACTION_REFRESH_EKS_CONNECTION = 'Refresh EKS connection'
export const ACTION_SYNC_ADDON_LABELS = 'Sync addon labels'
export const ACTION_TAKE_OWNERSHIP = 'Take ownership'

// Human labels for the condition ids (ruling 5's fixed vocabulary). Only ids
// present in the server's conditions[] array ever render — no invented steps.
const CONDITION_LABELS: Record<string, string> = {
  git_definition: 'Git definition',
  credential_reference: 'Credential reference',
  ownership: 'Ownership',
  live_secret: 'Live Secret',
  comparison: 'Comparison',
  argocd_connection: 'ArgoCD connection',
  approval: 'Approval',
}

// ─────────────────────────────────────────────────────────────────────────────
// Display mapping — page-state matrix v3, row by row. Pure functions,
// exported for the matrix tests.
// ─────────────────────────────────────────────────────────────────────────────

function liveSecretMissing(view: ConnectionReconciliation): boolean {
  return view.conditions.some((c) => c.id === 'live_secret' && c.status === 'blocked')
}

/** The sync headline — the page's leading word, scope-named per matrix v3. */
export function reconSyncHeadline(view: ConnectionReconciliation): string {
  const s = view.sync
  switch (s.state) {
    case 'blocked':
      return HEADLINE_BLOCKED
    case 'synced':
      // The server only ever sends synced at full verification of the owned
      // scope; managed_scope names which scope that is.
      return view.management_mode === 'self_managed' ? HEADLINE_ADDON_LABELS_SYNCED : HEADLINE_CONNECTION_SYNCED
    case 'out_of_sync':
      if (view.management_mode === 'legacy_inline' && liveSecretMissing(view)) return HEADLINE_OUT_OF_SYNC_CANNOT_RESTORE
      if (s.approval_required) return HEADLINE_OUT_OF_SYNC_APPROVAL
      if (view.management_mode === 'self_managed') return HEADLINE_ADDON_LABELS_OUT_OF_SYNC
      return HEADLINE_OUT_OF_SYNC
    default: {
      // unknown — four honest shades, split by what the server recorded.
      if (!s.checked_at) return HEADLINE_NOT_CHECKED_YET
      if (s.verification_scope === 'none') return HEADLINE_UNKNOWN_CHECK_FAILED
      if (view.management_mode === 'legacy_inline') return HEADLINE_VERIFICATION_INCOMPLETE
      // Sharko-managed, checked, partial: everything compared matched, the
      // credential content could not be (EKS, an unreadable backend).
      return HEADLINE_EKS_PARTIAL
    }
  }
}

/** The scope qualifier beside the headline — rendered whenever the verification is narrower than the full connection. */
export function reconSyncQualifier(view: ConnectionReconciliation): string | null {
  if (view.management_mode === 'legacy_inline') return QUALIFIER_LEGACY_INLINE
  if (view.management_mode === 'self_managed') return QUALIFIER_SELF_MANAGED
  if (view.sync.verification_scope === 'partial' || view.sync.verification_scope === 'labels_only') {
    // Already the headline's own words on the clean-EKS row — don't say it twice.
    if (reconSyncHeadline(view) === HEADLINE_EKS_PARTIAL) return null
    return QUALIFIER_CREDENTIAL_NOT_COMPARED
  }
  return null
}

/**
 * Which condition an offered action renders beside (the section it
 * resolves). null → the action renders in the plan section instead.
 * sync_addon_labels is special-cased: it resolves the addon-assignments
 * drift group and renders there.
 */
export function actionTargetConditionId(view: ConnectionReconciliation): string | null {
  const has = (id: string) => view.conditions.some((c) => c.id === id)
  switch (view.plan.action) {
    case 'repair_connection':
      if (has('approval')) return 'approval'
      if (has('comparison')) return 'comparison'
      if (has('credential_reference')) return 'credential_reference'
      return null
    case 'take_over':
      return has('ownership') ? 'ownership' : null
    case 'migrate_credentials':
      return has('credential_reference') ? 'credential_reference' : null
    default:
      return null
  }
}

/** Human label first, raw path secondary — the drift tables lead with this. */
export function humanFieldLabel(path: string): string {
  switch (path) {
    case 'data.server':
      return 'API server address'
    case 'data.name':
      return 'Connection name'
    case 'data.config':
      return 'Credential material'
    case 'metadata.name':
      return 'Secret name'
    case 'metadata.namespace':
      return 'Namespace'
    case 'type':
      return 'Secret type'
  }
  const label = path.match(/^metadata\.labels\[(.+)\]$/)
  if (label) return `Label "${label[1]}"`
  const ann = path.match(/^metadata\.annotations\[(.+)\]$/)
  if (ann) return `Annotation "${ann[1]}"`
  return path
}

function shortSha(sha?: string): string {
  return sha ? sha.slice(0, 7) : ''
}

// ─────────────────────────────────────────────────────────────────────────────
// Small building blocks
// ─────────────────────────────────────────────────────────────────────────────

function ActionButton({
  onClick,
  label,
  loading,
  loadingLabel,
  icon: Icon,
  testId,
  strong,
}: {
  onClick: () => void
  label: string
  loading?: boolean
  /** Ruling 4: the request-local word the control itself shows while its request is in flight. */
  loadingLabel?: string
  icon: typeof RefreshCw
  testId: string
  strong?: boolean
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={loading}
      data-testid={testId}
      className={`inline-flex items-center gap-1.5 rounded-lg border px-3 py-1.5 text-sm font-medium disabled:cursor-not-allowed disabled:opacity-70 ${
        strong
          ? 'border-transparent bg-teal-600 text-white hover:bg-teal-700 dark:bg-teal-700 dark:hover:bg-teal-600'
          : 'border-[#6aade0] bg-white text-[#0a3a5a] hover:bg-[#e0f0ff] dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600'
      }`}
    >
      {loading ? <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true" /> : <Icon className="h-3.5 w-3.5" aria-hidden="true" />}
      {loading && loadingLabel ? loadingLabel : label}
    </button>
  )
}

function conditionIcon(status: string) {
  if (status === 'ok') return <CheckCircle className="h-4 w-4 shrink-0 text-green-600 dark:text-green-500" aria-hidden="true" />
  if (status === 'blocked') return <OctagonX className="h-4 w-4 shrink-0 text-red-600 dark:text-red-500" aria-hidden="true" />
  return <AlertTriangle className="h-4 w-4 shrink-0 text-amber-600 dark:text-amber-500" aria-hidden="true" />
}

function DriftTable({
  entries,
  sensitive,
  testId,
}: {
  entries: { path: string; status: string; expected?: string; live?: string }[]
  /** Sensitive rows have no expected/live in the response — both sides render the fixed REDACTED_TEXT. */
  sensitive?: boolean
  testId: string
}) {
  return (
    <table className="w-full text-left text-sm" data-testid={testId}>
      <thead>
        <tr className="text-[11px] uppercase tracking-wide text-[#5a8aaa] dark:text-gray-500">
          <th className="py-1 pr-3 font-medium">Field</th>
          <th className="py-1 pr-3 font-medium">Expected</th>
          <th className="py-1 pr-3 font-medium">Live</th>
          <th className="py-1 font-medium">Status</th>
        </tr>
      </thead>
      <tbody className="divide-y divide-border">
        {entries.map((entry, idx) => (
          <tr key={`${entry.path}-${idx}`}>
            <td className="py-1.5 pr-3">
              <div className="text-[#0a2a4a] dark:text-gray-200">{humanFieldLabel(entry.path)}</div>
              <div className="break-all font-mono text-[11px] text-[#5a8aaa] dark:text-gray-500">{entry.path}</div>
            </td>
            <td className="py-1.5 pr-3 font-mono text-xs text-[#0a2a4a] dark:text-gray-200">
              {sensitive ? REDACTED_TEXT : (entry.expected ?? '—')}
            </td>
            <td className="py-1.5 pr-3 font-mono text-xs text-[#0a2a4a] dark:text-gray-200">
              {sensitive ? REDACTED_TEXT : (entry.live ?? '—')}
            </td>
            <td className="py-1.5 text-xs font-medium text-amber-700 dark:text-amber-400">{entry.status}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// The view
// ─────────────────────────────────────────────────────────────────────────────

export function ConnectionReconciliationView({
  cluster,
  onRequestSync,
  onChanged,
  technicalEvidenceExtra,
}: {
  cluster: string
  /** Opens the parent's existing "Sync addon labels" confirm flow. */
  onRequestSync: () => void
  /** Parent refresh after any write (repair, takeover). */
  onChanged: () => void
  /** The redacted-YAML section, rendered by the parent (it owns the live read) — shown inside collapsed Technical evidence only. */
  technicalEvidenceExtra?: ReactNode
}) {
  const auth = useContext(AuthContext)
  const isAdmin = auth?.isAdmin === true
  // The reconciliation read is operator-or-higher on the server; a viewer
  // gets the calm access sentence and no request is fired at all.
  const canRead = auth?.role === 'admin' || auth?.role === 'operator'

  const [view, setView] = useState<ConnectionReconciliation | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [attempt, setAttempt] = useState(0)

  const [activity, setActivity] = useState<ActivityFeedItem[]>([])

  const [conditionsExpanded, setConditionsExpanded] = useState(false)
  const [evidenceOpen, setEvidenceOpen] = useState(false)

  const [showRepairConfirm, setShowRepairConfirm] = useState(false)
  const [repairInProgress, setRepairInProgress] = useState(false)
  const [takeoverOpen, setTakeoverOpen] = useState(false)

  useEffect(() => {
    if (!canRead) return
    let cancelled = false
    setLoading(true)
    setError(null)
    api
      .getConnectionReconciliation(cluster)
      .then((result) => {
        if (!cancelled) setView(result)
      })
      .catch((err) => {
        if (!cancelled) setError(err instanceof Error ? err.message : 'The check failed.')
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [cluster, attempt, canRead])

  useEffect(() => {
    if (!canRead) return
    let cancelled = false
    // The ring-sized budget, not the server's 50-entry default — routine
    // reads must never drown the lifecycle events out of the response (see
    // ACTIVITY_FETCH_LIMIT).
    fetchAuditLog({ cluster, limit: ACTIVITY_FETCH_LIMIT })
      .then((result) => {
        if (!cancelled) setActivity(mapActivityEntries((result.entries ?? []) as AuditEntry[]))
      })
      .catch((err) => {
        // The feed is supplemental — a failed audit read never breaks the page.
        console.warn('Failed to fetch recent activity:', err)
      })
    return () => {
      cancelled = true
    }
  }, [cluster, attempt, canRead])

  const recheck = () => setAttempt((a) => a + 1)

  const handleRepairConfirm = async () => {
    if (!isAdmin || !view || !view.plan.reviewed_commit) return
    setShowRepairConfirm(false)
    setRepairInProgress(true)
    try {
      // The repair echoes plan.reviewed_commit — the commit the check on
      // screen was pinned to (the existing R3-4 rule), exactly as the old
      // flow sent compared_commit.
      const result = await api.repairConnection(cluster, view.plan.reviewed_commit)
      showToast(result.message, 'success')
      recheck()
      onChanged()
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        // The branch moved, or git cannot say which commit it is on. The
        // server's sentence is deliberate — show it unchanged, and never
        // auto-retry: the person decides when to check again.
        showToast(err.message, 'info')
      } else {
        showToast(err instanceof Error ? err.message : 'The repair failed.', 'error')
      }
    } finally {
      setRepairInProgress(false)
    }
  }

  if (!canRead) {
    return (
      <p className="text-sm text-[#2a5a7a] dark:text-gray-400" data-testid="recon-needs-operator">
        {RECONCILIATION_NEEDS_OPERATOR}
      </p>
    )
  }

  const checkLabel = view?.sync.checked_at ? 'Check again' : 'Check now'
  const checkButton = (
    <ActionButton onClick={recheck} loading={loading} loadingLabel="Checking…" icon={RefreshCw} label={checkLabel} testId="recon-check" />
  )

  if (loading && !view) {
    return (
      <div className="space-y-2" data-testid="recon-loading">
        <p className="text-sm text-[#2a5a7a] dark:text-gray-400">Checking the connection…</p>
      </div>
    )
  }

  if (error && !view) {
    return (
      <div className="space-y-2" data-testid="recon-error">
        <p className="text-sm text-red-700 dark:text-red-400">{error}</p>
        <ActionButton onClick={recheck} loading={loading} loadingLabel="Checking…" icon={RefreshCw} label="Try again" testId="recon-retry" />
      </div>
    )
  }

  if (!view) return null

  const headline = reconSyncHeadline(view)
  const qualifier = reconSyncQualifier(view)
  const checkFailed = view.sync.state === 'unknown' && view.sync.verification_scope === 'none' && Boolean(view.sync.checked_at)
  const isEKS = view.definition.credential_source_type === CREDS_SOURCE_EKS_TOKEN
  const repairLabel = isEKS ? ACTION_REFRESH_EKS_CONNECTION : ACTION_REPAIR_CONNECTION

  const repairOffered = view.plan.action === 'repair_connection' && isAdmin && Boolean(view.plan.reviewed_commit)
  const targetConditionId = actionTargetConditionId(view)

  // The one contextual action button, built once so every placement renders
  // the same control.
  const actionButtonFor = (action: string): ReactNode => {
    switch (action) {
      case 'repair_connection':
        return repairOffered ? (
          <ActionButton
            onClick={() => setShowRepairConfirm(true)}
            loading={repairInProgress}
            loadingLabel="Repairing…"
            icon={Wrench}
            label={repairLabel}
            testId="recon-action-repair"
            strong={view.sync.state === 'out_of_sync'}
          />
        ) : null
      case 'sync_addon_labels':
        return (
          <ActionButton onClick={onRequestSync} icon={RotateCcw} label={ACTION_SYNC_ADDON_LABELS} testId="recon-action-sync" strong />
        )
      case 'take_over':
        // The takeover endpoint is admin-only (authz cluster.takeover:
        // RoleAdmin) — same rule as repair: an operator must not see a
        // button that could only ever come back 403. Absent, never greyed
        // out.
        return isAdmin ? (
          <ActionButton onClick={() => setTakeoverOpen(true)} icon={Wrench} label={ACTION_TAKE_OWNERSHIP} testId="recon-action-takeover" />
        ) : null
      case 'migrate_credentials':
        return (
          <a
            href={MIGRATE_CREDENTIALS_DOC_URL}
            target="_blank"
            rel="noreferrer"
            data-testid="recon-action-migrate"
            className="inline-block text-sm font-medium text-teal-700 hover:underline dark:text-teal-400"
          >
            {MIGRATE_CREDENTIALS_LINK_LABEL}
          </a>
        )
      default:
        return null
    }
  }

  // Where the offered action renders: beside its condition when that
  // condition exists; the addon-labels drift group for the label sync; the
  // plan section as the fallback home. Repair withheld (non-admin) renders
  // nowhere — absent, never greyed out.
  const syncActionInDrift = view.plan.action === 'sync_addon_labels' && view.drift.addon_labels.length > 0
  const actionInPlanSection =
    view.plan.action !== 'none' &&
    !syncActionInDrift &&
    (targetConditionId === null || !view.conditions.some((c) => c.id === targetConditionId))

  const allConditionsOK = view.conditions.every((c) => c.status === 'ok')
  const conditionsCompact = allConditionsOK && !conditionsExpanded && targetConditionId === null

  const hasDrift =
    view.drift.connection_configuration.length > 0 ||
    view.drift.credential_material.length > 0 ||
    view.drift.addon_labels.length > 0 ||
    view.drift.not_checked.length > 0

  const hasPlanContent =
    Boolean(view.plan.automatic) || Boolean(view.plan.requires_approval) || actionInPlanSection

  const healthWord = HEALTH_WORDS[view.health.state] ?? HEALTH_WORDS.not_checked

  const desiredSha = view.definition.desired_revision
  const appliedSha = view.definition.applied_revision

  return (
    <div className="space-y-4" data-testid="recon-view">
      {/* ── 1. Resource identity: the desired git definition ─────────────── */}
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 space-y-0.5 text-sm text-[#2a5a7a] dark:text-gray-300" data-testid="recon-identity">
          {view.definition.file && (
            <div>
              Defined in <span className="font-mono text-[13px]">{view.definition.file}</span>
              {view.definition.branch && (
                <>
                  {' '}
                  on <span className="font-mono text-[13px]">{view.definition.branch}</span>
                </>
              )}
            </div>
          )}
          <div>
            Desired revision:{' '}
            {desiredSha ? (
              <span className="font-mono text-[13px]" title={`Full commit: ${desiredSha}`} data-testid="recon-desired-revision">
                {shortSha(desiredSha)}
              </span>
            ) : (
              <span data-testid="recon-desired-revision">unknown</span>
            )}
            {' · '}Applied revision:{' '}
            {appliedSha ? (
              <span className="font-mono text-[13px]" title={`Full commit: ${appliedSha}`} data-testid="recon-applied-revision">
                {shortSha(appliedSha)}
              </span>
            ) : (
              <span data-testid="recon-applied-revision">not recorded</span>
            )}
          </div>
        </div>
        <div className="flex items-center gap-2">{checkButton}</div>
      </div>

      {/* A re-check that failed leaves the last good result on screen —
          with the failure said plainly, never silently. */}
      {error && (
        <p className="text-sm text-red-700 dark:text-red-400" data-testid="recon-refresh-error">
          {error}
        </p>
      )}

      {/* The page's model sentence — once, small. Not rendered when the
          check itself failed (review finding F7): a mode derived before
          classification is cosmetic, not authoritative. */}
      {!checkFailed && (
        <p className="text-xs text-[#3a6a8a] dark:text-gray-500" data-testid="recon-mode-statement">
          {view.mode_statement}
        </p>
      )}

      {/* ── 2. Reconciliation summary ────────────────────────────────────── */}
      <div className="space-y-1.5 rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:ring-gray-700 dark:bg-gray-800" role="status" data-testid="recon-summary">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <span className="text-xl font-semibold text-[#0a2a4a] dark:text-gray-100" data-testid="recon-sync-headline">
            {headline}
          </span>
          <span className="text-sm text-[#2a5a7a] dark:text-gray-400" data-testid="recon-health">
            ArgoCD connection: <span className="font-medium">{healthWord}</span>
          </span>
        </div>
        {qualifier && (
          <p className="text-sm text-[#2a5a7a] dark:text-gray-400" data-testid="recon-sync-qualifier">
            {qualifier}
          </p>
        )}
        {view.sync.reason && (
          <p className="text-sm text-[#2a5a7a] dark:text-gray-300" data-testid="recon-sync-reason">
            {view.sync.reason}
          </p>
        )}
        {view.health.state === 'unavailable' && view.health.message && (
          <p className="text-sm text-red-700 dark:text-red-400" data-testid="recon-health-message">
            {view.health.message}
          </p>
        )}
        <p className="text-[13px] text-[#5a8aaa] dark:text-gray-500" data-testid="recon-timestamps">
          {view.sync.checked_at ? (
            <>
              Checked <TimeChip iso={view.sync.checked_at} />
            </>
          ) : (
            <>Not checked yet</>
          )}
          {view.sync.last_successful_application && (
            <>
              {' · '}Last successful application <TimeChip iso={view.sync.last_successful_application} />
            </>
          )}
        </p>
      </div>

      {/* ── 3. Conditions — compact when all is routine, cards when not ──── */}
      <div data-testid="recon-conditions">
        {conditionsCompact ? (
          <button
            type="button"
            onClick={() => setConditionsExpanded(true)}
            aria-expanded={false}
            data-testid="recon-conditions-compact"
            className="inline-flex items-center gap-1.5 text-sm text-[#2a5a7a] hover:underline dark:text-gray-400"
          >
            <CheckCircle className="h-4 w-4 text-green-600 dark:text-green-500" aria-hidden="true" />
            All {view.conditions.length} checks passed.
            <ChevronRight className="h-3.5 w-3.5" aria-hidden="true" />
          </button>
        ) : (
          <div className="space-y-2">
            {allConditionsOK && (
              <button
                type="button"
                onClick={() => setConditionsExpanded(false)}
                aria-expanded={true}
                data-testid="recon-conditions-collapse"
                className="inline-flex items-center gap-1.5 text-sm text-[#2a5a7a] hover:underline dark:text-gray-400"
              >
                All {view.conditions.length} checks passed.
                <ChevronDown className="h-3.5 w-3.5" aria-hidden="true" />
              </button>
            )}
            {view.conditions.map((cond) => (
              <div
                key={cond.id}
                data-testid={`recon-condition-${cond.id}`}
                className={`flex flex-wrap items-center justify-between gap-2 rounded-md border border-border bg-card p-2.5 ${
                  cond.status === 'ok' ? '' : 'ring-1 ring-amber-300 dark:ring-amber-700'
                }`}
              >
                <div className="flex min-w-0 items-start gap-2">
                  {conditionIcon(cond.status)}
                  <div className="min-w-0">
                    <span className="text-sm font-medium text-[#0a2a4a] dark:text-gray-200">
                      {CONDITION_LABELS[cond.id] ?? cond.id.replace(/_/g, ' ')}
                    </span>
                    <p className="text-sm text-[#2a5a7a] dark:text-gray-400">{cond.detail}</p>
                  </div>
                </div>
                {cond.id === targetConditionId && actionButtonFor(view.plan.action)}
              </div>
            ))}
          </div>
        )}
      </div>

      {/* ── 4. Drift, grouped by managed domain ──────────────────────────── */}
      {hasDrift && (
        <div className="space-y-3" data-testid="recon-drift">
          {view.drift.connection_configuration.length > 0 && (
            <div className="rounded-md border border-border bg-card p-3">
              <h3 className="mb-2 text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Connection configuration</h3>
              <DriftTable entries={view.drift.connection_configuration} testId="recon-drift-config" />
            </div>
          )}
          {view.drift.credential_material.length > 0 && (
            <div className="rounded-md border border-border bg-card p-3">
              <h3 className="mb-2 text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Credential material</h3>
              <DriftTable entries={view.drift.credential_material} sensitive testId="recon-drift-credential" />
            </div>
          )}
          {view.drift.addon_labels.length > 0 && (
            <div className="rounded-md border border-border bg-card p-3">
              <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
                <h3 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Addon assignments</h3>
                {syncActionInDrift && actionButtonFor('sync_addon_labels')}
              </div>
              <DriftTable entries={view.drift.addon_labels} testId="recon-drift-labels" />
            </div>
          )}
          {view.drift.not_checked.length > 0 && (
            <div className="rounded-md border border-border bg-card p-3">
              <h3 className="mb-2 text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Not checked</h3>
              <table className="w-full text-left text-sm" data-testid="recon-drift-notchecked">
                <thead>
                  <tr className="text-[11px] uppercase tracking-wide text-[#5a8aaa] dark:text-gray-500">
                    <th className="py-1 pr-3 font-medium">Field</th>
                    <th className="py-1 font-medium">Reason</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {view.drift.not_checked.map((nc, idx) => (
                    <tr key={`${nc.path}-${idx}`}>
                      <td className="py-1.5 pr-3">
                        <div className="text-[#0a2a4a] dark:text-gray-200">{humanFieldLabel(nc.path)}</div>
                        <div className="break-all font-mono text-[11px] text-[#5a8aaa] dark:text-gray-500">{nc.path}</div>
                      </td>
                      <td className="py-1.5 text-xs text-[#2a5a7a] dark:text-gray-400">{nc.reason}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      {/* ── 5. Reconciliation plan — the server's sentences, verbatim ────── */}
      {hasPlanContent && (
        <div className="space-y-1.5 rounded-md border border-border bg-card p-3" data-testid="recon-plan">
          <h3 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">What happens next</h3>
          {view.plan.automatic && (
            <p className="text-sm text-[#2a5a7a] dark:text-gray-300" data-testid="recon-plan-automatic">
              {view.plan.automatic}
            </p>
          )}
          {view.plan.requires_approval && (
            <p className="text-sm text-[#2a5a7a] dark:text-gray-300" data-testid="recon-plan-approval">
              {view.plan.requires_approval}
            </p>
          )}
          <p className="text-xs text-[#3a6a8a] dark:text-gray-500" data-testid="recon-plan-untouched">
            {PLAN_UNTOUCHED_SENTENCE}
          </p>
          {actionInPlanSection && <div data-testid="recon-plan-action">{actionButtonFor(view.plan.action)}</div>}
        </div>
      )}

      {/* ── 7. Technical evidence — collapsed by default ─────────────────── */}
      <div data-testid="recon-technical-evidence">
        <button
          type="button"
          onClick={() => setEvidenceOpen((open) => !open)}
          aria-expanded={evidenceOpen}
          data-testid="recon-technical-evidence-toggle"
          className="inline-flex items-center gap-1.5 text-sm font-medium text-teal-700 hover:underline dark:text-teal-400"
        >
          {evidenceOpen ? <ChevronDown className="h-3.5 w-3.5" aria-hidden="true" /> : <ChevronRight className="h-3.5 w-3.5" aria-hidden="true" />}
          Technical evidence
        </button>
        {evidenceOpen && (
          <div className="mt-2 space-y-3" data-testid="recon-technical-evidence-body">
            <div className="space-y-0.5 text-xs text-[#3a6a8a] dark:text-gray-500">
              {view.definition.file && (
                <div>
                  File: <span className="font-mono">{view.definition.file}</span>
                </div>
              )}
              {view.definition.branch && (
                <div>
                  Branch: <span className="font-mono">{view.definition.branch}</span>
                </div>
              )}
              {desiredSha && (
                <div>
                  Desired revision: <span className="font-mono break-all">{desiredSha}</span>
                </div>
              )}
              {appliedSha && (
                <div>
                  Applied revision: <span className="font-mono break-all">{appliedSha}</span>
                </div>
              )}
              {view.definition.credential_source_type && (
                <div>
                  Credential source type: <span className="font-mono">{view.definition.credential_source_type}</span>
                </div>
              )}
              <div>
                Managed scope: <span className="font-mono">{view.managed_scope}</span> · Verification scope:{' '}
                <span className="font-mono">{view.sync.verification_scope}</span>
              </div>
            </div>
            {technicalEvidenceExtra}
          </div>
        )}
      </div>

      {/* ── Recent activity (Story 3, ruling 6) — lifecycle transitions the
          in-memory ring still holds; never presented as durable history. ── */}
      <div data-testid="recon-activity">
        <h3 className="mb-2 text-sm font-semibold text-[#0a2a4a] dark:text-gray-100" data-testid="recon-activity-label">
          {RECENT_ACTIVITY_LABEL}
        </h3>
        {activity.length === 0 ? (
          <p className="text-sm text-[#5a8aaa] dark:text-gray-500" data-testid="recon-activity-empty">
            {ACTIVITY_EMPTY_SENTENCE}
          </p>
        ) : (
          <div className="space-y-2">
            {activity.map((item, idx) => (
              <div key={idx} className="rounded-md border border-border bg-card p-2 text-xs" data-testid={`recon-activity-entry-${idx}`}>
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="font-medium text-[#0a2a4a] dark:text-gray-200">{item.title}</span>
                  <span className="text-[#5a8aaa] dark:text-gray-500">
                    <TimeChip iso={item.timestamp} />
                  </span>
                </div>
                <p className="mt-1 text-[#2a5a7a] dark:text-gray-400">
                  {item.actor}
                  {item.door && <> · via {item.door}</>}
                  {item.outcome && <> · {item.outcome}</>}
                  {item.readOnly && <> · {NO_CHANGES_MADE}</>}
                </p>
              </div>
            ))}
          </div>
        )}
        <Link
          to={`/audit?cluster=${encodeURIComponent(cluster)}`}
          data-testid="recon-view-audit-log"
          className="mt-2 inline-block text-sm text-teal-700 hover:underline dark:text-teal-400"
        >
          View audit log
        </Link>
      </div>

      {/* The repair confirm — the existing guarded flow: reviewed commit
          echoed back, desired revision and exact non-sensitive scopes shown
          (ruling 3). */}
      <ConfirmationModal
        open={showRepairConfirm}
        onClose={() => setShowRepairConfirm(false)}
        onConfirm={handleRepairConfirm}
        title={isEKS ? `Refresh EKS connection for "${cluster}"?` : `Repair connection for "${cluster}"?`}
        description={
          (isEKS
            ? 'This refreshes the short-lived sign-in token for this EKS connection and applies the reviewed git definition to the live connection Secret.'
            : "This applies the reviewed git definition to the live connection Secret, using this cluster's configured credentials source.") +
          ` Desired revision: ${shortSha(view.definition.desired_revision) || shortSha(view.plan.reviewed_commit)}.` +
          (view.plan.action_scopes.length > 0 ? ` It changes: ${view.plan.action_scopes.join(', ')}.` : '') +
          ` ${PLAN_UNTOUCHED_SENTENCE}`
        }
        confirmText={isEKS ? 'Refresh connection' : 'Repair connection'}
        loading={repairInProgress}
      />

      {/* The existing takeover entry point, mounted only for the one state
          that offers it (foreign ownership) — and only for the admin who can
          actually use it. */}
      {isAdmin && view.plan.action === 'take_over' && (
        <TakeoverDialog
          clusterName={cluster}
          open={takeoverOpen}
          onClose={() => setTakeoverOpen(false)}
          onDone={() => {
            setTakeoverOpen(false)
            recheck()
            onChanged()
          }}
        />
      )}
    </div>
  )
}

export default ConnectionReconciliationView

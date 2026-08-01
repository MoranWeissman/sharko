import { useState, useEffect, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  ShieldAlert,
  Clock,
  ExternalLink,
  X,
  Loader2,
  RefreshCw,
} from 'lucide-react'
import { fetchAuditLog } from '@/services/api'
import type { AuditEntry } from '@/services/models'

// UI title reads "GitOps corrections", not "Drift Alerts" (dashboard UX
// review 2026-08-01: "drift" was doing double duty — this panel's
// audit-derived orphan/self-heal events AND the Dashboard's addon
// version-drift rows both called themselves "drift"). This is a display
// name only; event names, the audit API, and component/file names are
// unchanged.
const DRIFT_EVENTS = ['orphan_detected', 'orphan_deleted_after_grace_period', 'drift_detected']
const POLL_INTERVAL = 30_000
const GRACE_PERIOD_MS = 6 * 60 * 1000 // 2 reconciler cycles ~ 6 minutes
const RETENTION_HOURS = 24

interface DriftAlert {
  id: string
  timestamp: string
  event: string
  resource: string
  status: 'pending' | 'resolved'
}

function isDriftEvent(entry: AuditEntry): boolean {
  return DRIFT_EVENTS.includes(entry.event)
}

function deriveDriftAlerts(entries: AuditEntry[]): DriftAlert[] {
  const now = Date.now()
  const cutoff = now - RETENTION_HOURS * 60 * 60 * 1000

  return entries
    .filter((e) => isDriftEvent(e) && new Date(e.timestamp).getTime() > cutoff)
    .map((e) => {
      const isPending =
        e.event === 'orphan_detected' &&
        now - new Date(e.timestamp).getTime() < GRACE_PERIOD_MS
      return {
        id: e.id,
        timestamp: e.timestamp,
        event: e.event,
        resource: e.resource,
        status: isPending ? 'pending' : 'resolved',
      } satisfies DriftAlert
    })
}

function timeAgo(timestamp: string): string {
  const secs = Math.floor((Date.now() - new Date(timestamp).getTime()) / 1000)
  if (secs < 60) return 'just now'
  if (secs < 3600) return `${Math.floor(secs / 60)}m ago`
  if (secs < 86400) return `${Math.floor(secs / 3600)}h ago`
  return `${Math.floor(secs / 86400)}d ago`
}

function countdown(timestamp: string): string {
  const remaining = GRACE_PERIOD_MS - (Date.now() - new Date(timestamp).getTime())
  if (remaining <= 0) return 'imminent'
  const mins = Math.floor(remaining / 60_000)
  const secs = Math.floor((remaining % 60_000) / 1000)
  return `${mins}m ${secs}s`
}

function eventLabel(event: string): string {
  switch (event) {
    case 'orphan_detected':
      return 'Orphan Detected'
    case 'orphan_deleted_after_grace_period':
      return 'Orphan Cleaned Up'
    case 'drift_detected':
      return 'Drift Detected'
    default:
      return event
  }
}

function statusBadge(alert: DriftAlert) {
  if (alert.status === 'pending') {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-amber-50 px-2 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-400">
        <span className="inline-block h-2 w-2 rounded-full bg-amber-500 animate-pulse" />
        Pending cleanup ({countdown(alert.timestamp)})
      </span>
    )
  }
  if (alert.event === 'orphan_deleted_after_grace_period') {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-green-50 px-2 py-0.5 text-xs font-medium text-green-700 dark:bg-green-900/30 dark:text-green-400">
        <span className="inline-block h-2 w-2 rounded-full bg-green-500" />
        Cleaned up
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1 rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">
      <span className="inline-block h-2 w-2 rounded-full bg-muted-foreground" />
      Resolved
    </span>
  )
}

export function DriftAlertsPanel() {
  const navigate = useNavigate()
  const [alerts, setAlerts] = useState<DriftAlert[]>([])
  const [dismissed, setDismissed] = useState<Set<string>>(new Set())
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const fetchAlerts = useCallback(async (showLoading = false) => {
    try {
      if (showLoading) setLoading(true)
      setError(null)
      // Fetch recent audit entries — the backend filters by source=reconciler
      // We request a broad set and filter client-side for drift events
      const result = await fetchAuditLog({
        source: 'reconciler',
        limit: 100,
      })
      const derived = deriveDriftAlerts(result.entries ?? [])
      setAlerts(derived)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to load drift alerts')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void fetchAlerts(true)
    const interval = setInterval(() => {
      void fetchAlerts(false)
    }, POLL_INTERVAL)
    return () => clearInterval(interval)
  }, [fetchAlerts])

  const handleDismiss = (id: string) => {
    setDismissed((prev) => new Set(prev).add(id))
  }

  const visibleAlerts = alerts.filter((a) => !dismissed.has(a.id))

  // Don't render empty panel
  if (!loading && !error && visibleAlerts.length === 0) {
    return null
  }

  if (loading) {
    return (
      <div className="rounded-xl ring-2 ring-amber-300 bg-amber-50/50 p-5 shadow-sm dark:ring-amber-700 dark:bg-amber-900/10">
        <div className="flex items-center gap-2 mb-3">
          <ShieldAlert className="h-4 w-4 text-amber-500" />
          <h3 className="text-base font-semibold text-card-foreground">GitOps corrections</h3>
        </div>
        <div className="flex items-center justify-center py-6 text-muted-foreground">
          <Loader2 className="mr-2 h-4 w-4 animate-spin" />
          <span className="text-xs">Loading drift alerts...</span>
        </div>
      </div>
    )
  }

  if (error) {
    return (
      <div className="rounded-xl ring-2 ring-amber-300 bg-amber-50/50 p-5 shadow-sm dark:ring-amber-700 dark:bg-amber-900/10">
        <div className="flex items-center gap-2 mb-3">
          <ShieldAlert className="h-4 w-4 text-amber-500" />
          <h3 className="text-base font-semibold text-card-foreground">GitOps corrections</h3>
        </div>
        <p className="text-sm text-red-600 dark:text-red-400">{error}</p>
      </div>
    )
  }

  const pendingCount = visibleAlerts.filter((a) => a.status === 'pending').length

  return (
    <div className="rounded-xl ring-2 ring-amber-300 bg-amber-50/50 p-5 shadow-sm dark:ring-amber-700 dark:bg-amber-900/10">
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2">
          <ShieldAlert className="h-4 w-4 text-amber-500" />
          <h3 className="text-base font-semibold text-card-foreground">GitOps corrections</h3>
          {pendingCount > 0 && (
            <span className="inline-flex items-center justify-center rounded-full bg-amber-100 px-2 py-0.5 text-xs font-semibold text-amber-700 dark:bg-amber-900/30 dark:text-amber-400">
              {pendingCount} pending
            </span>
          )}
        </div>
        <div className="flex items-center gap-1">
          <button
            onClick={() => navigate('/audit?source=reconciler')}
            className="rounded-lg px-2 py-1 text-xs text-teal-600 transition-colors hover:bg-muted hover:text-teal-700 dark:text-teal-400"
          >
            View audit log
          </button>
          <button
            onClick={() => void fetchAlerts(false)}
            className="rounded-lg p-1.5 text-muted-foreground transition-colors hover:bg-muted hover:text-card-foreground"
            aria-label="Refresh drift alerts"
            title="Refresh drift alerts"
          >
            <RefreshCw className="h-3.5 w-3.5" />
          </button>
        </div>
      </div>

      <div className="space-y-2">
        {visibleAlerts.map((alert) => (
          <div
            key={alert.id}
            className={`flex items-start gap-3 rounded-lg px-3 py-2 text-xs ${
              alert.status === 'pending'
                ? 'bg-amber-100/60 dark:bg-amber-900/20'
                : 'bg-card'
            }`}
          >
            <div
              className={`mt-0.5 h-2.5 w-2.5 shrink-0 rounded-full ${
                alert.status === 'pending' ? 'bg-amber-500 animate-pulse' : 'bg-green-500'
              }`}
            />
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2 flex-wrap">
                <span className="font-medium text-card-foreground">
                  {eventLabel(alert.event)}
                </span>
                {statusBadge(alert)}
              </div>
              <p className="mt-0.5 text-muted-foreground truncate" title={alert.resource}>
                {alert.resource}
              </p>
            </div>
            <div className="flex items-center gap-1 shrink-0">
              <span className="flex items-center gap-1 text-muted-foreground whitespace-nowrap">
                <Clock className="h-3 w-3" />
                {timeAgo(alert.timestamp)}
              </span>
              <button
                onClick={() => navigate(`/audit?source=reconciler&event=${alert.event}`)}
                className="rounded p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-card-foreground"
                title="View in audit log"
              >
                <ExternalLink className="h-3.5 w-3.5" />
              </button>
              <button
                onClick={() => handleDismiss(alert.id)}
                className="rounded p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-card-foreground"
                title="Dismiss alert"
                aria-label="Dismiss alert"
              >
                <X className="h-3.5 w-3.5" />
              </button>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}

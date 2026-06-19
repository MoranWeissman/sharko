import { useState } from 'react'
import { Link } from 'react-router-dom'
import { AlertTriangle, ExternalLink, X } from 'lucide-react'

/**
 * Non-blocking, dismissible banner shown at the top of the app when Sharko
 * can't reach or verify the active Git connection (V2-cleanup-50).
 *
 * Background: a broken connection (e.g. a corporate Zscaler TLS-inspection
 * proxy producing an x509 "unknown authority" error) used to be mistaken for
 * "the repo was never set up", throwing the user into the re-bootstrap wizard.
 * A broken connection is NOT a setup problem — it belongs in Settings →
 * Connections. So instead of hard-blocking the app, we keep the user in their
 * working app and surface the problem here, with a link to fix it.
 *
 * The amber/warning styling + AlertTriangle icon + dismiss X mirror the
 * established AttributionNudge / DriftAlertsPanel inline-banner pattern.
 */

/** Maps the machine reason tag to a short, plain-English secondary line. */
function reasonDetail(reason?: string): string {
  switch (reason) {
    case 'connection_error':
      return "Sharko reached your Git host but couldn't verify it — often a TLS or certificate problem (for example a corporate proxy that inspects traffic)."
    case 'no_connection':
      return 'There is no usable Git connection configured right now.'
    case 'error':
    default:
      return "The status check couldn't complete. Your connection may be offline or unreachable."
  }
}

export function ConnectionErrorBanner({ reason }: { reason?: string }) {
  const [dismissed, setDismissed] = useState(false)
  if (dismissed) return null

  return (
    <div
      role="alert"
      className="flex items-start gap-2 border-b border-amber-300 bg-amber-50 px-4 py-3 text-sm text-amber-900 dark:border-amber-700 dark:bg-amber-950/40 dark:text-amber-200"
    >
      <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0" aria-hidden="true" />
      <div className="flex-1">
        <p className="font-medium">Sharko can&apos;t reach your Git connection right now.</p>
        <p className="mt-1">{reasonDetail(reason)}</p>
        <Link
          to="/settings?section=connections"
          className="mt-2 inline-flex items-center gap-1 rounded-md border border-amber-400 bg-amber-100 px-3 py-1 text-xs font-medium hover:bg-amber-200 dark:border-amber-700 dark:bg-amber-900 dark:hover:bg-amber-800"
        >
          Open Settings → Connections
          <ExternalLink className="h-3 w-3" />
        </Link>
      </div>
      <button
        type="button"
        onClick={() => setDismissed(true)}
        className="ml-2 flex-shrink-0 rounded-md p-1 text-amber-700 hover:bg-amber-100 hover:text-amber-900 dark:text-amber-300 dark:hover:bg-amber-900 dark:hover:text-amber-100"
        aria-label="Dismiss"
      >
        <X className="h-4 w-4" />
      </button>
    </div>
  )
}

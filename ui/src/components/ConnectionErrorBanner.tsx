import { useState } from 'react'
import { Link } from 'react-router-dom'
import { AlertTriangle, ArrowRight, Wrench, X } from 'lucide-react'
import type { RepoStatusReason } from '@/services/api'

/**
 * Non-blocking, dismissible banner shown at the top of the app whenever
 * Sharko cannot confirm the install is healthy — a broken Git connection, a
 * rejected/unreachable ArgoCD credential, or a missing/degraded engine app
 * (V2-cleanup-50, extended by error review package 1 and the 2026-08-02
 * scope extension).
 *
 * Background: a broken connection (e.g. a corporate Zscaler TLS-inspection
 * proxy producing an x509 "unknown authority" error) used to be mistaken for
 * "the repo was never set up", throwing the user into the re-bootstrap
 * wizard. Later, an expired ArgoCD token produced the same false "the engine
 * app already exists but is not healthy" claim. None of that is a setup
 * problem — it belongs in Settings → Connections, or (for a genuinely broken
 * engine app) a user-invited repair screen. So instead of hard-blocking the
 * app or auto-opening the wizard, Sharko keeps the user in their working app
 * and surfaces the actual problem here, with a way to fix it.
 *
 * The amber/warning styling + AlertTriangle icon + dismiss X mirror the
 * established AttributionNudge / DriftAlertsPanel inline-banner pattern.
 */

type BannerAction = 'settings' | 'repair'

interface BannerContent {
  heading: string
  body: string
  action: BannerAction
}

/** Maps the machine reason tag to the heading/body/action the banner shows. */
function bannerContent(reason?: RepoStatusReason): BannerContent {
  switch (reason) {
    case 'connection_error':
      return {
        heading: "Sharko can't reach your Git connection right now.",
        body: "Sharko reached your Git host but couldn't verify it — often a TLS or certificate problem (for example a corporate proxy that inspects traffic).",
        action: 'settings',
      }
    case 'no_connection':
      // Review findings r1, L17: the old heading ("can't reach your Git
      // connection") implies Sharko tried and failed to connect — but this
      // reason means no connection is configured at all, so there was
      // nothing to try. Match the heading to what the body already says.
      return {
        heading: 'No Git connection is set up.',
        body: 'There is no usable Git connection configured right now.',
        action: 'settings',
      }
    case 'argocd_auth_failed':
      return {
        heading: "Sharko's ArgoCD credential is no longer valid.",
        body: "ArgoCD rejected the token Sharko uses to check on the cluster (401) — the token is likely expired or was revoked.",
        action: 'settings',
      }
    case 'argocd_forbidden':
      // Review findings r1, H1: a 403 means the token is valid but lacks
      // permission — Sharko never got to look at the engine app, so this is
      // a couldn't-check state, not a broken-app state. No repair button:
      // repairing the engine app is not the fix for a permissions problem,
      // and offering one would wrongly imply Sharko found something broken.
      return {
        heading: "ArgoCD refused Sharko's token permission to read applications.",
        body: "The token is valid but lacks permission to check the engine app, so Sharko can't confirm whether it's healthy. Grant the token permission, or replace it, in Settings → Connections.",
        action: 'settings',
      }
    case 'argocd_unreachable':
      return {
        heading: "Sharko can't reach ArgoCD right now.",
        body: "Sharko couldn't get an answer from ArgoCD, so it can't confirm whether the cluster is in sync.",
        action: 'settings',
      }
    case 'bootstrap_unreachable':
      return {
        heading: "ArgoCD can't reach your Git repo right now.",
        body: "This is usually a connection or network problem on ArgoCD's side, not a setup problem — check your connection in Settings, and your network or proxy.",
        action: 'settings',
      }
    case 'error':
      return {
        heading: "Sharko can't reach your Git connection right now.",
        body: "The status check couldn't complete. Your connection may be offline or unreachable.",
        action: 'settings',
      }
    // "bootstrap_degraded" and any other initialized-but-unhealthy reason —
    // ArgoCD read the repo and found the engine app missing or degraded, a
    // problem the repair screen can act on.
    default:
      return {
        heading: "There's an issue with Sharko's engine app.",
        body: 'The ArgoCD application Sharko uses to manage addons is missing or not healthy.',
        action: 'repair',
      }
  }
}

export function ConnectionErrorBanner({
  reason,
  onOpenRepair,
}: {
  reason?: RepoStatusReason
  /** Called when the user clicks the repair action (only shown for engine-app problems). */
  onOpenRepair?: () => void
}) {
  const [dismissed, setDismissed] = useState(false)
  if (dismissed) return null

  const { heading, body, action } = bannerContent(reason)

  return (
    <div
      role="alert"
      className="flex items-start gap-2 border-b border-amber-300 bg-amber-50 px-4 py-3 text-sm text-amber-900 dark:border-amber-700 dark:bg-amber-950/40 dark:text-amber-200"
    >
      <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0" aria-hidden="true" />
      <div className="flex-1">
        <p className="font-medium">{heading}</p>
        <p className="mt-1">{body}</p>
        {action === 'repair' ? (
          <button
            type="button"
            onClick={onOpenRepair}
            className="mt-2 inline-flex items-center gap-1 rounded-md border border-amber-400 bg-amber-100 px-3 py-1 text-xs font-medium hover:bg-amber-200 dark:border-amber-700 dark:bg-amber-900 dark:hover:bg-amber-800"
          >
            <Wrench className="h-3 w-3" />
            Review and repair
          </button>
        ) : (
          <Link
            to="/settings?section=connections"
            className="mt-2 inline-flex items-center gap-1 rounded-md border border-amber-400 bg-amber-100 px-3 py-1 text-xs font-medium hover:bg-amber-200 dark:border-amber-700 dark:bg-amber-900 dark:hover:bg-amber-800"
          >
            Open Settings → Connections
            <ArrowRight className="h-3 w-3" />
          </Link>
        )}
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

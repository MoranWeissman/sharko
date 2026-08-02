import { BrowserRouter, Routes, Route, Navigate, useLocation } from 'react-router-dom'
import { lazy, Suspense, useState, useEffect } from 'react'
import { Loader2 } from 'lucide-react'
import { AuthProvider, useAuth } from '@/hooks/useAuth'
import { ConnectionProvider, useConnections } from '@/hooks/useConnections'
import { ThemeProvider } from '@/hooks/useTheme'
import { AddonStatesProvider } from '@/hooks/useAddonStates'
import { Layout } from '@/components/Layout'
import { Login } from '@/views/Login'
import { FirstRunWizard } from '@/components/FirstRunWizard'
import { ConnectionErrorBanner } from '@/components/ConnectionErrorBanner'
import { ErrorBoundary } from '@/components/ErrorBoundary'
import { api } from '@/services/api'

// Lazy-loaded views — split into separate chunks for faster initial load
const Dashboard = lazy(() => import('@/views/Dashboard'))
const ClustersOverview = lazy(() => import('@/views/ClustersOverview'))
const ClusterDetail = lazy(() => import('@/views/ClusterDetail'))
const AddonCatalog = lazy(() => import('@/views/AddonCatalog'))
const AddonDetail = lazy(() => import('@/views/AddonDetail'))
const Observability = lazy(() => import('@/views/Observability'))
const SystemView = lazy(() => import('@/views/SystemView'))
const Dashboards = lazy(() => import('@/views/Dashboards'))
const Settings = lazy(() => import('@/views/Settings'))
const UserInfo = lazy(() => import('@/views/UserInfo'))
const AuditViewer = lazy(() => import('@/views/AuditViewer'))

// V2-cleanup-61.1 (A1): plain `<Navigate to="..." replace />` drops the
// current query string — a deep-link like `/version-matrix?drift=true`
// lands on the destination page with the `?drift=true` silently gone.
// This wrapper carries the query string across the redirect so downstream
// consumers of the param (now or later) actually see it.
function RedirectPreservingQuery({ to }: { to: string }) {
  const location = useLocation()
  return <Navigate to={`${to}${location.search}`} replace />
}

function PageLoader() {
  return (
    <div className="flex items-center justify-center h-full min-h-[200px]">
      <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
    </div>
  )
}

/**
 * The set of `reason` tags that mean "Sharko couldn't reach or verify the Git
 * connection" — a BROKEN connection, not a fresh install. (`connections.length
 * === 0`, the genuine fresh-install path, is handled upstream by
 * <FirstRunWizard/> before this gate ever runs, so any of these reaching the
 * gate means an existing-but-broken connection.)
 *
 *   - "connection_error" — backend reached the repo step but the fetch failed
 *     (TLS/transport/auth, e.g. a corporate Zscaler proxy x509 error).
 *   - "no_connection"    — backend has no usable active Git provider.
 *   - "error"            — the HTTP probe itself failed (set by the .catch).
 *
 * V2-cleanup-50: a broken connection must NOT throw the user into the
 * re-bootstrap wizard. It belongs in Settings → Connections, surfaced via a
 * non-blocking banner.
 */
export const CONNECTION_ERROR_REASONS = ['connection_error', 'no_connection', 'error'] as const

export function isConnectionErrorReason(reason?: string): boolean {
  return reason != null && (CONNECTION_ERROR_REASONS as readonly string[]).includes(reason)
}

/**
 * Pure helper for the wizard gate. Returns true ONLY when the GitOps repo is
 * genuinely not set up yet (day zero) — the wizard's real job.
 *
 * Locked decision (scope extension, 2026-08-02 — supersedes the narrower
 * V2-cleanup-51/error-review-package-1 rule below): once the repo is
 * initialized, the wizard must NEVER auto-open again, for ANY reason —
 * including a genuinely missing or degraded engine app (`bootstrap_degraded`
 * / "partial" / "absent" on the backend), not just a broken connection. An
 * uninvited wizard takeover on a working install is exactly the bug this
 * review package exists to fix; auto-opening it for more (even honest)
 * reasons would recreate the same hijack with better wording. Every
 * initialized-but-unhealthy state is surfaced by the banner instead
 * (`shouldShowConnectionErrorBanner`), with a button the user clicks to open
 * the repair screen themselves — a user-invited wizard is fine, an
 * uninvited one is not.
 *
 * V2-cleanup-50: when the repo is NOT initialized BUT the reason is a broken
 * connection (TLS/transport/auth failure — `connection_error`/`no_connection`/
 * `error`), we deliberately return false so the user KEEPS their working app
 * instead of being forced to re-bootstrap. A non-blocking banner surfaces the
 * connection problem and points at Settings → Connections.
 *
 * The only case that still fires is `initialized === false` for a genuine
 * "not set up yet" reason (e.g. "not_bootstrapped", or no reason at all on
 * first load) — a Git/ArgoCD connection was saved but Initialize was never
 * run. That is still day zero, just resumed past steps 1-3, so the wizard's
 * Step 4 auto-opens for it — this is the ONLY auto path left.
 *
 * `dismissed` is the session-scoped escape hatch (sessionStorage
 * `sharko:dismiss-wizard=1`). When true, the user can explore the (degraded)
 * dashboard for the rest of the session — a fresh tab brings the wizard back.
 *
 * A null repoStatus (still loading or connection-less) means "don't show the
 * wizard yet" — the parent gate handles those upstream branches.
 */
export function shouldShowSetupWizard(
  repoStatus: { initialized: boolean; bootstrap_synced?: boolean; reason?: string } | null,
  dismissed: boolean,
): boolean {
  if (dismissed) return false
  if (!repoStatus) return false

  // Initialized, for ANY reason (healthy, degraded, absent, unreachable,
  // auth-failed, or anything else) — never auto-open. See the decision note
  // above.
  if (repoStatus.initialized) return false

  // Not initialized: a broken connection is an environment problem, not a setup
  // problem. Suppress the wizard and let the banner surface it instead.
  if (isConnectionErrorReason(repoStatus.reason)) {
    return false
  }

  // Not initialized for a genuine reason (e.g. "not_bootstrapped") — fire.
  return true
}

/**
 * Whether the connection-error banner should be shown. Two cases:
 *
 *   - Not initialized because of a broken connection — the wizard is
 *     suppressed for exactly this reason (see shouldShowSetupWizard), so the
 *     banner is the only surface for it.
 *   - Initialized but NOT confirmed healthy, for ANY reason — a broken Git
 *     connection, a rejected/unreachable ArgoCD credential, or a missing/
 *     degraded engine app. Since the wizard never auto-opens post-setup
 *     (scope extension, 2026-08-02), the banner is the ONLY place any of
 *     these problems get surfaced; it names the actual problem and, for the
 *     engine-app case, offers a button to open the repair screen on request.
 */
export function shouldShowConnectionErrorBanner(
  repoStatus: { initialized: boolean; bootstrap_synced?: boolean; reason?: string } | null,
): boolean {
  if (!repoStatus) return false
  if (repoStatus.initialized) return !repoStatus.bootstrap_synced
  return isConnectionErrorReason(repoStatus.reason)
}

export function ConnectedApp() {
  const { connections, loading } = useConnections()
  const [repoStatus, setRepoStatus] = useState<{ initialized: boolean; bootstrap_synced?: boolean; reason?: string } | null>(null)
  const [checkingRepo, setCheckingRepo] = useState(true)
  // User-invited repair screen: set only by clicking the banner's repair
  // button (never by the gate itself — see shouldShowSetupWizard). Reusing
  // FirstRunWizard's Step 4 here means the same honest per-state copy
  // (missing/degraded/unreachable/auth-failed/couldn't-check) that Step 4
  // already renders during day-zero resume also serves the invited-repair
  // case, with no new UI to build.
  const [repairWizardOpen, setRepairWizardOpen] = useState(false)

  useEffect(() => {
    if (!loading) {
      if (connections.length > 0) {
        api.getRepoStatus()
          .then(data => setRepoStatus(data))
          // Failed probe → degraded posture, route to wizard
          // (bootstrap_synced=false makes the gate fire even if the
          // initialized flag is misreported elsewhere).
          .catch(() => setRepoStatus({ initialized: false, bootstrap_synced: false, reason: 'error' }))
          .finally(() => setCheckingRepo(false))
      } else {
        setCheckingRepo(false)
      }
    }
  }, [loading, connections.length])

  // Show spinner while loading connections or checking repo status
  if (loading || checkingRepo) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-[#bee0ff] dark:bg-gray-950">
        <div className="h-8 w-8 animate-spin rounded-full border-4 border-[#6aade0] border-t-[#1a3d5c] dark:border-gray-700 dark:border-t-teal-500" />
      </div>
    )
  }

  // No connections yet — show full first-run wizard
  if (connections.length === 0) {
    return <FirstRunWizard />
  }

  // Dismiss-flag escape hatch. The wizard's X button writes
  // `sharko:dismiss-wizard` into sessionStorage so a user who clicked X is
  // not immediately re-trapped here on the next render. Session-scoped on
  // purpose: a fresh tab / hard refresh brings the wizard back, so the user
  // can't permanently skip setup, but they can dismiss it for the current
  // session to look around the app, run a CLI command, etc.
  const dismissed = sessionStorage.getItem('sharko:dismiss-wizard') === '1'

  if (shouldShowSetupWizard(repoStatus, dismissed)) {
    return <FirstRunWizard initialStep={4} />
  }

  // The user clicked "repair" on the banner below — open the SAME Step 4
  // screen the day-zero resume path uses, but only because they asked for
  // it. onExit resets this flag so closing/finishing the wizard returns to
  // the normal app instead of leaving this overlay stuck open.
  if (repairWizardOpen) {
    return (
      <FirstRunWizard initialStep={4} onExit={() => setRepairWizardOpen(false)} />
    )
  }

  // V2-cleanup-50 / scope extension 2026-08-02: the wizard never auto-opens
  // for a broken connection OR an initialized-but-unhealthy bootstrap — both
  // surface here as a non-blocking banner above the app instead of
  // hard-blocking the user.
  const showConnError = shouldShowConnectionErrorBanner(repoStatus)

  return (
    <Suspense fallback={<PageLoader />}>
      {showConnError && (
        <ConnectionErrorBanner
          reason={repoStatus?.reason}
          onOpenRepair={() => setRepairWizardOpen(true)}
        />
      )}
      <Routes>
        <Route path="/" element={<Layout />}>
          <Route index element={<Navigate to="/dashboard" replace />} />
          <Route path="dashboard" element={<Dashboard />} />
          {/* Wrap Clusters in an ErrorBoundary so a transient 500 from
              /clusters that escapes the local error state never leaves the
              page blank — the boundary renders a recoverable fallback with
              a Try Again button. */}
          <Route
            path="clusters"
            element={
              <ErrorBoundary>
                <ClustersOverview />
              </ErrorBoundary>
            }
          />
          <Route path="clusters/:name" element={<ClusterDetail />} />
          <Route path="addons" element={<AddonCatalog />} />
          <Route path="addons/:name" element={<AddonDetail />} />
          <Route path="version-matrix" element={<RedirectPreservingQuery to="/addons" />} />
          <Route path="observability" element={<Observability />} />
          <Route path="system" element={<SystemView />} />
          {/* V2-cleanup-61.4 (F2): the standalone Upgrade Checker page
              duplicated AddonDetail's Upgrade tab (per-addon analysis,
              conflicts, AI summary, downgrade guard, per-cluster upgrade —
              all already live there) and had no real inbound deep-link with
              an addon/version to preserve, so it's a plain redirect to the
              catalog — same pattern as /version-matrix. */}
          <Route path="upgrade" element={<RedirectPreservingQuery to="/addons" />} />
          <Route path="dashboards" element={<Dashboards />} />
          <Route path="audit" element={<AuditViewer />} />
          <Route path="settings" element={<Settings />} />
          <Route path="users" element={<Navigate to="/settings?section=users" replace />} />
          <Route path="api-keys" element={<Navigate to="/settings?section=api-keys" replace />} />
          <Route path="user" element={<UserInfo />} />
        </Route>
      </Routes>
    </Suspense>
  )
}

function AppRoutes() {
  const { isAuthenticated, loading } = useAuth()

  if (loading) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-gray-50 dark:bg-gray-900">
        <div className="h-8 w-8 animate-spin rounded-full border-4 border-gray-200 border-t-teal-600 dark:border-gray-700 dark:border-t-teal-500" />
      </div>
    )
  }

  if (!isAuthenticated) {
    return <Login />
  }

  return (
    <ConnectionProvider>
      {/*
        AddonStatesProvider — single poll loop for addon health/sync.
        Mounted INSIDE ConnectionProvider because it depends on the active
        connection (via the API base URL / auth header). See
        ui/src/hooks/useAddonStates.tsx for the design rationale.
      */}
      <AddonStatesProvider>
        <ConnectedApp />
      </AddonStatesProvider>
    </ConnectionProvider>
  )
}

export default function App() {
  return (
    <BrowserRouter>
      <ThemeProvider>
        <AuthProvider>
          <AppRoutes />
        </AuthProvider>
      </ThemeProvider>
    </BrowserRouter>
  )
}

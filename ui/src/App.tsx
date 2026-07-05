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
const UpgradeChecker = lazy(() => import('@/views/UpgradeChecker'))

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
 * Pure helper for the wizard gate. Returns true when the wizard should auto-
 * open at Step 4 because either the GitOps repo isn't initialized yet, OR it
 * is initialized but the cluster-side ArgoCD bootstrap
 * (`cluster-addons-bootstrap`) is missing/degraded.
 *
 * V2-cleanup-50: when the repo is NOT initialized BUT the reason is a broken
 * connection (TLS/transport/auth failure — `connection_error`/`no_connection`/
 * `error`), we deliberately return false so the user KEEPS their working app
 * instead of being forced to re-bootstrap. A non-blocking banner surfaces the
 * connection problem and points at Settings → Connections.
 *
 * The #435 connection-error exclusion applies ONLY to the not-initialized
 * branch. The genuine wizard states still fire:
 *   - `reason === "not_bootstrapped"` (repo reachable, files genuinely absent).
 *   - `initialized === true && !bootstrap_synced` (repo seeded but the
 *     cluster-side ArgoCD bootstrap is genuinely missing/degraded — the
 *     recovery surface).
 *
 * V2-cleanup-51: the initialized-but-unhealthy branch gets ONE new exception.
 * When `reason === "bootstrap_unreachable"` the bootstrap is unhealthy only
 * because ArgoCD can't reach/compare the repo (a connection problem), so we
 * return false — re-init can't fix a connection problem. Any other reason
 * (including "bootstrap_degraded" or no reason) still fires the recovery
 * wizard.
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

  // Initialized-but-unhealthy bootstrap. There are two sub-cases:
  //
  //   - reason === "bootstrap_unreachable" — ArgoCD simply can't reach/compare
  //     the repo right now (a connection/network problem, e.g. a corporate
  //     Zscaler proxy). Re-initializing CAN'T fix a connection problem, so we
  //     must NOT trap the user in the re-init wizard. Keep them in their working
  //     app — the connection-health bell alert + the Dashboard banner already
  //     surface this honestly. (V2-cleanup-51)
  //   - any OTHER reason (incl. "bootstrap_degraded" or no reason) — the
  //     bootstrap is genuinely missing/degraded and re-init may repair it, so
  //     the recovery wizard still fires (unchanged V124-22 behaviour).
  if (repoStatus.initialized) {
    if (repoStatus.reason === 'bootstrap_unreachable') return false
    return !repoStatus.bootstrap_synced
  }

  // Not initialized: a broken connection is an environment problem, not a setup
  // problem. Suppress the wizard and let the banner surface it instead.
  if (isConnectionErrorReason(repoStatus.reason)) {
    return false
  }

  // Not initialized for a genuine reason (e.g. "not_bootstrapped") — fire.
  return true
}

/**
 * Whether the connection-error banner should be shown: the wizard is suppressed
 * BECAUSE of a broken connection (not a fresh install — that path is handled by
 * the upstream <FirstRunWizard/>). Mirrors the suppression branch in
 * shouldShowSetupWizard so the two stay in lockstep.
 */
export function shouldShowConnectionErrorBanner(
  repoStatus: { initialized: boolean; bootstrap_synced?: boolean; reason?: string } | null,
): boolean {
  if (!repoStatus) return false
  if (repoStatus.initialized) return false
  return isConnectionErrorReason(repoStatus.reason)
}

export function ConnectedApp() {
  const { connections, loading } = useConnections()
  const [repoStatus, setRepoStatus] = useState<{ initialized: boolean; bootstrap_synced?: boolean; reason?: string } | null>(null)
  const [checkingRepo, setCheckingRepo] = useState(true)

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

  // V2-cleanup-50: when the wizard is suppressed because the Git connection is
  // broken (not a fresh install — that's handled above), surface it as a
  // non-blocking banner above the app instead of hard-blocking the user.
  const showConnError = shouldShowConnectionErrorBanner(repoStatus)

  return (
    <Suspense fallback={<PageLoader />}>
      {showConnError && <ConnectionErrorBanner reason={repoStatus?.reason} />}
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
          <Route path="upgrade" element={<UpgradeChecker />} />
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

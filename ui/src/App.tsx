import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { lazy, Suspense, useState, useEffect } from 'react'
import { Loader2 } from 'lucide-react'
import { AuthProvider, useAuth } from '@/hooks/useAuth'
import { ConnectionProvider, useConnections } from '@/hooks/useConnections'
import { ThemeProvider } from '@/hooks/useTheme'
import { AddonStatesProvider } from '@/hooks/useAddonStates'
import { Layout } from '@/components/Layout'
import { Login } from '@/views/Login'
import { FirstRunWizard } from '@/components/FirstRunWizard'
import { ErrorBoundary } from '@/components/ErrorBoundary'
import { api } from '@/services/api'

// Lazy-loaded views — split into separate chunks for faster initial load
const Dashboard = lazy(() => import('@/views/Dashboard'))
const ClustersOverview = lazy(() => import('@/views/ClustersOverview'))
const ClusterDetail = lazy(() => import('@/views/ClusterDetail'))
const AddonCatalog = lazy(() => import('@/views/AddonCatalog'))
const AddonDetail = lazy(() => import('@/views/AddonDetail'))
const Observability = lazy(() => import('@/views/Observability'))
const Dashboards = lazy(() => import('@/views/Dashboards'))
const Settings = lazy(() => import('@/views/Settings'))
const UserInfo = lazy(() => import('@/views/UserInfo'))
const AuditViewer = lazy(() => import('@/views/AuditViewer'))
const UpgradeChecker = lazy(() => import('@/views/UpgradeChecker'))

function PageLoader() {
  return (
    <div className="flex items-center justify-center h-full min-h-[200px]">
      <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
    </div>
  )
}

/**
 * V124-22 / BUG-046: pure helper for the wizard gate. Returns true when
 * the wizard should auto-open at Step 4 because either the GitOps repo
 * isn't initialized yet, OR it is initialized but the cluster-side
 * ArgoCD bootstrap (`cluster-addons-bootstrap`) is missing/degraded —
 * V124-15 made the operation framework treat the latter as a real
 * failure, so leaving the user on a dashboard full of errors is the
 * asymmetry this closes.
 *
 * The `dismissed` argument is the V124-16 / BUG-035 escape hatch
 * (sessionStorage `sharko:dismiss-wizard=1`). When true, returning false
 * lets the user explore the (degraded) dashboard for the rest of the
 * session — a fresh tab brings the wizard back, so they can't permanently
 * skip recovery.
 *
 * Treats a null repoStatus (still loading or connection-less) as "don't
 * show the wizard yet" — the parent gate handles those upstream branches.
 */
export function shouldShowSetupWizard(
  repoStatus: { initialized: boolean; bootstrap_synced?: boolean; reason?: string } | null,
  dismissed: boolean,
): boolean {
  if (dismissed) return false
  if (!repoStatus) return false
  return !repoStatus.initialized || !repoStatus.bootstrap_synced
}

function ConnectedApp() {
  const { connections, loading } = useConnections()
  const [repoStatus, setRepoStatus] = useState<{ initialized: boolean; bootstrap_synced?: boolean; reason?: string } | null>(null)
  const [checkingRepo, setCheckingRepo] = useState(true)

  useEffect(() => {
    if (!loading) {
      if (connections.length > 0) {
        api.getRepoStatus()
          .then(data => setRepoStatus(data))
          // V124-22: failed probe → degraded posture, route to wizard
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

  // V124-16 / BUG-035: dismiss-flag escape hatch. The wizard's X button writes
  // `sharko:dismiss-wizard` into sessionStorage so a user who clicked X is
  // not immediately re-trapped here on the next render. Session-scoped on
  // purpose: a fresh tab / hard refresh brings the wizard back, so the user
  // can't permanently skip setup, but they can dismiss it for the current
  // session to look around the app, run a CLI command, etc.
  const dismissed = sessionStorage.getItem('sharko:dismiss-wizard') === '1'

  // V124-22 / BUG-046: wizard gate fires on either branch — repo not
  // initialized OR initialized-but-bootstrap-degraded. The bootstrap_synced
  // arm closes the V124-15 asymmetry (operation framework treats the
  // partial-state as a failure, but App.tsx used to drop the user on a
  // dashboard splattered with errors instead of opening the wizard).
  // Helper is exported for shouldShowSetupWizard.test.ts.
  if (shouldShowSetupWizard(repoStatus, dismissed)) {
    return <FirstRunWizard initialStep={4} />
  }

  return (
    <Suspense fallback={<PageLoader />}>
      <Routes>
        <Route path="/" element={<Layout />}>
          <Route index element={<Navigate to="/dashboard" replace />} />
          <Route path="dashboard" element={<Dashboard />} />
          {/* V124-2.3: wrap Clusters route in an ErrorBoundary so a transient
              500 from /clusters that escapes the local error state never
              leaves the page blank — the boundary renders a recoverable
              fallback with a Try Again button. */}
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
          <Route path="version-matrix" element={<Navigate to="/addons" replace />} />
          <Route path="observability" element={<Observability />} />
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

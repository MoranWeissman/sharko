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

function ConnectedApp() {
  const { connections, loading } = useConnections()
  const [repoStatus, setRepoStatus] = useState<{ initialized: boolean; reason?: string } | null>(null)
  const [checkingRepo, setCheckingRepo] = useState(true)

  useEffect(() => {
    if (!loading) {
      if (connections.length > 0) {
        api.getRepoStatus()
          .then(data => setRepoStatus(data))
          .catch(() => setRepoStatus({ initialized: false, reason: 'error' }))
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

  // Connection exists but repo not initialized — resume wizard at Step 4 (Init)
  if (repoStatus && !repoStatus.initialized) {
    return <FirstRunWizard initialStep={4} />
  }

  return (
    <Suspense fallback={<PageLoader />}>
      <Routes>
        <Route path="/" element={<Layout />}>
          <Route index element={<Navigate to="/dashboard" replace />} />
          <Route path="dashboard" element={<Dashboard />} />
          <Route path="clusters" element={<ClustersOverview />} />
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

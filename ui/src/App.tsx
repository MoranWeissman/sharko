import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { AuthProvider, useAuth } from '@/hooks/useAuth'
import { ConnectionProvider } from '@/hooks/useConnections'
import { ThemeProvider } from '@/hooks/useTheme'
import { Layout } from '@/components/Layout'
import { Login } from '@/views/Login'
import { Dashboard } from '@/views/Dashboard'
import { ClustersOverview } from '@/views/ClustersOverview'
import { ClusterDetail } from '@/views/ClusterDetail'
import { AddonCatalog } from '@/views/AddonCatalog'
import { AddonDetail } from '@/views/AddonDetail'
import { Observability } from '@/views/Observability'
import { Dashboards } from '@/views/Dashboards'
import { UserInfo } from '@/views/UserInfo'
import { Settings } from '@/views/Settings'

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
          <Route path="upgrade" element={<Navigate to="/addons" replace />} />
          <Route path="dashboards" element={<Dashboards />} />
          <Route path="settings" element={<Settings />} />
          <Route path="users" element={<Navigate to="/settings?section=users" replace />} />
          <Route path="api-keys" element={<Navigate to="/settings?section=api-keys" replace />} />
          <Route path="user" element={<UserInfo />} />
        </Route>
      </Routes>
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

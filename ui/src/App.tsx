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
import { Connections } from '@/views/Connections'
import { VersionMatrix } from '@/views/VersionMatrix'
import { Docs } from '@/views/Docs'
import { Observability } from '@/views/Observability'
import { Dashboards } from '@/views/Dashboards'
import { UpgradeChecker } from '@/views/UpgradeChecker'
import { AIAssistant } from '@/views/AIAssistant'
import { UserInfo } from '@/views/UserInfo'
import MigrationPage from '@/views/MigrationPage'
import MigrationDetail from '@/views/MigrationDetail'
import UserManagement from '@/views/UserManagement'

function AppRoutes() {
  const { isAuthenticated, loading } = useAuth()

  if (loading) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-gray-50 dark:bg-gray-900">
        <div className="h-8 w-8 animate-spin rounded-full border-4 border-gray-200 border-t-cyan-600 dark:border-gray-700 dark:border-t-cyan-500" />
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
          <Route path="version-matrix" element={<VersionMatrix />} />
          <Route path="observability" element={<Observability />} />
          <Route path="upgrade" element={<UpgradeChecker />} />
          <Route path="assistant" element={<AIAssistant />} />
          <Route path="dashboards" element={<Dashboards />} />
          <Route path="docs" element={<Docs />} />
          <Route path="settings" element={<Connections />} />
          <Route path="users" element={<UserManagement />} />
          <Route path="user" element={<UserInfo />} />
          <Route path="migration" element={<MigrationPage />} />
          <Route path="migration/:id" element={<MigrationDetail />} />
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

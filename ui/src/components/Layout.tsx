import { useState, useEffect } from 'react'
import { NavLink, Outlet, useNavigate, useLocation, Link } from 'react-router-dom'
import {
  LayoutDashboard,
  Server,
  Package,
  TableProperties,
  Activity,
  ArrowUpCircle,
  BarChart3,
  BookOpen,
  Settings,
  ChevronLeft,
  ChevronRight,
  Plug,
  Sun,
  Moon,
  LogOut,
  GitPullRequest,
  User,
  Menu,
  Search,
  MessageSquare,
} from 'lucide-react'
import { useConnections } from '@/hooks/useConnections'
import { FloatingAssistant } from '@/components/FloatingAssistant'
import { CommandPalette } from '@/components/CommandPalette'
import { useTheme } from '@/hooks/useTheme'
import { useAuth } from '@/hooks/useAuth'

interface NavItem {
  to: string
  label: string
  icon: typeof LayoutDashboard
}

interface NavSection {
  label: string
  items: NavItem[]
  adminOnly?: boolean
}

const navSections: NavSection[] = [
  {
    label: 'Monitor',
    items: [
      { to: '/', label: 'Dashboard', icon: LayoutDashboard },
      { to: '/clusters', label: 'Clusters', icon: Server },
      { to: '/addons', label: 'Add-ons Catalog', icon: Package },
      { to: '/version-matrix', label: 'Version Drift Detector', icon: TableProperties },
      { to: '/observability', label: 'Observability', icon: Activity },
    ],
  },
  {
    label: 'Operate',
    items: [
      { to: '/upgrade', label: 'Add-on Upgrade Checker', icon: ArrowUpCircle },
      { to: '/migration', label: 'Migration', icon: GitPullRequest },
      { to: '/dashboards', label: 'Dashboards', icon: BarChart3 },
    ],
  },
  {
    label: 'Configure',
    adminOnly: true,
    items: [
      { to: '/settings', label: 'Settings', icon: Settings },
      { to: '/users', label: 'User Management', icon: User },
    ],
  },
  {
    label: 'Help',
    items: [
      { to: '/assistant', label: 'AI Assistant', icon: MessageSquare },
      { to: '/docs', label: 'Docs', icon: BookOpen },
    ],
  },
]

const routeLabels: Record<string, string> = {
  dashboard: 'Dashboard',
  clusters: 'Clusters',
  addons: 'Add-ons Catalog',
  'version-matrix': 'Version Drift Detector',
  observability: 'Observability',
  upgrade: 'Add-on Upgrade Checker',
  migration: 'Migration',
  assistant: 'AI Assistant',
  dashboards: 'Dashboards',
  docs: 'Docs',
  settings: 'Settings',
  users: 'User Management',
  user: 'Account',
}

function Breadcrumbs() {
  const location = useLocation()
  const segments = location.pathname.split('/').filter(Boolean)
  // Only show breadcrumbs on detail pages (2+ segments like /clusters/name)
  if (segments.length <= 1) return null

  const crumbs: { label: string; path: string }[] = []
  let path = ''
  for (const seg of segments) {
    path += '/' + seg
    crumbs.push({ label: routeLabels[seg] || decodeURIComponent(seg), path })
  }

  return (
    <nav className="flex items-center gap-1.5 text-sm text-gray-500 dark:text-gray-400">
      {crumbs.map((crumb, i) => (
        <span key={crumb.path} className="flex items-center gap-1.5">
          {i > 0 && <span className="text-gray-300 dark:text-gray-600">/</span>}
          {i < crumbs.length - 1 ? (
            <Link to={crumb.path} className="hover:text-gray-700 dark:hover:text-gray-200">
              {crumb.label}
            </Link>
          ) : (
            <span className="font-medium text-gray-900 dark:text-gray-100">{crumb.label}</span>
          )}
        </span>
      ))}
    </nav>
  )
}

export function Layout() {
  const navigate = useNavigate()
  const [collapsed, setCollapsed] = useState(false)
  const { theme, toggleTheme } = useTheme()
  const { logout, isAdmin } = useAuth()

  const [appVersion, setAppVersion] = useState('')
  const [userMenuOpen, setUserMenuOpen] = useState(false)
  const [mobileOpen, setMobileOpen] = useState(false)
  const { activeConnection, loading } = useConnections()

  useEffect(() => {
    fetch('/api/v1/health')
      .then((r) => r.json())
      .then((d) => setAppVersion(d.version ?? ''))
      .catch(() => {})
  }, [])

  return (
    <div className="flex h-screen bg-gray-50 dark:bg-gray-950">
      {/* Mobile overlay */}
      {mobileOpen && (
        <div className="fixed inset-0 z-40 bg-black/50 lg:hidden" onClick={() => setMobileOpen(false)} />
      )}

      {/* Sidebar — always dark like ArgoCD */}
      <aside
        className={`flex flex-col bg-slate-900 shadow-sm transition-all duration-200 ${
          collapsed ? 'w-16' : 'w-60'
        } ${mobileOpen ? 'fixed inset-y-0 left-0 z-50 w-60' : 'hidden lg:flex'}`}
      >
        {/* Logo / title + mobile close */}
        <div
          className="flex h-14 cursor-pointer items-center gap-2 border-b border-slate-700 px-4 transition-colors hover:bg-slate-800"
          onClick={() => { navigate('/'); setMobileOpen(false) }}
        >
          <Package className="h-6 w-6 shrink-0 text-cyan-400" />
          {!collapsed && (
            <div className="flex flex-col leading-tight">
              <span className="text-sm font-bold text-white">AAP</span>
              <span className="text-[10px] text-slate-400">
                ArgoCD Addons Platform
              </span>
            </div>
          )}
        </div>

        {/* Navigation */}
        <nav className="flex-1 overflow-y-auto px-2 py-3">
          {navSections.filter(s => !s.adminOnly || isAdmin).map((section, si) => (
            <div key={section.label} className={si > 0 ? 'mt-4 border-t border-slate-700 pt-3' : ''}>
              {!collapsed && (
                <span className="mb-1 block px-3 text-[10px] font-semibold uppercase tracking-wider text-slate-500">
                  {section.label}
                </span>
              )}
              <div className="space-y-0.5">
                {section.items.map((item) => (
                  <NavLink
                    key={item.to}
                    to={item.to}
                    end={item.to === '/'}
                    onClick={() => setMobileOpen(false)}
                    className={({ isActive }) =>
                      `flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-colors ${
                        isActive
                          ? 'border-l-[3px] border-cyan-400 bg-slate-700 text-white'
                          : 'border-l-[3px] border-transparent text-slate-300 hover:bg-slate-800 hover:text-white'
                      } ${collapsed && !mobileOpen ? 'justify-center px-0' : ''}`
                    }
                    title={collapsed && !mobileOpen ? item.label : undefined}
                  >
                    <item.icon className="h-5 w-5 shrink-0" />
                    {(!collapsed || mobileOpen) && <span>{item.label}</span>}
                  </NavLink>
                ))}
              </div>
            </div>
          ))}
        </nav>

        {/* Collapse toggle */}
        <div className="border-t border-slate-700 p-2">
          <button
            onClick={() => setCollapsed((c) => !c)}
            className="flex w-full items-center justify-center rounded-lg p-2 text-slate-400 hover:bg-slate-800 hover:text-white"
            aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
          >
            {collapsed ? (
              <ChevronRight className="h-5 w-5" />
            ) : (
              <ChevronLeft className="h-5 w-5" />
            )}
          </button>

          {appVersion && (
            <p className="mt-1 text-center text-[10px] text-slate-500" title={`Version ${appVersion}`}>
              {collapsed ? `v${appVersion.split('.')[0]}` : `v${appVersion}`}
            </p>
          )}
        </div>
      </aside>

      {/* Right side: top bar + content */}
      <div className="flex flex-1 flex-col overflow-hidden">
        {/* Top bar */}
        <header className="flex h-14 shrink-0 items-center justify-between border-b border-gray-200 bg-white px-4 dark:border-gray-700 dark:bg-gray-900">
          {/* Left: mobile hamburger + breadcrumbs */}
          <div className="flex items-center gap-3">
            <button
              onClick={() => setMobileOpen(true)}
              className="rounded-lg p-2 text-gray-500 hover:bg-gray-100 lg:hidden dark:text-gray-400 dark:hover:bg-gray-800"
              aria-label="Open menu"
            >
              <Menu className="h-5 w-5" />
            </button>
            <Breadcrumbs />
          </div>

          {/* Right: search + connection + user dropdown */}
          <div className="flex items-center gap-3">
            {/* Search trigger */}
            <button
              onClick={() => { const e = new KeyboardEvent('keydown', { key: 'k', metaKey: true }); window.dispatchEvent(e) }}
              className="hidden items-center gap-1.5 rounded-lg border border-gray-200 bg-gray-50 px-3 py-1.5 text-xs text-gray-400 transition-colors hover:bg-gray-100 sm:flex dark:border-gray-700 dark:bg-gray-800 dark:hover:bg-gray-700"
            >
              <Search className="h-3.5 w-3.5" />
              <span>Search...</span>
              <kbd className="ml-2 rounded border border-gray-300 bg-white px-1 py-0.5 text-[9px] font-medium dark:border-gray-600 dark:bg-gray-700">
                {navigator.platform?.includes('Mac') ? '⌘' : 'Ctrl'}K
              </kbd>
            </button>
            {!loading && activeConnection && (
              <div className="hidden items-center gap-2 rounded-lg border border-gray-200 bg-gray-50 px-3 py-1.5 text-xs text-gray-500 sm:flex dark:border-gray-600 dark:bg-gray-800 dark:text-gray-400">
                <Plug className="h-3.5 w-3.5" />
                <span>{activeConnection}</span>
              </div>
            )}

            {/* User avatar dropdown */}
            <div className="relative">
              <button
                onClick={() => setUserMenuOpen((o) => !o)}
                className="flex h-8 w-8 items-center justify-center rounded-full bg-cyan-600 text-sm font-bold text-white hover:bg-cyan-700"
                aria-label="User menu"
              >
                <User className="h-4 w-4" />
              </button>
              {userMenuOpen && (
                <>
                  <div className="fixed inset-0 z-40" onClick={() => setUserMenuOpen(false)} />
                  <div className="absolute right-0 top-10 z-50 w-48 rounded-lg border border-gray-200 bg-white py-1 shadow-lg dark:border-gray-700 dark:bg-gray-800">
                    <button
                      onClick={() => { navigate('/user'); setUserMenuOpen(false) }}
                      className="flex w-full items-center gap-2 px-4 py-2 text-left text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-700"
                    >
                      <User className="h-4 w-4" />
                      Account
                    </button>
                    <button
                      onClick={() => { toggleTheme(); setUserMenuOpen(false) }}
                      className="flex w-full items-center gap-2 px-4 py-2 text-left text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-700"
                    >
                      {theme === 'dark' ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
                      {theme === 'dark' ? 'Light Mode' : 'Dark Mode'}
                    </button>
                    <div className="my-1 border-t border-gray-200 dark:border-gray-700" />
                    <button
                      onClick={logout}
                      className="flex w-full items-center gap-2 px-4 py-2 text-left text-sm text-red-600 hover:bg-gray-100 dark:text-red-400 dark:hover:bg-gray-700"
                    >
                      <LogOut className="h-4 w-4" />
                      Log out
                    </button>
                  </div>
                </>
              )}
            </div>
          </div>
        </header>

        {/* Content */}
        <main className="flex-1 overflow-auto">
          <div className="p-6 lg:p-8">
            <Outlet />
          </div>
        </main>
      </div>

      {/* Floating AI Assistant */}
      <FloatingAssistant />

      {/* Command Palette (Cmd+K) */}
      <CommandPalette />
    </div>
  )
}

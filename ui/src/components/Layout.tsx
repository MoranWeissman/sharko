import { useState, useEffect, useCallback } from 'react'
import { NavLink, Outlet, useNavigate, useLocation, Link } from 'react-router-dom'
import {
  LayoutDashboard,
  Server,
  Package,
  Activity,
  BarChart3,
  Settings,
  ChevronLeft,
  ChevronRight,
  ChevronDown,
  Plug,
  Sun,
  Moon,
  LogOut,
  User,
  Menu,
  Search,
  Sparkles,
  X,
} from 'lucide-react'
import { useConnections } from '@/hooks/useConnections'
import { FloatingAssistant } from '@/components/FloatingAssistant'
import { CommandPalette } from '@/components/CommandPalette'
import { useTheme } from '@/hooks/useTheme'
import { useAuth } from '@/hooks/useAuth'
import { AIAssistant } from '@/views/AIAssistant'

interface NavItem {
  to: string
  label: string
  icon: typeof LayoutDashboard
  children?: { to: string; label: string }[]
}

interface NavSection {
  label: string
  items: NavItem[]
  adminOnly?: boolean
}

const navSections: NavSection[] = [
  {
    label: 'Overview',
    items: [
      { to: '/', label: 'Dashboard', icon: LayoutDashboard },
      { to: '/clusters', label: 'Clusters', icon: Server },
      {
        to: '/addons',
        label: 'Addons',
        icon: Package,
        children: [
          { to: '/addons', label: 'Catalog' },
          { to: '/version-matrix', label: 'Version Drift' },
        ],
      },
    ],
  },
  {
    label: 'Manage',
    items: [
      { to: '/observability', label: 'Observability', icon: Activity },
      { to: '/dashboards', label: 'Dashboards', icon: BarChart3 },
    ],
  },
  {
    label: 'Configure',
    adminOnly: true,
    items: [
      { to: '/settings', label: 'Settings', icon: Settings },
    ],
  },
]

const routeLabels: Record<string, string> = {
  dashboard: 'Dashboard',
  clusters: 'Clusters',
  addons: 'Addons Catalog',
  'version-matrix': 'Version Drift Detector',
  observability: 'Observability',
  upgrade: 'Addon Upgrade Checker',
  dashboards: 'Dashboards',
  settings: 'Settings',
  users: 'User Management',
  'api-keys': 'API Keys',
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

function getAIPageContext(pathname: string): string | undefined {
  const routes: Record<string, string> = {
    '/dashboard': 'the Dashboard (overview stats)',
    '/clusters': 'the Clusters page',
    '/addons': 'the Addons Catalog',
    '/version-matrix': 'the Addons Version Drift Detector',
    '/observability': 'the Observability page',
    '/upgrade': 'the Addon Upgrade Checker',
    '/settings': 'the Settings page',
  }
  if (routes[pathname]) return routes[pathname]
  if (pathname.startsWith('/clusters/')) {
    const name = pathname.split('/')[2]
    return `the Cluster Detail page for "${name}"`
  }
  if (pathname.startsWith('/addons/')) {
    const name = pathname.split('/')[2]
    return `the Addon Detail page for "${name}"`
  }
  return undefined
}

export function Layout() {
  const navigate = useNavigate()
  const location = useLocation()
  const [collapsed, setCollapsed] = useState(false)
  const [addonsExpanded, setAddonsExpanded] = useState(true)
  const [aiPanelOpen, setAiPanelOpen] = useState(false)
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

  const openAiPanel = useCallback(() => {
    setAiPanelOpen(true)
    setCollapsed(true)
  }, [])

  useEffect(() => {
    const handler = () => openAiPanel()
    window.addEventListener('open-assistant', handler)
    return () => window.removeEventListener('open-assistant', handler)
  }, [openAiPanel])

  return (
    <div className="flex h-screen bg-[#bee0ff] dark:bg-gray-950">
      {/* Mobile overlay */}
      {mobileOpen && (
        <div className="fixed inset-0 z-40 bg-black/50 lg:hidden" onClick={() => setMobileOpen(false)} />
      )}

      {/* Sidebar — always dark */}
      <aside
        className={`flex flex-col bg-[#0a2a4a] shadow-sm transition-all duration-200 ${
          collapsed ? 'w-16' : 'w-52'
        } ${mobileOpen ? 'fixed inset-y-0 left-0 z-50 w-52' : 'hidden lg:flex'}`}
      >
        {/* Logo / title + mobile close */}
        <Link
          to="/dashboard"
          aria-label="Sharko — go to dashboard"
          className="flex items-center gap-2 border-b border-[#14466e] px-3 py-2.5 transition-colors hover:bg-[#14466e]"
          onClick={() => setMobileOpen(false)}
        >
          <img src="/sharko-mascot.png" alt="" className={collapsed && !mobileOpen ? 'h-8 w-auto' : 'h-10 w-auto'} />
          {(!collapsed || mobileOpen) && (
            <div className="min-w-0">
              <span className="text-base font-bold text-blue-400">Sharko</span>
              {appVersion && (
                <p className="text-[10px] text-[#5a9ad0]">v{appVersion}</p>
              )}
            </div>
          )}
        </Link>

        {/* Navigation */}
        <nav className="flex-1 overflow-y-auto px-2 py-3">
          {navSections.filter(s => !s.adminOnly || isAdmin).map((section, si) => (
            <div key={section.label} className={si > 0 ? 'mt-4 border-t border-[#14466e] pt-3' : ''}>
              {!collapsed && (
                <span className="mb-1 block px-3 text-[10px] font-semibold uppercase tracking-wider text-[#5a9ad0]">
                  {section.label}
                </span>
              )}
              <div className="space-y-0.5">
                {section.items.map((item) => {
                  if (item.children) {
                    const isParentActive = item.children.some(child =>
                      location.pathname === child.to || location.pathname.startsWith(child.to + '/')
                    )
                    return (
                      <div key={item.to}>
                        <button
                          onClick={() => setAddonsExpanded(e => !e)}
                          className={`flex w-full items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-colors ${
                            isParentActive
                              ? 'border-l-[3px] border-[#9fcffb] bg-[#14466e] text-white'
                              : 'border-l-[3px] border-transparent text-[#7ab0d8] hover:bg-[#14466e] hover:text-white'
                          } ${collapsed && !mobileOpen ? 'justify-center px-0' : ''}`}
                          title={collapsed && !mobileOpen ? item.label : undefined}
                        >
                          <item.icon className="h-5 w-5 shrink-0" />
                          {(!collapsed || mobileOpen) && (
                            <>
                              <span className="flex-1 text-left">{item.label}</span>
                              <ChevronDown className={`h-4 w-4 shrink-0 transition-transform ${addonsExpanded ? 'rotate-180' : ''}`} />
                            </>
                          )}
                        </button>
                        {addonsExpanded && (!collapsed || mobileOpen) && (
                          <div className="ml-4 mt-0.5 space-y-0.5 border-l border-[#14466e] pl-2">
                            {item.children.map(child => (
                              <NavLink
                                key={child.to}
                                to={child.to}
                                end={child.to === '/addons'}
                                onClick={() => setMobileOpen(false)}
                                className={({ isActive }) =>
                                  `block rounded-lg px-3 py-1.5 text-sm transition-colors ${
                                    isActive
                                      ? 'text-[#bee0ff] font-medium'
                                      : 'text-[#5a9ad0] hover:text-[#bee0ff]'
                                  }`
                                }
                              >
                                {child.label}
                              </NavLink>
                            ))}
                          </div>
                        )}
                      </div>
                    )
                  }
                  return (
                    <NavLink
                      key={item.to}
                      to={item.to}
                      end={item.to === '/'}
                      onClick={() => setMobileOpen(false)}
                      className={({ isActive }) =>
                        `flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-colors ${
                          isActive
                            ? 'border-l-[3px] border-[#9fcffb] bg-[#14466e] text-white'
                            : 'border-l-[3px] border-transparent text-[#7ab0d8] hover:bg-[#14466e] hover:text-white'
                        } ${collapsed && !mobileOpen ? 'justify-center px-0' : ''}`
                      }
                      title={collapsed && !mobileOpen ? item.label : undefined}
                    >
                      <item.icon className="h-5 w-5 shrink-0" />
                      {(!collapsed || mobileOpen) && <span>{item.label}</span>}
                    </NavLink>
                  )
                })}
              </div>
            </div>
          ))}
        </nav>

        {/* Collapse toggle */}
        <div className="border-t border-[#14466e] p-2">
          <button
            onClick={() => setCollapsed((c) => !c)}
            className="flex w-full items-center justify-center rounded-lg p-2 text-[#5a9ad0] hover:bg-[#14466e] hover:text-white"
            aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
          >
            {collapsed ? (
              <ChevronRight className="h-5 w-5" />
            ) : (
              <ChevronLeft className="h-5 w-5" />
            )}
          </button>
        </div>
      </aside>

      {/* Right side: top bar + content */}
      <div className="flex flex-1 flex-col overflow-hidden">
        {/* Top bar */}
        <header className="flex h-14 shrink-0 items-center justify-between border-b border-[#90c8ee] bg-[#d6eeff] px-4 dark:border-gray-700 dark:bg-gray-900">
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
            {/* AI panel toggle */}
            <button
              onClick={() => {
                if (aiPanelOpen) {
                  setAiPanelOpen(false)
                } else {
                  openAiPanel()
                }
              }}
              className={`flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs transition-colors ${
                aiPanelOpen
                  ? 'bg-teal-100 text-teal-700 dark:bg-teal-900/30 dark:text-teal-400'
                  : 'border border-gray-200 bg-gray-50 text-gray-500 hover:bg-gray-100 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-400 dark:hover:bg-gray-700'
              }`}
              aria-label="Toggle AI Assistant"
            >
              <Sparkles className="h-3.5 w-3.5" />
              {!aiPanelOpen && <span className="hidden sm:inline">Ask AI</span>}
            </button>

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
                className="flex h-8 w-8 items-center justify-center rounded-full bg-teal-600 text-sm font-bold text-white hover:bg-teal-700"
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

      {/* AI Panel — right side */}
      {aiPanelOpen && (
        <div className="flex w-[380px] shrink-0 flex-col border-l border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-900">
          {/* Panel header */}
          <div className="flex h-14 items-center justify-between border-b border-gray-200 bg-gradient-to-r from-teal-600 to-blue-700 px-4 dark:border-gray-700">
            <div className="flex items-center gap-2 text-white">
              <Sparkles className="h-4 w-4" />
              <div>
                <span className="text-sm font-semibold">Sharko AI</span>
                {getAIPageContext(location.pathname) && (
                  <p className="text-[10px] text-teal-200">Viewing {getAIPageContext(location.pathname)}</p>
                )}
              </div>
            </div>
            <button
              onClick={() => setAiPanelOpen(false)}
              className="rounded-lg p-1 text-white/80 hover:bg-white/20 hover:text-white"
              aria-label="Close AI panel"
            >
              <X className="h-4 w-4" />
            </button>
          </div>
          {/* Chat content */}
          <div className="flex-1 overflow-hidden">
            <AIAssistant embedded pageContext={getAIPageContext(location.pathname)} />
          </div>
        </div>
      )}

      {/* Floating AI Assistant */}
      <FloatingAssistant />

      {/* Command Palette (Cmd+K) */}
      <CommandPalette />
    </div>
  )
}

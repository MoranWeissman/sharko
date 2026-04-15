import { useState, useEffect, useCallback, useRef } from 'react'
import { NavLink, Outlet, useNavigate, useLocation, Link } from 'react-router-dom'
import {
  LayoutDashboard,
  Server,
  Package,
  Activity,
  BarChart3,
  ClipboardList,
  Settings,
  ChevronLeft,
  ChevronRight,
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
import { NotificationBell } from '@/components/NotificationBell'
import { ToastContainer } from '@/components/ToastNotification'
import { fetchTrackedPRs } from '@/services/api'

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
    label: 'Overview',
    items: [
      { to: '/', label: 'Dashboard', icon: LayoutDashboard },
      { to: '/clusters', label: 'Clusters', icon: Server },
      { to: '/addons', label: 'Addons', icon: Package },
    ],
  },
  {
    label: 'Manage',
    items: [
      { to: '/observability', label: 'Observability', icon: Activity },
      { to: '/dashboards', label: 'Dashboards', icon: BarChart3 },
      { to: '/audit', label: 'Audit Log', icon: ClipboardList },
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
  observability: 'Observability',
  dashboards: 'Dashboards',
  audit: 'Audit Log',
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
    <nav className="flex items-center gap-1.5 text-sm text-[#2a5a7a] dark:text-gray-400">
      {crumbs.map((crumb, i) => (
        <span key={crumb.path} className="flex items-center gap-1.5">
          {i > 0 && <span className="text-[#5a8aaa] dark:text-gray-600">/</span>}
          {i < crumbs.length - 1 ? (
            <Link to={crumb.path} className="hover:text-[#0a3a5a] dark:hover:text-gray-200">
              {crumb.label}
            </Link>
          ) : (
            <span className="font-medium text-[#0a2a4a] dark:text-gray-100">{crumb.label}</span>
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
    '/observability': 'the Observability page',
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
  const [aiPanelOpen, setAiPanelOpen] = useState(false)
  const [aiInitialMessage, setAiInitialMessage] = useState<string | undefined>()
  const { theme, toggleTheme } = useTheme()
  const { logout, isAdmin } = useAuth()

  const [appVersion, setAppVersion] = useState('')
  const [userMenuOpen, setUserMenuOpen] = useState(false)
  const [mobileOpen, setMobileOpen] = useState(false)
  const { activeConnection, loading } = useConnections()
  const [openPRCount, setOpenPRCount] = useState(0)
  const [aiPanelWidth, setAiPanelWidth] = useState(380)
  const isDragging = useRef(false)

  useEffect(() => {
    fetch('/api/v1/health')
      .then((r) => r.json())
      .then((d) => setAppVersion(d.version ?? ''))
      .catch(() => {})
  }, [])

  // Poll open PR count for sidebar badge
  useEffect(() => {
    const fetchPRCount = () => {
      fetchTrackedPRs({ status: 'open' })
        .then((data) => setOpenPRCount(data.prs?.length ?? 0))
        .catch(() => {})
    }
    fetchPRCount()
    const interval = setInterval(fetchPRCount, 30_000)
    return () => clearInterval(interval)
  }, [])

  const openAiPanel = useCallback(() => {
    setAiPanelOpen(true)
    setCollapsed(true)
  }, [])

  const handleMouseDown = useCallback((e: React.MouseEvent) => {
    isDragging.current = true
    e.preventDefault()

    const handleMouseMove = (e: MouseEvent) => {
      if (!isDragging.current) return
      const newWidth = Math.min(700, Math.max(320, window.innerWidth - e.clientX))
      setAiPanelWidth(newWidth)
    }

    const handleMouseUp = () => {
      isDragging.current = false
      document.removeEventListener('mousemove', handleMouseMove)
      document.removeEventListener('mouseup', handleMouseUp)
    }

    document.addEventListener('mousemove', handleMouseMove)
    document.addEventListener('mouseup', handleMouseUp)
  }, [])

  useEffect(() => {
    const handler = (e: Event) => {
      const detail = (e as CustomEvent).detail
      if (typeof detail === 'string' && detail) {
        setAiInitialMessage(detail)
      }
      openAiPanel()
    }
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
        className={`flex flex-col border-r-2 border-[#14466e] bg-[#1a3d5c] transition-all duration-200 ${
          collapsed ? 'w-24' : 'w-52'
        } ${mobileOpen ? 'fixed inset-y-0 left-0 z-50 w-52' : 'hidden lg:flex'}`}
      >
        {/* Logo / title + mobile close */}
        <Link
          to="/dashboard"
          aria-label="Sharko — go to dashboard"
          className={`flex items-center border-b border-[#14466e] transition-colors hover:bg-[#14466e] ${
            collapsed && !mobileOpen ? 'justify-center px-1 py-3' : 'gap-0 px-3 py-2'
          }`}
          onClick={() => setMobileOpen(false)}
        >
          <img src="/sharko-mascot.png" alt="" className="h-16 w-auto shrink-0" />
          {(!collapsed || mobileOpen) && (
            <div className="min-w-0 -ml-1">
              <span className="text-2xl leading-tight text-blue-400" style={{ fontFamily: '"Quicksand", sans-serif', fontWeight: 700 }}>Sharko</span>
              {appVersion && (
                <p className="text-[10px] text-[#5a9ad0] leading-tight">v{appVersion}</p>
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
                {section.items.map((item) => (
                  <NavLink
                    key={item.to}
                    to={item.to}
                    end={item.to === '/'}
                    onClick={() => setMobileOpen(false)}
                    className={({ isActive }) =>
                      `relative flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-colors ${
                        isActive
                          ? 'border-l-[3px] border-[#9fcffb] bg-[#14466e] text-white'
                          : 'border-l-[3px] border-transparent text-[#7ab0d8] hover:bg-[#14466e] hover:text-white'
                      } ${collapsed && !mobileOpen ? 'justify-center px-0' : ''}`
                    }
                    title={collapsed && !mobileOpen ? item.label : undefined}
                  >
                    <item.icon className="h-5 w-5 shrink-0" />
                    {(!collapsed || mobileOpen) && (
                      <span className="flex flex-1 items-center justify-between">
                        <span>{item.label}</span>
                        {item.to === '/' && openPRCount > 0 && (
                          <span className="ml-1 inline-flex h-5 min-w-[20px] items-center justify-center rounded-full bg-teal-500 px-1.5 text-[10px] font-bold text-white">
                            {openPRCount}
                          </span>
                        )}
                      </span>
                    )}
                    {collapsed && !mobileOpen && item.to === '/' && openPRCount > 0 && (
                      <span className="absolute -top-1 -right-1 inline-flex h-4 min-w-[16px] items-center justify-center rounded-full bg-teal-500 px-1 text-[9px] font-bold text-white">
                        {openPRCount}
                      </span>
                    )}
                  </NavLink>
                ))}
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
        <header className="flex h-14 shrink-0 items-center justify-between border-b border-[#6aade0] bg-[#f0f7ff] px-4 dark:border-gray-700 dark:bg-gray-900">
          {/* Left: mobile hamburger + breadcrumbs */}
          <div className="flex items-center gap-3">
            <button
              onClick={() => setMobileOpen(true)}
              className="rounded-lg p-2 text-[#2a5a7a] hover:bg-[#d6eeff] lg:hidden dark:text-gray-400 dark:hover:bg-gray-800"
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
                  : 'ring-2 ring-[#6aade0] bg-[#e8f4ff] text-[#2a5a7a] hover:bg-[#d6eeff] dark:border-gray-700 dark:bg-gray-800 dark:text-gray-400 dark:hover:bg-gray-700'
              }`}
              aria-label="Toggle AI Assistant"
            >
              <Sparkles className="h-3.5 w-3.5" />
              {!aiPanelOpen && <span className="hidden sm:inline">Ask AI</span>}
            </button>

            <NotificationBell />

            {/* Search trigger */}
            <button
              onClick={() => { const e = new KeyboardEvent('keydown', { key: 'k', metaKey: true }); window.dispatchEvent(e) }}
              className="hidden items-center gap-1.5 rounded-lg ring-2 ring-[#6aade0] bg-[#e8f4ff] px-3 py-1.5 text-xs text-[#3a6a8a] transition-colors hover:bg-[#d6eeff] sm:flex dark:border-gray-700 dark:bg-gray-800 dark:hover:bg-gray-700"
            >
              <Search className="h-3.5 w-3.5" />
              <span>Search...</span>
              <kbd className="ml-2 rounded border border-[#5a9dd0] bg-[#e8f4ff] px-1 py-0.5 text-[9px] font-medium dark:border-gray-600 dark:bg-gray-700">
                {navigator.platform?.includes('Mac') ? '⌘' : 'Ctrl'}K
              </kbd>
            </button>
            {!loading && activeConnection && (
              <div className="hidden items-center gap-2 rounded-lg ring-2 ring-[#6aade0] bg-[#e8f4ff] px-3 py-1.5 text-xs text-[#2a5a7a] sm:flex dark:border-gray-600 dark:bg-gray-800 dark:text-gray-400">
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
                  <div className="absolute right-0 top-10 z-50 w-48 rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] py-1 shadow-lg dark:border-gray-700 dark:bg-gray-800">
                    <button
                      onClick={() => { navigate('/user'); setUserMenuOpen(false) }}
                      className="flex w-full items-center gap-2 px-4 py-2 text-left text-sm text-[#0a3a5a] hover:bg-[#d6eeff] dark:text-gray-300 dark:hover:bg-gray-700"
                    >
                      <User className="h-4 w-4" />
                      Account
                    </button>
                    <button
                      onClick={() => { toggleTheme(); setUserMenuOpen(false) }}
                      className="flex w-full items-center gap-2 px-4 py-2 text-left text-sm text-[#0a3a5a] hover:bg-[#d6eeff] dark:text-gray-300 dark:hover:bg-gray-700"
                    >
                      {theme === 'dark' ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
                      {theme === 'dark' ? 'Light Mode' : 'Dark Mode'}
                    </button>
                    <div className="my-1 border-t border-[#6aade0] dark:border-gray-700" />
                    <button
                      onClick={logout}
                      className="flex w-full items-center gap-2 px-4 py-2 text-left text-sm text-red-600 hover:bg-[#d6eeff] dark:text-red-400 dark:hover:bg-gray-700"
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
        <>
          {/* Resize handle */}
          <div
            onMouseDown={handleMouseDown}
            className="w-1 cursor-col-resize bg-[#6aade0] hover:bg-teal-500 transition-colors dark:bg-gray-700 dark:hover:bg-teal-600"
          />
        <div style={{ width: aiPanelWidth }} className="flex shrink-0 flex-col border-l border-[#6aade0] bg-[#f0f7ff] dark:border-gray-700 dark:bg-gray-900">
          {/* Panel header */}
          <div className="flex h-14 items-center justify-between border-b border-[#6aade0] bg-gradient-to-r from-teal-600 to-blue-700 px-4 dark:border-gray-700">
            <div className="flex items-center gap-2 text-white">
              <Sparkles className="h-4 w-4" />
              <div>
                <span className="text-sm font-semibold" style={{ fontFamily: '"Quicksand", sans-serif', fontWeight: 700 }}>Sharko AI</span>
                {getAIPageContext(location.pathname) && (
                  <p className="text-[10px] text-teal-200">Viewing {getAIPageContext(location.pathname)}</p>
                )}
              </div>
            </div>
            <button
              onClick={() => { setAiPanelOpen(false); setAiInitialMessage(undefined) }}
              className="rounded-lg p-1 text-white/80 hover:bg-white/20 hover:text-white"
              aria-label="Close AI panel"
            >
              <X className="h-4 w-4" />
            </button>
          </div>
          {/* Chat content */}
          <div className="flex-1 overflow-hidden">
            <AIAssistant embedded pageContext={getAIPageContext(location.pathname)} initialMessage={aiInitialMessage} />
          </div>
        </div>
        </>
      )}

      {/* Floating AI Assistant */}
      <FloatingAssistant />

      {/* Command Palette (Cmd+K) */}
      <CommandPalette />

      {/* Toast notifications */}
      <ToastContainer />
    </div>
  )
}

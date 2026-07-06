import { useState, useEffect, useCallback, useRef } from 'react'
import { NavLink, Outlet, useNavigate, useLocation, Link } from 'react-router-dom'
import {
  LayoutDashboard,
  Server,
  Package,
  Activity,
  BarChart3,
  ClipboardList,
  Network,
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
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useTheme } from '@/hooks/useTheme'
import { useAuth } from '@/hooks/useAuth'
import { AIAssistant, type AIAssistantSeed } from '@/views/AIAssistant'
import { NotificationBell } from '@/components/NotificationBell'
import { ToastContainer } from '@/components/ToastNotification'
import { api, fetchTrackedPRs } from '@/services/api'

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
    // V2-cleanup-61.3 (A3): this section is all read-only pages (System /
    // Observability / External Dashboards / Audit) — "Manage" implied real
    // management actions that actually live under "Overview".
    label: 'Monitor',
    items: [
      { to: '/system', label: 'System', icon: Network },
      { to: '/observability', label: 'Observability', icon: Activity },
      // A4: "Dashboards" read as a sibling/typo of "Dashboard" above.
      { to: '/dashboards', label: 'External Dashboards', icon: BarChart3 },
      { to: '/audit', label: 'Audit Log', icon: ClipboardList },
    ],
  },
  {
    // V2-cleanup-61.3 (A6): non-admins have 5 sections allowlisted inside
    // Settings (Settings.tsx ALLOWED_NON_ADMIN) and SystemView links every
    // role there — but this section was adminOnly, so non-admins had NO nav
    // path to reach it at all. Show it for every role; Settings.tsx already
    // gates individual sections per role.
    label: 'Configure',
    items: [
      { to: '/settings', label: 'Settings', icon: Settings },
    ],
  },
]

const routeLabels: Record<string, string> = {
  dashboard: 'Dashboard',
  clusters: 'Clusters',
  addons: 'Addons Catalog',
  system: 'System',
  observability: 'Observability',
  dashboards: 'External Dashboards',
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
    '/system': 'the System page (Sharko/ArgoCD → repo/clusters chain)',
    '/observability': 'the Observability page',
    '/dashboards': 'the External Dashboards page (external dashboard links)',
    '/audit': 'the Audit Log page',
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
  const [aiSeed, setAiSeed] = useState<AIAssistantSeed | undefined>()
  const { theme, toggleTheme } = useTheme()
  const { logout, isAdmin } = useAuth()

  // -------------------------------------------------------------------------
  // AI assistant opt-in gate (V2-cleanup-55.4). THIS is where the gate lives.
  //
  // The assistant is OPT-IN and hidden by default: every entry point rendered
  // by this layout — the "Ask AI" top-bar button, the right-side chat panel,
  // the floating bubble (FloatingAssistant), and the `open-assistant` event
  // listener — renders/fires only when an AI provider is actually configured.
  //
  // "Configured" comes from GET /api/v1/upgrade/ai-status, which reports
  // `enabled: true` only when Settings → AI has a provider other than "none"
  // (see internal/ai/client.go IsEnabled). A default deployment has no AI
  // provider, so no assistant UI appears at all.
  //
  // To enable the assistant: Settings → AI → configure a provider.
  // The ask-AI affordances inside AddonDetail / ClusterDetail apply the same
  // gate via their own `aiEnabled` state.
  // -------------------------------------------------------------------------
  const [aiConfigured, setAiConfigured] = useState(false)
  useEffect(() => {
    // Defensive — older test fixtures may not mock getAIStatus.
    if (typeof api.getAIStatus !== 'function') return
    api
      .getAIStatus()
      .then((res) => setAiConfigured(!!res.enabled))
      .catch(() => setAiConfigured(false))
  }, [])

  const [appVersion, setAppVersion] = useState('')
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
      const maxWidth = Math.min(700, window.innerWidth - 400)
      const newWidth = Math.min(maxWidth, Math.max(320, window.innerWidth - e.clientX))
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
    // Assistant hidden by default (opt-in gate above): ignore open-assistant
    // events entirely when no AI provider is configured.
    if (!aiConfigured) return
    const handler = (e: Event) => {
      const detail = (e as CustomEvent).detail
      // Structured seed: { message: string, nonce: string }
      if (detail && typeof detail === 'object' && typeof detail.message === 'string' && detail.message) {
        setAiSeed({ message: detail.message, nonce: detail.nonce ?? crypto.randomUUID() })
      } else if (typeof detail === 'string' && detail) {
        // Backward-compat: plain string detail (legacy callers)
        setAiSeed({ message: detail, nonce: crypto.randomUUID() })
      }
      // detail is absent or empty → manual open; do not alter the seed
      openAiPanel()
    }
    window.addEventListener('open-assistant', handler)
    return () => window.removeEventListener('open-assistant', handler)
  }, [openAiPanel, aiConfigured])

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
                <p className="text-xs text-[#5a9ad0] leading-tight">v{appVersion}</p>
              )}
            </div>
          )}
        </Link>

        {/* Navigation */}
        <nav className="flex-1 overflow-y-auto px-2 py-3">
          {navSections.filter(s => !s.adminOnly || isAdmin).map((section, si) => (
            <div key={section.label} className={si > 0 ? 'mt-4 border-t border-[#14466e] pt-3' : ''}>
              {!collapsed && (
                <span className="mb-1 block px-3 text-xs font-semibold uppercase tracking-wider text-[#5a9ad0]">
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
                          <span
                            className="ml-1 inline-flex h-5 min-w-[20px] items-center justify-center rounded-full bg-teal-500 px-1.5 text-xs font-bold text-white"
                            title={`${openPRCount} open pull request${openPRCount !== 1 ? 's' : ''}`}
                            aria-label={`${openPRCount} open pull request${openPRCount !== 1 ? 's' : ''}`}
                          >
                            {openPRCount}
                          </span>
                        )}
                      </span>
                    )}
                    {/* V2-cleanup-65.1: kept at 9px deliberately — this is a
                      * superscript-style open-PR-count bubble overlaid on
                      * the collapsed-sidebar nav icon in a fixed h-4
                      * (16px) circle; bumping to text-xs (12px) doesn't fit
                      * inside the circle for 2-digit counts. The expanded
                      * (non-collapsed) sibling badge above has more room
                      * and was raised to text-xs. */}
                    {collapsed && !mobileOpen && item.to === '/' && openPRCount > 0 && (
                      <span
                        className="absolute -top-1 -right-1 inline-flex h-4 min-w-[16px] items-center justify-center rounded-full bg-teal-500 px-1 text-[9px] font-bold text-white"
                        title={`${openPRCount} open pull request${openPRCount !== 1 ? 's' : ''}`}
                        aria-label={`${openPRCount} open pull request${openPRCount !== 1 ? 's' : ''}`}
                      >
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
      <div className="flex flex-col overflow-hidden" style={{ flex: '1 1 0', minWidth: 400 }}>
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
            {/* AI panel toggle — only when an AI provider is configured (opt-in gate) */}
            {aiConfigured && (
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
                    : 'ring-2 ring-[#6aade0] bg-[#e8f4ff] text-[#2a5a7a] hover:bg-[#d6eeff] dark:ring-gray-700 dark:bg-gray-800 dark:text-gray-400 dark:hover:bg-gray-700'
                }`}
                aria-label="Toggle AI Assistant"
              >
                <Sparkles className="h-3.5 w-3.5" />
                {!aiPanelOpen && <span className="hidden sm:inline">Ask AI</span>}
              </button>
            )}

            <NotificationBell />

            {/* Search trigger */}
            <button
              onClick={() => { const e = new KeyboardEvent('keydown', { key: 'k', metaKey: true }); window.dispatchEvent(e) }}
              className="hidden items-center gap-1.5 rounded-lg ring-2 ring-[#6aade0] bg-[#e8f4ff] px-3 py-1.5 text-xs text-[#3a6a8a] transition-colors hover:bg-[#d6eeff] sm:flex dark:ring-gray-700 dark:bg-gray-800 dark:hover:bg-gray-700"
            >
              <Search className="h-3.5 w-3.5" />
              <span>Search...</span>
              <kbd className="ml-2 rounded border border-[#5a9dd0] bg-[#e8f4ff] px-1 py-0.5 text-xs font-medium dark:border-gray-600 dark:bg-gray-700">
                {navigator.platform?.includes('Mac') ? '⌘' : 'Ctrl'}K
              </kbd>
            </button>
            {!loading && activeConnection && (
              <div className="hidden items-center gap-2 rounded-lg ring-2 ring-[#6aade0] bg-[#e8f4ff] px-3 py-1.5 text-xs text-[#2a5a7a] sm:flex dark:border-gray-600 dark:bg-gray-800 dark:text-gray-400">
                <Plug className="h-3.5 w-3.5" />
                <span>{activeConnection}</span>
              </div>
            )}

            {/* User avatar dropdown. V2-cleanup-61.4 (G2): this used to be a
                hand-rolled `absolute` panel + a `fixed inset-0` click-catcher
                div for "outside click" — no Escape handling, no focus trap,
                no ARIA menu semantics. Swapped for the shadcn/Radix
                DropdownMenu primitive: Escape closes it, outside click
                closes it, arrow keys move between items, and focus returns
                to the avatar button on close, all for free from Radix. */}
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <button
                  className="flex h-8 w-8 items-center justify-center rounded-full bg-teal-600 text-sm font-bold text-white hover:bg-teal-700"
                  aria-label="User menu"
                >
                  <User className="h-4 w-4" />
                </button>
              </DropdownMenuTrigger>
              <DropdownMenuContent
                align="end"
                className="w-48 rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] dark:ring-gray-700 dark:bg-gray-800"
              >
                <DropdownMenuItem
                  onSelect={() => navigate('/user')}
                  className="text-[#0a3a5a] focus:bg-[#d6eeff] dark:text-gray-300 dark:focus:bg-gray-700"
                >
                  <User className="h-4 w-4" />
                  Account
                </DropdownMenuItem>
                <DropdownMenuItem
                  onSelect={() => toggleTheme()}
                  className="text-[#0a3a5a] focus:bg-[#d6eeff] dark:text-gray-300 dark:focus:bg-gray-700"
                >
                  {theme === 'dark' ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
                  {theme === 'dark' ? 'Light Mode' : 'Dark Mode'}
                </DropdownMenuItem>
                <DropdownMenuSeparator className="bg-[#6aade0] dark:bg-gray-700" />
                <DropdownMenuItem
                  variant="destructive"
                  onSelect={logout}
                  className="focus:bg-[#d6eeff] dark:focus:bg-gray-700"
                >
                  <LogOut className="h-4 w-4" />
                  Log out
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </header>

        {/* Content */}
        <main className="flex-1 overflow-auto">
          <div className="p-6 lg:p-8">
            <Outlet />
          </div>
        </main>
      </div>

      {/* AI Panel — right side; only when an AI provider is configured (opt-in gate) */}
      {aiConfigured && aiPanelOpen && (
        <>
          {/* Resize handle */}
          <div
            onMouseDown={handleMouseDown}
            className="group relative w-1.5 cursor-col-resize bg-[#6aade0] hover:bg-teal-500 transition-colors dark:bg-gray-700 dark:hover:bg-teal-600 flex items-center justify-center"
          >
            <div className="absolute flex flex-col gap-1 opacity-0 group-hover:opacity-100 transition-opacity pointer-events-none">
              <div className="h-1 w-1 rounded-full bg-white/80" />
              <div className="h-1 w-1 rounded-full bg-white/80" />
              <div className="h-1 w-1 rounded-full bg-white/80" />
            </div>
          </div>
        <div style={{ width: aiPanelWidth }} className="flex shrink-0 flex-col border-l border-[#6aade0] bg-[#f0f7ff] dark:border-gray-700 dark:bg-gray-900">
          {/* Panel header */}
          <div className="flex h-14 items-center justify-between border-b border-[#6aade0] bg-gradient-to-r from-teal-600 to-blue-700 px-4 dark:border-gray-700">
            <div className="flex items-center gap-2 text-white">
              <Sparkles className="h-4 w-4" />
              <div>
                <span className="text-sm font-semibold" style={{ fontFamily: '"Quicksand", sans-serif', fontWeight: 700 }}>Sharko AI</span>
                {getAIPageContext(location.pathname) && (
                  <p className="text-xs text-teal-200">Viewing {getAIPageContext(location.pathname)}</p>
                )}
              </div>
            </div>
            <button
              onClick={() => { setAiPanelOpen(false); setAiSeed(undefined) }}
              className="rounded-lg p-1 text-white/80 hover:bg-white/20 hover:text-white"
              aria-label="Close AI panel"
            >
              <X className="h-4 w-4" />
            </button>
          </div>
          {/* Chat content */}
          <div className="flex-1 overflow-hidden">
            <AIAssistant embedded pageContext={getAIPageContext(location.pathname)} initialMessageSeed={aiSeed} />
          </div>
        </div>
        </>
      )}

      {/* Floating AI Assistant — only when an AI provider is configured (opt-in gate) */}
      {aiConfigured && <FloatingAssistant />}

      {/* Command Palette (Cmd+K) */}
      <CommandPalette />

      {/* Toast notifications */}
      <ToastContainer />
    </div>
  )
}

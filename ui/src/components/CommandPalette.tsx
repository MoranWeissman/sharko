import { useState, useEffect, useCallback, useRef, useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { Search, Server, Package, FileText, LayoutDashboard, Activity, ArrowUpCircle, GitPullRequest, Settings, BookOpen } from 'lucide-react'
import { api } from '@/services/api'

interface SearchResult {
  label: string
  type: 'page' | 'cluster' | 'addon'
  path: string
  icon: typeof Search
}

const PAGE_RESULTS: SearchResult[] = [
  { label: 'Dashboard', type: 'page', path: '/dashboard', icon: LayoutDashboard },
  { label: 'Clusters', type: 'page', path: '/clusters', icon: Server },
  { label: 'Add-ons Catalog', type: 'page', path: '/addons', icon: Package },
  { label: 'Version Matrix', type: 'page', path: '/version-matrix', icon: FileText },
  { label: 'Observability', type: 'page', path: '/observability', icon: Activity },
  { label: 'Upgrade Checker', type: 'page', path: '/upgrade', icon: ArrowUpCircle },
  { label: 'Migration', type: 'page', path: '/migration', icon: GitPullRequest },
  { label: 'Settings', type: 'page', path: '/settings', icon: Settings },
  { label: 'Docs', type: 'page', path: '/docs', icon: BookOpen },
]

export function CommandPalette() {
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [selectedIndex, setSelectedIndex] = useState(0)
  const [clusters, setClusters] = useState<string[]>([])
  const [addons, setAddons] = useState<string[]>([])
  const inputRef = useRef<HTMLInputElement>(null)

  // Global keyboard shortcut
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault()
        setOpen(true)
      }
      if (e.key === 'Escape') {
        setOpen(false)
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [])

  // Focus input when opened
  useEffect(() => {
    if (open) {
      setQuery('')
      setSelectedIndex(0)
      setTimeout(() => inputRef.current?.focus(), 50)
    }
  }, [open])

  // Load clusters and addons for search
  useEffect(() => {
    if (!open) return
    api.getClusters().then(r => setClusters((r?.clusters || []).map(cl => cl.name))).catch(() => {})
    api.getAddonCatalog().then(r => setAddons((r?.addons || []).map(ad => ad.addon_name))).catch(() => {})
  }, [open])

  // Build search results
  const results = useMemo(() => {
    const term = query.toLowerCase().trim()
    if (!term) return PAGE_RESULTS.slice(0, 6) // show top pages when empty

    const matches: SearchResult[] = []

    // Pages
    for (const p of PAGE_RESULTS) {
      if (p.label.toLowerCase().includes(term)) {
        matches.push(p)
      }
    }

    // Clusters
    for (const name of clusters) {
      if (name.toLowerCase().includes(term)) {
        matches.push({ label: name, type: 'cluster', path: `/clusters/${name}`, icon: Server })
      }
    }

    // Addons
    for (const name of addons) {
      if (name.toLowerCase().includes(term)) {
        matches.push({ label: name, type: 'addon', path: `/addons/${name}`, icon: Package })
      }
    }

    return matches.slice(0, 10)
  }, [query, clusters, addons])

  // Reset selection when results change
  useEffect(() => {
    setSelectedIndex(0)
  }, [results])

  const select = useCallback((result: SearchResult) => {
    navigate(result.path)
    setOpen(false)
  }, [navigate])

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setSelectedIndex(i => Math.min(i + 1, results.length - 1))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setSelectedIndex(i => Math.max(i - 1, 0))
    } else if (e.key === 'Enter' && results[selectedIndex]) {
      e.preventDefault()
      select(results[selectedIndex])
    }
  }

  if (!open) return null

  const typeLabel = { page: 'Page', cluster: 'Cluster', addon: 'Add-on' }

  return (
    <>
      {/* Backdrop */}
      <div className="fixed inset-0 z-[60] bg-black/50 backdrop-blur-sm" onClick={() => setOpen(false)} />

      {/* Modal */}
      <div className="fixed left-1/2 top-[20%] z-[60] w-full max-w-lg -translate-x-1/2">
        <div className="overflow-hidden rounded-xl border border-gray-200 bg-white shadow-2xl dark:border-gray-700 dark:bg-gray-900">
          {/* Search input */}
          <div className="flex items-center gap-3 border-b border-gray-200 px-4 dark:border-gray-700">
            <Search className="h-5 w-5 shrink-0 text-gray-400" />
            <input
              ref={inputRef}
              type="text"
              value={query}
              onChange={e => setQuery(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder="Search pages, clusters, add-ons..."
              className="w-full bg-transparent py-3.5 text-sm text-gray-900 placeholder-gray-400 outline-none dark:text-gray-100"
            />
            <kbd className="hidden shrink-0 rounded border border-gray-200 bg-gray-50 px-1.5 py-0.5 text-[10px] font-medium text-gray-400 sm:block dark:border-gray-700 dark:bg-gray-800">
              ESC
            </kbd>
          </div>

          {/* Results */}
          <div className="max-h-72 overflow-y-auto py-2">
            {results.length === 0 ? (
              <div className="px-4 py-6 text-center text-sm text-gray-500">
                No results for "{query}"
              </div>
            ) : (
              results.map((result, i) => (
                <button
                  key={result.path}
                  onClick={() => select(result)}
                  onMouseEnter={() => setSelectedIndex(i)}
                  className={`flex w-full items-center gap-3 px-4 py-2.5 text-left text-sm transition-colors ${
                    i === selectedIndex
                      ? 'bg-cyan-50 text-cyan-700 dark:bg-cyan-900/20 dark:text-cyan-400'
                      : 'text-gray-700 hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-gray-800'
                  }`}
                >
                  <result.icon className="h-4 w-4 shrink-0 text-gray-400" />
                  <span className="flex-1 truncate">{result.label}</span>
                  <span className="shrink-0 text-[10px] uppercase tracking-wide text-gray-400">
                    {typeLabel[result.type]}
                  </span>
                </button>
              ))
            )}
          </div>

          {/* Footer hint */}
          <div className="border-t border-gray-200 px-4 py-2 text-[10px] text-gray-400 dark:border-gray-700">
            <span className="mr-3">Arrow keys to navigate</span>
            <span className="mr-3">Enter to select</span>
            <span>Esc to close</span>
          </div>
        </div>
      </div>
    </>
  )
}

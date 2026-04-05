import { useState, useEffect, useCallback, useRef, useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { Search, Server, Package, FileText, LayoutDashboard, Activity, ArrowUpCircle, Settings, BookOpen } from 'lucide-react'
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
  { label: 'Addons Catalog', type: 'page', path: '/addons', icon: Package },
  { label: 'Version Drift Detector', type: 'page', path: '/version-matrix', icon: FileText },
  { label: 'Observability', type: 'page', path: '/observability', icon: Activity },
  { label: 'Upgrade Checker', type: 'page', path: '/upgrade', icon: ArrowUpCircle },
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

  const typeLabel = { page: 'Page', cluster: 'Cluster', addon: 'Addon' }

  return (
    <>
      {/* Backdrop */}
      <div className="fixed inset-0 z-[60] bg-black/50 backdrop-blur-sm" onClick={() => setOpen(false)} />

      {/* Modal */}
      <div className="fixed left-1/2 top-[20%] z-[60] w-full max-w-lg -translate-x-1/2">
        <div className="overflow-hidden rounded-xl border-2 border-[#6aade0] bg-[#f0f7ff] shadow-2xl dark:border-gray-700 dark:bg-gray-900">
          {/* Search input */}
          <div className="flex items-center gap-3 border-b border-[#6aade0] px-4 dark:border-gray-700">
            <Search className="h-5 w-5 shrink-0 text-[#3a6a8a]" />
            <input
              ref={inputRef}
              type="text"
              value={query}
              onChange={e => setQuery(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder="Search pages, clusters, addons..."
              className="w-full bg-transparent py-3.5 text-sm text-[#0a2a4a] placeholder-[#5a8aaa] outline-none dark:text-gray-100"
            />
            <kbd className="hidden shrink-0 rounded border-2 border-[#6aade0] bg-[#e8f4ff] px-1.5 py-0.5 text-[10px] font-medium text-[#3a6a8a] sm:block dark:border-gray-700 dark:bg-gray-800">
              ESC
            </kbd>
          </div>

          {/* Results */}
          <div className="max-h-72 overflow-y-auto py-2">
            {results.length === 0 ? (
              <div className="px-4 py-6 text-center text-sm text-[#2a5a7a]">
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
                      ? 'bg-teal-50 text-teal-700 dark:bg-teal-900/20 dark:text-teal-400'
                      : 'text-[#0a3a5a] hover:bg-[#e8f4ff] dark:text-gray-300 dark:hover:bg-gray-800'
                  }`}
                >
                  <result.icon className="h-4 w-4 shrink-0 text-[#3a6a8a]" />
                  <span className="flex-1 truncate">{result.label}</span>
                  <span className="shrink-0 text-[10px] uppercase tracking-wide text-[#3a6a8a]">
                    {typeLabel[result.type]}
                  </span>
                </button>
              ))
            )}
          </div>

          {/* Footer hint */}
          <div className="border-t border-[#6aade0] px-4 py-2 text-[10px] text-[#3a6a8a] dark:border-gray-700">
            <span className="mr-3">Arrow keys to navigate</span>
            <span className="mr-3">Enter to select</span>
            <span>Esc to close</span>
          </div>
        </div>
      </div>
    </>
  )
}

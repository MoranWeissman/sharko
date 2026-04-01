import { useState, useEffect, useCallback, useMemo } from 'react'
import { BookOpen, Search } from 'lucide-react'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { api } from '@/services/api'

interface DocEntry {
  slug: string
  title: string
  order: number
}

interface DocContent {
  slug: string
  content: string
}

export function Docs() {
  const [pages, setPages] = useState<DocEntry[]>([])
  const [activeSlug, setActiveSlug] = useState<string>('')
  const [content, setContent] = useState<string>('')
  const [loading, setLoading] = useState(true)
  const [search, setSearch] = useState('')
  const [allContent, setAllContent] = useState<Map<string, string>>(new Map())

  // Load page list
  useEffect(() => {
    api.docsList().then((list) => {
      setPages(list)
      if (list.length > 0 && !activeSlug) {
        setActiveSlug(list[0].slug)
      }
    }).catch(() => {}).finally(() => setLoading(false))
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Load content when page changes
  const loadContent = useCallback(async (slug: string) => {
    if (allContent.has(slug)) {
      setContent(allContent.get(slug) || '')
      return
    }
    try {
      const doc: DocContent = await api.docsGet(slug)
      setContent(doc.content)
      setAllContent(prev => new Map(prev).set(slug, doc.content))
    } catch {
      setContent('# Not Found\n\nThis document could not be loaded.')
    }
  }, [allContent])

  useEffect(() => {
    if (activeSlug) {
      void loadContent(activeSlug)
    }
  }, [activeSlug, loadContent])

  // Pre-load all docs for search
  useEffect(() => {
    if (pages.length === 0) return
    pages.forEach(p => {
      if (!allContent.has(p.slug)) {
        api.docsGet(p.slug).then((doc: DocContent) => {
          setAllContent(prev => new Map(prev).set(p.slug, doc.content))
        }).catch(() => {})
      }
    })
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pages])

  // Search results
  const searchResults = useMemo(() => {
    if (!search.trim()) return null
    const term = search.toLowerCase()
    const results: { slug: string; title: string; snippet: string }[] = []
    for (const page of pages) {
      const text = allContent.get(page.slug) || ''
      const idx = text.toLowerCase().indexOf(term)
      if (idx >= 0) {
        const start = Math.max(0, idx - 40)
        const end = Math.min(text.length, idx + term.length + 80)
        let snippet = text.slice(start, end).replace(/\n/g, ' ')
        if (start > 0) snippet = '...' + snippet
        if (end < text.length) snippet = snippet + '...'
        results.push({ slug: page.slug, title: page.title, snippet })
      }
    }
    return results
  }, [search, pages, allContent])

  if (loading) {
    return (
      <div className="flex items-center justify-center py-20 text-gray-500">
        Loading documentation...
      </div>
    )
  }

  return (
    <div>
      <div className="mb-6 flex items-center gap-3">
        <BookOpen className="h-7 w-7 text-cyan-600 dark:text-cyan-400" />
        <h1 className="text-2xl font-bold text-gray-900 dark:text-white">
          Documentation
        </h1>
      </div>

      <div className="flex gap-6">
        {/* Left sidebar */}
        <nav className="w-56 shrink-0 space-y-4">
          {/* Search */}
          <div className="relative">
            <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-gray-400" />
            <input
              type="text"
              placeholder="Search docs..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full rounded-lg border border-gray-200 bg-white py-2 pl-9 pr-3 text-sm text-gray-900 placeholder-gray-400 focus:border-cyan-500 focus:outline-none focus:ring-1 focus:ring-cyan-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100"
            />
          </div>

          {/* Search results */}
          {searchResults && (
            <div className="rounded-lg border border-cyan-200 bg-cyan-50 p-3 dark:border-cyan-800 dark:bg-cyan-900/20">
              <p className="mb-2 text-xs font-medium text-cyan-700 dark:text-cyan-400">
                {searchResults.length} result{searchResults.length !== 1 ? 's' : ''}
              </p>
              {searchResults.length === 0 ? (
                <p className="text-xs text-gray-500">No matches found.</p>
              ) : (
                <ul className="space-y-2">
                  {searchResults.map((r) => (
                    <li key={r.slug}>
                      <button
                        onClick={() => { setActiveSlug(r.slug); setSearch('') }}
                        className="block w-full text-left"
                      >
                        <span className="text-xs font-medium text-cyan-700 dark:text-cyan-400">{r.title}</span>
                        <span className="block truncate text-xs text-gray-500">{r.snippet}</span>
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          )}

          {/* Page navigation */}
          <ul className="space-y-1">
            {pages.map((page) => {
              const isActive = page.slug === activeSlug
              return (
                <li key={page.slug}>
                  <button
                    onClick={() => { setActiveSlug(page.slug); setSearch('') }}
                    className={`flex w-full items-center gap-2.5 rounded-lg px-3 py-2 text-left text-sm font-medium transition-colors ${
                      isActive
                        ? 'bg-cyan-50 text-cyan-700 dark:bg-cyan-900/30 dark:text-cyan-400'
                        : 'text-gray-600 hover:bg-gray-100 hover:text-gray-900 dark:text-gray-400 dark:hover:bg-gray-800 dark:hover:text-gray-200'
                    }`}
                  >
                    <span>{page.title}</span>
                  </button>
                </li>
              )
            })}
          </ul>
        </nav>

        {/* Right content area */}
        <div className="min-w-0 flex-1 rounded-xl border border-gray-200 bg-white p-8 shadow-sm dark:border-gray-700 dark:bg-gray-900">
          <article className="prose prose-gray max-w-none dark:prose-invert prose-headings:text-gray-900 dark:prose-headings:text-white prose-a:text-cyan-600 dark:prose-a:text-cyan-400 prose-code:rounded prose-code:bg-gray-100 prose-code:px-1.5 prose-code:py-0.5 prose-code:text-sm dark:prose-code:bg-gray-800 prose-pre:bg-gray-900 prose-pre:text-gray-100">
            <Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown>
          </article>
        </div>
      </div>
    </div>
  )
}

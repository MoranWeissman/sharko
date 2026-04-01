import { useState, useEffect } from 'react'
import { useLocation } from 'react-router-dom'
import { MessageSquare, X, Sparkles } from 'lucide-react'
import { AIAssistant } from '@/views/AIAssistant'

const routeContext: Record<string, string> = {
  '/dashboard': 'the Dashboard (overview stats)',
  '/clusters': 'the Clusters page',
  '/addons': 'the Add-ons Catalog',
  '/version-matrix': 'the Version Matrix',
  '/observability': 'the Observability page',
  '/upgrade': 'the Add-on Upgrade Checker',
  '/migration': 'the Migration page',
  '/settings': 'the Settings page',
  '/docs': 'the Documentation',
}

function getPageContext(pathname: string): string | undefined {
  // Exact match first
  if (routeContext[pathname]) return routeContext[pathname]
  // Dynamic routes
  if (pathname.startsWith('/clusters/')) {
    const name = pathname.split('/')[2]
    return `the Cluster Detail page for "${name}"`
  }
  if (pathname.startsWith('/addons/')) {
    const name = pathname.split('/')[2]
    return `the Add-on Detail page for "${name}"`
  }
  if (pathname.startsWith('/migration/')) {
    const id = pathname.split('/')[2]
    return `Migration Detail for migration "${id}"`
  }
  return undefined
}

export function FloatingAssistant() {
  const [open, setOpen] = useState(false)
  const [initialMessage, setInitialMessage] = useState<string | undefined>()
  const location = useLocation()
  const pageContext = getPageContext(location.pathname)

  // Listen for programmatic open requests from other components
  useEffect(() => {
    const handler = (e: Event) => {
      const msg = (e as CustomEvent<string>).detail
      setInitialMessage(msg || undefined)
      setOpen(true)
    }
    window.addEventListener('open-assistant', handler)
    return () => window.removeEventListener('open-assistant', handler)
  }, [])

  return (
    <>
      {/* Chat panel */}
      {open && (
        <div className="fixed bottom-20 right-6 z-50 flex h-[600px] w-[420px] flex-col overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-2xl dark:border-gray-700 dark:bg-gray-900">
          {/* Header */}
          <div className="flex items-center justify-between border-b border-gray-200 bg-gradient-to-r from-cyan-600 to-blue-700 px-4 py-3 dark:border-gray-700">
            <div className="flex items-center gap-2 text-white">
              <Sparkles className="h-4 w-4" />
              <span className="text-sm font-semibold">AI Assistant</span>
              {pageContext && (
                <span className="text-[10px] text-cyan-200">on {pageContext}</span>
              )}
            </div>
            <button
              onClick={() => setOpen(false)}
              className="rounded-lg p-1 text-white/80 hover:bg-white/20 hover:text-white"
            >
              <X className="h-4 w-4" />
            </button>
          </div>

          {/* Chat content — reuse the full AIAssistant component */}
          <div className="flex-1 overflow-hidden">
            <AIAssistant embedded pageContext={pageContext} initialMessage={initialMessage} />
          </div>
        </div>
      )}

      {/* Floating bubble */}
      <button
        onClick={() => setOpen((o) => !o)}
        className={`fixed bottom-6 right-6 z-50 flex h-14 w-14 items-center justify-center rounded-full shadow-lg transition-all duration-200 ${
          open
            ? 'bg-gray-600 hover:bg-gray-700'
            : 'bg-gradient-to-br from-cyan-500 to-blue-600 hover:from-cyan-600 hover:to-blue-700'
        }`}
        aria-label={open ? 'Close AI Assistant' : 'Open AI Assistant'}
      >
        {open ? (
          <X className="h-6 w-6 text-white" />
        ) : (
          <MessageSquare className="h-6 w-6 text-white" />
        )}
      </button>
    </>
  )
}

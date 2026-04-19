import { useCallback, useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Clipboard, Compass, Search } from 'lucide-react'
import { cn } from '@/lib/utils'
import { MarketplaceBrowseTab } from '@/components/MarketplaceBrowseTab'
import { MarketplaceSearchTab } from '@/components/MarketplaceSearchTab'
import { MarketplacePasteURLTab } from '@/components/MarketplacePasteURLTab'

/**
 * MarketplaceTab — top-level container rendered inside the AddonCatalog page.
 * Hosts three subtabs:
 *
 *   Browse    — curated catalog grid with filters (V121-2)
 *   Search    — name search across curated + ArtifactHub (V121-3.6)
 *   Paste URL — power-user repo+chart validator (V121-4.1)
 *
 * Subtab state lives in the URL (?mp_view=browse|search|paste) so deep links
 * share. Default is Browse to preserve the v1.20→v1.21 upgrade UX.
 */

type MarketplaceView = 'browse' | 'search' | 'paste'

const VALID_VIEWS: MarketplaceView[] = ['browse', 'search', 'paste']

function parseView(params: URLSearchParams): MarketplaceView {
  const v = params.get('mp_view')
  return VALID_VIEWS.includes(v as MarketplaceView) ? (v as MarketplaceView) : 'browse'
}

function panelLabel(view: MarketplaceView): string {
  switch (view) {
    case 'browse':
      return 'Browse curated catalog'
    case 'search':
      return 'Search any chart'
    case 'paste':
      return 'Paste a Helm repo URL'
  }
}

export function MarketplaceTab() {
  const [searchParams, setSearchParams] = useSearchParams()
  const view = useMemo(() => parseView(searchParams), [searchParams])

  const setView = useCallback(
    (next: MarketplaceView) => {
      const out = new URLSearchParams(searchParams.toString())
      if (next === 'browse') out.delete('mp_view')
      else out.set('mp_view', next)
      setSearchParams(out, { replace: true })
    },
    [searchParams, setSearchParams],
  )

  return (
    <div className="flex flex-col gap-4">
      <div
        role="tablist"
        aria-label="Marketplace view"
        className="inline-flex w-fit rounded-md border border-[#5a9dd0] bg-white p-0.5 dark:border-gray-600 dark:bg-gray-800"
      >
        <SubTabButton
          active={view === 'browse'}
          onClick={() => setView('browse')}
          icon={<Compass className="h-3.5 w-3.5" aria-hidden="true" />}
          label="Browse curated"
        />
        <SubTabButton
          active={view === 'search'}
          onClick={() => setView('search')}
          icon={<Search className="h-3.5 w-3.5" aria-hidden="true" />}
          label="Search ArtifactHub"
        />
        <SubTabButton
          active={view === 'paste'}
          onClick={() => setView('paste')}
          icon={<Clipboard className="h-3.5 w-3.5" aria-hidden="true" />}
          label="Paste Helm URL"
        />
      </div>

      <div role="tabpanel" aria-label={panelLabel(view)}>
        {view === 'browse' && <MarketplaceBrowseTab />}
        {view === 'search' && <MarketplaceSearchTab />}
        {view === 'paste' && <MarketplacePasteURLTab />}
      </div>
    </div>
  )
}

interface SubTabButtonProps {
  active: boolean
  onClick: () => void
  icon: React.ReactNode
  label: string
}

function SubTabButton({ active, onClick, icon, label }: SubTabButtonProps) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active}
      onClick={onClick}
      className={cn(
        'inline-flex items-center gap-1.5 rounded px-3 py-1.5 text-xs font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-teal-500',
        active
          ? 'bg-teal-600 text-white shadow-sm dark:bg-teal-700'
          : 'text-[#2a5a7a] hover:bg-[#f0f7ff] dark:text-gray-300 dark:hover:bg-gray-700',
      )}
    >
      {icon}
      {label}
    </button>
  )
}

export default MarketplaceTab

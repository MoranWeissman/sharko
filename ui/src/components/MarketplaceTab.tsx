import { useCallback, useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Compass, Search } from 'lucide-react'
import { cn } from '@/lib/utils'
import { MarketplaceBrowseTab } from '@/components/MarketplaceBrowseTab'
import { MarketplaceSearchTab } from '@/components/MarketplaceSearchTab'
import { MarketplaceAddonDetail } from '@/components/MarketplaceAddonDetail'

/**
 * MarketplaceTab — top-level container rendered inside the AddonCatalog page.
 *
 * v1.21 QA Bundle 2 simplified the surface:
 *
 *   Browse   — curated catalog grid with filters (V121-2)
 *   Search   — name search across curated + ArtifactHub (V121-3.6)
 *   (Detail) — in-page addon detail view replaces the tablist when
 *              ?mp_addon=<name> is set on the URL. Maintainer feedback
 *              (2026-04-19): the popup-style Configure modal was too cramped;
 *              an in-page view shows README + an embedded "Add to catalog"
 *              panel without losing the Marketplace context.
 *
 * The Paste-URL subtab was retired in Bundle 2 — the manual Add Addon form
 * on the AddonCatalog page is the canonical "non-marketplace" entry point
 * (it auto-validates the repo URL and surfaces a chart-name dropdown).
 *
 * Subtab state lives in the URL (?mp_view=browse|search) so deep links share.
 * Default is Browse to preserve the v1.20→v1.21 upgrade UX. The detail-view
 * navigation uses ?mp_addon=<name> and preserves any existing filter state in
 * mp_q / mp_cat / mp_curated / mp_lic / mp_tier so "← Back" returns the user
 * exactly where they came from.
 */

type MarketplaceView = 'browse' | 'search'

const VALID_VIEWS: MarketplaceView[] = ['browse', 'search']

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
  }
}

export function MarketplaceTab() {
  const [searchParams, setSearchParams] = useSearchParams()
  const view = useMemo(() => parseView(searchParams), [searchParams])
  const detailAddon = searchParams.get('mp_addon')?.trim() || null

  // Detail-view source — set by the Search-result click path so the
  // detail component knows whether to fetch via the curated endpoint
  // (?mp_src=curated, default) or the ArtifactHub package endpoint
  // (?mp_src=ah&mp_repo=<ahrepo>).
  const detailSource = (searchParams.get('mp_src') ?? 'curated') as
    | 'curated'
    | 'ah'
  const detailRepo = searchParams.get('mp_repo')?.trim() || null

  const setView = useCallback(
    (next: MarketplaceView) => {
      const out = new URLSearchParams(searchParams.toString())
      // Switching subtab implicitly leaves the detail view.
      out.delete('mp_addon')
      out.delete('mp_src')
      out.delete('mp_repo')
      if (next === 'browse') out.delete('mp_view')
      else out.set('mp_view', next)
      setSearchParams(out, { replace: true })
    },
    [searchParams, setSearchParams],
  )

  const handleBackToBrowse = useCallback(() => {
    const out = new URLSearchParams(searchParams.toString())
    out.delete('mp_addon')
    out.delete('mp_src')
    out.delete('mp_repo')
    setSearchParams(out, { replace: false })
  }, [searchParams, setSearchParams])

  // Detail-view short-circuit. Return early so the tablist isn't rendered;
  // the back-link inside the detail header restores it.
  if (detailAddon) {
    return (
      <MarketplaceAddonDetail
        addonName={detailAddon}
        source={detailSource === 'ah' ? 'ah' : 'curated'}
        ahRepoName={detailRepo}
        onBack={handleBackToBrowse}
      />
    )
  }

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
      </div>

      <div role="tabpanel" aria-label={panelLabel(view)}>
        {view === 'browse' && <MarketplaceBrowseTab />}
        {view === 'search' && <MarketplaceSearchTab />}
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

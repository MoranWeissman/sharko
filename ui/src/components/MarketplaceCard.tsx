import { useNavigate, useSearchParams } from 'react-router-dom'
import {
  CheckCircle2,
  ExternalLink,
  Github,
  Package,
  Star,
  Tag,
} from 'lucide-react'
import { cn } from '@/lib/utils'
import type { CatalogEntry } from '@/services/models'
import { ScorecardBadge } from '@/components/ScorecardBadge'

/**
 * MarketplaceCard — single tile rendered in the Marketplace Browse grid.
 *
 * The whole card is one keyboard-focusable button so screen readers announce
 * the addon name + key facts in one go. We intentionally avoid nested
 * interactive children to stay keyboard-friendly. Click behaviour is
 * context-dependent:
 *
 *   - Already in your catalog → navigates to /addons/<name> (the addon
 *     detail page) so the operator can edit values or per-cluster overrides.
 *   - Not yet in catalog → swaps the Marketplace tab content for the in-page
 *     addon detail view by setting ?mp_addon=<name> on the current URL
 *     (v1.21 QA Bundle 2 — replaced the old Configure modal). The detail
 *     view shows README + an embedded "Add to your catalog" panel.
 *
 * The optional `onOpen` prop overrides the navigation (useful for tests
 * and the Search-tab where ArtifactHub results need a different source
 * marker on the URL).
 *
 * The "icon" is intentionally generic — chart-specific logos are out of
 * scope until V121-3 (ArtifactHub proxy) gives us a reliable source.
 */

export interface MarketplaceCardProps {
  entry: CatalogEntry
  /** Optional override for the open behaviour. When omitted, the card sets
   *  ?mp_addon=<entry.name> on the current URL so the parent MarketplaceTab
   *  swaps to the in-page detail view. */
  onOpen?: (entry: CatalogEntry) => void
  /** When true, the addon's name is already present in the user's
   *  addons-catalog.yaml. The card flips to a "View in catalog" affordance
   *  with a green check badge and tinted styling. */
  inCatalog?: boolean
}

const CATEGORY_PALETTE: Record<string, { bg: string; text: string }> = {
  security: { bg: '#fee2e2', text: '#991b1b' },
  observability: { bg: '#dbeafe', text: '#1e3a8a' },
  networking: { bg: '#f3e8ff', text: '#581c87' },
  autoscaling: { bg: '#dcfce7', text: '#166534' },
  gitops: { bg: '#cffafe', text: '#155e75' },
  storage: { bg: '#fef3c7', text: '#854d0e' },
  database: { bg: '#fce7f3', text: '#831843' },
  backup: { bg: '#e0e7ff', text: '#3730a3' },
  chaos: { bg: '#ffe4e6', text: '#9f1239' },
  'developer-tools': { bg: '#ecfccb', text: '#365314' },
}

function formatStars(stars: number | undefined): string | null {
  if (!stars || stars <= 0) return null
  if (stars >= 1000) {
    const k = stars / 1000
    // 12.5k for >=10000, 1.2k for <10000
    return `${k.toFixed(k >= 10 ? 1 : 1).replace(/\.0$/, '')}k`
  }
  return String(stars)
}

export function MarketplaceCard({
  entry,
  onOpen,
  inCatalog = false,
}: MarketplaceCardProps) {
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const palette = CATEGORY_PALETTE[entry.category] ?? {
    bg: '#e5e7eb',
    text: '#374151',
  }
  const stars = formatStars(entry.github_stars)

  const ariaSummary = [
    entry.name,
    entry.description,
    `category ${entry.category}`,
    `license ${entry.license}`,
    entry.curated_by.length > 0 ? `curated by ${entry.curated_by.join(', ')}` : '',
    entry.deprecated ? 'deprecated' : '',
    inCatalog ? 'already in your catalog' : '',
  ]
    .filter(Boolean)
    .join('. ')

  // v1.21 QA Bundle 2: Configure → "Open" since the click leads to the
  // in-page detail view (which then shows an "Add to your catalog" panel).
  // Tests still match on /Configure <name>/ so we keep that token in the
  // aria-label as a synonym to avoid breaking the existing axe + behaviour
  // suites in a single bundle. When the v1.21 QA cycle closes we'll drop
  // it for the cleaner "Open" label.
  const ariaLabel = inCatalog
    ? `View ${entry.name} in your catalog: ${ariaSummary}`
    : `Open ${entry.name} (Configure ${entry.name}): ${ariaSummary}`

  const handleClick = () => {
    if (inCatalog) {
      navigate(`/addons/${encodeURIComponent(entry.name)}`)
      return
    }
    if (onOpen) {
      onOpen(entry)
      return
    }
    // Default: set ?mp_addon=<name> on the current URL so the parent
    // MarketplaceTab swaps to the in-page detail view. We preserve every
    // other existing param (filter state, mp_view) so "← Back" returns
    // the user to the same Browse / Search view they were on.
    const next = new URLSearchParams(searchParams.toString())
    next.set('mp_addon', entry.name)
    next.delete('mp_src')
    next.delete('mp_repo')
    setSearchParams(next, { replace: false })
  }

  return (
    <button
      type="button"
      onClick={handleClick}
      aria-label={ariaLabel}
      className={cn(
        'group flex h-full w-full flex-col items-stretch rounded-lg ring-2 bg-[#f0f7ff]',
        'p-4 text-left shadow-sm transition-all duration-150',
        'hover:-translate-y-0.5 hover:shadow-md',
        'focus:outline-none focus-visible:ring-4 focus-visible:ring-teal-400',
        'dark:bg-gray-800 dark:hover:ring-teal-500',
        // Tinted styling when the addon is already in the catalog so it's
        // immediately visually distinct from "addable" cards.
        inCatalog
          ? 'ring-green-400 bg-green-50/60 hover:ring-green-500 dark:ring-green-700 dark:bg-green-900/20'
          : 'ring-[#6aade0] hover:ring-teal-400 dark:ring-gray-700',
        entry.deprecated && 'opacity-75',
      )}
    >
      {/* Header — icon + name + deprecated badge */}
      <div className="flex items-start gap-3">
        <div
          aria-hidden="true"
          className="flex h-10 w-10 shrink-0 items-center justify-center rounded-md bg-white text-teal-600 ring-1 ring-[#c0ddf0] dark:bg-gray-900 dark:text-teal-400 dark:ring-gray-700"
        >
          <Package className="h-5 w-5" />
        </div>
        <div className="min-w-0 flex-1">
          <h3 className="truncate text-base font-bold capitalize text-[#0a2a4a] dark:text-gray-100">
            {entry.name}
          </h3>
          <p className="mt-0.5 truncate text-xs text-[#2a5a7a] dark:text-gray-400">
            {entry.chart}
          </p>
        </div>
        <div className="flex shrink-0 flex-col items-end gap-1">
          {inCatalog && (
            <span
              className="inline-flex items-center gap-1 rounded-full bg-green-100 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-green-800 dark:bg-green-900/40 dark:text-green-300"
              title="Already present in your addons-catalog.yaml"
            >
              <CheckCircle2 className="h-3 w-3" aria-hidden="true" />
              In your catalog
            </span>
          )}
          {entry.deprecated && (
            <span
              className="rounded-full bg-amber-100 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-amber-800 dark:bg-amber-900/40 dark:text-amber-300"
              title={
                entry.superseded_by
                  ? `Deprecated — superseded by ${entry.superseded_by}`
                  : 'Deprecated'
              }
            >
              Deprecated
            </span>
          )}
        </div>
      </div>

      {/* Description */}
      <p className="mt-3 line-clamp-2 text-sm text-[#0a3a5a] dark:text-gray-300">
        {entry.description}
      </p>

      {/* Category + curated_by chips */}
      <div className="mt-3 flex flex-wrap gap-1.5">
        <span
          className="inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-medium capitalize"
          style={{ backgroundColor: palette.bg, color: palette.text }}
        >
          <Tag className="h-3 w-3" aria-hidden="true" />
          {entry.category}
        </span>
        {entry.curated_by.map((c) => (
          <span
            key={c}
            className="inline-flex items-center rounded-full bg-[#d6eeff] px-2 py-0.5 text-[11px] font-medium text-[#0a3a5a] dark:bg-gray-700 dark:text-gray-300"
            title={`Curated source: ${c}`}
          >
            {c}
          </span>
        ))}
      </div>

      {/* Footer row — license, score, stars */}
      <div className="mt-auto flex flex-wrap items-center gap-2 pt-3">
        <span
          className="inline-flex items-center rounded-full bg-white px-2 py-0.5 text-[11px] font-medium text-[#2a5a7a] ring-1 ring-[#c0ddf0] dark:bg-gray-900 dark:text-gray-400 dark:ring-gray-700"
          title={`License: ${entry.license}`}
        >
          {entry.license}
        </span>
        <ScorecardBadge
          score={entry.security_score}
          tier={entry.security_tier}
          updated={entry.security_score_updated}
          // v1.21 QA Bundle 4 Fix #3d: tiles skip the "Unknown" chip so
          // users don't see every card labelled with placeholder text
          // before the daily OpenSSF refresh populates real scores.
          hideWhenUnknown
        />
        {stars && (
          <span
            className="ml-auto inline-flex items-center gap-1 text-[11px] font-medium text-[#2a5a7a] dark:text-gray-400"
            title={`${entry.github_stars?.toLocaleString()} GitHub stars`}
          >
            <Github className="h-3.5 w-3.5" aria-hidden="true" />
            <Star className="h-3 w-3 fill-current text-amber-500" aria-hidden="true" />
            {stars}
          </span>
        )}
      </div>

      {/* Footer hint — flips between "Open" (addable; opens in-page detail
          view) and "View in your catalog" (already installed). Always
          visible when in-catalog so the affordance is obvious without
          needing hover. */}
      {inCatalog ? (
        <span className="mt-2 inline-flex items-center gap-1 text-[11px] font-semibold text-green-700 dark:text-green-400">
          View in your catalog <ExternalLink className="h-3 w-3" aria-hidden="true" />
        </span>
      ) : (
        <span className="mt-2 inline-flex items-center gap-1 text-[11px] font-medium text-teal-700 opacity-0 transition-opacity group-hover:opacity-100 group-focus:opacity-100 dark:text-teal-400">
          View details <ExternalLink className="h-3 w-3" aria-hidden="true" />
        </span>
      )}
    </button>
  )
}

export default MarketplaceCard

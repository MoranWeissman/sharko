import { Github, Star, Package, Tag, ExternalLink } from 'lucide-react'
import { cn } from '@/lib/utils'
import type { CatalogEntry } from '@/services/models'
import { ScorecardBadge } from '@/components/ScorecardBadge'

/**
 * MarketplaceCard — single tile rendered in the Marketplace Browse grid.
 *
 * The whole card is one keyboard-focusable button so screen readers announce
 * the addon name + key facts in one go. We intentionally avoid nested
 * interactive children to stay keyboard-friendly; the Configure modal is
 * opened by the parent via onOpen().
 *
 * The "icon" is intentionally generic — chart-specific logos are out of
 * scope until V121-3 (ArtifactHub proxy) gives us a reliable source.
 */

export interface MarketplaceCardProps {
  entry: CatalogEntry
  onOpen: (entry: CatalogEntry) => void
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

export function MarketplaceCard({ entry, onOpen }: MarketplaceCardProps) {
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
  ]
    .filter(Boolean)
    .join('. ')

  return (
    <button
      type="button"
      onClick={() => onOpen(entry)}
      aria-label={`Configure ${entry.name}: ${ariaSummary}`}
      className={cn(
        'group flex h-full w-full flex-col items-stretch rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff]',
        'p-4 text-left shadow-sm transition-all duration-150',
        'hover:-translate-y-0.5 hover:shadow-md hover:ring-teal-400',
        'focus:outline-none focus-visible:ring-4 focus-visible:ring-teal-400',
        'dark:bg-gray-800 dark:ring-gray-700 dark:hover:ring-teal-500',
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
        {entry.deprecated && (
          <span
            className="shrink-0 rounded-full bg-amber-100 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-amber-800 dark:bg-amber-900/40 dark:text-amber-300"
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

      {/* "Open" hint — visually subtle, important for screen readers */}
      <span className="mt-2 inline-flex items-center gap-1 text-[11px] font-medium text-teal-700 opacity-0 transition-opacity group-hover:opacity-100 group-focus:opacity-100 dark:text-teal-400">
        Configure <ExternalLink className="h-3 w-3" aria-hidden="true" />
      </span>
    </button>
  )
}

export default MarketplaceCard

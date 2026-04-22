import type { CatalogSourceRecord } from '@/services/models'

/**
 * SourceBadge — V123-1.7. A small pill that tells an operator whether a
 * marketplace entry came from the Sharko-shipped embedded catalog or from a
 * third-party catalog URL configured via `SHARKO_CATALOG_URLS`.
 *
 * - Embedded entries render a neutral blue "Internal" badge.
 * - Third-party entries render a deeper-blue badge containing the URL host
 *   (e.g. `catalogs.example.com`). When the optional `sourceRecord` prop is
 *   supplied, the tooltip also shows the last-fetched timestamp + status
 *   returned by `GET /api/v1/catalog/sources`.
 *
 * The full URL is NEVER rendered as a clickable link — URL paths may carry
 * auth tokens, and we don't want them leaking via Referer headers or
 * browser-history sync. Tooltip display is fine (the viewer is already
 * authenticated).
 *
 * Palette follows the Sharko blue family (`#d6eeff` / `#b4dcf5` / `#0a3a5a`
 * / `#2a5a7a` / `#c0ddf0`). No generic `gray-*` utilities — see
 * `.claude/team/frontend-expert.md`.
 */

type SourceBadgeProps = {
  /** The `source` field on a `CatalogEntry`: `"embedded"`, a third-party URL, or undefined. */
  source: string | undefined
  /** Matching record from `GET /api/v1/catalog/sources`. When absent the tooltip
   *  falls back to just the raw URL. */
  sourceRecord?: CatalogSourceRecord
  /** Tile size — true on Marketplace cards, false/omitted on the detail page. */
  compact?: boolean
}

function formatHost(source: string): string | null {
  try {
    return new URL(source).host || null
  } catch {
    return null
  }
}

export function SourceBadge({ source, sourceRecord, compact }: SourceBadgeProps) {
  const isEmbedded = !source || source === 'embedded'

  if (isEmbedded) {
    return (
      <span
        className={`inline-flex items-center rounded-full bg-[#d6eeff] font-medium text-[#0a3a5a] ring-1 ring-[#c0ddf0] dark:bg-[#1a3a5a] dark:text-[#d6eeff] dark:ring-[#2a5a7a] ${
          compact ? 'px-2 py-0.5 text-[11px]' : 'px-2.5 py-1 text-xs'
        }`}
        title="Source: Sharko embedded catalog"
        aria-label="Source: Sharko embedded catalog (curated)"
      >
        Internal
      </span>
    )
  }

  const host = source ? formatHost(source) : null
  const label = host ?? 'Third-party'
  const lastFetchedLine = sourceRecord?.last_fetched
    ? `Last fetched: ${sourceRecord.last_fetched}`
    : null
  const statusLine = sourceRecord?.status ? `Status: ${sourceRecord.status}` : null
  const tooltip = [`Source: ${source}`, lastFetchedLine, statusLine]
    .filter(Boolean)
    .join('\n')

  return (
    <span
      className={`inline-flex items-center rounded-full bg-[#b4dcf5] font-medium text-[#0a3a5a] ring-1 ring-[#2a5a7a] dark:bg-[#2a5a7a] dark:text-[#d6eeff] dark:ring-[#b4dcf5] ${
        compact ? 'px-2 py-0.5 text-[11px]' : 'px-2.5 py-1 text-xs'
      }`}
      title={tooltip}
      aria-label={`Source: ${label} (third-party catalog)`}
    >
      {label}
    </span>
  )
}

export default SourceBadge

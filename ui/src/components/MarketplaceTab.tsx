import { useCallback, useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Sparkles } from 'lucide-react'
import { api } from '@/services/api'
import type {
  CatalogCategory,
  CatalogCuratedBy,
  CatalogEntry,
} from '@/services/models'
import { LoadingState } from '@/components/LoadingState'
import { ErrorState } from '@/components/ErrorState'
import { MarketplaceCard } from '@/components/MarketplaceCard'
import {
  MarketplaceFilters,
  type MarketplaceFiltersValue,
  type ScoreTierFilter,
} from '@/components/MarketplaceFilters'
import { MarketplaceConfigureModal } from '@/components/MarketplaceConfigureModal'

/**
 * MarketplaceTab — top-level view rendered inside the AddonCatalog page when
 * the user picks the "Marketplace" tab.
 *
 * URL state contract (so deep links survive a refresh):
 *   ?mp_q=<text>            search box
 *   ?mp_cat=a,b             category multi-select
 *   ?mp_curated=a,b         curated_by multi-select
 *   ?mp_lic=a,b             license multi-select
 *   ?mp_tier=any|strong|moderate|weak|unknown   OpenSSF tier
 *
 * We fetch the full curated catalog once and filter client-side. The list is
 * tiny (curated by Sharko), so this keeps the UI snappy and lets us mix
 * AND/OR semantics (multi-category OR within category, AND across category
 * vs. license vs. curator) without round-tripping the server for every
 * checkbox flip.
 */

const VALID_TIERS: ScoreTierFilter[] = [
  'any',
  'strong',
  'moderate',
  'weak',
  'unknown',
]

const TIER_TO_MIN_SCORE: Record<ScoreTierFilter, number> = {
  any: 0,
  strong: 8,
  moderate: 5,
  weak: 0,
  unknown: 0,
}

function parseList(raw: string | null): string[] {
  if (!raw) return []
  return raw
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
}

function parseFilters(params: URLSearchParams): MarketplaceFiltersValue {
  const tier = (params.get('mp_tier') ?? 'any') as ScoreTierFilter
  return {
    q: params.get('mp_q') ?? '',
    categories: parseList(params.get('mp_cat')) as CatalogCategory[],
    curatedBy: parseList(params.get('mp_curated')) as CatalogCuratedBy[],
    licenses: parseList(params.get('mp_lic')),
    scoreTier: VALID_TIERS.includes(tier) ? tier : 'any',
  }
}

function writeFilters(
  current: URLSearchParams,
  next: MarketplaceFiltersValue,
): URLSearchParams {
  // Mutate a clone so we don't trample unrelated query params (e.g. ?tab=).
  const out = new URLSearchParams(current.toString())
  if (next.q) out.set('mp_q', next.q)
  else out.delete('mp_q')
  if (next.categories.length > 0) out.set('mp_cat', next.categories.join(','))
  else out.delete('mp_cat')
  if (next.curatedBy.length > 0) out.set('mp_curated', next.curatedBy.join(','))
  else out.delete('mp_curated')
  if (next.licenses.length > 0) out.set('mp_lic', next.licenses.join(','))
  else out.delete('mp_lic')
  if (next.scoreTier !== 'any') out.set('mp_tier', next.scoreTier)
  else out.delete('mp_tier')
  return out
}

function matchesScoreTier(
  entry: CatalogEntry,
  tier: ScoreTierFilter,
): boolean {
  if (tier === 'any') return true
  const score = entry.security_score
  const isUnknown = score === 'unknown' || score === undefined
  if (tier === 'unknown') return isUnknown
  if (isUnknown) return false
  const numeric = typeof score === 'number' ? score : Number(score)
  if (Number.isNaN(numeric)) return false
  switch (tier) {
    case 'strong':
      return numeric >= TIER_TO_MIN_SCORE.strong
    case 'moderate':
      return numeric >= TIER_TO_MIN_SCORE.moderate && numeric < TIER_TO_MIN_SCORE.strong
    case 'weak':
      return numeric < TIER_TO_MIN_SCORE.moderate
    default:
      return true
  }
}

export function MarketplaceTab() {
  const [searchParams, setSearchParams] = useSearchParams()

  const [entries, setEntries] = useState<CatalogEntry[] | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const [selectedEntry, setSelectedEntry] = useState<CatalogEntry | null>(null)
  const [modalOpen, setModalOpen] = useState(false)

  const filters = useMemo(() => parseFilters(searchParams), [searchParams])
  const setFilters = useCallback(
    (next: MarketplaceFiltersValue) => {
      setSearchParams(writeFilters(searchParams, next), { replace: true })
    },
    [searchParams, setSearchParams],
  )

  // One-shot fetch — server-side filters offer no win at this scale and would
  // disable the AND-across-categories UX we want.
  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    api
      .listCuratedCatalog()
      .then((resp) => {
        if (!cancelled) setEntries(resp.addons)
      })
      .catch((e: unknown) => {
        if (!cancelled) {
          setError(e instanceof Error ? e.message : 'Failed to load catalog')
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [])

  const allEntries = entries ?? []
  const availableLicenses = useMemo(() => {
    const set = new Set<string>()
    for (const e of allEntries) set.add(e.license)
    return Array.from(set).sort((a, b) => a.localeCompare(b))
  }, [allEntries])

  const filtered = useMemo(() => {
    if (!entries) return []
    const q = filters.q.trim().toLowerCase()
    return entries.filter((e) => {
      // Hide deprecated unless the user searched for one explicitly.
      if (e.deprecated && !q) return false

      // Categories — OR within (any selected category matches).
      if (filters.categories.length > 0 && !filters.categories.includes(e.category)) {
        return false
      }
      // Curated_by — AND (must include all selected).
      if (filters.curatedBy.length > 0) {
        for (const c of filters.curatedBy) {
          if (!e.curated_by.includes(c)) return false
        }
      }
      // License — OR within.
      if (filters.licenses.length > 0 && !filters.licenses.includes(e.license)) {
        return false
      }
      // Score tier.
      if (!matchesScoreTier(e, filters.scoreTier)) return false
      // Free-text search.
      if (q) {
        const hay = [
          e.name,
          e.description,
          ...(e.maintainers ?? []),
          e.chart,
        ]
          .join(' ')
          .toLowerCase()
        if (!hay.includes(q)) return false
      }
      return true
    })
  }, [entries, filters])

  const handleOpenConfigure = useCallback((entry: CatalogEntry) => {
    setSelectedEntry(entry)
    setModalOpen(true)
  }, [])

  if (loading) {
    return <LoadingState message="Loading curated marketplace…" />
  }
  if (error) {
    return <ErrorState message={error} />
  }
  if (!entries || entries.length === 0) {
    return (
      <div className="rounded-lg border border-teal-200 bg-teal-50 p-6 text-center text-sm text-teal-700 dark:border-teal-700 dark:bg-teal-900/30 dark:text-teal-400">
        The curated catalog is empty.
      </div>
    )
  }

  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-[18rem_1fr]">
      <MarketplaceFilters
        value={filters}
        onChange={setFilters}
        availableLicenses={availableLicenses}
        catalogEntries={allEntries}
      />

      <section aria-label="Marketplace results" className="flex min-w-0 flex-col gap-3">
        <header className="flex items-center justify-between">
          <p className="text-sm text-[#2a5a7a] dark:text-gray-400">
            <Sparkles className="mr-1 inline h-4 w-4 text-teal-600" aria-hidden="true" />
            Showing <strong className="text-[#0a2a4a] dark:text-gray-100">{filtered.length}</strong>{' '}
            of {entries.length} curated addons
          </p>
        </header>

        {filtered.length === 0 ? (
          <div
            role="status"
            className="rounded-lg border border-teal-200 bg-teal-50 p-6 text-center text-sm text-teal-700 dark:border-teal-700 dark:bg-teal-900/30 dark:text-teal-400"
          >
            No addons match the current filters. Try clearing one or two filters
            from the sidebar.
          </div>
        ) : (
          <ul
            role="list"
            className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3"
          >
            {filtered.map((entry) => (
              <li key={entry.name} className="flex">
                <MarketplaceCard entry={entry} onOpen={handleOpenConfigure} />
              </li>
            ))}
          </ul>
        )}
      </section>

      <MarketplaceConfigureModal
        entry={selectedEntry}
        open={modalOpen}
        onOpenChange={(v) => {
          setModalOpen(v)
          if (!v) setSelectedEntry(null)
        }}
      />
    </div>
  )
}

export default MarketplaceTab

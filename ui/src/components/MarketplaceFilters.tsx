import { useEffect, useMemo, useRef, useState } from 'react'
import { Search, X } from 'lucide-react'
import { cn } from '@/lib/utils'
import type {
  CatalogCategory,
  CatalogCuratedBy,
  CatalogEntry,
} from '@/services/models'

/**
 * MarketplaceFilters — left-rail filter panel for the Marketplace Browse tab.
 *
 * State is owned by the parent (MarketplaceTab) so the URL can stay in sync;
 * this component is purely presentational. The search input is debounced
 * locally to avoid hammering the parent's setState on every keystroke.
 *
 * Tier mapping (matches backend `min_score` semantics):
 *   - "strong"   → min_score = 8
 *   - "moderate" → min_score = 5
 *   - "weak"     → min_score = 0  (everything ≥ 0 with a known score)
 *   - "unknown"  → don't filter, but include unknown entries
 *   - "any"      → no filter at all (default)
 */

export type ScoreTierFilter = 'any' | 'strong' | 'moderate' | 'weak' | 'unknown'

const ALL_CATEGORIES: CatalogCategory[] = [
  'security',
  'observability',
  'networking',
  'autoscaling',
  'gitops',
  'storage',
  'database',
  'backup',
  'chaos',
  'developer-tools',
]

const ALL_CURATED_BY: CatalogCuratedBy[] = [
  'cncf-graduated',
  'cncf-incubating',
  'cncf-sandbox',
  'aws-eks-blueprints',
  'azure-aks-addon',
  'gke-marketplace',
  'artifacthub-verified',
  'artifacthub-official',
]

const SCORE_TIERS: { value: ScoreTierFilter; label: string }[] = [
  { value: 'any', label: 'Any' },
  { value: 'strong', label: 'Strong (≥ 8.0)' },
  { value: 'moderate', label: 'Moderate (≥ 5.0)' },
  { value: 'weak', label: 'Weak (< 5.0)' },
  { value: 'unknown', label: 'Unknown only' },
]

export interface MarketplaceFiltersValue {
  q: string
  categories: CatalogCategory[]
  curatedBy: CatalogCuratedBy[]
  licenses: string[]
  scoreTier: ScoreTierFilter
  /**
   * V123-2.4: when true, narrow the visible entries to those with
   * `verified === true` (cosign-keyless signature accepted by the
   * configured trust policy). Pseudo-filter — not persisted on the
   * server, applied client-side in MarketplaceBrowseTab.
   */
  signedOnly: boolean
}

export interface MarketplaceFiltersProps {
  value: MarketplaceFiltersValue
  onChange: (next: MarketplaceFiltersValue) => void
  /** Distinct license values from the loaded catalog — used to populate the license multi-select. */
  availableLicenses: string[]
  /** Distinct categories actually present in the loaded catalog — for badge counts. */
  catalogEntries: CatalogEntry[]
}

function toggle<T>(arr: T[], v: T): T[] {
  return arr.includes(v) ? arr.filter((x) => x !== v) : [...arr, v]
}

export function MarketplaceFilters({
  value,
  onChange,
  availableLicenses,
  catalogEntries,
}: MarketplaceFiltersProps) {
  const [searchDraft, setSearchDraft] = useState(value.q)
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // Keep local search input in sync when the parent resets (e.g. "Clear filters").
  useEffect(() => {
    setSearchDraft(value.q)
  }, [value.q])

  useEffect(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current)
    if (searchDraft === value.q) return
    debounceRef.current = setTimeout(() => {
      onChange({ ...value, q: searchDraft })
    }, 200)
    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchDraft])

  const counts = useMemo(() => {
    const cat = new Map<string, number>()
    const cur = new Map<string, number>()
    const lic = new Map<string, number>()
    for (const e of catalogEntries) {
      cat.set(e.category, (cat.get(e.category) ?? 0) + 1)
      for (const c of e.curated_by) cur.set(c, (cur.get(c) ?? 0) + 1)
      lic.set(e.license, (lic.get(e.license) ?? 0) + 1)
    }
    return { cat, cur, lic }
  }, [catalogEntries])

  const isDirty =
    value.q !== '' ||
    value.categories.length > 0 ||
    value.curatedBy.length > 0 ||
    value.licenses.length > 0 ||
    value.scoreTier !== 'any' ||
    value.signedOnly

  const clear = () =>
    onChange({
      q: '',
      categories: [],
      curatedBy: [],
      licenses: [],
      scoreTier: 'any',
      signedOnly: false,
    })

  // V123-2.4: count of cosign-verified entries — surfaced next to the
  // "Signed only" checkbox to mirror the per-group count pattern used
  // by the other filter groups.
  const signedCount = useMemo(
    () => catalogEntries.filter((e) => e.verified === true).length,
    [catalogEntries],
  )

  return (
    <aside
      aria-label="Marketplace filters"
      className="flex flex-col gap-5 rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:bg-gray-800 dark:ring-gray-700"
    >
      {/* Search */}
      <div>
        <label
          htmlFor="marketplace-search"
          className="mb-1 block text-xs font-semibold uppercase tracking-wide text-[#0a3a5a] dark:text-gray-300"
        >
          Search
        </label>
        <div className="relative">
          <Search
            className="pointer-events-none absolute left-2 top-1/2 h-4 w-4 -translate-y-1/2 text-[#3a6a8a]"
            aria-hidden="true"
          />
          <input
            id="marketplace-search"
            type="search"
            value={searchDraft}
            onChange={(e) => setSearchDraft(e.target.value)}
            placeholder="Name, description, maintainer…"
            className="w-full rounded-md border border-[#5a9dd0] bg-white py-1.5 pl-8 pr-2 text-sm placeholder:text-[#5a8aaa] focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-900 dark:text-gray-100"
          />
        </div>
      </div>

      {/* Category */}
      <FilterGroup label="Category">
        {ALL_CATEGORIES.map((c) => {
          const count = counts.cat.get(c) ?? 0
          if (count === 0) return null
          const checked = value.categories.includes(c)
          return (
            <FilterCheckbox
              key={c}
              id={`f-cat-${c}`}
              label={c}
              count={count}
              checked={checked}
              onChange={() =>
                onChange({ ...value, categories: toggle(value.categories, c) })
              }
            />
          )
        })}
      </FilterGroup>

      {/* Curated by */}
      <FilterGroup label="Curated by">
        {ALL_CURATED_BY.map((c) => {
          const count = counts.cur.get(c) ?? 0
          if (count === 0) return null
          const checked = value.curatedBy.includes(c)
          return (
            <FilterCheckbox
              key={c}
              id={`f-cur-${c}`}
              label={c}
              count={count}
              checked={checked}
              onChange={() =>
                onChange({ ...value, curatedBy: toggle(value.curatedBy, c) })
              }
            />
          )
        })}
      </FilterGroup>

      {/* V123-2.4 — Signature: pseudo-filter for cosign-verified entries.
          Single checkbox; count shows how many entries currently verify. */}
      <FilterGroup label="Signature">
        <FilterCheckbox
          id="f-signed-only"
          label="Signed only"
          count={signedCount}
          checked={value.signedOnly}
          onChange={() =>
            onChange({ ...value, signedOnly: !value.signedOnly })
          }
        />
      </FilterGroup>

      {/* License */}
      <FilterGroup label="License">
        {availableLicenses.map((l) => {
          const count = counts.lic.get(l) ?? 0
          if (count === 0) return null
          const checked = value.licenses.includes(l)
          return (
            <FilterCheckbox
              key={l}
              id={`f-lic-${l}`}
              label={l}
              count={count}
              checked={checked}
              onChange={() =>
                onChange({ ...value, licenses: toggle(value.licenses, l) })
              }
            />
          )
        })}
      </FilterGroup>

      {/* OpenSSF tier */}
      <fieldset>
        <legend className="mb-2 text-xs font-semibold uppercase tracking-wide text-[#0a3a5a] dark:text-gray-300">
          OpenSSF Scorecard
        </legend>
        <div className="space-y-1">
          {SCORE_TIERS.map((t) => (
            <label
              key={t.value}
              htmlFor={`f-tier-${t.value}`}
              className="flex cursor-pointer items-center gap-2 text-sm text-[#0a3a5a] dark:text-gray-200"
            >
              <input
                id={`f-tier-${t.value}`}
                type="radio"
                name="marketplace-score-tier"
                value={t.value}
                checked={value.scoreTier === t.value}
                onChange={() => onChange({ ...value, scoreTier: t.value })}
                className="h-4 w-4 cursor-pointer accent-teal-600"
              />
              {t.label}
            </label>
          ))}
        </div>
      </fieldset>

      {/* Clear */}
      <button
        type="button"
        disabled={!isDirty}
        onClick={clear}
        className={cn(
          'inline-flex items-center justify-center gap-1 rounded-md border border-[#5a9dd0] bg-white px-3 py-1.5 text-sm font-medium text-[#0a3a5a]',
          'hover:bg-[#d6eeff] focus:outline-none focus-visible:ring-2 focus-visible:ring-[#6aade0]',
          'disabled:cursor-not-allowed disabled:opacity-50',
          'dark:border-gray-600 dark:bg-gray-900 dark:text-gray-200 dark:hover:bg-gray-700',
        )}
      >
        <X className="h-3.5 w-3.5" aria-hidden="true" />
        Clear filters
      </button>
    </aside>
  )
}

function FilterGroup({
  label,
  children,
}: {
  label: string
  children: React.ReactNode
}) {
  return (
    <fieldset>
      <legend className="mb-2 text-xs font-semibold uppercase tracking-wide text-[#0a3a5a] dark:text-gray-300">
        {label}
      </legend>
      <div className="max-h-48 space-y-1 overflow-y-auto pr-1">{children}</div>
    </fieldset>
  )
}

function FilterCheckbox({
  id,
  label,
  count,
  checked,
  onChange,
}: {
  id: string
  label: string
  count: number
  checked: boolean
  onChange: () => void
}) {
  return (
    <label
      htmlFor={id}
      className="flex cursor-pointer items-center justify-between gap-2 text-sm text-[#0a3a5a] dark:text-gray-200"
    >
      <span className="flex items-center gap-2">
        <input
          id={id}
          type="checkbox"
          checked={checked}
          onChange={onChange}
          aria-label={label}
          className="h-4 w-4 cursor-pointer accent-teal-600"
        />
        <span aria-hidden="true" className="capitalize">
          {label}
        </span>
      </span>
      <span aria-hidden="true" className="text-xs text-[#3a6a8a] dark:text-gray-500">
        {count}
      </span>
    </label>
  )
}

export default MarketplaceFilters

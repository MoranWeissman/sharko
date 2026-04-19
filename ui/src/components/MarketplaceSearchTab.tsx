import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  AlertTriangle,
  CheckCircle2,
  Github,
  Package,
  Search,
  Sparkles,
  Star,
} from 'lucide-react'
import { api } from '@/services/api'
import type {
  ArtifactHubSearchResult,
  CatalogEntry,
  CatalogSearchResponse,
} from '@/services/models'
import { showToast } from '@/components/ToastNotification'
import { MarketplaceCard } from '@/components/MarketplaceCard'
import { MarketplaceConfigureModal } from '@/components/MarketplaceConfigureModal'

/**
 * MarketplaceSearchTab — the discovery tab in the Marketplace.
 *
 * Why a separate tab from Browse:
 *   • Browse = filterable curated grid (offline; no network on filter flip)
 *   • Search = name search across curated AND ArtifactHub (250 ms debounce,
 *     server-side cached)
 *
 * Layout is two stacked sections so the curated label is unambiguous:
 *   1. Curated results — full MarketplaceCard with Configure button
 *   2. ArtifactHub results — slim card with verified-publisher + star count
 *
 * Failure modes the UI handles:
 *   • Empty query → friendly "Search any chart" pitch
 *   • Loading → skeleton rows
 *   • ArtifactHub unreachable → banner above the AH section + Retry button
 *     that POSTs /catalog/reprobe
 *   • No results in either bucket → status row inside that section
 *
 * Accessibility (WCAG 2.1 AA, design §4.8):
 *   • Search input has aria-label and is auto-focused on tab open
 *   • Result count update fires aria-live="polite" announcement
 *   • Each result tile is a single keyboard-focusable button
 *   • Banners use role="alert" / role="status" appropriately
 */

const DEBOUNCE_MS = 250
const SEARCH_LIMIT = 20

export function MarketplaceSearchTab() {
  const [rawQuery, setRawQuery] = useState('')
  const [debounced, setDebounced] = useState('')
  const [resp, setResp] = useState<CatalogSearchResponse | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [retrying, setRetrying] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  // External pkg → opens Configure modal pre-filled. We synthesise a
  // CatalogEntry-shaped object for the existing modal to consume.
  const [configureEntry, setConfigureEntry] = useState<CatalogEntry | null>(null)
  const [modalOpen, setModalOpen] = useState(false)
  // Track which result bucket opened the modal so the addon_added audit event
  // records the right `source` (curated → "marketplace", external → "artifacthub").
  const [configureSource, setConfigureSource] =
    useState<'marketplace' | 'artifacthub'>('marketplace')

  // Auto-focus the search box when the tab mounts.
  useEffect(() => {
    inputRef.current?.focus()
  }, [])

  // Debounce keystrokes — 250 ms is the design-doc value (§4.8 / story 3.6).
  useEffect(() => {
    const t = setTimeout(() => setDebounced(rawQuery.trim()), DEBOUNCE_MS)
    return () => clearTimeout(t)
  }, [rawQuery])

  // Issue search when debounced query changes.
  useEffect(() => {
    if (debounced === '') {
      setResp(null)
      setError(null)
      setLoading(false)
      return
    }
    let cancelled = false
    setLoading(true)
    setError(null)
    api
      .searchCatalog(debounced, SEARCH_LIMIT)
      .then((r) => {
        if (!cancelled) setResp(r)
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : 'Search failed')
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [debounced])

  // Retry button — clears backoff + caches, re-issues the same query.
  const handleRetry = useCallback(async () => {
    setRetrying(true)
    try {
      const probe = await api.reprobeArtifactHub()
      if (probe.reachable) {
        showToast('ArtifactHub is reachable again', 'success')
        // Re-issue the search by bumping debounced; if same value, force re-run
        // by clearing then setting on the next tick.
        const q = debounced
        setDebounced('')
        setTimeout(() => setDebounced(q), 0)
      } else {
        showToast(`ArtifactHub still unreachable${probe.last_error ? ` (${probe.last_error})` : ''}`, 'info')
      }
    } catch (e) {
      showToast(`Retry failed — ${e instanceof Error ? e.message : 'unknown'}`, 'info')
    } finally {
      setRetrying(false)
    }
  }, [debounced])

  // Open Configure for a curated entry (full schema already available).
  const handleOpenCurated = useCallback((entry: CatalogEntry) => {
    setConfigureEntry(entry)
    setConfigureSource('marketplace')
    setModalOpen(true)
  }, [])

  // Open Configure for an external pkg — fetch detail to enrich the entry,
  // then open the modal. We synthesise a minimal CatalogEntry the modal can
  // consume; missing fields (license, category, security_score) are best-effort.
  const handleOpenExternal = useCallback(async (pkg: ArtifactHubSearchResult) => {
    const synthesised: CatalogEntry = {
      name: pkg.normalized_name || pkg.name,
      description: pkg.description ?? '',
      chart: pkg.name,
      repo: pkg.repository.url || '',
      default_namespace: pkg.normalized_name || pkg.name,
      default_sync_wave: 0,
      maintainers: [],
      license: '',
      // Cast — backend never sends arbitrary strings in this field. For
      // external entries we use 'developer-tools' as the generic bucket so
      // the modal renders without choking on the literal-union type. This is
      // cosmetic only; the catalog write doesn't persist this value.
      category: 'developer-tools',
      curated_by: [],
      github_stars: pkg.stars,
      homepage: pkg.repository.url,
    }
    setConfigureEntry(synthesised)
    setConfigureSource('artifacthub')
    setModalOpen(true)
    // Best-effort enrichment via /catalog/remote — license + maintainers fill
    // the modal nicely. Failure is non-fatal.
    try {
      const detail = await api.getRemoteCatalogPackage(
        pkg.repository.name,
        pkg.name,
      )
      if (detail.package) {
        setConfigureEntry((prev) =>
          prev
            ? {
                ...prev,
                description: detail.package?.description ?? prev.description,
                license: detail.package?.license ?? prev.license,
                maintainers:
                  detail.package?.maintainers
                    ?.map((m) => m.name)
                    .filter((n): n is string => !!n) ?? prev.maintainers,
                homepage: detail.package?.home_url ?? prev.homepage,
              }
            : prev,
        )
      }
    } catch {
      // ignored — the modal still works with partial data
    }
  }, [])

  const totalCount = useMemo(() => {
    if (!resp) return 0
    return resp.curated.length + resp.artifacthub.length
  }, [resp])

  return (
    <div className="flex flex-col gap-4">
      {/* Search input */}
      <div className="relative">
        <Search
          aria-hidden="true"
          className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[#3a6a8a]"
        />
        <input
          ref={inputRef}
          type="search"
          value={rawQuery}
          onChange={(e) => setRawQuery(e.target.value)}
          aria-label="Search addons by name"
          placeholder="Search any Helm chart on ArtifactHub or our curated catalog…"
          className="w-full rounded-md border border-[#5a9dd0] bg-white py-2.5 pl-9 pr-3 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-900 dark:text-gray-100"
        />
      </div>

      {/* Live region for screen readers — fires when result count changes. */}
      <div className="sr-only" role="status" aria-live="polite">
        {loading
          ? 'Searching…'
          : resp
            ? `${totalCount} results for ${debounced}`
            : ''}
      </div>

      {/* Empty state — no query yet */}
      {!debounced && !loading && (
        <div
          role="status"
          className="rounded-lg border border-teal-200 bg-teal-50 p-8 text-center dark:border-teal-700 dark:bg-teal-900/30"
        >
          <Sparkles className="mx-auto h-8 w-8 text-teal-600" aria-hidden="true" />
          <h3 className="mt-2 text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
            Search any chart on ArtifactHub
          </h3>
          <p className="mt-1 text-xs text-[#2a5a7a] dark:text-gray-400">
            Try{' '}
            <button
              type="button"
              onClick={() => setRawQuery('prometheus')}
              className="font-medium text-teal-700 underline focus:outline-none focus-visible:ring-2 focus-visible:ring-teal-500 dark:text-teal-400"
            >
              prometheus
            </button>
            {', '}
            <button
              type="button"
              onClick={() => setRawQuery('cert-manager')}
              className="font-medium text-teal-700 underline focus:outline-none focus-visible:ring-2 focus-visible:ring-teal-500 dark:text-teal-400"
            >
              cert-manager
            </button>
            {', or '}
            <button
              type="button"
              onClick={() => setRawQuery('argo-cd')}
              className="font-medium text-teal-700 underline focus:outline-none focus-visible:ring-2 focus-visible:ring-teal-500 dark:text-teal-400"
            >
              argo-cd
            </button>
          </p>
        </div>
      )}

      {/* Loading skeleton */}
      {loading && debounced && (
        <div className="space-y-2" aria-hidden="true">
          {[0, 1, 2].map((i) => (
            <div
              key={i}
              className="h-16 animate-pulse rounded-lg bg-[#e0eef9] dark:bg-gray-800"
            />
          ))}
        </div>
      )}

      {/* Generic error — server didn't even return a body */}
      {error && !loading && (
        <div
          role="alert"
          className="flex items-start gap-2 rounded-md border border-red-300 bg-red-50 p-3 text-sm text-red-900 dark:border-red-700 dark:bg-red-950/40 dark:text-red-200"
        >
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
          <p>Search failed: {error}</p>
        </div>
      )}

      {/* Results */}
      {resp && !loading && (
        <div className="flex flex-col gap-6">
          {/* ─── Curated section ─── */}
          <section aria-label="Curated results">
            <header className="mb-2 flex items-center gap-2 text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
              <CheckCircle2
                className="h-4 w-4 text-teal-600 dark:text-teal-400"
                aria-hidden="true"
              />
              Curated by Sharko
              <span className="text-xs font-normal text-[#3a6a8a] dark:text-gray-500">
                ({resp.curated.length})
              </span>
            </header>
            {resp.curated.length === 0 ? (
              <p className="rounded-md border border-dashed border-[#c0ddf0] bg-white p-4 text-center text-xs text-[#3a6a8a] dark:border-gray-700 dark:bg-gray-900 dark:text-gray-500">
                No curated matches for &ldquo;{debounced}&rdquo;.
              </p>
            ) : (
              <ul
                role="list"
                className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3"
              >
                {resp.curated.map((entry) => (
                  <li key={entry.name} className="flex">
                    <MarketplaceCard entry={entry} onOpen={handleOpenCurated} />
                  </li>
                ))}
              </ul>
            )}
          </section>

          {/* ─── ArtifactHub section ─── */}
          <section aria-label="ArtifactHub results">
            <header className="mb-2 flex items-center gap-2 text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
              <Package
                className="h-4 w-4 text-[#3a6a8a]"
                aria-hidden="true"
              />
              From ArtifactHub
              <span className="text-xs font-normal text-[#3a6a8a] dark:text-gray-500">
                ({resp.artifacthub.length}
                {resp.stale ? ', cached' : ''})
              </span>
            </header>

            {resp.artifacthub_error && (
              <div
                role="alert"
                className="mb-2 flex items-start gap-2 rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-700 dark:bg-amber-950/40 dark:text-amber-200"
              >
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
                <div className="flex-1">
                  <p>
                    ArtifactHub unreachable
                    {resp.stale ? ' — showing cached results.' : ' — showing curated only.'}
                  </p>
                  <button
                    type="button"
                    onClick={handleRetry}
                    disabled={retrying}
                    className="mt-1 text-xs font-medium underline hover:no-underline disabled:opacity-50"
                  >
                    {retrying ? 'Retrying…' : 'Retry connectivity'}
                  </button>
                </div>
              </div>
            )}

            {resp.artifacthub.length === 0 && !resp.artifacthub_error ? (
              <p className="rounded-md border border-dashed border-[#c0ddf0] bg-white p-4 text-center text-xs text-[#3a6a8a] dark:border-gray-700 dark:bg-gray-900 dark:text-gray-500">
                No ArtifactHub matches for &ldquo;{debounced}&rdquo;.
              </p>
            ) : (
              <ul
                role="list"
                className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3"
              >
                {resp.artifacthub.map((pkg) => (
                  <li key={pkg.package_id} className="flex">
                    <ArtifactHubResultCard pkg={pkg} onOpen={handleOpenExternal} />
                  </li>
                ))}
              </ul>
            )}
          </section>
        </div>
      )}

      <MarketplaceConfigureModal
        entry={configureEntry}
        open={modalOpen}
        onOpenChange={(v) => {
          setModalOpen(v)
          if (!v) setConfigureEntry(null)
        }}
        source={configureSource}
      />
    </div>
  )
}

// ─── ArtifactHub result card ─────────────────────────────────────────────────

interface ArtifactHubResultCardProps {
  pkg: ArtifactHubSearchResult
  onOpen: (pkg: ArtifactHubSearchResult) => void
}

function ArtifactHubResultCard({ pkg, onOpen }: ArtifactHubResultCardProps) {
  const repoLabel =
    pkg.repository.display_name || pkg.repository.name || 'unknown repository'
  const orgLabel =
    pkg.repository.organization_name || pkg.repository.user_alias || ''
  const stars = formatStars(pkg.stars)

  const ariaSummary = [
    pkg.display_name || pkg.name,
    pkg.description ?? '',
    `from ${repoLabel}`,
    pkg.repository.verified_publisher ? 'verified publisher' : '',
    pkg.repository.official ? 'official' : '',
  ]
    .filter(Boolean)
    .join('. ')

  return (
    <button
      type="button"
      onClick={() => onOpen(pkg)}
      aria-label={`Configure ${pkg.name}: ${ariaSummary}`}
      className="group flex h-full w-full flex-col items-stretch rounded-lg ring-1 ring-[#c0ddf0] bg-white p-4 text-left shadow-sm transition-all duration-150 hover:-translate-y-0.5 hover:shadow-md hover:ring-teal-400 focus:outline-none focus-visible:ring-4 focus-visible:ring-teal-400 dark:bg-gray-900 dark:ring-gray-700 dark:hover:ring-teal-500"
    >
      <div className="flex items-start gap-3">
        <div
          aria-hidden="true"
          className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md bg-[#f0f7ff] text-[#3a6a8a] ring-1 ring-[#c0ddf0] dark:bg-gray-800 dark:ring-gray-700"
        >
          <Package className="h-4 w-4" />
        </div>
        <div className="min-w-0 flex-1">
          <h3 className="truncate text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
            {pkg.display_name || pkg.name}
          </h3>
          <p className="truncate text-xs text-[#3a6a8a] dark:text-gray-500">
            {repoLabel}
            {orgLabel ? ` · ${orgLabel}` : ''}
          </p>
        </div>
        {stars && (
          <span
            className="ml-auto flex shrink-0 items-center gap-0.5 text-[11px] font-medium text-[#3a6a8a] dark:text-gray-400"
            title={`${pkg.stars?.toLocaleString()} stars`}
          >
            <Star className="h-3 w-3 fill-current text-amber-500" aria-hidden="true" />
            {stars}
          </span>
        )}
      </div>

      {pkg.description && (
        <p className="mt-2 line-clamp-2 text-xs text-[#0a3a5a] dark:text-gray-300">
          {pkg.description}
        </p>
      )}

      <div className="mt-2 flex flex-wrap items-center gap-1.5">
        <span className="rounded-full bg-[#e8f3fb] px-2 py-0.5 text-[10px] font-medium text-[#1a4a6a] dark:bg-gray-800 dark:text-gray-300">
          ArtifactHub
        </span>
        {pkg.repository.verified_publisher && (
          <span
            className="inline-flex items-center gap-1 rounded-full bg-blue-100 px-2 py-0.5 text-[10px] font-medium text-blue-800 dark:bg-blue-900/40 dark:text-blue-300"
            title="Verified publisher — repo ownership confirmed via metadata token"
          >
            <CheckCircle2 className="h-3 w-3" aria-hidden="true" />
            Verified
          </span>
        )}
        {pkg.repository.official && (
          <span
            className="rounded-full bg-purple-100 px-2 py-0.5 text-[10px] font-medium text-purple-800 dark:bg-purple-900/40 dark:text-purple-300"
            title="Official package (manually granted by ArtifactHub)"
          >
            Official
          </span>
        )}
        {pkg.version && (
          <span className="ml-auto inline-flex items-center gap-1 text-[10px] font-mono text-[#3a6a8a] dark:text-gray-500">
            <Github className="h-3 w-3" aria-hidden="true" />
            {pkg.version}
          </span>
        )}
      </div>
    </button>
  )
}

function formatStars(stars: number | undefined): string | null {
  if (!stars || stars <= 0) return null
  if (stars >= 1000) {
    const k = stars / 1000
    return `${k.toFixed(1).replace(/\.0$/, '')}k`
  }
  return String(stars)
}

export default MarketplaceSearchTab

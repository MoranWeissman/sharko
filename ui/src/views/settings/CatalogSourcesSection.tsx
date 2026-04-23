import { useCallback, useEffect, useState } from 'react'
import { Loader2, RefreshCw, ShieldCheck, CheckCircle, XCircle, Library } from 'lucide-react'
import { api } from '@/services/api'
import type { CatalogSourceRecord } from '@/services/models'
import { SourceBadge } from '@/components/SourceBadge'

/**
 * CatalogSourcesSection — V123-1.8. Settings → Catalog Sources. Admin-only,
 * read-only list of the catalogs Sharko loads entries from.
 *
 * V123-1.1 resolved the open design question as *env-only*: the list is
 * driven by the `SHARKO_CATALOG_URLS` environment variable on the Sharko
 * pod, not an editable ConfigMap. This section therefore has no add/remove
 * controls — the only mutating action is the Tier-2 "Refresh now" button
 * which forces `GET` of every configured URL and rebuilds the in-memory
 * merged catalog.
 *
 * Palette rules (frontend-expert.md):
 * - Sharko blue family (`#d6eeff` / `#0a3a5a` / `#2a5a7a` / `#b4dcf5` / …).
 * - Every light-mode color class has a `dark:` sibling.
 * - No `gray-*` utilities in light mode.
 *
 * Security: the raw URL is rendered as *text*, never as an `<a href>`.
 * Paths may carry auth tokens and we don't want them leaking via Referer
 * headers or browser-history sync (same rationale as V123-1.7 SourceBadge).
 */
export function CatalogSourcesSection() {
  const [records, setRecords] = useState<CatalogSourceRecord[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const [statusMsg, setStatusMsg] = useState<
    { kind: 'success' | 'error'; text: string } | null
  >(null)

  const load = useCallback(async () => {
    setError(null)
    try {
      // Defensive guard — matches V123-1.7 pattern in AddonDetail.tsx where
      // legacy test mocks may not include the newer `api` method.
      if (typeof api.listCatalogSources !== 'function') {
        setRecords([])
        return
      }
      const data = await api.listCatalogSources()
      setRecords(data)
    } catch {
      setError('Failed to load catalog sources')
    }
  }, [])

  useEffect(() => {
    load()
  }, [load])

  const onRefresh = async () => {
    if (typeof api.refreshCatalogSources !== 'function') {
      setStatusMsg({ kind: 'error', text: 'Refresh failed' })
      return
    }
    setRefreshing(true)
    setStatusMsg(null)
    try {
      const data = await api.refreshCatalogSources()
      setRecords(data)
      setStatusMsg({ kind: 'success', text: 'Catalog sources refreshed' })
    } catch {
      setStatusMsg({ kind: 'error', text: 'Refresh failed' })
    } finally {
      setRefreshing(false)
    }
  }

  // "embedded" sentinel is the pseudo-source that the backend always
  // includes. When it's the ONLY record, we nudge the admin to set the env
  // variable to extend the catalog — not an error, just a hint.
  const onlyEmbedded =
    records !== null &&
    records.length === 1 &&
    records[0].url === 'embedded'

  return (
    <section
      aria-label="Catalog Sources"
      className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-6 shadow-sm dark:bg-gray-800 dark:ring-gray-700 space-y-5"
    >
      {/* Header */}
      <header className="flex items-start justify-between gap-3">
        <div className="flex items-center gap-3">
          <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-[#d6eeff] dark:bg-gray-700">
            <Library className="h-5 w-5 text-[#0a3a5a] dark:text-[#d6eeff]" aria-hidden />
          </div>
          <div>
            <h4 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
              Catalog Sources
            </h4>
            <p className="mt-0.5 text-xs text-[#2a5a7a] dark:text-gray-400 max-w-prose">
              Configured via the{' '}
              <code className="rounded bg-[#e8f4ff] px-1 font-mono text-[11px] text-[#0a3a5a] dark:bg-gray-900 dark:text-[#d6eeff]">
                SHARKO_CATALOG_URLS
              </code>{' '}
              environment variable. Not editable here — update the env and
              restart Sharko to change the list.
            </p>
          </div>
        </div>
        <button
          type="button"
          onClick={onRefresh}
          disabled={refreshing}
          aria-label="Refresh catalog sources"
          className="inline-flex shrink-0 items-center gap-1.5 rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-1.5 text-xs font-medium text-[#0a3a5a] shadow-sm hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-200 dark:hover:bg-gray-600"
        >
          {refreshing ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden />
          ) : (
            <RefreshCw className="h-3.5 w-3.5" aria-hidden />
          )}
          {refreshing ? 'Refreshing…' : 'Refresh now'}
        </button>
      </header>

      {/* Body */}
      {error ? (
        <div
          role="alert"
          className="flex items-center justify-between gap-3 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-400"
        >
          <span className="inline-flex items-center gap-2">
            <XCircle className="h-4 w-4" aria-hidden />
            {error}
          </span>
          <button
            type="button"
            onClick={load}
            className="rounded-md border border-red-300 px-2 py-1 text-xs font-medium text-red-700 hover:bg-red-100 dark:border-red-700 dark:text-red-300 dark:hover:bg-red-900/40"
          >
            Retry
          </button>
        </div>
      ) : records === null ? (
        <div
          aria-live="polite"
          className="flex items-center gap-2 text-sm text-[#2a5a7a] dark:text-gray-400"
        >
          <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
          Loading catalog sources…
        </div>
      ) : (
        <div className="overflow-hidden rounded-lg ring-1 ring-[#b4dcf5] dark:ring-gray-700">
          <table className="min-w-full divide-y divide-[#b4dcf5] text-left text-sm dark:divide-gray-700">
            <thead className="bg-[#e8f4ff] dark:bg-gray-900">
              <tr>
                <th
                  scope="col"
                  className="px-4 py-2 text-xs font-semibold uppercase tracking-wide text-[#3a6a8a] dark:text-gray-400"
                >
                  Source
                </th>
                <th
                  scope="col"
                  className="px-4 py-2 text-xs font-semibold uppercase tracking-wide text-[#3a6a8a] dark:text-gray-400"
                >
                  Status
                </th>
                <th
                  scope="col"
                  className="px-4 py-2 text-xs font-semibold uppercase tracking-wide text-[#3a6a8a] dark:text-gray-400"
                >
                  Entries
                </th>
                <th
                  scope="col"
                  className="px-4 py-2 text-xs font-semibold uppercase tracking-wide text-[#3a6a8a] dark:text-gray-400"
                >
                  Last fetched
                </th>
                <th
                  scope="col"
                  className="px-4 py-2 text-xs font-semibold uppercase tracking-wide text-[#3a6a8a] dark:text-gray-400"
                >
                  Verified
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[#c0ddf0] bg-[#f0f7ff] dark:divide-gray-700 dark:bg-gray-800">
              {records.map((r) => (
                <tr key={r.url}>
                  <td className="px-4 py-3 align-top">
                    <div className="flex flex-col gap-1">
                      <SourceBadge source={r.url} sourceRecord={r} />
                      {r.url !== 'embedded' && (
                        <span className="break-all text-[11px] text-[#3a6a8a] dark:text-gray-500">
                          {r.url}
                        </span>
                      )}
                    </div>
                  </td>
                  <td className="px-4 py-3 align-top">
                    <StatusChip status={r.status} />
                  </td>
                  <td className="px-4 py-3 align-top tabular-nums text-[#0a2a4a] dark:text-gray-100">
                    {r.entry_count}
                  </td>
                  <td className="px-4 py-3 align-top text-[#2a5a7a] dark:text-gray-400">
                    {r.last_fetched ?? '—'}
                  </td>
                  <td className="px-4 py-3 align-top">
                    {r.verified ? (
                      <span
                        aria-label={
                          r.issuer
                            ? `Verified (issuer: ${r.issuer})`
                            : 'Verified'
                        }
                        className="inline-flex items-center gap-1 rounded-full bg-emerald-50 px-2 py-0.5 text-[11px] font-medium text-emerald-700 ring-1 ring-emerald-200 dark:bg-emerald-900/20 dark:text-emerald-300 dark:ring-emerald-800"
                      >
                        <ShieldCheck className="h-3 w-3" aria-hidden />
                        Verified
                      </span>
                    ) : (
                      <span className="text-[11px] text-[#3a6a8a] dark:text-gray-500">
                        —
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Inline status message — mirrors the AIConfigSection "testResult" strip. */}
      {statusMsg ? (
        <div
          role="status"
          aria-live="polite"
          className={`flex items-center gap-2 rounded-lg px-3 py-2 text-xs ${
            statusMsg.kind === 'success'
              ? 'bg-green-50 text-green-700 dark:bg-green-900/20 dark:text-green-400'
              : 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-400'
          }`}
        >
          {statusMsg.kind === 'success' ? (
            <CheckCircle className="h-3.5 w-3.5" aria-hidden />
          ) : (
            <XCircle className="h-3.5 w-3.5" aria-hidden />
          )}
          {statusMsg.text}
        </div>
      ) : null}

      {/* Helpful-hint empty state. Only the embedded pseudo-source present
          = no `SHARKO_CATALOG_URLS` value at startup. */}
      {onlyEmbedded && !error ? (
        <p className="rounded-lg bg-[#e8f4ff] px-3 py-2 text-xs text-[#2a5a7a] dark:bg-gray-900 dark:text-gray-400">
          No third-party sources configured. Set{' '}
          <code className="rounded bg-[#d6eeff] px-1 font-mono text-[11px] text-[#0a3a5a] dark:bg-gray-700 dark:text-[#d6eeff]">
            SHARKO_CATALOG_URLS
          </code>{' '}
          to extend the catalog.
        </p>
      ) : null}
    </section>
  )
}

function StatusChip({ status }: { status: CatalogSourceRecord['status'] }) {
  if (status === 'ok') {
    return (
      <span
        className="inline-flex items-center gap-1 rounded-full bg-emerald-50 px-2 py-0.5 text-[11px] font-medium text-emerald-700 ring-1 ring-emerald-200 dark:bg-emerald-900/20 dark:text-emerald-300 dark:ring-emerald-800"
      >
        <span className="inline-block h-1.5 w-1.5 rounded-full bg-emerald-500" />
        ok
      </span>
    )
  }
  if (status === 'stale') {
    return (
      <span
        className="inline-flex items-center gap-1 rounded-full bg-amber-50 px-2 py-0.5 text-[11px] font-medium text-amber-700 ring-1 ring-amber-200 dark:bg-amber-900/20 dark:text-amber-300 dark:ring-amber-800"
      >
        <span className="inline-block h-1.5 w-1.5 rounded-full bg-amber-500" />
        stale
      </span>
    )
  }
  return (
    <span
      className="inline-flex items-center gap-1 rounded-full bg-red-50 px-2 py-0.5 text-[11px] font-medium text-red-700 ring-1 ring-red-200 dark:bg-red-900/20 dark:text-red-300 dark:ring-red-800"
    >
      <span className="inline-block h-1.5 w-1.5 rounded-full bg-red-500" />
      failed
    </span>
  )
}

export default CatalogSourcesSection

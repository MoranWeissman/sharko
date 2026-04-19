import { forwardRef, useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  AlertCircle,
  ArrowLeft,
  CheckCircle2,
  ExternalLink,
  FileText,
  Github,
  GitPullRequest,
  Info,
  Loader2,
  Package,
  Star,
  Tag,
} from 'lucide-react'
import {
  api,
  addAddon,
  isAddonAlreadyExistsError,
  type AddAddonResponse,
} from '@/services/api'
import type {
  CatalogEntry,
  CatalogReadmeResponse,
  CatalogVersionsResponse,
} from '@/services/models'
import { LoadingState } from '@/components/LoadingState'
import { ErrorState } from '@/components/ErrorState'
import { ScorecardBadge } from '@/components/ScorecardBadge'
import { AttributionNudge } from '@/components/AttributionNudge'
import { MarkdownRenderer } from '@/components/MarkdownRenderer'
import { VersionPicker } from '@/components/VersionPicker'
import { showToast } from '@/components/ToastNotification'

/**
 * MarketplaceAddonDetail — v1.21 QA Bundle 2 in-page Marketplace detail view.
 *
 * Replaces the popup-style MarketplaceConfigureModal that earlier Bundles
 * shipped. Maintainer feedback (2026-04-19): the modal was too cramped for
 * the metadata an operator wants when picking an addon, and the title
 * "Configure <addon>" was misleading — clicking the button doesn't configure
 * the addon on a cluster, it adds a new entry to the catalog (which then
 * spawns an ApplicationSet).
 *
 * Layout (top → bottom):
 *
 *   1. Back link + title row
 *      "← Back to Marketplace" + addon name + "✓ In your catalog" badge
 *
 *   2. Hero section
 *      Icon + name + one-line description + category/curator chips +
 *      license + OpenSSF score + GitHub stars + chart name
 *
 *   3. Action panel — "Add <addon> to your catalog"
 *      Embedded form (NOT a modal) with explainer text:
 *        "This creates an ArgoCD ApplicationSet for <addon> and adds an
 *         entry to your `addons-catalog.yaml`. The addon will be available
 *         to deploy on any cluster afterwards."
 *      Display name + Namespace + Chart-version picker (from VersionPicker).
 *      No sync-wave (Bundle 1 decision — operators set it on the addon
 *      page after creation).
 *      AttributionNudge inline if user has no PAT.
 *      "Add to catalog" submit (NOT "Configure").
 *      When the addon is already in the catalog, the panel collapses to
 *      a friendly link to the addon detail page so we don't tempt the user
 *      to open a no-op PR.
 *
 *   4. README section
 *      Markdown rendered via MarkdownRenderer (the in-house renderer used
 *      everywhere else — same XSS-safe parsing, no dangerouslySetInnerHTML).
 *      Loading skeleton while fetching; empty state when ArtifactHub doesn't
 *      have a README for this chart.
 *
 *   5. Metadata footer
 *      Helm chart name, repo URL, docs URL, source URL, maintainers.
 *
 * Data fetching:
 *   - Curated source: /catalog/addons/{name} for metadata + /catalog/addons/{name}/readme for README
 *   - ArtifactHub source: /catalog/remote/{repo}/{name} for both metadata AND README
 *
 * Accessibility (WCAG 2.1 AA):
 *   - The back link is the first focusable element when the view mounts and
 *     receives focus on initial render so keyboard users land in a sensible
 *     spot after the tab swap.
 *   - The header is wrapped in a <header role="banner"> landmark, the
 *     action panel in <section aria-labelledby=...>, README in another
 *     <section>, and metadata in a <footer> — the page reads as a coherent
 *     document, not a soup of divs.
 *   - All interactive controls are keyboard-navigable (no custom click
 *     handlers on non-button elements).
 */

export interface MarketplaceAddonDetailProps {
  /** Addon name from the URL (?mp_addon=). For curated source this is the
   *  curated catalog name; for AH source this is the ArtifactHub chart name. */
  addonName: string
  /** Where to fetch metadata + README from. */
  source: 'curated' | 'ah'
  /** ArtifactHub repo name — required when source is "ah". */
  ahRepoName?: string | null
  /** Called when the user clicks the "← Back to Marketplace" link. */
  onBack: () => void
}

interface DuplicateInfo {
  addon: string
  existingUrl: string
}

export function MarketplaceAddonDetail({
  addonName,
  source,
  ahRepoName,
  onBack,
}: MarketplaceAddonDetailProps) {
  // ─── Metadata + README state ─────────────────────────────────────────────
  const [entry, setEntry] = useState<CatalogEntry | null>(null)
  const [entryLoading, setEntryLoading] = useState(true)
  const [entryError, setEntryError] = useState<string | null>(null)

  const [readmeResp, setReadmeResp] = useState<CatalogReadmeResponse | null>(
    null,
  )
  const [readmeLoading, setReadmeLoading] = useState(true)

  // ─── Add-to-catalog form state ───────────────────────────────────────────
  const [name, setName] = useState('')
  const [namespace, setNamespace] = useState('')
  const [version, setVersion] = useState('')
  const [showPrereleases, setShowPrereleases] = useState(false)
  const [versionTouched, setVersionTouched] = useState(false)

  const [versionsResp, setVersionsResp] = useState<CatalogVersionsResponse | null>(
    null,
  )
  const [versionsLoading, setVersionsLoading] = useState(false)
  const [versionsError, setVersionsError] = useState<string | null>(null)

  // Pre-flight duplicate detection — same approach as the (now-retired)
  // Configure modal: lower-cased compare against the user's catalog so the
  // submit button is gated before the network round-trip.
  const [existingNames, setExistingNames] = useState<Set<string> | null>(null)
  const [hasPersonalToken, setHasPersonalToken] = useState<boolean | undefined>(
    undefined,
  )

  const [submitting, setSubmitting] = useState(false)
  const [duplicateInfo, setDuplicateInfo] = useState<DuplicateInfo | null>(null)
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [submitResult, setSubmitResult] = useState<AddAddonResponse | null>(null)

  const backLinkRef = useRef<HTMLButtonElement>(null)

  // ─── Initial focus on the back link for keyboard accessibility ───────────
  useEffect(() => {
    backLinkRef.current?.focus()
  }, [])

  // ─── Load metadata ───────────────────────────────────────────────────────
  useEffect(() => {
    let cancelled = false
    setEntryLoading(true)
    setEntryError(null)
    setEntry(null)

    const load = async () => {
      try {
        if (source === 'curated') {
          const e = await api.getCuratedCatalogEntry(addonName)
          if (!cancelled) setEntry(e)
        } else {
          if (!ahRepoName) {
            throw new Error('ArtifactHub repo name missing on URL (?mp_repo=)')
          }
          const detail = await api.getRemoteCatalogPackage(ahRepoName, addonName)
          if (cancelled) return
          if (!detail.package) {
            setEntryError('Package not found on ArtifactHub')
            return
          }
          const pkg = detail.package
          // Synthesise a CatalogEntry from the AH package shape so the rest
          // of this component renders uniformly. Fields the UI needs but AH
          // doesn't always expose (license/category) get sensible defaults.
          setEntry({
            name: pkg.normalized_name || pkg.name,
            description: pkg.description ?? '',
            chart: pkg.name,
            repo: pkg.repository.url || '',
            default_namespace: pkg.normalized_name || pkg.name,
            default_sync_wave: 0,
            maintainers:
              pkg.maintainers
                ?.map((m) => m.name)
                .filter((n): n is string => !!n) ?? [],
            license: pkg.license ?? '',
            // 'developer-tools' is the catch-all category for external
            // entries — cosmetic only, not persisted on submit.
            category: 'developer-tools',
            curated_by: [],
            github_stars: pkg.stars,
            homepage: pkg.home_url ?? pkg.repository.url,
            source_url: pkg.repository.url,
          } as CatalogEntry)
          // ArtifactHub package detail already includes the README, so we
          // can prime that state too and skip the second request below.
          setReadmeResp({
            readme: pkg.readme ?? '',
            source: 'artifacthub',
            ah_repo: pkg.repository.name,
            ah_chart: pkg.name,
          })
          setReadmeLoading(false)
        }
      } catch (e) {
        if (!cancelled) {
          setEntryError(e instanceof Error ? e.message : 'Failed to load addon')
        }
      } finally {
        if (!cancelled) setEntryLoading(false)
      }
    }
    void load()

    return () => {
      cancelled = true
    }
  }, [addonName, source, ahRepoName])

  // ─── Load README (curated source only — AH source primed it above) ───────
  useEffect(() => {
    if (source !== 'curated') return
    let cancelled = false
    setReadmeLoading(true)
    setReadmeResp(null)
    api
      .getCuratedCatalogReadme(addonName)
      .then((resp) => {
        if (!cancelled) setReadmeResp(resp)
      })
      .catch(() => {
        // README is best-effort — render an empty state rather than blocking
        // the "Add to catalog" panel.
        if (!cancelled) {
          setReadmeResp({ readme: '', source: 'artifacthub' })
        }
      })
      .finally(() => {
        if (!cancelled) setReadmeLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [addonName, source])

  // ─── Load chart versions for the picker ──────────────────────────────────
  useEffect(() => {
    if (!entry) return
    let cancelled = false
    setVersionsLoading(true)
    setVersionsError(null)
    setVersion('')
    setVersionTouched(false)

    if (source === 'curated') {
      // Curated entries → use the cached /catalog/addons/{name}/versions endpoint.
      api
        .listCuratedCatalogVersions(entry.name)
        .then((resp) => {
          if (cancelled) return
          setVersionsResp(resp)
          if (resp.latest_stable) setVersion(resp.latest_stable)
          else if (resp.versions[0]) setVersion(resp.versions[0].version)
        })
        .catch((e: unknown) => {
          if (cancelled) return
          setVersionsError(
            e instanceof Error ? e.message : 'Failed to load chart versions',
          )
        })
        .finally(() => {
          if (!cancelled) setVersionsLoading(false)
        })
    } else {
      // AH entries → use /catalog/validate so we get the chart's versions
      // without needing a curated-catalog entry. The validate response uses a
      // flat versions[] shape; we re-wrap it into CatalogVersionsResponse so
      // the shared VersionPicker doesn't need to know about either origin.
      api
        .validateCatalogChart(entry.repo, entry.chart)
        .then((resp) => {
          if (cancelled) return
          if (!resp.valid || !resp.versions) {
            setVersionsError(resp.message || 'Repo or chart not reachable')
            return
          }
          const wrapped: CatalogVersionsResponse = {
            addon: entry.name,
            chart: entry.chart,
            repo: entry.repo,
            versions: resp.versions,
            latest_stable: resp.latest_stable,
            cached_at: resp.cached_at ?? new Date().toISOString(),
          }
          setVersionsResp(wrapped)
          if (wrapped.latest_stable) {
            setVersion(wrapped.latest_stable)
          } else if (wrapped.versions[0]) {
            setVersion(wrapped.versions[0].version)
          }
        })
        .catch((e: unknown) => {
          if (cancelled) return
          setVersionsError(
            e instanceof Error ? e.message : 'Failed to load chart versions',
          )
        })
        .finally(() => {
          if (!cancelled) setVersionsLoading(false)
        })
    }
    return () => {
      cancelled = true
    }
  }, [entry, source])

  // ─── Pre-flight duplicate check + PAT lookup ─────────────────────────────
  useEffect(() => {
    let cancelled = false
    api
      .getAddonCatalog()
      .then((resp) => {
        if (cancelled) return
        const names = new Set(
          resp.addons.map((a) => a.addon_name.trim().toLowerCase()),
        )
        setExistingNames(names)
      })
      .catch(() => {
        if (!cancelled) setExistingNames(new Set())
      })
    api
      .getMe()
      .then((me) => {
        if (!cancelled) setHasPersonalToken(me.has_github_token)
      })
      .catch(() => {
        if (!cancelled) setHasPersonalToken(undefined)
      })
    return () => {
      cancelled = true
    }
  }, [])

  // ─── Seed form name/namespace once entry loads ───────────────────────────
  useEffect(() => {
    if (!entry) return
    setName(entry.name)
    setNamespace(entry.default_namespace)
  }, [entry])

  // ─── Derived state ───────────────────────────────────────────────────────
  const trimmedName = name.trim()
  const inCatalog = useMemo(() => {
    if (!existingNames || trimmedName.length === 0) return false
    return existingNames.has(trimmedName.toLowerCase())
  }, [existingNames, trimmedName])

  // The entry-level "is the original addon already in your catalog?" check.
  // Flipping the form's display name doesn't change this — we want a stable
  // signal for the top-bar badge.
  const entryInCatalog = useMemo(() => {
    if (!existingNames || !entry) return false
    return existingNames.has(entry.name.trim().toLowerCase())
  }, [existingNames, entry])

  const versionInList = useMemo(
    () => versionsResp?.versions.some((v) => v.version === version) ?? false,
    [versionsResp, version],
  )
  const versionInvalid =
    versionTouched && !!version && versionsResp !== null && !versionInList

  const formValid =
    trimmedName.length > 0 &&
    namespace.trim().length > 0 &&
    version.trim().length > 0 &&
    !versionInvalid &&
    !inCatalog &&
    !submitting &&
    submitResult === null

  const prURL = submitResult?.pr_url || submitResult?.result?.pr_url
  const prID = submitResult?.pr_id ?? submitResult?.result?.pr_id
  const merged = submitResult?.merged ?? submitResult?.result?.merged ?? false

  const handleSubmit = async () => {
    if (!formValid || !entry) return
    setSubmitting(true)
    setSubmitError(null)
    setDuplicateInfo(null)
    try {
      const res = await addAddon({
        name: trimmedName,
        chart: entry.chart,
        repo_url: entry.repo,
        version: version.trim(),
        namespace: namespace.trim(),
        // No sync_wave field — Bundle 1 decision; operators set it on the
        // addon page after creation.
        source: source === 'curated' ? 'marketplace' : 'artifacthub',
      })
      setSubmitResult(res)
      const label = prID || res.pr_id || res.result?.pr_id
        ? `PR #${res.pr_id ?? res.result?.pr_id}`
        : 'PR'
      const wasMerged = res.merged ?? res.result?.merged ?? false
      const url = res.pr_url || res.result?.pr_url
      if (url) {
        showToast(
          wasMerged ? `${label} merged →` : `${label} opened →`,
          'success',
        )
      } else {
        showToast(`${entry.name} added`, 'success')
      }
    } catch (e) {
      if (isAddonAlreadyExistsError(e)) {
        setDuplicateInfo({ addon: e.addon, existingUrl: e.existingUrl })
      } else {
        const msg = e instanceof Error ? e.message : 'Failed to open PR'
        setSubmitError(msg)
        showToast(`Failed to add addon — ${msg}`, 'info')
      }
    } finally {
      setSubmitting(false)
    }
  }

  // ─── Render ──────────────────────────────────────────────────────────────
  if (entryLoading) {
    return (
      <div className="flex flex-col gap-3">
        <BackLink onBack={onBack} ref={backLinkRef} />
        <LoadingState message="Loading addon details…" />
      </div>
    )
  }
  if (entryError || !entry) {
    return (
      <div className="flex flex-col gap-3">
        <BackLink onBack={onBack} ref={backLinkRef} />
        <ErrorState message={entryError ?? 'Addon not found'} />
      </div>
    )
  }

  return (
    <article className="flex flex-col gap-5" aria-labelledby="mp-addon-detail-title">
      {/* ─── 1. Top bar ─── */}
      <header className="flex flex-wrap items-start gap-3">
        <BackLink onBack={onBack} ref={backLinkRef} />
        <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2">
          <h1
            id="mp-addon-detail-title"
            className="truncate text-xl font-bold capitalize text-[#0a2a4a] dark:text-gray-100"
          >
            {entry.name}
          </h1>
          {entryInCatalog && (
            <Link
              to={`/addons/${encodeURIComponent(entry.name)}`}
              className="inline-flex items-center gap-1 rounded-full bg-green-100 px-2 py-0.5 text-[11px] font-semibold uppercase tracking-wide text-green-800 hover:bg-green-200 focus:outline-none focus-visible:ring-2 focus-visible:ring-green-500 dark:bg-green-900/40 dark:text-green-300 dark:hover:bg-green-900/60"
              title="Open the addon page in your catalog"
            >
              <CheckCircle2 className="h-3 w-3" aria-hidden="true" />
              In your catalog
            </Link>
          )}
          {entry.deprecated && (
            <span className="rounded-full bg-amber-100 px-2 py-0.5 text-[11px] font-semibold uppercase tracking-wide text-amber-800 dark:bg-amber-900/40 dark:text-amber-300">
              Deprecated
            </span>
          )}
          {source === 'ah' && (
            <span
              className="rounded-full bg-[#e8f3fb] px-2 py-0.5 text-[11px] font-medium text-[#1a4a6a] dark:bg-gray-800 dark:text-gray-300"
              title="Result fetched from ArtifactHub"
            >
              ArtifactHub
            </span>
          )}
        </div>
      </header>

      {/* ─── 2. Hero ─── */}
      <section
        aria-label="Addon overview"
        className="flex flex-col gap-3 rounded-lg border border-[#c0ddf0] bg-[#f0f7ff] p-4 dark:border-gray-700 dark:bg-gray-900 sm:flex-row sm:items-start"
      >
        <div
          aria-hidden="true"
          className="flex h-14 w-14 shrink-0 items-center justify-center rounded-md bg-white text-teal-600 ring-1 ring-[#c0ddf0] dark:bg-gray-800 dark:text-teal-400 dark:ring-gray-700"
        >
          <Package className="h-7 w-7" />
        </div>
        <div className="min-w-0 flex-1 space-y-2">
          <p className="text-sm text-[#0a3a5a] dark:text-gray-300">
            {entry.description || 'No description published for this chart.'}
          </p>
          <div className="flex flex-wrap items-center gap-2 text-xs text-[#2a5a7a] dark:text-gray-400">
            {entry.category && (
              <span className="inline-flex items-center gap-1 rounded-full bg-[#d6eeff] px-2 py-0.5 font-medium capitalize text-[#0a3a5a] dark:bg-gray-700 dark:text-gray-300">
                <Tag className="h-3 w-3" aria-hidden="true" />
                {entry.category}
              </span>
            )}
            {entry.curated_by.map((c) => (
              <span
                key={c}
                className="inline-flex items-center rounded-full bg-[#d6eeff] px-2 py-0.5 font-medium text-[#0a3a5a] dark:bg-gray-700 dark:text-gray-300"
                title={`Curated source: ${c}`}
              >
                {c}
              </span>
            ))}
            {entry.license && (
              <span
                className="inline-flex items-center rounded-full bg-white px-2 py-0.5 font-medium text-[#2a5a7a] ring-1 ring-[#c0ddf0] dark:bg-gray-800 dark:text-gray-400 dark:ring-gray-700"
                title={`License: ${entry.license}`}
              >
                {entry.license}
              </span>
            )}
            <ScorecardBadge
              score={entry.security_score}
              tier={entry.security_tier}
              updated={entry.security_score_updated}
            />
            {entry.github_stars !== undefined && entry.github_stars > 0 && (
              <span
                className="inline-flex items-center gap-1 font-medium"
                title={`${entry.github_stars.toLocaleString()} GitHub stars`}
              >
                <Github className="h-3.5 w-3.5" aria-hidden="true" />
                <Star className="h-3 w-3 fill-current text-amber-500" aria-hidden="true" />
                {formatStars(entry.github_stars)}
              </span>
            )}
          </div>
        </div>
      </section>

      {/* ─── 3. Action panel — Add to catalog ─── */}
      <section
        aria-labelledby="mp-add-panel-title"
        className="flex flex-col gap-3 rounded-lg border border-teal-200 bg-white p-4 shadow-sm dark:border-teal-700 dark:bg-gray-900"
      >
        <header className="flex flex-col gap-1">
          <h2
            id="mp-add-panel-title"
            className="text-base font-bold capitalize text-[#0a2a4a] dark:text-gray-100"
          >
            Add {entry.name} to your catalog
          </h2>
          <p className="text-sm text-[#2a5a7a] dark:text-gray-400">
            This creates an ArgoCD <strong>ApplicationSet</strong> for{' '}
            <code className="rounded bg-[#e8f3fb] px-1 py-0.5 font-mono text-xs text-[#0a3a5a] dark:bg-gray-800 dark:text-gray-300">
              {entry.name}
            </code>{' '}
            and adds an entry to your{' '}
            <code className="rounded bg-[#e8f3fb] px-1 py-0.5 font-mono text-xs text-[#0a3a5a] dark:bg-gray-800 dark:text-gray-300">
              addons-catalog.yaml
            </code>
            . The addon will be available to deploy on any cluster afterwards
            (you enable it per-cluster from the Catalog tab).
          </p>
        </header>

        {entryInCatalog ? (
          <div
            role="status"
            className="flex items-start gap-2 rounded-md border border-green-300 bg-green-50 p-3 text-sm text-green-900 dark:border-green-700 dark:bg-green-950/40 dark:text-green-200"
          >
            <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
            <p>
              <span className="font-semibold capitalize">{entry.name}</span> is
              already in your catalog.{' '}
              <Link
                to={`/addons/${encodeURIComponent(entry.name)}`}
                className="font-medium underline hover:no-underline"
              >
                Open its page
              </Link>{' '}
              to edit values or enable it on a cluster.
            </p>
          </div>
        ) : (
          <>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <Field label="Display name" htmlFor="mp-add-name" required>
                <input
                  id="mp-add-name"
                  type="text"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  className="w-full rounded-md border border-[#5a9dd0] bg-white px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100"
                />
              </Field>
              <Field label="Namespace" htmlFor="mp-add-ns" required>
                <input
                  id="mp-add-ns"
                  type="text"
                  value={namespace}
                  onChange={(e) => setNamespace(e.target.value)}
                  className="w-full rounded-md border border-[#5a9dd0] bg-white px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100"
                />
              </Field>
            </div>

            <Field label="Chart version" htmlFor="mp-add-version" required>
              <VersionPicker
                inputId="mp-add-version"
                value={version}
                onChange={(v) => {
                  setVersion(v)
                  setVersionTouched(true)
                }}
                versionsResp={versionsResp}
                loading={versionsLoading}
                error={versionsError}
                showPrereleases={showPrereleases}
                onShowPrereleasesChange={setShowPrereleases}
                invalid={versionInvalid}
              />
            </Field>

            {(inCatalog || duplicateInfo) && !submitResult && (
              <div
                role="alert"
                className="flex items-start gap-2 rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-700 dark:bg-amber-950/40 dark:text-amber-200"
              >
                <Info className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
                <p>
                  <span className="font-semibold">
                    {duplicateInfo?.addon ?? trimmedName}
                  </span>{' '}
                  is already in the catalog.{' '}
                  <Link
                    to={
                      duplicateInfo?.existingUrl ??
                      `/addons/${encodeURIComponent(trimmedName)}`
                    }
                    className="font-medium underline hover:no-underline"
                  >
                    Open its page
                  </Link>{' '}
                  to edit it, or change the Display name to register a
                  different copy.
                </p>
              </div>
            )}

            {hasPersonalToken === false && !submitResult && (
              <AttributionNudge inline />
            )}
            {submitResult?.attribution_warning === 'no_per_user_pat' &&
              hasPersonalToken !== false && <AttributionNudge inline />}

            {submitResult && prURL && (
              <div
                role="status"
                className="flex items-start gap-2 rounded-md border border-green-300 bg-green-50 p-3 text-sm text-green-900 dark:border-green-700 dark:bg-green-950/40 dark:text-green-200"
              >
                <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
                <div className="flex-1">
                  <p className="font-medium">
                    {merged
                      ? 'PR merged — addon added to your catalog'
                      : 'PR opened — your addon is on its way'}
                  </p>
                  <a
                    href={prURL}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="mt-1 inline-flex items-center gap-1 text-xs font-medium underline hover:no-underline"
                  >
                    <GitPullRequest className="h-3 w-3" aria-hidden="true" />
                    {prID ? `View PR #${prID} on GitHub` : 'View PR on GitHub'}
                    <ExternalLink className="h-3 w-3" aria-hidden="true" />
                  </a>
                </div>
              </div>
            )}

            {submitError && !submitResult && (
              <div
                role="alert"
                className="flex items-start gap-2 rounded-md border border-red-300 bg-red-50 p-3 text-sm text-red-900 dark:border-red-700 dark:bg-red-950/40 dark:text-red-200"
              >
                <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
                <p>{submitError}</p>
              </div>
            )}

            <div className="mt-1 flex flex-wrap items-center justify-end gap-2">
              <button
                type="button"
                onClick={onBack}
                className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] focus:outline-none focus-visible:ring-2 focus-visible:ring-[#6aade0] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
              >
                {submitResult ? 'Back to Marketplace' : 'Cancel'}
              </button>
              {!submitResult && (
                <button
                  type="button"
                  onClick={handleSubmit}
                  disabled={!formValid}
                  title={
                    inCatalog
                      ? `${trimmedName} is already in the catalog`
                      : !formValid
                        ? 'Fix the highlighted fields first'
                        : 'Open a PR adding this addon to your catalog'
                  }
                  className="inline-flex items-center gap-2 rounded-md bg-teal-600 px-4 py-2 text-sm font-medium text-white hover:bg-teal-700 focus:outline-none focus-visible:ring-2 focus-visible:ring-teal-500 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-teal-700 dark:hover:bg-teal-600"
                >
                  {submitting ? (
                    <>
                      <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
                      Submitting…
                    </>
                  ) : (
                    'Add to catalog'
                  )}
                </button>
              )}
            </div>
          </>
        )}
      </section>

      {/* ─── 4. README ─── */}
      <section
        aria-labelledby="mp-readme-title"
        className="flex flex-col gap-2 rounded-lg border border-[#c0ddf0] bg-white p-4 dark:border-gray-700 dark:bg-gray-900"
      >
        <header className="flex items-center gap-2 border-b border-[#c0ddf0] pb-2 dark:border-gray-700">
          <FileText className="h-4 w-4 text-teal-600 dark:text-teal-400" aria-hidden="true" />
          <h2
            id="mp-readme-title"
            className="text-base font-bold text-[#0a2a4a] dark:text-gray-100"
          >
            README
          </h2>
          {readmeResp?.ah_repo && readmeResp?.ah_chart && (
            <a
              href={`https://artifacthub.io/packages/helm/${encodeURIComponent(
                readmeResp.ah_repo,
              )}/${encodeURIComponent(readmeResp.ah_chart)}`}
              target="_blank"
              rel="noopener noreferrer"
              className="ml-auto inline-flex items-center gap-1 text-xs font-medium text-teal-700 underline hover:no-underline focus:outline-none focus-visible:ring-2 focus-visible:ring-teal-500 dark:text-teal-400"
            >
              View on ArtifactHub
              <ExternalLink className="h-3 w-3" aria-hidden="true" />
            </a>
          )}
        </header>

        {readmeLoading ? (
          <div className="space-y-2 py-2" aria-hidden="true">
            <div className="h-4 w-1/3 animate-pulse rounded bg-[#e0eef9] dark:bg-gray-800" />
            <div className="h-3 w-full animate-pulse rounded bg-[#e0eef9] dark:bg-gray-800" />
            <div className="h-3 w-5/6 animate-pulse rounded bg-[#e0eef9] dark:bg-gray-800" />
            <div className="h-3 w-2/3 animate-pulse rounded bg-[#e0eef9] dark:bg-gray-800" />
          </div>
        ) : readmeResp && readmeResp.readme.trim().length > 0 ? (
          <div className="prose prose-sm max-w-none dark:prose-invert">
            <MarkdownRenderer content={readmeResp.readme} />
          </div>
        ) : (
          <p className="py-2 text-sm italic text-[#3a6a8a] dark:text-gray-500">
            No README available from ArtifactHub for this chart.
          </p>
        )}
      </section>

      {/* ─── 5. Metadata footer ─── */}
      <footer className="flex flex-col gap-2 rounded-lg border border-dashed border-[#c0ddf0] bg-[#f7fbff] p-4 text-xs text-[#2a5a7a] dark:border-gray-700 dark:bg-gray-900 dark:text-gray-400">
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
          <span>
            <strong>Helm chart:</strong>{' '}
            <code className="font-mono">{entry.chart}</code>
          </span>
          {entry.repo && (
            <span>
              <strong>Repo:</strong>{' '}
              <a
                href={entry.repo}
                target="_blank"
                rel="noopener noreferrer"
                className="text-teal-700 underline hover:no-underline dark:text-teal-400"
              >
                {entry.repo}
              </a>
            </span>
          )}
          {entry.docs_url && (
            <a
              href={entry.docs_url}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1 text-teal-700 underline hover:no-underline dark:text-teal-400"
            >
              Docs <ExternalLink className="h-3 w-3" aria-hidden="true" />
            </a>
          )}
          {entry.source_url && (
            <a
              href={entry.source_url}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1 text-teal-700 underline hover:no-underline dark:text-teal-400"
            >
              Source <ExternalLink className="h-3 w-3" aria-hidden="true" />
            </a>
          )}
        </div>
        {entry.maintainers.length > 0 && (
          <p>
            <strong>Maintainers:</strong> {entry.maintainers.join(', ')}
          </p>
        )}
        <p className="italic text-[#3a6a8a] dark:text-gray-500">
          Trust signals shown above are sourced by Sharko (curator chips +
          OpenSSF score). They are not a substitute for your own security
          review.
        </p>
      </footer>
    </article>
  )
}

// ─── Helpers ───────────────────────────────────────────────────────────────

function formatStars(stars: number): string {
  if (stars >= 1000) {
    const k = stars / 1000
    return `${k.toFixed(1).replace(/\.0$/, '')}k`
  }
  return String(stars)
}

interface BackLinkProps {
  onBack: () => void
}

const BackLink = forwardRef<HTMLButtonElement, BackLinkProps>(function BackLink(
  { onBack },
  ref,
) {
  return (
    <button
      ref={ref}
      type="button"
      onClick={onBack}
      className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-sm font-medium text-teal-700 hover:bg-[#d6eeff] focus:outline-none focus-visible:ring-2 focus-visible:ring-teal-500 dark:text-teal-400 dark:hover:bg-gray-800"
    >
      <ArrowLeft className="h-4 w-4" aria-hidden="true" />
      Back to Marketplace
    </button>
  )
})

function Field({
  label,
  htmlFor,
  required,
  children,
}: {
  label: string
  htmlFor: string
  required?: boolean
  children: React.ReactNode
}) {
  return (
    <div>
      <label
        htmlFor={htmlFor}
        className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300"
      >
        {label}
        {required && (
          <span className="text-red-500" aria-hidden="true">
            {' '}
            *
          </span>
        )}
      </label>
      {children}
    </div>
  )
}

export default MarketplaceAddonDetail

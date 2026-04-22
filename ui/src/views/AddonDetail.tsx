import { useState, useEffect, useMemo, useCallback, useRef } from 'react'
import * as yaml from 'yaml'
import { useParams, useNavigate, Link, useSearchParams } from 'react-router-dom'
import { DetailNavPanel } from '@/components/DetailNavPanel'
import {
  ArrowLeft,
  Search,
  CheckCircle,
  AlertTriangle,
  XCircle,
  Ban,
  ExternalLink,
  Activity,
  Trash2,
  ArrowUpCircle,
  Loader2,
  LayoutGrid,
  Server,
  FileCode,
  Pencil,
  Plus,
  Minus,
  X,
  RefreshCw,
  Sparkles,
  ChevronDown,
  MessageSquare,
  Shield,
  Star,
} from 'lucide-react'
import { api, removeAddon, upgradeAddon, configureAddon, getAddonPRs } from '@/services/api'
import type { AddonCatalogItem, CatalogEntry, CatalogSourceRecord, ConnectionsListResponse, UpgradeCheckResponse, UpgradeRecommendations, RecommendationCard, ValueDiffEntry, ConflictCheckEntry, TrackedPR, AddonValuesSchemaResponse, MeResponse } from '@/services/models'
import { ValuesEditor } from '@/components/ValuesEditor'
import { RecentPRsPanel } from '@/components/RecentPRsPanel'
import { showToast } from '@/components/ToastNotification'
import { AttributionNudge } from '@/components/AttributionNudge'
import { MarkdownRenderer } from '@/components/MarkdownRenderer'
import { SourceBadge } from '@/components/SourceBadge'
import { StatCard } from '@/components/StatCard'
import { StatusBadge } from '@/components/StatusBadge'
import { LoadingState } from '@/components/LoadingState'
import { ErrorState } from '@/components/ErrorState'
import { YamlViewer } from '@/components/YamlViewer'
import { RoleGuard } from '@/components/RoleGuard'
import { ConfirmationModal } from '@/components/ConfirmationModal'

/** Returns true if target is a strictly lower semver than current. Fails open (returns false) if either can't be parsed. */
function isDowngrade(current: string, target: string): boolean {
  const parseSemver = (v: string): [number, number, number] | null => {
    const trimmed = v.replace(/^v/, '').split('-')[0]
    const parts = trimmed.split('.').map(Number)
    if (parts.length < 3 || parts.some(isNaN)) return null
    return [parts[0], parts[1], parts[2]]
  }
  const c = parseSemver(current)
  const t = parseSemver(target)
  if (!c || !t) return false
  if (t[0] !== c[0]) return t[0] < c[0]
  if (t[1] !== c[1]) return t[1] < c[1]
  return t[2] < c[2]
}

function cardGridClass(count: number): string {
  if (count === 1) return 'grid gap-3 grid-cols-1'
  if (count === 2) return 'grid gap-3 sm:grid-cols-2'
  return 'grid gap-3 sm:grid-cols-3'
}

function cardDescription(card: RecommendationCard): string {
  if (card.has_security) return card.advisory_summary ?? 'Includes security fix'
  if (card.has_breaking) return 'Breaking changes — review carefully'
  return 'Stable update'
}

function labelTooltip(label: string, currentMajor: number): string {
  if (label === 'Patch') return 'Same major.minor.X — lowest risk update'
  if (label === `Latest in ${currentMajor}.x`) return 'Latest stable in your current major'
  if (label === 'Latest Stable') return 'Newest stable version overall — may include breaking changes'
  if (label.startsWith('Latest in ')) return 'Stepping stone to next major — fewer breaking changes than jumping to latest'
  return ''
}

function RecommendedVersions({
  addonName,
  onAnalyze,
}: {
  addonName: string
  onAnalyze: (version: string) => void
}) {
  const [recommendations, setRecommendations] = useState<UpgradeRecommendations | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    api
      .getUpgradeRecommendations(addonName)
      .then((data) => setRecommendations(data))
      .catch(() => {
        // Recommendations are a best-effort enhancement — fail silently
        setRecommendations(null)
      })
      .finally(() => setLoading(false))
  }, [addonName])

  if (loading) return null

  // NEW: prefer cards array from backend
  if (recommendations?.cards && recommendations.cards.length > 0) {
    const cards = recommendations.cards
    const currentMajorRaw = parseInt(recommendations.current_version.split('.')[0], 10)
    const currentMajor = isNaN(currentMajorRaw) ? -1 : currentMajorRaw
    return (
      <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5 dark:ring-gray-700 dark:bg-gray-800">
        <h3 className="mb-1 text-base font-semibold text-[#0a2a4a] dark:text-gray-100">Recommended Upgrades</h3>
        <p className="mb-3 text-xs text-[#3a6a8a] dark:text-gray-400">Smart suggestions based on your current version</p>
        <div className={cardGridClass(cards.length)}>
          {cards.map((card) => (
            <div
              key={card.label}
              className={[
                'flex flex-col gap-2 rounded-lg px-4 py-3',
                card.is_recommended
                  ? 'bg-[#e0f7f4] ring-2 ring-teal-500 dark:bg-teal-900/20 dark:ring-teal-500'
                  : 'bg-[#e0f0ff] dark:bg-gray-700/50',
              ].join(' ')}
            >
              {/* Recommended indicator + reason */}
              {card.is_recommended && (
                <div className="space-y-0.5">
                  <div className="flex items-center gap-1 text-teal-600 dark:text-teal-400">
                    <Star className="h-3.5 w-3.5 fill-current" />
                    <span className="text-xs font-semibold">Recommended</span>
                  </div>
                  {card.reason && (
                    <p className="text-[11px] italic text-teal-700/80 dark:text-teal-400/80">
                      {card.reason}
                    </p>
                  )}
                </div>
              )}

              {/* Label + badges row */}
              <div className="flex items-start justify-between gap-2">
                <span
                  className="text-xs font-semibold uppercase tracking-wide text-[#3a6a8a] dark:text-gray-400 cursor-help"
                  title={currentMajor >= 0 ? labelTooltip(card.label, currentMajor) : undefined}
                >
                  {card.label}
                </span>
                <div className="flex items-center gap-1">
                  {card.has_security && (
                    <span className="flex items-center gap-0.5 rounded-full bg-green-100 px-1.5 py-0.5 text-[10px] font-semibold text-green-700 dark:bg-green-900/30 dark:text-green-400">
                      <Shield className="h-3 w-3" />
                      Security
                    </span>
                  )}
                  {(card.has_breaking || card.cross_major) && (
                    <span className="flex items-center gap-0.5 rounded-full bg-amber-100 px-1.5 py-0.5 text-[10px] font-semibold text-amber-700 dark:bg-amber-900/30 dark:text-amber-400">
                      <AlertTriangle className="h-3 w-3" />
                      Major change
                    </span>
                  )}
                </div>
              </div>

              {/* Version + description */}
              <div>
                <p className="font-mono text-base font-bold text-[#0a2a4a] dark:text-gray-100">{card.version}</p>
                <p className="mt-0.5 text-xs text-[#2a5a7a] dark:text-gray-400">{cardDescription(card)}</p>
              </div>

              <button
                type="button"
                onClick={() => onAnalyze(card.version)}
                className="mt-auto rounded-lg border border-teal-400 bg-white px-3 py-1.5 text-xs font-medium text-teal-700 hover:bg-teal-50 dark:border-teal-600 dark:bg-gray-800 dark:text-teal-400 dark:hover:bg-teal-900/20"
              >
                Analyze
              </button>
            </div>
          ))}
        </div>
      </div>
    )
  }

  // LEGACY FALLBACK: older backends that don't return cards
  const items: { label: string; version: string; description: string }[] = []
  if (recommendations?.next_patch) {
    items.push({ label: 'Next Patch', version: recommendations.next_patch, description: 'Safe bugfix update' })
  }
  if (recommendations?.next_minor) {
    items.push({ label: 'Next Minor', version: recommendations.next_minor, description: 'Feature update, same major' })
  }
  if (recommendations?.latest_stable) {
    const alreadyShown = items.some((i) => i.version === recommendations.latest_stable)
    if (!alreadyShown) {
      items.push({ label: 'Latest Stable', version: recommendations.latest_stable, description: 'Latest stable release' })
    }
  }

  if (items.length === 0) return null

  return (
    <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5 dark:ring-gray-700 dark:bg-gray-800">
      <h3 className="mb-1 text-base font-semibold text-[#0a2a4a] dark:text-gray-100">Recommended Upgrades</h3>
      <p className="mb-3 text-xs text-[#3a6a8a] dark:text-gray-400">Smart suggestions based on your current version</p>
      <div className="grid gap-3 sm:grid-cols-3">
        {items.map((item) => (
          <div
            key={item.label}
            className="flex flex-col gap-2 rounded-lg bg-[#e0f0ff] px-4 py-3 dark:bg-gray-700/50"
          >
            <div>
              <span className="text-xs font-semibold uppercase tracking-wide text-[#3a6a8a] dark:text-gray-400">
                {item.label}
              </span>
              <p className="mt-0.5 font-mono text-base font-bold text-[#0a2a4a] dark:text-gray-100">{item.version}</p>
              <p className="mt-0.5 text-xs text-[#2a5a7a] dark:text-gray-400">{item.description}</p>
            </div>
            <button
              type="button"
              onClick={() => onAnalyze(item.version)}
              className="mt-auto rounded-lg border border-teal-400 bg-white px-3 py-1.5 text-xs font-medium text-teal-700 hover:bg-teal-50 dark:border-teal-600 dark:bg-gray-800 dark:text-teal-400 dark:hover:bg-teal-900/20"
            >
              Analyze
            </button>
          </div>
        ))}
      </div>
    </div>
  )
}

function UpgradeVersionList({
  addonName,
  currentVersion,
  onAnalyze,
}: {
  addonName: string
  currentVersion: string
  onAnalyze: (version: string) => void
}) {
  const [versions, setVersions] = useState<{ version: string; app_version?: string }[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [visibleCount, setVisibleCount] = useState(5)
  const [searchQuery, setSearchQuery] = useState('')

  useEffect(() => {
    api
      .getUpgradeVersions(addonName)
      .then((data) => {
        // Filter out the current version — only show newer/different versions
        const available = (data.versions ?? []).filter((v) => v.version !== currentVersion)
        setVersions(available)
      })
      .catch(() => {
        setError('Could not check for available upgrades')
      })
      .finally(() => setLoading(false))
  }, [addonName, currentVersion])

  if (loading) return <LoadingState message="Checking for upgrades..." />

  if (error) {
    return (
      <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5">
        <h3 className="text-base font-semibold text-[#0a2a4a]">Available Upgrades</h3>
        <p className="mt-2 text-sm text-[#2a5a7a]">{error}</p>
        <p className="mt-1 text-xs text-[#3a6a8a]">
          The upgrade versions API may not be configured.
        </p>
      </div>
    )
  }

  if (versions.length === 0) {
    return (
      <div className="rounded-xl ring-2 ring-green-300 bg-green-50 p-5 dark:ring-green-700 dark:bg-green-950/30">
        <div className="flex items-center gap-2">
          <CheckCircle className="h-5 w-5 text-green-600 dark:text-green-400" />
          <h3 className="text-base font-semibold text-green-800 dark:text-green-300">Running latest version</h3>
        </div>
        <p className="mt-1 text-sm text-green-700 dark:text-green-400">
          No newer versions available for {addonName}.
        </p>
      </div>
    )
  }

  const versionPattern = /^\d+\.\d+/

  const filteredVersions = searchQuery
    ? versions.filter((v) => v.version.includes(searchQuery))
    : versions

  const effectiveVisibleCount = searchQuery ? filteredVersions.length : visibleCount
  const visibleVersions = filteredVersions.slice(0, effectiveVisibleCount)
  const remaining = filteredVersions.length - effectiveVisibleCount

  const showNotFound =
    searchQuery.length > 0 &&
    filteredVersions.length === 0 &&
    versionPattern.test(searchQuery)

  return (
    <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5 dark:ring-gray-700 dark:bg-gray-800">
      <h3 className="text-base font-semibold text-[#0a2a4a] dark:text-gray-100">Available Versions</h3>
      <p className="mb-1 text-xs text-[#3a6a8a] dark:text-gray-400">from Helm repository</p>
      <p className="mb-3 text-sm text-[#2a5a7a] dark:text-gray-300">
        {versions.length} newer version{versions.length !== 1 ? 's' : ''} available
      </p>

      {/* Search input */}
      <div className="relative mb-3">
        <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[#5a9dd0] dark:text-gray-400" />
        <input
          type="text"
          placeholder="Jump to version (e.g. 0.12.5)..."
          value={searchQuery}
          onChange={(e) => setSearchQuery(e.target.value)}
          className="w-full rounded-md ring-2 ring-[#5a9dd0] bg-[#e8f4ff] pl-9 pr-3 py-2 text-sm text-[#0a2a4a] placeholder-[#5a8aaa] focus:outline-none focus:ring-teal-500 dark:ring-gray-600 dark:bg-gray-900 dark:text-gray-200 dark:placeholder-gray-500"
        />
      </div>

      <div className="space-y-2">
        {visibleVersions.map((v, i) => (
          <div
            key={v.version}
            className="rounded-lg bg-[#e0f0ff] px-4 py-3 dark:bg-gray-700/50"
          >
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2">
                <span className="font-mono text-sm font-bold text-[#0a2a4a] dark:text-gray-100">{v.version}</span>
                {i === 0 && !searchQuery && (
                  <span className="rounded-full bg-green-100 px-2 py-0.5 text-[10px] font-semibold text-green-700 dark:bg-green-900/40 dark:text-green-400">
                    LATEST
                  </span>
                )}
                {v.app_version && (
                  <span className="text-xs text-[#3a6a8a] dark:text-gray-400">app {v.app_version}</span>
                )}
              </div>
              <div className="flex items-center gap-2">
                <button
                  type="button"
                  onClick={() => onAnalyze(v.version)}
                  className="rounded-lg border border-teal-400 bg-white px-3 py-1.5 text-xs font-medium text-teal-700 hover:bg-teal-50 dark:border-teal-600 dark:bg-gray-800 dark:text-teal-400 dark:hover:bg-teal-900/20"
                >
                  Analyze
                </button>
              </div>
            </div>
          </div>
        ))}
      </div>

      {showNotFound && (
        <div className="mt-2 rounded-lg ring-2 ring-[#6aade0] bg-[#e8f4ff] p-3 dark:ring-gray-600 dark:bg-gray-900/50">
          {searchQuery === currentVersion ? (
            <p className="text-sm text-amber-700 dark:text-amber-400">
              Already on version {searchQuery} — nothing to upgrade.
            </p>
          ) : (
            <>
              <p className="text-sm text-[#2a5a7a] dark:text-gray-300">
                Version "{searchQuery}" not in common versions. Try analyzing it directly:
              </p>
              <button
                type="button"
                onClick={() => onAnalyze(searchQuery)}
                className="mt-2 rounded-lg bg-teal-600 px-4 py-1.5 text-xs font-medium text-white hover:bg-teal-700"
              >
                Analyze {searchQuery}
              </button>
            </>
          )}
        </div>
      )}

      {remaining > 0 && (
        <button
          type="button"
          onClick={() => setVisibleCount((c) => c + 5)}
          className="mt-3 w-full rounded-lg bg-[#e0f0ff] px-4 py-2.5 text-center text-sm font-medium text-teal-600 hover:bg-[#d6eeff] dark:bg-gray-700/50 dark:text-teal-400 dark:hover:bg-gray-700"
        >
          Show more versions ({remaining} remaining)
        </button>
      )}
    </div>
  )
}

type InlineChangeTab = 'added' | 'removed' | 'changed'

function InlineUpgradeResults({
  addonName,
  targetVersion,
  currentVersion,
  result,
  analyzing,
  analyzeError,
  onRetry,
  onClose,
  onUpgrade,
  onUpgradeComplete,
}: {
  addonName: string
  targetVersion: string
  currentVersion: string
  result: UpgradeCheckResponse | null
  analyzing: boolean
  analyzeError: string | null
  onRetry: () => void
  onClose: () => void
  onUpgrade: (version: string) => Promise<{ pr_url?: string; pull_request_url?: string }>
  onUpgradeComplete?: () => void
}) {
  const [activeTab, setActiveTab] = useState<InlineChangeTab>('added')
  const [aiSummary, setAiSummary] = useState<string | null>(null)
  const [aiLoading, setAiLoading] = useState(false)
  const [aiError, setAiError] = useState<string | null>(null)
  // Inline upgrade state for this analysis
  const [upgradeConfirming, setUpgradeConfirming] = useState(false)
  const [upgradeSubmitting, setUpgradeSubmitting] = useState(false)
  const [upgradePrUrl, setUpgradePrUrl] = useState<string | null>(null)
  const [upgradeError, setUpgradeError] = useState<string | null>(null)
  const [upgradeDone, setUpgradeDone] = useState(false)
  // Downgrade modal state
  const [downgradeModalOpen, setDowngradeModalOpen] = useState(false)

  const downgrade = isDowngrade(currentVersion, targetVersion)

  const handleUpgradeConfirm = async () => {
    setUpgradeSubmitting(true)
    setUpgradeError(null)
    try {
      const res = await onUpgrade(targetVersion)
      const prUrl = res?.pr_url || res?.pull_request_url || null
      setUpgradePrUrl(prUrl)
      setUpgradeDone(true)
      setUpgradeConfirming(false)
      // Auto-refresh the addon data after 2 seconds so the version shows as updated
      if (onUpgradeComplete) {
        setTimeout(() => { onUpgradeComplete() }, 2000)
      }
    } catch (err) {
      setUpgradeError(err instanceof Error ? err.message : 'Upgrade failed')
      setUpgradeConfirming(false)
    } finally {
      setUpgradeSubmitting(false)
    }
  }

  const handleGetAISummary = useCallback(async () => {
    setAiLoading(true)
    setAiError(null)
    setAiSummary(null)
    try {
      const data = await api.getAISummary(addonName, targetVersion)
      setAiSummary(data.summary)
    } catch (err) {
      setAiError(err instanceof Error ? err.message : 'AI analysis failed')
    } finally {
      setAiLoading(false)
    }
  }, [addonName, targetVersion])

  if (analyzing) {
    return <LoadingState message={`Analyzing upgrade to ${targetVersion}...`} />
  }

  if (analyzeError) {
    return (
      <div className="rounded-xl ring-2 ring-red-300 bg-red-50 p-5 dark:ring-red-700 dark:bg-red-950/30">
        <div className="flex items-center gap-2 mb-2">
          <AlertTriangle className="h-5 w-5 text-red-500" />
          <h3 className="text-base font-semibold text-red-800 dark:text-red-300">Analysis Failed</h3>
        </div>
        <p className="text-sm text-red-700 dark:text-red-400 mb-3">{analyzeError}</p>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={onRetry}
            className="inline-flex items-center gap-2 rounded-lg bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700"
          >
            <RefreshCw className="h-4 w-4" />
            Retry
          </button>
          <button
            type="button"
            onClick={onClose}
            className="rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300"
          >
            Dismiss
          </button>
        </div>
      </div>
    )
  }

  if (!result) return null

  const risk = getRisk(result)

  return (
    <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5 dark:ring-gray-700 dark:bg-gray-800">
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-2">
          {riskIcon(risk)}
          <h3 className="text-base font-semibold text-[#0a2a4a] dark:text-gray-100">
            Upgrade Analysis: {result.current_version} &rarr; {result.target_version}
          </h3>
        </div>
        <button
          type="button"
          onClick={onClose}
          className="rounded-md p-1 text-[#3a6a8a] hover:bg-[#d6eeff] hover:text-[#0a2a4a] dark:text-gray-400 dark:hover:bg-gray-700"
          aria-label="Close analysis"
        >
          <X className="h-4 w-4" />
        </button>
      </div>

      {/* Downgrade warning banner */}
      {downgrade && (
        <div className="mb-4 flex items-start gap-2 rounded-lg border border-amber-300 bg-amber-50 px-4 py-3 dark:border-amber-700 dark:bg-amber-950/40">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
          <div>
            <p className="text-sm font-semibold text-amber-800 dark:text-amber-300">
              ⚠ This is a downgrade.
            </p>
            <p className="mt-0.5 text-xs text-amber-700 dark:text-amber-400">
              Helm values from a newer version may not be backward-compatible with <span className="font-mono">{targetVersion}</span>. Review the changes carefully before proceeding.
            </p>
          </div>
        </div>
      )}

      {/* Baseline unavailable banner */}
      {result.baseline_unavailable && (
        <div className="mb-4 flex items-start gap-2 rounded-lg border border-blue-300 bg-blue-50 px-4 py-3 dark:border-blue-700 dark:bg-blue-950/40">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-blue-500" />
          <div>
            <p className="text-sm font-medium text-blue-800 dark:text-blue-300">
              Current version not available in Helm repo
            </p>
            <p className="mt-0.5 text-xs text-blue-700 dark:text-blue-400">
              {result.baseline_note || 'Showing target version details only. Diff comparison is not available.'}
            </p>
          </div>
        </div>
      )}

      {/* Baseline note (fallback version used) */}
      {result.baseline_note && !result.baseline_unavailable && (
        <div className="mb-4 flex items-center gap-2 rounded-lg border border-amber-300 bg-amber-50 px-4 py-3 dark:border-amber-700 dark:bg-amber-950/40">
          <AlertTriangle className="h-4 w-4 shrink-0 text-amber-500" />
          <p className="text-sm text-amber-700 dark:text-amber-400">{result.baseline_note}</p>
        </div>
      )}

      {/* Risk summary */}
      <div className={`mb-4 rounded-lg border px-4 py-3 ${
        risk === 'conflicts' ? 'border-red-300 bg-red-50 dark:border-red-700 dark:bg-red-950/40'
          : risk === 'minor' ? 'border-amber-300 bg-amber-50 dark:border-amber-700 dark:bg-amber-950/40'
          : 'border-green-300 bg-green-50 dark:border-green-700 dark:bg-green-950/40'
      }`}>
        <div className="flex items-center gap-2">
          {riskIcon(risk)}
          <span className="text-sm font-semibold">{riskLabel(risk)}</span>
          <span className="text-sm text-[#2a5a7a] dark:text-gray-400">
            &mdash; {result.total_changes} total change{result.total_changes !== 1 ? 's' : ''}
          </span>
        </div>
      </div>

      {/* Conflicts */}
      {(result.conflicts ?? []).length > 0 && (
        <div className="mb-4 rounded-lg border-2 border-red-300 bg-red-50 p-4 dark:border-red-700 dark:bg-red-950/30">
          <div className="flex items-center gap-2 mb-3">
            <AlertTriangle className="h-4 w-4 text-red-500" />
            <span className="text-sm font-semibold text-red-800 dark:text-red-300">
              {result.conflicts.length} Conflict{result.conflicts.length !== 1 ? 's' : ''}
            </span>
          </div>
          <div className="space-y-2">
            {result.conflicts.map((c: ConflictCheckEntry) => (
              <div key={`${c.path}-${c.source}`} className="rounded bg-red-100/50 px-3 py-2 text-xs dark:bg-red-900/20">
                <code className="font-medium text-[#0a2a4a] dark:text-gray-100">{c.path}</code>
                <div className="mt-1 flex flex-wrap gap-2">
                  <span>yours: <code className="rounded bg-blue-100 px-1 text-blue-700 dark:bg-blue-900/40 dark:text-blue-300">{c.configured_value}</code></span>
                  <span>old: <code className="rounded bg-[#d6eeff] px-1 text-[#1a4a6a] dark:bg-gray-800 dark:text-gray-400">{c.old_default}</code></span>
                  <span>new: <code className="rounded bg-amber-100 px-1 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300">{c.new_default}</code></span>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Change tabs */}
      <div className="mb-3 flex gap-1 rounded-lg bg-[#d0e8f8] p-1 dark:bg-gray-900">
        {([
          { key: 'added' as InlineChangeTab, label: 'Added', count: (result.added ?? []).length },
          { key: 'removed' as InlineChangeTab, label: 'Removed', count: (result.removed ?? []).length },
          { key: 'changed' as InlineChangeTab, label: 'Changed', count: (result.changed ?? []).length },
        ]).map((tab) => (
          <button
            key={tab.key}
            type="button"
            onClick={() => setActiveTab(tab.key)}
            className={`flex-1 rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
              activeTab === tab.key
                ? 'bg-white text-[#0a2a4a] shadow-sm dark:bg-gray-700 dark:text-white'
                : 'text-[#2a5a7a] hover:bg-[#e0f0ff] dark:text-gray-400 dark:hover:bg-gray-800'
            }`}
          >
            {tab.label} ({tab.count})
          </button>
        ))}
      </div>

      {/* Change list */}
      <div className="max-h-60 overflow-y-auto rounded-lg bg-white ring-1 ring-[#c0ddf0] dark:bg-gray-900 dark:ring-gray-700">
        {activeTab === 'added' && (
          (result.added ?? []).length === 0
            ? <p className="py-4 text-center text-sm text-[#2a5a7a] dark:text-gray-400">No added fields.</p>
            : (result.added ?? []).map((entry: ValueDiffEntry) => (
              <div key={entry.path} className="flex items-start gap-3 border-b border-[#e0f0ff] px-4 py-2.5 last:border-0 dark:border-gray-800">
                <Plus className="mt-0.5 h-4 w-4 shrink-0 text-green-500" />
                <div className="min-w-0 flex-1">
                  <code className="text-xs font-medium text-[#0a2a4a] dark:text-gray-100">{entry.path}</code>
                  {entry.new_value != null && (
                    <p className="mt-0.5 text-xs text-green-600 dark:text-green-400">
                      Default: <code className="rounded bg-green-100 px-1 dark:bg-green-900/40">{entry.new_value}</code>
                    </p>
                  )}
                </div>
              </div>
            ))
        )}
        {activeTab === 'removed' && (
          (result.removed ?? []).length === 0
            ? <p className="py-4 text-center text-sm text-[#2a5a7a] dark:text-gray-400">No removed fields.</p>
            : (result.removed ?? []).map((entry: ValueDiffEntry) => (
              <div key={entry.path} className="flex items-start gap-3 border-b border-[#e0f0ff] px-4 py-2.5 last:border-0 dark:border-gray-800">
                <Minus className="mt-0.5 h-4 w-4 shrink-0 text-red-500" />
                <div className="min-w-0 flex-1">
                  <code className="text-xs font-medium text-[#0a2a4a] dark:text-gray-100">{entry.path}</code>
                  {entry.old_value != null && (
                    <p className="mt-0.5 text-xs text-red-600 dark:text-red-400">
                      Was: <code className="rounded bg-red-100 px-1 line-through dark:bg-red-900/40">{entry.old_value}</code>
                    </p>
                  )}
                </div>
              </div>
            ))
        )}
        {activeTab === 'changed' && (
          (result.changed ?? []).length === 0
            ? <p className="py-4 text-center text-sm text-[#2a5a7a] dark:text-gray-400">No changed defaults.</p>
            : (result.changed ?? []).map((entry: ValueDiffEntry) => (
              <div key={entry.path} className="flex items-start gap-3 border-b border-[#e0f0ff] px-4 py-2.5 last:border-0 dark:border-gray-800">
                <RefreshCw className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
                <div className="min-w-0 flex-1">
                  <code className="text-xs font-medium text-[#0a2a4a] dark:text-gray-100">{entry.path}</code>
                  <p className="mt-0.5 flex flex-wrap items-center gap-1 text-xs">
                    <code className="rounded bg-red-100 px-1 text-red-700 dark:bg-red-900/40 dark:text-red-400">{entry.old_value ?? '(empty)'}</code>
                    <span className="text-[#3a6a8a]">&rarr;</span>
                    <code className="rounded bg-green-100 px-1 text-green-700 dark:bg-green-900/40 dark:text-green-400">{entry.new_value ?? '(empty)'}</code>
                  </p>
                </div>
              </div>
            ))
        )}
      </div>

      {/* Release notes */}
      {result.release_notes && (
        <details className="mt-4 rounded-lg ring-1 ring-[#c0ddf0] bg-white dark:ring-gray-700 dark:bg-gray-900">
          <summary className="cursor-pointer px-4 py-3 text-sm font-semibold text-[#0a2a4a] dark:text-gray-100 select-none flex items-center gap-2">
            <ChevronDown className="h-4 w-4 text-[#3a6a8a]" />
            Release Notes
          </summary>
          <div className="border-t border-[#c0ddf0] px-4 py-3 dark:border-gray-700">
            <MarkdownRenderer content={result.release_notes} />
          </div>
        </details>
      )}

      {/* AI Analysis button */}
      <div className="mt-4">
        {!aiSummary && !aiLoading && !aiError && (
          <button
            type="button"
            onClick={handleGetAISummary}
            className="inline-flex items-center gap-2 rounded-lg bg-[#0a2a4a] px-4 py-2 text-sm font-medium text-white hover:bg-[#14466e] dark:bg-teal-700 dark:hover:bg-teal-600"
          >
            <Sparkles className="h-4 w-4" />
            AI Analysis
          </button>
        )}
        {aiLoading && (
          <div className="flex items-center gap-2 text-sm text-[#2a5a7a] dark:text-gray-400">
            <Loader2 className="h-4 w-4 animate-spin" />
            Generating AI analysis...
          </div>
        )}
        {aiError && (
          <div className="rounded-lg bg-red-50 px-4 py-3 dark:bg-red-950/30">
            <p className="text-sm text-red-600 dark:text-red-400">{aiError}</p>
            <button
              type="button"
              onClick={handleGetAISummary}
              className="mt-2 inline-flex items-center gap-1 text-xs font-medium text-red-600 hover:text-red-800 dark:text-red-400"
            >
              <RefreshCw className="h-3 w-3" />
              Retry
            </button>
          </div>
        )}
        {aiSummary && (
          <div className="rounded-lg ring-1 ring-[#c0ddf0] bg-white px-4 py-3 dark:ring-gray-700 dark:bg-gray-900">
            <div className="flex items-center gap-2 mb-2">
              <Sparkles className="h-4 w-4 text-teal-500" />
              <span className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">AI Analysis</span>
            </div>
            <MarkdownRenderer content={aiSummary} />
          </div>
        )}
      </div>

      {/* Downgrade typed-confirmation modal */}
      <ConfirmationModal
        open={downgradeModalOpen}
        onClose={() => setDowngradeModalOpen(false)}
        onConfirm={handleUpgradeConfirm}
        title={`Downgrade ${addonName} to ${targetVersion}?`}
        description={`This is a downgrade from ${currentVersion}. Helm values may not be backward-compatible. This will create a pull request with the version change.`}
        confirmText="Confirm Downgrade"
        typeToConfirm="DOWNGRADE"
        destructive
        loading={upgradeSubmitting}
      />

      {/* Upgrade action */}
      {!upgradeDone && !upgradeSubmitting && !upgradeConfirming && (
        <div className="mt-4 border-t border-[#c0ddf0] pt-4 dark:border-gray-700">
          <button
            type="button"
            onClick={() => {
              setUpgradeError(null)
              if (downgrade) {
                setDowngradeModalOpen(true)
              } else {
                setUpgradeConfirming(true)
              }
            }}
            className="inline-flex items-center gap-2 rounded-lg bg-teal-600 px-5 py-2.5 text-sm font-semibold text-white hover:bg-teal-700 dark:bg-teal-700 dark:hover:bg-teal-600"
          >
            <ArrowUpCircle className="h-4 w-4" />
            {downgrade ? 'Downgrade' : 'Upgrade'} to {targetVersion}
          </button>
        </div>
      )}
      {upgradeConfirming && (
        <div className="mt-4 border-t border-[#c0ddf0] pt-4 dark:border-gray-700">
          <div className="flex items-start gap-3 rounded-lg bg-amber-50 px-4 py-3 dark:bg-amber-950/40">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
            <div className="flex-1">
              {result && (result.conflicts ?? []).length > 0 ? (
                <p className="text-sm font-semibold text-amber-800 dark:text-amber-300">
                  This upgrade has conflicts. Are you sure you want to upgrade to {targetVersion}?
                </p>
              ) : (
                <p className="text-sm font-semibold text-amber-800 dark:text-amber-300">
                  Upgrade {addonName} to {targetVersion}?
                </p>
              )}
              <p className="mt-0.5 text-xs text-amber-700 dark:text-amber-400">This will create a pull request with the version change.</p>
              <div className="mt-3 flex items-center gap-2">
                <button
                  type="button"
                  onClick={handleUpgradeConfirm}
                  className="inline-flex items-center gap-2 rounded-lg bg-teal-600 px-4 py-2 text-sm font-medium text-white hover:bg-teal-700 dark:bg-teal-700 dark:hover:bg-teal-600"
                >
                  Yes, upgrade
                </button>
                <button
                  type="button"
                  onClick={() => setUpgradeConfirming(false)}
                  className="rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300"
                >
                  Cancel
                </button>
              </div>
            </div>
          </div>
        </div>
      )}
      {upgradeSubmitting && (
        <div className="mt-4 border-t border-[#c0ddf0] pt-4 dark:border-gray-700">
          <div className="space-y-2">
            <div className="flex items-center gap-2">
              <CheckCircle className="h-4 w-4 text-green-500" />
              <span className="text-sm text-[#2a5a7a] dark:text-gray-400">Upgrade confirmed</span>
            </div>
            <div className="flex items-center gap-2">
              <Loader2 className="h-4 w-4 animate-spin text-teal-500" />
              <span className="text-sm text-[#2a5a7a] dark:text-gray-400">Creating pull request...</span>
            </div>
          </div>
        </div>
      )}
      {upgradeDone && (() => {
        // Extract PR number from URL (e.g. https://github.com/org/repo/pull/42 → 42)
        const prNum = upgradePrUrl ? upgradePrUrl.match(/\/pull\/(\d+)/)?.[1] : null
        return (
          <div className="mt-4 border-t border-[#c0ddf0] pt-4 dark:border-gray-700">
            <div className="rounded-lg ring-2 ring-green-300 bg-green-50 p-4 dark:ring-green-700 dark:bg-green-950/30">
              <div className="space-y-1.5">
                {/* Step 1 done */}
                <div className="flex items-center gap-2">
                  <CheckCircle className="h-4 w-4 text-green-500" />
                  <span className="text-sm text-green-700 dark:text-green-400">
                    {prNum ? `PR #${prNum} created.` : 'Pull request created.'}
                    {upgradePrUrl && (
                      <>
                        {' '}
                        <a
                          href={upgradePrUrl}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="inline-flex items-center gap-0.5 font-medium text-teal-600 hover:underline dark:text-teal-400"
                        >
                          View PR <ExternalLink className="h-3 w-3" />
                        </a>
                      </>
                    )}
                  </span>
                </div>
                {/* Step 2: waiting for merge */}
                <div className="flex items-center gap-2">
                  <CheckCircle className="h-4 w-4 text-green-500" />
                  <span className="text-sm text-green-700 dark:text-green-400">Upgrade initiated!</span>
                </div>
              </div>
              <div className="mt-2 flex items-start gap-1.5">
                <p className="text-xs text-green-600 dark:text-green-500">
                  The addon will update once the PR merges and ArgoCD syncs. Track merge status in the Pending Upgrades panel above.
                </p>
              </div>
            </div>
          </div>
        )
      })()}
      {upgradeError && (
        <div className="mt-4 border-t border-[#c0ddf0] pt-4 dark:border-gray-700">
          <p className="text-sm text-red-600 dark:text-red-400">{upgradeError}</p>
          <button
            type="button"
            onClick={() => {
              window.dispatchEvent(new CustomEvent('open-assistant', {
                detail: `Addon ${addonName} upgrade to ${targetVersion} failed with error: ${upgradeError}. Why did this fail and how do I fix it?`,
              }))
            }}
            className="mt-2 inline-flex items-center gap-2 rounded-lg border border-[#6aade0] bg-[#f0f7ff] px-3 py-1.5 text-xs font-medium text-[#0a2a4a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
          >
            <MessageSquare className="h-3.5 w-3.5" />
            Ask AI
          </button>
        </div>
      )}
    </div>
  )
}

function getRisk(result: UpgradeCheckResponse): 'safe' | 'minor' | 'conflicts' {
  if ((result.conflicts ?? []).length > 0) return 'conflicts'
  if (result.total_changes > 0) return 'minor'
  return 'safe'
}

function riskLabel(risk: 'safe' | 'minor' | 'conflicts') {
  if (risk === 'conflicts') return 'Conflicts Found'
  if (risk === 'minor') return 'Minor Changes'
  return 'Safe to Upgrade'
}

function riskIcon(risk: 'safe' | 'minor' | 'conflicts') {
  if (risk === 'conflicts')
    return <AlertTriangle className="h-5 w-5 text-red-500" />
  if (risk === 'minor')
    return <RefreshCw className="h-5 w-5 text-amber-500" />
  return <CheckCircle className="h-5 w-5 text-green-500" />
}

function HealthProgressBar({ healthy, total }: { healthy: number; total: number }) {
  if (total === 0) return null
  const pct = (healthy / total) * 100
  const barColor =
    pct === 100 ? 'bg-green-500' : pct > 50 ? 'bg-yellow-500' : 'bg-red-500'

  return (
    <div className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:border-gray-700 dark:bg-gray-800">
      <h3 className="mb-2 text-base font-semibold text-[#0a2a4a] dark:text-gray-100">Overall Health</h3>
      <div className="h-3 w-full overflow-hidden rounded-full bg-[#c0ddf0] dark:bg-gray-700">
        <div
          className={`h-full rounded-full transition-all ${barColor}`}
          style={{ width: `${pct}%` }}
        />
      </div>
      <p className="mt-1.5 text-sm text-[#2a5a7a] dark:text-gray-400">
        {healthy} of {total} applications are healthy ({Math.round(pct)}%)
      </p>
    </div>
  )
}

function PerClusterUpgradeRow({
  clusterName,
  deployedVersion,
  catalogVersion,
  isDrifted,
  onUpgrade,
}: {
  clusterName: string
  deployedVersion: string
  catalogVersion: string
  isDrifted: boolean
  onUpgrade: (cluster: string) => Promise<{ pr_url?: string; pull_request_url?: string }>
}) {
  const [state, setState] = useState<'idle' | 'confirm' | 'loading' | 'done'>('idle')
  const [prUrl, setPrUrl] = useState<string | null>(null)
  const [upgradeError, setUpgradeError] = useState<string | null>(null)
  const [downgradeModalOpen, setDowngradeModalOpen] = useState(false)

  const isDowngradingCluster = isDowngrade(deployedVersion, catalogVersion)

  const handleConfirm = async () => {
    setState('loading')
    setUpgradeError(null)
    try {
      const result = await onUpgrade(clusterName)
      const url = result?.pr_url || result?.pull_request_url || null
      setPrUrl(url)
      setState('done')
    } catch (err) {
      setUpgradeError(err instanceof Error ? err.message : 'Upgrade failed')
      setState('idle')
    }
  }

  return (
    <div className="rounded-lg bg-[#e0f0ff] px-4 py-2.5">
      {/* Downgrade typed-confirmation modal */}
      <ConfirmationModal
        open={downgradeModalOpen}
        onClose={() => setDowngradeModalOpen(false)}
        onConfirm={handleConfirm}
        title={`Downgrade ${clusterName} to ${catalogVersion}?`}
        description={`This is a downgrade from ${deployedVersion}. Helm values may not be backward-compatible.`}
        confirmText="Confirm Downgrade"
        typeToConfirm="DOWNGRADE"
        destructive
        loading={state === 'loading'}
      />

      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <Link
            to={`/clusters/${clusterName}`}
            className="text-sm font-medium text-[#0a6aaa] hover:underline"
          >
            {clusterName}
          </Link>
          <span className="font-mono text-sm text-[#1a4a6a]">{deployedVersion}</span>
          {isDowngradingCluster && isDrifted && (
            <span className="flex items-center gap-0.5 rounded-full bg-amber-100 px-2 py-0.5 text-[10px] font-semibold text-amber-700 dark:bg-amber-900/30 dark:text-amber-400">
              <AlertTriangle className="h-3 w-3" />
              Downgrade
            </span>
          )}
        </div>
        {isDrifted ? (
          state === 'done' ? (
            <div className="flex items-center gap-1.5 text-xs text-green-600 dark:text-green-400">
              <CheckCircle className="h-3.5 w-3.5" />
              {isDowngradingCluster ? 'Downgrade' : 'Upgrade'} initiated
              {prUrl && (
                <a
                  href={prUrl}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="ml-1 inline-flex items-center gap-1 text-teal-600 hover:underline dark:text-teal-400"
                >
                  <ExternalLink className="h-3 w-3" />
                  PR
                </a>
              )}
            </div>
          ) : state === 'loading' ? (
            <span className="flex items-center gap-1.5 text-xs text-[#2a5a7a]">
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
              {isDowngradingCluster ? 'Downgrading...' : 'Upgrading...'}
            </span>
          ) : (
            <RoleGuard adminOnly>
              <button
                type="button"
                onClick={() => {
                  if (isDowngradingCluster) {
                    setDowngradeModalOpen(true)
                  } else {
                    setState('confirm')
                  }
                }}
                className="rounded-lg bg-[#0a2a4a] px-3 py-1.5 text-xs font-medium text-white hover:bg-[#14466e]"
              >
                {isDowngradingCluster ? 'Downgrade' : 'Upgrade'} to {catalogVersion}
              </button>
            </RoleGuard>
          )
        ) : (
          <span className="text-xs text-green-600">✓ Current</span>
        )}
      </div>
      {state === 'confirm' && !isDowngradingCluster && (
        <div className="mt-2 flex items-center gap-2 rounded-md bg-amber-50 px-3 py-1.5 dark:bg-amber-950/40">
          <AlertTriangle className="h-3.5 w-3.5 shrink-0 text-amber-500" />
          <span className="text-xs text-amber-800 dark:text-amber-300">
            Upgrade {clusterName} to {catalogVersion}?
          </span>
          <button
            type="button"
            onClick={handleConfirm}
            className="ml-1 rounded bg-teal-600 px-2.5 py-1 text-xs font-medium text-white hover:bg-teal-700"
          >
            Yes
          </button>
          <button
            type="button"
            onClick={() => setState('idle')}
            className="rounded px-2.5 py-1 text-xs font-medium text-[#3a6a8a] hover:bg-[#d6eeff] dark:text-gray-400 dark:hover:bg-gray-700"
          >
            Cancel
          </button>
        </div>
      )}
      {upgradeError && (
        <p className="mt-1 text-xs text-red-600 dark:text-red-400">{upgradeError}</p>
      )}
    </div>
  )
}

export function AddonDetail() {
  const { name } = useParams<{ name: string }>()
  const navigate = useNavigate()
  const [addon, setAddon] = useState<AddonCatalogItem | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [searchParams, setSearchParams] = useSearchParams()
  const activeSection = searchParams.get('section') || 'overview'
  const contentPanelRef = useRef<HTMLDivElement>(null)
  const setActiveSection = (s: string) => {
    setSearchParams({ section: s }, { replace: true })
    // Scroll the content panel into view so the user sees the section change
    setTimeout(() => {
      contentPanelRef.current?.scrollIntoView({ behavior: 'smooth', block: 'nearest' })
    }, 0)
  }

  const [valuesYaml, setValuesYaml] = useState<string | null>(null)
  const [argocdBaseURL, setArgocdBaseURL] = useState<string>('')

  // Values editor (v1.20) — schema + the user's per-user PAT signal for the
  // proactive AttributionNudge. We fetch lazily when the user opens the tab.
  const [valuesSchema, setValuesSchema] = useState<AddonValuesSchemaResponse | null>(null)
  const [valuesSchemaLoading, setValuesSchemaLoading] = useState(false)
  const [me, setMe] = useState<MeResponse | null>(null)
  const [gitRepoBase, setGitRepoBase] = useState<string>('')
  const [gitDefaultBranch, setGitDefaultBranch] = useState<string>('main')

  // V123-1.7: catalog-entry lookup so we can render a Source badge/section
  // in the Overview. AddonCatalogItem (deployed) has no source field; we
  // fetch the matching CatalogEntry (curated catalog) by name. Missing
  // entry (e.g. addon added via Paste URL before the catalog was loaded)
  // is non-fatal — we just render nothing.
  const [catalogEntry, setCatalogEntry] = useState<CatalogEntry | null>(null)
  const [catalogSources, setCatalogSources] = useState<CatalogSourceRecord[]>([])

  // V121-7.4: AI configuration. Pulled once for the Values + Catalog tabs
  // to render the "AI not configured" banner and the per-addon opt-out
  // toggle. We don't refresh — Settings changes are infrequent and the
  // user can soft-refresh to pick up changes.
  const [aiEnabled, setAIEnabled] = useState<boolean>(false)
  useEffect(() => {
    // Defensive — older test fixtures may not mock getAIConfig.
    if (typeof api.getAIConfig !== 'function') return
    api.getAIConfig()
      .then((cfg) => setAIEnabled(!!cfg.current_provider && cfg.current_provider !== 'none' && cfg.current_provider !== ''))
      .catch(() => setAIEnabled(false))
  }, [])

  // V123-1.7: look up the matching catalog entry so we can show source
  // attribution in the Overview. Failure is non-fatal — older test
  // fixtures may not mock getCuratedCatalogEntry / listCatalogSources.
  useEffect(() => {
    if (!name) return
    if (typeof api.getCuratedCatalogEntry !== 'function') return
    let cancelled = false
    api.getCuratedCatalogEntry(name)
      .then((e) => {
        if (!cancelled) setCatalogEntry(e)
      })
      .catch(() => {
        if (!cancelled) setCatalogEntry(null)
      })
    return () => {
      cancelled = true
    }
  }, [name])

  useEffect(() => {
    if (typeof api.listCatalogSources !== 'function') return
    let cancelled = false
    api.listCatalogSources()
      .then((resp) => {
        if (!cancelled) setCatalogSources(resp ?? [])
      })
      .catch(() => {
        if (!cancelled) setCatalogSources([])
      })
    return () => {
      cancelled = true
    }
  }, [])

  const catalogSourceRecord = useMemo<CatalogSourceRecord | undefined>(() => {
    if (!catalogEntry) return undefined
    const key = catalogEntry.source ?? 'embedded'
    return catalogSources.find((s) => s.url === key)
  }, [catalogSources, catalogEntry])

  // Filter state
  const [search, setSearch] = useState('')
  const [envFilter, setEnvFilter] = useState('all')
  const [statusFilter, setStatusFilter] = useState('all')
  const [healthFilter, setHealthFilter] = useState('all')

  // Remove addon
  const [removeModalOpen, setRemoveModalOpen] = useState(false)
  const [removing, setRemoving] = useState(false)
  const [removeError, setRemoveError] = useState<string | null>(null)

  // Advanced config editing
  const [isEditingConfig, setIsEditingConfig] = useState(false)
  const [editSyncWave, setEditSyncWave] = useState<number>(0)
  const [editSelfHeal, setEditSelfHeal] = useState<boolean>(true)
  const [editSyncOptionsText, setEditSyncOptionsText] = useState<string>('')
  const [editHelmValues, setEditHelmValues] = useState<{ key: string; value: string }[]>([])
  const [editIgnoreDifferencesYaml, setEditIgnoreDifferencesYaml] = useState<string>('')
  const [editAdditionalSourcesYaml, setEditAdditionalSourcesYaml] = useState<string>('')
  const [configSaving, setConfigSaving] = useState(false)
  const [configError, setConfigError] = useState<string | null>(null)
  const [configSuccess, setConfigSuccess] = useState<string | null>(null)

  // Inline upgrade analysis
  const [inlineAnalysisVersion, setInlineAnalysisVersion] = useState<string | null>(null)
  const [inlineAnalysisResult, setInlineAnalysisResult] = useState<UpgradeCheckResponse | null>(null)
  const [inlineAnalyzing, setInlineAnalyzing] = useState(false)
  const [inlineAnalyzeError, setInlineAnalyzeError] = useState<string | null>(null)
  const inlineResultRef = useRef<HTMLDivElement>(null)

  // Tracked PRs for this addon
  const [addonPRs, setAddonPRs] = useState<TrackedPR[]>([])
  const [isRefreshing, setIsRefreshing] = useState(false)

  const fetchAddonData = useCallback(async (background = false) => {
    if (!name) return
    if (background) {
      setIsRefreshing(true)
    } else {
      setLoading(true)
    }
    try {
      const res = await api.getAddonDetail(name)
      setAddon(res.addon)
    } catch (e: unknown) {
      if (!background) {
        setError(e instanceof Error ? e.message : 'Failed to load addon details')
      }
    } finally {
      setLoading(false)
      setIsRefreshing(false)
    }
  }, [name])

  const handleRefresh = useCallback(() => {
    void fetchAddonData(true)
  }, [fetchAddonData])

  useEffect(() => {
    if (!name) return
    void fetchAddonData()

    api
      .getAddonValues(name)
      .then((res) => setValuesYaml(res.values_yaml))
      .catch(() => {
        // Values file may not exist for all addons — that's OK
      })

    api
      .getConnections()
      .then((res: ConnectionsListResponse) => {
        const active = res.connections.find(c => c.name === res.active_connection || c.is_active)
        if (active?.argocd_server_url) {
          setArgocdBaseURL(active.argocd_server_url.replace(/\/$/, ''))
        }
        // Build the GitHub deep-link base for "Edit in GitHub". Only GitHub is
        // supported for the deep link today — Azure DevOps URLs use a
        // different layout and are skipped (the editor still works fine).
        if (active?.git_provider === 'github' && active.git_repo_identifier) {
          setGitRepoBase(`https://github.com/${active.git_repo_identifier}`)
        }
        if (active?.gitops?.base_branch) {
          setGitDefaultBranch(active.gitops.base_branch)
        }
      })
      .catch(() => {})

    // Fetch /users/me once for the proactive attribution nudge in the editor.
    api
      .getMe()
      .then(setMe)
      .catch(() => setMe(null))
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [name])

  // Lazy-load schema when the user clicks into the Values tab.
  useEffect(() => {
    if (!name) return
    if (activeSection !== 'values') return
    if (valuesSchema || valuesSchemaLoading) return
    setValuesSchemaLoading(true)
    api
      .getAddonValuesSchema(name)
      .then((res) => {
        setValuesSchema(res)
        // Sync the read-only viewer's copy too — saves a round trip.
        if (typeof res.current_values === 'string') {
          setValuesYaml(res.current_values)
        }
      })
      .catch(() => {
        // Schema fetch is best-effort; the editor falls back to whatever
        // valuesYaml we already have.
        setValuesSchema({ addon_name: name, current_values: valuesYaml ?? '', schema: null })
      })
      .finally(() => setValuesSchemaLoading(false))
  }, [name, activeSection, valuesSchema, valuesSchemaLoading, valuesYaml])

  // Auto-refresh every 30s
  useEffect(() => {
    const interval = setInterval(() => {
      void fetchAddonData(true)
    }, 30_000)
    return () => clearInterval(interval)
  }, [fetchAddonData])

  // Fetch tracked PRs for this addon and auto-refresh while any are open
  useEffect(() => {
    if (!name) return
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | null = null

    const load = () => {
      getAddonPRs(name)
        .then((res) => {
          if (cancelled) return
          setAddonPRs(res.prs ?? [])
          const hasOpen = (res.prs ?? []).some((pr) => pr.last_status === 'open')
          if (hasOpen) {
            timer = setTimeout(load, 15000)
          }
        })
        .catch(() => {})
    }

    load()
    return () => {
      cancelled = true
      if (timer) clearTimeout(timer)
    }
  }, [name])

  const handleRemoveAddon = useCallback(async () => {
    if (!name) return
    setRemoving(true)
    setRemoveError(null)
    try {
      await removeAddon(name)
      navigate('/addons')
    } catch (e: unknown) {
      setRemoveError(e instanceof Error ? e.message : 'Failed to remove addon')
      setRemoving(false)
    }
  }, [name, navigate])

  const handleUpgradeAddon = useCallback(async (version: string, cluster?: string) => {
    if (!name) throw new Error('No addon name')
    // Bug #3: block upgrading to the current catalog version
    if (addon && version === addon.version && !cluster) {
      throw new Error('Already on this version — nothing to upgrade.')
    }
    return upgradeAddon(name, { version, cluster })
  }, [name, addon])

  const handleStartEditConfig = useCallback(() => {
    if (!addon) return
    setEditSyncWave(addon.syncWave ?? 0)
    setEditSelfHeal(addon.selfHeal !== false)
    setEditSyncOptionsText((addon.syncOptions ?? []).join(', '))
    setEditHelmValues(
      Object.entries(addon.extraHelmValues ?? {}).map(([key, value]) => ({ key, value })),
    )
    setEditIgnoreDifferencesYaml(
      addon.ignoreDifferences && addon.ignoreDifferences.length > 0
        ? yaml.stringify(addon.ignoreDifferences)
        : '',
    )
    setEditAdditionalSourcesYaml(
      addon.additionalSources && addon.additionalSources.length > 0
        ? yaml.stringify(addon.additionalSources)
        : '',
    )
    setConfigError(null)
    setConfigSuccess(null)
    setIsEditingConfig(true)
  }, [addon])

  const handleCancelEditConfig = useCallback(() => {
    setIsEditingConfig(false)
    setConfigError(null)
    setConfigSuccess(null)
  }, [])

  const handleSaveConfig = useCallback(async () => {
    if (!name || !addon) return
    setConfigSaving(true)
    setConfigError(null)
    setConfigSuccess(null)
    try {
      const syncOptions = editSyncOptionsText
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean)

      const extraHelmValues: Record<string, string> = {}
      for (const { key, value } of editHelmValues) {
        if (key.trim()) {
          extraHelmValues[key.trim()] = value
        }
      }

      const payload: {
        sync_wave?: number
        self_heal?: boolean
        sync_options?: string[]
        extra_helm_values?: Record<string, string>
        ignore_differences?: Record<string, unknown>[]
        additional_sources?: Record<string, unknown>[]
      } = {}

      if (editSyncWave !== (addon.syncWave ?? 0)) payload.sync_wave = editSyncWave
      if (editSelfHeal !== (addon.selfHeal !== false)) payload.self_heal = editSelfHeal
      const origOptions = (addon.syncOptions ?? []).join(',')
      if (syncOptions.join(',') !== origOptions) payload.sync_options = syncOptions
      const origHelm = JSON.stringify(addon.extraHelmValues ?? {})
      if (JSON.stringify(extraHelmValues) !== origHelm) payload.extra_helm_values = extraHelmValues

      // Parse YAML fields
      if (editIgnoreDifferencesYaml.trim()) {
        const parsed = yaml.parse(editIgnoreDifferencesYaml)
        const asArray = Array.isArray(parsed) ? parsed : [parsed]
        const origIgnore = JSON.stringify(addon.ignoreDifferences ?? [])
        if (JSON.stringify(asArray) !== origIgnore) payload.ignore_differences = asArray
      } else if (addon.ignoreDifferences && addon.ignoreDifferences.length > 0) {
        payload.ignore_differences = []
      }

      if (editAdditionalSourcesYaml.trim()) {
        const parsed = yaml.parse(editAdditionalSourcesYaml)
        const asArray = Array.isArray(parsed) ? parsed : [parsed]
        const origSources = JSON.stringify(addon.additionalSources ?? [])
        if (JSON.stringify(asArray) !== origSources) payload.additional_sources = asArray
      } else if (addon.additionalSources && addon.additionalSources.length > 0) {
        payload.additional_sources = []
      }

      const result = await configureAddon(name, payload)
      const prUrl = result?.pr_url || result?.pull_request_url
      setConfigSuccess(prUrl ? `Configuration updated. PR: ${prUrl}` : 'Configuration updated successfully.')
      setIsEditingConfig(false)
      // Refresh addon data
      api.getAddonDetail(name).then((res) => setAddon(res.addon)).catch(() => {})
    } catch (e: unknown) {
      setConfigError(e instanceof Error ? e.message : 'Failed to save configuration')
    } finally {
      setConfigSaving(false)
    }
  }, [name, addon, editSyncWave, editSelfHeal, editSyncOptionsText, editHelmValues, editIgnoreDifferencesYaml, editAdditionalSourcesYaml])

  const handleInlineAnalyze = useCallback(async (version: string) => {
    if (!name) return
    // Bug #3: block analyzing the current catalog version
    if (addon && version === addon.version) {
      setInlineAnalysisVersion(version)
      setInlineAnalyzeError('Already on this version — nothing to upgrade.')
      setInlineAnalysisResult(null)
      setInlineAnalyzing(false)
      setTimeout(() => {
        inlineResultRef.current?.scrollIntoView({ behavior: 'smooth', block: 'nearest' })
      }, 0)
      return
    }
    // Bug #2: if re-analyzing the same version, reset to null first so React
    // sees a state change and re-renders the results panel with fresh state
    if (inlineAnalysisVersion === version) {
      setInlineAnalysisVersion(null)
      setInlineAnalysisResult(null)
      // Allow React to flush the null state before re-setting
      await new Promise<void>((resolve) => setTimeout(resolve, 0))
    }
    setInlineAnalysisVersion(version)
    setInlineAnalyzing(true)
    setInlineAnalyzeError(null)
    setInlineAnalysisResult(null)

    // Scroll immediately so the user sees the "Analyzing..." state appear right away
    setTimeout(() => {
      inlineResultRef.current?.scrollIntoView({ behavior: 'smooth', block: 'nearest' })
    }, 0)

    try {
      const data = await api.checkUpgrade(name, version)
      setInlineAnalysisResult({
        ...data,
        added: data.added ?? [],
        removed: data.removed ?? [],
        changed: data.changed ?? [],
        conflicts: data.conflicts ?? [],
      })
    } catch (err) {
      setInlineAnalyzeError(err instanceof Error ? err.message : 'Analysis failed')
    } finally {
      setInlineAnalyzing(false)
    }
  }, [name, addon, inlineAnalysisVersion])

  const handleCloseInlineAnalysis = useCallback(() => {
    setInlineAnalysisVersion(null)
    setInlineAnalysisResult(null)
    setInlineAnalyzing(false)
    setInlineAnalyzeError(null)
  }, [])

  const enabledApps = useMemo(
    () => (addon ? addon.applications.filter((a) => a.enabled) : []),
    [addon],
  )

  const disabledApps = useMemo(
    () => (addon ? addon.applications.filter((a) => !a.enabled) : []),
    [addon],
  )

  const uniqueEnvironments = useMemo(() => {
    const envs = enabledApps
      .map((a) => a.cluster_environment)
      .filter((e): e is string => Boolean(e))
    return [...new Set(envs)].sort()
  }, [enabledApps])

  const uniqueStatuses = useMemo(() => {
    const statuses = enabledApps.map((a) => a.status)
    return [...new Set(statuses)].sort()
  }, [enabledApps])

  const uniqueHealthStatuses = useMemo(() => {
    const healths = enabledApps.map((a) => a.health_status ?? 'Unknown')
    return [...new Set(healths)].sort()
  }, [enabledApps])

  const filteredApps = useMemo(() => {
    let result = enabledApps

    if (search) {
      const q = search.toLowerCase()
      result = result.filter(
        (a) =>
          a.cluster_name.toLowerCase().includes(q) ||
          a.cluster_environment?.toLowerCase().includes(q) ||
          a.application_name?.toLowerCase().includes(q),
      )
    }

    if (envFilter !== 'all') {
      result = result.filter((a) => a.cluster_environment === envFilter)
    }

    if (statusFilter !== 'all') {
      result = result.filter((a) => a.status === statusFilter)
    }

    if (healthFilter !== 'all') {
      if (healthFilter === 'unknown') {
        result = result.filter(
          (a) => !a.health_status || a.health_status.toLowerCase() === 'unknown',
        )
      } else {
        result = result.filter(
          (a) => a.health_status?.toLowerCase() === healthFilter.toLowerCase(),
        )
      }
    }

    return result
  }, [enabledApps, search, envFilter, statusFilter, healthFilter])

  // Compute environment versions from applications
  const envVersions = useMemo(() => {
    if (!addon) return []
    const map = new Map<string, string>()
    for (const app of addon.applications) {
      if (app.enabled && app.cluster_environment) {
        const version = app.deployed_version ?? app.configured_version ?? 'N/A'
        if (!map.has(app.cluster_environment)) {
          map.set(app.cluster_environment, version)
        }
      }
    }
    return Array.from(map.entries())
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([env, version]) => ({ env, version }))
  }, [addon])

  if (loading) {
    return (
      <div className="space-y-4">
        <div className="flex items-center gap-3">
          <button
            type="button"
            onClick={() => navigate('/addons')}
            className="rounded-md p-2 hover:bg-[#d6eeff] dark:hover:bg-gray-700"
          >
            <ArrowLeft className="h-5 w-5 dark:text-gray-300" />
          </button>
          <h2 className="text-2xl font-bold text-[#0a2a4a] dark:text-gray-100">Loading Addon Details...</h2>
        </div>
        <LoadingState message="Loading addon details..." />
      </div>
    )
  }

  if (error) {
    return (
      <div className="space-y-4">
        <div className="flex items-center gap-3">
          <button
            type="button"
            onClick={() => navigate('/addons')}
            className="rounded-md p-2 hover:bg-[#d6eeff] dark:hover:bg-gray-700"
          >
            <ArrowLeft className="h-5 w-5 dark:text-gray-300" />
          </button>
          <h2 className="text-2xl font-bold text-[#0a2a4a] dark:text-gray-100">Addon Details</h2>
        </div>
        <ErrorState message={error} />
      </div>
    )
  }

  if (!addon) {
    return (
      <div className="space-y-4">
        <div className="flex items-center gap-3">
          <button
            type="button"
            onClick={() => navigate('/addons')}
            className="rounded-md p-2 hover:bg-[#d6eeff] dark:hover:bg-gray-700"
          >
            <ArrowLeft className="h-5 w-5 dark:text-gray-300" />
          </button>
          <h2 className="text-2xl font-bold text-[#0a2a4a] dark:text-gray-100">Addon Details</h2>
        </div>
        <p className="text-[#2a5a7a] dark:text-gray-400">Addon not found.</p>
      </div>
    )
  }

  const healthPct =
    addon.enabled_clusters > 0
      ? Math.round((addon.healthy_applications / addon.enabled_clusters) * 100)
      : 0

  const namespace =
    addon.applications.find((a) => a.enabled && a.namespace)?.namespace ??
    addon.namespace ??
    addon.addon_name

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-3">
          <button
            type="button"
            onClick={() => navigate('/addons')}
            className="rounded-md p-2 hover:bg-[#d6eeff] dark:hover:bg-gray-700"
            aria-label="Back to addons"
          >
            <ArrowLeft className="h-5 w-5 dark:text-gray-300" />
          </button>
          <div>
            <h1 className="text-2xl font-bold text-[#0a2a4a] dark:text-gray-100">{addon.addon_name}</h1>
            <p className="text-sm text-[#2a5a7a] dark:text-gray-400">
              {addon.chart} &middot; Namespace: {namespace}
            </p>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <button
            onClick={handleRefresh}
            className="rounded-md p-2 text-[#3a6a8a] hover:bg-[#d6eeff] dark:text-gray-400 dark:hover:bg-gray-700"
            title="Refresh"
          >
            <RefreshCw className={`h-4 w-4 ${isRefreshing ? 'animate-spin' : ''}`} />
          </button>
          <RoleGuard adminOnly>
            <button
              type="button"
              onClick={() => setActiveSection('upgrade')}
              className="inline-flex items-center gap-2 rounded-lg border border-teal-300 bg-[#f0f7ff] px-3 py-2 text-sm font-medium text-teal-700 hover:bg-teal-50 dark:border-teal-700 dark:bg-gray-800 dark:text-teal-400 dark:hover:bg-teal-900/20"
            >
              <ArrowUpCircle className="h-4 w-4" />
              Upgrade
            </button>
            <button
              type="button"
              onClick={() => { setRemoveError(null); setRemoveModalOpen(true) }}
              className="inline-flex items-center gap-2 rounded-lg border border-red-300 bg-[#f0f7ff] px-3 py-2 text-sm font-medium text-red-600 hover:bg-red-50 dark:border-red-700 dark:bg-gray-800 dark:text-red-400 dark:hover:bg-red-900/20"
            >
              <Trash2 className="h-4 w-4" />
              Remove
            </button>
          </RoleGuard>
        </div>
      </div>

      <ConfirmationModal
        open={removeModalOpen}
        onClose={() => setRemoveModalOpen(false)}
        onConfirm={handleRemoveAddon}
        title={`Remove addon "${name}"?`}
        description="This will remove the addon from the catalog. This action creates a pull request and cannot be undone."
        confirmText="Remove"
        typeToConfirm={name}
        destructive
        loading={removing}
      />
      {removeError && (
        <p className="text-sm text-red-600 dark:text-red-400">{removeError}</p>
      )}

      <div className="flex gap-6">
        <DetailNavPanel
          sections={[
            {
              items: [
                { key: 'overview', label: 'Overview', icon: LayoutGrid },
                { key: 'clusters', label: 'Clusters', badge: enabledApps.length, icon: Server },
                { key: 'upgrade', label: 'Upgrade', icon: ArrowUpCircle },
                { key: 'values', label: 'Values', icon: Pencil },
                { key: 'catalog', label: 'ArgoCD App Options', icon: FileCode },
              ],
            },
          ]}
          activeKey={activeSection}
          onSelect={setActiveSection}
        />

        <div ref={contentPanelRef} className="flex-1 space-y-6">
          {activeSection === 'overview' && (
            <>
              {/* Addon info card */}
              <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5 dark:ring-gray-700 dark:bg-gray-800">
                <div className="grid gap-x-8 gap-y-2 sm:grid-cols-2 lg:grid-cols-3">
                  <div>
                    <p className="text-xs font-semibold uppercase tracking-wide text-[#5a8aaa] dark:text-gray-500">Version</p>
                    <p className="mt-0.5 font-mono text-sm font-bold text-[#0a2a4a] dark:text-gray-100">{addon.version}</p>
                  </div>
                  <div>
                    <p className="text-xs font-semibold uppercase tracking-wide text-[#5a8aaa] dark:text-gray-500">Chart</p>
                    <p className="mt-0.5 font-mono text-sm text-[#0a2a4a] dark:text-gray-100">{addon.chart}</p>
                  </div>
                  <div>
                    <p className="text-xs font-semibold uppercase tracking-wide text-[#5a8aaa] dark:text-gray-500">Namespace</p>
                    <p className="mt-0.5 text-sm text-[#0a2a4a] dark:text-gray-100">{namespace}</p>
                  </div>
                  <div>
                    <p className="text-xs font-semibold uppercase tracking-wide text-[#5a8aaa] dark:text-gray-500">Sync Wave</p>
                    <p className="mt-0.5 font-mono text-sm text-[#0a2a4a] dark:text-gray-100">{addon.syncWave ?? 0}</p>
                  </div>
                  <div>
                    <p className="text-xs font-semibold uppercase tracking-wide text-[#5a8aaa] dark:text-gray-500">Self-Heal</p>
                    <p className={`mt-0.5 text-sm font-medium ${addon.selfHeal !== false ? 'text-green-600 dark:text-green-400' : 'text-amber-600 dark:text-amber-400'}`}>
                      {addon.selfHeal !== false ? 'Enabled' : 'Disabled'}
                    </p>
                  </div>
                  {addon.repo_url && (
                    <div>
                      <p className="text-xs font-semibold uppercase tracking-wide text-[#5a8aaa] dark:text-gray-500">Helm Repository</p>
                      <a
                        href={addon.repo_url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="mt-0.5 inline-flex items-center gap-1 text-sm text-[#0a6aaa] hover:underline dark:text-[#6aade0]"
                      >
                        {addon.repo_url.length > 40 ? addon.repo_url.slice(0, 40) + '…' : addon.repo_url}
                        <ExternalLink className="h-3 w-3 shrink-0" />
                      </a>
                    </div>
                  )}
                  {/* V123-1.7 — catalog source attribution. Only shown when
                      we were able to look up a matching curated catalog
                      entry. Source URL is rendered as TEXT (never a
                      clickable link — paths may carry auth tokens). */}
                  {catalogEntry && (
                    <div>
                      <p className="text-xs font-semibold uppercase tracking-wide text-[#5a8aaa] dark:text-gray-500">Source</p>
                      {catalogEntry.source && catalogEntry.source !== 'embedded' ? (
                        <div className="mt-0.5 text-sm">
                          <div className="break-all text-[#0a3a5a] dark:text-[#d6eeff]">
                            {catalogEntry.source}
                          </div>
                          {catalogSourceRecord ? (
                            <div className="mt-0.5 text-xs text-[#2a5a7a] dark:text-[#b4dcf5]">
                              Status: {catalogSourceRecord.status}
                              {catalogSourceRecord.last_fetched
                                ? ` \u00b7 Last fetched ${catalogSourceRecord.last_fetched}`
                                : ''}
                            </div>
                          ) : null}
                        </div>
                      ) : (
                        <div className="mt-0.5">
                          <SourceBadge
                            source={catalogEntry.source}
                            sourceRecord={catalogSourceRecord}
                          />
                        </div>
                      )}
                    </div>
                  )}
                </div>
                {/* Links */}
                <div className="mt-3 flex flex-wrap items-center gap-3 border-t border-[#c0ddf0] pt-3 dark:border-gray-700">
                  <a
                    href={`https://artifacthub.io/packages/helm/${encodeURIComponent(addon.chart)}`}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex items-center gap-1 rounded-md bg-[#e0f0ff] px-3 py-1 text-xs font-medium text-[#0a6aaa] hover:bg-[#d6eeff] dark:bg-gray-700 dark:text-[#6aade0] dark:hover:bg-gray-600"
                  >
                    <ExternalLink className="h-3 w-3" />
                    ArtifactHub
                  </a>
                </div>
              </div>

              {/* Summary stat cards */}
              <div className="grid grid-cols-2 gap-4 lg:grid-cols-5">
                <StatCard
                  title="Active Apps"
                  value={`${addon.enabled_clusters} / ${addon.total_clusters}`}
                  icon={<Activity className="h-5 w-5" />}
                />
                <StatCard
                  title="Healthy"
                  value={`${addon.healthy_applications} (${healthPct}%)`}
                  icon={<CheckCircle className="h-5 w-5" />}
                  color="success"
                />
                <StatCard
                  title="Degraded"
                  value={addon.degraded_applications}
                  icon={<AlertTriangle className="h-5 w-5" />}
                  color={addon.degraded_applications > 0 ? 'warning' : 'default'}
                />
                <StatCard
                  title="Not Deployed"
                  value={addon.missing_applications}
                  icon={<XCircle className="h-5 w-5" />}
                  color={addon.missing_applications > 0 ? 'error' : 'default'}
                />
                <StatCard
                  title="Disabled in Git"
                  value={disabledApps.length}
                  icon={<Ban className="h-5 w-5" />}
                />
              </div>

              {/* Overall health progress bar */}
              <HealthProgressBar
                healthy={addon.healthy_applications}
                total={addon.enabled_clusters}
              />

              {/* Pending Upgrade Banner */}
              {addonPRs.length > 0 && addonPRs.map((pr) => {
                const isOpen = pr.last_status === 'open'
                const isMerged = pr.last_status === 'merged'
                if (!isOpen && !isMerged) return null
                const targetVersion = pr.pr_title.match(/to\s+([\d.v][^\s]*)/i)?.[1] ?? null
                return (
                  <div
                    key={pr.pr_id}
                    className={`flex items-start gap-3 rounded-lg border px-4 py-3 ${
                      isOpen
                        ? 'border-amber-300 bg-amber-50 dark:border-amber-700 dark:bg-amber-950/40'
                        : 'border-green-300 bg-green-50 dark:border-green-700 dark:bg-green-950/40'
                    }`}
                  >
                    <ArrowUpCircle className={`mt-0.5 h-4 w-4 shrink-0 ${isOpen ? 'text-amber-500' : 'text-green-500'}`} />
                    <div className="flex-1 min-w-0">
                      <p className={`text-sm font-semibold ${isOpen ? 'text-amber-800 dark:text-amber-300' : 'text-green-800 dark:text-green-300'}`}>
                        {isOpen ? 'Upgrade in progress' : 'Upgrade merged'}
                        {addon && targetVersion && (
                          <span className="ml-2 font-normal">
                            {addon.version} &rarr; {targetVersion}
                          </span>
                        )}
                      </p>
                      <p className={`mt-0.5 text-xs ${isOpen ? 'text-amber-700 dark:text-amber-400' : 'text-green-700 dark:text-green-400'}`}>
                        PR:{' '}
                        <a
                          href={pr.pr_url}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="underline hover:no-underline"
                        >
                          {pr.pr_title}
                        </a>
                        {' '}
                        &mdash; Status:{' '}
                        <span className="font-medium capitalize">{pr.last_status}</span>
                      </p>
                    </div>
                  </div>
                )
              })}

              {/* AppSet info */}
              <div className="rounded-lg bg-[#e8f4ff] p-3 text-sm text-[#2a5a7a] dark:bg-gray-800 dark:text-gray-400">
                <span className="font-medium text-[#0a2a4a] dark:text-gray-200">ApplicationSet:</span>{' '}
                {addon.addon_name} — manages deployments across all clusters with this addon enabled
              </div>

              {/* Environment Versions */}
              {envVersions.length > 0 && (
                <div className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:border-gray-700 dark:bg-gray-800">
                  <h3 className="mb-3 text-base font-semibold text-[#0a2a4a] dark:text-gray-100">
                    Environment Versions
                  </h3>
                  <div className="space-y-2">
                    {envVersions.map(({ env, version }) => (
                      <div
                        key={env}
                        className="flex items-center justify-between rounded ring-2 ring-[#6aade0] px-3 py-2 dark:border-gray-700"
                      >
                        <span className="rounded-full border border-teal-200 bg-teal-50 px-2 py-0.5 text-xs font-medium text-teal-700 dark:border-teal-600 dark:bg-teal-900/30 dark:text-teal-400">
                          {env}
                        </span>
                        <span className="font-mono text-sm text-[#1a4a6a] dark:text-gray-400">{version}</span>
                      </div>
                    ))}
                  </div>
                </div>
              )}

              {/* Advanced Configuration — moved to the ArgoCD App Options
                  tab in v1.21 QA Bundle 1 to remove the duplicate section.
                  Overview now just points the operator to the canonical
                  home so they don't have to hunt for it. */}
              <div className="rounded-lg bg-[#e8f4ff] p-4 text-sm text-[#2a5a7a] ring-1 ring-[#c0ddf0] dark:bg-gray-800 dark:text-gray-300 dark:ring-gray-700">
                <p className="flex flex-wrap items-center gap-1">
                  <span className="font-medium text-[#0a2a4a] dark:text-gray-100">
                    Advanced ArgoCD Application options
                  </span>
                  <span>(sync wave, sync options, ignore differences, additional sources) live in the</span>
                  <button
                    type="button"
                    onClick={() => setActiveSection('catalog')}
                    className="font-semibold text-teal-600 underline hover:no-underline dark:text-teal-400"
                  >
                    ArgoCD App Options tab &rarr;
                  </button>
                </p>
              </div>

              {/* The full Advanced Configuration form (sync wave, self-heal,
                  sync options, ignore differences, extra Helm values,
                  additional sources) lived here through v1.20. It was
                  removed in v1.21 QA Bundle 1 because the same form lives
                  in the ArgoCD App Options tab — the duplicate confused
                  testers. The pointer card above takes them there. */}
            </>
          )}

          {activeSection === 'clusters' && (
            <>
              {/* Filter controls */}
              <div className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:border-gray-700 dark:bg-gray-800">
                <h3 className="mb-3 text-base font-semibold text-[#0a2a4a] dark:text-gray-100">
                  Filter Applications
                </h3>
                <div className="flex flex-wrap items-center gap-3">
                  <div className="relative flex-1" style={{ minWidth: 200, maxWidth: 300 }}>
                    <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[#3a6a8a]" />
                    <input
                      type="text"
                      placeholder="Search clusters, environments, or apps..."
                      value={search}
                      onChange={(e) => setSearch(e.target.value)}
                      className="w-full rounded-lg border border-[#5a9dd0] py-2 pl-10 pr-4 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-900 dark:text-gray-200 dark:placeholder-[#5a8aaa]"
                    />
                  </div>

                  <select
                    value={envFilter}
                    onChange={(e) => setEnvFilter(e.target.value)}
                    className="rounded-lg border border-[#5a9dd0] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-900 dark:text-gray-200"
                  >
                    <option value="all">All Environments</option>
                    {uniqueEnvironments.map((env) => (
                      <option key={env} value={env}>
                        {env}
                      </option>
                    ))}
                  </select>

                  <select
                    value={statusFilter}
                    onChange={(e) => setStatusFilter(e.target.value)}
                    className="rounded-lg border border-[#5a9dd0] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-900 dark:text-gray-200"
                  >
                    <option value="all">All Status</option>
                    {uniqueStatuses.map((s) => (
                      <option key={s} value={s}>
                        {s}
                      </option>
                    ))}
                  </select>

                  <select
                    value={healthFilter}
                    onChange={(e) => setHealthFilter(e.target.value)}
                    className="rounded-lg border border-[#5a9dd0] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-900 dark:text-gray-200"
                  >
                    <option value="all">All Health</option>
                    {uniqueHealthStatuses.map((h) => (
                      <option key={h} value={h}>
                        {h}
                      </option>
                    ))}
                  </select>
                </div>
                <p className="mt-2 text-xs text-[#2a5a7a] dark:text-gray-400">
                  Showing {filteredApps.length} of {enabledApps.length} applications
                </p>
              </div>

              {/* Cluster Applications Table */}
              <div className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] dark:border-gray-700 dark:bg-gray-800">
                <div className="border-b px-4 py-3 dark:border-gray-700">
                  <h3 className="text-base font-semibold text-[#0a2a4a] dark:text-gray-100">
                    Cluster Applications
                  </h3>
                </div>
                <div className="overflow-x-auto">
                  <table className="w-full text-sm">
                    <thead className="border-b bg-[#d0e8f8] text-xs uppercase text-[#2a5a7a] dark:border-gray-700 dark:bg-gray-900 dark:text-gray-400">
                      <tr>
                        <th className="px-4 py-3 text-left">Cluster</th>
                        <th className="px-4 py-3 text-left">Status</th>
                        <th className="px-4 py-3 text-left">Health</th>
                        <th className="px-4 py-3 text-left">Version</th>
                        <th className="px-4 py-3 text-left">ArgoCD</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-[#6aade0] dark:divide-gray-700">
                      {filteredApps.map((app) => (
                        <tr key={app.cluster_name} className="hover:bg-[#d6eeff] dark:hover:bg-gray-700">
                          <td className="px-4 py-3">
                            <Link
                              to={`/clusters/${app.cluster_name}?section=addons&addon=${encodeURIComponent(addon.addon_name)}`}
                              title={`Jump to ${addon.addon_name} on ${app.cluster_name}`}
                              className="font-medium text-teal-600 hover:text-teal-800 hover:underline dark:text-teal-400 dark:hover:text-teal-300"
                            >
                              {app.cluster_name}
                            </Link>
                          </td>
                          <td className="px-4 py-3">
                            <StatusBadge status={app.status} />
                          </td>
                          <td className="px-4 py-3">
                            <StatusBadge
                              status={app.health_status ?? 'Unknown'}
                            />
                          </td>
                          <td className="px-4 py-3">
                            <span className="font-mono text-xs text-[#1a4a6a] dark:text-gray-400">
                              {app.deployed_version ?? app.configured_version ?? 'N/A'}
                            </span>
                            {app.deployed_version &&
                              app.configured_version &&
                              app.deployed_version !== app.configured_version && (
                                <span className="ml-1 text-xs text-yellow-600 dark:text-yellow-400">
                                  (configured: {app.configured_version})
                                </span>
                              )}
                          </td>
                          <td className="px-4 py-3">
                            {app.application_name && argocdBaseURL ? (
                              <a
                                href={`${argocdBaseURL}/applications/${app.application_name}`}
                                target="_blank"
                                rel="noopener noreferrer"
                                title={`Open ${app.application_name} in ArgoCD`}
                                className="text-[#2a5a7a] hover:text-teal-600 dark:text-gray-400 dark:hover:text-teal-400"
                              >
                                <ExternalLink className="h-4 w-4" />
                              </a>
                            ) : (
                              <span className="text-xs text-[#3a6a8a]">N/A</span>
                            )}
                          </td>
                        </tr>
                      ))}
                      {filteredApps.length === 0 && (
                        <tr>
                          <td
                            colSpan={5}
                            className="px-4 py-8 text-center text-[#3a6a8a] dark:text-gray-500"
                          >
                            {enabledApps.length === 0
                              ? 'This addon is not currently deployed on any clusters.'
                              : 'No applications match the current filters.'}
                          </td>
                        </tr>
                      )}
                    </tbody>
                  </table>
                </div>
              </div>

              {/* Disabled clusters section */}
              {disabledApps.length > 0 && (
                <div className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:border-gray-700 dark:bg-gray-800" id="disabled-clusters-section">
                  <h3 className="mb-3 text-base font-semibold text-[#0a2a4a] dark:text-gray-100">
                    Disabled on {disabledApps.length} Clusters
                  </h3>
                  <div className="flex flex-wrap gap-2">
                    {disabledApps.map((app) => (
                      <Link
                        key={app.cluster_name}
                        to={`/clusters/${app.cluster_name}?section=addons&addon=${encodeURIComponent(addon.addon_name)}`}
                        title={`Jump to ${addon.addon_name} on ${app.cluster_name}`}
                        className="inline-flex items-center gap-1.5 rounded-full ring-2 ring-[#6aade0] bg-[#d0e8f8] px-3 py-1 text-xs font-medium text-[#1a4a6a] transition-colors hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
                      >
                        <Ban className="h-3 w-3" />
                        {app.cluster_name}
                      </Link>
                    ))}
                  </div>
                </div>
              )}
            </>
          )}

          {activeSection === 'upgrade' && (
            <div className="space-y-6">
              {/* Current version */}
              <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5">
                <h3 className="text-base font-semibold text-[#0a2a4a]">Current Catalog Version</h3>
                <p className="mt-1 font-mono text-lg font-bold text-[#0a2a4a]">{addon.version}</p>
                <p className="mt-1 text-sm text-[#2a5a7a]">Chart: {addon.chart}</p>
              </div>

              {/* Smart upgrade recommendations */}
              <RecommendedVersions
                addonName={addon.addon_name}
                onAnalyze={handleInlineAnalyze}
              />

              {/* Available versions */}
              <UpgradeVersionList
                addonName={addon.addon_name}
                currentVersion={addon.version}
                onAnalyze={handleInlineAnalyze}
              />

              {/* Inline upgrade analysis results */}
              {(inlineAnalysisVersion !== null) && (
                <div ref={inlineResultRef}>
                  <InlineUpgradeResults
                    addonName={addon.addon_name}
                    targetVersion={inlineAnalysisVersion}
                    currentVersion={addon.version}
                    result={inlineAnalysisResult}
                    analyzing={inlineAnalyzing}
                    analyzeError={inlineAnalyzeError}
                    onRetry={() => handleInlineAnalyze(inlineAnalysisVersion)}
                    onClose={handleCloseInlineAnalysis}
                    onUpgrade={handleUpgradeAddon}
                    onUpgradeComplete={() => fetchAddonData(true)}
                  />
                </div>
              )}

              {/* Per-cluster versions */}
              <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5">
                <h3 className="mb-3 text-base font-semibold text-[#0a2a4a]">Per-Cluster Versions</h3>
                {enabledApps.length === 0 ? (
                  <p className="text-sm text-[#2a5a7a]">No clusters have this addon enabled.</p>
                ) : (
                  <div className="space-y-2">
                    {enabledApps.map((app) => {
                      const deployedVersion =
                        app.deployed_version ?? app.configured_version ?? 'N/A'
                      const isDrifted =
                        deployedVersion !== addon.version && deployedVersion !== 'N/A'
                      return (
                        <PerClusterUpgradeRow
                          key={app.cluster_name}
                          clusterName={app.cluster_name}
                          deployedVersion={deployedVersion}
                          catalogVersion={addon.version}
                          isDrifted={isDrifted}
                          onUpgrade={(cluster) => handleUpgradeAddon(addon.version, cluster)}
                        />
                      )
                    })}
                  </div>
                )}
              </div>
            </div>
          )}

          {activeSection === 'values' && (
            <>
              <p className="text-xs italic text-[#3a6a8a] dark:text-gray-400">
                Looking for sync wave, sync options, or ignore-differences?{' '}
                <button
                  type="button"
                  onClick={() => setActiveSection('catalog')}
                  className="font-semibold text-teal-600 underline hover:no-underline dark:text-teal-400"
                >
                  ArgoCD App Options tab →
                </button>
              </p>
              {valuesSchemaLoading && !valuesSchema ? (
                <LoadingState message="Loading values..." />
              ) : (
                <ValuesEditor
                  title={`Global Values — ${addon.addon_name}`}
                  subtitle="Submitting opens a PR against your GitOps repo. Once merged, ArgoCD reconciles the change to every cluster that has this addon enabled."
                  initialYAML={valuesSchema?.current_values ?? valuesYaml ?? ''}
                  schema={valuesSchema?.schema ?? null}
                  hasPersonalToken={me?.has_github_token}
                  githubFileURL={
                    gitRepoBase
                      ? `${gitRepoBase}/blob/${gitDefaultBranch}/configuration/addons-global-values/${addon.addon_name}.yaml`
                      : undefined
                  }
                  onSubmit={async (newYAML) => {
                    const result = await api.setAddonValues(addon.addon_name, newYAML)
                    // Refresh the local copy so subsequent edits diff against
                    // the latest content.
                    setValuesSchema((prev) =>
                      prev ? { ...prev, current_values: newYAML } : prev,
                    )
                    setValuesYaml(newYAML)
                    return result
                  }}
                  versionMismatch={
                    valuesSchema?.values_version_mismatch
                      ? {
                          catalogVersion: valuesSchema.values_version_mismatch.catalog_version,
                          valuesVersion: valuesSchema.values_version_mismatch.values_version,
                        }
                      : null
                  }
                  onRefreshFromUpstream={async () => {
                    // V121-6.4: same endpoint as setAddonValues, with
                    // refresh_from_upstream: true. Backend regenerates the
                    // values file via the smart-values pipeline.
                    const result = await api.refreshAddonValuesFromUpstream(addon.addon_name)
                    try {
                      const fresh = await api.getAddonValuesSchema(addon.addon_name)
                      setValuesSchema(fresh)
                      setValuesYaml(fresh.current_values)
                    } catch {
                      // no-op — the PR link in the toast is enough
                    }
                    return result
                  }}
                  // v1.21 QA Bundle 4 Fix #4: additive merge. The editor
                  // owns the modal and the apply-through-onSubmit path;
                  // we just plug in the preview API call.
                  onPreviewMerge={() => api.previewMergeAddonValues(addon.addon_name)}
                  // v1.21 Bundle 5: legacy `<addon>:` wrap migration. The
                  // backend's values-schema endpoint flags wrapped files;
                  // the editor renders a yellow banner with a "Migrate
                  // this file" action that opens a Tier 2 PR.
                  legacyWrapDetected={!!valuesSchema?.legacy_wrap_detected}
                  onMigrateLegacyWrap={async () => {
                    try {
                      const res = await api.unwrapGlobalValues(addon.addon_name)
                      if (res.pr_url || res.pr_id) {
                        const label = res.pr_id ? `PR #${res.pr_id}` : 'PR'
                        showToast(res.merged ? `${label} merged →` : `${label} opened →`, 'success')
                      } else if (res.message) {
                        showToast(res.message, 'info')
                      }
                      // Re-fetch so the editor reflects the migrated body
                      // and the banner clears.
                      const fresh = await api.getAddonValuesSchema(addon.addon_name)
                      setValuesSchema(fresh)
                      setValuesYaml(fresh.current_values)
                    } catch (err) {
                      const msg = err instanceof Error ? err.message : 'Failed to migrate values file'
                      showToast(`Migration failed: ${msg}`, 'info')
                    }
                  }}
                  belowEditor={({ refreshKey }) => (
                    <RecentPRsPanel
                      title="Recent changes (last 5)"
                      refreshKey={refreshKey}
                      load={() => api.getAddonValuesRecentPRs(addon.addon_name, 5)}
                    />
                  )}
                  // V121-7.4: AI banner / annotate-now wiring.
                  // - "AI not configured" banner: when the file's header
                  //   says annotation is disabled AND the global AI
                  //   provider is `none`.
                  // - "Opted out" note: when the file's header carries
                  //   the per-addon opt-out directive.
                  // - Annotate-now button: only when AI is configured AND
                  //   the addon is not opted out (clearing opt-out is a
                  //   separate Catalog-tab action, deliberately).
                  showAINotConfiguredBanner={
                    !aiEnabled && valuesSchema?.ai_annotated === false && !valuesSchema?.ai_opt_out
                  }
                  showAIOptedOutNote={!!valuesSchema?.ai_opt_out}
                  onAnnotateNow={
                    aiEnabled && !valuesSchema?.ai_opt_out
                      ? async () => {
                          try {
                            const res = await api.annotateAddonValues(addon.addon_name)
                            if (res.pr_url || res.pr_id) {
                              const label = res.pr_id ? `PR #${res.pr_id}` : 'PR'
                              showToast(res.merged ? `${label} merged →` : `${label} opened →`, 'success')
                            } else if (res.ai_skip_reason) {
                              showToast(`AI annotate skipped: ${res.ai_skip_reason}`, 'info')
                            } else {
                              showToast('AI annotate completed', 'success')
                            }
                            // Re-fetch so the editor body reflects the new commit.
                            const fresh = await api.getAddonValuesSchema(addon.addon_name)
                            setValuesSchema(fresh)
                            setValuesYaml(fresh.current_values)
                          } catch (err) {
                            // Backend may return 422 with the SecretLeak
                            // payload. The fetch wrapper carries the
                            // parsed body via `(err as { body }).body`
                            // when present — see api.ts.
                            const body = (err as { body?: { code?: string; message?: string; matches?: { pattern: string }[] } }).body
                            if (body?.code === 'secret_detected_blocked') {
                              const patterns = body.matches?.map((m) => m.pattern).join(', ') || 'unknown'
                              showToast(`AI annotate blocked: secret-like content detected (${patterns}). No PR opened.`, 'info')
                            } else {
                              const msg = err instanceof Error ? err.message : 'AI annotate failed'
                              showToast(`AI annotate failed: ${msg}`, 'info')
                            }
                          }
                        }
                      : undefined
                  }
                />
              )}
            </>
          )}

          {activeSection === 'catalog' && (
            <div className="space-y-4">
              <div className="rounded-lg bg-[#e0f0ff] p-3 text-xs text-[#0a3a5a] dark:bg-gray-700 dark:text-gray-300">
                <p>
                  ArgoCD Application options for <span className="font-mono">{addon.addon_name}</span> —
                  sync wave, sync options, ignore differences, and additional sources. These control how
                  ArgoCD deploys the addon. Editing opens a PR against{' '}
                  <span className="font-mono">addons-catalog.yaml</span>. Looking for Helm values?{' '}
                  <button
                    type="button"
                    onClick={() => setActiveSection('values')}
                    className="font-semibold text-teal-600 underline hover:no-underline dark:text-teal-400"
                  >
                    Values tab →
                  </button>
                </p>
              </div>

              <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5 dark:ring-gray-700 dark:bg-gray-800">
                <div className="mb-4 flex items-center justify-between">
                  <h3 className="text-base font-semibold text-[#0a2a4a] dark:text-gray-100">
                    ArgoCD Application Options
                  </h3>
                  {!isEditingConfig && (
                    <RoleGuard adminOnly>
                      <button
                        type="button"
                        onClick={handleStartEditConfig}
                        className="inline-flex items-center gap-1.5 rounded-lg border border-[#5a9dd0] bg-white px-3 py-1.5 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
                      >
                        <Pencil className="h-3 w-3" />
                        Edit
                      </button>
                    </RoleGuard>
                  )}
                </div>

                {/* Proactive attribution nudge for Tier 2 editors without a PAT */}
                {isEditingConfig && me?.has_github_token === false && (
                  <div className="mb-4">
                    <AttributionNudge inline />
                  </div>
                )}

                {/* Success / error banners */}
                {configSuccess && (
                  <div className="mb-4 rounded-lg bg-green-50 px-4 py-3 text-sm text-green-700 ring-1 ring-green-200 dark:bg-green-900/20 dark:text-green-400">
                    {configSuccess}
                  </div>
                )}
                {configError && (
                  <div className="mb-4 rounded-lg bg-red-50 px-4 py-3 text-sm text-red-600 ring-1 ring-red-200 dark:bg-red-900/20 dark:text-red-400">
                    {configError}
                  </div>
                )}

                <div className="space-y-4">
                  {/* Sync Wave */}
                  <div className="flex items-center justify-between gap-4">
                    <div>
                      <p className="text-sm font-medium text-[#0a2a4a] dark:text-gray-100">
                        Sync Wave
                      </p>
                      <p className="text-xs text-[#3a6a8a] dark:text-gray-400">
                        Deploy order — lower numbers first. Use <span className="font-mono text-[#1a4a6a] dark:text-gray-300">-1</span> for CRDs, <span className="font-mono text-[#1a4a6a] dark:text-gray-300">0</span> for the default wave.
                      </p>
                    </div>
                    {isEditingConfig ? (
                      <input
                        type="number"
                        value={editSyncWave}
                        onChange={(e) => setEditSyncWave(Number(e.target.value))}
                        placeholder="e.g. 0 or -1"
                        className="w-32 rounded-md border border-[#5a9dd0] bg-white px-3 py-1.5 text-right text-sm font-mono text-[#0a2a4a] focus:border-[#1a6aaa] focus:outline-none focus:ring-1 focus:ring-[#1a6aaa] dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100"
                      />
                    ) : (
                      <span className="font-mono text-sm text-[#0a2a4a] dark:text-gray-100">{addon.syncWave ?? 0}</span>
                    )}
                  </div>

                  {/* Self-Heal */}
                  <div className="flex items-center justify-between gap-4">
                    <div>
                      <p className="text-sm font-medium text-[#0a2a4a] dark:text-gray-100">
                        Self-Heal
                      </p>
                      <p className="text-xs text-[#3a6a8a] dark:text-gray-400">ArgoCD reverts manual drift back to the Git state when enabled.</p>
                    </div>
                    {isEditingConfig ? (
                      <label className="flex cursor-pointer items-center gap-2">
                        <span className="text-xs text-[#2a5a7a] dark:text-gray-400">{editSelfHeal ? 'Enabled' : 'Disabled'}</span>
                        <button
                          type="button"
                          role="switch"
                          aria-checked={editSelfHeal}
                          onClick={() => setEditSelfHeal((v) => !v)}
                          className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors focus:outline-none ${
                            editSelfHeal ? 'bg-[#1a6aaa]' : 'bg-[#c0ddf0] dark:bg-gray-600'
                          }`}
                        >
                          <span
                            className={`inline-block h-3.5 w-3.5 rounded-full bg-white shadow transition-transform ${
                              editSelfHeal ? 'translate-x-4' : 'translate-x-1'
                            }`}
                          />
                        </button>
                      </label>
                    ) : (
                      <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${
                        addon.selfHeal === false
                          ? 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400'
                          : 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400'
                      }`}>
                        {addon.selfHeal === false ? 'Disabled' : 'Enabled'}
                      </span>
                    )}
                  </div>

                  {/* Sync Options */}
                  <div>
                    <p className="text-sm font-medium text-[#0a2a4a] dark:text-gray-100">
                      Sync Options
                    </p>
                    <p className="mb-2 text-xs text-[#3a6a8a] dark:text-gray-400">
                      ArgoCD sync options, comma-separated. Example:{' '}
                      <span className="font-mono text-[#1a4a6a] dark:text-gray-300">CreateNamespace=true, ServerSideApply=true, PruneLast=true</span>.
                    </p>
                    {isEditingConfig ? (
                      <textarea
                        value={editSyncOptionsText}
                        onChange={(e) => setEditSyncOptionsText(e.target.value)}
                        placeholder="e.g. CreateNamespace=true, ServerSideApply=true"
                        rows={2}
                        className="w-full rounded-md border border-[#5a9dd0] bg-white px-3 py-2 text-sm font-mono text-[#0a2a4a] focus:border-[#1a6aaa] focus:outline-none focus:ring-1 focus:ring-[#1a6aaa] dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100"
                      />
                    ) : addon.syncOptions && addon.syncOptions.length > 0 ? (
                      <div className="flex flex-wrap gap-1">
                        {addon.syncOptions.map((opt: string) => (
                          <span key={opt} className="rounded bg-[#d6eeff] px-2 py-0.5 text-xs font-mono text-[#0a2a4a] dark:bg-gray-700 dark:text-gray-300">{opt}</span>
                        ))}
                      </div>
                    ) : (
                      <p className="text-xs text-[#5a8aaa] dark:text-gray-500">Default (CreateNamespace=true)</p>
                    )}
                  </div>

                  {/* Ignore Differences */}
                  <div>
                    <p className="text-sm font-medium text-[#0a2a4a] dark:text-gray-100">
                      Ignore Differences
                    </p>
                    <p className="mb-2 text-xs text-[#3a6a8a] dark:text-gray-400">
                      Fields ArgoCD ignores during diff — typically Helm-injected fields like autoscaler-managed replicas.
                    </p>
                    {isEditingConfig ? (
                      <textarea
                        value={editIgnoreDifferencesYaml}
                        onChange={(e) => setEditIgnoreDifferencesYaml(e.target.value)}
                        placeholder={`# Example:\n# - group: apps\n#   kind: Deployment\n#   jsonPointers:\n#     - /spec/replicas`}
                        rows={6}
                        className="w-full rounded-md border border-[#5a9dd0] bg-white px-3 py-2 text-sm font-mono text-[#0a2a4a] focus:border-[#1a6aaa] focus:outline-none focus:ring-1 focus:ring-[#1a6aaa] dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100"
                      />
                    ) : addon.ignoreDifferences && addon.ignoreDifferences.length > 0 ? (
                      <pre className="rounded bg-[#071828] p-3 text-xs text-[#bee0ff] overflow-auto">
                        {JSON.stringify(addon.ignoreDifferences, null, 2)}
                      </pre>
                    ) : (
                      <p className="text-xs text-[#5a8aaa] dark:text-gray-500">None configured</p>
                    )}
                  </div>

                  {/* Additional Sources */}
                  <div>
                    <p className="text-sm font-medium text-[#0a2a4a] dark:text-gray-100">
                      Additional Sources
                    </p>
                    <p className="mb-2 text-xs text-[#3a6a8a] dark:text-gray-400">Extra chart or manifest sources deployed alongside the main addon (ArgoCD multi-source).</p>
                    {isEditingConfig ? (
                      <textarea
                        value={editAdditionalSourcesYaml}
                        onChange={(e) => setEditAdditionalSourcesYaml(e.target.value)}
                        placeholder={`# Example:\n# - repoURL: https://github.com/org/repo\n#   path: charts/my-chart\n#   version: "1.0.0"`}
                        rows={6}
                        className="w-full rounded-md border border-[#5a9dd0] bg-white px-3 py-2 text-sm font-mono text-[#0a2a4a] focus:border-[#1a6aaa] focus:outline-none focus:ring-1 focus:ring-[#1a6aaa] dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100"
                      />
                    ) : addon.additionalSources && addon.additionalSources.length > 0 ? (
                      <div className="space-y-2">
                        {addon.additionalSources.map((src, i: number) => (
                          <div key={i} className="rounded bg-[#e0f0ff] px-3 py-2 text-xs dark:bg-gray-700">
                            {src.chart && <p><span className="text-[#3a6a8a] dark:text-gray-400">Chart:</span> <span className="font-mono text-[#0a2a4a] dark:text-gray-100">{src.chart} @ {src.version}</span></p>}
                            {src.path && <p><span className="text-[#3a6a8a] dark:text-gray-400">Path:</span> <span className="font-mono text-[#0a2a4a] dark:text-gray-100">{src.path}</span></p>}
                            {src.repoURL && <p><span className="text-[#3a6a8a] dark:text-gray-400">Repo:</span> <span className="font-mono text-[#0a2a4a] dark:text-gray-100">{src.repoURL}</span></p>}
                          </div>
                        ))}
                      </div>
                    ) : (
                      <p className="text-xs text-[#5a8aaa] dark:text-gray-500">Single source (main chart only)</p>
                    )}
                  </div>

                  {/* Edit mode action buttons */}
                  {isEditingConfig && (
                    <div className="flex items-center gap-3 border-t border-[#c0ddf0] pt-4 dark:border-gray-700">
                      <button
                        type="button"
                        onClick={handleSaveConfig}
                        disabled={configSaving}
                        className="inline-flex items-center gap-2 rounded-lg bg-[#0a2a4a] px-4 py-2 text-sm font-medium text-white hover:bg-[#14466e] disabled:cursor-not-allowed disabled:opacity-50"
                      >
                        {configSaving && <Loader2 className="h-4 w-4 animate-spin" />}
                        Save (opens PR)
                      </button>
                      <button
                        type="button"
                        onClick={handleCancelEditConfig}
                        disabled={configSaving}
                        className="rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600 disabled:opacity-50"
                      >
                        Cancel
                      </button>
                    </div>
                  )}
                </div>
              </div>

              {/* Raw global values (for quick inspection — editing is in Values tab) */}
              {valuesYaml && (
                <details className="rounded-xl ring-2 ring-[#6aade0] bg-white dark:ring-gray-700 dark:bg-gray-800">
                  <summary className="cursor-pointer px-5 py-3 text-sm font-medium text-[#0a2a4a] dark:text-gray-100 select-none">
                    Raw default values (read-only)
                  </summary>
                  <div className="border-t border-[#c0ddf0] p-4 dark:border-gray-700">
                    <YamlViewer yaml={valuesYaml} title="Global Default Values" />
                  </div>
                </details>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
export default AddonDetail

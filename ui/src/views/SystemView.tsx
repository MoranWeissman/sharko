// V2-cleanup-57.3: the System page — PHASE 1, read-only.
//
// Status information used to live in 4 scattered places (bootstrap banner,
// notification bell, cluster statuses, settings health) and nothing showed
// the whole chain. This page answers exactly ONE question: "where is it
// broken?" — with four labeled arrows, each showing live status:
//
//   1. Sharko → Git repo     (Sharko's own git connection)      GET /repo/status
//   2. ArgoCD → Git repo     (ArgoCD's repo sync health)        GET /repo/status
//   3. Sharko → clusters     (per-cluster direct test state)    GET /clusters
//   4. ArgoCD → clusters     (per-cluster connection_status)    GET /clusters
//
// Plus the detected ArgoCD version (GET /observability/overview →
// control_plane.argocd_version) compared against the tested range shipped in
// ui/src/generated/argocd-tested-range.json (kept fresh by a weekly CI job).
//
// Everything is read-only: every element links to the existing page where
// you'd actually act (Settings → Connections, cluster detail). No actions
// live here.

import { useEffect, useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import {
  ArrowRight,
  CheckCircle2,
  ChevronDown,
  ChevronUp,
  ExternalLink,
  Fingerprint,
  GitBranch,
  HelpCircle,
  Server,
  Waves,
  XCircle,
} from 'lucide-react'
import { api, getSystemCapabilities } from '@/services/api'
import type { Cluster, SystemCapabilitiesResponse } from '@/services/models'
import {
  ARGOCD_CONN_LABEL,
  ARGOCD_CONN_TOOLTIP,
  SHARKO_CONN_LABEL,
  SHARKO_CONN_TOOLTIP,
} from '@/components/WhoseConnectionLabel'
import { ClusterIdentityPanel } from '@/components/ClusterIdentityPanel'
import testedRange from '@/generated/argocd-tested-range.json'

// ─────────────────────────────────────────────────────────────────────────────
// Bell-alert titles from the connection-health poller (#436). These strings
// are a stable contract with internal/notifications/connection_poller.go —
// when one of these alerts is active, its description is surfaced on the
// matching arrow as extra detail.
// ─────────────────────────────────────────────────────────────────────────────
export const GIT_CONN_ALERT_TITLE = "Sharko can't reach your Git connection"
export const ARGO_REPO_ALERT_TITLE = "ArgoCD can't sync the repo"

// Repo-arrow labels in the same voice as WhoseConnectionLabel (#447).
export const SHARKO_REPO_LABEL = 'Sharko → Git repo'
export const SHARKO_REPO_TOOLTIP =
  "This is Sharko's own connection to the Git repo: Sharko uses it for every commit and pull request. It can work even when ArgoCD's own connection to the repo is failing."
export const ARGOCD_REPO_LABEL = 'ArgoCD → Git repo'
export const ARGOCD_REPO_TOOLTIP =
  "This is ArgoCD's own connection to the Git repo (how it syncs your clusters). It can fail even when Sharko reaches the repo fine."

export type ArrowStatus = 'healthy' | 'degraded' | 'unknown'

export interface RepoStatus {
  initialized: boolean
  bootstrap_synced?: boolean
  reason?: string
}

export interface ArrowVerdict {
  status: ArrowStatus
  detail: string
}

// ─────────────────────────────────────────────────────────────────────────────
// Pure derivations (exported for tests)
// ─────────────────────────────────────────────────────────────────────────────

/** Arrow 1 — Sharko's own connection to the Git repo, from GET /repo/status. */
export function deriveSharkoRepoArrow(repo: RepoStatus | null): ArrowVerdict {
  if (!repo) {
    return { status: 'unknown', detail: "Couldn't determine the Git connection status." }
  }
  if (repo.reason === 'no_connection') {
    return {
      status: 'degraded',
      detail: 'No Git connection is configured. Sharko needs one for every commit and pull request.',
    }
  }
  if (repo.reason === 'connection_error' || repo.reason === 'error') {
    return {
      status: 'degraded',
      detail: "Sharko can't reach the Git repo right now (network, TLS, or auth problem).",
    }
  }
  if (repo.initialized) {
    return { status: 'healthy', detail: 'Sharko can read and write the repo.' }
  }
  if (repo.reason === 'not_bootstrapped') {
    return {
      status: 'healthy',
      detail: "Sharko can reach the repo — it just hasn't been initialized yet.",
    }
  }
  return { status: 'unknown', detail: "Couldn't determine the Git connection status." }
}

/** Arrow 2 — ArgoCD's own connection to the Git repo, from GET /repo/status. */
export function deriveArgoRepoArrow(repo: RepoStatus | null): ArrowVerdict {
  if (!repo) {
    return { status: 'unknown', detail: "Couldn't determine ArgoCD's repo sync status." }
  }
  if (!repo.initialized) {
    return {
      status: 'unknown',
      detail: "Can't assess until the repo is set up — ArgoCD has nothing to sync yet.",
    }
  }
  if (repo.bootstrap_synced) {
    return { status: 'healthy', detail: 'ArgoCD is syncing the repo — the bootstrap application is healthy.' }
  }
  if (repo.reason === 'bootstrap_unreachable') {
    return {
      status: 'degraded',
      detail:
        "ArgoCD can't reach the repo (a connection problem — often a proxy or TLS trust issue on the ArgoCD side).",
    }
  }
  return {
    status: 'degraded',
    detail: 'ArgoCD read the repo but the bootstrap application is degraded or missing.',
  }
}

/**
 * Arrow 3 (per cluster) — Sharko's own direct connection / test state.
 *
 * V2-cleanup-85.4: reads the auto-derived `derived_health_status` first —
 * computed fresh server-side on every read, with NO manual "Test
 * connection" click required — instead of depending solely on
 * `sharko_status`, which stays empty forever until someone clicks Test.
 * That gap used to make a perfectly reachable, actively-synced cluster
 * read as "unknown" here even though its own detail page already showed
 * it green. Both `derived_health_status` values that mean "Sharko can
 * reach it" — "healthy" (an addon is Synced+Healthy) and "reachable"
 * (connectivity confirmed but no addon yet) — count as healthy for this
 * tally; only "unknown" stays uncounted.
 */
export function deriveSharkoClusterStatus(c: Cluster): ArrowStatus {
  if (c.test_failing) return 'degraded'
  if (c.sharko_status === 'Unreachable') return 'degraded'
  if (
    c.derived_health_status === 'healthy' ||
    c.derived_health_status === 'reachable' ||
    c.sharko_status === 'Connected' ||
    c.sharko_status === 'Verified' ||
    c.sharko_status === 'Operational'
  ) {
    return 'healthy'
  }
  return 'unknown'
}

/**
 * Honest per-cluster label for arrow 3's expandable list — the ArrowStatus
 * enum only has one "good" state (healthy), but `derived_health_status`
 * distinguishes "healthy" (an addon is up) from "reachable" (Sharko can
 * reach it, no addon deployed yet). Returns undefined to fall back to the
 * StatusPill's default label ("Healthy") when there's nothing extra to say
 * (degraded/unknown, or the older manual sharko_status-only signal).
 */
export function deriveSharkoClusterLabel(c: Cluster): string | undefined {
  if (deriveSharkoClusterStatus(c) !== 'healthy') return undefined
  if (c.derived_health_status === 'healthy') return 'Healthy'
  if (c.derived_health_status === 'reachable') return 'Reachable'
  return undefined
}

/** Arrow 4 (per cluster) — ArgoCD's own connection (connection_status + check verdict). */
export function deriveArgoClusterStatus(c: Cluster): ArrowStatus {
  if (c.connection_status === 'Successful') return 'healthy'
  if (c.connectivity_status === 'verified_argocd' || c.connectivity_status === 'verified_check') {
    return 'healthy'
  }
  if (c.connectivity_status === 'check_failed') return 'degraded'
  if (c.connection_status === 'Failed') return 'degraded'
  return 'unknown'
}

export interface Aggregate {
  status: ArrowStatus
  label: string
}

/** Roll a list of per-cluster statuses up into one arrow verdict + label. */
export function aggregateStatuses(statuses: ArrowStatus[]): Aggregate {
  const total = statuses.length
  if (total === 0) return { status: 'unknown', label: 'No clusters yet' }
  const healthy = statuses.filter((s) => s === 'healthy').length
  const anyDegraded = statuses.some((s) => s === 'degraded')
  const label = `${healthy} of ${total} healthy`
  if (anyDegraded) return { status: 'degraded', label }
  if (healthy === total) return { status: 'healthy', label }
  return { status: 'unknown', label }
}

// ─────────────────────────────────────────────────────────────────────────────
// ArgoCD version vs tested range (deliberately dumb and safe: MINOR-version
// comparison only; unknown/missing/unparseable → no badge, never blocks).
// ─────────────────────────────────────────────────────────────────────────────

export interface TestedRange {
  tested_min: string
  tested_max: string
  tested_versions: string[]
  updated: string
}

export function parseMajorMinor(v?: string): { major: number; minor: number } | null {
  if (!v) return null
  const m = /^\s*v?(\d+)\.(\d+)/.exec(v)
  if (!m) return null
  return { major: parseInt(m[1], 10), minor: parseInt(m[2], 10) }
}

/**
 * True only when the detected version parses AND its (major, minor) falls
 * outside [tested_min, tested_max]. Anything unparseable → false (no badge).
 */
export function versionOutsideTestedRange(
  detected: string | undefined,
  range: Pick<TestedRange, 'tested_min' | 'tested_max'> = testedRange,
): boolean {
  const d = parseMajorMinor(detected)
  const lo = parseMajorMinor(range.tested_min)
  const hi = parseMajorMinor(range.tested_max)
  if (!d || !lo || !hi) return false
  const cmp = (a: { major: number; minor: number }, b: { major: number; minor: number }) =>
    a.major !== b.major ? a.major - b.major : a.minor - b.minor
  return cmp(d, lo) < 0 || cmp(d, hi) > 0
}

/** "v3.2" or "v3.1–v3.2" for the badge text. */
export function testedRangeLabel(
  range: Pick<TestedRange, 'tested_min' | 'tested_max'> = testedRange,
): string {
  if (range.tested_min === range.tested_max) return range.tested_min
  return `${range.tested_min}–${range.tested_max}`
}

// ─────────────────────────────────────────────────────────────────────────────
// Presentational bits
// ─────────────────────────────────────────────────────────────────────────────

function StatusPill({ status, label }: { status: ArrowStatus; label?: string }) {
  // V3 U3: source colors from clusterStatus.ts instead of hardcoding.
  // Use more prominent sizing (text-sm, h-4 icon, larger padding) and the
  // canonical severity→color mapping. ArrowStatus maps: healthy→green (good),
  // degraded→red (problem), unknown→gray (unknown).
  const Icon = status === 'healthy' ? CheckCircle2 : status === 'degraded' ? XCircle : HelpCircle

  const colorClasses =
    status === 'healthy'
      ? 'bg-green-50 text-green-700 dark:bg-green-900/30 dark:text-green-400'
      : status === 'degraded'
        ? 'bg-red-50 text-red-700 dark:bg-red-900/30 dark:text-red-400'
        : 'bg-gray-50 text-gray-700 dark:bg-gray-900/30 dark:text-gray-400'

  const defaultLabel = status === 'healthy' ? 'Healthy' : status === 'degraded' ? 'Problem' : 'Unknown'

  return (
    <span
      className={`inline-flex items-center gap-2 rounded-full px-3 py-1 text-sm font-medium ${colorClasses}`}
    >
      <Icon className="h-4 w-4" />
      {label ?? defaultLabel}
    </span>
  )
}

interface ArrowCardProps {
  from: string
  to: string
  caption: ReactNode
  status: ArrowStatus
  statusLabel?: string
  detail: string
  /** Optional live bell-alert description (#436) shown as a second line. */
  alertDetail?: string
  actionTo: string
  actionLabel: string
  children?: ReactNode
}

function ArrowCard({
  from,
  to,
  caption,
  status,
  statusLabel,
  detail,
  alertDetail,
  actionTo,
  actionLabel,
  children,
}: ArrowCardProps) {
  return (
    <div className="flex flex-col gap-3 rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:ring-gray-700 dark:bg-gray-800">
      <div className="flex items-start justify-between gap-2">
        <div className="flex flex-col gap-0.5">
          <div className="flex items-center gap-2 text-sm font-semibold text-[#0a2a4a] dark:text-white">
            <span>{from}</span>
            <ArrowRight className="h-4 w-4 shrink-0 text-[#5a8aaa] dark:text-gray-500" aria-hidden="true" />
            <span>{to}</span>
          </div>
          {caption}
        </div>
        <StatusPill status={status} label={statusLabel} />
      </div>
      <p className="text-sm text-[#2a5a7a] dark:text-gray-300">{detail}</p>
      {alertDetail && (
        <p className="text-xs text-amber-700 dark:text-amber-400">{alertDetail}</p>
      )}
      {children}
      <Link
        to={actionTo}
        className="mt-auto inline-flex w-fit items-center gap-1.5 text-xs font-medium text-[#1a4a6a] underline-offset-2 hover:underline dark:text-blue-300"
      >
        <ExternalLink className="h-3.5 w-3.5" />
        {actionLabel}
      </Link>
    </div>
  )
}

/** Expandable per-cluster status list under a cluster arrow. */
function ClusterList({
  clusters,
  derive,
  deriveLabel,
  toggleLabel,
}: {
  clusters: Cluster[]
  derive: (c: Cluster) => ArrowStatus
  /** Optional honest label override (e.g. "Reachable" vs "Healthy") — falls back to the StatusPill default. */
  deriveLabel?: (c: Cluster) => string | undefined
  toggleLabel: string
}) {
  const [open, setOpen] = useState(false)
  if (clusters.length === 0) return null
  return (
    <div>
      <button
        onClick={() => setOpen((o) => !o)}
        className="inline-flex items-center gap-1 text-xs font-medium text-[#3a6a8a] hover:text-[#0a2a4a] dark:text-gray-400 dark:hover:text-white"
        aria-expanded={open}
      >
        {open ? <ChevronUp className="h-3.5 w-3.5" /> : <ChevronDown className="h-3.5 w-3.5" />}
        {toggleLabel}
      </button>
      {open && (
        <ul className="mt-2 space-y-1.5">
          {clusters.map((c) => (
            <li key={c.name} className="flex items-center justify-between gap-2">
              <Link
                to={`/clusters/${encodeURIComponent(c.name)}`}
                className="truncate text-sm text-[#1a4a6a] underline-offset-2 hover:underline dark:text-blue-300"
              >
                {c.name}
              </Link>
              <StatusPill status={derive(c)} label={deriveLabel?.(c)} />
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// The page
// ─────────────────────────────────────────────────────────────────────────────

export function SystemView() {
  const [repoStatus, setRepoStatus] = useState<RepoStatus | null>(null)
  const [clusters, setClusters] = useState<Cluster[]>([])
  const [alertDescriptions, setAlertDescriptions] = useState<Record<string, string>>({})
  const [argocdVersion, setArgocdVersion] = useState<string | undefined>(undefined)
  const [capabilities, setCapabilities] = useState<SystemCapabilitiesResponse | null>(null)
  const [capabilitiesLoading, setCapabilitiesLoading] = useState(true)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let cancelled = false
    Promise.allSettled([
      api.getRepoStatus(),
      api.getClusters(),
      api.getNotifications(),
      api.getObservability(),
      getSystemCapabilities(),
    ]).then(([repoRes, clustersRes, notifRes, obsRes, capsRes]) => {
      if (cancelled) return
      if (repoRes.status === 'fulfilled') setRepoStatus(repoRes.value)
      if (clustersRes.status === 'fulfilled') setClusters(clustersRes.value.clusters ?? [])
      if (notifRes.status === 'fulfilled') {
        const map: Record<string, string> = {}
        for (const n of notifRes.value.notifications ?? []) {
          if (n.title === GIT_CONN_ALERT_TITLE || n.title === ARGO_REPO_ALERT_TITLE) {
            map[n.title] = n.description
          }
        }
        setAlertDescriptions(map)
      }
      if (obsRes.status === 'fulfilled') {
        const v = obsRes.value.control_plane?.argocd_version
        if (v) setArgocdVersion(v)
      }
      if (capsRes.status === 'fulfilled' && capsRes.value) setCapabilities(capsRes.value)
      setCapabilitiesLoading(false)
      setLoading(false)
    })
    return () => {
      cancelled = true
    }
  }, [])

  const sharkoRepo = deriveSharkoRepoArrow(repoStatus)
  const argoRepo = deriveArgoRepoArrow(repoStatus)
  // Filter out the hub 'in-cluster' entry from health counts (V3 U3).
  const managedClusters = clusters.filter((c) => c.name !== 'in-cluster')
  const sharkoClusterAgg = aggregateStatuses(managedClusters.map(deriveSharkoClusterStatus))
  const argoClusterAgg = aggregateStatuses(managedClusters.map(deriveArgoClusterStatus))

  const detectedMM = parseMajorMinor(argocdVersion)
  const outsideRange = versionOutsideTestedRange(argocdVersion)

  if (loading) {
    return (
      <div className="flex items-center justify-center py-24">
        <div className="h-8 w-8 animate-spin rounded-full border-4 border-[#6aade0] border-t-[#1a3d5c] dark:border-gray-700 dark:border-t-teal-500" />
      </div>
    )
  }

  return (
    <div className="space-y-8">
      {/* Header */}
      <div>
        <h1 className="text-2xl font-bold text-[#0a2a4a] dark:text-white">System</h1>
        <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-400">
          One read-only view of the whole chain — where is it broken? Fix things from Settings or
          the cluster pages; nothing on this page changes anything.
        </p>
      </div>

      {/* Detected ArgoCD version + tested-range badge */}
      <div className="flex flex-wrap items-center gap-3 rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:ring-gray-700 dark:bg-gray-800">
        <Waves className="h-5 w-5 text-[#3a6a8a] dark:text-gray-400" aria-hidden="true" />
        <span className="text-sm font-medium text-[#0a2a4a] dark:text-white">
          {argocdVersion ? `ArgoCD ${argocdVersion} detected` : 'ArgoCD version unknown'}
        </span>
        {argocdVersion && !outsideRange && (
          <span className="text-xs text-[#3a6a8a] dark:text-gray-400">
            Sharko is tested with {testedRangeLabel()}
          </span>
        )}
        {outsideRange && detectedMM && (
          <span
            data-testid="argocd-version-badge"
            className="inline-flex items-center gap-1.5 rounded-full bg-amber-50 px-2.5 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-400"
          >
            ArgoCD v{detectedMM.major}.{detectedMM.minor} detected — Sharko is tested with{' '}
            {testedRangeLabel()}
          </span>
        )}
      </div>

      {/* The Git repo — arrows 1 & 2 */}
      <section>
        <div className="mb-3 flex items-center gap-2">
          <GitBranch className="h-4 w-4 text-[#3a6a8a] dark:text-gray-400" aria-hidden="true" />
          <h2 className="text-sm font-semibold uppercase tracking-wider text-[#3a6a8a] dark:text-gray-400">
            The Git repo
          </h2>
        </div>
        <div className="grid gap-4 md:grid-cols-2">
          <ArrowCard
            from="Sharko"
            to="Git repo"
            caption={
              <span
                className="w-fit cursor-help text-xs font-medium text-[#5a8aaa] dark:text-gray-500"
                title={SHARKO_REPO_TOOLTIP}
              >
                {SHARKO_REPO_LABEL}
              </span>
            }
            status={sharkoRepo.status}
            detail={sharkoRepo.detail}
            alertDetail={alertDescriptions[GIT_CONN_ALERT_TITLE]}
            actionTo="/settings?section=connections"
            actionLabel="Check in Settings → Connections"
          />
          <ArrowCard
            from="ArgoCD"
            to="Git repo"
            caption={
              <span
                className="w-fit cursor-help text-xs font-medium text-[#5a8aaa] dark:text-gray-500"
                title={ARGOCD_REPO_TOOLTIP}
              >
                {ARGOCD_REPO_LABEL}
              </span>
            }
            status={argoRepo.status}
            detail={argoRepo.detail}
            alertDetail={alertDescriptions[ARGO_REPO_ALERT_TITLE]}
            actionTo="/settings?section=connections"
            actionLabel="Check in Settings → Connections"
          />
        </div>
      </section>

      {/* The clusters — arrows 3 & 4 */}
      <section>
        <div className="mb-3 flex items-center gap-2">
          <Server className="h-4 w-4 text-[#3a6a8a] dark:text-gray-400" aria-hidden="true" />
          <h2 className="text-sm font-semibold uppercase tracking-wider text-[#3a6a8a] dark:text-gray-400">
            The clusters
          </h2>
        </div>
        <div className="grid gap-4 md:grid-cols-2">
          <ArrowCard
            from="Sharko"
            to="Clusters"
            caption={<span className="text-xs font-medium text-[#5a8aaa] dark:text-gray-500">{SHARKO_CONN_LABEL}</span>}
            status={sharkoClusterAgg.status}
            statusLabel={sharkoClusterAgg.label}
            detail={SHARKO_CONN_TOOLTIP}
            actionTo="/clusters"
            actionLabel="Open the Clusters page"
          >
            <ClusterList
              clusters={managedClusters}
              derive={deriveSharkoClusterStatus}
              deriveLabel={deriveSharkoClusterLabel}
              toggleLabel={`Per-cluster status (${SHARKO_CONN_LABEL})`}
            />
          </ArrowCard>
          <ArrowCard
            from="ArgoCD"
            to="Clusters"
            caption={<span className="text-xs font-medium text-[#5a8aaa] dark:text-gray-500">{ARGOCD_CONN_LABEL}</span>}
            status={argoClusterAgg.status}
            statusLabel={argoClusterAgg.label}
            detail={ARGOCD_CONN_TOOLTIP}
            actionTo="/clusters"
            actionLabel="Open the Clusters page"
          >
            <ClusterList
              clusters={managedClusters}
              derive={deriveArgoClusterStatus}
              toggleLabel={`Per-cluster status (${ARGOCD_CONN_LABEL})`}
            />
          </ArrowCard>
        </div>
      </section>

      {/* Sharko's own identity (V2-cleanup-89.2) — moved here from the
        * Register Cluster dialog's Layer 1. It's read-only information
        * about what Sharko has auto-detected about itself, not something a
        * newcomer registering a cluster needs to act on — this "whole
        * chain" page is the natural home for it. */}
      <section>
        <div className="mb-3 flex items-center gap-2">
          <Fingerprint className="h-4 w-4 text-[#3a6a8a] dark:text-gray-400" aria-hidden="true" />
          <h2 className="text-sm font-semibold uppercase tracking-wider text-[#3a6a8a] dark:text-gray-400">
            Sharko's identity
          </h2>
        </div>
        <ClusterIdentityPanel capabilities={capabilities} loading={capabilitiesLoading} />
      </section>
    </div>
  )
}

export default SystemView

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
  AlertTriangle,
  ArrowRight,
  CheckCircle2,
  ExternalLink,
  Fingerprint,
  GitBranch,
  HelpCircle,
  KeyRound,
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
import { HomeClusterCard, type HomeClusterInfo } from '@/components/HomeClusterCard'
import { ManagedSecretsSummaryLine } from '@/components/ManagedSecretsSummaryLine'
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

// Pure repo/ArgoCD-connection derivations now live in lib/repoHealth.ts
// (WQ-3) so the nav-badge hook can read the exact same verdicts without
// pulling this whole view into Layout's always-mounted bundle. Re-exported
// here so existing imports of these names from '@/views/SystemView' keep
// working unchanged.
export type { ArrowStatus, RepoStatus, ArrowVerdict } from '@/lib/repoHealth'
export { deriveSharkoRepoArrow, deriveArgoRepoArrow } from '@/lib/repoHealth'
import type { ArrowStatus, RepoStatus } from '@/lib/repoHealth'
import { deriveSharkoRepoArrow, deriveArgoRepoArrow } from '@/lib/repoHealth'

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
        <p className="text-sm text-amber-700 dark:text-amber-400">{alertDetail}</p>
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

export interface ClusterStatusPart {
  text: string
  tone: 'healthy' | 'degraded' | 'unknown'
}

/**
 * Same three buckets as the summary sentence, kept apart (instead of
 * pre-joined into one string) so the on-screen line can color each bucket —
 * healthy green, with-issues red, unknown gray — while the sentence itself
 * stays plain text. `summarizeClusterStatuses` below is the plain-string
 * version built from these same parts.
 */
export function clusterStatusParts(
  statuses: ArrowStatus[],
): { total: number; clusterWord: string; parts: ClusterStatusPart[] } {
  const total = statuses.length
  const clusterWord = total === 1 ? 'managed cluster' : 'managed clusters'
  const healthy = statuses.filter((s) => s === 'healthy').length
  const degraded = statuses.filter((s) => s === 'degraded').length
  const unknown = statuses.filter((s) => s === 'unknown').length

  const parts: ClusterStatusPart[] = []
  if (healthy > 0) parts.push({ text: `${healthy} healthy`, tone: 'healthy' })
  if (degraded > 0) parts.push({ text: `${degraded} with issues`, tone: 'degraded' })
  if (unknown > 0) parts.push({ text: `${unknown} unknown`, tone: 'unknown' })

  return { total, clusterWord, parts }
}

/**
 * One honest line per cluster arrow — "N managed clusters — X healthy, Y
 * with issues" — instead of the old expandable per-cluster list (walk-day
 * finding, maintainer-approved). Counts all three buckets (healthy,
 * degraded, unknown) but only mentions the non-zero ones, so "8 managed
 * clusters — 7 healthy, 1 with issues" reads clean instead of "0 unknown".
 */
export function summarizeClusterStatuses(statuses: ArrowStatus[]): string {
  const { total, clusterWord, parts } = clusterStatusParts(statuses)
  if (parts.length === 0) return `${total} ${clusterWord}`
  return `${total} ${clusterWord} — ${parts.map((p) => p.text).join(', ')}`
}

// Tone → text color, reusing the same healthy/degraded/unknown palette as
// StatusPill above, so a bucket phrase and the pill icon agree on color.
const CLUSTER_STATUS_TONE_CLASSES: Record<ClusterStatusPart['tone'], string> = {
  healthy: 'text-green-700 dark:text-green-400',
  degraded: 'text-red-700 dark:text-red-400',
  unknown: 'text-gray-700 dark:text-gray-400',
}

/**
 * One-line cluster status summary under a cluster arrow, linking to
 * /clusters. This is the ONLY count summary on the card — the StatusPill
 * next to the arrow shows a plain Healthy/Problem/Unknown word instead of
 * its own count, so the two don't say the same thing twice.
 */
function ClusterStatusLine({
  clusters,
  derive,
}: {
  clusters: Cluster[]
  derive: (c: Cluster) => ArrowStatus
}) {
  if (clusters.length === 0) return null
  const { total, clusterWord, parts } = clusterStatusParts(clusters.map(derive))
  return (
    <Link
      to="/clusters"
      className="inline-flex w-fit text-sm text-[#1a4a6a] underline-offset-2 hover:underline dark:text-blue-300"
    >
      {`${total} ${clusterWord}`}
      {parts.length > 0 && ' — '}
      {parts.map((part, i) => (
        <span key={part.tone}>
          {i > 0 ? ', ' : ''}
          <span className={CLUSTER_STATUS_TONE_CLASSES[part.tone]}>{part.text}</span>
        </span>
      ))}
    </Link>
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
  // Home-cluster identity card (WQ-3 — moved here from Dashboard). Every
  // field degrades independently to undefined/"—" rather than failing the
  // whole page fetch — same contract HomeClusterCard has always had.
  const [homeCluster, setHomeCluster] = useState<HomeClusterInfo | null>(null)
  const [sharkoVersion, setSharkoVersion] = useState<string | undefined>(undefined)
  const [uptime, setUptime] = useState<string | undefined>(undefined)

  useEffect(() => {
    let cancelled = false
    Promise.allSettled([
      api.getRepoStatus(),
      api.getClusters(),
      api.getNotifications(),
      api.getObservability(),
      getSystemCapabilities(),
      api.getHomeCluster(),
      api.health(),
      api.getFleetStatus(),
    ]).then(([repoRes, clustersRes, notifRes, obsRes, capsRes, homeRes, healthRes, fleetRes]) => {
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
        // ONE ArgoCD version source for the whole page (WQ-3): this same
        // string feeds both the tested-range badge below AND the home-
        // cluster card's ArgoCD version field, so the two can never
        // contradict each other.
        const v = obsRes.value.control_plane?.argocd_version
        if (v) setArgocdVersion(v)
      }
      if (capsRes.status === 'fulfilled' && capsRes.value) setCapabilities(capsRes.value)
      if (homeRes.status === 'fulfilled') setHomeCluster(homeRes.value)
      if (healthRes.status === 'fulfilled') setSharkoVersion(healthRes.value?.version)
      if (fleetRes.status === 'fulfilled') setUptime(fleetRes.value?.uptime)
      setCapabilitiesLoading(false)
      setLoading(false)
    })
    return () => {
      cancelled = true
    }
  }, [])

  const sharkoRepo = deriveSharkoRepoArrow(repoStatus)
  const argoRepo = deriveArgoRepoArrow(repoStatus)
  // ArgoCD "connected" for the home-cluster card: derived from the same
  // version string the tested-range badge reads (a non-empty version means
  // observability's control-plane read actually reached ArgoCD) — not a
  // separate /config read, which is exactly the second-source contradiction
  // this unification closes.
  const argocdConnected = argocdVersion !== undefined
  // Filter out the hub 'in-cluster' entry from health counts (V3 U3).
  const managedClusters = clusters.filter((c) => c.name !== 'in-cluster')
  const sharkoClusterAgg = aggregateStatuses(managedClusters.map(deriveSharkoClusterStatus))
  const argoClusterAgg = aggregateStatuses(managedClusters.map(deriveArgoClusterStatus))

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

      {/* Detected ArgoCD version — said once, with an honest warning when it's
          outside the tested range instead of a second, near-duplicate line
          (walk finding: both used to render together). */}
      <div className="flex flex-wrap items-center gap-3 rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:ring-gray-700 dark:bg-gray-800">
        <Waves className="h-5 w-5 text-[#3a6a8a] dark:text-gray-400" aria-hidden="true" />
        <span
          data-testid="argocd-version-line"
          className={`inline-flex items-center gap-1.5 text-sm font-medium ${
            outsideRange ? 'text-amber-700 dark:text-amber-400' : 'text-[#0a2a4a] dark:text-white'
          }`}
        >
          {outsideRange && <AlertTriangle className="h-4 w-4 shrink-0" aria-hidden="true" />}
          {argocdVersion ? `ArgoCD ${argocdVersion} detected` : 'ArgoCD version unknown'}
        </span>
        {argocdVersion && (
          <span
            className={`text-xs ${
              outsideRange ? 'text-amber-700 dark:text-amber-400' : 'text-[#3a6a8a] dark:text-gray-400'
            }`}
          >
            {`Sharko is tested with ${testedRangeLabel()}`}
            {outsideRange ? ' — outside the tested range' : ''}
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
            to="Managed Clusters"
            caption={<span className="text-xs font-medium text-[#5a8aaa] dark:text-gray-500">{SHARKO_CONN_LABEL}</span>}
            status={sharkoClusterAgg.status}
            // The count ("N of M healthy") only lives on the pill when
            // there are no clusters to show a ClusterStatusLine for —
            // otherwise that line is the one place the count is said.
            statusLabel={managedClusters.length === 0 ? sharkoClusterAgg.label : undefined}
            detail={SHARKO_CONN_TOOLTIP}
            actionTo="/clusters"
            actionLabel="Open the Managed Clusters page"
          >
            <ClusterStatusLine clusters={managedClusters} derive={deriveSharkoClusterStatus} />
          </ArrowCard>
          <ArrowCard
            from="ArgoCD"
            to="Managed Clusters"
            caption={<span className="text-xs font-medium text-[#5a8aaa] dark:text-gray-500">{ARGOCD_CONN_LABEL}</span>}
            status={argoClusterAgg.status}
            statusLabel={managedClusters.length === 0 ? argoClusterAgg.label : undefined}
            detail={ARGOCD_CONN_TOOLTIP}
            actionTo="/clusters"
            actionLabel="Open the Managed Clusters page"
          >
            <ClusterStatusLine clusters={managedClusters} derive={deriveArgoClusterStatus} />
          </ArrowCard>
        </div>
      </section>

      {/* Home-cluster identity card (WQ-3 — moved here from Dashboard).
          System is the engine-room page, so "where Sharko + ArgoCD run,
          and what version of each" belongs next to the connection arrows
          above rather than on the work-queue Dashboard. */}
      {homeCluster && (
        <HomeClusterCard
          homeCluster={homeCluster}
          sharkoVersion={sharkoVersion}
          argocdVersion={argocdVersion}
          argocdConnected={argocdConnected}
          uptime={uptime}
        />
      )}

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

      {/* Managed secrets — S1: this used to be two full tables bolted onto
        * the bottom of this page. The System page's job is how Sharko
        * itself is set up and doing, not a resource list — so, same as
        * Managed Clusters and Addons, every secret now has its own page
        * (/secrets). This stays a single quiet line with the headline fact
        * and a link. */}
      <section>
        <div className="mb-3 flex items-center gap-2">
          <KeyRound className="h-4 w-4 text-[#3a6a8a] dark:text-gray-400" aria-hidden="true" />
          <h2 className="text-sm font-semibold uppercase tracking-wider text-[#3a6a8a] dark:text-gray-400">
            Managed secrets
          </h2>
        </div>
        <ManagedSecretsSummaryLine />
      </section>
    </div>
  )
}

export default SystemView

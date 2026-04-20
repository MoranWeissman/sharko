import { memo, useEffect, useMemo, useState } from 'react'
import { Info, Loader2 } from 'lucide-react'
import { ValuesEditor } from '@/components/ValuesEditor'
import { RecentPRsPanel } from '@/components/RecentPRsPanel'
import { api } from '@/services/api'
import type {
  AddonComparisonStatus,
  ClusterAddonValuesResponse,
  MeResponse,
} from '@/services/models'

/**
 * PerClusterAddonOverridesEditor — drives the per-cluster overrides UX.
 *
 * Renders:
 *  - an addon picker scoped to addons that are enabled (or could be enabled)
 *    on this cluster,
 *  - the shared ValuesEditor pointed at the per-cluster overrides path,
 *  - the same diff/nudge/PR-link UX as the global editor.
 *
 * Pre-fetches /users/me once for the proactive AttributionNudge signal.
 *
 * v1.21.8: wrapped in React.memo with a content-based equality check on
 * `addons`. The parent (ClusterDetail) polls every 30s and produces a
 * fresh `data.addon_comparisons` array reference on every tick — even
 * when the content is unchanged. Without memo, each tick re-rendered
 * this component, which re-ran the `eligible` useMemo (returning a new
 * array ref) and, combined with the ValuesEditor's `selected`-based key,
 * caused the editor to remount repeatedly. That remount storm saturated
 * the event loop after post-migration cache invalidations and made tab
 * clicks feel dead. Memoising on addon identity (addon_name +
 * git_configured + argocd_deployed — the three fields the filter and
 * picker care about) eliminates the storm while still re-rendering when
 * the user enables/disables or adopts an addon.
 */
export interface PerClusterAddonOverridesEditorProps {
  clusterName: string
  addons: AddonComparisonStatus[]
  /** GitHub https://github.com/<owner>/<repo> base, optional. */
  gitRepoBase?: string
  /** Default branch (e.g. "main"); used in the GitHub deep link. */
  gitDefaultBranch?: string
  /**
   * Called after a successful save so the parent can refresh the diff
   * panel. Optional — the toast inside ValuesEditor is enough on its own.
   */
  onSaved?: () => void
}

function PerClusterAddonOverridesEditorImpl({
  clusterName,
  addons,
  gitRepoBase,
  gitDefaultBranch = 'main',
  onSaved,
}: PerClusterAddonOverridesEditorProps) {
  // Eligible addons: any addon currently configured (git-configured or
  // ArgoCD-deployed). The list is small enough to fit in a single select.
  const eligible = useMemo(
    () =>
      addons
        .filter((a) => a.git_configured || a.argocd_deployed)
        .sort((a, b) => a.addon_name.localeCompare(b.addon_name)),
    [addons],
  )

  const [selected, setSelected] = useState<string>(() => eligible[0]?.addon_name ?? '')
  const [data, setData] = useState<ClusterAddonValuesResponse | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [me, setMe] = useState<MeResponse | null>(null)

  // Keep `selected` pointed at a valid addon. Two cases to handle:
  //  (1) initial mount with zero eligible addons, then eligible populates —
  //      seed `selected` with the first entry.
  //  (2) the currently selected addon was removed from the cluster — fall
  //      back to the first eligible entry so the editor doesn't disappear.
  // Critically, we must NEVER transiently flip `selected` to empty-string
  // when a valid option still exists, or the `key={clusterName/selected}`
  // on ValuesEditor would unmount + remount the editor.
  useEffect(() => {
    if (eligible.length === 0) return
    if (!selected) {
      setSelected(eligible[0].addon_name)
      return
    }
    const stillPresent = eligible.some((a) => a.addon_name === selected)
    if (!stillPresent) {
      setSelected(eligible[0].addon_name)
    }
  }, [eligible, selected])

  useEffect(() => {
    api.getMe().then(setMe).catch(() => setMe(null))
  }, [])

  useEffect(() => {
    if (!selected) {
      setData(null)
      return
    }
    setLoading(true)
    setError(null)
    api
      .getClusterAddonValues(clusterName, selected)
      .then(setData)
      .catch((e) => setError(e instanceof Error ? e.message : 'Failed to load overrides'))
      .finally(() => setLoading(false))
  }, [clusterName, selected])

  if (eligible.length === 0) {
    return (
      <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-6 text-sm text-[#2a5a7a] dark:ring-gray-700 dark:bg-gray-800 dark:text-gray-400">
        This cluster has no addons configured yet, so there's nothing to
        override. Enable an addon from the Addons tab first.
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:ring-gray-700 dark:bg-gray-800">
        <label htmlFor="cluster-addon-picker" className="block text-xs font-semibold uppercase tracking-wide text-[#3a6a8a] dark:text-gray-400">
          Addon
        </label>
        <select
          id="cluster-addon-picker"
          value={selected}
          onChange={(e) => setSelected(e.target.value)}
          className="mt-1 block w-full max-w-xs rounded-md border border-[#6aade0] bg-white px-3 py-1.5 text-sm text-[#0a2a4a] focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100"
        >
          {eligible.map((a) => (
            <option key={a.addon_name} value={a.addon_name}>
              {a.addon_name}
            </option>
          ))}
        </select>
        <p className="mt-2 flex items-start gap-1.5 text-xs text-[#3a6a8a] dark:text-gray-500">
          <Info className="mt-0.5 h-3 w-3 shrink-0" />
          <span>
            Anything here overrides global values for{' '}
            <span className="font-mono">{clusterName}</span>. Leave empty to use the global
            defaults. Save opens a PR — on merge, ArgoCD reconciles only this cluster.
          </span>
        </p>
      </div>

      {loading && (
        <div className="flex items-center gap-2 text-sm text-[#3a6a8a] dark:text-gray-400">
          <Loader2 className="h-4 w-4 animate-spin" />
          Loading overrides…
        </div>
      )}
      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

      {!loading && !error && selected && (
        <ValuesEditor
          key={`${clusterName}/${selected}`}
          title={`${selected} overrides on ${clusterName}`}
          subtitle="Only this cluster is affected. Other clusters keep using the global defaults until you edit them too."
          initialYAML={data?.current_overrides ?? ''}
          schema={data?.schema ?? null}
          hasPersonalToken={me?.has_github_token}
          githubFileURL={
            gitRepoBase
              ? `${gitRepoBase}/blob/${gitDefaultBranch}/configuration/addons-clusters-values/${clusterName}.yaml`
              : undefined
          }
          allowEmpty
          onSubmit={async (newYAML) => {
            const result = await api.setClusterAddonValues(clusterName, selected, newYAML)
            // Refresh local copy so subsequent edits diff against the new content.
            setData((prev) => (prev ? { ...prev, current_overrides: newYAML } : prev))
            onSaved?.()
            return result
          }}
          belowEditor={({ refreshKey }) => (
            <RecentPRsPanel
              title="Recent changes (last 5)"
              refreshKey={refreshKey}
              load={() => api.getClusterAddonValuesRecentPRs(clusterName, selected, 5)}
            />
          )}
        />
      )}
    </div>
  )
}

/**
 * Shallow signature of the addon fields the editor actually uses. If this
 * signature is identical across renders, the editor safely skips the
 * re-render — no event-loop churn, no editor remount, no network churn.
 *
 * Only `addon_name`, `git_configured`, and `argocd_deployed` influence
 * eligibility; other fields (status, issues, sync status, versions) are
 * irrelevant to the picker and are intentionally ignored.
 */
function addonsSignature(addons: AddonComparisonStatus[]): string {
  const parts: string[] = []
  for (const a of addons) {
    parts.push(
      `${a.addon_name}|${a.git_configured ? '1' : '0'}|${a.argocd_deployed ? '1' : '0'}`,
    )
  }
  return parts.join(',')
}

function propsEqual(
  prev: PerClusterAddonOverridesEditorProps,
  next: PerClusterAddonOverridesEditorProps,
): boolean {
  if (prev.clusterName !== next.clusterName) return false
  if (prev.gitRepoBase !== next.gitRepoBase) return false
  if (prev.gitDefaultBranch !== next.gitDefaultBranch) return false
  if (prev.onSaved !== next.onSaved) return false
  if (prev.addons === next.addons) return true
  if (prev.addons.length !== next.addons.length) return false
  return addonsSignature(prev.addons) === addonsSignature(next.addons)
}

export const PerClusterAddonOverridesEditor = memo(
  PerClusterAddonOverridesEditorImpl,
  propsEqual,
)

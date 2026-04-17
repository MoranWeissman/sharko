import { useEffect, useMemo, useState } from 'react'
import { Loader2 } from 'lucide-react'
import { ValuesEditor } from '@/components/ValuesEditor'
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

export function PerClusterAddonOverridesEditor({
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

  useEffect(() => {
    if (!selected && eligible.length > 0) {
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
        <p className="mt-2 text-xs text-[#3a6a8a] dark:text-gray-500">
          Editing overrides for <span className="font-mono">{clusterName}</span>. Save submits a
          PR; an empty submission clears the override and falls back to the global default.
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
        />
      )}
    </div>
  )
}

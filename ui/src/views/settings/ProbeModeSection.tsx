import { useCallback, useEffect, useState } from 'react'
import { Loader2, Radar, CheckCircle2 } from 'lucide-react'
import { api } from '@/services/api'
import { showToast } from '@/components/ToastNotification'
import type { ProbeMode } from '@/services/models'

/**
 * Settings → Connectivity Probe (V2-cleanup-85.4-frontend).
 *
 * Server-wide, admin-only toggle for how Sharko confirms a newly registered
 * cluster is reachable, before any real addon exists on it:
 *   - "check-app" (default): Sharko deploys a tiny throwaway ArgoCD app to
 *     the cluster and watches it sync. Proves the whole path works, but it
 *     IS a deployment, even if transient.
 *   - "api-test": Sharko never deploys anything — it reads reachability
 *     straight from ArgoCD's own connection to the cluster.
 *
 * GET /api/v1/settings/probe-mode returns { probe_mode }; PUT with the same
 * shape saves it (admin-only on the backend — 403 for non-admins, so the
 * caller must gate this section with isAdmin before rendering).
 */

const OPTIONS: { value: ProbeMode; title: string; helper: string }[] = [
  {
    value: 'check-app',
    title: 'Deploy a temporary check app (default)',
    helper:
      'It lands a tiny throwaway app on each new cluster to confirm reachability, then removes it once your first real addon is deployed.',
  },
  {
    value: 'api-test',
    title: 'Run an API test — deploy nothing',
    helper:
      "Sharko checks reachability through the ArgoCD connection instead, and never deploys anything to your cluster.",
  },
]

export function ProbeModeSection() {
  const [mode, setMode] = useState<ProbeMode | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [saving, setSaving] = useState<ProbeMode | null>(null)

  const load = useCallback(() => {
    setLoading(true)
    setLoadError(null)
    api
      .getProbeMode()
      .then((res) => setMode(res.probe_mode))
      .catch((err) => setLoadError(err instanceof Error ? err.message : 'Failed to load'))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    load()
  }, [load])

  async function handleSelect(next: ProbeMode) {
    if (next === mode || saving) return
    const previous = mode
    setSaving(next)
    setMode(next) // optimistic — reverted on failure
    try {
      const res = await api.setProbeMode(next)
      setMode(res.probe_mode)
      showToast('Connectivity probe setting saved', 'success')
    } catch (err) {
      setMode(previous)
      showToast(
        `Failed to save connectivity probe setting — ${err instanceof Error ? err.message : 'unknown error'}`,
        'error',
      )
    } finally {
      setSaving(null)
    }
  }

  return (
    <section
      aria-label="Connectivity Probe"
      className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-6 shadow-sm dark:bg-gray-800 dark:ring-gray-700 space-y-5"
    >
      <header className="flex items-center gap-3">
        <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-[#d6eeff] dark:bg-gray-700">
          <Radar className="h-5 w-5 text-[#0a3a5a] dark:text-[#d6eeff]" aria-hidden />
        </div>
        <div>
          <h4 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
            Connectivity Probe
          </h4>
          <p className="mt-0.5 text-xs text-[#2a5a7a] dark:text-gray-400 max-w-prose">
            How Sharko confirms a newly registered cluster is reachable, before you've deployed a
            real addon to it.
          </p>
        </div>
      </header>

      {loading ? (
        <div
          aria-live="polite"
          className="flex items-center gap-2 text-sm text-[#2a5a7a] dark:text-gray-400"
        >
          <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
          Loading connectivity probe setting…
        </div>
      ) : loadError ? (
        <div
          role="alert"
          className="flex items-center justify-between gap-3 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-400"
        >
          <span>{loadError}</span>
          <button
            type="button"
            onClick={load}
            className="rounded-md border border-red-300 px-2 py-1 text-xs font-medium text-red-700 hover:bg-red-100 dark:border-red-700 dark:text-red-300 dark:hover:bg-red-900/40"
          >
            Retry
          </button>
        </div>
      ) : (
        <div role="radiogroup" aria-label="Connectivity probe mode" className="space-y-2">
          {OPTIONS.map((opt) => {
            const selected = mode === opt.value
            const isSaving = saving === opt.value
            return (
              <label
                key={opt.value}
                className={`flex cursor-pointer items-start gap-3 rounded-lg px-3 py-2.5 ring-1 transition-colors ${
                  selected
                    ? 'ring-2 ring-teal-500 bg-[#e0f0ff] dark:bg-gray-700 dark:ring-teal-500'
                    : 'ring-[#b4dcf5] hover:bg-[#e0f0ff] dark:ring-gray-700 dark:hover:bg-gray-700'
                }`}
              >
                <input
                  type="radio"
                  name="probe-mode"
                  value={opt.value}
                  checked={selected}
                  onChange={() => handleSelect(opt.value)}
                  className="mt-0.5 h-4 w-4 shrink-0 border-[#5a9dd0] text-teal-600 focus:ring-teal-500"
                />
                <div className="flex-1">
                  <span className="flex items-center gap-1.5 text-sm font-medium text-[#0a2a4a] dark:text-gray-100">
                    {opt.title}
                    {isSaving && <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden />}
                    {selected && !isSaving && (
                      <CheckCircle2 className="h-3.5 w-3.5 text-teal-600 dark:text-teal-400" aria-hidden />
                    )}
                  </span>
                  <p className="mt-0.5 text-xs text-[#3a6a8a] dark:text-gray-400">{opt.helper}</p>
                </div>
              </label>
            )
          })}
        </div>
      )}
    </section>
  )
}

export default ProbeModeSection

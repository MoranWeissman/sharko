import { useCallback, useEffect, useState } from 'react'
import { Loader2, Power } from 'lucide-react'
import { api } from '@/services/api'
import { showToast } from '@/components/ToastNotification'

/**
 * Settings → Addon Values Engine (gitops-proud P4-I, D2).
 *
 * Server-wide, admin-only off switch for the addon-values secrets engine —
 * the one that checks and pushes addon secret VALUES (not the cluster
 * connection itself, which has no switch; that engine is Sharko's own job).
 * If another tool already delivers secrets into your clusters (External
 * Secrets Operator, Sealed Secrets, a vault agent, and others), you may
 * prefer to leave this off and let that tool keep doing its job.
 *
 * When off, the engine runs no check or write passes — rows on the Secret
 * Sync page keep showing their last-known facts, and its engine strip says
 * plainly that the engine is switched off.
 *
 * GET /api/v1/settings/addon-values-engine-enabled returns
 * { addon_values_engine_enabled }; PUT with the same shape saves it
 * (admin-only on the backend — 403 for non-admins, so the caller must gate
 * this section with isAdmin before rendering).
 */

export function AddonValuesEngineSection() {
  const [enabled, setEnabled] = useState<boolean | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  const load = useCallback(() => {
    setLoading(true)
    setLoadError(null)
    api
      .getAddonValuesEngineEnabled()
      .then((res) => setEnabled(res.addon_values_engine_enabled))
      .catch((err) => setLoadError(err instanceof Error ? err.message : 'Failed to load'))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    load()
  }, [load])

  async function handleToggle() {
    if (enabled === null || saving) return
    const next = !enabled
    const previous = enabled
    setSaving(true)
    setEnabled(next) // optimistic — reverted on failure
    try {
      const res = await api.setAddonValuesEngineEnabled(next)
      setEnabled(res.addon_values_engine_enabled)
      showToast('Addon values engine setting saved', 'success')
    } catch (err) {
      setEnabled(previous)
      showToast(
        `Failed to save addon values engine setting — ${err instanceof Error ? err.message : 'unknown error'}`,
        'error',
      )
    } finally {
      setSaving(false)
    }
  }

  return (
    <section
      aria-label="Addon Values Engine"
      className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-6 shadow-sm dark:bg-gray-800 dark:ring-gray-700 space-y-5"
    >
      <header className="flex items-center gap-3">
        <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-[#d6eeff] dark:bg-gray-700">
          <Power className="h-5 w-5 text-[#0a3a5a] dark:text-[#d6eeff]" aria-hidden />
        </div>
        <div>
          <h4 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
            Addon Values Engine
          </h4>
          <p className="mt-0.5 text-xs text-[#2a5a7a] dark:text-gray-400 max-w-prose">
            Sharko applies addon secret values from your secrets store to the clusters. If you
            already have a tool that delivers secrets into your clusters (External Secrets
            Operator, Sealed Secrets, a vault agent, and others), you may prefer to leave this
            off. Cluster connection secrets are Sharko's own job and are not affected.
          </p>
        </div>
      </header>

      {loading ? (
        <div
          aria-live="polite"
          className="flex items-center gap-2 text-sm text-[#2a5a7a] dark:text-gray-400"
        >
          <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
          Loading addon values engine setting…
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
        <div className="flex items-center justify-between gap-4 rounded-lg px-3 py-2.5 ring-1 ring-[#b4dcf5] dark:ring-gray-700">
          <div>
            <p className="text-sm font-medium text-[#0a2a4a] dark:text-gray-100">
              Apply addon secret values
            </p>
            <p className="mt-0.5 text-sm text-[#3a6a8a] dark:text-gray-400">
              {enabled
                ? 'Sharko checks and applies addon secret values on its normal schedule.'
                : 'Sharko is not checking or applying addon secret values. Rows on Secret Sync keep showing their last-known facts.'}
            </p>
          </div>
          <label className="flex shrink-0 cursor-pointer items-center gap-2">
            <span className="text-xs text-[#2a5a7a] dark:text-gray-400">
              {enabled ? 'On' : 'Off'}
            </span>
            <button
              type="button"
              role="switch"
              aria-checked={enabled ?? false}
              aria-label="Apply addon secret values"
              onClick={handleToggle}
              disabled={saving}
              className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors focus:outline-none disabled:cursor-not-allowed disabled:opacity-60 ${
                enabled ? 'bg-[#1a6aaa]' : 'bg-[#c0ddf0] dark:bg-gray-600'
              }`}
            >
              {saving ? (
                <Loader2 className="mx-auto h-3.5 w-3.5 animate-spin text-white" aria-hidden />
              ) : (
                <span
                  className={`inline-block h-3.5 w-3.5 rounded-full bg-white shadow transition-transform ${
                    enabled ? 'translate-x-4' : 'translate-x-1'
                  }`}
                />
              )}
            </button>
          </label>
        </div>
      )}
    </section>
  )
}

export default AddonValuesEngineSection

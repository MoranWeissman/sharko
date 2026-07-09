import { useCallback, useEffect, useState } from 'react'
import { Loader2, Lock } from 'lucide-react'
import { api } from '@/services/api'
import { showToast } from '@/components/ToastNotification'

/**
 * Settings → Inline Credentials (V2-cleanup-89.6).
 *
 * Server-wide, admin-only kill switch for the "Paste a kubeconfig" cluster
 * registration path. Sharko has no user RBAC today — there is a single
 * admin login, so this is necessarily an install-wide switch. When V2.x
 * scoped RBAC lands this is expected to become a per-role permission
 * instead.
 *
 * GET /api/v1/settings/allow-inline-credentials returns
 * { allow_inline_credentials }; PUT with the same shape saves it
 * (admin-only on the backend — 403 for non-admins, so the caller must gate
 * this section with isAdmin before rendering).
 */

export function InlineCredentialsSection() {
  const [allow, setAllow] = useState<boolean | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  const load = useCallback(() => {
    setLoading(true)
    setLoadError(null)
    api
      .getAllowInlineCredentials()
      .then((res) => setAllow(res.allow_inline_credentials))
      .catch((err) => setLoadError(err instanceof Error ? err.message : 'Failed to load'))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    load()
  }, [load])

  async function handleToggle() {
    if (allow === null || saving) return
    const next = !allow
    const previous = allow
    setSaving(true)
    setAllow(next) // optimistic — reverted on failure
    try {
      const res = await api.setAllowInlineCredentials(next)
      setAllow(res.allow_inline_credentials)
      showToast('Inline credentials setting saved', 'success')
    } catch (err) {
      setAllow(previous)
      showToast(
        `Failed to save inline credentials setting — ${err instanceof Error ? err.message : 'unknown error'}`,
        'error',
      )
    } finally {
      setSaving(false)
    }
  }

  return (
    <section
      aria-label="Inline Credentials"
      className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-6 shadow-sm dark:bg-gray-800 dark:ring-gray-700 space-y-5"
    >
      <header className="flex items-center gap-3">
        <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-[#d6eeff] dark:bg-gray-700">
          <Lock className="h-5 w-5 text-[#0a3a5a] dark:text-[#d6eeff]" aria-hidden />
        </div>
        <div>
          <h4 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
            Inline Credentials
          </h4>
          <p className="mt-0.5 text-xs text-[#2a5a7a] dark:text-gray-400 max-w-prose">
            Allow pasting credentials directly in the UI/API. Turn off to enforce GitOps-clean
            secret-store pointers.
          </p>
        </div>
      </header>

      {loading ? (
        <div
          aria-live="polite"
          className="flex items-center gap-2 text-sm text-[#2a5a7a] dark:text-gray-400"
        >
          <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
          Loading inline credentials setting…
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
              Allow pasting credentials
            </p>
            <p className="mt-0.5 text-xs text-[#3a6a8a] dark:text-gray-400">
              {allow
                ? 'Operators can paste a kubeconfig at registration time.'
                : 'The "Paste a kubeconfig" option is hidden — every registration must point at a secret store.'}
            </p>
          </div>
          <label className="flex shrink-0 cursor-pointer items-center gap-2">
            <span className="text-xs text-[#2a5a7a] dark:text-gray-400">
              {allow ? 'Allowed' : 'Forbidden'}
            </span>
            <button
              type="button"
              role="switch"
              aria-checked={allow ?? false}
              aria-label="Allow pasting credentials"
              onClick={handleToggle}
              disabled={saving}
              className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors focus:outline-none disabled:cursor-not-allowed disabled:opacity-60 ${
                allow ? 'bg-[#1a6aaa]' : 'bg-[#c0ddf0] dark:bg-gray-600'
              }`}
            >
              {saving ? (
                <Loader2 className="mx-auto h-3.5 w-3.5 animate-spin text-white" aria-hidden />
              ) : (
                <span
                  className={`inline-block h-3.5 w-3.5 rounded-full bg-white shadow transition-transform ${
                    allow ? 'translate-x-4' : 'translate-x-1'
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

export default InlineCredentialsSection

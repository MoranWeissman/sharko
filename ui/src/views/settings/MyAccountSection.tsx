import { useState, useEffect, useCallback } from 'react'
import {
  Loader2,
  Github,
  CheckCircle,
  XCircle,
  Trash2,
  Save,
  Eye,
  EyeOff,
} from 'lucide-react'
import { api } from '@/services/api'
import type { MeResponse } from '@/services/models'

const labelCls = 'block text-sm font-medium text-[#0a3a5a] dark:text-gray-300'
const inputCls =
  'mt-1 block w-full rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-2 text-sm text-[#0a2a4a] shadow-sm placeholder:text-[#3a6a8a] focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 dark:placeholder:text-[#2a5a7a]'

/**
 * "My Account" — user-scoped settings.
 *
 * Today this only houses the personal GitHub PAT used by the v1.20 tiered
 * attribution model: when set, Tier 2 (configuration) actions commit as the
 * user instead of the Sharko service account.
 */
export function MyAccountSection() {
  const [me, setMe] = useState<MeResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [token, setToken] = useState('')
  const [revealToken, setRevealToken] = useState(false)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<{ ok: boolean; msg: string } | null>(null)
  const [error, setError] = useState<string | null>(null)

  const refresh = useCallback(() => {
    setLoading(true)
    api
      .getMe()
      .then(setMe)
      .catch((e) => setError(e instanceof Error ? e.message : 'Failed to load profile'))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    refresh()
  }, [refresh])

  const handleSave = async () => {
    if (!token.trim()) return
    setSaving(true)
    setError(null)
    setTestResult(null)
    try {
      await api.setMyGitHubToken(token.trim())
      setToken('')
      refresh()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to save token')
    } finally {
      setSaving(false)
    }
  }

  const handleClear = async () => {
    if (!confirm('Remove your personal GitHub PAT? Tier 2 actions will fall back to the service account.')) return
    setSaving(true)
    setError(null)
    setTestResult(null)
    try {
      await api.clearMyGitHubToken()
      refresh()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to clear token')
    } finally {
      setSaving(false)
    }
  }

  const handleTest = async () => {
    setTesting(true)
    setTestResult(null)
    try {
      const res = await api.testMyGitHubToken()
      setTestResult({ ok: true, msg: `Token valid (GitHub login: ${res.github_login})` })
    } catch (e) {
      setTestResult({ ok: false, msg: e instanceof Error ? e.message : 'Test failed' })
    } finally {
      setTesting(false)
    }
  }

  if (loading) {
    return (
      <div className="flex items-center gap-2 text-sm text-[#3a6a8a] dark:text-gray-400">
        <Loader2 className="h-4 w-4 animate-spin" /> Loading profile…
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold text-[#0a2a4a] dark:text-gray-100">My Account</h2>
        <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-400">
          Settings that apply only to you ({me?.username ?? 'unknown'}).
        </p>
      </div>

      {error && (
        <div className="rounded-md border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">
          {error}
        </div>
      )}

      <div className="rounded-lg border border-[#cfe5f7] bg-white p-4 shadow-sm dark:border-gray-700 dark:bg-gray-800">
        <div className="flex items-start gap-3">
          <Github className="mt-0.5 h-5 w-5 text-[#0a3a5a] dark:text-gray-200" />
          <div className="flex-1">
            <h3 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
              Personal GitHub Token
            </h3>
            <p className="mt-1 text-xs text-[#3a6a8a] dark:text-gray-400">
              Used for Tier 2 (configuration) commits — editing addon catalog metadata or values —
              so the resulting Git commit is authored by you instead of the Sharko service account.
              Stored encrypted at rest. Operational actions (cluster ops, addon enable/disable,
              version upgrades) keep using the service token with a Co-authored-by trailer.
            </p>

            <div className="mt-3 flex items-center gap-2">
              {me?.has_github_token ? (
                <span className="inline-flex items-center gap-1 rounded-full bg-emerald-100 px-2 py-0.5 text-xs font-medium text-emerald-800 dark:bg-emerald-900 dark:text-emerald-300">
                  <CheckCircle className="h-3 w-3" /> Token configured
                </span>
              ) : (
                <span className="inline-flex items-center gap-1 rounded-full bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-800 dark:bg-amber-900 dark:text-amber-300">
                  <XCircle className="h-3 w-3" /> No token set
                </span>
              )}
            </div>

            <div className="mt-4 space-y-3">
              <div>
                <label className={labelCls} htmlFor="gh-token">
                  {me?.has_github_token ? 'Replace token' : 'Add token'}
                </label>
                <div className="relative">
                  <input
                    id="gh-token"
                    type={revealToken ? 'text' : 'password'}
                    autoComplete="off"
                    spellCheck={false}
                    placeholder="ghp_xxx or github_pat_xxx"
                    value={token}
                    onChange={(e) => setToken(e.target.value)}
                    className={inputCls + ' pr-10'}
                  />
                  <button
                    type="button"
                    onClick={() => setRevealToken((v) => !v)}
                    className="absolute right-2 top-1/2 -translate-y-1/2 rounded p-1 text-[#3a6a8a] hover:bg-[#e0eef9] dark:text-gray-400 dark:hover:bg-gray-700"
                    aria-label={revealToken ? 'Hide token' : 'Show token'}
                  >
                    {revealToken ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                  </button>
                </div>
                <p className="mt-1 text-xs text-[#3a6a8a] dark:text-gray-500">
                  Needs <code>repo</code> scope (or fine-grained equivalent: contents read/write,
                  pull requests read/write) on the GitOps repository.
                </p>
              </div>

              <div className="flex items-center gap-2">
                <button
                  type="button"
                  onClick={handleSave}
                  disabled={!token.trim() || saving}
                  className="inline-flex items-center gap-1 rounded-md bg-teal-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-teal-700 disabled:cursor-not-allowed disabled:bg-gray-300"
                >
                  {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}
                  Save token
                </button>

                {me?.has_github_token && (
                  <>
                    <button
                      type="button"
                      onClick={handleTest}
                      disabled={testing}
                      className="inline-flex items-center gap-1 rounded-md border border-[#5a9dd0] bg-white px-3 py-1.5 text-sm font-medium text-[#0a3a5a] hover:bg-[#e0eef9] disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 dark:hover:bg-gray-600"
                    >
                      {testing ? <Loader2 className="h-4 w-4 animate-spin" /> : <CheckCircle className="h-4 w-4" />}
                      Test
                    </button>
                    <button
                      type="button"
                      onClick={handleClear}
                      disabled={saving}
                      className="inline-flex items-center gap-1 rounded-md border border-red-300 bg-white px-3 py-1.5 text-sm font-medium text-red-700 hover:bg-red-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-red-800 dark:bg-gray-800 dark:text-red-400 dark:hover:bg-red-950"
                    >
                      <Trash2 className="h-4 w-4" />
                      Remove
                    </button>
                  </>
                )}
              </div>

              {testResult && (
                <div
                  className={
                    'rounded-md border px-3 py-2 text-sm ' +
                    (testResult.ok
                      ? 'border-emerald-300 bg-emerald-50 text-emerald-800 dark:border-emerald-800 dark:bg-emerald-950 dark:text-emerald-300'
                      : 'border-red-300 bg-red-50 text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300')
                  }
                >
                  {testResult.ok ? (
                    <CheckCircle className="mr-1 inline h-4 w-4" />
                  ) : (
                    <XCircle className="mr-1 inline h-4 w-4" />
                  )}
                  {testResult.msg}
                </div>
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

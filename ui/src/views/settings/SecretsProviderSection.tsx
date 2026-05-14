import { useState, useEffect, useCallback } from 'react'
import { Shield, Globe, Activity, Loader2, CheckCircle, KeyRound } from 'lucide-react'
import { useConnections } from '@/hooks/useConnections'
import { api } from '@/services/api'
import { LoadingState } from '@/components/LoadingState'
import { ErrorState } from '@/components/ErrorState'

// V125-1-10.7: widened to include 'argocd' so admins can pick the
// ArgoCD provider in the Settings dropdown. The 'argocd' type means
// "read cluster credentials from ArgoCD's cluster Secret in the argocd
// namespace" and requires no extra Region/Prefix/Namespace inputs —
// it always uses the in-cluster argocd namespace.
interface ProviderFormData {
  provider_type: '' | 'argocd' | 'aws-sm' | 'k8s-secrets'
  provider_region: string
  provider_prefix: string
}

type ProviderType = ProviderFormData['provider_type']

interface ProviderInfo {
  type: string
  region: string
  prefix?: string
  status: string
  error?: string
}

const labelCls = 'block text-sm font-medium text-[#0a3a5a] dark:text-gray-300'
const inputCls =
  'mt-1 block w-full rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-2 text-sm text-[#0a2a4a] shadow-sm placeholder:text-[#3a6a8a] focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 dark:placeholder:text-[#2a5a7a]'
const selectCls =
  'mt-1 block w-full rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-2 text-sm text-[#0a2a4a] shadow-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100'

export function SecretsProviderSection() {
  const { connections, loading, error, refreshConnections } = useConnections()

  const existingConn = connections.find((c) => c.is_active) ?? connections[0] ?? null

  const [form, setForm] = useState<ProviderFormData>({
    provider_type: '',
    provider_region: '',
    provider_prefix: '',
  })

  const [providerInfo, setProviderInfo] = useState<ProviderInfo | null>(null)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [justSaved, setJustSaved] = useState(false)

  const fetchProviderInfo = useCallback(() => {
    api
      .getProviders()
      .then((data) => {
        if (data.configured_provider) {
          setProviderInfo(data.configured_provider as ProviderInfo)
          const p = data.configured_provider as ProviderInfo
          setForm({
            provider_type: (p.type as ProviderType) || '',
            provider_region: p.region || '',
            provider_prefix: p.prefix || '',
          })
        }
      })
      .catch(() => setProviderInfo(null))
  }, [])

  useEffect(() => {
    fetchProviderInfo()
  }, [fetchProviderInfo])

  async function handleSave() {
    if (!existingConn) return
    setSaving(true)
    setSaveError(null)
    try {
      // Build minimal payload preserving existing connection data
      const connPayload = buildConnectionPayload(existingConn, form)
      await api.updateConnection(existingConn.name, connPayload)
      refreshConnections()
      fetchProviderInfo()
      setJustSaved(true)
      setTimeout(() => setJustSaved(false), 3000)
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'Save failed')
    } finally {
      setSaving(false)
    }
  }

  if (loading) return <LoadingState message="Loading secrets provider..." />
  if (error) return <ErrorState message={error} onRetry={refreshConnections} />

  return (
    <div className="space-y-6">
      {/* Current status card */}
      {providerInfo && (
        <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5 shadow-sm dark:bg-gray-800">
          <p className="mb-3 text-xs font-semibold uppercase tracking-wide text-[#3a6a8a] dark:text-gray-500">Current Configuration</p>
          <dl className="grid grid-cols-1 gap-4 sm:grid-cols-3">
            <div>
              <dt className="flex items-center gap-1.5 text-sm text-[#2a5a7a] dark:text-gray-400">
                <Shield className="h-3.5 w-3.5" />
                Type
              </dt>
              <dd className="mt-1 font-mono text-sm font-medium text-[#0a2a4a] dark:text-gray-100">
                {providerInfo.type}
              </dd>
            </div>
            <div>
              <dt className="flex items-center gap-1.5 text-sm text-[#2a5a7a] dark:text-gray-400">
                <Globe className="h-3.5 w-3.5" />
                Region
              </dt>
              <dd className="mt-1 font-mono text-sm font-medium text-[#0a2a4a] dark:text-gray-100">
                {providerInfo.region || '—'}
              </dd>
            </div>
            <div>
              <dt className="flex items-center gap-1.5 text-sm text-[#2a5a7a] dark:text-gray-400">
                <Activity className="h-3.5 w-3.5" />
                Status
              </dt>
              <dd className="mt-1 flex items-center gap-1.5 text-sm font-medium">
                {providerInfo.status === 'connected' ? (
                  <><span className="inline-block h-2 w-2 rounded-full bg-green-500" /><span className="text-green-600 dark:text-green-400">Connected</span></>
                ) : providerInfo.status === 'configured' ? (
                  <><span className="inline-block h-2 w-2 rounded-full bg-yellow-500" /><span className="text-yellow-600 dark:text-yellow-400">Configured</span></>
                ) : (
                  <>
                    <span className="inline-block h-2 w-2 rounded-full bg-red-500" />
                    <span className="text-red-600 dark:text-red-400">
                      Error{providerInfo.error ? `: ${providerInfo.error}` : ''}
                    </span>
                  </>
                )}
              </dd>
            </div>
          </dl>
        </div>
      )}

      {/* Edit form */}
      {existingConn ? (
        <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-6 shadow-sm dark:bg-gray-800">
          <div className="mb-4 flex items-center gap-2">
            <KeyRound className="h-4 w-4 text-[#2a5a7a]" />
            <h5 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Configure Provider</h5>
          </div>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div className="sm:col-span-2">
              <label className={labelCls}>Provider Type</label>
              <select
                className={selectCls}
                value={form.provider_type}
                onChange={(e) => setForm(prev => ({ ...prev, provider_type: e.target.value as ProviderType }))}
              >
                <option value="">None</option>
                <option value="argocd">ArgoCD (auto — reads cluster credentials from the ArgoCD Secret)</option>
                <option value="aws-sm">AWS Secrets Manager (aws-sm)</option>
                <option value="k8s-secrets">Kubernetes Secrets (k8s-secrets)</option>
              </select>
              {form.provider_type === 'argocd' ? (
                <p className="mt-1 text-[10px] text-[#3a6a8a]">
                  Sharko reads credentials from the ArgoCD cluster Secret it creates during register-cluster. No additional setup required when Sharko runs in-cluster.
                </p>
              ) : (
                <p className="mt-1 text-[10px] text-[#3a6a8a]">How Sharko retrieves cluster credentials for secret-based providers.</p>
              )}
            </div>
            {form.provider_type === 'aws-sm' && (
              <div>
                <label className={labelCls}>Region</label>
                <input
                  className={inputCls}
                  value={form.provider_region}
                  onChange={(e) => setForm(prev => ({ ...prev, provider_region: e.target.value }))}
                  placeholder="e.g. eu-west-1"
                />
              </div>
            )}
            {(form.provider_type === 'aws-sm' || form.provider_type === 'k8s-secrets') && (
              <div>
                <label className={labelCls}>Prefix <span className="text-[#3a6a8a] font-normal">(optional)</span></label>
                <input
                  className={inputCls}
                  value={form.provider_prefix}
                  onChange={(e) => setForm(prev => ({ ...prev, provider_prefix: e.target.value }))}
                  placeholder="e.g. k8s- (prepended to cluster name for SM lookup)"
                />
                <p className="mt-1 text-[10px] text-[#3a6a8a]">Prepended to cluster name when looking up the secret.</p>
              </div>
            )}
          </div>

          {saveError && (
            <p className="mt-3 text-sm text-red-600 dark:text-red-400">{saveError}</p>
          )}

          <div className="mt-5 flex items-center gap-3">
            <button
              type="button"
              onClick={handleSave}
              disabled={saving}
              className="inline-flex items-center gap-1.5 rounded-lg bg-teal-600 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-teal-700 disabled:opacity-50 dark:bg-teal-700 dark:hover:bg-teal-600"
            >
              {saving && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              Save Provider
            </button>
            {justSaved && (
              <span className="flex items-center gap-1 text-xs text-green-600 dark:text-green-400">
                <CheckCircle className="h-3.5 w-3.5" /> Saved
              </span>
            )}
          </div>
        </div>
      ) : (
        <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-6 shadow-sm dark:bg-gray-800">
          <p className="text-sm text-[#2a5a7a] dark:text-gray-400">
            Configure a <span className="font-semibold">Connection</span> first before setting up the secrets provider.
          </p>
        </div>
      )}
    </div>
  )
}

// Build a full connection update payload, preserving existing fields
function buildConnectionPayload(
  conn: { name: string; git_provider: string; git_repo_identifier: string; argocd_server_url: string; argocd_namespace: string },
  providerForm: ProviderFormData
) {
  let gitUrl = ''
  if (conn.git_provider === 'github') {
    gitUrl = `https://github.com/${conn.git_repo_identifier}`
  } else if (conn.git_provider === 'azuredevops') {
    const parts = conn.git_repo_identifier.split('/')
    if (parts.length >= 3) {
      gitUrl = `https://dev.azure.com/${parts[0]}/${parts[1]}/_git/${parts[2]}`
    }
  }
  return {
    name: conn.name,
    git: { repo_url: gitUrl },
    argocd: {
      server_url: conn.argocd_server_url || '',
      namespace: conn.argocd_namespace || 'argocd',
      insecure: true,
    },
    provider: providerForm.provider_type
      ? {
          type: providerForm.provider_type,
          region: providerForm.provider_region || undefined,
          prefix: providerForm.provider_prefix || undefined,
        }
      : undefined,
  }
}

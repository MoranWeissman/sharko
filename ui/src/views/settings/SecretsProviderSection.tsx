import { useState, useEffect, useCallback } from 'react'
import { Shield, Globe, Activity, Loader2, CheckCircle, KeyRound } from 'lucide-react'
import { useConnections } from '@/hooks/useConnections'
import { api } from '@/services/api'
import { LoadingState } from '@/components/LoadingState'
import { ErrorState } from '@/components/ErrorState'
import {
  VALID_PROVIDER_TYPES,
  type ProviderType as GeneratedProviderType,
} from '@/generated/provider-types'

// V125-1-13.7: the dropdown options are now generated from the backend
// factory in internal/providers/provider.go via cmd/gen-provider-types.
// The Settings dropdown can no longer drift from the set of accepted
// provider Type strings — see ui/src/generated/provider-types.ts and
// the "Provider Types Up To Date" CI check. A new arm in providers.New()'s
// switch + `make generate-provider-types` is the only edit required to
// surface a new provider in the UI.
//
// `'' | GeneratedProviderType` because the form's empty value represents
// "no provider configured", which corresponds to providers.New()'s
// auto-default arm — that arm is intentionally filtered out of the
// generated const because it isn't a user-selectable type.
type ProviderType = '' | GeneratedProviderType

interface ProviderFormData {
  provider_type: ProviderType
  provider_region: string
  provider_prefix: string
}

// Per-option display labels. Centralised here (not in the generator) because
// labels are UI copy, not backend contract. The keys are typed against the
// generated `GeneratedProviderType` so adding a new arm to providers.New()
// will produce a TypeScript compile error here — forcing the implementer
// to author a friendly label before the dropdown can ship.
const PROVIDER_LABELS: Record<GeneratedProviderType, string> = {
  argocd: 'ArgoCD (auto — reads cluster credentials from the ArgoCD Secret)',
  'aws-sm': 'AWS Secrets Manager (aws-sm)',
  'aws-secrets-manager': 'AWS Secrets Manager (aws-secrets-manager alias)',
  azure: 'Azure Key Vault (azure)',
  'azure-kv': 'Azure Key Vault (azure-kv alias)',
  'azure-key-vault': 'Azure Key Vault (azure-key-vault alias)',
  gcp: 'GCP Secret Manager (gcp)',
  'gcp-sm': 'GCP Secret Manager (gcp-sm alias)',
  'google-secret-manager': 'GCP Secret Manager (google-secret-manager alias)',
  'k8s-secrets': 'Kubernetes Secrets (k8s-secrets)',
  kubernetes: 'Kubernetes Secrets (kubernetes alias)',
}

// Provider types that surface the AWS-style Region input. AWS Secrets
// Manager is the only provider that requires a region today; this set
// stays a hand-curated UI affordance because the backend has no
// per-provider input-shape metadata to derive from.
const REGION_PROVIDERS: ReadonlySet<GeneratedProviderType> = new Set<GeneratedProviderType>([
  'aws-sm',
  'aws-secrets-manager',
])

// Provider types that surface the Prefix input. Both AWS Secrets Manager
// and the Kubernetes-Secrets backend honour a name prefix.
const PREFIX_PROVIDERS: ReadonlySet<GeneratedProviderType> = new Set<GeneratedProviderType>([
  'aws-sm',
  'aws-secrets-manager',
  'k8s-secrets',
  'kubernetes',
])

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
                {VALID_PROVIDER_TYPES.map((t) => (
                  <option key={t} value={t}>
                    {PROVIDER_LABELS[t]}
                  </option>
                ))}
              </select>
              {form.provider_type === 'argocd' ? (
                <p className="mt-1 text-[10px] text-[#3a6a8a]">
                  Sharko reads credentials from the ArgoCD cluster Secret it creates during register-cluster. No additional setup required when Sharko runs in-cluster.
                </p>
              ) : (
                <p className="mt-1 text-[10px] text-[#3a6a8a]">How Sharko retrieves cluster credentials for secret-based providers.</p>
              )}
            </div>
            {form.provider_type !== '' && REGION_PROVIDERS.has(form.provider_type) && (
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
            {form.provider_type !== '' && PREFIX_PROVIDERS.has(form.provider_type) && (
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

import { useState, useEffect, useCallback } from 'react'
import { Shield, Globe, Activity, Loader2, CheckCircle, KeyRound, ChevronRight } from 'lucide-react'
import { useConnections } from '@/hooks/useConnections'
import { api } from '@/services/api'
import { LoadingState } from '@/components/LoadingState'
import { ErrorState } from '@/components/ErrorState'
import { showToast } from '@/components/ToastNotification'
import { type ProviderType as GeneratedProviderType } from '@/generated/provider-types'

// The set of accepted type STRINGS is generated from the backend factory in
// internal/providers/provider.go via cmd/gen-provider-types — see
// ui/src/generated/provider-types.ts and the "Provider Types Up To Date"
// CI check. The generated union includes every alias each switch arm
// accepts (aws-sm / aws-secrets-manager, kubernetes / k8s-secrets, ...).
// Rendering one dropdown row per accepted STRING showed ~12 rows for 5
// real backends (V2-cleanup-55.2 Bug A), so the dropdown renders one row
// per canonical backend and CANONICAL_TYPE below collapses every alias
// onto its row. The generated file stays untouched — it remains the
// contract for "strings the backend will accept", not for UI copy.

// One canonical value per real backend. This is the value the form saves;
// every canonical value is a member of the generated union, so anything
// the form submits is guaranteed backend-acceptable.
type CanonicalProviderType = 'argocd' | 'aws-sm' | 'k8s-secrets' | 'azure' | 'gcp'

// `'' | CanonicalProviderType` because the form's empty value represents
// "no provider configured", which corresponds to the factory's
// auto-default arm — that arm isn't a user-selectable type.
type ProviderType = '' | CanonicalProviderType

// Sentinel for "the connection's stored provider type doesn't canonicalize
// onto any known row." Before this guard, canonicalizeProviderType's ''
// fallback made an unrecognized stored type indistinguishable from
// genuinely unconfigured — the dropdown silently showed "None", and a
// Save the user thought was a no-op (or unrelated, e.g. saving a region
// tweak) persisted `provider: undefined`, wiping the backend's config
// (L7). Selecting this sentinel keeps the form from claiming "None" and
// makes Save pass the raw stored type through unchanged instead.
const UNKNOWN_PROVIDER = '__unknown__' as const
type FormProviderType = ProviderType | typeof UNKNOWN_PROVIDER

interface ProviderFormData {
  provider_type: FormProviderType
  provider_region: string
  provider_prefix: string
  addon_secret_provider_type: FormProviderType
  addon_secret_provider_region: string
  addon_secret_provider_prefix: string
}

// Maps every backend-accepted type string (including aliases) onto its
// canonical dropdown row. Typed as a TOTAL Record over the generated
// union so a new arm in the backend factory (after `make
// generate-provider-types`) is a TypeScript compile error here until the
// implementer maps it — the same drift guard the old per-string label
// record provided, now at the canonicalisation layer.
const CANONICAL_TYPE: Record<GeneratedProviderType, CanonicalProviderType> = {
  argocd: 'argocd',
  'aws-sm': 'aws-sm',
  'aws-secrets-manager': 'aws-sm',
  azure: 'azure',
  'azure-kv': 'azure',
  'azure-key-vault': 'azure',
  gcp: 'gcp',
  'gcp-sm': 'gcp',
  'google-secret-manager': 'gcp',
  'k8s-secrets': 'k8s-secrets',
  kubernetes: 'k8s-secrets',
}

// Collapses any stored provider type (canonical or alias) onto the
// canonical row so a connection saved under e.g. "kubernetes" still
// selects the "Kubernetes Secrets" row. Unknown / empty input maps to ''
// ("None"). Exported for the dropdown drift-guard test.
export function canonicalizeProviderType(raw: string | undefined | null): ProviderType {
  if (!raw) return ''
  return (CANONICAL_TYPE as Record<string, CanonicalProviderType>)[raw] ?? ''
}

interface ProviderOption {
  value: CanonicalProviderType
  label: string
  // Azure Key Vault and GCP Secret Manager exist as factory arms but
  // their constructors unconditionally return "not yet implemented"
  // (internal/providers/azure.go / gcp.go), and the cluster-creds
  // factory (NewClusterTestProvider) doesn't accept them at all. They
  // render disabled with a "not yet supported" note instead of
  // masquerading as working backends.
  supported: boolean
}

// One row per real backend, human labels. UI copy lives here, not in the
// generator, because labels are presentation — the backend contract is
// only the accepted strings.
const PROVIDER_OPTIONS: readonly ProviderOption[] = [
  { value: 'argocd', label: 'ArgoCD (auto — reads cluster credentials from the ArgoCD Secret)', supported: true },
  { value: 'aws-sm', label: 'AWS Secrets Manager', supported: true },
  { value: 'k8s-secrets', label: 'Kubernetes Secrets', supported: true },
  { value: 'azure', label: 'Azure Key Vault — not yet supported', supported: false },
  { value: 'gcp', label: 'GCP Secret Manager — not yet supported', supported: false },
]

// Addon-secret provider options (argocd is NOT valid here — the backend
// rejects it for addon secrets). The UI never offers "argocd" in the
// addon-secret dropdown.
const ADDON_SECRET_PROVIDER_OPTIONS: readonly ProviderOption[] = [
  { value: 'aws-sm', label: 'AWS Secrets Manager', supported: true },
  { value: 'k8s-secrets', label: 'Kubernetes Secrets', supported: true },
  { value: 'azure', label: 'Azure Key Vault — not yet supported', supported: false },
  { value: 'gcp', label: 'GCP Secret Manager — not yet supported', supported: false },
]

function isSupported(t: ProviderType): boolean {
  return PROVIDER_OPTIONS.find((o) => o.value === t)?.supported ?? true
}

// Provider types that surface the AWS-style Region input. AWS Secrets
// Manager is the only provider that requires a region today; this set
// stays a hand-curated UI affordance because the backend has no
// per-provider input-shape metadata to derive from. Canonical values
// only — the form value is always canonicalized on hydration.
const REGION_PROVIDERS: ReadonlySet<ProviderType> = new Set<ProviderType>(['aws-sm'])

// Provider types that surface the Prefix input. Both AWS Secrets Manager
// and the Kubernetes-Secrets backend honour a name prefix.
const PREFIX_PROVIDERS: ReadonlySet<ProviderType> = new Set<ProviderType>([
  'aws-sm',
  'k8s-secrets',
])

interface ProviderInfo {
  type: string
  region: string
  prefix?: string
  status: string
  error?: string
  addon_secret_status?: 'ok' | 'missing' | 'invalid_argocd'
  addon_secret_message?: string
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
    addon_secret_provider_type: '',
    addon_secret_provider_region: '',
    addon_secret_provider_prefix: '',
  })

  const [providerInfo, setProviderInfo] = useState<ProviderInfo | null>(null)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [justSaved, setJustSaved] = useState(false)
  // Non-null iff the currently hydrated form.provider_type === UNKNOWN_PROVIDER;
  // holds the raw stored string so Save can pass it through unchanged.
  const [unknownRawType, setUnknownRawType] = useState<string | null>(null)
  const [unknownAddonSecretRawType, setUnknownAddonSecretRawType] = useState<string | null>(null)
  const [showAdvanced, setShowAdvanced] = useState(false)
  const [showAddonSecretAdvanced, setShowAddonSecretAdvanced] = useState(false)

  // GET /providers feeds the STATUS card only (type / region / live
  // health). It is deliberately NOT the source the form hydrates from:
  // the server builds that payload without the prefix field
  // (internal/api/system.go handleGetProviders — providerDisplay()
  // returns the prefix but it never enters the JSON), which was the
  // V2-cleanup-55.2 Bug B "saved prefix comes back blank" live bug.
  const fetchProviderInfo = useCallback(() => {
    api
      .getProviders()
      .then((data) => {
        if (data.configured_provider) {
          setProviderInfo(data.configured_provider as ProviderInfo)
        }
      })
      .catch(() => setProviderInfo(null))
  }, [])

  useEffect(() => {
    fetchProviderInfo()
  }, [fetchProviderInfo])

  // Form hydration source of truth: the connection's own stored provider
  // config. GET /connections round-trips type + region + prefix in full
  // (models.ConnectionResponse.Provider), and it's the exact record the
  // Save button writes — so what you saved is what you see when you come
  // back. Types stored under an alias (e.g. "kubernetes") canonicalize
  // onto their dropdown row.
  const connProvider = existingConn?.provider ?? null
  const connAddonSecretProvider = existingConn?.addon_secret_provider ?? null
  const hasConnProvider = connProvider != null
  const connType = connProvider?.type ?? ''
  const connRegion = connProvider?.region ?? ''
  const connPrefix = connProvider?.prefix ?? ''
  const connAddonSecretType = connAddonSecretProvider?.type ?? ''
  const connAddonSecretRegion = connAddonSecretProvider?.region ?? ''
  const connAddonSecretPrefix = connAddonSecretProvider?.prefix ?? ''

  useEffect(() => {
    if (!hasConnProvider) return
    const canonical = canonicalizeProviderType(connType)
    const addonSecretCanonical = canonicalizeProviderType(connAddonSecretType)

    let providerTypeValue: FormProviderType = canonical
    if (connType && canonical === '') {
      // Non-empty stored type that maps to nothing we recognize — do NOT
      // hydrate as "None" (see UNKNOWN_PROVIDER comment above).
      setUnknownRawType(connType)
      providerTypeValue = UNKNOWN_PROVIDER
    } else {
      setUnknownRawType(null)
    }

    let addonSecretTypeValue: FormProviderType = addonSecretCanonical
    if (connAddonSecretType && addonSecretCanonical === '') {
      setUnknownAddonSecretRawType(connAddonSecretType)
      addonSecretTypeValue = UNKNOWN_PROVIDER
    } else {
      setUnknownAddonSecretRawType(null)
    }

    setForm({
      provider_type: providerTypeValue,
      provider_region: connRegion,
      provider_prefix: connPrefix,
      addon_secret_provider_type: addonSecretTypeValue,
      addon_secret_provider_region: connAddonSecretRegion,
      addon_secret_provider_prefix: connAddonSecretPrefix,
    })
    // Auto-expand Advanced if there's a stored prefix (F5)
    setShowAdvanced(!!connPrefix)
    setShowAddonSecretAdvanced(!!connAddonSecretPrefix)
  }, [hasConnProvider, connType, connRegion, connPrefix, connAddonSecretType, connAddonSecretRegion, connAddonSecretPrefix])

  // Fallback for installs where the provider is configured via env vars
  // only (no provider block on the connection): hydrate type/region from
  // the /providers status payload so the form still reflects reality.
  // Prefix is unavailable on that payload (see above) — nothing stored on
  // the connection means nothing to lose.
  useEffect(() => {
    if (hasConnProvider || !providerInfo) return
    const canonical = canonicalizeProviderType(providerInfo.type)
    const fallbackPrefix = providerInfo.prefix || ''

    let providerTypeValue: FormProviderType = canonical
    if (providerInfo.type && canonical === '') {
      setUnknownRawType(providerInfo.type)
      providerTypeValue = UNKNOWN_PROVIDER
    } else {
      setUnknownRawType(null)
    }

    setForm({
      provider_type: providerTypeValue,
      provider_region: providerInfo.region || '',
      provider_prefix: fallbackPrefix,
      addon_secret_provider_type: '',
      addon_secret_provider_region: '',
      addon_secret_provider_prefix: '',
    })
    setShowAdvanced(!!fallbackPrefix)
  }, [hasConnProvider, providerInfo])

  async function handleSave() {
    if (!existingConn) return

    // Validate: when cluster-creds is argocd, addon-secret backend is REQUIRED
    if (form.provider_type === 'argocd' && !form.addon_secret_provider_type) {
      setSaveError('ArgoCD only tells Sharko how to reach your clusters. Choose where addon secret values come from.')
      return
    }
    if (form.provider_type === 'argocd' && form.addon_secret_provider_type === UNKNOWN_PROVIDER) {
      setSaveError('ArgoCD only tells Sharko how to reach your clusters. Choose where addon secret values come from.')
      return
    }

    setSaving(true)
    setSaveError(null)
    try {
      // Build minimal payload preserving existing connection data
      const connPayload = buildConnectionPayload(existingConn, form, unknownRawType, unknownAddonSecretRawType)
      await api.updateConnection(existingConn.name, connPayload)
      // Toast first: refreshConnections() flips the shared loading flag,
      // and feedback rendered inside this section would be swallowed by
      // the transient re-render. The app-wide toast (ToastContainer in
      // Layout) survives it.
      showToast('Secrets provider saved', 'success')
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

  // Only blank the section into a spinner on the INITIAL load (no
  // connection data yet). refreshConnections() after a successful save
  // also sets loading=true; replacing the whole form with LoadingState
  // at that moment was the "click Save and it glitches, nothing happens"
  // bug — the button and its Saved indicator vanished mid-confirmation.
  if (loading && !existingConn) return <LoadingState message="Loading secrets provider..." />
  if (error) return <ErrorState message={error} onRetry={refreshConnections} />

  return (
    <div className="space-y-6">
      {/* F4: Plain-English opener + docs link + positioning line (V3-P1.2) */}
      <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5 shadow-sm dark:ring-gray-700 dark:bg-gray-800">
        <p className="text-sm text-[#2a5a7a] dark:text-gray-300">
          Sharko is a GitOps agent with an API: your portal or pipeline asks for "a cluster with these addons," and Sharko opens a pull request — it never changes your cluster behind your back.
        </p>
        <p className="mt-2 text-sm text-[#2a5a7a] dark:text-gray-300">
          Sharko needs each cluster's credentials to reach it — this is where those credentials come from.
        </p>
        <a
          href="/user-guide/secrets-provider/"
          className="mt-2 inline-flex items-center gap-1.5 text-sm font-medium text-[#0a3a5a] hover:text-teal-600 dark:text-gray-100 dark:hover:text-teal-400"
        >
          Learn how this works
          <ChevronRight className="h-3.5 w-3.5" />
        </a>
      </div>

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

      {/* Edit form — split into two sections (V3-P1.2) */}
      {existingConn ? (
        <>
          {/* Section 1: How Sharko reaches your clusters (cluster-credentials provider) */}
          <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-6 shadow-sm dark:bg-gray-800">
            <div className="mb-4 flex items-center gap-2">
              <KeyRound className="h-4 w-4 text-[#2a5a7a]" />
              <h5 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">How Sharko reaches your clusters</h5>
            </div>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div className="sm:col-span-2">
                <label className={labelCls}>Provider Type</label>
                <select
                  className={selectCls}
                  value={form.provider_type}
                  onChange={(e) => {
                    setUnknownRawType(null)
                    setForm(prev => ({ ...prev, provider_type: e.target.value as ProviderType }))
                  }}
                >
                  <option value="">None</option>
                  {unknownRawType && (
                    <option value={UNKNOWN_PROVIDER} disabled>
                      Unknown provider &quot;{unknownRawType}&quot; (keep as-is)
                    </option>
                  )}
                  {PROVIDER_OPTIONS.map((opt) => (
                    <option key={opt.value} value={opt.value} disabled={!opt.supported}>
                      {opt.label}
                    </option>
                  ))}
                </select>
                {form.provider_type === 'argocd' ? (
                  <p className="mt-1 text-xs text-[#3a6a8a]">
                    Sharko reads credentials from the ArgoCD cluster Secret it creates during register-cluster. No additional setup required when Sharko runs in-cluster.
                  </p>
                ) : form.provider_type === UNKNOWN_PROVIDER ? (
                  <p className="mt-1 text-xs text-amber-600 dark:text-amber-400">
                    This connection's stored provider type ("{unknownRawType}") isn't one Sharko's UI recognizes. Saving now keeps it unchanged — pick a different option above to replace it.
                  </p>
                ) : form.provider_type !== '' && !isSupported(form.provider_type) ? (
                  <p className="mt-1 text-xs text-amber-600 dark:text-amber-400">
                    This provider is not yet supported — Sharko can't retrieve cluster credentials with it. Pick AWS Secrets Manager, Kubernetes Secrets, or ArgoCD.
                  </p>
                ) : (
                  <p className="mt-1 text-xs text-[#3a6a8a]">How Sharko retrieves cluster credentials.</p>
                )}
              </div>
              {form.provider_type !== '' && form.provider_type !== UNKNOWN_PROVIDER && REGION_PROVIDERS.has(form.provider_type) && (
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
              {form.provider_type !== '' && form.provider_type !== UNKNOWN_PROVIDER && PREFIX_PROVIDERS.has(form.provider_type) && (
                <div className="sm:col-span-2">
                  <button
                    type="button"
                    onClick={() => setShowAdvanced(!showAdvanced)}
                    className="flex items-center gap-1.5 text-sm font-medium text-[#2a5a7a] hover:text-[#0a3a5a] dark:text-gray-400 dark:hover:text-gray-200"
                  >
                    <ChevronRight className={`h-3.5 w-3.5 transition-transform ${showAdvanced ? 'rotate-90' : ''}`} />
                    Advanced (optional)
                  </button>
                  {showAdvanced && (
                    <div className="mt-3">
                      <label className={labelCls}>Prefix</label>
                      <input
                        className={inputCls}
                        value={form.provider_prefix}
                        onChange={(e) => setForm(prev => ({ ...prev, provider_prefix: e.target.value }))}
                        placeholder="e.g. k8s- (prepended to cluster name for SM lookup)"
                      />
                      <p className="mt-1 text-xs text-[#3a6a8a] dark:text-gray-400">Prepended to cluster name when looking up the secret.</p>
                    </div>
                  )}
                </div>
              )}
            </div>
          </div>

          {/* Section 2: Where addon secret values come from (addon-secret provider) */}
          <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-6 shadow-sm dark:bg-gray-800">
            <div className="mb-4 flex items-center gap-2">
              <KeyRound className="h-4 w-4 text-[#2a5a7a]" />
              <h5 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Where addon secret values come from</h5>
            </div>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div className="sm:col-span-2">
                <label className={labelCls}>Addon Secret Provider Type</label>
                <select
                  className={selectCls}
                  value={form.addon_secret_provider_type}
                  onChange={(e) => {
                    setUnknownAddonSecretRawType(null)
                    setForm(prev => ({ ...prev, addon_secret_provider_type: e.target.value as ProviderType }))
                  }}
                >
                  <option value="">None</option>
                  {unknownAddonSecretRawType && (
                    <option value={UNKNOWN_PROVIDER} disabled>
                      Unknown provider &quot;{unknownAddonSecretRawType}&quot; (keep as-is)
                    </option>
                  )}
                  {ADDON_SECRET_PROVIDER_OPTIONS.map((opt) => (
                    <option key={opt.value} value={opt.value} disabled={!opt.supported}>
                      {opt.label}
                    </option>
                  ))}
                </select>
                {form.provider_type === 'argocd' && !form.addon_secret_provider_type ? (
                  <p className="mt-1 text-xs text-amber-600 dark:text-amber-400">
                    ArgoCD only tells Sharko how to reach your clusters. Choose where addon secret values come from.
                  </p>
                ) : form.addon_secret_provider_type === UNKNOWN_PROVIDER ? (
                  <p className="mt-1 text-xs text-amber-600 dark:text-amber-400">
                    This connection's stored addon-secret provider type ("{unknownAddonSecretRawType}") isn't one Sharko's UI recognizes. Saving now keeps it unchanged — pick a different option above to replace it.
                  </p>
                ) : form.addon_secret_provider_type !== '' && !isSupported(form.addon_secret_provider_type) ? (
                  <p className="mt-1 text-xs text-amber-600 dark:text-amber-400">
                    This provider is not yet supported — Sharko can't retrieve addon secrets with it. Pick AWS Secrets Manager or Kubernetes Secrets.
                  </p>
                ) : (
                  <p className="mt-1 text-xs text-[#3a6a8a]">Where Sharko fetches addon secret values (credentials, API keys, etc.).</p>
                )}
                {providerInfo?.addon_secret_status && providerInfo.addon_secret_status !== 'ok' && providerInfo.addon_secret_message && (
                  <p className="mt-1 text-xs text-amber-600 dark:text-amber-400">
                    {providerInfo.addon_secret_message}
                  </p>
                )}
              </div>
              {form.addon_secret_provider_type !== '' && form.addon_secret_provider_type !== UNKNOWN_PROVIDER && REGION_PROVIDERS.has(form.addon_secret_provider_type) && (
                <div>
                  <label className={labelCls}>Region</label>
                  <input
                    className={inputCls}
                    value={form.addon_secret_provider_region}
                    onChange={(e) => setForm(prev => ({ ...prev, addon_secret_provider_region: e.target.value }))}
                    placeholder="e.g. eu-west-1"
                  />
                </div>
              )}
              {form.addon_secret_provider_type !== '' && form.addon_secret_provider_type !== UNKNOWN_PROVIDER && PREFIX_PROVIDERS.has(form.addon_secret_provider_type) && (
                <div className="sm:col-span-2">
                  <button
                    type="button"
                    onClick={() => setShowAddonSecretAdvanced(!showAddonSecretAdvanced)}
                    className="flex items-center gap-1.5 text-sm font-medium text-[#2a5a7a] hover:text-[#0a3a5a] dark:text-gray-400 dark:hover:text-gray-200"
                  >
                    <ChevronRight className={`h-3.5 w-3.5 transition-transform ${showAddonSecretAdvanced ? 'rotate-90' : ''}`} />
                    Advanced (optional)
                  </button>
                  {showAddonSecretAdvanced && (
                    <div className="mt-3">
                      <label className={labelCls}>Prefix</label>
                      <input
                        className={inputCls}
                        value={form.addon_secret_provider_prefix}
                        onChange={(e) => setForm(prev => ({ ...prev, addon_secret_provider_prefix: e.target.value }))}
                        placeholder="e.g. addon- (prepended to secret name for SM lookup)"
                      />
                      <p className="mt-1 text-xs text-[#3a6a8a] dark:text-gray-400">Prepended to secret name when looking up addon secrets.</p>
                    </div>
                  )}
                </div>
              )}
            </div>
          </div>

          {/* Save button (shared across both sections) */}
          <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-6 shadow-sm dark:bg-gray-800">
            {saveError && (
              <p className="mb-3 text-sm text-red-600 dark:text-red-400">{saveError}</p>
            )}
            <div className="flex items-center gap-3">
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
        </>
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

// Build a full connection update payload, preserving existing fields.
// `unknownRawType` is non-null iff the form is showing the "unknown
// provider, keep as-is" sentinel (UNKNOWN_PROVIDER) — in that case the
// original stored type string is written back unchanged instead of the
// sentinel itself, so Save can never silently wipe an unrecognized
// provider config just because the UI didn't have a row for it (L7).
function buildConnectionPayload(
  conn: { name: string; git_provider: string; git_repo_identifier: string; argocd_server_url: string; argocd_namespace: string },
  providerForm: ProviderFormData,
  unknownRawType: string | null,
  unknownAddonSecretRawType: string | null
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
  const resolvedProviderType =
    providerForm.provider_type === UNKNOWN_PROVIDER ? unknownRawType : providerForm.provider_type
  const resolvedAddonSecretProviderType =
    providerForm.addon_secret_provider_type === UNKNOWN_PROVIDER ? unknownAddonSecretRawType : providerForm.addon_secret_provider_type
  return {
    name: conn.name,
    git: { repo_url: gitUrl },
    argocd: {
      server_url: conn.argocd_server_url || '',
      namespace: conn.argocd_namespace || 'argocd',
      insecure: true,
    },
    provider: resolvedProviderType
      ? {
          type: resolvedProviderType,
          region: providerForm.provider_region || undefined,
          prefix: providerForm.provider_prefix || undefined,
        }
      : undefined,
    addon_secret_provider: resolvedAddonSecretProviderType
      ? {
          type: resolvedAddonSecretProviderType,
          region: providerForm.addon_secret_provider_region || undefined,
          prefix: providerForm.addon_secret_provider_prefix || undefined,
        }
      : undefined,
  }
}

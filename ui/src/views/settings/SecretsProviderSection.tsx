import { useState, useEffect, useCallback } from 'react'
import { Shield, Globe, Activity, Loader2, CheckCircle, XCircle, KeyRound, ChevronRight } from 'lucide-react'
import { useConnections } from '@/hooks/useConnections'
import { api } from '@/services/api'
import type { Cluster as ClusterInfo } from '@/services/models'
import { LoadingState } from '@/components/LoadingState'
import { ErrorState } from '@/components/ErrorState'
import { ConfirmationModal } from '@/components/ConfirmationModal'
import { showToast } from '@/components/ToastNotification'
import {
  type ProviderType,
  PROVIDER_OPTIONS,
  ADDON_SECRET_PROVIDER_OPTIONS,
  REGION_PROVIDERS,
  PREFIX_PROVIDERS,
  SECRETS_PROVIDER_DOC_URL,
  backendLabel,
  canonicalizeProviderType,
} from '@/lib/secretsBackends'
import { buildConnectionUpdatePayload } from './connectionUpdate'

// The list of backends, their names and the alias mapping live in
// '@/lib/secretsBackends', so the first-run wizard shows the same list.
export { canonicalizeProviderType }

// Sentinel for "the connection's stored provider type doesn't canonicalize
// onto any known row." Before this guard, canonicalizeProviderType's ''
// fallback made an unrecognized stored type indistinguishable from
// genuinely unconfigured — the dropdown silently showed "None", and a
// Save the user thought was a no-op (or unrelated, e.g. saving a region
// tweak) persisted `provider: undefined`, wiping the backend's config
// (L7). Selecting this sentinel keeps the form from claiming "None" and
// makes Save pass the raw stored type through unchanged instead.
/**
 * The empty choice for the addon secrets backend. Leaving it empty does not
 * mean "no backend": the server uses the cluster backend for addon secret
 * values too (internal/providers AddonSecretConfigFromConnection).
 */
export const ADDON_BACKEND_SAME_AS_CLUSTER_LABEL = 'Same as the cluster backend'

/**
 * Said before saving a different backend, when some clusters read their login
 * from the one that is in use now. Pinned word for word in the tests.
 */
export const SWITCH_BACKEND_TITLE = 'Switch the secrets backend?'
export function switchBackendWarning(count: number): string {
  return count === 1
    ? '1 cluster reads its login from the current backend. After you switch, Sharko will look for that login in the new backend. Copy it there first, or this cluster will stop working the next time Sharko rebuilds its connection.'
    : `${count} clusters read their login from the current backend. After you switch, Sharko will look for those logins in the new backend. Copy them there first, or these clusters will stop working the next time Sharko rebuilds their connection.`
}
/** Said instead, when Sharko could not find out which clusters would be hit. */
export const SWITCH_BACKEND_UNKNOWN_WARNING =
  'Sharko could not check which clusters read their login from the current backend. If some do, copy their logins to the new backend first, or they will stop working the next time Sharko rebuilds their connection.'
export const SWITCH_BACKEND_CONFIRM = 'Switch backend'

/**
 * Clusters whose login Sharko reads out of the secrets backend. A kubeconfig
 * cluster keeps its whole login there. An EKS-token cluster keeps its stored
 * EKS details there (address, CA, region, role), and Sharko makes the token
 * from them (internal/providers routing: both sources are fetched from the
 * backend). Both stop working if the new backend does not hold them.
 */
const BACKEND_LOGIN_SOURCES = ['secret-kubeconfig', 'eks-token']
/**
 * A cluster with no recorded login source (an older entry) is tried against the
 * backend first (internal/providers routing: the "" case), so it counts too.
 */
function readsFromBackend(credsSource: string | undefined): boolean {
  return !credsSource || BACKEND_LOGIN_SOURCES.includes(credsSource)
}
/** Said beside a cluster whose login source was never recorded. */
export const SWITCH_BACKEND_UNRECORDED_NOTE = 'login source not recorded, tried in the backend first'

/** The server's most clusters per page (internal/api/pagination.go). Kept local so tests that mock the api keep working. */
const CLUSTERS_PAGE_SIZE = 100

/** Every cluster, all pages (the server gives at most 100 at a time). */
async function listAllClusters() {
  const all: ClusterInfo[] = []
  for (let page = 1; page <= 1000; page++) {
    const { data, total } = await api.getClustersPage(page, CLUSTERS_PAGE_SIZE)
    const got = data.clusters ?? []
    all.push(...got)
    if (got.length < CLUSTERS_PAGE_SIZE || (total !== null && all.length >= total)) break
  }
  return all
}
/** Said beside an EKS-token cluster in the list, so the reader knows what is read. */
export const SWITCH_BACKEND_EKS_NOTE = 'stored EKS details'

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

function isSupported(t: ProviderType): boolean {
  return PROVIDER_OPTIONS.find((o) => o.value === t)?.supported ?? true
}

// A region is only sent when the provider uses one. A region left over from
// switching away from AWS Secrets Manager is dropped. An unknown provider is
// kept as-is, so Save never wipes a config this screen does not know about.
function regionToSend(type: ProviderType | typeof UNKNOWN_PROVIDER, region: string): string | undefined {
  if (type === UNKNOWN_PROVIDER) return region || undefined
  return REGION_PROVIDERS.has(type) ? region || undefined : undefined
}

// A prefix is only sent when the provider uses one. A value left over from
// switching to Kubernetes Secrets is dropped. An unknown provider is kept
// as-is, so Save never wipes a config this screen does not know about.
function prefixToSend(type: ProviderType | typeof UNKNOWN_PROVIDER, prefix: string): string | undefined {
  if (type === UNKNOWN_PROVIDER) return prefix || undefined
  return PREFIX_PROVIDERS.has(type) ? prefix || undefined : undefined
}

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

  // Explicit "Test" action (v4-wave2 8.1) — separate from the passive
  // status card above, which only reflects the health as of the last page
  // load. This calls POST /providers/test on demand and shows a plain-words
  // pass/fail result, the same contract Test Git / Test ArgoCD use in
  // ConnectionSection.
  const [vaultTest, setVaultTest] = useState<'idle' | 'testing' | 'ok' | 'error'>('idle')
  const [vaultTestMessage, setVaultTestMessage] = useState<string | undefined>()

  async function handleTestVault() {
    setVaultTest('testing')
    setVaultTestMessage(undefined)
    try {
      const res = await api.testProvider()
      setVaultTest(res.status === 'connected' || res.status === 'ok' ? 'ok' : 'error')
      setVaultTestMessage(res.message)
    } catch (err) {
      setVaultTest('error')
      setVaultTestMessage(err instanceof Error ? err.message : 'Test failed')
    }
  }

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

  // Set while the "switch the backend?" question is on screen. `names` is the
  // clusters that read their login from the current backend; null means Sharko
  // could not find out.
  const [switchWarning, setSwitchWarning] = useState<{ names: string[] | null; eks: string[]; unrecorded: string[] } | null>(null)

  async function handleSave() {
    if (!existingConn) return
    // Switching to a different backend: ask first when clusters depend on the
    // one in use now. Only the backend itself counts, not region or prefix.
    const savedType = existingConn.provider?.type ?? providerInfo?.type ?? ''
    const savedBackend = canonicalizeProviderType(savedType)
    if (savedBackend !== '' && form.provider_type !== UNKNOWN_PROVIDER && form.provider_type !== savedBackend) {
      let names: string[] | null
      let eks: string[] = []
      let unrecorded: string[] = []
      try {
        const hit = (await listAllClusters()).filter((c) => c.managed !== false && readsFromBackend(c.creds_source))
        names = hit.map((c) => c.name)
        eks = hit.filter((c) => c.creds_source === 'eks-token').map((c) => c.name)
        unrecorded = hit.filter((c) => !c.creds_source).map((c) => c.name)
      } catch {
        names = null
      }
      if (names === null || names.length > 0) {
        setSwitchWarning({ names, eks, unrecorded })
        return
      }
    }
    await saveNow()
  }

  async function saveNow() {
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
      const connPayload = buildSecretsProviderPayload(
        existingConn,
        form,
        unknownRawType,
        unknownAddonSecretRawType,
        existingConn.provider ?? null,
        existingConn.addon_secret_provider ?? null,
      )
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
      {/* Short scope sentence + docs link. The product-introduction
        * paragraph that used to open this section (UI copy pass) is gone —
        * a user already inside Settings doesn't need Sharko's elevator
        * pitch repeated to them. */}
      <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5 shadow-sm dark:ring-gray-700 dark:bg-gray-800">
        <p className="text-sm text-[#2a5a7a] dark:text-gray-300">
          Choose where Sharko reads cluster credentials and addon secret values.
        </p>
        <a
          href={SECRETS_PROVIDER_DOC_URL}
          target="_blank"
          rel="noopener noreferrer"
          className="mt-2 inline-flex items-center gap-1.5 text-sm font-medium text-[#0a3a5a] hover:text-teal-600 dark:text-gray-100 dark:hover:text-teal-400"
        >
          Learn how this works
          <ChevronRight className="h-3.5 w-3.5" />
        </a>
      </div>

      <ConfirmationModal
        open={switchWarning !== null}
        onClose={() => setSwitchWarning(null)}
        onConfirm={() => {
          setSwitchWarning(null)
          void saveNow()
        }}
        title={SWITCH_BACKEND_TITLE}
        description={
          switchWarning?.names ? switchBackendWarning(switchWarning.names.length) : SWITCH_BACKEND_UNKNOWN_WARNING
        }
        confirmText={SWITCH_BACKEND_CONFIRM}
        destructive
        extraContent={
          switchWarning?.names ? (
            <ul className="mt-3 list-disc space-y-0.5 pl-5 text-sm text-[#0a2a4a] dark:text-gray-200" data-testid="switch-backend-clusters">
              {switchWarning.names.map((n) => (
                <li key={n}>
                  <span className="font-mono">{n}</span>
                  {switchWarning.eks.includes(n) ? ` (${SWITCH_BACKEND_EKS_NOTE})` : ''}
                  {switchWarning.unrecorded.includes(n) ? ` (${SWITCH_BACKEND_UNRECORDED_NOTE})` : ''}
                </li>
              ))}
            </ul>
          ) : undefined
        }
      />

      {/* Current status card */}
      {(
        <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5 shadow-sm dark:bg-gray-800" data-testid="current-configuration">
          <p className="mb-3 text-xs font-semibold uppercase tracking-wide text-[#3a6a8a] dark:text-gray-500">Current Configuration</p>
          <dl className="grid grid-cols-1 gap-4 sm:grid-cols-3">
            <div>
              <dt className="flex items-center gap-1.5 text-sm text-[#2a5a7a] dark:text-gray-400">
                <Shield className="h-3.5 w-3.5" />
                Type
              </dt>
              <dd className="mt-1 font-mono text-sm font-medium text-[#0a2a4a] dark:text-gray-100">
                {providerInfo?.type ? backendLabel(providerInfo.type) : 'Not set'}
              </dd>
            </div>
            <div>
              <dt className="flex items-center gap-1.5 text-sm text-[#2a5a7a] dark:text-gray-400">
                <Globe className="h-3.5 w-3.5" />
                Region
              </dt>
              <dd className="mt-1 font-mono text-sm font-medium text-[#0a2a4a] dark:text-gray-100">
                {providerInfo?.type ? providerInfo.region || '—' : '—'}
              </dd>
            </div>
            <div>
              <dt className="flex items-center gap-1.5 text-sm text-[#2a5a7a] dark:text-gray-400">
                <Activity className="h-3.5 w-3.5" />
                Status
              </dt>
              <dd className="mt-1 flex items-center gap-1.5 text-sm font-medium">
                {!providerInfo?.type ? (
                  <span className="text-[#3a6a8a] dark:text-gray-400">Not set</span>
                ) : providerInfo.status === 'connected' ? (
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
          {providerInfo?.type && (
          <div className="mt-4 flex items-center gap-3 border-t border-[#6aade0]/40 pt-4 dark:border-gray-700">
            <button
              type="button"
              onClick={() => { void handleTestVault() }}
              disabled={vaultTest === 'testing'}
              className="inline-flex items-center gap-1.5 rounded-lg border border-[#5a9dd0] px-3 py-1.5 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
            >
              {vaultTest === 'testing' ? <Loader2 className="h-3 w-3 animate-spin" /> : <Shield className="h-3 w-3" />}
              Test Secrets Store
            </button>
            {vaultTest === 'ok' && (
              <span className="flex items-center gap-1 text-sm text-green-600 dark:text-green-400">
                <CheckCircle className="h-3.5 w-3.5" />
                {vaultTestMessage || 'Connected'}
              </span>
            )}
            {vaultTest === 'error' && (
              <span className="flex items-center gap-1 text-sm text-red-600 dark:text-red-400">
                <XCircle className="h-3.5 w-3.5" />
                {vaultTestMessage || 'Failed'}
              </span>
            )}
          </div>
          )}
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
                    const next = e.target.value as ProviderType
                    setForm(prev => ({
                      ...prev,
                      provider_type: next,
                      // A region only belongs to AWS Secrets Manager. Clear it
                      // when switching to anything else.
                      provider_region: REGION_PROVIDERS.has(next) ? prev.provider_region : '',
                    }))
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
                  <p className="mt-1 text-sm text-[#3a6a8a]">
                    Sharko reads each cluster's login from the secret ArgoCD already keeps for it. Nothing else to set up when Sharko runs inside the cluster.
                  </p>
                ) : form.provider_type === UNKNOWN_PROVIDER ? (
                  <p className="mt-1 text-sm text-amber-600 dark:text-amber-400">
                    This connection's stored provider type ("{unknownRawType}") isn't one Sharko's UI recognizes. Saving now keeps it unchanged — pick a different option above to replace it.
                  </p>
                ) : form.provider_type !== '' && !isSupported(form.provider_type) ? (
                  <p className="mt-1 text-sm text-amber-600 dark:text-amber-400">
                    This provider is not yet supported — Sharko can't retrieve cluster credentials with it. Pick AWS Secrets Manager, Kubernetes Secrets, or ArgoCD.
                  </p>
                ) : (
                  <p className="mt-1 text-sm text-[#3a6a8a]">How Sharko retrieves cluster credentials.</p>
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
                        placeholder="e.g. k8s- (added before the cluster name)"
                      />
                      <p className="mt-1 text-sm text-[#3a6a8a] dark:text-gray-400">Added before the cluster name when Sharko looks for its secret.</p>
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
                    const next = e.target.value as ProviderType
                    setForm(prev => ({
                      ...prev,
                      addon_secret_provider_type: next,
                      addon_secret_provider_region: REGION_PROVIDERS.has(next) ? prev.addon_secret_provider_region : '',
                    }))
                  }}
                >
                  <option value="">{ADDON_BACKEND_SAME_AS_CLUSTER_LABEL}</option>
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
                  <p className="mt-1 text-sm text-amber-600 dark:text-amber-400">
                    ArgoCD only tells Sharko how to reach your clusters. Choose where addon secret values come from.
                  </p>
                ) : form.addon_secret_provider_type === UNKNOWN_PROVIDER ? (
                  <p className="mt-1 text-sm text-amber-600 dark:text-amber-400">
                    This connection's stored addon-secret provider type ("{unknownAddonSecretRawType}") isn't one Sharko's UI recognizes. Saving now keeps it unchanged — pick a different option above to replace it.
                  </p>
                ) : form.addon_secret_provider_type !== '' && !isSupported(form.addon_secret_provider_type) ? (
                  <p className="mt-1 text-sm text-amber-600 dark:text-amber-400">
                    This provider is not yet supported — Sharko can't retrieve addon secrets with it. Pick AWS Secrets Manager or Kubernetes Secrets.
                  </p>
                ) : (
                  <p className="mt-1 text-sm text-[#3a6a8a]">Where Sharko fetches addon secret values (credentials, API keys, etc.).</p>
                )}
                {providerInfo?.addon_secret_status && providerInfo.addon_secret_status !== 'ok' && providerInfo.addon_secret_message && (
                  <p className="mt-1 text-sm text-amber-600 dark:text-amber-400">
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
                        placeholder="e.g. addon- (added before the secret name)"
                      />
                      <p className="mt-1 text-sm text-[#3a6a8a] dark:text-gray-400">Added before the secret name when Sharko looks for addon secrets.</p>
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
                <span className="flex items-center gap-1 text-sm text-green-600 dark:text-green-400">
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

// An empty type is how the server is told to REMOVE a saved backend. Leaving
// the block out means "do not touch it", so "None" has to say it out loud.
const NO_BACKEND = { type: '' }

// The saved block as the server returns it. This screen has no fields for
// the namespace or the role, so it sends the saved ones back unchanged as
// long as the backend is still the same one. A different backend starts clean.
interface StoredProviderBlock {
  type: string
  namespace?: string
  role_arn?: string
}

function keptHiddenFields(
  newType: string,
  stored: StoredProviderBlock | null
): { namespace?: string; role_arn?: string } {
  if (!stored || !newType) return {}
  const newCanon = canonicalizeProviderType(newType)
  const same = newCanon ? newCanon === canonicalizeProviderType(stored.type) : newType === stored.type
  if (!same) return {}
  return {
    ...(stored.namespace ? { namespace: stored.namespace } : {}),
    ...(stored.role_arn ? { role_arn: stored.role_arn } : {}),
  }
}

// Build the connection update payload for this section, on top of the
// shared minimal-payload helper (see connectionUpdate.ts). This page never
// touches git or argocd identity — only the provider / addon_secret_provider
// blocks — so those are the only extras layered onto the shared
// { name, git: { provider } } base.
//
// `unknownRawType` is non-null iff the form is showing the "unknown
// provider, keep as-is" sentinel (UNKNOWN_PROVIDER) — in that case the
// original stored type string is written back unchanged instead of the
// sentinel itself, so Save can never silently wipe an unrecognized
// provider config just because the UI didn't have a row for it (L7).
function buildSecretsProviderPayload(
  conn: { name: string; git_provider: string },
  providerForm: ProviderFormData,
  unknownRawType: string | null,
  unknownAddonSecretRawType: string | null,
  storedProvider: StoredProviderBlock | null,
  storedAddonSecretProvider: StoredProviderBlock | null
) {
  const hadProvider = storedProvider != null
  const hadAddonSecretProvider = storedAddonSecretProvider != null
  const resolvedProviderType =
    providerForm.provider_type === UNKNOWN_PROVIDER ? unknownRawType : providerForm.provider_type
  const resolvedAddonSecretProviderType =
    providerForm.addon_secret_provider_type === UNKNOWN_PROVIDER ? unknownAddonSecretRawType : providerForm.addon_secret_provider_type
  return buildConnectionUpdatePayload(conn, {
    provider: resolvedProviderType
      ? {
          type: resolvedProviderType,
          region: regionToSend(providerForm.provider_type, providerForm.provider_region),
          prefix: prefixToSend(providerForm.provider_type, providerForm.provider_prefix),
          ...keptHiddenFields(resolvedProviderType, storedProvider),
        }
      : hadProvider
        ? NO_BACKEND
        : undefined,
    addon_secret_provider: resolvedAddonSecretProviderType
      ? {
          type: resolvedAddonSecretProviderType,
          region: regionToSend(providerForm.addon_secret_provider_type, providerForm.addon_secret_provider_region),
          prefix: prefixToSend(providerForm.addon_secret_provider_type, providerForm.addon_secret_provider_prefix),
          ...keptHiddenFields(resolvedAddonSecretProviderType, storedAddonSecretProvider),
        }
      : hadAddonSecretProvider
        ? NO_BACKEND
        : undefined,
  })
}

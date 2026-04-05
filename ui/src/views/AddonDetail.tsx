import { useState, useEffect, useMemo, useCallback } from 'react'
import { useParams, useNavigate, Link, useSearchParams } from 'react-router-dom'
import { DetailNavPanel } from '@/components/DetailNavPanel'
import {
  ArrowLeft,
  Search,
  CheckCircle,
  AlertTriangle,
  XCircle,
  Ban,
  ExternalLink,
  Activity,
  Trash2,
  ArrowUpCircle,
  Loader2,
  LayoutGrid,
  Server,
  FileCode,
} from 'lucide-react'
import { api, removeAddon, upgradeAddon } from '@/services/api'
import type { AddonCatalogItem, ConnectionsListResponse } from '@/services/models'
import { StatCard } from '@/components/StatCard'
import { StatusBadge } from '@/components/StatusBadge'
import { LoadingState } from '@/components/LoadingState'
import { ErrorState } from '@/components/ErrorState'
import { YamlViewer } from '@/components/YamlViewer'
import { RoleGuard } from '@/components/RoleGuard'
import { ConfirmationModal } from '@/components/ConfirmationModal'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'

function UpgradeVersionList({
  addonName,
  currentVersion,
  onUpgrade,
}: {
  addonName: string
  currentVersion: string
  onUpgrade: (version: string) => void
}) {
  const [versions, setVersions] = useState<{ version: string; app_version?: string }[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    api
      .getUpgradeVersions(addonName)
      .then((data) => {
        // Filter out the current version — only show newer/different versions
        const available = (data.versions ?? []).filter((v) => v.version !== currentVersion)
        setVersions(available)
      })
      .catch(() => {
        setError('Could not check for available upgrades')
      })
      .finally(() => setLoading(false))
  }, [addonName, currentVersion])

  if (loading) return <LoadingState message="Checking for upgrades..." />

  if (error) {
    return (
      <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5">
        <h3 className="text-base font-semibold text-[#0a2a4a]">Available Upgrades</h3>
        <p className="mt-2 text-sm text-[#2a5a7a]">{error}</p>
        <p className="mt-1 text-xs text-[#3a6a8a]">
          The upgrade versions API may not be configured. You can still upgrade manually using the
          dialog above.
        </p>
      </div>
    )
  }

  if (versions.length === 0) {
    return (
      <div className="rounded-xl ring-2 ring-green-300 bg-green-50 p-5">
        <h3 className="text-base font-semibold text-green-800">Up to date</h3>
        <p className="mt-1 text-sm text-green-700">
          No newer versions available for {addonName}.
        </p>
      </div>
    )
  }

  return (
    <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5">
      <h3 className="mb-3 text-base font-semibold text-[#0a2a4a]">Available Upgrades</h3>
      <div className="space-y-2">
        {versions.map((v, i) => (
          <div
            key={v.version}
            className="flex items-center justify-between rounded-lg bg-[#e0f0ff] px-4 py-3"
          >
            <div className="flex items-center gap-2">
              <span className="font-mono text-sm font-bold text-[#0a2a4a]">{v.version}</span>
              {i === 0 && (
                <span className="rounded-full bg-green-100 px-2 py-0.5 text-[10px] font-semibold text-green-700">
                  LATEST
                </span>
              )}
              {v.app_version && (
                <span className="text-xs text-[#3a6a8a]">app {v.app_version}</span>
              )}
            </div>
            <RoleGuard adminOnly>
              <button
                type="button"
                onClick={() => onUpgrade(v.version)}
                className="rounded-lg bg-[#0a2a4a] px-4 py-1.5 text-xs font-medium text-white hover:bg-[#14466e]"
              >
                Upgrade
              </button>
            </RoleGuard>
          </div>
        ))}
      </div>
    </div>
  )
}

function CompareVersions({ addonName, currentVersion }: { addonName: string; currentVersion: string }) {
  const [targetVersion, setTargetVersion] = useState('')
  const [changelog, setChangelog] = useState<{ version: string; app_version: string; created: string; description: string }[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [availableVersions, setAvailableVersions] = useState<string[]>([])

  // Fetch available versions on mount for the dropdown
  useEffect(() => {
    api.getAddonChangelog(addonName)
      .then(data => {
        const versions = (data.versions ?? []).map(v => v.version)
        setAvailableVersions(versions)
        if (versions.length > 0 && !targetVersion) {
          setTargetVersion(versions[0]) // default to latest
        }
      })
      .catch(() => {}) // silent fail for version list
  }, [addonName])

  const handleCompare = () => {
    if (!targetVersion) return
    setLoading(true)
    setError(null)
    api.getAddonChangelog(addonName, currentVersion, targetVersion)
      .then(data => setChangelog(data.versions ?? []))
      .catch(e => setError(e instanceof Error ? e.message : 'Failed to fetch changelog'))
      .finally(() => setLoading(false))
  }

  return (
    <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5">
      <h3 className="mb-3 text-base font-semibold text-[#0a2a4a]">Compare Versions</h3>
      <p className="mb-4 text-sm text-[#2a5a7a]">See what changed between your current version and a target.</p>

      <div className="flex items-end gap-4 mb-4">
        <div className="flex-1">
          <label className="block text-xs font-medium text-[#1a4a6a] mb-1">From (current)</label>
          <div className="rounded-lg bg-[#e0f0ff] px-3 py-2 font-mono text-sm text-[#0a2a4a]">{currentVersion}</div>
        </div>
        <div className="text-[#3a6a8a] text-lg">→</div>
        <div className="flex-1">
          <label className="block text-xs font-medium text-[#1a4a6a] mb-1">To (target)</label>
          <select
            value={targetVersion}
            onChange={e => setTargetVersion(e.target.value)}
            className="w-full rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-2 text-sm text-[#0a2a4a]"
          >
            {availableVersions.map(v => (
              <option key={v} value={v}>{v}</option>
            ))}
          </select>
        </div>
        <button
          onClick={handleCompare}
          disabled={!targetVersion || loading}
          className="rounded-lg bg-[#0a2a4a] px-4 py-2 text-sm font-medium text-white hover:bg-[#14466e] disabled:opacity-50"
        >
          {loading ? 'Loading...' : 'Compare'}
        </button>
      </div>

      {error && <p className="text-sm text-red-600 mb-3">{error}</p>}

      {changelog && changelog.length > 0 && (
        <div className="space-y-2">
          <p className="text-xs text-[#3a6a8a] mb-2">{changelog.length} version{changelog.length !== 1 ? 's' : ''} between {currentVersion} and {targetVersion}</p>
          {changelog.map(v => (
            <div key={v.version} className="flex items-center justify-between rounded-lg bg-[#e0f0ff] px-4 py-3">
              <div>
                <span className="font-mono text-sm font-bold text-[#0a2a4a]">{v.version}</span>
                {v.app_version && (
                  <span className="ml-2 text-xs text-[#3a6a8a]">app: {v.app_version}</span>
                )}
              </div>
              <span className="text-xs text-[#3a6a8a]">
                {new Date(v.created).toLocaleDateString()}
              </span>
            </div>
          ))}
        </div>
      )}

      {changelog && changelog.length === 0 && (
        <p className="text-sm text-[#2a5a7a]">No versions found between {currentVersion} and {targetVersion}.</p>
      )}
    </div>
  )
}

function HealthProgressBar({ healthy, total }: { healthy: number; total: number }) {
  if (total === 0) return null
  const pct = (healthy / total) * 100
  const barColor =
    pct === 100 ? 'bg-green-500' : pct > 50 ? 'bg-yellow-500' : 'bg-red-500'

  return (
    <div className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:border-gray-700 dark:bg-gray-800">
      <h3 className="mb-2 text-base font-semibold text-[#0a2a4a] dark:text-gray-100">Overall Health</h3>
      <div className="h-3 w-full overflow-hidden rounded-full bg-[#c0ddf0] dark:bg-gray-700">
        <div
          className={`h-full rounded-full transition-all ${barColor}`}
          style={{ width: `${pct}%` }}
        />
      </div>
      <p className="mt-1.5 text-sm text-[#2a5a7a] dark:text-gray-400">
        {healthy} of {total} applications are healthy ({Math.round(pct)}%)
      </p>
    </div>
  )
}

export function AddonDetail() {
  const { name } = useParams<{ name: string }>()
  const navigate = useNavigate()
  const [addon, setAddon] = useState<AddonCatalogItem | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [searchParams, setSearchParams] = useSearchParams()
  const activeSection = searchParams.get('section') || 'overview'
  const setActiveSection = (s: string) => setSearchParams({ section: s }, { replace: true })

  const [valuesYaml, setValuesYaml] = useState<string | null>(null)
  const [argocdBaseURL, setArgocdBaseURL] = useState<string>('')

  // Filter state
  const [search, setSearch] = useState('')
  const [envFilter, setEnvFilter] = useState('all')
  const [statusFilter, setStatusFilter] = useState('all')
  const [healthFilter, setHealthFilter] = useState('all')

  // Remove addon
  const [removeModalOpen, setRemoveModalOpen] = useState(false)
  const [removing, setRemoving] = useState(false)
  const [removeError, setRemoveError] = useState<string | null>(null)

  // Upgrade addon
  const [upgradeOpen, setUpgradeOpen] = useState(false)
  const [upgradeVersion, setUpgradeVersion] = useState('')
  const [upgradeCluster, setUpgradeCluster] = useState('')
  const [upgradeSubmitting, setUpgradeSubmitting] = useState(false)
  const [upgradeError, setUpgradeError] = useState<string | null>(null)
  const [upgradeResult, setUpgradeResult] = useState<string | null>(null)

  useEffect(() => {
    if (!name) return
    api
      .getAddonDetail(name)
      .then((res) => setAddon(res.addon))
      .catch((e: unknown) =>
        setError(e instanceof Error ? e.message : 'Failed to load addon details'),
      )
      .finally(() => setLoading(false))

    api
      .getAddonValues(name)
      .then((res) => setValuesYaml(res.values_yaml))
      .catch(() => {
        // Values file may not exist for all addons — that's OK
      })

    api
      .getConnections()
      .then((res: ConnectionsListResponse) => {
        const active = res.connections.find(c => c.name === res.active_connection || c.is_active)
        if (active?.argocd_server_url) {
          setArgocdBaseURL(active.argocd_server_url.replace(/\/$/, ''))
        }
      })
      .catch(() => {})
  }, [name])

  const handleRemoveAddon = useCallback(async () => {
    if (!name) return
    setRemoving(true)
    setRemoveError(null)
    try {
      await removeAddon(name)
      navigate('/addons')
    } catch (e: unknown) {
      setRemoveError(e instanceof Error ? e.message : 'Failed to remove addon')
      setRemoving(false)
    }
  }, [name, navigate])

  const handleUpgrade = useCallback(async () => {
    if (!name || !upgradeVersion.trim()) return
    setUpgradeSubmitting(true)
    setUpgradeError(null)
    setUpgradeResult(null)
    try {
      const result = await upgradeAddon(name, {
        version: upgradeVersion.trim(),
        cluster: upgradeCluster.trim() || undefined,
      })
      const prUrl = result?.pr_url || result?.pull_request_url
      setUpgradeResult(prUrl ? `Upgrade initiated. PR: ${prUrl}` : 'Upgrade initiated successfully.')
    } catch (e: unknown) {
      setUpgradeError(e instanceof Error ? e.message : 'Failed to upgrade addon')
    } finally {
      setUpgradeSubmitting(false)
    }
  }, [name, upgradeVersion, upgradeCluster])

  const enabledApps = useMemo(
    () => (addon ? addon.applications.filter((a) => a.enabled) : []),
    [addon],
  )

  const disabledApps = useMemo(
    () => (addon ? addon.applications.filter((a) => !a.enabled) : []),
    [addon],
  )

  const uniqueEnvironments = useMemo(() => {
    const envs = enabledApps
      .map((a) => a.cluster_environment)
      .filter((e): e is string => Boolean(e))
    return [...new Set(envs)].sort()
  }, [enabledApps])

  const uniqueStatuses = useMemo(() => {
    const statuses = enabledApps.map((a) => a.status)
    return [...new Set(statuses)].sort()
  }, [enabledApps])

  const uniqueHealthStatuses = useMemo(() => {
    const healths = enabledApps.map((a) => a.health_status ?? 'Unknown')
    return [...new Set(healths)].sort()
  }, [enabledApps])

  const filteredApps = useMemo(() => {
    let result = enabledApps

    if (search) {
      const q = search.toLowerCase()
      result = result.filter(
        (a) =>
          a.cluster_name.toLowerCase().includes(q) ||
          a.cluster_environment?.toLowerCase().includes(q) ||
          a.application_name?.toLowerCase().includes(q),
      )
    }

    if (envFilter !== 'all') {
      result = result.filter((a) => a.cluster_environment === envFilter)
    }

    if (statusFilter !== 'all') {
      result = result.filter((a) => a.status === statusFilter)
    }

    if (healthFilter !== 'all') {
      if (healthFilter === 'unknown') {
        result = result.filter(
          (a) => !a.health_status || a.health_status.toLowerCase() === 'unknown',
        )
      } else {
        result = result.filter(
          (a) => a.health_status?.toLowerCase() === healthFilter.toLowerCase(),
        )
      }
    }

    return result
  }, [enabledApps, search, envFilter, statusFilter, healthFilter])

  // Compute environment versions from applications
  const envVersions = useMemo(() => {
    if (!addon) return []
    const map = new Map<string, string>()
    for (const app of addon.applications) {
      if (app.enabled && app.cluster_environment) {
        const version = app.deployed_version ?? app.configured_version ?? 'N/A'
        if (!map.has(app.cluster_environment)) {
          map.set(app.cluster_environment, version)
        }
      }
    }
    return Array.from(map.entries())
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([env, version]) => ({ env, version }))
  }, [addon])

  if (loading) {
    return (
      <div className="space-y-4">
        <div className="flex items-center gap-3">
          <button
            type="button"
            onClick={() => navigate('/addons')}
            className="rounded-md p-2 hover:bg-[#d6eeff] dark:hover:bg-gray-700"
          >
            <ArrowLeft className="h-5 w-5 dark:text-gray-300" />
          </button>
          <h2 className="text-2xl font-bold text-[#0a2a4a] dark:text-gray-100">Loading Addon Details...</h2>
        </div>
        <LoadingState message="Loading addon details..." />
      </div>
    )
  }

  if (error) {
    return (
      <div className="space-y-4">
        <div className="flex items-center gap-3">
          <button
            type="button"
            onClick={() => navigate('/addons')}
            className="rounded-md p-2 hover:bg-[#d6eeff] dark:hover:bg-gray-700"
          >
            <ArrowLeft className="h-5 w-5 dark:text-gray-300" />
          </button>
          <h2 className="text-2xl font-bold text-[#0a2a4a] dark:text-gray-100">Addon Details</h2>
        </div>
        <ErrorState message={error} />
      </div>
    )
  }

  if (!addon) {
    return (
      <div className="space-y-4">
        <div className="flex items-center gap-3">
          <button
            type="button"
            onClick={() => navigate('/addons')}
            className="rounded-md p-2 hover:bg-[#d6eeff] dark:hover:bg-gray-700"
          >
            <ArrowLeft className="h-5 w-5 dark:text-gray-300" />
          </button>
          <h2 className="text-2xl font-bold text-[#0a2a4a] dark:text-gray-100">Addon Details</h2>
        </div>
        <p className="text-[#2a5a7a] dark:text-gray-400">Addon not found.</p>
      </div>
    )
  }

  const healthPct =
    addon.enabled_clusters > 0
      ? Math.round((addon.healthy_applications / addon.enabled_clusters) * 100)
      : 0

  const namespace =
    addon.applications.find((a) => a.enabled && a.namespace)?.namespace ??
    addon.namespace ??
    addon.addon_name

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-3">
          <button
            type="button"
            onClick={() => navigate('/addons')}
            className="rounded-md p-2 hover:bg-[#d6eeff] dark:hover:bg-gray-700"
            aria-label="Back to addons"
          >
            <ArrowLeft className="h-5 w-5 dark:text-gray-300" />
          </button>
          <div>
            <h1 className="text-2xl font-bold text-[#0a2a4a] dark:text-gray-100">{addon.addon_name}</h1>
            <p className="text-sm text-[#2a5a7a] dark:text-gray-400">
              {addon.chart} &middot; Namespace: {namespace}
            </p>
          </div>
        </div>
        <RoleGuard adminOnly>
          <div className="flex shrink-0 items-center gap-2">
            <button
              type="button"
              onClick={() => { setUpgradeVersion(''); setUpgradeCluster(''); setUpgradeError(null); setUpgradeResult(null); setUpgradeOpen(true) }}
              className="inline-flex items-center gap-2 rounded-lg border border-teal-300 bg-[#f0f7ff] px-3 py-2 text-sm font-medium text-teal-700 hover:bg-teal-50 dark:border-teal-700 dark:bg-gray-800 dark:text-teal-400 dark:hover:bg-teal-900/20"
            >
              <ArrowUpCircle className="h-4 w-4" />
              Upgrade
            </button>
            <button
              type="button"
              onClick={() => { setRemoveError(null); setRemoveModalOpen(true) }}
              className="inline-flex items-center gap-2 rounded-lg border border-red-300 bg-[#f0f7ff] px-3 py-2 text-sm font-medium text-red-600 hover:bg-red-50 dark:border-red-700 dark:bg-gray-800 dark:text-red-400 dark:hover:bg-red-900/20"
            >
              <Trash2 className="h-4 w-4" />
              Remove
            </button>
          </div>
        </RoleGuard>
      </div>

      <ConfirmationModal
        open={removeModalOpen}
        onClose={() => setRemoveModalOpen(false)}
        onConfirm={handleRemoveAddon}
        title={`Remove addon "${name}"?`}
        description="This will remove the addon from the catalog. This action creates a pull request and cannot be undone."
        confirmText="Remove"
        typeToConfirm={name}
        destructive
        loading={removing}
      />
      {removeError && (
        <p className="text-sm text-red-600 dark:text-red-400">{removeError}</p>
      )}

      {/* Upgrade Dialog */}
      <Dialog open={upgradeOpen} onOpenChange={(v) => { if (!v) setUpgradeOpen(false) }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Upgrade {addon.addon_name}</DialogTitle>
            <DialogDescription>Set a new version for this addon.</DialogDescription>
          </DialogHeader>
          <div className="space-y-4 py-2">
            <div>
              <label className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300">
                New Version <span className="text-red-500">*</span>
              </label>
              <input
                type="text"
                value={upgradeVersion}
                onChange={(e) => setUpgradeVersion(e.target.value)}
                placeholder="e.g. 4.9.0"
                className="w-full rounded-md border border-[#5a9dd0] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 dark:placeholder-[#5a8aaa]"
              />
            </div>
            <div>
              <label className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300">
                Specific Cluster (optional)
              </label>
              <select
                value={upgradeCluster}
                onChange={(e) => setUpgradeCluster(e.target.value)}
                className="w-full rounded-md border border-[#5a9dd0] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200"
              >
                <option value="">All clusters (global)</option>
                {enabledApps.map((app) => (
                  <option key={app.cluster_name} value={app.cluster_name}>
                    {app.cluster_name}
                  </option>
                ))}
              </select>
            </div>
            {upgradeError && <p className="text-sm text-red-600 dark:text-red-400">{upgradeError}</p>}
            {upgradeResult && <p className="text-sm text-green-600 dark:text-green-400">{upgradeResult}</p>}
          </div>
          <DialogFooter>
            <button
              type="button"
              onClick={() => setUpgradeOpen(false)}
              disabled={upgradeSubmitting}
              className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
            >
              {upgradeResult ? 'Close' : 'Cancel'}
            </button>
            {!upgradeResult && (
              <button
                type="button"
                onClick={handleUpgrade}
                disabled={!upgradeVersion.trim() || upgradeSubmitting}
                className="inline-flex items-center gap-2 rounded-md bg-teal-600 px-4 py-2 text-sm font-medium text-white hover:bg-teal-700 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-teal-700 dark:hover:bg-teal-600"
              >
                {upgradeSubmitting && <Loader2 className="h-4 w-4 animate-spin" />}
                Upgrade
              </button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <div className="flex gap-6">
        <DetailNavPanel
          sections={[
            {
              items: [
                { key: 'overview', label: 'Overview', icon: LayoutGrid },
                { key: 'clusters', label: 'Clusters', badge: enabledApps.length, icon: Server },
                { key: 'upgrade', label: 'Upgrade', icon: ArrowUpCircle },
                { key: 'config', label: 'Config', icon: FileCode },
              ],
            },
          ]}
          activeKey={activeSection}
          onSelect={setActiveSection}
        />

        <div className="flex-1 space-y-6">
          {activeSection === 'overview' && (
            <>
              {/* Summary stat cards */}
              <div className="grid grid-cols-2 gap-4 lg:grid-cols-5">
                <StatCard
                  title="Active Apps"
                  value={`${addon.enabled_clusters} / ${addon.total_clusters}`}
                  icon={<Activity className="h-5 w-5" />}
                />
                <StatCard
                  title="Healthy"
                  value={`${addon.healthy_applications} (${healthPct}%)`}
                  icon={<CheckCircle className="h-5 w-5" />}
                  color="success"
                />
                <StatCard
                  title="Degraded"
                  value={addon.degraded_applications}
                  icon={<AlertTriangle className="h-5 w-5" />}
                  color={addon.degraded_applications > 0 ? 'warning' : 'default'}
                />
                <StatCard
                  title="Not Deployed"
                  value={addon.missing_applications}
                  icon={<XCircle className="h-5 w-5" />}
                  color={addon.missing_applications > 0 ? 'error' : 'default'}
                />
                <StatCard
                  title="Disabled in Git"
                  value={disabledApps.length}
                  icon={<Ban className="h-5 w-5" />}
                />
              </div>

              {/* Overall health progress bar */}
              <HealthProgressBar
                healthy={addon.healthy_applications}
                total={addon.enabled_clusters}
              />

              {/* Environment Versions */}
              {envVersions.length > 0 && (
                <div className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:border-gray-700 dark:bg-gray-800">
                  <h3 className="mb-3 text-base font-semibold text-[#0a2a4a] dark:text-gray-100">
                    Environment Versions
                  </h3>
                  <div className="space-y-2">
                    {envVersions.map(({ env, version }) => (
                      <div
                        key={env}
                        className="flex items-center justify-between rounded ring-2 ring-[#6aade0] px-3 py-2 dark:border-gray-700"
                      >
                        <span className="rounded-full border border-teal-200 bg-teal-50 px-2 py-0.5 text-xs font-medium text-teal-700 dark:border-teal-600 dark:bg-teal-900/30 dark:text-teal-400">
                          {env}
                        </span>
                        <span className="font-mono text-sm text-[#1a4a6a] dark:text-gray-400">{version}</span>
                      </div>
                    ))}
                  </div>
                </div>
              )}

              {/* Advanced Configuration — collapsed by default */}
              <details className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff]">
                <summary className="cursor-pointer px-5 py-4 text-base font-semibold text-[#0a2a4a] select-none">
                  Advanced Configuration
                </summary>
                <div className="border-t border-[#6aade0] px-5 py-4 space-y-4">
                  {/* Sync Wave */}
                  <div className="flex items-center justify-between">
                    <div>
                      <p className="text-sm font-medium text-[#0a2a4a]">Sync Wave</p>
                      <p className="text-xs text-[#3a6a8a]">Controls deployment ordering. Negative = earlier, positive = later.</p>
                    </div>
                    <span className="font-mono text-sm text-[#0a2a4a]">{addon.syncWave ?? 0}</span>
                  </div>

                  {/* Self-Heal */}
                  <div className="flex items-center justify-between">
                    <div>
                      <p className="text-sm font-medium text-[#0a2a4a]">Self-Heal</p>
                      <p className="text-xs text-[#3a6a8a]">When enabled, ArgoCD reverts manual changes automatically.</p>
                    </div>
                    <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${
                      addon.selfHeal === false
                        ? 'bg-amber-100 text-amber-700'
                        : 'bg-green-100 text-green-700'
                    }`}>
                      {addon.selfHeal === false ? 'Disabled' : 'Enabled'}
                    </span>
                  </div>

                  {/* Sync Options */}
                  <div>
                    <p className="text-sm font-medium text-[#0a2a4a]">Sync Options</p>
                    <p className="text-xs text-[#3a6a8a] mb-2">ArgoCD sync options applied to this addon.</p>
                    {addon.syncOptions && addon.syncOptions.length > 0 ? (
                      <div className="flex flex-wrap gap-1">
                        {addon.syncOptions.map((opt: string) => (
                          <span key={opt} className="rounded bg-[#d6eeff] px-2 py-0.5 text-xs font-mono text-[#0a2a4a]">{opt}</span>
                        ))}
                      </div>
                    ) : (
                      <p className="text-xs text-[#5a8aaa]">Default (CreateNamespace=true)</p>
                    )}
                  </div>

                  {/* Ignore Differences */}
                  <div>
                    <p className="text-sm font-medium text-[#0a2a4a]">Ignore Differences</p>
                    <p className="text-xs text-[#3a6a8a] mb-2">Fields ignored during ArgoCD sync comparison.</p>
                    {addon.ignoreDifferences && addon.ignoreDifferences.length > 0 ? (
                      <pre className="rounded bg-[#071828] p-3 text-xs text-[#bee0ff] overflow-auto">
                        {JSON.stringify(addon.ignoreDifferences, null, 2)}
                      </pre>
                    ) : (
                      <p className="text-xs text-[#5a8aaa]">None configured</p>
                    )}
                  </div>

                  {/* Extra Helm Values */}
                  <div>
                    <p className="text-sm font-medium text-[#0a2a4a]">Extra Helm Values</p>
                    <p className="text-xs text-[#3a6a8a] mb-2">Additional Helm parameters injected during rendering.</p>
                    {addon.extraHelmValues && Object.keys(addon.extraHelmValues).length > 0 ? (
                      <div className="space-y-1">
                        {Object.entries(addon.extraHelmValues).map(([k, v]) => (
                          <div key={k} className="flex items-center gap-2 text-xs">
                            <span className="font-mono font-medium text-[#0a2a4a]">{k}</span>
                            <span className="text-[#3a6a8a]">=</span>
                            <span className="font-mono text-[#1a4a6a]">{v as string}</span>
                          </div>
                        ))}
                      </div>
                    ) : (
                      <p className="text-xs text-[#5a8aaa]">None configured</p>
                    )}
                  </div>

                  {/* Additional Sources */}
                  <div>
                    <p className="text-sm font-medium text-[#0a2a4a]">Additional Sources</p>
                    <p className="text-xs text-[#3a6a8a] mb-2">Extra chart or manifest sources deployed alongside the main addon.</p>
                    {addon.additionalSources && addon.additionalSources.length > 0 ? (
                      <div className="space-y-2">
                        {addon.additionalSources.map((src, i: number) => (
                          <div key={i} className="rounded bg-[#e0f0ff] px-3 py-2 text-xs">
                            {src.chart && <p><span className="text-[#3a6a8a]">Chart:</span> <span className="font-mono text-[#0a2a4a]">{src.chart} @ {src.version}</span></p>}
                            {src.path && <p><span className="text-[#3a6a8a]">Path:</span> <span className="font-mono text-[#0a2a4a]">{src.path}</span></p>}
                            {src.repoURL && <p><span className="text-[#3a6a8a]">Repo:</span> <span className="font-mono text-[#0a2a4a]">{src.repoURL}</span></p>}
                          </div>
                        ))}
                      </div>
                    ) : (
                      <p className="text-xs text-[#5a8aaa]">Single source (main chart only)</p>
                    )}
                  </div>
                </div>
              </details>
            </>
          )}

          {activeSection === 'clusters' && (
            <>
              {/* Filter controls */}
              <div className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:border-gray-700 dark:bg-gray-800">
                <h3 className="mb-3 text-base font-semibold text-[#0a2a4a] dark:text-gray-100">
                  Filter Applications
                </h3>
                <div className="flex flex-wrap items-center gap-3">
                  <div className="relative flex-1" style={{ minWidth: 200, maxWidth: 300 }}>
                    <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[#3a6a8a]" />
                    <input
                      type="text"
                      placeholder="Search clusters, environments, or apps..."
                      value={search}
                      onChange={(e) => setSearch(e.target.value)}
                      className="w-full rounded-lg border border-[#5a9dd0] py-2 pl-10 pr-4 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-900 dark:text-gray-200 dark:placeholder-[#5a8aaa]"
                    />
                  </div>

                  <select
                    value={envFilter}
                    onChange={(e) => setEnvFilter(e.target.value)}
                    className="rounded-lg border border-[#5a9dd0] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-900 dark:text-gray-200"
                  >
                    <option value="all">All Environments</option>
                    {uniqueEnvironments.map((env) => (
                      <option key={env} value={env}>
                        {env}
                      </option>
                    ))}
                  </select>

                  <select
                    value={statusFilter}
                    onChange={(e) => setStatusFilter(e.target.value)}
                    className="rounded-lg border border-[#5a9dd0] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-900 dark:text-gray-200"
                  >
                    <option value="all">All Status</option>
                    {uniqueStatuses.map((s) => (
                      <option key={s} value={s}>
                        {s}
                      </option>
                    ))}
                  </select>

                  <select
                    value={healthFilter}
                    onChange={(e) => setHealthFilter(e.target.value)}
                    className="rounded-lg border border-[#5a9dd0] px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-900 dark:text-gray-200"
                  >
                    <option value="all">All Health</option>
                    {uniqueHealthStatuses.map((h) => (
                      <option key={h} value={h}>
                        {h}
                      </option>
                    ))}
                  </select>
                </div>
                <p className="mt-2 text-xs text-[#2a5a7a] dark:text-gray-400">
                  Showing {filteredApps.length} of {enabledApps.length} applications
                </p>
              </div>

              {/* Cluster Applications Table */}
              <div className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] dark:border-gray-700 dark:bg-gray-800">
                <div className="border-b px-4 py-3 dark:border-gray-700">
                  <h3 className="text-base font-semibold text-[#0a2a4a] dark:text-gray-100">
                    Cluster Applications
                  </h3>
                </div>
                <div className="overflow-x-auto">
                  <table className="w-full text-sm">
                    <thead className="border-b bg-[#d0e8f8] text-xs uppercase text-[#2a5a7a] dark:border-gray-700 dark:bg-gray-900 dark:text-gray-400">
                      <tr>
                        <th className="px-4 py-3 text-left">Cluster</th>
                        <th className="px-4 py-3 text-left">Status</th>
                        <th className="px-4 py-3 text-left">Health</th>
                        <th className="px-4 py-3 text-left">Version</th>
                        <th className="px-4 py-3 text-left">ArgoCD</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-[#6aade0] dark:divide-gray-700">
                      {filteredApps.map((app) => (
                        <tr key={app.cluster_name} className="hover:bg-[#d6eeff] dark:hover:bg-gray-700">
                          <td className="px-4 py-3">
                            <Link
                              to={`/clusters/${app.cluster_name}`}
                              className="font-medium text-teal-600 hover:text-teal-800 hover:underline dark:text-teal-400 dark:hover:text-teal-300"
                            >
                              {app.cluster_name}
                            </Link>
                          </td>
                          <td className="px-4 py-3">
                            <StatusBadge status={app.status} />
                          </td>
                          <td className="px-4 py-3">
                            <StatusBadge
                              status={app.health_status ?? 'Unknown'}
                            />
                          </td>
                          <td className="px-4 py-3">
                            <span className="font-mono text-xs text-[#1a4a6a] dark:text-gray-400">
                              {app.deployed_version ?? app.configured_version ?? 'N/A'}
                            </span>
                            {app.deployed_version &&
                              app.configured_version &&
                              app.deployed_version !== app.configured_version && (
                                <span className="ml-1 text-xs text-yellow-600 dark:text-yellow-400">
                                  (configured: {app.configured_version})
                                </span>
                              )}
                          </td>
                          <td className="px-4 py-3">
                            {app.application_name && argocdBaseURL ? (
                              <a
                                href={`${argocdBaseURL}/applications/${app.application_name}`}
                                target="_blank"
                                rel="noopener noreferrer"
                                title={`Open ${app.application_name} in ArgoCD`}
                                className="text-[#2a5a7a] hover:text-teal-600 dark:text-gray-400 dark:hover:text-teal-400"
                              >
                                <ExternalLink className="h-4 w-4" />
                              </a>
                            ) : (
                              <span className="text-xs text-[#3a6a8a]">N/A</span>
                            )}
                          </td>
                        </tr>
                      ))}
                      {filteredApps.length === 0 && (
                        <tr>
                          <td
                            colSpan={5}
                            className="px-4 py-8 text-center text-[#3a6a8a] dark:text-gray-500"
                          >
                            {enabledApps.length === 0
                              ? 'This addon is not currently deployed on any clusters.'
                              : 'No applications match the current filters.'}
                          </td>
                        </tr>
                      )}
                    </tbody>
                  </table>
                </div>
              </div>

              {/* Disabled clusters section */}
              {disabledApps.length > 0 && (
                <div className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:border-gray-700 dark:bg-gray-800" id="disabled-clusters-section">
                  <h3 className="mb-3 text-base font-semibold text-[#0a2a4a] dark:text-gray-100">
                    Disabled on {disabledApps.length} Clusters
                  </h3>
                  <div className="flex flex-wrap gap-2">
                    {disabledApps.map((app) => (
                      <Link
                        key={app.cluster_name}
                        to={`/clusters/${app.cluster_name}`}
                        className="inline-flex items-center gap-1.5 rounded-full ring-2 ring-[#6aade0] bg-[#d0e8f8] px-3 py-1 text-xs font-medium text-[#1a4a6a] transition-colors hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
                      >
                        <Ban className="h-3 w-3" />
                        {app.cluster_name}
                      </Link>
                    ))}
                  </div>
                </div>
              )}
            </>
          )}

          {activeSection === 'upgrade' && (
            <div className="space-y-6">
              {/* Current version */}
              <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5">
                <h3 className="text-base font-semibold text-[#0a2a4a]">Current Catalog Version</h3>
                <p className="mt-1 font-mono text-lg font-bold text-[#0a2a4a]">{addon.version}</p>
                <p className="mt-1 text-sm text-[#2a5a7a]">Chart: {addon.chart}</p>
              </div>

              {/* Available versions */}
              <UpgradeVersionList
                addonName={addon.addon_name}
                currentVersion={addon.version}
                onUpgrade={(version) => {
                  setUpgradeVersion(version)
                  setUpgradeCluster('')
                  setUpgradeError(null)
                  setUpgradeResult(null)
                  setUpgradeOpen(true)
                }}
              />

              {/* Compare versions */}
              <CompareVersions addonName={addon.addon_name} currentVersion={addon.version} />

              {/* Per-cluster versions */}
              <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5">
                <h3 className="mb-3 text-base font-semibold text-[#0a2a4a]">Per-Cluster Versions</h3>
                {enabledApps.length === 0 ? (
                  <p className="text-sm text-[#2a5a7a]">No clusters have this addon enabled.</p>
                ) : (
                  <div className="space-y-2">
                    {enabledApps.map((app) => {
                      const deployedVersion =
                        app.deployed_version ?? app.configured_version ?? 'N/A'
                      const isDrifted =
                        deployedVersion !== addon.version && deployedVersion !== 'N/A'
                      return (
                        <div
                          key={app.cluster_name}
                          className="flex items-center justify-between rounded-lg bg-[#e0f0ff] px-4 py-2.5"
                        >
                          <div className="flex items-center gap-3">
                            <Link
                              to={`/clusters/${app.cluster_name}`}
                              className="text-sm font-medium text-[#0a6aaa] hover:underline"
                            >
                              {app.cluster_name}
                            </Link>
                            <span className="font-mono text-sm text-[#1a4a6a]">
                              {deployedVersion}
                            </span>
                          </div>
                          {isDrifted ? (
                            <RoleGuard adminOnly>
                              <button
                                type="button"
                                onClick={() => {
                                  setUpgradeVersion(addon.version)
                                  setUpgradeCluster(app.cluster_name)
                                  setUpgradeError(null)
                                  setUpgradeResult(null)
                                  setUpgradeOpen(true)
                                }}
                                className="rounded-lg bg-[#0a2a4a] px-3 py-1.5 text-xs font-medium text-white hover:bg-[#14466e]"
                              >
                                Upgrade to {addon.version}
                              </button>
                            </RoleGuard>
                          ) : (
                            <span className="text-xs text-green-600">✓ Current</span>
                          )}
                        </div>
                      )
                    })}
                  </div>
                )}
              </div>
            </div>
          )}

          {activeSection === 'config' && (
            <>
              {valuesYaml ? (
                <YamlViewer yaml={valuesYaml} title="Global Default Values" />
              ) : (
                <p className="text-sm text-[#2a5a7a]">No default values template found for this addon.</p>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  )
}

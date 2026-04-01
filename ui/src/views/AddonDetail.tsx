import { useState, useEffect, useMemo } from 'react'
import { useParams, useNavigate, Link } from 'react-router-dom'
import {
  ArrowLeft,
  Search,
  CheckCircle,
  AlertTriangle,
  XCircle,
  Ban,
  ExternalLink,
  Activity,
} from 'lucide-react'
import { api } from '@/services/api'
import type { AddonCatalogItem, ConnectionsListResponse } from '@/services/models'
import { StatCard } from '@/components/StatCard'
import { StatusBadge } from '@/components/StatusBadge'
import { LoadingState } from '@/components/LoadingState'
import { ErrorState } from '@/components/ErrorState'
import { YamlViewer } from '@/components/YamlViewer'

function HealthProgressBar({ healthy, total }: { healthy: number; total: number }) {
  if (total === 0) return null
  const pct = (healthy / total) * 100
  const barColor =
    pct === 100 ? 'bg-green-500' : pct > 50 ? 'bg-yellow-500' : 'bg-red-500'

  return (
    <div className="rounded-lg border bg-white p-4 dark:border-gray-700 dark:bg-gray-800">
      <h3 className="mb-2 text-base font-semibold text-gray-900 dark:text-gray-100">Overall Health</h3>
      <div className="h-3 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-gray-700">
        <div
          className={`h-full rounded-full transition-all ${barColor}`}
          style={{ width: `${pct}%` }}
        />
      </div>
      <p className="mt-1.5 text-sm text-gray-500 dark:text-gray-400">
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

  const [valuesYaml, setValuesYaml] = useState<string | null>(null)
  const [argocdBaseURL, setArgocdBaseURL] = useState<string>('')

  // Filter state
  const [search, setSearch] = useState('')
  const [envFilter, setEnvFilter] = useState('all')
  const [statusFilter, setStatusFilter] = useState('all')
  const [healthFilter, setHealthFilter] = useState('all')

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
            className="rounded-md p-2 hover:bg-gray-100 dark:hover:bg-gray-700"
          >
            <ArrowLeft className="h-5 w-5 dark:text-gray-300" />
          </button>
          <h2 className="text-2xl font-bold text-gray-900 dark:text-gray-100">Loading Addon Details...</h2>
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
            className="rounded-md p-2 hover:bg-gray-100 dark:hover:bg-gray-700"
          >
            <ArrowLeft className="h-5 w-5 dark:text-gray-300" />
          </button>
          <h2 className="text-2xl font-bold text-gray-900 dark:text-gray-100">Addon Details</h2>
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
            className="rounded-md p-2 hover:bg-gray-100 dark:hover:bg-gray-700"
          >
            <ArrowLeft className="h-5 w-5 dark:text-gray-300" />
          </button>
          <h2 className="text-2xl font-bold text-gray-900 dark:text-gray-100">Addon Details</h2>
        </div>
        <p className="text-gray-500 dark:text-gray-400">Addon not found.</p>
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
      <div className="flex items-center gap-3">
        <button
          type="button"
          onClick={() => navigate('/addons')}
          className="rounded-md p-2 hover:bg-gray-100 dark:hover:bg-gray-700"
          aria-label="Back to addons"
        >
          <ArrowLeft className="h-5 w-5 dark:text-gray-300" />
        </button>
        <div>
          <h1 className="text-2xl font-bold text-gray-900 dark:text-gray-100">{addon.addon_name}</h1>
          <p className="text-sm text-gray-500 dark:text-gray-400">
            {addon.chart} &middot; Namespace: {namespace}
          </p>
        </div>
      </div>

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

      {/* Filter controls */}
      <div className="rounded-lg border bg-white p-4 dark:border-gray-700 dark:bg-gray-800">
        <h3 className="mb-3 text-base font-semibold text-gray-900 dark:text-gray-100">
          Filter Applications
        </h3>
        <div className="flex flex-wrap items-center gap-3">
          <div className="relative flex-1" style={{ minWidth: 200, maxWidth: 300 }}>
            <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
            <input
              type="text"
              placeholder="Search clusters, environments, or apps..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full rounded-lg border border-gray-300 py-2 pl-10 pr-4 text-sm focus:border-cyan-500 focus:outline-none focus:ring-1 focus:ring-cyan-500 dark:border-gray-600 dark:bg-gray-900 dark:text-gray-200 dark:placeholder-gray-500"
            />
          </div>

          <select
            value={envFilter}
            onChange={(e) => setEnvFilter(e.target.value)}
            className="rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-cyan-500 focus:outline-none focus:ring-1 focus:ring-cyan-500 dark:border-gray-600 dark:bg-gray-900 dark:text-gray-200"
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
            className="rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-cyan-500 focus:outline-none focus:ring-1 focus:ring-cyan-500 dark:border-gray-600 dark:bg-gray-900 dark:text-gray-200"
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
            className="rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-cyan-500 focus:outline-none focus:ring-1 focus:ring-cyan-500 dark:border-gray-600 dark:bg-gray-900 dark:text-gray-200"
          >
            <option value="all">All Health</option>
            {uniqueHealthStatuses.map((h) => (
              <option key={h} value={h}>
                {h}
              </option>
            ))}
          </select>
        </div>
        <p className="mt-2 text-xs text-gray-500 dark:text-gray-400">
          Showing {filteredApps.length} of {enabledApps.length} applications
        </p>
      </div>

      {/* Two-column layout: env versions + cluster table */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-12">
        {/* Left: Environment Versions */}
        <div className="lg:col-span-4">
          <div className="rounded-lg border bg-white p-4 dark:border-gray-700 dark:bg-gray-800">
            <h3 className="mb-3 text-base font-semibold text-gray-900 dark:text-gray-100">
              Environment Versions
            </h3>
            {envVersions.length > 0 ? (
              <div className="space-y-2">
                {envVersions.map(({ env, version }) => (
                  <div
                    key={env}
                    className="flex items-center justify-between rounded border border-gray-100 px-3 py-2 dark:border-gray-700"
                  >
                    <span className="rounded-full border border-cyan-200 bg-cyan-50 px-2 py-0.5 text-xs font-medium text-cyan-700 dark:border-cyan-600 dark:bg-cyan-900/30 dark:text-cyan-400">
                      {env}
                    </span>
                    <span className="font-mono text-sm text-gray-600 dark:text-gray-400">{version}</span>
                  </div>
                ))}
              </div>
            ) : (
              <p className="text-sm text-gray-400">No environment versions available.</p>
            )}
          </div>
        </div>

        {/* Right: Cluster Applications Table */}
        <div className="lg:col-span-8">
          <div className="rounded-lg border bg-white dark:border-gray-700 dark:bg-gray-800">
            <div className="border-b px-4 py-3 dark:border-gray-700">
              <h3 className="text-base font-semibold text-gray-900 dark:text-gray-100">
                Cluster Applications
              </h3>
            </div>
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead className="border-b bg-gray-50 text-xs uppercase text-gray-500 dark:border-gray-700 dark:bg-gray-900 dark:text-gray-400">
                  <tr>
                    <th className="px-4 py-3 text-left">Cluster</th>
                    <th className="px-4 py-3 text-left">Status</th>
                    <th className="px-4 py-3 text-left">Health</th>
                    <th className="px-4 py-3 text-left">Version</th>
                    <th className="px-4 py-3 text-left">ArgoCD</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-200 dark:divide-gray-700">
                  {filteredApps.map((app) => (
                    <tr key={app.cluster_name} className="hover:bg-gray-50 dark:hover:bg-gray-700">
                      <td className="px-4 py-3">
                        <Link
                          to={`/clusters/${app.cluster_name}`}
                          className="font-medium text-cyan-600 hover:text-cyan-800 hover:underline dark:text-cyan-400 dark:hover:text-cyan-300"
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
                        <span className="font-mono text-xs text-gray-600 dark:text-gray-400">
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
                            className="text-gray-500 hover:text-cyan-600 dark:text-gray-400 dark:hover:text-cyan-400"
                          >
                            <ExternalLink className="h-4 w-4" />
                          </a>
                        ) : (
                          <span className="text-xs text-gray-400">N/A</span>
                        )}
                      </td>
                    </tr>
                  ))}
                  {filteredApps.length === 0 && (
                    <tr>
                      <td
                        colSpan={5}
                        className="px-4 py-8 text-center text-gray-400 dark:text-gray-500"
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
        </div>
      </div>

      {/* Disabled clusters section */}
      {disabledApps.length > 0 && (
        <div className="rounded-lg border bg-white p-4 dark:border-gray-700 dark:bg-gray-800" id="disabled-clusters-section">
          <h3 className="mb-3 text-base font-semibold text-gray-900 dark:text-gray-100">
            Disabled on {disabledApps.length} Clusters
          </h3>
          <div className="flex flex-wrap gap-2">
            {disabledApps.map((app) => (
              <Link
                key={app.cluster_name}
                to={`/clusters/${app.cluster_name}`}
                className="inline-flex items-center gap-1.5 rounded-full border border-gray-200 bg-gray-50 px-3 py-1 text-xs font-medium text-gray-600 transition-colors hover:bg-gray-100 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
              >
                <Ban className="h-3 w-3" />
                {app.cluster_name}
              </Link>
            ))}
          </div>
        </div>
      )}

      {/* Global default values */}
      {valuesYaml && (
        <YamlViewer yaml={valuesYaml} title="Global Default Values" />
      )}
    </div>
  )
}

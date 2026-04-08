import { useState, useEffect, useCallback } from 'react'
import {
  GitMerge,
  GitBranch,
  Loader2,
  CheckCircle,
  Info,
} from 'lucide-react'
import { useConnections } from '@/hooks/useConnections'
import { api } from '@/services/api'
import { LoadingState } from '@/components/LoadingState'
import { ErrorState } from '@/components/ErrorState'
import type { AddonCatalogItem } from '@/services/models'

interface GitOpsFormData {
  gitops_base_branch: string
  gitops_pr_auto_merge: boolean
  gitops_host_cluster_name: string
  gitops_default_addons: string
}

const labelCls = 'block text-sm font-medium text-[#0a3a5a] dark:text-gray-300'
const inputCls =
  'mt-1 block w-full rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-2 text-sm text-[#0a2a4a] shadow-sm placeholder:text-[#3a6a8a] focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 dark:placeholder:text-[#2a5a7a]'

export function GitOpsSection() {
  const { connections, loading, error, refreshConnections } = useConnections()

  const existingConn = connections.find((c) => c.is_active) ?? connections[0] ?? null

  const [form, setForm] = useState<GitOpsFormData>({
    gitops_base_branch: 'main',
    gitops_pr_auto_merge: false,
    gitops_host_cluster_name: '',
    gitops_default_addons: '',
  })

  // Addon catalog for the checklist
  const [catalogAddons, setCatalogAddons] = useState<AddonCatalogItem[]>([])
  const [catalogLoading, setCatalogLoading] = useState(false)
  const [selectedDefaults, setSelectedDefaults] = useState<string[]>([])

  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [justSaved, setJustSaved] = useState(false)

  // Detected host cluster name from platform info
  const [detectedClusterName, setDetectedClusterName] = useState<string | null>(null)

  const fetchCatalog = useCallback(() => {
    setCatalogLoading(true)
    api.getAddonCatalog()
      .then((res) => {
        setCatalogAddons(res?.addons || [])
      })
      .catch(() => setCatalogAddons([]))
      .finally(() => setCatalogLoading(false))
  }, [])

  const fetchPlatformInfo = useCallback(() => {
    api.health()
      .then((data: Record<string, unknown>) => {
        const clusterName = (data?.host_cluster_name || data?.cluster_name) as string | undefined
        if (clusterName) setDetectedClusterName(clusterName)
      })
      .catch(() => {})
  }, [])

  useEffect(() => {
    fetchCatalog()
    fetchPlatformInfo()
  }, [fetchCatalog, fetchPlatformInfo])

  // Parse default addons from form whenever form changes
  useEffect(() => {
    const defaults = form.gitops_default_addons
      ? form.gitops_default_addons.split(',').map(s => s.trim()).filter(Boolean)
      : []
    setSelectedDefaults(defaults)
  }, [form.gitops_default_addons])

  function toggleDefault(addonName: string) {
    setSelectedDefaults(prev => {
      const next = prev.includes(addonName)
        ? prev.filter(n => n !== addonName)
        : [...prev, addonName]
      setForm(f => ({ ...f, gitops_default_addons: next.join(',') }))
      return next
    })
  }

  async function handleSave() {
    if (!existingConn) return
    setSaving(true)
    setSaveError(null)
    try {
      const connPayload = buildConnectionPayload(existingConn, form)
      await api.updateConnection(existingConn.name, connPayload)
      refreshConnections()
      setJustSaved(true)
      setTimeout(() => setJustSaved(false), 3000)
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'Save failed')
    } finally {
      setSaving(false)
    }
  }

  if (loading) return <LoadingState message="Loading GitOps settings..." />
  if (error) return <ErrorState message={error} onRetry={refreshConnections} />

  if (!existingConn) {
    return (
      <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-6 shadow-sm dark:bg-gray-800">
        <p className="text-sm text-[#2a5a7a] dark:text-gray-400">
          Configure a <span className="font-semibold">Connection</span> first before setting up GitOps.
        </p>
      </div>
    )
  }

  const isHostClusterDetected = !!detectedClusterName && !form.gitops_host_cluster_name

  return (
    <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-6 shadow-sm dark:bg-gray-800 space-y-6">
      {/* Git section */}
      <div>
        <div className="mb-3 flex items-center gap-2">
          <GitBranch className="h-4 w-4 text-[#2a5a7a]" />
          <h5 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Git</h5>
        </div>
        <div className="max-w-xs">
          <label className={labelCls}>Base Branch</label>
          <input
            className={inputCls}
            value={form.gitops_base_branch}
            onChange={(e) => setForm(prev => ({ ...prev, gitops_base_branch: e.target.value }))}
            placeholder="main"
          />
        </div>
      </div>

      {/* Automation section */}
      <div>
        <p className="mb-3 text-xs font-semibold uppercase tracking-wide text-[#3a6a8a] dark:text-gray-500">Automation</p>
        <label className="flex items-center gap-3 cursor-pointer">
          <div className="relative">
            <input
              type="checkbox"
              className="sr-only peer"
              checked={form.gitops_pr_auto_merge}
              onChange={(e) => setForm(prev => ({ ...prev, gitops_pr_auto_merge: e.target.checked }))}
            />
            <div className="h-5 w-9 rounded-full border border-[#5a9dd0] bg-[#f0f7ff] peer-checked:bg-teal-500 peer-checked:border-teal-500 transition-colors dark:border-gray-600 dark:bg-gray-700" />
            <div className="absolute top-0.5 left-0.5 h-4 w-4 rounded-full bg-white shadow transition-transform peer-checked:translate-x-4" />
          </div>
          <div>
            <span className="text-sm font-medium text-[#0a3a5a] dark:text-gray-300">Auto-merge PRs</span>
            <p className="text-[10px] text-[#3a6a8a]">Automatically merge addon change PRs when checks pass</p>
          </div>
        </label>
      </div>

      {/* Cluster section */}
      <div>
        <p className="mb-3 text-xs font-semibold uppercase tracking-wide text-[#3a6a8a] dark:text-gray-500">Cluster</p>
        <div className="max-w-xs">
          <label className={labelCls}>
            Host Cluster Name
            <span className="ml-1 text-[#3a6a8a] font-normal">(optional)</span>
          </label>
          {isHostClusterDetected ? (
            <div className="mt-1 flex items-center gap-2">
              <div className="flex-1 rounded-lg border border-[#5a9dd0] bg-[#e0f0ff] px-3 py-2 text-sm text-[#0a2a4a] dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100">
                {detectedClusterName}
              </div>
              <span className="flex items-center gap-1 rounded-full bg-teal-100 px-2 py-0.5 text-[10px] font-medium text-teal-700 dark:bg-teal-900/30 dark:text-teal-400">
                <Info className="h-3 w-3" />
                detected
              </span>
            </div>
          ) : (
            <input
              className={inputCls}
              value={form.gitops_host_cluster_name}
              onChange={(e) => setForm(prev => ({ ...prev, gitops_host_cluster_name: e.target.value }))}
              placeholder={detectedClusterName || 'e.g. management'}
            />
          )}
          <p className="mt-1 text-[10px] text-[#3a6a8a]">Name of the cluster running Sharko + ArgoCD</p>
          {isHostClusterDetected && (
            <button
              type="button"
              onClick={() => setDetectedClusterName(null)}
              className="mt-1 text-[10px] text-[#3a6a8a] underline hover:text-[#1a4a6a]"
            >
              Override
            </button>
          )}
        </div>
      </div>

      {/* Default Addons checklist */}
      <div>
        <div className="mb-3 flex items-center gap-2">
          <GitMerge className="h-4 w-4 text-[#2a5a7a]" />
          <h5 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Default Addons</h5>
        </div>
        <p className="mb-3 text-[10px] text-[#3a6a8a]">
          Addons enabled by default on newly registered clusters.
        </p>
        {catalogLoading ? (
          <div className="flex items-center gap-2 text-xs text-[#3a6a8a]">
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
            Loading addon catalog...
          </div>
        ) : catalogAddons.length === 0 ? (
          <p className="text-xs text-[#3a6a8a] dark:text-gray-500">
            No addons in catalog yet. Add addons to the catalog to configure defaults.
          </p>
        ) : (
          <div className="grid grid-cols-1 gap-1.5 sm:grid-cols-2">
            {catalogAddons.map(addon => (
              <label key={addon.addon_name} className="flex items-center gap-2 text-sm cursor-pointer rounded-lg px-2 py-1.5 hover:bg-[#d6eeff] dark:hover:bg-gray-700 transition-colors">
                <input
                  type="checkbox"
                  checked={selectedDefaults.includes(addon.addon_name)}
                  onChange={() => toggleDefault(addon.addon_name)}
                  className="h-3.5 w-3.5 rounded border-[#5a9dd0] text-teal-600 focus:ring-teal-500"
                />
                <span className="text-[#0a2a4a] dark:text-gray-100">{addon.addon_name}</span>
                <span className="text-xs text-[#3a6a8a] dark:text-gray-400">v{addon.version}</span>
              </label>
            ))}
          </div>
        )}
        {selectedDefaults.length > 0 && (
          <p className="mt-2 text-[10px] text-[#3a6a8a]">
            Selected: {selectedDefaults.join(', ')}
          </p>
        )}
      </div>

      {saveError && (
        <p className="text-sm text-red-600 dark:text-red-400">{saveError}</p>
      )}

      <div className="flex items-center gap-3 pt-2">
        <button
          type="button"
          onClick={handleSave}
          disabled={saving}
          className="inline-flex items-center gap-1.5 rounded-lg bg-teal-600 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-teal-700 disabled:opacity-50 dark:bg-teal-700 dark:hover:bg-teal-600"
        >
          {saving && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
          Save GitOps Settings
        </button>
        {justSaved && (
          <span className="flex items-center gap-1 text-xs text-green-600 dark:text-green-400">
            <CheckCircle className="h-3.5 w-3.5" /> Saved
          </span>
        )}
      </div>
    </div>
  )
}

// Build a full connection update payload, preserving existing connection data
function buildConnectionPayload(
  conn: { name: string; git_provider: string; git_repo_identifier: string; argocd_server_url: string; argocd_namespace: string },
  gitopsForm: GitOpsFormData
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
    gitops: {
      base_branch: gitopsForm.gitops_base_branch || 'main',
      branch_prefix: 'sharko/',
      commit_prefix: 'sharko:',
      pr_auto_merge: gitopsForm.gitops_pr_auto_merge,
      host_cluster_name: gitopsForm.gitops_host_cluster_name || undefined,
      default_addons: gitopsForm.gitops_default_addons || undefined,
    },
  }
}

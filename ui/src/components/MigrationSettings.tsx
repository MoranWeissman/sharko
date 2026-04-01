import { useState, useEffect, useCallback } from 'react'
import {
  Loader2,
  CheckCircle,
  XCircle,
  ChevronDown,
  ChevronUp,
  Shield,
} from 'lucide-react'
import { api } from '@/services/api'
import type { MigrationSettings as MigrationSettingsType } from '@/services/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { SearchableSelect } from '@/components/SearchableSelect'

interface MigrationSettingsProps {
  onConfigured: () => void
}

export function MigrationSettings({ onConfigured }: MigrationSettingsProps) {
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [configured, setConfigured] = useState(false)
  const [editing, setEditing] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Git provider
  const [gitProvider, setGitProvider] = useState<'github' | 'azuredevops'>('github')
  // GitHub fields
  const [ghOwner, setGhOwner] = useState('')
  const [ghRepo, setGhRepo] = useState('')
  const [ghToken, setGhToken] = useState('')
  // Azure DevOps fields
  const [azOrg, setAzOrg] = useState('')
  const [azProject, setAzProject] = useState('')
  const [azRepo, setAzRepo] = useState('')
  const [azPat, setAzPat] = useState('')
  // ArgoCD fields
  const [argoUrl, setArgoUrl] = useState('')
  const [argoToken, setArgoToken] = useState('')
  const [argoNamespace, setArgoNamespace] = useState('argocd')
  const [argoInsecure, setArgoInsecure] = useState(false)

  // Azure DevOps auto-discovery
  const [azProjects, setAzProjects] = useState<string[]>([])
  const [azRepos, setAzRepos] = useState<string[]>([])
  const [loadingProjects, setLoadingProjects] = useState(false)
  const [loadingRepos, setLoadingRepos] = useState(false)
  const [azDiscovered, setAzDiscovered] = useState(false)

  // Test results
  const [testingGit, setTestingGit] = useState(false)
  const [testingArgo, setTestingArgo] = useState(false)
  const [gitOk, setGitOk] = useState<boolean | null>(null)
  const [argoOk, setArgoOk] = useState<boolean | null>(null)

  const [collapsed, setCollapsed] = useState(false)

  const loadSettings = useCallback(async () => {
    try {
      setLoading(true)
      const settings = await api.getMigrationSettings()
      setConfigured(settings.configured)
      if (settings.configured) {
        setGitProvider(settings.old_git.provider as 'github' | 'azuredevops')
        if (settings.old_git.provider === 'github') {
          setGhOwner(settings.old_git.owner ?? '')
          setGhRepo(settings.old_git.repo ?? '')
        } else {
          setAzOrg(settings.old_git.organization ?? '')
          setAzProject(settings.old_git.project ?? '')
          setAzRepo(settings.old_git.repository ?? '')
        }
        setArgoUrl(settings.old_argocd.server_url)
        setArgoNamespace(settings.old_argocd.namespace)
        setArgoInsecure(settings.old_argocd.insecure ?? false)
        onConfigured()
      }
    } catch {
      // Settings not configured yet - that's fine
    } finally {
      setLoading(false)
    }
  }, [onConfigured])

  useEffect(() => {
    void loadSettings()
  }, [loadSettings])

  // Azure DevOps: fetch projects when org + PAT are set
  const handleDiscoverProjects = async () => {
    if (!azOrg || !azPat) return
    setLoadingProjects(true)
    setAzProjects([])
    setAzRepos([])
    setAzProject('')
    setAzRepo('')
    setAzDiscovered(false)
    try {
      const projects = await api.azureListProjects(azOrg, azPat)
      setAzProjects(projects)
      setAzDiscovered(true)
    } catch {
      setError('Failed to fetch projects. Check organization name and PAT.')
    } finally {
      setLoadingProjects(false)
    }
  }

  // Azure DevOps: fetch repos when project is selected
  const handleProjectChange = async (project: string) => {
    setAzProject(project)
    setAzRepo('')
    setAzRepos([])
    if (!project || !azOrg || !azPat) return
    setLoadingRepos(true)
    try {
      const repos = await api.azureListRepos(azOrg, project, azPat)
      setAzRepos(repos)
    } catch {
      setError('Failed to fetch repositories.')
    } finally {
      setLoadingRepos(false)
    }
  }

  const buildSettings = (): MigrationSettingsType => ({
    old_git: gitProvider === 'github'
      ? { provider: 'github', owner: ghOwner, repo: ghRepo, token: ghToken }
      : { provider: 'azuredevops', organization: azOrg, project: azProject, repository: azRepo, pat: azPat },
    old_argocd: {
      server_url: argoUrl,
      token: argoToken,
      namespace: argoNamespace,
      insecure: argoInsecure,
    },
    configured: true,
  })

  // Separate error messages for git and argocd
  const [gitError, setGitError] = useState<string | null>(null)
  const [argoError, setArgoError] = useState<string | null>(null)

  const handleTestGit = async () => {
    setTestingGit(true)
    setGitOk(null)
    setGitError(null)
    setError(null)
    try {
      await api.saveMigrationSettings(buildSettings())
      const result = await api.testMigrationConnection() as { git: boolean; git_error: string; argocd: boolean; argocd_error: string }
      setGitOk(result.git)
      if (!result.git && result.git_error) setGitError(result.git_error)
    } catch (e: unknown) {
      setGitOk(false)
      setGitError(e instanceof Error ? e.message : 'Git connection test failed')
    } finally {
      setTestingGit(false)
    }
  }

  const handleTestArgo = async () => {
    setTestingArgo(true)
    setArgoOk(null)
    setArgoError(null)
    setError(null)
    try {
      await api.saveMigrationSettings(buildSettings())
      const result = await api.testMigrationConnection() as { git: boolean; git_error: string; argocd: boolean; argocd_error: string }
      setArgoOk(result.argocd)
      if (!result.argocd && result.argocd_error) setArgoError(result.argocd_error)
    } catch (e: unknown) {
      setArgoOk(false)
      setArgoError(e instanceof Error ? e.message : 'ArgoCD connection test failed')
    } finally {
      setTestingArgo(false)
    }
  }

  const handleSave = async () => {
    setSaving(true)
    setError(null)
    try {
      await api.saveMigrationSettings(buildSettings())
      setConfigured(true)
      setEditing(false)
      setCollapsed(true)
      onConfigured()
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to save settings')
    } finally {
      setSaving(false)
    }
  }

  const canSave = gitOk === true && argoOk === true

  if (loading) {
    return (
      <div className="rounded-xl border border-gray-200 bg-white p-6 shadow-sm dark:border-gray-700 dark:bg-gray-800">
        <div className="flex items-center gap-2 text-gray-500 dark:text-gray-400">
          <Loader2 className="h-5 w-5 animate-spin" />
          <span>Loading migration settings...</span>
        </div>
      </div>
    )
  }

  // Configured view (collapsed)
  if (configured && !editing) {
    return (
      <div className="rounded-xl border border-gray-200 bg-white shadow-sm dark:border-gray-700 dark:bg-gray-800">
        <button
          onClick={() => setCollapsed((c) => !c)}
          className="flex w-full items-center justify-between p-6"
        >
          <div className="flex items-center gap-3">
            <Shield className="h-5 w-5 text-cyan-600 dark:text-cyan-400" />
            <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
              Migration Settings
            </h3>
            <Badge variant="secondary" className="bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400">
              <CheckCircle className="mr-1 h-3 w-3" />
              Configured
            </Badge>
          </div>
          {collapsed ? <ChevronDown className="h-5 w-5 text-gray-400" /> : <ChevronUp className="h-5 w-5 text-gray-400" />}
        </button>

        {!collapsed && (
          <div className="border-t border-gray-200 p-6 dark:border-gray-700">
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div>
                <p className="text-sm font-medium text-gray-500 dark:text-gray-400">Git Provider</p>
                <p className="text-sm text-gray-900 dark:text-gray-100">{gitProvider === 'github' ? 'GitHub' : 'Azure DevOps'}</p>
              </div>
              {gitProvider === 'github' ? (
                <>
                  <div>
                    <p className="text-sm font-medium text-gray-500 dark:text-gray-400">Owner / Repo</p>
                    <p className="text-sm text-gray-900 dark:text-gray-100">{ghOwner} / {ghRepo}</p>
                  </div>
                  <div>
                    <p className="text-sm font-medium text-gray-500 dark:text-gray-400">Token</p>
                    <p className="text-sm text-gray-900 dark:text-gray-100">********</p>
                  </div>
                </>
              ) : (
                <>
                  <div>
                    <p className="text-sm font-medium text-gray-500 dark:text-gray-400">Organization / Project</p>
                    <p className="text-sm text-gray-900 dark:text-gray-100">{azOrg} / {azProject}</p>
                  </div>
                  <div>
                    <p className="text-sm font-medium text-gray-500 dark:text-gray-400">Repository</p>
                    <p className="text-sm text-gray-900 dark:text-gray-100">{azRepo}</p>
                  </div>
                  <div>
                    <p className="text-sm font-medium text-gray-500 dark:text-gray-400">PAT</p>
                    <p className="text-sm text-gray-900 dark:text-gray-100">********</p>
                  </div>
                </>
              )}
              <div>
                <p className="text-sm font-medium text-gray-500 dark:text-gray-400">ArgoCD Server</p>
                <p className="text-sm text-gray-900 dark:text-gray-100">{argoUrl}</p>
              </div>
              <div>
                <p className="text-sm font-medium text-gray-500 dark:text-gray-400">ArgoCD Namespace</p>
                <p className="text-sm text-gray-900 dark:text-gray-100">{argoNamespace}</p>
              </div>
            </div>
            <div className="mt-4 flex items-center gap-3">
              <Button variant="outline" onClick={() => { setEditing(true); setGitOk(null); setArgoOk(null) }}>
                Reconfigure
              </Button>
              <Button
                variant="destructive"
                size="sm"
                onClick={async () => {
                  if (!confirm('Delete all migration credentials? This cannot be undone.')) return
                  try {
                    await api.saveMigrationSettings({
                      old_git: { provider: '' },
                      old_argocd: { server_url: '', token: '', namespace: '' },
                      configured: false,
                    })
                    setConfigured(false)
                    setGhToken(''); setAzPat(''); setArgoToken('')
                    setGhOwner(''); setGhRepo(''); setAzOrg(''); setAzProject(''); setAzRepo('')
                    setArgoUrl(''); setArgoNamespace('argocd')
                  } catch {
                    setError('Failed to clear settings')
                  }
                }}
              >
                Clear All Credentials
              </Button>
            </div>
          </div>
        )}
      </div>
    )
  }

  // Edit / New configuration form
  return (
    <div className="rounded-xl border border-gray-200 bg-white p-6 shadow-sm dark:border-gray-700 dark:bg-gray-800">
      <div className="mb-6 flex items-center gap-3">
        <Shield className="h-5 w-5 text-cyan-600 dark:text-cyan-400" />
        <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
          Migration Settings
        </h3>
      </div>

      {error && (
        <div className="mb-4 rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-900/30 dark:text-red-400">
          {error}
        </div>
      )}

      {/* OLD Git Repository */}
      <div className="mb-6">
        <h4 className="mb-3 text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">
          Source Git Repository (OLD)
        </h4>

        <div className="mb-3">
          <label className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">
            Git Provider
          </label>
          <select
            value={gitProvider}
            onChange={(e) => setGitProvider(e.target.value as 'github' | 'azuredevops')}
            className="h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 dark:bg-input/30"
          >
            <option value="github">GitHub</option>
            <option value="azuredevops">Azure DevOps</option>
          </select>
        </div>

        {gitProvider === 'github' ? (
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div>
              <label className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">Owner</label>
              <Input value={ghOwner} onChange={(e) => setGhOwner(e.target.value)} placeholder="org-or-user" />
            </div>
            <div>
              <label className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">Repository</label>
              <Input value={ghRepo} onChange={(e) => setGhRepo(e.target.value)} placeholder="repo-name" />
            </div>
            <div className="sm:col-span-2">
              <label className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">Token</label>
              <Input type="password" value={ghToken} onChange={(e) => setGhToken(e.target.value)} placeholder="ghp_..." />
            </div>
          </div>
        ) : (
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div>
              <label className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">PAT</label>
              <Input type="password" value={azPat} onChange={(e) => setAzPat(e.target.value)} placeholder="Personal Access Token" />
            </div>
            <div>
              <label className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">Organization</label>
              <Input value={azOrg} onChange={(e) => { setAzOrg(e.target.value); setAzDiscovered(false) }} placeholder="my-org" />
            </div>
            <div className="sm:col-span-2">
              <Button
                variant="outline"
                size="sm"
                onClick={handleDiscoverProjects}
                disabled={!azOrg || !azPat || loadingProjects}
              >
                {loadingProjects ? <Loader2 className="mr-1 h-4 w-4 animate-spin" /> : null}
                {azDiscovered ? 'Refresh' : 'Connect & Discover'}
              </Button>
              {azDiscovered && <CheckCircle className="ml-2 inline h-4 w-4 text-green-500" />}
            </div>

            {azProjects.length > 0 && (
              <div>
                <label className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">Project</label>
                <SearchableSelect
                  options={azProjects}
                  value={azProject}
                  onChange={handleProjectChange}
                  placeholder="Search projects..."
                />
              </div>
            )}

            {azRepos.length > 0 && (
              <div>
                <label className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">Repository</label>
                <SearchableSelect
                  options={azRepos}
                  value={azRepo}
                  onChange={setAzRepo}
                  placeholder="Search repositories..."
                />
              </div>
            )}

            {loadingRepos && (
              <div className="flex items-center gap-2 text-sm text-gray-500">
                <Loader2 className="h-4 w-4 animate-spin" />
                Loading repositories...
              </div>
            )}
          </div>
        )}

        <div className="mt-3 flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={handleTestGit}
            disabled={testingGit}
          >
            {testingGit ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
            Test Git Connection
          </Button>
          {gitOk === true && <CheckCircle className="h-4 w-4 text-green-500" />}
          {gitOk === false && <XCircle className="h-4 w-4 text-red-500" />}
          {gitError && <span className="text-xs text-red-500">{gitError}</span>}
        </div>
      </div>

      {/* OLD ArgoCD */}
      <div className="mb-6">
        <h4 className="mb-3 text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">
          Source ArgoCD (OLD)
        </h4>

        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div className="sm:col-span-2">
            <label className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">Server URL</label>
            <Input value={argoUrl} onChange={(e) => setArgoUrl(e.target.value)} placeholder="https://argocd.old-cluster.example.com" />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">Token</label>
            <Input type="password" value={argoToken} onChange={(e) => setArgoToken(e.target.value)} placeholder="ArgoCD auth token" />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">Namespace</label>
            <Input value={argoNamespace} onChange={(e) => setArgoNamespace(e.target.value)} placeholder="argocd" />
          </div>
          <div className="flex items-center gap-2 sm:col-span-2">
            <input
              type="checkbox"
              id="argoInsecure"
              checked={argoInsecure}
              onChange={(e) => setArgoInsecure(e.target.checked)}
              className="h-4 w-4 rounded border-gray-300"
            />
            <label htmlFor="argoInsecure" className="text-sm text-gray-700 dark:text-gray-300">
              Skip TLS verification (insecure)
            </label>
          </div>
        </div>

        <div className="mt-3 flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={handleTestArgo}
            disabled={testingArgo}
          >
            {testingArgo ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
            Test ArgoCD Connection
          </Button>
          {argoOk === true && <CheckCircle className="h-4 w-4 text-green-500" />}
          {argoOk === false && <XCircle className="h-4 w-4 text-red-500" />}
          {argoError && <span className="text-xs text-red-500">{argoError}</span>}
        </div>
      </div>

      {/* Actions */}
      <div className="flex items-center gap-3 border-t border-gray-200 pt-4 dark:border-gray-700">
        <Button onClick={handleSave} disabled={!canSave || saving}>
          {saving && <Loader2 className="h-4 w-4 animate-spin" />}
          Save Settings
        </Button>
        {configured && (
          <Button variant="outline" onClick={() => { setEditing(false); setGitOk(null); setArgoOk(null) }}>
            Cancel
          </Button>
        )}
        {!canSave && (
          <p className="text-sm text-gray-500 dark:text-gray-400">
            Test both connections before saving
          </p>
        )}
      </div>
    </div>
  )
}

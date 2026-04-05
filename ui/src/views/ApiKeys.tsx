import { useState, useEffect, useCallback } from 'react'
import { Key, Plus, Trash2, Loader2, Copy, Check } from 'lucide-react'
import { listTokens, createToken, revokeToken } from '@/services/api'
import { LoadingState } from '@/components/LoadingState'
import { ErrorState } from '@/components/ErrorState'
import { ConfirmationModal } from '@/components/ConfirmationModal'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'

interface TokenEntry {
  name: string
  role: string
  created_at?: string
  last_used_at?: string
}

export function ApiKeys({ embedded }: { embedded?: boolean } = {}) {
  const [tokens, setTokens] = useState<TokenEntry[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  // Create token dialog
  const [createOpen, setCreateOpen] = useState(false)
  const [newName, setNewName] = useState('')
  const [newRole, setNewRole] = useState('viewer')
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState<string | null>(null)
  const [createdToken, setCreatedToken] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)

  // Revoke token
  const [revokeTarget, setRevokeTarget] = useState<string | null>(null)
  const [revoking, setRevoking] = useState(false)
  const [revokeError, setRevokeError] = useState<string | null>(null)

  const fetchTokens = useCallback(async () => {
    try {
      setError(null)
      const data = await listTokens()
      setTokens(data ?? [])
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to load API tokens')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void fetchTokens()
  }, [fetchTokens])

  const openCreate = useCallback(() => {
    setCreateOpen(true)
    setNewName('')
    setNewRole('viewer')
    setCreateError(null)
    setCreatedToken(null)
    setCopied(false)
  }, [])

  const handleCreate = useCallback(async () => {
    if (!newName.trim()) return
    setCreating(true)
    setCreateError(null)
    try {
      const result = await createToken({ name: newName.trim(), role: newRole })
      const token = result?.token || result?.api_token || result?.value || JSON.stringify(result)
      setCreatedToken(token)
      void fetchTokens()
    } catch (e: unknown) {
      setCreateError(e instanceof Error ? e.message : 'Failed to create token')
    } finally {
      setCreating(false)
    }
  }, [newName, newRole, fetchTokens])

  const handleCopy = useCallback(() => {
    if (!createdToken) return
    navigator.clipboard.writeText(createdToken).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    }).catch(() => {})
  }, [createdToken])

  const handleRevoke = useCallback(async () => {
    if (!revokeTarget) return
    setRevoking(true)
    setRevokeError(null)
    try {
      await revokeToken(revokeTarget)
      setRevokeTarget(null)
      void fetchTokens()
    } catch (e: unknown) {
      setRevokeError(e instanceof Error ? e.message : 'Failed to revoke token')
      setRevoking(false)
    }
  }, [revokeTarget, fetchTokens])

  function formatDate(iso?: string) {
    if (!iso) return '--'
    try {
      return new Date(iso).toLocaleDateString(undefined, {
        year: 'numeric', month: 'short', day: 'numeric',
      })
    } catch {
      return iso
    }
  }

  if (loading) return <LoadingState message="Loading API tokens..." />
  if (error) return <ErrorState message={error} onRetry={fetchTokens} />

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-start justify-between gap-4">
        {!embedded && (
          <div>
            <h2 className="text-2xl font-bold text-gray-900 dark:text-gray-100">API Keys</h2>
            <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
              Manage API tokens for programmatic access to Sharko.
            </p>
          </div>
        )}
        <button
          type="button"
          onClick={openCreate}
          className="inline-flex shrink-0 items-center gap-2 rounded-lg bg-teal-600 px-4 py-2 text-sm font-medium text-white hover:bg-teal-700 dark:bg-teal-700 dark:hover:bg-teal-600"
        >
          <Plus className="h-4 w-4" />
          Create API Key
        </button>
      </div>

      {/* Token table */}
      <div className="overflow-x-auto rounded-xl border border-gray-200 bg-[#f0f7ff] shadow-sm dark:border-gray-700 dark:bg-gray-800">
        <table className="w-full text-left text-sm">
          <thead className="border-b border-gray-200 bg-[#d0e8f8] text-xs uppercase text-gray-500 dark:border-gray-700 dark:bg-gray-900 dark:text-gray-400">
            <tr>
              <th className="px-6 py-3">Name</th>
              <th className="px-6 py-3">Role</th>
              <th className="px-6 py-3">Created</th>
              <th className="px-6 py-3">Last Used</th>
              <th className="px-6 py-3">Actions</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-200 dark:divide-gray-700">
            {tokens.map((token) => (
              <tr key={token.name} className="hover:bg-[#d6eeff] dark:hover:bg-gray-700">
                <td className="px-6 py-3 font-medium text-gray-900 dark:text-gray-100">
                  <div className="flex items-center gap-2">
                    <Key className="h-4 w-4 text-gray-400" />
                    {token.name}
                  </div>
                </td>
                <td className="px-6 py-3">
                  <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${
                    token.role === 'admin'
                      ? 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'
                      : token.role === 'operator'
                      ? 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400'
                      : 'bg-gray-100 text-gray-700 dark:bg-gray-700 dark:text-gray-400'
                  }`}>
                    {token.role}
                  </span>
                </td>
                <td className="px-6 py-3 text-gray-500 dark:text-gray-400">
                  {formatDate(token.created_at)}
                </td>
                <td className="px-6 py-3 text-gray-500 dark:text-gray-400">
                  {formatDate(token.last_used_at)}
                </td>
                <td className="px-6 py-3">
                  <button
                    type="button"
                    onClick={() => { setRevokeError(null); setRevokeTarget(token.name) }}
                    className="inline-flex items-center gap-1.5 rounded-md border border-red-300 bg-[#f0f7ff] px-2.5 py-1 text-xs font-medium text-red-600 hover:bg-red-50 dark:border-red-700 dark:bg-gray-800 dark:text-red-400 dark:hover:bg-red-900/20"
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                    Revoke
                  </button>
                </td>
              </tr>
            ))}
            {tokens.length === 0 && (
              <tr>
                <td colSpan={5} className="px-6 py-8 text-center text-gray-400 dark:text-gray-500">
                  No API tokens yet. Create one to get started.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {revokeError && (
        <p className="text-sm text-red-600 dark:text-red-400">{revokeError}</p>
      )}

      {/* Revoke confirmation */}
      <ConfirmationModal
        open={!!revokeTarget}
        onClose={() => setRevokeTarget(null)}
        onConfirm={handleRevoke}
        title={`Revoke token "${revokeTarget}"?`}
        description="This will permanently invalidate this token. Any integrations using it will stop working immediately."
        confirmText="Revoke"
        destructive
        loading={revoking}
      />

      {/* Create token dialog */}
      <Dialog open={createOpen} onOpenChange={(v) => { if (!v) setCreateOpen(false) }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create API Key</DialogTitle>
            <DialogDescription>
              {createdToken
                ? 'Copy your token now — it will not be shown again.'
                : 'Create a new API token for programmatic access.'}
            </DialogDescription>
          </DialogHeader>
          {createdToken ? (
            <div className="space-y-3 py-2">
              <div className="flex items-center gap-2 rounded-md border border-gray-200 bg-[#d0e8f8] px-3 py-2 dark:border-gray-700 dark:bg-gray-900">
                <code className="flex-1 break-all text-xs text-gray-800 dark:text-gray-200">{createdToken}</code>
                <button
                  type="button"
                  onClick={handleCopy}
                  className="shrink-0 rounded p-1 text-gray-500 hover:bg-gray-200 dark:hover:bg-gray-700"
                  title="Copy token"
                >
                  {copied ? <Check className="h-4 w-4 text-green-500" /> : <Copy className="h-4 w-4" />}
                </button>
              </div>
              <p className="text-xs text-amber-600 dark:text-amber-400">
                Store this token securely. It cannot be retrieved after closing this dialog.
              </p>
            </div>
          ) : (
            <div className="space-y-4 py-2">
              <div>
                <label className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">
                  Name <span className="text-red-500">*</span>
                </label>
                <input
                  type="text"
                  value={newName}
                  onChange={(e) => setNewName(e.target.value)}
                  placeholder="e.g. ci-deploy"
                  className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 dark:placeholder-gray-500"
                />
              </div>
              <div>
                <label className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">
                  Role
                </label>
                <select
                  value={newRole}
                  onChange={(e) => setNewRole(e.target.value)}
                  className="w-full rounded-md border border-gray-300 px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200"
                >
                  <option value="viewer">viewer</option>
                  <option value="operator">operator</option>
                  <option value="admin">admin</option>
                </select>
              </div>
              {createError && <p className="text-sm text-red-600 dark:text-red-400">{createError}</p>}
            </div>
          )}
          <DialogFooter>
            <button
              type="button"
              onClick={() => setCreateOpen(false)}
              disabled={creating}
              className="rounded-md border border-gray-300 bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-gray-700 hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
            >
              {createdToken ? 'Done' : 'Cancel'}
            </button>
            {!createdToken && (
              <button
                type="button"
                onClick={handleCreate}
                disabled={!newName.trim() || creating}
                className="inline-flex items-center gap-2 rounded-md bg-teal-600 px-4 py-2 text-sm font-medium text-white hover:bg-teal-700 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-teal-700 dark:hover:bg-teal-600"
              >
                {creating && <Loader2 className="h-4 w-4 animate-spin" />}
                Create Key
              </button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

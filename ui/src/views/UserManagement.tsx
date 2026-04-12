import { useState, useEffect, useCallback } from 'react'
import { Users, Plus, Shield, Eye, Wrench, Trash2, Key, Check, X, Copy, Loader2, AlertTriangle, Clock } from 'lucide-react'
import { api, listTokens, createToken, revokeToken } from '@/services/api'
import { Button } from '@/components/ui/button'
import { LoadingState } from '@/components/LoadingState'
import { ErrorState } from '@/components/ErrorState'
import { ConfirmationModal } from '@/components/ConfirmationModal'
import type { APIToken } from '@/services/models'

interface UserAccount {
  username: string
  enabled: boolean
  role: string
}

const EXPIRY_OPTIONS = [
  { label: '30 days', value: '30d' },
  { label: '90 days', value: '90d' },
  { label: '180 days', value: '180d' },
  { label: '365 days', value: '365d' },
  { label: 'No expiry', value: '0' },
] as const

const ROLE_COLORS: Record<string, string> = {
  admin: 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400',
  operator: 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400',
  viewer: 'bg-[#d6eeff] text-[#0a3a5a] dark:bg-gray-800 dark:text-gray-400',
}

export function UserManagement({ embedded }: { embedded?: boolean } = {}) {
  const [users, setUsers] = useState<UserAccount[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [showAddForm, setShowAddForm] = useState(false)
  const [newUsername, setNewUsername] = useState('')
  const [newRole, setNewRole] = useState('viewer')
  const [tempPassword, setTempPassword] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)

  // Token management state
  const [tokens, setTokens] = useState<APIToken[]>([])
  const [tokensLoading, setTokensLoading] = useState(true)
  const [tokensError, setTokensError] = useState<string | null>(null)
  const [showTokenForm, setShowTokenForm] = useState(false)
  const [tokenName, setTokenName] = useState('')
  const [tokenRole, setTokenRole] = useState('viewer')
  const [tokenExpires, setTokenExpires] = useState('365d')
  const [creatingToken, setCreatingToken] = useState(false)
  const [tokenCreateError, setTokenCreateError] = useState<string | null>(null)
  const [createdTokenValue, setCreatedTokenValue] = useState<string | null>(null)
  const [tokenCopied, setTokenCopied] = useState(false)
  const [revokeTarget, setRevokeTarget] = useState<string | null>(null)
  const [revoking, setRevoking] = useState(false)
  const [revokeError, setRevokeError] = useState<string | null>(null)

  const fetchTokens = useCallback(async () => {
    try {
      setTokensError(null)
      const data = await listTokens()
      setTokens(data ?? [])
    } catch (e: unknown) {
      setTokensError(e instanceof Error ? e.message : 'Failed to load tokens')
    } finally {
      setTokensLoading(false)
    }
  }, [])

  const fetchUsers = useCallback(async () => {
    try {
      setError(null)
      const data = await api.listUsers()
      setUsers(data ?? [])
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to load users')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void fetchUsers()
    void fetchTokens()
  }, [fetchUsers, fetchTokens])

  const handleCreate = async () => {
    if (!newUsername.trim()) return
    setActionError(null)
    try {
      const result = await api.createUser({ username: newUsername.trim(), role: newRole })
      setTempPassword(result.temp_password)
      setNewUsername('')
      setNewRole('viewer')
      setShowAddForm(false)
      void fetchUsers()
    } catch (e: unknown) {
      setActionError(e instanceof Error ? e.message : 'Failed to create user')
    }
  }

  const handleToggleEnabled = async (user: UserAccount) => {
    setActionError(null)
    try {
      await api.updateUser(user.username, { enabled: !user.enabled })
      void fetchUsers()
    } catch (e: unknown) {
      setActionError(e instanceof Error ? e.message : 'Failed to update user')
    }
  }

  const handleChangeRole = async (user: UserAccount, role: string) => {
    setActionError(null)
    try {
      await api.updateUser(user.username, { role })
      void fetchUsers()
    } catch (e: unknown) {
      setActionError(e instanceof Error ? e.message : 'Failed to update role')
    }
  }

  const handleResetPassword = async (username: string) => {
    if (!confirm(`Reset password for ${username}?`)) return
    setActionError(null)
    try {
      const result = await api.resetPassword(username)
      setTempPassword(result.temp_password)
    } catch (e: unknown) {
      setActionError(e instanceof Error ? e.message : 'Failed to reset password')
    }
  }

  const handleDelete = async (username: string) => {
    if (!confirm(`Delete user "${username}"? This cannot be undone.`)) return
    setActionError(null)
    try {
      await api.deleteUser(username)
      void fetchUsers()
    } catch (e: unknown) {
      setActionError(e instanceof Error ? e.message : 'Failed to delete user')
    }
  }

  // Token handlers
  const openTokenForm = () => {
    setShowTokenForm(true)
    setTokenName('')
    setTokenRole('viewer')
    setTokenExpires('365d')
    setTokenCreateError(null)
    setCreatedTokenValue(null)
    setTokenCopied(false)
  }

  const handleCreateToken = async () => {
    if (!tokenName.trim()) return
    setCreatingToken(true)
    setTokenCreateError(null)
    try {
      const result = await createToken({
        name: tokenName.trim(),
        role: tokenRole,
        expires: tokenExpires,
      })
      const token = result?.token || result?.api_token || result?.value || JSON.stringify(result)
      setCreatedTokenValue(token)
      setShowTokenForm(false)
      void fetchTokens()
    } catch (e: unknown) {
      setTokenCreateError(e instanceof Error ? e.message : 'Failed to create token')
    } finally {
      setCreatingToken(false)
    }
  }

  const handleCopyToken = () => {
    if (!createdTokenValue) return
    navigator.clipboard.writeText(createdTokenValue).then(() => {
      setTokenCopied(true)
      setTimeout(() => setTokenCopied(false), 2000)
    }).catch(() => {})
  }

  const handleRevokeToken = async () => {
    if (!revokeTarget) return
    setRevoking(true)
    setRevokeError(null)
    try {
      await revokeToken(revokeTarget)
      setRevokeTarget(null)
      void fetchTokens()
    } catch (e: unknown) {
      setRevokeError(e instanceof Error ? e.message : 'Failed to revoke token')
    } finally {
      setRevoking(false)
    }
  }

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

  function tokenStatusBadge(token: APIToken) {
    if (token.expired) {
      return (
        <span className="inline-flex items-center gap-1 rounded-full bg-red-100 px-2 py-0.5 text-xs font-medium text-red-700 dark:bg-red-900/30 dark:text-red-400">
          <X className="h-3 w-3" />
          Expired
        </span>
      )
    }
    if (token.expiring_soon) {
      return (
        <span className="inline-flex items-center gap-1 rounded-full bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-400">
          <AlertTriangle className="h-3 w-3" />
          Expiring Soon
        </span>
      )
    }
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-green-100 px-2 py-0.5 text-xs font-medium text-green-700 dark:bg-green-900/30 dark:text-green-400">
        <Check className="h-3 w-3" />
        Active
      </span>
    )
  }

  return (
    <div className="mx-auto max-w-screen-lg space-y-6">
      {!embedded && (
        <div className="flex items-center gap-3">
          <Users className="h-7 w-7 text-teal-600 dark:text-teal-400" />
          <div>
            <h1 className="text-2xl font-bold text-[#0a2a4a] dark:text-white">User Management</h1>
            <p className="text-sm text-[#2a5a7a] dark:text-gray-400">Manage user accounts, roles, and access</p>
          </div>
        </div>
      )}

      {/* Temp password banner */}
      {tempPassword && (
        <div className="rounded-xl border-2 border-green-300 bg-green-50 p-4 dark:border-green-700 dark:bg-green-900/20">
          <div className="flex items-center justify-between">
            <div>
              <h3 className="text-sm font-semibold text-green-800 dark:text-green-300">Temporary Password</h3>
              <p className="mt-1 text-xs text-green-600 dark:text-green-400">Share this with the user. They must change it on first login.</p>
              <code className="mt-2 block rounded bg-green-100 px-3 py-2 font-mono text-lg font-bold text-green-900 dark:bg-green-800 dark:text-green-100">
                {tempPassword}
              </code>
            </div>
            <button onClick={() => setTempPassword(null)} className="text-green-500 hover:text-green-700">
              <X className="h-5 w-5" />
            </button>
          </div>
        </div>
      )}

      {/* Error banner */}
      {actionError && (
        <div className="rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-900/20 dark:text-red-400">
          {actionError}
        </div>
      )}

      {/* Role legend */}
      <div className="flex flex-wrap gap-4 rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 shadow-sm dark:border-gray-700 dark:bg-gray-800">
        <div className="text-xs font-medium text-[#2a5a7a] dark:text-gray-400">Roles:</div>
        <div className="flex items-center gap-1.5 text-xs">
          <Shield className="h-3.5 w-3.5 text-red-500" />
          <span className="font-medium text-[#0a3a5a] dark:text-gray-300">Admin</span>
          <span className="text-[#3a6a8a]">— full access, user management</span>
        </div>
        <div className="flex items-center gap-1.5 text-xs">
          <Wrench className="h-3.5 w-3.5 text-blue-500" />
          <span className="font-medium text-[#0a3a5a] dark:text-gray-300">Operator</span>
          <span className="text-[#3a6a8a]">— all except user management</span>
        </div>
        <div className="flex items-center gap-1.5 text-xs">
          <Eye className="h-3.5 w-3.5 text-[#2a5a7a]" />
          <span className="font-medium text-[#0a3a5a] dark:text-gray-300">Viewer</span>
          <span className="text-[#3a6a8a]">— read-only access</span>
        </div>
      </div>

      {/* Users table */}
      <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] shadow-sm dark:border-gray-700 dark:bg-gray-800">
        <div className="flex items-center justify-between border-b border-[#6aade0] p-4 dark:border-gray-700">
          <h3 className="text-lg font-semibold text-[#0a2a4a] dark:text-gray-100">
            Users ({users.length})
          </h3>
          <Button onClick={() => setShowAddForm(true)} disabled={showAddForm}>
            <Plus className="h-4 w-4" />
            Add User
          </Button>
        </div>

        {/* Add user form */}
        {showAddForm && (
          <div className="border-b border-[#6aade0] bg-[#d0e8f8] p-4 dark:border-gray-700 dark:bg-gray-900/50">
            <div className="flex items-end gap-3">
              <div className="flex-1">
                <label className="mb-1 block text-xs font-medium text-[#1a4a6a] dark:text-gray-400">Username</label>
                <input
                  type="text"
                  value={newUsername}
                  onChange={(e) => setNewUsername(e.target.value)}
                  placeholder="e.g. john.doe"
                  className="w-full rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-2 text-sm dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100"
                  autoFocus
                />
              </div>
              <div className="w-40">
                <label className="mb-1 block text-xs font-medium text-[#1a4a6a] dark:text-gray-400">Role</label>
                <select
                  value={newRole}
                  onChange={(e) => setNewRole(e.target.value)}
                  className="w-full rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-2 text-sm dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100"
                >
                  <option value="viewer">Viewer</option>
                  <option value="operator">Operator</option>
                  <option value="admin">Admin</option>
                </select>
              </div>
              <Button onClick={handleCreate} disabled={!newUsername.trim()}>
                <Check className="h-4 w-4" />
                Create
              </Button>
              <Button variant="outline" onClick={() => { setShowAddForm(false); setNewUsername(''); }}>
                Cancel
              </Button>
            </div>
          </div>
        )}

        {loading ? (
          <LoadingState message="Loading users..." />
        ) : error ? (
          <ErrorState message={error} onRetry={fetchUsers} />
        ) : users.length === 0 ? (
          <div className="py-12 text-center text-[#2a5a7a]">
            <Users className="mx-auto mb-3 h-10 w-10 text-[#5a8aaa] dark:text-gray-600" />
            <p className="text-lg font-medium">No users configured</p>
            <p className="mt-1 text-sm">Add your first user above.</p>
          </div>
        ) : (
          <div className="divide-y divide-[#a0d0f0] dark:divide-gray-700">
            {users.map((user) => {
              return (
                <div key={user.username} className="flex items-center gap-4 px-4 py-3">
                  {/* Avatar */}
                  <div className={`flex h-9 w-9 items-center justify-center rounded-full text-sm font-bold ${
                    user.enabled
                      ? 'bg-teal-100 text-teal-700 dark:bg-teal-900/40 dark:text-teal-400'
                      : 'bg-[#d6eeff] text-[#3a6a8a] dark:bg-gray-800'
                  }`}>
                    {user.username.charAt(0).toUpperCase()}
                  </div>

                  {/* Name + status */}
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className={`text-sm font-medium ${user.enabled ? 'text-[#0a2a4a] dark:text-gray-100' : 'text-[#3a6a8a] line-through'}`}>
                        {user.username}
                      </span>
                      {!user.enabled && (
                        <span className="rounded bg-[#d6eeff] px-1.5 py-0.5 text-[10px] text-[#2a5a7a] dark:bg-gray-800">disabled</span>
                      )}
                    </div>
                  </div>

                  {/* Role badge */}
                  <select
                    value={user.role}
                    onChange={(e) => handleChangeRole(user, e.target.value)}
                    className={`rounded-lg border-0 px-2 py-1 text-xs font-medium ${ROLE_COLORS[user.role] || ROLE_COLORS.viewer}`}
                  >
                    <option value="viewer">Viewer</option>
                    <option value="operator">Operator</option>
                    <option value="admin">Admin</option>
                  </select>

                  {/* Actions */}
                  <div className="flex items-center gap-1">
                    <button
                      onClick={() => handleToggleEnabled(user)}
                      className="rounded-lg p-2 text-[#3a6a8a] hover:bg-[#d6eeff] hover:text-[#1a4a6a] dark:hover:bg-gray-700"
                      title={user.enabled ? 'Disable user' : 'Enable user'}
                    >
                      {user.enabled ? <X className="h-4 w-4" /> : <Check className="h-4 w-4" />}
                    </button>
                    <button
                      onClick={() => handleResetPassword(user.username)}
                      className="rounded-lg p-2 text-[#3a6a8a] hover:bg-[#d6eeff] hover:text-[#1a4a6a] dark:hover:bg-gray-700"
                      title="Reset password"
                    >
                      <Key className="h-4 w-4" />
                    </button>
                    <button
                      onClick={() => handleDelete(user.username)}
                      className="rounded-lg p-2 text-[#3a6a8a] hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20"
                      title="Delete user"
                    >
                      <Trash2 className="h-4 w-4" />
                    </button>
                  </div>
                </div>
              )
            })}
          </div>
        )}
      </div>

      {/* Created token banner */}
      {createdTokenValue && (
        <div className="rounded-xl ring-2 ring-amber-400 bg-amber-50 p-4 dark:ring-amber-600 dark:bg-amber-900/20">
          <div className="flex items-start justify-between gap-3">
            <div className="flex-1 space-y-2">
              <h3 className="text-sm font-semibold text-amber-800 dark:text-amber-300">New API Token Created</h3>
              <div className="flex items-center gap-2 rounded-md ring-2 ring-amber-300 bg-white px-3 py-2 dark:ring-amber-700 dark:bg-gray-900">
                <code className="flex-1 break-all text-xs font-bold text-[#0a3a5a] dark:text-gray-200">{createdTokenValue}</code>
                <button
                  type="button"
                  onClick={handleCopyToken}
                  className="shrink-0 rounded p-1 text-amber-600 hover:bg-amber-100 dark:hover:bg-amber-800/30"
                  title="Copy token"
                >
                  {tokenCopied ? <Check className="h-4 w-4 text-green-500" /> : <Copy className="h-4 w-4" />}
                </button>
              </div>
              <p className="flex items-center gap-1.5 text-xs text-red-600 dark:text-red-400">
                <AlertTriangle className="h-3.5 w-3.5" />
                This token will not be shown again. Copy it now and store it securely.
              </p>
            </div>
            <button onClick={() => setCreatedTokenValue(null)} className="text-amber-500 hover:text-amber-700">
              <X className="h-5 w-5" />
            </button>
          </div>
        </div>
      )}

      {/* API Tokens section */}
      <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] shadow-sm dark:border-gray-700 dark:bg-gray-800">
        <div className="flex items-center justify-between border-b border-[#6aade0] p-4 dark:border-gray-700">
          <h3 className="flex items-center gap-2 text-lg font-semibold text-[#0a2a4a] dark:text-gray-100">
            <Key className="h-5 w-5 text-[#3a6a8a]" />
            API Tokens ({tokens.length})
          </h3>
          <Button onClick={openTokenForm} disabled={showTokenForm}>
            <Plus className="h-4 w-4" />
            Create Token
          </Button>
        </div>

        {/* Create token form */}
        {showTokenForm && (
          <div className="border-b border-[#6aade0] bg-[#d0e8f8] p-4 dark:border-gray-700 dark:bg-gray-900/50">
            <div className="flex items-end gap-3 flex-wrap">
              <div className="flex-1 min-w-[160px]">
                <label className="mb-1 block text-xs font-medium text-[#1a4a6a] dark:text-gray-400">Token Name</label>
                <input
                  type="text"
                  value={tokenName}
                  onChange={(e) => setTokenName(e.target.value)}
                  placeholder="e.g. ci-deploy"
                  className="w-full rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-2 text-sm dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100"
                  autoFocus
                />
              </div>
              <div className="w-36">
                <label className="mb-1 block text-xs font-medium text-[#1a4a6a] dark:text-gray-400">Role</label>
                <select
                  value={tokenRole}
                  onChange={(e) => setTokenRole(e.target.value)}
                  className="w-full rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-2 text-sm dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100"
                >
                  <option value="viewer">Viewer</option>
                  <option value="operator">Operator</option>
                  <option value="admin">Admin</option>
                </select>
              </div>
              <div className="w-36">
                <label className="mb-1 block text-xs font-medium text-[#1a4a6a] dark:text-gray-400">Expires in</label>
                <select
                  value={tokenExpires}
                  onChange={(e) => setTokenExpires(e.target.value)}
                  className="w-full rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-2 text-sm dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100"
                >
                  {EXPIRY_OPTIONS.map(opt => (
                    <option key={opt.value} value={opt.value}>{opt.label}</option>
                  ))}
                </select>
              </div>
              <Button onClick={handleCreateToken} disabled={!tokenName.trim() || creatingToken}>
                {creatingToken ? <Loader2 className="h-4 w-4 animate-spin" /> : <Check className="h-4 w-4" />}
                Create
              </Button>
              <Button variant="outline" onClick={() => { setShowTokenForm(false); setTokenName(''); setTokenCreateError(null) }}>
                Cancel
              </Button>
            </div>
            {tokenCreateError && (
              <p className="mt-2 text-sm text-red-600 dark:text-red-400">{tokenCreateError}</p>
            )}
          </div>
        )}

        {tokensLoading ? (
          <LoadingState message="Loading tokens..." />
        ) : tokensError ? (
          <ErrorState message={tokensError} onRetry={fetchTokens} />
        ) : tokens.length === 0 ? (
          <div className="py-12 text-center text-[#2a5a7a]">
            <Key className="mx-auto mb-3 h-10 w-10 text-[#5a8aaa] dark:text-gray-600" />
            <p className="text-lg font-medium">No API tokens</p>
            <p className="mt-1 text-sm">Create a token for programmatic access.</p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-left text-sm">
              <thead className="border-b border-[#6aade0] bg-[#d0e8f8] text-xs uppercase text-[#2a5a7a] dark:border-gray-700 dark:bg-gray-900 dark:text-gray-400">
                <tr>
                  <th className="px-5 py-3">Name</th>
                  <th className="px-5 py-3">Role</th>
                  <th className="px-5 py-3">Status</th>
                  <th className="px-5 py-3">Created</th>
                  <th className="px-5 py-3">Expires</th>
                  <th className="px-5 py-3">Last Used</th>
                  <th className="px-5 py-3">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[#a0d0f0] dark:divide-gray-700">
                {tokens.map((token) => (
                  <tr key={token.name} className={`hover:bg-[#d6eeff] dark:hover:bg-gray-700 ${token.expired ? 'opacity-60' : ''}`}>
                    <td className="px-5 py-3 font-medium text-[#0a2a4a] dark:text-gray-100">
                      <div className="flex items-center gap-2">
                        <Key className="h-4 w-4 text-[#3a6a8a]" />
                        {token.name}
                      </div>
                    </td>
                    <td className="px-5 py-3">
                      <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${
                        ROLE_COLORS[token.role] || ROLE_COLORS.viewer
                      }`}>
                        {token.role}
                      </span>
                    </td>
                    <td className="px-5 py-3">
                      {tokenStatusBadge(token)}
                    </td>
                    <td className="px-5 py-3 text-[#2a5a7a] dark:text-gray-400">
                      {formatDate(token.created_at)}
                    </td>
                    <td className="px-5 py-3 text-[#2a5a7a] dark:text-gray-400">
                      {token.expires_at ? (
                        <span className="flex items-center gap-1.5">
                          <Clock className="h-3.5 w-3.5" />
                          {formatDate(token.expires_at)}
                        </span>
                      ) : (
                        <span className="text-[#5a8aaa]">Never</span>
                      )}
                    </td>
                    <td className="px-5 py-3 text-[#2a5a7a] dark:text-gray-400">
                      {formatDate(token.last_used_at)}
                    </td>
                    <td className="px-5 py-3">
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
              </tbody>
            </table>
          </div>
        )}
      </div>

      {revokeError && (
        <p className="text-sm text-red-600 dark:text-red-400">{revokeError}</p>
      )}

      {/* Revoke confirmation modal */}
      <ConfirmationModal
        open={!!revokeTarget}
        onClose={() => setRevokeTarget(null)}
        onConfirm={handleRevokeToken}
        title={`Revoke token "${revokeTarget}"?`}
        description="This will permanently invalidate this token. Any integrations using it will stop working immediately."
        confirmText="Revoke"
        destructive
        loading={revoking}
      />
    </div>
  )
}

import { useState, useEffect, useCallback } from 'react'
import { Users, Plus, Shield, Eye, Wrench, Trash2, Key, Check, X } from 'lucide-react'
import { api } from '@/services/api'
import { Button } from '@/components/ui/button'
import { LoadingState } from '@/components/LoadingState'
import { ErrorState } from '@/components/ErrorState'

interface UserAccount {
  username: string
  enabled: boolean
  role: string
}

const ROLE_COLORS: Record<string, string> = {
  admin: 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400',
  operator: 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400',
  viewer: 'bg-gray-100 text-gray-700 dark:bg-gray-800 dark:text-gray-400',
}

export default function UserManagement() {
  const [users, setUsers] = useState<UserAccount[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [showAddForm, setShowAddForm] = useState(false)
  const [newUsername, setNewUsername] = useState('')
  const [newRole, setNewRole] = useState('viewer')
  const [tempPassword, setTempPassword] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)

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
  }, [fetchUsers])

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

  return (
    <div className="mx-auto max-w-screen-lg space-y-6">
      <div className="flex items-center gap-3">
        <Users className="h-7 w-7 text-cyan-600 dark:text-cyan-400" />
        <div>
          <h1 className="text-2xl font-bold text-gray-900 dark:text-white">User Management</h1>
          <p className="text-sm text-gray-500 dark:text-gray-400">Manage user accounts, roles, and access</p>
        </div>
      </div>

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
      <div className="flex flex-wrap gap-4 rounded-xl border border-gray-200 bg-white p-4 shadow-sm dark:border-gray-700 dark:bg-gray-800">
        <div className="text-xs font-medium text-gray-500 dark:text-gray-400">Roles:</div>
        <div className="flex items-center gap-1.5 text-xs">
          <Shield className="h-3.5 w-3.5 text-red-500" />
          <span className="font-medium text-gray-700 dark:text-gray-300">Admin</span>
          <span className="text-gray-400">— full access, user management</span>
        </div>
        <div className="flex items-center gap-1.5 text-xs">
          <Wrench className="h-3.5 w-3.5 text-blue-500" />
          <span className="font-medium text-gray-700 dark:text-gray-300">Operator</span>
          <span className="text-gray-400">— all except user management</span>
        </div>
        <div className="flex items-center gap-1.5 text-xs">
          <Eye className="h-3.5 w-3.5 text-gray-500" />
          <span className="font-medium text-gray-700 dark:text-gray-300">Viewer</span>
          <span className="text-gray-400">— read-only access</span>
        </div>
      </div>

      {/* Users table */}
      <div className="rounded-xl border border-gray-200 bg-white shadow-sm dark:border-gray-700 dark:bg-gray-800">
        <div className="flex items-center justify-between border-b border-gray-200 p-4 dark:border-gray-700">
          <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
            Users ({users.length})
          </h3>
          <Button onClick={() => setShowAddForm(true)} disabled={showAddForm}>
            <Plus className="h-4 w-4" />
            Add User
          </Button>
        </div>

        {/* Add user form */}
        {showAddForm && (
          <div className="border-b border-gray-200 bg-gray-50 p-4 dark:border-gray-700 dark:bg-gray-900/50">
            <div className="flex items-end gap-3">
              <div className="flex-1">
                <label className="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">Username</label>
                <input
                  type="text"
                  value={newUsername}
                  onChange={(e) => setNewUsername(e.target.value)}
                  placeholder="e.g. john.doe"
                  className="w-full rounded-lg border border-gray-300 bg-white px-3 py-2 text-sm dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100"
                  autoFocus
                />
              </div>
              <div className="w-40">
                <label className="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">Role</label>
                <select
                  value={newRole}
                  onChange={(e) => setNewRole(e.target.value)}
                  className="w-full rounded-lg border border-gray-300 bg-white px-3 py-2 text-sm dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100"
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
          <div className="py-12 text-center text-gray-500">
            <Users className="mx-auto mb-3 h-10 w-10 text-gray-300 dark:text-gray-600" />
            <p className="text-lg font-medium">No users configured</p>
            <p className="mt-1 text-sm">Add your first user above.</p>
          </div>
        ) : (
          <div className="divide-y divide-gray-100 dark:divide-gray-700">
            {users.map((user) => {
              return (
                <div key={user.username} className="flex items-center gap-4 px-4 py-3">
                  {/* Avatar */}
                  <div className={`flex h-9 w-9 items-center justify-center rounded-full text-sm font-bold ${
                    user.enabled
                      ? 'bg-cyan-100 text-cyan-700 dark:bg-cyan-900/40 dark:text-cyan-400'
                      : 'bg-gray-100 text-gray-400 dark:bg-gray-800'
                  }`}>
                    {user.username.charAt(0).toUpperCase()}
                  </div>

                  {/* Name + status */}
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className={`text-sm font-medium ${user.enabled ? 'text-gray-900 dark:text-gray-100' : 'text-gray-400 line-through'}`}>
                        {user.username}
                      </span>
                      {!user.enabled && (
                        <span className="rounded bg-gray-100 px-1.5 py-0.5 text-[10px] text-gray-500 dark:bg-gray-800">disabled</span>
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
                      className="rounded-lg p-2 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-gray-700"
                      title={user.enabled ? 'Disable user' : 'Enable user'}
                    >
                      {user.enabled ? <X className="h-4 w-4" /> : <Check className="h-4 w-4" />}
                    </button>
                    <button
                      onClick={() => handleResetPassword(user.username)}
                      className="rounded-lg p-2 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-gray-700"
                      title="Reset password"
                    >
                      <Key className="h-4 w-4" />
                    </button>
                    <button
                      onClick={() => handleDelete(user.username)}
                      className="rounded-lg p-2 text-gray-400 hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20"
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
    </div>
  )
}

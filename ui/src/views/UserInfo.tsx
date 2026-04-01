import { useState } from 'react'
import { Lock } from 'lucide-react'
import { api } from '@/services/api'

export function UserInfo() {
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [status, setStatus] = useState<{ type: 'success' | 'error'; message: string } | null>(null)
  const [loading, setLoading] = useState(false)

  const handleUpdatePassword = async (e: React.FormEvent) => {
    e.preventDefault()
    setStatus(null)

    if (!currentPassword || !newPassword) {
      setStatus({ type: 'error', message: 'All fields are required' })
      return
    }
    if (newPassword !== confirmPassword) {
      setStatus({ type: 'error', message: 'New passwords do not match' })
      return
    }
    if (newPassword.length < 8) {
      setStatus({ type: 'error', message: 'Password must be at least 8 characters' })
      return
    }

    setLoading(true)
    try {
      await api.updatePassword(currentPassword, newPassword)
      setStatus({ type: 'success', message: 'Password updated successfully. Use the new password on next login.' })
      setCurrentPassword('')
      setNewPassword('')
      setConfirmPassword('')
    } catch (err) {
      setStatus({ type: 'error', message: err instanceof Error ? err.message : 'Failed to update password' })
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="space-y-6">
      <h2 className="text-2xl font-bold text-gray-900 dark:text-gray-100">User Info</h2>

      {/* Update Password */}
      <div className="max-w-lg">
        <button
          type="button"
          className="mb-4 inline-flex items-center gap-2 rounded-lg bg-gray-500 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-gray-600 dark:bg-gray-600 dark:hover:bg-gray-500"
          onClick={() => document.getElementById('password-form')?.classList.toggle('hidden')}
        >
          <Lock className="h-4 w-4" />
          UPDATE PASSWORD
        </button>

        <form id="password-form" onSubmit={handleUpdatePassword} className="hidden space-y-4 rounded-lg border border-gray-200 bg-gray-50 p-6 dark:border-gray-700 dark:bg-gray-800">
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-300">Current Password</label>
            <input
              type="password"
              value={currentPassword}
              onChange={(e) => setCurrentPassword(e.target.value)}
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-cyan-500 focus:outline-none focus:ring-1 focus:ring-cyan-500 dark:border-gray-600 dark:bg-gray-900 dark:text-white"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-300">New Password</label>
            <input
              type="password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-cyan-500 focus:outline-none focus:ring-1 focus:ring-cyan-500 dark:border-gray-600 dark:bg-gray-900 dark:text-white"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-300">Confirm New Password</label>
            <input
              type="password"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              className="mt-1 block w-full rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-cyan-500 focus:outline-none focus:ring-1 focus:ring-cyan-500 dark:border-gray-600 dark:bg-gray-900 dark:text-white"
            />
          </div>

          {status && (
            <p className={`text-sm ${status.type === 'success' ? 'text-green-600 dark:text-green-400' : 'text-red-600 dark:text-red-400'}`}>
              {status.message}
            </p>
          )}

          <button
            type="submit"
            disabled={loading}
            className="rounded-lg bg-cyan-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-cyan-700 disabled:opacity-50 dark:bg-cyan-700 dark:hover:bg-cyan-600"
          >
            {loading ? 'Updating...' : 'Save Password'}
          </button>
        </form>
      </div>

      {/* User Details */}
      <div className="max-w-lg rounded-lg bg-gray-50 p-6 dark:bg-gray-800">
        <div className="space-y-2 text-sm text-gray-600 dark:text-gray-400">
          <p><span className="font-medium text-gray-700 dark:text-gray-300">Username:</span> admin</p>
          <p><span className="font-medium text-gray-700 dark:text-gray-300">Issuer:</span> local</p>
        </div>
      </div>
    </div>
  )
}

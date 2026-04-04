import { useState } from 'react'
import { useAuth } from '@/hooks/useAuth'

export function Login() {
  const { login, error: authError } = useAuth()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!username || !password) {
      setError('Username and password are required')
      return
    }
    setError('')
    setLoading(true)
    try {
      await login(username, password)
    } catch {
      setError(authError || 'Invalid credentials')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="flex min-h-screen">
      {/* Left side — mascot (desktop only) */}
      <div className="hidden lg:flex lg:w-3/4 bg-[#0B1426] items-center justify-center">
        <img
          src="/sharko-mascot-login.png"
          alt="Sharko mascot"
          className="max-h-[80vh] max-w-full object-contain"
        />
      </div>

      {/* Right side — login panel */}
      <div className="flex w-full flex-col bg-gray-900 lg:w-1/4 lg:min-w-[320px]">
        <div className="flex flex-1 flex-col items-center justify-center px-8 py-12">
          {/* Brand header */}
          <div className="mb-8 flex items-center gap-2">
            <img src="/sharko-icon-32.png" alt="" className="h-8 w-8" />
            <span className="font-semibold text-cyan-500 text-[28px]">sharko</span>
          </div>

          <form onSubmit={handleSubmit} className="w-full max-w-sm space-y-5">
            <div>
              <label
                htmlFor="username"
                className="block text-sm font-medium text-gray-300"
              >
                Username
              </label>
              <input
                id="username"
                type="text"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                autoComplete="username"
                autoFocus
                className="mt-1 block w-full rounded-lg border border-gray-600 bg-gray-800 px-4 py-2.5 text-sm text-white placeholder-gray-500 focus:border-cyan-500 focus:outline-none focus:ring-1 focus:ring-cyan-500"
                placeholder="admin"
              />
            </div>

            <div>
              <label
                htmlFor="password"
                className="block text-sm font-medium text-gray-300"
              >
                Password
              </label>
              <input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="current-password"
                className="mt-1 block w-full rounded-lg border border-gray-600 bg-gray-800 px-4 py-2.5 text-sm text-white placeholder-gray-500 focus:border-cyan-500 focus:outline-none focus:ring-1 focus:ring-cyan-500"
                placeholder="Password"
              />
            </div>

            {error && (
              <p className="text-sm text-red-400">{error}</p>
            )}

            <button
              type="submit"
              disabled={loading}
              className="w-full rounded-lg bg-cyan-600 px-4 py-2.5 text-sm font-semibold text-white transition-colors hover:bg-cyan-700 focus:outline-none focus:ring-2 focus:ring-cyan-500 focus:ring-offset-2 focus:ring-offset-gray-900 disabled:opacity-50"
            >
              {loading ? 'Signing in...' : 'Sign In'}
            </button>
          </form>
        </div>

        {/* Footer */}
        <p className="pb-6 text-center text-xs text-gray-600">
          Sharko v1.0.0
        </p>
      </div>
    </div>
  )
}

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
    <div className="relative flex min-h-screen items-center justify-center overflow-hidden">
      {/* Background image — covers viewport, stays centered on resize */}
      <img
        src="/login-bg.jpg"
        alt=""
        className="absolute inset-0 h-full w-full object-cover"
      />

      {/* Subtle animated overlay for depth */}
      <div className="absolute inset-0 bg-gradient-to-br from-gray-900/30 via-transparent to-gray-900/40" />

      {/* Desktop: side-by-side layout */}
      <div className="relative flex w-full min-h-screen">
        {/* Left side — background shows through (desktop only) */}
        <div className="hidden flex-1 lg:block" />

        {/* Right side — login form */}
        <div className="flex w-full flex-col items-center justify-center px-6 py-12 lg:w-[420px] lg:min-w-[420px] lg:bg-gray-900/80 lg:backdrop-blur-xl lg:px-8">
          <div className="w-full max-w-sm rounded-2xl bg-gray-900/80 p-8 shadow-2xl backdrop-blur-xl lg:rounded-none lg:bg-transparent lg:p-0 lg:shadow-none lg:backdrop-blur-none">
            {/* Logo */}
            <div className="mb-8 text-center">
              <h1 className="text-3xl font-bold text-white">
                AAP
              </h1>
              <p className="mt-1 text-sm font-medium text-gray-300">
                ArgoCD Addons Platform
              </p>
              <p className="mt-0.5 text-xs text-gray-500">
                Control plane for Kubernetes add-ons
              </p>
            </div>

            <form onSubmit={handleSubmit} className="space-y-5">
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
                  className="mt-1 block w-full rounded-lg border border-gray-600 bg-gray-800/70 px-4 py-2.5 text-sm text-white placeholder-gray-500 focus:border-cyan-500 focus:outline-none focus:ring-1 focus:ring-cyan-500"
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
                  className="mt-1 block w-full rounded-lg border border-gray-600 bg-gray-800/70 px-4 py-2.5 text-sm text-white placeholder-gray-500 focus:border-cyan-500 focus:outline-none focus:ring-1 focus:ring-cyan-500"
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
                {loading ? 'Signing in...' : 'SIGN IN'}
              </button>
            </form>

            {/* Footer */}
            <p className="mt-12 text-center text-[10px] text-gray-600">
              AAP
            </p>
          </div>
        </div>
      </div>
    </div>
  )
}

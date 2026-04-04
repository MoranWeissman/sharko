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
      {/* Left side — background image centered (ArgoCD style) */}
      <div
        className="hidden lg:block lg:flex-1 bg-[#0B1426] bg-contain bg-center bg-no-repeat"
        style={{ backgroundImage: "url('/sharko-login-bg.png')" }}
      />

      {/* Right side — login panel (narrow, ArgoCD style) */}
      <div className="flex w-full flex-col bg-gray-900 lg:w-[320px] lg:min-w-[280px]">
        <div className="flex flex-1 flex-col items-end justify-start px-8 pt-16 lg:pt-12">
          {/* Brand name — top right, like ArgoCD */}
          <div className="mb-12">
            <span className="font-bold text-cyan-400 text-[28px] tracking-tight">sharko</span>
          </div>

          <form onSubmit={handleSubmit} className="w-full space-y-6">
            <div>
              <label htmlFor="username" className="block text-xs text-gray-400 mb-1">
                Username
              </label>
              <input
                id="username"
                type="text"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                autoComplete="username"
                autoFocus
                className="block w-full border-0 border-b border-gray-600 bg-transparent px-0 py-2 text-sm text-white placeholder-gray-600 focus:border-cyan-500 focus:outline-none focus:ring-0"
                placeholder="admin"
              />
            </div>

            <div>
              <label htmlFor="password" className="block text-xs text-gray-400 mb-1">
                Password
              </label>
              <input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="current-password"
                className="block w-full border-0 border-b border-gray-600 bg-transparent px-0 py-2 text-sm text-white placeholder-gray-600 focus:border-cyan-500 focus:outline-none focus:ring-0"
                placeholder="Password"
              />
            </div>

            {error && (
              <p className="text-sm text-red-400">{error}</p>
            )}

            <button
              type="submit"
              disabled={loading}
              className="w-full text-sm font-semibold text-cyan-400 uppercase tracking-wider hover:text-cyan-300 focus:outline-none disabled:opacity-50 py-2 text-right"
            >
              {loading ? 'Signing in...' : 'SIGN IN'}
            </button>
          </form>
        </div>

        {/* Footer — bottom right */}
        <p className="pb-4 pr-8 text-right text-[10px] text-gray-600">
          sharko v1.0.0
        </p>
      </div>
    </div>
  )
}

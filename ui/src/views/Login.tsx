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
      {/* Left side — background covers full area, mascot centered */}
      <div
        className="hidden lg:block lg:flex-1 bg-[#0a2a4a] bg-contain bg-center bg-no-repeat"
        style={{ backgroundImage: "url('/sharko-login-bg.png')" }}
      />

      {/* Right side — login panel */}
      <div className="flex w-full flex-col bg-[#f0f7ff] lg:w-[440px] lg:min-w-[400px]">
        <div className="flex flex-1 flex-col items-center justify-center px-10 py-12">
          {/* Brand header — logo + name + description */}
          <div className="mb-10 flex flex-col items-center gap-2 text-center">
            <img src="/sharko-banner.png" alt="Sharko" className="h-16 w-auto" />
            <h1 className="text-2xl font-bold text-[#0a2a4a]">Sharko</h1>
            <p className="text-sm text-[#4a8abf]">Addon management for Kubernetes clusters</p>
          </div>

          <form onSubmit={handleSubmit} className="w-full space-y-5">
            <div>
              <label htmlFor="username" className="block text-xs text-[#4a8abf] mb-1.5">
                Username
              </label>
              <input
                id="username"
                type="text"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                autoComplete="username"
                autoFocus
                className="block w-full rounded-md border border-[#90c8ee] bg-[#e0f0ff] px-3 py-2.5 text-sm text-[#0a2a4a] placeholder-[#7ab0d8] focus:border-[#9fcffb] focus:outline-none focus:ring-1 focus:ring-[#9fcffb]"
                placeholder="admin"
              />
            </div>

            <div>
              <label htmlFor="password" className="block text-xs text-[#4a8abf] mb-1.5">
                Password
              </label>
              <input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="current-password"
                className="block w-full rounded-md border border-[#90c8ee] bg-[#e0f0ff] px-3 py-2.5 text-sm text-[#0a2a4a] placeholder-[#7ab0d8] focus:border-[#9fcffb] focus:outline-none focus:ring-1 focus:ring-[#9fcffb]"
                placeholder="Password"
              />
            </div>

            {error && (
              <p className="text-sm text-red-400">{error}</p>
            )}

            <button
              type="submit"
              disabled={loading}
              className="w-full rounded-full bg-[#0a2a4a] px-4 py-2.5 text-sm font-semibold text-white transition-colors hover:bg-[#14466e] focus:outline-none focus:ring-2 focus:ring-[#9fcffb] focus:ring-offset-2 focus:ring-offset-gray-900 disabled:opacity-50"
            >
              {loading ? 'Signing in...' : 'Sign In'}
            </button>
          </form>
        </div>

        {/* Footer */}
        <p className="pb-4 text-center text-[10px] text-[#4a8abf]">
          Sharko v1.0.0
        </p>
      </div>
    </div>
  )
}

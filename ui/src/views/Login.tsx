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
        className="hidden lg:block lg:flex-1 bg-[#0B1426] bg-cover bg-center bg-no-repeat"
        style={{ backgroundImage: "url('/sharko-login-bg.png')" }}
      />

      {/* Right side — login panel */}
      <div className="flex w-full flex-col bg-[#1a2332] lg:w-[380px] lg:min-w-[340px]">
        <div className="flex flex-1 flex-col items-center justify-center px-10 py-12">
          {/* Brand header — icon + text, bigger */}
          <div className="mb-10 flex items-center gap-3">
            <img src="/sharko-icon-64.png" alt="" className="h-10 w-10" />
            <span className="font-bold text-cyan-400 text-[32px] tracking-tight">sharko</span>
          </div>

          <form onSubmit={handleSubmit} className="w-full space-y-5">
            <div>
              <label htmlFor="username" className="block text-xs text-gray-400 mb-1.5">
                Username
              </label>
              <input
                id="username"
                type="text"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                autoComplete="username"
                autoFocus
                className="block w-full rounded-md border border-gray-600 bg-gray-800/50 px-3 py-2.5 text-sm text-white placeholder-gray-500 focus:border-cyan-500 focus:outline-none focus:ring-1 focus:ring-cyan-500"
                placeholder="admin"
              />
            </div>

            <div>
              <label htmlFor="password" className="block text-xs text-gray-400 mb-1.5">
                Password
              </label>
              <input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="current-password"
                className="block w-full rounded-md border border-gray-600 bg-gray-800/50 px-3 py-2.5 text-sm text-white placeholder-gray-500 focus:border-cyan-500 focus:outline-none focus:ring-1 focus:ring-cyan-500"
                placeholder="Password"
              />
            </div>

            {error && (
              <p className="text-sm text-red-400">{error}</p>
            )}

            <button
              type="submit"
              disabled={loading}
              className="w-full rounded-full bg-cyan-500 px-4 py-2.5 text-sm font-semibold text-white transition-colors hover:bg-cyan-600 focus:outline-none focus:ring-2 focus:ring-cyan-400 focus:ring-offset-2 focus:ring-offset-gray-900 disabled:opacity-50"
            >
              {loading ? 'Signing in...' : 'Sign In'}
            </button>
          </form>
        </div>

        {/* Footer */}
        <p className="pb-4 text-center text-[10px] text-gray-600">
          Sharko v1.0.0
        </p>
      </div>
    </div>
  )
}

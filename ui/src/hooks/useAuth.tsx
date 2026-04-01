import { createContext, useContext, useState, useCallback, useEffect, type ReactNode } from 'react'

interface AuthContextType {
  token: string | null
  username: string | null
  role: string | null
  login: (username: string, password: string) => Promise<void>
  logout: () => void
  isAuthenticated: boolean
  isAdmin: boolean
  loading: boolean
  error: string | null
}

const AuthContext = createContext<AuthContextType | null>(null)

const TOKEN_KEY = 'aap-auth-token'
const USER_KEY = 'aap-auth-user'
const ROLE_KEY = 'aap-auth-role'

export function AuthProvider({ children }: { children: ReactNode }) {
  const [token, setToken] = useState<string | null>(() => sessionStorage.getItem(TOKEN_KEY))
  const [username, setUsername] = useState<string | null>(() => sessionStorage.getItem(USER_KEY))
  const [role, setRole] = useState<string | null>(() => sessionStorage.getItem(ROLE_KEY))
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  // Verify existing token on mount
  useEffect(() => {
    if (!token) {
      setLoading(false)
      return
    }
    fetch('/api/v1/health', {
      headers: { Authorization: `Bearer ${token}` },
    })
      .then((r) => {
        if (!r.ok) {
          setToken(null)
          setUsername(null)
          setRole(null)
          sessionStorage.removeItem(TOKEN_KEY)
          sessionStorage.removeItem(USER_KEY)
          sessionStorage.removeItem(ROLE_KEY)
        }
      })
      .catch(() => {})
      .finally(() => setLoading(false))
  }, [token])

  const login = useCallback(async (user: string, password: string) => {
    setError(null)
    const res = await fetch('/api/v1/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: user, password }),
    })
    if (!res.ok) {
      const data = await res.json().catch(() => ({ error: 'Login failed' }))
      setError(data.error || 'Invalid credentials')
      throw new Error(data.error || 'Login failed')
    }
    const data = await res.json()
    setToken(data.token)
    setUsername(data.username || user)
    setRole(data.role || 'viewer')
    sessionStorage.setItem(TOKEN_KEY, data.token)
    sessionStorage.setItem(USER_KEY, data.username || user)
    sessionStorage.setItem(ROLE_KEY, data.role || 'viewer')
  }, [])

  const logout = useCallback(() => {
    setToken(null)
    setUsername(null)
    setRole(null)
    sessionStorage.removeItem(TOKEN_KEY)
    sessionStorage.removeItem(USER_KEY)
    sessionStorage.removeItem(ROLE_KEY)
  }, [])

  const isAdmin = role === 'admin' || !role // no role = auth disabled = full access

  return (
    <AuthContext.Provider value={{ token, username, role, login, logout, isAuthenticated: !!token, isAdmin, loading, error }}>
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}

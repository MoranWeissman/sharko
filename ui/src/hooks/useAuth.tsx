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

export const AuthContext = createContext<AuthContextType | null>(null)

const TOKEN_KEY = 'sharko-auth-token'
const USER_KEY = 'sharko-auth-user'
const ROLE_KEY = 'sharko-auth-role'

// V124-23 / BUG-047: the wizard's X button (V124-16) writes
// `sharko:dismiss-wizard=1` into sessionStorage so the wizard gate doesn't
// re-trap the user mid-session. sessionStorage in a single tab persists
// across logout/login cycles within that tab, so without symmetric
// clearance a user who dismissed the wizard, logged out, and logged back
// in (same tab) would still have the flag set — re-login would NOT bring
// the wizard back even when the system was in a genuinely broken state
// (no repo, no ArgoCD bootstrap). Clearing on both login and logout
// expands the lifecycle so re-login is treated as the fresh-session
// intent it implies. The dismiss-on-X behavior itself is unchanged.
const DISMISS_WIZARD_KEY = 'sharko:dismiss-wizard'

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
    // V124-23 / BUG-047: see DISMISS_WIZARD_KEY rationale above. Symmetric
    // clearance on login + logout — fresh auth state implies fresh wizard
    // gate, even if the previous tab session dismissed it.
    sessionStorage.removeItem(DISMISS_WIZARD_KEY)
  }, [])

  const logout = useCallback(() => {
    setToken(null)
    setUsername(null)
    setRole(null)
    sessionStorage.removeItem(TOKEN_KEY)
    sessionStorage.removeItem(USER_KEY)
    sessionStorage.removeItem(ROLE_KEY)
    // V124-23 / BUG-047: clear the wizard-dismiss flag so the next login
    // (same tab or otherwise) starts with a clean wizard gate. Without
    // this, a dismiss → logout → login cycle in one tab leaves the
    // wizard suppressed even when the system state warrants it.
    sessionStorage.removeItem(DISMISS_WIZARD_KEY)
  }, [])

  const isAdmin = role === 'admin'

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

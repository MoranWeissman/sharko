import { createContext, useContext, useState, useCallback, useEffect, type ReactNode } from 'react'
import { getSession, setSession, clearSession, subscribeToLogout, type AuthSession } from '@/lib/authStorage'

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

const EMPTY_SESSION: AuthSession = { token: null, username: null, role: null }

// The wizard's X button writes `sharko:dismiss-wizard=1` into sessionStorage
// so the wizard gate doesn't re-trap the user mid-session. sessionStorage in
// a single tab persists across logout/login cycles within that tab, so
// without symmetric clearance a user who dismissed the wizard, logged out,
// and logged back in (same tab) would still have the flag set — re-login
// would NOT bring the wizard back even when the system was in a genuinely
// broken state. Clearing on both login and logout treats re-login as the
// fresh-session intent it implies. This flag is deliberately per-tab (a
// fresh tab should see the wizard again), so it stays in sessionStorage —
// unlike the auth session itself, which lives in localStorage (authStorage).
const DISMISS_WIZARD_KEY = 'sharko:dismiss-wizard'

export function AuthProvider({ children }: { children: ReactNode }) {
  const [session, setSessionState] = useState<AuthSession>(() => getSession())
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const { token, username, role } = session

  // Cross-tab logout: another tab clearing the token in localStorage fires a
  // `storage` event here. React to it by dropping this tab's auth state too
  // — the user sees the login screen on their next interaction/render.
  // Login in another tab does not need the same live push; this tab picks
  // it up naturally the next time it reads storage.
  useEffect(() => {
    return subscribeToLogout(() => {
      setSessionState(EMPTY_SESSION)
    })
  }, [])

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
          clearSession()
          setSessionState(EMPTY_SESSION)
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
    const nextUsername = data.username || user
    const nextRole = data.role || 'viewer'
    setSession(data.token, nextUsername, nextRole)
    setSessionState({ token: data.token, username: nextUsername, role: nextRole })
    // See DISMISS_WIZARD_KEY comment — symmetric clearance on login +
    // logout so fresh auth implies a fresh wizard gate.
    sessionStorage.removeItem(DISMISS_WIZARD_KEY)
  }, [])

  const logout = useCallback(() => {
    clearSession()
    setSessionState(EMPTY_SESSION)
    // Clear the wizard-dismiss flag so the next login starts with a clean
    // wizard gate (see DISMISS_WIZARD_KEY comment).
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

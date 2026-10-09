/**
 * authStorage — the ONLY module that touches browser storage for the auth
 * session (token/username/role).
 *
 * Backed by localStorage, not sessionStorage: localStorage is shared across
 * every tab for the same origin, so opening a link in a new tab (or
 * middle-clicking a row) lands the new tab already logged in instead of
 * bouncing to the login screen. sessionStorage is per-tab and was the
 * original (buggy) home for these keys — see the maintainer finding this
 * module fixes: "right-click -> open in new tab forces a re-login".
 *
 * One-time migration: a tab that was logged in under the old sessionStorage
 * scheme still has its token sitting in sessionStorage. The first time this
 * module is asked for the session in that tab, it copies the token/username/
 * role into localStorage and removes them from sessionStorage, so an
 * already-logged-in tab stays logged in after this ships — no forced
 * re-login on deploy.
 *
 * Cross-tab logout: logging out in one tab clears localStorage, which fires
 * a `storage` event in every OTHER open tab (browsers never fire it in the
 * tab that made the change). `subscribeToLogout` lets a consumer (useAuth)
 * react to that so the other tabs drop back to the login screen too. Login
 * does NOT need the same live push — a tab that logs in elsewhere picks up
 * the new session the next time it reads storage.
 *
 * NOTE: `sharko:dismiss-wizard` is a DIFFERENT key, owned by FirstRunWizard/
 * App.tsx. It stays in sessionStorage on purpose (it's a per-tab "dismiss
 * for this session" flag, not part of the auth session) and must not be
 * touched here.
 */

const TOKEN_KEY = 'sharko-auth-token'
const USER_KEY = 'sharko-auth-user'
const ROLE_KEY = 'sharko-auth-role'

export interface AuthSession {
  token: string | null
  username: string | null
  role: string | null
}

/**
 * Copies a legacy sessionStorage session into localStorage and clears the
 * sessionStorage copy. Safe to call repeatedly — once sessionStorage no
 * longer has a token, this is a no-op read of a couple of keys.
 */
function migrateFromSessionStorage(): void {
  const sessionToken = sessionStorage.getItem(TOKEN_KEY)
  if (!sessionToken) return

  // Don't clobber a session that's already live in localStorage.
  if (!localStorage.getItem(TOKEN_KEY)) {
    localStorage.setItem(TOKEN_KEY, sessionToken)
    const sessionUser = sessionStorage.getItem(USER_KEY)
    const sessionRole = sessionStorage.getItem(ROLE_KEY)
    if (sessionUser) localStorage.setItem(USER_KEY, sessionUser)
    if (sessionRole) localStorage.setItem(ROLE_KEY, sessionRole)
  }

  sessionStorage.removeItem(TOKEN_KEY)
  sessionStorage.removeItem(USER_KEY)
  sessionStorage.removeItem(ROLE_KEY)
}

export function getSession(): AuthSession {
  migrateFromSessionStorage()
  return {
    token: localStorage.getItem(TOKEN_KEY),
    username: localStorage.getItem(USER_KEY),
    role: localStorage.getItem(ROLE_KEY),
  }
}

export function getToken(): string | null {
  migrateFromSessionStorage()
  return localStorage.getItem(TOKEN_KEY)
}

export function getUsername(): string | null {
  migrateFromSessionStorage()
  return localStorage.getItem(USER_KEY)
}

export function getRole(): string | null {
  migrateFromSessionStorage()
  return localStorage.getItem(ROLE_KEY)
}

export function setSession(token: string, username: string, role: string): void {
  localStorage.setItem(TOKEN_KEY, token)
  localStorage.setItem(USER_KEY, username)
  localStorage.setItem(ROLE_KEY, role)
}

export function clearSession(): void {
  localStorage.removeItem(TOKEN_KEY)
  localStorage.removeItem(USER_KEY)
  localStorage.removeItem(ROLE_KEY)
}

/**
 * Subscribes to cross-tab logout. Fires `onLogout` when another tab removes
 * the auth token from localStorage (browsers only dispatch `storage` events
 * to OTHER tabs, never the one that made the change, so this never
 * self-triggers on this tab's own clearSession() call). Returns an
 * unsubscribe function.
 */
export function subscribeToLogout(onLogout: () => void): () => void {
  const handler = (event: StorageEvent): void => {
    if (event.key === TOKEN_KEY && event.newValue === null) {
      onLogout()
    }
  }
  window.addEventListener('storage', handler)
  return () => window.removeEventListener('storage', handler)
}

/**
 * useAuth — V124-23 / BUG-047 regression tests.
 *
 * V124-16 introduced a sessionStorage `sharko:dismiss-wizard` flag so a user
 * who clicked X on the FirstRunWizard could explore the (degraded) app for
 * the rest of their session without being immediately re-trapped at the
 * wizard. The flag was meant to be session-scoped — a fresh tab brings the
 * wizard back. But sessionStorage in a single tab persists across login
 * and logout cycles within that tab, so a user who dismissed the wizard,
 * logged out, then logged back in (same tab) still had the flag set —
 * re-login could not bring the wizard back even when system state warranted
 * it (no repo, no ArgoCD bootstrap).
 *
 * Fix: clear the flag on BOTH login and logout. Symmetric clearance — fresh
 * auth state implies fresh wizard gate. The dismiss-on-X behaviour itself
 * (FirstRunWizard's onClose handler writing the flag) is unchanged.
 *
 * These tests render the AuthProvider with a child consumer that triggers
 * login() / logout() on demand, then assert the flag is gone. Same shape
 * as the auth tests would take in any React Testing Library codebase —
 * pre-set sessionStorage, exercise the hook through its public surface,
 * assert the post-state.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { AuthProvider, useAuth } from '@/hooks/useAuth'

const DISMISS_KEY = 'sharko:dismiss-wizard'

// Test consumer — exposes login + logout as buttons so we can drive them
// via fireEvent. Keeps the test focused on the hook's lifecycle effects
// rather than the UI shell.
function TestConsumer() {
  const { login, logout, token } = useAuth()
  return (
    <div>
      <span data-testid="token">{token ?? 'none'}</span>
      <button
        data-testid="login-btn"
        onClick={() => {
          // Fire-and-forget — the test awaits via waitFor on the post-state.
          void login('admin', 'secret').catch(() => {
            // Swallow rejection — failed-login tests assert via the flag
            // explicitly rather than reading the rejection.
          })
        }}
      >
        login
      </button>
      <button data-testid="logout-btn" onClick={() => logout()}>
        logout
      </button>
    </div>
  )
}

function renderWithProvider() {
  return render(
    <AuthProvider>
      <TestConsumer />
    </AuthProvider>,
  )
}

describe('useAuth — BUG-047 sharko:dismiss-wizard lifecycle', () => {
  beforeEach(() => {
    sessionStorage.clear()
    localStorage.clear()
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.restoreAllMocks()
    sessionStorage.clear()
    localStorage.clear()
  })

  it('clears sharko:dismiss-wizard on successful login', async () => {
    // Pre-state: user dismissed the wizard in some prior session.
    sessionStorage.setItem(DISMISS_KEY, '1')
    expect(sessionStorage.getItem(DISMISS_KEY)).toBe('1')

    // Mock a successful login response. AuthProvider's verify-token effect
    // hits /api/v1/health on mount when a token exists; we start with no
    // token so that effect short-circuits without a fetch call.
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({ token: 'test-token-123', username: 'admin', role: 'admin' }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    renderWithProvider()
    fireEvent.click(screen.getByTestId('login-btn'))

    await waitFor(() => {
      expect(screen.getByTestId('token').textContent).toBe('test-token-123')
    })

    // Post-state: dismiss flag MUST be cleared so the next render of the
    // wizard gate (App.tsx shouldShowSetupWizard) sees a clean slate.
    expect(sessionStorage.getItem(DISMISS_KEY)).toBeNull()
    // And the auth fields landed in localStorage (shared across tabs), NOT
    // sessionStorage — that's the fix that keeps a second tab logged in.
    expect(localStorage.getItem('sharko-auth-token')).toBe('test-token-123')
    expect(sessionStorage.getItem('sharko-auth-token')).toBeNull()
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/auth/login',
      expect.objectContaining({ method: 'POST' }),
    )
  })

  it('clears sharko:dismiss-wizard on logout', async () => {
    // Pre-state: the user is mid-session, has dismissed the wizard, and
    // now clicks "Log out" in the user menu. We seed the flag plus a stale
    // token so the AuthProvider mounts with isAuthenticated=true. The auth
    // session lives in localStorage (the dismiss flag stays in
    // sessionStorage — it's deliberately per-tab).
    sessionStorage.setItem(DISMISS_KEY, '1')
    localStorage.setItem('sharko-auth-token', 'stale-token')
    localStorage.setItem('sharko-auth-user', 'admin')
    localStorage.setItem('sharko-auth-role', 'admin')

    // The verify-token effect hits /api/v1/health on mount — answer with
    // 200 so the token sticks long enough for our logout click.
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }),
    )

    renderWithProvider()
    // Wait for the verify effect to settle so the click hits a stable hook.
    await waitFor(() => {
      expect(screen.getByTestId('token').textContent).toBe('stale-token')
    })

    fireEvent.click(screen.getByTestId('logout-btn'))

    await waitFor(() => {
      expect(screen.getByTestId('token').textContent).toBe('none')
    })

    // Post-state: dismiss flag MUST be cleared so the next login (same tab
    // or otherwise) gets a fresh wizard gate.
    expect(sessionStorage.getItem(DISMISS_KEY)).toBeNull()
    // Auth keys also cleared from localStorage (this part was the
    // pre-existing logout contract — we re-assert here to lock down the
    // full lifecycle).
    expect(localStorage.getItem('sharko-auth-token')).toBeNull()
    expect(localStorage.getItem('sharko-auth-user')).toBeNull()
    expect(localStorage.getItem('sharko-auth-role')).toBeNull()
  })

  it('does NOT clear sharko:dismiss-wizard on failed login', async () => {
    // Pre-state: user dismissed the wizard, then tried to log in with bad
    // credentials. The flag should stay because no auth state actually
    // changed — clearing it would silently hide the wizard for an unrelated
    // failure.
    sessionStorage.setItem(DISMISS_KEY, '1')

    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ error: 'Invalid credentials' }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    renderWithProvider()
    fireEvent.click(screen.getByTestId('login-btn'))

    // Wait for the login attempt to settle — error handling doesn't update
    // the token, so we look at the fetch having been called instead.
    await waitFor(() => {
      expect(globalThis.fetch).toHaveBeenCalledWith(
        '/api/v1/auth/login',
        expect.objectContaining({ method: 'POST' }),
      )
    })

    // Flag survives the failed attempt — only successful login clears it.
    expect(sessionStorage.getItem(DISMISS_KEY)).toBe('1')
  })
})

/**
 * useAuth — login survives "open in new tab" (walk finding).
 *
 * The maintainer's finding: right-click a link -> open in new tab -> the
 * new tab shows the login screen even though the user is already logged
 * in. Root cause was the auth session living in sessionStorage, which is
 * per-tab. The fix moves it to localStorage (shared across tabs) via
 * `authStorage`, with a one-time migration for tabs that still have the
 * old sessionStorage-only session, plus a cross-tab logout so logging out
 * in one tab logs the others out too.
 */
describe('useAuth — login survives new tabs (localStorage session)', () => {
  beforeEach(() => {
    sessionStorage.clear()
    localStorage.clear()
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.restoreAllMocks()
    sessionStorage.clear()
    localStorage.clear()
  })

  it('initializes authenticated from localStorage alone (new-tab simulation)', async () => {
    // Simulate a brand new tab: sessionStorage is empty (fresh per-tab
    // storage), but localStorage already has a session from the tab that
    // logged in.
    localStorage.setItem('sharko-auth-token', 'shared-token')
    localStorage.setItem('sharko-auth-user', 'admin')
    localStorage.setItem('sharko-auth-role', 'admin')

    // The verify-token effect hits /api/v1/health on mount — answer 200 so
    // the token isn't treated as stale.
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }),
    )

    renderWithProvider()

    // Authenticated immediately from localStorage, no login click needed.
    expect(screen.getByTestId('token').textContent).toBe('shared-token')
  })

  it('migrates a legacy sessionStorage-only session to localStorage on init', async () => {
    // Simulate a tab that was logged in before this fix shipped: the token
    // is only in sessionStorage.
    sessionStorage.setItem('sharko-auth-token', 'legacy-token')
    sessionStorage.setItem('sharko-auth-user', 'admin')
    sessionStorage.setItem('sharko-auth-role', 'admin')

    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }),
    )

    renderWithProvider()

    // Stays authenticated through the migration — no forced re-login.
    expect(screen.getByTestId('token').textContent).toBe('legacy-token')

    // The session moved to localStorage and was removed from sessionStorage.
    expect(localStorage.getItem('sharko-auth-token')).toBe('legacy-token')
    expect(localStorage.getItem('sharko-auth-user')).toBe('admin')
    expect(localStorage.getItem('sharko-auth-role')).toBe('admin')
    expect(sessionStorage.getItem('sharko-auth-token')).toBeNull()
    expect(sessionStorage.getItem('sharko-auth-user')).toBeNull()
    expect(sessionStorage.getItem('sharko-auth-role')).toBeNull()
  })

  it('clears auth state when another tab logs out (cross-tab logout)', async () => {
    localStorage.setItem('sharko-auth-token', 'shared-token')
    localStorage.setItem('sharko-auth-user', 'admin')
    localStorage.setItem('sharko-auth-role', 'admin')

    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }),
    )

    renderWithProvider()
    await waitFor(() => {
      expect(screen.getByTestId('token').textContent).toBe('shared-token')
    })

    // Simulate another tab logging out: it clears localStorage and the
    // browser fires a `storage` event in THIS tab (browsers never fire it
    // in the tab that made the change, so we dispatch it by hand here).
    localStorage.removeItem('sharko-auth-token')
    window.dispatchEvent(
      new StorageEvent('storage', {
        key: 'sharko-auth-token',
        oldValue: 'shared-token',
        newValue: null,
        storageArea: localStorage,
      }),
    )

    await waitFor(() => {
      expect(screen.getByTestId('token').textContent).toBe('none')
    })
  })

  it('ignores storage events for unrelated keys', async () => {
    localStorage.setItem('sharko-auth-token', 'shared-token')
    localStorage.setItem('sharko-auth-user', 'admin')
    localStorage.setItem('sharko-auth-role', 'admin')

    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }),
    )

    renderWithProvider()
    await waitFor(() => {
      expect(screen.getByTestId('token').textContent).toBe('shared-token')
    })

    window.dispatchEvent(
      new StorageEvent('storage', {
        key: 'sharko-theme',
        oldValue: 'light',
        newValue: null,
        storageArea: localStorage,
      }),
    )

    // Unrelated key removal must not log the user out.
    expect(screen.getByTestId('token').textContent).toBe('shared-token')
  })
})

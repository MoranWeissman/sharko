import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import {
  getSession,
  getToken,
  getUsername,
  getRole,
  setSession,
  clearSession,
  subscribeToLogout,
} from '../authStorage'

/**
 * authStorage — unit tests for the module fixing "login must survive open
 * in new tab" (walk finding). sessionStorage is per-tab, so a token stored
 * there never shows up in a freshly opened tab; localStorage is shared
 * across tabs for the same origin, so this module is backed by it instead.
 */
describe('authStorage', () => {
  beforeEach(() => {
    sessionStorage.clear()
    localStorage.clear()
  })

  afterEach(() => {
    sessionStorage.clear()
    localStorage.clear()
  })

  describe('setSession / getSession / clearSession', () => {
    it('round-trips a session through localStorage', () => {
      setSession('tok-1', 'alice', 'admin')

      expect(getSession()).toEqual({ token: 'tok-1', username: 'alice', role: 'admin' })
      expect(getToken()).toBe('tok-1')
      expect(getUsername()).toBe('alice')
      expect(getRole()).toBe('admin')
      expect(localStorage.getItem('sharko-auth-token')).toBe('tok-1')
    })

    it('clearSession removes all three keys from localStorage', () => {
      setSession('tok-1', 'alice', 'admin')
      clearSession()

      expect(getSession()).toEqual({ token: null, username: null, role: null })
      expect(localStorage.getItem('sharko-auth-token')).toBeNull()
      expect(localStorage.getItem('sharko-auth-user')).toBeNull()
      expect(localStorage.getItem('sharko-auth-role')).toBeNull()
    })

    it('getSession returns all-null when nothing is stored', () => {
      expect(getSession()).toEqual({ token: null, username: null, role: null })
    })
  })

  describe('new-tab simulation', () => {
    it('reads an existing localStorage session even when sessionStorage is empty', () => {
      // Simulates a fresh tab opened via "open in new tab": sessionStorage
      // starts empty in the new tab, but localStorage already carries the
      // session another tab created.
      localStorage.setItem('sharko-auth-token', 'shared-token')
      localStorage.setItem('sharko-auth-user', 'alice')
      localStorage.setItem('sharko-auth-role', 'admin')

      expect(getToken()).toBe('shared-token')
      expect(getSession()).toEqual({ token: 'shared-token', username: 'alice', role: 'admin' })
    })
  })

  describe('one-time migration from sessionStorage', () => {
    it('copies a sessionStorage-only session into localStorage and removes it from sessionStorage', () => {
      sessionStorage.setItem('sharko-auth-token', 'legacy-token')
      sessionStorage.setItem('sharko-auth-user', 'alice')
      sessionStorage.setItem('sharko-auth-role', 'admin')

      const session = getSession()

      expect(session).toEqual({ token: 'legacy-token', username: 'alice', role: 'admin' })
      expect(localStorage.getItem('sharko-auth-token')).toBe('legacy-token')
      expect(localStorage.getItem('sharko-auth-user')).toBe('alice')
      expect(localStorage.getItem('sharko-auth-role')).toBe('admin')
      expect(sessionStorage.getItem('sharko-auth-token')).toBeNull()
      expect(sessionStorage.getItem('sharko-auth-user')).toBeNull()
      expect(sessionStorage.getItem('sharko-auth-role')).toBeNull()
    })

    it('does not overwrite an existing localStorage session with a stale sessionStorage one', () => {
      localStorage.setItem('sharko-auth-token', 'current-token')
      localStorage.setItem('sharko-auth-user', 'bob')
      localStorage.setItem('sharko-auth-role', 'viewer')
      sessionStorage.setItem('sharko-auth-token', 'stale-token')
      sessionStorage.setItem('sharko-auth-user', 'alice')
      sessionStorage.setItem('sharko-auth-role', 'admin')

      const session = getSession()

      // localStorage wins — it's the live session.
      expect(session).toEqual({ token: 'current-token', username: 'bob', role: 'viewer' })
      // The stale sessionStorage copy is still cleared out.
      expect(sessionStorage.getItem('sharko-auth-token')).toBeNull()
    })

    it('is a no-op when neither storage has a token', () => {
      expect(getToken()).toBeNull()
      expect(sessionStorage.length).toBe(0)
      expect(localStorage.length).toBe(0)
    })

    it('getToken alone (not just getSession) triggers the migration', () => {
      sessionStorage.setItem('sharko-auth-token', 'legacy-token')
      sessionStorage.setItem('sharko-auth-user', 'alice')
      sessionStorage.setItem('sharko-auth-role', 'admin')

      expect(getToken()).toBe('legacy-token')
      expect(localStorage.getItem('sharko-auth-token')).toBe('legacy-token')
      expect(sessionStorage.getItem('sharko-auth-token')).toBeNull()
    })
  })

  describe('subscribeToLogout', () => {
    it('fires the callback when the token key is removed from localStorage', () => {
      const onLogout = vi.fn()
      const unsubscribe = subscribeToLogout(onLogout)

      window.dispatchEvent(
        new StorageEvent('storage', {
          key: 'sharko-auth-token',
          oldValue: 'shared-token',
          newValue: null,
          storageArea: localStorage,
        }),
      )

      expect(onLogout).toHaveBeenCalledTimes(1)
      unsubscribe()
    })

    it('does not fire for unrelated keys', () => {
      const onLogout = vi.fn()
      const unsubscribe = subscribeToLogout(onLogout)

      window.dispatchEvent(
        new StorageEvent('storage', {
          key: 'sharko-theme',
          oldValue: 'light',
          newValue: 'dark',
          storageArea: localStorage,
        }),
      )

      expect(onLogout).not.toHaveBeenCalled()
      unsubscribe()
    })

    it('does not fire when the token key is set (login), only when removed', () => {
      const onLogout = vi.fn()
      const unsubscribe = subscribeToLogout(onLogout)

      window.dispatchEvent(
        new StorageEvent('storage', {
          key: 'sharko-auth-token',
          oldValue: null,
          newValue: 'new-token',
          storageArea: localStorage,
        }),
      )

      expect(onLogout).not.toHaveBeenCalled()
      unsubscribe()
    })

    it('stops firing after unsubscribe', () => {
      const onLogout = vi.fn()
      const unsubscribe = subscribeToLogout(onLogout)
      unsubscribe()

      window.dispatchEvent(
        new StorageEvent('storage', {
          key: 'sharko-auth-token',
          oldValue: 'shared-token',
          newValue: null,
          storageArea: localStorage,
        }),
      )

      expect(onLogout).not.toHaveBeenCalled()
    })
  })
})

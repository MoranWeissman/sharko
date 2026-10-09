import { describe, expect, it, vi } from 'vitest'
import {
  RELOAD_FLAG,
  clearChunkReloadFlag,
  handleChunkLoadFailure,
  isChunkLoadError,
  type StorageLike,
} from '../chunkReloadGuard'

// sprint-backlog-burndown-r1 Lane B-a — the reload guard is the tiny piece
// of logic that decides whether a rolled-over tab gets ONE automatic
// reload, or none. Tested here without touching a real window/sessionStorage
// or triggering a real chunk failure — see main.tsx for the wiring.

function fakeStorage(initial: Record<string, string> = {}): StorageLike & { data: Record<string, string> } {
  const data: Record<string, string> = { ...initial }
  return {
    data,
    getItem: (key) => (key in data ? data[key] : null),
    setItem: (key, value) => {
      data[key] = value
    },
    removeItem: (key) => {
      delete data[key]
    },
  }
}

describe('handleChunkLoadFailure', () => {
  it('reloads once and sets the flag on the first failure', () => {
    const storage = fakeStorage()
    const reload = vi.fn()

    const reloaded = handleChunkLoadFailure({ storage, reload })

    expect(reloaded).toBe(true)
    expect(reload).toHaveBeenCalledTimes(1)
    expect(storage.getItem(RELOAD_FLAG)).toBe('1')
  })

  it('does NOT reload again when the flag is already set', () => {
    const storage = fakeStorage({ [RELOAD_FLAG]: '1' })
    const reload = vi.fn()

    const reloaded = handleChunkLoadFailure({ storage, reload })

    expect(reloaded).toBe(false)
    expect(reload).not.toHaveBeenCalled()
    // Flag stays set — still not a second reload attempt.
    expect(storage.getItem(RELOAD_FLAG)).toBe('1')
  })
})

describe('clearChunkReloadFlag', () => {
  it('removes the flag so a later failure gets its own fresh reload', () => {
    const storage = fakeStorage({ [RELOAD_FLAG]: '1' })

    clearChunkReloadFlag(storage)

    expect(storage.getItem(RELOAD_FLAG)).toBeNull()

    const reload = vi.fn()
    const reloaded = handleChunkLoadFailure({ storage, reload })
    expect(reloaded).toBe(true)
    expect(reload).toHaveBeenCalledTimes(1)
  })
})

describe('isChunkLoadError', () => {
  it('matches the Chrome dynamic-import failure message', () => {
    const err = new Error('Failed to fetch dynamically imported module: https://x/assets/Foo-abc.js')
    expect(isChunkLoadError(err)).toBe(true)
  })

  it('matches the Firefox dynamic-import failure message', () => {
    const err = new Error('error loading dynamically imported module: https://x/assets/Foo-abc.js')
    expect(isChunkLoadError(err)).toBe(true)
  })

  it('matches the Safari module script failure message', () => {
    const err = new Error('Importing a module script failed.')
    expect(isChunkLoadError(err)).toBe(true)
  })

  it('does not match an unrelated error', () => {
    expect(isChunkLoadError(new Error('network timeout'))).toBe(false)
  })

  it('does not false-positive on a generic fetch failure', () => {
    expect(isChunkLoadError(new Error('Failed to fetch'))).toBe(false)
  })

  it('handles non-Error values without throwing', () => {
    expect(isChunkLoadError('error loading dynamically imported module')).toBe(true)
    expect(isChunkLoadError(null)).toBe(false)
    expect(isChunkLoadError(undefined)).toBe(false)
  })
})

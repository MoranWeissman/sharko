import { describe, it, expect, beforeEach, vi } from 'vitest'
import { getCached, setCached, hasCached, clearCached, clearAllCached } from '@/lib/viewCache'

describe('viewCache', () => {
  beforeEach(() => {
    clearAllCached()
  })

  it('miss: getCached returns undefined for a key that was never set', () => {
    expect(getCached('nope')).toBeUndefined()
    expect(hasCached('nope')).toBe(false)
  })

  it('hit: setCached then getCached returns the same data', () => {
    setCached('dashboard', { hello: 'world' })
    const entry = getCached<{ hello: string }>('dashboard')
    expect(entry).toBeDefined()
    expect(entry!.data).toEqual({ hello: 'world' })
    expect(hasCached('dashboard')).toBe(true)
  })

  it('hit: setCached stamps a timestamp', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-08-01T00:00:00Z'))
    const entry = setCached('clusters', [1, 2, 3])
    expect(entry.timestamp).toBe(new Date('2026-08-01T00:00:00Z').getTime())
    expect(getCached('clusters')!.timestamp).toBe(entry.timestamp)
    vi.useRealTimers()
  })

  it('update: setCached again for the same key overwrites the prior entry', () => {
    setCached('addon-catalog', { addons: ['a'] })
    setCached('addon-catalog', { addons: ['a', 'b'] })
    expect(getCached<{ addons: string[] }>('addon-catalog')!.data).toEqual({ addons: ['a', 'b'] })
  })

  it('keys are independent — setting one key does not disturb another', () => {
    setCached('dashboard', 'dash-data')
    setCached('clusters', 'cluster-data')
    expect(getCached('dashboard')!.data).toBe('dash-data')
    expect(getCached('clusters')!.data).toBe('cluster-data')
  })

  it('clearCached removes only the given key', () => {
    setCached('dashboard', 1)
    setCached('clusters', 2)
    clearCached('dashboard')
    expect(hasCached('dashboard')).toBe(false)
    expect(hasCached('clusters')).toBe(true)
  })

  it('clearAllCached wipes every key', () => {
    setCached('dashboard', 1)
    setCached('clusters', 2)
    clearAllCached()
    expect(hasCached('dashboard')).toBe(false)
    expect(hasCached('clusters')).toBe(false)
  })
})

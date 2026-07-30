import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { useConnectionHealth } from '@/hooks/useConnectionHealth'
import { api } from '@/services/api'

// useConnectionHealth (v4-wave2 8.1) aggregates git/ArgoCD/vault health
// behind one hook so a failing connection can be surfaced anywhere in the
// app (not just the Connections settings page) without duplicating the
// test-endpoint calls. Reuses POST /connections/test + POST /providers/test.

vi.mock('@/services/api', () => ({
  api: {
    testConnection: vi.fn(),
    testProvider: vi.fn(),
  },
}))

const mockedApi = vi.mocked(api)

beforeEach(() => {
  vi.clearAllMocks()
})

describe('useConnectionHealth', () => {
  it('does nothing when enabled=false — no network calls fire', async () => {
    const { result } = renderHook(() => useConnectionHealth(false))
    expect(result.current.loading).toBe(false)
    expect(result.current.git).toBe('idle')
    expect(mockedApi.testConnection).not.toHaveBeenCalled()
    expect(mockedApi.testProvider).not.toHaveBeenCalled()
  })

  it('reports all-ok when every connection succeeds', async () => {
    mockedApi.testConnection.mockResolvedValue({ git: { status: 'ok' }, argocd: { status: 'ok' } })
    mockedApi.testProvider.mockResolvedValue({ status: 'connected' })

    const { result } = renderHook(() => useConnectionHealth(true))

    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.git).toBe('ok')
    expect(result.current.argocd).toBe('ok')
    expect(result.current.vault).toBe('ok')
    expect(result.current.anyFailing).toBe(false)
    expect(result.current.failingMessages).toEqual([])
  })

  it('aggregates plain-words failures from whichever connections are down', async () => {
    mockedApi.testConnection.mockResolvedValue({
      git: { status: 'error', message: "Sharko can't reach your Git host — the credentials were rejected." },
      argocd: { status: 'ok' },
    })
    mockedApi.testProvider.mockResolvedValue({
      status: 'error',
      message: "Sharko can't reach your secrets store — the request timed out.",
    })

    const { result } = renderHook(() => useConnectionHealth(true))

    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.git).toBe('error')
    expect(result.current.argocd).toBe('ok')
    expect(result.current.vault).toBe('error')
    expect(result.current.anyFailing).toBe(true)
    expect(result.current.failingMessages).toEqual([
      "Sharko can't reach your Git host — the credentials were rejected.",
      "Sharko can't reach your secrets store — the request timed out.",
    ])
  })

  it('treats a thrown network error as a failure for both git and argocd', async () => {
    mockedApi.testConnection.mockRejectedValue(new Error('network error'))
    mockedApi.testProvider.mockResolvedValue({ status: 'connected' })

    const { result } = renderHook(() => useConnectionHealth(true))

    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.git).toBe('error')
    expect(result.current.argocd).toBe('error')
    expect(result.current.anyFailing).toBe(true)
  })
})

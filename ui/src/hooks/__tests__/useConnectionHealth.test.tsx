import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { useConnectionHealth, isAWSLoginMessageId, isAWSUnreachableMessageId } from '@/hooks/useConnectionHealth'
import { api } from '@/services/api'
import { CONNECTION_FAILURE_MESSAGES } from '@/generated/connection-sentences'

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
  it('shows the generated sentence for an AWS message_id, and the server words for any other id', async () => {
    mockedApi.testConnection.mockResolvedValue({ git: { status: 'ok' }, argocd: { status: 'ok' } })
    mockedApi.testProvider.mockResolvedValue({
      status: 'error',
      message: 'server words',
      message_id: 'connectionFailureVaultAWSLoginRefused',
    })
    const first = renderHook(() => useConnectionHealth(true))
    await waitFor(() => expect(first.result.current.loading).toBe(false))
    expect(first.result.current.failingMessages).toEqual([
      CONNECTION_FAILURE_MESSAGES.connectionFailureVaultAWSLoginRefused,
    ])

    mockedApi.testProvider.mockResolvedValue({
      status: 'error',
      message: 'server words',
      message_id: 'connectionFailureVaultTimeout',
    })
    const second = renderHook(() => useConnectionHealth(true))
    await waitFor(() => expect(second.result.current.loading).toBe(false))
    expect(second.result.current.failingMessages).toEqual(['server words'])
  })
  it('shows the generated sentence for the no-AWS-login id too (ep4 round R, R2)', async () => {
    mockedApi.testConnection.mockResolvedValue({ git: { status: 'ok' }, argocd: { status: 'ok' } })
    for (const id of ['connectionFailureVaultAWSNoLogin', 'connectionFailureSecretsAWSNoLogin'] as const) {
      mockedApi.testProvider.mockResolvedValue({ status: 'error', message: 'server words', message_id: id })
      const { result } = renderHook(() => useConnectionHealth(true))
      await waitFor(() => expect(result.current.loading).toBe(false))
      expect(result.current.failingMessages).toEqual([CONNECTION_FAILURE_MESSAGES[id]])
      expect(result.current.vaultMessageId).toBe(id)
    }
  })
  it('only the no-answer AWS ids count as "check the region in Settings"', () => {
    expect(isAWSUnreachableMessageId('connectionFailureVaultAWSUnreachable')).toBe(true)
    expect(isAWSUnreachableMessageId('connectionFailureSecretsAWSUnreachable')).toBe(true)
    for (const id of [
      'connectionFailureVaultAWSLoginRefused',
      'connectionFailureSecretsAWSLoginRefused',
      'connectionFailureVaultAWSNoLogin',
      'connectionFailureSecretsAWSNoLogin',
    ]) {
      expect(isAWSLoginMessageId(id)).toBe(true)
      expect(isAWSUnreachableMessageId(id)).toBe(false)
    }
    expect(isAWSUnreachableMessageId(undefined)).toBe(false)
  })
})

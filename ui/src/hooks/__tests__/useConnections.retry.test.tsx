import { describe, it, expect, vi } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import type { ReactNode } from 'react'

const getConnections = vi.fn()
vi.mock('@/services/api', () => ({
  api: { getConnections: () => getConnections(), setActiveConnection: vi.fn() },
}))

import { ConnectionProvider, useConnections } from '@/hooks/useConnections'

const wrapper = ({ children }: { children: ReactNode }) => (
  <ConnectionProvider>{children}</ConnectionProvider>
)

describe('useConnections — Try again after a failed load (ep3 fix 22)', () => {
  it('keeps the error and an empty list on failure, then recovers on refresh', async () => {
    getConnections.mockRejectedValueOnce(new Error('network down'))
    const { result } = renderHook(() => useConnections(), { wrapper })
    await waitFor(() => expect(result.current.error).toBe('network down'))
    expect(result.current.connections).toEqual([])

    getConnections.mockResolvedValueOnce({
      connections: [{ name: 'main' }],
      active_connection: 'main',
    })
    act(() => result.current.refreshConnections())
    await waitFor(() => expect(result.current.connections).toHaveLength(1))
    expect(result.current.error).toBeNull()
  })
})

// Walk 5, serious 2: a viewer saw red crosses on Git and ArgoCD in Settings,
// as if the connection were broken, because the check was refused (403).

import { render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ConnectionSection } from '@/views/settings/ConnectionSection'

const testConnectionMock = vi.fn()

vi.mock('@/hooks/useConnections', () => ({
  useConnections: () => ({
    connections: [
      {
        name: 'main',
        is_active: true,
        git_provider: 'github',
        git_repo_identifier: 'acme/repo',
        argocd_server_url: 'https://argocd.example.test',
        argocd_namespace: 'argocd',
      },
    ],
    loading: false,
    error: null,
    refreshConnections: vi.fn(),
  }),
}))

vi.mock('@/services/api', () => ({
  api: {
    testConnection: () => testConnectionMock(),
    getRepoStatus: vi.fn().mockResolvedValue({ initialized: true }),
    testCredentials: vi.fn(),
    createConnection: vi.fn(),
    updateConnection: vi.fn(),
  },
  initRepo: vi.fn(),
  getOperation: vi.fn(),
  operationHeartbeat: vi.fn(),
}))

describe('ConnectionSection live marks', () => {
  beforeEach(() => {
    testConnectionMock.mockReset()
  })

  it('a refused check (403) draws no red cross and says the role is not allowed', async () => {
    testConnectionMock.mockImplementation(() => Promise.reject(
      Object.assign(new Error("action 'connection.test' requires role 'operator', you have 'viewer'"), { status: 403 }),
    ))
    const { container } = render(<ConnectionSection />)
    const note = await screen.findByTestId('connection-live-note')
    expect(note.textContent).toBe('Your role is not allowed to do this. Ask an operator or an admin.')
    expect(container.querySelectorAll('svg.text-red-500')).toHaveLength(0)
    expect(screen.queryByText(/requires role/)).not.toBeInTheDocument()
  })

  it("Sharko's own request limit (429) draws no red cross and says to try again", async () => {
    testConnectionMock.mockImplementation(() => Promise.reject(
      Object.assign(new Error('rate limit exceeded'), { status: 429, code: 'sharko_rate_limit' }),
    ))
    const { container } = render(<ConnectionSection />)
    const note = await screen.findByTestId('connection-live-note')
    expect(note.textContent).toBe(
      'Sharko is getting too many requests from your address right now. Try again in a moment.',
    )
    expect(container.querySelectorAll('svg.text-red-500')).toHaveLength(0)
  })

  it('a really failed check keeps its red cross', async () => {
    testConnectionMock.mockImplementation(() => Promise.reject(new Error('Failed to fetch')))
    const { container } = render(<ConnectionSection />)
    await waitFor(() => expect(container.querySelectorAll('svg.text-red-500').length).toBeGreaterThan(0))
    expect(screen.queryByTestId('connection-live-note')).not.toBeInTheDocument()
  })
})

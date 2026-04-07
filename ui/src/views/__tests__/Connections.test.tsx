import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { Connections } from '@/views/Connections'
import type { ConnectionResponse } from '@/services/models'

const mockConnections: ConnectionResponse[] = [
  {
    name: 'production',
    description: 'Production environment',
    git_provider: 'github',
    git_repo_identifier: 'my-org/k8s-addons',
    git_token_masked: 'ghp_****1234',
    argocd_server_url: 'https://argocd.prod.example.com',
    argocd_token_masked: 'argo****5678',
    argocd_namespace: 'argocd',
    is_default: true,
    is_active: true,
  },
]

const mockRefreshConnections = vi.fn()

vi.mock('@/hooks/useConnections', () => ({
  useConnections: () => ({
    connections: mockConnections,
    activeConnection: 'production',
    loading: false,
    error: null,
    refreshConnections: mockRefreshConnections,
    setActiveConnection: vi.fn(),
  }),
}))

const mockHealth = vi.fn().mockResolvedValue({ status: 'ok' })
const mockCreateConnection = vi.fn().mockResolvedValue({})
const mockUpdateConnection = vi.fn().mockResolvedValue({})
const mockTestCredentials = vi.fn().mockResolvedValue({
  git: { status: 'ok', message: '' },
  argocd: { status: 'ok', message: '' },
})

vi.mock('@/services/api', () => ({
  api: {
    health: () => mockHealth(),
    createConnection: (...args: unknown[]) => mockCreateConnection(...args),
    updateConnection: (...args: unknown[]) => mockUpdateConnection(...args),
    testCredentials: (...args: unknown[]) => mockTestCredentials(...args),
    getAIStatus: () => Promise.resolve({ enabled: false }),
    getAISummary: () => Promise.resolve({ summary: '' }),
    getAIConfig: () => Promise.resolve({ current_provider: 'none', available_providers: [] }),
    setAIProvider: () => Promise.resolve({ status: 'ok', provider: 'none' }),
    testConnection: () => Promise.resolve({ git: { status: 'ok' }, argocd: { status: 'ok' } }),
    getProviders: () => Promise.resolve({ configured_provider: null, available_types: [] }),
    getRepoStatus: () => Promise.resolve({ initialized: true }),
  },
  initRepo: () => Promise.resolve({ status: 'initialized' }),
}))

function renderSettings() {
  return render(
    <MemoryRouter>
      <Connections />
    </MemoryRouter>,
  )
}

describe('Settings (Connections)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockHealth.mockResolvedValue({ status: 'ok' })
    mockTestCredentials.mockResolvedValue({
      git: { status: 'ok', message: '' },
      argocd: { status: 'ok', message: '' },
    })
  })

  it('renders the settings page title', () => {
    renderSettings()
    expect(screen.getByText('Settings')).toBeInTheDocument()
  })

  it('renders the Connection section heading', () => {
    renderSettings()
    expect(screen.getByText('Connection')).toBeInTheDocument()
  })

  it('renders the connection form with Repository URL field', () => {
    renderSettings()
    expect(screen.getByPlaceholderText('https://github.com/org/repo')).toBeInTheDocument()
  })

  it('renders Update Connection button when a connection exists', () => {
    renderSettings()
    expect(screen.getByText('Update Connection')).toBeInTheDocument()
  })

  it('renders Platform Info section', async () => {
    renderSettings()
    expect(screen.getByText('Platform Info')).toBeInTheDocument()
    expect(screen.getByText('Deployment Mode')).toBeInTheDocument()
    expect(screen.getByText('API Health')).toBeInTheDocument()
    await waitFor(() => {
      expect(screen.getByText('Healthy')).toBeInTheDocument()
    })
  })

  it('renders Git Provider in Platform Info', () => {
    renderSettings()
    expect(screen.getAllByText('Git Provider').length).toBeGreaterThanOrEqual(1)
  })

  it('renders ArgoCD Server in Platform Info', () => {
    renderSettings()
    expect(screen.getByText('ArgoCD Server')).toBeInTheDocument()
  })

  it('does not show connection tokens', () => {
    renderSettings()
    expect(screen.queryByText('ghp_****1234')).not.toBeInTheDocument()
    expect(screen.queryByText('argo****5678')).not.toBeInTheDocument()
  })

  it('shows Test Git and Test ArgoCD buttons', () => {
    renderSettings()
    expect(screen.getByText('Test Git')).toBeInTheDocument()
    expect(screen.getByText('Test ArgoCD')).toBeInTheDocument()
  })

  it('shows Test Connection button when connection exists', () => {
    renderSettings()
    expect(screen.getByText('Test Connection')).toBeInTheDocument()
  })

  it('calls updateConnection when form is submitted', async () => {
    const user = userEvent.setup()
    renderSettings()

    await user.click(screen.getByText('Update Connection'))

    await waitFor(() => {
      expect(mockUpdateConnection).toHaveBeenCalled()
    })
  })

  it('renders AI Configuration section', () => {
    renderSettings()
    expect(screen.getByText('AI Configuration')).toBeInTheDocument()
  })
})

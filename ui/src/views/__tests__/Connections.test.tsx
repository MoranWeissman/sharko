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
  {
    name: 'staging',
    description: 'Staging environment',
    git_provider: 'azuredevops',
    git_repo_identifier: 'MyOrg/MyProject/k8s-addons',
    git_token_masked: '****abcd',
    argocd_server_url: 'https://argocd.staging.example.com',
    argocd_token_masked: 'argo****efgh',
    argocd_namespace: 'argocd',
    is_default: false,
    is_active: false,
  },
]

const mockRefreshConnections = vi.fn()
const mockSetActiveConnection = vi.fn()

vi.mock('@/hooks/useConnections', () => ({
  useConnections: () => ({
    connections: mockConnections,
    activeConnection: 'production',
    loading: false,
    error: null,
    refreshConnections: mockRefreshConnections,
    setActiveConnection: mockSetActiveConnection,
  }),
}))

const mockSetActive = vi.fn().mockResolvedValue({})
const mockHealth = vi.fn().mockResolvedValue({ status: 'ok' })
const mockCreateConnection = vi.fn().mockResolvedValue({})
const mockUpdateConnection = vi.fn().mockResolvedValue({})
const mockTestCredentials = vi.fn().mockResolvedValue({
  git: { status: 'ok', message: '' },
  argocd: { status: 'ok', message: '' },
})

vi.mock('@/services/api', () => ({
  api: {
    setActiveConnection: (...args: unknown[]) => mockSetActive(...args),
    health: () => mockHealth(),
    createConnection: (...args: unknown[]) => mockCreateConnection(...args),
    updateConnection: (...args: unknown[]) => mockUpdateConnection(...args),
    testCredentials: (...args: unknown[]) => mockTestCredentials(...args),
    getAIStatus: () => Promise.resolve({ enabled: false }),
    getAISummary: () => Promise.resolve({ summary: '' }),
    getDatadogStatus: () => Promise.resolve({ enabled: false, site: "" }),
    getAIConfig: () => Promise.resolve({ current_provider: 'none', available_providers: [] }),
    setAIProvider: () => Promise.resolve({ status: 'ok', provider: 'none' }),
  },
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
  })

  it('renders the settings page title', () => {
    renderSettings()
    expect(screen.getByText('Settings')).toBeInTheDocument()
  })

  it('renders the Active Connections section heading', () => {
    renderSettings()
    expect(screen.getByText('Active Connections')).toBeInTheDocument()
  })

  it('renders connection cards with names and badges', () => {
    renderSettings()
    expect(screen.getAllByText('production').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('staging').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText('Default')).toBeInTheDocument()
    expect(screen.getByText('Active')).toBeInTheDocument()
  })

  it('renders connection details', () => {
    renderSettings()
    expect(screen.getByText('my-org/k8s-addons')).toBeInTheDocument()
    // ArgoCD URL appears in both connection card and Platform Info
    expect(screen.getAllByText('https://argocd.prod.example.com').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('github').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('azuredevops').length).toBeGreaterThanOrEqual(1)
  })

  it('does not show tokens', () => {
    renderSettings()
    expect(screen.queryByText('ghp_****1234')).not.toBeInTheDocument()
    expect(screen.queryByText('argo****5678')).not.toBeInTheDocument()
  })

  it('shows Switch link for inactive connections only', () => {
    renderSettings()
    const switchButtons = screen.getAllByText('Switch')
    // Only the staging (inactive) connection should have a Switch button
    expect(switchButtons).toHaveLength(1)
  })

  it('calls setActiveConnection when Switch is clicked', async () => {
    const user = userEvent.setup()
    renderSettings()

    await user.click(screen.getByText('Switch'))

    expect(mockSetActive).toHaveBeenCalledWith('staging')
    expect(mockRefreshConnections).toHaveBeenCalled()
  })

  it('renders Platform Info section with redesigned fields', async () => {
    renderSettings()
    expect(screen.getByText('Platform Info')).toBeInTheDocument()
    expect(screen.getByText('Deployment Mode')).toBeInTheDocument()
    expect(screen.getByText('Unknown')).toBeInTheDocument()
    expect(screen.getByText('API Health')).toBeInTheDocument()
    // Git Provider appears in both cards and platform info
    expect(screen.getAllByText('Git Provider').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText('ArgoCD Server')).toBeInTheDocument()
    await waitFor(() => {
      expect(screen.getByText('Healthy')).toBeInTheDocument()
    })
  })

  it('renders Add Connection button', () => {
    renderSettings()
    expect(screen.getByText('Add Connection')).toBeInTheDocument()
  })

  it('renders Edit buttons on each connection card', () => {
    renderSettings()
    const editButtons = screen.getAllByText('Edit')
    expect(editButtons).toHaveLength(2)
  })

  it('shows add form when Add Connection is clicked', async () => {
    const user = userEvent.setup()
    renderSettings()

    await user.click(screen.getByText('Add Connection'))

    expect(screen.getByText('New Connection')).toBeInTheDocument()
    expect(screen.getByText('Save')).toBeInTheDocument()
  })

  it('shows edit form when Edit is clicked on a connection', async () => {
    const user = userEvent.setup()
    renderSettings()

    const editButtons = screen.getAllByText('Edit')
    await user.click(editButtons[0])

    expect(screen.getByText('Edit Connection')).toBeInTheDocument()
    expect(screen.getByText('Update')).toBeInTheDocument()
  })

  it('calls createConnection when add form is submitted', async () => {
    const user = userEvent.setup()
    renderSettings()

    await user.click(screen.getByText('Add Connection'))

    // Fill in the git repo URL
    const repoUrl = screen.getByPlaceholderText('https://github.com/org/repo')
    await user.type(repoUrl, 'https://github.com/test-org/test-repo')

    await user.click(screen.getByText('Save'))

    await waitFor(() => {
      expect(mockCreateConnection).toHaveBeenCalled()
    })
  })

  it('calls updateConnection when edit form is submitted', async () => {
    const user = userEvent.setup()
    renderSettings()

    const editButtons = screen.getAllByText('Edit')
    await user.click(editButtons[0])

    await user.click(screen.getByText('Update'))

    await waitFor(() => {
      expect(mockUpdateConnection).toHaveBeenCalled()
    })
  })

  it('collapses add form when Cancel is clicked', async () => {
    const user = userEvent.setup()
    renderSettings()

    await user.click(screen.getByText('Add Connection'))
    expect(screen.getByText('New Connection')).toBeInTheDocument()

    // Click the Cancel button inside the add form
    const cancelButtons = screen.getAllByText('Cancel')
    await user.click(cancelButtons[0])

    expect(screen.queryByText('New Connection')).not.toBeInTheDocument()
  })

  it('displays ArgoCD URL without truncation', () => {
    renderSettings()
    const urlElements = screen.getAllByText('https://argocd.prod.example.com')
    // Check that none of the URL elements have the truncate class
    for (const el of urlElements) {
      expect(el.className).not.toContain('truncate')
    }
  })
})

import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { GitOpsSection } from '@/views/settings/GitOpsSection'

/*
 * V3-P2.2 — Default Addons UI (searchable table, Save opens a PR).
 *
 *   1. Hydrates selected defaults from GET /default-addons
 *   2. Renders a searchable table (F13 pattern: search + "showing X of Y" + scroll)
 *   3. Toggling checkboxes updates the selection
 *   4. "Save default addons" calls PUT /default-addons and shows the PR link
 *   5. GitOps settings (base branch etc.) still save via updateConnection
 *   6. default_addons is NOT in the connection payload
 */

const getConnectionsMock = vi.fn()
const getAddonCatalogMock = vi.fn()
const getDefaultAddonsMock = vi.fn()
const putDefaultAddonsMock = vi.fn()
const updateConnectionMock = vi.fn()
const healthMock = vi.fn()

vi.mock('@/hooks/useConnections', () => ({
  useConnections: () => ({
    connections: getConnectionsMock(),
    loading: false,
    error: null,
    refreshConnections: vi.fn(),
  }),
}))

vi.mock('@/services/api', () => ({
  api: {
    getAddonCatalog: () => getAddonCatalogMock(),
    getDefaultAddons: () => getDefaultAddonsMock(),
    putDefaultAddons: (addons: string[]) => putDefaultAddonsMock(addons),
    updateConnection: (name: string, payload: unknown) => updateConnectionMock(name, payload),
    health: () => healthMock(),
  },
}))

describe('GitOpsSection — Default Addons (V3-P2.2)', () => {
  beforeEach(() => {
    getConnectionsMock.mockReset()
    getAddonCatalogMock.mockReset()
    getDefaultAddonsMock.mockReset()
    putDefaultAddonsMock.mockReset()
    updateConnectionMock.mockReset()
    healthMock.mockReset()

    // Default mocks: one active connection, a small catalog, and two selected defaults.
    getConnectionsMock.mockReturnValue([
      {
        name: 'main-conn',
        is_active: true,
        git_provider: 'github',
        git_repo_identifier: 'org/repo',
        argocd_server_url: 'https://argocd.example.com',
        argocd_namespace: 'argocd',
        gitops: {
          base_branch: 'main',
          pr_auto_merge: false,
          host_cluster_name: 'hub',
          default_addons: '', // Ignored — UI reads from GET /default-addons
        },
      },
    ])
    getAddonCatalogMock.mockResolvedValue({
      addons: [
        { addon_name: 'cert-manager', version: '1.12.0' },
        { addon_name: 'external-dns', version: '6.20.4' },
        { addon_name: 'ingress-nginx', version: '4.7.0' },
      ],
    })
    getDefaultAddonsMock.mockResolvedValue({ addons: ['cert-manager', 'ingress-nginx'] })
    healthMock.mockResolvedValue({ status: 'ok' })
  })

  it('hydrates selected defaults from GET /default-addons', async () => {
    render(<GitOpsSection />)

    await waitFor(() => expect(getDefaultAddonsMock).toHaveBeenCalled())
    await waitFor(() => expect(screen.getByText('2 selected')).toBeInTheDocument())
  })

  it('renders the searchable table (F13: search + showing X of Y + scroll)', async () => {
    render(<GitOpsSection />)

    await waitFor(() => expect(screen.getByPlaceholderText('Search addons...')).toBeInTheDocument())
    expect(screen.getByText('Showing 3 of 3')).toBeInTheDocument()
    // The scrollable container has max-h-40 overflow-y-auto (same as F13 pattern).
    expect(screen.getByText('cert-manager')).toBeInTheDocument()
    expect(screen.getByText('external-dns')).toBeInTheDocument()
    expect(screen.getByText('ingress-nginx')).toBeInTheDocument()
  })

  it('filters the table as you type', async () => {
    const user = userEvent.setup()
    render(<GitOpsSection />)

    await waitFor(() => expect(screen.getByPlaceholderText('Search addons...')).toBeInTheDocument())

    await user.type(screen.getByPlaceholderText('Search addons...'), 'nginx')

    await waitFor(() => expect(screen.getByText('Showing 1 of 3')).toBeInTheDocument())
    expect(screen.getByText('ingress-nginx')).toBeInTheDocument()
    expect(screen.queryByText('cert-manager')).not.toBeInTheDocument()
    expect(screen.queryByText('external-dns')).not.toBeInTheDocument()
  })

  it('toggles checkboxes and updates the selection count', async () => {
    const user = userEvent.setup()
    render(<GitOpsSection />)

    await waitFor(() => expect(screen.getByText('2 selected')).toBeInTheDocument())

    // Uncheck cert-manager.
    const certCheckbox = screen.getAllByRole('checkbox').find((cb) =>
      cb.parentElement?.textContent?.includes('cert-manager')
    )!
    await user.click(certCheckbox)

    await waitFor(() => expect(screen.getByText('1 selected')).toBeInTheDocument())

    // Check external-dns.
    const dnsCheckbox = screen.getAllByRole('checkbox').find((cb) =>
      cb.parentElement?.textContent?.includes('external-dns')
    )!
    await user.click(dnsCheckbox)

    await waitFor(() => expect(screen.getByText('2 selected')).toBeInTheDocument())
  })

  it('"Save default addons" calls PUT /default-addons and shows the PR link', async () => {
    putDefaultAddonsMock.mockResolvedValue({ pr_url: 'https://github.com/org/repo/pull/123', pr_id: 123 })
    const user = userEvent.setup()
    render(<GitOpsSection />)

    await waitFor(() => expect(screen.getByText('Save default addons')).toBeInTheDocument())

    await user.click(screen.getByText('Save default addons'))

    await waitFor(() => expect(putDefaultAddonsMock).toHaveBeenCalledWith(['cert-manager', 'ingress-nginx']))
    await waitFor(() => expect(screen.getByText('PR #123')).toBeInTheDocument())
    expect(screen.getByText('PR #123').closest('a')).toHaveAttribute(
      'href',
      'https://github.com/org/repo/pull/123'
    )
  })

  it('"Save GitOps Settings" calls updateConnection without default_addons', async () => {
    updateConnectionMock.mockResolvedValue({})
    const user = userEvent.setup()
    render(<GitOpsSection />)

    await waitFor(() => expect(screen.getByText('Save GitOps Settings')).toBeInTheDocument())

    await user.click(screen.getByText('Save GitOps Settings'))

    await waitFor(() => expect(updateConnectionMock).toHaveBeenCalled())
    const payload = updateConnectionMock.mock.calls[0][1]
    expect(payload.gitops).toBeDefined()
    expect(payload.gitops.default_addons).toBeUndefined()
    expect(payload.gitops.base_branch).toBe('main')
  })

  it('does NOT call updateConnection for default addons', async () => {
    putDefaultAddonsMock.mockResolvedValue({ pr_url: 'https://github.com/org/repo/pull/123', pr_id: 123 })
    const user = userEvent.setup()
    render(<GitOpsSection />)

    await waitFor(() => expect(screen.getByText('Save default addons')).toBeInTheDocument())

    await user.click(screen.getByText('Save default addons'))

    await waitFor(() => expect(putDefaultAddonsMock).toHaveBeenCalled())
    expect(updateConnectionMock).not.toHaveBeenCalled()
  })
})

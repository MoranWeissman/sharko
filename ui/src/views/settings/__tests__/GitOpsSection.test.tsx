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
const getMigrationStatusMock = vi.fn()

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
    putDefaultAddons: (addons: string[], dryRun?: boolean) => putDefaultAddonsMock(addons, dryRun),
    updateConnection: (name: string, payload: unknown) => updateConnectionMock(name, payload),
    health: () => healthMock(),
    getMigrationStatus: () => getMigrationStatusMock(),
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
    getMigrationStatusMock.mockReset()

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
    // v3 by default — the pre-existing tests below all exercise the full
    // Default Addons panel, which only renders on a v3-layout repo.
    getMigrationStatusMock.mockResolvedValue({ format: 'v3', migration_available: true, message: '' })
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

    await waitFor(() => expect(putDefaultAddonsMock).toHaveBeenCalledWith(['cert-manager', 'ingress-nginx'], undefined))
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

  // V3-TX-A3 — Preview on every PR-opening operation. Surface 8: Save Default Addons.
  it('"Preview changes" calls putDefaultAddons(dry_run) and renders the dry-run without saving', async () => {
    putDefaultAddonsMock.mockResolvedValue({
      pr_title: 'Update default addons',
      files_to_write: [
        { path: 'configuration/default-addons.yaml', action: 'update' },
      ],
    })
    const user = userEvent.setup()
    render(<GitOpsSection />)

    await waitFor(() => expect(screen.getByText('Preview changes')).toBeInTheDocument())

    await user.click(screen.getByText('Preview changes'))

    // Dry-run call carries dry_run: true.
    await waitFor(() =>
      expect(putDefaultAddonsMock).toHaveBeenCalledWith(['cert-manager', 'ingress-nginx'], true),
    )
    // The returned diff renders via the shared DryRunPreview.
    await waitFor(() => expect(screen.getByText('Update default addons')).toBeInTheDocument())
    expect(screen.getByText('configuration/default-addons.yaml')).toBeInTheDocument()
    // Preview is a courtesy — no PR was opened (no PR link yet).
    expect(screen.queryByText('PR #123')).not.toBeInTheDocument()
  })

  // Walk finding — gitea gitops-save bug: the payload used to rebuild
  // git.repo_url from git_repo_identifier, which produced an empty,
  // provider-less git block for gitea and a 400 "git.provider is
  // required" from the server. The fix echoes the stored provider
  // verbatim and leaves the rest of git/argocd out of the payload.
  it('"Save GitOps Settings" echoes the connection\'s provider without rebuilding git/argocd', async () => {
    updateConnectionMock.mockResolvedValue({})
    const user = userEvent.setup()
    render(<GitOpsSection />)

    await waitFor(() => expect(screen.getByText('Save GitOps Settings')).toBeInTheDocument())
    await user.click(screen.getByText('Save GitOps Settings'))

    await waitFor(() => expect(updateConnectionMock).toHaveBeenCalled())
    const payload = updateConnectionMock.mock.calls[0][1]
    expect(payload.name).toBe('main-conn')
    expect(payload.git).toEqual({ provider: 'github' })
    expect(payload.argocd).toBeUndefined()
  })
})

describe('GitOpsSection — Default Addons hidden on v4 repos (walk finding)', () => {
  beforeEach(() => {
    getConnectionsMock.mockReset()
    getAddonCatalogMock.mockReset()
    getDefaultAddonsMock.mockReset()
    putDefaultAddonsMock.mockReset()
    updateConnectionMock.mockReset()
    healthMock.mockReset()
    getMigrationStatusMock.mockReset()

    getConnectionsMock.mockReturnValue([
      {
        name: 'main-conn',
        is_active: true,
        git_provider: 'gitea',
        git_repo_identifier: 'org/repo',
        argocd_server_url: 'https://argocd.example.com',
        argocd_namespace: 'argocd',
        gitops: { base_branch: 'main', pr_auto_merge: false },
      },
    ])
    getAddonCatalogMock.mockResolvedValue({
      addons: [{ addon_name: 'cert-manager', version: '1.12.0' }],
    })
    getDefaultAddonsMock.mockResolvedValue({ addons: [] })
    healthMock.mockResolvedValue({ status: 'ok' })
  })

  it('hides the Default Addons panel and shows the muted v4 notice instead', async () => {
    getMigrationStatusMock.mockResolvedValue({ format: 'v4', migration_available: false, message: '' })
    render(<GitOpsSection />)

    await waitFor(() =>
      expect(screen.getByTestId('default-addons-v4-notice')).toBeInTheDocument()
    )
    expect(
      screen.getByText('Default addons are not part of the v4 layout — picks happen in the catalog.')
    ).toBeInTheDocument()
    // The v3-only searchable-table UI must not render at all.
    expect(screen.queryByPlaceholderText('Search addons...')).not.toBeInTheDocument()
    expect(screen.queryByText('Save default addons')).not.toBeInTheDocument()
    expect(screen.queryByText('Preview changes')).not.toBeInTheDocument()
  })

  it('shows the full Default Addons panel on a v3 repo', async () => {
    getMigrationStatusMock.mockResolvedValue({ format: 'v3', migration_available: true, message: '' })
    render(<GitOpsSection />)

    await waitFor(() => expect(screen.getByPlaceholderText('Search addons...')).toBeInTheDocument())
    expect(screen.queryByTestId('default-addons-v4-notice')).not.toBeInTheDocument()
  })
})

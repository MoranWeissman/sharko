// ManagedSecrets.orphaned — leftover-secrets S1.2. Orphaned rows: a secret
// Sharko once wrote to a cluster whose source definition someone later
// hand-deleted from git. Nothing asks for it anymore, but it still carries
// Sharko's own labels on the cluster. Sharko never deletes it on its own —
// this suite holds down the operator-gated, explicitly-confirmed Delete
// that's the one exception, and the honest panel that goes with it.
//
// Pins:
//  - the chip renders with the right label and count;
//  - a row renders with its namespace, name, and the "Orphaned" status;
//  - row actions are Delete ONLY — no Refresh, no Sync;
//  - a viewer sees no Delete at all (the whole actions column is gated);
//  - cancel on the confirm does nothing — no API call, row still there;
//  - confirm calls deleteOrphanedSecret with the exact cluster/namespace/
//    name and refetches the list;
//  - the panel shows the locked sentence and fires NO live-read request.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { ManagedSecrets } from '@/views/ManagedSecrets'
import { SecretDetailPage } from '@/views/SecretDetailPage'
import { AuthContext } from '@/hooks/useAuth'
import type { ManagedSecretsResponse } from '@/services/models'

// SSF-9: real react-router-dom, no useNavigate mock — a row click now
// navigates to its own full page (SecretDetailPage) instead of opening a
// drawer in place. See renderPage.

const mockShowToast = vi.fn()
vi.mock('@/components/ToastNotification', async () => {
  const actual = await vi.importActual('@/components/ToastNotification')
  return { ...actual, showToast: (...args: unknown[]) => mockShowToast(...args) }
})

const mockGetManagedSecrets = vi.fn()
const mockGetClusterComparison = vi.fn()
const mockGetConnectionSecretResource = vi.fn()
const mockGetAddonValuesSecretResource = vi.fn()
const mockDeleteOrphanedSecret = vi.fn()
const mockFetchAuditLog = vi.fn()

vi.mock('@/services/api', () => ({
  api: { getClusterComparison: (...args: unknown[]) => mockGetClusterComparison(...args) },
  getManagedSecrets: (...args: unknown[]) => mockGetManagedSecrets(...args),
  getConnectionSecretResource: (...args: unknown[]) => mockGetConnectionSecretResource(...args),
  getAddonValuesSecretResource: (...args: unknown[]) => mockGetAddonValuesSecretResource(...args),
  deleteOrphanedSecret: (...args: unknown[]) => mockDeleteOrphanedSecret(...args),
  triggerSecretsReconcile: vi.fn(),
  checkAllAddonValuesSecrets: vi.fn(),
  reconcileCluster: vi.fn(),
  resyncClusterLabels: vi.fn(),
  refreshAddonValuesSecret: vi.fn(),
  syncAddonValuesSecret: vi.fn(),
  fetchAuditLog: (...args: unknown[]) => mockFetchAuditLog(...args),
}))

function authFor(role: string) {
  return {
    token: 'test-token',
    username: role,
    role,
    login: vi.fn(),
    logout: vi.fn(),
    isAuthenticated: true,
    isAdmin: role === 'admin',
    loading: false,
    error: null,
  }
}

function renderPage(role = 'operator', initialEntries: string[] = ['/secret-sync']) {
  return render(
    <AuthContext.Provider value={authFor(role)}>
      <MemoryRouter initialEntries={initialEntries}>
        <Routes>
          <Route path="/secret-sync" element={<ManagedSecrets />} />
          <Route path="/secret-sync/:rowKey" element={<SecretDetailPage />} />
        </Routes>
      </MemoryRouter>
    </AuthContext.Provider>,
  )
}

const response: ManagedSecretsResponse = {
  cluster_connection_secrets: [],
  addon_values_secrets: [
    {
      cluster: 'prod-eu',
      addon: 'datadog',
      secret_name: 'datadog-secrets',
      secret_namespace: 'datadog',
      state: 'in_sync',
      source: 'AWS Secrets Manager',
      self_heals: true,
    },
  ],
  orphaned_secrets: [
    {
      cluster: 'staging-us',
      secret_name: 'eso-creds',
      secret_namespace: 'external-secrets',
      addon: 'eso',
      state: 'orphaned',
      source: 'AWS Secrets Manager',
      last_checked: '2026-08-05T00:00:00Z',
    },
  ],
  engines: {
    cluster_connection: { wired: true, enabled: true, interval_seconds: 30, last_run: '2026-08-05T00:00:00Z' },
    addon_values: { wired: true, enabled: true, interval_seconds: 300, last_run: '2026-08-04T23:55:00Z' },
  },
  addon_values_secret_source: 'AWS Secrets Manager',
}

beforeEach(() => {
  vi.clearAllMocks()
  mockGetManagedSecrets.mockResolvedValue(response)
  mockFetchAuditLog.mockResolvedValue({ entries: [] })
})

describe('orphaned rows — the chip and the row', () => {
  it('renders a "Not in config" filter chip with the right count (SSF-8 word pass — was "Orphaned")', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByTestId('filter-chip-orphaned')).toBeInTheDocument())
    const chip = screen.getByTestId('filter-chip-orphaned')
    expect(chip).toHaveTextContent('Not in config')
    expect(chip).toHaveTextContent('1')
  })

  it('renders an orphaned row with its namespace, name, and the "Not in config" status', async () => {
    renderPage()
    const row = await screen.findByTestId('secret-row-orphaned-staging-us-external-secrets-eso-creds')
    expect(within(row).getByTestId('cell-name')).toHaveTextContent('eso-creds')
    expect(within(row).getByTestId('cell-namespace')).toHaveTextContent('external-secrets')
    expect(within(row).getByText('Not in config')).toBeInTheDocument()
    const dot = within(row).getAllByTestId('status-dot')[0]
    expect(dot).toHaveAttribute('data-status', 'orphaned')
  })

  it('offers Delete only — no Check now, no Sync', async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByTestId('secret-row-orphaned-staging-us-external-secrets-eso-creds')

    await user.click(screen.getByRole('button', { name: 'Actions for staging-us / eso' }))
    expect(await screen.findByRole('menuitem', { name: /Delete/ })).toBeInTheDocument()
    expect(screen.queryByRole('menuitem', { name: /Check now/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('menuitem', { name: /Sync/ })).not.toBeInTheDocument()
  })

  it('a viewer sees no Delete action at all — the whole actions column is role-gated', async () => {
    renderPage('viewer')
    await screen.findByTestId('secret-row-orphaned-staging-us-external-secrets-eso-creds')
    expect(screen.queryByRole('button', { name: 'Actions for staging-us / eso' })).not.toBeInTheDocument()
  })
})

describe('orphaned rows — the delete confirm flow', () => {
  it('cancel does nothing — no API call, the row is still there', async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByTestId('secret-row-orphaned-staging-us-external-secrets-eso-creds')

    await user.click(screen.getByRole('button', { name: 'Actions for staging-us / eso' }))
    await user.click(await screen.findByRole('menuitem', { name: /Delete/ }))

    const modal = await screen.findByText(/This permanently deletes secret/)
    expect(modal).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Cancel' }))

    await waitFor(() => expect(screen.queryByText(/This permanently deletes secret/)).not.toBeInTheDocument())
    expect(mockDeleteOrphanedSecret).not.toHaveBeenCalled()
    expect(screen.getByTestId('secret-row-orphaned-staging-us-external-secrets-eso-creds')).toBeInTheDocument()
  })

  it('confirm calls deleteOrphanedSecret with the exact cluster/namespace/name, shows the server message, and refetches', async () => {
    const user = userEvent.setup()
    mockDeleteOrphanedSecret.mockResolvedValue({
      status: 'deleted',
      cluster: 'staging-us',
      namespace: 'external-secrets',
      name: 'eso-creds',
      message: 'Deleted secret "external-secrets/eso-creds" from cluster "staging-us".',
    })
    renderPage()
    await screen.findByTestId('secret-row-orphaned-staging-us-external-secrets-eso-creds')

    await user.click(screen.getByRole('button', { name: 'Actions for staging-us / eso' }))
    await user.click(await screen.findByRole('menuitem', { name: /Delete/ }))
    await screen.findByText(/This permanently deletes secret/)

    // The confirm body names the exact blast radius (locked wording).
    expect(
      screen.getByText(
        'This permanently deletes secret "external-secrets/eso-creds" from cluster "staging-us". Sharko wrote it once, but nothing in git asks for it anymore. This cannot be undone.',
      ),
    ).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Delete' }))

    await waitFor(() => expect(mockDeleteOrphanedSecret).toHaveBeenCalledWith('staging-us', 'external-secrets', 'eso-creds'))
    await waitFor(() =>
      expect(mockShowToast).toHaveBeenCalledWith('Deleted secret "external-secrets/eso-creds" from cluster "staging-us".', 'success'),
    )
    // The list refetches after a successful delete.
    await waitFor(() => expect(mockGetManagedSecrets).toHaveBeenCalledTimes(2))
  })

  it('a failed delete toasts the server error and never closes silently', async () => {
    const user = userEvent.setup()
    mockDeleteOrphanedSecret.mockRejectedValue(
      Object.assign(new Error('Someone re-claimed this secret since Sharko last checked — nothing was deleted.'), { status: 409 }),
    )
    renderPage()
    await screen.findByTestId('secret-row-orphaned-staging-us-external-secrets-eso-creds')

    await user.click(screen.getByRole('button', { name: 'Actions for staging-us / eso' }))
    await user.click(await screen.findByRole('menuitem', { name: /Delete/ }))
    await screen.findByText(/This permanently deletes secret/)
    await user.click(screen.getByRole('button', { name: 'Delete' }))

    await waitFor(() =>
      expect(mockShowToast).toHaveBeenCalledWith(
        'Someone re-claimed this secret since Sharko last checked — nothing was deleted.',
        'error',
      ),
    )
  })
})

describe('orphaned rows — the panel', () => {
  async function openRow() {
    await waitFor(() => expect(screen.getByTestId('secret-row-orphaned-staging-us-external-secrets-eso-creds')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('secret-row-orphaned-staging-us-external-secrets-eso-creds'))
    return screen.findByTestId('secret-detail-panel')
  }

  it('shows the panel sentence (SSF-8 word pass: "Orphaned" → "Not in config")', async () => {
    renderPage()
    const panel = await openRow()
    expect(within(panel).getByTestId('diff-verdict')).toHaveTextContent(
      'Not in config — its source in git is gone. Sharko wrote this secret once, but nothing asks for it anymore.',
    )
  })

  it('fires NO live-read request for an orphaned row', async () => {
    renderPage()
    await openRow()
    expect(mockGetAddonValuesSecretResource).not.toHaveBeenCalled()
    expect(mockGetConnectionSecretResource).not.toHaveBeenCalled()
  })

  it('shows a Delete button in the panel, gated to admin/operator, that opens the same confirm', async () => {
    const user = userEvent.setup()
    renderPage()
    const panel = await openRow()

    expect(within(panel).queryByTestId('detail-refresh')).not.toBeInTheDocument()
    expect(within(panel).queryByTestId('detail-sync')).not.toBeInTheDocument()
    const deleteButton = within(panel).getByTestId('detail-delete')
    expect(deleteButton).toBeInTheDocument()

    await user.click(deleteButton)
    expect(await screen.findByText(/This permanently deletes secret/)).toBeInTheDocument()
  })
})

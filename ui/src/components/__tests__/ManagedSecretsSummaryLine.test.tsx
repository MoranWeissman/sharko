// ManagedSecretsSummaryLine — the System page's one quiet line about
// managed secrets (S1). Covers: the "all in sync" / "N out of sync" /
// "not managing any secrets yet" wording, and the link to /secret-sync
// (gitops-proud P4-I D1 rename — the page used to be at /secrets).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { ManagedSecretsSummaryLine } from '@/components/ManagedSecretsSummaryLine'

const mockGetManagedSecrets = vi.fn()
vi.mock('@/services/api', () => ({
  getManagedSecrets: (...args: unknown[]) => mockGetManagedSecrets(...args),
}))

function renderLine() {
  return render(
    <MemoryRouter>
      <ManagedSecretsSummaryLine />
    </MemoryRouter>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('ManagedSecretsSummaryLine', () => {
  it('says all in sync when nothing is out of sync', async () => {
    mockGetManagedSecrets.mockResolvedValue({
      cluster_connection_secrets: [{ cluster: 'prod-eu', state: 'in_sync' }],
      addon_values_secrets: [{ cluster: 'prod-eu', addon: 'datadog', state: 'in_sync' }],
      engines: { cluster_connection: { wired: true }, addon_values: { wired: true } },
    })
    renderLine()

    await waitFor(() => expect(screen.getByText(/Sharko manages 2 secrets — all in sync\./)).toBeInTheDocument())
    // Secrets-area rename (SN-5): the link goes into the Secrets area and
    // the button says "View Secrets" — "Secret Sync" is retired as a name.
    expect(screen.getByRole('link', { name: 'View Secrets' })).toHaveAttribute('href', '/secrets/connections')
  })

  it('counts out_of_sync and missing rows together as "out of sync"', async () => {
    mockGetManagedSecrets.mockResolvedValue({
      cluster_connection_secrets: [
        { cluster: 'prod-eu', state: 'out_of_sync' },
        { cluster: 'staging-us', state: 'missing' },
      ],
      addon_values_secrets: [{ cluster: 'prod-eu', addon: 'datadog', state: 'in_sync' }],
      engines: { cluster_connection: { wired: true }, addon_values: { wired: true } },
    })
    renderLine()

    await waitFor(() => expect(screen.getByText(/Sharko manages 3 secrets — 2 out of sync\./)).toBeInTheDocument())
  })

  it('says it is not managing any secrets yet when both tables are empty', async () => {
    mockGetManagedSecrets.mockResolvedValue({
      cluster_connection_secrets: [],
      addon_values_secrets: [],
      engines: { cluster_connection: { wired: false }, addon_values: { wired: false } },
    })
    renderLine()

    await waitFor(() => expect(screen.getByText('Sharko is not managing any secrets yet.')).toBeInTheDocument())
  })

  it('renders nothing (never a crash) when the fetch fails', async () => {
    mockGetManagedSecrets.mockRejectedValue(new Error('boom'))
    const { container } = renderLine()

    await waitFor(() => expect(container.textContent).toBe(''))
  })
})

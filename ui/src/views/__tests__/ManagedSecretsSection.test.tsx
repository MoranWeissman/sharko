// ManagedSecretsSection — the System page's "Managed secrets" tables.
//
// Covers: both tables render from GET /api/v1/system/managed-secrets,
// search/pagination behave, row click-through routes to the right page,
// and unknown timestamps render as an honest "Unknown" instead of a blank
// or a fabricated "just now".

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { ManagedSecretsSection } from '@/components/ManagedSecretsSection'
import type { ManagedSecretsResponse } from '@/services/models'

const mockNavigate = vi.fn()
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom')
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  }
})

const mockGetManagedSecrets = vi.fn()
const mockTriggerSecretsReconcile = vi.fn()
vi.mock('@/services/api', () => ({
  getManagedSecrets: (...args: unknown[]) => mockGetManagedSecrets(...args),
  triggerSecretsReconcile: (...args: unknown[]) => mockTriggerSecretsReconcile(...args),
}))

function renderSection() {
  return render(
    <MemoryRouter>
      <ManagedSecretsSection />
    </MemoryRouter>,
  )
}

// Tables render in a fixed order: connection secrets first, addon values
// second — see ManagedSecretsSection's JSX below the engine-line card.
function connectionTable() {
  return screen.getAllByRole('table')[0]
}
function addonTable() {
  return screen.getAllByRole('table')[1]
}

const baseResponse: ManagedSecretsResponse = {
  cluster_connection_secrets: [
    {
      cluster: 'prod-eu',
      secret_namespace: 'argocd',
      secret_name: 'prod-eu',
      state: 'in_sync',
      last_checked: '2026-08-05T00:00:00Z',
      last_repaired: '2026-08-04T23:00:00Z',
      last_repaired_detail: 'secret created',
    },
    {
      cluster: 'staging-us',
      state: 'unknown',
    },
  ],
  addon_values_secrets: [
    {
      cluster: 'prod-eu',
      addon: 'datadog',
      secret_name: 'datadog-secrets',
      secret_namespace: 'datadog',
    },
  ],
  engines: {
    cluster_connection: { wired: true, interval_seconds: 30, last_run: '2026-08-05T00:00:00Z' },
    addon_values: { wired: true, interval_seconds: 300, last_run: '2026-08-04T23:55:00Z' },
  },
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('ManagedSecretsSection', () => {
  it('renders both tables with the rows the API returns', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderSection()

    await waitFor(() => expect(screen.getAllByRole('table')).toHaveLength(2))

    expect(within(connectionTable()).getByText('prod-eu')).toBeInTheDocument()
    expect(within(connectionTable()).getByText('staging-us')).toBeInTheDocument()
    expect(within(connectionTable()).getByText('argocd/prod-eu')).toBeInTheDocument()

    expect(within(addonTable()).getByText('datadog')).toBeInTheDocument()
    expect(within(addonTable()).getByText('datadog/datadog-secrets')).toBeInTheDocument()
  })

  it('renders an honest "Unknown" for a row with no last_checked/last_repaired, never a fake timestamp', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderSection()

    await waitFor(() => expect(within(connectionTable()).getByText('staging-us')).toBeInTheDocument())

    // staging-us's row (state "unknown", no secret name/timestamps) must
    // show "Unknown" for its secret, state badge, last-checked, and
    // last-repaired cells — never a blank or a guessed value.
    const stagingRow = within(connectionTable()).getByText('staging-us').closest('tr')
    expect(stagingRow).not.toBeNull()
    expect(within(stagingRow as HTMLElement).getAllByText('Unknown').length).toBe(4)

    // The addon-values row always shows "Unknown" for both timestamp
    // columns — the values engine has no per-row history (documented gap).
    const addonRow = within(addonTable()).getByText('datadog').closest('tr')
    expect(addonRow).not.toBeNull()
    expect(within(addonRow as HTMLElement).getAllByText('Unknown').length).toBe(2)
  })

  it('filters the connection-secrets table by search text', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderSection()

    await waitFor(() => expect(within(connectionTable()).getByText('prod-eu')).toBeInTheDocument())
    const search = screen.getByPlaceholderText('Search by cluster or secret name...')
    fireEvent.change(search, { target: { value: 'staging' } })

    expect(within(connectionTable()).queryByText('prod-eu')).not.toBeInTheDocument()
    expect(within(connectionTable()).getByText('staging-us')).toBeInTheDocument()
    // The addon-values table has its own independent search box — untouched.
    expect(within(addonTable()).getByText('prod-eu')).toBeInTheDocument()
  })

  it('filters the connection-secrets table by state', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderSection()

    await waitFor(() => expect(within(connectionTable()).getByText('prod-eu')).toBeInTheDocument())
    const stateFilter = screen.getByLabelText('State')
    fireEvent.change(stateFilter, { target: { value: 'unknown' } })

    expect(within(connectionTable()).queryByText('prod-eu')).not.toBeInTheDocument()
    expect(within(connectionTable()).getByText('staging-us')).toBeInTheDocument()
  })

  it('clicking a connection-secret row navigates to that cluster page', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderSection()

    await waitFor(() => expect(within(connectionTable()).getByText('prod-eu')).toBeInTheDocument())
    fireEvent.click(within(connectionTable()).getByText('prod-eu'))

    expect(mockNavigate).toHaveBeenCalledWith('/clusters/prod-eu')
  })

  it('clicking an addon-values row navigates to that addon page', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderSection()

    await waitFor(() => expect(within(addonTable()).getByText('datadog')).toBeInTheDocument())
    fireEvent.click(within(addonTable()).getByText('datadog'))

    expect(mockNavigate).toHaveBeenCalledWith('/addons/datadog')
  })

  it('paginates the connection-secrets table', async () => {
    const manyRows = Array.from({ length: 12 }, (_, i) => ({
      cluster: `cluster-${String(i).padStart(2, '0')}`,
      state: 'in_sync',
      last_checked: '2026-08-05T00:00:00Z',
    }))
    mockGetManagedSecrets.mockResolvedValue({
      cluster_connection_secrets: manyRows,
      addon_values_secrets: [],
      engines: {
        cluster_connection: { wired: true, interval_seconds: 30 },
        addon_values: { wired: false },
      },
    })
    renderSection()

    await waitFor(() => expect(within(connectionTable()).getByText('cluster-00')).toBeInTheDocument())
    // Default page size is 10 — the 11th/12th rows are on page 2.
    expect(within(connectionTable()).queryByText('cluster-11')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Next' }))

    await waitFor(() => expect(within(connectionTable()).getByText('cluster-11')).toBeInTheDocument())
    expect(within(connectionTable()).queryByText('cluster-00')).not.toBeInTheDocument()
  })

  it('reports an engine as not running, rather than fabricating cadence info, when it is not wired', async () => {
    mockGetManagedSecrets.mockResolvedValue({
      cluster_connection_secrets: [],
      addon_values_secrets: [],
      engines: {
        cluster_connection: { wired: false },
        addon_values: { wired: false },
      },
    })
    renderSection()

    await waitFor(() => expect(screen.getAllByText('· Not running on this server.')).toHaveLength(2))
  })
})

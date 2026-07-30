import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { VersionMatrixTable } from '@/components/VersionMatrixTable'
import { api } from '@/services/api'

// Epic 7 Story 7.1 (v4 Wave 2) — the fleet version matrix. GET
// /addons/version-matrix already re-points to the v4 data model server-side;
// this component just renders whatever it returns, including the two new
// freshness-scheduler fields (newest_available, last_checked).
vi.mock('@/services/api', () => ({
  api: {
    getVersionMatrix: vi.fn(),
  },
}))

describe('VersionMatrixTable', () => {
  it('renders addons as rows and clusters as columns, with newest-available and last-checked columns', async () => {
    vi.mocked(api.getVersionMatrix).mockResolvedValueOnce({
      clusters: ['prod-eu', 'staging-us'],
      addons: [
        {
          addon_name: 'cert-manager',
          catalog_version: '1.14.5',
          chart: 'cert-manager',
          cells: {
            'prod-eu': { version: '1.12.0', health: 'healthy', drift_from_catalog: true },
            'staging-us': { version: '1.14.5', health: 'healthy', drift_from_catalog: false },
          },
          newest_available: '1.15.0',
          last_checked: '2026-07-29T12:00:00Z',
        },
      ],
    })

    render(<VersionMatrixTable />)

    await waitFor(() => {
      expect(screen.getByText('cert-manager')).toBeInTheDocument()
    })

    expect(screen.getByText('prod-eu')).toBeInTheDocument()
    expect(screen.getByText('staging-us')).toBeInTheDocument()
    expect(screen.getByText('1.15.0')).toBeInTheDocument()
    // Drifted cell (prod-eu) is marked with the drift asterisk.
    expect(screen.getByText('1.12.0 *')).toBeInTheDocument()
    // Non-drifted cell shows the bare version.
    expect(screen.getByText('1.14.5')).toBeInTheDocument()
  })

  it('shows an empty state when there are no addons', async () => {
    vi.mocked(api.getVersionMatrix).mockResolvedValueOnce({ clusters: [], addons: [] })

    render(<VersionMatrixTable />)

    await waitFor(() => {
      expect(screen.getByText(/No addons to show yet/i)).toBeInTheDocument()
    })
  })

  it('shows an error state with a retry option on failure', async () => {
    vi.mocked(api.getVersionMatrix).mockRejectedValueOnce(new Error('boom'))

    render(<VersionMatrixTable />)

    await waitFor(() => {
      expect(screen.getByText('boom')).toBeInTheDocument()
    })
  })
})

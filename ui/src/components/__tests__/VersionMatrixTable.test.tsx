import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
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

// Sort-first-what's-outdated (dashboard UX review 2026-08-01, walk finding
// #1): rows with a newer version available move to the top, so landing on
// the matrix from the Dashboard's Upgrades card shows what's outdated
// without scrolling. Same isNewerVersion the Dashboard's Upgrades stat
// uses (lib/utils.ts).
describe('VersionMatrixTable — sort-first-what\'s-outdated (walk finding #1)', () => {
  it('sorts rows with an available upgrade before rows that are up to date, and marks them', async () => {
    vi.mocked(api.getVersionMatrix).mockResolvedValueOnce({
      clusters: ['prod-eu'],
      addons: [
        {
          addon_name: 'up-to-date-addon',
          catalog_version: '1.0.0',
          chart: 'up-to-date-addon',
          cells: { 'prod-eu': { version: '1.0.0', health: 'healthy', drift_from_catalog: false } },
          newest_available: '1.0.0',
          last_checked: '2026-07-29T12:00:00Z',
        },
        {
          addon_name: 'outdated-addon',
          catalog_version: '1.0.0',
          chart: 'outdated-addon',
          cells: { 'prod-eu': { version: '1.0.0', health: 'healthy', drift_from_catalog: false } },
          newest_available: '2.0.0',
          last_checked: '2026-07-29T12:00:00Z',
        },
      ],
    })

    render(<VersionMatrixTable />)

    await waitFor(() => {
      expect(screen.getByText('outdated-addon')).toBeInTheDocument()
    })

    const rows = screen.getAllByRole('row').filter((r) => r.textContent?.includes('-addon'))
    expect(rows[0].textContent).toContain('outdated-addon')
    expect(rows[1].textContent).toContain('up-to-date-addon')

    // The outdated row carries the "newer version available" marker; the
    // up-to-date one doesn't.
    expect(screen.getByLabelText('A newer version is available')).toBeInTheDocument()
  })
})

// outdatedOnly (WQ-2) — the Fleet Status Strip's Upgrades segment is now a
// bare clickable number that deep-links to the matrix with
// ?view=matrix&filter=outdated. AddonCatalog reads that param and passes
// outdatedOnly down; the table hides up-to-date rows and shows a
// dismissible "outdated only ×" chip.
describe('VersionMatrixTable — outdatedOnly filter (WQ-2)', () => {
  const twoRowFixture = {
    clusters: ['prod-eu'],
    addons: [
      {
        addon_name: 'up-to-date-addon',
        catalog_version: '1.0.0',
        chart: 'up-to-date-addon',
        cells: { 'prod-eu': { version: '1.0.0', health: 'healthy', drift_from_catalog: false } },
        newest_available: '1.0.0',
        last_checked: '2026-07-29T12:00:00Z',
      },
      {
        addon_name: 'outdated-addon',
        catalog_version: '1.0.0',
        chart: 'outdated-addon',
        cells: { 'prod-eu': { version: '1.0.0', health: 'healthy', drift_from_catalog: false } },
        newest_available: '2.0.0',
        last_checked: '2026-07-29T12:00:00Z',
      },
    ],
  }

  it('shows only rows with an available upgrade when outdatedOnly is true', async () => {
    vi.mocked(api.getVersionMatrix).mockResolvedValueOnce(twoRowFixture)

    render(<VersionMatrixTable outdatedOnly />)

    await waitFor(() => {
      expect(screen.getByText('outdated-addon')).toBeInTheDocument()
    })
    expect(screen.queryByText('up-to-date-addon')).not.toBeInTheDocument()
  })

  it('shows every row when outdatedOnly is false (default)', async () => {
    vi.mocked(api.getVersionMatrix).mockResolvedValueOnce(twoRowFixture)

    render(<VersionMatrixTable />)

    await waitFor(() => {
      expect(screen.getByText('outdated-addon')).toBeInTheDocument()
    })
    expect(screen.getByText('up-to-date-addon')).toBeInTheDocument()
  })

  it('renders a dismissible "outdated only" chip that clears the filter on click', async () => {
    vi.mocked(api.getVersionMatrix).mockResolvedValueOnce(twoRowFixture)
    const onClear = vi.fn()

    render(<VersionMatrixTable outdatedOnly onClearOutdatedFilter={onClear} />)

    await waitFor(() => {
      expect(screen.getByTestId('matrix-outdated-chip')).toBeInTheDocument()
    })
    expect(screen.getByTestId('matrix-outdated-chip').textContent).toContain('outdated only')

    fireEvent.click(screen.getByRole('button', { name: /clear the outdated filter/i }))
    expect(onClear).toHaveBeenCalledTimes(1)
  })

  it('does not render the chip when outdatedOnly is false', async () => {
    vi.mocked(api.getVersionMatrix).mockResolvedValueOnce(twoRowFixture)

    render(<VersionMatrixTable />)

    await waitFor(() => {
      expect(screen.getByText('outdated-addon')).toBeInTheDocument()
    })
    expect(screen.queryByTestId('matrix-outdated-chip')).not.toBeInTheDocument()
  })

  it('shows a "nothing outdated" message when the filter excludes every row', async () => {
    vi.mocked(api.getVersionMatrix).mockResolvedValueOnce({
      clusters: ['prod-eu'],
      addons: [
        {
          addon_name: 'up-to-date-addon',
          catalog_version: '1.0.0',
          chart: 'up-to-date-addon',
          cells: { 'prod-eu': { version: '1.0.0', health: 'healthy', drift_from_catalog: false } },
          newest_available: '1.0.0',
          last_checked: '2026-07-29T12:00:00Z',
        },
      ],
    })

    render(<VersionMatrixTable outdatedOnly />)

    await waitFor(() => {
      expect(screen.getByText(/Nothing outdated/i)).toBeInTheDocument()
    })
    expect(screen.queryByText('up-to-date-addon')).not.toBeInTheDocument()
  })
})

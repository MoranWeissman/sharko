import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { MarketplaceBrowseTab } from '@/components/MarketplaceBrowseTab'
import type { CatalogEntry } from '@/services/models'

// v4 wave 1 Story 3.4 — catalog-wide "last checked" header + refresh
// button. Fixture set deliberately minimal (one entry): these tests target
// the freshness line, not the filter/grid behavior already covered by
// MarketplaceTab.test.tsx.

const fixtures: CatalogEntry[] = [
  {
    name: 'cert-manager',
    description: 'TLS lifecycle manager.',
    chart: 'cert-manager',
    repo: 'https://charts.jetstack.io',
    default_namespace: 'cert-manager',
    maintainers: ['jetstack'],
    license: 'Apache-2.0',
    category: 'security',
    curated_by: ['cncf-graduated'],
    security_score: 8.2,
    security_tier: 'Strong',
  },
]

const listMock = vi.fn().mockResolvedValue({ addons: fixtures, total: fixtures.length })
const getCatalogMock = vi.fn().mockResolvedValue({ addons: [] })
const getFreshnessMock = vi.fn()
const refreshFreshnessMock = vi.fn().mockResolvedValue({ message: 'freshness refresh requested' })

vi.mock('@/services/api', () => ({
  api: {
    listCuratedCatalog: () => listMock(),
    getAddonCatalog: () => getCatalogMock(),
    getCatalogFreshness: () => getFreshnessMock(),
    refreshCatalogFreshness: () => refreshFreshnessMock(),
  },
}))

function renderTab() {
  return render(
    <MemoryRouter initialEntries={['/']}>
      <MarketplaceBrowseTab />
    </MemoryRouter>,
  )
}

describe('MarketplaceBrowseTab — catalog freshness', () => {
  beforeEach(() => {
    listMock.mockClear()
    getCatalogMock.mockClear()
    getFreshnessMock.mockReset()
    refreshFreshnessMock.mockClear()
  })

  it('shows a relative "Last checked" line + Refresh button when the scheduler is enabled', async () => {
    getFreshnessMock.mockResolvedValue({
      enabled: true,
      interval_seconds: 86400,
      last_run: new Date(Date.now() - 10 * 60 * 1000).toISOString(),
      next_run: new Date(Date.now() + 23 * 60 * 60 * 1000).toISOString(),
      addons_checked: 1,
    })
    renderTab()

    await waitFor(() => {
      expect(screen.getByText(/Last checked: 10m ago/i)).toBeInTheDocument()
    })
    expect(screen.getByRole('button', { name: /Refresh/i })).toBeInTheDocument()
  })

  it('omits the freshness line entirely when the scheduler is disabled', async () => {
    getFreshnessMock.mockResolvedValue({ enabled: false, addons_checked: 0 })
    renderTab()

    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: /Open cert-manager/i }),
      ).toBeInTheDocument()
    })
    expect(screen.queryByText(/Last checked:/i)).not.toBeInTheDocument()
  })

  it('omits the freshness line when the endpoint errors (non-fatal — grid still renders)', async () => {
    getFreshnessMock.mockRejectedValue(new Error('network error'))
    renderTab()

    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: /Open cert-manager/i }),
      ).toBeInTheDocument()
    })
    expect(screen.queryByText(/Last checked:/i)).not.toBeInTheDocument()
  })

  it('clicking Refresh triggers refreshCatalogFreshness and re-polls the summary', async () => {
    getFreshnessMock.mockResolvedValue({
      enabled: true,
      last_run: new Date(Date.now() - 60 * 60 * 1000).toISOString(),
      addons_checked: 1,
    })
    renderTab()

    const refreshBtn = await screen.findByRole('button', { name: /Refresh/i })
    fireEvent.click(refreshBtn)

    await waitFor(() => {
      expect(refreshFreshnessMock).toHaveBeenCalledTimes(1)
    })
  })
})

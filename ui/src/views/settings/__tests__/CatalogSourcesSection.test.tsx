import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { CatalogSourceRecord } from '@/services/models'
import { CatalogSourcesSection } from '@/views/settings/CatalogSourcesSection'

/*
 * V123-1.8 — Settings → Catalog Sources admin section.
 *
 * Six cases per story spec:
 *   1. loading → records on successful fetch
 *   2. refresh button fires api.refreshCatalogSources
 *   3. error + retry when listCatalogSources rejects
 *   4. entry_count + last_fetched text on each row
 *   5. renders SourceBadge per row (embedded + third-party)
 *   6. does NOT render raw URL as a clickable anchor
 */

const listMock = vi.fn()
const refreshMock = vi.fn()

vi.mock('@/services/api', () => ({
  api: {
    listCatalogSources: () => listMock(),
    refreshCatalogSources: () => refreshMock(),
  },
}))

const embeddedRecord: CatalogSourceRecord = {
  url: 'embedded',
  status: 'ok',
  last_fetched: '2026-04-23T10:00:00Z',
  entry_count: 12,
  verified: false,
}

const thirdPartyRecord: CatalogSourceRecord = {
  url: 'https://catalogs.example.com/addons.yaml',
  status: 'ok',
  last_fetched: '2026-04-23T10:05:00Z',
  entry_count: 4,
  verified: true,
  issuer: 'acme-ci',
}

describe('CatalogSourcesSection', () => {
  beforeEach(() => {
    listMock.mockReset()
    refreshMock.mockReset()
  })

  it('renders loading state then record list on successful fetch', async () => {
    listMock.mockResolvedValue([embeddedRecord])
    render(<CatalogSourcesSection />)

    // Loading state text is live-region friendly.
    expect(screen.getByText(/Loading catalog sources/i)).toBeInTheDocument()

    // Wait for the embedded record to appear (SourceBadge renders "Internal").
    await waitFor(() =>
      expect(screen.getByText('Internal')).toBeInTheDocument(),
    )

    // Table header is present (semantic table structure).
    expect(screen.getByRole('columnheader', { name: /Source/ })).toBeInTheDocument()
    expect(screen.getByRole('columnheader', { name: /Status/ })).toBeInTheDocument()
    expect(screen.getByRole('columnheader', { name: /Entries/ })).toBeInTheDocument()
  })

  it('fires refreshCatalogSources when the Refresh button is clicked', async () => {
    listMock.mockResolvedValue([embeddedRecord])
    refreshMock.mockResolvedValue([embeddedRecord, thirdPartyRecord])
    const user = userEvent.setup()
    render(<CatalogSourcesSection />)

    // Wait for initial render to complete.
    await waitFor(() => expect(listMock).toHaveBeenCalledTimes(1))
    await waitFor(() =>
      expect(screen.getByText('Internal')).toBeInTheDocument(),
    )

    const btn = screen.getByRole('button', { name: /Refresh catalog sources/i })
    await user.click(btn)

    await waitFor(() => expect(refreshMock).toHaveBeenCalledTimes(1))

    // Updated list: the third-party row appears now.
    await waitFor(() =>
      expect(screen.getByText('catalogs.example.com')).toBeInTheDocument(),
    )
    // Success status strip.
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        /Catalog sources refreshed/i,
      ),
    )
  })

  it('shows error + retry when listCatalogSources rejects; retry succeeds', async () => {
    listMock.mockRejectedValueOnce(new Error('boom')).mockResolvedValueOnce([
      embeddedRecord,
    ])
    const user = userEvent.setup()
    render(<CatalogSourcesSection />)

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(/Failed to load catalog sources/i)

    const retryBtn = screen.getByRole('button', { name: /^Retry$/i })
    await user.click(retryBtn)

    // Second call resolved — embedded record appears.
    await waitFor(() =>
      expect(screen.getByText('Internal')).toBeInTheDocument(),
    )
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('renders entry_count + last_fetched text on each row (including null dash)', async () => {
    const twoRows: CatalogSourceRecord[] = [
      { ...embeddedRecord, entry_count: 7, last_fetched: '2026-04-23T09:00:00Z' },
      {
        url: 'https://catalogs.example.com/addons.yaml',
        status: 'stale',
        last_fetched: null,
        entry_count: 3,
        verified: false,
      },
    ]
    listMock.mockResolvedValue(twoRows)
    render(<CatalogSourcesSection />)

    await waitFor(() =>
      expect(screen.getByText('catalogs.example.com')).toBeInTheDocument(),
    )

    // Entry counts appear as plain numbers.
    expect(screen.getByText('7')).toBeInTheDocument()
    expect(screen.getByText('3')).toBeInTheDocument()

    // RFC3339 string rendered verbatim.
    expect(
      screen.getByText('2026-04-23T09:00:00Z'),
    ).toBeInTheDocument()

    // Null last_fetched renders the em/en dash placeholder.
    expect(screen.getAllByText('—').length).toBeGreaterThan(0)
  })

  it('renders a SourceBadge per row (embedded + third-party)', async () => {
    listMock.mockResolvedValue([embeddedRecord, thirdPartyRecord])
    render(<CatalogSourcesSection />)

    // Embedded → "Internal" label (from SourceBadge).
    await waitFor(() =>
      expect(screen.getByText('Internal')).toBeInTheDocument(),
    )

    // Third-party → host string (from SourceBadge).
    expect(screen.getByText('catalogs.example.com')).toBeInTheDocument()

    // Verified pill only on the third-party row (it's the only one with
    // verified=true). The accessible name exposes the issuer when present.
    expect(
      screen.getByLabelText(/Verified \(issuer: acme-ci\)/i),
    ).toBeInTheDocument()
  })

  it('does NOT render the raw URL as a clickable anchor', async () => {
    listMock.mockResolvedValue([thirdPartyRecord])
    const { container } = render(<CatalogSourcesSection />)

    // Wait for the data to render.
    await waitFor(() =>
      expect(screen.getByText('catalogs.example.com')).toBeInTheDocument(),
    )

    // The full URL IS rendered as text (for admin visibility).
    expect(
      screen.getByText('https://catalogs.example.com/addons.yaml'),
    ).toBeInTheDocument()

    // …but it is never wrapped in an anchor element. Auth tokens may be
    // embedded in URL paths; linkifying would leak them via Referer.
    expect(
      container.querySelector('a[href*="catalogs.example.com"]'),
    ).toBeNull()
  })
})

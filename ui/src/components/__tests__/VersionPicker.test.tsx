import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { VersionPicker } from '@/components/VersionPicker'
import type { CatalogVersionsResponse } from '@/services/models'

// v4 wave 1 Story 3.4 — "last checked" is the honest-staleness signal the
// freshness scheduler feeds through cached_at. These tests cover the new
// VersionPicker behavior only; the picker's pre-existing pill/datalist
// behavior is exercised indirectly through its consumers
// (MarketplaceAddonDetail.test.tsx, AddonCatalog.test.tsx).

function baseProps(overrides: Partial<CatalogVersionsResponse> = {}) {
  const versionsResp: CatalogVersionsResponse = {
    addon: 'cert-manager',
    chart: 'cert-manager',
    repo: 'https://charts.jetstack.io',
    versions: [{ version: '1.2.0', prerelease: false }],
    latest_stable: '1.2.0',
    cached_at: new Date(Date.now() - 5 * 60 * 1000).toISOString(),
    ...overrides,
  }
  return versionsResp
}

describe('VersionPicker — last checked', () => {
  it('shows a relative "Last checked" line when cached_at is present', () => {
    render(
      <VersionPicker
        inputId="v"
        value=""
        onChange={vi.fn()}
        versionsResp={baseProps()}
        showPrereleases={false}
        onShowPrereleasesChange={vi.fn()}
      />,
    )
    expect(screen.getByText(/Last checked: 5m ago/i)).toBeInTheDocument()
  })

  it('still shows "Last checked" when version_check_unknown is true (stale-but-dated, never hidden)', () => {
    const resp = baseProps({
      versions: [],
      latest_stable: undefined,
      version_check_unknown: true,
    })
    render(
      <VersionPicker
        inputId="v"
        value=""
        onChange={vi.fn()}
        versionsResp={resp}
        showPrereleases={false}
        onShowPrereleasesChange={vi.fn()}
      />,
    )
    expect(screen.getByText(/Last checked:/i)).toBeInTheDocument()
  })

  it('renders no "Last checked" line when there is no versionsResp yet', () => {
    render(
      <VersionPicker
        inputId="v"
        value=""
        onChange={vi.fn()}
        versionsResp={null}
        showPrereleases={false}
        onShowPrereleasesChange={vi.fn()}
      />,
    )
    expect(screen.queryByText(/Last checked:/i)).not.toBeInTheDocument()
  })
})

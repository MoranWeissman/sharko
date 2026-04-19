import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { MarketplaceConfigureModal } from '@/components/MarketplaceConfigureModal'
import type { CatalogEntry } from '@/services/models'

// V121-5 wiring tests for the Configure modal: duplicate guard, Submit ->
// addAddon('source: marketplace'), success PR banner, and inline duplicate
// error from the server's 409 fallback. The MarketplaceTab.test covers the
// generic open/close path; this file focuses on the orchestration tail.

const entry: CatalogEntry = {
  name: 'cert-manager',
  description: 'TLS lifecycle manager.',
  chart: 'cert-manager',
  repo: 'https://charts.jetstack.io',
  default_namespace: 'cert-manager',
  default_sync_wave: 1,
  maintainers: ['jetstack'],
  license: 'Apache-2.0',
  category: 'security',
  curated_by: ['cncf-graduated'],
  security_score: 8.2,
  security_tier: 'Strong',
}

const versionsResp = {
  addon: 'cert-manager',
  chart: 'cert-manager',
  repo: 'https://charts.jetstack.io',
  versions: [
    { version: '1.20.0', prerelease: false },
    { version: '1.19.0', prerelease: false },
  ],
  latest_stable: '1.20.0',
  cached_at: '2026-04-17T00:00:00Z',
}

const getAddonCatalogMock = vi.fn()
const getMeMock = vi.fn()
const listVersionsMock = vi.fn()
const addAddonMock = vi.fn()
const showToastMock = vi.fn()

vi.mock('@/services/api', () => ({
  api: {
    listCuratedCatalogVersions: (...args: unknown[]) => listVersionsMock(...args),
    getAddonCatalog: () => getAddonCatalogMock(),
    getMe: () => getMeMock(),
  },
  addAddon: (...args: unknown[]) => addAddonMock(...args),
  isAddonAlreadyExistsError: (e: unknown) =>
    typeof e === 'object' && e !== null && (e as { code?: string }).code === 'addon_already_exists',
}))

vi.mock('@/components/ToastNotification', () => ({
  showToast: (...args: unknown[]) => showToastMock(...args),
}))

function renderModal() {
  return render(
    <MemoryRouter>
      <MarketplaceConfigureModal entry={entry} open onOpenChange={() => {}} />
    </MemoryRouter>,
  )
}

describe('MarketplaceConfigureModal V121-5', () => {
  beforeEach(() => {
    getAddonCatalogMock.mockReset()
    getMeMock.mockReset()
    listVersionsMock.mockReset()
    addAddonMock.mockReset()
    showToastMock.mockReset()

    listVersionsMock.mockResolvedValue(versionsResp)
    getMeMock.mockResolvedValue({ username: 'a', role: 'admin', has_github_token: true })
  })

  it('5.1: blocks Submit and shows inline message when name already in catalog', async () => {
    getAddonCatalogMock.mockResolvedValue({
      addons: [{ addon_name: 'cert-manager', chart: 'cert-manager', repo_url: '', version: '', total_clusters: 0, enabled_clusters: 0, healthy_applications: 0 }],
    })
    renderModal()
    await waitFor(() => expect(screen.getByLabelText(/Display name/i)).toHaveValue('cert-manager'))
    // Pre-flight should fire and the duplicate banner should appear.
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(/already in the catalog/i),
    )
    const submit = screen.getByRole('button', { name: /Submit & open PR/i })
    expect(submit).toBeDisabled()

    // Renaming to a free name re-enables Submit.
    fireEvent.change(screen.getByLabelText(/Display name/i), {
      target: { value: 'cert-manager-eu' },
    })
    await waitFor(() => expect(submit).not.toBeDisabled())
    expect(screen.queryByText(/already in the catalog/i)).not.toBeInTheDocument()
    expect(addAddonMock).not.toHaveBeenCalled()
  })

  it('5.2: Submit POSTs source=marketplace and shows merged toast when auto-merge fired', async () => {
    getAddonCatalogMock.mockResolvedValue({ addons: [] })
    addAddonMock.mockResolvedValue({
      pr_url: 'https://github.com/x/y/pull/42',
      pr_id: 42,
      merged: true,
    })
    renderModal()
    await waitFor(() => expect(screen.getByLabelText(/Display name/i)).toHaveValue('cert-manager'))
    // Wait for versions to populate so the form is valid.
    await waitFor(() =>
      expect(screen.getByLabelText(/Chart version/i)).toHaveValue('1.20.0'),
    )
    fireEvent.click(screen.getByRole('button', { name: /Submit & open PR/i }))
    await waitFor(() => expect(addAddonMock).toHaveBeenCalledTimes(1))
    expect(addAddonMock).toHaveBeenCalledWith(
      expect.objectContaining({
        name: 'cert-manager',
        chart: 'cert-manager',
        repo_url: 'https://charts.jetstack.io',
        version: '1.20.0',
        namespace: 'cert-manager',
        sync_wave: 1,
        source: 'marketplace',
      }),
    )
    await waitFor(() =>
      expect(showToastMock).toHaveBeenCalledWith('PR #42 merged →', 'success'),
    )
    // Success banner with PR link is in the document.
    expect(screen.getByText(/PR merged/i)).toBeInTheDocument()
    expect(
      screen.getByRole('link', { name: /View PR #42 on GitHub/i }),
    ).toHaveAttribute('href', 'https://github.com/x/y/pull/42')
  })

  it('5.3: shows opened toast (not merged) when auto-merge did not fire', async () => {
    getAddonCatalogMock.mockResolvedValue({ addons: [] })
    addAddonMock.mockResolvedValue({
      pr_url: 'https://github.com/x/y/pull/43',
      pr_id: 43,
      merged: false,
    })
    renderModal()
    await waitFor(() =>
      expect(screen.getByLabelText(/Chart version/i)).toHaveValue('1.20.0'),
    )
    fireEvent.click(screen.getByRole('button', { name: /Submit & open PR/i }))
    await waitFor(() =>
      expect(showToastMock).toHaveBeenCalledWith('PR #43 opened →', 'success'),
    )
  })

  it('5.1 server fallback: renders inline duplicate error from 409 response', async () => {
    getAddonCatalogMock.mockResolvedValue({ addons: [] }) // pre-flight finds nothing
    const dupErr = Object.assign(new Error('cert-manager is already in your catalog'), {
      code: 'addon_already_exists',
      status: 409,
      addon: 'cert-manager',
      existingUrl: '/addons/cert-manager',
    })
    addAddonMock.mockRejectedValue(dupErr)
    renderModal()
    await waitFor(() =>
      expect(screen.getByLabelText(/Chart version/i)).toHaveValue('1.20.0'),
    )
    fireEvent.click(screen.getByRole('button', { name: /Submit & open PR/i }))
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(/already in the catalog/i),
    )
    // 409 fallback should not raise a generic toast.
    expect(showToastMock).not.toHaveBeenCalled()
  })

  it('renders proactive AttributionNudge when user has no personal PAT', async () => {
    getAddonCatalogMock.mockResolvedValue({ addons: [] })
    getMeMock.mockResolvedValue({ username: 'a', role: 'admin', has_github_token: false })
    renderModal()
    await waitFor(() => expect(getMeMock).toHaveBeenCalled())
    await waitFor(() =>
      expect(screen.getByText(/attributed to the Sharko service account/i)).toBeInTheDocument(),
    )
  })
})

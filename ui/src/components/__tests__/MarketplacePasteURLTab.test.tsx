import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { MarketplacePasteURLTab } from '@/components/MarketplacePasteURLTab'

// Mocks for the API functions the tab + modal call. The Configure modal also
// fetches /addons/catalog and /me on open for V121-5.1's duplicate guard +
// AttributionNudge — return empty defaults so opening the modal doesn't error.
const validateMock = vi.fn()
const getCatalogMock = vi.fn().mockResolvedValue({ addons: [] })
const getMeMock = vi
  .fn()
  .mockResolvedValue({ username: 'tester', role: 'admin', has_github_token: true })
const listVersionsMock = vi.fn()
const addAddonMock = vi.fn()

vi.mock('@/services/api', () => ({
  api: {
    validateCatalogChart: (...args: unknown[]) => validateMock(...args),
    getAddonCatalog: () => getCatalogMock(),
    getMe: () => getMeMock(),
    listCuratedCatalogVersions: (...args: unknown[]) => listVersionsMock(...args),
  },
  addAddon: (...args: unknown[]) => addAddonMock(...args),
  isAddonAlreadyExistsError: (e: unknown) =>
    typeof e === 'object' && e !== null && (e as { code?: string }).code === 'addon_already_exists',
}))

function renderTab() {
  return render(
    <MemoryRouter>
      <MarketplacePasteURLTab />
    </MemoryRouter>,
  )
}

describe('MarketplacePasteURLTab', () => {
  beforeEach(() => {
    validateMock.mockReset()
    getCatalogMock.mockClear()
    getMeMock.mockClear()
    listVersionsMock.mockReset()
    addAddonMock.mockReset()
  })

  it('disables Validate until repo + chart are filled', () => {
    renderTab()
    const validate = screen.getByRole('button', { name: /^Validate$/i })
    expect(validate).toBeDisabled()

    fireEvent.change(screen.getByLabelText(/Chart repo URL/i), {
      target: { value: 'https://charts.jetstack.io' },
    })
    expect(validate).toBeDisabled()

    fireEvent.change(screen.getByLabelText(/^Chart name/i), {
      target: { value: 'cert-manager' },
    })
    expect(validate).toBeEnabled()
  })

  it('shows success state with version count on valid response', async () => {
    validateMock.mockResolvedValueOnce({
      valid: true,
      chart: 'cert-manager',
      repo: 'https://charts.jetstack.io',
      versions: [
        { version: '1.20.2', prerelease: false },
        { version: '1.20.1', prerelease: false },
      ],
      latest_stable: '1.20.2',
      cached_at: '2026-04-17T00:00:00Z',
      description: 'TLS lifecycle manager.',
    })

    renderTab()
    fireEvent.change(screen.getByLabelText(/Chart repo URL/i), {
      target: { value: 'https://charts.jetstack.io' },
    })
    fireEvent.change(screen.getByLabelText(/^Chart name/i), {
      target: { value: 'cert-manager' },
    })
    fireEvent.click(screen.getByRole('button', { name: /^Validate$/i }))

    await waitFor(() =>
      expect(validateMock).toHaveBeenCalledWith(
        'https://charts.jetstack.io',
        'cert-manager',
      ),
    )
    await waitFor(() =>
      expect(screen.getByText(/Found 2 versions/i)).toBeInTheDocument(),
    )
    expect(screen.getByText(/latest stable 1\.20\.2/i)).toBeInTheDocument()
    // Configure button appears on success.
    expect(screen.getByRole('button', { name: /^Configure$/i })).toBeInTheDocument()
  })

  it('renders structured failure with remediation hint', async () => {
    validateMock.mockResolvedValueOnce({
      valid: false,
      chart: 'ghost',
      repo: 'https://charts.jetstack.io',
      error_code: 'chart_not_found',
      message: 'chart ghost is not present in this repository\u2019s index.yaml',
    })

    renderTab()
    fireEvent.change(screen.getByLabelText(/Chart repo URL/i), {
      target: { value: 'https://charts.jetstack.io' },
    })
    fireEvent.change(screen.getByLabelText(/^Chart name/i), {
      target: { value: 'ghost' },
    })
    fireEvent.click(screen.getByRole('button', { name: /^Validate$/i }))

    await waitFor(() => {
      expect(
        screen.getByText(/Chart not found in this repo/i),
      ).toBeInTheDocument()
    })
    // Remediation hint references index.yaml.
    expect(screen.getAllByText(/index\.yaml/i).length).toBeGreaterThan(0)
    // Configure button is NOT shown on failure.
    expect(
      screen.queryByRole('button', { name: /^Configure$/i }),
    ).not.toBeInTheDocument()
  })

  it('opens Configure modal pre-filled when Configure is clicked', async () => {
    validateMock.mockResolvedValueOnce({
      valid: true,
      chart: 'cert-manager',
      repo: 'https://charts.jetstack.io',
      versions: [{ version: '1.20.2', prerelease: false }],
      latest_stable: '1.20.2',
      cached_at: '2026-04-17T00:00:00Z',
    })

    renderTab()
    fireEvent.change(screen.getByLabelText(/Chart repo URL/i), {
      target: { value: 'https://charts.jetstack.io' },
    })
    fireEvent.change(screen.getByLabelText(/^Chart name/i), {
      target: { value: 'cert-manager' },
    })
    fireEvent.click(screen.getByRole('button', { name: /^Validate$/i }))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /^Configure$/i })).toBeInTheDocument(),
    )

    fireEvent.click(screen.getByRole('button', { name: /^Configure$/i }))
    await waitFor(() => expect(screen.getByRole('dialog')).toBeInTheDocument())

    // Pre-fill expectations: name and namespace use the chart name.
    expect(screen.getByLabelText(/Display name/i)).toHaveValue('cert-manager')
    expect(screen.getByLabelText(/Namespace/i)).toHaveValue('cert-manager')

    // skipVersionFetch means listCuratedCatalogVersions must NOT be called —
    // that endpoint would 404 for non-curated charts.
    expect(listVersionsMock).not.toHaveBeenCalled()
  })

  it('resets validation state when inputs change after a result', async () => {
    validateMock.mockResolvedValueOnce({
      valid: true,
      chart: 'cert-manager',
      repo: 'https://charts.jetstack.io',
      versions: [{ version: '1.20.2', prerelease: false }],
      latest_stable: '1.20.2',
      cached_at: '2026-04-17T00:00:00Z',
    })

    renderTab()
    fireEvent.change(screen.getByLabelText(/Chart repo URL/i), {
      target: { value: 'https://charts.jetstack.io' },
    })
    fireEvent.change(screen.getByLabelText(/^Chart name/i), {
      target: { value: 'cert-manager' },
    })
    fireEvent.click(screen.getByRole('button', { name: /^Validate$/i }))
    await waitFor(() =>
      expect(screen.getByText(/Found 1 version/i)).toBeInTheDocument(),
    )

    // Change the chart name → success banner clears.
    fireEvent.change(screen.getByLabelText(/^Chart name/i), {
      target: { value: 'cert-manager-renamed' },
    })
    expect(screen.queryByText(/Found 1 version/i)).not.toBeInTheDocument()
  })
})

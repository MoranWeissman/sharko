import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { AddonDetail } from '@/views/AddonDetail'
import { AuthContext } from '@/hooks/useAuth'

// Capture navigation so we can prove remove-addon NO LONGER blindly navigates
// away (V2-cleanup-24, defect 2.1) when the PR is merely opened for review.
const mockNavigate = vi.fn()
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom')
  return { ...actual, useNavigate: () => mockNavigate }
})

const mockRemoveAddon = vi.fn()

vi.mock('@/services/api', () => ({
  getAddonPRs: vi.fn().mockResolvedValue({ prs: [] }),
  upgradeAddon: vi.fn().mockResolvedValue({ pr_url: '' }),
  configureAddon: vi.fn().mockResolvedValue({}),
  removeAddon: (...args: unknown[]) => mockRemoveAddon(...args),
  api: {
    getConnections: vi.fn().mockResolvedValue({ connections: [], active_connection: '' }),
    getAddonValues: vi.fn().mockRejectedValue(new Error('not found')),
    getMe: vi.fn().mockResolvedValue({ username: 'tester', role: 'admin', has_github_token: true }),
    getAddonValuesSchema: vi.fn().mockResolvedValue({ addon_name: 'ingress-nginx', current_values: '', schema: null }),
    getAIConfig: vi.fn().mockResolvedValue({ current_provider: 'none', available_providers: [], annotate_on_seed: false }),
    getAddonDetail: vi.fn().mockResolvedValue({
      addon: {
        addon_name: 'ingress-nginx',
        chart: 'ingress-nginx',
        repo_url: 'https://kubernetes.github.io/ingress-nginx',
        namespace: 'ingress-nginx',
        version: '4.8.0',
        total_clusters: 1,
        enabled_clusters: 1,
        healthy_applications: 1,
        degraded_applications: 0,
        missing_applications: 0,
        applications: [],
      },
    }),
  },
}))

const adminCtx = {
  token: 't',
  username: 'tester',
  role: 'admin',
  login: vi.fn(),
  logout: vi.fn(),
  isAuthenticated: true,
  isAdmin: true,
  loading: false,
  error: null,
}

function renderDetail() {
  return render(
    <AuthContext.Provider value={adminCtx}>
      <MemoryRouter initialEntries={['/addons/ingress-nginx']}>
        <Routes>
          <Route path="/addons/:name" element={<AddonDetail />} />
        </Routes>
      </MemoryRouter>
    </AuthContext.Provider>,
  )
}

async function openRemoveModalAndConfirm() {
  // The header Remove button (admin-gated) opens the type-to-confirm modal.
  const removeButtons = await screen.findAllByRole('button', { name: /^Remove$/i })
  fireEvent.click(removeButtons[0])
  // Type the addon name to satisfy the type-to-confirm gate.
  const input = await screen.findByPlaceholderText('ingress-nginx')
  fireEvent.change(input, { target: { value: 'ingress-nginx' } })
  // The modal footer's Remove button is now enabled.
  const confirmButtons = screen.getAllByRole('button', { name: /^Remove$/i })
  fireEvent.click(confirmButtons[confirmButtons.length - 1])
}

describe('AddonDetail — remove surfaces its PR (V2-cleanup-24 defect 2.1)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  // V3-TX-A3 — Preview on every PR-opening operation. Surface 4: Remove Addon.
  it('Preview changes calls removeAddon(dry-run) and renders the deletions without removing', async () => {
    mockRemoveAddon.mockImplementation((_name: string, dryRun?: boolean) => {
      if (dryRun) {
        return Promise.resolve({
          pr_title: 'Remove addon ingress-nginx',
          files_to_write: [
            { path: 'configuration/addons-catalog.yaml', action: 'update' },
            { path: 'configuration/addons-global-values/ingress-nginx.yaml', action: 'delete' },
          ],
        })
      }
      return Promise.resolve({ pr_url: 'https://github.com/example/repo/pull/99', pr_id: 99, merged: false })
    })

    renderDetail()
    await waitFor(() =>
      expect(screen.getAllByText('ingress-nginx').length).toBeGreaterThanOrEqual(1),
    )

    // Open the remove modal (header Remove button).
    const removeButtons = await screen.findAllByRole('button', { name: /^Remove$/i })
    fireEvent.click(removeButtons[0])

    // Click Preview changes inside the modal.
    const previewBtn = await screen.findByRole('button', { name: /preview changes/i })
    fireEvent.click(previewBtn)

    await waitFor(() => expect(mockRemoveAddon).toHaveBeenCalledWith('ingress-nginx', true))
    // The deletion (red `-`) path is the whole point of a destructive preview.
    await waitFor(() =>
      expect(
        screen.getByText('configuration/addons-global-values/ingress-nginx.yaml'),
      ).toBeInTheDocument(),
    )
    expect(screen.getByText('Remove addon ingress-nginx')).toBeInTheDocument()
    // Preview is a courtesy — the real (non-dry-run) removal was NOT called.
    expect(mockRemoveAddon).not.toHaveBeenCalledWith('ingress-nginx')
    expect(mockNavigate).not.toHaveBeenCalledWith('/addons')
  })

  it('shows the open-PR result and does NOT navigate away', async () => {
    mockRemoveAddon.mockResolvedValue({
      pr_url: 'https://github.com/example/repo/pull/77',
      pr_id: 77,
      merged: false,
    })

    renderDetail()
    await waitFor(() =>
      expect(screen.getAllByText('ingress-nginx').length).toBeGreaterThanOrEqual(1),
    )

    await openRemoveModalAndConfirm()

    // The removal PR is surfaced as a clickable link — the response is no
    // longer thrown away.
    const link = await screen.findByRole('link', { name: /View PR #77 on GitHub/i })
    expect(link).toHaveAttribute('href', 'https://github.com/example/repo/pull/77')
    // And we stayed on the page (the old code navigated to /addons blindly).
    expect(mockNavigate).not.toHaveBeenCalledWith('/addons')
  })

  it('navigates away only when the removal PR is already merged', async () => {
    mockRemoveAddon.mockResolvedValue({
      pr_url: 'https://github.com/example/repo/pull/78',
      pr_id: 78,
      merged: true,
    })

    renderDetail()
    await waitFor(() =>
      expect(screen.getAllByText('ingress-nginx').length).toBeGreaterThanOrEqual(1),
    )

    await openRemoveModalAndConfirm()

    await waitFor(() => expect(mockNavigate).toHaveBeenCalledWith('/addons'))
  })
})

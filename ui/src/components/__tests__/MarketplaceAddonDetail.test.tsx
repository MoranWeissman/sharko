import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { AuthProvider } from '@/hooks/useAuth'
import { MarketplaceAddonDetail } from '@/components/MarketplaceAddonDetail'

// V2-cleanup-14 — the Marketplace "Add addon to catalog" flow gains:
//   - an admin-gated auto-merge toggle that sends auto_merge
//   - a Preview step (dry_run) that renders the files it would write
//   - a clickable PR link
//   - post-submit navigation: merged → /addons/<name>; opened →
//     /dashboard?prs_state=pending
//
// These tests assert each surface. The component fires several read-only
// api calls on mount (metadata, README, versions, catalog, getMe, sources);
// they're stubbed with minimal happy-path shapes so the action panel renders.

const mockNavigate = vi.fn()
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom')
  return { ...actual, useNavigate: () => mockNavigate }
})

const mockAddAddon = vi.fn()
const mockShowToast = vi.fn()

vi.mock('@/components/ToastNotification', () => ({
  showToast: (...args: unknown[]) => mockShowToast(...args),
}))

vi.mock('@/services/api', () => ({
  addAddon: (...args: unknown[]) => mockAddAddon(...args),
  isAddonAlreadyExistsError: () => false,
  api: {
    getCuratedCatalogEntry: vi.fn().mockResolvedValue({
      name: 'prometheus',
      description: 'Monitoring',
      chart: 'kube-prometheus-stack',
      repo: 'https://prometheus-community.github.io/helm-charts',
      default_namespace: 'monitoring',
      default_sync_wave: 0,
      maintainers: [],
      license: 'Apache-2.0',
      category: 'observability',
      curated_by: [],
    }),
    getCuratedCatalogReadme: vi.fn().mockResolvedValue({
      readme: '',
      source: 'artifacthub',
    }),
    listCuratedCatalogVersions: vi.fn().mockResolvedValue({
      addon: 'prometheus',
      chart: 'kube-prometheus-stack',
      repo: 'https://prometheus-community.github.io/helm-charts',
      versions: [{ version: '45.0.0' }],
      latest_stable: '45.0.0',
      cached_at: new Date().toISOString(),
    }),
    getAddonCatalog: vi.fn().mockResolvedValue({ addons: [] }),
    getMe: vi.fn().mockResolvedValue({ has_github_token: true }),
    listCatalogSources: vi.fn().mockResolvedValue([]),
  },
}))

function renderDetail() {
  sessionStorage.setItem('sharko-auth-token', 'test-token')
  sessionStorage.setItem('sharko-auth-user', 'tester')
  sessionStorage.setItem('sharko-auth-role', 'admin')
  return render(
    <MemoryRouter>
      <AuthProvider>
        <MarketplaceAddonDetail
          addonName="prometheus"
          source="curated"
          onBack={() => {}}
        />
      </AuthProvider>
    </MemoryRouter>,
  )
}

async function waitForActionPanel() {
  // Wait for the "Add to catalog" button to be ENABLED, not merely present.
  // The submit/preview buttons stay disabled until the async version load
  // (listCuratedCatalogVersions) resolves and the form becomes valid;
  // clicking before then is a no-op and makes the test flaky.
  await waitFor(() => {
    expect(
      screen.getByRole('button', { name: /add to catalog/i }),
    ).toBeEnabled()
  })
}

describe('MarketplaceAddonDetail — V2-cleanup-14 add-addon flow', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    sessionStorage.clear()
  })

  // V2-cleanup-40: per-flow auto-merge toggle removed. Global GitOps setting governs.
  it('does NOT render the auto-merge toggle and does NOT send auto_merge', async () => {
    mockAddAddon.mockResolvedValue({ pr_id: 8, pr_url: 'https://gh/pr/8', merged: false })
    renderDetail()
    await waitForActionPanel()

    // Toggle must be gone.
    expect(screen.queryByLabelText(/merge pr automatically/i)).not.toBeInTheDocument()

    // Shows the global-setting hint text.
    expect(screen.getByText(/global GitOps setting/i)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /add to catalog/i }))

    await waitFor(() => expect(mockAddAddon).toHaveBeenCalled())
    const arg = mockAddAddon.mock.calls[0][0]
    // auto_merge must NOT be present on the call.
    expect(arg.auto_merge).toBeUndefined()
    expect(arg.dry_run).toBe(false)
  })

  it('previews the files that would be written (dry-run) without opening a PR', async () => {
    mockAddAddon.mockResolvedValue({
      dry_run: {
        pr_title: 'sharko: add addon prometheus',
        effective_addons: ['prometheus'],
        files_to_write: [
          { path: 'configuration/addons-catalog.yaml', action: 'update' },
          { path: 'configuration/addons-global-values/prometheus.yaml', action: 'create' },
        ],
        secrets_to_create: [],
      },
    })
    renderDetail()
    await waitForActionPanel()

    fireEvent.click(screen.getByRole('button', { name: /preview/i }))

    await waitFor(() =>
      expect(
        screen.getByText('configuration/addons-global-values/prometheus.yaml'),
      ).toBeInTheDocument(),
    )
    expect(
      screen.getByText('configuration/addons-catalog.yaml'),
    ).toBeInTheDocument()
    // The preview call set dry_run:true.
    expect(mockAddAddon.mock.calls[0][0].dry_run).toBe(true)
  })

  it('navigates to the addon page and toasts when the PR auto-merged', async () => {
    mockAddAddon.mockResolvedValue({ pr_id: 9, pr_url: 'https://gh/pr/9', merged: true })
    renderDetail()
    await waitForActionPanel()

    fireEvent.click(screen.getByRole('button', { name: /add to catalog/i }))

    await waitFor(() =>
      expect(mockNavigate).toHaveBeenCalledWith('/addons/prometheus'),
    )
    expect(mockShowToast).toHaveBeenCalledWith(
      expect.stringContaining('added to your catalog'),
      'success',
    )
  })

  it('navigates to the pending-PR dashboard when a PR is opened for review', async () => {
    mockAddAddon.mockResolvedValue({ pr_id: 10, pr_url: 'https://gh/pr/10', merged: false })
    renderDetail()
    await waitForActionPanel()

    fireEvent.click(screen.getByRole('button', { name: /add to catalog/i }))

    await waitFor(() =>
      expect(mockNavigate).toHaveBeenCalledWith('/dashboard?prs_state=pending'),
    )
    expect(mockShowToast).toHaveBeenCalledWith(
      expect.stringContaining('PR #10 opened'),
      'success',
    )
  })
})

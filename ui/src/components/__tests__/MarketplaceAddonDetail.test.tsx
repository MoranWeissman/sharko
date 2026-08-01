import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { AuthProvider } from '@/hooks/useAuth'
import { MarketplaceAddonDetail } from '@/components/MarketplaceAddonDetail'
import { api } from '@/services/api'

// V2-cleanup-14 — the Marketplace "Add addon to catalog" flow gains:
//   - an admin-gated auto-merge toggle that sends auto_merge
//   - a Preview step (dry_run) that renders the files it would write
//   - a clickable PR link
//   - post-submit navigation via explicit terminal-state buttons
//     (V2-cleanup-66.1): merged → "View addon" (/addons/<name>); opened →
//     "Track on Dashboard" (/dashboard?prs_state=pending) — no automatic jump
//

// These tests assert each surface. The component fires several read-only
// api calls on mount (metadata, README, versions, catalog, getMe, sources);
// they're stubbed with minimal happy-path shapes so the action panel renders.

const mockNavigate = vi.fn()
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom')
  return { ...actual, useNavigate: () => mockNavigate }
})

const mockAddToCatalog = vi.fn()
const mockShowToast = vi.fn()
// v4 walk-findings W2, item 5: the pending-add-PR check uses the standalone
// fetchTrackedPRs export, not the `api` object. Defaults to "no open PRs"
// so the existing happy-path tests are unaffected; per-test overrides use
// vi.mocked(...) where needed.
const mockFetchTrackedPRs = vi.fn().mockResolvedValue({ prs: [] })

vi.mock('@/components/ToastNotification', () => ({
  showToast: (...args: unknown[]) => mockShowToast(...args),
}))

vi.mock('@/services/api', () => ({
  fetchTrackedPRs: (...args: unknown[]) => mockFetchTrackedPRs(...args),
  api: {
    addToCatalog: (...args: unknown[]) => mockAddToCatalog(...args),
    // v4 walk-findings W2, item 4: the optional "also enable on a cluster"
    // selector fetches managed clusters on mount (defensive — component
    // guards with typeof-check, but wiring it keeps the fixture honest).
    getClusters: vi.fn().mockResolvedValue({ clusters: [] }),
    getCuratedCatalogEntry: vi.fn().mockResolvedValue({
      name: 'prometheus',
      description: 'Monitoring',
      chart: 'kube-prometheus-stack',
      repo: 'https://prometheus-community.github.io/helm-charts',
      default_namespace: 'monitoring',
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
  //
  // v4 wave 2.5 review fix round, B-3 — this button now posts to
  // POST /api/v1/catalog/addons (the legacy POST /addons 409s on a v4
  // repo). The request is the AddToCatalogRequest shape: one `addons`
  // entry with `from_marketplace: true` (name unchanged from the curated
  // entry), not the old flat addAddon body.
  it('does NOT render the auto-merge toggle and does NOT send auto_merge', async () => {
    mockAddToCatalog.mockResolvedValue({ added: ['prometheus'], enabled: [], pr_id: 8, pr_url: 'https://gh/pr/8', merged: false })
    renderDetail()
    await waitForActionPanel()

    // Toggle must be gone.
    expect(screen.queryByLabelText(/merge pr automatically/i)).not.toBeInTheDocument()

    // Shows the global-setting hint text.
    expect(screen.getByText(/global GitOps setting/i)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /add to catalog/i }))

    await waitFor(() => expect(mockAddToCatalog).toHaveBeenCalled())
    const arg = mockAddToCatalog.mock.calls[0][0]
    // auto_merge must NOT be present on the call.
    expect(arg.auto_merge).toBeUndefined()
    expect(arg.dry_run).toBe(false)
    expect(arg.addons).toEqual([
      expect.objectContaining({ name: 'prometheus', from_marketplace: true }),
    ])
  })

  it('previews the files that would be written (dry-run) without opening a PR', async () => {
    mockAddToCatalog.mockResolvedValue({
      added: [],
      enabled: [],
      dry_run: {
        pr_title: 'sharko: add prometheus to catalog',
        effective_addons: ['prometheus'],
        files_to_write: [
          { path: 'catalog.yaml', action: 'update' },
          { path: 'values/global/prometheus.yaml', action: 'create' },
        ],
        secrets_to_create: [],
      },
    })
    renderDetail()
    await waitForActionPanel()

    fireEvent.click(screen.getByRole('button', { name: /preview/i }))

    await waitFor(() =>
      expect(
        screen.getByText('values/global/prometheus.yaml'),
      ).toBeInTheDocument(),
    )
    expect(screen.getAllByText('catalog.yaml').length).toBeGreaterThan(0)
    // The preview call set dry_run:true.
    expect(mockAddToCatalog.mock.calls[0][0].dry_run).toBe(true)
  })

  // V2-cleanup-66.1 — a merged PR used to navigate away the instant the POST
  // resolved, so the PR lifecycle window never got any screen time. Now the
  // page STAYS PUT showing the terminal "Merged" state, and the user leaves
  // via an explicit "View addon" button.
  it('keeps the page open on merge, toasts, and only navigates when "View addon" is clicked', async () => {
    mockAddToCatalog.mockResolvedValue({ added: ['prometheus'], enabled: [], pr_id: 9, pr_url: 'https://gh/pr/9', merged: true })
    renderDetail()
    await waitForActionPanel()

    fireEvent.click(screen.getByRole('button', { name: /add to catalog/i }))

    const viewAddonBtn = await screen.findByRole('button', { name: /view addon/i })
    expect(mockShowToast).toHaveBeenCalledWith(
      expect.stringContaining('added to your catalog'),
      'success',
    )
    // No automatic navigation happened yet.
    expect(mockNavigate).not.toHaveBeenCalled()

    fireEvent.click(viewAddonBtn)
    expect(mockNavigate).toHaveBeenCalledWith('/addons/prometheus')
  })

  // Auto-merge OFF: the PR is open for review, so the addon isn't really in
  // the catalog yet. The page stays put; the user opts into the Dashboard's
  // pending-PR view via an explicit button instead of an automatic jump.
  it('keeps the page open when a PR is opened for review, and only navigates when "Track on Dashboard" is clicked', async () => {
    mockAddToCatalog.mockResolvedValue({ added: ['prometheus'], enabled: [], pr_id: 10, pr_url: 'https://gh/pr/10', merged: false })
    renderDetail()
    await waitForActionPanel()

    fireEvent.click(screen.getByRole('button', { name: /add to catalog/i }))

    const trackBtn = await screen.findByRole('button', { name: /track on dashboard/i })
    expect(mockShowToast).toHaveBeenCalledWith(
      expect.stringContaining('PR #10 opened'),
      'success',
    )
    expect(mockNavigate).not.toHaveBeenCalled()

    fireEvent.click(trackBtn)
    expect(mockNavigate).toHaveBeenCalledWith('/dashboard?prs_state=pending')
  })

  // v4 wave 2.5 review fix round, B-1/B-3 — the version-resolving contract:
  // sending no version is now valid when from_marketplace is true, and the
  // wizard/combo NO-version payloads work. This screen still collects a
  // version (pre-filled from the version picker), but a 422 with code
  // version_required must surface as a plain message, not a raw 502.
  it('surfaces a plain version_required message instead of a raw gateway error', async () => {
    // The real error is a CatalogAddError (carries .code), but the catch
    // path here only reads .message — a plain Error exercises the same
    // branch without needing the class from the (mocked-away) api module.
    mockAddToCatalog.mockRejectedValue(
      new Error('pick a version — Sharko has no version data for prometheus'),
    )
    renderDetail()
    await waitForActionPanel()

    fireEvent.click(screen.getByRole('button', { name: /add to catalog/i }))

    await waitFor(() => {
      expect(
        screen.getByText(/no version data for prometheus/i),
      ).toBeInTheDocument()
    })
  })

  // V2-cleanup-70.1 — the embedded-catalog "Internal" source badge showed the
  // same meaningless word on every built-in addon (no third-party catalog
  // configured), so it's hidden on this browsing surface. The mocked catalog
  // entry above has no `source` field, i.e. embedded.
  it('does not render an "Internal" source badge for a built-in catalog entry', async () => {
    renderDetail()
    await waitForActionPanel()

    expect(screen.queryByText('Internal')).not.toBeInTheDocument()
  })

  // v4 wave 1 Story 3.1 — "extended entries": required_values, secrets, and
  // quirks render in a "What you need to know" section when the catalog
  // entry carries them.
  it('renders required values, secrets, and quirks when the entry carries them', async () => {
    vi.mocked(api.getCuratedCatalogEntry).mockResolvedValueOnce({
      name: 'prometheus',
      description: 'Monitoring',
      chart: 'kube-prometheus-stack',
      repo: 'https://prometheus-community.github.io/helm-charts',
      default_namespace: 'monitoring',
      maintainers: [],
      license: 'Apache-2.0',
      category: 'observability',
      curated_by: [],
      required_values: [
        { key: 'grafana.adminPassword', description: 'Set explicitly or a random one is generated.' },
      ],
      secrets: [
        { name: 'Alertmanager receiver credentials', description: 'Only needed if you wire alerting.' },
      ],
      quirks: ['Ships its own CRDs — a second copy elsewhere collides.'],
    })

    renderDetail()
    await waitForActionPanel()

    expect(await screen.findByText('What you need to know')).toBeInTheDocument()
    expect(screen.getByText('grafana.adminPassword')).toBeInTheDocument()
    expect(screen.getByText(/Alertmanager receiver credentials/)).toBeInTheDocument()
    expect(
      screen.getByText('Ships its own CRDs — a second copy elsewhere collides.'),
    ).toBeInTheDocument()
  })

  // Baseline for the section above: the default mocked entry (no
  // required_values/secrets/quirks) must NOT render the section at all —
  // additive UI, invisible when there's nothing to show.
  it('does not render "What you need to know" when the entry has no extended fields', async () => {
    renderDetail()
    await waitForActionPanel()

    expect(screen.queryByText('What you need to know')).not.toBeInTheDocument()
  })
})

// v4 walk-findings W2, item 3: Preview/Add used to go quietly disabled
// forever whenever the versions fetch failed — formValid requires a
// version, and nothing near the buttons said why it was empty.
describe('MarketplaceAddonDetail — disabled-reason banner (v4 walk-findings W2, item 3)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    sessionStorage.clear()
  })

  it('names the reason and offers a Retry when the version list fails to load', async () => {
    vi.mocked(api.listCuratedCatalogVersions).mockRejectedValueOnce(
      new Error('network error'),
    )
    renderDetail()

    // The buttons stay disabled (no version resolved), and the reason
    // renders right next to them instead of a silent dead end.
    await waitFor(() => {
      expect(
        screen.getByText(/pick a version first — the version list failed to load/i),
      ).toBeInTheDocument()
    })
    expect(screen.getByRole('button', { name: /add to catalog/i })).toBeDisabled()
    expect(screen.getByRole('button', { name: /^preview$/i })).toBeDisabled()

    // Retry re-fires the exact same fetch — recovers once it succeeds.
    vi.mocked(api.listCuratedCatalogVersions).mockResolvedValueOnce({
      addon: 'prometheus',
      chart: 'kube-prometheus-stack',
      repo: 'https://prometheus-community.github.io/helm-charts',
      versions: [{ version: '45.0.0', prerelease: false }],
      latest_stable: '45.0.0',
      cached_at: new Date().toISOString(),
    })
    fireEvent.click(screen.getByRole('button', { name: /retry/i }))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /add to catalog/i })).toBeEnabled()
    })
    expect(
      screen.queryByText(/pick a version first/i),
    ).not.toBeInTheDocument()
  })

  it('shows a plain "pick a version" reason (no Retry) when there simply is no version yet', async () => {
    // Fetch succeeds, but the chart has no versions to auto-select — the
    // honest, non-error case the banner also has to cover.
    vi.mocked(api.listCuratedCatalogVersions).mockResolvedValueOnce({
      addon: 'prometheus',
      chart: 'kube-prometheus-stack',
      repo: 'https://prometheus-community.github.io/helm-charts',
      versions: [],
      cached_at: new Date().toISOString(),
    })
    renderDetail()

    await waitFor(() => {
      expect(screen.getByText(/^pick a version first\.$/i)).toBeInTheDocument()
    })
    expect(screen.queryByText(/failed to load/i)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /retry/i })).not.toBeInTheDocument()
  })
})

// v4 walk-findings W2, item 4 — the OPTIONAL add+enable combo on the
// Marketplace add door, mirroring the cluster-side V4EnableAddonDialog.
describe('MarketplaceAddonDetail — add+enable combo (v4 walk-findings W2, item 4)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    sessionStorage.clear()
    mockFetchTrackedPRs.mockResolvedValue({ prs: [] })
  })

  it('offers no cluster by default and does not send enable_on_cluster', async () => {
    vi.mocked(api.getClusters).mockResolvedValue({
      clusters: [{ name: 'prod-eu', labels: {}, connection_status: 'connected' }],
    })
    mockAddToCatalog.mockResolvedValue({ added: ['prometheus'], enabled: [], pr_id: 8, pr_url: 'https://gh/pr/8', merged: false })
    renderDetail()
    await waitForActionPanel()

    const select = await screen.findByLabelText(/also enable on a cluster/i)
    expect(select).toHaveValue('')

    fireEvent.click(screen.getByRole('button', { name: /add to catalog/i }))
    await waitFor(() => expect(mockAddToCatalog).toHaveBeenCalled())
    const arg = mockAddToCatalog.mock.calls[0][0]
    expect(arg.enable_on_cluster).toBeUndefined()
    expect(arg.yes).toBeUndefined()
  })

  it('picking a cluster sends the combo payload and names both PRs happening in one', async () => {
    vi.mocked(api.getClusters).mockResolvedValue({
      clusters: [{ name: 'prod-eu', labels: {}, connection_status: 'connected' }],
    })
    mockAddToCatalog.mockResolvedValue({
      added: ['prometheus'],
      enabled: ['prometheus'],
      cluster: 'prod-eu',
      pr_id: 9,
      pr_url: 'https://gh/pr/9',
      merged: false,
    })
    renderDetail()
    await waitForActionPanel()

    const select = await screen.findByLabelText(/also enable on a cluster/i)
    fireEvent.change(select, { target: { value: 'prod-eu' } })

    expect(screen.getByText(/one pull request will add/i)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /add to catalog/i }))
    await waitFor(() => expect(mockAddToCatalog).toHaveBeenCalled())
    const arg = mockAddToCatalog.mock.calls[0][0]
    expect(arg.enable_on_cluster).toBe('prod-eu')
    expect(arg.yes).toBe(true)
  })

  it('does not render the selector when there are no managed clusters', async () => {
    vi.mocked(api.getClusters).mockResolvedValue({ clusters: [] })
    renderDetail()
    await waitForActionPanel()

    expect(screen.queryByLabelText(/also enable on a cluster/i)).not.toBeInTheDocument()
  })
})

// v4 walk-findings W2, item 5 — an addon with an open catalog-add PR is
// invisible on GET /catalog/addons (merged-branch-only read), so the
// Marketplace detail page must check the tracked-PR list instead of just
// the catalog list, and must not offer a second, competing add.
describe('MarketplaceAddonDetail — pending add-PR (v4 walk-findings W2, item 5)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    sessionStorage.clear()
    vi.mocked(api.getClusters).mockResolvedValue({ clusters: [] })
  })

  it('shows the pending state and PR link instead of the add form, and does not call addToCatalog', async () => {
    mockFetchTrackedPRs.mockResolvedValue({
      prs: [
        {
          pr_id: 42,
          pr_url: 'https://gh/pr/42',
          pr_branch: 'sharko/catalog-add-prometheus',
          pr_title: 'sharko: add prometheus to catalog',
          addon: 'prometheus',
          operation: 'catalog-add',
          user: 'tester',
          source: 'ui',
          created_at: new Date().toISOString(),
          last_status: 'open',
          last_polled_at: new Date().toISOString(),
        },
      ],
    })
    renderDetail()

    expect(await screen.findByText(/already has an add-PR open/i)).toBeInTheDocument()
    // The banner's static text appears as soon as `entry` (from
    // getCuratedCatalogEntry) and `pendingAddonPRs` (from fetchTrackedPRs)
    // have both loaded — but the "View PR #42" link inside it also needs
    // `name` to have been seeded from `entry` by a SEPARATE effect
    // (pendingPR is matched against the trimmed `name` state, not
    // `entry.name` directly). That seed effect and the pendingAddonPRs
    // fetch are two independent promise chains with no ordering guarantee
    // between them, so the very first render carrying the banner text can
    // still be one render short of the link — asserting on it with a
    // synchronous getByRole right after the findByText above was the race
    // (it worked when the seed effect happened to flush first, and failed
    // under heavier scheduling load when it didn't). findByRole waits for
    // the same final, settled state instead of assuming that first render
    // is already it.
    const link = await screen.findByRole('link', { name: /view pr #42/i })
    expect(link).toHaveAttribute('href', 'https://gh/pr/42')

    // The form itself must not render — no duplicate-add door while pending.
    expect(screen.queryByLabelText(/display name/i)).not.toBeInTheDocument()
    expect(mockAddToCatalog).not.toHaveBeenCalled()
  })

  it('a non-pending addon renders the ordinary add form', async () => {
    mockFetchTrackedPRs.mockResolvedValue({ prs: [] })
    renderDetail()
    await waitForActionPanel()

    expect(screen.queryByText(/already has an add-PR open/i)).not.toBeInTheDocument()
    expect(screen.getByLabelText(/display name/i)).toBeInTheDocument()
  })
})

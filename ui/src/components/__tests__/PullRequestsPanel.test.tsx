/**
 * PullRequestsPanel — V125-1-6 filter chip + search + badge tests.
 *
 * Verifies:
 *   - Per-row category badge renders the expected label per Operation
 *   - Filter chip click sends the right ?operation= CSV to the BE
 *   - Free-text search filters in real time on title/cluster/addon
 *   - "View all on GitHub →" appears when server response equals limit
 *   - Existing `cluster` prop still scopes the panel
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { PullRequestsPanel } from '@/components/PullRequestsPanel'
import * as api from '@/services/api'
import { useConnectionsOptional } from '@/hooks/useConnections'
import type { TrackedPR } from '@/services/models'

// fetchTrackedPRs is mocked per-test so we can assert the arguments and
// drive the panel through different data shapes. fetchMergedPRs returns
// nothing — we don't exercise the Merged tab in this file.
vi.mock('@/services/api', async () => {
  return {
    fetchTrackedPRs: vi.fn(),
    fetchMergedPRs: vi.fn().mockResolvedValue({ prs: [], limit: 100 }),
    refreshPR: vi.fn().mockResolvedValue({ status: 'ok' }),
  }
})

// Defaults to "no connection metadata" (the real hook's behavior outside a
// ConnectionProvider) — individual tests override via mockReturnValue to
// exercise the escape-hatch's provider-aware wording.
vi.mock('@/hooks/useConnections', () => ({
  useConnectionsOptional: vi.fn().mockReturnValue(null),
}))

function trackedPR(overrides: Partial<TrackedPR>): TrackedPR {
  return {
    pr_id: 1,
    pr_url: 'https://github.com/test/repo/pull/1',
    pr_branch: 'sharko/test',
    pr_title: 'Test PR',
    operation: 'addon-add',
    user: 'admin',
    source: 'api',
    created_at: new Date().toISOString(),
    last_status: 'open',
    last_polled_at: new Date().toISOString(),
    ...overrides,
  }
}

function renderPanel(props: { cluster?: string } = {}) {
  return render(
    <MemoryRouter>
      <PullRequestsPanel {...props} />
    </MemoryRouter>,
  )
}

describe('PullRequestsPanel V125-1-6', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    // Reset to the "no connection metadata" default — individual tests in
    // the escape-hatch describe block below override this.
    vi.mocked(useConnectionsOptional).mockReturnValue(null)
  })

  it('renders per-row category badge per operation', async () => {
    vi.mocked(api.fetchTrackedPRs).mockResolvedValue({
      prs: [
        trackedPR({ pr_id: 1, operation: 'register-cluster', pr_title: 'Register cluster prod' }),
        trackedPR({ pr_id: 2, operation: 'addon-upgrade', pr_title: 'Upgrade datadog' }),
        trackedPR({ pr_id: 3, operation: 'init-repo', pr_title: 'Initialize repository' }),
        trackedPR({ pr_id: 4, operation: 'ai-tool-enable', pr_title: 'AI: enable cert-manager' }),
      ],
    })

    renderPanel()

    await waitFor(() => {
      expect(screen.getByText('Register cluster')).toBeInTheDocument()
      expect(screen.getByText('Upgrade addon')).toBeInTheDocument()
      expect(screen.getByText('Init repo')).toBeInTheDocument()
      expect(screen.getByText('AI assistant')).toBeInTheDocument()
    })
  })

  // v4 wave 2.5 review, L — the catalog-add op codes had no UI mapping, so
  // every catalog approval PR landed in the gray "Other" bucket instead of
  // a teal Addons badge.
  it('renders a named badge for catalog-add and catalog-add-enable (not the gray "Other" fallback)', async () => {
    vi.mocked(api.fetchTrackedPRs).mockResolvedValue({
      prs: [
        trackedPR({ pr_id: 5, operation: 'catalog-add', pr_title: 'Add cert-manager to catalog' }),
        trackedPR({ pr_id: 6, operation: 'catalog-add-enable', pr_title: 'Add + enable vault' }),
      ],
    })

    renderPanel()

    await waitFor(() => {
      expect(screen.getByText('Catalog: add addon(s)')).toBeInTheDocument()
      expect(screen.getByText('Catalog: add + enable')).toBeInTheDocument()
    })
    expect(screen.queryByText('Other')).not.toBeInTheDocument()
  })

  // Filter chips + search only earn their keep once the list is long
  // enough to need filtering (panel-lens finding) — a >10-row fixture
  // seeds these two tests so the controls actually render.
  function elevenPendingPRs(): TrackedPR[] {
    const prs = [
      trackedPR({ pr_id: 10, operation: 'addon-add', pr_title: 'Add datadog', cluster: 'prod-eu', addon: 'datadog' }),
      trackedPR({ pr_id: 11, operation: 'addon-add', pr_title: 'Add metrics-server', cluster: 'staging-us', addon: 'metrics-server' }),
    ]
    for (let i = 0; i < 9; i++) {
      prs.push(trackedPR({ pr_id: 100 + i, operation: 'values-edit', pr_title: `Filler PR ${i}` }))
    }
    return prs
  }

  it('clicking the Clusters filter chip sends the cluster operation CSV', async () => {
    vi.mocked(api.fetchTrackedPRs).mockResolvedValue({ prs: elevenPendingPRs() })

    renderPanel()

    // Wait for the initial fetch (empty filter).
    await waitFor(() => {
      expect(api.fetchTrackedPRs).toHaveBeenCalledWith(
        expect.objectContaining({ limit: 100 }),
      )
    })

    fireEvent.click(await screen.findByRole('button', { name: 'Clusters' }))

    await waitFor(() => {
      const lastCall = vi.mocked(api.fetchTrackedPRs).mock.calls.at(-1)?.[0]
      expect(lastCall?.operation).toBe(
        'register-cluster,remove-cluster,update-cluster,adopt-cluster,unadopt-cluster',
      )
    })
  })

  it('search input filters rows in real time', async () => {
    vi.mocked(api.fetchTrackedPRs).mockResolvedValue({ prs: elevenPendingPRs() })

    renderPanel()

    await waitFor(() => {
      expect(screen.getByText('Add datadog')).toBeInTheDocument()
      expect(screen.getByText('Add metrics-server')).toBeInTheDocument()
    })

    const search = await screen.findByLabelText('Search PRs') as HTMLInputElement
    fireEvent.change(search, { target: { value: 'metrics' } })

    await waitFor(() => {
      expect(screen.queryByText('Add datadog')).not.toBeInTheDocument()
      expect(screen.getByText('Add metrics-server')).toBeInTheDocument()
    })
  })

  it('filter chips and search stay hidden when the tab has 10 or fewer rows', async () => {
    vi.mocked(api.fetchTrackedPRs).mockResolvedValue({
      prs: [trackedPR({ pr_id: 1, pr_title: 'Only one PR' })],
    })

    renderPanel()

    await waitFor(() => {
      expect(screen.getByText('Only one PR')).toBeInTheDocument()
    })

    expect(screen.queryByRole('button', { name: 'Clusters' })).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Search PRs')).not.toBeInTheDocument()
  })

  it('"View all on GitHub →" appears when row count equals the limit cap', async () => {
    // Build 100 PRs (matches PR_FETCH_LIMIT) and a server-side limit of 100.
    const seed: TrackedPR[] = []
    for (let i = 0; i < 100; i++) {
      seed.push(trackedPR({ pr_id: 1000 + i, pr_title: `PR ${i}`, operation: 'addon-add' }))
    }
    vi.mocked(api.fetchTrackedPRs).mockResolvedValue({ prs: seed, limit: 100 })

    renderPanel()

    // The escape-hatch link is hidden when there's no active connection
    // (test mode). The text "Showing N of M open PRs (server cap)" still
    // appears so the user knows they're hitting the cap even without
    // the link.
    await waitFor(() => {
      expect(screen.getByText(/server cap/i)).toBeInTheDocument()
    })
  })

  it('cluster prop scopes the panel and hides the cluster column', async () => {
    vi.mocked(api.fetchTrackedPRs).mockResolvedValue({
      prs: [trackedPR({ pr_id: 5, cluster: 'prod-eu', pr_title: 'Update prod-eu' })],
    })

    renderPanel({ cluster: 'prod-eu' })

    await waitFor(() => {
      const lastCall = vi.mocked(api.fetchTrackedPRs).mock.calls.at(-1)?.[0]
      expect(lastCall?.cluster).toBe('prod-eu')
    })

    // Title says "Cluster PRs" rather than "Pull Requests".
    expect(screen.getByText('Cluster PRs')).toBeInTheDocument()
  })

  // Panel-lens findings: pending PR titles are links (same as merged rows),
  // and the always-"Open" status pill column is gone (every row on this
  // tab is open by definition — the column said nothing).
  it('pending PR title is a link, and there is no Status column', async () => {
    vi.mocked(api.fetchTrackedPRs).mockResolvedValue({
      prs: [trackedPR({ pr_id: 7, pr_title: 'Add cert-manager', pr_url: 'https://github.com/test/repo/pull/7' })],
    })

    renderPanel()

    const link = await screen.findByRole('link', { name: 'Add cert-manager' })
    expect(link).toHaveAttribute('href', 'https://github.com/test/repo/pull/7')

    expect(screen.queryByText('Status')).not.toBeInTheDocument()
    expect(screen.queryByText('Open')).not.toBeInTheDocument()
  })

  // S2 (scale-walk) fix: the Merged tab now honors category chips too,
  // filtering client-side via categorizeMergedTitle's best-effort title
  // match (the merge endpoint's own `operation` field is a near-useless
  // guess — see that function's comment). Previously the chips were
  // removed entirely on this tab; now they render just like Pending's.
  it('Merged tab shows category chips too, and clicking one narrows the list (S2 fix)', async () => {
    vi.mocked(api.fetchTrackedPRs).mockResolvedValue({ prs: [] })
    const merged = [
      {
        pr_id: 300,
        pr_url: 'https://github.com/test/repo/pull/300',
        pr_branch: 'sharko/register-prod',
        pr_title: 'sharko: register cluster prod',
        merged_at: new Date().toISOString(),
        author: 'admin',
      },
    ]
    for (let i = 0; i < 11; i++) {
      merged.push({
        pr_id: 200 + i,
        pr_url: `https://github.com/test/repo/pull/${200 + i}`,
        pr_branch: `sharko/example-${i}`,
        pr_title: `sharko: add addon datadog-${i}`,
        merged_at: new Date().toISOString(),
        author: 'admin',
      })
    }
    vi.mocked(api.fetchMergedPRs).mockResolvedValue({ prs: merged, limit: 100 })

    renderPanel()

    // Auto-switches to Merged (pending is empty).
    await waitFor(() => {
      expect(screen.getByRole('tab', { name: 'Merged' })).toHaveAttribute('aria-selected', 'true')
    })
    expect(await screen.findByLabelText('Search PRs')).toBeInTheDocument()
    const clustersChip = await screen.findByRole('button', { name: 'Clusters' })
    expect(screen.getByText(/register cluster prod/)).toBeInTheDocument()
    expect(screen.getByText(/add addon datadog-0\b/)).toBeInTheDocument()

    fireEvent.click(clustersChip)

    await waitFor(() => {
      expect(screen.getByText(/register cluster prod/)).toBeInTheDocument()
      expect(screen.queryByText(/add addon datadog/)).not.toBeInTheDocument()
    })
  })

  describe('escape hatch wording is provider-aware', () => {
    function mockConnection(gitProvider: string) {
      vi.mocked(useConnectionsOptional).mockReturnValue({
        connections: [
          {
            name: 'main',
            git_provider: gitProvider,
            git_repo_identifier: 'org/repo',
          } as never,
        ],
        activeConnection: 'main',
        loading: false,
        error: null,
        setActiveConnection: vi.fn(),
        refreshConnections: vi.fn(),
      })
    }

    it('renders a github.com link only for a GitHub connection', async () => {
      mockConnection('github')
      const seed: TrackedPR[] = []
      for (let i = 0; i < 100; i++) {
        seed.push(trackedPR({ pr_id: 1000 + i, pr_title: `PR ${i}`, operation: 'addon-add' }))
      }
      vi.mocked(api.fetchTrackedPRs).mockResolvedValue({ prs: seed, limit: 100 })

      renderPanel()

      const link = await screen.findByRole('link', { name: /view all on github/i })
      expect(link).toHaveAttribute('href', expect.stringContaining('github.com/org/repo'))
    })

    it('never renders a github.com link for a non-GitHub connection — plain wording instead', async () => {
      mockConnection('gitea')
      const seed: TrackedPR[] = []
      for (let i = 0; i < 100; i++) {
        seed.push(trackedPR({ pr_id: 2000 + i, pr_title: `PR ${i}`, operation: 'addon-add' }))
      }
      vi.mocked(api.fetchTrackedPRs).mockResolvedValue({ prs: seed, limit: 100 })

      renderPanel()

      await waitFor(() => {
        expect(screen.getByText(/view the rest on your git host/i)).toBeInTheDocument()
      })
      expect(screen.queryByRole('link', { name: /github/i })).not.toBeInTheDocument()
    })
  })

  describe('LW-8: Auto-show Merged when Pending is empty', () => {
    it('shows Merged tab when pending=0 and no explicit URL selection', async () => {
      // pending=0, merged has content
      vi.mocked(api.fetchTrackedPRs).mockResolvedValue({ prs: [] })
      vi.mocked(api.fetchMergedPRs).mockResolvedValue({
        prs: [
          {
            pr_id: 100,
            pr_url: 'https://github.com/test/repo/pull/100',
            pr_branch: 'sharko/example',
            pr_title: 'Merged PR example',
            merged_at: new Date().toISOString(),
            author: 'admin',
          },
        ],
        limit: 100,
      })

      renderPanel()

      // Should auto-switch to the Merged tab. The merged-row title renders
      // inside an anchor next to "#100", so the title text is split across
      // nodes — assert via the Merged tab being selected and the merged row
      // link (aria-label carries the PR id) being present. findByRole waits
      // for the async fetchMergedPRs render to settle (avoids a race where
      // the tab has flipped but the merged rows haven't fetched yet).
      await waitFor(() => {
        const mergedTab = screen.getByRole('tab', { name: 'Merged' })
        expect(mergedTab).toHaveAttribute('aria-selected', 'true')
      })
      expect(
        await screen.findByRole('link', { name: 'Open merged PR #100 in browser' }),
      ).toBeInTheDocument()
    })

    it('still shows Pending when pending>0', async () => {
      vi.mocked(api.fetchTrackedPRs).mockResolvedValue({
        prs: [trackedPR({ pr_id: 1, pr_title: 'Pending work' })],
      })

      renderPanel()

      await waitFor(() => {
        expect(screen.getByText('Pending work')).toBeInTheDocument()
      })

      // Pending tab should be active (default behavior unchanged)
      const pendingButton = screen.getByRole('tab', { name: 'Pending' })
      expect(pendingButton).toHaveAttribute('aria-selected', 'true')
    })

    it('honors explicit ?prs_state=pending even when pending=0', async () => {
      vi.mocked(api.fetchTrackedPRs).mockResolvedValue({ prs: [] })

      render(
        <MemoryRouter initialEntries={['/?prs_state=pending']}>
          <PullRequestsPanel />
        </MemoryRouter>,
      )

      // Explicit URL selection wins; should show "No tracked PRs" on pending tab
      await waitFor(() => {
        expect(screen.getByText('No tracked PRs')).toBeInTheDocument()
      })

      const pendingButton = screen.getByRole('tab', { name: 'Pending' })
      expect(pendingButton).toHaveAttribute('aria-selected', 'true')
    })
  })

  // S2 (scale-walk) — root cause: a category click re-fetches pending with
  // an operation filter and used to report the FILTERED count to the same
  // handler that auto-defaults to Merged when pending===0 (meant for the
  // BASELINE count only). Fixed by keying the auto-default purely off the
  // baseline (category==='all') fetch.
  describe('category click never hijacks the tab (S2 fix #1 + #3)', () => {
    it('a category click with zero matches does not switch to Merged, and the filter bar stays visible', async () => {
      vi.mocked(api.fetchTrackedPRs).mockImplementation(async (filters?: { operation?: string }) => {
        if (filters?.operation) {
          // The narrow category has nothing — elevenPendingPRs() has no
          // init-repo PR.
          return { prs: [] }
        }
        return { prs: elevenPendingPRs() }
      })
      vi.mocked(api.fetchMergedPRs).mockResolvedValue({ prs: [], limit: 100 })

      renderPanel()

      // Baseline loads first — filter bar shows (11 > 10), Pending active.
      await waitFor(() => {
        expect(screen.getByLabelText('Search PRs')).toBeInTheDocument()
      })
      expect(screen.getByRole('tab', { name: 'Pending' })).toHaveAttribute('aria-selected', 'true')

      fireEvent.click(screen.getByRole('button', { name: 'Init' }))

      await waitFor(() => {
        expect(screen.getByText('No pending PRs in the selected category.')).toBeInTheDocument()
      })
      // The bug: this used to flip to Merged because the FILTERED count
      // (0) hit the same auto-default handler the baseline count did.
      expect(screen.getByRole('tab', { name: 'Pending' })).toHaveAttribute('aria-selected', 'true')
      expect(screen.getByLabelText('Search PRs')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: 'Init' })).toBeInTheDocument()
    })

    it('a genuinely empty baseline (not a category filter) still auto-switches to Merged', async () => {
      vi.mocked(api.fetchTrackedPRs).mockResolvedValue({ prs: [] })
      vi.mocked(api.fetchMergedPRs).mockResolvedValue({
        prs: [
          {
            pr_id: 900,
            pr_url: 'https://github.com/test/repo/pull/900',
            pr_branch: 'sharko/example',
            pr_title: 'Merged PR example',
            merged_at: new Date().toISOString(),
            author: 'admin',
          },
        ],
        limit: 100,
      })

      renderPanel()

      await waitFor(() => {
        expect(screen.getByRole('tab', { name: 'Merged' })).toHaveAttribute('aria-selected', 'true')
      })
    })
  })

  // S3 (scale-walk) — both tabs page the already-fetched list 10 at a time.
  describe('paging (S3)', () => {
    function fifteenPendingPRs(): TrackedPR[] {
      const prs: TrackedPR[] = []
      for (let i = 0; i < 15; i++) {
        prs.push(trackedPR({ pr_id: 400 + i, operation: 'values-edit', pr_title: `Values PR ${i}` }))
      }
      return prs
    }

    it('shows 10 pending PRs with a "Show more" control, revealing the rest on click — fetch stays capped at 100', async () => {
      vi.mocked(api.fetchTrackedPRs).mockResolvedValue({ prs: fifteenPendingPRs() })

      renderPanel()

      await waitFor(() => {
        expect(screen.getByText('Values PR 0')).toBeInTheDocument()
      })
      expect(screen.getByText('Values PR 9')).toBeInTheDocument()
      expect(screen.queryByText('Values PR 10')).not.toBeInTheDocument()

      await waitFor(() => {
        expect(api.fetchTrackedPRs).toHaveBeenCalledWith(expect.objectContaining({ limit: 100 }))
      })

      fireEvent.click(screen.getByRole('button', { name: /show 5 more/i }))

      await waitFor(() => {
        expect(screen.getByText('Values PR 14')).toBeInTheDocument()
      })
      expect(screen.queryByRole('button', { name: /show.*more/i })).not.toBeInTheDocument()
    })

    it('shows 10 merged PRs with a "Show more" control, revealing the rest on click', async () => {
      vi.mocked(api.fetchTrackedPRs).mockResolvedValue({ prs: [] })
      const merged = []
      for (let i = 0; i < 13; i++) {
        merged.push({
          pr_id: 500 + i,
          pr_url: `https://github.com/test/repo/pull/${500 + i}`,
          pr_branch: `sharko/example-${i}`,
          pr_title: `Merged example ${i}`,
          merged_at: new Date().toISOString(),
          author: 'admin',
        })
      }
      vi.mocked(api.fetchMergedPRs).mockResolvedValue({ prs: merged, limit: 100 })

      renderPanel()

      await waitFor(() => {
        expect(screen.getByRole('tab', { name: 'Merged' })).toHaveAttribute('aria-selected', 'true')
      })
      expect(await screen.findByText(/Merged example 0/)).toBeInTheDocument()
      expect(screen.queryByText(/Merged example 10/)).not.toBeInTheDocument()

      fireEvent.click(screen.getByRole('button', { name: /show 3 more/i }))

      await waitFor(() => {
        expect(screen.getByText(/Merged example 12/)).toBeInTheDocument()
      })
    })
  })

  // S4 (scale-walk) — the AI chip only shows when a real AI PR exists in
  // the current tab's fetched list.
  describe('AI chip only when real (S4)', () => {
    it('is hidden when nothing in the fetched list matches the AI bucket', async () => {
      vi.mocked(api.fetchTrackedPRs).mockResolvedValue({ prs: elevenPendingPRs() })

      renderPanel()

      await waitFor(() => {
        expect(screen.getByLabelText('Search PRs')).toBeInTheDocument()
      })
      expect(screen.queryByRole('button', { name: 'AI' })).not.toBeInTheDocument()
    })

    it('shows when an ai-tool-* PR is in the fetched list', async () => {
      const prs = [
        ...elevenPendingPRs(),
        trackedPR({ pr_id: 950, operation: 'ai-tool-enable', pr_title: '[Sharko] Enable cert-manager on prod' }),
      ]
      vi.mocked(api.fetchTrackedPRs).mockResolvedValue({ prs })

      renderPanel()

      const aiChip = await screen.findByRole('button', { name: 'AI' })
      expect(aiChip).toBeInTheDocument()
    })

    it('falls back to All once the selected AI category has nothing left (its last PR merged away)', async () => {
      // shouldAdvanceTime keeps real wall-clock time flowing alongside the
      // fake one, so findByRole/waitFor's own internal polling still
      // works normally — only the PR panel's setInterval-driven poll needs
      // the deliberate fast-forward below. Plain vi.useFakeTimers() (no
      // real-time passthrough) hangs findByRole/waitFor entirely; calling
      // it only right before the fast-forward misses the interval created
      // by the earlier chip click (a REAL setInterval already ticking on
      // the real clock, which a later-installed fake clock can't reach).
      vi.useFakeTimers({ shouldAdvanceTime: true })
      let aiPRStillOpen = true
      vi.mocked(api.fetchTrackedPRs).mockImplementation(async (filters?: { operation?: string }) => {
        const base = elevenPendingPRs()
        if (aiPRStillOpen) {
          base.push(trackedPR({ pr_id: 950, operation: 'ai-tool-enable', pr_title: '[Sharko] Enable cert-manager on prod' }))
        }
        if (filters?.operation) {
          const ops = filters.operation.split(',')
          return { prs: base.filter((pr) => ops.includes(pr.operation)) }
        }
        return { prs: base }
      })

      renderPanel()

      const aiChip = await screen.findByRole('button', { name: 'AI' })
      fireEvent.click(aiChip)
      await waitFor(() => {
        expect(screen.getByRole('button', { name: 'AI' }).className).toContain('bg-teal-600')
      })

      // Simulate the AI PR merging away, then fast-forward past the poll
      // interval so the panel observes it.
      aiPRStillOpen = false
      await vi.advanceTimersByTimeAsync(30_000)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: 'All' }).className).toContain('bg-teal-600')
      })

      vi.useRealTimers()
    })
  })
})

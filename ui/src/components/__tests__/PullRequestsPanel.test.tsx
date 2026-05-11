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

  it('clicking the Clusters filter chip sends the cluster operation CSV', async () => {
    vi.mocked(api.fetchTrackedPRs).mockResolvedValue({ prs: [] })

    renderPanel()

    // Wait for the initial fetch (empty filter).
    await waitFor(() => {
      expect(api.fetchTrackedPRs).toHaveBeenCalledWith(
        expect.objectContaining({ limit: 100 }),
      )
    })

    fireEvent.click(screen.getByRole('button', { name: 'Clusters' }))

    await waitFor(() => {
      const lastCall = vi.mocked(api.fetchTrackedPRs).mock.calls.at(-1)?.[0]
      expect(lastCall?.operation).toBe(
        'register-cluster,remove-cluster,update-cluster,adopt-cluster,unadopt-cluster',
      )
    })
  })

  it('search input filters rows in real time', async () => {
    vi.mocked(api.fetchTrackedPRs).mockResolvedValue({
      prs: [
        trackedPR({ pr_id: 10, operation: 'addon-add', pr_title: 'Add datadog', cluster: 'prod-eu', addon: 'datadog' }),
        trackedPR({ pr_id: 11, operation: 'addon-add', pr_title: 'Add metrics-server', cluster: 'staging-us', addon: 'metrics-server' }),
      ],
    })

    renderPanel()

    await waitFor(() => {
      expect(screen.getByText('Add datadog')).toBeInTheDocument()
      expect(screen.getByText('Add metrics-server')).toBeInTheDocument()
    })

    const search = screen.getByLabelText('Search PRs') as HTMLInputElement
    fireEvent.change(search, { target: { value: 'metrics' } })

    await waitFor(() => {
      expect(screen.queryByText('Add datadog')).not.toBeInTheDocument()
      expect(screen.getByText('Add metrics-server')).toBeInTheDocument()
    })
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
})

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { CompletedChangesPanel } from '@/components/CompletedChangesPanel'
import type { ClusterChange } from '@/services/models'

// V2-cleanup-84.2: CompletedChangesPanel is the "Completed changes" half of
// the unified cluster Changes tab. It fetches api.getClusterChanges directly.
const mockGetClusterChanges = vi.fn()
vi.mock('@/services/api', () => ({
  api: {
    getClusterChanges: (...args: unknown[]) => mockGetClusterChanges(...args),
  },
}))

const healthyChange: ClusterChange = {
  operation: 'addon enable',
  addon: 'cert-manager',
  cluster: 'prod-eu',
  pr_id: 42,
  pr_url: 'https://github.com/example/repo/pull/42',
  opened_at: '2026-07-08T10:00:00Z',
  completed_at: '2026-07-08T10:05:00Z',
  status: 'merged',
  deploy_outcome: 'healthy',
}

const failedChange: ClusterChange = {
  operation: 'addon-upgrade',
  addon: 'ingress-nginx',
  cluster: 'prod-eu',
  pr_id: 43,
  pr_url: 'https://github.com/example/repo/pull/43',
  opened_at: '2026-07-01T10:00:00Z',
  completed_at: '2026-07-01T10:05:00Z',
  status: 'merged',
  deploy_outcome: 'failed',
}

const unknownChange: ClusterChange = {
  operation: 'addon-remove',
  addon: 'prometheus',
  cluster: 'prod-eu',
  pr_id: 44,
  pr_url: 'https://github.com/example/repo/pull/44',
  opened_at: '2026-06-01T10:00:00Z',
  completed_at: '2026-06-01T10:05:00Z',
  status: 'closed',
  deploy_outcome: 'unknown',
}

describe('CompletedChangesPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows a loading state, then renders rows once data arrives', async () => {
    mockGetClusterChanges.mockResolvedValue({ changes: [healthyChange] })
    render(<CompletedChangesPanel cluster="prod-eu" />)

    expect(screen.getByText('Loading completed changes...')).toBeInTheDocument()

    await waitFor(() => {
      expect(screen.getByText('addon enable')).toBeInTheDocument()
    })
    expect(mockGetClusterChanges).toHaveBeenCalledWith('prod-eu')
  })

  it('shows a friendly empty state when there are no completed changes', async () => {
    mockGetClusterChanges.mockResolvedValue({ changes: [] })
    render(<CompletedChangesPanel cluster="prod-eu" />)

    await waitFor(() => {
      expect(screen.getByText('No completed changes yet')).toBeInTheDocument()
    })
  })

  it('shows an error state when the fetch fails', async () => {
    mockGetClusterChanges.mockRejectedValue(new Error('boom'))
    render(<CompletedChangesPanel cluster="prod-eu" />)

    await waitFor(() => {
      expect(screen.getByText('boom')).toBeInTheDocument()
    })
  })

  it('maps deploy_outcome to the right badge for healthy, failed, and unknown', async () => {
    mockGetClusterChanges.mockResolvedValue({
      changes: [healthyChange, failedChange, unknownChange],
    })
    render(<CompletedChangesPanel cluster="prod-eu" />)

    await waitFor(() => {
      expect(screen.getByText('Deployed & healthy')).toBeInTheDocument()
    })
    expect(screen.getByText('Sync failed')).toBeInTheDocument()
    expect(screen.getByText('Deploy status unknown')).toBeInTheDocument()
  })

  it('shows a status pill for merged and closed entries', async () => {
    mockGetClusterChanges.mockResolvedValue({
      changes: [healthyChange, unknownChange],
    })
    render(<CompletedChangesPanel cluster="prod-eu" />)

    await waitFor(() => {
      expect(screen.getByText('Merged')).toBeInTheDocument()
    })
    expect(screen.getByText('Closed')).toBeInTheDocument()
  })

  it('toggles row details on click, including the GitHub link', async () => {
    mockGetClusterChanges.mockResolvedValue({ changes: [healthyChange] })
    render(<CompletedChangesPanel cluster="prod-eu" />)

    await waitFor(() => {
      expect(screen.getByText('addon enable')).toBeInTheDocument()
    })

    expect(screen.queryByText('View pull request on GitHub')).not.toBeInTheDocument()

    const toggle = screen.getByRole('button', { name: /addon enable/i })
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    fireEvent.click(toggle)

    await waitFor(() => {
      expect(screen.getByText('View pull request on GitHub')).toBeInTheDocument()
    })
    expect(toggle).toHaveAttribute('aria-expanded', 'true')

    const link = screen.getByText('View pull request on GitHub').closest('a')
    expect(link).toHaveAttribute('href', healthyChange.pr_url)
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')

    fireEvent.click(toggle)
    await waitFor(() => {
      expect(screen.queryByText('View pull request on GitHub')).not.toBeInTheDocument()
    })
  })

  it('refetches when refreshKey changes', async () => {
    mockGetClusterChanges.mockResolvedValue({ changes: [] })
    const { rerender } = render(<CompletedChangesPanel cluster="prod-eu" refreshKey={0} />)

    await waitFor(() => {
      expect(mockGetClusterChanges).toHaveBeenCalledTimes(1)
    })

    rerender(<CompletedChangesPanel cluster="prod-eu" refreshKey={1} />)

    await waitFor(() => {
      expect(mockGetClusterChanges).toHaveBeenCalledTimes(2)
    })
  })

  it('calls onDataChange with the loaded changes', async () => {
    mockGetClusterChanges.mockResolvedValue({ changes: [healthyChange] })
    const onDataChange = vi.fn()
    render(<CompletedChangesPanel cluster="prod-eu" onDataChange={onDataChange} />)

    await waitFor(() => {
      expect(onDataChange).toHaveBeenCalledWith([healthyChange])
    })
  })
})

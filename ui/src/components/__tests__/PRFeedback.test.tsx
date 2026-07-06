import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act, fireEvent, cleanup } from '@testing-library/react'
import {
  PRLink,
  PRResultBanner,
  PRProgressBanner,
  PRLifecycleProgress,
  PRModelExplainer,
  PR_MODEL_EXPLAINER_DISMISSED_KEY,
  extractPR,
} from '@/components/PRFeedback'

// Mock the refreshPR api call used by PRLifecycleProgress polling.
const mockRefreshPR = vi.fn()
vi.mock('@/services/api', () => ({
  refreshPR: (...args: unknown[]) => mockRefreshPR(...args),
}))

describe('extractPR', () => {
  it('reads top-level pr fields', () => {
    expect(
      extractPR({ pr_url: 'https://gh/pull/5', pr_id: 5, merged: true }),
    ).toEqual({ prUrl: 'https://gh/pull/5', prId: 5, merged: true })
  })

  it('falls back to the result-wrapped (attribution-warning) shape', () => {
    expect(
      extractPR({
        attribution_warning: 'no_per_user_pat',
        result: { pr_url: 'https://gh/pull/9', pr_id: 9, merged: false },
      } as never),
    ).toEqual({ prUrl: 'https://gh/pull/9', prId: 9, merged: false })
  })

  it('accepts the legacy pull_request_url alias', () => {
    expect(extractPR({ pull_request_url: 'https://gh/pull/3' })).toEqual({
      prUrl: 'https://gh/pull/3',
      prId: null,
      merged: false,
    })
  })

  it('returns nulls when there is no PR', () => {
    expect(extractPR(null)).toEqual({ prUrl: null, prId: null, merged: false })
    expect(extractPR({})).toEqual({ prUrl: null, prId: null, merged: false })
  })
})

describe('PRLink', () => {
  it('renders a clickable link with the PR number and opens in a new tab', () => {
    render(<PRLink url="https://github.com/example/repo/pull/42" id={42} />)
    const link = screen.getByRole('link', { name: /View PR #42 on GitHub/i })
    expect(link).toHaveAttribute('href', 'https://github.com/example/repo/pull/42')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
  })

  it('falls back to a generic label when the id is unknown', () => {
    render(<PRLink url="https://gh/pull/1" />)
    expect(screen.getByRole('link', { name: /View PR on GitHub/i })).toBeInTheDocument()
  })
})

describe('PRResultBanner', () => {
  it('renders the merged message and a clickable PR link when merged', () => {
    render(
      <PRResultBanner
        result={{ pr_url: 'https://gh/pull/7', pr_id: 7, merged: true }}
        mergedMessage="PR merged — change applied"
        openMessage="PR opened — merge it to apply"
      />,
    )
    expect(screen.getByText('PR merged — change applied')).toBeInTheDocument()
    const link = screen.getByRole('link', { name: /View PR #7 on GitHub/i })
    expect(link).toHaveAttribute('href', 'https://gh/pull/7')
  })

  it('renders the open message (not merged) and a clickable PR link when the PR is open', () => {
    render(
      <PRResultBanner
        result={{ pr_url: 'https://gh/pull/8', pr_id: 8, merged: false }}
        mergedMessage="PR merged — change applied"
        openMessage="PR opened — merge it to apply"
      />,
    )
    expect(screen.getByText('PR opened — merge it to apply')).toBeInTheDocument()
    expect(screen.queryByText('PR merged — change applied')).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: /View PR #8 on GitHub/i })).toBeInTheDocument()
  })

  it('renders nothing when there is no PR url', () => {
    const { container } = render(<PRResultBanner result={{ merged: true }} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('renders the optional hint line', () => {
    render(
      <PRResultBanner
        result={{ pr_url: 'https://gh/pull/4', pr_id: 4 }}
        hint="ArgoCD will sync shortly."
      />,
    )
    expect(screen.getByText('ArgoCD will sync shortly.')).toBeInTheDocument()
  })
})

describe('PRProgressBanner', () => {
  it('renders nothing when idle', () => {
    const { container } = render(<PRProgressBanner phase="idle" />)
    expect(container).toBeEmptyDOMElement()
  })

  it('renders the submitting message with a spinner', () => {
    render(<PRProgressBanner phase="submitting" submittingMessage="Working…" />)
    expect(screen.getByText('Working…')).toBeInTheDocument()
  })

  it('renders the merged terminal message', () => {
    render(<PRProgressBanner phase="merged" mergedMessage="All done — merged" />)
    expect(screen.getByText('All done — merged')).toBeInTheDocument()
  })

  it('renders the opened terminal message', () => {
    render(<PRProgressBanner phase="opened" openedMessage="PR is open" />)
    expect(screen.getByText('PR is open')).toBeInTheDocument()
  })
})

describe('PRLifecycleProgress', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows "Creating PR…" while result is null (POST in flight)', () => {
    render(
      <PRLifecycleProgress result={null} autoMergeExpected={false} />,
    )
    expect(screen.getByText('Creating PR…')).toBeInTheDocument()
  })

  it('shows all 3 steps; step 2 has PR link when result has merged:true', () => {
    render(
      <PRLifecycleProgress
        result={{ pr_url: 'https://gh/pull/5', pr_id: 5, merged: true }}
        autoMergeExpected={true}
        mergedLabel="PR merged — done"
        openLabel="PR open"
      />,
    )
    // Step 1 done.
    expect(screen.getByText('Creating PR…')).toBeInTheDocument()
    // Step 2 shows the PR link.
    const link = screen.getByRole('link', { name: /View PR #5 on GitHub/i })
    expect(link).toHaveAttribute('href', 'https://gh/pull/5')
    // Step 3 shows merged label — no polling needed because result.merged is true.
    expect(screen.getByText('PR merged — done')).toBeInTheDocument()
    expect(mockRefreshPR).not.toHaveBeenCalled()
  })

  it('shows "open for review" label immediately when autoMergeExpected=false', () => {
    render(
      <PRLifecycleProgress
        result={{ pr_url: 'https://gh/pull/6', pr_id: 6, merged: false }}
        autoMergeExpected={false}
        openLabel="Open for review"
      />,
    )
    expect(screen.getByText('Open for review')).toBeInTheDocument()
    // No polling started.
    expect(mockRefreshPR).not.toHaveBeenCalled()
  })

  it('polls refreshPR and transitions to merged when status=merged', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    mockRefreshPR.mockResolvedValue({ status: 'merged' })

    render(
      <PRLifecycleProgress
        result={{ pr_url: 'https://gh/pull/7', pr_id: 7, merged: false }}
        autoMergeExpected={true}
        mergedLabel="All merged!"
        openLabel="Open"
      />,
    )

    // Advance one poll interval (7s) and drain the event loop.
    await act(() => vi.advanceTimersByTimeAsync(7001))

    expect(mockRefreshPR).toHaveBeenCalledWith(7)
    expect(screen.getByText('All merged!')).toBeInTheDocument()
    vi.useRealTimers()
  })

  it('stops polling and shows open label after timeout (MAX_POLLS reached)', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    mockRefreshPR.mockResolvedValue({ status: 'open' })

    render(
      <PRLifecycleProgress
        result={{ pr_url: 'https://gh/pull/8', pr_id: 8, merged: false }}
        autoMergeExpected={true}
        openLabel="Timed out — open"
      />,
    )

    // Advance past MAX_POLLS (18) × 7s = 126s.
    await act(() => vi.advanceTimersByTimeAsync(130_000))

    expect(screen.getByText('Timed out — open')).toBeInTheDocument()
    // Capped at MAX_POLLS=18.
    expect(mockRefreshPR.mock.calls.length).toBeLessThanOrEqual(18)
    vi.useRealTimers()
  })

  it('stops polling immediately when status=closed (PR closed without merge)', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    mockRefreshPR.mockResolvedValue({ status: 'closed' })

    render(
      <PRLifecycleProgress
        result={{ pr_url: 'https://gh/pull/9', pr_id: 9, merged: false }}
        autoMergeExpected={true}
        openLabel="Closed"
      />,
    )

    await act(() => vi.advanceTimersByTimeAsync(7001))

    expect(screen.getByText('Closed')).toBeInTheDocument()
    expect(mockRefreshPR).toHaveBeenCalledTimes(1)
    vi.useRealTimers()
  })
})

// V2-cleanup-61.3 (F1b) — the one-time PR-model explainer shared by the
// addon-add flow (AddonCatalog.tsx) and the cluster-registration flow
// (ClustersOverview.tsx). Persistence is a single shared localStorage flag
// so dismissing it on either page hides it on both.
describe('PRModelExplainer', () => {
  beforeEach(() => {
    window.localStorage.clear()
  })

  it('appears the first time it is mounted', () => {
    render(<PRModelExplainer />)
    expect(screen.getByText('Why a pull request?')).toBeInTheDocument()
  })

  it('does not appear once dismissed', () => {
    render(<PRModelExplainer />)
    fireEvent.click(screen.getByRole('button', { name: /dismiss/i }))
    expect(screen.queryByText('Why a pull request?')).not.toBeInTheDocument()

    // Simulate remounting on a fresh page (e.g. navigating from Addons to
    // Clusters) — the shared localStorage flag keeps it hidden.
    cleanup()
    render(<PRModelExplainer />)
    expect(screen.queryByText('Why a pull request?')).not.toBeInTheDocument()
  })

  it('stays hidden across mounts once the shared flag is set directly', () => {
    window.localStorage.setItem(PR_MODEL_EXPLAINER_DISMISSED_KEY, '1')
    render(<PRModelExplainer />)
    expect(screen.queryByText('Why a pull request?')).not.toBeInTheDocument()
  })
})

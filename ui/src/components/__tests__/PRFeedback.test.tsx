import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import {
  PRLink,
  PRResultBanner,
  PRProgressBanner,
  extractPR,
} from '@/components/PRFeedback'

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

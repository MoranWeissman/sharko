import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ClusterIdentityPanel } from '@/components/ClusterIdentityPanel'

// Fix 12: the identity panel spoke of a "hub" and "spokes", and of "IAM",
// to people who may run a single kind cluster. Plain words only.
describe('ClusterIdentityPanel wording', () => {
  const caps = { aws: { detected: false, method: 'none' }, hub_platform: 'unknown' }

  it('the heading says it is detected once, with no hub word', () => {
    render(<ClusterIdentityPanel capabilities={caps as never} loading={false} />)
    expect(screen.getByText('Detected once — applies to every cluster')).toBeInTheDocument()
    expect(screen.queryByText(/hub/i)).not.toBeInTheDocument()
  })

  it('the explanation says AWS role and EKS cluster, not IAM, hub or spoke', () => {
    render(<ClusterIdentityPanel capabilities={caps as never} loading={false} />)
    fireEvent.click(screen.getByRole('button', { name: /how identity-based access works/i }))
    const text = screen.getByTestId('identity-how-it-works').textContent ?? ''
    expect(text).toContain('Sharko runs with one AWS role. Every EKS cluster that opts in trusts that one role')
    expect(text).not.toMatch(/IAM|hub|spoke/)
  })

  it('says Sharko renews the short-lived token, and not that nothing is stored', () => {
    render(<ClusterIdentityPanel capabilities={caps as never} loading={false} />)
    fireEvent.click(screen.getByRole('button', { name: /how identity-based access works/i }))
    const text = screen.getByTestId('identity-how-it-works').textContent ?? ''
    expect(text).toContain('it makes a short-lived token and renews it before it runs out.')
    expect(text).not.toMatch(/on demand|nothing is stored/)
  })
})

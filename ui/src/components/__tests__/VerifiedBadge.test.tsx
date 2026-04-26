import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { VerifiedBadge } from '@/components/VerifiedBadge'

describe('VerifiedBadge', () => {
  it('renders "Verified" when verified is true', () => {
    render(<VerifiedBadge verified={true} />)
    expect(screen.getByText('Verified')).toBeInTheDocument()
  })

  it('renders "Unsigned" when verified is false', () => {
    render(<VerifiedBadge verified={false} />)
    expect(screen.getByText('Unsigned')).toBeInTheDocument()
  })

  it('tooltip contains signatureIdentity when verified + identity provided', () => {
    render(
      <VerifiedBadge
        verified={true}
        signatureIdentity="https://github.com/cncf/cert-manager/.github/workflows/release.yaml@refs/heads/main"
      />,
    )
    const badge = screen.getByLabelText(/Verified — signed by/)
    expect(badge.getAttribute('title')).toContain(
      'https://github.com/cncf/cert-manager',
    )
  })

  it('tooltip falls back to generic message when verified but no identity', () => {
    render(<VerifiedBadge verified={true} />)
    const badge = screen.getByLabelText('Verified')
    expect(badge.getAttribute('title')).toContain(
      'cosign keyless signature accepted',
    )
  })

  it('aria-label includes signing identity when present', () => {
    render(<VerifiedBadge verified={true} signatureIdentity="release@org" />)
    expect(screen.getByLabelText(/release@org/)).toBeInTheDocument()
  })

  it('compact mode applies tile-size classes', () => {
    const { container } = render(<VerifiedBadge verified={true} compact />)
    expect(container.innerHTML).toContain('text-[11px]')
  })

  it('does NOT include gray-* Tailwind utilities (palette guard)', () => {
    const v = render(<VerifiedBadge verified={true} />)
    const u = render(<VerifiedBadge verified={false} />)
    expect(v.container.innerHTML).not.toMatch(/\bbg-gray-/)
    expect(v.container.innerHTML).not.toMatch(/\btext-gray-/)
    expect(u.container.innerHTML).not.toMatch(/\bbg-gray-/)
    expect(u.container.innerHTML).not.toMatch(/\btext-gray-/)
  })

  it('renders an icon for both states', () => {
    const { container: vc } = render(<VerifiedBadge verified={true} />)
    const { container: uc } = render(<VerifiedBadge verified={false} />)
    expect(vc.querySelector('svg')).toBeTruthy()
    expect(uc.querySelector('svg')).toBeTruthy()
  })

  it('treats missing verified as Unsigned (defensive default)', () => {
    render(<VerifiedBadge />)
    expect(screen.getByText('Unsigned')).toBeInTheDocument()
  })
})

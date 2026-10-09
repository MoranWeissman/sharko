import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { SourceBadge } from '@/components/SourceBadge'

describe('SourceBadge', () => {
  it('renders "Internal" when source is "embedded"', () => {
    render(<SourceBadge source="embedded" />)
    expect(screen.getByText('Internal')).toBeInTheDocument()
  })

  it('renders "Internal" when source is undefined', () => {
    render(<SourceBadge source={undefined} />)
    expect(screen.getByText('Internal')).toBeInTheDocument()
  })

  it('renders the URL host for a well-formed third-party URL', () => {
    render(<SourceBadge source="https://catalogs.example.com/addons.yaml" />)
    expect(screen.getByText('catalogs.example.com')).toBeInTheDocument()
  })

  it('falls back to "Third-party" when URL is malformed', () => {
    render(<SourceBadge source="not a url" />)
    expect(screen.getByText('Third-party')).toBeInTheDocument()
  })

  it('tooltip contains the full URL for third-party', () => {
    render(<SourceBadge source="https://catalogs.example.com/addons.yaml" />)
    const badge = screen.getByLabelText(
      /Source: catalogs\.example\.com \(third-party catalog\)/,
    )
    expect(badge.getAttribute('title')).toContain(
      'Source: https://catalogs.example.com/addons.yaml',
    )
  })

  it('tooltip includes Last fetched line when sourceRecord provided', () => {
    render(
      <SourceBadge
        source="https://catalogs.example.com/addons.yaml"
        sourceRecord={{
          url: 'https://catalogs.example.com/addons.yaml',
          status: 'ok',
          last_fetched: '2026-04-22T10:00:00Z',
          entry_count: 5,
          verified: false,
        }}
      />,
    )
    const badge = screen.getByLabelText(/third-party catalog/)
    expect(badge.getAttribute('title')).toContain(
      'Last fetched: 2026-04-22T10:00:00Z',
    )
    expect(badge.getAttribute('title')).toContain('Status: ok')
  })

  it('aria-label contains the host string', () => {
    render(<SourceBadge source="https://catalogs.example.com/addons.yaml" />)
    expect(screen.getByLabelText(/catalogs\.example\.com/)).toBeInTheDocument()
  })

  it('does NOT include gray-* Tailwind utilities (palette rule guard)', () => {
    const { container } = render(<SourceBadge source="embedded" />)
    expect(container.innerHTML).not.toMatch(/\bbg-gray-/)
    expect(container.innerHTML).not.toMatch(/\btext-gray-/)
  })
})

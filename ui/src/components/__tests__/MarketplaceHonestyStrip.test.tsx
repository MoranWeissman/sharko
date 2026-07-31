import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { MarketplaceHonestyStrip } from '@/components/MarketplaceHonestyStrip'

/**
 * v4 wave 2.5 review fix round, H-4 — the honesty strip used to link "See
 * the curated file" at the empty v3 seed template instead of the real
 * curated list, and "propose an addon" at CONTRIBUTING.md instead of the
 * catalog-entries doc. Both hrefs are asserted directly so a future drift
 * fails a test instead of shipping a dead link again.
 */
describe('MarketplaceHonestyStrip', () => {
  it('links "See the curated file" at the real curated catalog.yaml', () => {
    render(<MarketplaceHonestyStrip />)
    const link = screen.getByRole('link', { name: /see the curated file/i })
    expect(link).toHaveAttribute(
      'href',
      'https://github.com/MoranWeissman/sharko/blob/main/catalog/addons.yaml',
    )
  })

  it('links "propose an addon" at the catalog-entries contributing doc', () => {
    render(<MarketplaceHonestyStrip />)
    const link = screen.getByRole('link', { name: /propose an addon/i })
    expect(link).toHaveAttribute(
      'href',
      'https://github.com/MoranWeissman/sharko/blob/main/docs/site/community/contributing-catalog-entries.md',
    )
  })
})

/**
 * Accessibility audit (Story V121-8.4 — WCAG 2.1 AA)
 *
 * Runs axe-core against the v1.21-new Marketplace pages: Browse, Search,
 * the in-page Addon Detail (v1.21 QA Bundle 2), and the Marketplace
 * tablist itself. Fails the suite on any serious or critical violation.
 *
 * Why we don't audit existing pre-v1.21 pages here: the design doc (§4.8)
 * commits to "WCAG 2.1 AA on new pages" only — retrofitting the rest of
 * the app is on the v1.22 backlog. Adding them to this run would surface
 * pre-existing legacy violations and block the v1.21 release for work
 * that was never in scope.
 *
 * jsdom limitations: axe's color-contrast rule depends on a real layout
 * engine; jsdom returns transparent for most computed styles. We disable
 * `color-contrast` here and rely on (a) the shadcn/ui defaults being
 * 4.5:1 by construction and (b) the manual sweep documented in
 * docs/developer-guide/accessibility.md (Story 8.4 manual leg).
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, waitFor, screen, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import axe from 'axe-core'
import { MarketplaceTab } from '@/components/MarketplaceTab'
import { MarketplaceSearchTab } from '@/components/MarketplaceSearchTab'
import type { CatalogEntry } from '@/services/models'

// Stub the API surface MarketplaceTab + child components touch on mount.
const fixtures: CatalogEntry[] = [
  {
    name: 'cert-manager',
    description: 'TLS lifecycle manager.',
    chart: 'cert-manager',
    repo: 'https://charts.jetstack.io',
    default_namespace: 'cert-manager',
    default_sync_wave: 1,
    maintainers: ['jetstack'],
    license: 'Apache-2.0',
    category: 'security',
    curated_by: ['cncf-graduated'],
    security_score: 8.2,
    security_tier: 'Strong',
    github_stars: 12500,
  },
]

const listMock = vi.fn().mockResolvedValue({ addons: fixtures, total: fixtures.length })
const listVersionsMock = vi.fn().mockResolvedValue({
  addon: 'cert-manager',
  chart: 'cert-manager',
  repo: 'https://charts.jetstack.io',
  versions: [{ version: '1.20.0', prerelease: false }],
  latest_stable: '1.20.0',
  cached_at: '2026-04-17T00:00:00Z',
})
const getCatalogMock = vi.fn().mockResolvedValue({ addons: [] })
const getMeMock = vi
  .fn()
  .mockResolvedValue({ username: 'a11y', role: 'admin', has_github_token: true })
const searchCatalogMock = vi
  .fn()
  .mockResolvedValue({ query: '', curated: [], artifacthub: [] })
const getEntryMock = vi.fn().mockResolvedValue(fixtures[0])
const getReadmeMock = vi
  .fn()
  .mockResolvedValue({ readme: '## Hello\nWelcome.', source: 'artifacthub' })

vi.mock('@/services/api', () => ({
  api: {
    listCuratedCatalog: () => listMock(),
    listCuratedCatalogVersions: (...a: unknown[]) => listVersionsMock(...a),
    getAddonCatalog: () => getCatalogMock(),
    getMe: () => getMeMock(),
    searchCatalog: (...a: unknown[]) => searchCatalogMock(...a),
    getCuratedCatalogEntry: (...a: unknown[]) => getEntryMock(...a),
    getCuratedCatalogReadme: (...a: unknown[]) => getReadmeMock(...a),
    reprobeArtifactHub: () => Promise.resolve({ reachable: true, probed_at: '' }),
  },
  addAddon: vi.fn(),
  isAddonAlreadyExistsError: () => false,
}))

// Common axe configuration. We:
//   - Restrict to WCAG 2.0/2.1 A + AA tags (matches our public commitment).
//   - Disable `color-contrast` (jsdom limitation — see file header).
//   - Keep `region` enabled so we catch missing landmarks early.
const axeOpts: axe.RunOptions = {
  runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'] },
  rules: {
    'color-contrast': { enabled: false },
  },
}

async function expectNoSeriousViolations(container: HTMLElement, label: string) {
  const results = await axe.run(container, axeOpts)
  const blocking = results.violations.filter(
    (v) => v.impact === 'serious' || v.impact === 'critical',
  )
  if (blocking.length > 0) {
    const summary = blocking
      .map((v) => `${v.id} (${v.impact}) — ${v.help}: ${v.nodes.length} node(s)`)
      .join('\n')
    throw new Error(`${label}: ${blocking.length} blocking a11y violations\n${summary}`)
  }
  expect(blocking).toEqual([])
}

describe('Marketplace pages — WCAG 2.1 AA (axe-core)', () => {
  beforeEach(() => {
    listMock.mockClear()
    listVersionsMock.mockClear()
    getCatalogMock.mockClear()
    getMeMock.mockClear()
    searchCatalogMock.mockClear()
    getEntryMock.mockClear()
    getReadmeMock.mockClear()
  })

  it('Marketplace tab (Browse view) has no serious violations', async () => {
    const { container } = render(
      <MemoryRouter>
        <MarketplaceTab />
      </MemoryRouter>,
    )
    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: /Open cert-manager/i }),
      ).toBeInTheDocument()
    })
    await expectNoSeriousViolations(container, 'MarketplaceTab/Browse')
  })

  it('Marketplace Search tab has no serious violations', async () => {
    const { container } = render(
      <MemoryRouter>
        <MarketplaceSearchTab />
      </MemoryRouter>,
    )
    // Search tab renders a search input + empty state on first paint.
    await waitFor(() => {
      expect(screen.getByRole('searchbox')).toBeInTheDocument()
    })
    await expectNoSeriousViolations(container, 'MarketplaceSearchTab')
  })

  it('In-page Addon Detail (opened from Browse) has no serious violations', async () => {
    const { container } = render(
      <MemoryRouter>
        <MarketplaceTab />
      </MemoryRouter>,
    )
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: /Open cert-manager/i }),
      ).toBeInTheDocument(),
    )
    fireEvent.click(screen.getByRole('button', { name: /Open cert-manager/i }))
    await waitFor(() =>
      expect(
        screen.getByRole('heading', { name: /Add cert-manager to your catalog/i }),
      ).toBeInTheDocument(),
    )
    await expectNoSeriousViolations(container, 'MarketplaceAddonDetail')
  })
})

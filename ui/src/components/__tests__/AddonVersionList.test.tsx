import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { AddonVersionList } from '@/components/AddonVersionList'
import type { VersionMatrixResponse } from '@/services/models'

// AddonVersionList — S2 (scale-walk day 7). Replaces both the old
// VersionMatrixTable (a 50-column grid) and BehindCatalogList (a separate
// flat behind-only view) with one addon-first list. Data is a prop, not a
// self-fetch (the parent, AddonCatalog, owns the single GET
// /addons/version-matrix call so the "Behind catalog version" stat card
// and this list share one request) — so these tests just hand it fixtures
// directly, no api mocking needed.

function noop() {}

describe('AddonVersionList — spread summary math', () => {
  it('says "all N on VERSION" when every cell matches the catalog version', async () => {
    const data: VersionMatrixResponse = {
      clusters: ['a', 'b', 'c', 'd', 'e'],
      addons: [
        {
          addon_name: 'all-match',
          catalog_version: '2.0.0',
          chart: 'all-match',
          cells: {
            a: { version: '2.0.0', health: 'Healthy', drift_from_catalog: false },
            b: { version: '2.0.0', health: 'Healthy', drift_from_catalog: false },
            c: { version: '2.0.0', health: 'Healthy', drift_from_catalog: false },
            d: { version: '2.0.0', health: 'Healthy', drift_from_catalog: false },
            e: { version: '2.0.0', health: 'Healthy', drift_from_catalog: false },
          },
        },
      ],
    }
    render(<AddonVersionList data={data} loading={false} error={null} onRetry={noop} />)
    expect(await screen.findByText('all 5 on 2.0.0')).toBeInTheDocument()
  })

  it('says "N on VERSION · M behind" when some cells are drifted', async () => {
    const data: VersionMatrixResponse = {
      clusters: ['prod-eu', 'prod-us', 'staging-eu'],
      addons: [
        {
          addon_name: 'cert-manager',
          catalog_version: '1.14.0',
          chart: 'cert-manager',
          cells: {
            'prod-eu': { version: '1.12.0', health: 'Healthy', drift_from_catalog: true },
            'prod-us': { version: '1.14.0', health: 'Healthy', drift_from_catalog: false },
            'staging-eu': { version: '1.10.0', health: 'Degraded', drift_from_catalog: true },
          },
        },
      ],
    }
    render(<AddonVersionList data={data} loading={false} error={null} onRetry={noop} />)
    expect(await screen.findByText('1 on 1.14.0 · 2 behind')).toBeInTheDocument()
  })

  it('says "not deployed on any cluster" for an addon row with no cells', async () => {
    const data: VersionMatrixResponse = {
      clusters: ['prod-eu'],
      addons: [
        {
          addon_name: 'nowhere-addon',
          catalog_version: '1.0.0',
          chart: 'nowhere-addon',
          cells: {},
        },
      ],
    }
    render(<AddonVersionList data={data} loading={false} error={null} onRetry={noop} />)
    expect(await screen.findByText('not deployed on any cluster')).toBeInTheDocument()
  })
})

describe('AddonVersionList — drift-first sorting', () => {
  it('sorts addons with any behind cell before addons with none, stable within each group', async () => {
    const data: VersionMatrixResponse = {
      clusters: ['prod-eu'],
      addons: [
        {
          addon_name: 'aaa-clean',
          catalog_version: '1.0.0',
          chart: 'aaa-clean',
          cells: { 'prod-eu': { version: '1.0.0', health: 'Healthy', drift_from_catalog: false } },
        },
        {
          addon_name: 'bbb-drifted',
          catalog_version: '1.0.0',
          chart: 'bbb-drifted',
          cells: { 'prod-eu': { version: '0.9.0', health: 'Healthy', drift_from_catalog: true } },
        },
        {
          addon_name: 'ccc-clean',
          catalog_version: '1.0.0',
          chart: 'ccc-clean',
          cells: { 'prod-eu': { version: '1.0.0', health: 'Healthy', drift_from_catalog: false } },
        },
        {
          addon_name: 'ddd-drifted',
          catalog_version: '1.0.0',
          chart: 'ddd-drifted',
          cells: { 'prod-eu': { version: '0.8.0', health: 'Healthy', drift_from_catalog: true } },
        },
      ],
    }
    render(<AddonVersionList data={data} loading={false} error={null} onRetry={noop} />)
    await screen.findByText('aaa-clean')

    const names = screen
      .getAllByRole('button', { expanded: false })
      .map((btn) => btn.textContent ?? '')
      .filter((t) => t.includes('-clean') || t.includes('-drifted'))
    // Drifted rows (server order preserved among ties: bbb before ddd)
    // come first, then clean rows (aaa before ccc).
    const order = names.map((t) =>
      t.includes('bbb-drifted')
        ? 'bbb-drifted'
        : t.includes('ddd-drifted')
          ? 'ddd-drifted'
          : t.includes('aaa-clean')
            ? 'aaa-clean'
            : 'ccc-clean',
    )
    expect(order).toEqual(['bbb-drifted', 'ddd-drifted', 'aaa-clean', 'ccc-clean'])
  })
})

describe('AddonVersionList — behind-catalog filter chip', () => {
  const twoAddonData: VersionMatrixResponse = {
    clusters: ['prod-eu'],
    addons: [
      {
        addon_name: 'clean-addon',
        catalog_version: '1.0.0',
        chart: 'clean-addon',
        cells: { 'prod-eu': { version: '1.0.0', health: 'Healthy', drift_from_catalog: false } },
      },
      {
        addon_name: 'drifted-addon',
        catalog_version: '1.0.0',
        chart: 'drifted-addon',
        cells: { 'prod-eu': { version: '0.9.0', health: 'Healthy', drift_from_catalog: true } },
      },
    ],
  }

  it('shows every addon by default, chip inactive', async () => {
    render(<AddonVersionList data={twoAddonData} loading={false} error={null} onRetry={noop} />)
    await screen.findByText('clean-addon')
    expect(screen.getByText('drifted-addon')).toBeInTheDocument()
  })

  it('clicking the "Behind catalog" chip filters to only drifted addons', async () => {
    render(<AddonVersionList data={twoAddonData} loading={false} error={null} onRetry={noop} />)
    await screen.findByText('clean-addon')

    fireEvent.click(screen.getByTestId('version-list-behind-chip'))

    expect(screen.getByText('drifted-addon')).toBeInTheDocument()
    expect(screen.queryByText('clean-addon')).not.toBeInTheDocument()
  })

  it('initialBehindCatalogOnly=true (deep link) starts pre-filtered and active', async () => {
    render(
      <AddonVersionList
        data={twoAddonData}
        loading={false}
        error={null}
        onRetry={noop}
        initialBehindCatalogOnly
      />,
    )
    await screen.findByText('drifted-addon')
    expect(screen.queryByText('clean-addon')).not.toBeInTheDocument()
    expect(screen.getByTestId('version-list-behind-chip')).toHaveTextContent('Behind catalog')
  })

  it('dismissing the active chip calls onClearBehindCatalogFilter and restores every addon', async () => {
    const onClear = vi.fn()
    render(
      <AddonVersionList
        data={twoAddonData}
        loading={false}
        error={null}
        onRetry={noop}
        initialBehindCatalogOnly
        onClearBehindCatalogFilter={onClear}
      />,
    )
    const chip = await screen.findByTestId('version-list-behind-chip')
    fireEvent.click(within(chip).getByRole('button', { name: /clear the behind catalog filter/i }))

    expect(onClear).toHaveBeenCalledTimes(1)
    expect(screen.getByText('clean-addon')).toBeInTheDocument()
  })

  it('shows a "nothing behind" message when the filter excludes every row', async () => {
    const allClean: VersionMatrixResponse = {
      clusters: ['prod-eu'],
      addons: [
        {
          addon_name: 'clean-addon',
          catalog_version: '1.0.0',
          chart: 'clean-addon',
          cells: { 'prod-eu': { version: '1.0.0', health: 'Healthy', drift_from_catalog: false } },
        },
      ],
    }
    render(<AddonVersionList data={allClean} loading={false} error={null} onRetry={noop} initialBehindCatalogOnly />)
    expect(await screen.findByText(/Nothing behind/)).toBeInTheDocument()
  })
})

describe('AddonVersionList — "Update available" filter chip (upstream freshness)', () => {
  const data: VersionMatrixResponse = {
    clusters: ['prod-eu'],
    addons: [
      {
        addon_name: 'up-to-date-addon',
        catalog_version: '1.0.0',
        chart: 'up-to-date-addon',
        cells: { 'prod-eu': { version: '1.0.0', health: 'Healthy', drift_from_catalog: false } },
        newest_available: '1.0.0',
      },
      {
        addon_name: 'outdated-addon',
        catalog_version: '1.0.0',
        chart: 'outdated-addon',
        cells: { 'prod-eu': { version: '1.0.0', health: 'Healthy', drift_from_catalog: false } },
        newest_available: '2.0.0',
      },
    ],
  }

  it('initialOutdatedOnly=true starts filtered to rows with an available upgrade', async () => {
    render(<AddonVersionList data={data} loading={false} error={null} onRetry={noop} initialOutdatedOnly />)
    await screen.findByText('outdated-addon')
    expect(screen.queryByText('up-to-date-addon')).not.toBeInTheDocument()
  })
})

describe('AddonVersionList — paging', () => {
  function buildManyAddons(count: number): VersionMatrixResponse {
    return {
      clusters: ['prod-eu'],
      addons: Array.from({ length: count }, (_, i) => ({
        addon_name: `addon-${String(i).padStart(2, '0')}`,
        catalog_version: '1.0.0',
        chart: `addon-${i}`,
        cells: { 'prod-eu': { version: '1.0.0', health: 'Healthy', drift_from_catalog: false } },
      })),
    }
  }

  it('addon list pages by 10 with a "Show more" button', async () => {
    render(<AddonVersionList data={buildManyAddons(15)} loading={false} error={null} onRetry={noop} />)
    await screen.findByText('addon-00')

    expect(screen.queryByText('addon-10')).not.toBeInTheDocument()
    const showMore = screen.getByRole('button', { name: /Show 5 more/ })
    fireEvent.click(showMore)

    expect(await screen.findByText('addon-10')).toBeInTheDocument()
    expect(screen.getByText('addon-14')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Show.*more/ })).not.toBeInTheDocument()
  })

  it('cluster rows inside an expanded addon page by 10 with their own "Show more"', async () => {
    const clusters = Array.from({ length: 14 }, (_, i) => `cluster-${String(i).padStart(2, '0')}`)
    const cells: VersionMatrixResponse['addons'][number]['cells'] = {}
    for (const c of clusters) {
      cells[c] = { version: '1.0.0', health: 'Healthy', drift_from_catalog: false }
    }
    const data: VersionMatrixResponse = {
      clusters,
      addons: [{ addon_name: 'big-addon', catalog_version: '1.0.0', chart: 'big-addon', cells }],
    }
    render(<AddonVersionList data={data} loading={false} error={null} onRetry={noop} />)
    const row = await screen.findByText('big-addon')
    fireEvent.click(row.closest('button') as HTMLElement)

    expect(screen.getByText('cluster-00')).toBeInTheDocument()
    expect(screen.queryByText('cluster-10')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /Show 4 more/ }))
    expect(await screen.findByText('cluster-13')).toBeInTheDocument()
  })
})

describe('AddonVersionList — loading, error, and empty states', () => {
  it('shows a loading state', () => {
    render(<AddonVersionList data={null} loading error={null} onRetry={noop} />)
    expect(screen.getByText(/Loading addon versions/)).toBeInTheDocument()
  })

  it('shows an error state with retry', () => {
    const onRetry = vi.fn()
    render(<AddonVersionList data={null} loading={false} error="boom" onRetry={onRetry} />)
    expect(screen.getByText('boom')).toBeInTheDocument()
  })

  it('shows an empty state when there are no addons', () => {
    render(
      <AddonVersionList data={{ clusters: [], addons: [] }} loading={false} error={null} onRetry={noop} />,
    )
    expect(screen.getByText(/No addons to show yet/)).toBeInTheDocument()
  })
})

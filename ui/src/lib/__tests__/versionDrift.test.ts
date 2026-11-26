import { describe, it, expect } from 'vitest'
import { isAheadOfCatalog, isBehindCatalog } from '@/lib/versionDrift'
import { summarizeBehindCatalog } from '@/components/FleetStatusStrip'

// Pre-release walk 4: after two clusters were upgraded ahead of the catalog,
// the Addons card said "1 Behind catalog version" and the Dashboard said
// "2 behind". A cluster on a NEWER version is not behind.
describe('behind vs ahead of the catalog', () => {
  const same = { version: '6.7.1', health: 'Healthy', drift_from_catalog: false }
  const older = { version: '6.6.0', health: 'Healthy', drift_from_catalog: true }
  const newer = { version: '6.8.0', health: 'Healthy', drift_from_catalog: true, ahead_of_catalog: true }

  it('only an older version is behind', () => {
    expect(isBehindCatalog(same)).toBe(false)
    expect(isBehindCatalog(older)).toBe(true)
    expect(isBehindCatalog(newer)).toBe(false)
    expect(isBehindCatalog(undefined)).toBe(false)
  })

  it('only a newer version is ahead', () => {
    expect(isAheadOfCatalog(same)).toBe(false)
    expect(isAheadOfCatalog(older)).toBe(false)
    expect(isAheadOfCatalog(newer)).toBe(true)
  })

  it('the Dashboard count leaves out clusters that are ahead', () => {
    const matrix = {
      clusters: ['spoke-1', 'spoke-2', 'spoke-3'],
      addons: [
        {
          addon_name: 'podinfo',
          catalog_version: '6.7.1',
          chart: 'podinfo',
          cells: { 'spoke-1': newer, 'spoke-2': newer, 'spoke-3': older },
        },
      ],
    }
    expect(summarizeBehindCatalog(matrix as never).behindCount).toBe(1)
  })
})

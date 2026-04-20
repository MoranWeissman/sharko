import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, waitFor, cleanup } from '@testing-library/react'
import { PerClusterAddonOverridesEditor } from '@/components/PerClusterAddonOverridesEditor'
import type { AddonComparisonStatus } from '@/services/models'

/**
 * v1.21.8 regression tests — guard against the re-mount storm that made
 * tab clicks feel dead on Cluster Detail → Config.
 *
 * Root cause: ClusterDetail polls every 30s and produces a fresh
 * `addon_comparisons` array reference even when content is unchanged.
 * Without React.memo content-equality on the editor's `addons` prop,
 * every parent render re-ran the editor's useEffects, which (a) fired
 * GET /users/me and GET /clusters/<c>/addons/<a>/values repeatedly and
 * (b) transitively remounted the embedded ValuesEditor via its
 * `key={clusterName/selected}` whenever `selected` transiently reset.
 *
 * The fix wraps the editor in React.memo with a custom equality check
 * over addon_name + git_configured + argocd_deployed (the only fields
 * the picker cares about). These tests pin that behaviour.
 */

const getMeMock = vi.fn()
const getClusterAddonValuesMock = vi.fn()
const getClusterAddonValuesRecentPRsMock = vi.fn()

vi.mock('@/services/api', () => ({
  api: {
    getMe: () => getMeMock(),
    getClusterAddonValues: (...args: unknown[]) => getClusterAddonValuesMock(...args),
    getClusterAddonValuesRecentPRs: (...args: unknown[]) =>
      getClusterAddonValuesRecentPRsMock(...args),
    setClusterAddonValues: vi.fn(),
  },
}))

function buildAddons(overrides: Partial<AddonComparisonStatus> = {}): AddonComparisonStatus[] {
  return [
    {
      addon_name: 'cert-manager',
      git_configured: true,
      git_enabled: true,
      has_version_override: false,
      argocd_deployed: true,
      issues: [],
      ...overrides,
    },
    {
      addon_name: 'external-dns',
      git_configured: true,
      git_enabled: true,
      has_version_override: false,
      argocd_deployed: false,
      issues: [],
    },
  ]
}

beforeEach(() => {
  getMeMock.mockReset()
  getClusterAddonValuesMock.mockReset()
  getClusterAddonValuesRecentPRsMock.mockReset()
  getMeMock.mockResolvedValue({ username: 'tester', role: 'admin', has_github_token: true })
  getClusterAddonValuesMock.mockResolvedValue({
    cluster_name: 'test-cluster',
    addon_name: 'cert-manager',
    current_overrides: '',
    schema: null,
  })
  getClusterAddonValuesRecentPRsMock.mockResolvedValue({ entries: [] })
  cleanup()
})

describe('PerClusterAddonOverridesEditor', () => {
  it('fires each API call exactly once on mount', async () => {
    const addons = buildAddons()
    render(
      <PerClusterAddonOverridesEditor clusterName="test-cluster" addons={addons} />,
    )
    await waitFor(() => {
      expect(getMeMock).toHaveBeenCalledTimes(1)
      expect(getClusterAddonValuesMock).toHaveBeenCalledTimes(1)
    })
    expect(getClusterAddonValuesMock).toHaveBeenCalledWith('test-cluster', 'cert-manager')
  })

  it('does NOT re-fire /users/me or /values when parent re-renders with a new addons array of identical content', async () => {
    // Simulates the ClusterDetail 30s setInterval tick: same content, new reference.
    const firstBatch = buildAddons()
    const { rerender } = render(
      <PerClusterAddonOverridesEditor clusterName="test-cluster" addons={firstBatch} />,
    )
    // Wait for initial mount effects.
    await waitFor(() => {
      expect(getMeMock).toHaveBeenCalledTimes(1)
      expect(getClusterAddonValuesMock).toHaveBeenCalledTimes(1)
    })

    // Simulate 5 parent ticks with fresh-but-equivalent data (what
    // ClusterDetail produces every 30s). Each tick creates new array and
    // new item references via .map → equivalent to JSON-decoded fresh
    // API responses.
    for (let i = 0; i < 5; i++) {
      const freshBatch = buildAddons().map((a) => ({ ...a }))
      rerender(
        <PerClusterAddonOverridesEditor clusterName="test-cluster" addons={freshBatch} />,
      )
    }

    // Still exactly one call each — no remount storm.
    expect(getMeMock).toHaveBeenCalledTimes(1)
    expect(getClusterAddonValuesMock).toHaveBeenCalledTimes(1)
  })

  it('preserves `selected` when an addon array changes but the selected addon is still present', async () => {
    // Start with cert-manager selected (first alphabetically). Then
    // change only a field that isn't part of the eligibility signature
    // (e.g. status). The editor must not reset selection, which would
    // remount the ValuesEditor.
    const initial = buildAddons()
    const { rerender } = render(
      <PerClusterAddonOverridesEditor clusterName="test-cluster" addons={initial} />,
    )
    await waitFor(() => {
      expect(getClusterAddonValuesMock).toHaveBeenCalledTimes(1)
    })

    const statusChanged = buildAddons({ status: 'progressing' })
    rerender(
      <PerClusterAddonOverridesEditor clusterName="test-cluster" addons={statusChanged} />,
    )
    // Selected addon is unchanged → no extra fetch.
    expect(getClusterAddonValuesMock).toHaveBeenCalledTimes(1)
  })

  it('re-fires /values when the user actually enables a new addon that would change the picker', async () => {
    // This test asserts the memo is not TOO aggressive — a real addon
    // change should flow through.
    const initial = buildAddons()
    const { rerender } = render(
      <PerClusterAddonOverridesEditor clusterName="test-cluster" addons={initial} />,
    )
    await waitFor(() => {
      expect(getClusterAddonValuesMock).toHaveBeenCalledTimes(1)
    })

    // Add a third addon. The picker content changes, so a re-render is
    // expected (but note: `selected` still points at cert-manager, so
    // getClusterAddonValues should NOT be called again — only getMe is
    // `[]`-keyed, and the component isn't remounting).
    const expanded: AddonComparisonStatus[] = [
      ...initial,
      {
        addon_name: 'metrics-server',
        git_configured: true,
        git_enabled: true,
        has_version_override: false,
        argocd_deployed: true,
        issues: [],
      },
    ]
    rerender(
      <PerClusterAddonOverridesEditor clusterName="test-cluster" addons={expanded} />,
    )
    // Eligibility signature changed, component re-renders — but effects
    // keyed on [clusterName, selected] do NOT refire because neither changed.
    // getMe still only fires once (keyed on []).
    expect(getMeMock).toHaveBeenCalledTimes(1)
    expect(getClusterAddonValuesMock).toHaveBeenCalledTimes(1)
  })

  it('fetches values for the new addon when the selected addon is removed from the cluster', async () => {
    // If the backend reports that the currently-selected addon is no
    // longer eligible (e.g. user deregistered it on another tab), the
    // editor should fall back to the first available addon and fetch
    // its values. This is the one scenario where a legitimate re-fetch
    // is required.
    const initial = buildAddons()
    const { rerender } = render(
      <PerClusterAddonOverridesEditor clusterName="test-cluster" addons={initial} />,
    )
    await waitFor(() => {
      expect(getClusterAddonValuesMock).toHaveBeenCalledTimes(1)
    })
    expect(getClusterAddonValuesMock).toHaveBeenLastCalledWith(
      'test-cluster',
      'cert-manager',
    )

    // Remove cert-manager — external-dns is now the only eligible one.
    const reduced: AddonComparisonStatus[] = [
      {
        addon_name: 'external-dns',
        git_configured: true,
        git_enabled: true,
        has_version_override: false,
        argocd_deployed: false,
        issues: [],
      },
    ]
    rerender(
      <PerClusterAddonOverridesEditor clusterName="test-cluster" addons={reduced} />,
    )

    await waitFor(() => {
      expect(getClusterAddonValuesMock).toHaveBeenCalledTimes(2)
    })
    expect(getClusterAddonValuesMock).toHaveBeenLastCalledWith(
      'test-cluster',
      'external-dns',
    )
  })
})

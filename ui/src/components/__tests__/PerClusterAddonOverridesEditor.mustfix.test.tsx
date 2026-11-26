import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, waitFor, screen } from '@testing-library/react'
import { PerClusterAddonOverridesEditor } from '@/components/PerClusterAddonOverridesEditor'
import type { AddonComparisonStatus } from '@/services/models'

/*
 * Review must-fix: the cluster Config tab said "These values apply only to
 * <cluster>. Save opens a pull request." while the file was loading and after
 * the first read failed. On a repo where this screen is read-only that is
 * false, and the page only knows which kind of repo it is after a good answer.
 */

const getClusterAddonValuesMock = vi.fn()

vi.mock('@/services/api', () => ({
  api: {
    getMe: () => Promise.resolve({ username: 'tester', role: 'admin', has_github_token: true }),
    getClusterAddonValues: (...args: unknown[]) => getClusterAddonValuesMock(...args),
    getClusterAddonValuesRecentPRs: () => Promise.resolve({ entries: [] }),
    setClusterAddonValues: vi.fn(),
  },
}))

const addons: AddonComparisonStatus[] = [
  {
    addon_name: 'reloader',
    git_configured: true,
    git_enabled: true,
    has_version_override: false,
    argocd_deployed: true,
    issues: [],
  },
]

const SAVE_SENTENCE = 'opens a pull request'

beforeEach(() => {
  getClusterAddonValuesMock.mockReset()
})

describe('PerClusterAddonOverridesEditor — before a good answer', () => {
  it('does not say "Save opens a pull request" and shows no editor while loading', async () => {
    getClusterAddonValuesMock.mockReturnValue(new Promise(() => {})) // never answers
    const { container } = render(<PerClusterAddonOverridesEditor clusterName="spoke-1" addons={addons} />)
    await waitFor(() => expect(screen.getByText('Loading overrides…')).toBeInTheDocument())
    expect(container.textContent).not.toContain(SAVE_SENTENCE)
    expect(screen.queryByRole('button', { name: /submit/i })).toBeNull()
  })

  it('does not say it, and shows no editor, when the first read fails', async () => {
    getClusterAddonValuesMock.mockRejectedValue(new Error('Bad Gateway'))
    const { container } = render(<PerClusterAddonOverridesEditor clusterName="spoke-1" addons={addons} />)
    await waitFor(() => expect(screen.getByText('Bad Gateway')).toBeInTheDocument())
    expect(container.textContent).not.toContain(SAVE_SENTENCE)
    expect(screen.queryByRole('button', { name: /submit/i })).toBeNull()
  })

  it('does not say it on a read-only answer', async () => {
    getClusterAddonValuesMock.mockResolvedValue({
      cluster_name: 'spoke-1', addon_name: 'reloader', current_overrides: 'a: 1\n', schema: null,
      read_only: true, file_exists: true, values_file: 'values/clusters/spoke-1/reloader.yaml',
    })
    const { container } = render(<PerClusterAddonOverridesEditor clusterName="spoke-1" addons={addons} />)
    await waitFor(() => expect(container.textContent).toContain('values/clusters/spoke-1/reloader.yaml'))
    expect(container.textContent).not.toContain(SAVE_SENTENCE)
  })

  it('still says it when a good answer says the file can be saved from here', async () => {
    getClusterAddonValuesMock.mockResolvedValue({
      cluster_name: 'spoke-1', addon_name: 'reloader', current_overrides: '', schema: null,
    })
    const { container } = render(<PerClusterAddonOverridesEditor clusterName="spoke-1" addons={addons} />)
    await waitFor(() => expect(container.textContent).toContain('Save opens a pull request.'))
  })
})

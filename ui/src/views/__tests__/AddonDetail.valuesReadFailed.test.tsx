import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { AddonDetail } from '@/views/AddonDetail'
import { AuthContext } from '@/hooks/useAuth'
import { VALUES_READ_FAILED_SENTENCE } from '@/components/ReadOnlyValues'

/*
 * Review must-fix: when reading the values file failed (502 from Git, a
 * request limit, the network), the Values tab fell back to the old empty
 * editor with "Submitting opens a PR...", "1 lines · 0 chars" and a Submit
 * button that could only be refused. A failed read is now shown as a failed
 * read, with a way to try again.
 */

const recentPRsMock = vi.fn().mockResolvedValue({ entries: [] })
const schemaMock = vi.fn()

vi.mock('@/services/api', () => ({
  getAddonPRs: vi.fn().mockResolvedValue({ prs: [] }),
  upgradeAddon: vi.fn().mockResolvedValue({ pr_url: '' }),
  removeAddon: vi.fn().mockResolvedValue({}),
  configureAddon: vi.fn(),
  api: {
    getConnections: vi.fn().mockResolvedValue({ connections: [], active_connection: '' }),
    getAddonValues: vi.fn().mockRejectedValue(new Error('not found')),
    getMe: vi.fn().mockResolvedValue({ username: 'tester', role: 'admin', has_github_token: true }),
    getAddonValuesSchema: (...a: unknown[]) => schemaMock(...a),
    getAddonValuesRecentPRs: (...a: unknown[]) => recentPRsMock(...a),
    getAIConfig: vi.fn().mockResolvedValue({ current_provider: 'none', available_providers: [], annotate_on_seed: false }),
    getUpgradeRecommendations: vi.fn().mockResolvedValue({ current_version: '2.2.21' }),
    getUpgradeVersions: vi.fn().mockResolvedValue({ versions: [] }),
    getAddonDetail: vi.fn().mockResolvedValue({
      addon: {
        addon_name: 'reloader',
        chart: 'reloader',
        repo_url: 'https://stakater.github.io/stakater-charts',
        namespace: 'reloader',
        version: '2.2.21',
        selfHeal: true,
        syncOptions: [],
        extraHelmValues: {},
        total_clusters: 3,
        enabled_clusters: 2,
        healthy_applications: 2,
        degraded_applications: 0,
        missing_applications: 0,
        applications: [],
      },
    }),
  },
}))

const adminCtx = {
  token: 't',
  username: 'tester',
  role: 'admin',
  login: vi.fn(),
  logout: vi.fn(),
  isAuthenticated: true,
  isAdmin: true,
  loading: false,
  error: null,
}

function renderSection(section: string) {
  return render(
    <AuthContext.Provider value={adminCtx}>
      <MemoryRouter initialEntries={[`/addons/reloader?section=${section}`]}>
        <Routes>
          <Route path="/addons/:name" element={<AddonDetail />} />
        </Routes>
      </MemoryRouter>
    </AuthContext.Provider>,
  )
}



beforeEach(() => {
  schemaMock.mockReset()
  recentPRsMock.mockClear()
})

describe('AddonDetail Values tab — the file cannot be read', () => {
  it('pins the sentence', () => {
    expect(VALUES_READ_FAILED_SENTENCE).toBe(
      'Sharko could not read the values file from Git just now. Nothing was changed.',
    )
  })

  it('says so, with the reason, and offers no editor and no Submit', async () => {
    schemaMock.mockRejectedValue(Object.assign(new Error('Bad Gateway'), { status: 502 }))
    const { container } = renderSection('values')
    await waitFor(() => expect(screen.getByText(VALUES_READ_FAILED_SENTENCE)).toBeInTheDocument())
    expect(screen.getByText('Bad Gateway')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /submit/i })).toBeNull()
    for (const old of ['Submitting opens a PR', 'Pull new fields from upstream', 'lines ·', 'Recent changes', 'Discard changes']) {
      expect(container.textContent).not.toContain(old)
    }
    // It asked once. A failed read must not be asked again and again.
    await new Promise((r) => setTimeout(r, 50))
    expect(schemaMock).toHaveBeenCalledTimes(1)
    expect(recentPRsMock).not.toHaveBeenCalled()
  })

  it('reads again on Try Again and then shows the file', async () => {
    schemaMock.mockRejectedValueOnce(new Error('Bad Gateway'))
    schemaMock.mockResolvedValue({
      addon_name: 'reloader', current_values: 'replicaCount: 1\n', schema: null,
      read_only: true, file_exists: true, values_file: 'values/global/reloader.yaml',
    })
    const { container } = renderSection('values')
    await waitFor(() => expect(screen.getByText(VALUES_READ_FAILED_SENTENCE)).toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', { name: 'Try Again' }))
    await waitFor(() => expect(container.textContent).toContain('values/global/reloader.yaml'))
    expect(screen.queryByText(VALUES_READ_FAILED_SENTENCE)).toBeNull()
    expect(schemaMock).toHaveBeenCalledTimes(2)
  })
})

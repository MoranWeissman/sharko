// Review must-fix: Settings, Connection said "Repository not initialized.
// Your Git repository has not been set up yet." with two Initialize buttons
// for ANY not-initialized answer. When GitHub is only limiting Sharko, or the
// repo could not be read, the repo may be set up perfectly well: Sharko just
// could not look. The yellow bar at the top says "nothing in Settings needs
// changing" in that state, and Settings said the opposite.

import { render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  ConnectionSection,
  REPO_NOT_CHECKED,
  REPO_NOT_CHECKED_GIT_LIMIT,
} from '@/views/settings/ConnectionSection'

const repoStatusMock = vi.fn()

vi.mock('@/hooks/useConnections', () => ({
  useConnections: () => ({
    connections: [
      {
        name: 'main',
        is_active: true,
        git_provider: 'github',
        git_repo_identifier: 'acme/repo',
        argocd_server_url: 'https://argocd.example.test',
        argocd_namespace: 'argocd',
      },
    ],
    loading: false,
    error: null,
    refreshConnections: vi.fn(),
  }),
}))

vi.mock('@/services/api', () => ({
  api: {
    testConnection: vi.fn().mockResolvedValue({ git: { status: 'ok' }, argocd: { status: 'ok' } }),
    getRepoStatus: () => repoStatusMock(),
    testCredentials: vi.fn(),
    createConnection: vi.fn(),
    updateConnection: vi.fn(),
  },
  initRepo: vi.fn(),
  getOperation: vi.fn(),
  operationHeartbeat: vi.fn(),
}))

const INIT_HEADING = 'Repository not initialized'
// The page always has one "Initialize Repository" card at the bottom, with
// two buttons. It claims nothing about the repo ("Safe to run on an
// already-initialized repository"). The box this test is about adds two more.
const ALWAYS_THERE_BUTTONS = 2
const INIT_BODY = 'has not been set up yet'

describe('ConnectionSection — repo status box', () => {
  beforeEach(() => {
    repoStatusMock.mockReset()
  })

  it('pins the two sentences', () => {
    expect(REPO_NOT_CHECKED_GIT_LIMIT).toBe(
      'Sharko could not check the repository just now, because GitHub, or the Git host you use, says Sharko has asked too often. Nothing in Settings needs changing. It clears by itself.',
    )
    expect(REPO_NOT_CHECKED).toBe(
      'Sharko could not read the Git repository just now, so it does not know whether the repository is set up.',
    )
  })

  const notChecked = [
    { reason: 'git_rate_limited', want: REPO_NOT_CHECKED_GIT_LIMIT },
    { reason: 'connection_error', want: REPO_NOT_CHECKED },
    { reason: 'no_connection', want: REPO_NOT_CHECKED },
    { reason: 'error', want: REPO_NOT_CHECKED },
  ]
  for (const c of notChecked) {
    it(`reason ${c.reason}: no "not initialized" box and none of its Initialize buttons`, async () => {
      repoStatusMock.mockResolvedValue({ initialized: false, bootstrap_synced: false, reason: c.reason })
      const { container } = render(<ConnectionSection />)
      const note = await screen.findByTestId('repo-not-checked-note')
      expect(note.textContent).toBe(c.want)
      expect(container.textContent).not.toContain(INIT_HEADING)
      expect(container.textContent).not.toContain(INIT_BODY)
      expect(screen.getAllByRole('button', { name: /Initialize/ })).toHaveLength(ALWAYS_THERE_BUTTONS)
    })
  }

  it('a repo that was read and found empty still gets the Initialize box', async () => {
    repoStatusMock.mockResolvedValue({ initialized: false, bootstrap_synced: false, reason: 'not_bootstrapped' })
    const { container } = render(<ConnectionSection />)
    await waitFor(() => expect(container.textContent).toContain(INIT_HEADING))
    expect(container.textContent).toContain(INIT_BODY)
    expect(screen.getAllByRole('button', { name: /Initialize/ })).toHaveLength(ALWAYS_THERE_BUTTONS + 2)
    expect(screen.queryByTestId('repo-not-checked-note')).toBeNull()
  })

  it('a repo that is set up shows neither', async () => {
    repoStatusMock.mockResolvedValue({ initialized: true, bootstrap_synced: true })
    const { container } = render(<ConnectionSection />)
    await waitFor(() => expect(repoStatusMock).toHaveBeenCalled())
    await new Promise((r) => setTimeout(r, 20))
    expect(container.textContent).not.toContain(INIT_HEADING)
    expect(screen.queryByTestId('repo-not-checked-note')).toBeNull()
  })
})

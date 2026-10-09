/**
 * v4.0.4 U2 (demo video): on a Gitea connection the UI said "View PR #N on
 * GitHub" and "Set up your personal GitHub PAT". Text that names the Git
 * host must follow the active connection's git_provider, and be neutral
 * when the host is unknown.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { gitHostLabel, personalTokenName } from '@/lib/gitHost'
import { PRLink, prLinkText } from '@/components/PRFeedback'
import { AttributionNudge } from '@/components/AttributionNudge'
import { RecentPRsPanel } from '@/components/RecentPRsPanel'
import { MyAccountSection, personalTokenTestOkMessage } from '@/views/settings/MyAccountSection'

const conn = vi.hoisted(() => ({ provider: null as string | null }))

vi.mock('@/hooks/useConnections', () => ({
  useConnectionsOptional: () =>
    conn.provider === null
      ? null
      : {
          connections: [{ name: 'main', git_provider: conn.provider }],
          activeConnection: 'main',
          loading: false,
          error: null,
          setActiveConnection: vi.fn(),
          refreshConnections: vi.fn(),
        },
}))

vi.mock('@/services/api', () => ({
  refreshPR: vi.fn(),
  api: {
    getMe: vi.fn().mockResolvedValue({ username: 'admin', has_github_token: true }),
    setMyGitHubToken: vi.fn(),
    clearMyGitHubToken: vi.fn(),
    testMyGitHubToken: vi.fn(),
  },
}))

beforeEach(() => {
  conn.provider = null
})

describe('gitHostLabel / personalTokenName', () => {
  it('names each known host and returns null for an unknown one', () => {
    expect(gitHostLabel('github')).toBe('GitHub')
    expect(gitHostLabel('gitea')).toBe('Gitea')
    expect(gitHostLabel('azuredevops')).toBe('Azure DevOps')
    expect(gitHostLabel('')).toBeNull()
    expect(gitHostLabel(undefined)).toBeNull()
  })

  it('pins the token wording by exact text', () => {
    expect(personalTokenName('Gitea')).toBe('personal Gitea token')
    expect(personalTokenName('GitHub')).toBe('personal GitHub token')
    expect(personalTokenName(null)).toBe('personal Git token')
  })

  it('pins the PR link wording by exact text', () => {
    expect(prLinkText(4, 'Gitea')).toBe('View PR #4 on Gitea')
    expect(prLinkText(4, 'GitHub')).toBe('View PR #4 on GitHub')
    expect(prLinkText(4, null)).toBe('View PR #4')
    expect(prLinkText(null, null)).toBe('View PR')
  })
})

describe('PRLink follows the Git host', () => {
  it('on Gitea says "on Gitea", never "on GitHub"', () => {
    conn.provider = 'gitea'
    render(<PRLink url="http://gitea.local/o/r/pulls/4" id={4} />)
    expect(screen.getByRole('link', { name: 'View PR #4 on Gitea' })).toBeInTheDocument()
    expect(screen.queryByText(/GitHub/)).not.toBeInTheDocument()
  })

  it('on GitHub says "on GitHub"', () => {
    conn.provider = 'github'
    render(<PRLink url="https://github.com/o/r/pull/4" id={4} />)
    expect(screen.getByRole('link', { name: 'View PR #4 on GitHub' })).toBeInTheDocument()
  })

  it('with no connection loaded stays neutral', () => {
    render(<PRLink url="https://example.com/pr/4" id={4} />)
    expect(screen.getByRole('link', { name: 'View PR #4' })).toBeInTheDocument()
  })
})

describe('AttributionNudge follows the Git host', () => {
  it('on Gitea asks for a personal Gitea token, never a GitHub PAT', () => {
    conn.provider = 'gitea'
    render(
      <MemoryRouter>
        <AttributionNudge inline />
      </MemoryRouter>,
    )
    expect(screen.getByRole('link', { name: /Set up your personal Gitea token/ })).toBeInTheDocument()
    expect(screen.queryByText(/GitHub/)).not.toBeInTheDocument()
    expect(screen.queryByText(/PAT/)).not.toBeInTheDocument()
  })

  it('full banner on Gitea', () => {
    conn.provider = 'gitea'
    render(
      <MemoryRouter>
        <AttributionNudge />
      </MemoryRouter>,
    )
    expect(screen.getByText('No personal Gitea token set up')).toBeInTheDocument()
    expect(screen.queryByText(/GitHub/)).not.toBeInTheDocument()
  })

  it('on GitHub asks for a personal GitHub token', () => {
    conn.provider = 'github'
    render(
      <MemoryRouter>
        <AttributionNudge inline />
      </MemoryRouter>,
    )
    expect(screen.getByRole('link', { name: /Set up your personal GitHub token/ })).toBeInTheDocument()
    expect(screen.queryByText(/PAT/)).not.toBeInTheDocument()
  })
})

describe('RecentPRsPanel "view all" link follows the Git host', () => {
  const load = () =>
    Promise.resolve({ entries: [], values_file: 'values/global/x.yaml', view_all_url: 'http://gitea.local/o/r/pulls' })

  it('on Gitea says "View all on Gitea"', async () => {
    conn.provider = 'gitea'
    render(<RecentPRsPanel load={load} />)
    expect(await screen.findByText('View all on Gitea')).toBeInTheDocument()
    expect(screen.queryByText(/GitHub/)).not.toBeInTheDocument()
  })

  it('on GitHub says "View all on GitHub"', async () => {
    conn.provider = 'github'
    render(<RecentPRsPanel load={load} />)
    expect(await screen.findByText('View all on GitHub')).toBeInTheDocument()
  })
})

describe('My Account token section follows the Git host', () => {
  it('on Gitea: Gitea heading, no GitHub token hints, and the Test button (U9c)', async () => {
    conn.provider = 'gitea'
    render(<MyAccountSection />)
    expect(await screen.findByText('Personal Gitea token')).toBeInTheDocument()
    expect(screen.queryByText(/GitHub/)).not.toBeInTheDocument()
    expect(screen.queryByPlaceholderText(/ghp_/)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Test' })).toBeInTheDocument()
  })

  it('the Test success line names the host when there is no GitHub login (U9c)', () => {
    expect(personalTokenTestOkMessage(undefined, 'Gitea')).toBe('Token works: it can read the GitOps repo on Gitea')
    expect(personalTokenTestOkMessage('octocat', 'GitHub')).toBe('Token valid (GitHub login: octocat)')
    expect(personalTokenTestOkMessage(undefined, 'Gitea')).not.toMatch(/GitHub/)
    expect(personalTokenTestOkMessage(undefined, null)).toBe('Token works: it can read the GitOps repo on your Git host')
  })

  it('on GitHub: GitHub heading, GitHub hints and the Test button', async () => {
    conn.provider = 'github'
    render(<MyAccountSection />)
    expect(await screen.findByText('Personal GitHub token')).toBeInTheDocument()
    expect(screen.getByPlaceholderText(/ghp_/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Test' })).toBeInTheDocument()
  })
})

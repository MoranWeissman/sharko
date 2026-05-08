// FirstRunWizard.test.tsx — V124-6.4 / BUG-024 regression coverage.
//
// When the wizard is mounted with `initialStep={4}` (resume mode triggered
// by App.tsx detecting an existing connection but un-initialized repo), the
// header MUST drop the "Step N of M" counter. Showing "Step 4 of 4 —
// Initialize" makes it look like steps 1-3 vanished, which the maintainer's
// 2026-05-08 walkthrough flagged as confusing.

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { FirstRunWizard, detectGitProvider } from '@/components/FirstRunWizard'

vi.mock('@/hooks/useConnections', () => ({
  useConnections: () => ({
    connections: [],
    activeConnection: null,
    setActiveConnection: vi.fn(),
    loading: false,
    error: null,
    refreshConnections: vi.fn(),
  }),
}))

vi.mock('@/services/api', () => ({
  api: {
    testGitConnection: vi.fn().mockResolvedValue({ ok: true }),
    testArgocdConnection: vi.fn().mockResolvedValue({ ok: true }),
    saveConnection: vi.fn().mockResolvedValue({ ok: true }),
  },
  initRepo: vi.fn().mockResolvedValue({ operation_id: 'op-1' }),
  getOperation: vi.fn().mockResolvedValue({
    id: 'op-1',
    status: 'pending',
    steps: [],
  }),
  operationHeartbeat: vi.fn().mockResolvedValue({}),
}))

function renderWizard(initialStep?: number) {
  return render(
    <MemoryRouter>
      {initialStep !== undefined ? (
        <FirstRunWizard initialStep={initialStep} />
      ) : (
        <FirstRunWizard />
      )}
    </MemoryRouter>,
  )
}

// V124-10 / BUG-028 — detectGitProvider unit tests.
//
// The wizard's `buildPayload` calls detectGitProvider(form.git_url) and the
// resulting value flows into the JSON body sent to POST /api/v1/connections/.
// `undefined` is dropped by JSON.stringify and the backend's auto-derive
// takes over; recognized hosts populate `git.provider` so the wizard's
// "Test Connection" round-trip carries the value verbatim.
describe('detectGitProvider', () => {
  it('returns "github" for github.com', () => {
    expect(detectGitProvider('https://github.com/foo/bar')).toBe('github')
  })

  it('returns "github" for *.github.com (e.g. api.github.com)', () => {
    expect(detectGitProvider('https://api.github.com/foo/bar')).toBe('github')
  })

  it('returns "azuredevops" for dev.azure.com', () => {
    expect(
      detectGitProvider('https://dev.azure.com/myorg/myproj/_git/myrepo'),
    ).toBe('azuredevops')
  })

  it('returns "azuredevops" for legacy *.visualstudio.com', () => {
    expect(
      detectGitProvider('https://myorg.visualstudio.com/myproj/_git/myrepo'),
    ).toBe('azuredevops')
  })

  it('returns undefined for unsupported hosts (gitlab.com)', () => {
    expect(detectGitProvider('https://gitlab.com/foo/bar')).toBeUndefined()
  })

  it('returns undefined for unsupported hosts (bitbucket.org)', () => {
    expect(detectGitProvider('https://bitbucket.org/foo/bar')).toBeUndefined()
  })

  it('returns undefined for malformed URLs (URL constructor throws)', () => {
    expect(detectGitProvider('not-a-url')).toBeUndefined()
  })

  it('returns undefined for empty input', () => {
    expect(detectGitProvider('')).toBeUndefined()
  })

  it('is case-insensitive on the host portion', () => {
    expect(detectGitProvider('https://GitHub.COM/foo/bar')).toBe('github')
    expect(detectGitProvider('https://DEV.AZURE.COM/o/p/_git/r')).toBe(
      'azuredevops',
    )
  })
})

describe('FirstRunWizard step header', () => {
  it('shows "Step 1 of 4 — Welcome" when started fresh from step 1', () => {
    renderWizard(1)
    const label = screen.getByTestId('wizard-step-label')
    expect(label.textContent).toBe('Step 1 of 4 — Welcome')
  })

  it('shows "Step 1 of 4 — Welcome" with no initialStep prop (default = 1)', () => {
    renderWizard()
    const label = screen.getByTestId('wizard-step-label')
    expect(label.textContent).toBe('Step 1 of 4 — Welcome')
  })

  it('drops the "Step N of M" counter when resumed at step 4', () => {
    // V124-6.4: resume-mode header reads "Resuming setup — Initialize"
    // because steps 1-3 were completed in a prior session. The "Step 4 of 4"
    // counter would imply the user just clicked through 1-3 in this session,
    // which they did not.
    renderWizard(4)
    const label = screen.getByTestId('wizard-step-label')
    expect(label.textContent).toBe('Resuming setup — Initialize')
    // Defensive: explicitly assert the counter substring is absent.
    expect(label.textContent).not.toContain('Step 4 of 4')
  })
})

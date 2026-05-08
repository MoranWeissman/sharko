// FirstRunWizard.test.tsx — V124-6.4 / BUG-024 regression coverage.
//
// When the wizard is mounted with `initialStep={4}` (resume mode triggered
// by App.tsx detecting an existing connection but un-initialized repo), the
// header MUST drop the "Step N of M" counter. Showing "Step 4 of 4 —
// Initialize" makes it look like steps 1-3 vanished, which the maintainer's
// 2026-05-08 walkthrough flagged as confusing.
//
// V124-14 / BUG-032 also adds: when the backend marks an init op `failed`
// with a descriptive error (e.g. "argocd application 'cluster-addons-bootstrap'
// did not reach synced state: timeout: ..."), the wizard MUST render that
// error string verbatim instead of "Repository initialized successfully."

import { describe, it, expect, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { FirstRunWizard, detectGitProvider } from '@/components/FirstRunWizard'
import * as apiModule from '@/services/api'

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

vi.mock('@/services/api', () => {
  // V124-15 / BUG-033: re-declare the typed error class inside the mock so
  // tests that throw a 401-shaped error from `getOperation` exercise the
  // real `isUnauthorizedError` path. Vitest hoists vi.mock; importing the
  // real class would create a circular reference.
  class OperationApiError extends Error {
    status: number
    constructor(message: string, status: number) {
      super(message)
      this.name = 'OperationApiError'
      this.status = status
    }
  }
  return {
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
    OperationApiError,
    // Mirror the real isUnauthorizedError predicate so the wizard's catch
    // block treats the mocked OperationApiError(401) as fatal.
    isUnauthorizedError: (err: unknown) => {
      if (err instanceof OperationApiError) return err.status === 401
      if (err instanceof Error) {
        return /\b401\b|unauthorized|unauthenticated|session expired/i.test(
          err.message,
        )
      }
      return false
    },
  }
})

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

// V124-14 / BUG-032 — wizard surfaces operation `failed` errors verbatim.
//
// When the backend's runInitOperation Fail()s the session because the
// ArgoCD root app never reaches Synced, the operation polling endpoint
// returns `{ status: 'failed', error: 'argocd application "cluster-addons-bootstrap"
// did not reach synced state: timeout: sync verification timed out after 2m0s' }`.
//
// The wizard MUST display that error string verbatim — anything more
// generic (or worse, "Repository initialized successfully") would put the
// user back into the silent-failure trap that BUG-032 created.
describe('FirstRunWizard step 4 — sync-failure surfacing (V124-14 / BUG-032)', () => {
  it('renders the backend failure error string when operation status=failed', async () => {
    const failureError =
      'argocd application "cluster-addons-bootstrap" did not reach synced state: timeout: sync verification timed out after 2m0s'

    // Drive the polling effect: first poll returns running, second returns failed.
    const initRepoMock = apiModule.initRepo as ReturnType<typeof vi.fn>
    const getOperationMock = apiModule.getOperation as ReturnType<typeof vi.fn>
    initRepoMock.mockResolvedValueOnce({ operation_id: 'op-failure-1' })
    getOperationMock.mockResolvedValue({
      id: 'op-failure-1',
      status: 'failed',
      error: failureError,
      steps: [],
    })

    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      renderWizard(4)

      // Click "Initialize & Auto-merge" to kick off init.
      const initBtn = screen.getByRole('button', { name: /Initialize.*Auto-merge/i })
      fireEvent.click(initBtn)

      // Let the initRepo promise resolve (microtasks).
      await act(async () => {
        await Promise.resolve()
      })

      // Advance past the 2s polling interval so the polled getOperation runs.
      await act(async () => {
        vi.advanceTimersByTime(2100)
        await Promise.resolve()
      })

      await waitFor(() => {
        expect(screen.getByText(failureError)).toBeInTheDocument()
      })

      // Belt-and-suspenders: success message must NOT be shown.
      expect(
        screen.queryByText(/Repository initialized successfully/i),
      ).not.toBeInTheDocument()
    } finally {
      vi.useRealTimers()
      // Reset the default mock so it doesn't leak into other tests.
      getOperationMock.mockResolvedValue({
        id: 'op-1',
        status: 'pending',
        steps: [],
      })
      initRepoMock.mockResolvedValue({ operation_id: 'op-1' })
    }
  })
})

// V124-15 / BUG-033 — wizard surfaces 401 (session expired) during polling.
//
// Pre-V124-15, the wizard's polling useEffect had a blanket `catch {}` that
// swallowed every error including 401s. When the user's auth token expired
// mid-init (real reproducer: 5-minute disk-full recovery window), every poll
// silently 401'd while the wizard kept rendering the last-known step list,
// looking frozen with no way out. The fix:
//   1. `getOperation` now throws an OperationApiError with status=401.
//   2. The wizard's catch block detects 401 via `isUnauthorizedError`,
//      stops both polling and heartbeat intervals, sets state=error, and
//      renders "Session expired — please log in again." plus a Log in
//      again button.
//   3. Other transients (network blips, 5xx) keep the swallow-and-retry
//      behavior — only 401 is fatal.
describe('FirstRunWizard step 4 — 401 during polling (V124-15 / BUG-033)', () => {
  it('surfaces a 401 as "Session expired", stops polling, and shows Log in again', async () => {
    const initRepoMock = apiModule.initRepo as ReturnType<typeof vi.fn>
    const getOperationMock = apiModule.getOperation as ReturnType<typeof vi.fn>
    initRepoMock.mockResolvedValueOnce({ operation_id: 'op-401' })

    // The mock's OperationApiError is exposed on the mocked module so we
    // can throw the real shape the wizard expects.
    const ErrCtor = (apiModule as unknown as {
      OperationApiError: new (msg: string, status: number) => Error & { status: number }
    }).OperationApiError
    getOperationMock.mockImplementation(async () => {
      throw new ErrCtor('Unauthorized: session expired', 401)
    })

    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      renderWizard(4)

      const initBtn = screen.getByRole('button', {
        name: /Initialize.*Auto-merge/i,
      })
      fireEvent.click(initBtn)

      // Resolve the initRepo promise.
      await act(async () => {
        await Promise.resolve()
      })

      // First poll tick — getOperation throws 401.
      await act(async () => {
        vi.advanceTimersByTime(2100)
        await Promise.resolve()
      })

      // The wizard should now show the session-expired message + button.
      await waitFor(() => {
        expect(
          screen.getByText(/Session expired — please log in again\./i),
        ).toBeInTheDocument()
      })
      expect(
        screen.getByRole('button', { name: /Log in again/i }),
      ).toBeInTheDocument()

      // Polling MUST be stopped — record the call count, advance time
      // well past several poll intervals, and verify it didn't grow.
      const callsAfterFirst401 = getOperationMock.mock.calls.length
      await act(async () => {
        vi.advanceTimersByTime(10_000)
        await Promise.resolve()
      })
      expect(getOperationMock.mock.calls.length).toBe(callsAfterFirst401)

      // Belt-and-suspenders: the success message must NOT appear.
      expect(
        screen.queryByText(/Repository initialized successfully/i),
      ).not.toBeInTheDocument()
    } finally {
      vi.useRealTimers()
      // Reset mocks so this test doesn't leak its 401-throwing behavior.
      getOperationMock.mockReset()
      getOperationMock.mockResolvedValue({
        id: 'op-1',
        status: 'pending',
        steps: [],
      })
      initRepoMock.mockResolvedValue({ operation_id: 'op-1' })
    }
  })
})

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

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { FirstRunWizard, detectGitProvider } from '@/components/FirstRunWizard'
import * as apiModule from '@/services/api'

// V124-16: per-test override for the connections mock so the resume-mode
// tests (BUG-037 / BUG-038) can simulate "an existing connection is loaded"
// without rewriting the whole mock graph. vi.hoisted runs before vi.mock
// hoisting, so the holder is safe to reference inside the mock factory.
const mockState = vi.hoisted(() => ({
  connections: [] as Array<Record<string, unknown>>,
  refreshConnectionsSpy: undefined as ReturnType<typeof vi.fn> | undefined,
}))

vi.mock('@/hooks/useConnections', () => ({
  useConnections: () => ({
    connections: mockState.connections,
    activeConnection: null,
    setActiveConnection: vi.fn(),
    loading: false,
    error: null,
    // Recreate the spy on first read of each test so per-test refresh-call
    // assertions don't bleed across tests.
    refreshConnections:
      mockState.refreshConnectionsSpy ??
      (mockState.refreshConnectionsSpy = vi.fn()),
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
      // V124-16 / BUG-037+038: full surface of wizard-relevant connection
      // calls. testCredentials drives StepGit/StepArgoCD test buttons,
      // createConnection / updateConnection are the save paths, and
      // deleteConnection is what the new "Clear all configuration" link
      // hits. discoverArgocd is touched on step-3 entry.
      testCredentials: vi.fn().mockResolvedValue({
        git: { status: 'ok' },
        argocd: { status: 'ok' },
      }),
      createConnection: vi.fn().mockResolvedValue({ status: 'created' }),
      updateConnection: vi.fn().mockResolvedValue({ status: 'updated' }),
      deleteConnection: vi.fn().mockResolvedValue({ status: 'deleted' }),
      discoverArgocd: vi
        .fn()
        .mockResolvedValue({ server_url: '', has_env_token: false, namespace: 'argocd' }),
      // Legacy shims kept for older tests that may still reference them.
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

// V124-16: wipe the dismiss-flag and reset the configurable connections
// mock between tests so resume-mode setup can't leak into fresh-mode tests.
beforeEach(() => {
  sessionStorage.clear()
  mockState.connections = []
  mockState.refreshConnectionsSpy?.mockClear()
})

afterEach(() => {
  sessionStorage.clear()
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

// V124-16 / BUG-035..038 — escape hatches + back-navigation in resume mode.
//
// Resume mode is when App.tsx mounts <FirstRunWizard initialStep={4} />
// because at least one connection exists but the repo is not yet
// initialized. Pre-V124-16 the wizard had three coupled issues that turned
// resume mode into a soft-lock:
//   - X button navigated, but App.tsx re-rendered the wizard immediately
//   - StepInit had no Back button (no way to revisit Git/ArgoCD config)
//   - No way to clear Sharko state and start from scratch
//   - The footer copy lied ("Settings" — but Settings is route-gated away)
//
// Each test below covers one bug.

// Helper — a representative resume-mode connection. Mirrors the shape that
// `useConnections` returns from the real ConnectionProvider.
const resumeConnection = {
  name: 'github-foo-bar',
  description: '',
  git_provider: 'github',
  git_repo_identifier: 'foo/bar',
  git_token_masked: '****',
  argocd_server_url: 'https://argocd.example.com',
  argocd_token_masked: '****',
  argocd_namespace: 'argocd',
  is_default: true,
  is_active: true,
  provider: { type: 'k8s-secrets', region: '', prefix: '' },
}

describe('FirstRunWizard — V124-16 escape hatches (BUG-035 / 036 / 037 / 038)', () => {
  it('BUG-035: X button writes the dismiss flag so App.tsx skips the resume gate', () => {
    mockState.connections = [resumeConnection]
    renderWizard(4)

    expect(sessionStorage.getItem('sharko:dismiss-wizard')).toBeNull()

    // The X button uses title="Skip to Dashboard" and an icon child; query
    // it by its accessible title attribute.
    const xButton = screen.getByTitle('Skip to Dashboard')
    fireEvent.click(xButton)

    // Flag is set so App.tsx's wizard gate (which reads
    // sessionStorage.getItem('sharko:dismiss-wizard') === '1') skips
    // re-rendering the wizard for the rest of the session. A fresh tab
    // clears it automatically — the dismiss is "for now", not forever.
    expect(sessionStorage.getItem('sharko:dismiss-wizard')).toBe('1')
  })

  it('BUG-036: footer copy in resume mode points at in-wizard controls (not Settings)', () => {
    mockState.connections = [resumeConnection]
    renderWizard(4)

    expect(
      screen.getByText(
        /Initialize the repository to continue, or use the controls above to edit or reset\./i,
      ),
    ).toBeInTheDocument()
    // The fresh-mode copy must NOT appear in resume mode — Settings is
    // hard-gated by App.tsx so claiming "later in Settings" is misleading.
    expect(
      screen.queryByText(/update connections later in Settings/i),
    ).not.toBeInTheDocument()
  })

  it('BUG-036: footer copy in fresh mode keeps the Settings hint', () => {
    mockState.connections = []
    renderWizard(1)

    expect(
      screen.getByText(/You can always update connections later in Settings\./i),
    ).toBeInTheDocument()
  })

  it('BUG-038: StepInit shows a Back button in resume mode that walks back through step 3 → step 2', async () => {
    mockState.connections = [resumeConnection]
    renderWizard(4)

    // The wizard should render StepInit. Use the "Initialize Repository"
    // heading inside StepInit as a step-4 sentinel — multiple "Resuming
    // setup" strings render in resume mode (header label + StepInit
    // banner) so a generic match is ambiguous.
    expect(
      screen.getByRole('heading', { name: /Initialize Repository/i }),
    ).toBeInTheDocument()

    // The Back button is the only "Back" button in StepInit's idle state.
    const backFromStep4 = screen.getByRole('button', { name: /^Back$/ })
    fireEvent.click(backFromStep4)

    // Now on step 3 (StepArgoCD). The "Save & Continue" button is unique
    // to StepArgoCD so use it as a step-3 sentinel.
    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: /Save & Continue/i }),
      ).toBeInTheDocument()
    })

    // StepArgoCD has its own Back button → walk back to step 2.
    const backFromStep3 = screen.getByRole('button', { name: /^Back$/ })
    fireEvent.click(backFromStep3)

    // On step 2 (StepGit), the form should be pre-populated with the
    // existing connection's git URL — verify by reading the input value.
    await waitFor(() => {
      const gitUrlInput = screen.getByPlaceholderText(
        /https:\/\/github\.com\/your-org\/your-repo/i,
      ) as HTMLInputElement
      expect(gitUrlInput.value).toBe('https://github.com/foo/bar')
    })
  })

  it('BUG-037: step 2 in resume mode shows "Clear all configuration" and DELETEs on confirm', async () => {
    mockState.connections = [resumeConnection]
    renderWizard(4)

    // Walk back from step 4 → step 3 → step 2.
    fireEvent.click(screen.getByRole('button', { name: /^Back$/ }))
    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: /Save & Continue/i }),
      ).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('button', { name: /^Back$/ }))

    // The clear-config link is rendered only when an existing connection
    // is loaded — assert visibility.
    const clearBtn = await screen.findByRole('button', {
      name: /Clear all configuration and start over/i,
    })
    expect(clearBtn).toBeInTheDocument()

    // Confirm dialog: stub window.confirm to return true and verify the
    // DELETE call goes out for the loaded connection.
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)
    const deleteMock = (apiModule.api as unknown as {
      deleteConnection: ReturnType<typeof vi.fn>
    }).deleteConnection
    deleteMock.mockClear()

    fireEvent.click(clearBtn)

    await waitFor(() => {
      expect(deleteMock).toHaveBeenCalledWith('github-foo-bar')
    })
    expect(confirmSpy).toHaveBeenCalledTimes(1)

    confirmSpy.mockRestore()
  })

  it('BUG-037: step 2 in resume mode does NOT delete when user cancels the confirm', async () => {
    mockState.connections = [resumeConnection]
    renderWizard(4)

    // Walk back to step 2.
    fireEvent.click(screen.getByRole('button', { name: /^Back$/ }))
    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: /Save & Continue/i }),
      ).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('button', { name: /^Back$/ }))

    const clearBtn = await screen.findByRole('button', {
      name: /Clear all configuration and start over/i,
    })

    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false)
    const deleteMock = (apiModule.api as unknown as {
      deleteConnection: ReturnType<typeof vi.fn>
    }).deleteConnection
    deleteMock.mockClear()

    fireEvent.click(clearBtn)

    // Cancellation: confirm fires, but DELETE never goes out.
    expect(confirmSpy).toHaveBeenCalledTimes(1)
    expect(deleteMock).not.toHaveBeenCalled()

    confirmSpy.mockRestore()
  })
})

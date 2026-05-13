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

// V124-17 / BUG-039 + BUG-040 + BUG-042 — wizard polish for resume mode.
//
// Three issues from the V124-16 maintainer validation:
//   - BUG-039: Back button on StepInit was visible during the 'running'
//     state. Clicking it mid-flight rewound the wizard chrome but the init
//     operation kept running on the backend; a later Initialize click then
//     hit an already-init check and could orphan the session.
//   - BUG-040: in resume mode, the password/token inputs render empty by
//     design (security: never re-display saved secrets). The previous
//     placeholder ("Personal access token (PAT)") read as "my saved
//     credential just got wiped" — the backend's PUT /connections/{name}
//     actually preserves saved tokens when the request omits them, so the
//     data is safe but the UX is misleading.
//   - BUG-042: StepInit's Back button uses the same secondary-button
//     classes as StepGit/StepArgoCD's Back so the affordance looks the
//     same on every step.
describe('FirstRunWizard — V124-17 wizard polish (BUG-039 / 040 / 042)', () => {
  it('BUG-039: Back button is hidden in StepInit while state is running', async () => {
    mockState.connections = [resumeConnection]

    // initRepo resolves with operation_id so the polling effect kicks in
    // and state stays in 'running' until getOperation returns a terminal
    // status. We never advance getOperation past 'running' here so we
    // can assert the running-state UI.
    const initRepoMock = apiModule.initRepo as ReturnType<typeof vi.fn>
    const getOperationMock = apiModule.getOperation as ReturnType<typeof vi.fn>
    initRepoMock.mockResolvedValueOnce({ operation_id: 'op-running-1' })
    getOperationMock.mockResolvedValue({
      id: 'op-running-1',
      status: 'running',
      steps: [{ name: 'Creating bootstrap files', status: 'running' }],
    })

    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      renderWizard(4)

      // Idle state — Back IS present (V124-16 behaviour, baseline).
      expect(screen.getByRole('button', { name: /^Back$/ })).toBeInTheDocument()

      // Click Initialize — wizard transitions to 'running'.
      fireEvent.click(
        screen.getByRole('button', { name: /Initialize.*Auto-merge/i }),
      )

      // Resolve the initRepo promise so setOperationId fires and the
      // polling effect kicks in.
      await act(async () => {
        await Promise.resolve()
      })

      // While the operation is running, Back must NOT be in the document.
      // BUG-039 root cause: the V124-16 conditional was `{resumed && onBack
      // && (<button>Back</button>)}` — no state guard.
      await waitFor(() => {
        expect(
          screen.queryByRole('button', { name: /^Back$/ }),
        ).not.toBeInTheDocument()
      })

      // Belt-and-suspenders: the running indicator IS visible.
      expect(
        screen.getByText(/Starting initialization|Creating bootstrap files/i),
      ).toBeInTheDocument()
    } finally {
      vi.useRealTimers()
      getOperationMock.mockResolvedValue({
        id: 'op-1',
        status: 'pending',
        steps: [],
      })
      initRepoMock.mockResolvedValue({ operation_id: 'op-1' })
    }
  })

  it('BUG-040: in resume mode, the GitHub token input shows the saved-credential placeholder + helper text', async () => {
    mockState.connections = [resumeConnection]
    renderWizard(4)

    // Walk back to step 2 (StepGit).
    fireEvent.click(screen.getByRole('button', { name: /^Back$/ }))
    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: /Save & Continue/i }),
      ).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('button', { name: /^Back$/ }))

    // The placeholder MUST advertise the saved-credential semantic. We
    // grep on the bullet+saved phrase rather than the legacy "Personal
    // access token (PAT)" string.
    await waitFor(() => {
      expect(
        screen.getByPlaceholderText(
          /•••••• \(saved — leave blank to keep, or enter new value to replace\)/,
        ),
      ).toBeInTheDocument()
    })

    // The helper text below the input also explains the merge semantic.
    expect(
      screen.getByText(
        /Submitting blank keeps your saved credential\. Enter a new value to replace it\./,
      ),
    ).toBeInTheDocument()
  })

  it('BUG-040: in resume mode, the ArgoCD token input shows the saved-credential placeholder + helper text', async () => {
    mockState.connections = [resumeConnection]
    renderWizard(4)

    // Walk back to step 3 (StepArgoCD).
    fireEvent.click(screen.getByRole('button', { name: /^Back$/ }))

    await waitFor(() => {
      // ArgoCD token field has the same saved-credential placeholder.
      const inputs = screen.getAllByPlaceholderText(
        /•••••• \(saved — leave blank to keep, or enter new value to replace\)/,
      )
      // At least the ArgoCD token input is present on step 3.
      expect(inputs.length).toBeGreaterThanOrEqual(1)
    })

    // Helper text appears for the ArgoCD token (one or both inputs may
    // render this on step 3, depending on how the test reaches step 3 —
    // we walked back from step 4 → step 3, so only StepArgoCD is mounted
    // and exactly one helper-text occurrence is expected).
    expect(
      screen.getByText(
        /Submitting blank keeps your saved credential\. Enter a new value to replace it\./,
      ),
    ).toBeInTheDocument()
  })

  it('BUG-040: blank-submit in resume mode sends an empty token (server-side PUT preserves saved value)', async () => {
    mockState.connections = [resumeConnection]
    renderWizard(4)

    // Walk to step 3 (StepArgoCD), where Save & Continue lives.
    fireEvent.click(screen.getByRole('button', { name: /^Back$/ }))
    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: /Save & Continue/i }),
      ).toBeInTheDocument()
    })

    // Test the ArgoCD connection so canSave gates open.
    fireEvent.click(screen.getByRole('button', { name: /Test Connection/i }))
    await waitFor(() => {
      expect(screen.getByText(/Connected/i)).toBeInTheDocument()
    })

    // Save without typing a token — should hit updateConnection (PUT) with
    // an empty/undefined token field. The wizard's buildPayload coerces
    // empty strings to undefined, which JSON.stringify drops.
    const updateMock = (apiModule.api as unknown as {
      updateConnection: ReturnType<typeof vi.fn>
    }).updateConnection
    updateMock.mockClear()

    fireEvent.click(screen.getByRole('button', { name: /Save & Continue/i }))

    await waitFor(() => {
      expect(updateMock).toHaveBeenCalledTimes(1)
    })

    // First arg is the connection name, second is the payload object.
    const [name, payload] = updateMock.mock.calls[0]
    expect(name).toBe('github-foo-bar')

    // Both tokens must be undefined-or-absent so the backend's
    // token-preserving merge keeps the saved values.
    expect(payload.git.token).toBeUndefined()
    expect(payload.argocd.token).toBeUndefined()
  })

  it('BUG-040: typing a new token in resume mode sends it through to the PUT body', async () => {
    mockState.connections = [resumeConnection]
    renderWizard(4)

    // Walk to step 2 (StepGit) so we can type into the Git token field.
    fireEvent.click(screen.getByRole('button', { name: /^Back$/ }))
    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: /Save & Continue/i }),
      ).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('button', { name: /^Back$/ }))

    // Find the GitHub token field by its saved-credential placeholder
    // and type a new value into it.
    const tokenInput = await screen.findByPlaceholderText(
      /•••••• \(saved — leave blank to keep, or enter new value to replace\)/,
    )
    fireEvent.change(tokenInput, { target: { value: 'new-pat-12345' } })

    // Test git, advance to step 3, save.
    fireEvent.click(screen.getByRole('button', { name: /Test Connection/i }))
    await waitFor(() => {
      expect(screen.getByText(/Connected/i)).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('button', { name: /Next/i }))

    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: /Save & Continue/i }),
      ).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('button', { name: /Test Connection/i }))
    await waitFor(() => {
      expect(screen.getByText(/Connected/i)).toBeInTheDocument()
    })

    const updateMock = (apiModule.api as unknown as {
      updateConnection: ReturnType<typeof vi.fn>
    }).updateConnection
    updateMock.mockClear()

    fireEvent.click(screen.getByRole('button', { name: /Save & Continue/i }))

    await waitFor(() => {
      expect(updateMock).toHaveBeenCalledTimes(1)
    })

    const [, payload] = updateMock.mock.calls[0]
    expect(payload.git.token).toBe('new-pat-12345')
  })

  it('BUG-042: StepInit Back button uses the same secondary-button classes as StepGit/StepArgoCD Back', async () => {
    mockState.connections = [resumeConnection]
    renderWizard(4)

    // Capture StepInit's Back classes (idle state).
    const stepInitBack = screen.getByRole('button', { name: /^Back$/ })
    const stepInitClasses = stepInitBack.className

    // Walk back to step 3 and capture StepArgoCD's Back classes.
    fireEvent.click(stepInitBack)
    const stepArgoCDBack = await screen.findByRole('button', { name: /^Back$/ })
    expect(stepArgoCDBack.className).toBe(stepInitClasses)

    // Walk back to step 2 and capture StepGit's Back classes.
    fireEvent.click(stepArgoCDBack)
    const stepGitBack = await screen.findByRole('button', { name: /^Back$/ })
    expect(stepGitBack.className).toBe(stepInitClasses)
  })
})

// V124-19 / BUG-044 — Test Connection must honor use_saved for blank-keep.
//
// V124-17 surfaced the saved-credential placeholder ("leave blank to keep,
// or enter new value to replace") on Step 2 + Step 3. V124-19 closes the
// matching backend contract gap: the wizard now sends `use_saved: true`
// (with the loaded connection name) whenever the user clicks Test
// Connection in resume mode WITHOUT typing a fresh token. The backend
// fetches the saved credentials server-side and tests with those, so the
// Next gate enables on a successful test against saved creds — no
// "argocd token not configured" rejection.
describe('FirstRunWizard — V124-19 use_saved test-credential contract (BUG-044)', () => {
  it('sends use_saved=true on Test Connection when in resume mode with blank git token', async () => {
    mockState.connections = [resumeConnection]
    renderWizard(4)

    // Walk back from step 4 → step 3 → step 2 (StepGit).
    fireEvent.click(screen.getByRole('button', { name: /^Back$/ }))
    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: /Save & Continue/i }),
      ).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('button', { name: /^Back$/ }))

    // Sanity: the saved-credential placeholder is showing (StepGit's git
    // token field renders empty in resume mode by design).
    await screen.findByPlaceholderText(
      /•••••• \(saved — leave blank to keep, or enter new value to replace\)/,
    )

    const testCredsMock = (apiModule.api as unknown as {
      testCredentials: ReturnType<typeof vi.fn>
    }).testCredentials
    testCredsMock.mockClear()

    // Click Test Connection without typing anything into the token field.
    fireEvent.click(screen.getByRole('button', { name: /Test Connection/i }))

    await waitFor(() => {
      expect(testCredsMock).toHaveBeenCalledTimes(1)
    })

    // The body sent to the backend MUST carry `use_saved: true` AND the
    // loaded connection name so the backend can look it up. The git.token
    // field in the body is omitted (undefined) — that's the contract:
    // we don't ship the (empty) form value, we tell the backend to use
    // its own.
    const [body] = testCredsMock.mock.calls[0]
    expect(body.use_saved).toBe(true)
    expect(body.name).toBe('github-foo-bar')
    expect(body.git?.token).toBeUndefined()
  })

  it('Next button enables after a successful use_saved test (no fresh-credential entry needed)', async () => {
    mockState.connections = [resumeConnection]
    renderWizard(4)

    // Walk to StepGit.
    fireEvent.click(screen.getByRole('button', { name: /^Back$/ }))
    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: /Save & Continue/i }),
      ).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('button', { name: /^Back$/ }))

    await screen.findByPlaceholderText(
      /•••••• \(saved — leave blank to keep, or enter new value to replace\)/,
    )

    // Pre-test: Next is disabled (testStatus.git === 'idle').
    const nextBtn = screen.getByRole('button', { name: /Next/i })
    expect(nextBtn).toBeDisabled()

    // Mock returns ok for both services (the default mock).
    fireEvent.click(screen.getByRole('button', { name: /Test Connection/i }))

    // After the use_saved test resolves, the Connected indicator + an
    // enabled Next button confirm the gate opened on saved creds alone.
    await waitFor(() => {
      expect(screen.getByText(/Connected/i)).toBeInTheDocument()
    })
    expect(screen.getByRole('button', { name: /Next/i })).toBeEnabled()
  })

  it('does NOT send use_saved when the user has typed a fresh git token (replace path)', async () => {
    mockState.connections = [resumeConnection]
    renderWizard(4)

    // Walk to StepGit.
    fireEvent.click(screen.getByRole('button', { name: /^Back$/ }))
    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: /Save & Continue/i }),
      ).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('button', { name: /^Back$/ }))

    const tokenInput = await screen.findByPlaceholderText(
      /•••••• \(saved — leave blank to keep, or enter new value to replace\)/,
    )
    // User types a replacement value.
    fireEvent.change(tokenInput, { target: { value: 'ghp_new_value' } })

    const testCredsMock = (apiModule.api as unknown as {
      testCredentials: ReturnType<typeof vi.fn>
    }).testCredentials
    testCredsMock.mockClear()

    fireEvent.click(screen.getByRole('button', { name: /Test Connection/i }))

    await waitFor(() => {
      expect(testCredsMock).toHaveBeenCalledTimes(1)
    })

    // Replace path: the new token MUST go through to the backend, and
    // use_saved MUST NOT be set (we want the backend to test the new
    // candidate, not the saved record).
    const [body] = testCredsMock.mock.calls[0]
    expect(body.use_saved).toBeUndefined()
    expect(body.git?.token).toBe('ghp_new_value')
    // The connection name is still passed through so the auto-fill path
    // can back-fill ArgoCD token if that field was left blank — that
    // pre-V124-19 behavior is preserved.
    expect(body.name).toBe('github-foo-bar')
  })

  it('typing into the token field after a successful use_saved test invalidates the test status (Next disables)', async () => {
    mockState.connections = [resumeConnection]
    renderWizard(4)

    // Walk to StepGit.
    fireEvent.click(screen.getByRole('button', { name: /^Back$/ }))
    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: /Save & Continue/i }),
      ).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('button', { name: /^Back$/ }))

    const tokenInput = await screen.findByPlaceholderText(
      /•••••• \(saved — leave blank to keep, or enter new value to replace\)/,
    )

    // Run a use_saved test → Next becomes enabled.
    fireEvent.click(screen.getByRole('button', { name: /Test Connection/i }))
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /Next/i })).toBeEnabled()
    })

    // Now the user starts typing a replacement. patchForm clears
    // testStatus on every change → Next disables until they re-test.
    fireEvent.change(tokenInput, { target: { value: 'ghp_partial' } })
    expect(screen.getByRole('button', { name: /Next/i })).toBeDisabled()
  })
})

// shouldShowSetupWizard.test.ts — V124-22 / BUG-046 wizard-gate coverage,
// updated by the 2026-08-02 scope extension (error review package 1).
//
// V124-15 made the operation framework treat "repo initialized but ArgoCD
// bootstrap missing or unhealthy" as a failure. App.tsx's wizard gate
// previously only checked `repoStatus.initialized` — so on first paint the
// user saw a dashboard splattered with errors instead of the recovery
// wizard. V124-22 closed that asymmetry by extending /repo/status with a
// `bootstrap_synced` field and broadening the gate to check both, and later
// stories (V2-cleanup-51, error review package 1) carved out more and more
// individual reasons that should NOT auto-open the wizard.
//
// LOCKED DECISION (2026-08-02): that carve-out approach was itself the bug.
// An expired ArgoCD token produced a reason the gate didn't know to exclude,
// so the wizard hijacked a working install and lied about why. The fix is
// structural, not another exception: once the repo is initialized, the
// wizard NEVER auto-opens again, for ANY reason. Every initialized-but-
// unhealthy state (missing/degraded engine app, broken Git connection,
// rejected/unreachable ArgoCD credential — all of it) is surfaced by the
// banner instead, with a button the user can click to open the repair
// screen themselves. The wizard's only remaining auto-open path is the
// genuine day-zero case: the repo was never initialized.
//
// We test the extracted helpers rather than mounting App because:
//   - both gates are pure: they only depend on (repoStatus, dismissed)
//   - App.test.tsx doesn't exist and the brief explicitly says don't
//     create one just for this story (architecture carve-out)

import { describe, it, expect } from 'vitest'
import { shouldShowSetupWizard, shouldShowConnectionErrorBanner } from '@/App'

describe('shouldShowSetupWizard', () => {
  it('returns false while repo status is still loading (null)', () => {
    // The parent ConnectedApp shows a spinner during this state — the
    // gate must not auto-flash the wizard before we know the answer.
    expect(shouldShowSetupWizard(null, false)).toBe(false)
  })

  it('returns true when repo is not initialized (existing V124-11 behaviour preserved)', () => {
    expect(
      shouldShowSetupWizard(
        { initialized: false, bootstrap_synced: false, reason: 'not_bootstrapped' },
        false,
      ),
    ).toBe(true)
  })

  it('returns false when repo is initialized AND bootstrap is healthy', () => {
    // The all-green path — wizard stays out of the way, dashboard renders.
    expect(
      shouldShowSetupWizard({ initialized: true, bootstrap_synced: true }, false),
    ).toBe(false)
  })

  // --- Locked 2026-08-02: initialized + ANY reason never auto-opens -------

  it('returns false when initialized but bootstrap is not synced, with no reason at all (scope extension)', () => {
    // Pre-scope-extension this used to auto-open (BUG-046 fix). Now the
    // banner is the only surface for it — see shouldShowConnectionErrorBanner
    // below.
    expect(
      shouldShowSetupWizard({ initialized: true, bootstrap_synced: false }, false),
    ).toBe(false)
  })

  it('returns false when initialized but the engine app is genuinely degraded (reason: bootstrap_degraded)', () => {
    expect(
      shouldShowSetupWizard(
        { initialized: true, bootstrap_synced: false, reason: 'bootstrap_degraded' },
        false,
      ),
    ).toBe(false)
  })

  it('returns false when initialized but ArgoCD cannot reach the repo (reason: bootstrap_unreachable)', () => {
    expect(
      shouldShowSetupWizard(
        { initialized: true, bootstrap_synced: false, reason: 'bootstrap_unreachable' },
        false,
      ),
    ).toBe(false)
  })

  it('returns false when initialized but ArgoCD rejected Sharko\'s token (reason: argocd_auth_failed)', () => {
    expect(
      shouldShowSetupWizard(
        { initialized: true, bootstrap_synced: false, reason: 'argocd_auth_failed' },
        false,
      ),
    ).toBe(false)
  })

  it('returns false when initialized but Sharko could not reach ArgoCD at all (reason: argocd_unreachable)', () => {
    expect(
      shouldShowSetupWizard(
        { initialized: true, bootstrap_synced: false, reason: 'argocd_unreachable' },
        false,
      ),
    ).toBe(false)
  })

  it('treats missing bootstrap_synced as "initialized, not confirmed healthy" — still no auto-wizard', () => {
    // A stale backend that omits bootstrap_synced used to route to the
    // wizard. Now "initialized" alone is enough to keep the wizard closed —
    // the banner (not the wizard) is where an unconfirmed-healthy state
    // gets surfaced.
    expect(
      shouldShowSetupWizard({ initialized: true } as { initialized: boolean }, false),
    ).toBe(false)
  })

  it('returns false when the user has dismissed the wizard via the X button (V124-16 / BUG-035)', () => {
    // sessionStorage `sharko:dismiss-wizard=1` lets the user explore the
    // dashboard for the rest of the session. A fresh tab brings the wizard
    // back so they can't permanently skip day-zero setup.
    expect(
      shouldShowSetupWizard({ initialized: false, bootstrap_synced: false }, true),
    ).toBe(false)
  })

  // V2-cleanup-50 — a BROKEN connection (TLS/transport/auth failure) must NOT
  // throw the user into the re-bootstrap wizard. The reason tag tells the gate
  // "this is an environment problem, keep the user in their working app". The
  // banner surfaces the problem separately.
  it('returns false when not initialized because of a connection_error (V2-cleanup-50)', () => {
    expect(
      shouldShowSetupWizard({ initialized: false, reason: 'connection_error' }, false),
    ).toBe(false)
  })

  it('returns false when not initialized because of no_connection (V2-cleanup-50)', () => {
    expect(
      shouldShowSetupWizard({ initialized: false, reason: 'no_connection' }, false),
    ).toBe(false)
  })

  it('returns false when not initialized because the probe itself failed (reason: error) (V2-cleanup-50)', () => {
    expect(
      shouldShowSetupWizard({ initialized: false, reason: 'error' }, false),
    ).toBe(false)
  })

  // CRITICAL GUARD — the connection-error exclusion must NOT swallow the
  // genuine day-zero wizard state — the only auto-open path left.
  it('still fires the wizard for a genuine not_bootstrapped state (guard against the exclusion swallowing real setup)', () => {
    expect(
      shouldShowSetupWizard({ initialized: false, reason: 'not_bootstrapped' }, false),
    ).toBe(true)
  })
})

describe('shouldShowConnectionErrorBanner', () => {
  it('returns false while repo status is still loading (null)', () => {
    expect(shouldShowConnectionErrorBanner(null)).toBe(false)
  })

  it('returns false when repo is initialized AND bootstrap is healthy (all-green path)', () => {
    expect(
      shouldShowConnectionErrorBanner({ initialized: true, bootstrap_synced: true }),
    ).toBe(false)
  })

  // Locked 2026-08-02: since the wizard never auto-opens post-setup, the
  // banner is the ONLY surface for every initialized-but-unhealthy reason.
  it.each([
    'bootstrap_degraded',
    'bootstrap_unreachable',
    'argocd_auth_failed',
    'argocd_unreachable',
    undefined,
  ])('fires for initialized-but-unhealthy reason=%s', (reason) => {
    expect(
      shouldShowConnectionErrorBanner({ initialized: true, bootstrap_synced: false, reason }),
    ).toBe(true)
  })

  it('fires when not initialized because of a broken connection (connection_error/no_connection/error)', () => {
    expect(
      shouldShowConnectionErrorBanner({ initialized: false, reason: 'connection_error' }),
    ).toBe(true)
    expect(
      shouldShowConnectionErrorBanner({ initialized: false, reason: 'no_connection' }),
    ).toBe(true)
    expect(
      shouldShowConnectionErrorBanner({ initialized: false, reason: 'error' }),
    ).toBe(true)
  })

  it('does NOT fire when not initialized for a genuine day-zero reason (the wizard handles it instead)', () => {
    expect(
      shouldShowConnectionErrorBanner({ initialized: false, reason: 'not_bootstrapped' }),
    ).toBe(false)
  })
})

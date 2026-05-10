// shouldShowSetupWizard.test.ts — V124-22 / BUG-046 wizard-gate coverage.
//
// V124-15 made the operation framework treat "repo initialized but ArgoCD
// bootstrap missing or unhealthy" as a failure. App.tsx's wizard gate
// previously only checked `repoStatus.initialized` — so on first paint the
// user saw a dashboard splattered with errors instead of the recovery
// wizard. V124-22 closes that asymmetry by extending /repo/status with a
// `bootstrap_synced` field and broadening the gate to check both.
//
// We test the extracted helper rather than mounting App because:
//   - the gate is pure: only depends on (repoStatus, dismissed)
//   - App.test.tsx doesn't exist and the brief explicitly says don't
//     create one just for this story (architecture carve-out)
//   - the helper's three branches map 1:1 to the user-visible behaviours
//     that V124-22 changes (initialized arm, bootstrap_synced arm, dismiss
//     escape hatch from V124-16)

import { describe, it, expect } from 'vitest'
import { shouldShowSetupWizard } from '@/App'

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

  it('returns true when repo is initialized but bootstrap is not synced (V124-22 / BUG-046 fix)', () => {
    // The BUG-046 reproducer: user wiped the GitHub repo + ran
    // `sharko-dev.sh argocd-reset`. Repo files come back via re-init, but
    // the cluster-side ArgoCD bootstrap is missing/degraded. Pre-fix the
    // dashboard rendered with errors; post-fix the wizard auto-opens.
    expect(
      shouldShowSetupWizard({ initialized: true, bootstrap_synced: false }, false),
    ).toBe(true)
  })

  it('returns false when repo is initialized AND bootstrap is healthy', () => {
    // The all-green path — wizard stays out of the way, dashboard renders.
    expect(
      shouldShowSetupWizard({ initialized: true, bootstrap_synced: true }, false),
    ).toBe(false)
  })

  it('returns false when the user has dismissed the wizard via the X button (V124-16 / BUG-035)', () => {
    // sessionStorage `sharko:dismiss-wizard=1` lets the user explore the
    // (degraded) dashboard for the rest of the session. A fresh tab brings
    // the wizard back so they can't permanently skip recovery — but
    // within a session, dismiss must win over both V124-22 arms.
    expect(
      shouldShowSetupWizard({ initialized: false, bootstrap_synced: false }, true),
    ).toBe(false)
    expect(
      shouldShowSetupWizard({ initialized: true, bootstrap_synced: false }, true),
    ).toBe(false)
  })

  it('treats missing bootstrap_synced as falsy (defensive — protects against an older backend that omits the field)', () => {
    // If a stale backend serves only {initialized: true} without the new
    // field, App.tsx must still route to the wizard rather than a half-
    // configured dashboard. Type-only guarantees from the API client are
    // not enough — the wire shape is what shows up at runtime.
    expect(
      shouldShowSetupWizard({ initialized: true } as { initialized: boolean }, false),
    ).toBe(true)
  })
})

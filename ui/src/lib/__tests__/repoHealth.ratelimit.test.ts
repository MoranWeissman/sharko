import { describe, it, expect } from 'vitest'
import { deriveSharkoRepoArrow, deriveArgoRepoArrow, GIT_LIMIT_ARROW_DETAIL } from '@/lib/repoHealth'
import { shouldShowSetupWizard, shouldShowConnectionErrorBanner } from '@/App'

// Walk 6: with GitHub's request limit used up the System page said "network,
// TLS, or auth problem" and the setup wizard / yellow bar treated it as a
// broken connection.
describe('a Git host request limit', () => {
  const repo = { initialized: false, bootstrap_synced: false, reason: 'git_rate_limited' as const }

  it('pins the sentence by exact text', () => {
    expect(GIT_LIMIT_ARROW_DETAIL).toBe(
      "Can't check right now: the Git host says Sharko has asked too often. Nothing is broken. Try again in a moment.",
    )
  })

  it('is "unknown" on both arrows, never a problem and never the old TLS sentence', () => {
    for (const v of [deriveSharkoRepoArrow(repo), deriveArgoRepoArrow(repo)]) {
      expect(v.status).toBe('unknown')
      expect(v.detail).toBe(GIT_LIMIT_ARROW_DETAIL)
      expect(v.detail).not.toMatch(/TLS|auth problem|network/)
    }
  })

  it('does not open the setup wizard and does show the bar', () => {
    expect(shouldShowSetupWizard(repo, false)).toBe(false)
    expect(shouldShowConnectionErrorBanner(repo)).toBe(true)
  })

  it('a real connection error keeps its old sentence', () => {
    const v = deriveSharkoRepoArrow({ initialized: false, reason: 'connection_error' })
    expect(v.status).toBe('degraded')
    expect(v.detail).toBe("Sharko can't reach the Git repo right now (network, TLS, or auth problem).")
  })
})

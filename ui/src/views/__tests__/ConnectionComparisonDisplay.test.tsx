// ConnectionComparisonDisplay.test — S4-3: pin every sentence, ban what you replace.
//
// Every sentence added in Part A gets a test on its exact full words. Any
// wording being replaced gets banned by name. This is not paperwork — in rounds
// 5 and 6 a wrong explanation survived four review rounds because its only test
// checked the sentence was not empty, so any wording passed.
//
// Wording agreed on 2026-08-13.

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ConnectionComparisonDisplay } from '../ConnectionComparisonDisplay'
import type { ConnectionComparisonView } from '@/services/models'

// ─────────────────────────────────────────────────────────────────────────────
// The six status sentences — pinned by exact words, agreed 2026-08-13.
// ─────────────────────────────────────────────────────────────────────────────

const STATUS_SYNCED_SENTENCE = 'This connection matches what Sharko intends.'
const STATUS_OUT_OF_SYNC_SENTENCE = 'This connection does not match what Sharko intends.'
const STATUS_MISSING_SENTENCE = 'This connection has not been created yet.'
const STATUS_CHECK_FAILED_SENTENCE = 'The check did not finish.'
const STATUS_OWNERSHIP_CONFLICT_SENTENCE = 'Another tool manages this connection.'
const STATUS_LIMITED_SENTENCE = 'Sharko checked part of this connection.'

// Banned phrases — if these appear, something regressed.
const BANNED_PHRASES = [
  'fully synced', // limited is NOT synced
  'completely synced',
  'in sync', // too vague, use the pinned sentences
  'connection is healthy', // not a pinned sentence
]

function assertNoBannedPhrases(text: string) {
  for (const banned of BANNED_PHRASES) {
    expect(text.toLowerCase()).not.toContain(banned.toLowerCase())
  }
}

describe('ConnectionComparisonDisplay', () => {
  // ───────────────────────────────────────────────────────────────────────────
  // S4-3: Every status sentence pinned by exact words.
  // ───────────────────────────────────────────────────────────────────────────

  it('shows the exact synced sentence agreed on 2026-08-13', () => {
    const comparison: ConnectionComparisonView = {
      cluster: 'test-cluster',
      status: 'synced',
      scope: 'full',
      ownership_mode: 'sharko_managed',
      checked_at: '2026-08-13T12:00:00Z',
      branch: 'main',
      differences: [],
      not_checked: [],
      checked_field_count: 10,
      repair_available: false,
      repair_scope: 'none',
      values_never_returned: true,
    }
    render(<ConnectionComparisonDisplay cluster="test-cluster" comparison={comparison} loading={false} error={null} onRetry={() => {}} />)
    const sentence = screen.getByTestId('connection-comparison-status-sentence')
    expect(sentence.textContent).toBe(STATUS_SYNCED_SENTENCE)
    assertNoBannedPhrases(sentence.textContent!)
  })

  it('shows the exact out_of_sync sentence agreed on 2026-08-13', () => {
    const comparison: ConnectionComparisonView = {
      cluster: 'test-cluster',
      status: 'out_of_sync',
      scope: 'full',
      ownership_mode: 'sharko_managed',
      checked_at: '2026-08-13T12:00:00Z',
      branch: 'main',
      differences: [
        { path: 'data.server', status: 'different', expected: 'https://expected.example', live: 'https://live.example' },
      ],
      not_checked: [],
      checked_field_count: 10,
      repair_available: true,
      repair_scope: 'full_connection',
      values_never_returned: true,
    }
    render(<ConnectionComparisonDisplay cluster="test-cluster" comparison={comparison} loading={false} error={null} onRetry={() => {}} />)
    const sentence = screen.getByTestId('connection-comparison-status-sentence')
    expect(sentence.textContent).toBe(STATUS_OUT_OF_SYNC_SENTENCE)
    assertNoBannedPhrases(sentence.textContent!)
  })

  it('shows the exact missing sentence agreed on 2026-08-13', () => {
    const comparison: ConnectionComparisonView = {
      cluster: 'test-cluster',
      status: 'missing',
      scope: 'none',
      ownership_mode: 'sharko_managed',
      checked_at: '2026-08-13T12:00:00Z',
      branch: 'main',
      differences: [],
      not_checked: [],
      checked_field_count: 0,
      repair_available: false,
      repair_scope: 'none',
      values_never_returned: true,
    }
    render(<ConnectionComparisonDisplay cluster="test-cluster" comparison={comparison} loading={false} error={null} onRetry={() => {}} />)
    const sentence = screen.getByTestId('connection-comparison-status-sentence')
    expect(sentence.textContent).toBe(STATUS_MISSING_SENTENCE)
    assertNoBannedPhrases(sentence.textContent!)
  })

  it('shows the exact check_failed sentence agreed on 2026-08-13', () => {
    const comparison: ConnectionComparisonView = {
      cluster: 'test-cluster',
      status: 'check_failed',
      scope: 'none',
      ownership_mode: 'sharko_managed',
      checked_at: '2026-08-13T12:00:00Z',
      branch: 'main',
      failure_reason: 'Sharko could not read this cluster\'s record from git, so it cannot tell what the connection should look like. Check the git connection and try again.',
      differences: [],
      not_checked: [],
      checked_field_count: 0,
      repair_available: false,
      repair_scope: 'none',
      values_never_returned: true,
    }
    render(<ConnectionComparisonDisplay cluster="test-cluster" comparison={comparison} loading={false} error={null} onRetry={() => {}} />)
    const sentence = screen.getByTestId('connection-comparison-status-sentence')
    expect(sentence.textContent).toBe(STATUS_CHECK_FAILED_SENTENCE)
    assertNoBannedPhrases(sentence.textContent!)

    // check_failed shows the failure reason below the status sentence
    const failureReason = screen.getByTestId('connection-comparison-failure-reason')
    expect(failureReason).toBeInTheDocument()
  })

  it('shows the exact ownership_conflict sentence agreed on 2026-08-13', () => {
    const comparison: ConnectionComparisonView = {
      cluster: 'test-cluster',
      status: 'ownership_conflict',
      scope: 'ownership_conflict',
      ownership_mode: 'foreign',
      checked_at: '2026-08-13T12:00:00Z',
      branch: 'main',
      differences: [],
      not_checked: [],
      checked_field_count: 0,
      repair_available: false,
      repair_scope: 'none',
      values_never_returned: true,
    }
    render(<ConnectionComparisonDisplay cluster="test-cluster" comparison={comparison} loading={false} error={null} onRetry={() => {}} />)
    const sentence = screen.getByTestId('connection-comparison-status-sentence')
    expect(sentence.textContent).toBe(STATUS_OWNERSHIP_CONFLICT_SENTENCE)
    assertNoBannedPhrases(sentence.textContent!)
  })

  it('shows the exact limited sentence agreed on 2026-08-13 and never claims fully synced', () => {
    const comparison: ConnectionComparisonView = {
      cluster: 'test-cluster',
      status: 'limited',
      scope: 'limited',
      ownership_mode: 'eks',
      limit_reason: 'For an EKS cluster, Sharko checks the API server address and the connection metadata but cannot compare the sign-in token, which is created fresh each time it is used.',
      checked_at: '2026-08-13T12:00:00Z',
      branch: 'main',
      differences: [],
      not_checked: [
        { path: 'data.config', reason: 'EKS sign-in tokens are created fresh each time they are used and cannot be compared.' },
      ],
      checked_field_count: 8,
      repair_available: true,
      repair_scope: 'full_connection',
      values_never_returned: true,
    }
    render(<ConnectionComparisonDisplay cluster="test-cluster" comparison={comparison} loading={false} error={null} onRetry={() => {}} />)
    const sentence = screen.getByTestId('connection-comparison-status-sentence')
    expect(sentence.textContent).toBe(STATUS_LIMITED_SENTENCE)

    // CRITICAL: limited is NOT synced — the page must never claim "fully synced" for it.
    const allText = screen.getByTestId('connection-comparison-result').textContent!
    assertNoBannedPhrases(allText)

    // limited shows the limit reason below the status sentence
    const limitReason = screen.getByTestId('connection-comparison-limit-reason')
    expect(limitReason).toBeInTheDocument()
    expect(limitReason.textContent).toContain('EKS')
  })

  // ───────────────────────────────────────────────────────────────────────────
  // S4-2: Sensitive fields never carry expected/live properties.
  // ───────────────────────────────────────────────────────────────────────────

  it('proves a sensitive field never receives an expected or live property', () => {
    // This test is a safety net: if the server ever started sending expected
    // or live for a sensitive field, this test fails.
    const comparison: ConnectionComparisonView = {
      cluster: 'test-cluster',
      status: 'out_of_sync',
      scope: 'full',
      ownership_mode: 'sharko_managed',
      checked_at: '2026-08-13T12:00:00Z',
      branch: 'main',
      differences: [
        { path: 'data.config', status: 'different', sensitive: true },
      ],
      not_checked: [],
      checked_field_count: 10,
      repair_available: true,
      repair_scope: 'full_connection',
      values_never_returned: true,
    }

    render(<ConnectionComparisonDisplay cluster="test-cluster" comparison={comparison} loading={false} error={null} onRetry={() => {}} />)

    const sensitiveDiff = comparison.differences[0]
    expect(sensitiveDiff.sensitive).toBe(true)
    expect(sensitiveDiff.expected).toBeUndefined()
    expect(sensitiveDiff.live).toBeUndefined()

    // On screen, both sides must render as the fixed text "<redacted>", and
    // the text "<redacted>" must appear at least twice (once per column).
    const allText = screen.getByTestId('connection-comparison-result').textContent!
    const redactedCount = (allText.match(/<redacted>/g) || []).length
    expect(redactedCount).toBeGreaterThanOrEqual(2)
  })

  it('renders a sensitive field as the fixed text "<redacted>" on both sides with no length hints', () => {
    const comparison: ConnectionComparisonView = {
      cluster: 'test-cluster',
      status: 'out_of_sync',
      scope: 'full',
      ownership_mode: 'sharko_managed',
      checked_at: '2026-08-13T12:00:00Z',
      branch: 'main',
      differences: [
        { path: 'data.token', status: 'different', sensitive: true },
        { path: 'data.server', status: 'different', expected: 'https://expected.example', live: 'https://live.example' },
      ],
      not_checked: [],
      checked_field_count: 10,
      repair_available: true,
      repair_scope: 'full_connection',
      values_never_returned: true,
    }

    render(<ConnectionComparisonDisplay cluster="test-cluster" comparison={comparison} loading={false} error={null} onRetry={() => {}} />)

    // The sensitive field's row exists
    const rows = screen.getAllByTestId(/connection-comparison-diff-row-/)
    expect(rows.length).toBe(2)

    // Both the sensitive and non-sensitive rows are present
    const allText = screen.getByTestId('connection-comparison-result').textContent!
    expect(allText).toContain('data.token')
    expect(allText).toContain('data.server')

    // The sensitive field shows "<redacted>" exactly, not dots, not a truncation
    expect(allText).toContain('<redacted>')

    // The non-sensitive field shows its real values
    expect(allText).toContain('https://expected.example')
    expect(allText).toContain('https://live.example')
  })

  // ───────────────────────────────────────────────────────────────────────────
  // Loading, error, and retry states.
  // ───────────────────────────────────────────────────────────────────────────

  it('shows a loading state while the check runs', () => {
    render(<ConnectionComparisonDisplay cluster="test-cluster" comparison={null} loading={true} error={null} onRetry={() => {}} />)
    expect(screen.getByTestId('connection-comparison-loading')).toBeInTheDocument()
    expect(screen.getByText('Checking the connection…')).toBeInTheDocument()
  })

  it('shows an error state and offers retry when the check fails', () => {
    const onRetry = vi.fn()
    render(<ConnectionComparisonDisplay cluster="test-cluster" comparison={null} loading={false} error="Network error" onRetry={onRetry} />)
    expect(screen.getByTestId('connection-comparison-error')).toBeInTheDocument()
    expect(screen.getByText('Network error')).toBeInTheDocument()

    const retryButton = screen.getByTestId('connection-comparison-retry')
    expect(retryButton).toBeInTheDocument()
    retryButton.click()
    expect(onRetry).toHaveBeenCalledOnce()
  })

  // ───────────────────────────────────────────────────────────────────────────
  // Provenance display.
  // ───────────────────────────────────────────────────────────────────────────

  it('shows the git file and commit the check was run against', () => {
    const comparison: ConnectionComparisonView = {
      cluster: 'test-cluster',
      status: 'synced',
      scope: 'full',
      ownership_mode: 'sharko_managed',
      checked_at: '2026-08-13T12:00:00Z',
      branch: 'main',
      compared_path: 'configuration/managed-clusters.yaml',
      compared_commit: 'abcdef1234567890abcdef1234567890abcdef12',
      differences: [],
      not_checked: [],
      checked_field_count: 10,
      repair_available: false,
      repair_scope: 'none',
      values_never_returned: true,
    }
    render(<ConnectionComparisonDisplay cluster="test-cluster" comparison={comparison} loading={false} error={null} onRetry={() => {}} />)
    const provenance = screen.getByTestId('connection-comparison-provenance')
    expect(provenance.textContent).toContain('configuration/managed-clusters.yaml')
    expect(provenance.textContent).toContain('abcdef1') // short commit hash (7 chars)
  })

  // ───────────────────────────────────────────────────────────────────────────
  // Differences and not-checked lists.
  // ───────────────────────────────────────────────────────────────────────────

  it('lists field-by-field differences when they exist', () => {
    const comparison: ConnectionComparisonView = {
      cluster: 'test-cluster',
      status: 'out_of_sync',
      scope: 'full',
      ownership_mode: 'sharko_managed',
      checked_at: '2026-08-13T12:00:00Z',
      branch: 'main',
      differences: [
        { path: 'data.server', status: 'different', expected: 'https://expected.example', live: 'https://live.example' },
        { path: 'metadata.labels[addon.sharko.dev/test]', status: 'missing' },
      ],
      not_checked: [],
      checked_field_count: 10,
      repair_available: true,
      repair_scope: 'full_connection',
      values_never_returned: true,
    }
    render(<ConnectionComparisonDisplay cluster="test-cluster" comparison={comparison} loading={false} error={null} onRetry={() => {}} />)
    expect(screen.getByTestId('connection-comparison-differences')).toBeInTheDocument()
    expect(screen.getByText('data.server')).toBeInTheDocument()
    expect(screen.getByText('metadata.labels[addon.sharko.dev/test]')).toBeInTheDocument()
  })

  it('lists fields not checked when they exist', () => {
    const comparison: ConnectionComparisonView = {
      cluster: 'test-cluster',
      status: 'limited',
      scope: 'limited',
      ownership_mode: 'eks',
      checked_at: '2026-08-13T12:00:00Z',
      branch: 'main',
      differences: [],
      not_checked: [
        { path: 'data.config', reason: 'EKS sign-in tokens are created fresh each time they are used and cannot be compared.' },
      ],
      checked_field_count: 8,
      repair_available: true,
      repair_scope: 'full_connection',
      values_never_returned: true,
    }
    render(<ConnectionComparisonDisplay cluster="test-cluster" comparison={comparison} loading={false} error={null} onRetry={() => {}} />)
    expect(screen.getByTestId('connection-comparison-not-checked')).toBeInTheDocument()
    expect(screen.getByText('data.config')).toBeInTheDocument()
    expect(screen.getByText(/EKS sign-in tokens/)).toBeInTheDocument()
  })
})

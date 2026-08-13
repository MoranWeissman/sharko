// ConnectionComparisonDisplay — S4-1, S4-2, S4-3 (Part A of connection-repair Step 4).
//
// The browser-facing half of the connection-comparison check: runs the check
// when the connection page opens, shows the answer in plain words, and lists
// the field-by-field differences inline and already open.
//
// SECURITY RULES (still binding, all of them):
// - A sensitive field arrives as path + status + sensitive:true. There is NO
//   expected property and NO live property in the response — the server left
//   them out entirely, so there is nothing on the page to hide.
// - Both sides render as the fixed text "<redacted>". The same fixed text
//   every time.
// - NEVER a mask whose length hints at the value. No dots-per-character, no
//   "8 characters", no truncated start or end. The width of a redacted field
//   on screen MUST NOT depend on the value behind it.

import type { ConnectionComparisonView } from '@/services/models'

// ─────────────────────────────────────────────────────────────────────────────
// Pinned sentences — wording agreed on 2026-08-13.
//
// Every sentence here has an exact-text test in
// ConnectionComparisonDisplay.test.tsx. Any change to these words must be
// deliberate, not accidental rewording.
// ─────────────────────────────────────────────────────────────────────────────

/**
 * status: synced — every field inside the scope was checked and matched.
 * Wording agreed 2026-08-13.
 */
const STATUS_SYNCED_SENTENCE = 'This connection matches what Sharko intends.'

/**
 * status: out_of_sync — at least one field inside the scope did not match.
 * Wording agreed 2026-08-13.
 */
const STATUS_OUT_OF_SYNC_SENTENCE = 'This connection does not match what Sharko intends.'

/**
 * status: missing — the connection Secret does not exist on the cluster.
 * Wording agreed 2026-08-13.
 */
const STATUS_MISSING_SENTENCE = 'This connection has not been created yet.'

/**
 * status: check_failed — the check could not finish.
 * Wording agreed 2026-08-13.
 */
const STATUS_CHECK_FAILED_SENTENCE = 'The check did not finish.'

/**
 * status: ownership_conflict — another tool manages this connection.
 * Wording agreed 2026-08-13.
 */
const STATUS_OWNERSHIP_CONFLICT_SENTENCE = 'Another tool manages this connection.'

/**
 * status: limited — Sharko checked part of the connection.
 * Wording agreed 2026-08-13.
 */
const STATUS_LIMITED_SENTENCE = 'Sharko checked part of this connection.'

// The fixed redaction text for sensitive fields on both sides.
const REDACTED_TEXT = '<redacted>'

interface ConnectionComparisonDisplayProps {
  cluster: string
  comparison: ConnectionComparisonView | null
  loading: boolean
  error: string | null
  onRetry: () => void
}

export function ConnectionComparisonDisplay({
  cluster,
  comparison,
  loading,
  error,
  onRetry,
}: ConnectionComparisonDisplayProps) {
  if (loading) {
    return (
      <div className="space-y-2" data-testid="connection-comparison-loading">
        <p className="text-sm text-[#2a5a7a] dark:text-gray-400">Checking the connection…</p>
      </div>
    )
  }

  if (error) {
    return (
      <div className="space-y-2" data-testid="connection-comparison-error">
        <p className="text-sm text-red-700 dark:text-red-400">{error}</p>
        <button
          type="button"
          onClick={onRetry}
          data-testid="connection-comparison-retry"
          className="inline-flex items-center gap-1.5 rounded-lg border border-[#6aade0] bg-white px-2.5 py-1 text-xs font-medium text-[#0a3a5a] hover:bg-[#e0f0ff] dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
        >
          Try again
        </button>
      </div>
    )
  }

  if (!comparison) {
    return null
  }

  // S4-1: Give each of the six answers its own plain sentence and its own
  // visual treatment.
  const statusSentence = getStatusSentence(comparison)
  const statusColor = getStatusColor(comparison.status)

  return (
    <div className="space-y-4" data-testid="connection-comparison-result">
      {/* Status headline */}
      <div>
        <p className={`text-sm font-medium ${statusColor}`} data-testid="connection-comparison-status-sentence">
          {statusSentence}
        </p>
        {/* For check_failed, show the server's failure reason. */}
        {comparison.status === 'check_failed' && comparison.failure_reason && (
          <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-400" data-testid="connection-comparison-failure-reason">
            {comparison.failure_reason}
          </p>
        )}
        {/* For limited, show the limit reason. */}
        {comparison.status === 'limited' && comparison.limit_reason && (
          <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-400" data-testid="connection-comparison-limit-reason">
            {comparison.limit_reason}
          </p>
        )}
      </div>

      {/* Provenance: what this was checked against. ABOVE the differences, not below.
          The short commit (7 chars) is visible at a glance; hovering shows the full
          commit in a title attribute so a person can get it without leaving the page. */}
      {(comparison.compared_path || comparison.branch) && (
        <div className="text-xs text-[#3a6a8a] dark:text-gray-500" data-testid="connection-comparison-provenance">
          {comparison.compared_path && <div>File: {comparison.compared_path}</div>}
          {comparison.compared_commit ? (
            <div>
              Commit: <span className="font-mono" title={`Full commit: ${comparison.compared_commit}`}>{comparison.compared_commit.substring(0, 7)}</span>
            </div>
          ) : (
            <div>Branch: {comparison.branch} (commit unknown)</div>
          )}
        </div>
      )}

      {/* S4-2: The differences, inline and expanded. */}
      {comparison.differences.length > 0 && (
        <div className="rounded-md border border-border bg-card p-3" data-testid="connection-comparison-differences">
          <h3 className="mb-2 text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Differences</h3>
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="text-[11px] uppercase tracking-wide text-[#5a8aaa] dark:text-gray-500">
                <th className="py-1 pr-3 font-medium">Field</th>
                <th className="py-1 pr-3 font-medium">Expected</th>
                <th className="py-1 pr-3 font-medium">Live</th>
                <th className="py-1 font-medium">Status</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {comparison.differences.map((diff, idx) => {
                // For a sensitive field: both sides render as the fixed
                // REDACTED_TEXT. The same text every time, no length hints.
                const expectedDisplay = diff.sensitive ? REDACTED_TEXT : diff.expected ?? '—'
                const liveDisplay = diff.sensitive ? REDACTED_TEXT : diff.live ?? '—'
                const statusLabel = diff.status === 'same' ? 'Same' : diff.status === 'different' ? 'Different' : diff.status === 'missing' ? 'Missing' : 'Unexpected'
                const statusColorClass = diff.status === 'same' ? 'text-emerald-700 dark:text-emerald-400' : 'text-amber-700 dark:text-amber-400'

                return (
                  <tr key={idx} data-testid={`connection-comparison-diff-row-${idx}`}>
                    <td className="break-all py-1.5 pr-3 font-mono text-xs text-[#2a5a7a] dark:text-gray-300">{diff.path}</td>
                    <td className="py-1.5 pr-3 font-mono text-xs text-[#0a2a4a] dark:text-gray-200">{expectedDisplay}</td>
                    <td className="py-1.5 pr-3 font-mono text-xs text-[#0a2a4a] dark:text-gray-200">{liveDisplay}</td>
                    <td className={`py-1.5 text-xs font-medium ${statusColorClass}`}>{statusLabel}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      {/* Fields not checked, if any. */}
      {comparison.not_checked.length > 0 && (
        <div className="rounded-md border border-border bg-card p-3" data-testid="connection-comparison-not-checked">
          <h3 className="mb-2 text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Not checked</h3>
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="text-[11px] uppercase tracking-wide text-[#5a8aaa] dark:text-gray-500">
                <th className="py-1 pr-3 font-medium">Field</th>
                <th className="py-1 font-medium">Reason</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {comparison.not_checked.map((nc, idx) => (
                <tr key={idx} data-testid={`connection-comparison-not-checked-row-${idx}`}>
                  <td className="break-all py-1.5 pr-3 font-mono text-xs text-[#2a5a7a] dark:text-gray-300">{nc.path}</td>
                  <td className="py-1.5 text-xs text-[#2a5a7a] dark:text-gray-400">{nc.reason}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

// getStatusSentence maps the server's status to the pinned sentence.
function getStatusSentence(comparison: ConnectionComparisonView): string {
  switch (comparison.status) {
    case 'synced':
      return STATUS_SYNCED_SENTENCE
    case 'out_of_sync':
      return STATUS_OUT_OF_SYNC_SENTENCE
    case 'missing':
      return STATUS_MISSING_SENTENCE
    case 'check_failed':
      return STATUS_CHECK_FAILED_SENTENCE
    case 'ownership_conflict':
      return STATUS_OWNERSHIP_CONFLICT_SENTENCE
    case 'limited':
      return STATUS_LIMITED_SENTENCE
    default:
      // This should never happen if the server only sends the six known statuses.
      return 'Unknown status.'
  }
}

// getStatusColor returns the appropriate color class for each status.
function getStatusColor(status: string): string {
  switch (status) {
    case 'synced':
      return 'text-emerald-700 dark:text-emerald-400'
    case 'limited':
      // Limited is not synced — it's an honest partial answer, not a full healthy verdict.
      return 'text-blue-700 dark:text-blue-400'
    case 'out_of_sync':
    case 'ownership_conflict':
      return 'text-amber-700 dark:text-amber-400'
    case 'missing':
    case 'check_failed':
      return 'text-red-700 dark:text-red-400'
    default:
      return 'text-[#2a5a7a] dark:text-gray-400'
  }
}

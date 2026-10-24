/**
 * EP3 round S shared contract: the UI never shows "PR #0". A pull request
 * number is shown only when it is a real number above zero. When it is not
 * known, the text says "a pull request" (or whatever the caller passes).
 */
export function hasPRNumber(id: unknown): id is number {
  return typeof id === 'number' && Number.isFinite(id) && id > 0
}

/** "PR #12", or `whenUnknown` when the number is not known. */
export function prRef(id: unknown, whenUnknown = 'a pull request'): string {
  return hasPRNumber(id) ? `PR #${id}` : whenUnknown
}

/** "#12 " in front of a pull request title, or nothing when the number is not known. */
export function prNumberPrefix(id: unknown): string {
  return hasPRNumber(id) ? `#${id} ` : ''
}

/** The plain label for a merged pull request whose finish has not run yet. */
export const PR_FINISHING_LABEL = 'Finishing'

/**
 * True when the server says a merged pull request is still being finished
 * (`status: "finishing"`). `last_status` is read too, in case the server
 * puts it there.
 */
export function isFinishingPR(pr: { status?: string; last_status?: string }): boolean {
  return (pr.status ?? '').toLowerCase() === 'finishing' || (pr.last_status ?? '').toLowerCase() === 'finishing'
}

// Telling three kinds of failed call apart, so a page never says "the
// connection is failing" about something that is not a broken connection.
//
//   - "not allowed": the server refused this person's role (HTTP 403).
//     Nothing is broken; the person just may not do this.
//   - "busy": Sharko's own limit on how many changes one address may ask for
//     in a minute (HTTP 429 with code sharko_rate_limit).
//   - "limit": a service Sharko uses (GitHub most often) said Sharko asked
//     too often (any other HTTP 429).
//
// The sentences are the exact words a page shows. The two 429 sentences are
// the same words the server sends (internal/api/router.go), so an older
// server that still sends a bare "Too Many Requests" reads the same.
// Pinned by exact text in lib/__tests__/apiFailure.test.ts.

export const NOT_ALLOWED_SENTENCE =
  'Your role is not allowed to do this. Ask an operator or an admin.'
export const SHARKO_BUSY_SENTENCE =
  'Sharko is getting too many requests from your address right now. Try again in a moment.'
export const UPSTREAM_LIMIT_SENTENCE =
  'GitHub, or another service Sharko uses, says Sharko has asked too often. Try again in a moment.'

export type ApiFailureKind = 'not-allowed' | 'busy' | 'limit' | 'other'

// Read the fields, not `instanceof`: many tests mock the api module.
export function apiFailureKind(err: unknown): ApiFailureKind {
  const e = err as { status?: unknown; code?: unknown; message?: unknown } | null
  if (!e || typeof e !== 'object') return 'other'
  if (e.status === 403) return 'not-allowed'
  if (e.status === 429) return e.code === 'sharko_rate_limit' ? 'busy' : 'limit'
  return 'other'
}

/** The plain sentence for a refusal or a request limit; undefined for any other failure. */
export function apiFailureSentence(err: unknown): string | undefined {
  switch (apiFailureKind(err)) {
    case 'not-allowed':
      return NOT_ALLOWED_SENTENCE
    case 'busy':
      return SHARKO_BUSY_SENTENCE
    case 'limit':
      return UPSTREAM_LIMIT_SENTENCE
    default:
      return undefined
  }
}

/**
 * True when the failure says nothing about whether a connection works: the
 * check was refused or turned away, it did not fail. A page shows the
 * sentence from apiFailureSentence and leaves its red mark off.
 */
export function isNotACheckResult(err: unknown): boolean {
  return apiFailureKind(err) !== 'other'
}

/**
 * What a cluster-test result slot holds when the test was refused or turned
 * away instead of run. It is not a result about the cluster, so no page may
 * draw "Test failing" or "Unreachable" from it.
 */
export interface TestNotRun {
  notRun: true
  /** The plain sentence to show. */
  error: string
}

export function testNotRunOf(err: unknown): TestNotRun | undefined {
  const sentence = apiFailureSentence(err)
  return sentence ? { notRun: true, error: sentence } : undefined
}

export function isTestNotRun(v: unknown): v is TestNotRun {
  return typeof v === 'object' && v !== null && (v as { notRun?: unknown }).notRun === true
}

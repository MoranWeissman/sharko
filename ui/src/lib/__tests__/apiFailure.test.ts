import { describe, it, expect } from 'vitest'
import {
  apiFailureKind,
  apiFailureSentence,
  NOT_ALLOWED_SENTENCE,
  SHARKO_BUSY_SENTENCE,
  UPSTREAM_LIMIT_SENTENCE,
  testNotRunOf,
  isTestNotRun,
} from '@/lib/apiFailure'

// Walks 5, 6, 10: a refusal or a request limit was shown as "the connection is
// failing". These pin the plain sentences and what counts as which kind.
describe('apiFailure', () => {
  it('pins the three sentences by exact text', () => {
    expect(NOT_ALLOWED_SENTENCE).toBe('Your role is not allowed to do this. Ask an operator or an admin.')
    expect(SHARKO_BUSY_SENTENCE).toBe(
      'Sharko is getting too many requests from your address right now. Try again in a moment.',
    )
    expect(UPSTREAM_LIMIT_SENTENCE).toBe(
      'GitHub, or another service Sharko uses, says Sharko has asked too often. Try again in a moment.',
    )
  })

  it('403 is "not allowed", whatever the server worded', () => {
    const err = { status: 403, message: "action 'cluster.test' requires role 'operator', you have 'viewer'" }
    expect(apiFailureKind(err)).toBe('not-allowed')
    expect(apiFailureSentence(err)).toBe(NOT_ALLOWED_SENTENCE)
    expect(apiFailureSentence(err)).not.toMatch(/requires role/)
  })

  it('429 with Sharko code is Sharko busy, any other 429 is the service limit', () => {
    expect(apiFailureSentence({ status: 429, code: 'sharko_rate_limit' })).toBe(SHARKO_BUSY_SENTENCE)
    expect(apiFailureSentence({ status: 429, message: 'Too Many Requests' })).toBe(UPSTREAM_LIMIT_SENTENCE)
    expect(apiFailureSentence({ status: 429, message: 'rate limit exceeded' })).not.toMatch(/Too Many Requests|rate limit exceeded/)
  })

  it('a real failure is not touched', () => {
    expect(apiFailureKind({ status: 502 })).toBe('other')
    expect(apiFailureKind(new Error('boom'))).toBe('other')
    expect(apiFailureKind(null)).toBe('other')
    expect(apiFailureSentence({ status: 500 })).toBeUndefined()
    expect(testNotRunOf({ status: 500 })).toBeUndefined()
  })

  it('testNotRunOf marks a refused test as not run', () => {
    const r = testNotRunOf({ status: 403 })
    expect(isTestNotRun(r)).toBe(true)
    expect(r?.error).toBe(NOT_ALLOWED_SENTENCE)
    expect(isTestNotRun({ reachable: false })).toBe(false)
  })
})

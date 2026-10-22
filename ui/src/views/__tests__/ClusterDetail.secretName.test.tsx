// ep3 review G2: the cluster page calls the lookup key "Secret name", the same
// word the Add cluster form uses. "Secret path" must not come back in any
// sentence a person can read on that page (labels, toasts, PR messages, error
// fallbacks). Comments are skipped; they are not shown to anyone.
import { describe, it, expect } from 'vitest'
import source from '../ClusterDetail.tsx?raw'
import { stripComments } from '@/__tests__/sourceScan'
import { SECRET_PATH_FAILED_FALLBACK } from '../ClusterDetail'

describe('ClusterDetail wording: Secret name, not Secret path', () => {
  it('has no "secret path" in any shown string', () => {
    const code = stripComments(source as string)
    const hits = code.split('\n').filter(l => /secret path/i.test(l))
    expect(hits).toEqual([])
  })

  it('pins the exact words people read', () => {
    const code = stripComments(source as string)
    expect(code).toContain('aria-label="Edit secret name"')
    expect(code).toContain('mergedMessage="PR merged — secret name updated"')
    expect(code).toContain('openMessage="PR opened — secret name updates once it merges"')
    expect(code).toContain("'Secret name updated'")
    expect(SECRET_PATH_FAILED_FALLBACK).toBe(
      'Sharko could not write the secret name change to Git. Nothing was changed.',
    )
  })
})

// Lives here, not in services/api, so a page can use it while a test mocks
// the api module: it is a pure check on an answer, not a network call.
// services/api re-exports both names, so existing imports keep working.
import type { VerifyResult } from '@/services/models'

/**
 * The verification stage the server names when the CREDENTIAL LOOKUP itself
 * failed, before any cluster was contacted.
 *
 * The wire value is written in exactly one place server-side —
 * `Stage: "credentials"` in internal/api/clusters_discover.go's handleTestCluster
 * — on the one branch it takes when fetching this cluster's sign-in details
 * did not work. Whatever the reason, that branch means: nothing was asked of
 * the cluster.
 *
 * It is pinned as a written-out literal on both sides. Go's half is
 * TestTestCluster_CredentialFailureStageIsPinned in internal/api; this
 * constant and its test are the browser's half, in the same shape as the
 * banned-wording pair that already spans the two languages.
 */
export const VERIFY_STAGE_CREDENTIALS = 'credentials'

/**
 * True when a cluster test failed because Sharko could not read the cluster's
 * sign-in details — not because anything was wrong with the cluster.
 *
 * WHY THIS IS A TYPED CHECK AND NOT A SEARCH THROUGH THE MESSAGE. The adopt
 * dialog used to answer this question by lower-casing the server's
 * `error_message` and looking in it for "secret", "not found", "credential",
 * "unavailable" and "no credentials available". Two things were wrong with
 * that, and the second one is live:
 *
 *   1. It is the browser deciding what to render by reading a human sentence
 *      the server wrote, which the product owner ruled out: presentation
 *      structure follows typed facts.
 *   2. Since the credentials hotfix, a credentials-backend failure carries
 *      ONE fixed sentence — the backend's own words never leave the server —
 *      and that sentence contains none of the five phrases. So the search
 *      answered "no" for every credentials failure there is, and the
 *      credentials-optional contract it was implementing had quietly stopped
 *      working: a cluster whose credentials simply are not configured was
 *      being shown as a failed verification and deselected, when the server
 *      would have adopted it happily.
 *
 * The server's own adopt path (internal/orchestrator/adopt.go) treats ANY
 * credential-lookup failure the same way — "a failed credential lookup is the
 * NORMAL case, not a fatal one — skip verification instead of failing the
 * adoption" — with no sub-classification by reason. This matches it exactly.
 */
export function isCredentialLookupFailure(v: Pick<VerifyResult, 'success' | 'stage'> | { success?: boolean; stage?: string }): boolean {
  return v.success === false && v.stage === VERIFY_STAGE_CREDENTIALS
}

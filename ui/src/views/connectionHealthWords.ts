// connectionHealthWords — the four words either surface may show for a
// connection's health, in ONE place.
//
// # Why this file exists
//
// The fleet list and the connection's own page each held their own copy of
// this table — HEALTH_COLUMN_WORDS in ManagedSecrets.tsx and HEALTH_WORDS in
// ConnectionReconciliationView.tsx — and each held its own copy of the
// lookup that reads it. The two copies happened to agree word for word. What
// held them together was a comment in each file saying they did: "shared in
// spirit with the fleet list's HEALTH_COLUMN_WORDS — both surfaces render the
// same word for the same connection."
//
// Nothing enforced it. The tests on either side pinned words separately —
// one file's test pinned all four, the other's pinned one — so an edit to
// either table alone would have left every test green while the same
// connection read "Unavailable" in the list and something else on its own
// page. That is the exact shape of defect this round has been removing: two
// authors, one fact.
//
// So there is one author now. Both surfaces import from here, and there is
// nothing left to keep in step: the tables cannot disagree because there is
// only one. Each file keeps its own re-export under its own long-standing
// name, so no caller or test had to be rewritten to prove the point.
//
// # The words themselves
//
// They are the BROWSER's row vocabulary, not the server's sentences. The
// server sends one of the four values of ConnectionHealthWord; these are the
// words a person reads for them. That is why they are not in
// @/generated/connection-sentences: nothing here is a copy of anything the
// server says.

import type { ConnectionHealthWord } from '@/services/models'

/**
 * The health words — ArgoCD's own answer about the connection, independent
 * of the git state beside it. Showing Connected next to an unverifiable git
 * state is correct, not a contradiction.
 *
 * The Record is typed on ConnectionHealthWord, so a new value on the wire
 * type is a compile error here rather than a silently missing word.
 */
export const CONNECTION_HEALTH_WORDS: Record<ConnectionHealthWord, string> = {
  connected: 'Connected',
  unavailable: 'Unavailable',
  not_checked: 'Not checked',
  // (B13 item 3) NOT a synonym for "Not checked". There is no connection
  // Secret, so there is nothing for ArgoCD to probe and no check is coming.
  // "Not checked" here would promise a check that is not on its way.
  unknown: 'Unknown',
}

/**
 * The word to show for a health value off the wire.
 *
 * TWO DIFFERENT ABSENCES, DELIBERATELY NOT THE SAME ANSWER. No health value
 * at all means the server said nothing — an older server, or a row from
 * before the field existed — and "Not checked" is the honest word for that.
 * A value this browser does not recognise means a NEWER server, and falling
 * through to "Not checked" would make a specific claim (a probe is on its
 * way) that may well be false. "Unknown" is the only word that stays true
 * whatever the new value turns out to mean.
 */
export function connectionHealthWord(health?: string): string {
  if (!health) return CONNECTION_HEALTH_WORDS.not_checked
  return CONNECTION_HEALTH_WORDS[health as ConnectionHealthWord] ?? CONNECTION_HEALTH_WORDS.unknown
}

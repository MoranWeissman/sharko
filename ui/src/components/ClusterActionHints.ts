// V2-cleanup-65.1 — maintainer feedback (2026-07-06): a first-time user
// cannot tell the "Test" button apart from the "Diagnose" button — both read
// as "check something" verbs. Renamed to plain-words labels and given a
// one-line explanation each. Exported once here (same pattern as
// WhoseConnectionLabel.tsx's label/tooltip constants) so the button's
// InfoHint and the matching result-surface header can't drift apart.
//
// "Test connection" = Sharko's OWN connection test to the cluster (Sharko
// fetches credentials from the secret backend and talks to the cluster
// directly — see WhoseConnectionLabel's SHARKO_CONN_TOOLTIP for the
// whose-connection angle on the same button).
//
// "Check permissions" = permission checks on the cluster with suggested
// copy-paste YAML fixes (DiagnoseModal.tsx / api.diagnoseCluster). The API
// and component names are unchanged — this is a UI wording change only.

import { CONNECTION_SENTENCES } from '@/generated/connection-sentences';

export const TEST_CONNECTION_LABEL = 'Test connection';
export const TEST_CONNECTION_HINT =
  "Runs Sharko's own connection test to this cluster — is it reachable?";

export const CHECK_PERMISSIONS_LABEL = 'Check permissions';
export const CHECK_PERMISSIONS_HINT =
  'Checks the permissions on the cluster and suggests fixes if any are missing.';

// B13 item 9 — ONE sentence for one situation, on one button.
//
// "Sync addon labels" is offered on two pages: the cluster detail page and
// the Cluster connections list. When there is nothing to apply, both pages
// have to say so, and they said it differently. The cluster page said
// "Nothing to apply — this secret already matches git."; the connections
// list said "Nothing to apply — this connection already matches the
// Git-defined connection." Same button, same situation, two answers — and
// the cluster page's version also used the pre-B5 vocabulary ("this secret",
// "git") that the locked model replaced.
//
// Exported here, imported by both, and pinned by exact string in each
// page's own tests so they cannot drift apart again.
export const SYNC_ADDON_LABELS_NOTHING_TO_APPLY =
  'Nothing to apply — this connection already matches the Git-defined connection.';

// ── Same button, same write, two more pairs of different words ───────────
//
// "Sync addon labels" writes ONE thing: Sharko's own addon-label keys, back
// onto the cluster's connection. Both pages that offer the button described
// that write twice each, in their own words — once as the button's hint,
// once in the confirm box read right before the write happens:
//
//   hint     ClusterDetail  "Applies git's addon labels to this cluster's
//                            secret now."
//            ManagedSecrets "Sync addon labels puts git's addon labels back
//                            on this secret. Nothing else on it changes."
//   confirm  ClusterDetail  "Applies git's addon labels to this cluster's
//                            ArgoCD secret — one time; the self-heal setting
//                            is not changed"
//            ManagedSecrets "This writes the secret on cluster "…" now. No
//                            pull request. It copies git's addon labels onto
//                            the cluster's ArgoCD secret. The self-heal
//                            setting doesn't change."
//
// Both new sentences drop "this cluster's secret" / "this cluster's ArgoCD
// secret". The locked model calls the thing a CONNECTION, the same word
// SYNC_ADDON_LABELS_NOTHING_TO_APPLY above already uses, and the reader on
// either page is looking at a connection — not at a Kubernetes object.

/**
 * The button's hint on both pages. Two short sentences: what it writes, and
 * the reassurance that nothing else on the connection moves.
 */
export const SYNC_ADDON_LABELS_HINT =
  'Puts the addon labels Git defines back on this connection. Nothing else on it changes.';

/**
 * Everything after the opening sentence of the confirm box. Kept separate
 * from syncAddonLabelsConfirmText below only because the opening names the
 * cluster and this half never changes.
 */
export const SYNC_ADDON_LABELS_CONFIRM_BODY =
  "No pull request. It puts back the addon labels Git defines, and nothing else. The self-heal setting doesn't change.";

/**
 * The confirm box's full description, for both pages.
 *
 * It names the cluster even though both confirm boxes already carry the
 * cluster in their TITLE. That repetition is deliberate: this is the last
 * thing a person reads before a live write, and the Cluster connections list
 * is a long table where opening the wrong row's menu is easy.
 */
export function syncAddonLabelsConfirmText(cluster: string): string {
  return `This writes to cluster "${cluster}" now. ${SYNC_ADDON_LABELS_CONFIRM_BODY}`;
}

// ── The browser writes no promise about what Sharko will do ──────────────
//
// The Cluster connections list used to author its own promise for a
// self-managed connection whose addon labels drifted:
//
//   "Sharko re-applies the addon labels git declares on the next pass.
//    Nothing to do here."
//
// The server has its own sentence for the identical fact —
// `planAutomaticLabelSync` in internal/api/connection_reconciliation.go —
// and it was NOT the same sentence. Two authors, one promise, different
// words.
//
// The sentence still cannot be rendered from the response on this path: the
// fleet response's connection rows (connectionSecretRow /
// ConnectionSecretRow) carry no plan field at all — only the connection
// page's own reconciliation response does, and this sentence is needed in a
// row's action menu, which never fetches it. That is the "no server response
// to render" case, so the value comes from the GENERATED contract instead of
// being typed here: cmd/gen-connection-sentences reads the Go constant at
// runtime, and CI's "Connection Sentences Up To Date" job fails the PR if
// the file below and the Go constant ever disagree. Editing the Go sentence
// now changes this one, in the same commit, or CI stops the change.
//
// What the comment here used to claim while this was a hand-typed literal,
// and why it was wrong: it said the literal was "character for character
// identical to the server's constant" and "pinned by an exact-string test on
// BOTH sides". Neither held. Three different sentences were in play for the
// one fact, and the browser-side "pin"
// (ManagedSecrets.reconciliationcontract.test.tsx) asserted that this
// constant equals this file's own literal — a constant compared with itself,
// which cannot fail and proves nothing. That is exactly how the two sides
// drifted apart without a single test going red.
export const SHARKO_REAPPLIES_ADDON_LABELS = CONNECTION_SENTENCES.planAutomaticLabelSync;

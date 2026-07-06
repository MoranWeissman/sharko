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

export const TEST_CONNECTION_LABEL = 'Test connection';
export const TEST_CONNECTION_HINT =
  "Runs Sharko's own connection test to this cluster — is it reachable?";

export const CHECK_PERMISSIONS_LABEL = 'Check permissions';
export const CHECK_PERMISSIONS_HINT =
  'Checks the permissions on the cluster and suggests fixes if any are missing.';

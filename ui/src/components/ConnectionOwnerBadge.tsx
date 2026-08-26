// V2-cleanup-57.2: per-cluster connection ownership.
//
// A cluster whose managed-clusters.yaml entry carries
// `connectionManagedBy: user` has a SELF-MANAGED ArgoCD connection: the
// user created the ArgoCD cluster secret by hand and maintains it; Sharko
// never writes, rotates, or deletes it — it only syncs addon labels onto
// it. This read-only caption tells the user that at a glance, using the
// same small-caption idiom as WhoseConnectionLabel (V2-cleanup-55.3).

import { CONNECTION_SENTENCES } from '@/generated/connection-sentences';

export const CONN_OWNER_USER_LABEL = 'connection: managed by you';

/**
 * What this caption says about a self-managed connection.
 *
 * THE FACT IS THE SERVER'S, NOT THIS FILE'S. It used to be hand-typed here:
 * "You created and maintain the ArgoCD cluster secret for this cluster.
 * Sharko only manages the addon labels on it — it never writes, rotates, or
 * deletes the credentials." Every word after the first full stop is the
 * browser promising what the server will and will not do — the class the
 * product owner ruled out under "zero browser-authored promises about server
 * behaviour" — and the server already has its own sentence for exactly this
 * mode (`modeStatementSelfManaged`), which said it differently. Two authors,
 * one fact.
 *
 * This badge has no server response to render from: it is handed one typed
 * field, `connection_managed_by`, in a table row that fetches no
 * reconciliation view. That is the documented "no server response on that
 * path" case, and the answer is the same one ClusterActionHints uses — take
 * the value from the GENERATED contract, which cmd/gen-connection-sentences
 * writes from the Go constant and CI's "Connection Sentences Up To Date" job
 * keeps honest. Reword the Go sentence and this caption moves with it in the
 * same commit, or CI stops the change.
 *
 * The second half is the browser's own: a pointer to a page in the docs is a
 * fact about the documentation, and it promises nothing about the server.
 */
export const CONN_OWNER_USER_TOOLTIP =
  CONNECTION_SENTENCES.modeStatementSelfManaged +
  ' See the operator guide: Managing cluster connections yourself.';

interface ConnectionOwnerBadgeProps {
  /** The cluster's connection_managed_by value from the API. */
  managedBy?: string;
}

/**
 * Small read-only caption rendered next to a cluster's connection status
 * when the connection is self-managed. Renders nothing for Sharko-managed
 * clusters (the default) so the existing layout is untouched for them.
 * Native `title` tooltip — this renders inside table rows and cards where
 * a Radix tooltip per row would be heavy.
 */
export function ConnectionOwnerBadge({ managedBy }: ConnectionOwnerBadgeProps) {
  if (managedBy !== 'user') return null;
  return (
    <span
      className="w-fit cursor-help rounded bg-[#e0f0ff] px-1.5 py-0.5 text-xs font-medium text-[#2a5a7a] ring-1 ring-[#6aade0] dark:bg-gray-800 dark:text-gray-300 dark:ring-gray-600"
      title={CONN_OWNER_USER_TOOLTIP}
    >
      {CONN_OWNER_USER_LABEL}
    </span>
  );
}

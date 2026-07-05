// V2-cleanup-57.2: per-cluster connection ownership.
//
// A cluster whose managed-clusters.yaml entry carries
// `connectionManagedBy: user` has a SELF-MANAGED ArgoCD connection: the
// user created the ArgoCD cluster secret by hand and maintains it; Sharko
// never writes, rotates, or deletes it — it only syncs addon labels onto
// it. This read-only caption tells the user that at a glance, using the
// same small-caption idiom as WhoseConnectionLabel (V2-cleanup-55.3).

export const CONN_OWNER_USER_LABEL = 'connection: managed by you';
export const CONN_OWNER_USER_TOOLTIP =
  'You created and maintain the ArgoCD cluster secret for this cluster. ' +
  'Sharko only manages the addon labels on it — it never writes, rotates, or deletes the credentials. ' +
  'See the operator guide: Managing cluster connections yourself.';

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
      className="w-fit cursor-help rounded bg-[#e0f0ff] px-1.5 py-0.5 text-[10px] font-medium text-[#2a5a7a] ring-1 ring-[#6aade0] dark:bg-gray-800 dark:text-gray-300 dark:ring-gray-600"
      title={CONN_OWNER_USER_TOOLTIP}
    >
      {CONN_OWNER_USER_LABEL}
    </span>
  );
}

// Cluster connection-status classification used across the UI to render
// the ArgoCD per-cluster connection state in a consistent, three-state way.
//
// Three states:
//   - "connected": ArgoCD has probed the cluster and reports it healthy
//                  (status "Successful" or "Connected").
//   - "pending":   ArgoCD has not yet observed a probe result (status "",
//                  "Unknown", "missing" or "missing_from_argocd"). This is
//                  the transient post-registration window (~10-60s on real
//                  installs) — surface a neutral state, not a failure.
//   - "failed":    ArgoCD has observed an explicit failure (status
//                  "Failed" or anything else). The "anything else" fall-
//                  through is intentional so a future ArgoCD status we
//                  don't know about renders as a (red) attention item
//                  rather than a silent green.
export type ClusterConnectionKind = 'connected' | 'pending' | 'failed';

export function classifyClusterConnection(status: string | null | undefined): ClusterConnectionKind {
  const s = (status ?? '').toLowerCase().trim();
  if (s === 'successful' || s === 'connected') return 'connected';
  if (s === '' || s === 'unknown' || s === 'missing' || s === 'missing_from_argocd') {
    return 'pending';
  }
  return 'failed';
}

// Convenience predicate — true only when ArgoCD has confirmed connectivity.
// Use this for explicit "green checkmark" rendering.
export function isClusterConnected(status: string | null | undefined): boolean {
  return classifyClusterConnection(status) === 'connected';
}

// True only when ArgoCD has reported an explicit failure. Use this to
// decide whether to surface a cluster in problem/attention lists.
export function isClusterFailed(status: string | null | undefined): boolean {
  return classifyClusterConnection(status) === 'failed';
}

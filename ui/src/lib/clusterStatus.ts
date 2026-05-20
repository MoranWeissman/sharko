// Cluster connection-status classification used across the UI to render
// the ArgoCD per-cluster connection state in a consistent, three-state way.
//
// BUG-033: the UI used to treat any value that wasn't an explicit
// "Successful" / "Connected" as a hard "Disconnected" failure (red). This
// produced a false-failure UX after registering a new cluster — ArgoCD
// reports an empty `connectionState.status` until its cluster-info refresher
// runs the first probe (a window of ~10-60s in real installs), and the UI
// flashed red "Disconnected" the whole time even though the registration
// flow had completed successfully.
//
// BUG-034 already made the ClusterDetail page model these three states
// correctly; this helper centralises the same classification so the
// Dashboard's ClusterCard, the Dashboard "Clusters Needing Attention"
// filter, and any future caller share one source of truth.
//
// Three states:
//   - "connected": ArgoCD has probed the cluster and reports it healthy
//                  (status "Successful" or "Connected").
//   - "pending":   ArgoCD has not yet observed a probe result for this
//                  cluster (status "", "Unknown", "missing" or
//                  "missing_from_argocd"). This is the transient
//                  post-registration window — surface a neutral state, not
//                  a failure.
//   - "failed":    ArgoCD has observed an explicit failure
//                  (status "Failed" or anything else). The "anything else"
//                  fall-through is intentional so a future ArgoCD status
//                  we don't know about renders as a (red) attention item
//                  rather than as a silent green.
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

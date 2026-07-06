// Canonical cluster-connection vocabulary (V2-cleanup-61.2, finding D2).
//
// This file is the SINGLE SOURCE OF TRUTH for how the "ArgoCD → cluster"
// connection state is named, colored, and explained anywhere in the UI:
// the Clusters table, the cluster grid cards, the Dashboard ClusterCard,
// the stat cards, and the legend all read from CLUSTER_CONNECTION_STATES.
// The written contract lives in docs/site/user-guide/status-vocabulary.md —
// keep the two in sync.
//
// Four states:
//   - "connected": ArgoCD has probed the cluster and reports it healthy
//                  (status "Successful" or "Connected").
//   - "pending":   ArgoCD has not yet observed a probe result (status "",
//                  "Unknown", "missing" or "missing_from_argocd"). This is
//                  the transient post-registration window (~10-60s on real
//                  installs) — surface a neutral state, not a failure.
//   - "unmanaged": the cluster exists in ArgoCD but has no entry in
//                  Sharko's Git catalog (status "not_in_git"). Not broken —
//                  it just isn't Sharko-managed yet (adopt it to manage it).
//   - "failed":    ArgoCD has observed an explicit failure (status
//                  "Failed" or anything else). The "anything else" fall-
//                  through is intentional so a future ArgoCD status we
//                  don't know about renders as a (red) attention item
//                  rather than a silent green.
export type ClusterConnectionKind = 'connected' | 'pending' | 'unmanaged' | 'failed';

// Severity scale shared by every status surface (finding D3):
//   problem   → red     — something is broken, act now
//   attention → amber   — not broken, but you probably want to act
//   pending   → blue/neutral — a change is underway, wait
//   unknown   → gray/neutral — no information yet
//   good      → green family — working as intended
export type StatusSeverity = 'problem' | 'attention' | 'pending' | 'unknown' | 'good';

// Explicit worst-first ordering used to fold several status parts into ONE
// composite pill (finding D4). Index 0 is the worst.
export const SEVERITY_ORDER: readonly StatusSeverity[] = [
  'problem',
  'attention',
  'pending',
  'unknown',
  'good',
] as const;

/** Returns the worst severity in the list per SEVERITY_ORDER ('good' for []). */
export function worstSeverity(severities: StatusSeverity[]): StatusSeverity {
  for (const sev of SEVERITY_ORDER) {
    if (severities.includes(sev)) return sev;
  }
  return 'good';
}

export interface ClusterConnectionStateDef {
  /** The ONE user-facing name for this state. */
  label: string;
  /** One-sentence plain-English meaning (tooltips, legend, popover). */
  meaning: string;
  severity: StatusSeverity;
  /** Status-dot color classes. */
  dot: string;
  /** Text color classes for the label. */
  text: string;
}

export const CLUSTER_CONNECTION_STATES: Record<ClusterConnectionKind, ClusterConnectionStateDef> = {
  connected: {
    label: 'Connected',
    meaning: 'ArgoCD is connected to this cluster.',
    severity: 'good',
    dot: 'bg-green-500',
    text: 'text-green-700 dark:text-green-400',
  },
  pending: {
    label: 'Connecting…',
    meaning:
      "Waiting for ArgoCD's first connection result — normal for about a minute after registering.",
    severity: 'pending',
    // Neutral blue-tinted styling — matches the light-mode palette used
    // elsewhere for neutral states (see StatusBadge.tsx).
    dot: 'bg-[#3a6a8a] dark:bg-gray-400',
    text: 'text-[#1a4a6a] dark:text-gray-300',
  },
  unmanaged: {
    label: 'Not managed',
    meaning:
      "In ArgoCD but not in Sharko's Git catalog — adopt it to let Sharko manage its addons.",
    severity: 'attention',
    dot: 'bg-amber-500',
    text: 'text-amber-700 dark:text-amber-400',
  },
  failed: {
    label: 'Disconnected',
    meaning: 'ArgoCD tried to reach this cluster and failed.',
    severity: 'problem',
    dot: 'bg-red-500',
    text: 'text-red-700 dark:text-red-400',
  },
};

// Stable display order for legends and breakdowns (good → bad).
export const CLUSTER_CONNECTION_KINDS: readonly ClusterConnectionKind[] = [
  'connected',
  'pending',
  'unmanaged',
  'failed',
] as const;

export function classifyClusterConnection(status: string | null | undefined): ClusterConnectionKind {
  const s = (status ?? '').toLowerCase().trim();
  if (s === 'successful' || s === 'connected') return 'connected';
  if (s === '' || s === 'unknown' || s === 'missing' || s === 'missing_from_argocd') {
    return 'pending';
  }
  if (s === 'not_in_git') return 'unmanaged';
  return 'failed';
}

/** Canonical state definition for a raw ArgoCD connection_status string. */
export function getClusterConnectionState(status: string | null | undefined): ClusterConnectionStateDef {
  return CLUSTER_CONNECTION_STATES[classifyClusterConnection(status)];
}

// Convenience predicate — true only when ArgoCD has confirmed connectivity.
// Use this for explicit "green checkmark" rendering.
export function isClusterConnected(status: string | null | undefined): boolean {
  return classifyClusterConnection(status) === 'connected';
}

// True only when ArgoCD has reported an explicit failure. Use this to
// decide whether to surface a cluster in problem/attention lists.
// Note (V2-cleanup-61.2): "not_in_git" is now classified as 'unmanaged',
// not 'failed' — an unmanaged cluster is not a broken one, so it no longer
// lands in "needs attention" lists just for being unadopted.
export function isClusterFailed(status: string | null | undefined): boolean {
  return classifyClusterConnection(status) === 'failed';
}

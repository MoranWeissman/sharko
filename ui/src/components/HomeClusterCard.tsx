import { Server } from 'lucide-react'

export interface HomeClusterInfo {
  available: boolean
  message?: string
  kubernetes_version?: string
  node_count?: number
  nodes_ready?: number
  nodes_not_ready?: number
}

export interface HomeClusterCardProps {
  homeCluster: HomeClusterInfo
  /** From GET /health — the Sharko server's own version. */
  sharkoVersion?: string
  /** From GET /config — argocd.version (only set when argocd.connected). */
  argocdVersion?: string
  argocdConnected: boolean
  /** From GET /fleet/status — a server-formatted human string (e.g. "3h12m"). */
  uptime?: string
}

const DEFAULT = '—'

// Compact Tier 3 identity card (dashboard facelift, Package 3) — "where
// Sharko + ArgoCD run, and what version of each". Every field degrades to
// "—" independently instead of the old all-or-nothing swap to a bare
// message when the home-cluster probe (Kubernetes-only) failed — Sharko's
// own version and the ArgoCD connection are known via entirely separate
// calls and shouldn't disappear just because the K8s node probe did.
export function HomeClusterCard({ homeCluster, sharkoVersion, argocdVersion, argocdConnected, uptime }: HomeClusterCardProps) {
  const k8sVersion = homeCluster.available ? homeCluster.kubernetes_version || DEFAULT : DEFAULT
  const nodeCount = homeCluster.available && homeCluster.node_count != null ? String(homeCluster.node_count) : DEFAULT
  const argocd = argocdConnected ? argocdVersion || DEFAULT : DEFAULT

  const hasReadiness =
    homeCluster.available && homeCluster.node_count != null && homeCluster.nodes_ready != null
  const allReady = hasReadiness && homeCluster.nodes_ready === homeCluster.node_count && (homeCluster.node_count ?? 0) > 0

  return (
    <div className="max-w-md rounded-xl border border-border bg-card p-4 shadow-sm">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Server className="h-4 w-4 text-muted-foreground" />
          <h3 className="text-sm font-semibold text-card-foreground">Sharko's home cluster</h3>
        </div>
        {hasReadiness && (
          <span
            className={`rounded-full px-2 py-0.5 text-xs font-medium ${
              allReady
                ? 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400'
                : 'bg-muted text-muted-foreground'
            }`}
          >
            {allReady ? 'all nodes ready' : `${homeCluster.nodes_ready}/${homeCluster.node_count} nodes ready`}
          </span>
        )}
      </div>

      <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-2">
        <div>
          <div className="text-xs text-muted-foreground">Sharko version</div>
          <div className="text-sm font-semibold text-card-foreground">{sharkoVersion || DEFAULT}</div>
        </div>
        <div>
          <div className="text-xs text-muted-foreground">ArgoCD version</div>
          <div className="text-sm font-semibold text-card-foreground">{argocd}</div>
        </div>
        <div>
          <div className="text-xs text-muted-foreground">Kubernetes version</div>
          <div className="text-sm font-semibold text-card-foreground">{k8sVersion}</div>
        </div>
        <div>
          <div className="text-xs text-muted-foreground">Nodes</div>
          <div className="text-sm font-semibold text-card-foreground">{nodeCount}</div>
        </div>
      </div>

      {uptime ? (
        <p className="mt-3 text-xs text-muted-foreground">
          up {uptime}{homeCluster.available ? ' · running in-cluster' : ''}
        </p>
      ) : !homeCluster.available && homeCluster.message ? (
        <p className="mt-3 text-xs text-muted-foreground">{homeCluster.message}</p>
      ) : null}
    </div>
  )
}

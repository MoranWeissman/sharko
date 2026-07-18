import { useNavigate } from 'react-router-dom'
import { Server, Info } from 'lucide-react'
import { AddonDots } from '@/components/AddonDots'
import { getClusterConnectionState, classifyClusterConnection, isClusterNeedsAttention } from '@/lib/clusterStatus'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'

interface ClusterAddonSummary {
  name: string
  health: string
}

interface ClusterCardProps {
  name: string
  connectionStatus: string
  addonSummary: ClusterAddonSummary[]
  healthyCount: number
  totalCount: number
}

// The pill renders the canonical "ArgoCD → cluster" vocabulary from
// lib/clusterStatus.ts (V2-cleanup-61.2, finding D2) — one name, one
// color, one meaning across ClusterCard, ConnectionStatus, stat cards,
// and the legend. A freshly-registered cluster (ArgoCD has the secret but
// has not yet produced a probe result) renders as a neutral "Connecting…"
// pill rather than a red "Disconnected" failure — the ArgoCD probe window
// can be ~10-60s on real installs.
//
// LW-3..LW-6: collapsed connection indicator (one line), named reason,
// honest addon count label, no self-hosted badge.

export function ClusterCard({
  name,
  connectionStatus,
  addonSummary,
  healthyCount,
  totalCount,
}: ClusterCardProps) {
  const navigate = useNavigate()
  const pill = getClusterConnectionState(connectionStatus)
  const kind = classifyClusterConnection(connectionStatus)

  // LW-3: derive a plain-English reason for why this cluster needs attention
  let attentionReason = ''
  if (isClusterNeedsAttention(connectionStatus)) {
    attentionReason = pill.label // "Disconnected", "Not connected"
  } else if (kind === 'pending' && totalCount > 0) {
    attentionReason = 'Not reporting'
  } else if (kind === 'connected' && totalCount > 0 && healthyCount === 0) {
    attentionReason = 'All addons unhealthy'
  }

  return (
    <div
      onClick={() => navigate(`/clusters/${name}`)}
      onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); navigate(`/clusters/${name}`) } }}
      role="button"
      tabIndex={0}
      className="group cursor-pointer rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 shadow-sm transition-all duration-150 hover:-translate-y-0.5 hover:border-teal-400 hover:shadow-md dark:ring-gray-700 dark:bg-gray-800 dark:hover:border-teal-500"
    >
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <Server className="h-4 w-4 text-teal-600 dark:text-teal-400" />
        <h3 className="truncate text-sm font-bold text-[#0a2a4a] dark:text-gray-100">{name}</h3>
        {/* LW-6: removed ClusterTypeBadge — cosmetic guess from hostname, weak signal on cramped card */}
      </div>
      {/* LW-5: collapsed connection indicator — ONE line with state label + dot + optional "i" for explanation */}
      <TooltipProvider delayDuration={200}>
        <div className="mb-2 flex items-center gap-1.5">
          <div className={`h-2 w-2 rounded-full ${pill.dot}`} />
          <span className={`text-xs ${pill.text}`}>{pill.label} to ArgoCD</span>
          <Tooltip>
            <TooltipTrigger asChild>
              <Info className="h-3 w-3 text-[#5a8aaa] dark:text-gray-400" />
            </TooltipTrigger>
            <TooltipContent side="top" className="text-xs max-w-xs">
              {pill.meaning}
            </TooltipContent>
          </Tooltip>
        </div>
      </TooltipProvider>
      {/* LW-3: name the reason inline when this is on the needs-attention list */}
      {attentionReason && (
        <p className="mb-2 text-xs font-medium text-red-700 dark:text-red-400">
          {attentionReason}
        </p>
      )}
      {/* LW-4: honest addon count label — "N of M addons healthy" */}
      <p className="mb-2 text-xs text-[#2a5a7a] dark:text-gray-400">
        {totalCount > 0 ? `${healthyCount} of ${totalCount} addons healthy` : 'No addons'}
      </p>
      <AddonDots addons={addonSummary} />
    </div>
  )
}

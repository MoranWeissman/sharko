import { useNavigate } from 'react-router-dom'
import { Server } from 'lucide-react'
import { AddonDots } from '@/components/AddonDots'
import { ClusterTypeBadge } from '@/components/ClusterTypeBadge'
import { classifyClusterConnection } from '@/lib/clusterStatus'

interface ClusterAddonSummary {
  name: string
  health: string
}

interface ClusterCardProps {
  name: string
  /** Optional API server URL — when present, renders a cosmetic
   *  ClusterTypeBadge inline with the cluster name. */
  serverUrl?: string
  connectionStatus: string
  addonSummary: ClusterAddonSummary[]
  healthyCount: number
  totalCount: number
}

// A freshly-registered cluster (ArgoCD has the secret but has not yet
// produced a probe result) renders as a neutral "Connecting…" pill rather
// than a red "Disconnected" failure — the ArgoCD probe window can be
// ~10-60s on real installs.
const PILL_STYLES: Record<
  ReturnType<typeof classifyClusterConnection>,
  { dot: string; text: string; label: string }
> = {
  connected: {
    dot: 'bg-green-500',
    text: 'text-green-700 dark:text-green-400',
    label: 'Connected',
  },
  pending: {
    // Neutral blue-tinted styling — matches the light-mode palette used
    // elsewhere for "Unknown" cluster status (see StatusBadge.tsx).
    dot: 'bg-[#3a6a8a] dark:bg-gray-400',
    text: 'text-[#1a4a6a] dark:text-gray-300',
    label: 'Connecting…',
  },
  failed: {
    dot: 'bg-red-500',
    text: 'text-red-700 dark:text-red-400',
    label: 'Disconnected',
  },
}

export function ClusterCard({
  name,
  serverUrl,
  connectionStatus,
  addonSummary,
  healthyCount,
  totalCount,
}: ClusterCardProps) {
  const navigate = useNavigate()
  const pill = PILL_STYLES[classifyClusterConnection(connectionStatus)]

  return (
    <div
      onClick={() => navigate(`/clusters/${name}`)}
      onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); navigate(`/clusters/${name}`) } }}
      role="button"
      tabIndex={0}
      className="group cursor-pointer rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 shadow-sm transition-all duration-150 hover:-translate-y-0.5 hover:border-teal-400 hover:shadow-md dark:border-gray-700 dark:bg-gray-800 dark:hover:border-teal-500"
    >
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <Server className="h-4 w-4 text-teal-600 dark:text-teal-400" />
        <h3 className="truncate text-sm font-bold text-[#0a2a4a] dark:text-gray-100">{name}</h3>
        {/* Cosmetic type pill derived from server hostname. */}
        {serverUrl !== undefined && <ClusterTypeBadge server={serverUrl} compact />}
      </div>
      <div className="mb-2 flex items-center gap-1.5">
        <div className={`h-2 w-2 rounded-full ${pill.dot}`} />
        <span className={`text-xs ${pill.text}`}>{pill.label}</span>
      </div>
      <p className="mb-2 text-xs text-[#2a5a7a] dark:text-gray-400">
        {totalCount > 0 ? `${healthyCount}/${totalCount} healthy` : 'No addons'}
      </p>
      <AddonDots addons={addonSummary} />
    </div>
  )
}

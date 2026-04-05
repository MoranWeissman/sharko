import { useNavigate } from 'react-router-dom'
import { Server } from 'lucide-react'
import { AddonDots } from '@/components/AddonDots'

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

export function ClusterCard({
  name,
  connectionStatus,
  addonSummary,
  healthyCount,
  totalCount,
}: ClusterCardProps) {
  const navigate = useNavigate()
  const isConnected = connectionStatus === 'Successful' || connectionStatus === 'Connected'

  return (
    <div
      onClick={() => navigate(`/clusters/${name}`)}
      onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); navigate(`/clusters/${name}`) } }}
      role="button"
      tabIndex={0}
      className="group cursor-pointer rounded-xl border border-[#90c8ee] bg-[#f0f7ff] p-4 shadow-sm transition-all duration-150 hover:-translate-y-0.5 hover:border-teal-400 hover:shadow-md dark:border-gray-700 dark:bg-gray-800 dark:hover:border-teal-500"
    >
      <div className="mb-2 flex items-center gap-2">
        <Server className="h-4 w-4 text-teal-600 dark:text-teal-400" />
        <h3 className="truncate text-sm font-bold text-[#0a2a4a] dark:text-gray-100">{name}</h3>
      </div>
      <div className="mb-2 flex items-center gap-1.5">
        <div className={`h-2 w-2 rounded-full ${isConnected ? 'bg-green-500' : 'bg-red-500'}`} />
        <span className={`text-xs ${isConnected ? 'text-green-700 dark:text-green-400' : 'text-red-700 dark:text-red-400'}`}>
          {isConnected ? 'Connected' : 'Disconnected'}
        </span>
      </div>
      <p className="mb-2 text-xs text-[#2a5a7a] dark:text-gray-400">
        {totalCount > 0 ? `${healthyCount}/${totalCount} healthy` : 'No addons'}
      </p>
      <AddonDots addons={addonSummary} />
    </div>
  )
}

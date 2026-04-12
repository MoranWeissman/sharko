import { AlertTriangle } from 'lucide-react';
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip';

interface StatusBadgeProps {
  status: string;
  size?: 'sm' | 'md';
  testFailing?: boolean;
}

// --- Cluster status definitions (5-state) ---

interface ClusterStatusDef {
  dot: string;
  bg: string;
  text: string;
  label: string;
  tooltip: string;
}

const clusterStatusMap: Record<string, ClusterStatusDef> = {
  unknown: {
    dot: 'bg-[#3a6a8a] dark:bg-gray-400',
    bg: 'bg-[#d6eeff] dark:bg-gray-700',
    text: 'text-[#1a4a6a] dark:text-gray-400',
    label: 'Unknown',
    tooltip: 'Cluster not yet tested. Run a test to verify connectivity.',
  },
  connected: {
    dot: 'bg-green-500',
    bg: 'bg-green-50 dark:bg-green-900/30',
    text: 'text-green-700 dark:text-green-400',
    label: 'Connected',
    tooltip: 'Stage 1 test passed. Sharko can create and manage secrets.',
  },
  verified: {
    dot: 'bg-blue-500',
    bg: 'bg-blue-50 dark:bg-blue-900/30',
    text: 'text-blue-700 dark:text-blue-400',
    label: 'Verified',
    tooltip: 'Stage 2 test passed. Full ArgoCD pipeline verified.',
  },
  operational: {
    dot: 'bg-purple-500',
    bg: 'bg-purple-50 dark:bg-purple-900/30',
    text: 'text-purple-700 dark:text-purple-400',
    label: 'Operational',
    tooltip: 'At least one addon is deployed and healthy.',
  },
  unreachable: {
    dot: 'bg-red-500',
    bg: 'bg-red-50 dark:bg-red-900/30',
    text: 'text-red-700 dark:text-red-400',
    label: 'Unreachable',
    tooltip: 'Last test failed. Check IAM and network access.',
  },
};

const CLUSTER_STATUSES = ['unknown', 'connected', 'verified', 'operational', 'unreachable'];

export function isClusterStatus(status: string): boolean {
  return CLUSTER_STATUSES.includes(status.toLowerCase());
}

export function getClusterStatusDef(status: string): ClusterStatusDef {
  return clusterStatusMap[status.toLowerCase()] ?? clusterStatusMap.unknown;
}

export { clusterStatusMap, CLUSTER_STATUSES };

// --- Addon status colors (existing) ---

function getStatusColor(status: string): { dot: string; bg: string; text: string } {
  const s = status.toLowerCase();

  // Check cluster statuses first
  if (clusterStatusMap[s]) {
    const def = clusterStatusMap[s];
    return { dot: def.dot, bg: def.bg, text: def.text };
  }

  if (['healthy', 'synced', 'completed'].includes(s)) {
    return { dot: 'bg-green-500', bg: 'bg-green-50 dark:bg-green-900/30', text: 'text-green-700 dark:text-green-400' };
  }
  if (['degraded', 'unhealthy', 'failed', 'error'].includes(s)) {
    return { dot: 'bg-red-500', bg: 'bg-red-50 dark:bg-red-900/30', text: 'text-red-700 dark:text-red-400' };
  }
  if (['running', 'progressing', 'outofsync'].includes(s)) {
    return { dot: 'bg-blue-500', bg: 'bg-blue-50 dark:bg-blue-900/30', text: 'text-blue-700 dark:text-blue-400' };
  }
  if (['waiting', 'gated', 'paused', 'warning', 'missing', 'missing_in_argocd'].includes(s)) {
    return { dot: 'bg-amber-500', bg: 'bg-amber-50 dark:bg-amber-900/30', text: 'text-amber-700 dark:text-amber-400' };
  }
  if (['cancelled'].includes(s)) {
    return { dot: 'bg-[#2a5a7a]', bg: 'bg-[#d6eeff] dark:bg-gray-700', text: 'text-[#1a4a6a] dark:text-gray-400' };
  }
  if (['untracked_in_argocd', 'not_in_git'].includes(s)) {
    return { dot: 'bg-purple-500', bg: 'bg-purple-50 dark:bg-purple-900/30', text: 'text-purple-700 dark:text-purple-400' };
  }

  return { dot: 'bg-[#3a6a8a]', bg: 'bg-[#d6eeff] dark:bg-gray-700', text: 'text-[#1a4a6a] dark:text-gray-300' };
}

const statusDisplayNames: Record<string, string> = {
  disabled_in_git: 'Not Enabled',
  missing_in_argocd: 'Not Deployed',
  untracked_in_argocd: 'Unmanaged',
  unknown_state: 'Unknown',
  unknown_health: 'Unknown',
};

export function statusDisplayName(status: string): string {
  const s = status.toLowerCase();
  // Cluster statuses have their own display names
  if (clusterStatusMap[s]) return clusterStatusMap[s].label;
  return statusDisplayNames[s] ?? status;
}

export function StatusBadge({ status, size = 'sm', testFailing }: StatusBadgeProps) {
  const colors = getStatusColor(status);
  const sizeClasses =
    size === 'md' ? 'px-2.5 py-1 text-sm' : 'px-2 py-0.5 text-xs';

  const s = status.toLowerCase();
  const clusterDef = clusterStatusMap[s];

  // If this is a cluster status with a tooltip, wrap in Tooltip
  if (clusterDef) {
    const tooltipText = testFailing
      ? `${clusterDef.tooltip} (Test currently failing — verify connectivity.)`
      : clusterDef.tooltip;

    return (
      <TooltipProvider>
        <Tooltip>
          <TooltipTrigger asChild>
            <span
              className={`inline-flex items-center gap-1.5 rounded-full font-medium ${colors.bg} ${colors.text} ${sizeClasses} cursor-help`}
            >
              <span className={`inline-block h-2 w-2 rounded-full ${colors.dot}`} />
              {statusDisplayName(status)}
              {testFailing && (
                <AlertTriangle className="h-3 w-3 text-amber-500" />
              )}
            </span>
          </TooltipTrigger>
          <TooltipContent>
            <p className="max-w-xs text-xs">{tooltipText}</p>
          </TooltipContent>
        </Tooltip>
      </TooltipProvider>
    );
  }

  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full font-medium ${colors.bg} ${colors.text} ${sizeClasses}`}
    >
      <span className={`inline-block h-2 w-2 rounded-full ${colors.dot}`} />
      {statusDisplayName(status)}
    </span>
  );
}

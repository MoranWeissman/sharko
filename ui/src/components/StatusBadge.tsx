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

// --- Cluster status definitions (5-state Sharko test ladder) ---
//
// V2-cleanup-61.2 (findings D3 + E2): these are Sharko's OWN test results
// for a cluster (Unknown → Connected → Verified → Operational, plus
// Unreachable). Color law: green family = good (green / teal / emerald for
// the three good rungs), red = problem, neutral = no information. Purple
// is retired everywhere — it used to mean both "best state" and "warning".
// Tooltips say what each test actually verified, in plain words — no
// internal "Stage 1"/"Stage 2" vocabulary.

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
    tooltip:
      'Sharko reached the cluster directly and confirmed it can create and manage secrets there.',
  },
  verified: {
    dot: 'bg-teal-500',
    bg: 'bg-teal-50 dark:bg-teal-900/30',
    text: 'text-teal-700 dark:text-teal-400',
    label: 'Verified',
    tooltip:
      'A test deployment went through ArgoCD successfully — the full deploy path to this cluster works.',
  },
  operational: {
    dot: 'bg-emerald-600',
    bg: 'bg-emerald-50 dark:bg-emerald-900/30',
    text: 'text-emerald-700 dark:text-emerald-400',
    label: 'Operational',
    tooltip: 'At least one addon is deployed and healthy on this cluster.',
  },
  unreachable: {
    dot: 'bg-red-500',
    bg: 'bg-red-50 dark:bg-red-900/30',
    text: 'text-red-700 dark:text-red-400',
    label: 'Unreachable',
    tooltip: 'The last connection test failed. Check IAM and network access.',
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

  // Audit result statuses (from audit log)
  if (s === 'success' || s === 'succeeded') {
    return { dot: 'bg-green-500', bg: 'bg-green-50 dark:bg-green-900/30', text: 'text-green-700 dark:text-green-400' };
  }
  if (s === 'partial') {
    return { dot: 'bg-amber-500', bg: 'bg-amber-50 dark:bg-amber-900/30', text: 'text-amber-700 dark:text-amber-400' };
  }
  if (s === 'rejected') {
    return { dot: 'bg-orange-500', bg: 'bg-orange-50 dark:bg-orange-900/30', text: 'text-orange-700 dark:text-orange-400' };
  }
  if (s === 'failure' || s === 'failed') {
    return { dot: 'bg-red-500', bg: 'bg-red-50 dark:bg-red-900/30', text: 'text-red-700 dark:text-red-400' };
  }

  if (['healthy', 'synced', 'completed'].includes(s)) {
    return { dot: 'bg-green-500', bg: 'bg-green-50 dark:bg-green-900/30', text: 'text-green-700 dark:text-green-400' };
  }
  // Problem states are red — including "missing" (enabled in the catalog
  // but ArgoCD has no Application for it), which used to render amber
  // (V2-cleanup-61.2, findings D1 + D3).
  if (['degraded', 'unhealthy', 'failed', 'error', 'sync_failing', 'missing', 'missing_in_argocd'].includes(s)) {
    return { dot: 'bg-red-500', bg: 'bg-red-50 dark:bg-red-900/30', text: 'text-red-700 dark:text-red-400' };
  }
  if (['running', 'progressing', 'outofsync', 'deploying'].includes(s)) {
    return { dot: 'bg-blue-500', bg: 'bg-blue-50 dark:bg-blue-900/30', text: 'text-blue-700 dark:text-blue-400' };
  }
  if (['waiting', 'gated', 'paused', 'warning'].includes(s)) {
    return { dot: 'bg-amber-500', bg: 'bg-amber-50 dark:bg-amber-900/30', text: 'text-amber-700 dark:text-amber-400' };
  }
  if (['cancelled'].includes(s)) {
    return { dot: 'bg-[#2a5a7a]', bg: 'bg-[#d6eeff] dark:bg-gray-700', text: 'text-[#1a4a6a] dark:text-gray-400' };
  }
  // "Not managed" states are amber attention, NOT purple — purple used to
  // collide with the best-state Operational color (V2-cleanup-61.2, D3).
  if (['untracked_in_argocd', 'not_in_git'].includes(s)) {
    return { dot: 'bg-amber-500', bg: 'bg-amber-50 dark:bg-amber-900/30', text: 'text-amber-700 dark:text-amber-400' };
  }
  if (s === 'sharko_system') {
    return { dot: 'bg-[#6aade0]', bg: 'bg-[#d6eeff] dark:bg-blue-900/30', text: 'text-[#1a4a6a] dark:text-blue-300' };
  }

  return { dot: 'bg-[#3a6a8a]', bg: 'bg-[#d6eeff] dark:bg-gray-700', text: 'text-[#1a4a6a] dark:text-gray-300' };
}

const statusDisplayNames: Record<string, string> = {
  disabled_in_git: 'Not Enabled',
  // The PROBLEM name (V2-cleanup-61.2, D1): enabled in the catalog but
  // ArgoCD has no Application for it. Distinct from the benign
  // "Not deployed yet" (in the catalog, enabled nowhere).
  missing_in_argocd: 'Missing from ArgoCD',
  // Same word everywhere: matches the cluster-level "Not managed" state.
  untracked_in_argocd: 'Not managed',
  sharko_system: 'Sharko system',
  unknown_state: 'Unknown',
  unknown_health: 'Unknown',
  sync_failing: 'Sync failing',
  deploying: 'Deploying',
  // Audit result display names
  success: 'Succeeded',
  succeeded: 'Succeeded',
  partial: 'Partly done',
  rejected: 'Rejected',
  failure: 'Failed',
  failed: 'Failed',
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
              className={`inline-flex items-center gap-1.5 rounded-full font-medium whitespace-nowrap ${colors.bg} ${colors.text} ${sizeClasses} cursor-help`}
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
      className={`inline-flex items-center gap-1.5 rounded-full font-medium whitespace-nowrap ${colors.bg} ${colors.text} ${sizeClasses}`}
    >
      <span className={`inline-block h-2 w-2 rounded-full ${colors.dot}`} />
      {statusDisplayName(status)}
    </span>
  );
}

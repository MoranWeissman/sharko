interface StatusBadgeProps {
  status: string;
  size?: 'sm' | 'md';
}

function getStatusColor(status: string): { dot: string; bg: string; text: string } {
  const s = status.toLowerCase();

  if (['healthy', 'synced', 'connected', 'completed'].includes(s)) {
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
    return { dot: 'bg-gray-500', bg: 'bg-gray-100 dark:bg-gray-700', text: 'text-gray-600 dark:text-gray-400' };
  }
  if (['untracked_in_argocd', 'not_in_git'].includes(s)) {
    return { dot: 'bg-purple-500', bg: 'bg-purple-50 dark:bg-purple-900/30', text: 'text-purple-700 dark:text-purple-400' };
  }

  return { dot: 'bg-gray-400', bg: 'bg-gray-100 dark:bg-gray-700', text: 'text-gray-600 dark:text-gray-300' };
}

const statusDisplayNames: Record<string, string> = {
  disabled_in_git: 'Not Enabled',
  missing_in_argocd: 'Not Deployed',
  untracked_in_argocd: 'Unmanaged',
  unknown_state: 'Unknown',
  unknown_health: 'Unknown',
};

export function statusDisplayName(status: string): string {
  return statusDisplayNames[status.toLowerCase()] ?? status;
}

export function StatusBadge({ status, size = 'sm' }: StatusBadgeProps) {
  const colors = getStatusColor(status);
  const sizeClasses =
    size === 'md' ? 'px-2.5 py-1 text-sm' : 'px-2 py-0.5 text-xs';

  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full font-medium ${colors.bg} ${colors.text} ${sizeClasses}`}
    >
      <span className={`inline-block h-2 w-2 rounded-full ${colors.dot}`} />
      {statusDisplayName(status)}
    </span>
  );
}

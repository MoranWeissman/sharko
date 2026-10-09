import { useState } from 'react';
import { RefreshCw } from 'lucide-react';
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip';

interface RefreshButtonProps {
  onRefresh: () => void | Promise<void>;
  lastUpdated?: Date | string;
  tooltip?: string;
}

function formatRelativeTime(date: Date | string): string {
  const now = Date.now();
  const then = typeof date === 'string' ? new Date(date).getTime() : date.getTime();
  const seconds = Math.floor((now - then) / 1000);

  if (seconds < 60) return 'Updated just now';
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `Updated ${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `Updated ${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `Updated ${days}d ago`;
}

/**
 * RefreshButton — labeled Refresh button with spinner + last-updated (V3 RW1.1).
 *
 * A button with a RefreshCw (lucide) icon AND the word "Refresh" (not icon-only),
 * matching primary/secondary button styling. Shows a spinner (animate-spin on the
 * icon) while the refresh promise is in flight. Optional lastUpdated renders a
 * muted "Updated <relative>" beside/under it. Optional tooltip via existing
 * tooltip component.
 */
export function RefreshButton({ onRefresh, lastUpdated, tooltip }: RefreshButtonProps) {
  const [isRefreshing, setIsRefreshing] = useState(false);

  const handleRefresh = async () => {
    if (isRefreshing) return;
    setIsRefreshing(true);
    try {
      await onRefresh();
    } finally {
      setIsRefreshing(false);
    }
  };

  const button = (
    <button
      type="button"
      onClick={handleRefresh}
      disabled={isRefreshing}
      className="inline-flex items-center gap-2 rounded-md border-2 border-[#6aade0] bg-[#f0f7ff] px-3 py-1.5 text-sm font-medium text-[#0a2a4a] hover:bg-[#e0f0ff] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-teal-500 disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 dark:hover:bg-gray-700"
    >
      <RefreshCw className={`h-4 w-4 ${isRefreshing ? 'animate-spin' : ''}`} />
      Refresh
    </button>
  );

  const wrappedButton = tooltip ? (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger asChild>{button}</TooltipTrigger>
        <TooltipContent>
          <p className="text-xs">{tooltip}</p>
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  ) : (
    button
  );

  if (!lastUpdated) {
    return wrappedButton;
  }

  return (
    <div className="inline-flex items-center gap-3">
      {wrappedButton}
      <span className="text-sm text-[#5a8aaa] dark:text-gray-500">
        {formatRelativeTime(lastUpdated)}
      </span>
    </div>
  );
}

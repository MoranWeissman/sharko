import { CheckCircle2, XCircle } from 'lucide-react';
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip';

interface ConnectivityBadgeProps {
  /** 'verified_argocd' | 'verified_check' | 'check_failed' | '' | undefined */
  connectivityStatus?: string;
  /** Reason string when connectivityStatus === 'check_failed' */
  connectivityDetail?: string;
  /** Sharko test reaching status string (e.g. "Connected", "Unreachable") */
  sharkoStatus?: string;
  /** RFC3339 timestamp of last Sharko test */
  lastTestAt?: string;
  /** True when the last Sharko test failed */
  testFailing?: boolean;
  /** Error code from the last failing Sharko test */
  testErrorCode?: string;
}

// Format a UTC ISO-8601 timestamp as a relative "X ago" string.
function relativeTime(isoString: string): string {
  const then = new Date(isoString).getTime();
  const now = Date.now();
  const diffMs = now - then;
  if (diffMs < 0) return 'just now';
  const secs = Math.floor(diffMs / 1000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  return `${days}d ago`;
}

/**
 * ConnectivityBadge renders the ArgoCD-deployed connectivity check verdict
 * and the Sharko connectivity test result for a cluster.
 *
 * Priority:
 *  1. 'verified_argocd' or 'verified_check' → green "Connectivity verified ✓"
 *  2. 'check_failed' → red/amber "Connectivity check failed" + detail
 *  3. Nothing (empty/absent) → render nothing (ArgoCD status rendering stays as-is)
 *
 * Secondary: when sharkoStatus is present, show a line about Sharko's reach test.
 */
export function ConnectivityBadge({
  connectivityStatus,
  connectivityDetail,
  sharkoStatus,
  lastTestAt,
  testFailing,
  testErrorCode,
}: ConnectivityBadgeProps) {
  const primaryBadge = renderPrimary(connectivityStatus, connectivityDetail);
  const secondaryLine = renderSecondary(sharkoStatus, lastTestAt, testFailing, testErrorCode);

  if (!primaryBadge && !secondaryLine) return null;

  return (
    <div className="flex flex-col gap-1">
      {primaryBadge}
      {secondaryLine}
    </div>
  );
}

function renderPrimary(status?: string, detail?: string) {
  if (!status) return null;

  if (status === 'verified_argocd' || status === 'verified_check') {
    const tooltipText =
      status === 'verified_argocd'
        ? 'ArgoCD successfully connected to this cluster'
        : 'Sharko deployed a test workload through ArgoCD and it succeeded — the full deployment pipeline is working';
    return (
      <TooltipProvider>
        <Tooltip>
          <TooltipTrigger asChild>
            <span className="inline-flex items-center gap-1.5 rounded-full bg-green-50 px-2 py-0.5 text-xs font-medium text-green-700 dark:bg-green-900/30 dark:text-green-400 cursor-help">
              <CheckCircle2 className="h-3 w-3" />
              Connectivity verified ✓
            </span>
          </TooltipTrigger>
          <TooltipContent>
            <p className="max-w-xs text-xs">{tooltipText}</p>
          </TooltipContent>
        </Tooltip>
      </TooltipProvider>
    );
  }

  if (status === 'check_failed') {
    const tooltipContent = detail
      ? detail
      : 'Connectivity check failed — inspect the connectivity-check Application in ArgoCD for details';
    return (
      <TooltipProvider>
        <Tooltip>
          <TooltipTrigger asChild>
            <span
              className="inline-flex items-center gap-1.5 rounded-full bg-amber-50 px-2 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-400 cursor-help"
              data-detail={detail || undefined}
            >
              <XCircle className="h-3 w-3" />
              Connectivity check failed
            </span>
          </TooltipTrigger>
          <TooltipContent>
            <p className="max-w-xs text-xs">{tooltipContent}</p>
          </TooltipContent>
        </Tooltip>
      </TooltipProvider>
    );
  }

  return null;
}

function renderSecondary(
  sharkoStatus?: string,
  lastTestAt?: string,
  testFailing?: boolean,
  testErrorCode?: string,
) {
  if (!sharkoStatus) return null;

  const timeStr = lastTestAt ? relativeTime(lastTestAt) : null;

  if (testFailing) {
    return (
      <span className="text-xs text-amber-600 dark:text-amber-400">
        Sharko test failed{testErrorCode ? ` (${testErrorCode})` : ''}
        {timeStr && ` · ${timeStr}`}
      </span>
    );
  }

  if (timeStr) {
    return (
      <span className="text-xs text-[#3a6a8a] dark:text-gray-400">
        Sharko can reach it · tested {timeStr}
      </span>
    );
  }

  return (
    <span className="text-xs text-[#3a6a8a] dark:text-gray-400">
      Sharko status: {sharkoStatus}
    </span>
  );
}

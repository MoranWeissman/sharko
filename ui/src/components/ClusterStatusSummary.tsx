// V2-cleanup-61.2 (finding D4): ONE composite status pill per cluster row.
//
// A managed-cluster row used to stack up to four separate pills/captions
// (whose-connection caption, ArgoCD status badge, ownership caption,
// connectivity-check badge + Sharko-test line) whose distinctions lived in
// hover-only tooltips — invisible on touch and keyboard. This component
// folds them into ONE pill showing the WORST of the parts, with an
// accessible popover (keyboard + touch friendly, via the shadcn/radix
// Popover) that lists the full breakdown.
//
// The worst-of ordering is explicit: SEVERITY_ORDER in lib/clusterStatus.ts
// (problem > attention > pending > unknown > good).

import { ChevronDown } from 'lucide-react';
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover';
import {
  getClusterConnectionState,
  worstSeverity,
  type StatusSeverity,
} from '@/lib/clusterStatus';
import {
  ARGOCD_CONN_LABEL,
  ARGOCD_CONN_TOOLTIP,
  SHARKO_CONN_LABEL,
  SHARKO_CONN_TOOLTIP,
} from '@/components/WhoseConnectionLabel';
import {
  CONN_OWNER_USER_LABEL,
  CONN_OWNER_USER_TOOLTIP,
} from '@/components/ConnectionOwnerBadge';

/** One line of the status breakdown. */
export interface ClusterStatusPart {
  /** Whose signal this is (e.g. "ArgoCD → cluster"). */
  who: string;
  /** Explanation of the `who` attribution. */
  whoTooltip?: string;
  /** The status name shown to the user. */
  label: string;
  severity: StatusSeverity;
  /** One-sentence plain-English meaning. */
  meaning: string;
}

export interface ClusterStatusSummaryProps {
  /** ArgoCD's own connection state for the cluster (connection_status). */
  connectionStatus?: string;
  /** 'verified_argocd' | 'verified_check' | 'check_pending' | 'check_failed' | '' */
  connectivityStatus?: string;
  connectivityDetail?: string;
  /** Sharko observation status (sharko_status), e.g. "Connected". */
  sharkoStatus?: string;
  /** RFC3339 timestamp of the last Sharko test. */
  lastTestAt?: string;
  testFailing?: boolean;
  testErrorCode?: string;
  /** 'user' when the ArgoCD cluster secret is self-managed. */
  connectionManagedBy?: string;
}

// Severity → pill + dot styling. ONE color per severity (finding D3):
// red = problem, amber = attention, blue-neutral = in progress,
// gray-neutral = unknown, green = good.
export const SEVERITY_STYLES: Record<
  StatusSeverity,
  { dot: string; pill: string }
> = {
  problem: {
    dot: 'bg-red-500',
    pill: 'bg-red-50 text-red-700 dark:bg-red-900/30 dark:text-red-400',
  },
  attention: {
    dot: 'bg-amber-500',
    pill: 'bg-amber-50 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400',
  },
  pending: {
    dot: 'bg-[#3a6a8a] dark:bg-gray-400',
    pill: 'bg-[#d6eeff] text-[#1a4a6a] dark:bg-gray-700 dark:text-gray-300',
  },
  unknown: {
    dot: 'bg-[#3a6a8a] dark:bg-gray-400',
    pill: 'bg-[#d6eeff] text-[#1a4a6a] dark:bg-gray-700 dark:text-gray-300',
  },
  good: {
    dot: 'bg-green-500',
    pill: 'bg-green-50 text-green-700 dark:bg-green-900/30 dark:text-green-400',
  },
};

function relativeTime(isoString: string): string {
  const diffMs = Date.now() - new Date(isoString).getTime();
  if (diffMs < 0) return 'just now';
  const secs = Math.floor(diffMs / 1000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}

/**
 * Builds the ordered list of status parts for a cluster. Pure — exported
 * for tests.
 */
export function clusterStatusParts(props: ClusterStatusSummaryProps): ClusterStatusPart[] {
  const parts: ClusterStatusPart[] = [];

  // 1. ArgoCD's own connection — always present.
  const conn = getClusterConnectionState(props.connectionStatus);
  parts.push({
    who: ARGOCD_CONN_LABEL,
    whoTooltip: ARGOCD_CONN_TOOLTIP,
    label: conn.label,
    severity: conn.severity,
    meaning: conn.meaning,
  });

  // 2. Deploy-path check (a test workload deployed through ArgoCD).
  // Only surface when pending or failed — not the standing success/"Verified" row.
  const cs = props.connectivityStatus ?? '';
  if (cs === 'check_pending') {
    parts.push({
      who: 'Deploy check',
      label: 'Running…',
      severity: 'pending',
      meaning:
        props.connectivityDetail ||
        'The deploy check is still rolling out — usually under a minute.',
    });
  } else if (cs === 'check_failed') {
    parts.push({
      who: 'Deploy check',
      label: 'Failed',
      severity: 'attention',
      meaning:
        props.connectivityDetail ||
        'The test workload could not be deployed to this cluster.',
    });
  }

  // 3. Sharko's own direct connection test.
  if (props.sharkoStatus) {
    const when = props.lastTestAt ? ` Tested ${relativeTime(props.lastTestAt)}.` : '';
    if (props.testFailing) {
      parts.push({
        who: SHARKO_CONN_LABEL,
        whoTooltip: SHARKO_CONN_TOOLTIP,
        label: 'Test failing',
        severity: 'attention',
        meaning: `Sharko's last direct connection test failed${
          props.testErrorCode ? ` (${props.testErrorCode})` : ''
        }.${when}`,
      });
    } else {
      parts.push({
        who: SHARKO_CONN_LABEL,
        whoTooltip: SHARKO_CONN_TOOLTIP,
        label: 'Reachable',
        severity: 'good',
        meaning: `Sharko can reach this cluster directly.${when}`,
      });
    }
  }

  return parts;
}

/**
 * The composite state is the WORST of the parts, per SEVERITY_ORDER
 * (problem > attention > pending > unknown > good). Pure — exported for
 * tests.
 */
export function compositeClusterStatus(parts: ClusterStatusPart[]): ClusterStatusPart {
  const worst = worstSeverity(parts.map((p) => p.severity));
  return parts.find((p) => p.severity === worst) ?? parts[0];
}

export function ClusterStatusSummary(props: ClusterStatusSummaryProps) {
  const parts = clusterStatusParts(props);
  const composite = compositeClusterStatus(parts);
  const style = SEVERITY_STYLES[composite.severity];
  const selfManaged = props.connectionManagedBy === 'user';

  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          onClick={(e) => e.stopPropagation()}
          aria-label={`Cluster status: ${composite.label} — show details`}
          data-testid="cluster-status-pill"
          className={`inline-flex w-fit items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium ${style.pill} focus:outline-none focus-visible:ring-2 focus-visible:ring-teal-500`}
        >
          <span className={`inline-block h-2 w-2 rounded-full ${style.dot}`} />
          {composite.label}
          <ChevronDown className="h-3 w-3 opacity-70" aria-hidden="true" />
        </button>
      </PopoverTrigger>
      <PopoverContent
        align="start"
        className="w-80 space-y-3 text-left"
        onClick={(e) => e.stopPropagation()}
      >
        {parts.map((part) => {
          const partStyle = SEVERITY_STYLES[part.severity];
          return (
            <div key={`${part.who}-${part.label}`}>
              <p
                className="cursor-help text-xs font-medium text-[#5a8aaa] dark:text-gray-500"
                title={part.whoTooltip}
              >
                {part.who}
              </p>
              <p className="mt-0.5 flex items-center gap-1.5 text-sm font-medium">
                <span className={`inline-block h-2 w-2 rounded-full ${partStyle.dot}`} />
                {part.label}
              </p>
              <p className="mt-0.5 text-xs text-[#2a5a7a] dark:text-gray-400">{part.meaning}</p>
            </div>
          );
        })}
        {selfManaged && (
          <p className="border-t border-[#c0ddf0] pt-2 text-xs text-[#2a5a7a] dark:border-gray-700 dark:text-gray-400">
            <span className="font-medium">{CONN_OWNER_USER_LABEL}.</span>{' '}
            {CONN_OWNER_USER_TOOLTIP}
          </p>
        )}
      </PopoverContent>
    </Popover>
  );
}

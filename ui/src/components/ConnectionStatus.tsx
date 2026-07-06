import { CheckCircle, XCircle, Clock, GitMerge } from 'lucide-react';
import {
  classifyClusterConnection,
  CLUSTER_CONNECTION_STATES,
  type ClusterConnectionKind,
} from '@/lib/clusterStatus';

// V2-cleanup-61.2 (finding D2): this component renders the canonical
// "ArgoCD → cluster" connection vocabulary from lib/clusterStatus.ts —
// the same names, colors, and meanings used by ClusterCard, the stat
// cards, and the legend. Do not invent labels here.

interface ConnectionStatusProps {
  status: string;
}

const KIND_ICONS: Record<ClusterConnectionKind, React.ElementType> = {
  connected: CheckCircle,
  pending: Clock,
  unmanaged: GitMerge,
  failed: XCircle,
};

export function ConnectionStatus({ status }: ConnectionStatusProps) {
  const kind = classifyClusterConnection(status);
  const def = CLUSTER_CONNECTION_STATES[kind];
  const Icon = KIND_ICONS[kind];

  return (
    <span
      className={`inline-flex items-center gap-1.5 ${def.text}`}
      title={def.meaning}
    >
      <Icon className="h-4 w-4" />
      <span className="text-sm font-medium">{def.label}</span>
    </span>
  );
}

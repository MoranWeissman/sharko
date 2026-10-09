import {
  CLUSTER_CONNECTION_KINDS,
  CLUSTER_CONNECTION_STATES,
} from '@/lib/clusterStatus';

// V2-cleanup-61.2 (finding D2): the legend lists EXACTLY the connection
// states the Clusters view can display — the canonical "ArgoCD → cluster"
// vocabulary from lib/clusterStatus.ts. It previously explained a
// five-state test ladder the table almost never showed while omitting the
// states it did show.
export function ClusterStatusLegend() {
  return (
    <div className="flex flex-wrap items-center gap-4 rounded-lg bg-[#d0e8f8] px-4 py-2 text-xs dark:bg-gray-900">
      <span className="font-semibold text-[#0a2a4a] dark:text-gray-200">Cluster Status:</span>
      {CLUSTER_CONNECTION_KINDS.map((key) => {
        const def = CLUSTER_CONNECTION_STATES[key];
        return (
          <span
            key={key}
            className="inline-flex cursor-help items-center gap-1.5"
            title={def.meaning}
          >
            <span className={`inline-block h-2.5 w-2.5 rounded-full ${def.dot}`} />
            <span className="text-[#2a5a7a] dark:text-gray-400">{def.label}</span>
          </span>
        );
      })}
    </div>
  );
}

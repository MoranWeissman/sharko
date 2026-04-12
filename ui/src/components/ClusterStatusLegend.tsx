import { CLUSTER_STATUSES, clusterStatusMap } from '@/components/StatusBadge';

export function ClusterStatusLegend() {
  return (
    <div className="flex flex-wrap items-center gap-4 rounded-lg bg-[#d0e8f8] px-4 py-2 text-xs dark:bg-gray-900">
      <span className="font-semibold text-[#0a2a4a] dark:text-gray-200">Cluster Status:</span>
      {CLUSTER_STATUSES.map((key) => {
        const def = clusterStatusMap[key];
        return (
          <span key={key} className="inline-flex items-center gap-1.5">
            <span className={`inline-block h-2.5 w-2.5 rounded-full ${def.dot}`} />
            <span className="text-[#2a5a7a] dark:text-gray-400">{def.label}</span>
          </span>
        );
      })}
    </div>
  );
}

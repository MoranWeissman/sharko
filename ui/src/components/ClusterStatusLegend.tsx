import {
  CLUSTER_CONNECTION_KINDS,
  CLUSTER_CONNECTION_STATES,
  NO_ADDONS_YET_STATE,
  type ClusterConnectionStateDef,
} from '@/lib/clusterStatus';

// V2-cleanup-61.2 (finding D2): the legend lists EXACTLY the connection
// states the Clusters view can display, from lib/clusterStatus.ts. Ep3 dry
// run (finding 15): the meanings are written on screen, not hidden in hover
// text (which cannot be read in a recording or on a touch screen), and
// "Not checked yet" is listed because a cluster with nothing deployed shows it.
const LEGEND_ENTRIES: ClusterConnectionStateDef[] = (() => {
  const defs = CLUSTER_CONNECTION_KINDS.map((key) => CLUSTER_CONNECTION_STATES[key]);
  // "Not checked yet" belongs right after "Connecting…".
  const at = CLUSTER_CONNECTION_KINDS.indexOf('pending') + 1;
  return [...defs.slice(0, at), NO_ADDONS_YET_STATE, ...defs.slice(at)];
})();

export function ClusterStatusLegend() {
  return (
    <div className="rounded-lg bg-[#d0e8f8] px-4 py-3 text-xs dark:bg-gray-900">
      <p className="font-semibold text-[#0a2a4a] dark:text-gray-200">Cluster Status:</p>
      <ul className="mt-2 grid gap-x-6 gap-y-2 sm:grid-cols-2">
        {LEGEND_ENTRIES.map((def) => (
          <li key={def.label} className="flex items-start gap-2">
            <span
              className={`mt-1 inline-block h-2.5 w-2.5 shrink-0 rounded-full ${def.pillDot ?? def.dot}`}
            />
            <span>
              <span className="font-medium text-[#0a2a4a] dark:text-gray-200">{def.label}</span>
              <span className="block text-[#2a5a7a] dark:text-gray-400">{def.meaning}</span>
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

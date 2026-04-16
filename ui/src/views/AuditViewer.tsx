import { useState, useEffect, useCallback, useRef } from 'react';
import { ClipboardList, RefreshCw, Radio, Loader2, Key, User as UserIcon, Users as UsersIcon } from 'lucide-react';
import { fetchAuditLog, createAuditStream } from '@/services/api';
import type { AuditEntry } from '@/services/models';

/** Renders the small attribution-mode icon for an audit entry. */
function AttributionIcon({ mode }: { mode?: AuditEntry['attribution_mode'] }) {
  if (!mode) return <span className="text-[#5a8aaa] dark:text-gray-600">—</span>;
  switch (mode) {
    case 'service':
      return (
        <span title="Service token used — no human author on the commit" className="inline-flex">
          <Key className="h-3.5 w-3.5 text-[#3a6a8a] dark:text-gray-500" aria-label="Service token" />
        </span>
      );
    case 'per_user':
      return (
        <span title="Per-user PAT used — commit authored by the user" className="inline-flex">
          <UserIcon className="h-3.5 w-3.5 text-emerald-600 dark:text-emerald-400" aria-label="Per-user PAT" />
        </span>
      );
    case 'co_author':
      return (
        <span title="Service token used; user listed as Co-authored-by trailer" className="inline-flex">
          <UsersIcon className="h-3.5 w-3.5 text-amber-600 dark:text-amber-400" aria-label="Co-authored" />
        </span>
      );
    default:
      return <span className="text-[#5a8aaa] dark:text-gray-600">—</span>;
  }
}

const ACTION_OPTIONS = ['', 'create', 'update', 'delete', 'test', 'diagnose', 'sync', 'init'];
const SOURCE_OPTIONS = ['', 'ui', 'api', 'cli', 'webhook'];
const RESULT_OPTIONS = ['', 'success', 'failure', 'error', 'partial'];

export function AuditViewer() {
  const [entries, setEntries] = useState<AuditEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [liveTail, setLiveTail] = useState(false);
  const [liveEntries, setLiveEntries] = useState<AuditEntry[]>([]);
  const eventSourceRef = useRef<EventSource | null>(null);

  // Filter state
  const [filterUser, setFilterUser] = useState('');
  const [filterAction, setFilterAction] = useState('');
  const [filterSource, setFilterSource] = useState('');
  const [filterResult, setFilterResult] = useState('');
  const [filterCluster, setFilterCluster] = useState('');
  const [filterSince, setFilterSince] = useState('');

  const fetchData = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const result = await fetchAuditLog({
        user: filterUser || undefined,
        action: filterAction || undefined,
        source: filterSource || undefined,
        result: filterResult || undefined,
        cluster: filterCluster || undefined,
        since: filterSince || undefined,
        limit: 200,
      });
      setEntries(result.entries ?? []);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to load audit log');
    } finally {
      setLoading(false);
    }
  }, [filterUser, filterAction, filterSource, filterResult, filterCluster, filterSince]);

  // Initial fetch + auto-refresh every 30s
  useEffect(() => {
    void fetchData();
    const interval = setInterval(() => {
      if (!liveTail) void fetchData();
    }, 30000);
    return () => clearInterval(interval);
  }, [fetchData, liveTail]);

  // Live tail SSE
  useEffect(() => {
    if (!liveTail) {
      if (eventSourceRef.current) {
        eventSourceRef.current.close();
        eventSourceRef.current = null;
      }
      setLiveEntries([]);
      return;
    }

    const source = createAuditStream();
    eventSourceRef.current = source;

    source.onmessage = (e) => {
      try {
        const entry = JSON.parse(e.data) as AuditEntry;
        setLiveEntries((prev) => [entry, ...prev].slice(0, 200));
      } catch {
        // ignore parse errors
      }
    };

    source.onerror = () => {
      // SSE will auto-reconnect
    };

    return () => {
      source.close();
      eventSourceRef.current = null;
    };
  }, [liveTail]);

  const displayEntries = liveTail ? [...liveEntries, ...entries] : entries;

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="text-2xl font-bold text-[#0a2a4a] dark:text-gray-100">Audit Log</h2>
          <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-400">
            Recent API events and operations across all clusters.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => setLiveTail((v) => !v)}
            className={`inline-flex items-center gap-1.5 rounded-lg px-3 py-2 text-xs font-medium transition-colors ${
              liveTail
                ? 'bg-green-100 text-green-700 ring-2 ring-green-300 dark:bg-green-900/30 dark:text-green-400 dark:ring-green-700'
                : 'ring-2 ring-[#6aade0] bg-[#e8f4ff] text-[#2a5a7a] hover:bg-[#d6eeff] dark:ring-gray-700 dark:bg-gray-800 dark:text-gray-400 dark:hover:bg-gray-700'
            }`}
          >
            <Radio className={`h-3.5 w-3.5 ${liveTail ? 'animate-pulse' : ''}`} />
            Live Tail
          </button>
          <button
            type="button"
            onClick={() => void fetchData()}
            disabled={loading}
            className="inline-flex items-center gap-1.5 rounded-lg ring-2 ring-[#6aade0] bg-[#e8f4ff] px-3 py-2 text-xs text-[#2a5a7a] hover:bg-[#d6eeff] disabled:opacity-50 dark:ring-gray-700 dark:bg-gray-800 dark:text-gray-400 dark:hover:bg-gray-700"
          >
            {loading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
            Refresh
          </button>
        </div>
      </div>

      {/* Filters */}
      <div className="rounded-lg ring-2 ring-[#6aade0] bg-[#d0e8f8] p-4 dark:ring-gray-700 dark:bg-gray-900">
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
          <div>
            <label className="mb-1 block text-xs font-medium text-[#0a3a5a] dark:text-gray-400">User</label>
            <input
              type="text"
              value={filterUser}
              onChange={(e) => setFilterUser(e.target.value)}
              placeholder="Any user"
              className="w-full rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-2 py-1.5 text-xs focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:placeholder-[#5a8aaa]"
            />
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-[#0a3a5a] dark:text-gray-400">Action</label>
            <select
              value={filterAction}
              onChange={(e) => setFilterAction(e.target.value)}
              className="w-full rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-2 py-1.5 text-xs focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200"
            >
              {ACTION_OPTIONS.map((opt) => (
                <option key={opt} value={opt}>{opt || 'All'}</option>
              ))}
            </select>
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-[#0a3a5a] dark:text-gray-400">Source</label>
            <select
              value={filterSource}
              onChange={(e) => setFilterSource(e.target.value)}
              className="w-full rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-2 py-1.5 text-xs focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200"
            >
              {SOURCE_OPTIONS.map((opt) => (
                <option key={opt} value={opt}>{opt || 'All'}</option>
              ))}
            </select>
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-[#0a3a5a] dark:text-gray-400">Result</label>
            <select
              value={filterResult}
              onChange={(e) => setFilterResult(e.target.value)}
              className="w-full rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-2 py-1.5 text-xs focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200"
            >
              {RESULT_OPTIONS.map((opt) => (
                <option key={opt} value={opt}>{opt || 'All'}</option>
              ))}
            </select>
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-[#0a3a5a] dark:text-gray-400">Cluster</label>
            <input
              type="text"
              value={filterCluster}
              onChange={(e) => setFilterCluster(e.target.value)}
              placeholder="Any cluster"
              className="w-full rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-2 py-1.5 text-xs focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:placeholder-[#5a8aaa]"
            />
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-[#0a3a5a] dark:text-gray-400">Since (RFC3339)</label>
            <input
              type="text"
              value={filterSince}
              onChange={(e) => setFilterSince(e.target.value)}
              placeholder="2024-01-01T00:00:00Z"
              className="w-full rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-2 py-1.5 text-xs focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:placeholder-[#5a8aaa]"
            />
          </div>
        </div>
      </div>

      {/* Error */}
      {error && (
        <div className="rounded-md bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-400">
          {error}
        </div>
      )}

      {/* Table */}
      <div className="overflow-x-auto rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] shadow-sm dark:ring-gray-700 dark:bg-gray-800">
        <table className="w-full text-left text-sm">
          <thead className="border-b border-[#6aade0] bg-[#d0e8f8] text-xs uppercase text-[#2a5a7a] dark:border-gray-700 dark:bg-gray-900 dark:text-gray-400">
            <tr>
              <th className="px-4 py-3">Timestamp</th>
              <th className="px-4 py-3">Event</th>
              <th className="px-4 py-3">User</th>
              <th className="px-4 py-3">Action</th>
              <th className="px-4 py-3">Resource</th>
              <th className="px-4 py-3">Source</th>
              <th className="px-4 py-3" title="Git commit attribution mode">Attr.</th>
              <th className="px-4 py-3">Result</th>
              <th className="px-4 py-3">Duration</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#6aade0] dark:divide-gray-700">
            {loading && displayEntries.length === 0 ? (
              <tr>
                <td colSpan={9} className="px-4 py-12 text-center">
                  <Loader2 className="mx-auto h-5 w-5 animate-spin text-[#2a5a7a] dark:text-gray-400" />
                </td>
              </tr>
            ) : displayEntries.length === 0 ? (
              <tr>
                <td colSpan={8} className="px-4 py-12 text-center text-[#3a6a8a] dark:text-gray-500">
                  <ClipboardList className="mx-auto mb-2 h-8 w-8 text-[#5a8aaa] dark:text-gray-600" />
                  No audit entries found.
                </td>
              </tr>
            ) : (
              displayEntries.map((entry, i) => (
                <tr key={entry.id || i} className="hover:bg-[#d6eeff] dark:hover:bg-gray-700">
                  <td className="whitespace-nowrap px-4 py-2 font-mono text-xs text-[#2a5a7a] dark:text-gray-400">
                    {entry.timestamp ? new Date(entry.timestamp).toLocaleString() : '-'}
                  </td>
                  <td className="px-4 py-2 text-xs font-medium text-[#0a2a4a] dark:text-gray-200">
                    {entry.event}
                  </td>
                  <td className="px-4 py-2 text-xs text-[#2a5a7a] dark:text-gray-400">
                    {entry.user || '-'}
                  </td>
                  <td className="px-4 py-2">
                    <span className="rounded-full bg-[#d6eeff] px-2 py-0.5 text-xs font-medium text-[#1a4a6a] dark:bg-gray-700 dark:text-gray-300">
                      {entry.action}
                    </span>
                  </td>
                  <td className="px-4 py-2 font-mono text-xs text-[#2a5a7a] dark:text-gray-400">
                    {entry.resource || '-'}
                  </td>
                  <td className="px-4 py-2 text-xs text-[#3a6a8a] dark:text-gray-500">
                    {entry.source || '-'}
                  </td>
                  <td className="px-4 py-2">
                    <AttributionIcon mode={entry.attribution_mode} />
                  </td>
                  <td className="px-4 py-2">
                    <span
                      className={`rounded-full px-2 py-0.5 text-xs font-medium ${
                        entry.result === 'success'
                          ? 'bg-green-50 text-green-700 dark:bg-green-900/30 dark:text-green-400'
                          : entry.result === 'failure' || entry.result === 'error'
                            ? 'bg-red-50 text-red-700 dark:bg-red-900/30 dark:text-red-400'
                            : 'bg-[#d6eeff] text-[#1a4a6a] dark:bg-gray-700 dark:text-gray-300'
                      }`}
                    >
                      {entry.result}
                    </span>
                  </td>
                  <td className="px-4 py-2 text-xs text-[#3a6a8a] dark:text-gray-500">
                    {entry.duration_ms != null ? `${entry.duration_ms}ms` : '-'}
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
export default AuditViewer

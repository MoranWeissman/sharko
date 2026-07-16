import { useState, useEffect, useCallback, useRef, useMemo, Fragment } from 'react';
import {
  ClipboardList,
  RefreshCw,
  Radio,
  Loader2,
  Key,
  User as UserIcon,
  Users as UsersIcon,
  Info,
  Search,
  ChevronDown,
  ChevronRight,
  CalendarClock,
  Download,
} from 'lucide-react';
import { fetchAuditLog, createAuditStream } from '@/services/api';
import type { AuditEntry } from '@/services/models';
import { useDebouncedValue } from '@/views/audit/useDebouncedValue';
import {
  eventPhrase,
  parseResource,
  RESULT_OPTIONS,
} from '@/views/audit/auditLabels';
import { relativeTime } from '@/lib/time';
import { StatusBadge } from '@/components/StatusBadge';

/** Renders the small attribution-mode icon + label for an audit entry's detail. */
function AttributionBadge({ mode }: { mode?: AuditEntry['attribution_mode'] }) {
  if (!mode) return <span className="text-[#5a8aaa] dark:text-gray-600">—</span>;
  switch (mode) {
    case 'service':
      return (
        <span className="inline-flex items-center gap-1.5 text-[#3a6a8a] dark:text-gray-400" title="Service token used — no human author on the commit">
          <Key className="h-3.5 w-3.5" aria-hidden="true" />
          Service token (no human author)
        </span>
      );
    case 'per_user':
      return (
        <span className="inline-flex items-center gap-1.5 text-emerald-700 dark:text-emerald-400" title="Per-user PAT used — commit authored by the user">
          <UserIcon className="h-3.5 w-3.5" aria-hidden="true" />
          Per-user token (user is the commit author)
        </span>
      );
    case 'co_author':
      return (
        <span className="inline-flex items-center gap-1.5 text-amber-700 dark:text-amber-400" title="Service token used; user listed as Co-authored-by trailer">
          <UsersIcon className="h-3.5 w-3.5" aria-hidden="true" />
          Co-authored (service token + user trailer)
        </span>
      );
    default:
      return <span className="text-[#5a8aaa] dark:text-gray-600">—</span>;
  }
}

// The REAL action values the backend records. Every mutating HTTP request
// gets one of create/update/patch/delete from methodToAction
// (internal/api/audit_middleware.go). A handful of routes emit their own
// entry outside that middleware with a fixed Action word: login (also
// login_failed), logout, init, sync (cluster secret reconcile), push
// (Git webhook), block (secret-leak guard). "test" and "diagnose" were
// never written by any of these paths — removed (V2-cleanup-85.3).
const ACTION_OPTIONS = ['', 'create', 'update', 'patch', 'delete', 'login', 'logout', 'init', 'sync', 'push', 'block'];
const SOURCE_OPTIONS = ['', 'ui', 'api', 'cli', 'webhook'];

// Quick presets for the "Since" filter — value is the lookback in milliseconds.
const SINCE_PRESETS: { label: string; ms: number }[] = [
  { label: 'Last 1h', ms: 60 * 60 * 1000 },
  { label: 'Last 24h', ms: 24 * 60 * 60 * 1000 },
  { label: 'Last 7d', ms: 7 * 24 * 60 * 60 * 1000 },
];

const INITIAL_LIMIT = 200;
const LIMIT_STEP = 200;

/** Small field-row used inside the expandable detail panel. */
function DetailField({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5">
      <dt className="text-xs font-semibold uppercase tracking-wide text-[#5a8aaa] dark:text-gray-500">{label}</dt>
      <dd className="text-xs text-[#2a5a7a] dark:text-gray-300">{children}</dd>
    </div>
  );
}

export function AuditViewer() {
  const [entries, setEntries] = useState<AuditEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [liveTail, setLiveTail] = useState(false);
  const [liveEntries, setLiveEntries] = useState<AuditEntry[]>([]);
  const [limit, setLimit] = useState(INITIAL_LIMIT);
  const [hasMore, setHasMore] = useState(false);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const eventSourceRef = useRef<EventSource | null>(null);

  // Raw filter inputs (update on every keystroke).
  const [filterUser, setFilterUser] = useState('');
  const [filterAction, setFilterAction] = useState('');
  const [filterSource, setFilterSource] = useState('');
  const [filterResult, setFilterResult] = useState('');
  const [filterCluster, setFilterCluster] = useState('');
  const [filterSince, setFilterSince] = useState(''); // datetime-local value (local time)
  const [search, setSearch] = useState(''); // client-side free-text over loaded entries

  // Debounced copies — these are what actually drive the fetch (item 3.4).
  const dUser = useDebouncedValue(filterUser, 300);
  const dCluster = useDebouncedValue(filterCluster, 300);
  const dSince = useDebouncedValue(filterSince, 300);
  const dSearch = useDebouncedValue(search, 200);

  // Convert the datetime-local value (local wall-clock) to RFC3339 for the API.
  const sinceRFC3339 = useMemo(() => {
    if (!dSince) return undefined;
    const d = new Date(dSince);
    return Number.isNaN(d.getTime()) ? undefined : d.toISOString();
  }, [dSince]);

  const fetchData = useCallback(
    async (nextLimit: number, mode: 'replace' | 'more') => {
      try {
        if (mode === 'more') setLoadingMore(true);
        else setLoading(true);
        setError(null);
        const result = await fetchAuditLog({
          user: dUser || undefined,
          action: filterAction || undefined,
          source: filterSource || undefined,
          result: filterResult || undefined,
          cluster: dCluster || undefined,
          since: sinceRFC3339,
          limit: nextLimit,
        });
        const got = result.entries ?? [];
        setEntries(got);
        // If we got back a full page, there may be more history to load.
        setHasMore(got.length >= nextLimit);
      } catch (e: unknown) {
        setError(e instanceof Error ? e.message : 'Failed to load audit log');
      } finally {
        setLoading(false);
        setLoadingMore(false);
      }
    },
    [dUser, filterAction, filterSource, filterResult, dCluster, sinceRFC3339],
  );

  // Reset paging to the first page whenever a server-side filter changes.
  useEffect(() => {
    setLimit(INITIAL_LIMIT);
  }, [dUser, filterAction, filterSource, filterResult, dCluster, sinceRFC3339]);

  // Fetch the historical page (initial + on filter/limit change). Auto-refresh
  // every 30s when not live-tailing.
  useEffect(() => {
    void fetchData(limit, 'replace');
    const interval = setInterval(() => {
      if (!liveTail) void fetchData(limit, 'replace');
    }, 30000);
    return () => clearInterval(interval);
  }, [fetchData, limit, liveTail]);

  // Live tail SSE — prepends new entries to the live buffer (item 7.17: paging
  // applies only to the historical list; the live stream keeps prepending).
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
        setLiveEntries((prev) => [entry, ...prev].slice(0, 500));
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

  const handleLoadMore = useCallback(() => {
    const next = limit + LIMIT_STEP;
    setLimit(next);
    void fetchData(next, 'more');
  }, [limit, fetchData]);

  const handlePreset = useCallback((ms: number) => {
    // datetime-local wants "YYYY-MM-DDTHH:mm" in local time.
    const d = new Date(Date.now() - ms);
    const pad = (n: number) => String(n).padStart(2, '0');
    const local = `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
    setFilterSince(local);
  }, []);

  const toggleExpanded = useCallback((id: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  // The combined list: live entries first (when tailing), then the page.
  const combined = useMemo(
    () => (liveTail ? [...liveEntries, ...entries] : entries),
    [liveTail, liveEntries, entries],
  );

  // Client-side free-text search over the LOADED entries only (item 3.5).
  const displayEntries = useMemo(() => {
    const q = dSearch.trim().toLowerCase();
    if (!q) return combined;
    return combined.filter((e) => {
      const haystack = [e.event, e.user, e.resource, e.detail, e.error, eventPhrase(e.event)]
        .filter(Boolean)
        .join(' ')
        .toLowerCase();
      return haystack.includes(q);
    });
  }, [combined, dSearch]);

  const exportCSV = useCallback(() => {
    // CSV export of currently displayed entries
    const headers = ['Timestamp', 'Who', 'Event', 'Action', 'Resource', 'Result', 'Source', 'Detail', 'Error'];
    const csvRows = [headers.join(',')];

    displayEntries.forEach((e) => {
      const row = [
        e.timestamp ? new Date(e.timestamp).toISOString() : '',
        e.user || '',
        e.event || '',
        e.action || '',
        e.resource || '',
        e.result || '',
        e.source || '',
        (e.detail || '').replace(/"/g, '""'),
        (e.error || '').replace(/"/g, '""'),
      ];
      csvRows.push(row.map((v) => `"${v}"`).join(','));
    });

    const blob = new Blob([csvRows.join('\n')], { type: 'text/csv' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `sharko-audit-${new Date().toISOString().split('T')[0]}.csv`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  }, [displayEntries]);

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold text-[#0a2a4a] dark:text-gray-100">Audit Log</h1>
          <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-400">
            Recent API events and operations across all clusters.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={exportCSV}
            disabled={displayEntries.length === 0}
            className="inline-flex items-center gap-1.5 rounded-lg ring-2 ring-[#6aade0] bg-[#e8f4ff] px-3 py-2 text-xs text-[#2a5a7a] hover:bg-[#d6eeff] disabled:opacity-50 dark:ring-gray-700 dark:bg-gray-800 dark:text-gray-400 dark:hover:bg-gray-700"
            title="Export displayed entries as CSV"
          >
            <Download className="h-3.5 w-3.5" />
            Export CSV
          </button>
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
            onClick={() => void fetchData(limit, 'replace')}
            disabled={loading}
            className="inline-flex items-center gap-1.5 rounded-lg ring-2 ring-[#6aade0] bg-[#e8f4ff] px-3 py-2 text-xs text-[#2a5a7a] hover:bg-[#d6eeff] disabled:opacity-50 dark:ring-gray-700 dark:bg-gray-800 dark:text-gray-400 dark:hover:bg-gray-700"
          >
            {loading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
            Refresh
          </button>
        </div>
      </div>

      {/* Retention banner — explains the two-stream architecture. */}
      <div
        role="status"
        aria-live="polite"
        className="flex items-start gap-3 rounded-lg ring-2 ring-[#6aade0] bg-[#e0f0ff] px-4 py-3 dark:ring-gray-700 dark:bg-gray-800"
      >
        <Info className="mt-0.5 h-4 w-4 flex-shrink-0 text-[#0a3a5a] dark:text-blue-300" aria-hidden="true" />
        <p className="text-xs text-[#0a3a5a] dark:text-gray-300">
          Showing the last <strong>1000 in-memory events</strong>. The buffer is wiped on pod restart —
          see{' '}
          <a
            href="https://sharko.readthedocs.io/en/latest/operator/audit-log/"
            target="_blank"
            rel="noopener noreferrer"
            className="underline decoration-dotted underline-offset-2 hover:text-teal-700 focus-visible:outline focus-visible:outline-2 focus-visible:outline-teal-500 dark:hover:text-teal-300"
          >
            audit log retention
          </a>{' '}
          for the long-term retention model via your cluster's log pipeline (Loki, Splunk, ELK,
          CloudWatch, GCP Logging).
        </p>
      </div>

      {/* Filters */}
      <div className="space-y-3 rounded-lg ring-2 ring-[#6aade0] bg-[#d0e8f8] p-4 dark:ring-gray-700 dark:bg-gray-900">
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
          <div>
            <label htmlFor="audit-filter-user" className="mb-1 block text-xs font-medium text-[#0a3a5a] dark:text-gray-400">Who</label>
            <input
              id="audit-filter-user"
              type="text"
              value={filterUser}
              onChange={(e) => setFilterUser(e.target.value)}
              placeholder="Any user"
              className="w-full rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-2 py-1.5 text-xs focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:placeholder-[#5a8aaa]"
            />
          </div>
          <div>
            <label htmlFor="audit-filter-action" className="mb-1 block text-xs font-medium text-[#0a3a5a] dark:text-gray-400">Action</label>
            <select
              id="audit-filter-action"
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
            <label htmlFor="audit-filter-source" className="mb-1 block text-xs font-medium text-[#0a3a5a] dark:text-gray-400">Source</label>
            <select
              id="audit-filter-source"
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
            <label htmlFor="audit-filter-result" className="mb-1 block text-xs font-medium text-[#0a3a5a] dark:text-gray-400">Result</label>
            <select
              id="audit-filter-result"
              value={filterResult}
              onChange={(e) => setFilterResult(e.target.value)}
              className="w-full rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-2 py-1.5 text-xs focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200"
            >
              {RESULT_OPTIONS.map((opt) => (
                <option key={opt.value} value={opt.value}>{opt.label}</option>
              ))}
            </select>
          </div>
          <div>
            <label htmlFor="audit-filter-cluster" className="mb-1 block text-xs font-medium text-[#0a3a5a] dark:text-gray-400">Cluster</label>
            <input
              id="audit-filter-cluster"
              type="text"
              value={filterCluster}
              onChange={(e) => setFilterCluster(e.target.value)}
              placeholder="Any cluster"
              className="w-full rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-2 py-1.5 text-xs focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:placeholder-[#5a8aaa]"
            />
          </div>
        </div>

        {/* Since (date picker + presets) and free-text search row. */}
        <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
          <div>
            <label htmlFor="audit-filter-since" className="mb-1 flex items-center gap-1 text-xs font-medium text-[#0a3a5a] dark:text-gray-400">
              <CalendarClock className="h-3.5 w-3.5" aria-hidden="true" /> Since
            </label>
            <div className="flex flex-wrap items-center gap-2">
              <input
                id="audit-filter-since"
                type="datetime-local"
                value={filterSince}
                onChange={(e) => setFilterSince(e.target.value)}
                className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-2 py-1.5 text-xs focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200"
              />
              {SINCE_PRESETS.map((p) => (
                <button
                  key={p.label}
                  type="button"
                  onClick={() => handlePreset(p.ms)}
                  className="rounded-md ring-1 ring-[#6aade0] bg-[#e8f4ff] px-2 py-1 text-xs font-medium text-[#2a5a7a] hover:bg-[#d6eeff] dark:ring-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
                >
                  {p.label}
                </button>
              ))}
              {filterSince && (
                <button
                  type="button"
                  onClick={() => setFilterSince('')}
                  className="rounded-md px-2 py-1 text-xs text-[#3a6a8a] underline-offset-2 hover:underline dark:text-gray-400"
                >
                  Clear
                </button>
              )}
            </div>
          </div>
          <div>
            <label htmlFor="audit-search" className="mb-1 flex items-center gap-1 text-xs font-medium text-[#0a3a5a] dark:text-gray-400">
              <Search className="h-3.5 w-3.5" aria-hidden="true" /> Search
            </label>
            <input
              id="audit-search"
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Filter loaded entries…"
              className="w-full rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-2 py-1.5 text-xs focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:placeholder-[#5a8aaa]"
            />
            <p className="mt-1 text-sm text-[#5a8aaa] dark:text-gray-500">
              Searches the entries already loaded below (event, who, resource, detail).
            </p>
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
              <th className="px-4 py-3 w-8" aria-label="Expand" />
              <th className="px-4 py-3">Time</th>
              <th className="px-4 py-3">Who</th>
              <th className="px-4 py-3">Action</th>
              <th className="px-4 py-3">Result</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#6aade0] dark:divide-gray-700">
            {loading && displayEntries.length === 0 ? (
              <tr>
                <td colSpan={5} className="px-4 py-12 text-center">
                  <Loader2 className="mx-auto h-5 w-5 animate-spin text-[#2a5a7a] dark:text-gray-400" />
                </td>
              </tr>
            ) : displayEntries.length === 0 ? (
              <tr>
                <td colSpan={5} className="px-4 py-12 text-center text-[#3a6a8a] dark:text-gray-500">
                  <ClipboardList className="mx-auto mb-2 h-8 w-8 text-[#5a8aaa] dark:text-gray-600" />
                  No audit entries found.
                </td>
              </tr>
            ) : (
              displayEntries.map((entry, i) => {
                const rowId = entry.id || `${entry.timestamp}-${i}`;
                const isOpen = expanded.has(rowId);
                const isFailure = entry.result === 'failure' || entry.result === 'error' || entry.result === 'rejected';
                return (
                  <Fragment key={rowId}>
                    <tr
                      onClick={() => toggleExpanded(rowId)}
                      className="cursor-pointer hover:bg-[#d6eeff] dark:hover:bg-gray-700"
                    >
                      <td className="px-4 py-2 text-[#3a6a8a] dark:text-gray-500">
                        <button
                          type="button"
                          onClick={(e) => {
                            e.stopPropagation();
                            toggleExpanded(rowId);
                          }}
                          aria-label={isOpen ? 'Collapse details' : 'Expand details'}
                          aria-expanded={isOpen}
                          className="inline-flex"
                        >
                          {isOpen ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
                        </button>
                      </td>
                      <td className="whitespace-nowrap px-4 py-2 text-xs text-[#2a5a7a] dark:text-gray-400" title={entry.timestamp ? new Date(entry.timestamp).toLocaleString() : ''}>
                        {entry.timestamp ? relativeTime(entry.timestamp) : '—'}
                      </td>
                      <td className="px-4 py-2 text-xs font-medium text-[#0a2a4a] dark:text-gray-200">
                        {entry.user === 'sharko' || entry.user === 'system' ? (
                          <span className="text-[#0a3a5a] dark:text-gray-400">{entry.user}</span>
                        ) : (
                          <span>{entry.user || 'anonymous'}</span>
                        )}
                      </td>
                      <td className="px-4 py-2 text-xs text-[#2a5a7a] dark:text-gray-300">
                        <span className="font-medium text-[#0a2a4a] dark:text-gray-200">
                          {eventPhrase(entry.event)}
                        </span>
                        {parseResource(entry.resource) && (
                          <span className="text-[#3a6a8a] dark:text-gray-400"> — {parseResource(entry.resource)}</span>
                        )}
                        {isFailure && entry.error ? (
                          <span className="block mt-0.5 text-red-600 dark:text-red-400 text-xs">{entry.error}</span>
                        ) : null}
                      </td>
                      <td className="px-4 py-2">
                        <StatusBadge status={entry.result || 'unknown'} />
                      </td>
                    </tr>
                    {isOpen && (
                      <tr className="bg-[#e8f4ff] dark:bg-gray-900/60">
                        <td />
                        <td colSpan={4} className="px-4 py-3">
                          <dl className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
                            {entry.detail && (
                              <div className="sm:col-span-2 lg:col-span-3">
                                <DetailField label="Detail">
                                  <span className="whitespace-pre-wrap">{entry.detail}</span>
                                </DetailField>
                              </div>
                            )}
                            {isFailure && entry.error && (
                              <div className="sm:col-span-2 lg:col-span-3">
                                <DetailField label="Error">
                                  <span className="whitespace-pre-wrap text-red-700 dark:text-red-400">{entry.error}</span>
                                </DetailField>
                              </div>
                            )}
                            <DetailField label="Event code">
                              <code className="font-mono text-xs">{entry.event || '—'}</code>
                            </DetailField>
                            <DetailField label="Resource">
                              <span className="font-mono text-xs">{entry.resource || '—'}</span>
                              {parseResource(entry.resource) && (
                                <span className="ml-1 text-[#5a8aaa] dark:text-gray-500">({parseResource(entry.resource)})</span>
                              )}
                            </DetailField>
                            <DetailField label="Source">{entry.source || '—'}</DetailField>
                            <DetailField label="Duration">
                              {entry.duration_ms != null ? `${entry.duration_ms} ms` : '—'}
                            </DetailField>
                            <DetailField label="Attribution">
                              <AttributionBadge mode={entry.attribution_mode} />
                            </DetailField>
                            {entry.request_id && (
                              <DetailField label="Request ID">
                                <code className="font-mono text-xs">{entry.request_id}</code>
                              </DetailField>
                            )}
                          </dl>
                        </td>
                      </tr>
                    )}
                  </Fragment>
                );
              })
            )}
          </tbody>
        </table>
      </div>

      {/* Load more — re-fetches a larger limit (item 7.17). Hidden while live-tailing. */}
      {!liveTail && hasMore && displayEntries.length > 0 && (
        <div className="flex justify-center">
          <button
            type="button"
            onClick={handleLoadMore}
            disabled={loadingMore}
            className="inline-flex items-center gap-1.5 rounded-lg ring-2 ring-[#6aade0] bg-[#e8f4ff] px-4 py-2 text-xs font-medium text-[#2a5a7a] hover:bg-[#d6eeff] disabled:opacity-50 dark:ring-gray-700 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
          >
            {loadingMore ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <ChevronDown className="h-3.5 w-3.5" />}
            Load more
          </button>
        </div>
      )}
    </div>
  );
}
export default AuditViewer

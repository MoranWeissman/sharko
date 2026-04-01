import { useState, useEffect, useCallback } from 'react';
import {
  ArrowUpCircle,
  AlertTriangle,
  Plus,
  Minus,
  RefreshCw,
  CheckCircle,
  ChevronDown,
  Search,
  Sparkles,
  FileText,
} from 'lucide-react';
import { api } from '@/services/api';
import type {
  AddonCatalogItem,
  AvailableVersion,
  UpgradeCheckResponse,
  ValueDiffEntry,
  ConflictCheckEntry,
} from '@/services/models';
import { LoadingState } from '@/components/LoadingState';
import { ErrorState } from '@/components/ErrorState';
import { MarkdownRenderer } from '@/components/MarkdownRenderer';

// ---------------------------------------------------------------------------
// Tab type
// ---------------------------------------------------------------------------

type ChangeTab = 'added' | 'removed' | 'changed';

// ---------------------------------------------------------------------------
// Risk level helpers
// ---------------------------------------------------------------------------

function getRisk(result: UpgradeCheckResponse): 'safe' | 'minor' | 'conflicts' {
  if ((result.conflicts ?? []).length > 0) return 'conflicts';
  if (result.total_changes > 0) return 'minor';
  return 'safe';
}

function riskStyles(risk: 'safe' | 'minor' | 'conflicts') {
  if (risk === 'conflicts')
    return 'border-red-300 bg-red-50 dark:border-red-700 dark:bg-red-950/40';
  if (risk === 'minor')
    return 'border-amber-300 bg-amber-50 dark:border-amber-700 dark:bg-amber-950/40';
  return 'border-green-300 bg-green-50 dark:border-green-700 dark:bg-green-950/40';
}

function riskLabel(risk: 'safe' | 'minor' | 'conflicts') {
  if (risk === 'conflicts') return 'Conflicts Found';
  if (risk === 'minor') return 'Minor Changes';
  return 'Safe to Upgrade';
}

function riskIcon(risk: 'safe' | 'minor' | 'conflicts') {
  if (risk === 'conflicts')
    return <AlertTriangle className="h-5 w-5 text-red-500" />;
  if (risk === 'minor')
    return <RefreshCw className="h-5 w-5 text-amber-500" />;
  return <CheckCircle className="h-5 w-5 text-green-500" />;
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

const PAGE_SIZE = 15;

function PaginatedList({ items, renderItem, emptyMessage }: { items: ValueDiffEntry[]; renderItem: (entry: ValueDiffEntry) => React.ReactNode; emptyMessage: string }) {
  const [showCount, setShowCount] = useState(PAGE_SIZE);

  if (items.length === 0)
    return <p className="py-4 text-center text-sm text-gray-500 dark:text-gray-400">{emptyMessage}</p>;

  const visible = items.slice(0, showCount);
  const remaining = items.length - showCount;

  return (
    <div>
      <div className="divide-y divide-gray-100 dark:divide-gray-800">
        {visible.map(renderItem)}
      </div>
      {remaining > 0 && (
        <button
          onClick={() => setShowCount(c => c + PAGE_SIZE)}
          className="w-full border-t border-gray-100 px-4 py-3 text-center text-sm font-medium text-cyan-600 hover:bg-gray-50 dark:border-gray-800 dark:text-cyan-400 dark:hover:bg-gray-800"
        >
          Show {Math.min(remaining, PAGE_SIZE)} more ({remaining} remaining)
        </button>
      )}
    </div>
  );
}

function AddedFields({ items }: { items: ValueDiffEntry[] }) {
  return (
    <PaginatedList
      items={items}
      emptyMessage="No added fields."
      renderItem={(entry) => (
        <div key={entry.path} className="flex items-start gap-3 px-4 py-3">
          <Plus className="mt-0.5 h-4 w-4 shrink-0 text-green-500" />
          <div className="min-w-0 flex-1">
            <code className="text-sm font-medium text-gray-900 dark:text-gray-100">{entry.path}</code>
            {entry.new_value != null && (
              <p className="mt-0.5 text-sm text-green-600 dark:text-green-400">
                Default: <code className="rounded bg-green-100 px-1 dark:bg-green-900/40">{entry.new_value}</code>
              </p>
            )}
          </div>
        </div>
      )}
    />
  );
}

function RemovedFields({ items }: { items: ValueDiffEntry[] }) {
  return (
    <PaginatedList
      items={items}
      emptyMessage="No removed fields."
      renderItem={(entry) => (
        <div key={entry.path} className="flex items-start gap-3 px-4 py-3">
          <Minus className="mt-0.5 h-4 w-4 shrink-0 text-red-500" />
          <div className="min-w-0 flex-1">
            <code className="text-sm font-medium text-gray-900 dark:text-gray-100">{entry.path}</code>
            {entry.old_value != null && (
              <p className="mt-0.5 text-sm text-red-600 dark:text-red-400">
                Was: <code className="rounded bg-red-100 px-1 line-through dark:bg-red-900/40">{entry.old_value}</code>
              </p>
            )}
          </div>
        </div>
      )}
    />
  );
}

function ChangedFields({ items }: { items: ValueDiffEntry[] }) {
  return (
    <PaginatedList
      items={items}
      emptyMessage="No changed defaults."
      renderItem={(entry) => (
        <div key={entry.path} className="flex items-start gap-3 px-4 py-3">
          <RefreshCw className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
          <div className="min-w-0 flex-1">
            <code className="text-sm font-medium text-gray-900 dark:text-gray-100">{entry.path}</code>
            <p className="mt-0.5 flex flex-wrap items-center gap-1 text-sm">
              <code className="rounded bg-red-100 px-1 text-red-700 dark:bg-red-900/40 dark:text-red-400">
                {entry.old_value ?? '(empty)'}
              </code>
              <span className="text-gray-400">&rarr;</span>
              <code className="rounded bg-green-100 px-1 text-green-700 dark:bg-green-900/40 dark:text-green-400">
                {entry.new_value ?? '(empty)'}
              </code>
            </p>
          </div>
        </div>
      )}
    />
  );
}

function ReleaseNotesSection({ notes }: { notes: string }) {
  const [expanded, setExpanded] = useState(notes.length <= 500);

  const displayText = expanded ? notes : notes.split('\n').slice(0, 8).join('\n');
  const totalLines = notes.split('\n').length;

  return (
    <div className="rounded-xl border border-gray-200 bg-white shadow-sm dark:border-gray-700 dark:bg-gray-900">
      <div
        className="flex cursor-pointer items-center justify-between border-b border-gray-100 px-6 py-3 dark:border-gray-800"
        onClick={() => setExpanded((e) => !e)}
      >
        <div className="flex items-center gap-2">
          <FileText className="h-4 w-4 text-blue-500" />
          <h3 className="text-sm font-semibold text-gray-900 dark:text-white">Release Notes</h3>
        </div>
        <ChevronDown className={`h-4 w-4 text-gray-400 transition-transform ${expanded ? 'rotate-180' : ''}`} />
      </div>
      <div className="px-6 py-4">
        <MarkdownRenderer content={displayText} />
        {!expanded && totalLines > 8 && (
          <button
            onClick={(e) => { e.stopPropagation(); setExpanded(true); }}
            className="mt-2 text-xs font-medium text-cyan-600 hover:text-cyan-700 dark:text-cyan-400"
          >
            Show all ({totalLines} lines)
          </button>
        )}
      </div>
    </div>
  );
}

function ConflictsSection({ conflicts }: { conflicts: ConflictCheckEntry[] }) {
  if (conflicts.length === 0) return null;
  return (
    <div className="rounded-xl border-2 border-red-300 bg-red-50 dark:border-red-700 dark:bg-red-950/30">
      <div className="flex items-center gap-3 border-b border-red-200 px-6 py-4 dark:border-red-800">
        <AlertTriangle className="h-5 w-5 text-red-500" />
        <div>
          <h3 className="text-lg font-semibold text-red-800 dark:text-red-300">
            {conflicts.length} Conflict{conflicts.length !== 1 ? 's' : ''} Detected
          </h3>
          <p className="text-sm text-red-600 dark:text-red-400">
            These configured values may need review after upgrading
          </p>
        </div>
      </div>
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-red-200 text-left text-xs font-medium uppercase tracking-wider text-red-700 dark:border-red-800 dark:text-red-400">
              <th className="px-6 py-3">Path</th>
              <th className="px-6 py-3">Your Value</th>
              <th className="px-6 py-3">Old Default</th>
              <th className="px-6 py-3">New Default</th>
              <th className="px-6 py-3">Source</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-red-100 dark:divide-red-900">
            {conflicts.map((c) => (
              <tr key={`${c.path}-${c.source}`}>
                <td className="whitespace-nowrap px-6 py-3">
                  <code className="text-gray-900 dark:text-gray-100">{c.path}</code>
                </td>
                <td className="px-6 py-3">
                  <code className="rounded bg-blue-100 px-1 text-blue-700 dark:bg-blue-900/40 dark:text-blue-300">
                    {c.configured_value}
                  </code>
                </td>
                <td className="px-6 py-3">
                  <code className="rounded bg-gray-100 px-1 text-gray-600 dark:bg-gray-800 dark:text-gray-400">
                    {c.old_default}
                  </code>
                </td>
                <td className="px-6 py-3">
                  <code className="rounded bg-amber-100 px-1 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300">
                    {c.new_default}
                  </code>
                </td>
                <td className="px-6 py-3 text-gray-600 dark:text-gray-400">{c.source}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="border-t border-red-200 px-6 py-3 text-xs text-red-600 dark:border-red-800 dark:text-red-400">
        These values are overridden in your configuration and the upstream default has changed. Review if your override is still appropriate.
      </p>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

export function UpgradeChecker() {
  // Addon list
  const [addons, setAddons] = useState<AddonCatalogItem[]>([]);
  const [addonsLoading, setAddonsLoading] = useState(true);
  const [addonsError, setAddonsError] = useState<string | null>(null);

  // Selected addon
  const [selectedAddon, setSelectedAddon] = useState<string>('');
  const [addonSearch, setAddonSearch] = useState('');
  const [addonDropdownOpen, setAddonDropdownOpen] = useState(false);

  // Version list
  const [versions, setVersions] = useState<AvailableVersion[]>([]);
  const [versionsLoading, setVersionsLoading] = useState(false);

  // Selected version
  const [selectedVersion, setSelectedVersion] = useState<string>('');

  // Analysis result
  const [result, setResult] = useState<UpgradeCheckResponse | null>(null);
  const [analyzing, setAnalyzing] = useState(false);
  const [analyzeError, setAnalyzeError] = useState<string | null>(null);

  // Change tab
  const [activeTab, setActiveTab] = useState<ChangeTab>('added');

  // AI state
  const [aiEnabled, setAiEnabled] = useState(false);
  const [aiProvider, setAiProvider] = useState('');
  const [aiSummary, setAiSummary] = useState<string | null>(null);
  const [aiLoading, setAiLoading] = useState(false);
  const [aiError, setAiError] = useState<string | null>(null);

  // Check AI status on mount
  useEffect(() => {
    api.getAIConfig()
      .then((data) => {
        setAiEnabled(data.current_provider !== '' && data.current_provider !== 'none');
        setAiProvider(data.current_provider);
      })
      .catch(() => setAiEnabled(false));
  }, []);

  // Load addons on mount
  useEffect(() => {
    api
      .getAddonCatalog()
      .then((data) => setAddons(data.addons))
      .catch((err: Error) => setAddonsError(err.message))
      .finally(() => setAddonsLoading(false));
  }, []);

  // Load versions when addon changes
  useEffect(() => {
    if (!selectedAddon) {
      setVersions([]);
      setSelectedVersion('');
      return;
    }
    setVersionsLoading(true);
    setSelectedVersion('');
    setResult(null);
    setAnalyzeError(null);
    api
      .getUpgradeVersions(selectedAddon)
      .then((data) => setVersions(data.versions))
      .catch(() => setVersions([]))
      .finally(() => setVersionsLoading(false));
  }, [selectedAddon]);

  const currentVersion = addons.find((a) => a.addon_name === selectedAddon)?.version;

  const filteredAddons = addons.filter(
    (a) =>
      a.addon_name.toLowerCase().includes(addonSearch.toLowerCase()) ||
      a.chart.toLowerCase().includes(addonSearch.toLowerCase()),
  );

  const availableVersions = versions.filter((v) => v.version !== currentVersion);

  const handleGetAISummary = useCallback(async () => {
    if (!selectedAddon || !selectedVersion) return;
    setAiLoading(true);
    setAiError(null);
    setAiSummary(null);
    try {
      const data = await api.getAISummary(selectedAddon, selectedVersion);
      setAiSummary(data.summary);
    } catch (err) {
      setAiError(err instanceof Error ? err.message : 'AI analysis failed');
    } finally {
      setAiLoading(false);
    }
  }, [selectedAddon, selectedVersion]);

  const handleAnalyze = useCallback(async () => {
    if (!selectedAddon || !selectedVersion) return;
    setAnalyzing(true);
    setAnalyzeError(null);
    setResult(null);
    setAiSummary(null);
    setAiError(null);
    try {
      const data = await api.checkUpgrade(selectedAddon, selectedVersion);
      // Normalize null arrays from Go's JSON encoding
      setResult({
        ...data,
        added: data.added ?? [],
        removed: data.removed ?? [],
        changed: data.changed ?? [],
        conflicts: data.conflicts ?? [],
      });
      setActiveTab('added');
    } catch (err) {
      setAnalyzeError(err instanceof Error ? err.message : 'Analysis failed');
    } finally {
      setAnalyzing(false);
    }
  }, [selectedAddon, selectedVersion]);

  // -------------------------------------------------------------------------

  if (addonsLoading) return <LoadingState message="Loading addon catalog..." />;
  if (addonsError) return <ErrorState message={addonsError} />;

  return (
    <div className="space-y-8">
      {/* Header */}
      <div>
        <div className="flex items-center gap-3">
          <ArrowUpCircle className="h-8 w-8 text-cyan-500" />
          <div>
            <h1 className="text-2xl font-bold text-gray-900 dark:text-white">Add-on Upgrade Checker</h1>
            <p className="text-sm text-gray-500 dark:text-gray-400">
              Analyze the impact of upgrading an add-on to a new version
            </p>
          </div>
        </div>
      </div>

      {/* Step 1: Addon Selection */}
      <div className="rounded-xl border border-gray-200 bg-white p-6 shadow-sm dark:border-gray-700 dark:bg-gray-900">
        <h2 className="mb-4 flex items-center gap-2 text-lg font-semibold text-gray-900 dark:text-white">
          <span className="flex h-6 w-6 items-center justify-center rounded-full bg-cyan-100 text-xs font-bold text-cyan-700 dark:bg-cyan-900 dark:text-cyan-300">
            1
          </span>
          Select Addon
        </h2>

        <div className="relative max-w-md">
          <div
            className="flex cursor-pointer items-center justify-between rounded-lg border border-gray-300 bg-white px-3 py-2 text-sm dark:border-gray-600 dark:bg-gray-800"
            onClick={() => setAddonDropdownOpen((o) => !o)}
            role="combobox"
            aria-expanded={addonDropdownOpen}
            aria-label="Select addon"
          >
            <span className={selectedAddon ? 'text-gray-900 dark:text-white' : 'text-gray-400'}>
              {selectedAddon
                ? `${selectedAddon} (v${currentVersion})`
                : 'Choose an addon...'}
            </span>
            <ChevronDown className="h-4 w-4 text-gray-400" />
          </div>

          {addonDropdownOpen && (
            <div className="absolute z-20 mt-1 w-full rounded-lg border border-gray-200 bg-white shadow-lg dark:border-gray-600 dark:bg-gray-800">
              <div className="border-b border-gray-100 p-2 dark:border-gray-700">
                <div className="flex items-center gap-2 rounded-md border border-gray-200 bg-gray-50 px-2 dark:border-gray-600 dark:bg-gray-700">
                  <Search className="h-4 w-4 text-gray-400" />
                  <input
                    type="text"
                    placeholder="Search addons..."
                    className="w-full border-0 bg-transparent py-1.5 text-sm text-gray-900 outline-none placeholder:text-gray-400 dark:text-white"
                    value={addonSearch}
                    onChange={(e) => setAddonSearch(e.target.value)}
                    autoFocus
                  />
                </div>
              </div>
              <div className="max-h-60 overflow-y-auto py-1">
                {filteredAddons.length === 0 && (
                  <p className="px-4 py-2 text-sm text-gray-500">No addons found.</p>
                )}
                {filteredAddons.map((addon) => (
                  <button
                    key={addon.addon_name}
                    className={`flex w-full items-center justify-between px-4 py-2 text-left text-sm hover:bg-gray-50 dark:hover:bg-gray-700 ${
                      addon.addon_name === selectedAddon
                        ? 'bg-cyan-50 font-medium text-cyan-700 dark:bg-cyan-900/30 dark:text-cyan-400'
                        : 'text-gray-700 dark:text-gray-200'
                    }`}
                    onClick={() => {
                      setSelectedAddon(addon.addon_name);
                      setAddonDropdownOpen(false);
                      setAddonSearch('');
                    }}
                  >
                    <span>{addon.addon_name}</span>
                    <span className="text-xs text-gray-400">v{addon.version}</span>
                  </button>
                ))}
              </div>
            </div>
          )}
        </div>
      </div>

      {/* Step 2: Version Selection */}
      {selectedAddon && (
        <div className="rounded-xl border border-gray-200 bg-white p-6 shadow-sm dark:border-gray-700 dark:bg-gray-900">
          <h2 className="mb-4 flex items-center gap-2 text-lg font-semibold text-gray-900 dark:text-white">
            <span className="flex h-6 w-6 items-center justify-center rounded-full bg-cyan-100 text-xs font-bold text-cyan-700 dark:bg-cyan-900 dark:text-cyan-300">
              2
            </span>
            Select Target Version
          </h2>

          {versionsLoading ? (
            <p className="text-sm text-gray-500 dark:text-gray-400">Loading versions...</p>
          ) : availableVersions.length === 0 ? (
            <p className="text-sm text-gray-500 dark:text-gray-400">No other versions available.</p>
          ) : (
            <div className="flex flex-wrap items-end gap-4">
              <div className="w-64">
                <label htmlFor="version-select" className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300">
                  Target version
                </label>
                <select
                  id="version-select"
                  className="w-full rounded-lg border border-gray-300 bg-white px-3 py-2 text-sm text-gray-900 dark:border-gray-600 dark:bg-gray-800 dark:text-white"
                  value={selectedVersion}
                  onChange={(e) => {
                    setSelectedVersion(e.target.value);
                    setResult(null);
                    setAnalyzeError(null);
                  }}
                >
                  <option value="">Select version...</option>
                  {availableVersions.map((v) => (
                    <option key={v.version} value={v.version}>
                      {v.version}{v.app_version ? ` (app: ${v.app_version})` : ''}
                    </option>
                  ))}
                </select>
              </div>

              <button
                disabled={!selectedVersion || analyzing}
                onClick={handleAnalyze}
                className="inline-flex items-center gap-2 rounded-lg bg-cyan-600 px-5 py-2 text-sm font-medium text-white transition-colors hover:bg-cyan-700 disabled:cursor-not-allowed disabled:opacity-50"
              >
                {analyzing ? (
                  <RefreshCw className="h-4 w-4 animate-spin" />
                ) : (
                  <ArrowUpCircle className="h-4 w-4" />
                )}
                Analyze Upgrade
              </button>
            </div>
          )}
        </div>
      )}

      {/* Analyzing state */}
      {analyzing && <LoadingState message="Analyzing upgrade impact..." />}

      {/* Error state */}
      {analyzeError && <ErrorState message={analyzeError} />}

      {/* Step 3: Results */}
      {result && !analyzing && (
        <div className="space-y-6">
          {/* Summary card */}
          {(() => {
            const risk = getRisk(result);
            return (
              <div className={`rounded-xl border-2 p-6 ${riskStyles(risk)}`}>
                <div className="flex items-center justify-between">
                  <div>
                    <h2 className="text-lg font-semibold text-gray-900 dark:text-white">
                      Upgrading <span className="font-bold">{result.addon_name}</span>{' '}
                      from <code className="text-sm">v{result.current_version}</code>{' '}
                      to <code className="text-sm">v{result.target_version}</code>
                    </h2>
                    <div className="mt-2 flex flex-wrap gap-4 text-sm">
                      <span className="inline-flex items-center gap-1 text-green-700 dark:text-green-400">
                        <Plus className="h-3.5 w-3.5" /> {result.added.length} added
                      </span>
                      <span className="inline-flex items-center gap-1 text-red-700 dark:text-red-400">
                        <Minus className="h-3.5 w-3.5" /> {result.removed.length} removed
                      </span>
                      <span className="inline-flex items-center gap-1 text-amber-700 dark:text-amber-400">
                        <RefreshCw className="h-3.5 w-3.5" /> {result.changed.length} changed
                      </span>
                      <span className={`inline-flex items-center gap-1 ${result.conflicts.length > 0 ? 'text-red-700 dark:text-red-400' : 'text-green-700 dark:text-green-400'}`}>
                        {result.conflicts.length > 0
                          ? <><AlertTriangle className="h-3.5 w-3.5" /> {result.conflicts.length} conflict{result.conflicts.length !== 1 ? 's' : ''} with your config</>
                          : <><CheckCircle className="h-3.5 w-3.5" /> No conflicts with your config</>
                        }
                      </span>
                    </div>
                    {result.conflicts.length === 0 && (
                      <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                        None of the changed defaults conflict with your global or per-cluster overrides. Safe to upgrade from a values perspective.
                      </p>
                    )}
                  </div>
                  <div className="flex items-center gap-2">
                    {riskIcon(risk)}
                    <span className="text-sm font-semibold text-gray-700 dark:text-gray-300">
                      {riskLabel(risk)}
                    </span>
                  </div>
                </div>
              </div>
            );
          })()}

          {/* No-changes state */}
          {result.total_changes === 0 && result.conflicts.length === 0 && (
            <div className="rounded-xl border-2 border-green-300 bg-green-50 p-6 text-center dark:border-green-700 dark:bg-green-950/30">
              <CheckCircle className="mx-auto h-10 w-10 text-green-500" />
              <h3 className="mt-2 text-lg font-semibold text-green-800 dark:text-green-300">
                No breaking changes detected. Safe to upgrade.
              </h3>
            </div>
          )}

          {/* Release Notes — above AI and changes */}
          {result.release_notes && result.release_notes.trim() !== '' && (
            <ReleaseNotesSection notes={result.release_notes} />
          )}

          {/* AI Analysis */}
          {aiEnabled ? (
            <div className="rounded-xl border border-purple-200 bg-white shadow-sm dark:border-purple-800 dark:bg-gray-900">
              <div className="flex items-center justify-between border-b border-purple-100 px-6 py-3 dark:border-purple-900">
                <div className="flex items-center gap-2">
                  <Sparkles className="h-4 w-4 text-purple-500" />
                  <h3 className="text-sm font-semibold text-gray-900 dark:text-white">AI Analysis</h3>
                </div>
                {!aiSummary && !aiLoading && (
                  <button
                    onClick={handleGetAISummary}
                    className="inline-flex items-center gap-1.5 rounded-lg bg-purple-600 px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-purple-700"
                  >
                    <Sparkles className="h-3.5 w-3.5" />
                    Analyze with AI
                  </button>
                )}
              </div>
              <div className="px-6 py-5">
                {aiLoading && (
                  <div className="flex items-center gap-3 text-sm text-gray-500 dark:text-gray-400">
                    <RefreshCw className="h-4 w-4 animate-spin" />
                    Analyzing with AI ({aiProvider || 'loading'})...
                  </div>
                )}
                {aiError && (
                  <div className="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-400">
                    {aiError}
                  </div>
                )}
                {aiSummary && (
                  <div>
                    <div className="prose-sm max-w-none text-sm leading-relaxed text-gray-700 dark:text-gray-300">
                      {aiSummary.split('\n').map((line, i) => {
                        const trimmed = line.trim();
                        if (trimmed === '') return <div key={i} className="h-3" />;
                        if (trimmed.startsWith('## '))
                          return <h3 key={i} className="mt-5 mb-2 text-base font-bold text-gray-900 dark:text-white border-b border-gray-200 dark:border-gray-700 pb-1.5">{trimmed.slice(3)}</h3>;
                        if (trimmed.startsWith('### '))
                          return <h4 key={i} className="mt-4 mb-1.5 text-sm font-bold text-gray-800 dark:text-gray-200">{trimmed.slice(4)}</h4>;
                        if (trimmed.startsWith('**') && trimmed.includes('**')) {
                          const content = trimmed.replace(/\*\*/g, '');
                          if (trimmed.endsWith('**'))
                            return <h4 key={i} className="mt-4 mb-1.5 text-sm font-bold text-gray-900 dark:text-white">{content}</h4>;
                          return <p key={i} className="mt-2"><strong className="text-gray-900 dark:text-white">{content.split(':')[0]}:</strong>{content.includes(':') ? content.slice(content.indexOf(':') + 1) : ''}</p>;
                        }
                        if (/^\d+\.\s/.test(trimmed))
                          return <div key={i} className="ml-4 mt-1.5 flex gap-2.5"><span className="shrink-0 font-bold text-cyan-600 dark:text-cyan-400">{trimmed.match(/^\d+/)?.[0]}.</span><span className="flex-1">{trimmed.replace(/^\d+\.\s*/, '')}</span></div>;
                        if (trimmed.startsWith('- ') || trimmed.startsWith('* '))
                          return <div key={i} className="ml-4 mt-1.5 flex gap-2.5"><span className="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-cyan-500" /><span className="flex-1">{trimmed.slice(2)}</span></div>;
                        return <p key={i} className="mt-1.5">{line}</p>;
                      })}
                    </div>
                    <p className="mt-5 border-t border-gray-100 pt-3 text-[10px] text-gray-400 dark:border-gray-800 dark:text-gray-600">
                      Powered by {aiProvider === 'ollama' ? 'Ollama (local)' : aiProvider === 'gemini' ? 'Google Gemini' : aiProvider === 'claude' ? 'Claude (Anthropic)' : aiProvider === 'openai' ? 'OpenAI' : 'AI'}
                    </p>
                  </div>
                )}
                {!aiSummary && !aiLoading && !aiError && (
                  <p className="text-xs text-gray-400 dark:text-gray-500">Click "Analyze with AI" for a detailed summary of changes, risk assessment, and action items.</p>
                )}
              </div>
            </div>
          ) : (
            <div className="flex items-center gap-2 rounded-lg border border-gray-200 bg-gray-50 px-4 py-3 text-sm text-gray-500 dark:border-gray-700 dark:bg-gray-800/50 dark:text-gray-400">
              <Sparkles className="h-4 w-4 shrink-0 text-gray-400" />
              <span>
                Enable an AI provider in{' '}
                <a
                  href="/settings"
                  className="font-medium text-cyan-600 underline hover:text-cyan-700 dark:text-cyan-400 dark:hover:text-cyan-300"
                >
                  Settings
                </a>{' '}
                to get AI-powered upgrade analysis.
              </span>
            </div>
          )}

          {/* Changes tabs */}
          {result.total_changes > 0 && (
            <div className="rounded-xl border border-gray-200 bg-white shadow-sm dark:border-gray-700 dark:bg-gray-900">
              <div className="flex border-b border-gray-200 dark:border-gray-700">
                {([
                  { key: 'added' as ChangeTab, label: 'Added Fields', count: result.added.length, color: 'text-green-600 dark:text-green-400' },
                  { key: 'removed' as ChangeTab, label: 'Removed Fields', count: result.removed.length, color: 'text-red-600 dark:text-red-400' },
                  { key: 'changed' as ChangeTab, label: 'Changed Defaults', count: result.changed.length, color: 'text-amber-600 dark:text-amber-400' },
                ]).map((tab) => (
                  <button
                    key={tab.key}
                    onClick={() => setActiveTab(tab.key)}
                    className={`flex items-center gap-2 border-b-2 px-5 py-3 text-sm font-medium transition-colors ${
                      activeTab === tab.key
                        ? 'border-cyan-500 text-cyan-700 dark:text-cyan-400'
                        : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200'
                    }`}
                  >
                    {tab.label}
                    <span
                      className={`rounded-full px-2 py-0.5 text-xs font-semibold ${
                        activeTab === tab.key
                          ? 'bg-cyan-100 text-cyan-700 dark:bg-cyan-900 dark:text-cyan-300'
                          : 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400'
                      }`}
                    >
                      {tab.count}
                    </span>
                  </button>
                ))}
              </div>

              <div>
                {activeTab === 'added' && <AddedFields items={result.added} />}
                {activeTab === 'removed' && <RemovedFields items={result.removed} />}
                {activeTab === 'changed' && <ChangedFields items={result.changed} />}
              </div>
            </div>
          )}

          {/* Conflicts */}
          <ConflictsSection conflicts={result.conflicts} />

        </div>
      )}
    </div>
  );
}

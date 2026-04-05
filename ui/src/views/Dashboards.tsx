import { useState } from 'react';
import {
  BarChart3,
  ExternalLink,
  Pencil,
  Plus,
  Trash2,
  X,
} from 'lucide-react';
import { useDashboards, extractUrlFromIframe } from '@/hooks/useDashboards';
import type { EmbeddedDashboard } from '@/hooks/useDashboards';

// ---------------------------------------------------------------------------
// Provider badge colors
// ---------------------------------------------------------------------------

const providerStyles: Record<
  EmbeddedDashboard['provider'],
  { bg: string; text: string; label: string }
> = {
  datadog: {
    bg: 'bg-purple-100 dark:bg-purple-900/40',
    text: 'text-purple-800 dark:text-purple-300',
    label: 'Datadog',
  },
  grafana: {
    bg: 'bg-orange-100 dark:bg-orange-900/40',
    text: 'text-orange-800 dark:text-orange-300',
    label: 'Grafana',
  },
  custom: {
    bg: 'bg-gray-100 dark:bg-gray-700',
    text: 'text-gray-600 dark:text-gray-300',
    label: 'Custom',
  },
};

// ---------------------------------------------------------------------------
// Provider helper text
// ---------------------------------------------------------------------------

const providerHelpText: Record<EmbeddedDashboard['provider'], string> = {
  datadog:
    'Go to your Datadog dashboard \u2192 Share \u2192 Generate public URL or Get embed code. Paste the URL or the full iframe snippet \u2014 the URL will be extracted automatically. Add your domain to the allowed referrers in Datadog.',
  grafana:
    'Use a Grafana dashboard URL. Ensure anonymous access or set up auth. Example: https://grafana.example.com/d/abc123/my-dashboard',
  custom: 'Enter any URL that can be loaded in an iframe.',
};

// ---------------------------------------------------------------------------
// Dashboard Form (shared for Add & Edit)
// ---------------------------------------------------------------------------

function DashboardForm({
  title,
  submitLabel,
  initial,
  onSubmit,
  onCancel,
}: {
  title: string;
  submitLabel: string;
  initial?: { name: string; url: string; provider: EmbeddedDashboard['provider'] };
  onSubmit: (d: Omit<EmbeddedDashboard, 'id'>) => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState(initial?.name ?? '');
  const [url, setUrl] = useState(initial?.url ?? '');
  const [provider, setProvider] = useState<EmbeddedDashboard['provider']>(
    initial?.provider ?? 'datadog',
  );

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim() || !url.trim()) return;
    const resolvedUrl = extractUrlFromIframe(url.trim());
    onSubmit({ name: name.trim(), url: resolvedUrl, provider });
  };

  return (
    <form
      onSubmit={handleSubmit}
      className="rounded-xl border border-gray-200 bg-[#e0f0ff] p-6 shadow-sm dark:border-gray-700 dark:bg-gray-900"
    >
      <div className="mb-4 flex items-center justify-between">
        <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
          {title}
        </h3>
        <button
          type="button"
          onClick={onCancel}
          className="rounded-lg p-1 text-gray-400 hover:bg-[#d6eeff] hover:text-gray-600 dark:hover:bg-gray-800 dark:hover:text-gray-200"
          aria-label="Close form"
        >
          <X className="h-5 w-5" />
        </button>
      </div>

      <div className="space-y-4">
        <div>
          <label
            htmlFor="dashboard-name"
            className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300"
          >
            Name
          </label>
          <input
            id="dashboard-name"
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. Cluster Monitoring - NonProd"
            className="w-full rounded-lg border border-gray-300 bg-[#e0f0ff] px-3 py-2 text-sm text-gray-900 placeholder-gray-400 focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 dark:placeholder-gray-500"
            required
          />
        </div>

        <div>
          <label
            htmlFor="dashboard-provider"
            className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300"
          >
            Provider
          </label>
          <select
            id="dashboard-provider"
            value={provider}
            onChange={(e) =>
              setProvider(e.target.value as EmbeddedDashboard['provider'])
            }
            className="w-full rounded-lg border border-gray-300 bg-[#e0f0ff] px-3 py-2 text-sm text-gray-900 focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100"
          >
            <option value="datadog">Datadog</option>
            <option value="grafana">Grafana</option>
            <option value="custom">Custom</option>
          </select>
        </div>

        <div>
          <label
            htmlFor="dashboard-url"
            className="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-300"
          >
            URL
          </label>
          {provider === 'datadog' ? (
            <textarea
              id="dashboard-url"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder={'Paste a URL:\nhttps://app.datadoghq.eu/graph/embed?token=...\n\nOr paste the full embed code:\n<iframe src="https://app.datadoghq.eu/graph/embed?token=..." ...></iframe>'}
              rows={4}
              className="w-full rounded-lg border border-gray-300 bg-[#e0f0ff] px-3 py-2 font-mono text-xs text-gray-900 placeholder-gray-400 focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 dark:placeholder-gray-500"
              required
            />
          ) : (
            <input
              id="dashboard-url"
              type="text"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder={provider === 'grafana' ? 'https://grafana.example.com/d/abc123/my-dashboard' : 'https://...'}
              className="w-full rounded-lg border border-gray-300 bg-[#e0f0ff] px-3 py-2 text-sm text-gray-900 placeholder-gray-400 focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 dark:placeholder-gray-500"
              required
            />
          )}
          <p className="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
            {providerHelpText[provider]}
          </p>
        </div>

        <button
          type="submit"
          className="w-full rounded-lg bg-teal-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-teal-700 focus:outline-none focus:ring-2 focus:ring-teal-500 focus:ring-offset-2 dark:focus:ring-offset-gray-900"
        >
          {submitLabel}
        </button>
      </div>
    </form>
  );
}

// ---------------------------------------------------------------------------
// Main View
// ---------------------------------------------------------------------------

export function Dashboards() {
  const { dashboards, addDashboard, updateDashboard, removeDashboard } =
    useDashboards();
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [showForm, setShowForm] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);

  const selected = dashboards.find((d) => d.id === selectedId) ?? null;

  // Auto-select first dashboard when list changes and nothing is selected
  const effectiveSelected =
    selected ?? (dashboards.length > 0 ? dashboards[0] : null);

  const editingDashboard = editingId
    ? dashboards.find((d) => d.id === editingId) ?? null
    : null;

  const handleAdd = (d: Omit<EmbeddedDashboard, 'id'>) => {
    addDashboard(d);
    setShowForm(false);
  };

  const handleUpdate = (d: Omit<EmbeddedDashboard, 'id'>) => {
    if (editingId) {
      updateDashboard(editingId, d);
      setEditingId(null);
    }
  };

  const handleEdit = (id: string) => {
    setEditingId(id);
    setShowForm(false);
  };

  const handleRemove = (id: string) => {
    removeDashboard(id);
    if (selectedId === id) setSelectedId(null);
    if (editingId === id) setEditingId(null);
  };

  /** Extract the actual URL in case the stored value is a raw iframe snippet (backwards compat). */
  function resolveUrl(dashboard: EmbeddedDashboard): string {
    return extractUrlFromIframe(dashboard.url);
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <h1 className="flex items-center gap-2 text-2xl font-bold text-gray-900 dark:text-gray-100">
          <BarChart3 className="h-7 w-7 text-teal-500" />
          External Dashboards
        </h1>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
          Embed Datadog and other monitoring dashboards
        </p>
      </div>

      <div className="flex gap-6">
        {/* Sidebar */}
        <aside className="w-80 shrink-0 space-y-2">
          {dashboards.length > 0 && (
            <button
              onClick={() => {
                setShowForm(true);
                setEditingId(null);
              }}
              className="flex w-full items-center justify-center gap-2 rounded-lg border-2 border-dashed border-gray-300 px-3 py-2 text-sm font-medium text-gray-500 transition-colors hover:border-teal-400 hover:text-teal-600 dark:border-gray-600 dark:text-gray-400 dark:hover:border-teal-500 dark:hover:text-teal-400"
            >
              <Plus className="h-4 w-4" />
              Add Dashboard
            </button>
          )}

          {dashboards.map((d) => {
            const isActive = effectiveSelected?.id === d.id;
            const style = providerStyles[d.provider];
            return (
              <button
                key={d.id}
                onClick={() => setSelectedId(d.id)}
                className={`flex w-full items-center gap-2 rounded-lg px-3 py-2.5 text-left text-sm font-medium transition-colors ${
                  isActive
                    ? 'border-l-[3px] border-teal-400 bg-teal-50 text-teal-700 dark:bg-teal-900/30 dark:text-teal-300'
                    : 'border-l-[3px] border-transparent text-gray-700 hover:bg-[#d6eeff] dark:text-gray-300 dark:hover:bg-gray-800'
                }`}
              >
                <span className="min-w-0 flex-1 text-sm leading-tight">{d.name}</span>
                <span
                  className={`shrink-0 rounded-full px-1.5 py-0.5 text-[10px] font-semibold ${style.bg} ${style.text}`}
                >
                  {style.label}
                </span>
              </button>
            );
          })}
        </aside>

        {/* Main area */}
        <div className="min-w-0 flex-1">
          {showForm && (
            <div className="mb-6">
              <DashboardForm
                title="Add Dashboard"
                submitLabel="Save Dashboard"
                onSubmit={handleAdd}
                onCancel={() => setShowForm(false)}
              />
            </div>
          )}

          {editingDashboard && (
            <div className="mb-6">
              <DashboardForm
                title="Edit Dashboard"
                submitLabel="Update Dashboard"
                initial={{
                  name: editingDashboard.name,
                  url: editingDashboard.url,
                  provider: editingDashboard.provider,
                }}
                onSubmit={handleUpdate}
                onCancel={() => setEditingId(null)}
              />
            </div>
          )}

          {effectiveSelected ? (
            <div className="space-y-3">
              {/* Toolbar */}
              <div className="flex items-center justify-between rounded-lg border border-gray-200 bg-[#e0f0ff] px-4 py-2.5 shadow-sm dark:border-gray-700 dark:bg-gray-900">
                <span className="text-sm font-semibold text-gray-900 dark:text-gray-100">
                  {effectiveSelected.name}
                </span>
                <div className="flex items-center gap-2">
                  <a
                    href={effectiveSelected.url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="flex items-center gap-1 rounded-md px-2.5 py-1.5 text-xs font-medium text-gray-600 hover:bg-[#d6eeff] dark:text-gray-400 dark:hover:bg-gray-800"
                  >
                    <ExternalLink className="h-3.5 w-3.5" />
                    Open in new tab
                  </a>
                  <button
                    onClick={() => handleEdit(effectiveSelected.id)}
                    className="flex items-center gap-1 rounded-md px-2.5 py-1.5 text-xs font-medium text-gray-600 hover:bg-[#d6eeff] dark:text-gray-400 dark:hover:bg-gray-800"
                  >
                    <Pencil className="h-3.5 w-3.5" />
                    Edit
                  </button>
                  <button
                    onClick={() => handleRemove(effectiveSelected.id)}
                    className="flex items-center gap-1 rounded-md px-2.5 py-1.5 text-xs font-medium text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/20"
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                    Remove
                  </button>
                </div>
              </div>

              {/* Iframe */}
              <div className="overflow-hidden rounded-xl border border-gray-200 shadow-md dark:border-gray-700">
                <iframe
                  src={resolveUrl(effectiveSelected)}
                  title={effectiveSelected.name}
                  className="w-full bg-[#e0f0ff] dark:bg-gray-950"
                  style={{ height: 'calc(100vh - 280px)' }}
                  sandbox="allow-same-origin allow-scripts allow-popups allow-forms allow-popups-to-escape-sandbox"
                />
              </div>

              <p className="text-center text-xs text-gray-400 dark:text-gray-500">
                Dashboard requires authentication with the provider
              </p>
            </div>
          ) : !showForm && !editingId ? (
            /* Empty state — only show when form is not open */
            <div className="flex flex-col items-center justify-center rounded-xl border-2 border-dashed border-gray-300 py-20 dark:border-gray-700">
              <BarChart3 className="h-12 w-12 text-gray-300 dark:text-gray-600" />
              <h3 className="mt-4 text-lg font-semibold text-gray-700 dark:text-gray-300">
                No dashboards configured
              </h3>
              <p className="mt-1 max-w-sm text-center text-sm text-gray-500 dark:text-gray-400">
                Embed monitoring dashboards from Datadog, Grafana, or any
                provider that supports iframe embedding.
              </p>
              <button
                onClick={() => setShowForm(true)}
                className="mt-6 flex items-center gap-2 rounded-lg bg-teal-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-teal-700"
              >
                <Plus className="h-4 w-4" />
                Add your first dashboard
              </button>
              <p className="mt-3 text-xs text-gray-400 dark:text-gray-500">
                Supports Datadog, Grafana, and custom dashboard URLs
              </p>
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
}

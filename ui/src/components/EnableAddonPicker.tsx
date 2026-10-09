/**
 * EnableAddonPicker — searchable dialog for staging addons to enable on a cluster.
 *
 * Opens a Dialog with a live-filter Input. Clicking an addon name stages it as
 * a pending-enable (the parent updates addonToggles and calls onClose when the
 * user is done — the picker stays open so multi-select is natural). The picker
 * lists addons from the REAL catalog (not from comparison state) — never
 * shows connectivity-check or untracked ArgoCD apps (V2-cleanup-32).
 */
import { useState, useRef, useEffect } from 'react';
import { Search, Plus, Loader2, AlertTriangle, RefreshCw } from 'lucide-react';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog';

interface EnableAddonPickerProps {
  /** Whether the picker dialog is open. */
  open: boolean;
  /** All catalog addon names from the real catalog fetch. */
  allAddonNames: string[];
  /** Addons that are currently enabled (original + already-staged enables). */
  enabledNames: Set<string>;
  /** True while the catalog is being fetched. */
  loading?: boolean;
  /** Non-null when the catalog fetch failed; the picker shows a retry button. */
  error?: string | null;
  /** Called when the user clicks an addon to stage it for enable. */
  onEnable: (addonName: string) => void;
  /** Called when the user clicks Done / closes the dialog. */
  onClose: () => void;
  /** Called when the user clicks Retry after a catalog fetch failure. */
  onRetry?: () => void;
}

export function EnableAddonPicker({
  open,
  allAddonNames,
  enabledNames,
  loading = false,
  error = null,
  onEnable,
  onClose,
  onRetry,
}: EnableAddonPickerProps) {
  const [query, setQuery] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);

  // Focus the search field whenever the dialog opens; reset query on close.
  useEffect(() => {
    if (open) {
      setQuery('');
      setTimeout(() => inputRef.current?.focus(), 60);
    }
  }, [open]);

  const available = allAddonNames
    .filter((n) => !enabledNames.has(n))
    .filter((n) => !query || n.toLowerCase().includes(query.toLowerCase()))
    .sort();

  return (
    <Dialog open={open} onOpenChange={(v) => { if (!v) onClose(); }}>
      <DialogContent
        className="max-w-md bg-[#f0f7ff] dark:bg-gray-800"
        aria-describedby={undefined}
      >
        <DialogHeader>
          <DialogTitle className="text-[#0a2a4a] dark:text-gray-100">
            Add addon
          </DialogTitle>
        </DialogHeader>

        {/* Loading state */}
        {loading && (
          <div className="flex items-center justify-center gap-2 py-4 text-sm text-[#3a6a8a] dark:text-gray-400">
            <Loader2 className="h-4 w-4 animate-spin" />
            Loading catalog…
          </div>
        )}

        {/* Error state */}
        {!loading && error && (
          <div className="flex flex-col items-center gap-2 py-4">
            <div className="flex items-center gap-2 text-sm text-red-600 dark:text-red-400">
              <AlertTriangle className="h-4 w-4 shrink-0" />
              {error}
            </div>
            {onRetry && (
              <button
                type="button"
                data-testid="addon-picker-retry"
                onClick={onRetry}
                className="inline-flex items-center gap-1.5 rounded-md bg-[#e8f4ff] px-3 py-1.5 text-xs font-medium text-[#2a5a7a] ring-1 ring-[#6aade0] hover:bg-[#d6eeff] dark:bg-gray-700 dark:text-gray-300 dark:ring-gray-600 dark:hover:bg-gray-600"
              >
                <RefreshCw className="h-3.5 w-3.5" />
                Retry
              </button>
            )}
          </div>
        )}

        {/* Search field (shown only when catalog loaded) */}
        {!loading && !error && (
          <div className="relative">
            <Search className="absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-[#5a8aaa]" />
            <input
              ref={inputRef}
              data-testid="addon-picker-search"
              type="text"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search addons…"
              className="h-9 w-full rounded-md bg-[#e8f4ff] pl-8 pr-3 text-sm text-[#0a2a4a] placeholder:text-[#5a8aaa] outline-none ring-2 ring-[#6aade0] focus:ring-teal-500 dark:bg-gray-700 dark:text-gray-100"
            />
          </div>
        )}

        {/* Addon list (shown only when catalog loaded) */}
        {!loading && !error && (
          <div
            data-testid="addon-picker-list"
            className="max-h-60 space-y-1 overflow-y-auto"
          >
            {available.length === 0 ? (
              <p className="py-3 text-center text-sm text-[#5a8aaa] dark:text-gray-400">
                {allAddonNames.filter((n) => !enabledNames.has(n)).length === 0
                  ? 'All catalog addons are already enabled on this cluster.'
                  : 'No addons match your search.'}
              </p>
            ) : (
              available.map((addonName) => (
                <button
                  key={addonName}
                  type="button"
                  data-testid={`addon-picker-item-${addonName}`}
                  onClick={() => onEnable(addonName)}
                  className="flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm hover:bg-[#d6eeff] dark:hover:bg-gray-700"
                >
                  <Plus className="h-3.5 w-3.5 shrink-0 text-teal-600 dark:text-teal-400" />
                  <span className="text-[#0a2a4a] dark:text-gray-200">{addonName}</span>
                </button>
              ))
            )}
          </div>
        )}

        <DialogFooter>
          <button
            type="button"
            data-testid="addon-picker-done"
            onClick={onClose}
            className="rounded-md bg-teal-600 px-4 py-2 text-sm font-medium text-white hover:bg-teal-700 dark:bg-teal-700 dark:hover:bg-teal-600"
          >
            Done
          </button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

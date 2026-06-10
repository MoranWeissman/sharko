/**
 * EnableAddonPicker — searchable dialog for staging addons to enable on a cluster.
 *
 * Opens a Dialog with a live-filter Input. Clicking an addon name stages it as
 * a pending-enable (the parent updates addonToggles and calls onClose when the
 * user is done — the picker stays open so multi-select is natural). The picker
 * never lists addons that are already enabled or already staged.
 */
import { useState, useRef, useEffect } from 'react';
import { Search, Plus } from 'lucide-react';
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
  /** All catalog addon names from the loaded addonToggles map. */
  allAddonNames: string[];
  /** Addons that are currently enabled (original + already-staged enables). */
  enabledNames: Set<string>;
  /** Called when the user clicks an addon to stage it for enable. */
  onEnable: (addonName: string) => void;
  /** Called when the user clicks Done / closes the dialog. */
  onClose: () => void;
}

export function EnableAddonPicker({
  open,
  allAddonNames,
  enabledNames,
  onEnable,
  onClose,
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
            Enable addon
          </DialogTitle>
        </DialogHeader>

        {/* Search field */}
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

        {/* Addon list */}
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
                <span className="capitalize text-[#0a2a4a] dark:text-gray-200">{addonName}</span>
              </button>
            ))
          )}
        </div>

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

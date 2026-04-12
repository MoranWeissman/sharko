import { useState } from 'react';
import { AlertTriangle, X } from 'lucide-react';

interface ArgoCDStatusBannerProps {
  visible: boolean;
}

export function ArgoCDStatusBanner({ visible }: ArgoCDStatusBannerProps) {
  const [dismissed, setDismissed] = useState(false);

  if (!visible || dismissed) return null;

  return (
    <div className="flex items-center justify-between rounded-lg border-2 border-amber-300 bg-amber-50 px-4 py-3 dark:border-amber-700 dark:bg-amber-900/20">
      <div className="flex items-center gap-2 text-sm text-amber-800 dark:text-amber-300">
        <AlertTriangle className="h-4 w-4 shrink-0" />
        <span>ArgoCD temporarily unreachable — showing last known state</span>
      </div>
      <button
        type="button"
        onClick={() => setDismissed(true)}
        className="ml-4 rounded p-0.5 text-amber-600 hover:bg-amber-100 dark:text-amber-400 dark:hover:bg-amber-800"
        aria-label="Dismiss"
      >
        <X className="h-4 w-4" />
      </button>
    </div>
  );
}

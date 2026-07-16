import { type ReactNode, useState } from 'react';
import { AlertTriangle, Info, X } from 'lucide-react';

interface InfoBannerProps {
  variant: 'info' | 'warning';
  title: string;
  children?: ReactNode;
  action?: ReactNode;
  onDismiss?: () => void;
}

/**
 * InfoBanner — reusable calm info banner (V3 RW1.1).
 *
 * A shared banner component with two variants:
 * - 'info': calm lavender/blue styling with Info icon
 * - 'warning': amber styling with AlertTriangle icon
 *
 * Optional inline action node and optional dismiss button. Extracted from
 * ConnectionErrorBanner and ArgoCDStatusBanner patterns to provide a single
 * reusable component for later stories (W8 connectivity-check explainer,
 * W4a stuck-reason, version/upgrade notices).
 */
export function InfoBanner({ variant, title, children, action, onDismiss }: InfoBannerProps) {
  const [dismissed, setDismissed] = useState(false);

  if (dismissed) return null;

  const handleDismiss = () => {
    setDismissed(true);
    onDismiss?.();
  };

  const isInfo = variant === 'info';
  const Icon = isInfo ? Info : AlertTriangle;

  const colorClasses = isInfo
    ? 'border-[#6aade0] bg-[#e8f4ff] text-[#1a4a6a] dark:border-blue-700 dark:bg-blue-950/40 dark:text-blue-200'
    : 'border-amber-300 bg-amber-50 text-amber-900 dark:border-amber-700 dark:bg-amber-950/40 dark:text-amber-200';

  const buttonClasses = isInfo
    ? 'text-[#2a5a7a] hover:bg-[#d6eeff] dark:text-blue-300 dark:hover:bg-blue-900'
    : 'text-amber-700 hover:bg-amber-100 dark:text-amber-300 dark:hover:bg-amber-900';

  return (
    <div
      role="alert"
      className={`flex items-start gap-2 rounded-lg border-2 px-4 py-3 text-sm ${colorClasses}`}
    >
      <Icon className="mt-0.5 h-4 w-4 flex-shrink-0" aria-hidden="true" />
      <div className="flex-1">
        <p className="font-medium">{title}</p>
        {children && <div className="mt-1">{children}</div>}
        {action && <div className="mt-2">{action}</div>}
      </div>
      {onDismiss && (
        <button
          type="button"
          onClick={handleDismiss}
          className={`ml-2 flex-shrink-0 rounded-md p-1 ${buttonClasses}`}
          aria-label="Dismiss"
        >
          <X className="h-4 w-4" />
        </button>
      )}
    </div>
  );
}

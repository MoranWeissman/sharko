import type { ReactNode } from 'react';

interface StatCardProps {
  title: string;
  value: string | number;
  icon?: ReactNode;
  color?: 'default' | 'success' | 'error' | 'warning';
  onClick?: () => void;
  selected?: boolean;
  subtitle?: string;
  size?: 'default' | 'large';
}

const borderColorMap: Record<string, string> = {
  default: 'border-l-gray-300 dark:border-l-gray-600',
  success: 'border-l-green-500',
  error: 'border-l-red-500',
  warning: 'border-l-yellow-500',
};

export function StatCard({
  title,
  value,
  icon,
  color = 'default',
  onClick,
  selected = false,
  subtitle,
  size = 'default',
}: StatCardProps) {
  const borderClass = borderColorMap[color];
  const isClickable = Boolean(onClick);

  const selectedClass = selected
    ? 'ring-2 ring-teal-500 ring-offset-1 shadow-md dark:ring-offset-gray-900'
    : '';

  const interactiveClass = isClickable
    ? 'cursor-pointer transition-shadow hover:shadow-md'
    : '';

  // Tier 1 hero variant (dashboard facelift): bg-card, rounded-xl, a soft
  // shadow, NO ring — the "permanently neutral" stat cards no longer need a
  // colored shell (Package 2 #3 already dropped the last of the severity
  // color from these), so the ring-2 border that used to be the universal
  // card shell is gone here too. Icon moves inline-left in a caption-style
  // title row instead of floating absolute top-right (one icon spec, kills
  // the h-5/h-6 mix).
  if (size === 'large') {
    return (
      <div
        role={isClickable ? 'button' : undefined}
        tabIndex={isClickable ? 0 : undefined}
        onClick={onClick}
        onKeyDown={
          isClickable
            ? (e) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault();
                  onClick?.();
                }
              }
            : undefined
        }
        className={`relative rounded-xl bg-card p-6 shadow-sm transition-shadow ${selectedClass} ${interactiveClass}`}
      >
        <div className="text-4xl font-bold tabular-nums text-card-foreground">{value}</div>
        <div className="mt-2 flex items-center gap-1.5 text-xs font-medium uppercase tracking-wide text-muted-foreground">
          {icon && <span className="text-muted-foreground [&_svg]:h-4 [&_svg]:w-4">{icon}</span>}
          <span>{title}</span>
        </div>
        {subtitle && (
          <div className="mt-1 text-sm text-muted-foreground normal-case tracking-normal">{subtitle}</div>
        )}
      </div>
    );
  }

  // Default variant — used by other pages (AddonDetail, AddonCatalog,
  // ClusterDetail, ClustersOverview). Same shell as before, hex colors
  // mapped onto the design-token system (Package 1) so it reads correctly
  // in both themes without a paired dark:* override.
  return (
    <div
      role={isClickable ? 'button' : undefined}
      tabIndex={isClickable ? 0 : undefined}
      onClick={onClick}
      onKeyDown={
        isClickable
          ? (e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault();
                onClick?.();
              }
            }
          : undefined
      }
      className={`relative rounded-lg ring-2 ring-border border-l-4 bg-card p-4 shadow-sm ${borderClass} ${selectedClass} ${interactiveClass}`}
    >
      {icon && (
        <div className="absolute right-4 top-4 text-muted-foreground">{icon}</div>
      )}
      <div className="text-2xl font-bold text-card-foreground">{value}</div>
      <div className="mt-1 text-sm text-muted-foreground">{title}</div>
      {subtitle && (
        <div className="mt-0.5 text-sm text-muted-foreground">{subtitle}</div>
      )}
    </div>
  );
}

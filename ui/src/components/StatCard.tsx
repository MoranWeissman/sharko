import type { ReactNode } from 'react';

export interface StatCardStatItem {
  label: string;
  value: string | number;
  /** Optional test hook — scoped queries in tests without ambiguous text matches. */
  testId?: string;
}

interface StatCardProps {
  title: string;
  value: string | number;
  icon?: ReactNode;
  color?: 'default' | 'success' | 'error' | 'warning';
  onClick?: () => void;
  selected?: boolean;
  subtitle?: string;
  size?: 'default' | 'large';
  /**
   * Large variant only (dashboard UX review 2026-08-01, finding H2 +
   * Package 2 #5): a row of small labeled numbers ("Total 10 · Connected 8
   * · Disconnected 1") instead of one big poster number. Falls back to a
   * single stat built from title/value when omitted, so existing large
   * callers keep working unchanged.
   */
  stats?: StatCardStatItem[];
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
  stats,
}: StatCardProps) {
  const borderClass = borderColorMap[color];
  const isClickable = Boolean(onClick);

  const selectedClass = selected
    ? 'ring-2 ring-teal-500 ring-offset-1 shadow-md dark:ring-offset-gray-900'
    : '';

  const interactiveClass = isClickable
    ? 'cursor-pointer transition-shadow hover:shadow-md'
    : '';

  // Tier 1 hero variant (dashboard UX review 2026-08-01, finding H2 +
  // Package 2 #5): "a labeled row of small stats", title first — NOT a
  // poster number. A big lone numeral over a tiny label reads well for one
  // count but stops meaning anything once a card wants to say "10 total, 8
  // connected, 1 disconnected" — the old text-4xl treatment forced that
  // into a fraction or a hand-picked single number instead. Title moves
  // above the stats (text-sm font-semibold, always readable) and the
  // number(s) shrink to text-lg — still bold and tabular, just no longer
  // the loudest thing on the card. bg-card, rounded-xl, soft shadow, no
  // ring — stat cards stay permanently neutral (Package 2 #3).
  if (size === 'large') {
    const items: StatCardStatItem[] = stats ?? [{ label: title, value }];
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
        <div className="flex items-center gap-1.5 text-sm font-semibold text-card-foreground">
          {icon && <span className="text-muted-foreground [&_svg]:h-4 [&_svg]:w-4">{icon}</span>}
          <span>{title}</span>
        </div>
        <div className="mt-3 flex flex-wrap items-baseline gap-x-5 gap-y-1.5">
          {items.map((item) => (
            <div key={item.label} className="flex flex-col" data-testid={item.testId}>
              <span className="text-xs text-muted-foreground">{item.label}</span>
              <span className="text-lg font-semibold tabular-nums text-card-foreground">{item.value}</span>
            </div>
          ))}
        </div>
        {subtitle && (
          <div className="mt-2 text-sm text-muted-foreground">{subtitle}</div>
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

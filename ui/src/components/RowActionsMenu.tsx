import type { ReactNode } from 'react';
import { Loader2, MoreVertical } from 'lucide-react';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { InfoHint } from '@/components/InfoHint';

export interface RowAction {
  label: string;
  icon?: ReactNode;
  onSelect: () => void;
  destructive?: boolean;
  /** Greys the item out and blocks onSelect — e.g. "Sync" when there's nothing to push. */
  disabled?: boolean;
  /**
   * Plain-words reason shown via an info hint — ONLY rendered when
   * `disabled` is true (S7.1 fix: the previous per-page copy of this
   * pattern rendered the hint whenever a reason string was set, which was
   * always, so every row — including enabled ones — drew an info icon,
   * and a screen reader announced "Why is Refresh unavailable?" on an
   * enabled button). Ignored when `disabled` is falsy.
   */
  disabledReason?: string;
  /** Shows a spinner in place of the icon and blocks onSelect while true. */
  loading?: boolean;
}

interface RowActionsMenuProps {
  actions: RowAction[];
  label?: string;
}

/**
 * RowActionsMenu — row-end kebab actions menu (V3 RW1.1; extended for
 * S1.2/S7.1 of the Managed Secrets rebuild). A MoreVertical (kebab)
 * trigger opening a radix DropdownMenu with the provided actions.
 * Destructive actions (e.g. "Remove") render in red and are grouped at
 * the BOTTOM, separated by a DropdownMenuSeparator from safe actions.
 *
 * An action can be `disabled` with a `disabledReason` — the item greys
 * out, onSelect is blocked, and an info hint appears carrying the reason
 * (and ONLY then — an enabled item never shows one). The item itself is
 * NOT given Radix's own `disabled` prop, because that also sets
 * `pointer-events: none` on the item and everything inside it, which
 * would make the nested info-hint trigger unclickable too; the item stays
 * pointer-interactive but its onSelect handler checks `disabled` itself,
 * and the hint's own wrapper opts back into `pointer-events-auto` so it
 * stays clickable inside a visually "disabled" row.
 *
 * Accessible: keyboard navigation + aria-label.
 */
export function RowActionsMenu({ actions, label = 'Row actions' }: RowActionsMenuProps) {
  const safeActions = actions.filter((a) => !a.destructive);
  const destructiveActions = actions.filter((a) => a.destructive);

  const renderAction = (action: RowAction, idx: number) => {
    const blocked = !!(action.disabled || action.loading);
    return (
      <DropdownMenuItem
        key={idx}
        variant={action.destructive ? 'destructive' : 'default'}
        onSelect={(e) => {
          if (blocked) {
            e.preventDefault();
            return;
          }
          action.onSelect();
        }}
        aria-disabled={blocked || undefined}
        className={`justify-between gap-3 ${action.disabled ? 'opacity-50' : ''}`}
      >
        <span className="flex items-center">
          {action.loading ? (
            <Loader2 className="mr-2 h-3.5 w-3.5 animate-spin" />
          ) : (
            action.icon && <span className="mr-2">{action.icon}</span>
          )}
          {action.label}
        </span>
        {action.disabled && action.disabledReason && (
          <span className="pointer-events-auto">
            <InfoHint text={action.disabledReason} label={`Why is ${action.label} unavailable?`} />
          </span>
        )}
      </DropdownMenuItem>
    );
  };

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label={label}
          onClick={(e) => e.stopPropagation()}
          className="inline-flex h-8 w-8 items-center justify-center rounded-md text-[#2a5a7a] hover:bg-[#e0f0ff] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-teal-500 dark:text-gray-400 dark:hover:bg-gray-700"
        >
          <MoreVertical className="h-4 w-4" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" onClick={(e) => e.stopPropagation()}>
        {safeActions.map(renderAction)}
        {destructiveActions.length > 0 && safeActions.length > 0 && (
          <DropdownMenuSeparator />
        )}
        {destructiveActions.map(renderAction)}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

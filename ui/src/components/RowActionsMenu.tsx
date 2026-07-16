import type { ReactNode } from 'react';
import { MoreVertical } from 'lucide-react';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';

interface RowAction {
  label: string;
  icon?: ReactNode;
  onSelect: () => void;
  destructive?: boolean;
}

interface RowActionsMenuProps {
  actions: RowAction[];
  label?: string;
}

/**
 * RowActionsMenu — row-end kebab actions menu (V3 RW1.1).
 *
 * A MoreVertical (kebab) trigger opening a radix DropdownMenu with the provided
 * actions. Destructive actions (e.g. "Remove") render in red and are grouped at
 * the BOTTOM, separated by a DropdownMenuSeparator from safe actions.
 *
 * Accessible: keyboard navigation + aria-label.
 */
export function RowActionsMenu({ actions, label = 'Row actions' }: RowActionsMenuProps) {
  const safeActions = actions.filter((a) => !a.destructive);
  const destructiveActions = actions.filter((a) => a.destructive);

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label={label}
          className="inline-flex h-8 w-8 items-center justify-center rounded-md text-[#2a5a7a] hover:bg-[#e0f0ff] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-teal-500 dark:text-gray-400 dark:hover:bg-gray-700"
        >
          <MoreVertical className="h-4 w-4" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {safeActions.map((action, idx) => (
          <DropdownMenuItem key={idx} onSelect={action.onSelect}>
            {action.icon && <span className="mr-2">{action.icon}</span>}
            {action.label}
          </DropdownMenuItem>
        ))}
        {destructiveActions.length > 0 && safeActions.length > 0 && (
          <DropdownMenuSeparator />
        )}
        {destructiveActions.map((action, idx) => (
          <DropdownMenuItem key={idx} onSelect={action.onSelect} variant="destructive">
            {action.icon && <span className="mr-2">{action.icon}</span>}
            {action.label}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

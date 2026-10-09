import { Info } from 'lucide-react';
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover';

// V2-cleanup-61.4 (G3): a handful of spots put genuinely load-bearing
// explanations ONLY inside a native `title=` attribute — invisible to touch
// and to keyboard users (a hover-only tooltip never fires without a mouse).
// This is the shared accessible affordance for those spots: a small "ⓘ"
// trigger that opens a real (Radix) Popover on click OR keyboard focus, so
// the explanation is reachable without a pointer. It's additive — the
// existing `title` attribute on the element it sits next to is left alone,
// so it still helps mouse users and keeps existing assertions on that
// attribute intact.

interface InfoHintProps {
  /** The explanation shown inside the popover. */
  text: string;
  /** Accessible name for the trigger button (screen readers / tests). */
  label?: string;
  className?: string;
}

export function InfoHint({ text, label = 'More info', className }: InfoHintProps) {
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          onClick={(e) => e.stopPropagation()}
          aria-label={label}
          className={`inline-flex shrink-0 items-center justify-center rounded-full text-[#5a8aaa] hover:text-[#0a3a5a] focus:outline-none focus-visible:ring-2 focus-visible:ring-teal-500 dark:text-gray-500 dark:hover:text-gray-300 ${className ?? ''}`}
        >
          <Info className="h-3.5 w-3.5" />
        </button>
      </PopoverTrigger>
      <PopoverContent
        align="start"
        className="w-72 text-sm"
        onClick={(e) => e.stopPropagation()}
      >
        {text}
      </PopoverContent>
    </Popover>
  );
}

export default InfoHint;

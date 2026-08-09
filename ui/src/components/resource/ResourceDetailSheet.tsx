// ResourceDetailSheet — the house "click a row, see the detail" side panel
// (S1.3). A thin wrapper around the repo's existing Sheet primitives
// (`@/components/ui/sheet`) that fixes the two things every detail panel
// needs and would otherwise reinvent per page: a title + optional
// subtitle header, and a scrollable body region so a panel with a lot of
// content (state, timestamps, a diff, action buttons) never overflows the
// viewport.
//
// The row itself never grows to show this — clicking a row opens this
// panel instead of expanding the row in place (that was the old
// per-row-expandable-diff pattern this replaces).

import type { ReactNode } from 'react'
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetDescription } from '@/components/ui/sheet'

export function ResourceDetailSheet({
  open,
  onOpenChange,
  title,
  subtitle,
  children,
  testId,
  wide,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: ReactNode
  subtitle?: ReactNode
  children: ReactNode
  testId?: string
  /**
   * Opt-in wider panel (P3-F2; widened to 760px in SSF-4, then back down to
   * 640px in the SSF-8 drawer calm-down — the panel now opens on one plain
   * sentence with everything else collapsed, so it no longer needs the
   * extra room a side-by-side diff used to justify; the comparison zone
   * that still wants width shows on demand instead of by default). The
   * default stays `sm:max-w-lg` (512px) so nothing that already uses this
   * sheet changes width; a panel that puts two things SIDE BY SIDE — the
   * Managed Secrets diff, which shows what a secret should be next to what
   * is actually on the cluster — asks for `sm:max-w-[640px]` instead. Below
   * that breakpoint the sheet is full-width either way and the caller's own
   * grid stacks.
   *
   * A prop rather than a per-page override so the two sizes stay a short,
   * named list instead of every page inventing its own width.
   */
  wide?: boolean
}) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className={`flex w-full flex-col ${wide ? 'sm:max-w-[640px]' : 'sm:max-w-lg'}`}
        data-testid={testId}
      >
        <SheetHeader>
          <SheetTitle>{title}</SheetTitle>
          {subtitle && <SheetDescription>{subtitle}</SheetDescription>}
        </SheetHeader>
        <div className="flex-1 space-y-4 overflow-y-auto px-4 pb-4">{children}</div>
      </SheetContent>
    </Sheet>
  )
}

export default ResourceDetailSheet

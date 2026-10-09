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
//
// SSF-9 (Secret Sync finish pass): the `wide` variant (introduced in P3-F2,
// widened to 760px in SSF-4, back to 640px in SSF-8) was removed here —
// Secret Sync was its only consumer, and SSF-9 retired that panel entirely
// in favour of a full page (SecretDetailPage.tsx). If a future panel needs
// more room than the default again, that's a new named variant to add
// back, not a reason to leave this one unused.

import type { ReactNode } from 'react'
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetDescription } from '@/components/ui/sheet'

export function ResourceDetailSheet({
  open,
  onOpenChange,
  title,
  subtitle,
  children,
  testId,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: ReactNode
  subtitle?: ReactNode
  children: ReactNode
  testId?: string
}) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="flex w-full flex-col sm:max-w-lg" data-testid={testId}>
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

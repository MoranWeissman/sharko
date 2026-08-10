import { type ReactNode } from 'react'
import { Inbox } from 'lucide-react'

interface EmptyStateProps {
  title: string
  description?: string
  action?: ReactNode
  /**
   * Compact recipe for panels embedded inside a bigger page (dashboard
   * cards, etc.) — small icon, one line, no mascot. ~60px tall instead of
   * the full-page ~250px mascot layout. Full-page views (a whole tab/route
   * with nothing else on it) should keep the default (mascot) look.
   */
  compact?: boolean
  /** Icon shown in the compact variant only. Defaults to a plain inbox glyph. */
  icon?: ReactNode
}

export function EmptyState({ title, description, action, compact = false, icon }: EmptyStateProps) {
  if (compact) {
    return (
      <div className="flex flex-col items-center justify-center gap-1 py-3.5 text-center">
        <span className="text-muted-foreground">{icon ?? <Inbox className="h-5 w-5" />}</span>
        <p className="text-sm font-medium text-card-foreground">{title}</p>
        {description && (
          <p className="text-sm text-muted-foreground">{description}</p>
        )}
        {action}
      </div>
    )
  }

  return (
    <div className="flex flex-col items-center justify-center gap-4 py-16 text-center">
      <img
        src="/sharko-mascot.png"
        alt=""
        className="h-20 w-auto opacity-80"
      />
      <div>
        <h3 className="text-lg font-semibold text-card-foreground">{title}</h3>
        {description && (
          <p className="mt-1 text-sm text-muted-foreground">{description}</p>
        )}
      </div>
      {action}
    </div>
  )
}

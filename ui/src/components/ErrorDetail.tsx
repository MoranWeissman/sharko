import { ApiError } from '@/services/api'

/**
 * ErrorDetail — renders an error the way the adopted error standard wants
 * (error review package 2): a plain headline, an action-oriented hint, and
 * an optional dim technical-detail line. Never a colon-glued chain.
 *
 * Accepts anything thrown from the api.ts helpers — an ApiError (structured
 * cause/hint/code from the server's boundary), a bare Error (e.g. "Session
 * expired", or an offline fetch failure), or any other value — and always
 * renders at least a headline, falling back to `fallbackMessage` when the
 * thrown value isn't an Error at all.
 */
export interface ErrorDetailProps {
  error: unknown
  /** Shown when `error` isn't an Error instance (defensive — shouldn't
   * normally happen with the api.ts helpers). */
  fallbackMessage?: string
  className?: string
}

export function ErrorDetail({ error, fallbackMessage = 'Something went wrong.', className }: ErrorDetailProps) {
  const headline = error instanceof Error ? error.message : fallbackMessage
  const hint = error instanceof ApiError ? error.hint : undefined
  const cause = error instanceof ApiError ? error.cause : undefined

  return (
    <div className={className}>
      <p className="text-sm font-medium text-red-600 dark:text-red-400">{headline}</p>
      {hint && (
        <p className="mt-1 text-sm text-[#3a6a8a] dark:text-gray-400">{hint}</p>
      )}
      {cause && (
        <p className="mt-1 font-mono text-xs text-[#5a8aaa] dark:text-gray-500">{cause}</p>
      )}
    </div>
  )
}

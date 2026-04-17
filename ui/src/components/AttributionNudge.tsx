import { Link } from 'react-router-dom'
import { AlertTriangle, ExternalLink } from 'lucide-react'

/**
 * Banner shown next to a Tier 2 (configuration) action when the user has not
 * configured a personal GitHub PAT. The action will still proceed using the
 * service account, but git history will not show the user as the author —
 * only as a Co-authored-by trailer.
 *
 * Render this whenever the backend returns `attribution_warning: "no_per_user_pat"`
 * in a mutation response, OR proactively before the user clicks if you've
 * already detected (via /users/me) that has_github_token=false.
 */
export function AttributionNudge({
  className,
  inline,
}: {
  className?: string
  /** When true, renders a slimmer single-line variant (for narrow rails). */
  inline?: boolean
}) {
  return (
    <div
      role="alert"
      className={
        'flex items-start gap-2 rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-700 dark:bg-amber-950/40 dark:text-amber-200 ' +
        (className ?? '')
      }
    >
      <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0" aria-hidden="true" />
      <div className="flex-1">
        {inline ? (
          <span>
            This change will be attributed to the Sharko service account.{' '}
            <Link
              to="/settings?section=my-account"
              className="inline-flex items-center gap-1 font-medium underline hover:no-underline"
            >
              Set up your personal GitHub PAT
              <ExternalLink className="h-3 w-3" />
            </Link>{' '}
            for proper attribution.
          </span>
        ) : (
          <>
            <p className="font-medium">No personal GitHub token configured</p>
            <p className="mt-1">
              This change will be committed to Git as the Sharko service account, with you listed
              only as a co-author. Set up a personal GitHub PAT so future configuration changes are
              authored by you directly.
            </p>
            <Link
              to="/settings?section=my-account"
              className="mt-2 inline-flex items-center gap-1 rounded-md border border-amber-400 bg-amber-100 px-3 py-1 text-xs font-medium hover:bg-amber-200 dark:border-amber-700 dark:bg-amber-900 dark:hover:bg-amber-800"
            >
              Open Settings → My Account
              <ExternalLink className="h-3 w-3" />
            </Link>
          </>
        )}
      </div>
    </div>
  )
}

import { Link } from 'react-router-dom'
import { EmptyState } from '@/components/EmptyState'

/** Words of the page shown for an address Sharko does not have. Pinned by exact text. */
export const NOT_FOUND_TITLE = 'This page does not exist'
export const NOT_FOUND_DESCRIPTION = 'The address may be mistyped, or the page may have moved.'
export const NOT_FOUND_LINK = 'Go to the Dashboard'

export function NotFound() {
  return (
    <div data-testid="not-found-page">
      <EmptyState
        title={NOT_FOUND_TITLE}
        description={NOT_FOUND_DESCRIPTION}
        action={
          <Link
            to="/dashboard"
            className="inline-flex items-center gap-2 rounded-md bg-[#0a2a4a] px-4 py-2 text-sm font-semibold text-white hover:bg-[#0d3558] dark:bg-blue-700 dark:hover:bg-blue-600"
          >
            {NOT_FOUND_LINK}
          </Link>
        }
      />
    </div>
  )
}

export default NotFound

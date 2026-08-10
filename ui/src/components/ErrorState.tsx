interface ErrorStateAction {
  label: string;
  onClick: () => void;
}

interface ErrorStateProps {
  message: string;
  /** A small, dim technical-detail line shown under the message (error
   * review package 2) — e.g. an ApiError's `cause`. Optional, backward
   * compatible: callers that only pass `message` render exactly as before. */
  detail?: string;
  /** A named action distinct from the generic "Try Again" retry button —
   * e.g. "Open Settings → Connections" for an error whose fix lives
   * elsewhere in the app. */
  action?: ErrorStateAction;
  onRetry?: () => void;
}

export function ErrorState({ message, detail, action, onRetry }: ErrorStateProps) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 py-12 text-center">
      <img
        src="/sharko-mascot.png"
        alt=""
        className="h-16 w-auto opacity-70"
      />
      <p className="text-sm text-[#0a3a5a] dark:text-gray-300">{message}</p>
      {detail && (
        <p className="max-w-md font-mono text-sm text-[#5a8aaa] dark:text-gray-500">{detail}</p>
      )}
      {(onRetry || action) && (
        <div className="flex items-center gap-3">
          {onRetry && (
            <button
              type="button"
              onClick={onRetry}
              className="rounded-md bg-teal-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-teal-700 focus:outline-none focus:ring-2 focus:ring-teal-400 focus:ring-offset-2 dark:ring-offset-gray-900"
            >
              Try Again
            </button>
          )}
          {action && (
            <button
              type="button"
              onClick={action.onClick}
              className="rounded-md border border-[#5a9dd0] px-4 py-2 text-sm font-medium text-[#0a3a5a] transition-colors hover:bg-[#d6eeff] dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
            >
              {action.label}
            </button>
          )}
        </div>
      )}
    </div>
  );
}

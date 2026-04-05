interface ErrorStateProps {
  message: string;
  onRetry?: () => void;
}

export function ErrorState({ message, onRetry }: ErrorStateProps) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 py-12 text-center">
      <img
        src="/sharko-mascot.png"
        alt=""
        className="h-16 w-auto opacity-70"
      />
      <p className="text-sm text-[#0a3a5a] dark:text-gray-300">{message}</p>
      {onRetry && (
        <button
          type="button"
          onClick={onRetry}
          className="rounded-md bg-teal-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-teal-700 focus:outline-none focus:ring-2 focus:ring-teal-400 focus:ring-offset-2 dark:ring-offset-gray-900"
        >
          Try Again
        </button>
      )}
    </div>
  );
}

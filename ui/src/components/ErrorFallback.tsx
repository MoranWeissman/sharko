import { AlertTriangle } from 'lucide-react';

interface ErrorFallbackProps {
  error: Error;
  resetErrorBoundary: () => void;
}

export function ErrorFallback({ error, resetErrorBoundary }: ErrorFallbackProps) {
  return (
    <div className="mx-auto max-w-lg rounded-lg border border-gray-200 bg-white shadow-sm dark:border-gray-700 dark:bg-gray-800">
      <div className="h-1 rounded-t-lg bg-red-500" />
      <div className="flex flex-col items-center gap-4 p-6 text-center">
        <AlertTriangle className="h-10 w-10 text-red-500" />
        <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
          Something went wrong
        </h2>
        <pre className="w-full overflow-auto rounded-md bg-gray-100 p-3 text-left font-mono text-sm text-gray-700 dark:bg-gray-700 dark:text-gray-200">
          {error.message}
        </pre>
        <button
          type="button"
          onClick={resetErrorBoundary}
          className="rounded-md bg-red-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-red-700 focus:outline-none focus:ring-2 focus:ring-red-500 focus:ring-offset-2 dark:ring-offset-gray-800"
        >
          Try again
        </button>
      </div>
    </div>
  );
}

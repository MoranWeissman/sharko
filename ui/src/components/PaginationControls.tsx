// Shared pagination controls extracted from AddonCatalog.tsx
// Used by: AddonCatalog, ClustersOverview, Observability

export type PageSize = 20 | 50 | 100;

/**
 * Pagination controls with ellipsis support.
 * Shows: Prev | 1 ... 3 4 5 ... 10 | Next
 */
export function PaginationControls({
  page,
  totalPages,
  onPageChange,
}: {
  page: number;
  totalPages: number;
  onPageChange: (p: number) => void;
}) {
  if (totalPages <= 1) return null;

  return (
    <div className="flex items-center gap-2">
      <button
        type="button"
        disabled={page <= 1}
        onClick={() => onPageChange(page - 1)}
        className="rounded border px-3 py-1 text-sm font-medium text-[#0a3a5a] transition-colors hover:bg-[#d6eeff] disabled:cursor-not-allowed disabled:opacity-40 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
      >
        Prev
      </button>
      {Array.from({ length: totalPages }, (_, i) => i + 1)
        .filter(
          (p) =>
            p === 1 ||
            p === totalPages ||
            Math.abs(p - page) <= 1,
        )
        .reduce<(number | 'ellipsis')[]>((acc, p, idx, arr) => {
          if (idx > 0 && p - (arr[idx - 1] as number) > 1) {
            acc.push('ellipsis');
          }
          acc.push(p);
          return acc;
        }, [])
        .map((item, idx) =>
          item === 'ellipsis' ? (
            <span key={`e-${idx}`} className="px-1 text-[#3a6a8a]">
              ...
            </span>
          ) : (
            <button
              key={item}
              type="button"
              onClick={() => onPageChange(item)}
              className={`rounded px-3 py-1 text-sm font-medium transition-colors ${
                item === page
                  ? 'bg-teal-600 text-white'
                  : 'text-[#0a3a5a] hover:bg-[#d6eeff] dark:text-gray-300 dark:hover:bg-gray-700'
              }`}
            >
              {item}
            </button>
          ),
        )}
      <button
        type="button"
        disabled={page >= totalPages}
        onClick={() => onPageChange(page + 1)}
        className="rounded border px-3 py-1 text-sm font-medium text-[#0a3a5a] transition-colors hover:bg-[#d6eeff] disabled:cursor-not-allowed disabled:opacity-40 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
      >
        Next
      </button>
    </div>
  );
}

/**
 * Page size selector (e.g., "Show: 20 | 50 | 100")
 */
export function PageSizeSelector({
  pageSize,
  onChange,
}: {
  pageSize: PageSize;
  onChange: (size: PageSize) => void;
}) {
  const sizes: PageSize[] = [20, 50, 100];
  return (
    <div className="flex items-center gap-2">
      <span className="text-xs text-[#2a5a7a] dark:text-gray-400">Show:</span>
      <div className="flex gap-1">
        {sizes.map((size) => (
          <button
            key={size}
            type="button"
            onClick={() => onChange(size)}
            className={`rounded px-2 py-0.5 text-xs font-medium transition-colors ${
              pageSize === size
                ? 'bg-teal-600 text-white'
                : 'text-[#2a5a7a] hover:bg-[#d6eeff] dark:text-gray-400 dark:hover:bg-gray-800'
            }`}
          >
            {size}
          </button>
        ))}
      </div>
    </div>
  );
}

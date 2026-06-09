import { useEffect, useState } from 'react';

/**
 * useDebouncedValue returns a copy of `value` that only updates after `delayMs`
 * has elapsed without a further change. Used to keep the audit filters from
 * firing a fetch on every keystroke (V2-cleanup-25, item 3.4).
 */
export function useDebouncedValue<T>(value: T, delayMs = 300): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const id = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(id);
  }, [value, delayMs]);
  return debounced;
}

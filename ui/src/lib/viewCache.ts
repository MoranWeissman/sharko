// Tiny module-scoped cache of "last successful response" per view (perf
// S2). Navigating to a page you've already loaded this browser session
// paints instantly from the last data instead of a blank spinner, then
// refreshes quietly in the background — see the fetch/mount pattern in
// Dashboard.tsx, AddonCatalog.tsx, and ClustersOverview.tsx.
//
// Deliberately a plain in-memory Map, not sessionStorage/localStorage: it
// lives for the tab's JS session only (a full page reload starts cold,
// same as today) and never needs to survive a reload or hold anything
// beyond what's already in React state.
//
// No generic invalidation system on purpose — a view that mutates data and
// wants the cache to reflect it just calls setCached again at its own
// existing refetch site (see each view's mutation success handlers).

export interface CacheEntry<T> {
  data: T;
  timestamp: number;
}

const store = new Map<string, CacheEntry<unknown>>();

/** Returns the cached entry for `key`, or undefined on a cache miss. */
export function getCached<T>(key: string): CacheEntry<T> | undefined {
  return store.get(key) as CacheEntry<T> | undefined;
}

/** Stores `data` under `key`, stamped with the current time. */
export function setCached<T>(key: string, data: T): CacheEntry<T> {
  const entry: CacheEntry<T> = { data, timestamp: Date.now() };
  store.set(key, entry);
  return entry;
}

/** True if `key` has a cached entry. */
export function hasCached(key: string): boolean {
  return store.has(key);
}

/** Drops the cached entry for `key`, if any. */
export function clearCached(key: string): void {
  store.delete(key);
}

/** Drops every cached entry. Mainly for tests. */
export function clearAllCached(): void {
  store.clear();
}

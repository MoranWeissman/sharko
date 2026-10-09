// sprint-backlog-burndown-r1 Lane B-a — rolled-over tab self-heal.
//
// After a live image roll, an already-open browser tab can still be holding
// the OLD index.html, which references hashed chunk files (JS/CSS) the NEW
// image no longer ships. When the app tries to lazy-load one of those old
// chunks, Vite fires `vite:preloadError` on `window` for a failed
// modulepreload/dynamic import, and (depending on how the failure surfaces)
// it can also show up as an unhandled promise rejection from a lazy route's
// import(). Either way, the fix for the TAB is the same: reload once to
// pick up the current shell, which references chunks that actually exist.
//
// This module holds the guarded, testable logic. main.tsx wires it to the
// two browser events; this file has no DOM/window dependency so it can be
// unit tested without a real chunk failure.

/** sessionStorage key used to guard against reload-looping a genuinely
 * broken deploy — set on the first reload, cleared on a successful boot. */
export const RELOAD_FLAG = 'sharko:chunk-reload'

/** Minimal Storage-shaped interface so tests can pass a fake instead of a
 * real sessionStorage. */
export interface StorageLike {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
  removeItem(key: string): void
}

export interface ChunkReloadGuardDeps {
  storage: StorageLike
  reload: () => void
}

/**
 * Handles a detected chunk-load failure. On the FIRST failure this session,
 * sets the guard flag and reloads the page once. If the flag is already
 * set — meaning a previous reload already happened and the app is failing
 * again — this does nothing, so a truly broken deploy fails loudly instead
 * of reload-looping forever.
 *
 * Returns true if a reload was triggered, false otherwise (useful for
 * tests and any caller that wants to log the outcome).
 */
export function handleChunkLoadFailure(deps: ChunkReloadGuardDeps): boolean {
  if (deps.storage.getItem(RELOAD_FLAG)) {
    return false
  }
  deps.storage.setItem(RELOAD_FLAG, '1')
  deps.reload()
  return true
}

/**
 * Clears the guard flag. Call this once the app has booted successfully so
 * a LATER genuine chunk failure (e.g. after the next deploy) gets its own
 * fresh reload attempt instead of being silently suppressed forever.
 */
export function clearChunkReloadFlag(storage: StorageLike): void {
  storage.removeItem(RELOAD_FLAG)
}

/**
 * True when `error` looks like a failed dynamic import / chunk load rather
 * than some unrelated failure. Used to filter the generic
 * `unhandledrejection` event down to the case this guard should react to —
 * a lazy route's `import()` rejecting because the chunk file is gone.
 */
export function isChunkLoadError(error: unknown): boolean {
  if (error == null) return false
  const message = error instanceof Error ? error.message : String(error)
  // Deliberately specific to the browsers' dynamic-import failure wording
  // (Chrome: "Failed to fetch dynamically imported module: ...", Firefox:
  // "error loading dynamically imported module: ...", Safari: "Importing a
  // module script failed."), NOT a generic "failed to fetch" — that would
  // false-positive on unrelated API fetch errors and reload the tab for the
  // wrong reason.
  return /dynamically imported module|importing a module script failed/i.test(message)
}

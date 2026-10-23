import type { AdoptBlocker, AdoptResult, AdoptWarning } from '@/services/models'

/**
 * Warnings and blockers of an adopt answer, in one shape.
 *
 * An older server sends warnings as plain sentences. They have no id, so the
 * sentence itself stands in for the id: the user still has to tick each one.
 * A missing field means none.
 */
export function adoptWarningsOf(r: Pick<AdoptResult, 'warnings'> | undefined | null): AdoptWarning[] {
  const raw = r?.warnings
  if (!Array.isArray(raw)) return []
  return raw
    .map((w): AdoptWarning | null => {
      if (typeof w === 'string') return w ? { id: w, message: w } : null
      if (w && typeof w === 'object' && typeof w.message === 'string') {
        return { ...w, id: typeof w.id === 'string' && w.id ? w.id : w.message }
      }
      return null
    })
    .filter((w): w is AdoptWarning => w !== null)
}

export function adoptBlockersOf(r: Pick<AdoptResult, 'blockers'> | undefined | null): AdoptBlocker[] {
  const raw = r?.blockers
  if (!Array.isArray(raw)) return []
  return raw.filter((b) => b && typeof b.message === 'string' && b.message !== '')
}

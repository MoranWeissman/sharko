import { type ClassValue, clsx } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

// V2-cleanup-61.1 (E3a): PR `operation` is the raw canonical enum from the
// backend (see internal/prtracker/types.go Op* constants — e.g.
// "register-cluster", "addon-enable"). Turn the hyphenated enum into a
// plain, readable phrase instead of surfacing it verbatim in toasts.
export function prettyOperation(operation: string): string {
  return operation.replace(/-/g, ' ')
}

// Shared strict-semver compare (dashboard UX review 2026-08-01) — used by
// the Dashboard's Upgrades stat AND the version matrix's sort-first-what's-
// outdated ordering, so "is this newer" means the same thing in both
// places. Fails open (not-newer) on anything that doesn't parse as
// MAJOR.MINOR.PATCH, so a malformed version string never gets flagged as
// an available upgrade. Same parser AddonDetail.tsx's isDowngrade uses.
export function parseSemver(v: string): [number, number, number] | null {
  const trimmed = v.replace(/^v/, '').split('-')[0]
  const parts = trimmed.split('.').map(Number)
  if (parts.length < 3 || parts.some((n) => Number.isNaN(n))) return null
  return [parts[0], parts[1], parts[2]]
}

export function isNewerVersion(current: string, candidate: string): boolean {
  const c = parseSemver(current)
  const n = parseSemver(candidate)
  if (!c || !n) return false
  if (n[0] !== c[0]) return n[0] > c[0]
  if (n[1] !== c[1]) return n[1] > c[1]
  return n[2] > c[2]
}

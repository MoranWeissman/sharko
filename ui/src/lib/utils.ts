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

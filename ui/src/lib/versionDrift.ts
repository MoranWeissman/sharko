import type { VersionMatrixCell } from '@/services/models'

/**
 * A cluster is "behind" the catalog only when its version differs from the
 * catalog's AND it is not newer than it.
 *
 * `drift_from_catalog` alone means "different", which includes a cluster
 * that was upgraded on its own, ahead of the rest. Counting those as
 * "behind" told people the clusters they had just upgraded were out of date
 * (pre-release walk 4).
 */
export function isBehindCatalog(cell: VersionMatrixCell | null | undefined): boolean {
  return !!cell?.drift_from_catalog && !cell?.ahead_of_catalog
}

/** True when the cluster runs a newer version than the catalog names. */
export function isAheadOfCatalog(cell: VersionMatrixCell | null | undefined): boolean {
  return !!cell?.drift_from_catalog && !!cell?.ahead_of_catalog
}

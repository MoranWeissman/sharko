// connectionRowCanonical — a TEST-ONLY stand-in for the server's canonical
// derivation, so this suite's fixtures look like what a real server sends.
//
// # Why this file exists, and why it is not production code
//
// B5 moved the fleet row's whole answer onto the server: `management_mode`,
// `managed_scope`, `sync_state`, `verification_scope`, `approval_required`,
// `headline`, `qualifier` and `health` all arrive computed
// (internal/api/connection_canonical.go), and the browser renders them and
// derives NOTHING. That is the product owner's ruling.
//
// Which leaves the fixtures. Dozens of them predate B5 and carry only the
// legacy `state` word. A row like that is a row from a server that has no
// canonical answer, and the page correctly renders it as "Not checked yet" —
// so every one of those tests would be asserting against a response no real
// server produces.
//
// So the FIXTURES go through this mapping, once, at the API mock. Production
// code never imports it. It fills in ONLY fields a fixture has not already
// set, which is what lets the B5 tests state a deliberately incoherent row
// (a `synced` word at `partial` verification) and prove the page refuses it.
//
// The words below are the exact Go constants from connection_canonical.go.

import type { ConnectionSecretRow, ManagedSecretsResponse } from '@/services/models'
import { CONNECTION_SENTENCES } from '@/generated/connection-sentences'

export const H_CONNECTION_SYNCED = CONNECTION_SENTENCES.headlineConnectionSynced
export const H_ADDON_LABELS_SYNCED = CONNECTION_SENTENCES.headlineAddonLabelsSynced
export const H_ADDON_LABELS_OUT_OF_SYNC = CONNECTION_SENTENCES.headlineAddonLabelsOutOfSync
export const H_OUT_OF_SYNC = CONNECTION_SENTENCES.headlineOutOfSync
export const H_OUT_OF_SYNC_APPROVAL = CONNECTION_SENTENCES.headlineOutOfSyncApproval
export const H_OUT_OF_SYNC_CANNOT_RESTORE = CONNECTION_SENTENCES.headlineOutOfSyncCannotRestore
export const H_BLOCKED = CONNECTION_SENTENCES.headlineBlocked
export const H_VERIFICATION_INCOMPLETE = CONNECTION_SENTENCES.headlineVerificationIncomplete
export const H_EKS_PARTIAL = CONNECTION_SENTENCES.headlineConfigurationMatchesEKS
export const H_UNKNOWN_CHECK_FAILED = CONNECTION_SENTENCES.headlineUnknownCheckFailed
export const H_NOT_CHECKED_YET = CONNECTION_SENTENCES.headlineNotCheckedYet

export const Q_SELF_MANAGED = CONNECTION_SENTENCES.qualifierSelfManaged
export const Q_LEGACY_INLINE =
  CONNECTION_SENTENCES.qualifierLegacyInline

type Mode = NonNullable<ConnectionSecretRow['management_mode']>

function managedScopeFor(mode: Mode): NonNullable<ConnectionSecretRow['managed_scope']> {
  if (mode === 'self_managed' || mode === 'legacy_inline') return 'addon_labels'
  if (mode === 'foreign_owned') return 'none'
  return 'full_connection'
}

/** The legacy row word a canonical sync state projects onto (connectionRowLegacyState in Go). */
function syncStateForLegacy(state: string): NonNullable<ConnectionSecretRow['sync_state']> {
  switch (state) {
    case 'in_sync':
      return 'synced'
    case 'out_of_sync':
    case 'missing':
      return 'out_of_sync'
    case 'foreign':
      return 'blocked'
    default:
      return 'unknown'
  }
}

function headlineFor(row: {
  mode: Mode
  scope: NonNullable<ConnectionSecretRow['managed_scope']>
  sync: NonNullable<ConnectionSecretRow['sync_state']>
  verification: NonNullable<ConnectionSecretRow['verification_scope']>
  approval: boolean
  liveSecretMissing: boolean
  checked: boolean
}): string {
  switch (row.sync) {
    case 'blocked':
      return H_BLOCKED
    case 'synced':
      return row.scope === 'addon_labels' ? H_ADDON_LABELS_SYNCED : H_CONNECTION_SYNCED
    case 'out_of_sync':
      if (row.mode === 'legacy_inline' && row.liveSecretMissing) return H_OUT_OF_SYNC_CANNOT_RESTORE
      if (row.approval) return H_OUT_OF_SYNC_APPROVAL
      if (row.mode === 'self_managed') return H_ADDON_LABELS_OUT_OF_SYNC
      return H_OUT_OF_SYNC
    default:
      if (!row.checked) return H_NOT_CHECKED_YET
      if (row.verification === 'none') return H_UNKNOWN_CHECK_FAILED
      if (row.mode === 'legacy_inline') return H_VERIFICATION_INCOMPLETE
      return H_EKS_PARTIAL
  }
}

/**
 * canonicalConnectionRow fills in the canonical fields a pre-B5 fixture is
 * missing. Anything the fixture already states is left exactly as written.
 */
export function canonicalConnectionRow(row: ConnectionSecretRow): ConnectionSecretRow {
  if (row.headline !== undefined) return row
  const mode = row.management_mode ?? 'sharko_managed'
  const scope = row.managed_scope ?? managedScopeFor(mode)
  const sync = row.sync_state ?? syncStateForLegacy(row.state)
  // A pre-B5 fixture that says in_sync means "compared and it matched", so
  // the honest scope is full. Everything else that is not a clean match had
  // no successful comparison behind it in these fixtures.
  const verification = row.verification_scope ?? (sync === 'synced' ? 'full' : sync === 'unknown' ? 'none' : 'full')
  const approval = row.approval_required ?? false
  const checked = Boolean(row.credential_checked_at ?? row.last_checked)
  return {
    ...row,
    management_mode: mode,
    managed_scope: scope,
    sync_state: sync,
    verification_scope: verification,
    approval_required: approval,
    headline: headlineFor({ mode, scope, sync, verification, approval, liveSecretMissing: row.state === 'missing', checked }),
    qualifier: row.qualifier ?? (mode === 'legacy_inline' ? Q_LEGACY_INLINE : mode === 'self_managed' ? Q_SELF_MANAGED : undefined),
    health: row.health ?? 'connected',
  }
}

/**
 * canonicalReconciliationSync fills in the detail endpoint's `headline` (and
 * `qualifier`) the same way, for suites whose reconciliation fixtures predate
 * B5. Same rule: anything the fixture already states wins.
 */
export function canonicalReconciliationSync<
  T extends {
    state: string
    verification_scope: string
    approval_required: boolean
    checked_at?: string
    headline?: string
    qualifier?: string
  },
>(sync: T, mode: Mode = 'sharko_managed', liveSecretMissing = false): T {
  if (sync.headline !== undefined) return sync
  const scope = managedScopeFor(mode)
  return {
    ...sync,
    headline: headlineFor({
      mode,
      scope,
      sync: sync.state as NonNullable<ConnectionSecretRow['sync_state']>,
      verification: sync.verification_scope as NonNullable<ConnectionSecretRow['verification_scope']>,
      approval: sync.approval_required,
      liveSecretMissing,
      checked: Boolean(sync.checked_at),
    }),
    qualifier: sync.qualifier ?? (mode === 'legacy_inline' ? Q_LEGACY_INLINE : mode === 'self_managed' ? Q_SELF_MANAGED : undefined),
  }
}

/**
 * withCanonicalConnectionRows wraps a whole managed-secrets response. Wire it
 * into the api mock so every fixture in a suite gets server-shaped connection
 * rows without touching each one.
 */
export function withCanonicalConnectionRows(res: ManagedSecretsResponse): ManagedSecretsResponse {
  if (!res || !Array.isArray(res.cluster_connection_secrets)) return res
  return { ...res, cluster_connection_secrets: res.cluster_connection_secrets.map(canonicalConnectionRow) }
}

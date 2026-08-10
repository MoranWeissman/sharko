// SecretDetailPage — SSF-9 (Secret Sync finish pass, stories 9+10): the
// Secret Sync drawer becomes a full page. PM's post-SSF-8 correction: a
// side panel that holds a COMPLETE task (state, actions, a two-column
// comparison, a YAML view) stays crowded even at 640px — it needs the
// whole screen, not a slice of one, with a clear way back.
//
// Route: /secret-sync/:rowKey — rowKey is the existing UnifiedRow.key,
// percent-encoded (row keys can contain "/"). Direct load, browser
// refresh, and a shared link must all work: this page fetches the SAME
// managed-secrets data the list page reads (useManagedSecretsData,
// exported from ManagedSecrets.tsx so both pages share one fetch/poll
// implementation) and finds its own row in that same set — no new
// endpoint, no state handed down from the list, no server change. A
// missing/unknown key (a stale link, a secret that's gone) renders a
// plain "this secret isn't in the current list" state with a way back —
// never a crash.
//
// `?row=<key>` compatibility: ManagedSecrets.tsx redirects any hit on
// that param, on the LIST route, straight to this page — this file never
// parses that param itself.
//
// State on the way back: ManagedSecrets.tsx hands its own query string to
// this page as router `location.state.listSearch` when it navigates here
// (openRowDetail) — the header's "Back to Secret Sync" link uses it so
// the list's search/filters/grouping/view/sort all come back exactly as
// they were. Browser Back needs no help from this file — it's plain
// history. Expanded groups and scroll position aren't part of the URL;
// ManagedSecrets.tsx saves and restores those itself, in sessionStorage,
// keyed by that same query string.
//
// SSF-11 (release correction): the route/data flow above was accepted as
// shipped — this story only fixed the LAYOUT. The container used to be
// `mx-auto max-w-3xl` (768px, an article-sized column) on all three
// states below, which sat the whole page in a narrow strip in the middle
// of an otherwise-empty workspace. It's `mx-auto max-w-screen-2xl` now —
// the same 1536px rule Dashboard.tsx already uses — and the actual title
// moved into SecretDetailContent so it can share a header row with the
// Check now / Sync actions instead of floating alone above them.

import { useMemo, useState } from 'react'
import { Link, useLocation, useNavigate, useParams } from 'react-router-dom'
import { ArrowLeft } from 'lucide-react'
import { deleteOrphanedSecret, resyncClusterLabels, syncAddonValuesSecret } from '@/services/api'
import { LoadingState } from '@/components/LoadingState'
import { EmptyState } from '@/components/EmptyState'
import { ConfirmationModal } from '@/components/ConfirmationModal'
import { showToast } from '@/components/ToastNotification'
import {
  buildUnifiedRows,
  deleteConfirmDescription,
  SecretDetailContent,
  syncConfirmButtonText,
  syncConfirmDescription,
  useManagedSecretsData,
  type UnifiedRow,
} from './ManagedSecrets'

export function SecretDetailPage() {
  // Secrets-area rename (SN-4): this page is mounted on three route
  // shapes, and works out which one it's on from the params alone:
  //
  //   /secrets/connections/:cluster       → one cluster connection Secret
  //   /secrets/addons/:cluster/:addon     → one addon Secret (the :addon
  //     segment is the addon name, or "namespace/name" for a leftover
  //     Secret that has no addon of its own — see the row lookup below)
  //   /secret-sync/:rowKey                → the pre-split shape; the app
  //     itself redirects it away (LegacySecretRedirect.tsx), but the
  //     existing test suites still mount it directly, and keeping it
  //     costs one branch.
  //
  // react-router-dom decodes dynamic segments already (same convention
  // every other detail page in this app relies on — see ClusterDetail.tsx /
  // AddonDetail.tsx's own useParams() usage — none of them
  // decodeURIComponent a second time), so an encoded "%2F" in the leftover
  // form arrives here as a plain "/".
  const { rowKey, cluster, addon } = useParams<{ rowKey?: string; cluster?: string; addon?: string }>()
  const area: 'connections' | 'addons' | null =
    rowKey !== undefined ? null : addon !== undefined ? 'addons' : 'connections'
  const navigate = useNavigate()
  const location = useLocation()
  const listSearch = (location.state as { listSearch?: string } | null)?.listSearch ?? ''
  // Back goes to the matching inventory, keeping the saved list query
  // string so the list comes back exactly as it was left.
  const backBase = area ? `/secrets/${area}` : '/secret-sync'
  const backHref = `${backBase}${listSearch ? `?${listSearch}` : ''}`
  const backLabel =
    area === 'connections'
      ? 'Back to Cluster connections'
      : area === 'addons'
        ? 'Back to Addon secrets'
        : 'Back to Secrets'

  const { data, loading, load } = useManagedSecretsData()
  const [syncTarget, setSyncTarget] = useState<UnifiedRow | null>(null)
  const [syncing, setSyncing] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<UnifiedRow | null>(null)
  const [deleting, setDeleting] = useState(false)

  const valuesSourceLabel = data?.addon_values_secret_source || 'secrets store'
  const unifiedRows = useMemo(
    () =>
      buildUnifiedRows(
        data?.cluster_connection_secrets ?? [],
        data?.addon_values_secrets ?? [],
        data?.orphaned_secrets ?? [],
        valuesSourceLabel,
      ),
    [data, valuesSourceLabel],
  )
  // SN-4: the row lookup, split by route instead of by row key.
  //   - legacy: exact key match, as before.
  //   - connections: the row whose key is `connection-<cluster>`.
  //   - addons: `values-<cluster>-<addon>` first; when there is no such
  //     row, a leftover ("orphaned") Secret on the same cluster whose
  //     `namespace/name` equals the decoded :addon segment — that keeps
  //     the URL shape without inventing a new identifier.
  const row = useMemo(() => {
    if (rowKey !== undefined) return unifiedRows.find((r) => r.key === rowKey) ?? null
    const c = cluster ?? ''
    if (area === 'connections') return unifiedRows.find((r) => r.key === `connection-${c}`) ?? null
    const a = addon ?? ''
    return (
      unifiedRows.find((r) => r.key === `values-${c}-${a}`) ??
      unifiedRows.find(
        (r) =>
          r.key.startsWith('orphaned-') &&
          r.cluster === c &&
          `${r.secretNamespace ?? ''}/${r.secretName ?? ''}` === a,
      ) ??
      null
    )
  }, [unifiedRows, rowKey, cluster, addon, area])

  const handleConfirmSync = async () => {
    if (!syncTarget) return
    setSyncing(true)
    try {
      if (syncTarget.kind === 'connection') {
        const result = await resyncClusterLabels(syncTarget.cluster)
        showToast(result.message, 'success')
      } else {
        const result = await syncAddonValuesSecret(syncTarget.cluster, syncTarget.addon!)
        showToast(result.message, 'success')
      }
      setSyncTarget(null)
      load()
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Sync failed', 'error')
    } finally {
      setSyncing(false)
    }
  }

  // leftover-secrets S1.2's Delete, now reachable from the page (SSF-9:
  // "Delete-orphaned flow must still work from the page"). There is no
  // secret left to show once this succeeds, so it navigates back to the
  // list — the same list view the reader came from, via backHref — rather
  // than leaving the page open on a row that no longer exists.
  const handleConfirmDelete = async () => {
    if (!deleteTarget) return
    setDeleting(true)
    try {
      const result = await deleteOrphanedSecret(deleteTarget.cluster, deleteTarget.secretNamespace ?? '', deleteTarget.secretName ?? '')
      showToast(result.message || 'Secret deleted.', 'success')
      setDeleteTarget(null)
      navigate(backHref)
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to delete secret', 'error')
    } finally {
      setDeleting(false)
    }
  }

  const backLink = (
    <Link
      to={backHref}
      data-testid="secret-detail-back"
      className="inline-flex items-center gap-1.5 text-sm font-medium text-teal-700 hover:underline dark:text-teal-400"
    >
      <ArrowLeft className="h-4 w-4" aria-hidden="true" />
      {backLabel}
    </Link>
  )

  if (loading) {
    return (
      <div className="mx-auto max-w-screen-2xl space-y-4" data-testid="secret-detail-container">
        {backLink}
        <LoadingState message="Loading secret..." />
      </div>
    )
  }

  if (!row) {
    return (
      <div className="mx-auto max-w-screen-2xl space-y-4" data-testid="secret-detail-container">
        {backLink}
        <EmptyState
          title="This secret isn't in the current list"
          description="It may have been removed, or the link is out of date."
          action={
            <button
              type="button"
              onClick={() => navigate(backHref)}
              data-testid="secret-detail-not-found-back"
              className="inline-flex items-center gap-2 rounded-md bg-[#0a2a4a] px-4 py-2 text-sm font-semibold text-white hover:bg-[#0d3558] dark:bg-blue-700 dark:hover:bg-blue-600"
            >
              <ArrowLeft className="h-4 w-4" aria-hidden="true" />
              {backLabel}
            </button>
          }
        />
      </div>
    )
  }

  return (
    <div className="mx-auto max-w-screen-2xl space-y-4" data-testid="secret-detail-container">
      {backLink}

      {/* SSF-11: the title moved into SecretDetailContent itself, in the
          same header row as its Check now / Sync actions — see
          secretTitleFor in ManagedSecrets.tsx. */}
      <SecretDetailContent
        row={row}
        onRequestSync={(r) => setSyncTarget(r)}
        onRequestDelete={(r) => setDeleteTarget(r)}
        onChanged={load}
      />

      <ConfirmationModal
        open={syncTarget !== null}
        onClose={() => setSyncTarget(null)}
        onConfirm={handleConfirmSync}
        title={
          // HL-1: the connection confirm carries the action's real name —
          // same per-kind treatment as the list page's own confirm.
          syncTarget?.kind === 'connection'
            ? `Re-apply addon labels on "${syncTarget.cluster}"?`
            : syncTarget?.kind === 'values'
              ? `Sync secret for cluster "${syncTarget.cluster}", addon "${syncTarget.addon}"?`
              : 'Sync?'
        }
        description={syncConfirmDescription(syncTarget)}
        confirmText={syncConfirmButtonText(syncTarget?.kind)}
        loading={syncing}
      />

      {/* leftover-secrets S1.2 — the orphaned-row Delete confirm, same
          pattern the list page's own row-menu Delete uses: a page-level
          target state, a destructive ConfirmationModal, and cancel does
          nothing at all — no request is made unless this confirms. */}
      <ConfirmationModal
        open={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        onConfirm={handleConfirmDelete}
        title={deleteTarget ? `Delete secret "${deleteTarget.secretNamespace ?? ''}/${deleteTarget.secretName ?? ''}"?` : 'Delete?'}
        description={deleteConfirmDescription(deleteTarget)}
        confirmText="Delete"
        destructive
        loading={deleting}
      />
    </div>
  )
}

export default SecretDetailPage

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { ValuesEditor } from '@/components/ValuesEditor'
import type { DryRunResult, ValuesEditResult } from '@/services/models'

/*
 * V3-TX-A3 — Preview on every PR-opening operation.
 *
 * ValuesEditor covers three PR-opening surfaces, each getting its own
 * "Preview changes" affordance that calls a dry-run and renders the result
 * via the shared DryRunPreview WITHOUT opening a PR:
 *   - Surface 9a: plain Save (onPreviewSubmit → setAddonValues/setClusterAddonValues dry-run)
 *   - Surface 9b: Refresh-from-upstream (onPreviewRefreshFromUpstream)
 *   - Surface 10: legacy-wrap "Migrate this file" (onPreviewMigrateLegacyWrap)
 *
 * Each test proves: click Preview → dry-run callback fired → files/deletions
 * render → the real submit callback was NOT called (separate confirm required).
 */

// showToast is a side-effect we don't care about here.
vi.mock('@/components/ToastNotification', () => ({
  showToast: vi.fn(),
}))

describe('ValuesEditor — Preview changes (V3-TX-A3)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('Surface 9a — plain Save preview calls onPreviewSubmit(dry-run) with the draft and renders it without submitting', async () => {
    const onSubmit = vi.fn(async (): Promise<ValuesEditResult> => ({}))
    const onPreviewSubmit = vi.fn(
      async (_newYAML: string): Promise<DryRunResult> => ({
        pr_title: 'Update ingress-nginx values',
        files_to_write: [
          { path: 'configuration/addons-global-values/ingress-nginx.yaml', action: 'update' },
        ],
      }),
    )

    render(
      <ValuesEditor
        title="Global Values — ingress-nginx"
        initialYAML="replicaCount: 1"
        onSubmit={onSubmit}
        onPreviewSubmit={onPreviewSubmit}
      />,
    )

    // Make the buffer dirty so Preview/Submit enable.
    const textarea = screen.getByRole('textbox')
    fireEvent.change(textarea, { target: { value: 'replicaCount: 3' } })

    const previewBtn = screen.getByRole('button', { name: /preview changes/i })
    fireEvent.click(previewBtn)

    await waitFor(() =>
      expect(onPreviewSubmit).toHaveBeenCalledWith('replicaCount: 3'),
    )
    // Dry-run rendered via the shared DryRunPreview.
    await waitFor(() =>
      expect(screen.getByText('Update ingress-nginx values')).toBeInTheDocument(),
    )
    expect(
      screen.getByText('configuration/addons-global-values/ingress-nginx.yaml'),
    ).toBeInTheDocument()
    // The real submit is a SEPARATE action — preview must not open the PR.
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('Surface 9b — Refresh preview calls onPreviewRefreshFromUpstream and renders it without refreshing', async () => {
    const onSubmit = vi.fn(async (): Promise<ValuesEditResult> => ({}))
    const onRefreshFromUpstream = vi.fn(async (): Promise<ValuesEditResult> => ({}))
    const onPreviewRefreshFromUpstream = vi.fn(
      async (): Promise<DryRunResult> => ({
        pr_title: 'Refresh ingress-nginx values from upstream',
        files_to_write: [
          { path: 'configuration/addons-global-values/ingress-nginx.yaml', action: 'update' },
        ],
      }),
    )

    render(
      <ValuesEditor
        title="Global Values — ingress-nginx"
        initialYAML="replicaCount: 1"
        onSubmit={onSubmit}
        onRefreshFromUpstream={onRefreshFromUpstream}
        onPreviewRefreshFromUpstream={onPreviewRefreshFromUpstream}
        versionMismatch={{ catalogVersion: '4.8.0', valuesVersion: '4.7.0' }}
      />,
    )

    // The version-mismatch banner's Preview button.
    const previewBtn = await screen.findByRole('button', { name: /preview changes/i })
    fireEvent.click(previewBtn)

    await waitFor(() => expect(onPreviewRefreshFromUpstream).toHaveBeenCalled())
    await waitFor(() =>
      expect(
        screen.getByText('Refresh ingress-nginx values from upstream'),
      ).toBeInTheDocument(),
    )
    // Preview must not fire the real refresh.
    expect(onRefreshFromUpstream).not.toHaveBeenCalled()
  })

  it('Surface 10 — Migrate preview calls onPreviewMigrateLegacyWrap and renders the deletion/rewrite without migrating', async () => {
    const onSubmit = vi.fn(async (): Promise<ValuesEditResult> => ({}))
    const onMigrateLegacyWrap = vi.fn(async (): Promise<void> => {})
    const onPreviewMigrateLegacyWrap = vi.fn(
      async (): Promise<DryRunResult> => ({
        pr_title: 'Unwrap legacy values for ingress-nginx',
        files_to_write: [
          { path: 'configuration/addons-global-values/ingress-nginx.yaml', action: 'update' },
          { path: 'configuration/addons-global-values/ingress-nginx.legacy.yaml', action: 'delete' },
        ],
      }),
    )

    render(
      <ValuesEditor
        title="Global Values — ingress-nginx"
        initialYAML="ingress-nginx:\n  replicaCount: 1"
        onSubmit={onSubmit}
        legacyWrapDetected
        onMigrateLegacyWrap={onMigrateLegacyWrap}
        onPreviewMigrateLegacyWrap={onPreviewMigrateLegacyWrap}
      />,
    )

    // The legacy-wrap banner's Preview button.
    const previewBtn = await screen.findByRole('button', { name: /preview changes/i })
    fireEvent.click(previewBtn)

    await waitFor(() => expect(onPreviewMigrateLegacyWrap).toHaveBeenCalled())
    await waitFor(() =>
      expect(screen.getByText('Unwrap legacy values for ingress-nginx')).toBeInTheDocument(),
    )
    // Deletion is rendered (red `-` marker path present).
    expect(
      screen.getByText('configuration/addons-global-values/ingress-nginx.legacy.yaml'),
    ).toBeInTheDocument()
    // Preview must not fire the real migration.
    expect(onMigrateLegacyWrap).not.toHaveBeenCalled()
  })

  it('a surfaced dry-run error does not block the user (courtesy, not a gate)', async () => {
    const onSubmit = vi.fn(async (): Promise<ValuesEditResult> => ({}))
    const onPreviewSubmit = vi.fn(async (_newYAML: string): Promise<DryRunResult> => {
      throw new Error('backend exploded')
    })

    render(
      <ValuesEditor
        title="Global Values — ingress-nginx"
        initialYAML="replicaCount: 1"
        onSubmit={onSubmit}
        onPreviewSubmit={onPreviewSubmit}
      />,
    )

    const textarea = screen.getByRole('textbox')
    fireEvent.change(textarea, { target: { value: 'replicaCount: 9' } })
    fireEvent.click(screen.getByRole('button', { name: /preview changes/i }))

    await waitFor(() => expect(screen.getByText('backend exploded')).toBeInTheDocument())
    // Submit is still available — the preview is a courtesy.
    expect(screen.getByRole('button', { name: /submit changes/i })).not.toBeDisabled()
  })
})

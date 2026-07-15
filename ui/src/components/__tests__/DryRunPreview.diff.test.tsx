import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { DryRunPreview } from '@/components/AddAddonFlow'
import type { DryRunResult } from '@/services/models'

/*
 * V3-D4 — Per-action rendering in DryRunPreview.
 *
 * create → 'new file' label, no content dump.
 * delete → 'removed' label, no content dump.
 * update → actual line-by-line diff shown inline and visible by default
 *          (green added / red removed, <redacted> verbatim).
 * Dumping a whole new/removed file as +/- lines was noise, and the
 * collapsed-by-default chevron made the feature look like it did nothing.
 */

describe('DryRunPreview — per-action rendering (V3-D4)', () => {
  it('an UPDATE file shows its diff inline and visible immediately (no click needed)', () => {
    const result: DryRunResult = {
      pr_title: 'Update ingress-nginx config',
      files_to_write: [
        {
          path: 'configuration/addons-global-values/ingress-nginx.yaml',
          action: 'update',
          diff: ' database:\n-  password: <redacted>\n+  password: <redacted>\n+  host: db.example.com',
        },
      ],
    }

    render(<DryRunPreview result={result} />)

    // The file row is present with the update marker and path.
    expect(
      screen.getByText('configuration/addons-global-values/ingress-nginx.yaml'),
    ).toBeInTheDocument()
    // The marker is amber for 'update'.
    expect(screen.getByText('~')).toBeInTheDocument()

    // The diff body is VISIBLE immediately (no expand control, no click needed).
    expect(screen.getByText(/\+\s+host: db\.example\.com/)).toBeInTheDocument()
    // The removed line (password: <redacted>) is also present.
    expect(screen.getByText(/-\s+password: <redacted>/)).toBeInTheDocument()
    // The added password line.
    expect(screen.getByText(/\+\s+password: <redacted>/)).toBeInTheDocument()

    // No expand button exists.
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })

  it('added lines get the green class, removed lines the red class', () => {
    const result: DryRunResult = {
      pr_title: 'Add feature flag',
      files_to_write: [
        {
          path: 'config.yaml',
          action: 'update',
          diff: '+featureX: true\n-legacyFlag: false',
        },
      ],
    }

    const { container } = render(<DryRunPreview result={result} />)

    // The diff is visible immediately (no expand step).
    // The added line should have the green class.
    const addedLine = container.querySelector('.text-green-600')
    expect(addedLine).toBeInTheDocument()
    expect(addedLine?.textContent).toContain('+featureX: true')

    // The removed line should have the red class.
    const removedLine = container.querySelector('.text-red-600')
    expect(removedLine).toBeInTheDocument()
    expect(removedLine?.textContent).toContain('-legacyFlag: false')
  })

  it('<redacted> in a diff renders verbatim (visible immediately)', () => {
    const result: DryRunResult = {
      pr_title: 'Rotate secret',
      files_to_write: [
        {
          path: 'secrets.yaml',
          action: 'update',
          diff: '-  apiKey: <redacted>\n+  apiKey: <redacted>',
        },
      ],
    }

    render(<DryRunPreview result={result} />)

    // <redacted> appears verbatim in both lines (removed and added), visible immediately.
    const redactedElements = screen.getAllByText(/<redacted>/)
    expect(redactedElements.length).toBeGreaterThanOrEqual(2)
  })

  it('a CREATE entry shows the "new file" label and no content dump', () => {
    const result: DryRunResult = {
      pr_title: 'Create new file',
      files_to_write: [
        {
          path: 'new-addon.yaml',
          action: 'create',
          diff: 'name: test\nversion: 1.0.0\n# ... 50 more lines of boilerplate',
        },
      ],
    }

    render(<DryRunPreview result={result} />)

    // The file row is present with the create marker, path, and "new file" label.
    expect(screen.getByText('new-addon.yaml')).toBeInTheDocument()
    expect(screen.getByText('+')).toBeInTheDocument()
    expect(screen.getByText('(new file)')).toBeInTheDocument()

    // The diff content is NOT rendered (no full-content dump).
    expect(screen.queryByText(/name: test/)).not.toBeInTheDocument()
    expect(screen.queryByText(/version: 1\.0\.0/)).not.toBeInTheDocument()

    // No expand control is rendered (no button).
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })

  it('a DELETE entry shows the "removed" label and no content dump', () => {
    const result: DryRunResult = {
      pr_title: 'Remove deprecated addon',
      files_to_write: [
        {
          path: 'old-addon.yaml',
          action: 'delete',
          diff: '- name: old-addon\n- version: 0.5.0\n- # ... entire removed file',
        },
      ],
    }

    render(<DryRunPreview result={result} />)

    // The file row is present with the delete marker, path, and "removed" label.
    expect(screen.getByText('old-addon.yaml')).toBeInTheDocument()
    expect(screen.getByText('-')).toBeInTheDocument()
    expect(screen.getByText('(removed)')).toBeInTheDocument()

    // The diff content is NOT rendered (no full-content dump).
    expect(screen.queryByText(/name: old-addon/)).not.toBeInTheDocument()
    expect(screen.queryByText(/version: 0\.5\.0/)).not.toBeInTheDocument()

    // No expand control is rendered (no button).
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })

  it('an update WITHOUT a diff renders a plain row (marker + path, no body, no crash)', () => {
    const result: DryRunResult = {
      pr_title: 'Update config',
      files_to_write: [
        {
          path: 'app.yaml',
          action: 'update',
          // No `diff` field — the backend may omit it for non-content ops.
        },
      ],
    }

    render(<DryRunPreview result={result} />)

    // The file row is present with the update marker and path.
    expect(screen.getByText('app.yaml')).toBeInTheDocument()
    expect(screen.getByText('~')).toBeInTheDocument()

    // No diff body is rendered (the file had no diff field).
    // The DOM should not crash or show an error; the row is just the marker + path.
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })

  it('multi-file op: an update + a delete renders the update diff inline AND the delete label (no deleted content)', () => {
    const result: DryRunResult = {
      pr_title: 'Update one, remove one',
      files_to_write: [
        {
          path: 'updated.yaml',
          action: 'update',
          diff: '+newFeature: enabled',
        },
        {
          path: 'removed.yaml',
          action: 'delete',
          diff: '- # Entire removed file content here...',
        },
      ],
    }

    render(<DryRunPreview result={result} />)

    // Both files are present.
    expect(screen.getByText('updated.yaml')).toBeInTheDocument()
    expect(screen.getByText('removed.yaml')).toBeInTheDocument()

    // The update's diff is visible immediately.
    expect(screen.getByText(/\+newFeature: enabled/)).toBeInTheDocument()

    // The delete label is visible.
    expect(screen.getByText('(removed)')).toBeInTheDocument()

    // The deleted file's content is NOT rendered.
    expect(screen.queryByText(/Entire removed file content here/)).not.toBeInTheDocument()
  })
})

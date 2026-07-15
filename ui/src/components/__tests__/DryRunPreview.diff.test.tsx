import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { DryRunPreview } from '@/components/AddAddonFlow'
import type { DryRunResult } from '@/services/models'

/*
 * V3-D2+D3 — Per-file diff rendering in DryRunPreview.
 *
 * Each file with a `diff` field becomes expandable: the header row shows
 * the action marker + path (unchanged), and clicking it expands a line-by-line
 * diff below with added lines green, removed lines red. Files without a diff
 * render exactly as before (no regression). Collapsed by default. Secret
 * values arrive as `<redacted>` from the server and render verbatim.
 */

describe('DryRunPreview — per-file diff rendering (V3-D2+D3)', () => {
  it('a file WITH a diff renders an expand control; after expanding, added/removed lines appear with correct colors', () => {
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

    // The diff body is NOT visible by default (collapsed).
    expect(screen.queryByText('host: db.example.com')).not.toBeInTheDocument()

    // Click the expand control (the button containing the path).
    const expandBtn = screen.getByRole('button', { expanded: false })
    fireEvent.click(expandBtn)

    // The diff body now renders, with the added line visible.
    expect(screen.getByText(/\+\s+host: db\.example\.com/)).toBeInTheDocument()
    // The removed line (password: <redacted>) is also present.
    expect(screen.getByText(/-\s+password: <redacted>/)).toBeInTheDocument()
    // The added password line.
    expect(screen.getByText(/\+\s+password: <redacted>/)).toBeInTheDocument()

    // The expand button is now marked expanded.
    expect(screen.getByRole('button', { expanded: true })).toBeInTheDocument()
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

    // Expand the diff.
    fireEvent.click(screen.getByRole('button', { expanded: false }))

    // The added line should have the green class.
    const addedLine = container.querySelector('.text-green-600')
    expect(addedLine).toBeInTheDocument()
    expect(addedLine?.textContent).toContain('+featureX: true')

    // The removed line should have the red class.
    const removedLine = container.querySelector('.text-red-600')
    expect(removedLine).toBeInTheDocument()
    expect(removedLine?.textContent).toContain('-legacyFlag: false')
  })

  it('<redacted> in a diff renders verbatim (present in the DOM after expand)', () => {
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

    // Before expand, <redacted> is not visible.
    expect(screen.queryByText(/<redacted>/)).not.toBeInTheDocument()

    // Expand the diff.
    fireEvent.click(screen.getByRole('button', { expanded: false }))

    // <redacted> appears verbatim in both lines (removed and added).
    const redactedElements = screen.getAllByText(/<redacted>/)
    expect(redactedElements.length).toBeGreaterThanOrEqual(2)
  })

  it('a file entry WITHOUT a diff renders the plain one-line row and NO expand control (no regression)', () => {
    const result: DryRunResult = {
      pr_title: 'Create new file',
      files_to_write: [
        {
          path: 'new-addon.yaml',
          action: 'create',
          // No `diff` field — the backend may omit it for non-content ops.
        },
      ],
    }

    render(<DryRunPreview result={result} />)

    // The file row is present with the create marker and path.
    expect(screen.getByText('new-addon.yaml')).toBeInTheDocument()
    expect(screen.getByText('+')).toBeInTheDocument()

    // No expand control is rendered (no button).
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })

  it('collapsed by default: the diff body is not visible until expanded', () => {
    const result: DryRunResult = {
      pr_title: 'Update config',
      files_to_write: [
        {
          path: 'app.yaml',
          action: 'update',
          diff: '+newKey: value\n oldKey: oldValue',
        },
      ],
    }

    render(<DryRunPreview result={result} />)

    // The file row is present.
    expect(screen.getByText('app.yaml')).toBeInTheDocument()
    // The diff body is NOT visible (collapsed by default).
    expect(screen.queryByText('newKey: value')).not.toBeInTheDocument()

    // Expand the diff.
    fireEvent.click(screen.getByRole('button', { expanded: false }))

    // The diff body now renders.
    expect(screen.getByText(/\+newKey: value/)).toBeInTheDocument()
  })

  it('multiple files with diffs each expand independently', () => {
    const result: DryRunResult = {
      pr_title: 'Multi-file update',
      files_to_write: [
        {
          path: 'file1.yaml',
          action: 'update',
          diff: '+line1',
        },
        {
          path: 'file2.yaml',
          action: 'update',
          diff: '+line2',
        },
      ],
    }

    render(<DryRunPreview result={result} />)

    // Both files are present.
    expect(screen.getByText('file1.yaml')).toBeInTheDocument()
    expect(screen.getByText('file2.yaml')).toBeInTheDocument()

    // Neither diff is visible by default.
    expect(screen.queryByText('line1')).not.toBeInTheDocument()
    expect(screen.queryByText('line2')).not.toBeInTheDocument()

    // Expand file1 only.
    const buttons = screen.getAllByRole('button', { expanded: false })
    fireEvent.click(buttons[0])

    // file1's diff is visible.
    expect(screen.getByText(/\+line1/)).toBeInTheDocument()
    // file2's diff is NOT visible (still collapsed).
    expect(screen.queryByText('line2')).not.toBeInTheDocument()
  })
})

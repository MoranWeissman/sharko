// StatusMark — P1-A adds the fifth state. These pins exist because the
// state vocabulary is what the whole Secret Sync page reads off: the word,
// the dot colour, the row's edge strip and the sort order all come from one
// table, and a change to any of them is a change to what Sharko claims
// about a secret.

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { StatusMark, statusLabel, statusSortRank, statusStripClassName, toResourceStatus } from '../StatusMark'

describe('StatusMark', () => {
  it('calls the fifth state "Foreign"', () => {
    expect(statusLabel('foreign')).toBe('Foreign')
  })

  it('sorts foreign after out-of-sync and missing, and ahead of never-checked', () => {
    expect(statusSortRank('out_of_sync')).toBeLessThan(statusSortRank('missing'))
    expect(statusSortRank('missing')).toBeLessThan(statusSortRank('foreign'))
    expect(statusSortRank('foreign')).toBeLessThan(statusSortRank('unknown'))
    expect(statusSortRank('unknown')).toBeLessThan(statusSortRank('in_sync'))
  })

  it('paints foreign neutral — never red, never amber', () => {
    render(<StatusMark status="foreign" />)
    const dot = screen.getByTestId('status-dot')
    expect(dot).toHaveAttribute('data-status', 'foreign')
    expect(dot.className).not.toMatch(/red|amber/)
    expect(dot.className).toMatch(/slate/)
    // Filled, not hollow: Sharko looked and knows exactly what is there.
    expect(dot).toHaveAttribute('data-hollow', 'false')
  })

  it('gives foreign a row strip in the same neutral colour as its dot', () => {
    const strip = statusStripClassName('foreign')
    expect(strip).toMatch(/border-slate-500/)
    expect(strip).toMatch(/dark:border-slate-400/)
  })

  it('keeps the word in plain dark ink for every state, colour only on the dot', () => {
    for (const state of ['in_sync', 'out_of_sync', 'missing', 'foreign', 'unknown']) {
      const { unmount } = render(<StatusMark status={state} />)
      expect(screen.getByTestId('status-mark').className).toMatch(/text-\[#13293f\]/)
      unmount()
    }
  })

  it('still reads an unrecognised state as never-checked rather than crashing', () => {
    expect(toResourceStatus('something-new')).toBe('unknown')
  })
})

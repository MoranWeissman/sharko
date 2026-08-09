// StatusMark — P1-A adds the fifth state. These pins exist because the
// state vocabulary is what the whole Secret Sync page reads off: the word,
// the dot colour, the row's edge strip and the sort order all come from one
// table, and a change to any of them is a change to what Sharko claims
// about a secret.

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { StatusMark, statusLabel, statusSortRank, statusStripClassName, toResourceStatus } from '../StatusMark'

describe('StatusMark', () => {
  it('calls the fifth state "Managed elsewhere" (SSF-8 word pass — was "Foreign")', () => {
    expect(statusLabel('foreign')).toBe('Managed elsewhere')
  })

  it('sorts missing first, then out-of-sync, then orphaned, then foreign, then never-checked, in-sync last (G3, leftover-secrets S1.2)', () => {
    expect(statusSortRank('missing')).toBeLessThan(statusSortRank('out_of_sync'))
    expect(statusSortRank('out_of_sync')).toBeLessThan(statusSortRank('orphaned'))
    expect(statusSortRank('orphaned')).toBeLessThan(statusSortRank('foreign'))
    expect(statusSortRank('foreign')).toBeLessThan(statusSortRank('unknown'))
    expect(statusSortRank('unknown')).toBeLessThan(statusSortRank('in_sync'))
  })

  it('calls the orphaned state "Not in config" (SSF-8 word pass — was "Orphaned") and gives it a violet dot, distinct from every other fill', () => {
    expect(statusLabel('orphaned')).toBe('Not in config')
    render(<StatusMark status="orphaned" />)
    const dot = screen.getByTestId('status-dot')
    expect(dot).toHaveAttribute('data-status', 'orphaned')
    expect(dot.className).toMatch(/violet/)
    expect(dot.className).not.toMatch(/red|amber|slate|green/)
    // Filled, not hollow — Sharko wrote this once and knows exactly what it is.
    expect(dot).toHaveAttribute('data-hollow', 'false')
  })

  it('gives orphaned a row strip in the same violet as its dot', () => {
    const strip = statusStripClassName('orphaned')
    expect(strip).toMatch(/border-violet-600/)
    expect(strip).toMatch(/dark:border-violet-500/)
  })

  it('sorts a FAILED check ahead of a genuinely never-checked row — same "unknown" word, different rank (G3)', () => {
    expect(statusSortRank('unknown', true)).toBeLessThan(statusSortRank('unknown', false))
    // Still behind foreign, still ahead of in_sync — a failed check doesn't
    // jump the whole queue, it only outranks the "nobody's looked" case.
    expect(statusSortRank('foreign')).toBeLessThan(statusSortRank('unknown', true))
    expect(statusSortRank('unknown', true)).toBeLessThan(statusSortRank('in_sync'))
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
    for (const state of ['in_sync', 'out_of_sync', 'missing', 'orphaned', 'foreign', 'unknown']) {
      const { unmount } = render(<StatusMark status={state} />)
      expect(screen.getByTestId('status-mark').className).toMatch(/text-\[#0a3a5a\]/)
      unmount()
    }
  })

  it('still reads an unrecognised state as never-checked rather than crashing', () => {
    expect(toResourceStatus('something-new')).toBe('unknown')
  })
})

import { describe, expect, it } from 'vitest'
import { csvCell, csvRow, toCSV } from '@/lib/csv'

/*
 * Walk 6: in the audit export only Detail and Error were escaped. A failed
 * login with the name  =1+1"<b>walk6x</b>  was kept as Who: the quote ended
 * the cell early (every column after it moved) and the cell started with "=",
 * which a spreadsheet may run as a formula.
 */
describe('csv', () => {
  it('doubles every quote, in any cell', () => {
    expect(csvCell('say "hi"')).toBe('"say ""hi"""')
  })

  it('makes a cell that starts like a formula plain text', () => {
    expect(csvCell('=1+1')).toBe(`"'=1+1"`)
    expect(csvCell('+1')).toBe(`"'+1"`)
    expect(csvCell('-1')).toBe(`"'-1"`)
    expect(csvCell('@SUM(A1)')).toBe(`"'@SUM(A1)"`)
    expect(csvCell('\t=1')).toBe(`"'\t=1"`)
  })

  it('leaves a normal cell alone, apart from the quotes around it', () => {
    expect(csvCell('admin')).toBe('"admin"')
    expect(csvCell('a-b=c')).toBe('"a-b=c"')
    expect(csvCell('')).toBe('""')
    expect(csvCell(undefined)).toBe('""')
    expect(csvCell(42)).toBe('"42"')
  })

  it('keeps the walker\'s bad name in one cell', () => {
    const row = csvRow(['2026-11-26T08:00:00.000Z', '=1+1"<b>walk6x</b>', 'login_failed'])
    expect(row).toBe(`"2026-11-26T08:00:00.000Z","'=1+1""<b>walk6x</b>","login_failed"`)
  })

  it('builds the whole file', () => {
    expect(toCSV(['Who', 'Event'], [['bob', 'login'], ['=x', 'a,b']])).toBe(
      `"Who","Event"\n"bob","login"\n"'=x","a,b"`,
    )
  })
})

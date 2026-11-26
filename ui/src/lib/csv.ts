/**
 * csv — one place that turns values into safe CSV text.
 *
 * Two rules, applied to EVERY cell, whatever column it is in:
 *
 * 1. Quotes. A cell is wrapped in double quotes and every double quote
 *    inside it is doubled. Without that, a quote in the text ends the cell
 *    early and every column after it moves.
 *
 * 2. Formulas. A spreadsheet treats a cell that starts with `=`, `+`, `-` or
 *    `@` (also a tab or a line break) as a formula and may run it. Such a
 *    cell gets a single quote in front, which makes the spreadsheet show it
 *    as plain text.
 *
 * The audit export needs both: the "Who" of a failed login is whatever text
 * somebody typed into the login form, with no login of their own.
 */

const FORMULA_START = /^[=+\-@\t\r\n]/

export function csvCell(value: unknown): string {
  let text = value === null || value === undefined ? '' : String(value)
  if (FORMULA_START.test(text)) {
    text = `'${text}`
  }
  return `"${text.replace(/"/g, '""')}"`
}

export function csvRow(values: unknown[]): string {
  return values.map(csvCell).join(',')
}

export function toCSV(headers: string[], rows: unknown[][]): string {
  return [csvRow(headers), ...rows.map(csvRow)].join('\n')
}

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { AuditViewer } from '@/views/AuditViewer'
import type { AuditEntry } from '@/services/models'

/*
 * Walk 6, through the real page:
 *  - the Who of a failed login with a hostile name broke the CSV export and
 *    could run as a formula;
 *  - a row made with an API key looked like a person's row.
 */
const mockFetchAuditLog = vi.fn()
vi.mock('@/services/api', () => ({
  fetchAuditLog: (...args: unknown[]) => mockFetchAuditLog(...args),
  createAuditStream: () => ({ close: vi.fn(), onmessage: null, onerror: null }),
}))

const entries: AuditEntry[] = [
  {
    id: '1', timestamp: '2026-11-26T09:00:00Z', level: 'warn', event: 'login_failed',
    user: '=1+1"<b>walk6x</b>', action: 'login', resource: 'session', source: 'api',
    result: 'failure', duration_ms: 1,
  },
  {
    id: '2', timestamp: '2026-11-26T09:01:00Z', level: 'info', event: 'token_created',
    user: 'ci-bot', auth_type: 'api_key', action: 'create', resource: 'token:x', source: 'api',
    result: 'success', duration_ms: 3, detail: 'role=viewer',
  },
  {
    id: '3', timestamp: '2026-11-26T09:02:00Z', level: 'info', event: 'token_created',
    user: 'bob', action: 'create', resource: '@cmd', source: 'ui',
    result: 'success', duration_ms: 3,
  },
]

describe('AuditViewer — export and API key rows', () => {
  let exported = ''
  const realBlob = globalThis.Blob

  beforeEach(() => {
    vi.clearAllMocks()
    exported = ''
    mockFetchAuditLog.mockResolvedValue({ entries, count: entries.length })
    // Catch the text the page hands to the download.
    globalThis.Blob = class extends realBlob {
      constructor(parts?: BlobPart[], options?: BlobPropertyBag) {
        super(parts, options)
        exported = (parts ?? []).map(String).join('')
      }
    } as typeof Blob
    URL.createObjectURL = vi.fn(() => 'blob:test')
    URL.revokeObjectURL = vi.fn()
    HTMLAnchorElement.prototype.click = vi.fn()
  })
  afterEach(() => {
    globalThis.Blob = realBlob
  })

  it('marks the row of an API key on screen', async () => {
    render(<MemoryRouter initialEntries={['/audit']}><AuditViewer /></MemoryRouter>)
    await waitFor(() => expect(screen.getByText('ci-bot (API key)')).toBeInTheDocument())
    expect(screen.getByText('bob')).toBeInTheDocument()
    expect(screen.queryByText('bob (API key)')).not.toBeInTheDocument()
  })

  it('marks a key named like Sharko itself as an API key, and leaves real system rows alone', async () => {
    mockFetchAuditLog.mockResolvedValue({
      entries: [
        { id: 'k1', timestamp: '2026-11-26T09:03:00Z', level: 'info', event: 'cluster_registered',
          user: 'sharko', auth_type: 'api_key', action: 'create', resource: 'cluster:prod', source: 'api',
          result: 'success', duration_ms: 3 },
        { id: 'k2', timestamp: '2026-11-26T09:04:00Z', level: 'info', event: 'cluster_registered',
          user: 'system', auth_type: 'api_key', action: 'create', resource: 'cluster:dev', source: 'api',
          result: 'success', duration_ms: 3 },
        { id: 's1', timestamp: '2026-11-26T09:05:00Z', level: 'info', event: 'cluster_secret_reconciled',
          user: 'sharko', action: 'reconcile', resource: 'cluster:stage', source: 'reconciler',
          result: 'success', duration_ms: 3 },
      ] as AuditEntry[],
      count: 3,
    })
    render(<MemoryRouter initialEntries={['/audit']}><AuditViewer /></MemoryRouter>)
    await waitFor(() => expect(screen.getByText('sharko (API key)')).toBeInTheDocument())
    expect(screen.getByText('system (API key)')).toBeInTheDocument()
    // The one real system row keeps the bare name.
    expect(screen.getAllByText('sharko')).toHaveLength(1)
  })

  it('exports every column safely', async () => {
    render(<MemoryRouter initialEntries={['/audit']}><AuditViewer /></MemoryRouter>)
    await waitFor(() => expect(screen.getByText('ci-bot (API key)')).toBeInTheDocument())
    fireEvent.click(screen.getByText('Export CSV'))

    const lines = exported.split('\n')
    expect(lines).toHaveLength(4)
    expect(lines[0]).toBe('"Timestamp","Who","Event","Action","Resource","Result","Source","Detail","Error"')
    // The hostile name stays in ONE cell, with its quote doubled and a
    // single quote in front of the "=".
    expect(lines[1]).toBe(
      `"2026-11-26T09:00:00.000Z","'=1+1""<b>walk6x</b>","login_failed","login","session","failure","api","",""`,
    )
    // The API key row says so in the file too.
    expect(lines[2]).toContain('"ci-bot (API key)"')
    // Any column is made safe, not only Who.
    expect(lines[3]).toContain(`"'@cmd"`)
    // Every line has the same number of cells.
    for (const line of lines) {
      expect(line.split('","')).toHaveLength(9)
    }
  })
})

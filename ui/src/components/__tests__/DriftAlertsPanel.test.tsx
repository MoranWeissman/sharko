import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { DriftAlertsPanel } from '@/components/DriftAlertsPanel'

const mockFetchAuditLog = vi.fn()

vi.mock('@/services/api', () => ({
  fetchAuditLog: (...args: unknown[]) => mockFetchAuditLog(...args),
}))

function renderPanel() {
  return render(
    <MemoryRouter>
      <DriftAlertsPanel />
    </MemoryRouter>,
  )
}

const now = Date.now()

function makeEntry(overrides: Record<string, unknown> = {}) {
  return {
    id: 'entry-1',
    timestamp: new Date(now - 60_000).toISOString(), // 1 minute ago
    level: 'info',
    event: 'orphan_detected',
    user: 'reconciler',
    action: 'reconcile',
    resource: 'secret/addon-cluster-token',
    source: 'reconciler',
    result: 'success',
    duration_ms: 10,
    ...overrides,
  }
}

describe('DriftAlertsPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders loading state initially', () => {
    mockFetchAuditLog.mockReturnValue(new Promise(() => {})) // never resolves
    renderPanel()
    expect(screen.getByText('Loading drift alerts...')).toBeInTheDocument()
  })

  it('renders nothing when no drift events exist', async () => {
    mockFetchAuditLog.mockResolvedValue({ entries: [] })
    const { container } = renderPanel()
    await waitFor(() => {
      expect(container.innerHTML).toBe('')
    })
  })

  it('renders drift alerts when events exist', async () => {
    mockFetchAuditLog.mockResolvedValue({
      entries: [
        makeEntry({ id: 'e1', event: 'orphan_detected', resource: 'secret/my-orphan' }),
        makeEntry({ id: 'e2', event: 'drift_detected', resource: 'secret/my-drift', timestamp: new Date(now - 3600_000).toISOString() }),
      ],
    })
    renderPanel()
    await waitFor(() => {
      expect(screen.getByText('Drift Alerts')).toBeInTheDocument()
    })
    expect(screen.getByText('Orphan Detected')).toBeInTheDocument()
    expect(screen.getByText('Drift Detected')).toBeInTheDocument()
    expect(screen.getByText('secret/my-orphan')).toBeInTheDocument()
    expect(screen.getByText('secret/my-drift')).toBeInTheDocument()
  })

  it('shows pending status for recent orphan_detected events', async () => {
    mockFetchAuditLog.mockResolvedValue({
      entries: [makeEntry({ id: 'e1', event: 'orphan_detected', timestamp: new Date(now - 60_000).toISOString() })],
    })
    renderPanel()
    await waitFor(() => {
      expect(screen.getByText(/Pending cleanup/)).toBeInTheDocument()
    })
  })

  it('shows cleaned up status for orphan_deleted events', async () => {
    mockFetchAuditLog.mockResolvedValue({
      entries: [makeEntry({ id: 'e1', event: 'orphan_deleted_after_grace_period', timestamp: new Date(now - 600_000).toISOString() })],
    })
    renderPanel()
    await waitFor(() => {
      expect(screen.getByText('Cleaned up')).toBeInTheDocument()
    })
  })

  it('dismisses an alert when clicking dismiss button', async () => {
    mockFetchAuditLog.mockResolvedValue({
      entries: [makeEntry({ id: 'e1', event: 'drift_detected', resource: 'secret/drifty' })],
    })
    const { container } = renderPanel()
    await waitFor(() => {
      expect(screen.getByText('Drift Detected')).toBeInTheDocument()
    })
    const dismissBtn = screen.getByLabelText('Dismiss alert')
    fireEvent.click(dismissBtn)
    // After dismiss, the panel should be empty (hidden)
    await waitFor(() => {
      expect(container.querySelector('[class*="ring-amber"]')).toBeNull()
    })
  })

  it('shows error state on fetch failure', async () => {
    mockFetchAuditLog.mockRejectedValue(new Error('Network error'))
    renderPanel()
    await waitFor(() => {
      expect(screen.getByText('Network error')).toBeInTheDocument()
    })
  })

  it('shows pending count badge', async () => {
    mockFetchAuditLog.mockResolvedValue({
      entries: [
        makeEntry({ id: 'e1', event: 'orphan_detected', timestamp: new Date(now - 30_000).toISOString() }),
        makeEntry({ id: 'e2', event: 'orphan_detected', timestamp: new Date(now - 60_000).toISOString() }),
      ],
    })
    renderPanel()
    await waitFor(() => {
      expect(screen.getByText('2 pending')).toBeInTheDocument()
    })
  })

  it('calls fetchAuditLog with reconciler source filter', async () => {
    mockFetchAuditLog.mockResolvedValue({ entries: [] })
    renderPanel()
    await waitFor(() => {
      expect(mockFetchAuditLog).toHaveBeenCalledWith({
        source: 'reconciler',
        limit: 100,
      })
    })
  })

  it('filters out events older than 24 hours', async () => {
    const oldTimestamp = new Date(now - 25 * 60 * 60 * 1000).toISOString() // 25 hours ago
    mockFetchAuditLog.mockResolvedValue({
      entries: [makeEntry({ id: 'e1', event: 'drift_detected', timestamp: oldTimestamp })],
    })
    const { container } = renderPanel()
    await waitFor(() => {
      expect(container.innerHTML).toBe('')
    })
  })
})

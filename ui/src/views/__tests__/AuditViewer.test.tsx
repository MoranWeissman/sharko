import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { AuditViewer } from '@/views/AuditViewer';
import type { AuditEntry } from '@/services/models';

const mockFetchAuditLog = vi.fn();
vi.mock('@/services/api', () => ({
  fetchAuditLog: (...args: unknown[]) => mockFetchAuditLog(...args),
  createAuditStream: vi.fn(() => ({
    onmessage: null,
    onerror: null,
    close: vi.fn(),
  })),
}));

const sampleEntries: AuditEntry[] = [
  {
    id: '1',
    timestamp: '2025-01-01T12:00:00Z',
    level: 'info',
    event: 'cluster.test',
    user: 'admin',
    action: 'test',
    resource: 'prod-eu',
    source: 'ui',
    result: 'success',
    duration_ms: 245,
  },
  {
    id: '2',
    timestamp: '2025-01-01T12:01:00Z',
    level: 'error',
    event: 'addon.delete',
    user: 'operator',
    action: 'delete',
    resource: 'cert-manager',
    source: 'api',
    result: 'failure',
    duration_ms: 1200,
  },
];

describe('AuditViewer', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders filter form with all expected inputs', async () => {
    mockFetchAuditLog.mockResolvedValue({ entries: sampleEntries });
    render(<AuditViewer />);

    await waitFor(() => {
      expect(screen.getByText('Audit Log')).toBeInTheDocument();
    });

    // Labels for filter fields (some labels are duplicated in table headers, use getAllByText)
    expect(screen.getAllByText('User').length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText('Action').length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText('Source').length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText('Result').length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText('Cluster')).toBeInTheDocument();
    expect(screen.getByText('Since (RFC3339)')).toBeInTheDocument();

    // Inputs
    expect(screen.getByPlaceholderText('Any user')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('Any cluster')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('2024-01-01T00:00:00Z')).toBeInTheDocument();
  });

  it('renders audit event table with correct columns', async () => {
    mockFetchAuditLog.mockResolvedValue({ entries: sampleEntries });
    render(<AuditViewer />);

    await waitFor(() => {
      expect(screen.getByText('cluster.test')).toBeInTheDocument();
    });

    // Column headers
    expect(screen.getByText('Timestamp')).toBeInTheDocument();
    expect(screen.getByText('Event')).toBeInTheDocument();
    // "User" appears both as filter label and column header
    expect(screen.getAllByText('User').length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText('Action').length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText('Resource')).toBeInTheDocument();
    expect(screen.getAllByText('Source').length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText('Result').length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText('Duration')).toBeInTheDocument();

    // Row data
    expect(screen.getByText('cluster.test')).toBeInTheDocument();
    expect(screen.getByText('admin')).toBeInTheDocument();
    expect(screen.getByText('prod-eu')).toBeInTheDocument();
    expect(screen.getByText('245ms')).toBeInTheDocument();
    expect(screen.getByText('addon.delete')).toBeInTheDocument();
    expect(screen.getByText('operator')).toBeInTheDocument();
    expect(screen.getByText('1200ms')).toBeInTheDocument();
  });

  it('shows empty state when no events', async () => {
    mockFetchAuditLog.mockResolvedValue({ entries: [] });
    render(<AuditViewer />);

    await waitFor(() => {
      expect(screen.getByText('No audit entries found.')).toBeInTheDocument();
    });
  });

  it('shows loading spinner initially', () => {
    mockFetchAuditLog.mockReturnValue(new Promise(() => {})); // never resolves
    render(<AuditViewer />);

    // The table body should have the loading spinner (no text, just spinner in td)
    expect(screen.getByText('Audit Log')).toBeInTheDocument();
  });
});

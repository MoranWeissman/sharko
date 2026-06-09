import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { AuditViewer } from '@/views/AuditViewer';
import type { AuditEntry } from '@/services/models';

const mockFetchAuditLog = vi.fn();

// Capture the live-stream EventSource so tests can drive onmessage.
type FakeSource = { onmessage: ((e: { data: string }) => void) | null; onerror: (() => void) | null; close: () => void };
let lastSource: FakeSource | null = null;
const mockCreateAuditStream = vi.fn(() => {
  lastSource = { onmessage: null, onerror: null, close: vi.fn() };
  return lastSource as unknown as EventSource;
});

vi.mock('@/services/api', () => ({
  fetchAuditLog: (...args: unknown[]) => mockFetchAuditLog(...args),
  createAuditStream: () => mockCreateAuditStream(),
}));

const sampleEntries: AuditEntry[] = [
  {
    id: '1',
    timestamp: '2025-01-01T12:00:00Z',
    level: 'info',
    event: 'addon_enabled_on_cluster',
    user: 'alice',
    action: 'create',
    resource: 'cluster:prod-eu addon:cert-manager',
    source: 'ui',
    result: 'success',
    duration_ms: 245,
    detail: 'PR #42 opened and merged',
  },
  {
    id: '2',
    timestamp: '2025-01-01T12:01:00Z',
    level: 'error',
    event: 'addon_upgraded',
    user: 'bob',
    action: 'update',
    resource: 'addon:cert-manager',
    source: 'api',
    result: 'failure',
    duration_ms: 1200,
    error: 'PR could not be merged (branch protection)',
    detail: 'attempted upgrade to v1.14.0',
  },
  {
    // Unknown event code → must de-snake-case, never blank.
    id: '3',
    timestamp: '2025-01-01T12:02:00Z',
    level: 'info',
    event: 'mystery_thing_happened',
    user: 'carol',
    action: 'create',
    resource: '',
    source: 'cli',
    result: 'rejected',
    duration_ms: 5,
  },
];

describe('AuditViewer', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    lastSource = null;
    mockFetchAuditLog.mockResolvedValue({ entries: sampleEntries });
  });

  it('shows the friendly default columns (Time / Who / Action / Result)', async () => {
    render(<AuditViewer />);
    await waitFor(() => expect(screen.getByText(/alice enabled an addon/i)).toBeInTheDocument());

    // Column headers live in the table's <thead>.
    const headers = document.querySelectorAll('thead th');
    const headerText = Array.from(headers).map((h) => h.textContent?.trim());
    expect(headerText).toContain('Time');
    expect(headerText).toContain('Who');
    expect(headerText).toContain('Action');
    expect(headerText).toContain('Result');
  });

  it('renders a known event code as a friendly action sentence', async () => {
    render(<AuditViewer />);
    await waitFor(() =>
      expect(
        screen.getByText(/alice enabled an addon on a cluster — cert-manager on prod-eu/i),
      ).toBeInTheDocument(),
    );
  });

  it('falls back to de-snake-cased text for an UNKNOWN event code (never blank)', async () => {
    render(<AuditViewer />);
    await waitFor(() => expect(screen.getByText(/Mystery thing happened/i)).toBeInTheDocument());
    // The raw snake_case code is not shown as the action.
    expect(screen.queryByText('mystery_thing_happened')).not.toBeInTheDocument();
  });

  it('reveals Detail and Error in the expanded row, including the failure error', async () => {
    render(<AuditViewer />);
    await waitFor(() => expect(screen.getByText(/bob upgraded an addon/i)).toBeInTheDocument());

    // Expand the failure row (the addon_upgraded entry, second row).
    const expandButtons = screen.getAllByLabelText(/Expand details/i);
    fireEvent.click(expandButtons[1]);

    expect(screen.getByText('attempted upgrade to v1.14.0')).toBeInTheDocument();
    expect(
      screen.getAllByText(/PR could not be merged \(branch protection\)/i).length,
    ).toBeGreaterThanOrEqual(1);
    // Attribution / Duration / Event code labels live in the detail panel.
    expect(screen.getByText('Attribution')).toBeInTheDocument();
    expect(screen.getByText('Duration')).toBeInTheDocument();
    expect(screen.getByText('Event code')).toBeInTheDocument();
  });

  it('offers exactly success / partial / rejected / failure in the Result filter', async () => {
    render(<AuditViewer />);
    await waitFor(() => expect(screen.getByText(/alice enabled an addon/i)).toBeInTheDocument());

    const select = screen.getByLabelText('Result') as HTMLSelectElement;
    const values = Array.from(select.options).map((o) => o.value);
    expect(values).toEqual(['', 'success', 'partial', 'rejected', 'failure']);
    expect(values).not.toContain('error');
  });

  it('debounces filter typing into a single fetch, not one per keystroke', async () => {
    render(<AuditViewer />);
    await waitFor(() => expect(screen.getByText(/alice enabled an addon/i)).toBeInTheDocument());

    const callsAfterInitial = mockFetchAuditLog.mock.calls.length;

    const userInput = screen.getByPlaceholderText('Any user');
    fireEvent.change(userInput, { target: { value: 'a' } });
    fireEvent.change(userInput, { target: { value: 'al' } });
    fireEvent.change(userInput, { target: { value: 'ali' } });
    fireEvent.change(userInput, { target: { value: 'alice' } });

    // Wait for the debounced fetch to land with the settled value.
    await waitFor(() => {
      const last = mockFetchAuditLog.mock.calls.at(-1)?.[0] as { user?: string } | undefined;
      expect(last?.user).toBe('alice');
    });

    // No intermediate per-keystroke value ('a','al','ali') ever reached the API.
    const userArgs = mockFetchAuditLog.mock.calls
      .slice(callsAfterInitial)
      .map((c) => (c[0] as { user?: string }).user);
    expect(userArgs).not.toContain('a');
    expect(userArgs).not.toContain('al');
    expect(userArgs).not.toContain('ali');
  });

  it('"Load more" re-fetches with a larger limit (beyond 200)', async () => {
    const fullPage = Array.from({ length: 200 }, (_, i) => ({ ...sampleEntries[0], id: `e${i}` }));
    mockFetchAuditLog.mockResolvedValue({ entries: fullPage });
    render(<AuditViewer />);

    const loadMore = await screen.findByRole('button', { name: /Load more/i });
    expect(mockFetchAuditLog.mock.calls[0][0]).toMatchObject({ limit: 200 });

    fireEvent.click(loadMore);

    await waitFor(() => {
      const limits = mockFetchAuditLog.mock.calls.map((c) => (c[0] as { limit: number }).limit);
      expect(Math.max(...limits)).toBeGreaterThan(200);
    });
  });

  it('live stream prepends a new entry to the list', async () => {
    render(<AuditViewer />);
    await waitFor(() => expect(screen.getByText(/alice enabled an addon/i)).toBeInTheDocument());

    fireEvent.click(screen.getByRole('button', { name: /Live Tail/i }));
    await waitFor(() => expect(lastSource).not.toBeNull());

    const streamed: AuditEntry = {
      id: 'live-1',
      timestamp: '2025-01-01T12:05:00Z',
      level: 'info',
      event: 'cluster_registered',
      user: 'dave',
      action: 'create',
      resource: 'cluster:new-cluster',
      source: 'ui',
      result: 'success',
      duration_ms: 30,
    };
    fireEvent.change(screen.getByPlaceholderText('Any user'), { target: { value: '' } }); // noop to ensure act
    lastSource!.onmessage?.({ data: JSON.stringify(streamed) });

    await waitFor(() =>
      expect(screen.getByText(/dave registered a cluster — new-cluster/i)).toBeInTheDocument(),
    );
  });

  it('search filters the loaded entries client-side (no server fetch)', async () => {
    render(<AuditViewer />);
    await waitFor(() => expect(screen.getByText(/alice enabled an addon/i)).toBeInTheDocument());

    const searchBox = screen.getByPlaceholderText('Filter loaded entries…');
    fireEvent.change(searchBox, { target: { value: 'mystery' } });

    await waitFor(() => {
      expect(screen.getByText(/Mystery thing happened/i)).toBeInTheDocument();
      expect(screen.queryByText(/alice enabled an addon/i)).not.toBeInTheDocument();
    });

    // Search never appears as a server query param (client-side only).
    const userArgs = mockFetchAuditLog.mock.calls.map((c) => c[0] as Record<string, unknown>);
    expect(userArgs.every((a) => !('search' in a))).toBe(true);
  });

  it('shows empty state when no events', async () => {
    mockFetchAuditLog.mockResolvedValue({ entries: [] });
    render(<AuditViewer />);
    await waitFor(() => expect(screen.getByText('No audit entries found.')).toBeInTheDocument());
  });
});

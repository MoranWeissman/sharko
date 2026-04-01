import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { Dashboards } from '@/views/Dashboards';
import { extractUrlFromIframe } from '@/hooks/useDashboards';

vi.mock('@/hooks/useTheme', () => ({
  useTheme: () => ({ theme: 'light', toggleTheme: vi.fn() }),
}));

vi.mock('@/services/api', () => ({
  api: {
    getEmbeddedDashboards: vi.fn().mockRejectedValue(new Error('no backend')),
    saveEmbeddedDashboards: vi.fn().mockRejectedValue(new Error('no backend')),
  },
}));

// Provide a stub for crypto.randomUUID in test env
let uuidCounter = 0;
vi.stubGlobal('crypto', {
  randomUUID: () => `test-uuid-${++uuidCounter}`,
});

function renderDashboards() {
  return render(
    <MemoryRouter>
      <Dashboards />
    </MemoryRouter>,
  );
}

describe('Dashboards', () => {
  beforeEach(() => {
    localStorage.clear();
    uuidCounter = 0;
  });

  it('renders empty state when no dashboards configured', () => {
    renderDashboards();
    expect(screen.getByText('No dashboards configured')).toBeInTheDocument();
    expect(screen.getByText('Add your first dashboard')).toBeInTheDocument();
    expect(
      screen.getByText('Supports Datadog, Grafana, and custom dashboard URLs'),
    ).toBeInTheDocument();
  });

  it('renders the page header', () => {
    renderDashboards();
    expect(screen.getByText('External Dashboards')).toBeInTheDocument();
    expect(
      screen.getByText('Embed Datadog and other monitoring dashboards'),
    ).toBeInTheDocument();
  });

  it('shows add form when clicking Add Dashboard button', () => {
    renderDashboards();
    fireEvent.click(screen.getByText('Add your first dashboard'));
    expect(screen.getByLabelText('Name')).toBeInTheDocument();
    expect(screen.getByLabelText('URL')).toBeInTheDocument();
    expect(screen.getByLabelText('Provider')).toBeInTheDocument();
    expect(screen.getByText('Save Dashboard')).toBeInTheDocument();
  });

  it('adds a dashboard and displays it in the sidebar', () => {
    renderDashboards();

    // Open form
    fireEvent.click(screen.getByText('Add your first dashboard'));

    // Fill in form
    fireEvent.change(screen.getByLabelText('Name'), {
      target: { value: 'Test Dashboard' },
    });
    fireEvent.change(screen.getByLabelText('URL'), {
      target: { value: 'https://app.datadoghq.eu/dashboard/abc' },
    });

    // Submit
    fireEvent.click(screen.getByText('Save Dashboard'));

    // Should appear in sidebar and toolbar
    const matches = screen.getAllByText('Test Dashboard');
    expect(matches.length).toBeGreaterThanOrEqual(1);
    // Should show iframe toolbar
    expect(screen.getByText('Open in new tab')).toBeInTheDocument();
  });

  it('loads dashboards from localStorage', async () => {
    const stored = [
      {
        id: 'stored-1',
        name: 'Preloaded Dashboard',
        url: 'https://grafana.example.com/d/abc',
        provider: 'grafana',
      },
    ];
    localStorage.setItem('aap-dashboards', JSON.stringify(stored));

    renderDashboards();
    await waitFor(() => {
      const matches = screen.getAllByText('Preloaded Dashboard');
      expect(matches.length).toBeGreaterThanOrEqual(1);
    });
  });

  it('shows edit form pre-filled with existing values', async () => {
    const stored = [
      {
        id: 'edit-1',
        name: 'My Dashboard',
        url: 'https://grafana.example.com/d/abc',
        provider: 'grafana',
      },
    ];
    localStorage.setItem('aap-dashboards', JSON.stringify(stored));

    renderDashboards();

    // Wait for dashboard to load from localStorage
    await waitFor(() => {
      expect(screen.getByText('Edit')).toBeInTheDocument();
    });

    // Click Edit button in toolbar
    fireEvent.click(screen.getByText('Edit'));

    // Form should show with pre-filled values
    expect(screen.getByText('Edit Dashboard')).toBeInTheDocument();
    expect(screen.getByText('Update Dashboard')).toBeInTheDocument();
    expect(screen.getByLabelText('Name')).toHaveValue('My Dashboard');
    expect(screen.getByLabelText('URL')).toHaveValue(
      'https://grafana.example.com/d/abc',
    );
    expect(screen.getByLabelText('Provider')).toHaveValue('grafana');
  });

  it('updates a dashboard when editing', async () => {
    const stored = [
      {
        id: 'edit-2',
        name: 'Old Name',
        url: 'https://grafana.example.com/d/abc',
        provider: 'grafana',
      },
    ];
    localStorage.setItem('aap-dashboards', JSON.stringify(stored));

    renderDashboards();

    // Wait for dashboard to load then open edit form
    await waitFor(() => {
      expect(screen.getByText('Edit')).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText('Edit'));

    // Change name
    fireEvent.change(screen.getByLabelText('Name'), {
      target: { value: 'New Name' },
    });

    // Submit
    fireEvent.click(screen.getByText('Update Dashboard'));

    // Updated name should be visible
    const matches = screen.getAllByText('New Name');
    expect(matches.length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByText('Old Name')).not.toBeInTheDocument();
  });

  it('shows provider-specific help text in the form', () => {
    renderDashboards();
    fireEvent.click(screen.getByText('Add your first dashboard'));

    // Default is Datadog
    expect(
      screen.getByText(/Paste the URL or the full iframe snippet/),
    ).toBeInTheDocument();

    // Switch to Grafana
    fireEvent.change(screen.getByLabelText('Provider'), {
      target: { value: 'grafana' },
    });
    expect(
      screen.getByText(/Ensure anonymous access or set up auth/),
    ).toBeInTheDocument();

    // Switch to Custom
    fireEvent.change(screen.getByLabelText('Provider'), {
      target: { value: 'custom' },
    });
    expect(
      screen.getByText(/Enter any URL that can be loaded in an iframe/),
    ).toBeInTheDocument();
  });

  it('has only one Add Dashboard button (in sidebar)', async () => {
    // Pre-populate so empty state doesn't show
    const stored = [
      {
        id: 'btn-1',
        name: 'Dashboard One',
        url: 'https://example.com',
        provider: 'custom',
      },
    ];
    localStorage.setItem('aap-dashboards', JSON.stringify(stored));

    renderDashboards();
    await waitFor(() => {
      expect(screen.getAllByText('Dashboard One').length).toBeGreaterThanOrEqual(1);
    });
    const addButtons = screen.getAllByText('Add Dashboard');
    expect(addButtons).toHaveLength(1);
  });
});

describe('extractUrlFromIframe', () => {
  it('extracts src from an iframe snippet with double quotes', () => {
    const iframe =
      '<iframe src="https://app.datadoghq.eu/graph/embed?token=abc123" width="600" height="300" frameborder="0"></iframe>';
    expect(extractUrlFromIframe(iframe)).toBe(
      'https://app.datadoghq.eu/graph/embed?token=abc123',
    );
  });

  it('extracts src from an iframe snippet with single quotes', () => {
    const iframe =
      "<iframe src='https://app.datadoghq.eu/graph/embed?token=xyz' width='600'></iframe>";
    expect(extractUrlFromIframe(iframe)).toBe(
      'https://app.datadoghq.eu/graph/embed?token=xyz',
    );
  });

  it('returns plain URL as-is', () => {
    const url = 'https://app.datadoghq.eu/dashboard/abc';
    expect(extractUrlFromIframe(url)).toBe(url);
  });
});

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview } from '@/views/ClustersOverview';
import { AuthProvider } from '@/hooks/useAuth';

// The Register New Cluster dialog must keep Cancel / Preview / Register on
// screen after a long Preview. A real browser check (Playwright, at 1280x800
// and at 1920x1080 with 125% zoom) found the buttons pushed below the
// dialog's cut-off edge when the dialog used the base grid with only a max
// height. The fix is a flex column: header and footer keep their size, the
// body shrinks and scrolls. jsdom does no layout, so this test pins the
// rendered classes and the shape of the DOM that make that work.

const mockGetClusters = vi.fn();
const mockRegisterCluster = vi.fn();

vi.mock('@/services/api', () => ({
  api: {
    getClusters: (...args: unknown[]) => mockGetClusters(...args),
    getAddonCatalog: () => Promise.resolve({ addons: [] }),
    health: () => Promise.resolve({ status: 'healthy', cluster_test_available: true }),
    getAllowInlineCredentials: () => Promise.resolve({ allow_inline_credentials: true }),
  },
  registerCluster: (...args: unknown[]) => mockRegisterCluster(...args),
  testClusterConnection: vi.fn(),
  unadoptCluster: vi.fn(),
}));

const longLine = '# ' + 'this file is written by Sharko and checked against the schema '.repeat(5);

function renderView() {
  localStorage.setItem('sharko-auth-token', 'test-token');
  localStorage.setItem('sharko-auth-user', 'tester');
  localStorage.setItem('sharko-auth-role', 'admin');
  return render(
    <MemoryRouter>
      <AuthProvider>
        <ClustersOverview />
      </AuthProvider>
    </MemoryRouter>,
  );
}

function classesOf(el: Element | null): string[] {
  return (el?.getAttribute('class') ?? '').split(/\s+/).filter(Boolean);
}

describe('ClustersOverview — Register New Cluster dialog layout', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    sessionStorage.clear();
    mockGetClusters.mockResolvedValue({
      clusters: [],
      health_stats: { total_in_git: 0, connected: 0, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
    });
    mockRegisterCluster.mockResolvedValue({
      status: 'success',
      dry_run: {
        pr_title: 'sharko: register cluster spoke-1',
        effective_addons: [],
        files_to_write: [
          {
            path: 'configuration/managed-clusters.yaml',
            action: 'create',
            diff: ['--- a', '+++ b', '@@ -0,0 +1,2 @@', '+' + longLine, '+clusters: []', ''].join('\n'),
          },
        ],
        secrets_to_create: [],
      },
    });
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve({ ok: true } as Response)));
  });

  it('keeps the footer outside the scrolling body after a long Preview', async () => {
    renderView();
    await waitFor(() => expect(screen.getByText('Clusters')).toBeInTheDocument());
    fireEvent.click(screen.getByRole('button', { name: /add cluster/i }));
    await waitFor(() => expect(screen.getByText('Register New Cluster')).toBeInTheDocument());

    const select = screen.getByDisplayValue(/Choose where this cluster's credentials come from/i);
    fireEvent.change(select, { target: { value: 'secret-kubeconfig' } });
    fireEvent.change(screen.getByPlaceholderText(/prod-us-east-1/i), { target: { value: 'spoke-1' } });
    fireEvent.click(screen.getByRole('button', { name: /^preview$/i }));
    await waitFor(() => expect(screen.getByText(/this file is written by Sharko/)).toBeInTheDocument());

    const content = document.body.querySelector('[data-slot="dialog-content"]');
    expect(content).toBeTruthy();
    const contentClasses = classesOf(content);
    // A flex column, not the base grid (a grid row grows past the max height).
    expect(contentClasses).toContain('flex');
    expect(contentClasses).toContain('flex-col');
    expect(contentClasses).not.toContain('grid');
    expect(contentClasses).toContain('overflow-hidden');
    // A percentage, not vh: vh grows with CSS zoom, a percentage does not.
    expect(contentClasses).toContain('max-h-[90%]');
    expect(contentClasses.some((c) => /^max-h-\[\d+vh\]$/.test(c))).toBe(false);

    const header = content!.querySelector(':scope > [data-slot="dialog-header"]');
    const body = screen.getByTestId('register-dialog-body');
    const footer = content!.querySelector(':scope > [data-slot="dialog-footer"]');
    expect(header).toBeTruthy();
    expect(footer).toBeTruthy();
    expect(classesOf(header)).toContain('shrink-0');
    expect(classesOf(footer)).toContain('shrink-0');
    expect(body.parentElement).toBe(content);
    expect(classesOf(body)).toContain('min-h-0');
    expect(classesOf(body)).toContain('overflow-y-auto');

    // The buttons live in the footer, not in the scrolling body.
    for (const name of ['Cancel', 'Preview', 'Register']) {
      const btn = screen.getByRole('button', { name: new RegExp(`^${name}$`, 'i') });
      expect(footer!.contains(btn)).toBe(true);
      expect(body.contains(btn)).toBe(false);
    }
    // The long preview line sits inside the scrolling body.
    expect(body.contains(screen.getByText(/this file is written by Sharko/))).toBe(true);
  });
});

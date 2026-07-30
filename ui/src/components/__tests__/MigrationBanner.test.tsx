import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react';
import { MigrationBanner } from '@/components/MigrationBanner';
import { AuthContext } from '@/hooks/useAuth';
import { api, MigrationPRAlreadyOpenError } from '@/services/api';

// migration-ui — the v3 -> v4 repo migration banner. Backend contract is
// GET /migration/status, POST /migration/preview, POST /migration/migrate
// (v4-wave2 Epic 5, PR #636). This pins the UI side: the banner shows only
// for a v3-format repo, offers Preview + Open migration PR to admins only,
// and clears itself once status flips to v4 — no manual dismiss exists.
//
// v4-wave2 review fixes pinned here too: the banner must read "PR is
// open" from GET /migration/status (server truth) rather than only its
// own in-memory state — a remount must not forget an already-open PR and
// offer to mint a second one — a failed background poll must not clear an
// already-showing banner, an "already_migrated" migrate result needs a
// visible message, and collapsing the preview panel must never trigger a
// refetch.

vi.mock('@/services/api', () => ({
  api: {
    getMigrationStatus: vi.fn(),
    previewMigration: vi.fn(),
    migrateRepo: vi.fn(),
  },
  MigrationPRAlreadyOpenError: class MigrationPRAlreadyOpenError extends Error {
    prUrl: string;
    prNumber?: number;
    constructor(body: { error?: string; migration_pr_url?: string; migration_pr_number?: number }) {
      super(body.error || 'a migration pull request is already open');
      this.name = 'MigrationPRAlreadyOpenError';
      this.prUrl = body.migration_pr_url || '';
      this.prNumber = body.migration_pr_number;
    }
  },
}));

const adminAuth = {
  token: 'test-token',
  username: 'admin',
  role: 'admin',
  login: vi.fn(),
  logout: vi.fn(),
  isAuthenticated: true,
  isAdmin: true,
  loading: false,
  error: null,
};

const viewerAuth = { ...adminAuth, role: 'viewer', username: 'viewer', isAdmin: false };

function renderAs(auth: typeof adminAuth) {
  return render(
    <AuthContext.Provider value={auth}>
      <MigrationBanner />
    </AuthContext.Provider>,
  );
}

const mockedApi = vi.mocked(api);

beforeEach(() => {
  vi.clearAllMocks();
});

describe('MigrationBanner', () => {
  it('renders nothing when the repo is already v4', async () => {
    mockedApi.getMigrationStatus.mockResolvedValue({
      format: 'v4',
      migration_available: false,
      message: 'this repo already uses the current format — nothing to migrate',
    });
    renderAs(adminAuth);
    await waitFor(() => expect(mockedApi.getMigrationStatus).toHaveBeenCalled());
    expect(screen.queryByTestId('migration-banner')).not.toBeInTheDocument();
  });

  it('renders nothing when the repo is empty (never set up)', async () => {
    mockedApi.getMigrationStatus.mockResolvedValue({
      format: 'empty',
      migration_available: false,
      message: 'this repo has not been set up yet — initialize it instead of migrating',
    });
    renderAs(adminAuth);
    await waitFor(() => expect(mockedApi.getMigrationStatus).toHaveBeenCalled());
    expect(screen.queryByTestId('migration-banner')).not.toBeInTheDocument();
  });

  it('renders nothing when the status probe fails (no connection yet)', async () => {
    mockedApi.getMigrationStatus.mockRejectedValue(new Error('no active connection'));
    renderAs(adminAuth);
    await waitFor(() => expect(mockedApi.getMigrationStatus).toHaveBeenCalled());
    expect(screen.queryByTestId('migration-banner')).not.toBeInTheDocument();
  });

  it('shows the plain-words banner for a v3 repo', async () => {
    mockedApi.getMigrationStatus.mockResolvedValue({
      format: 'v3',
      migration_available: true,
      message: 'v3 format — migration available',
    });
    renderAs(adminAuth);
    expect(await screen.findByText('This repo uses the older v3 layout')).toBeInTheDocument();
    expect(screen.getByText('One pull request migrates it. Reads keep working until then.')).toBeInTheDocument();
  });

  it('shows Preview and Migrate actions to an admin', async () => {
    mockedApi.getMigrationStatus.mockResolvedValue({
      format: 'v3',
      migration_available: true,
      message: 'v3 format — migration available',
    });
    renderAs(adminAuth);
    expect(await screen.findByText('Preview migration')).toBeInTheDocument();
    expect(screen.getByTestId('open-migration-pr')).toBeInTheDocument();
  });

  it('hides Preview/Migrate actions from a non-admin (migration.preview/migrate are RoleAdmin)', async () => {
    mockedApi.getMigrationStatus.mockResolvedValue({
      format: 'v3',
      migration_available: true,
      message: 'v3 format — migration available',
    });
    renderAs(viewerAuth);
    await screen.findByText('This repo uses the older v3 layout');
    expect(screen.queryByText('Preview migration')).not.toBeInTheDocument();
    expect(screen.queryByTestId('open-migration-pr')).not.toBeInTheDocument();
  });

  it('preview shows the file plan and notes (e.g. the v3 secrets-block note)', async () => {
    mockedApi.getMigrationStatus.mockResolvedValue({
      format: 'v3',
      migration_available: true,
      message: 'v3 format — migration available',
    });
    mockedApi.previewMigration.mockResolvedValue({
      format: 'v3',
      add: [{ path: 'engine/application.yaml', action: 'add' }],
      convert: [{ path: 'fleet/connections.yaml', from_path: 'configuration/managed-clusters.yaml', action: 'convert' }],
      remove: [{ path: 'bootstrap/Chart.yaml', action: 'remove' }],
      notes: [
        'configuration/addons-catalog.yaml lists secrets in your old catalog file. The new catalog file has no place for them yet, so they are not carried over — the addon still deploys, but check its secrets before you rely on them',
      ],
      pr_title: 'sharko: move this repo to the current format',
    });
    renderAs(adminAuth);
    fireEvent.click(await screen.findByText('Preview migration'));

    expect(await screen.findByText('Files this pull request would touch (3)')).toBeInTheDocument();
    expect(screen.getByText('engine/application.yaml')).toBeInTheDocument();
    expect(screen.getByText('configuration/managed-clusters.yaml → fleet/connections.yaml')).toBeInTheDocument();
    expect(screen.getByText(/lists secrets in your old catalog file/)).toBeInTheDocument();
  });

  it('preview surfaces a plain error when the preview call fails', async () => {
    mockedApi.getMigrationStatus.mockResolvedValue({
      format: 'v3',
      migration_available: true,
      message: 'v3 format — migration available',
    });
    mockedApi.previewMigration.mockRejectedValue(new Error('the repo cannot be migrated as it stands'));
    renderAs(adminAuth);
    fireEvent.click(await screen.findByText('Preview migration'));
    expect(await screen.findByText('the repo cannot be migrated as it stands')).toBeInTheDocument();
  });

  it('Open migration PR shows the PR link and flips to the "PR open" state', async () => {
    mockedApi.getMigrationStatus
      .mockResolvedValueOnce({
        format: 'v3',
        migration_available: true,
        message: 'v3 format — migration available',
      })
      // The follow-up status poll fired after a successful migrate now
      // reflects the freshly-opened PR — server truth, same as a fresh
      // GET would after the fact.
      .mockResolvedValue({
        format: 'v3',
        migration_available: true,
        message: 'v3 format — migration available',
        migration_pr_url: 'https://git.example.com/org/repo/pull/42',
        migration_pr_number: 42,
      });
    mockedApi.migrateRepo.mockResolvedValue({
      status: 'migrated',
      git: { pr_url: 'https://git.example.com/org/repo/pull/42', pr_id: 42, merged: false },
    });
    renderAs(adminAuth);
    fireEvent.click(await screen.findByTestId('open-migration-pr'));

    expect(await screen.findByText('Migration pull request open.')).toBeInTheDocument();
    const link = screen.getByRole('link', { name: /View it/ });
    expect(link).toHaveAttribute('href', 'https://git.example.com/org/repo/pull/42');
    // The PR-open state replaces the action buttons — re-running Migrate
    // isn't offered once a PR already exists.
    expect(screen.queryByTestId('open-migration-pr')).not.toBeInTheDocument();
  });

  it('shows a plain error when opening the migration PR fails', async () => {
    mockedApi.getMigrationStatus.mockResolvedValue({
      format: 'v3',
      migration_available: true,
      message: 'v3 format — migration available',
    });
    mockedApi.migrateRepo.mockRejectedValue(new Error('confirmation required: set yes: true in request body'));
    renderAs(adminAuth);
    fireEvent.click(await screen.findByTestId('open-migration-pr'));
    expect(await screen.findByText('confirmation required: set yes: true in request body')).toBeInTheDocument();
  });

  // ─── v4-wave2 review fixes ────────────────────────────────────────────

  it('a remounted banner reads "PR is open" from status, not its own state, and never offers to open a second one', async () => {
    // Simulates the double-PR bug's trigger: a fresh mount (e.g. after
    // navigating away and back) whose component state has no memory of
    // anything — migrateRepo is never even called in this test.
    mockedApi.getMigrationStatus.mockResolvedValue({
      format: 'v3',
      migration_available: true,
      message: 'v3 format — migration available',
      migration_pr_url: 'https://git.example.com/org/repo/pull/9',
      migration_pr_number: 9,
    });
    renderAs(adminAuth);

    expect(await screen.findByText('Migration pull request open.')).toBeInTheDocument();
    const link = screen.getByRole('link', { name: /View it/ });
    expect(link).toHaveAttribute('href', 'https://git.example.com/org/repo/pull/9');
    expect(screen.queryByTestId('open-migration-pr')).not.toBeInTheDocument();
    expect(mockedApi.migrateRepo).not.toHaveBeenCalled();
  });

  it('a 409 from migrateRepo (PR already open) renders the existing PR link instead of a red error', async () => {
    mockedApi.getMigrationStatus.mockResolvedValue({
      format: 'v3',
      migration_available: true,
      message: 'v3 format — migration available',
    });
    mockedApi.migrateRepo.mockRejectedValue(
      new MigrationPRAlreadyOpenError({
        error: 'a migration pull request is already open',
        migration_pr_url: 'https://git.example.com/org/repo/pull/9',
        migration_pr_number: 9,
      }),
    );
    renderAs(adminAuth);
    fireEvent.click(await screen.findByTestId('open-migration-pr'));

    expect(await screen.findByText('Migration pull request open.')).toBeInTheDocument();
    const link = screen.getByRole('link', { name: /View it/ });
    expect(link).toHaveAttribute('href', 'https://git.example.com/org/repo/pull/9');
    expect(screen.queryByText('a migration pull request is already open')).not.toBeInTheDocument();
  });

  it('a failed background poll keeps the banner showing instead of clearing it', async () => {
    mockedApi.getMigrationStatus
      .mockResolvedValueOnce({
        format: 'v3',
        migration_available: true,
        message: 'v3 format — migration available',
      })
      .mockRejectedValueOnce(new Error('network blip'));

    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      renderAs(adminAuth);
      await waitFor(() => expect(screen.getByTestId('migration-banner')).toBeInTheDocument());
      expect(mockedApi.getMigrationStatus).toHaveBeenCalledTimes(1);

      await act(async () => {
        vi.advanceTimersByTime(30000);
        await Promise.resolve();
      });

      await waitFor(() => expect(mockedApi.getMigrationStatus).toHaveBeenCalledTimes(2));
      // The poll failed, but the last-known v3 state — and the banner —
      // must still be on screen.
      expect(screen.getByTestId('migration-banner')).toBeInTheDocument();
      expect(screen.getByText('This repo uses the older v3 layout')).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it('an "already_migrated" migrate result shows a visible confirmation message', async () => {
    mockedApi.getMigrationStatus.mockResolvedValue({
      format: 'v3',
      migration_available: true,
      message: 'v3 format — migration available',
    });
    mockedApi.migrateRepo.mockResolvedValue({
      status: 'already_migrated',
      plan: { format: 'v4', add: [], convert: [], remove: [], notes: [], pr_title: '' },
    });
    renderAs(adminAuth);
    fireEvent.click(await screen.findByTestId('open-migration-pr'));

    expect(
      await screen.findByText('This repo was already on the current format — nothing to migrate.'),
    ).toBeInTheDocument();
    expect(screen.queryByTestId('open-migration-pr')).not.toBeInTheDocument();
  });

  it('collapsing the preview panel after a failed fetch does not trigger another fetch', async () => {
    mockedApi.getMigrationStatus.mockResolvedValue({
      format: 'v3',
      migration_available: true,
      message: 'v3 format — migration available',
    });
    mockedApi.previewMigration.mockRejectedValue(new Error('could not build the plan'));
    renderAs(adminAuth);

    const toggle = await screen.findByText('Preview migration');
    fireEvent.click(toggle); // opens, fetch fails
    await screen.findByText('could not build the plan');
    expect(mockedApi.previewMigration).toHaveBeenCalledTimes(1);

    fireEvent.click(toggle); // collapses — must NOT fetch again
    await waitFor(() => expect(screen.queryByText('could not build the plan')).not.toBeInTheDocument());
    expect(mockedApi.previewMigration).toHaveBeenCalledTimes(1);
  });
});

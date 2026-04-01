import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { UpgradeChecker } from '@/views/UpgradeChecker';

vi.mock('@/services/api', () => ({
  api: {
    getAddonCatalog: vi.fn().mockResolvedValue({
      addons: [
        {
          addon_name: 'istio',
          chart: 'istio',
          repo_url: 'https://charts.example.com',
          version: '1.20.0',
          total_clusters: 5,
          enabled_clusters: 4,
          healthy_applications: 4,
          degraded_applications: 0,
          missing_applications: 1,
          applications: [],
        },
        {
          addon_name: 'prometheus',
          chart: 'prometheus',
          repo_url: 'https://charts.example.com',
          version: '2.50.0',
          total_clusters: 3,
          enabled_clusters: 3,
          healthy_applications: 3,
          degraded_applications: 0,
          missing_applications: 0,
          applications: [],
        },
      ],
      total_addons: 2,
      total_clusters: 5,
      addons_only_in_git: 0,
    }),
    getUpgradeVersions: vi.fn().mockResolvedValue({
      addon_name: 'istio',
      chart: 'istio',
      repo_url: 'https://charts.example.com',
      versions: [
        { version: '1.22.0' },
        { version: '1.21.0' },
        { version: '1.20.0' },
      ],
    }),
    checkUpgrade: vi.fn().mockResolvedValue({
      addon_name: 'istio',
      chart: 'istio',
      current_version: '1.20.0',
      target_version: '1.22.0',
      total_changes: 2,
      added: [{ path: 'gateway.enabled', type: 'added', new_value: 'true' }],
      removed: [],
      changed: [{ path: 'pilot.replicas', type: 'changed', old_value: '1', new_value: '2' }],
      conflicts: [],
    }),
    getAIStatus: vi.fn().mockResolvedValue({ enabled: false }),
    getAIConfig: vi.fn().mockResolvedValue({ current_provider: 'none', available_providers: [] }),
    getAISummary: vi.fn().mockResolvedValue({ summary: 'Test AI summary' }),
  },
}));

function renderUpgradeChecker() {
  return render(
    <MemoryRouter>
      <UpgradeChecker />
    </MemoryRouter>,
  );
}

describe('UpgradeChecker', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders loading state initially', () => {
    renderUpgradeChecker();
    expect(screen.getByText('Loading addon catalog...')).toBeInTheDocument();
  });

  it('renders addon selection after data loads', async () => {
    renderUpgradeChecker();

    await waitFor(() => {
      expect(screen.getByText('Add-on Upgrade Checker')).toBeInTheDocument();
    });

    expect(screen.getByText('Select Addon')).toBeInTheDocument();
    expect(screen.getByText('Choose an addon...')).toBeInTheDocument();
  });

  it('renders error state when API fails', async () => {
    const { api } = await import('@/services/api');
    vi.mocked(api.getAddonCatalog).mockRejectedValueOnce(
      new Error('Network error'),
    );

    renderUpgradeChecker();

    await waitFor(() => {
      expect(screen.getByText('Network error')).toBeInTheDocument();
    });
  });
});

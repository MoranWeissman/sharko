import { describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

const mockTestClusterConnection = vi.fn();

vi.mock('@/services/api', async () => {
  const actual = await vi.importActual<typeof import('@/services/api')>('@/services/api');
  return {
    ...actual,
    testClusterConnection: (...args: unknown[]) => mockTestClusterConnection(...args),
  };
});

import { TestConnectionModal } from '@/components/TestConnectionModal';

const cleanupWarning =
  'Sharko created the namespace "sharko-test" for this test but could not remove it afterwards. You can delete it by hand.';

describe('TestConnectionModal cleanup warnings', () => {
  it('shows a cleanup warning and still shows the test as passed', async () => {
    mockTestClusterConnection.mockResolvedValue({
      success: true,
      reachable: true,
      stage: 'stage1',
      duration_ms: 10,
      server_version: 'v1.29.3',
      steps: [{ name: 'Ensure namespace', status: 'pass', detail: 'created' }],
      warnings: [cleanupWarning],
    });

    render(
      <MemoryRouter>
        <TestConnectionModal
          clusterName="prod-eu"
          open={true}
          onClose={() => {}}
          onSuggestionSelect={() => {}}
        />
      </MemoryRouter>,
    );

    await waitFor(() => expect(screen.getByText(cleanupWarning)).toBeInTheDocument());
    expect(screen.getByText(/Connected/)).toBeInTheDocument();
  });

  it('shows no warning box when cleanup worked', async () => {
    mockTestClusterConnection.mockResolvedValue({
      success: true,
      reachable: true,
      stage: 'stage1',
      duration_ms: 10,
      server_version: 'v1.29.3',
      steps: [{ name: 'Ensure namespace', status: 'pass', detail: 'created for this test, then removed' }],
    });

    render(
      <MemoryRouter>
        <TestConnectionModal
          clusterName="prod-eu"
          open={true}
          onClose={() => {}}
          onSuggestionSelect={() => {}}
        />
      </MemoryRouter>,
    );

    await waitFor(() => expect(screen.getByText(/Connected/)).toBeInTheDocument());
    expect(screen.queryByText(/could not remove/)).not.toBeInTheDocument();
  });
});

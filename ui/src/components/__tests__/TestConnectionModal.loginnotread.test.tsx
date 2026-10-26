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

// ep4 round T, T2. When Sharko could not read the cluster's login, it never
// asked the cluster. The modal says that in plain words and shows the reason
// the server sent. It does not tell the user to set a secret path, which was
// wrong advice when the real cause was Sharko's AWS login.
const notRead = "Sharko could not read this cluster's login, so it did not test the connection.";
const awsReason = 'AWS turned down the login Sharko uses for AWS.';
const oldHint = 'Set the secret path manually in cluster settings.';

function renderModal() {
  return render(
    <MemoryRouter>
      <TestConnectionModal clusterName="prod-eu" open={true} onClose={() => {}} onSuggestionSelect={() => {}} />
    </MemoryRouter>,
  );
}

describe('TestConnectionModal when the login could not be read', () => {
  it('says the connection was not tested, shows the reason, and drops the secret path hint', async () => {
    mockTestClusterConnection.mockResolvedValue({
      success: false,
      reachable: false,
      stage: 'credentials',
      error_code: 'ERR_AUTH',
      error_message: awsReason,
      duration_ms: 3,
      steps: [{ name: 'Fetch credentials', status: 'fail', detail: awsReason }],
    });
    renderModal();

    await waitFor(() => expect(screen.getByTestId('test-login-not-read')).toBeInTheDocument());
    expect(screen.getByTestId('test-login-not-read').textContent).toBe(notRead);
    expect(screen.getAllByText(awsReason, { exact: false }).length).toBeGreaterThan(0);
    expect(screen.queryByText(oldHint)).not.toBeInTheDocument();
    expect(screen.queryByText('Unreachable')).not.toBeInTheDocument();
  });

  it('keeps the old answer when the cluster itself did not answer', async () => {
    mockTestClusterConnection.mockResolvedValue({
      success: false,
      reachable: false,
      stage: 'connectivity',
      error_message: 'Sharko could not reach the cluster.',
      duration_ms: 3,
    });
    renderModal();

    await waitFor(() => expect(screen.getByText(oldHint)).toBeInTheDocument());
    expect(screen.queryByTestId('test-login-not-read')).not.toBeInTheDocument();
    expect(screen.queryByText(notRead)).not.toBeInTheDocument();
  });
});

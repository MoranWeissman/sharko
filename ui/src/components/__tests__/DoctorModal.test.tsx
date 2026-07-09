import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { DoctorModal } from '@/components/DoctorModal';
import type { DoctorClusterResponse } from '@/services/models';

const mockDoctorCluster = vi.fn();
vi.mock('@/services/api', () => ({
  doctorCluster: (...args: unknown[]) => mockDoctorCluster(...args),
}));

// V2-cleanup-88.5 — connection doctor rendering. Pins all three check
// statuses (pass / fail / not-applicable) and that the fix line only shows
// on failure, per the POST /clusters/{name}/doctor contract (V2-cleanup-88.4).
const allPassReport: DoctorClusterResponse = {
  overall: 'pass',
  checks: [
    { id: 'connection-credentials', status: 'pass', detail: 'Sharko read connection credentials for cluster "prod-eu".' },
    { id: 'addon-secret-paths', status: 'pass', detail: 'All 2 addon secret paths resolved.' },
    { id: 'assume-role', status: 'not-applicable', detail: 'No cross-account role is configured for this cluster.' },
    { id: 'cluster-access', status: 'pass', detail: 'Sharko created, read, and deleted a canary secret on the cluster.' },
  ],
};

const partialReport: DoctorClusterResponse = {
  overall: 'partial',
  checks: [
    { id: 'connection-credentials', status: 'pass', detail: 'Sharko read connection credentials for cluster "prod-eu".' },
    {
      id: 'addon-secret-paths',
      status: 'fail',
      detail: 'Sharko could not read secret path secrets/datadog/api-key for addon "datadog".',
      fix: 'Check that the secret exists at secrets/datadog/api-key in your configured backend.',
    },
    { id: 'assume-role', status: 'not-applicable', detail: 'No cross-account role is configured for this cluster.' },
    { id: 'cluster-access', status: 'pass', detail: 'Sharko created, read, and deleted a canary secret on the cluster.' },
  ],
};

const allFailReport: DoctorClusterResponse = {
  overall: 'fail',
  checks: [
    {
      id: 'connection-credentials',
      status: 'fail',
      detail: 'Sharko could not read connection credentials for cluster "prod-eu": secret not found.',
      fix: 'Check that the cluster is registered and its credentials still exist at the configured source.',
    },
    { id: 'addon-secret-paths', status: 'not-applicable', detail: 'No addons with secrets are enabled on this cluster.' },
    { id: 'assume-role', status: 'not-applicable', detail: 'No cross-account role is configured for this cluster.' },
    {
      id: 'cluster-access',
      status: 'fail',
      detail: 'Sharko could not reach the cluster.',
      fix: 'The role works in AWS, but the cluster doesn’t trust it yet — add an EKS access entry for it.',
    },
  ],
};

function renderModal(open = true) {
  const onClose = vi.fn();
  return {
    onClose,
    ...render(<DoctorModal clusterName="prod-eu" open={open} onClose={onClose} />),
  };
}

describe('DoctorModal', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders a loading state while the doctor runs', () => {
    mockDoctorCluster.mockReturnValue(new Promise(() => {})); // never resolves
    renderModal();

    expect(screen.getByText('Running the doctor…')).toBeInTheDocument();
    expect(screen.getByText('Run connection doctor: prod-eu')).toBeInTheDocument();
  });

  it('does not call the API when closed', () => {
    mockDoctorCluster.mockResolvedValue(allPassReport);
    renderModal(false);

    expect(mockDoctorCluster).not.toHaveBeenCalled();
  });

  it('renders all four checks with a green check on pass and a gray dash on not-applicable', async () => {
    mockDoctorCluster.mockResolvedValue(allPassReport);
    renderModal();

    await waitFor(() => {
      expect(screen.getByTestId('doctor-checks')).toBeInTheDocument();
    });

    expect(screen.getByText('All checks passed — this cluster’s connection looks healthy.')).toBeInTheDocument();

    expect(screen.getByTestId('doctor-check-connection-credentials')).toHaveAttribute('data-status', 'pass');
    expect(screen.getByTestId('doctor-check-addon-secret-paths')).toHaveAttribute('data-status', 'pass');
    expect(screen.getByTestId('doctor-check-assume-role')).toHaveAttribute('data-status', 'not-applicable');
    expect(screen.getByTestId('doctor-check-cluster-access')).toHaveAttribute('data-status', 'pass');

    // Plain-English labels, not raw IDs.
    expect(screen.getByText('Connection credentials')).toBeInTheDocument();
    expect(screen.getByText('Cross-account role')).toBeInTheDocument();

    // No fix lines when nothing failed.
    expect(screen.queryByTestId(/^doctor-fix-/)).not.toBeInTheDocument();
  });

  it('renders a red X and the fix line on a failed check (partial overall)', async () => {
    mockDoctorCluster.mockResolvedValue(partialReport);
    renderModal();

    await waitFor(() => {
      expect(screen.getByText('Some checks passed and some failed — see the fixes below.')).toBeInTheDocument();
    });

    expect(screen.getByTestId('doctor-check-addon-secret-paths')).toHaveAttribute('data-status', 'fail');
    expect(
      screen.getByText(/Sharko could not read secret path secrets\/datadog\/api-key/),
    ).toBeInTheDocument();
    const fix = screen.getByTestId('doctor-fix-addon-secret-paths');
    expect(fix).toHaveTextContent('Check that the secret exists at secrets/datadog/api-key in your configured backend.');

    // Passing checks in the same report carry no fix line.
    expect(screen.queryByTestId('doctor-fix-connection-credentials')).not.toBeInTheDocument();
  });

  it('renders the all-fail overall verdict and fix lines on every failed check', async () => {
    mockDoctorCluster.mockResolvedValue(allFailReport);
    renderModal();

    await waitFor(() => {
      expect(screen.getByText('Every check that ran failed — see the fixes below.')).toBeInTheDocument();
    });

    expect(screen.getByTestId('doctor-check-connection-credentials')).toHaveAttribute('data-status', 'fail');
    expect(screen.getByTestId('doctor-check-cluster-access')).toHaveAttribute('data-status', 'fail');
    expect(screen.getByTestId('doctor-fix-connection-credentials')).toBeInTheDocument();
    expect(screen.getByTestId('doctor-fix-cluster-access')).toHaveTextContent(/doesn.t trust it yet/);

    // not-applicable checks render with no fix line.
    expect(screen.queryByTestId('doctor-fix-addon-secret-paths')).not.toBeInTheDocument();
  });

  it('renders an error state when the doctor call itself fails', async () => {
    mockDoctorCluster.mockRejectedValue(new Error('cluster not found'));
    renderModal();

    await waitFor(() => {
      expect(screen.getByText('cluster not found')).toBeInTheDocument();
    });
  });
});

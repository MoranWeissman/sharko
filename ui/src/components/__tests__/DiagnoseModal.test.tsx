import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { DiagnoseModal } from '@/components/DiagnoseModal';
import type { DiagnosticReport } from '@/services/models';

const mockDiagnoseCluster = vi.fn();
vi.mock('@/services/api', () => ({
  diagnoseCluster: (...args: unknown[]) => mockDiagnoseCluster(...args),
}));

const sampleReport: DiagnosticReport = {
  identity: 'arn:aws:iam::123456789012:role/sharko',
  role_assumption: 'success',
  namespace_access: [
    { permission: 'create secrets in argocd', passed: true },
    { permission: 'list applications in argocd', passed: false, error: 'forbidden' },
  ],
  suggested_fixes: [
    {
      description: 'Grant list permission on applications',
      yaml: 'apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRoleBinding\nmetadata:\n  name: sharko-fix',
    },
  ],
};

function renderModal(open = true) {
  const onClose = vi.fn();
  return {
    onClose,
    ...render(
      <DiagnoseModal clusterName="prod-eu" open={open} onClose={onClose} />,
    ),
  };
}

describe('DiagnoseModal', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders loading state when open', () => {
    mockDiagnoseCluster.mockReturnValue(new Promise(() => {})); // never resolves
    renderModal();

    expect(screen.getByText('Running diagnostics...')).toBeInTheDocument();
    expect(screen.getByText('Diagnose: prod-eu')).toBeInTheDocument();
  });

  it('renders permission checks with summary after API response', async () => {
    mockDiagnoseCluster.mockResolvedValue(sampleReport);
    renderModal();

    await waitFor(() => {
      expect(screen.getByText('Permission Checks')).toBeInTheDocument();
    });

    // Summary line for failures
    expect(screen.getByText(/1 of 2 checks? failed/)).toBeInTheDocument();

    // Passing check
    expect(screen.getByText('create secrets in argocd')).toBeInTheDocument();
    // Failing check
    expect(screen.getByText('list applications in argocd')).toBeInTheDocument();
    expect(screen.getByText('forbidden')).toBeInTheDocument();
  });

  it('renders suggested fixes with YAML', async () => {
    mockDiagnoseCluster.mockResolvedValue(sampleReport);
    renderModal();

    await waitFor(() => {
      expect(screen.getByText('Suggested Fixes')).toBeInTheDocument();
    });

    expect(screen.getByText('Grant list permission on applications')).toBeInTheDocument();
    expect(screen.getByText(/apiVersion: rbac.authorization.k8s.io/)).toBeInTheDocument();
  });

  it('renders a copy button for fix YAML', async () => {
    mockDiagnoseCluster.mockResolvedValue(sampleReport);
    renderModal();

    await waitFor(() => {
      expect(screen.getByText('Suggested Fixes')).toBeInTheDocument();
    });

    expect(screen.getByLabelText('Copy YAML')).toBeInTheDocument();
  });

  it('renders error state on API failure', async () => {
    mockDiagnoseCluster.mockRejectedValue(new Error('cluster not found'));
    renderModal();

    await waitFor(() => {
      expect(screen.getByText('cluster not found')).toBeInTheDocument();
    });
  });

  it('does not call API when closed', () => {
    mockDiagnoseCluster.mockResolvedValue(sampleReport);
    renderModal(false);

    expect(mockDiagnoseCluster).not.toHaveBeenCalled();
  });
});

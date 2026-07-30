import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { TakeoverDialog, DropLegacyLabelsDialog } from '@/components/TakeoverDialog';
import type { TakeoverReport, DropLegacyLabelsResponse } from '@/services/models';

const mockPreflight = vi.fn();
const mockTakeover = vi.fn();
const mockDropLabels = vi.fn();

vi.mock('@/services/api', () => ({
  takeoverPreflight: (...args: unknown[]) => mockPreflight(...args),
  takeoverCluster: (...args: unknown[]) => mockTakeover(...args),
  dropLegacyLabels: (...args: unknown[]) => mockDropLabels(...args),
}));

// v4 Wave 2, Epic 6. The dialog's job is to make sure nobody reaches the
// button without having been shown what it will do, so these tests are
// mostly about what is NOT clickable and when.

const okReport: TakeoverReport = {
  cluster: 'prod-eu',
  ready: true,
  needs_acknowledgement: false,
  summary: 'prod-eu is ready to take over — every check came back clean.',
  server: 'https://prod-eu.example.org',
  findings: [
    {
      id: 'secret-owner',
      title: "Who owns this cluster's connection today",
      status: 'ok',
      detail: 'Nobody has claimed the connection for "prod-eu".',
      what_it_means: 'No other tool says it owns this connection, so Sharko can take it over cleanly.',
      what_to_do: 'Nothing to do.',
    },
    {
      id: 'appset-deletion-safety',
      title: 'What happens if this cluster stops matching an ApplicationSet',
      status: 'ok',
      detail: 'There are no ApplicationSets in the argocd namespace.',
      what_it_means: 'Changing labels on this cluster cannot delete anything.',
      what_to_do: 'Nothing to do.',
    },
    {
      id: 'cluster-applications',
      title: 'What is running on this cluster right now',
      status: 'ok',
      detail: 'No ArgoCD Applications are pointed at this cluster.',
      what_it_means: 'Nothing is being deployed here today.',
      what_to_do: 'Nothing to do.',
    },
    {
      id: 'name-collision',
      title: 'Whether the name is already taken',
      status: 'ok',
      detail: 'Sharko has no cluster called "prod-eu" yet.',
      what_it_means: 'The name is free.',
      what_to_do: 'Nothing to do.',
    },
  ],
};

const blockedReport: TakeoverReport = {
  ...okReport,
  ready: false,
  summary: 'prod-eu is not ready to take over yet: 1 thing to fix first.',
  findings: okReport.findings.map((f) =>
    f.id === 'name-collision'
      ? {
          ...f,
          status: 'blocked' as const,
          detail: 'Sharko already has a different cluster called "prod-eu".',
          what_it_means: 'Two clusters cannot share a name.',
          what_to_do: 'Rename one of them, then run this check again and it will go green.',
        }
      : f,
  ),
};

const warningReport: TakeoverReport = {
  ...okReport,
  needs_acknowledgement: true,
  summary: 'prod-eu can be taken over, but there is 1 thing to read and agree to first.',
  legacy_labels: { env: 'prod', team: 'platform' },
  legacy_labels_selected_by: { env: ['legacy-addons'] },
  findings: okReport.findings.map((f) =>
    f.id === 'appset-deletion-safety'
      ? {
          ...f,
          status: 'warning' as const,
          detail: '1 ApplicationSet would delete what it installed: legacy-addons.',
          what_it_means: 'Taking over does not change any labels by itself, so nothing is deleted today.',
          what_to_do: 'Set it to keep its resources before you drop this cluster’s old labels.',
          application_sets: ['legacy-addons'],
        }
      : f,
  ),
};

beforeEach(() => {
  vi.clearAllMocks();
});

describe('TakeoverDialog', () => {
  it('shows all four checks with their plain-English meaning', async () => {
    mockPreflight.mockResolvedValue(okReport);
    render(<TakeoverDialog clusterName="prod-eu" open onClose={() => {}} />);

    await waitFor(() => expect(screen.getByTestId('takeover-findings')).toBeInTheDocument());
    for (const id of ['secret-owner', 'appset-deletion-safety', 'cluster-applications', 'name-collision']) {
      expect(screen.getByTestId(`takeover-finding-${id}`)).toBeInTheDocument();
    }
    expect(screen.getByTestId('takeover-summary')).toHaveTextContent('ready to take over');
    expect(
      screen.getByText('No other tool says it owns this connection, so Sharko can take it over cleanly.'),
    ).toBeInTheDocument();
  });

  it('hides the button entirely when a check is blocking, and shows what to do', async () => {
    mockPreflight.mockResolvedValue(blockedReport);
    render(<TakeoverDialog clusterName="prod-eu" open onClose={() => {}} />);

    await waitFor(() => expect(screen.getByTestId('takeover-finding-name-collision')).toBeInTheDocument());
    expect(screen.queryByTestId('takeover-confirm')).not.toBeInTheDocument();
    expect(screen.getByTestId('takeover-todo-name-collision')).toHaveTextContent('run this check again');
  });

  it('re-runs the checks in place, so a fix shows up as green', async () => {
    mockPreflight.mockResolvedValueOnce(blockedReport).mockResolvedValueOnce(okReport);
    render(<TakeoverDialog clusterName="prod-eu" open onClose={() => {}} />);

    await waitFor(() => expect(screen.queryByTestId('takeover-confirm')).not.toBeInTheDocument());
    await userEvent.click(screen.getByTestId('takeover-recheck'));

    await waitFor(() =>
      expect(screen.getByTestId('takeover-finding-name-collision')).toHaveAttribute('data-status', 'ok'),
    );
    expect(screen.getByTestId('takeover-confirm')).toBeEnabled();
    expect(mockPreflight).toHaveBeenCalledTimes(2);
  });

  it('will not let you go ahead on a warning until you say you have read it', async () => {
    mockPreflight.mockResolvedValue(warningReport);
    mockTakeover.mockResolvedValue({ cluster: 'prod-eu', status: 'success', secret_swapped: true, message: 'done' });
    render(<TakeoverDialog clusterName="prod-eu" open onClose={() => {}} />);

    await waitFor(() => expect(screen.getByTestId('takeover-confirm')).toBeInTheDocument());
    expect(screen.getByTestId('takeover-confirm')).toBeDisabled();

    await userEvent.click(screen.getByTestId('takeover-acknowledge'));
    expect(screen.getByTestId('takeover-confirm')).toBeEnabled();

    await userEvent.click(screen.getByTestId('takeover-confirm'));
    await waitFor(() => expect(mockTakeover).toHaveBeenCalled());
    expect(mockTakeover).toHaveBeenCalledWith('prod-eu', {
      yes: true,
      acknowledge_warnings: true,
      preserve_legacy_labels: true,
    });
  });

  it('lists the labels being carried over, and who picks clusters using them, before the confirm', async () => {
    mockPreflight.mockResolvedValue(warningReport);
    render(<TakeoverDialog clusterName="prod-eu" open onClose={() => {}} />);

    await waitFor(() => expect(screen.getByTestId('takeover-carried-labels')).toBeInTheDocument());
    expect(screen.getByTestId('takeover-carried-label-env')).toHaveTextContent('env: prod');
    expect(screen.getByTestId('takeover-carried-label-env')).toHaveTextContent('legacy-addons');
    expect(screen.getByTestId('takeover-carried-label-team')).toHaveTextContent(
      'Nothing Sharko can see picks clusters using this one',
    );
  });

  it('keeps the labels by default and sends preserve=false only when unticked', async () => {
    mockPreflight.mockResolvedValue({ ...warningReport, needs_acknowledgement: false });
    mockTakeover.mockResolvedValue({ cluster: 'prod-eu', status: 'success', secret_swapped: true, message: 'done' });
    render(<TakeoverDialog clusterName="prod-eu" open onClose={() => {}} />);

    await waitFor(() => expect(screen.getByTestId('takeover-preserve')).toBeChecked());
    await userEvent.click(screen.getByTestId('takeover-preserve'));
    await userEvent.click(screen.getByTestId('takeover-confirm'));

    await waitFor(() => expect(mockTakeover).toHaveBeenCalled());
    expect(mockTakeover.mock.calls[0][1]).toMatchObject({ preserve_legacy_labels: false });
  });

  it('never calls the takeover just by being opened', async () => {
    mockPreflight.mockResolvedValue(okReport);
    render(<TakeoverDialog clusterName="prod-eu" open onClose={() => {}} />);
    await waitFor(() => expect(mockPreflight).toHaveBeenCalled());
    expect(mockTakeover).not.toHaveBeenCalled();
  });

  it('surfaces a failed check run instead of showing a button', async () => {
    mockPreflight.mockRejectedValue(new Error('argocd unreachable'));
    render(<TakeoverDialog clusterName="prod-eu" open onClose={() => {}} />);

    await waitFor(() => expect(screen.getByTestId('takeover-error')).toHaveTextContent('argocd unreachable'));
    expect(screen.queryByTestId('takeover-confirm')).not.toBeInTheDocument();
  });

  // v4-wave2 review fix — a failed initial preflight fetch must offer a
  // way back in, not strand the user on a dead error box.
  it('a failed initial preflight fetch gets a Retry button that re-runs the check', async () => {
    mockPreflight.mockRejectedValueOnce(new Error('argocd unreachable')).mockResolvedValueOnce(okReport);
    render(<TakeoverDialog clusterName="prod-eu" open onClose={() => {}} />);

    await waitFor(() => expect(screen.getByTestId('takeover-error')).toBeInTheDocument());
    const retryBtn = screen.getByTestId('takeover-retry');
    await userEvent.click(retryBtn);

    await waitFor(() => expect(screen.getByTestId('takeover-summary')).toHaveTextContent('ready to take over'));
    expect(mockPreflight).toHaveBeenCalledTimes(2);
  });

  it('disables "Check again" while a check is already in flight', async () => {
    let resolveSecond: (v: TakeoverReport) => void = () => {};
    mockPreflight
      .mockResolvedValueOnce(blockedReport)
      .mockReturnValueOnce(new Promise((resolve) => { resolveSecond = resolve; }));
    render(<TakeoverDialog clusterName="prod-eu" open onClose={() => {}} />);

    await waitFor(() => expect(screen.getByTestId('takeover-recheck')).toBeInTheDocument());
    const recheckBtn = screen.getByTestId('takeover-recheck');
    expect(recheckBtn).not.toBeDisabled();

    await userEvent.click(recheckBtn);
    // The report-driven view unmounts while loading (report is still the
    // stale one and the spinner takes over), so re-querying confirms the
    // recheck button is gone rather than clickable again mid-flight.
    expect(screen.queryByTestId('takeover-recheck')).not.toBeInTheDocument();

    resolveSecond(okReport);
    await waitFor(() => expect(screen.getByTestId('takeover-recheck')).not.toBeDisabled());
    expect(mockPreflight).toHaveBeenCalledTimes(2);
  });
});

describe('DropLegacyLabelsDialog', () => {
  const planWithWarning: DropLegacyLabelsResponse = {
    cluster: 'prod-eu',
    status: 'planned',
    removed: ['env', 'team'],
    warnings: [
      'The ApplicationSet "legacy-addons" picks clusters using env. Remove that label and this cluster stops matching it — it uses the default behaviour, which deletes the Application and everything that Application installed.',
    ],
    message: 'Plan: remove 2 label(s) from prod-eu’s connection — env and team. Nothing has been changed.',
  };

  it('opens on a dry run, so nothing is removed by looking', async () => {
    mockDropLabels.mockResolvedValue(planWithWarning);
    render(<DropLegacyLabelsDialog clusterName="prod-eu" open onClose={() => {}} />);

    await waitFor(() => expect(mockDropLabels).toHaveBeenCalledWith('prod-eu', { dry_run: true }));
  });

  it('warns by name about anything that still picks clusters using the labels, and gates on it', async () => {
    mockDropLabels.mockResolvedValue(planWithWarning);
    render(<DropLegacyLabelsDialog clusterName="prod-eu" open onClose={() => {}} />);

    await waitFor(() => expect(screen.getByTestId('drop-labels-warnings')).toBeInTheDocument());
    expect(screen.getByTestId('drop-labels-warnings')).toHaveTextContent('legacy-addons');
    expect(screen.getByTestId('drop-labels-confirm')).toBeDisabled();

    await userEvent.click(screen.getByTestId('drop-labels-acknowledge'));
    expect(screen.getByTestId('drop-labels-confirm')).toBeEnabled();

    mockDropLabels.mockResolvedValueOnce({
      ...planWithWarning,
      status: 'success',
      message: 'Removed 2 label(s).',
    });
    await userEvent.click(screen.getByTestId('drop-labels-confirm'));

    await waitFor(() => expect(screen.getByTestId('drop-labels-result')).toBeInTheDocument());
    expect(mockDropLabels).toHaveBeenLastCalledWith('prod-eu', {
      yes: true,
      labels: ['env', 'team'],
      acknowledge_warnings: true,
    });
  });

  it('offers no button when there is nothing left to remove', async () => {
    mockDropLabels.mockResolvedValue({
      cluster: 'prod-eu',
      status: 'success',
      message: 'There are no carried-over labels left on prod-eu’s connection — there is nothing to remove.',
    } as DropLegacyLabelsResponse);
    render(<DropLegacyLabelsDialog clusterName="prod-eu" open onClose={() => {}} />);

    await waitFor(() => expect(screen.getByText(/nothing to remove/)).toBeInTheDocument());
    expect(screen.queryByTestId('drop-labels-confirm')).not.toBeInTheDocument();
  });

  // v4-wave2 review fix — a failed initial dry-run fetch gets a Retry
  // button rather than stranding the user on a dead error box.
  it('a failed initial dry-run fetch gets a Retry button that re-runs it', async () => {
    mockDropLabels
      .mockRejectedValueOnce(new Error('argocd unreachable'))
      .mockResolvedValueOnce(planWithWarning);
    render(<DropLegacyLabelsDialog clusterName="prod-eu" open onClose={() => {}} />);

    await waitFor(() => expect(screen.getByTestId('drop-labels-error')).toBeInTheDocument());
    await userEvent.click(screen.getByTestId('drop-labels-retry'));

    await waitFor(() => expect(screen.getByTestId('drop-labels-warnings')).toBeInTheDocument());
    expect(mockDropLabels).toHaveBeenCalledTimes(2);
  });
});

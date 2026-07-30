import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { UnregisterConsequencesPanel } from '@/components/UnregisterConsequencesPanel';
import type { UnregisterConsequencesResponse } from '@/services/models';

const mockConsequences = vi.fn();
vi.mock('@/services/api', () => ({
  unregisterConsequences: (...args: unknown[]) => mockConsequences(...args),
}));

// v4 Wave 2, story 6.5 — unregister with your eyes open. The panel must
// read the consequences out before the confirm, and a failure to read them
// must never look like "there are none".

const response: UnregisterConsequencesResponse = {
  cluster: 'prod-eu',
  summary: 'Unregistering prod-eu has 2 thing(s) worth reading carefully first. Nothing has been changed yet.',
  confirmation_required: 'Nothing here has happened yet. To go ahead, send DELETE …',
  consequences: [
    {
      id: 'git-record',
      title: 'Its files leave the repo',
      detail: 'A pull request removes this cluster from the fleet record and deletes its addon file.',
      what_it_means: 'Sharko stops treating it as one of your clusters.',
      severity: 'info',
    },
    {
      id: 'argocd-connection',
      title: 'Its ArgoCD connection gets deleted',
      detail: 'Sharko owns this cluster’s connection, so a full removal deletes it from ArgoCD.',
      what_it_means: 'ArgoCD stops being able to reach the cluster.',
      severity: 'warning',
    },
    {
      id: 'carried-over-labels',
      title: 'The labels carried over from the previous owner go with it',
      detail: 'This cluster’s connection still carries env and team.',
      what_it_means: 'These ApplicationSets pick clusters using those labels: legacy-addons.',
      severity: 'warning',
    },
  ],
};

beforeEach(() => {
  vi.clearAllMocks();
});

describe('UnregisterConsequencesPanel', () => {
  it('reads out every consequence with its meaning', async () => {
    mockConsequences.mockResolvedValue(response);
    render(<UnregisterConsequencesPanel clusterName="prod-eu" open />);

    await waitFor(() => expect(screen.getByTestId('unregister-consequences')).toBeInTheDocument());
    expect(screen.getByTestId('unregister-consequence-git-record')).toHaveAttribute('data-severity', 'info');
    expect(screen.getByTestId('unregister-consequence-argocd-connection')).toHaveAttribute('data-severity', 'warning');
    expect(screen.getByTestId('unregister-consequence-carried-over-labels')).toHaveTextContent('legacy-addons');
    expect(screen.getByText(/Nothing has been changed yet/)).toBeInTheDocument();
  });

  it('fetches nothing while the confirmation is closed', () => {
    mockConsequences.mockResolvedValue(response);
    render(<UnregisterConsequencesPanel clusterName="prod-eu" open={false} />);
    expect(mockConsequences).not.toHaveBeenCalled();
  });

  it('says it could not check rather than implying there is nothing to worry about', async () => {
    mockConsequences.mockRejectedValue(new Error('argocd unreachable'));
    render(<UnregisterConsequencesPanel clusterName="prod-eu" open />);

    await waitFor(() => expect(screen.getByTestId('unregister-consequences-error')).toBeInTheDocument());
    expect(screen.getByTestId('unregister-consequences-error')).toHaveTextContent('is not the same as "nothing"');
  });

  // ClusterDetail review fix — onReadyChange is what the parent confirm
  // dialog gates its confirm button on. Not ready while loading; ready
  // once the data OR the error has actually rendered.
  it('onReadyChange reports false while loading, then true once the consequences render', async () => {
    let resolveFetch: (v: UnregisterConsequencesResponse) => void = () => {};
    mockConsequences.mockReturnValue(new Promise((resolve) => { resolveFetch = resolve; }));
    const onReadyChange = vi.fn();
    render(<UnregisterConsequencesPanel clusterName="prod-eu" open onReadyChange={onReadyChange} />);

    await waitFor(() => expect(onReadyChange).toHaveBeenCalledWith(false));
    expect(onReadyChange).not.toHaveBeenCalledWith(true);

    resolveFetch(response);
    await waitFor(() => expect(onReadyChange).toHaveBeenCalledWith(true));
  });

  it('onReadyChange reports true once the error state has rendered — a failure still counts as "shown"', async () => {
    mockConsequences.mockRejectedValue(new Error('argocd unreachable'));
    const onReadyChange = vi.fn();
    render(<UnregisterConsequencesPanel clusterName="prod-eu" open onReadyChange={onReadyChange} />);

    await waitFor(() => expect(screen.getByTestId('unregister-consequences-error')).toBeInTheDocument());
    expect(onReadyChange).toHaveBeenCalledWith(true);
  });

  it('onReadyChange reports false while the confirmation is closed', () => {
    mockConsequences.mockResolvedValue(response);
    const onReadyChange = vi.fn();
    render(<UnregisterConsequencesPanel clusterName="prod-eu" open={false} onReadyChange={onReadyChange} />);
    expect(onReadyChange).toHaveBeenCalledWith(false);
    expect(mockConsequences).not.toHaveBeenCalled();
  });
});

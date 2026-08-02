import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import type { AddonState } from '@/hooks/useAddonStates';
import {
  splitAddonStates,
  getConfirmedAddonProblemCount,
  getConfirmedProblemCount,
  buildConfirmedAddonRows,
  buildSettlingAddonRows,
  buildUnknownAddonRows,
  buildClusterAttentionRows,
  buildDriftRows,
  hasOpenIssues,
  OpenIssuesBlock,
} from '@/components/AttentionSection';

function makeState(overrides: Partial<AddonState>): AddonState {
  return {
    appName: 'cert-manager-prod',
    addonName: 'cert-manager',
    cluster: 'prod',
    healthStatus: 'Degraded',
    syncStatus: 'Synced',
    displayState: 'degraded',
    lastSeen: Date.now(),
    ...overrides,
  };
}

describe('splitAddonStates (settling-window honesty)', () => {
  it('puts a freshly-bad addon (badSince = now) in settling, not confirmed', () => {
    const byApp = new Map([
      ['cert-manager@prod', makeState({ badSince: Date.now() })],
    ]);
    const { confirmed, settling } = splitAddonStates(byApp);
    expect(confirmed).toHaveLength(0);
    expect(settling).toHaveLength(1);
  });

  it('promotes an addon past the 10-minute settling window to confirmed', () => {
    const elevenMinutesAgo = Date.now() - 11 * 60 * 1000;
    const byApp = new Map([
      ['cert-manager@prod', makeState({ badSince: elevenMinutesAgo })],
    ]);
    const { confirmed, settling } = splitAddonStates(byApp);
    expect(confirmed).toHaveLength(1);
    expect(settling).toHaveLength(0);
  });

  it('buckets unknown and progressing-advisory separately, never as problems', () => {
    const byApp = new Map([
      ['a@c1', makeState({ addonName: 'a', cluster: 'c1', displayState: 'unknown' })],
      ['b@c2', makeState({ addonName: 'b', cluster: 'c2', displayState: 'progressing-advisory' })],
    ]);
    const { confirmed, settling, unknown, progressing } = splitAddonStates(byApp);
    expect(confirmed).toHaveLength(0);
    expect(settling).toHaveLength(0);
    expect(unknown).toHaveLength(1);
    expect(progressing).toHaveLength(1);
  });
});

describe('getConfirmedProblemCount (one truth, two mirrors)', () => {
  it('adds confirmed addon problems to the cluster problem count', () => {
    const elevenMinutesAgo = Date.now() - 11 * 60 * 1000;
    const byApp = new Map([
      ['cert-manager@prod', makeState({ badSince: elevenMinutesAgo })],
    ]);
    expect(getConfirmedAddonProblemCount(byApp)).toBe(1);
    expect(getConfirmedProblemCount(byApp, 2)).toBe(3);
  });

  it('is just the cluster count when there are no confirmed addon problems', () => {
    expect(getConfirmedProblemCount(new Map(), 2)).toBe(2);
  });
});

describe('row builders', () => {
  it('buildConfirmedAddonRows produces a problem-severity row with a deep link', () => {
    const rows = buildConfirmedAddonRows([
      makeState({ advisoryMessage: 'sync failed', errorType: 'SyncError' }),
    ]);
    expect(rows).toHaveLength(1);
    expect(rows[0].severity).toBe('problem');
    expect(rows[0].reason).toBe('SyncError: sync failed');
    expect(rows[0].link).toContain('/clusters/prod');
  });

  it('buildSettlingAddonRows mentions the grace window', () => {
    const rows = buildSettlingAddonRows([makeState({ badSince: Date.now() })]);
    expect(rows[0].severity).toBe('attention');
    expect(rows[0].reason).toMatch(/settling/i);
    expect(rows[0].reason).toMatch(/grace window/i);
  });

  it('buildUnknownAddonRows explains ArgoCD is not reporting', () => {
    const rows = buildUnknownAddonRows([makeState({ displayState: 'unknown' })]);
    expect(rows[0].reason).toMatch(/not reporting a health status/i);
  });

  it('buildClusterAttentionRows uses clusterStatus.ts vocabulary', () => {
    const rows = buildClusterAttentionRows([{ name: 'spoke-us', connectionStatus: 'Failed' }]);
    expect(rows[0].severity).toBe('problem');
    expect(rows[0].title).toBe('spoke-us');
    expect(rows[0].link).toBe('/clusters/spoke-us');
  });

  it('buildDriftRows names the cluster count', () => {
    const rows = buildDriftRows([{ addon: 'cert-manager', count: 3 }]);
    expect(rows[0].reason).toBe('Different versions deployed across 3 clusters.');
  });
});

describe('hasOpenIssues (shared OR-of-lengths, no re-count)', () => {
  it('is false when every row list is empty and clusterProblemCount is 0', () => {
    expect(
      hasOpenIssues({
        clusterAttentionRows: [],
        confirmedAddonRows: [],
        settlingAddonRows: [],
        unknownAddonRows: [],
        driftRows: [],
        clusterProblemCount: 0,
      }),
    ).toBe(false);
  });

  it('is true when only clusterProblemCount is non-zero', () => {
    expect(
      hasOpenIssues({
        clusterAttentionRows: [],
        confirmedAddonRows: [],
        settlingAddonRows: [],
        unknownAddonRows: [],
        driftRows: [],
        clusterProblemCount: 1,
      }),
    ).toBe(true);
  });
});

describe('OpenIssuesBlock (folded into Observability\'s Fleet Health section, walk day 3)', () => {
  function renderBlock(props: Partial<React.ComponentProps<typeof OpenIssuesBlock>> = {}) {
    const onNavigate = props.onNavigate ?? (() => {});
    return render(
      <MemoryRouter>
        <OpenIssuesBlock
          onNavigate={onNavigate}
          clusterAttentionRows={[]}
          confirmedAddonRows={[]}
          settlingAddonRows={[]}
          unknownAddonRows={[]}
          driftRows={[]}
          clusterProblemCount={0}
          {...props}
        />
      </MemoryRouter>,
    );
  }

  it('renders a muted "No open issues." line when there are no issues at all (never nothing)', () => {
    renderBlock();
    expect(screen.getByText('No open issues.')).toBeInTheDocument();
    expect(screen.queryByText('Open issues')).not.toBeInTheDocument();
    expect(screen.queryByText('Needs Attention')).not.toBeInTheDocument();
  });

  it('shows the "Open issues" heading and the cluster chip, expanding to the named row on click', () => {
    renderBlock({
      clusterProblemCount: 1,
      clusterAttentionRows: [
        { key: 'cluster-prod', severity: 'problem', title: 'prod', reason: 'ArgoCD tried to reach this cluster and failed.', link: '/clusters/prod' },
      ],
    });

    expect(screen.getByText('Open issues')).toBeInTheDocument();
    expect(screen.queryByText('Needs Attention')).not.toBeInTheDocument();
    expect(screen.queryByText('No open issues.')).not.toBeInTheDocument();
    const chip = screen.getByRole('button', { name: /1 disconnected cluster/i });
    fireEvent.click(chip);
    expect(screen.getByText('prod')).toBeInTheDocument();
  });

  it('"View in Clusters" calls onNavigate with the deep link', () => {
    const onNavigate = vi.fn();
    renderBlock({
      onNavigate,
      clusterProblemCount: 1,
      clusterAttentionRows: [
        { key: 'cluster-prod', severity: 'problem', title: 'prod', reason: 'x', link: '/clusters/prod' },
      ],
    });
    fireEvent.click(screen.getByRole('button', { name: /1 disconnected cluster/i }));
    fireEvent.click(screen.getByRole('button', { name: /view in clusters/i }));
    expect(onNavigate).toHaveBeenCalledWith('/clusters?status=disconnected');
  });
});

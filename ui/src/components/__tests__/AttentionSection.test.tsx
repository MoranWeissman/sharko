import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
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
  countOpenIssues,
  classifyAddonGroupProblem,
  describeAddonProblem,
  AttentionRowView,
  GRACE_PERIOD_TOOLTIP,
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

describe('getConfirmedProblemCount (one truth, two mirrors — Dashboard thin line + nav badges)', () => {
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

  it('buildSettlingAddonRows shows state + where + when it started, moves the grace explanation to a tooltip', () => {
    const rows = buildSettlingAddonRows([makeState({ cluster: 'prod', healthStatus: 'Degraded', badSince: Date.now() })]);
    expect(rows[0].severity).toBe('attention');
    expect(rows[0].reason).toMatch(/^Degraded on prod — started \d+m ago$/);
    expect(rows[0].reason).not.toMatch(/new — confirming/i);
    expect(rows[0].reason).not.toMatch(/grace period/i);
    expect(rows[0].reason).not.toMatch(/settling/i);
    expect(rows[0].reason).not.toMatch(/grace window/i);
    expect(rows[0].tooltip).toBe(GRACE_PERIOD_TOOLTIP);
  });

  it('buildUnknownAddonRows leads with "no status reported"', () => {
    const rows = buildUnknownAddonRows([makeState({ displayState: 'unknown' })]);
    expect(rows[0].reason).toMatch(/^no status reported/i);
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

describe('hasOpenIssues / countOpenIssues (shared, no re-derivation)', () => {
  const empty = {
    clusterAttentionRows: [],
    confirmedAddonRows: [],
    settlingAddonRows: [],
    unknownAddonRows: [],
    driftRows: [],
    clusterProblemCount: 0,
  };

  it('hasOpenIssues is false when every row list is empty and clusterProblemCount is 0', () => {
    expect(hasOpenIssues(empty)).toBe(false);
    expect(countOpenIssues(empty)).toBe(0);
  });

  it('hasOpenIssues is true when only clusterProblemCount is non-zero', () => {
    expect(hasOpenIssues({ ...empty, clusterProblemCount: 1 })).toBe(true);
  });

  it('countOpenIssues sums every problem kind (cluster + confirmed + settling + unknown + drift)', () => {
    const rows = buildConfirmedAddonRows([makeState({})]);
    const total = countOpenIssues({
      ...empty,
      clusterProblemCount: 2,
      confirmedAddonRows: rows,
      settlingAddonRows: buildSettlingAddonRows([makeState({ addonName: 'b', cluster: 'c2', badSince: Date.now() })]),
      driftRows: buildDriftRows([{ addon: 'x', count: 2 }]),
    });
    expect(total).toBe(2 + 1 + 1 + 0 + 1);
  });
});

describe('classifyAddonGroupProblem (drives problem-tier-first sorting + the group header)', () => {
  const group = {
    addon_name: 'cert-manager',
    child_apps: [{ cluster_name: 'prod' }, { cluster_name: 'staging' }],
  };

  it('is "none" when no child app is in the state map', () => {
    const result = classifyAddonGroupProblem(group, new Map());
    expect(result.tier).toBe('none');
    expect(result.state).toBeNull();
    expect(result.extraCount).toBe(0);
  });

  it('is "confirmed" when a child is degraded past the settling window', () => {
    const elevenMinutesAgo = Date.now() - 11 * 60 * 1000;
    const byApp = new Map([
      ['cert-manager@prod', makeState({ cluster: 'prod', badSince: elevenMinutesAgo })],
    ]);
    const result = classifyAddonGroupProblem(group, byApp);
    expect(result.tier).toBe('confirmed');
    expect(result.state?.cluster).toBe('prod');
    expect(result.extraCount).toBe(0);
  });

  it('is "grace" (not confirmed) when a child is freshly degraded', () => {
    const byApp = new Map([
      ['cert-manager@prod', makeState({ cluster: 'prod', badSince: Date.now() })],
    ]);
    const result = classifyAddonGroupProblem(group, byApp);
    expect(result.tier).toBe('grace');
  });

  it('is "unknown" when a child has no status', () => {
    const byApp = new Map([
      ['cert-manager@prod', makeState({ cluster: 'prod', displayState: 'unknown', badSince: undefined })],
    ]);
    const result = classifyAddonGroupProblem(group, byApp);
    expect(result.tier).toBe('unknown');
  });

  it('picks the worst tier across children and counts the rest as extraCount', () => {
    const elevenMinutesAgo = Date.now() - 11 * 60 * 1000;
    const byApp = new Map([
      ['cert-manager@prod', makeState({ cluster: 'prod', badSince: elevenMinutesAgo })],
      ['cert-manager@staging', makeState({ cluster: 'staging', displayState: 'unknown', badSince: undefined })],
    ]);
    const result = classifyAddonGroupProblem(group, byApp);
    expect(result.tier).toBe('confirmed');
    expect(result.extraCount).toBe(1);
  });

  it('never counts healthy or progressing-advisory children as a problem', () => {
    const byApp = new Map([
      ['cert-manager@prod', makeState({ cluster: 'prod', displayState: 'healthy', badSince: undefined })],
      ['cert-manager@staging', makeState({ cluster: 'staging', displayState: 'progressing-advisory', badSince: undefined })],
    ]);
    const result = classifyAddonGroupProblem(group, byApp);
    expect(result.tier).toBe('none');
  });

  it('falls back to the byAppName index when the primary addon@cluster key misses (live-bug case: the feed carries the full app name as addonName)', () => {
    const groupWithAppNames = {
      addon_name: 'metrics-server',
      child_apps: [{ cluster_name: 'spoke-eu', app_name: 'metrics-server-spoke-eu' }],
    };
    // The feed's own key ("metrics-server-spoke-eu@spoke-eu") doesn't match
    // the primary key ("metrics-server@spoke-eu") the group needs — only
    // the byAppName index (keyed by the feed item's real app name) rescues
    // the match.
    const badState = makeState({
      addonName: 'metrics-server-spoke-eu',
      appName: 'metrics-server-spoke-eu',
      cluster: 'spoke-eu',
      badSince: Date.now() - 11 * 60 * 1000,
    });
    const byApp = new Map([['metrics-server-spoke-eu@spoke-eu', badState]]);
    const byAppName = new Map([['metrics-server-spoke-eu', badState]]);

    expect(classifyAddonGroupProblem(groupWithAppNames, byApp).tier).toBe('none');
    const result = classifyAddonGroupProblem(groupWithAppNames, byApp, byAppName);
    expect(result.tier).toBe('confirmed');
    expect(result.state?.cluster).toBe('spoke-eu');
  });
});

describe('describeAddonProblem (plain-words inline reason for a problem addon group)', () => {
  it('confirmed: names the cluster and detail, no "confirming" suffix', () => {
    const state = makeState({ cluster: 'spoke-eu', advisoryMessage: 'pod not ready', badSince: Date.now() - 20 * 60 * 1000 });
    const { text, tooltip } = describeAddonProblem(state, 'confirmed');
    expect(text).toMatch(/^degraded on spoke-eu — pod not ready/i);
    expect(text).not.toMatch(/confirming/i);
    expect(tooltip).toBeUndefined();
  });

  it('grace: drops the "new — confirming" phrase from the visible text, keeps it in the tooltip only', () => {
    const state = makeState({ cluster: 'spoke-eu', advisoryMessage: 'pod not ready', badSince: Date.now() - 4 * 60 * 1000 });
    const { text, tooltip } = describeAddonProblem(state, 'grace');
    expect(text).toMatch(/pod not ready/);
    expect(text).not.toMatch(/new — confirming/i);
    expect(text).not.toMatch(/settling/i);
    expect(tooltip).toBe(GRACE_PERIOD_TOOLTIP);
  });

  it('unknown: says "no status reported" and nothing else', () => {
    const state = makeState({ cluster: 'spoke-eu', displayState: 'unknown', badSince: undefined });
    const { text, tooltip } = describeAddonProblem(state, 'unknown');
    expect(text).toBe('no status reported on spoke-eu');
    expect(tooltip).toBeUndefined();
  });
});

describe('AttentionRowView (compact rows for cluster problems, drift, and orphan addon problems)', () => {
  it('renders the title as a link to the row\'s deep link, plus the reason', () => {
    render(
      <MemoryRouter>
        <AttentionRowView
          row={{ key: 'cluster-prod', severity: 'problem', title: 'prod', reason: 'ArgoCD tried to reach this cluster and failed.', link: '/clusters/prod' }}
        />
      </MemoryRouter>,
    );
    const link = screen.getByText('prod');
    expect(link.closest('a')).toHaveAttribute('href', '/clusters/prod');
    expect(screen.getByText('ArgoCD tried to reach this cluster and failed.')).toBeInTheDocument();
  });
});

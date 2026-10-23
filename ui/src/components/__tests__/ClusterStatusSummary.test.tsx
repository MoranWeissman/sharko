import { describe, it, expect } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import {
  ClusterStatusSummary,
  clusterStatusParts,
  compositeClusterStatus,
  type ClusterStatusPart,
} from '@/components/ClusterStatusSummary';
import { SEVERITY_ORDER, worstSeverity } from '@/lib/clusterStatus';

// V2-cleanup-61.2 (finding D4): one composite pill per cluster row; the
// composite state is the WORST of the parts, per an explicit order.

function part(severity: ClusterStatusPart['severity'], label: string): ClusterStatusPart {
  return { who: 'test', label, severity, meaning: `${label} meaning` };
}

describe('composite worst-of ordering', () => {
  it('the severity order is explicit: problem > attention > pending > unknown > good', () => {
    expect([...SEVERITY_ORDER]).toEqual(['problem', 'attention', 'pending', 'unknown', 'good']);
  });

  it('problem beats everything', () => {
    const winner = compositeClusterStatus([
      part('good', 'Connected'),
      part('attention', 'Test failing'),
      part('problem', 'Disconnected'),
      part('pending', 'Running…'),
    ]);
    expect(winner.label).toBe('Disconnected');
  });

  it('attention beats pending/unknown/good', () => {
    const winner = compositeClusterStatus([
      part('good', 'Connected'),
      part('pending', 'Running…'),
      part('attention', 'Deploy check failed'),
    ]);
    expect(winner.label).toBe('Deploy check failed');
  });

  it('pending beats good', () => {
    const winner = compositeClusterStatus([
      part('good', 'Reachable'),
      part('pending', 'Connecting…'),
    ]);
    expect(winner.label).toBe('Connecting…');
  });

  it('all-good composites resolve to the first good part', () => {
    const winner = compositeClusterStatus([
      part('good', 'Connected'),
      part('good', 'Reachable'),
    ]);
    expect(winner.label).toBe('Connected');
  });

  it('worstSeverity of an empty list is good', () => {
    expect(worstSeverity([])).toBe('good');
  });
});

describe('clusterStatusParts', () => {
  it('always includes the ArgoCD connection part', () => {
    const parts = clusterStatusParts({ connectionStatus: 'Successful' });
    expect(parts[0].who).toBe('ArgoCD → cluster');
    expect(parts[0].label).toBe('Connected');
    expect(parts[0].severity).toBe('good');
  });

  it('adds pending/failed deploy-check parts when they apply', () => {
    const parts = clusterStatusParts({
      connectionStatus: 'Successful',
      connectivityStatus: 'check_failed',
      connectivityDetail: 'pod stuck',
      sharkoStatus: 'Connected',
      lastTestAt: new Date().toISOString(),
      testFailing: true,
      testErrorCode: 'ERR_NETWORK',
    });
    expect(parts.map((p) => p.label)).toEqual(['Connected', 'Failed', 'Test failing']);
    expect(parts[1].meaning).toBe('pod stuck');
    expect(parts[2].meaning).toContain('ERR_NETWORK');
  });

  it('lists all three lines on every cluster: ArgoCD connection, deploy check, Sharko test', () => {
    // Ep3 dry run, finding 17: the docs promise the full breakdown. A line
    // with nothing to show says so, instead of leaving the line out.
    const bare = clusterStatusParts({ connectionStatus: 'Successful' });
    expect(bare.map((p) => p.who)).toEqual(['ArgoCD → cluster', 'Deploy check', 'Sharko → cluster']);
    expect(bare[1].label).toBe('Not run');
    expect(bare[1].meaning).toBe('No deploy check has run for this cluster.');
    expect(bare[2].label).toBe('Not run');
    expect(bare[2].meaning).toBe(
      'Sharko has not tested this cluster yet. Use Test connection to run it.',
    );
    expect(bare[1].informational).toBe(true);
    expect(bare[2].informational).toBe(true);

    const full = clusterStatusParts({
      connectionStatus: 'Successful',
      connectivityStatus: 'verified_check',
      sharkoStatus: 'Connected',
      lastTestAt: new Date().toISOString(),
    });
    expect(full.map((p) => p.label)).toEqual(['Connected', 'Passed', 'Reachable']);
    expect(full.every((p) => p.label !== 'Verified')).toBe(true);
  });

  it('a "Not run" line never changes what the pill says', () => {
    const parts = clusterStatusParts({ connectionStatus: 'Successful' });
    expect(compositeClusterStatus(parts).label).toBe('Connected');
    expect(compositeClusterStatus(parts).severity).toBe('good');
  });

  it('the test the user just ran on this page fills the Sharko line', () => {
    const passed = clusterStatusParts({
      connectionStatus: 'Successful',
      latestTest: { success: true, passed: 6, total: 6 },
    });
    expect(passed[2].label).toBe('Reachable');
    expect(passed[2].meaning).toBe('Sharko can reach this cluster directly. 6 of 6 checks passed.');
    expect(passed[2].informational).toBeUndefined();

    const failed = clusterStatusParts({
      connectionStatus: 'Successful',
      latestTest: { success: false, passed: 2, total: 6, failedStep: 'Read namespaces' },
    });
    expect(failed[2].label).toBe('Test failing');
    expect(failed[2].meaning).toBe(
      "Sharko's last direct connection test failed at: Read namespaces. 2 of 6 checks passed.",
    );
    expect(compositeClusterStatus(failed).label).toBe('Test failing');
  });

  it('still surfaces pending deploy-check state', () => {
    const parts = clusterStatusParts({
      connectionStatus: 'Successful',
      connectivityStatus: 'check_pending',
      connectivityDetail: 'waiting for pod',
    });
    expect(parts.map((p) => p.label)).toEqual(['Connected', 'Running…', 'Not run']);
    expect(parts[1].meaning).toBe('waiting for pod');
  });

  // v4 walk-findings W2, item 2: a managed cluster with zero enabled addons
  // never gets an ArgoCD probe result, so it sat at "Connecting…" forever.
  it('shows "No addons yet" instead of "Connecting…" when enabledAddonCount is 0', () => {
    const parts = clusterStatusParts({ connectionStatus: '', enabledAddonCount: 0 });
    expect(parts[0].label).toBe('Not checked yet');
    expect(parts[0].label).not.toContain('No addons yet');
    expect(parts[0].severity).toBe('unknown');
  });

  it('still shows "Connecting…" when enabledAddonCount is greater than 0', () => {
    const parts = clusterStatusParts({ connectionStatus: '', enabledAddonCount: 2 });
    expect(parts[0].label).toBe('Connecting…');
  });

  it('still shows "Connecting…" when enabledAddonCount is not passed at all', () => {
    const parts = clusterStatusParts({ connectionStatus: 'Unknown' });
    expect(parts[0].label).toBe('Connecting…');
  });

  it('never uses internal Stage vocabulary in any meaning', () => {
    const parts = clusterStatusParts({
      connectionStatus: 'Failed',
      connectivityStatus: 'verified_check',
      sharkoStatus: 'Connected',
      lastTestAt: new Date().toISOString(),
    });
    for (const p of parts) {
      expect(p.meaning).not.toMatch(/stage\s*\d/i);
    }
  });
});

describe('<ClusterStatusSummary />', () => {
  it('the popover shows ArgoCD connection, deploy check and Sharko test after Test connection passed', async () => {
    render(
      <ClusterStatusSummary
        connectionStatus="Successful"
        latestTest={{ success: true, passed: 6, total: 6 }}
      />,
    );
    fireEvent.click(screen.getByTestId('cluster-status-pill'));
    await waitFor(() => {
      expect(screen.getByText('ArgoCD → cluster')).toBeInTheDocument();
    });
    expect(screen.getByText('Deploy check')).toBeInTheDocument();
    expect(screen.getByText('Sharko → cluster')).toBeInTheDocument();
    expect(
      screen.getByText('Sharko can reach this cluster directly. 6 of 6 checks passed.'),
    ).toBeInTheDocument();
  });

  it('renders ONE pill showing the worst part, with details in an accessible popover', async () => {
    render(
      <ClusterStatusSummary
        connectionStatus="Successful"
        connectivityStatus="check_failed"
        connectivityDetail="test workload degraded"
        sharkoStatus="Connected"
        lastTestAt={new Date().toISOString()}
      />,
    );

    // The pill shows the worst part's label (check_failed → attention).
    const pill = screen.getByTestId('cluster-status-pill');
    expect(pill).toHaveTextContent('Failed');
    // Details are NOT visible until the popover opens.
    expect(screen.queryByText('test workload degraded')).not.toBeInTheDocument();

    // The trigger is a real button — keyboard/touch reachable.
    fireEvent.click(pill);
    await waitFor(() => {
      expect(screen.getByText('test workload degraded')).toBeInTheDocument();
    });
    // The breakdown names each part's owner.
    expect(screen.getByText('ArgoCD → cluster')).toBeInTheDocument();
    expect(screen.getByText('Sharko → cluster')).toBeInTheDocument();
  });

  it('shows the self-managed connection note only when connectionManagedBy is user', async () => {
    render(
      <ClusterStatusSummary connectionStatus="Successful" connectionManagedBy="user" />,
    );
    fireEvent.click(screen.getByTestId('cluster-status-pill'));
    await waitFor(() => {
      expect(screen.getByText(/connection: managed by you/)).toBeInTheDocument();
    });
  });
});

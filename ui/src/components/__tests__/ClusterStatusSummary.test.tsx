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
    expect(parts).toHaveLength(1);
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

  it('does NOT add a standing "Verified" success row when the cluster is simply connected', () => {
    // W11: the always-on "Deploy check / Verified" row was redundant with the
    // ArgoCD-connection part. A connected cluster shows the one fact.
    const partsWithoutSharko = clusterStatusParts({
      connectionStatus: 'Successful',
      connectivityStatus: 'verified_argocd',
    });
    expect(partsWithoutSharko).toHaveLength(1);
    expect(partsWithoutSharko[0].label).toBe('Connected');
    expect(partsWithoutSharko.every((p) => p.label !== 'Verified')).toBe(true);

    const partsWithSharko = clusterStatusParts({
      connectionStatus: 'Successful',
      connectivityStatus: 'verified_check',
      sharkoStatus: 'Connected',
      lastTestAt: new Date().toISOString(),
    });
    expect(partsWithSharko).toHaveLength(2); // ArgoCD-connection + Sharko-direct
    expect(partsWithSharko.map((p) => p.label)).toEqual(['Connected', 'Reachable']);
    expect(partsWithSharko.every((p) => p.label !== 'Verified')).toBe(true);
  });

  it('still surfaces pending deploy-check state', () => {
    const parts = clusterStatusParts({
      connectionStatus: 'Successful',
      connectivityStatus: 'check_pending',
      connectivityDetail: 'waiting for pod',
    });
    expect(parts.map((p) => p.label)).toEqual(['Connected', 'Running…']);
    expect(parts[1].meaning).toBe('waiting for pod');
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

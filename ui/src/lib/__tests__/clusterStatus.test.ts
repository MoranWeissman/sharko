import { describe, it, expect } from 'vitest';
import {
  classifyClusterConnection,
  isClusterConnected,
  isClusterFailed,
  isClusterNeedsAttention,
} from '@/lib/clusterStatus';

// BUG-033 regression coverage. The previous UI logic treated ANY value
// other than "Successful" / "Connected" as a hard "Disconnected"
// failure, which mis-classified the transient post-registration window
// (ArgoCD has the cluster Secret but has not yet run a connection
// probe → empty/Unknown status). These tests pin the classification so a
// future refactor can't quietly regress the registration UX.
//
// V2-cleanup-75.1 split "missing"/"missing_from_argocd" out of 'pending'
// into their own 'missing' state: a cluster ArgoCD has NO connection
// secret for at all was being shown as the calm "Connecting…" pending
// state forever, with no explanation. See the 'missing' describe block
// below for the new contract.
describe('classifyClusterConnection (BUG-033)', () => {
  describe('connected', () => {
    it('maps "Successful" to connected', () => {
      expect(classifyClusterConnection('Successful')).toBe('connected');
    });
    it('maps "Connected" to connected', () => {
      expect(classifyClusterConnection('Connected')).toBe('connected');
    });
    it('is case-insensitive', () => {
      expect(classifyClusterConnection('successful')).toBe('connected');
      expect(classifyClusterConnection('CONNECTED')).toBe('connected');
    });
  });

  describe('pending (BUG-033 — post-registration window)', () => {
    it('maps empty string to pending — ArgoCD has not probed yet', () => {
      expect(classifyClusterConnection('')).toBe('pending');
    });
    it('maps null to pending', () => {
      expect(classifyClusterConnection(null)).toBe('pending');
    });
    it('maps undefined to pending', () => {
      expect(classifyClusterConnection(undefined)).toBe('pending');
    });
    it('maps "Unknown" to pending', () => {
      expect(classifyClusterConnection('Unknown')).toBe('pending');
    });
    it('treats leading/trailing whitespace as pending — defensive parse', () => {
      expect(classifyClusterConnection('   ')).toBe('pending');
    });
  });

  describe('missing (V2-cleanup-75.1 — ArgoCD has no connection at all)', () => {
    it('maps "missing" to missing — distinct from the transient pending window', () => {
      expect(classifyClusterConnection('missing')).toBe('missing');
    });
    it('maps "missing_from_argocd" to missing', () => {
      expect(classifyClusterConnection('missing_from_argocd')).toBe('missing');
    });
    it('is case-insensitive', () => {
      expect(classifyClusterConnection('MISSING')).toBe('missing');
      expect(classifyClusterConnection('Missing_From_ArgoCD')).toBe('missing');
    });
  });

  describe('failed', () => {
    it('maps "Failed" to failed', () => {
      expect(classifyClusterConnection('Failed')).toBe('failed');
    });
    it('treats any unknown explicit status as failed (safe fallback)', () => {
      // We do NOT silently green-light a status we don't recognise —
      // surface it as an attention item so an operator can investigate.
      expect(classifyClusterConnection('SomeFutureArgoCDState')).toBe('failed');
    });
  });
});

describe('isClusterConnected (BUG-033)', () => {
  it('returns true only for green-checkmark states', () => {
    expect(isClusterConnected('Successful')).toBe(true);
    expect(isClusterConnected('Connected')).toBe(true);
  });
  it('returns false for pending states — no premature green', () => {
    expect(isClusterConnected('')).toBe(false);
    expect(isClusterConnected('Unknown')).toBe(false);
  });
  it('returns false for missing states', () => {
    expect(isClusterConnected('missing')).toBe(false);
    expect(isClusterConnected('missing_from_argocd')).toBe(false);
  });
  it('returns false for failed states', () => {
    expect(isClusterConnected('Failed')).toBe(false);
  });
});

describe('isClusterFailed (BUG-033)', () => {
  it('returns true only for explicit failure states', () => {
    expect(isClusterFailed('Failed')).toBe(true);
  });
  it('returns false for pending states — this is the BUG-033 contract', () => {
    // The whole point of BUG-033's fix: a cluster mid-registration is
    // NOT a failure, even though it isn't yet "Successful".
    expect(isClusterFailed('')).toBe(false);
    expect(isClusterFailed('Unknown')).toBe(false);
    expect(isClusterFailed(null)).toBe(false);
    expect(isClusterFailed(undefined)).toBe(false);
  });
  it('returns false for missing states (V2-cleanup-75.1 — "missing" is its own amber state, not "failed" red)', () => {
    expect(isClusterFailed('missing')).toBe(false);
    expect(isClusterFailed('missing_from_argocd')).toBe(false);
  });
  it('returns false for connected states', () => {
    expect(isClusterFailed('Successful')).toBe(false);
    expect(isClusterFailed('Connected')).toBe(false);
  });
});

describe('isClusterNeedsAttention (V2-cleanup-75.1)', () => {
  it('returns true for failed states', () => {
    expect(isClusterNeedsAttention('Failed')).toBe(true);
  });
  it('returns true for missing states — ArgoCD has no connection at all', () => {
    expect(isClusterNeedsAttention('missing')).toBe(true);
    expect(isClusterNeedsAttention('missing_from_argocd')).toBe(true);
  });
  it('returns false for pending states — the normal post-registration wait', () => {
    expect(isClusterNeedsAttention('')).toBe(false);
    expect(isClusterNeedsAttention('Unknown')).toBe(false);
  });
  it('returns false for connected states', () => {
    expect(isClusterNeedsAttention('Successful')).toBe(false);
    expect(isClusterNeedsAttention('Connected')).toBe(false);
  });
  it('returns false for unmanaged states — not broken, just not adopted', () => {
    expect(isClusterNeedsAttention('not_in_git')).toBe(false);
  });
});

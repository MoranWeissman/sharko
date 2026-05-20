import { describe, it, expect } from 'vitest';
import {
  classifyClusterConnection,
  isClusterConnected,
  isClusterFailed,
} from '@/lib/clusterStatus';

// BUG-033 regression coverage. The previous UI logic treated ANY value
// other than "Successful" / "Connected" as a hard "Disconnected"
// failure, which mis-classified the transient post-registration window
// (ArgoCD has the cluster Secret but has not yet run a connection
// probe → empty/Unknown/missing status). These tests pin the new
// three-state classification so a future refactor can't quietly
// regress the registration UX.
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
    it('maps "missing" to pending — backend value when cluster is in Git but not yet in ArgoCD', () => {
      expect(classifyClusterConnection('missing')).toBe('pending');
    });
    it('maps "missing_from_argocd" to pending', () => {
      expect(classifyClusterConnection('missing_from_argocd')).toBe('pending');
    });
    it('treats leading/trailing whitespace as pending — defensive parse', () => {
      expect(classifyClusterConnection('   ')).toBe('pending');
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
    expect(isClusterConnected('missing')).toBe(false);
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
    expect(isClusterFailed('missing')).toBe(false);
    expect(isClusterFailed('missing_from_argocd')).toBe(false);
    expect(isClusterFailed(null)).toBe(false);
    expect(isClusterFailed(undefined)).toBe(false);
  });
  it('returns false for connected states', () => {
    expect(isClusterFailed('Successful')).toBe(false);
    expect(isClusterFailed('Connected')).toBe(false);
  });
});

import { describe, it, expect } from 'vitest';
import {
  EVENT_LABELS,
  MAPPED_EVENT_COUNT,
  actionSentence,
  deSnakeCase,
  eventPhrase,
  parseResource,
  RESULT_OPTIONS,
  resultLabel,
} from '@/views/audit/auditLabels';

describe('auditLabels', () => {
  it('maps a known event code to its friendly phrase', () => {
    expect(EVENT_LABELS['addon_enabled_on_cluster']).toBe('enabled an addon on a cluster');
    expect(eventPhrase('cluster_registered')).toBe('registered a cluster');
  });

  it('de-snake-cases an unknown code (never blank)', () => {
    expect(eventPhrase('addon_enabled_on_cluster_extra')).toBe('Addon enabled on cluster extra');
    expect(deSnakeCase('mystery_thing_happened')).toBe('Mystery thing happened');
    expect(deSnakeCase('cluster.test')).toBe('Cluster test');
    expect(eventPhrase('weird_unmapped_code')).not.toBe('');
  });

  it('never returns blank, even for empty/undefined input', () => {
    expect(eventPhrase('')).toBe('Performed an action');
    expect(eventPhrase(undefined)).toBe('Performed an action');
  });

  it('parses cluster/addon resources into a readable target', () => {
    expect(parseResource('cluster:prod-eu addon:cert-manager')).toBe('cert-manager on prod-eu');
    expect(parseResource('addon:cert-manager')).toBe('cert-manager');
    expect(parseResource('cluster:prod-eu')).toBe('prod-eu');
    expect(parseResource('clusters:3')).toBe('3 clusters');
    expect(parseResource('just-a-name')).toBe('just-a-name');
    expect(parseResource('')).toBe('');
  });

  it('composes a full action sentence', () => {
    expect(
      actionSentence({ user: 'alice', event: 'addon_enabled_on_cluster', resource: 'cluster:prod-eu addon:cert-manager' }),
    ).toBe('alice enabled an addon on a cluster — cert-manager on prod-eu');
    // anonymous → "Someone"
    expect(actionSentence({ user: 'anonymous', event: 'login' })).toBe('Someone signed in');
  });

  it('exposes exactly the real result values', () => {
    expect(RESULT_OPTIONS.map((o) => o.value)).toEqual(['', 'success', 'partial', 'rejected', 'failure']);
    expect(resultLabel('rejected')).toBe('Rejected');
    expect(resultLabel(undefined)).toBe('—');
  });

  it('maps each result to a plain-English word (V2-cleanup-85.3)', () => {
    expect(resultLabel('success')).toBe('Succeeded');
    expect(resultLabel('partial')).toBe('Partly done');
    expect(resultLabel('failure')).toBe('Failed');
    expect(resultLabel('rejected')).toBe('Rejected');
    // Legacy value still buffered from before 85.2 — grouped with Failed.
    expect(resultLabel('error')).toBe('Failed');
  });

  it('matches the plain-English words in the Result filter options', () => {
    expect(RESULT_OPTIONS.map((o) => o.label)).toEqual([
      'All results',
      'Succeeded',
      'Partly done',
      'Rejected',
      'Failed',
    ]);
  });

  it('maps a meaningful number of event codes', () => {
    expect(MAPPED_EVENT_COUNT).toBeGreaterThanOrEqual(50);
  });
});

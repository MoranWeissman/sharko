/**
 * V124-3.1 — strict-mode dispatch count guard for ClustersOverview.
 *
 * Why this lives in its own file: we mock the `react` module at top level so
 * the wrapped `useState` is wired in BEFORE ClustersOverview imports React.
 * Mixing that mock into the main test file would affect every other test in
 * the suite (and several of them rely on real React.useState semantics for
 * unrelated render shapes), so we isolate it here.
 *
 * What we assert: the failed-background-refresh catch block in
 * ClustersOverview.fetchData must dispatch its setError side effect EXACTLY
 * ONCE per failed refresh, including under StrictMode.
 *
 * Why one-per-fetch matters: the V124-3.1 anti-pattern called setError from
 * inside a setState updater. Under React 18 StrictMode that updater was
 * double-invoked in dev, dispatching setError twice per failed fetch — even
 * though React's bailout-on-equal-value hid the visual symptom. The fix moves
 * the dispatch outside any updater. We codify the contract here so the
 * relationship "fetches == setError dispatches" is enforced regardless of the
 * React version's specific StrictMode rules.
 *
 * The dispatch count is observed by wrapping React.useState via vi.mock and
 * tagging any string-valued dispatch as a setError call (the only string-
 * typed useState slot in ClustersOverview is `error: string | null`).
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

// Capture every string-valued setState dispatch. The only `string | null`
// useState slot in ClustersOverview is `error`, so any string dispatch is a
// setError call. We populate this from inside the wrapped useState below.
const stringDispatches: string[] = [];

vi.mock('react', async () => {
  type ReactModule = typeof import('react');
  const actual = (await vi.importActual('react')) as ReactModule;
  const wrappedUseState = (<S,>(
    initial?: S | (() => S),
  ): [S, import('react').Dispatch<import('react').SetStateAction<S>>] => {
    const [value, setValue] = actual.useState<S>(initial as S);
    const wrapped = ((next: import('react').SetStateAction<S>) => {
      if (typeof next === 'string') {
        stringDispatches.push(next);
      }
      return setValue(next);
    }) as import('react').Dispatch<import('react').SetStateAction<S>>;
    return [value, wrapped];
  }) as ReactModule['useState'];
  return {
    ...actual,
    useState: wrappedUseState,
  };
});

vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom');
  return {
    ...actual,
    useNavigate: () => vi.fn(),
  };
});

const mockGetClusters = vi.fn();
vi.mock('@/services/api', () => ({
  api: {
    getClusters: (...args: unknown[]) => mockGetClusters(...args),
  },
}));

describe('ClustersOverview — V124-3.1 StrictMode dispatch count', () => {
  beforeEach(() => {
    stringDispatches.length = 0;
    mockGetClusters.mockReset();
  });

  it('dispatches setError exactly once per failed background refresh', async () => {
    // Imported lazily so the react mock above is fully wired before
    // ClustersOverview captures the useState reference.
    const { StrictMode } = await import('react');
    const { ClustersOverview } = await import('@/views/ClustersOverview');

    // No prior data → the failed refresh must surface the error. This is the
    // exact code path that ran the impure-updater anti-pattern.
    mockGetClusters.mockRejectedValue(new Error('strict-once'));

    render(
      <StrictMode>
        <MemoryRouter>
          <ClustersOverview />
        </MemoryRouter>
      </StrictMode>,
    );

    await waitFor(() => {
      expect(screen.getByText('strict-once')).toBeInTheDocument();
    });

    // StrictMode double-mounts useEffect in dev, so fetchData itself fires
    // twice on initial mount → two distinct catch blocks → two setError
    // dispatches is expected and OK. What we're guarding is that EACH
    // individual catch block dispatches setError EXACTLY ONCE — i.e. the
    // dispatch count tracks the fetch count, never doubles it. A regression
    // that re-introduces side effects inside a state-updater function would
    // (under React versions that double-invoke updaters in StrictMode) push
    // dispatches above fetchCalls.
    const fetchCalls = mockGetClusters.mock.calls.length;
    const errorDispatches = stringDispatches.filter(
      (v) => v === 'strict-once',
    ).length;
    expect(errorDispatches).toBe(fetchCalls);
  });
});

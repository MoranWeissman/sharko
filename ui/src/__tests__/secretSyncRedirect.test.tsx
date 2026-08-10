// secretSyncRedirect.test.tsx — Secrets-area rename (SN-1/SN-7) route
// coverage.
//
// The Secrets area has two real subpages (/secrets/connections and
// /secrets/addons) and two detail routes. Every old URL keeps working:
//
//   /secrets                  → /secrets/connections (query kept)
//   /secret-sync              → /secrets/connections (query kept);
//                               an old ?kind=values link → /secrets/addons,
//                               with the kind param dropped
//   /secret-sync/<row key>    → the right detail route for all three key
//                               prefixes; an unknown key → the Cluster
//                               connections inventory
//
// This mounts ConnectedApp with a real Outlet-rendering Layout stub (so
// child routes actually render), a mocked ManagedSecrets view that echoes
// its `area` prop and its search params, and a mocked SecretDetailPage
// that echoes its route params. The legacy detail redirect
// (LegacySecretRedirect.tsx) runs for REAL — resolving a `values-` or
// `orphaned-` key needs the managed-secrets list (hyphens make the split
// ambiguous by text alone), so getManagedSecrets is mocked with a fixture
// whose names contain hyphens on purpose.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Outlet, useParams, useSearchParams } from 'react-router-dom'

const mockState = vi.hoisted(() => ({
  connections: [{ name: 'default' }] as Array<Record<string, unknown>>,
}))

const mockGetManagedSecrets = vi.hoisted(() => vi.fn())

vi.mock('@/hooks/useConnections', () => ({
  useConnections: () => ({
    connections: mockState.connections,
    activeConnection: mockState.connections[0]?.name ?? null,
    setActiveConnection: vi.fn(),
    loading: false,
    error: null,
    refreshConnections: vi.fn(),
  }),
}))

vi.mock('@/services/api', () => ({
  api: {
    getRepoStatus: vi.fn(() =>
      Promise.resolve({ initialized: true, bootstrap_synced: true }),
    ),
  },
  getManagedSecrets: (...args: unknown[]) => mockGetManagedSecrets(...args),
}))

vi.mock('@/hooks/useAddonStates', () => ({
  AddonStatesProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  useAddonStates: () => ({ states: {}, loading: false }),
}))

// Layout DOES render an Outlet here (unlike the connection-error-gate
// fixture) — we need the child route to actually mount to prove the
// redirect landed somewhere real.
vi.mock('@/components/Layout', () => ({
  Layout: () => (
    <div data-testid="app-layout">
      <Outlet />
    </div>
  ),
}))

// Sentinel for the inventory pages — echoes the `area` prop App passes
// and its own query string, so the tests can confirm both which subpage
// a redirect landed on and that the query string survived, without
// rendering the real (heavy) ManagedSecrets view.
vi.mock('@/views/ManagedSecrets', () => ({
  default: function ManagedSecretsMock({ area }: { area?: string }) {
    const [params] = useSearchParams()
    return (
      <div data-testid={`secrets-${area ?? 'legacy'}-page`}>
        AREA:{area ?? ''}|Q:{params.toString()}
      </div>
    )
  },
}))

// Sentinel for the detail routes — echoes the decoded route params so the
// tests can confirm a legacy row key resolved to the right cluster/addon.
vi.mock('@/views/SecretDetailPage', () => ({
  default: function SecretDetailPageMock() {
    const { cluster = '', addon = '' } = useParams<{ cluster?: string; addon?: string }>()
    return (
      <div data-testid="secret-detail-page">
        DETAIL:{cluster}|{addon}
      </div>
    )
  },
}))

import { ConnectedApp } from '@/App'

// Names with hyphens on purpose: `values-prod-eu-cert-manager` cannot be
// split by text alone (cluster "prod-eu" + addon "cert-manager" vs
// "prod" + "eu-cert-manager") — only the data says which split is real.
const managedSecretsFixture = {
  cluster_connection_secrets: [{ cluster: 'prod-eu', state: 'in_sync' }],
  addon_values_secrets: [{ cluster: 'prod-eu', addon: 'cert-manager', state: 'in_sync' }],
  orphaned_secrets: [
    { cluster: 'staging-us', addon: '', secret_namespace: 'external-secrets', secret_name: 'eso-creds', state: 'orphaned', source: '' },
  ],
  engines: { cluster_connection: { wired: true }, addon_values: { wired: true } },
}

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <ConnectedApp />
    </MemoryRouter>,
  )
}

beforeEach(() => {
  mockState.connections = [{ name: 'default' }]
  mockGetManagedSecrets.mockResolvedValue(managedSecretsFixture)
  sessionStorage.clear()
})

describe('the Secrets area routes (SN-1)', () => {
  it('renders the Cluster connections inventory at /secrets/connections', async () => {
    renderAt('/secrets/connections')

    await waitFor(() => {
      expect(screen.getByTestId('secrets-connections-page')).toBeInTheDocument()
    })
  })

  it('renders the Addon secrets inventory at /secrets/addons — a separate URL, a separate subpage', async () => {
    renderAt('/secrets/addons')

    await waitFor(() => {
      expect(screen.getByTestId('secrets-addons-page')).toBeInTheDocument()
    })
    expect(screen.queryByTestId('secrets-connections-page')).not.toBeInTheDocument()
  })

  it('redirects a bare /secrets hit to /secrets/connections, keeping the query string', async () => {
    renderAt('/secrets?state=out_of_sync')

    await waitFor(() => {
      expect(screen.getByTestId('secrets-connections-page')).toBeInTheDocument()
    })
    expect(screen.getByTestId('secrets-connections-page').textContent).toContain('Q:state=out_of_sync')
  })

  it('renders the connection detail route /secrets/connections/:cluster directly', async () => {
    renderAt('/secrets/connections/prod-eu')

    await waitFor(() => {
      expect(screen.getByTestId('secret-detail-page')).toBeInTheDocument()
    })
    expect(screen.getByTestId('secret-detail-page').textContent).toBe('DETAIL:prod-eu|')
  })

  it('renders the addon detail route /secrets/addons/:cluster/:addon directly', async () => {
    renderAt('/secrets/addons/prod-eu/cert-manager')

    await waitFor(() => {
      expect(screen.getByTestId('secret-detail-page')).toBeInTheDocument()
    })
    expect(screen.getByTestId('secret-detail-page').textContent).toBe('DETAIL:prod-eu|cert-manager')
  })
})

describe('the old /secret-sync list URL redirects (SN-1)', () => {
  it('redirects a bare /secret-sync hit to Cluster connections', async () => {
    renderAt('/secret-sync')

    await waitFor(() => {
      expect(screen.getByTestId('secrets-connections-page')).toBeInTheDocument()
    })
  })

  it('preserves the query string across the redirect (an old filtered deep-link keeps working)', async () => {
    renderAt('/secret-sync?q=datadog&state=out_of_sync')

    await waitFor(() => {
      expect(screen.getByTestId('secrets-connections-page')).toBeInTheDocument()
    })
    expect(screen.getByTestId('secrets-connections-page').textContent).toContain('Q:q=datadog&state=out_of_sync')
  })

  it('sends an old ?kind=values link to Addon secrets and drops the kind param — the route says it now', async () => {
    renderAt('/secret-sync?kind=values&state=out_of_sync')

    await waitFor(() => {
      expect(screen.getByTestId('secrets-addons-page')).toBeInTheDocument()
    })
    expect(screen.getByTestId('secrets-addons-page').textContent).toContain('Q:state=out_of_sync')
    expect(screen.getByTestId('secrets-addons-page').textContent).not.toContain('kind=')
  })

  it('sends an old ?kind=connection link to Cluster connections without the kind param', async () => {
    renderAt('/secret-sync?kind=connection')

    await waitFor(() => {
      expect(screen.getByTestId('secrets-connections-page')).toBeInTheDocument()
    })
    expect(screen.getByTestId('secrets-connections-page').textContent).not.toContain('kind=')
  })

  it('still redirects the pre-rename /secrets alias with its query intact', async () => {
    renderAt('/secrets?row=connection-prod-eu&state=out_of_sync')

    await waitFor(() => {
      expect(screen.getByTestId('secrets-connections-page')).toBeInTheDocument()
    })
    expect(screen.getByTestId('secrets-connections-page').textContent).toContain(
      'Q:row=connection-prod-eu&state=out_of_sync',
    )
  })
})

describe('the old /secret-sync/:rowKey detail URL redirects (SN-1)', () => {
  it('resolves a connection-<cluster> key straight to /secrets/connections/<cluster>', async () => {
    renderAt('/secret-sync/connection-prod-eu')

    await waitFor(() => {
      expect(screen.getByTestId('secret-detail-page')).toBeInTheDocument()
    })
    // No lookup involved: everything after "connection-" IS the cluster.
    expect(screen.getByTestId('secret-detail-page').textContent).toBe('DETAIL:prod-eu|')
    expect(mockGetManagedSecrets).not.toHaveBeenCalled()
  })

  it('resolves a values-<cluster>-<addon> key against the data — hyphenated names split correctly', async () => {
    renderAt('/secret-sync/values-prod-eu-cert-manager')

    await waitFor(() => {
      expect(screen.getByTestId('secret-detail-page')).toBeInTheDocument()
    })
    expect(screen.getByTestId('secret-detail-page').textContent).toBe('DETAIL:prod-eu|cert-manager')
  })

  it('resolves an orphaned-<cluster>-<ns>-<name> key to the addon detail route with namespace/name as the last segment', async () => {
    renderAt('/secret-sync/orphaned-staging-us-external-secrets-eso-creds')

    await waitFor(() => {
      expect(screen.getByTestId('secret-detail-page')).toBeInTheDocument()
    })
    expect(screen.getByTestId('secret-detail-page').textContent).toBe('DETAIL:staging-us|external-secrets/eso-creds')
  })

  it('lands an unknown key on the Cluster connections inventory — never a blank page', async () => {
    renderAt('/secret-sync/some-key-nobody-ever-made')

    await waitFor(() => {
      expect(screen.getByTestId('secrets-connections-page')).toBeInTheDocument()
    })
  })

  it('lands a values key that matches nothing on the Cluster connections inventory too', async () => {
    renderAt('/secret-sync/values-gone-cluster-gone-addon')

    await waitFor(() => {
      expect(screen.getByTestId('secrets-connections-page')).toBeInTheDocument()
    })
  })

  it('falls back to the Cluster connections inventory when the lookup itself fails', async () => {
    mockGetManagedSecrets.mockRejectedValue(new Error('server unreachable'))
    renderAt('/secret-sync/values-prod-eu-cert-manager')

    await waitFor(() => {
      expect(screen.getByTestId('secrets-connections-page')).toBeInTheDocument()
    })
  })
})

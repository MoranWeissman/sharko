import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AdoptClustersDialog } from '../AdoptClustersDialog'
import type { Cluster } from '@/services/models'
import * as api from '@/services/api'
import type { TestClusterUnavailable } from '@/services/api'

// Mock the API module.
//
// isCredentialLookupFailure is deliberately NOT a vi.fn(). The whole decision
// under test here — does an unverifiable cluster stay adoptable — is that one
// predicate, so stubbing it would leave these tests asserting things about the
// stub. The real one is imported and passed straight through.
vi.mock('@/services/api', async () => {
  const actual = await vi.importActual<typeof import('@/services/api')>('@/services/api')
  return {
    testClusterConnection: vi.fn(),
    adoptClusters: vi.fn(),
    isTestClusterUnavailable: vi.fn(),
    isCredentialLookupFailure: actual.isCredentialLookupFailure,
    ApiError: actual.ApiError,
    VERIFY_STAGE_CREDENTIALS: actual.VERIFY_STAGE_CREDENTIALS,
  }
})

/**
 * The ONE sentence a credentials-backend failure carries, byte for byte as
 * internal/credsafe declares it.
 *
 * The fixture that used to sit here said `secret "cluster-creds" not found in
 * AWS Secrets Manager` — a message the server has not been able to send since
 * the credentials hotfix, which replaced every backend's own words with this
 * fixed sentence. That fixture was the reason this test stayed green while the
 * shipped path was broken: the dialog was searching the message for "not
 * found", the fixture obligingly contained it, and no real response ever did.
 */
const CREDENTIALS_BACKEND_SENTENCE =
  "Sharko could not read this cluster's sign-in details from the configured credentials source. " +
  'The server log for this request id says which step failed.'

describe('AdoptClustersDialog', () => {
  const mockOnClose = vi.fn()
  const mockOnSuccess = vi.fn()
  const mockOnDiagnose = vi.fn()

  const mockCluster1: Cluster = {
    name: 'prod-cluster',
    labels: {},
    server_url: 'https://prod.example.com',
    managed: false,
  }

  const mockCluster2: Cluster = {
    name: 'staging-cluster',
    labels: {},
    server_url: 'https://staging.example.com',
    managed: false,
  }

  beforeEach(() => {
    vi.clearAllMocks()
  })

  describe('F14: Credentials-optional contract', () => {
    it('keeps informational-not-verified clusters selected when test is unavailable', async () => {
      // Mock isTestClusterUnavailable to return true
      vi.mocked(api.isTestClusterUnavailable).mockReturnValue(true)
      vi.mocked(api.testClusterConnection).mockResolvedValue({
        unavailable: true,
        error: 'Test feature not configured',
        error_code: 'test_unavailable',
      } as unknown as TestClusterUnavailable)

      render(
        <AdoptClustersDialog
          open={true}
          onClose={mockOnClose}
          clusters={[mockCluster1]}
          onSuccess={mockOnSuccess}
          onDiagnose={mockOnDiagnose}
        />
      )

      // Wait for verification to complete
      await waitFor(() => {
        expect(screen.getByText('Not verified')).toBeInTheDocument()
      })

      // Check the confirm button is enabled (cluster is selected)
      const confirmButton = screen.getByRole('button', { name: /confirm adoption/i })
      expect(confirmButton).not.toBeDisabled()
    })

    it('keeps informational-not-verified clusters selected when the credential lookup failed', async () => {
      vi.mocked(api.isTestClusterUnavailable).mockReturnValue(false)
      vi.mocked(api.testClusterConnection).mockResolvedValue({
        success: false,
        stage: 'credentials',
        // The real sentence, not a made-up one. See the note on the constant.
        error_message: CREDENTIALS_BACKEND_SENTENCE,
        duration_ms: 100,
        reachable: false,
      })

      render(
        <AdoptClustersDialog
          open={true}
          onClose={mockOnClose}
          clusters={[mockCluster1]}
          onSuccess={mockOnSuccess}
          onDiagnose={mockOnDiagnose}
        />
      )

      // Wait for verification to complete
      await waitFor(() => {
        expect(screen.getByText('Not verified')).toBeInTheDocument()
      })

      // Check the confirm button is enabled (cluster is selected)
      const confirmButton = screen.getByRole('button', { name: /confirm adoption/i })
      expect(confirmButton).not.toBeDisabled()
    })

    // ── The decision is the STAGE, and nothing else ──────────────────────
    //
    // Two tests, one either side of the line the old substring search drew.
    // Together they say: the words in error_message change nothing, and the
    // stage changes everything. Neither of them can pass by accident, because
    // each carries the message the OTHER outcome's search would have wanted.

    it('keeps the cluster adoptable on a credential failure whose message says nothing about credentials', async () => {
      vi.mocked(api.isTestClusterUnavailable).mockReturnValue(false)
      vi.mocked(api.testClusterConnection).mockResolvedValue({
        success: false,
        stage: 'credentials',
        // Not one of the five phrases the old search hunted for. Under the
        // old code this cluster was shown as a failed verification and
        // deselected — which is what every real credentials failure became
        // once the backend's own words stopped leaving the server.
        error_message: 'The step did not complete.',
        duration_ms: 100,
        reachable: false,
      })

      render(
        <AdoptClustersDialog
          open={true}
          onClose={mockOnClose}
          clusters={[mockCluster1]}
          onSuccess={mockOnSuccess}
          onDiagnose={mockOnDiagnose}
        />
      )

      await waitFor(() => {
        expect(screen.getByText('Not verified')).toBeInTheDocument()
      })
      expect(screen.getByRole('button', { name: /confirm adoption/i })).not.toBeDisabled()
    })

    it('still fails a cluster the server DID reach, however much its message reads like a credentials problem', async () => {
      vi.mocked(api.isTestClusterUnavailable).mockReturnValue(false)
      vi.mocked(api.testClusterConnection).mockResolvedValue({
        success: false,
        // The cluster was contacted and something there is wrong. Whatever
        // this sentence says, that is a real failure and the cluster must not
        // be carried into an adoption as if it had simply not been checked.
        stage: 'connectivity',
        error_message: 'secret "cluster-creds" not found — no credentials available, credential store unavailable',
        duration_ms: 5000,
        reachable: false,
      })

      render(
        <AdoptClustersDialog
          open={true}
          onClose={mockOnClose}
          clusters={[mockCluster1]}
          onSuccess={mockOnSuccess}
          onDiagnose={mockOnDiagnose}
        />
      )

      await waitFor(() => {
        expect(screen.getByText('Unreachable')).toBeInTheDocument()
      })
      expect(screen.queryByText('Not verified')).not.toBeInTheDocument()
    })

    it('marks genuine verification failures as failed and unchecked', async () => {
      vi.mocked(api.isTestClusterUnavailable).mockReturnValue(false)
      vi.mocked(api.testClusterConnection).mockResolvedValue({
        success: false,
        stage: 'connectivity',
        error_message: 'Connection timeout: cluster unreachable',
        duration_ms: 5000,
        reachable: false,
      })

      render(
        <AdoptClustersDialog
          open={true}
          onClose={mockOnClose}
          clusters={[mockCluster1, mockCluster2]}
          onSuccess={mockOnSuccess}
          onDiagnose={mockOnDiagnose}
        />
      )

      // Wait for verification to complete — both clusters will fail
      await waitFor(() => {
        const failedElements = screen.getAllByText('Unreachable')
        expect(failedElements.length).toBeGreaterThan(0)
      })

      // In multi-cluster mode, check that the checkboxes are unchecked
      await waitFor(() => {
        const checkboxes = screen.getAllByRole('checkbox')
        checkboxes.forEach(checkbox => {
          expect(checkbox).not.toBeChecked()
        })
      })
    })
  })

  describe('F15: Confirm path allows credentials-optional adoption', () => {
    it('proceeds with adoption for informational-not-verified clusters', async () => {
      vi.mocked(api.isTestClusterUnavailable).mockReturnValue(true)
      vi.mocked(api.testClusterConnection).mockResolvedValue({
        unavailable: true,
        error: 'Test feature not configured',
        error_code: 'test_unavailable',
      } as unknown as TestClusterUnavailable)

      vi.mocked(api.adoptClusters).mockResolvedValue({
        results: [{
          name: 'prod-cluster',
          status: 'success',
          git: {
            pr_url: 'https://github.com/example/repo/pull/123',
          },
        }],
      })

      render(
        <AdoptClustersDialog
          open={true}
          onClose={mockOnClose}
          clusters={[mockCluster1]}
          onSuccess={mockOnSuccess}
          onDiagnose={mockOnDiagnose}
        />
      )

      // Wait for verification
      await waitFor(() => {
        expect(screen.getByText('Not verified')).toBeInTheDocument()
      })

      // Click confirm
      const confirmButton = screen.getByRole('button', { name: /confirm adoption/i })
      await userEvent.click(confirmButton)

      // Wait for adoption to complete
      await waitFor(() => {
        expect(api.adoptClusters).toHaveBeenCalledWith({
          clusters: ['prod-cluster'],
          acknowledged_findings: [],
        })
      })
    })
  })

  describe('F16: Single-cluster hides checkbox', () => {
    it('hides checkbox column when clusters.length === 1', async () => {
      vi.mocked(api.isTestClusterUnavailable).mockReturnValue(false)
      vi.mocked(api.testClusterConnection).mockResolvedValue({
        success: true,
        stage: 'connectivity',
        duration_ms: 100,
        reachable: true,
        server_version: '1.29.3',
      })

      render(
        <AdoptClustersDialog
          open={true}
          onClose={mockOnClose}
          clusters={[mockCluster1]}
          onSuccess={mockOnSuccess}
          onDiagnose={mockOnDiagnose}
        />
      )

      // Wait for verification
      await waitFor(() => {
        expect(screen.getByText('Reachable')).toBeInTheDocument()
      })

      // No checkboxes should be present
      const checkboxes = screen.queryAllByRole('checkbox')
      expect(checkboxes).toHaveLength(0)

      // Button label should not show count
      const confirmButton = screen.getByRole('button', { name: 'Confirm Adoption' })
      expect(confirmButton).toBeInTheDocument()
      expect(confirmButton.textContent).not.toContain('(1)')
    })

    it('shows checkbox column when clusters.length > 1', async () => {
      vi.mocked(api.isTestClusterUnavailable).mockReturnValue(false)
      vi.mocked(api.testClusterConnection).mockResolvedValue({
        success: true,
        stage: 'connectivity',
        duration_ms: 100,
        reachable: true,
        server_version: '1.29.3',
      })

      render(
        <AdoptClustersDialog
          open={true}
          onClose={mockOnClose}
          clusters={[mockCluster1, mockCluster2]}
          onSuccess={mockOnSuccess}
          onDiagnose={mockOnDiagnose}
        />
      )

      // Wait for verification
      await waitFor(() => {
        expect(screen.getAllByText('Reachable')).toHaveLength(2)
      })

      // Checkboxes should be present
      const checkboxes = screen.getAllByRole('checkbox')
      expect(checkboxes.length).toBeGreaterThan(0)

      // Button label should show count
      const confirmButton = screen.getByRole('button', { name: /confirm adoption \(2\)/i })
      expect(confirmButton).toBeInTheDocument()
    })
  })

  describe('F17: Error message renders legibly', () => {
    it('renders error message in full-width row below cluster row', async () => {
      // Use an error that doesn't match credentials-not-found pattern
      const longError = 'Connection timeout after 5000ms: unable to reach https://prod-cluster.k8s.aws.example.com:6443'
      vi.mocked(api.isTestClusterUnavailable).mockReturnValue(false)
      vi.mocked(api.testClusterConnection).mockResolvedValue({
        success: false,
        stage: 'connectivity',
        error_message: longError,
        duration_ms: 5000,
        reachable: false,
      })

      render(
        <AdoptClustersDialog
          open={true}
          onClose={mockOnClose}
          clusters={[mockCluster1]}
          onSuccess={mockOnSuccess}
          onDiagnose={mockOnDiagnose}
        />
      )

      // Wait for verification
      await waitFor(() => {
        expect(screen.getByText('Unreachable')).toBeInTheDocument()
      })

      // Error message should be present and contain the full text
      expect(screen.getByText(longError)).toBeInTheDocument()
    })

    it('renders informational message for not-verified state', async () => {
      vi.mocked(api.isTestClusterUnavailable).mockReturnValue(true)
      vi.mocked(api.testClusterConnection).mockResolvedValue({
        unavailable: true,
        error: 'Test feature not configured',
        error_code: 'test_unavailable',
      } as unknown as TestClusterUnavailable)

      render(
        <AdoptClustersDialog
          open={true}
          onClose={mockOnClose}
          clusters={[mockCluster1]}
          onSuccess={mockOnSuccess}
          onDiagnose={mockOnDiagnose}
        />
      )

      // Wait for verification
      await waitFor(() => {
        expect(screen.getByText('Not verified')).toBeInTheDocument()
      })

      // Should show informational message
      expect(screen.getByText(/not verified — connectivity will be checked when a secret-bearing addon needs it/i)).toBeInTheDocument()
    })
  })

  describe('F2: Adoption failure handling + type alignment', () => {
    it('surfaces per-cluster failures and does NOT call onSuccess', async () => {
      vi.mocked(api.isTestClusterUnavailable).mockReturnValue(false)
      vi.mocked(api.testClusterConnection).mockResolvedValue({
        success: true,
        stage: 'connectivity',
        duration_ms: 100,
        reachable: true,
        server_version: '1.29.3',
      })

      vi.mocked(api.adoptClusters).mockResolvedValue({
        results: [{
          name: 'prod-cluster',
          status: 'failed',
          error: 'cluster "prod-cluster" not found in ArgoCD — cannot adopt',
        }],
      })

      render(
        <AdoptClustersDialog
          open={true}
          onClose={mockOnClose}
          clusters={[mockCluster1]}
          onSuccess={mockOnSuccess}
          onDiagnose={mockOnDiagnose}
        />
      )

      // Wait for verification
      await waitFor(() => {
        expect(screen.getByText('Reachable')).toBeInTheDocument()
      })

      // Confirm adoption
      const confirmButton = screen.getByRole('button', { name: /confirm adoption/i })
      await userEvent.click(confirmButton)

      // Wait for adoption to complete
      await waitFor(() => {
        expect(api.adoptClusters).toHaveBeenCalledWith({
          clusters: ['prod-cluster'],
          acknowledged_findings: [],
        })
      })

      // Should show the error message
      await waitFor(() => {
        expect(screen.getByText('cluster "prod-cluster" not found in ArgoCD — cannot adopt')).toBeInTheDocument()
      })

      // onSuccess should NOT have been called
      expect(mockOnSuccess).not.toHaveBeenCalled()
    })

    it('renders PR link from git.pr_url when adoption succeeds', async () => {
      vi.mocked(api.isTestClusterUnavailable).mockReturnValue(false)
      vi.mocked(api.testClusterConnection).mockResolvedValue({
        success: true,
        stage: 'connectivity',
        duration_ms: 100,
        reachable: true,
        server_version: '1.29.3',
      })

      vi.mocked(api.adoptClusters).mockResolvedValue({
        results: [{
          name: 'prod-cluster',
          status: 'success',
          git: {
            pr_url: 'https://example.test/pr/1',
            merged: true,
          },
        }],
      })

      render(
        <AdoptClustersDialog
          open={true}
          onClose={mockOnClose}
          clusters={[mockCluster1]}
          onSuccess={mockOnSuccess}
          onDiagnose={mockOnDiagnose}
        />
      )

      // Wait for verification
      await waitFor(() => {
        expect(screen.getByText('Reachable')).toBeInTheDocument()
      })

      // Confirm adoption
      const confirmButton = screen.getByRole('button', { name: /confirm adoption/i })
      await userEvent.click(confirmButton)

      // Wait for done phase
      await waitFor(() => {
        expect(mockOnSuccess).toHaveBeenCalled()
      })

      // Should show the PR link
      const prLink = screen.getByRole('link', { name: /PR/i })
      expect(prLink).toBeInTheDocument()
      expect(prLink).toHaveAttribute('href', 'https://example.test/pr/1')
    })
  })

  // V3-TX-A3 — Preview on every PR-opening operation. Surface 1: Adopt.
  describe('V3-TX-A3: Preview changes', () => {
    it('Preview calls adoptClusters(dry_run) and renders the diff without adopting', async () => {
      vi.mocked(api.isTestClusterUnavailable).mockReturnValue(false)
      vi.mocked(api.testClusterConnection).mockResolvedValue({
        success: true,
        stage: 'connectivity',
        duration_ms: 100,
        reachable: true,
        server_version: '1.29.3',
      })

      // Dry-run returns the preview on the first result's `dry_run` field,
      // the same key the Go server sends.
      vi.mocked(api.adoptClusters).mockResolvedValue({
        results: [
          {
            name: 'prod-cluster',
            status: 'success',
            dry_run: {
              pr_title: 'Adopt cluster prod-cluster',
              verification: {
                success: true,
                stage: 'stage1',
                duration_ms: 5,
                notes: ['Write access was not checked because this is a preview.'],
              },
              files_to_write: [
                { path: 'configuration/managed-clusters.yaml', action: 'update' },
                { path: 'configuration/clusters/prod-cluster.yaml', action: 'create' },
              ],
            },
          },
        ],
      } as never)

      render(
        <AdoptClustersDialog
          open={true}
          onClose={mockOnClose}
          clusters={[mockCluster1]}
          onSuccess={mockOnSuccess}
          onDiagnose={mockOnDiagnose}
        />,
      )

      await waitFor(() => {
        expect(screen.getByText('Reachable')).toBeInTheDocument()
      })

      // Click Preview changes.
      await userEvent.click(screen.getByRole('button', { name: /preview changes/i }))

      // Dry-run call carries dry_run: true.
      await waitFor(() => {
        expect(api.adoptClusters).toHaveBeenCalledWith({
          clusters: ['prod-cluster'],
          dry_run: true,
        })
      })

      // Preview rendered via the shared DryRunPreview.
      await waitFor(() =>
        expect(screen.getByText('Adopt cluster prod-cluster')).toBeInTheDocument(),
      )
      expect(
        screen.getByText('configuration/clusters/prod-cluster.yaml'),
      ).toBeInTheDocument()
      expect(
        screen.getByText('Write access was not checked because this is a preview.'),
      ).toBeInTheDocument()

      // Preview must NOT have opened the PR — onSuccess is not called and the
      // real (non-dry-run) adopt was never invoked.
      expect(mockOnSuccess).not.toHaveBeenCalled()
      expect(api.adoptClusters).not.toHaveBeenCalledWith({ clusters: ['prod-cluster'], acknowledged_findings: [] })
    })
  })
  describe('episode 3 dry run: what adopt shows and asks', () => {
    const reachable = () => {
      vi.mocked(api.isTestClusterUnavailable).mockReturnValue(false)
      vi.mocked(api.testClusterConnection).mockResolvedValue({
        success: true,
        reachable: true,
        server_version: 'v1.31.0',
      } as never)
    }
    const open1 = () =>
      render(
        <AdoptClustersDialog
          open={true}
          onClose={mockOnClose}
          clusters={[mockCluster1]}
          onSuccess={mockOnSuccess}
          onDiagnose={mockOnDiagnose}
        />
      )

    it('finding 11: shows the version with one v, never two', async () => {
      reachable()
      open1()
      await waitFor(() => expect(screen.getByText('v1.31.0')).toBeInTheDocument())
      expect(screen.queryByText('vv1.31.0')).not.toBeInTheDocument()
    })

    it('finding 11: adds the v when the server version has none', async () => {
      reachable()
      vi.mocked(api.testClusterConnection).mockResolvedValue({
        success: true,
        reachable: true,
        server_version: '1.31.0',
      } as never)
      open1()
      await waitFor(() => expect(screen.getByText('v1.31.0')).toBeInTheDocument())
    })

    it('finding 4: a preview that fails shows every reason, in the review step', async () => {
      reachable()
      vi.mocked(api.adoptClusters).mockResolvedValue({
        results: [
          {
            name: 'prod-cluster',
            status: 'failed',
            error: '2 things to fix first',
            blockers: [
              { id: 'no-connection', message: 'ArgoCD has no connection saved for prod-cluster.' },
              { id: 'name-clash', message: 'Another cluster already uses this name.' },
            ],
          },
        ],
      })
      const user = userEvent.setup()
      open1()
      await user.click(await screen.findByRole('button', { name: /preview changes/i }))
      expect(await screen.findByText('ArgoCD has no connection saved for prod-cluster.')).toBeInTheDocument()
      expect(screen.getByText('Another cluster already uses this name.')).toBeInTheDocument()
      // The step is review again, so the error is not hidden.
      expect(screen.getByText(/This cluster cannot be adopted yet/)).toBeInTheDocument()
      expect(screen.getByRole('button', { name: /confirm adoption/i })).toBeDisabled()
    })

    it('finding 4: a preview that fails with a plain error still says so', async () => {
      reachable()
      vi.mocked(api.adoptClusters).mockRejectedValue(new Error('Git is not reachable'))
      const user = userEvent.setup()
      open1()
      await user.click(await screen.findByRole('button', { name: /preview changes/i }))
      expect(await screen.findByText('Git is not reachable')).toBeInTheDocument()
    })

    it('finding 13: each warning has its own box and confirm waits for every one', async () => {
      reachable()
      vi.mocked(api.adoptClusters).mockResolvedValue({
        results: [
          {
            name: 'prod-cluster',
            status: 'success',
            warnings: [
              { id: 'owner', message: 'Another tool owns this connection today.' },
              { id: 'appsets', message: 'Two ApplicationSets will start to pick this cluster.' },
            ],
            dry_run: { effective_addons: [], files_to_write: [], pr_title: 't', secrets_to_create: [] },
          },
        ],
      } as never)
      const user = userEvent.setup()
      open1()
      await user.click(await screen.findByRole('button', { name: /preview changes/i }))
      expect(await screen.findByText('Another tool owns this connection today.')).toBeInTheDocument()
      const confirm = screen.getByRole('button', { name: /confirm adoption/i })
      expect(confirm).toBeDisabled()
      await user.click(screen.getByTestId('adopt-warning-owner'))
      expect(confirm).toBeDisabled()
      await user.click(screen.getByTestId('adopt-warning-appsets'))
      expect(confirm).toBeEnabled()

      vi.mocked(api.adoptClusters).mockResolvedValue({
        results: [{ name: 'prod-cluster', status: 'success', git: { pr_url: 'https://git/pr/3' } }],
      } as never)
      await user.click(confirm)
      await waitFor(() =>
        expect(api.adoptClusters).toHaveBeenLastCalledWith({
          clusters: ['prod-cluster'],
          acknowledged_findings: ['owner', 'appsets'],
        }),
      )
    })

    it('finding 13: a 409 for a new warning shows it to tick, then goes ahead', async () => {
      reachable()
      vi.mocked(api.adoptClusters).mockRejectedValueOnce(
        new api.ApiError(
          409,
          { error: 'needs acknowledgement', results: [{ name: 'prod-cluster', warnings: [{ id: 'owner', message: 'Another tool owns this connection today.' }] }] },
          'Conflict',
        ),
      )
      const user = userEvent.setup()
      open1()
      await user.click(await screen.findByRole('button', { name: /confirm adoption/i }))
      expect(await screen.findByText('Another tool owns this connection today.')).toBeInTheDocument()
      const confirm = screen.getByRole('button', { name: /confirm adoption/i })
      expect(confirm).toBeDisabled()
      await user.click(screen.getByTestId('adopt-warning-owner'))
      vi.mocked(api.adoptClusters).mockResolvedValue({
        results: [{ name: 'prod-cluster', status: 'success', git: { pr_url: 'https://git/pr/3' } }],
      } as never)
      await user.click(confirm)
      await waitFor(() =>
        expect(api.adoptClusters).toHaveBeenLastCalledWith({
          clusters: ['prod-cluster'],
          acknowledged_findings: ['owner'],
        }),
      )
    })

    it('finding 3: the success screen with the PR link stays after the refresh asks for new clusters', async () => {
      reachable()
      vi.mocked(api.adoptClusters).mockResolvedValue({
        results: [{ name: 'prod-cluster', status: 'success', git: { pr_url: 'https://git/pr/3' } }],
      } as never)
      const user = userEvent.setup()
      open1()
      await user.click(await screen.findByRole('button', { name: /confirm adoption/i }))
      const link = await screen.findByRole('link', { name: /PR/ })
      expect(link).toHaveAttribute('href', 'https://git/pr/3')
      expect(screen.getByText('Adoption complete.')).toBeInTheDocument()
    })
  })
})

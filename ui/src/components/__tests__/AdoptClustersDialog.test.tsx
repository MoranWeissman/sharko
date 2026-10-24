import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AdoptClustersDialog, ackIdsFor } from '../AdoptClustersDialog'
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
      // Round S (episode 3 open question 3): plain words, no promise, and
      // the server's raw detail only in the tooltip.
      const line = screen.getByTestId('adopt-not-tested')
      expect(line).toHaveTextContent(/^Not tested\. Sharko could not test the connection to this cluster, so it did not check it\. You can still adopt it\.$/)
      expect(line).toHaveAttribute('title', 'Test feature not configured')
      expect(document.body.textContent).not.toMatch(/connectivity will be checked/i)
      expect(document.body.textContent).not.toMatch(/secret-bearing/i)
      expect(document.body.textContent).not.toContain('Test feature not configured')
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
      const prLink = screen.getByRole('link', { name: /view pull request/i })
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
      expect(screen.getAllByText('"prod-cluster" cannot be adopted yet. See the reasons below.').length).toBeGreaterThan(0)
      expect(screen.queryByText(/This cluster cannot be adopted yet/)).not.toBeInTheDocument()
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
          // The real 409 body (adoptNeedsAcknowledgementBody): code, the ids
          // still missing a tick, every warning once, and each named cluster
          // with its full list.
          {
            error: 'Sharko did not adopt anything. Read each warning and acknowledge it first — not acknowledged yet: owner',
            code: 'unacknowledged_findings',
            unacknowledged_findings: ['owner'],
            warnings: [{ id: 'owner', message: 'Another tool owns this connection today.' }],
            results: [
              {
                name: 'prod-cluster',
                status: 'skipped',
                warnings: [{ id: 'owner', message: 'Another tool owns this connection today.' }],
                unacknowledged_findings: ['owner'],
              },
            ],
          },
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
      const link = await screen.findByRole('link', { name: /view pull request/i })
      expect(link).toHaveAttribute('href', 'https://git/pr/3')
      // The pull request is open, not merged: the adopt is not done yet.
      expect(screen.getByText('Pull request opened, waiting for merge.')).toBeInTheDocument()
      expect(screen.queryByText('Adoption complete.')).not.toBeInTheDocument()
    })
  })

  // Round R (R16): the Adopt dialog tells the truth about an open pull
  // request, never loops on new warnings, and only counts selected clusters.
  describe('round R: adopt results, warnings and blockers', () => {
    const reachable = () => {
      vi.mocked(api.isTestClusterUnavailable).mockReturnValue(false)
      vi.mocked(api.testClusterConnection).mockResolvedValue({
        success: true,
        reachable: true,
        server_version: 'v1.31.0',
      } as never)
    }
    const preview = { effective_addons: [], files_to_write: [], pr_title: 't', secrets_to_create: [] }
    const openWith = (clusters: Cluster[]) =>
      render(
        <AdoptClustersDialog open={true} onClose={mockOnClose} clusters={clusters} onSuccess={mockOnSuccess} onDiagnose={mockOnDiagnose} />,
      )
    const openMsg =
      'Pull request #7 is open: https://git/pr/7. When it is merged, Sharko takes ownership of the ArgoCD connection and marks it adopted. If it is closed without merging, Sharko changes nothing on the connection.'

    it('an open pull request says it waits for the merge, never "Adoption complete." or "Adopted"', async () => {
      reachable()
      vi.mocked(api.adoptClusters).mockResolvedValue({
        results: [{ name: 'prod-cluster', status: 'success', merged: false, message: openMsg, git: { pr_url: 'https://git/pr/7', merged: false } }],
      } as never)
      const user = userEvent.setup()
      openWith([mockCluster1])
      await user.click(await screen.findByRole('button', { name: /confirm adoption/i }))
      const row = await screen.findByTestId('adopt-result-prod-cluster')
      expect(row).toHaveTextContent('Pull request opened, waiting for merge')
      expect(screen.getByRole('link', { name: /view pull request/i })).toHaveAttribute('href', 'https://git/pr/7')
      expect(screen.getByText(openMsg)).toBeInTheDocument()
      expect(screen.getByText('Pull request opened, waiting for merge.')).toBeInTheDocument()
      expect(screen.queryByText('Adoption complete.')).not.toBeInTheDocument()
      expect(screen.queryByText('Adopted')).not.toBeInTheDocument()
    })

    it('a result with no merged field reads as not merged', async () => {
      reachable()
      vi.mocked(api.adoptClusters).mockResolvedValue({
        results: [{ name: 'prod-cluster', status: 'success' }],
      } as never)
      const user = userEvent.setup()
      openWith([mockCluster1])
      await user.click(await screen.findByRole('button', { name: /confirm adoption/i }))
      await screen.findByTestId('adopt-result-prod-cluster')
      expect(screen.queryByText('Adoption complete.')).not.toBeInTheDocument()
      expect(screen.queryByText('Adopted')).not.toBeInTheDocument()
    })

    it('a merged adopt says "Adoption complete." and "Adopted"', async () => {
      reachable()
      vi.mocked(api.adoptClusters).mockResolvedValue({
        results: [{ name: 'prod-cluster', status: 'success', merged: true, git: { pr_url: 'https://git/pr/8', merged: true } }],
      } as never)
      const user = userEvent.setup()
      openWith([mockCluster1])
      await user.click(await screen.findByRole('button', { name: /confirm adoption/i }))
      expect(await screen.findByText('Adoption complete.')).toBeInTheDocument()
      expect(screen.getByText('Adopted')).toBeInTheDocument()
      expect(screen.queryByText('Pull request opened, waiting for merge')).not.toBeInTheDocument()
    })

    it('a partial adopt shows what the server said, not a green "Adopted"', async () => {
      reachable()
      // A sentence the server really sends (internal/orchestrator/adopt_v4.go,
      // the merge-failed partial). The old fixture used a sentence the server
      // stopped sending in round R. merged is always on the wire.
      const partialMsg = 'PR created but merge failed for cluster prod-cluster: https://git/pr/9'
      vi.mocked(api.adoptClusters).mockResolvedValue({
        results: [{ name: 'prod-cluster', status: 'partial', merged: false, message: partialMsg, git: { pr_url: 'https://git/pr/9', merged: false } }],
      } as never)
      const user = userEvent.setup()
      openWith([mockCluster1])
      await user.click(await screen.findByRole('button', { name: /confirm adoption/i }))
      const row = await screen.findByTestId('adopt-result-prod-cluster')
      expect(row).toHaveAttribute('data-outcome', 'partial')
      expect(row).toHaveTextContent('Not finished yet')
      expect(screen.getByText(partialMsg)).toBeInTheDocument()
      expect(screen.getByText('Not every cluster finished. Read each one below.')).toBeInTheDocument()
      expect(screen.queryByText('Adoption complete.')).not.toBeInTheDocument()
      expect(screen.queryByText('Adopted')).not.toBeInTheDocument()
    })

    it('a 409 with a new warning keeps the other cluster\'s ticks and sends them all', async () => {
      reachable()
      vi.mocked(api.adoptClusters).mockResolvedValueOnce({
        results: [
          { name: 'prod-cluster', status: 'success', warnings: [{ id: 'owner', message: 'Another tool owns prod today.' }], dry_run: preview },
          { name: 'staging-cluster', status: 'success', warnings: [{ id: 'appsets', message: 'Two ApplicationSets pick staging.' }], dry_run: preview },
        ],
      } as never)
      const user = userEvent.setup()
      openWith([mockCluster1, mockCluster2])
      await user.click(await screen.findByRole('button', { name: /preview changes/i }))
      await user.click(await screen.findByTestId('adopt-warning-owner'))
      await user.click(screen.getByTestId('adopt-warning-appsets'))

      // The server finds a new warning on staging only. Its 409 names only
      // staging, with staging's FULL list (internal/orchestrator/
      // adopt_findings.go puts every warning of the cluster in its result).
      vi.mocked(api.adoptClusters).mockRejectedValueOnce(
        new api.ApiError(
          409,
          {
            error: 'needs acknowledgement',
            code: 'unacknowledged_findings',
            unacknowledged_findings: ['new-hook'],
            warnings: [{ id: 'appsets', message: 'Two ApplicationSets pick staging.' }, { id: 'new-hook', message: 'A new hook watches staging.' }],
            results: [{ name: 'staging-cluster', warnings: [{ id: 'appsets', message: 'Two ApplicationSets pick staging.' }, { id: 'new-hook', message: 'A new hook watches staging.' }] }],
          },
          'Conflict',
        ),
      )
      await user.click(screen.getByRole('button', { name: /confirm adoption/i }))
      expect(await screen.findByText('There is something new to read. Tick each warning below, then confirm again.')).toBeInTheDocument()
      // prod's warning is still on screen and still ticked.
      expect(screen.getByText('Another tool owns prod today.')).toBeInTheDocument()
      expect(screen.getByTestId('adopt-warning-owner')).toBeChecked()
      await user.click(screen.getByTestId('adopt-warning-new-hook'))

      vi.mocked(api.adoptClusters).mockResolvedValueOnce({
        results: [
          { name: 'prod-cluster', status: 'success', merged: true },
          { name: 'staging-cluster', status: 'success', merged: true },
        ],
      } as never)
      await user.click(screen.getByRole('button', { name: /confirm adoption/i }))
      await waitFor(() =>
        expect(api.adoptClusters).toHaveBeenLastCalledWith({
          clusters: ['prod-cluster', 'staging-cluster'],
          acknowledged_findings: ['owner', 'appsets', 'new-hook'],
        }),
      )
    })

    it('a blocker on a cluster the user unticks does not hold Confirm back', async () => {
      reachable()
      vi.mocked(api.adoptClusters).mockResolvedValueOnce({
        results: [
          { name: 'prod-cluster', status: 'success', dry_run: preview },
          { name: 'staging-cluster', status: 'failed', error: '1 thing to fix first', blockers: [{ id: 'no-connection', message: 'ArgoCD has no connection saved for staging.' }] },
        ],
      } as never)
      const user = userEvent.setup()
      openWith([mockCluster1, mockCluster2])
      await user.click(await screen.findByRole('button', { name: /preview changes/i }))
      expect(await screen.findByText(/ArgoCD has no connection saved for staging\./)).toBeInTheDocument()
      expect(screen.getAllByText('"staging-cluster" cannot be adopted yet. See the reasons below.').length).toBeGreaterThan(0)
      const confirm = screen.getByRole('button', { name: /confirm adoption/i })
      expect(confirm).toBeDisabled()
      // Untick staging (the second cluster row).
      const rowBoxes = screen.getAllByRole('checkbox')
      await user.click(rowBoxes[1])
      expect(screen.queryByText(/ArgoCD has no connection saved for staging\./)).not.toBeInTheDocument()
      expect(screen.getByRole('button', { name: /confirm adoption/i })).toBeEnabled()
    })

    // The server no longer sends two warnings with one id (each foreign
    // owner gets foreign-owner-<8 hex>, round S S17). This stays as a guard
    // in case an old server answers.
    it('two warnings with the same id each need their own tick', async () => {
      reachable()
      vi.mocked(api.adoptClusters).mockResolvedValueOnce({
        results: [
          {
            name: 'prod-cluster',
            status: 'success',
            warnings: [
              { id: 'foreign-owner', message: 'The Application "a" renders this connection.' },
              { id: 'foreign-owner', message: 'The Application "b" renders this connection.' },
            ],
            dry_run: preview,
          },
        ],
      } as never)
      const user = userEvent.setup()
      openWith([mockCluster1])
      await user.click(await screen.findByRole('button', { name: /preview changes/i }))
      const boxes = await screen.findAllByTestId('adopt-warning-foreign-owner')
      expect(boxes).toHaveLength(2)
      await user.click(boxes[0])
      expect(boxes[0]).toBeChecked()
      expect(boxes[1]).not.toBeChecked()
      expect(screen.getByRole('button', { name: /confirm adoption/i })).toBeDisabled()
      await user.click(boxes[1])
      expect(screen.getByRole('button', { name: /confirm adoption/i })).toBeEnabled()
    })

    it('two foreign owners with the real ids (foreign-owner-<8 hex>) each need a tick, and both ids are sent', async () => {
      reachable()
      vi.mocked(api.adoptClusters).mockResolvedValueOnce({
        results: [
          {
            name: 'prod-cluster',
            status: 'success',
            warnings: [
              { id: 'foreign-owner-1a2b3c4d', message: 'The Application "a" renders this connection.' },
              { id: 'foreign-owner-5e6f7a8b', message: 'The Application "b" renders this connection.' },
            ],
            dry_run: preview,
          },
        ],
      } as never)
      const user = userEvent.setup()
      openWith([mockCluster1])
      await user.click(await screen.findByRole('button', { name: /preview changes/i }))
      const confirm = screen.getByRole('button', { name: /confirm adoption/i })
      await user.click(await screen.findByTestId('adopt-warning-foreign-owner-1a2b3c4d'))
      expect(confirm).toBeDisabled()
      await user.click(screen.getByTestId('adopt-warning-foreign-owner-5e6f7a8b'))
      expect(confirm).toBeEnabled()
      vi.mocked(api.adoptClusters).mockResolvedValue({
        results: [{ name: 'prod-cluster', status: 'success', git: { pr_url: 'https://git/pr/3' } }],
      } as never)
      await user.click(confirm)
      await waitFor(() =>
        expect(api.adoptClusters).toHaveBeenLastCalledWith({
          clusters: ['prod-cluster'],
          acknowledged_findings: ['foreign-owner-1a2b3c4d', 'foreign-owner-5e6f7a8b'],
        }),
      )
    })

    it('a new clusters array with the same names does not reset the ticks', async () => {
      reachable()
      vi.mocked(api.adoptClusters).mockResolvedValueOnce({
        results: [{ name: 'prod-cluster', status: 'success', warnings: [{ id: 'owner', message: 'Another tool owns prod today.' }], dry_run: preview }],
      } as never)
      const user = userEvent.setup()
      const { rerender } = openWith([mockCluster1])
      await user.click(await screen.findByRole('button', { name: /preview changes/i }))
      await user.click(await screen.findByTestId('adopt-warning-owner'))
      expect(screen.getByTestId('adopt-warning-owner')).toBeChecked()
      rerender(
        <AdoptClustersDialog open={true} onClose={mockOnClose} clusters={[{ ...mockCluster1 }]} onSuccess={mockOnSuccess} onDiagnose={mockOnDiagnose} />,
      )
      expect(screen.getByTestId('adopt-warning-owner')).toBeChecked()
      expect(api.testClusterConnection).toHaveBeenCalledTimes(1)
    })

    it('a preview that fails with no answer clears the old blockers', async () => {
      reachable()
      vi.mocked(api.adoptClusters).mockResolvedValueOnce({
        results: [{ name: 'prod-cluster', status: 'failed', blockers: [{ id: 'no-connection', message: 'ArgoCD has no connection saved for prod.' }] }],
      } as never)
      const user = userEvent.setup()
      openWith([mockCluster1])
      await user.click(await screen.findByRole('button', { name: /preview changes/i }))
      expect(await screen.findByText('ArgoCD has no connection saved for prod.')).toBeInTheDocument()
      vi.mocked(api.adoptClusters).mockRejectedValueOnce(new Error('Git is not reachable'))
      await user.click(screen.getByRole('button', { name: /preview changes/i }))
      expect(await screen.findByText('Git is not reachable')).toBeInTheDocument()
      expect(screen.queryByText('ArgoCD has no connection saved for prod.')).not.toBeInTheDocument()
    })
  })
})

// Round S (S31, S32 and the shared contract's other_pr_open 409). The 409
// bodies below follow the round S contract: other_pr_open carries code,
// pr_number and pr_url when known, and the server's own sentence in `error`.
describe('AdoptClustersDialog — round S', () => {
  const onClose = vi.fn()
  const onSuccess = vi.fn()
  const onDiagnose = vi.fn()
  const prod: Cluster = { name: 'prod-cluster', labels: {}, server_url: 'https://prod.example.com', managed: false }
  const staging: Cluster = { name: 'staging-cluster', labels: {}, server_url: 'https://staging.example.com', managed: false }
  const preview = { effective_addons: [], files_to_write: [], pr_title: 't', secrets_to_create: [] }
  const reachable = () => {
    vi.mocked(api.isTestClusterUnavailable).mockReturnValue(false)
    vi.mocked(api.testClusterConnection).mockResolvedValue({ success: true, reachable: true, server_version: 'v1.31.0' } as never)
  }
  const openWith = (clusters: Cluster[]) =>
    render(<AdoptClustersDialog open={true} onClose={onClose} clusters={clusters} onSuccess={onSuccess} onDiagnose={onDiagnose} />)
  const warningOn = (cluster: string, id: string) =>
    screen.getAllByTestId(`adopt-warning-${id}`).find((el) => el.getAttribute('data-cluster') === cluster)!
  const conflict = (body: Record<string, unknown>) => new api.ApiError(409, body as never, 'Conflict')

  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('S31: a tick on one cluster does not send that warning for a cluster whose warnings were never shown', async () => {
    reachable()
    const user = userEvent.setup()
    openWith([prod, staging])
    // Preview prod alone: staging is unticked, so the server answers for prod only.
    await user.click(await screen.findByRole('checkbox', { name: 'Select staging-cluster' }))
    vi.mocked(api.adoptClusters).mockResolvedValueOnce({
      results: [{ name: 'prod-cluster', status: 'success', warnings: [{ id: 'appset-team', message: 'The team ApplicationSet picks prod.' }], dry_run: preview }],
    } as never)
    await user.click(screen.getByRole('button', { name: /preview changes/i }))
    await user.click(await screen.findByTestId('adopt-warning-appset-team'))
    await user.click(screen.getByRole('button', { name: /back/i }))
    // Now pick staging again and confirm. Staging's warnings were never shown.
    await user.click(screen.getByRole('checkbox', { name: 'Select staging-cluster' }))
    vi.mocked(api.adoptClusters).mockRejectedValueOnce(
      conflict({
        error: 'needs acknowledgement',
        code: 'unacknowledged_findings',
        unacknowledged_findings: ['appset-team'],
        warnings: [{ id: 'appset-team', message: 'The team ApplicationSet picks prod.' }],
        results: [
          { name: 'prod-cluster', status: 'skipped', warnings: [{ id: 'appset-team', message: 'The team ApplicationSet picks prod.' }] },
          { name: 'staging-cluster', status: 'skipped', warnings: [{ id: 'appset-team', message: 'The team ApplicationSet picks staging.' }] },
        ],
      }),
    )
    await user.click(screen.getByRole('button', { name: /confirm adoption/i }))
    expect(api.adoptClusters).toHaveBeenLastCalledWith({ clusters: ['prod-cluster', 'staging-cluster'], acknowledged_findings: [] })
    // Staging's copy shows, not ticked; prod's tick stays.
    expect(await screen.findByText('The team ApplicationSet picks staging.')).toBeInTheDocument()
    expect(warningOn('prod-cluster', 'appset-team')).toBeChecked()
    expect(warningOn('staging-cluster', 'appset-team')).not.toBeChecked()
    expect(screen.getByRole('button', { name: /confirm adoption/i })).toBeDisabled()
    // Tick staging's copy too: now the id goes.
    await user.click(warningOn('staging-cluster', 'appset-team'))
    vi.mocked(api.adoptClusters).mockResolvedValueOnce({
      results: [
        { name: 'prod-cluster', status: 'success', merged: true },
        { name: 'staging-cluster', status: 'success', merged: true },
      ],
    } as never)
    await user.click(screen.getByRole('button', { name: /confirm adoption/i }))
    await waitFor(() =>
      expect(api.adoptClusters).toHaveBeenLastCalledWith({ clusters: ['prod-cluster', 'staging-cluster'], acknowledged_findings: ['appset-team'] }),
    )
  })

  it('S31: ackIdsFor sends an id only when every answered cluster has it ticked', () => {
    const w = (cluster: string, id: string, message = 'm') => ({ cluster, item: { id, message } })
    const key = (cluster: string, id: string, message = 'm') => `${cluster}\u0000${id}\u0000${message}`
    const warnings = [w('a', 'x'), w('b', 'x')]
    expect(ackIdsFor(['a', 'b'], warnings, new Set([key('a', 'x')]), new Set(['a', 'b']))).toEqual([])
    expect(ackIdsFor(['a', 'b'], warnings, new Set([key('a', 'x'), key('b', 'x')]), new Set(['a', 'b']))).toEqual(['x'])
    expect(ackIdsFor(['a', 'b'], [w('a', 'x')], new Set([key('a', 'x')]), new Set(['a']))).toEqual([])
    expect(ackIdsFor(['a', 'b'], [w('a', 'x')], new Set([key('a', 'x')]), new Set(['a', 'b']))).toEqual(['x'])
    // A tick on an unselected cluster does not count.
    expect(ackIdsFor(['a'], [w('a', 'x'), w('b', 'x')], new Set([key('b', 'x')]), new Set(['a', 'b']))).toEqual([])
  })

  it('S31: a 409 replaces the warnings of the clusters it names, so an old copy with other words goes', async () => {
    reachable()
    vi.mocked(api.adoptClusters).mockResolvedValueOnce({
      results: [
        { name: 'prod-cluster', status: 'success', warnings: [{ id: 'owner', message: 'Another tool owns prod today.' }], dry_run: preview },
        { name: 'staging-cluster', status: 'success', warnings: [{ id: 'appsets', message: 'Two ApplicationSets pick staging.' }], dry_run: preview },
      ],
    } as never)
    const user = userEvent.setup()
    openWith([prod, staging])
    await user.click(await screen.findByRole('button', { name: /preview changes/i }))
    await user.click(await screen.findByTestId('adopt-warning-owner'))
    await user.click(screen.getByTestId('adopt-warning-appsets'))
    vi.mocked(api.adoptClusters).mockRejectedValueOnce(
      conflict({
        error: 'needs acknowledgement',
        code: 'unacknowledged_findings',
        unacknowledged_findings: ['appsets'],
        warnings: [{ id: 'appsets', message: 'Three ApplicationSets pick staging.' }],
        results: [{ name: 'staging-cluster', status: 'skipped', warnings: [{ id: 'appsets', message: 'Three ApplicationSets pick staging.' }] }],
      }),
    )
    await user.click(screen.getByRole('button', { name: /confirm adoption/i }))
    expect(await screen.findByText('Three ApplicationSets pick staging.')).toBeInTheDocument()
    expect(screen.queryByText('Two ApplicationSets pick staging.')).not.toBeInTheDocument()
    expect(screen.getAllByTestId('adopt-warning-appsets')).toHaveLength(1)
    expect(warningOn('staging-cluster', 'appsets')).not.toBeChecked()
    // prod was not named: its warning and its tick stay.
    expect(warningOn('prod-cluster', 'owner')).toBeChecked()
  })

  it('S32: an answer with no results says nothing was adopted and stays on review', async () => {
    reachable()
    vi.mocked(api.adoptClusters).mockResolvedValue({ results: [] } as never)
    const user = userEvent.setup()
    openWith([prod])
    await user.click(await screen.findByRole('button', { name: /confirm adoption/i }))
    expect(await screen.findByText('Nothing was adopted. Sharko sent back no answer for any cluster. Try again.')).toBeInTheDocument()
    expect(screen.queryByText('Pull request opened, waiting for merge.')).not.toBeInTheDocument()
    expect(screen.queryByText('Adoption complete.')).not.toBeInTheDocument()
    expect(onSuccess).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: /confirm adoption/i })).toBeInTheDocument()
  })

  it('other_pr_open: shows the server sentence and a link to the open pull request', async () => {
    reachable()
    const sentence = 'A pull request to take over prod-cluster is already open. Merge or close it first.'
    vi.mocked(api.adoptClusters).mockRejectedValue(
      conflict({ error: sentence, code: 'other_pr_open', pr_number: 12, pr_url: 'https://git/pr/12' }),
    )
    const user = userEvent.setup()
    openWith([prod])
    await user.click(await screen.findByRole('button', { name: /confirm adoption/i }))
    expect(await screen.findByTestId('adopt-error')).toHaveTextContent(sentence)
    const link = screen.getByRole('link', { name: /View PR #12/ })
    expect(link).toHaveAttribute('href', 'https://git/pr/12')
    expect(screen.queryByText('There is something new to read. Tick each warning below, then confirm again.')).not.toBeInTheDocument()
  })

  it('other_pr_open: with no number the link never says "PR #0"', async () => {
    reachable()
    const sentence = 'A pull request to take over prod-cluster is already open. Merge or close it first.'
    vi.mocked(api.adoptClusters).mockRejectedValue(conflict({ error: sentence, code: 'other_pr_open', pr_url: 'https://git/pr/x' }))
    const user = userEvent.setup()
    openWith([prod])
    await user.click(await screen.findByRole('button', { name: /confirm adoption/i }))
    const link = await screen.findByRole('link', { name: /View PR/ })
    expect(link).toHaveAttribute('href', 'https://git/pr/x')
    expect(document.body.textContent).not.toMatch(/#0\b/)
  })
})

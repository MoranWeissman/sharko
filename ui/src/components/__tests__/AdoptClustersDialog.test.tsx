import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AdoptClustersDialog } from '../AdoptClustersDialog'
import type { Cluster } from '@/services/models'
import * as api from '@/services/api'

// Mock the API module
vi.mock('@/services/api', () => ({
  testClusterConnection: vi.fn(),
  adoptClusters: vi.fn(),
  isTestClusterUnavailable: vi.fn(),
}))

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
      } as any)

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

    it('keeps informational-not-verified clusters selected when credentials are not found', async () => {
      vi.mocked(api.isTestClusterUnavailable).mockReturnValue(false)
      vi.mocked(api.testClusterConnection).mockResolvedValue({
        success: false,
        stage: 'credentials',
        error_message: 'secret "cluster-creds" not found in AWS Secrets Manager',
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
        const failedElements = screen.getAllByText('Failed')
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
      } as any)

      vi.mocked(api.adoptClusters).mockResolvedValue({
        results: [{
          cluster: 'prod-cluster',
          success: true,
          pr_url: 'https://github.com/example/repo/pull/123',
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
        expect(screen.getByText('Passed')).toBeInTheDocument()
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
        expect(screen.getAllByText('Passed')).toHaveLength(2)
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
        expect(screen.getByText('Failed')).toBeInTheDocument()
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
      } as any)

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
})

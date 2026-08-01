import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { HomeClusterCard } from '@/components/HomeClusterCard'

describe('HomeClusterCard (dashboard facelift, Package 3)', () => {
  it('renders all four identity fields when everything is known', () => {
    render(
      <HomeClusterCard
        homeCluster={{ available: true, kubernetes_version: 'v1.29.0', node_count: 3, nodes_ready: 3 }}
        sharkoVersion="4.2.0"
        argocdVersion="2.11.0"
        argocdConnected
        uptime="3h12m"
      />,
    )

    expect(screen.getByText("Sharko's home cluster")).toBeInTheDocument()
    expect(screen.getByText('4.2.0')).toBeInTheDocument()
    expect(screen.getByText('2.11.0')).toBeInTheDocument()
    expect(screen.getByText('v1.29.0')).toBeInTheDocument()
    expect(screen.getByText('3')).toBeInTheDocument()
    expect(screen.getByText('all nodes ready')).toBeInTheDocument()
    expect(screen.getByText('up 3h12m · running in-cluster')).toBeInTheDocument()
  })

  it('shows a neutral "X/Y nodes ready" chip when not all nodes are ready — no severity color change', () => {
    render(
      <HomeClusterCard
        homeCluster={{ available: true, kubernetes_version: 'v1.29.0', node_count: 3, nodes_ready: 2 }}
        argocdConnected={false}
      />,
    )

    const chip = screen.getByText('2/3 nodes ready')
    expect(chip).toBeInTheDocument()
    expect(chip.className).not.toContain('green')
    expect(chip.className).not.toContain('red')
  })

  it('degrades every missing field to "—" independently — never errors', () => {
    render(
      <HomeClusterCard
        homeCluster={{ available: false, message: 'only available when running in-cluster' }}
        argocdConnected={false}
      />,
    )

    expect(screen.getByText("Sharko's home cluster")).toBeInTheDocument()
    // Sharko version, ArgoCD version, Kubernetes version, Nodes — all "—".
    expect(screen.getAllByText('—')).toHaveLength(4)
    expect(screen.getByText('only available when running in-cluster')).toBeInTheDocument()
    // No readiness chip when we don't know node counts.
    expect(screen.queryByText(/nodes ready/)).not.toBeInTheDocument()
  })

  it('shows Sharko + ArgoCD identity even when the Kubernetes-only home-cluster probe is unavailable', () => {
    // The old implementation swapped the WHOLE card body for the bare
    // message when `available: false` — Sharko's own version and the
    // ArgoCD connection come from separate, independent calls and
    // shouldn't disappear just because the K8s node probe failed.
    render(
      <HomeClusterCard
        homeCluster={{ available: false, message: 'only available when running in-cluster' }}
        sharkoVersion="4.2.0"
        argocdVersion="2.11.0"
        argocdConnected
      />,
    )

    expect(screen.getByText('4.2.0')).toBeInTheDocument()
    expect(screen.getByText('2.11.0')).toBeInTheDocument()
  })

  it('omits the footer entirely when uptime is unknown and the cluster is available', () => {
    render(
      <HomeClusterCard
        homeCluster={{ available: true, kubernetes_version: 'v1.29.0', node_count: 1, nodes_ready: 1 }}
        argocdConnected={false}
      />,
    )

    expect(screen.queryByText(/running in-cluster/)).not.toBeInTheDocument()
    expect(screen.queryByText(/^up /)).not.toBeInTheDocument()
  })

  it('drops the "running in-cluster" claim when the cluster is not available, even with a known uptime', () => {
    render(
      <HomeClusterCard
        homeCluster={{ available: false, message: 'only available when running in-cluster' }}
        argocdConnected={false}
        uptime="10m"
      />,
    )

    expect(screen.getByText('up 10m')).toBeInTheDocument()
    expect(screen.queryByText(/running in-cluster/)).not.toBeInTheDocument()
  })
})

import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import {
  ClusterTypeBadge,
  classifyClusterType,
} from '@/components/ClusterTypeBadge'

/**
 * Story V125-1-10.4 acceptance tests. Validates the hostname → pill mapping
 * defined in design doc 2026-05-13 §2.3 and exercises the documented edge
 * cases (empty / malformed / port / IPv6 / custom DNS).
 *
 * Implementation choice: the badge always renders something (never returns
 * `null`) — a real-but-unrecognized hostname falls through to `Self-hosted`,
 * while an empty/missing/malformed (no address at all) input renders
 * `Unknown` (V2-cleanup-77.1) — so the column is never visually empty.
 */
describe('ClusterTypeBadge — hostname → pill mapping (design §2.3)', () => {
  it('1. *.eks.amazonaws.com → "EKS" with orange variant', () => {
    const { container } = render(
      <ClusterTypeBadge server="https://abc123.gr7.us-east-1.eks.amazonaws.com" />,
    )
    expect(screen.getByText('EKS')).toBeInTheDocument()
    expect(container.innerHTML).toMatch(/bg-orange-100/)
    expect(container.innerHTML).toMatch(/text-orange-800/)
  })

  it('2. *.azmk8s.io → "AKS" with sky-blue variant', () => {
    const { container } = render(
      <ClusterTypeBadge server="https://my-aks-prod.hcp.eastus.azmk8s.io" />,
    )
    expect(screen.getByText('AKS')).toBeInTheDocument()
    expect(container.innerHTML).toMatch(/bg-sky-100/)
    expect(container.innerHTML).toMatch(/text-sky-800/)
  })

  it('3. *.gke.io → "GKE" with red variant', () => {
    const { container } = render(
      <ClusterTypeBadge server="https://control-plane.gke.io" />,
    )
    expect(screen.getByText('GKE')).toBeInTheDocument()
    expect(container.innerHTML).toMatch(/bg-red-100/)
    expect(container.innerHTML).toMatch(/text-red-800/)
  })

  it('4. *.googleapis.com → "GKE" with red variant', () => {
    const { container } = render(
      <ClusterTypeBadge server="https://1.2.3.4.container.googleapis.com" />,
    )
    expect(screen.getByText('GKE')).toBeInTheDocument()
    expect(container.innerHTML).toMatch(/bg-red-100/)
  })

  it('5. kind-prefixed hostname → "kind" (neutral palette)', () => {
    render(<ClusterTypeBadge server="https://kind-test-1:6443" />)
    expect(screen.getByText('kind')).toBeInTheDocument()
  })

  it('6. localhost → "kind"', () => {
    render(<ClusterTypeBadge server="https://localhost:6443" />)
    expect(screen.getByText('kind')).toBeInTheDocument()
  })

  it('7. 127.0.0.1 → "kind"', () => {
    render(<ClusterTypeBadge server="https://127.0.0.1:6443" />)
    expect(screen.getByText('kind')).toBeInTheDocument()
  })

  it('8. *.minikube.io → "minikube"', () => {
    render(<ClusterTypeBadge server="https://api.minikube.io" />)
    expect(screen.getByText('minikube')).toBeInTheDocument()
  })

  it('9. custom DNS (k8s.example.com) → "Self-hosted"', () => {
    render(<ClusterTypeBadge server="https://k8s.example.com" />)
    expect(screen.getByText('Self-hosted')).toBeInTheDocument()
  })

  it('9b. real non-cloud IP address (10.0.0.1) → "Self-hosted" (catch-all intact, V2-cleanup-77.1)', () => {
    render(<ClusterTypeBadge server="https://10.0.0.1:6443" />)
    expect(screen.getByText('Self-hosted')).toBeInTheDocument()
  })

  it('9c. real non-cloud on-prem hostname → "Self-hosted" (catch-all intact, V2-cleanup-77.1)', () => {
    render(<ClusterTypeBadge server="https://onprem.example.com:6443" />)
    expect(screen.getByText('Self-hosted')).toBeInTheDocument()
  })

  it('10. empty string → "Unknown" (no crash) [V2-cleanup-77.1]', () => {
    render(<ClusterTypeBadge server="" />)
    expect(screen.getByText('Unknown')).toBeInTheDocument()
  })

  it('11. malformed URL → "Unknown" (no crash, no thrown exception) [V2-cleanup-77.1]', () => {
    expect(() =>
      render(<ClusterTypeBadge server="not-a-url" />),
    ).not.toThrow()
    expect(screen.getByText('Unknown')).toBeInTheDocument()
  })

  it('12. URL with port stripped — "https://kind-test-1:6443" → "kind"', () => {
    render(<ClusterTypeBadge server="https://kind-test-1:6443" />)
    expect(screen.getByText('kind')).toBeInTheDocument()
  })
})

describe('ClusterTypeBadge — additional edge cases', () => {
  it('IPv6 [::1] → "Self-hosted" (not "kind"; spec says exact "127.0.0.1" only)', () => {
    render(<ClusterTypeBadge server="https://[::1]:6443" />)
    expect(screen.getByText('Self-hosted')).toBeInTheDocument()
  })

  it('undefined server → "Unknown" [V2-cleanup-77.1]', () => {
    render(<ClusterTypeBadge server={undefined} />)
    expect(screen.getByText('Unknown')).toBeInTheDocument()
  })

  it('whitespace-only server → "Unknown" [V2-cleanup-77.1]', () => {
    render(<ClusterTypeBadge server="   " />)
    expect(screen.getByText('Unknown')).toBeInTheDocument()
  })

  it('hostname matching is case-insensitive (EKS uppercase)', () => {
    render(
      <ClusterTypeBadge server="https://ABC123.GR7.US-EAST-1.EKS.AMAZONAWS.COM" />,
    )
    expect(screen.getByText('EKS')).toBeInTheDocument()
  })

  it('trailing slash + path are handled — eks URL with path stays "EKS"', () => {
    render(
      <ClusterTypeBadge server="https://abc123.eks.amazonaws.com/healthz?x=1" />,
    )
    expect(screen.getByText('EKS')).toBeInTheDocument()
  })

  it('compact mode applies tile-size classes', () => {
    const { container } = render(
      <ClusterTypeBadge server="https://kind-x" compact />,
    )
    expect(container.innerHTML).toContain('text-xs')
  })

  it('renders an aria-label and a title tooltip', () => {
    render(<ClusterTypeBadge server="https://x.eks.amazonaws.com" />)
    const badge = screen.getByLabelText('Cluster type: EKS')
    expect(badge).toBeInTheDocument()
    expect(badge.getAttribute('title')).toContain(
      'detected from API server hostname',
    )
  })

  it('Self-hosted tooltip explains that detection is heuristic', () => {
    render(<ClusterTypeBadge server="https://k8s.example.com" />)
    const badge = screen.getByLabelText('Cluster type: Self-hosted')
    expect(badge.getAttribute('title')).toMatch(/Self-hosted|unrecognized/)
  })

  it('palette guard — neutral pills do NOT use bg-gray-* / text-gray-* Tailwind utilities in light mode', () => {
    const { container: kindC } = render(
      <ClusterTypeBadge server="https://kind-foo" />,
    )
    const { container: selfC } = render(
      <ClusterTypeBadge server="https://k8s.example.com" />,
    )
    const { container: unknownC } = render(<ClusterTypeBadge server="" />)
    expect(kindC.innerHTML).not.toMatch(/\bbg-gray-/)
    expect(kindC.innerHTML).not.toMatch(/\btext-gray-/)
    expect(selfC.innerHTML).not.toMatch(/\bbg-gray-/)
    expect(selfC.innerHTML).not.toMatch(/\btext-gray-/)
    expect(unknownC.innerHTML).not.toMatch(/\bbg-gray-/)
    expect(unknownC.innerHTML).not.toMatch(/\btext-gray-/)
  })
})

describe('ClusterTypeBadge — "Unknown" (no API server address, V2-cleanup-77.1)', () => {
  it('renders visible "Unknown" text — never blank', () => {
    render(<ClusterTypeBadge server="" />)
    expect(screen.getByText('Unknown')).toBeInTheDocument()
  })

  it('renders visible "Unknown" text in compact mode — never blank', () => {
    render(<ClusterTypeBadge server={undefined} compact />)
    expect(screen.getByText('Unknown')).toBeInTheDocument()
  })

  it('uses muted/neutral styling distinct from Self-hosted — not red, not green', () => {
    const { container } = render(<ClusterTypeBadge server="" />)
    expect(container.innerHTML).not.toMatch(/\b(?:bg|text|ring)-red-/)
    expect(container.innerHTML).not.toMatch(/\b(?:bg|text|ring)-green-/)
    expect(container.innerHTML).not.toMatch(/\b(?:bg|text|ring)-emerald-/)
  })

  it('has an aria-label of "Cluster type: Unknown"', () => {
    render(<ClusterTypeBadge server="" />)
    expect(screen.getByLabelText('Cluster type: Unknown')).toBeInTheDocument()
  })

  it('tooltip explains no address is available, not a mis-detection', () => {
    render(<ClusterTypeBadge server="" />)
    const badge = screen.getByLabelText('Cluster type: Unknown')
    expect(badge.getAttribute('title')).toMatch(
      /No API server address is available/,
    )
  })
})

describe('classifyClusterType — pure helper (no DOM)', () => {
  it('exports a pure classifier helper for non-render callers', () => {
    expect(classifyClusterType('https://x.eks.amazonaws.com')).toBe('EKS')
    expect(classifyClusterType('https://x.azmk8s.io')).toBe('AKS')
    expect(classifyClusterType('https://x.gke.io')).toBe('GKE')
    expect(classifyClusterType('https://kind-y')).toBe('kind')
    expect(classifyClusterType('https://x.minikube.io')).toBe('minikube')
    expect(classifyClusterType('https://k8s.example.com')).toBe('Self-hosted')
    expect(classifyClusterType('https://10.0.0.1:6443')).toBe('Self-hosted')
    // No address at all → 'Unknown' (V2-cleanup-77.1), not 'Self-hosted'.
    expect(classifyClusterType(undefined)).toBe('Unknown')
    expect(classifyClusterType('')).toBe('Unknown')
    expect(classifyClusterType('   ')).toBe('Unknown')
    expect(classifyClusterType('garbage')).toBe('Unknown')
  })
})

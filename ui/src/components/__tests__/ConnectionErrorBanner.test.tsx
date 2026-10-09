import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { ConnectionErrorBanner } from '@/components/ConnectionErrorBanner'
import type { RepoStatusReason } from '@/services/api'

// Review findings r1, M10 — the banner had zero tests before this. One
// assertion per reason it handles: the heading it renders, whether the
// "Review and repair" button appears (only for the engine-app case, per
// H1: a couldn't-check reason like argocd_forbidden must NOT offer repair),
// and that the non-repair action always points at Settings → Connections.
function renderBanner(reason?: RepoStatusReason, onOpenRepair = vi.fn()) {
  return render(
    <MemoryRouter>
      <ConnectionErrorBanner reason={reason} onOpenRepair={onOpenRepair} />
    </MemoryRouter>,
  )
}

describe('ConnectionErrorBanner', () => {
  it('no_connection — heading matches "no connection configured", not a failed reach attempt (L17)', () => {
    renderBanner('no_connection')
    expect(screen.getByText('No Git connection is set up.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /review and repair/i })).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: /settings/i })).toHaveAttribute('href', '/settings?section=connections')
  })

  it('connection_error — heading names the Git connection problem', () => {
    renderBanner('connection_error')
    expect(screen.getByText("Sharko can't reach your Git connection right now.")).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /review and repair/i })).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: /settings/i })).toHaveAttribute('href', '/settings?section=connections')
  })

  it('not_bootstrapped — falls through to the default engine-app heading with a repair button', () => {
    // "not_bootstrapped" only ever reaches the banner via
    // shouldShowConnectionErrorBanner's initialized-but-unhealthy branch in
    // practice, but bannerContent's default arm must handle any reason
    // it doesn't special-case without crashing or mislabeling it as a
    // connection problem.
    renderBanner('not_bootstrapped' as RepoStatusReason)
    expect(screen.getByText("There's an issue with Sharko's engine app.")).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /review and repair/i })).toBeInTheDocument()
  })

  it('bootstrap_unreachable — heading names ArgoCD not the repo, no repair button', () => {
    renderBanner('bootstrap_unreachable')
    expect(screen.getByText("ArgoCD can't reach your Git repo right now.")).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /review and repair/i })).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: /settings/i })).toHaveAttribute('href', '/settings?section=connections')
  })

  it('bootstrap_degraded — the ONE reason with a repair button (Sharko actually found the engine app broken)', () => {
    renderBanner('bootstrap_degraded')
    expect(screen.getByText("There's an issue with Sharko's engine app.")).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /review and repair/i })).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /settings/i })).not.toBeInTheDocument()
  })

  it('argocd_auth_failed — heading names the rejected credential, no repair button', () => {
    renderBanner('argocd_auth_failed')
    expect(screen.getByText("Sharko's ArgoCD credential is no longer valid.")).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /review and repair/i })).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: /settings/i })).toHaveAttribute('href', '/settings?section=connections')
  })

  it('argocd_unreachable — heading says Sharko could not reach ArgoCD, no repair button', () => {
    renderBanner('argocd_unreachable')
    expect(screen.getByText("Sharko can't reach ArgoCD right now.")).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /review and repair/i })).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: /settings/i })).toHaveAttribute('href', '/settings?section=connections')
  })

  // H1 — a 403 means the token is valid but lacks permission. Sharko never
  // got to check the engine app, so this must be a couldn't-check message
  // with NO repair button (repairing the engine app is not the fix for a
  // permissions problem).
  it('argocd_forbidden — heading names the refused permission, NO repair button', () => {
    renderBanner('argocd_forbidden')
    expect(
      screen.getByText("ArgoCD refused Sharko's token permission to read applications."),
    ).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /review and repair/i })).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: /settings/i })).toHaveAttribute('href', '/settings?section=connections')
  })

  it('clicking "Review and repair" calls onOpenRepair (engine-app case only)', () => {
    const onOpenRepair = vi.fn()
    renderBanner('bootstrap_degraded', onOpenRepair)
    screen.getByRole('button', { name: /review and repair/i }).click()
    expect(onOpenRepair).toHaveBeenCalledTimes(1)
  })

  it('L18 — the Settings link uses a non-external icon, not ExternalLink', () => {
    const { container } = renderBanner('argocd_unreachable')
    const link = screen.getByRole('link', { name: /settings/i })
    // lucide's ExternalLink renders a distinctive box+arrow path; simplest
    // stable check is that no element carries the external-link-specific
    // class lucide assigns, and the link still renders an svg icon.
    expect(container.querySelector('.lucide-external-link')).not.toBeInTheDocument()
    expect(link.querySelector('svg')).toBeInTheDocument()
  })
})

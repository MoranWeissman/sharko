import { useConnectionsOptional } from '@/hooks/useConnections'
import { gitHostLabel } from '@/lib/gitHost'

/**
 * useGitHost — the active connection's Git host, for wording that names it.
 * `label` is "GitHub" / "Gitea" / "Azure DevOps", or null when unknown
 * (outside a ConnectionProvider, or before connections load). `provider` is
 * the raw git_provider value.
 */
export function useGitHost(): { provider: string | null; label: string | null } {
  const ctx = useConnectionsOptional()
  const active =
    ctx?.connections.find((c) => c.name === ctx.activeConnection) ?? ctx?.connections[0]
  const provider = active?.git_provider ?? null
  return { provider, label: gitHostLabel(provider) }
}

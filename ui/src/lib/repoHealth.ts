// Pure repo/ArgoCD-connection derivations, split out of SystemView.tsx
// (WQ-3, attention-move-badges) so the nav badge hook can read the exact
// same "is the machinery broken" verdict SystemView renders, without
// pulling SystemView's whole component tree (and its recharts-adjacent
// imports) into the always-mounted Layout bundle. SystemView.tsx re-exports
// these so existing imports (`from '@/views/SystemView'`) keep working.

export type ArrowStatus = 'healthy' | 'degraded' | 'unknown'

export interface RepoStatus {
  initialized: boolean
  bootstrap_synced?: boolean
  reason?: string
}

export interface ArrowVerdict {
  status: ArrowStatus
  detail: string
}

/** Arrow 1 — Sharko's own connection to the Git repo, from GET /repo/status. */
export function deriveSharkoRepoArrow(repo: RepoStatus | null): ArrowVerdict {
  if (!repo) {
    return { status: 'unknown', detail: "Couldn't determine the Git connection status." }
  }
  if (repo.reason === 'no_connection') {
    return {
      status: 'degraded',
      detail: 'No Git connection is configured. Sharko needs one for every commit and pull request.',
    }
  }
  if (repo.reason === 'connection_error' || repo.reason === 'error') {
    return {
      status: 'degraded',
      detail: "Sharko can't reach the Git repo right now (network, TLS, or auth problem).",
    }
  }
  if (repo.initialized) {
    return { status: 'healthy', detail: 'Sharko can read and write the repo.' }
  }
  if (repo.reason === 'not_bootstrapped') {
    return {
      status: 'healthy',
      detail: "Sharko can reach the repo — it just hasn't been initialized yet.",
    }
  }
  return { status: 'unknown', detail: "Couldn't determine the Git connection status." }
}

/** Arrow 2 — ArgoCD's own connection to the Git repo, from GET /repo/status. */
export function deriveArgoRepoArrow(repo: RepoStatus | null): ArrowVerdict {
  if (!repo) {
    return { status: 'unknown', detail: "Couldn't determine ArgoCD's repo sync status." }
  }
  if (!repo.initialized) {
    return {
      status: 'unknown',
      detail: "Can't assess until the repo is set up — ArgoCD has nothing to sync yet.",
    }
  }
  if (repo.bootstrap_synced) {
    return { status: 'healthy', detail: 'ArgoCD is syncing the repo — the engine app is healthy.' }
  }
  if (repo.reason === 'bootstrap_unreachable') {
    return {
      status: 'degraded',
      detail:
        "ArgoCD can't reach the repo (a connection problem — often a proxy or TLS trust issue on the ArgoCD side).",
    }
  }
  // Error review package 1: these two reasons mean Sharko never got a
  // usable answer from ArgoCD at all — distinct from the fallthrough below,
  // which asserts ArgoCD actually looked at the engine app and found it
  // degraded or missing. Asserting that here would be a claim Sharko never
  // verified.
  if (repo.reason === 'argocd_auth_failed') {
    return {
      status: 'degraded',
      detail: "ArgoCD rejected Sharko's token, so Sharko can't confirm whether the repo is synced.",
    }
  }
  if (repo.reason === 'argocd_unreachable') {
    return {
      status: 'unknown',
      detail: "Sharko couldn't reach ArgoCD to check the repo's sync status.",
    }
  }
  return {
    status: 'degraded',
    detail: 'ArgoCD read the repo but the engine app is degraded or missing.',
  }
}

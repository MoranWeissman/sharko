/**
 * useNavBadges — nav-wide "unread" counters for the sidebar (WQ-3,
 * attention-move-badges).
 *
 * Two counts, each grounded in an EXISTING signal so the badge never
 * invents a new severity vocabulary:
 *
 *   - observability: confirmed (non-settling) addon problems + failed/
 *     missing cluster connections — the EXACT same number the Dashboard's
 *     thin attention line shows (see getConfirmedProblemCount in
 *     components/AttentionSection.tsx — one shared computation, two
 *     mirrors).
 *
 *   - system: real broken-machinery signals only — ArgoCD unreachable
 *     (the same honesty rule ArgoCDStatusBanner uses: the /clusters fetch
 *     itself failing, not a heuristic over per-cluster status strings) and
 *     the Git connection being down (SystemView's own
 *     deriveSharkoRepoArrow / deriveArgoRepoArrow going 'degraded' —
 *     imported from lib/repoHealth.ts, not re-derived).
 *
 * Layout wraps every route, so this hook polls independently of whatever
 * page is mounted, on the same 30s cadence as the rest of the app's
 * background polling (see fetchPRCount in Layout.tsx). It does NOT add a
 * duplicate addon-state poll — useAddonStates() already reads from the
 * single app-wide AddonStatesProvider loop.
 */
import { useEffect, useState } from 'react'
import { api } from '@/services/api'
import { useAddonStates } from '@/hooks/useAddonStates'
import { getConfirmedProblemCount } from '@/components/AttentionSection'
import { deriveSharkoRepoArrow, deriveArgoRepoArrow } from '@/lib/repoHealth'

const POLL_INTERVAL_MS = 30_000

export interface NavBadgeCounts {
  /** Observability nav entry — mirrors the dashboard's thin attention line. */
  observability: number
  /** System nav entry — real broken-machinery signals only. */
  system: number
}

export function useNavBadges(): NavBadgeCounts {
  const { byApp } = useAddonStates()
  const [clusterProblemCount, setClusterProblemCount] = useState(0)
  const [argoUnreachable, setArgoUnreachable] = useState(false)
  const [gitConnectionDown, setGitConnectionDown] = useState(false)

  useEffect(() => {
    let cancelled = false

    const poll = () => {
      api
        .getDashboardStats()
        .then((stats) => {
          if (cancelled) return
          setClusterProblemCount((stats?.clusters?.failed ?? 0) + (stats?.clusters?.missing ?? 0))
        })
        .catch(() => {
          if (!cancelled) setClusterProblemCount(0)
        })

      // Same honesty rule as ArgoCDStatusBanner (Dashboard.tsx): the ONLY
      // signal for "ArgoCD unreachable" is the /clusters fetch itself
      // failing — never a heuristic over per-cluster connection strings.
      api
        .getClusters()
        .then(() => {
          if (!cancelled) setArgoUnreachable(false)
        })
        .catch(() => {
          if (!cancelled) setArgoUnreachable(true)
        })

      api
        .getRepoStatus()
        .then((repo) => {
          if (cancelled) return
          const sharko = deriveSharkoRepoArrow(repo)
          const argo = deriveArgoRepoArrow(repo)
          setGitConnectionDown(sharko.status === 'degraded' || argo.status === 'degraded')
        })
        .catch(() => {
          if (!cancelled) setGitConnectionDown(false)
        })
    }

    poll()
    const id = setInterval(poll, POLL_INTERVAL_MS)
    return () => {
      cancelled = true
      clearInterval(id)
    }
  }, [])

  return {
    observability: getConfirmedProblemCount(byApp, clusterProblemCount),
    system: (argoUnreachable ? 1 : 0) + (gitConnectionDown ? 1 : 0),
  }
}

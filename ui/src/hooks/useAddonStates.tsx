/**
 * Single source of truth for addon health/sync state across the Sharko UI.
 *
 * Design:
 *   - One Provider mounted high in the tree polls /api/v1/dashboard/attention
 *     every 30s and turns the response into a typed map keyed by both:
 *       • per-app:    "<addon>@<cluster>"
 *       • per-addon:  "<addon>"  (worst state across all clusters)
 *   - Apps absent from the attention list are healthy by definition
 *     (the backend handler skips them — see internal/api/dashboard.go).
 *   - Consumers call `useAddonStates()` to get the cache, or the helper
 *     `useAddonState(addon, cluster?)` to look one up directly. Both
 *     return the same `displayState` enum so downstream UI doesn't have
 *     to redo the Healthy/Progressing/Degraded mapping.
 *
 * displayState semantics — locked with maintainer:
 *   • 'healthy'              → Argo says Healthy + Synced. Green.
 *   • 'progressing-advisory' → Argo says Progressing. NOT a hard error;
 *                              show a small advisory chip with a link to
 *                              the addon-on-cluster page for investigation.
 *                              Treated as "operational" for dashboard
 *                              "apps with issues" counters.
 *   • 'degraded'             → Argo says Degraded / Error. Real failure. Red.
 *   • 'missing'              → Application missing in ArgoCD. Red.
 *   • 'unknown'              → ArgoCD can't determine state, OR the app is
 *                              Suspended (an intentional pause, not a
 *                              failure — Argo's own precedent). Amber/grey,
 *                              NOT counted as a red problem (dashboard UX
 *                              review 2026-08-01, finding M: Unknown used to
 *                              read as a hard failure; Suspended used to be
 *                              folded into 'degraded', which is harsher than
 *                              ArgoCD treats it).
 */
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import type { ReactNode } from 'react'
import { api } from '@/services/api'

export type AddonDisplayState =
  | 'healthy'
  | 'progressing-advisory'
  | 'degraded'
  | 'missing'
  | 'unknown'

export interface AddonState {
  /** ArgoCD app name (typically `<addon>-<cluster>`). Empty when synthesised. */
  appName: string
  addonName: string
  cluster: string
  /** Raw ArgoCD health status (Healthy / Progressing / Degraded / Missing / Unknown). */
  healthStatus: string
  /** Raw ArgoCD sync status (Synced / OutOfSync / Unknown). */
  syncStatus: string
  /** Mapped display state — what the UI should render. */
  displayState: AddonDisplayState
  /** Short, user-facing advisory text (Progressing/Degraded reason). */
  advisoryMessage?: string
  /** Type of error from ArgoCD conditions, if any. */
  errorType?: string
  /** Last time this state was observed by the cache. */
  lastSeen: number
  /**
   * Epoch ms when this app MOST RECENTLY entered a bad state (displayState
   * 'degraded' or 'missing'), tracked client-side so the dashboard can give
   * a freshly-broken app a settling window instead of screaming red the
   * instant it tips over (dashboard UX review 2026-08-01, finding H6).
   * undefined when the app is not currently in a bad state. Recovering
   * (leaving degraded/missing, including disappearing from the attention
   * feed entirely because it went Healthy) resets the clock — the NEXT
   * time it goes bad, the window starts over.
   *
   * Honesty note: this clock lives only in memory in the browser tab. A
   * page reload restarts it, so a genuinely-been-broken-for-an-hour app
   * can briefly re-show as "settling" right after a reload. Accepted
   * trade-off — see isSettling() below.
   */
  badSince?: number
}

/** How long a degraded/missing app gets the benefit of the doubt before it
 * counts as a confirmed (red) problem instead of an amber "settling" one. */
export const SETTLING_WINDOW_MS = 10 * 60 * 1000

/**
 * isSettling — true when `state` is currently in a bad state (degraded/
 * missing) AND it entered that state less than SETTLING_WINDOW_MS ago.
 * false for anything else, including apps with no badSince (never bad, or
 * currently healthy/progressing/unknown).
 */
export function isSettling(state: AddonState, nowMs: number = Date.now()): boolean {
  if (state.badSince === undefined) return false
  if (state.displayState !== 'degraded' && state.displayState !== 'missing') return false
  return nowMs - state.badSince < SETTLING_WINDOW_MS
}

export interface AddonStatesContextValue {
  /** Map keyed by `<addon>@<cluster>` for per-app lookups. */
  byApp: Map<string, AddonState>
  /** Map keyed by `<addon>` — value is the WORST state across all clusters. */
  byAddon: Map<string, AddonState>
  loading: boolean
  error: string | null
  /** Force a refresh; used by manual refresh buttons. */
  refresh: () => void
  /** Last successful fetch timestamp (ms since epoch). */
  lastFetched: number | null
}

const AddonStatesContext = createContext<AddonStatesContextValue | null>(null)

const POLL_INTERVAL_MS = 30_000

/**
 * mapHealthToDisplayState — single mapping function used everywhere. Keeping
 * this here (and exporting it for tests) is what guarantees consistency:
 * if a screen wants to render addon state, it goes through `displayState`,
 * not its own copy of these branches.
 */
export function mapHealthToDisplayState(
  health: string,
  sync: string,
): AddonDisplayState {
  const h = (health || '').toLowerCase()
  const s = (sync || '').toLowerCase()
  if (h === 'healthy' && (s === 'synced' || s === '')) return 'healthy'
  if (h === 'healthy') {
    // Healthy but OutOfSync — not a Progressing-advisory either; treat as
    // healthy at the top level (the Argo OutOfSync nuance shows in the
    // Sync badge separately on detail pages).
    return 'healthy'
  }
  if (h === 'progressing') return 'progressing-advisory'
  if (h === 'missing') return 'missing'
  if (h === 'degraded' || h === 'error') return 'degraded'
  // Suspended is an intentional pause (ArgoCD's own precedent), not a
  // failure — it used to fold into 'degraded' (red), which was harsher
  // than ArgoCD itself treats it. Same neutral bucket as Unknown.
  if (h === 'unknown' || h === '' || h === 'suspended') return 'unknown'
  return 'unknown'
}

/**
 * worseState — picks the more severe display state of two. Used to roll up
 * a per-addon state from N per-cluster observations.
 *
 * Severity (high → low):
 *   missing > degraded > unknown > progressing-advisory > healthy
 */
function worseState(
  a: AddonDisplayState,
  b: AddonDisplayState,
): AddonDisplayState {
  const rank: Record<AddonDisplayState, number> = {
    missing: 5,
    degraded: 4,
    unknown: 3,
    'progressing-advisory': 2,
    healthy: 1,
  }
  return rank[a] >= rank[b] ? a : b
}

/**
 * AddonStatesProvider — mount once near the top of the authenticated tree.
 * Owns the single poll loop. Consumers subscribe via useAddonStates / useAddonState.
 */
export function AddonStatesProvider({ children }: { children: ReactNode }) {
  const [byApp, setByApp] = useState<Map<string, AddonState>>(new Map())
  const [byAddon, setByAddon] = useState<Map<string, AddonState>>(new Map())
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [lastFetched, setLastFetched] = useState<number | null>(null)
  const refreshRef = useRef<number>(0)
  // First-seen-in-bad-state clock, keyed by `<addon>@<cluster>`. A plain
  // ref (not state) — it's read-modify-write bookkeeping, not something
  // that should trigger its own re-render. In-memory only; see the
  // honesty note on AddonState.badSince.
  const badSinceRef = useRef<Map<string, number>>(new Map())

  const fetchAttention = useCallback(async () => {
    try {
      setError(null)
      // /dashboard/attention only returns apps with issues. Healthy apps are
      // omitted; consumers should treat "addon not in the map" as Healthy
      // (see useAddonState fallback below).
      const items = await api.getAttentionItems()
      const now = Date.now()
      const nextByApp = new Map<string, AddonState>()
      const nextByAddon = new Map<string, AddonState>()
      const currentBadKeys = new Set<string>()

      for (const item of items || []) {
        const display = mapHealthToDisplayState(item.health, item.sync)
        const appKey = `${item.addon_name}@${item.cluster}`

        let badSince: number | undefined
        if (display === 'degraded' || display === 'missing') {
          currentBadKeys.add(appKey)
          const existing = badSinceRef.current.get(appKey)
          badSince = existing ?? now
          badSinceRef.current.set(appKey, badSince)
        }

        const state: AddonState = {
          appName: item.app_name,
          addonName: item.addon_name,
          cluster: item.cluster,
          healthStatus: item.health || 'Unknown',
          syncStatus: item.sync || 'Unknown',
          displayState: display,
          advisoryMessage: item.error || undefined,
          errorType: item.error_type || undefined,
          lastSeen: now,
          badSince,
        }

        nextByApp.set(appKey, state)

        // Roll-up per addon — keep the worst state observed.
        const existingRollup = nextByAddon.get(item.addon_name)
        if (!existingRollup) {
          nextByAddon.set(item.addon_name, state)
        } else {
          const worse = worseState(existingRollup.displayState, display)
          nextByAddon.set(
            item.addon_name,
            worse === existingRollup.displayState ? existingRollup : state,
          )
        }
      }

      // Recovered apps (no longer bad — including ones that dropped off
      // the attention feed entirely because they went Healthy) reset the
      // clock, so the NEXT time they go bad the settling window restarts.
      for (const key of badSinceRef.current.keys()) {
        if (!currentBadKeys.has(key)) badSinceRef.current.delete(key)
      }

      setByApp(nextByApp)
      setByAddon(nextByAddon)
      setLastFetched(now)
    } catch (e: unknown) {
      // Fail soft — keep the previous cache so the UI doesn't flap to red
      // on a transient 503. Surface the error so consumers can show a
      // staleness warning if they want.
      setError(e instanceof Error ? e.message : 'Failed to load addon states')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void fetchAttention()
    const id = setInterval(() => void fetchAttention(), POLL_INTERVAL_MS)
    return () => clearInterval(id)
  }, [fetchAttention, refreshRef])

  const refresh = useCallback(() => {
    refreshRef.current += 1
    void fetchAttention()
  }, [fetchAttention])

  const value = useMemo<AddonStatesContextValue>(
    () => ({ byApp, byAddon, loading, error, refresh, lastFetched }),
    [byApp, byAddon, loading, error, refresh, lastFetched],
  )

  return (
    <AddonStatesContext value={value}>
      {children}
    </AddonStatesContext>
  )
}

/**
 * useAddonStates — primary subscription hook. Returns the full cache.
 * Throws if not wrapped in AddonStatesProvider so missing-provider bugs
 * surface immediately (rather than silently returning empty data).
 */
export function useAddonStates(): AddonStatesContextValue {
  const ctx = useContext(AddonStatesContext)
  if (!ctx) {
    throw new Error('useAddonStates must be used within AddonStatesProvider')
  }
  return ctx
}

/**
 * useAddonState — convenience: look up one addon, optionally pinned to a
 * cluster. When the addon isn't in the cache it's Healthy (since the source
 * /dashboard/attention only emits non-Healthy apps).
 */
export function useAddonState(
  addonName: string,
  cluster?: string,
): AddonState {
  const { byApp, byAddon } = useAddonStates()
  const key = cluster ? `${addonName}@${cluster}` : addonName
  const found = (cluster ? byApp.get(key) : byAddon.get(key)) || null
  if (found) return found
  // Default-Healthy fallback — addon isn't on the attention list.
  return {
    appName: '',
    addonName,
    cluster: cluster || '',
    healthStatus: 'Healthy',
    syncStatus: 'Synced',
    displayState: 'healthy',
    // 0, not Date.now() — nothing reads lastSeen on this fallback path
    // today, and Date.now() is an impure call React disallows during
    // render (it would also make this synthetic object non-deterministic
    // between renders for no benefit).
    lastSeen: 0,
  }
}

/**
 * deepLinkToAddonOnCluster — central place to build the URL the maintainer
 * asked for: "link directly to the addon on the cluster page should be for
 * quick ref". Lives next to the state model so consumers don't reinvent
 * the URL shape.
 */
export function deepLinkToAddonOnCluster(
  addonName: string,
  cluster: string,
): string {
  return `/clusters/${encodeURIComponent(cluster)}?section=addons&addon=${encodeURIComponent(addonName)}`
}

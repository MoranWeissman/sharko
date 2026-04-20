import { ShieldCheck, ShieldAlert, ShieldQuestion } from 'lucide-react'
import { cn } from '@/lib/utils'
import type { CatalogScore, CatalogSecurityTier } from '@/services/models'

/**
 * ScorecardBadge — renders the OpenSSF Scorecard score + tier label that
 * accompanies every Marketplace tile (and, eventually, Search results +
 * Configure step). Tier mapping mirrors v1.21 design §4.6:
 *
 *   ≥ 8.0 → Strong   (green)
 *   ≥ 5.0 → Moderate (amber)
 *   <  5  → Weak     (red)
 *   ?     → unknown  (grey, tooltip explains the daily refresh)
 *
 * Color choices use explicit hex values rather than Tailwind palette tokens so
 * the badge stays readable on the blue-tinted Sharko light theme.
 */

export interface ScorecardBadgeProps {
  score: CatalogScore | undefined
  /**
   * Optional explicit tier from the API response. When omitted the component
   * derives one from `score`, so callers that haven't fetched the typed entry
   * yet (e.g. lite list payloads) still render correctly.
   */
  tier?: CatalogSecurityTier
  /** ISO date the score was last refreshed. Surfaced via title= tooltip. */
  updated?: string
  /** When set, badge is wrapped in a focusable element and renders a tooltip. */
  scorecardURL?: string
  size?: 'sm' | 'md'
  /**
   * v1.21 QA Bundle 4 Fix #3d: when true, the badge renders nothing when the
   * score is "unknown". Default false (legacy behaviour). Callers that render
   * a Marketplace tile set this to true so the "Unknown" chip doesn't flood
   * every card with placeholder metadata before the daily OpenSSF refresh
   * job has populated real scores.
   */
  hideWhenUnknown?: boolean
}

const TIER_PALETTES: Record<
  Exclude<CatalogSecurityTier, ''> | 'unknown',
  { bg: string; text: string; ring: string; icon: typeof ShieldCheck }
> = {
  Strong: {
    bg: '#dcfce7',
    text: '#166534',
    ring: '#86efac',
    icon: ShieldCheck,
  },
  Moderate: {
    bg: '#fef3c7',
    text: '#854d0e',
    ring: '#fcd34d',
    icon: ShieldAlert,
  },
  Weak: {
    bg: '#fee2e2',
    text: '#991b1b',
    ring: '#fca5a5',
    icon: ShieldAlert,
  },
  unknown: {
    bg: '#e5e7eb',
    text: '#374151',
    ring: '#d1d5db',
    icon: ShieldQuestion,
  },
}

function deriveTier(score: CatalogScore | undefined): keyof typeof TIER_PALETTES {
  if (score === undefined || score === 'unknown') return 'unknown'
  const n = typeof score === 'number' ? score : Number(score)
  if (Number.isNaN(n)) return 'unknown'
  if (n >= 8) return 'Strong'
  if (n >= 5) return 'Moderate'
  return 'Weak'
}

export function ScorecardBadge({
  score,
  tier,
  updated,
  scorecardURL,
  size = 'sm',
  hideWhenUnknown = false,
}: ScorecardBadgeProps) {
  const effectiveTier =
    (tier as keyof typeof TIER_PALETTES | undefined) ?? deriveTier(score)
  const palette = TIER_PALETTES[effectiveTier] ?? TIER_PALETTES.unknown
  const Icon = palette.icon

  const isUnknown = effectiveTier === 'unknown'
  // v1.21 QA Bundle 4 Fix #3d: Marketplace tiles pass hideWhenUnknown=true
  // so "Unknown" chips don't clutter every card during the initial refresh
  // window. Detail view keeps the chip to avoid looking like data is missing.
  if (isUnknown && hideWhenUnknown) {
    return null
  }
  const numeric = typeof score === 'number' ? score.toFixed(1) : null
  const label = isUnknown
    ? 'unknown'
    : numeric !== null
      ? `${numeric} · ${effectiveTier}`
      : effectiveTier

  const tooltip = isUnknown
    ? 'OpenSSF Scorecard score not yet available for this chart. Sharko refreshes Scorecard data daily; once the next refresh runs a real score will appear here.'
    : updated
      ? `OpenSSF Scorecard ${numeric ?? ''}/10 (${effectiveTier}). Refreshed daily; last update: ${updated}.`
      : `OpenSSF Scorecard ${numeric ?? ''}/10 · tier ${effectiveTier}. Higher is better. Refreshed daily.`

  const ariaLabel = isUnknown
    ? 'OpenSSF Scorecard: unknown — refreshed daily'
    : `OpenSSF Scorecard ${numeric ?? ''} of 10, tier ${effectiveTier}`

  const inner = (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded-full font-semibold ring-1',
        size === 'sm' ? 'px-2 py-0.5 text-[11px]' : 'px-2.5 py-1 text-xs',
      )}
      style={{
        backgroundColor: palette.bg,
        color: palette.text,
        // Tailwind ring with arbitrary color — fall back to inline style.
        boxShadow: `inset 0 0 0 1px ${palette.ring}`,
      }}
      role="img"
      aria-label={ariaLabel}
      title={tooltip}
    >
      <Icon
        className={size === 'sm' ? 'h-3 w-3' : 'h-3.5 w-3.5'}
        aria-hidden="true"
      />
      {label}
    </span>
  )

  if (scorecardURL) {
    return (
      <a
        href={scorecardURL}
        target="_blank"
        rel="noopener noreferrer"
        className="inline-flex rounded-full focus:outline-none focus-visible:ring-2 focus-visible:ring-[#6aade0]"
        aria-label={`${ariaLabel} — open Scorecard report`}
      >
        {inner}
      </a>
    )
  }
  return inner
}

export default ScorecardBadge

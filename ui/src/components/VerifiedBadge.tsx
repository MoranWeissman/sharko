import { CheckCircle2, ShieldOff } from 'lucide-react'

/**
 * VerifiedBadge — V123-2.4. A small pill that surfaces the cosign-keyless
 * verification outcome of a `CatalogEntry`.
 *
 * - Verified entries render a teal/green-accent "Verified" pill. Hover
 *   tooltip shows the OIDC signing identity (`signature_identity`) when
 *   present, otherwise a generic "cosign keyless signature accepted"
 *   message. Same forgery-resistance rationale as `<SourceBadge>`: the
 *   identity is rendered as text only (never an `<a>`), since the OIDC
 *   subject can be a workflow-ref URL.
 *
 * - Unverified entries render a muted-blue "Unsigned" pill using the
 *   existing V123-1.7 neutral palette so it doesn't read as an error.
 *
 * Scope decision (see story V123-2.4 brief): backend currently surfaces
 * only `verified: boolean`, NOT the failure-reason variants
 * (signature-mismatch / untrusted-identity). Those warning chip variants
 * are punted to V123-2.6.
 *
 * Defensive: a missing `verified` prop (`undefined`) is treated as
 * unsigned. Older API mocks and fixtures may not surface the field yet.
 *
 * Palette:
 *   Verified — light mint bg `#d1f0e6` + deep teal text `#0a4a3a` + mint
 *   ring `#5acca0`. Sole new accent in v1.23; reuse before adding more.
 *   Unsigned — `#eaf4fc` / `#2a5a7a` / `#c0ddf0` (neutral V123-1.7 family).
 *
 * No `gray-*` Tailwind utilities — see `.claude/team/frontend-expert.md`.
 */

type VerifiedBadgeProps = {
  /** The `verified` field on a `CatalogEntry`. Missing = false (defensive). */
  verified?: boolean
  /** OIDC subject when verified — used in tooltip + aria-label. */
  signatureIdentity?: string
  /** Tile size — true on Marketplace cards, false/omitted on the detail page. */
  compact?: boolean
}

export function VerifiedBadge({
  verified,
  signatureIdentity,
  compact,
}: VerifiedBadgeProps) {
  if (verified) {
    const tooltip = signatureIdentity
      ? `Verified — signed by ${signatureIdentity}`
      : 'Verified — cosign keyless signature accepted'
    const ariaLabel = signatureIdentity
      ? `Verified — signed by ${signatureIdentity}`
      : 'Verified'
    return (
      <span
        className={`inline-flex items-center gap-1 rounded-full bg-[#d1f0e6] font-medium text-[#0a4a3a] ring-1 ring-[#5acca0] dark:bg-[#1a4a3a] dark:text-[#d1f0e6] dark:ring-[#5acca0] ${
          compact ? 'px-2 py-0.5 text-[11px]' : 'px-2.5 py-1 text-xs'
        }`}
        title={tooltip}
        aria-label={ariaLabel}
      >
        <CheckCircle2
          className={compact ? 'h-3 w-3' : 'h-3.5 w-3.5'}
          aria-hidden="true"
        />
        Verified
      </span>
    )
  }
  return (
    <span
      className={`inline-flex items-center gap-1 rounded-full bg-[#eaf4fc] font-medium text-[#2a5a7a] ring-1 ring-[#c0ddf0] dark:bg-[#123044] dark:text-[#b4dcf5] dark:ring-[#2a5a7a] ${
        compact ? 'px-2 py-0.5 text-[11px]' : 'px-2.5 py-1 text-xs'
      }`}
      title="Unsigned — no cosign signature attached"
      aria-label="Unsigned (no cosign signature)"
    >
      <ShieldOff
        className={compact ? 'h-3 w-3' : 'h-3.5 w-3.5'}
        aria-hidden="true"
      />
      Unsigned
    </span>
  )
}

export default VerifiedBadge

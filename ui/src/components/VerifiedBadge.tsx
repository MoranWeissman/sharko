import { CheckCircle2, ShieldOff } from 'lucide-react'

/**
 * NOT CURRENTLY RENDERED ANYWHERE (V2-cleanup-68.1).
 *
 * Signing is off by default, so every catalog entry reads "Unsigned" and
 * there's no honest trust distinction to show today — the maintainer
 * approves every catalog entry, so all entries clear the same bar. The
 * badge is hidden at all three call sites (MarketplaceCard,
 * MarketplaceAddonDetail, AddonDetail) pending a future per-addon
 * trust-signal story. The backend signing machinery (`entry.verified`,
 * `entry.signature_identity`) is untouched — this component is kept on
 * disk, with its test, so it's ready to wire back in when that story
 * lands.
 *
 * Small pill surfacing the cosign-keyless verification outcome of a
 * `CatalogEntry`.
 *
 * - Verified entries render a teal/green-accent "Verified" pill. Hover
 *   tooltip shows the OIDC signing identity (`signature_identity`) when
 *   present, otherwise a generic "cosign keyless signature accepted"
 *   message. Identity is rendered as text only (never an `<a>`) — the
 *   OIDC subject can be a workflow-ref URL and we don't want it followed.
 * - Unverified entries render a muted-blue "Unsigned" pill so it doesn't
 *   read as an error.
 *
 * Defensive: a missing `verified` prop (`undefined`) is treated as
 * unsigned. The backend currently surfaces only `verified: boolean`, not
 * failure-reason variants (signature-mismatch / untrusted-identity).
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
          compact ? 'px-2 py-0.5 text-xs' : 'px-2.5 py-1 text-xs'
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
        compact ? 'px-2 py-0.5 text-xs' : 'px-2.5 py-1 text-xs'
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

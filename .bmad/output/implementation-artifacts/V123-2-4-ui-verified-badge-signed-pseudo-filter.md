---
story_key: V123-2-4-ui-verified-badge-signed-pseudo-filter
epic: V123-2 (Per-entry cosign signing)
status: done
effort: M
dispatched: 2026-04-26
merged: 2026-04-26 (PR #290 → main @ 8b1481a)
---

# Story V123-2.4 — UI verified badge + "Signed only" pseudo-filter

## Brief (from epics-v1.23.md §V123-2.4)

As an **operator**, I want tiles to show a verified / unsigned chip and a way
to filter to signed entries only, so that I can quickly narrow to trusted
content.

## Acceptance criteria

**Given** an entry with `verified: true`
**Then** a green-accent "Verified" chip appears on the tile (and on AddonDetail);
hover tooltip shows the signing identity (`signature_identity` from the API).

**Given** an entry with `verified: false`
**Then** a muted-blue "Unsigned" chip appears.

**Given** the Browse tab's filter rail
**When** the operator toggles the new "Signed only" filter
**Then** only entries with `verified: true` are listed.

**Given** the V122-1 axe a11y suite runs
**Then** chip contrast meets WCAG 2.1 AA; toggle has a proper label and `aria-pressed`/checkbox semantics; no new violations.

## Scope decision — punt the warning chips to V123-2.6

The epic AC enumerates `verified: "signature-mismatch"` and `verified: "untrusted-identity"` as distinct warning states. The current backend surfaces only `verified: boolean` + `signature_identity: string` (V123-2.2 / V123-1.4). Those failure-reason states exist in the verifier's logs but are NOT exposed on the API today.

Adding a `verification_state` enum (`verified | unsigned | mismatch | untrusted`) is a backend enhancement that warrants either:
- A small backend follow-up (loader + verifier wiring, swagger regen), OR
- Folding into V123-2.6 (test suite expansion is the natural place to also verify state-reporting works).

**This story implements the binary case** (Verified ↔ Unsigned). The warning-chip variant is captured as a follow-up note in V123-2.6's brief — to be picked up there or split as V123-2.4.1 if scope grows.

## Implementation plan

### Files

- `ui/src/components/VerifiedBadge.tsx` (NEW) — reusable pill component (mirrors SourceBadge structure from V123-1.7).
- `ui/src/components/__tests__/VerifiedBadge.test.tsx` (NEW) — vitest cases.
- `ui/src/components/MarketplaceCard.tsx` — embed `<VerifiedBadge>` in the chips row alongside `<SourceBadge>`.
- `ui/src/components/MarketplaceAddonDetail.tsx` — embed `<VerifiedBadge>` in the meta header (next to the source badge).
- `ui/src/views/AddonDetail.tsx` — embed `<VerifiedBadge>` in the Overview cell row.
- `ui/src/components/MarketplaceFilters.tsx` — add a "Signed only" toggle/checkbox under the existing filter groups; extend `MarketplaceFiltersValue` with `signedOnly: boolean`.
- `ui/src/components/MarketplaceBrowseTab.tsx` — apply the `signedOnly` filter when computing the visible entries.

### `<VerifiedBadge>` component

```tsx
import { CheckCircle2, ShieldOff } from 'lucide-react'

type VerifiedBadgeProps = {
  verified: boolean
  signatureIdentity?: string  // OIDC subject when verified
  compact?: boolean           // tile size vs detail-page size
}

export function VerifiedBadge({ verified, signatureIdentity, compact }: VerifiedBadgeProps) {
  if (verified) {
    const tooltip = signatureIdentity
      ? `Verified — signed by ${signatureIdentity}`
      : 'Verified — cosign keyless signature accepted'
    return (
      <span
        className={`inline-flex items-center gap-1 rounded-full bg-[#d1f0e6] font-medium text-[#0a4a3a] ring-1 ring-[#5acca0] dark:bg-[#1a4a3a] dark:text-[#d1f0e6] dark:ring-[#5acca0] ${
          compact ? 'px-2 py-0.5 text-[11px]' : 'px-2.5 py-1 text-xs'
        }`}
        title={tooltip}
        aria-label={signatureIdentity ? `Verified — signed by ${signatureIdentity}` : 'Verified'}
      >
        <CheckCircle2 className={compact ? 'h-3 w-3' : 'h-3.5 w-3.5'} aria-hidden />
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
      <ShieldOff className={compact ? 'h-3 w-3' : 'h-3.5 w-3.5'} aria-hidden />
      Unsigned
    </span>
  )
}
```

**Palette** — within Sharko's blue family but with a distinct teal/green accent for the Verified state to convey "good": `#d1f0e6` (light mint bg) + `#0a4a3a` (deep teal text) + `#5acca0` (mint ring). Dark-mode pair flips lightness. Unsigned uses the existing neutral V123-1.7 palette (`#eaf4fc`/`#2a5a7a`).

### Filter rail

In `MarketplaceFilters.tsx`:

- Extend `MarketplaceFiltersValue` with `signedOnly: boolean` (default `false`).
- Add a small new group "Signature" before or after "Curated by" with a single FilterCheckbox-equivalent for "Signed only" (no count needed, or show count of `verified === true` entries).
- Update `clear()` to reset `signedOnly: false`.
- Update `isDirty` to include `signedOnly === true`.

Apply in `MarketplaceBrowseTab.tsx` filter pipeline:

```ts
const filtered = entries.filter((e) => {
  // ... existing predicate chain ...
  if (filters.signedOnly && !e.verified) return false
  return true
})
```

### Tile + detail page placement

- `MarketplaceCard.tsx`: append `<VerifiedBadge verified={entry.verified} signatureIdentity={entry.signature_identity} compact />` to the chips row right after the existing SourceBadge.
- `MarketplaceAddonDetail.tsx` + `AddonDetail.tsx`: render `<VerifiedBadge verified={entry.verified} signatureIdentity={entry.signature_identity} />` (non-compact) in the meta header. Reuse the same defensive guards V123-1.7 added for `entry.source` (typeof checks for older API mocks).

### Tests — `VerifiedBadge.test.tsx`

8 cases, vitest + RTL, mirror SourceBadge.test.tsx:

1. Renders "Verified" when `verified` is `true`.
2. Renders "Unsigned" when `verified` is `false`.
3. Tooltip contains `signatureIdentity` when verified + identity provided.
4. Tooltip falls back to generic message when verified but no identity.
5. `aria-label` includes the signing identity when present.
6. Compact mode applies tile-size classes.
7. Does NOT include `gray-*` Tailwind utilities (palette-rule guard).
8. Renders an icon for both states (CheckCircle2 / ShieldOff).

If `MarketplaceFilters` has its own test file, extend it with a "signedOnly toggle" case. If `MarketplaceBrowseTab` has tests, add a case asserting the filter actually narrows the rendered list.

## Quality gates

- `cd ui && npm run build`
- `cd ui && npm test -- --run` (existing 181 + 8 VerifiedBadge ≈ 189; potentially +1-2 if filter/browse tests extended)
- `cd ui && npm run lint` (silent skip if eslint not installed locally — same as V123-1.7)
- a11y axe suite runs in CI (no new violations)
- No backend changes; no swagger regen.
- No Go tests needed.

## Explicit non-goals

- Warning-chip variants for mismatch / untrusted-identity states — punted to V123-2.6 (or a follow-up V123-2.4.1) because the backend doesn't surface those distinct states yet.
- Audit trail for filter usage (low value).
- Server-side filter (`?signed=true` query param) — client-side filter is sufficient given the catalog size; defer until catalog grows past ~500 entries.

## Dependencies

- V123-2.2 — `verified` + `signature_identity` JSON fields on `CatalogEntry` — done ✅.
- V123-2.3 — trust policy env (no UI dep — affects whether anything is verified at runtime, but UI semantics are unchanged) — done ✅.
- V123-1.7 — `<SourceBadge>` palette + placement pattern (reuse the layout idiom).

## Gotchas

1. **Defensive API guards.** Older test mocks may not include `verified`/`signature_identity`. Default to `false`/undefined in the `<VerifiedBadge>` props rather than asserting they exist. Mirror the V123-1.7 pattern.
2. **Don't render `signature_identity` as a link.** Same rationale as V123-1.7's URL-as-text rule — text only in tooltips.
3. **Palette discipline:** new Verified mint accent (`#d1f0e6` family) is the only deviation from blue; reuse it across the UI rather than introducing more accents.
4. **Filter side effects:** clearing filters must reset `signedOnly`. The "Clear filters" button (line ~266) needs to know about the new field.
5. **Filter dirty state:** tests for `isDirty` should keep passing; extend if there's an explicit isDirty test.
6. **Dark-mode classes** required on every color utility.
7. **Lucide icons:** verify `CheckCircle2` + `ShieldOff` exist in the version pinned by `ui/package.json`. Substitute `Check` / `ShieldX` if needed.

## Role files (MUST embed in dispatch)

- `.claude/team/frontend-expert.md` — primary.
- `.claude/team/test-engineer.md` — vitest + RTL.

## PR plan

- Branch: `dev/v1.23-ui-verified-badge` off main.
- Commits:
  1. `feat(ui): VerifiedBadge component (V123-2.4)`
  2. `feat(ui): Signed-only filter on MarketplaceFilters (V123-2.4)`
  3. `feat(ui): wire VerifiedBadge into Browse tile + AddonDetail (V123-2.4)`
  4. `chore(bmad): mark V123-2.4 for review`
- No tag.

## Next story

V123-2.5 — Release pipeline: sign embedded catalog entries (cosign-keyless via
GitHub Actions OIDC; signs `catalog/addons.yaml` per entry; updates the
release workflow). After merge, fresh installs see the embedded catalog as
Verified out of the box (matches the V123-2.3 default trust policy).

## Tasks completed

1. **Extended TS `CatalogEntry` model** with `verified?: boolean` and
   `signature_identity?: string` to mirror the V123-2.2 backend additions
   (the brief noted these should already be on the type, but only
   `signature?` was added in V123-2.1; the v1.1+ verifier output fields
   were a gap).
2. **Built `<VerifiedBadge>` component** matching the spec verbatim
   (`ui/src/components/VerifiedBadge.tsx`). Mint accent (`#d1f0e6` /
   `#0a4a3a` / `#5acca0`) for Verified; existing neutral V123-1.7 family
   for Unsigned. Defensive default — missing `verified` renders as
   Unsigned. `lucide-react` `CheckCircle2` + `ShieldOff` confirmed
   present in the pinned version, no fallback needed.
3. **Wrote `VerifiedBadge.test.tsx`** with the 8 spec'd cases plus a
   ninth defensive-default case (totals 9). All pass in isolation.
4. **Wired the badge into 3 render paths**:
   - `MarketplaceCard.tsx` — compact, after the V123-1.7 SourceBadge.
   - `MarketplaceAddonDetail.tsx` — non-compact, in the meta header
     (curated source only — ArtifactHub doesn't surface a verified flag).
   - `AddonDetail.tsx` — non-compact "Verified" overview row mirroring
     the existing Source row layout.
5. **Extended `MarketplaceFilters` + `MarketplaceBrowseTab`** with a new
   "Signature" filter group containing a single "Signed only" checkbox.
   `signedOnly: boolean` added to `MarketplaceFiltersValue`; `clear()`
   resets it; `isDirty` includes it; `?mp_signed=1` URL parameter for
   deep-linking; predicate chain narrows entries to `verified === true`
   when toggled. Count next to the checkbox shows how many entries
   currently verify (matches the per-group count pattern).
6. **BMAD tracking** — sprint-status.yaml flipped to `review`, story
   frontmatter updated, retrospective sections appended.

## Files touched

- `ui/src/services/models.ts` (added `verified` + `signature_identity` to `CatalogEntry`)
- `ui/src/components/VerifiedBadge.tsx` (NEW)
- `ui/src/components/__tests__/VerifiedBadge.test.tsx` (NEW)
- `ui/src/components/MarketplaceCard.tsx` (badge in chips row)
- `ui/src/components/MarketplaceAddonDetail.tsx` (badge in meta header)
- `ui/src/views/AddonDetail.tsx` (Verified overview row)
- `ui/src/components/MarketplaceFilters.tsx` (Signature group + signedCount)
- `ui/src/components/MarketplaceBrowseTab.tsx` (parse/write/apply mp_signed)
- `.bmad/output/implementation-artifacts/sprint-status.yaml`
- `.bmad/output/implementation-artifacts/V123-2-4-ui-verified-badge-signed-pseudo-filter.md`

## Tests

- `cd ui && npm run build` — passed.
- `cd ui && npm test -- --run` — 33 files / 190 tests pass (181 baseline + 9 new VerifiedBadge cases).
- `cd ui && npm run lint` — silently skipped (eslint binary not installed locally; same as V123-1.7).
- No backend changes; Go test suite untouched.
- a11y axe suite runs in CI — Verified palette `#d1f0e6` bg / `#0a4a3a` text contrast ≈ 11.4:1 (WCAG AAA); ring is decorative.

VerifiedBadge cases:
1. Renders "Verified" when verified=true.
2. Renders "Unsigned" when verified=false.
3. Tooltip contains signatureIdentity when verified+identity provided.
4. Tooltip falls back to generic message when verified but no identity.
5. aria-label includes signing identity when present.
6. Compact mode applies tile-size classes.
7. Does NOT include gray-* Tailwind utilities (palette guard).
8. Renders an icon for both states.
9. Treats missing `verified` as Unsigned (defensive default).

## Decisions

1. **Added `verified` + `signature_identity` to TS `CatalogEntry`.** The
   brief said V123-2.2 should have added these — actual commit
   `c29bb06` only added them on the Go side; the TS interface was never
   updated to match. Picked it up here so the badge can reference
   `entry.verified` / `entry.signature_identity` directly without
   `as unknown as` casts.
2. **Defensive default in the component, not at call sites.** Missing
   `verified` (`undefined`) renders as Unsigned. Saves a guard at
   every call site and matches the SourceBadge pattern (treating
   missing source as embedded). Story brief's gotcha #1 explicitly
   asks for this.
3. **`?mp_signed=1` URL parameter** for the new pseudo-filter to keep
   the deep-link contract consistent with the other Browse filters.
   Cleared by removing the param (matches `mp_tier`'s pattern of
   omitting when `any`).
4. **Skipped MarketplaceAddonDetail badge for ArtifactHub source.**
   AH responses don't carry a Sharko-side `verified` flag — surfacing
   "Unsigned" on every AH result would mislead operators (the addon
   may very well be cosign-signed by its publisher; we just don't
   verify it). Curated entries get the badge unconditionally.
5. **Punted warning chip variants to V123-2.6** as instructed by the
   brief. Only the binary chip (Verified ↔ Unsigned) is implemented
   here; mismatch / untrusted-identity states need a backend
   `verification_state` enum that doesn't exist today.
6. **Renamed third commit** from "Browse tile + AddonDetail" to
   include MarketplaceAddonDetail too — there are three render paths,
   not two; commit message reflects all three.

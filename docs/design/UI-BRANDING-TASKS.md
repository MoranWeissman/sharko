# UI Branding & Polish — Tasks for Implementation Session

> Guidance from the architect session. Follow these instructions precisely.
> Mascot image is already at: `assets/logo/sharko-mascot-login.png`

---

## Task 1 — Login Page Redesign

**Current state:** Login page uses an old AAP-era AI-generated background with "AAP / ArgoCD Addons Platform" text and blue cube icons on a space-themed background.

**Target:** ArgoCD-style login page with the Sharko mascot.

**Layout (reference: ArgoCD's login page):**
- Left/center (75% width): dark navy background with the Sharko mascot image
- Right (25% width): login panel with form

**Left side:**
- Background image: `assets/logo/sharko-mascot-login.png`
- The image has the tagline "Let's get your addons deployed!" baked in — no need to add text overlay
- Image should cover the left portion, vertically centered, maintain aspect ratio
- Background color behind/around image: dark navy (#0B1426) to blend with the image edges

**Right side login panel:**
- Top: shark fin icon (`assets/logo/sharko-icon.png`) + "sharko" text
- The "sharko" text: use the UI's existing font (Inter or whatever is configured), font-weight 600, color cyan-500 (#06b6d4 or the closest Tailwind cyan), ~28px
- Do NOT use an image for the wordmark — render "sharko" as actual text in CSS
- Below: Username input field
- Below: Password input field  
- Below: "Sign In" button in cyan
- Bottom: "Sharko v1.0.0" in small muted text

**Important:** The tagline text in the mascot image says "addons" (no hyphen). Keep consistent — use "addons" not "add-ons" throughout the UI.

---

## Task 2 — Sidebar Header

**Current state:** Sidebar probably shows "Sharko" in text from the v0.1.0 rebrand.

**Target:** Shark fin icon + "Sharko" text as the sidebar header.

- Use `assets/logo/sharko-icon-32.png` (or the SVG if available) as the icon
- "Sharko" text next to the icon in cyan, same font as the rest of the UI
- The icon + text should be a link to the dashboard (home page)

---

## Task 3 — Favicon

**Current state:** May still be the old AAP favicon or a generic one.

**Target:** Use the shark fin icon as favicon.

- Copy `assets/logo/sharko-icon-32.png` to `ui/public/favicon.png` (or convert to .ico)
- Update `ui/index.html` to reference the new favicon
- Also update the page title to "Sharko" if not already done

---

## Task 4 — Color Consistency Check

**Current state:** The UI already uses Tailwind cyan extensively (cyan-500, cyan-600, etc.) which is close to the Sharko brand cyan (#00C9E0).

**Decision: NO major color scheme change needed.** The existing cyan accent is already aligned with the brand. Don't change the Tailwind theme or shadcn/ui CSS variables. The current look is fine.

**Do verify:** 
- All buttons that were previously blue/indigo are now cyan (some may have been missed in the rebrand)
- The dark mode colors work well with the cyan accent
- No old blue/purple accent colors lingering from AAP

---

## Task 5 — Remove Old Login Background

- Delete the old AAP login background image from wherever it's stored (likely `ui/public/` or `ui/src/assets/`)
- Make sure no references to it remain in Login.tsx or CSS

---

## Notes from the Architect

### What NOT to change:
- Don't redesign the dashboard, version matrix, observability, or any existing read-only views — they're fine
- Don't change the shadcn/ui component library or Tailwind theme — it works
- Don't change the navigation structure (sidebar items, routing) — that's a separate discussion
- Don't touch the 1,200 lines of Phase 7 write capability code — it's reviewed and merged

### Brand Assets Reference

| Asset | Path | Usage |
|-------|------|-------|
| Shark fin icon (all sizes) | `assets/logo/sharko-icon-*.png` | Favicon, sidebar header, login panel |
| Shark fin banner (with wave) | `assets/logo/sharko-banner.png` | README hero |
| Mascot (thumbs up shark) | `assets/logo/sharko-mascot-login.png` | Login page background |

### Brand Colors
- **Primary cyan:** #00C9E0 (Tailwind cyan-500 #06b6d4 is close enough — use Tailwind's)
- **Deep blue:** #1E62D0 (accent, used in logo only, not needed in UI)
- **Dark navy:** #0B1426 (login background, matches mascot image)
- **All other colors:** keep the existing Tailwind/shadcn theme as-is

### Warbrick's Tips
- "1,200 lines of UI code added by agents and nobody looked at a single component. Maybe glance at the forms before shipping?"
- "The mascot has more personality than the entire settings page. Priorities."
- "If the favicon is still a blue cube, that's embarrassing for a v1.0.0 release."

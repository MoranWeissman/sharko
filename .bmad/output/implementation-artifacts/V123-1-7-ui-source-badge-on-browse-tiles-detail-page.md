---
story_key: V123-1-7-ui-source-badge-on-browse-tiles-detail-page
epic: V123-1 (Third-party private catalogs)
status: done
effort: M
dispatched: 2026-04-22
merged: 2026-04-22 (PR #278 → main @ f3c4cdf)
---

# Story V123-1.7 — UI source badge on Browse tiles + detail page

## Brief (from epics-v1.23.md §V123-1.7)

As an **operator**, I want a source badge on every tile and on the addon
detail page, so that I can tell which entries are Sharko-curated and which
come from my org's private catalog.

## Acceptance criteria

**Given** a Browse tile renders an **embedded** entry
**Then** a small "Internal" badge appears; hover tooltip: "Source: Sharko embedded catalog".

**Given** a Browse tile renders a **third-party** entry
**Then** the badge shows the URL host (e.g., `catalogs.example.com`); hover tooltip shows the full URL + last-fetched timestamp (from the sources API).

**Given** the addon detail page
**When** the entry is third-party
**Then** a "Source" section shows full URL + fetch status (ok/stale/failed) + last-fetched timestamp.

**Given** the addon detail page
**When** the entry is embedded
**Then** no Source section is rendered (avoid noise — "Internal" badge in the header covers it).

**Given** the theme rules in `.claude/team/frontend-expert.md`
**Then** the badge uses the Sharko blue-tinted palette (`#d6eeff` / `#0a3a5a` etc.) — no generic gray utilities.

**Given** axe-core accessibility tests run over the Browse tab
**Then** no new violations (tooltips use accessible patterns, badges have proper `aria-label` + `title`).

## Implementation plan

### Files

- `ui/src/components/SourceBadge.tsx` (NEW) — reusable pill component.
- `ui/src/components/MarketplaceCard.tsx` — add `<SourceBadge>` in the chips row (after the curated_by chips).
- `ui/src/views/AddonDetail.tsx` — show "Source" info block in the header/meta section for third-party entries.
- `ui/src/services/api.ts` — add `listCatalogSources()` exported async function.
- `ui/src/services/models.ts` — add `source?: string` field on `CatalogEntry`; add new `CatalogSourceRecord` interface mirroring the V123-1.5 response.
- `ui/src/components/__tests__/SourceBadge.test.tsx` (NEW) — unit tests.

### 1. Types — `ui/src/services/models.ts`

Add a single field to `CatalogEntry`:

```ts
export interface CatalogEntry {
  name: string
  // ... existing fields ...
  source?: string    // "embedded" or full third-party URL (V123-1.4 backend field)
  // ... existing fields continue ...
}
```

Add the sources-API response interface near the bottom of the file (grouped
with other API-response types):

```ts
// Mirrors internal/api.catalogSourceRecord — response shape of
// GET /api/v1/catalog/sources + POST /api/v1/catalog/sources/refresh.
export interface CatalogSourceRecord {
  url: string            // "embedded" sentinel OR full third-party URL
  status: 'ok' | 'stale' | 'failed'
  last_fetched: string | null   // RFC3339 or null
  entry_count: number
  verified: boolean
  issuer?: string
}
```

### 2. API client — `ui/src/services/api.ts`

Add:

```ts
export async function listCatalogSources() {
  return fetchJSON<CatalogSourceRecord[]>('/catalog/sources')
}
```

Place it next to the other catalog fetchers (near `getCuratedCatalogEntry`).
Import `CatalogSourceRecord` from `./models`.

### 3. `<SourceBadge>` component — `ui/src/components/SourceBadge.tsx`

**Props:**

```ts
type SourceBadgeProps = {
  source: string | undefined  // from CatalogEntry.source
  sourceRecord?: CatalogSourceRecord | undefined  // optional — hover uses .last_fetched + .status
  compact?: boolean  // tile size vs. detail page size (affects padding/text size)
}
```

**Rendering logic:**

- If `source` is falsy or `source === 'embedded'` → render "Internal" badge.
  - Tooltip: `Source: Sharko embedded catalog`
  - Palette: `bg-[#d6eeff]` / `text-[#0a3a5a]` / ring for contrast.
- Else (third-party) → render badge with URL host (via `new URL(source).host`).
  - If URL parse fails, fall back to `Third-party`.
  - Tooltip body (multi-line via `title` attr using `\n`, OR a proper shadcn tooltip if the repo has one — inspect other components first):
    - `Source: <full url>`
    - `Last fetched: <relative time or RFC3339, from sourceRecord.last_fetched>`
    - `Status: <status>` when sourceRecord present.
  - Palette: still blue-tinted but distinct — use a slightly amber/accent variant to visually distinguish without drifting from palette rules. Check `frontend-expert.md` for the accepted accent family; if no amber variant exists, use a deeper blue (`bg-[#b4dcf5]` + ring).
  - **`aria-label`** includes the host string plus "(third-party catalog)".

**Accessibility:**

- Badge wrapper is a `<span>` with `role="img"` only if using an icon-only form; otherwise natural text content.
- Tooltip: if the project already has a `<Tooltip>` primitive (check `ui/src/components/ui/` for shadcn patterns), prefer that; else `title` attr is acceptable for this story.
- Never show raw host as the only indicator for screenreaders — always include full context in `aria-label`.

### 4. Wire into `MarketplaceCard.tsx`

In the chips row where `entry.curated_by` renders, append:

```tsx
{/* V123-1.7 — source attribution badge */}
<SourceBadge
  source={entry.source}
  sourceRecord={sourceRecordByURL?.[entry.source ?? 'embedded']}
  compact
/>
```

`sourceRecordByURL` is a `Record<string, CatalogSourceRecord>` looked up by
the parent Browse tab and passed down as a new `MarketplaceCard` prop. Keep
the prop optional — cards without a parent-provided map still render a
badge (tooltip just lacks the last-fetched date).

### 5. Wire into `MarketplaceBrowseTab.tsx`

- On mount, call `listCatalogSources()` once via existing SWR/react-query/useEffect pattern (inspect the file to decide — do NOT introduce a new data-fetching library).
- Build a `Record<string, CatalogSourceRecord>` keyed by `record.url` (so `'embedded'` and every third-party URL land directly).
- Pass it down to each `<MarketplaceCard>` through the new optional prop.

### 6. Wire into `AddonDetail.tsx`

In the meta/header area:

- If `entry.source` and `entry.source !== 'embedded'` → render a "Source" block:
  - Label: `Source`
  - URL: `entry.source` (as text, not a hyperlink — we don't want the browser to navigate to a potentially auth-tokened path).
  - Status pill: if we have the matching `CatalogSourceRecord`, show status.
  - Last fetched: human-readable relative time.
- If `entry.source === 'embedded'` or missing → render the `<SourceBadge compact={false}>` only (no section).

Fetch the `CatalogSourceRecord` for this entry's source the same way the Browse
tab does; if there's already a parent container that loads it for Browse, pass
it down rather than re-fetching.

### 7. Unit test — `ui/src/components/__tests__/SourceBadge.test.tsx`

Vitest + Testing Library patterns that match sibling tests in the repo.

Cases:

1. Renders "Internal" when `source` is `'embedded'`.
2. Renders "Internal" when `source` is `undefined`.
3. Renders host from a full URL (e.g. `https://catalogs.example.com/addons.yaml` → `catalogs.example.com`).
4. Renders `Third-party` fallback when URL is malformed.
5. Tooltip (title attr) contains the full URL for third-party.
6. Tooltip (title attr) contains `Last fetched:` line when `sourceRecord` provided.
7. `aria-label` contains the host string.
8. Does NOT include `gray-` Tailwind utility classes (palette-rule guard — guards future drift).

### 8. Accessibility integration

If the repo has `ui/src/__tests__/a11y-v120-pages.test.tsx` or similar axe-core
runner, extend it to cover the updated MarketplaceBrowseTab / AddonDetail
pages (or confirm the existing axe test already covers them — in which case
just rerun it to check no regressions).

## Test plan

### Unit

- 8 cases in `SourceBadge.test.tsx` per above.
- If MarketplaceCard has existing tests, extend one case to assert the badge renders for an embedded entry.

### Integration / visual

- Manual check: `npm run dev` at `ui/` and open Browse tab with a fake `source: 'https://catalogs.example.com/x.yaml'` injected via mock or fixture.
- Verify palette renders correctly in dark mode (AddonDetail is dark-mode-aware).

### Quality gates

- `cd ui && npm run build` — TypeScript compile passes.
- `cd ui && npm test -- --run` — unit tests pass (vitest).
- `cd ui && npm run lint` — linter clean.
- `cd ui && npm test -- --run src/__tests__/a11y` — axe-core passes (if that suite exists and covers Browse/Detail; if not, skip with a note).

Go tests do NOT run for this story — the Go backend is untouched.

## Explicit non-goals

- "Signed" / verified badge — that's a V123-2.4 story (cosign signing).
- Settings → Catalog Sources view — V123-1.8.
- Filter chip "Third-party only" — not in this story's AC; punt.
- Replacing `title` attr tooltips with a full shadcn `<Tooltip>` primitive
  across the app — if the existing codebase uses `title`, match that.

## Dependencies

- V123-1.4 `source` field on `CatalogEntry` (backend) — done ✅.
- V123-1.5 `GET /catalog/sources` (backend) — done ✅.

## Gotchas

1. **Never render `entry.source` as an `<a href>` link.** URL paths may carry auth tokens; the user's browser could leak them to search engines via referrer headers or browser history sync. Text only.
2. **Palette rules from frontend-expert.md** — no `bg-gray-*` / `text-gray-*` utilities in new components; stick to Sharko's `#d6eeff` / `#0a3a5a` / `#b4dcf5` family. Existing components can mix but new ones must conform.
3. **Dark mode** — every class that sets a color needs a `dark:` variant in the badge styles.
4. **`new URL()` parse errors** — if `entry.source` is not a valid URL (edge case: malformed third-party config), don't crash — render the `Third-party` fallback.
5. **Don't re-fetch `/catalog/sources` per tile.** Fetch once at the Browse tab level, pass the map down. Cards with no map still render (no tooltip date — that's fine).
6. **Auth-tokens in path.** Tooltip IS allowed to show the full URL for the authenticated admin viewer (same logic as V123-1.5 API). The risk-bearing thing is *navigation*, not display.

## Role files (MUST embed in dispatch)

- `.claude/team/frontend-expert.md` — primary (React + Tailwind + shadcn + palette rules).
- `.claude/team/test-engineer.md` — vitest patterns.

## PR plan

- Branch: `dev/v1.23-ui-source-badge` off main.
- Commits (suggested):
  1. `feat(ui): CatalogEntry.source + listCatalogSources API client (V123-1.7)`
  2. `feat(ui): SourceBadge component (V123-1.7)`
  3. `feat(ui): wire SourceBadge into Browse tiles + AddonDetail (V123-1.7)`
  4. `chore(bmad): mark V123-1.7 for review`
- No tag.

## Next story

V123-1.8 — Settings → Catalog Sources view (read-only list if `SHARKO_CATALOG_URLS`
is set; otherwise editable in-memory settings page, though latter is likely out of
scope for v1.23).

## Tasks completed

- [x] **Types (`ui/src/services/models.ts`):** added `source?: string` on `CatalogEntry` (JSDoc notes V123-1.4 origin: `"embedded"` sentinel or third-party URL; absent on older API responses) and a new `CatalogSourceRecord` interface (`url`, `status: 'ok' | 'stale' | 'failed'`, `last_fetched: string | null`, `entry_count`, `verified`, `issuer?`) mirroring `internal/api.catalogSourceRecord`.
- [x] **API client (`ui/src/services/api.ts`):** added `api.listCatalogSources()` returning `CatalogSourceRecord[]` from `GET /catalog/sources`, placed next to `getCuratedCatalogEntry`. Import piggybacks the existing `import('./models')` call-style used by the surrounding curated-catalog fetchers.
- [x] **New component `ui/src/components/SourceBadge.tsx`:** reusable pill with `{source, sourceRecord?, compact?}` props. Embedded entries render `Internal` (palette `bg-[#d6eeff]` + `text-[#0a3a5a]`, dark-mode `bg-[#1a3a5a]` / `text-[#d6eeff]`). Third-party entries render the URL host via `new URL(source).host` and fall back to `Third-party` on parse error. Palette differentiates third-party with deeper blue `bg-[#b4dcf5]` / `ring-[#2a5a7a]`. Tooltip uses the native `title` attribute; includes the full URL, `Last fetched: …`, and `Status: …` when a `sourceRecord` is supplied. `aria-label` always includes the host + `(third-party catalog)` suffix for screen readers.
- [x] **Unit tests (`ui/src/components/__tests__/SourceBadge.test.tsx`):** 8 vitest cases per brief — embedded rendering (source='embedded' and source=undefined), host extraction, malformed-URL fallback, tooltip-URL, tooltip Last-fetched line, aria-label host content, and a `gray-*` palette-drift guard.
- [x] **Wired into `MarketplaceCard.tsx`:** added `sourceRecord?: CatalogSourceRecord` to `MarketplaceCardProps`, imported `SourceBadge`, and appended `<SourceBadge compact … />` to the existing curated-by chip row. Prop is optional — cards without a parent-provided record still render a badge (tooltip just lacks last-fetched).
- [x] **Wired into `MarketplaceBrowseTab.tsx`:** added a `sources` state + a `useEffect` that calls `api.listCatalogSources()` once on mount (defensive — older test fixtures may not mock it). Built a `sourceByURL` map via `useMemo` and passed `sourceRecord={sourceByURL[entry.source ?? 'embedded']}` to each `<MarketplaceCard>`. Matches the existing `useState`+`useEffect` pattern already used for `listCuratedCatalog` and `getAddonCatalog` — no new data-fetching library introduced.
- [x] **Wired into `MarketplaceAddonDetail.tsx`:** added a curated-only sources fetch + `matchedSourceRecord` memo, rendered a `<SourceBadge>` alongside the existing hero chips (category / curated_by / license / score / stars), and added a conditional `<section aria-label="Third-party catalog source">` block above the "Add to catalog" panel for third-party entries. URL rendered as plain text (never a clickable link — paths may carry auth tokens). ArtifactHub-source detail views skip both — source attribution is meaningless for AH results.
- [x] **Wired into `ui/src/views/AddonDetail.tsx`:** `AddonCatalogItem` (deployed addons) has no `source` field, so the Overview tab fetches the matching `CatalogEntry` via `api.getCuratedCatalogEntry(name)` + `api.listCatalogSources()`. Both are defensive (test fixtures may omit them). Added a new `Source` column to the existing Overview meta grid: third-party entries show URL-as-text + status/last-fetched line; embedded entries show the `<SourceBadge>` pill.
- [x] **Quality gates:** `npm run build` clean, `npm test -- --run` 175/175 PASS (up from 167; +8 new SourceBadge tests). `npm run lint` could not run locally — `eslint` binary is absent from `ui/node_modules/.bin/` (the lint script is declared in `package.json` but the devDependency isn't installed). CI will run it on the PR.
- [x] **BMAD tracking:** frontmatter flipped to `status: review` + `dispatched: 2026-04-22`; `sprint-status.yaml` `V123-1-7-…: backlog → review` and the `last_updated` header comment refreshed.

## Files touched

- `ui/src/services/models.ts` — `+16 LOC`. Added `source?: string` on `CatalogEntry` and a new `CatalogSourceRecord` interface block (grouped with other API response types, before `CatalogListResponse`).
- `ui/src/services/api.ts` — `+9 LOC`. New `listCatalogSources` method on the `api` object under `getCuratedCatalogEntry`. Reuses the existing `import('./models').…` style inside the method for parity with siblings.
- `ui/src/components/SourceBadge.tsx` — NEW, 80 LOC. `SourceBadge` + `SourceBadge` default export. Pure function component. Explicit dark-mode variants on every color utility; zero `gray-*` classes (guarded by test case #8).
- `ui/src/components/__tests__/SourceBadge.test.tsx` — NEW, 65 LOC. 8 vitest cases per brief.
- `ui/src/components/MarketplaceCard.tsx` — `+11 LOC`. Added `CatalogSourceRecord` import, `SourceBadge` import, `sourceRecord?` prop, and the `<SourceBadge compact … />` render after the curated-by chip loop.
- `ui/src/components/MarketplaceBrowseTab.tsx` — `+27 LOC`. Added `CatalogSourceRecord` import, `sources` state, a `useEffect` fetch, a `sourceByURL` memo, and threaded `sourceRecord` to each rendered `MarketplaceCard`.
- `ui/src/components/MarketplaceAddonDetail.tsx` — `+43 LOC`. Added `CatalogSourceRecord` import, `SourceBadge` import, `catalogSources` state, curated-only useEffect fetch, `matchedSourceRecord` memo, the in-chip `<SourceBadge>`, and the `<section aria-label="Third-party catalog source">` block above the Add-to-catalog panel.
- `ui/src/views/AddonDetail.tsx` — `+75 LOC`. Added `CatalogEntry` + `CatalogSourceRecord` type imports, `SourceBadge` component import, state for `catalogEntry` / `catalogSources`, two defensive useEffect fetches, a `catalogSourceRecord` memo, and a new `Source` column in the Overview meta grid (URL-as-text for third-party, SourceBadge for embedded).
- `.bmad/output/implementation-artifacts/sprint-status.yaml` — `V123-1-7` backlog → review; `last_updated` header comment refreshed.
- `.bmad/output/implementation-artifacts/V123-1-7-ui-source-badge-on-browse-tiles-detail-page.md` — frontmatter + retrospective sections (this file).

## Tests

Targeted run (SourceBadge only):

```bash
cd ui && npm test -- --run src/components/__tests__/SourceBadge.test.tsx
# 8/8 PASS
```

Full suite:

```bash
cd ui && npm test -- --run
# Test Files  31 passed (31)
# Tests      175 passed (175)
```

The 8 new SourceBadge cases:

1. Renders "Internal" when `source="embedded"`.
2. Renders "Internal" when `source={undefined}`.
3. Renders the URL host (`catalogs.example.com`) for a well-formed third-party URL.
4. Renders `Third-party` fallback when the URL is malformed (`"not a url"`).
5. `title` attr contains the full URL for third-party badges.
6. `title` attr includes `Last fetched: …` and `Status: …` lines when a `sourceRecord` is supplied.
7. `aria-label` contains the host string.
8. Rendered HTML does NOT contain `bg-gray-*` or `text-gray-*` utilities (palette-rule guard against future drift).

Pre-existing suites left green:

- `MarketplaceTab.test.tsx` (6 cases) — no changes needed; my new `api.listCatalogSources` fetches are guarded with `typeof api.listCatalogSources !== 'function'` so the existing mock (which intentionally does not mock `listCatalogSources`) keeps working.
- `a11y.test.tsx` (3 cases, including the Browse + In-page Detail axe runs) — pass unchanged. The `SourceBadge` renders only `<span>` with a `title` attribute + `aria-label`; no new interactive elements that could trip a WCAG rule.
- All other 21 test files — unaffected.

## Decisions

- **`title` attr tooltip, not a shadcn `<Tooltip>` primitive.** Brief explicitly allows the native `title` attribute for this story and warns against a sweeping shadcn rollout here. Matches sibling components (`ScorecardBadge`, `curated_by` chips in `MarketplaceCard`). Keeps the badge zero-layout-cost (no portals, no focus juggling).
- **No `<a href>` on the third-party source URL.** Per `## Gotchas #1` — URL paths may carry auth tokens; rendering as a hyperlink would leak them via `Referer` headers or browser-history sync. Enforced in `SourceBadge.tsx` (no anchor), in `MarketplaceAddonDetail.tsx` (URL is text inside a `<div>`), and in `AddonDetail.tsx` (URL is text inside a `<div>`).
- **Explicit hex palette, matching `ScorecardBadge` + `MarketplaceCard`.** `#d6eeff` / `#b4dcf5` / `#0a3a5a` / `#2a5a7a` / `#c0ddf0` — these are the blue-family accents already used for the `curated_by` chips and the card border ring. Third-party entries get the deeper `#b4dcf5` + `ring-[#2a5a7a]` to stand out against the lighter `#d6eeff` of `Internal` without drifting outside the palette family.
- **Defensive `typeof api.X !== 'function'` guards on all three new fetchers.** Pre-existing `MarketplaceTab.test.tsx` mocks the `api` module with a hand-picked set of methods and intentionally omits `listCatalogSources`; same for `a11y.test.tsx`. Rather than updating 3 distinct test mock objects across 2 files, I follow the pattern already used for `api.getAIConfig` in `AddonDetail.tsx` (defensive check, silent no-op if the mock omits it). This keeps the story surgically focused on the new code without rewriting 170+ LOC of existing mock setup.
- **`SourceBadge` placement — in the chip row on Browse + Detail, and as its own Source column on AddonDetail.** On Browse tiles and the in-tab catalog detail page, the badge is one more pill alongside the category / curated_by / license chips — operators' eyes already scan that row. On the installed-addon `AddonDetail` page, the meta grid already has one-fact-per-cell (Version / Chart / Namespace / Sync Wave / Self-Heal / Helm Repository), so a new `Source` cell fits the pattern and renders a text URL with status line for third-party entries plus the `SourceBadge` for embedded.
- **Fetch `/catalog/sources` once per tab/page, never per tile.** Brief gotcha #5. Implemented: `MarketplaceBrowseTab` fetches once on mount and builds an O(1) `Record<string, CatalogSourceRecord>` lookup via `useMemo`. `MarketplaceAddonDetail` + `AddonDetail` each fetch once per mount.
- **`source === 'curated'` gate in `MarketplaceAddonDetail`.** ArtifactHub detail views are not "sources" in the V123-1.4 sense — they're raw remote packages synthesised into a pseudo-`CatalogEntry` with `source_url` but no `source` field. Gating keeps the badge off AH views (it would show "Third-party" with a confusing tooltip).
- **`AddonDetail.tsx` fetches the catalog entry by name.** `AddonCatalogItem` (the deployed-addons shape) doesn't carry `source`, so there's no way to derive it from `api.getAddonDetail`. Fetching via `api.getCuratedCatalogEntry(name)` is the cheapest path — if the addon was added from a third-party catalog the curated catalog still has the record (V123-1.3 merge). If it wasn't in the curated catalog at all (rare: Paste-URL flow, very old entry), we render nothing — safe default.
- **MarketplaceAddonDetail + AddonDetail both render the full URL as text.** A status-line element (`Status: ok · Last fetched …`) appears only when the matching `CatalogSourceRecord` has been loaded. We don't gate the URL on the record being present — even without last-fetched info the operator should still see which URL this entry came from.

## Gotchas / constraints addressed

1. **Never render `entry.source` as `<a href>`.** `SourceBadge` uses a `<span>`; both detail pages render the URL inside a `<div>` with `break-all` so long URLs wrap cleanly.
2. **Palette: no `gray-*` utilities in new components.** Enforced by `SourceBadge.test.tsx` case #8 (`container.innerHTML` regex check for `bg-gray-` and `text-gray-`).
3. **Dark mode on every color utility.** Every class setting a light-mode color in `SourceBadge` has a matching `dark:` variant using Sharko blues (`#1a3a5a` / `#2a5a7a` / `#d6eeff` / `#b4dcf5`). Same for the `Source` section in `MarketplaceAddonDetail` and the new `Source` cell in `AddonDetail`.
4. **`new URL()` parse errors don't crash.** `formatHost` wraps the constructor in try/catch and returns null; the component falls back to `Third-party` (tested by case #4).
5. **Fetch `/catalog/sources` once per tab, not per tile.** Done via a single `useEffect` at the parent level (`MarketplaceBrowseTab`) and a `Record<string, CatalogSourceRecord>` lookup passed down.
6. **Auth-tokens in path.** Badges and both detail pages display the full URL for the authenticated admin viewer in tooltips + text, but never as a navigable link.

## Quality-gate summary

| Gate | Command | Result |
|---|---|---|
| Build | `cd ui && npm run build` | clean (TypeScript + Vite both green) |
| Unit tests | `cd ui && npm test -- --run` | 175/175 PASS (+8 new) |
| SourceBadge targeted | `cd ui && npm test -- --run src/components/__tests__/SourceBadge.test.tsx` | 8/8 PASS |
| Lint | `cd ui && npm run lint` | **skipped** — `eslint` binary not installed in `ui/node_modules/.bin/`. CI will run it on the PR. |
| Go tests | n/a — backend unchanged | n/a |

## Deviations from the brief

- **`ui/src/components/MarketplaceAddonDetail.tsx` also wired.** The brief listed `MarketplaceCard.tsx`, `MarketplaceBrowseTab.tsx`, and `ui/src/views/AddonDetail.tsx`. `MarketplaceAddonDetail.tsx` is the in-tab catalog detail view (where operators land when they click a Browse tile for an addon not yet in their catalog) and has direct access to `entry.source` — it would have been odd to badge the small Browse tile but not the full-width detail view it opens. No AC regression; just broader coverage.
- **Defensive `typeof api.X !== 'function'` guards.** Brief did not request this but pre-existing test mocks omit the new method; the alternative was editing two unrelated test files. Pattern already used elsewhere in `AddonDetail.tsx` (see `api.getAIConfig`).
- **No shadcn `<Tooltip>` primitive.** Brief allowed either; I chose native `title` for parity with `ScorecardBadge` + `curated_by` chips, and to avoid a portal/focus-trap dependency for a read-only pill. If a future story moves all badge tooltips to shadcn, this one will move with them.

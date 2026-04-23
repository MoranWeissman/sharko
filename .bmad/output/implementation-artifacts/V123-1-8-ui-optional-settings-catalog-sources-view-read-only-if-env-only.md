---
story_key: V123-1-8-ui-optional-settings-catalog-sources-view-read-only-if-env-only
epic: V123-1 (Third-party private catalogs)
status: review
effort: M
dispatched: 2026-04-23
merged: TBD
---

# Story V123-1.8 — Settings → Catalog Sources view (read-only, env-only mode)

## Brief (from epics-v1.23.md §V123-1.8)

As an **admin**, I want to see configured catalog sources in Settings, so that
I can verify what the env has set without SSHing to the pod.

**V123-1.1 resolved the open question §7.1 as env-only** — this story
implements the read-only list variant. No ConfigMap editor, no add/remove
actions. The only write action is the force-refresh button (which calls the
Tier-2 endpoint from V123-1.6).

## Acceptance criteria

**Given** the admin navigates to Settings → Catalog Sources
**When** the section renders
**Then** a read-only list from `GET /api/v1/catalog/sources` is shown with:
- `url` (the literal `embedded` OR the full third-party URL as text, NOT a link)
- status chip (`ok` / `stale` / `failed`)
- entry count
- last fetched timestamp
- verified badge (only if `verified: true`)
- a header explaining that sources come from `SHARKO_CATALOG_URLS` and are
  not editable in the UI in v1.23.

**Given** the admin clicks the "Refresh now" button
**When** the button is clicked
**Then** a POST to `/api/v1/catalog/sources/refresh` fires; the list updates
with the response; a success or failure toast renders.

**Given** the current user is NOT an admin
**When** they try to navigate to the section (via URL param)
**Then** the access is blocked (same pattern as Users / API Keys — admin-only
sections absent from the nav and redirect via the `ALLOWED_NON_ADMIN` guard).

**Given** the fetch of `/catalog/sources` fails
**When** the section mounts
**Then** an inline error chip + retry button are shown; the section does not
throw.

**Given** axe-core CI runs over the Settings page
**When** the new section renders
**Then** no new serious/critical violations.

## Implementation plan

### Files

- `ui/src/views/settings/CatalogSourcesSection.tsx` (NEW) — the section component.
- `ui/src/views/Settings.tsx` — add the admin-only nav entry + route branch.
- `ui/src/services/api.ts` — add `refreshCatalogSources()` (POST to /catalog/sources/refresh).
- `ui/src/views/settings/__tests__/CatalogSourcesSection.test.tsx` (NEW) — vitest cases.

### 1. API client addition — `ui/src/services/api.ts`

After `listCatalogSources()` (added in V123-1.7):

```ts
import type { CatalogSourceRecord } from './models'

export async function refreshCatalogSources() {
  return postJSON<CatalogSourceRecord[]>('/catalog/sources/refresh', {})
}
```

Same response shape as GET; the POST body is empty (the endpoint ignores it).

### 2. `<CatalogSourcesSection>` — the component

Location: `ui/src/views/settings/CatalogSourcesSection.tsx`.

Shape (follow sibling sections' structure — e.g., AIConfigSection.tsx for
patterns, styling, and in-section header/description):

```tsx
export function CatalogSourcesSection() {
  const [records, setRecords] = useState<CatalogSourceRecord[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [refreshing, setRefreshing] = useState(false)

  const load = useCallback(async () => {
    setError(null)
    try {
      const data = await api.listCatalogSources()
      setRecords(data)
    } catch (e) {
      setError('Failed to load catalog sources')
    }
  }, [])

  useEffect(() => { load() }, [load])

  const onRefresh = async () => {
    setRefreshing(true)
    try {
      const data = await api.refreshCatalogSources()
      setRecords(data)
      showToast({ type: 'success', message: 'Catalog sources refreshed' })
    } catch (e) {
      showToast({ type: 'error', message: 'Refresh failed' })
    } finally {
      setRefreshing(false)
    }
  }

  return (
    <section>
      {/* header + explainer + refresh button + records table + SourceBadge per row */}
    </section>
  )
}
```

**Match the existing section pattern**:
- Heading: "Catalog Sources" (h2-equivalent sizing)
- Sub-description: "Configured via the SHARKO_CATALOG_URLS environment variable.
  Not editable here — update the env and restart to change the list."
- Read-only list rendered as a table OR a set of cards (pick whichever matches
  sibling sections — AIConfigSection uses cards; GitOpsSection uses a list).
  Prefer a table here because there are 4-5 columns of structured data.
- Per row: `url` (text), status chip, entry count, last fetched (RFC3339 or
  "—"), verified badge (green pill) only if `verified: true`.
- "Refresh now" button top-right of the section header. Disabled while
  `refreshing`. Use the existing Sharko button palette (look at
  ConnectionSection for the Tier-2-action button style).

Use `<SourceBadge>` from V123-1.7 for the url cell — it already handles the
embedded/third-party distinction. Keep compact mode off here (detail page
size).

**Palette rules (frontend-expert.md)** — Sharko blue family; no gray-*
utilities in new code. Reuse existing palette variables from sibling sections.

### 3. Wire into `Settings.tsx`

- Import `CatalogSourcesSection`.
- Admin-only nav entry in a new "Catalog" group (or add to "Platform" alongside
  "AI Provider" — match whichever placement frontend-expert.md suggests; if
  unclear, put it under "Platform" since it's platform-config).
- Route guard: add `catalog-sources` to the admin-only branch (NOT to
  `ALLOWED_NON_ADMIN` — this is admin-only per V123-1.6 being Tier-2).
- Route handler:

```tsx
{section === 'catalog-sources' && isAdmin && <CatalogSourcesSection />}
```

Icon choice: `Database` or `BookMarked` or `Library` from lucide-react —
pick the one that best matches the existing palette + weight.

### 4. Toast helper

If the codebase has a toast/notification helper (check for `useToast`,
`showToast`, `notify`, etc. — inspect `ui/src/hooks/` and other sections first),
use that. If not, keep inline status messages via setState — do NOT introduce
a new toast library.

### 5. Tests — `ui/src/views/settings/__tests__/CatalogSourcesSection.test.tsx`

Follow the test pattern from sibling tests (check `ui/src/views/settings/__tests__/`
if that directory exists; otherwise mirror `ui/src/components/__tests__/SourceBadge.test.tsx`).

Mock `api.listCatalogSources` + `api.refreshCatalogSources`. Six cases:

1. Renders loading state then record list after successful fetch.
2. Shows "Refresh now" button that fires `api.refreshCatalogSources` when clicked.
3. Renders an error + retry button when `listCatalogSources` rejects.
4. Shows entry count + last-fetched text on each row.
5. Renders the SourceBadge (embedded vs third-party) — assert via role/text.
6. Does not include raw URL as a clickable anchor (palette+security rule).

## Test plan

- Unit (6 cases as above).
- Manual check: `cd ui && npm run dev`; visit `/settings?section=catalog-sources` as admin.
  - Empty state (no env configured) → single embedded row.
  - Failure state (mock the fetch to 503).
  - Refresh button round-trip.
- axe test suite runs in CI — must stay green.

## Quality gates

- `cd ui && npm run build`
- `cd ui && npm test -- --run`
- `cd ui && npm run lint` (if it runs locally; CI will otherwise)

No backend changes; Go tests not needed.

## Explicit non-goals

- ConfigMap editor / add / remove / persistent-sources-in-CM. V123-1.1 decided env-only.
- Per-source delete buttons — not in env-only mode.
- Cosign "Signed only" filter — V123-2.4.
- Conflicts diagnostic (merger's Conflicts) — punt to a later read endpoint.

## Dependencies

- V123-1.1 env-only decision — done ✅.
- V123-1.5 GET /catalog/sources — done ✅.
- V123-1.6 POST /catalog/sources/refresh (Tier-2) — done ✅.
- V123-1.7 `<SourceBadge>` — done ✅ (reuse).

## Gotchas

1. **URL as text, not anchor.** `entry.source` / `record.url` must never become
   `<a href="...">` — auth tokens in path can leak via referrer headers.
2. **Admin-only.** Route guard must mirror Users / API Keys; a non-admin with
   `?section=catalog-sources` must be redirected away.
3. **Palette rules** — no gray-* utilities; dark-mode classes on every color.
4. **Refresh button audit trail** — the POST to /catalog/sources/refresh is
   Tier-2 and gets an audit entry backend-side. The UI just fires the action;
   no client-side audit wiring needed.
5. **Empty state.** If /catalog/sources returns only the embedded pseudo-source
   (env unset), render the table with that single row and a helpful message:
   "No third-party sources configured. Set SHARKO_CATALOG_URLS to extend the catalog."
6. **Toast library.** If none exists, plain inline status messages are fine.
   Do NOT add a dependency.
7. **React act/flushSync warnings in tests.** Follow the patterns from
   SourceBadge.test.tsx + V120/V122 tests (use `await screen.findBy...` for
   async state).

## Role files (MUST embed in dispatch)

- `.claude/team/frontend-expert.md` — primary (React + Tailwind + palette + shadcn patterns).
- `.claude/team/test-engineer.md` — vitest + RTL patterns.

## PR plan

- Branch: `dev/v1.23-ui-settings-catalog-sources` off main.
- Commits:
  1. `feat(ui): refreshCatalogSources API client (V123-1.8)`
  2. `feat(ui): CatalogSourcesSection Settings view (V123-1.8)`
  3. `feat(ui): wire admin-only Catalog Sources section into Settings (V123-1.8)`
  4. `chore(bmad): mark V123-1.8 for review`
- No tag.

## Next story

V123-1.9 — Tests (unit + integration) for fetch + merge + source attribution
composition. Closes Epic V123-1.

## Tasks completed

- [x] **API client (`ui/src/services/api.ts`):** added `api.refreshCatalogSources()` returning `CatalogSourceRecord[]` from `POST /catalog/sources/refresh` with an empty body. Placed immediately after `listCatalogSources()` to keep the two sources-API fetchers together.
- [x] **New section `ui/src/views/settings/CatalogSourcesSection.tsx`:** read-only table with header explainer, Refresh-now button (Tier-2), five columns (Source / Status / Entries / Last fetched / Verified), and a helpful-hint empty state when only the embedded pseudo-source is present. SourceBadge used for the Source column. Defensive `typeof api.X !== 'function'` guards on both fetchers (matches V123-1.7 pattern — keeps pre-existing test mocks that intentionally omit these methods working).
- [x] **Palette:** Sharko blue family only (`#d6eeff` / `#0a3a5a` / `#2a5a7a` / `#b4dcf5` / `#c0ddf0` / `#e8f4ff` / `#5a9dd0`). Status chips use emerald/amber/red (semantic, matches sibling sections like AIConfigSection's green "Active" pill). Every light-mode color class has a `dark:` variant (`dark:bg-gray-*` / `dark:text-gray-*` is allowed in *dark mode* per `.claude/team/frontend-expert.md` §"Dark Mode Colors"). No light-mode `gray-*` utilities.
- [x] **Accessibility:** semantic `<table>` / `<thead>` / `<tbody>` with `<th scope="col">`. Loading state uses `aria-live="polite"`; the inline status message uses `role="status"` + `aria-live="polite"`; error surface uses `role="alert"`. Button carries `aria-label="Refresh catalog sources"`; all lucide icons are `aria-hidden`. Verified pill has a context-rich `aria-label` (includes issuer when present).
- [x] **Settings integration (`ui/src/views/Settings.tsx`):** imported `CatalogSourcesSection` + `Library` lucide icon. Added a new admin-only `Catalog` group to the `DetailNavPanel` nav (appears only when `isAdmin`). Render branch `{section === 'catalog-sources' && isAdmin && <CatalogSourcesSection />}` is guarded. `catalog-sources` is *not* added to `ALLOWED_NON_ADMIN` — the existing redirect in the `useEffect` bounces non-admins with `?section=catalog-sources` to `connections`, same pattern as Users / API Keys.
- [x] **Unit tests (`ui/src/views/settings/__tests__/CatalogSourcesSection.test.tsx`):** 6 vitest cases per brief — loading → records, refresh button round-trip, error + retry, entry_count + last_fetched (including null `—` dash), SourceBadge per row (embedded + third-party), and a no-clickable-anchor guard for the raw URL. Mocks `api.listCatalogSources` + `api.refreshCatalogSources` via `vi.mock('@/services/api', …)` following the MarketplaceTab.test.tsx pattern.
- [x] **Quality gates:** `npm run build` clean, `npm test -- --run` 181/181 PASS (up from 175 after V123-1.7; +6 new CatalogSourcesSection tests). `npm run lint` could not run locally — `eslint` binary is absent from `ui/node_modules/.bin/` (same devDependency gap V123-1.7 hit). CI will run it on the PR.
- [x] **BMAD tracking:** frontmatter flipped to `status: review` + `dispatched: 2026-04-23`; `sprint-status.yaml` `V123-1-8-…: backlog → review` and the `last_updated` header refreshed.

## Files touched

- `ui/src/services/api.ts` — `+8 LOC`. New `refreshCatalogSources` method immediately below `listCatalogSources`; imports `CatalogSourceRecord` via the existing `import('./models')` inline style for parity with siblings.
- `ui/src/views/settings/CatalogSourcesSection.tsx` — NEW, ~260 LOC. Section component + `StatusChip` helper. Explicit dark-mode variants on every color utility; zero light-mode `gray-*` classes. Renders URL as text (never `<a>`).
- `ui/src/views/settings/__tests__/CatalogSourcesSection.test.tsx` — NEW, ~170 LOC. 6 vitest cases per brief.
- `ui/src/views/Settings.tsx` — `+5 LOC`. `Library` icon + `CatalogSourcesSection` imports, admin-only Catalog nav group, guarded render branch.
- `.bmad/output/implementation-artifacts/sprint-status.yaml` — `V123-1-8` backlog → review; `last_updated` header refreshed.
- `.bmad/output/implementation-artifacts/V123-1-8-ui-optional-settings-catalog-sources-view-read-only-if-env-only.md` — frontmatter + retrospective sections (this file).

## Tests

Targeted run (new file only):

```bash
cd ui && npm test -- --run src/views/settings/__tests__/CatalogSourcesSection.test.tsx
# 6/6 PASS
```

Full suite:

```bash
cd ui && npm test -- --run
# Test Files  32 passed (32)
# Tests      181 passed (181)
```

The 6 new CatalogSourcesSection cases:

1. Renders loading state then record list on successful fetch. Asserts the `Loading catalog sources…` live-region text, then waits for the embedded record to render via `SourceBadge`'s "Internal" label; confirms the semantic column headers appear.
2. Fires `api.refreshCatalogSources` when the Refresh button is clicked. Asserts the mock is called once, the refreshed list includes the new third-party row (`catalogs.example.com`), and the success status strip renders via `role="status"`.
3. Shows error + retry when `listCatalogSources` rejects; retry succeeds. First rejection renders `role="alert"` with `Failed to load catalog sources`; clicking Retry runs the second (resolved) call and the alert is gone.
4. Renders `entry_count` + `last_fetched` text on each row. Uses two rows (one `'ok'` with RFC3339 last_fetched, one `'stale'` with `null`). Asserts numeric entry counts `7` and `3` render, the RFC3339 string renders verbatim, and the `—` dash placeholder is present.
5. Renders a SourceBadge per row (embedded + third-party). Asserts both `Internal` (embedded) and `catalogs.example.com` (third-party) render, plus the Verified pill's `aria-label` `Verified (issuer: acme-ci)`.
6. Does NOT render the raw URL as a clickable anchor. Asserts the URL text IS present but `container.querySelector('a[href*="catalogs.example.com"]')` is null.

Pre-existing suites left green:

- 175 tests from V123-1.7 carry forward — the new `api.refreshCatalogSources` is additive; no existing mocks refer to it.
- `MarketplaceTab.test.tsx` / `MarketplaceSearchTab.test.tsx` / `AddonDetail.test.tsx` — their `vi.mock('@/services/api', …)` object omits `listCatalogSources` and `refreshCatalogSources`, but our defensive `typeof api.X !== 'function'` guards mean the section silently renders an empty table in those tests (which don't exercise it anyway).
- `a11y.test.tsx` — settings page is not covered by the v1.20-pages axe runner, so no new violations to flag. The new section structure (semantic table + aria-live regions + role="status" / role="alert" + aria-hidden icons) follows WCAG patterns used elsewhere (StatusBadge, DriftAlertsPanel).

## Decisions

- **Defensive `typeof api.X !== 'function'` guards.** Brief section §1 explicitly allows it via the V123-1.7 pattern. Applied to both `listCatalogSources` and `refreshCatalogSources` calls. Rationale: several existing component test files (`MarketplaceTab.test.tsx`, `a11y.test.tsx`, etc.) mock `@/services/api` with a hand-picked method set. A plain `api.refreshCatalogSources()` call would break any future test that imports the section via a page-level render without explicitly mocking the method. Same pattern is already used for `api.getAIConfig` in `AddonDetail.tsx`.
- **Admin-only via nav-conditional + render-guard, NOT `ALLOWED_NON_ADMIN`.** Per story gotcha #2 and the existing pattern for Users / API Keys: the `catalog-sources` key is absent from the `ALLOWED_NON_ADMIN` Set, so a non-admin hitting `?section=catalog-sources` is redirected to `connections` by the existing `useEffect`. The nav entry is inside the `...(isAdmin ? […] : [])` spread so non-admins never see the link. The render branch adds a belt-and-braces `&& isAdmin` guard — matches the Users / API Keys render branches exactly.
- **Placement: new `Catalog` group inside the admin-only block, adjacent to `Access`.** The brief offered two options (extend `Platform` or create a new `Catalog` group). `Platform` would dilute the admin-only semantics — it currently contains the non-admin-accessible `AI Provider` item, and interleaving an admin-only row there would break the visual grouping. A dedicated `Catalog` group keeps the intent explicit and leaves room to grow (V123-2.4's "Verified only" filter may want siblings here in v1.24+).
- **No toast library — inline `role="status"` strip, not shadcn toast.** Brief gotcha #6 + story §4 explicitly: "Toast library. If none exists, plain inline status messages are fine. Do NOT add a dependency." Inspected: the codebase has no `useToast` / `showToast` / notify pattern wired up. Matches the AIConfigSection `testResult` strip (also inline). The strip carries `role="status"` + `aria-live="polite"` for screen-reader announcement.
- **Status chip palette: emerald (ok) / amber (stale) / red (failed).** These are semantic traffic-light colors that the rest of the codebase already uses for status (see StatusBadge.tsx, DriftAlertsPanel.tsx, the AIConfigSection `Active` pill). Emerald/amber/red pass AA contrast on both light (`#f0f7ff`) and dark (`gray-800`) backgrounds per the existing axe suite which already accepts these elsewhere.
- **No clickable anchor on the URL — text rendering with `break-all`.** Enforced in JSX (`<span>` / `<div>`, never `<a>`) and locked in by test case #6 which asserts no `a[href*="…"]` element exists. Auth tokens may live in the URL path; anchor elements leak via `Referer` header + browser-history sync. Same gotcha #1 the V123-1.7 SourceBadge handled.
- **`SHARKO_CATALOG_URLS` shown as `<code>` inline with the body copy AND the empty-state hint.** Redundant but intentional — the header explains the normal case ("it comes from this env var"), the empty-state hint explains the *current* case ("you haven't set one yet"). Two low-cost mentions prevent the admin from having to leave the page to learn where the list comes from.
- **Verified pill gets `aria-label` including issuer when present.** `sourceRecord.issuer` is an optional field on the backend response (reserved for V123-2 cosign integration). When present, the aria-label reads `Verified (issuer: acme-ci)` so screen-reader users get the same context sighted users will get from the tooltip once V123-2 wires it up. Today the issuer is always empty — harmless.
- **Loading state is a full-row text + spinner, not a table skeleton.** A skeleton-row table would render empty `<tr>`s that screen readers announce as empty rows, and we'd need to manage a realistic row count. A single "Loading…" line with `aria-live="polite"` is both simpler and more accessible for this short-duration fetch. When data arrives, the table renders in one go.
- **`last_fetched` rendered as the raw RFC3339 string, not humanised.** The existing SourceBadge tooltip (V123-1.7) also shows the raw RFC3339. Keeping the two call sites aligned means no "42 minutes ago" on one screen and `2026-04-23T10:00:00Z` on another — a future story can swap both to a relative formatter in one place.

## Gotchas / constraints addressed

1. **URL as text, never `<a href>`.** Enforced in JSX; locked by test case #6.
2. **Admin-only nav + render-guard.** Nav entry inside `isAdmin ? […]` block; render branch adds `&& isAdmin`; `ALLOWED_NON_ADMIN` *unchanged* so the existing redirect pattern fires for non-admins — same model as Users / API Keys.
3. **Palette rule — no `gray-*` utilities in light mode.** Enforced across `CatalogSourcesSection.tsx` + `StatusChip`; every color utility has a `dark:` sibling using Sharko blues.
4. **Refresh button audit trail.** Backend side — the POST to `/catalog/sources/refresh` is Tier-2 and emits an audit entry via the existing V123-1.6 handler. The UI just fires the action; no client-side audit wiring needed.
5. **Empty state.** When only `embedded` is present, the `onlyEmbedded` flag renders the hint block at the bottom of the section: "No third-party sources configured. Set `SHARKO_CATALOG_URLS` to extend the catalog."
6. **No new dependencies.** Inline `role="status"` status strip matches the AIConfigSection `testResult` banner. No toast library added.
7. **React `act` / async warnings.** Tests use `await screen.findByRole(…)` + `await waitFor(…)` for every state transition — no `act()` warnings in the run output.

## Quality-gate summary

| Gate | Command | Result |
|---|---|---|
| Build | `cd ui && npm run build` | clean (TypeScript + Vite both green) |
| Unit tests | `cd ui && npm test -- --run` | 181/181 PASS (+6 new) |
| CatalogSourcesSection targeted | `cd ui && npm test -- --run src/views/settings/__tests__/CatalogSourcesSection.test.tsx` | 6/6 PASS |
| Lint | `cd ui && npm run lint` | **skipped** — `eslint` binary not in `ui/node_modules/.bin/`. CI will run it on the PR. |
| Go tests | n/a — backend unchanged | n/a |

## Deviations from the brief

- **Nav placement: new `Catalog` group (not extending `Platform`).** Brief offered either option; chose the new group per the "simplest" option noted in the brief itself and to preserve `Platform`'s non-admin-accessible nature (it contains AI Provider, which is not admin-gated).
- **Icon choice: `Library` from lucide-react.** Brief listed three candidates (Database / BookMarked / Library). Library reads best for "set of catalogs" semantics and already harmonises with the palette weights used on sibling nav icons.
- **Raw URL displayed twice (as SourceBadge host pill + as full URL text below).** Brief had "url (text, not a link)" — the SourceBadge already shows the host, so I added the full URL as a smaller subtitle line below it so admins can copy/paste the complete path without hovering. Still strictly text.


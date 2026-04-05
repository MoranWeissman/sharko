# Sharko UI Redesign — Ocean Vibe

> Design spec for transforming the Sharko UI from a functional demo into a polished OSS product with personality.

**Date:** 2026-04-05
**Status:** Draft — pending user approval

---

## 1. Design Philosophy

**What should a platform engineer FEEL when they use Sharko?**

- **Confident** — like a captain at the helm. Clear status, no guessing.
- **Calm** — ocean blues, not alarm-red dashboards. Problems surface, but the default state is serenity.
- **Fast** — get in, see what matters, act, get out. No clicks to find information.

The shark mascot is Sharko's personality. It appears in sidebar branding, dashboard hero, empty states, loading screens, and error pages. Sharko is friendly, competent, and a bit playful — never corporate.

---

## 2. Ocean Theme — Color Palette

### Ocean Theme (Default)

Replace the current gray/slate sidebar and generic whites with a navy-to-blue palette.

| Role | Token | Value | Usage |
|------|-------|-------|-------|
| Sidebar BG | `--sidebar-bg` | `#0B1426` | Deep ocean — sidebar background |
| Sidebar Active | `--sidebar-active` | `#132038` | Slightly lighter for active state |
| Sidebar Border | `--sidebar-border` | `#1A2D4A` | Subtle separation |
| Sidebar Text | `--sidebar-text` | `#94B8DB` | Muted blue-gray |
| Sidebar Text Active | `--sidebar-text-active` | `#E0F0FF` | Bright on active |
| Accent | `--accent` | `#22D3EE` | Cyan-400 — links, active indicators, buttons |
| Accent Hover | `--accent-hover` | `#06B6D4` | Cyan-500 — hover state |
| Content BG | `--content-bg` | `#F0F7FF` | Very light blue tint (not pure white) |
| Card BG | `--card-bg` | `#FFFFFF` | White cards on light blue background |
| Card Border | `--card-border` | `#D6E5F5` | Soft blue-gray border |
| Hero Gradient Start | `--hero-start` | `#0E7490` | Cyan-700 |
| Hero Gradient End | `--hero-end` | `#1E40AF` | Blue-700 |
| Success | `--success` | `#22C55E` | Green-500 |
| Warning | `--warning` | `#F59E0B` | Amber-500 |
| Error | `--error` | `#EF4444` | Red-500 |

### Dark Theme (Alternative)

Standard dark mode — not ocean-themed, just a clean dark palette for nighttime use. Uses the existing dark theme CSS variables with minor refinements for card contrast.

### Implementation

- Define CSS custom properties in `index.css` under `:root` (ocean) and `.dark` (dark).
- Sidebar always uses `--sidebar-*` tokens (dark in both themes).
- Content area uses `--content-bg` and `--card-bg` (changes between themes).
- Theme toggle in user dropdown switches between ocean and dark.

---

## 3. Sidebar — Grouped Navigation with Mascot

**Pattern stolen from:** Headlamp (grouped sidebar with collapsible sections)

### Structure

```
┌──────────────────────┐
│  🦈 [mascot]  Sharko │  ← Logo row: sharko-mascot.png (28px) + "Sharko" text
│──────────────────────│
│  OVERVIEW            │  ← Section label (uppercase, 10px, muted)
│  ▸ Dashboard         │
│  ▸ Clusters          │
│  ▸ Addons Catalog    │
│──────────────────────│
│  MANAGE              │
│  ▸ Version Drift     │
│  ▸ Upgrade Checker   │
│  ▸ Observability     │
│  ▸ Dashboards        │
│──────────────────────│
│  CONFIGURE           │  ← Admin only
│  ▸ Settings          │  ← Single page with tabs (connections, users, API keys)
│──────────────────────│
│  HELP                │
│  ▸ AI Assistant      │
│  ▸ Docs              │
│──────────────────────│
│        [v1.0.0]      │
│       [collapse ‹]   │
└──────────────────────┘
```

### Changes from current

- **Logo row:** Replace `sharko-icon-32.png` with `sharko-mascot.png` (resized to 28px height). The mascot is more recognizable and has personality vs. the generic icon.
- **Section renaming:** "Monitor" → "Overview", "Operate" → "Manage". These are clearer for platform engineers.
- **Settings consolidation:** Remove separate "User Management" and "API Keys" routes from sidebar. They become tabs within Settings. (Reduces sidebar clutter from 12 items to 9.)
- **Collapsed state:** Show only mascot icon (no text). Section labels hidden.
- **Active indicator:** Cyan left border (keep current 3px pattern — it works).
- **Background:** `#0B1426` (deep ocean navy, not `slate-900`).

---

## 4. Dashboard — Cluster Cards + Activity Feed

**Patterns stolen from:** Rancher (cluster cards), Komodor (change timeline), ArgoCD (status dots)

### Layout

```
┌─────────────────────────────────────────────────────────────────────┐
│  [sharko-banner.png]                                                │
│  Addon management across all your Kubernetes clusters               │
│                                                           [Refresh] │
└─────────────────────────────────────────────────────────────────────┘

┌─── Needs Attention (if any) ────────────────────────────────────────┐
│  ⚠ 2 apps with issues  •  1 disconnected cluster  •  3 addons drift│
└─────────────────────────────────────────────────────────────────────┘

┌── Stats Row ────────────────────────────────────────────────────────┐
│  [Clusters: 5]  [Apps: 42/45 healthy]  [Addons: 8]  [Deployments]  │
└─────────────────────────────────────────────────────────────────────┘

┌── Cluster Cards (2-3 per row) ──────────────────────────────────────┐
│ ┌─────────────────┐ ┌─────────────────┐ ┌─────────────────┐        │
│ │ prod-us-east    │ │ prod-eu-west    │ │ staging         │        │
│ │ ● Connected     │ │ ● Connected     │ │ ● Connected     │        │
│ │ 8/8 healthy     │ │ 7/8 healthy     │ │ 5/5 healthy     │        │
│ │ [addon dots]    │ │ [addon dots]    │ │ [addon dots]    │        │
│ └─────────────────┘ └─────────────────┘ └─────────────────┘        │
└─────────────────────────────────────────────────────────────────────┘

┌── Bottom Row (3 columns) ───────────────────────────────────────────┐
│  Health Bars (keep)  │  Recent Activity  │  Version Drift (keep)    │
└─────────────────────────────────────────────────────────────────────┘
```

### Hero Section Changes

- Replace the text-only "Sharko" hero with `sharko-banner.png` as background image.
- Banner image positioned left/center, text overlaid on right.
- Keep the gradient as fallback behind the banner for consistent color.
- Subtle wave SVG at bottom edge of hero for ocean feel.

### Cluster Cards (NEW)

Each cluster is a card showing:
- **Cluster name** (bold)
- **Connection status** (green/red dot + "Connected"/"Disconnected")
- **Addon health summary** (e.g., "7/8 healthy")
- **Addon status dots** — small colored dots for each addon (green=healthy, amber=progressing, red=degraded, gray=unknown). Inspired by ArgoCD app tiles.
- **Click** → navigates to `/clusters/{name}`

Cards use a responsive grid: 3 columns on desktop, 2 on tablet, 1 on mobile.

### What stays the same

- Stats cards row (just restyle with ocean colors)
- Health bars (keep — they work well)
- Needs Attention banner (keep — important)
- Quick Actions (keep but move below cluster cards)
- Version Drift summary (keep)
- Recent Activity → rename from "Recent Syncs", same data

---

## 5. Detail Pages — Tabbed Layout

**Pattern stolen from:** Backstage (entity detail page with tabs)

### Cluster Detail (`/clusters/:name`)

Tabs: **Overview** | **Addons** | **History**

- **Overview:** Cluster info (ArgoCD server, connection status, labels, namespace), addon count, last sync time.
- **Addons:** Table of addons on this cluster with health, sync status, version, drift indicator.
- **History:** Git PR history for this cluster (commits, merges, changes). Uses the existing observability data filtered to this cluster.

### Addon Detail (`/addons/:name`)

Tabs: **Overview** | **Clusters** | **Upgrade**

- **Overview:** Addon description, catalog version, chart info, values template.
- **Clusters:** Which clusters have this addon, per-cluster version and health.
- **Upgrade:** Version comparison, upgrade impact check (existing upgrade checker scoped to this addon).

### Implementation

- Use a `<Tabs>` component (shadcn/ui already has one).
- Tab state stored in URL search params (`?tab=addons`) for bookmarkability.
- Each tab lazy-loads its content.

---

## 6. Addons Section — Sub-pages

Current: flat list at `/addons`. 

New structure:
- `/addons` — Addons Catalog (tile grid, not table). Each addon is a card with: name, description, version, health-across-clusters dots.
- `/version-matrix` — Version Drift Detector (keep existing, restyle).
- `/upgrade` — Upgrade Checker (keep existing, restyle).

The catalog view uses the ArgoCD tile pattern: each addon is a visual card rather than a table row. Cards show addon name, current catalog version, and a row of colored dots (one per cluster) showing health.

---

## 7. Settings Consolidation

Current: 3 separate pages (Settings/Connections, User Management, API Keys).

New: Single page at `/settings` with tabs:

**Connections** | **Users** | **API Keys** | **AI Provider**

- Connections tab = current Connections page content
- Users tab = current UserManagement page content (admin only)
- API Keys tab = current ApiKeys page content (admin only)
- AI Provider tab = AI config currently buried in Settings

Sidebar shows only "Settings" under Configure. Reduces sidebar items.

The `/users` and `/api-keys` routes redirect to `/settings?tab=users` and `/settings?tab=api-keys` for backwards compatibility.

---

## 8. Mascot Placement

The shark mascot appears throughout the app to give it personality:

| Location | Image | Size | Behavior |
|----------|-------|------|----------|
| Sidebar logo | `sharko-mascot.png` | 28px height | Always visible, next to "Sharko" text |
| Dashboard hero | `sharko-banner.png` | ~120px height | Background of hero gradient, left-aligned |
| Empty states | `sharko-mascot.png` | 80px | Centered above "No data" message. Shark looks curious. |
| Loading states | `sharko-mascot.png` | 48px | Centered with pulse animation. "Sharko is diving in..." |
| Error pages | `sharko-mascot.png` | 64px | Above error message. Shark looks concerned. |
| Login page | `sharko-login-bg.png` | Full panel | Left panel background (already implemented) |
| Login logo | `sharko-icon-256.png` | 56px | Right panel branding (already implemented) |

### Asset Pipeline

Copy to `ui/public/`:
- `sharko-mascot.png` — already there
- `sharko-banner.png` — needs to be copied from `assets/logo/`

---

## 9. Empty States, Loading, and Errors

### Empty States

When a page has no data (no clusters, no addons, etc.):

```
       [sharko-mascot.png @ 80px]
        
    No clusters connected yet
    
    Connect your first cluster to get started.
    
    [Add Cluster →]
```

Each page gets a contextual empty state:
- Clusters: "No clusters connected yet"
- Addons: "No addons in the catalog"
- Version Matrix: "Add clusters and addons to see version drift"
- Observability: "No sync activity yet"

### Loading States

Replace the current generic spinner with:

```
    [sharko-mascot.png @ 48px, pulse animation]
    Loading clusters...
```

### Error States

```
    [sharko-mascot.png @ 64px]
    
    Something went wrong
    
    {error.message}
    
    [Try Again]
```

---

## 10. Micro-interactions and Polish

- **Card hover:** Subtle lift (translate-y -1px) + shadow increase. 150ms ease.
- **Status transitions:** Smooth color changes on health dots (500ms, already exists on health bars).
- **Sidebar active:** Fade transition on active state change (200ms, already exists).
- **Page transitions:** No page-level animations (they feel slow). Instant navigation.
- **Tooltip on addon dots:** Hover a cluster card's addon dot → shows "external-dns: Healthy" tooltip.
- **Wave decoration:** Optional subtle wave SVG at the bottom of the hero section. CSS-only, no image dependency.

---

## 11. What We Are NOT Doing

- No light "ocean" theme variant (just ocean dark sidebar + light content area, and full dark mode)
- No animations on page load (fast > fancy)
- No custom icon set (Lucide icons are fine)
- No redesign of the login page (already done, looks good)
- No mobile app layout changes beyond existing responsive breakpoints
- No new API endpoints (purely frontend changes)
- No i18n or multi-language support
- No custom font (Inter is already loaded and works well)

---

## 12. File Impact Summary

### New files
- `ui/src/components/ClusterCard.tsx` — Cluster card for dashboard
- `ui/src/components/AddonDots.tsx` — Colored addon health dots (reusable)
- `ui/src/components/Tabs.tsx` — Generic tabs component (or use shadcn)
- `ui/src/components/EmptyState.tsx` — Mascot + message + action component
- `ui/src/components/WaveDecoration.tsx` — SVG wave for hero section

### Modified files
- `ui/src/index.css` — Ocean theme CSS variables
- `ui/src/components/Layout.tsx` — Sidebar restructure, ocean colors, mascot logo
- `ui/src/views/Dashboard.tsx` — Hero banner, cluster cards section, activity feed
- `ui/src/views/ClusterDetail.tsx` — Add tabs
- `ui/src/views/AddonDetail.tsx` — Add tabs
- `ui/src/views/AddonCatalog.tsx` — Tile grid instead of table
- `ui/src/views/Connections.tsx` — Move into Settings tabs
- `ui/src/views/UserManagement.tsx` — Move into Settings tabs
- `ui/src/views/ApiKeys.tsx` — Move into Settings tabs
- `ui/src/components/LoadingState.tsx` — Add mascot
- `ui/src/components/ErrorState.tsx` — Add mascot
- `ui/src/components/ErrorFallback.tsx` — Add mascot
- `ui/public/` — Copy `sharko-banner.png`

### Deleted files (content merged into Settings)
- None deleted — keep existing views as-is for backwards compat, just hide from sidebar

---

## 13. Implementation Order

1. **Ocean theme** — CSS variables, sidebar colors. Immediate visual impact, no layout changes.
2. **Sidebar restructure** — Mascot logo, section renaming, settings consolidation.
3. **Dashboard hero + cluster cards** — Banner image, cluster card component, addon dots.
4. **Empty/loading/error states** — Mascot in all three, contextual messages.
5. **Detail page tabs** — Cluster and addon detail pages get tabbed layout.
6. **Addons catalog tiles** — Replace table with card grid.
7. **Settings consolidation** — Merge connections/users/api-keys into tabbed page.
8. **Polish** — Wave decoration, hover effects, tooltips, final color tuning.

Each step is independently shippable and testable.

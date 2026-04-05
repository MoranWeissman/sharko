# Sharko UI Redesign v2 — Context-First UX

> Supersedes `2026-04-05-ui-redesign-ocean-vibe.md`. This is the authoritative UI design spec.

**Date:** 2026-04-05
**Status:** In progress — brainstorming ongoing

---

## Design Philosophy

**Everything in context, never leave the page.**

- Operations happen WHERE the data is, not on separate pages
- Lots of features is great, but the experience of using them should be context-wise
- Don't scatter features across many routes that have same or different purposes
- Users should feel guided, not dumped on

**What should a platform engineer FEEL?**
- Confident — clear status, no guessing
- Calm — ocean blues, problems surface but default state is serenity
- Fast — get in, see what matters, act, get out

---

## 1. Color Palette — Sky Blue Ocean

**Settled.** Dark navy sidebar + sky-blue content area. Distinct from ArgoCD (cyan on gray) and Akuity (purple on white).

| Role | Value | Usage |
|------|-------|-------|
| Sidebar BG | `#0a2a4a` | Dark navy sidebar |
| Sidebar Active | `#14466e` | Active nav item background |
| Sidebar Border | `#14466e` | Borders between sections |
| Sidebar Text | `#4a8abf` | Inactive nav items |
| Sidebar Text Active | `#bee0ff` | Active nav item text |
| Brand ("Sharko") | `#9fcffb` | Logo text — always blue, never changes with accent |
| Content BG | `#bee0ff` | Main content area background |
| Card BG | `#FFFFFF` | White cards on sky-blue background |
| Card Border | `#90c8ee` | Soft blue border on cards |
| Button Primary | `#0a2a4a` | Dark navy buttons — high contrast on blue BG |
| Button Primary Text | `#FFFFFF` or `#bee0ff` | Light text on dark buttons |
| Button Secondary | transparent, border `#0a2a4a` | Outlined buttons |
| Hero Gradient | `#0a2a4a` → `#1a6aaa` | Dashboard hero banner |
| Success | `#22C55E` | Green-500 |
| Warning | `#F59E0B` | Amber-500 |
| Error | `#EF4444` | Red-500 |

### Dark Mode

Standard dark theme — the sidebar stays dark (already is), content area switches to dark gray. Not ocean-themed in dark mode.

---

## 2. Sidebar — Compact, Expandable Groups

**Patterns from:** Akuity (expandable nav groups), ArgoCD (compact logo + version)

### Structure

```
┌──────────────────────┐
│  [mascot] Sharko     │  ← h-10 mascot + "Sharko" (blue) + version
│          v1.0.0      │
│──────────────────────│
│  OVERVIEW            │
│  ▸ Dashboard         │
│  ▸ Clusters          │
│  ▸ Addons ▾          │  ← Expandable
│     · Catalog        │
│     · Upgrades       │
│     · Version Drift  │
│──────────────────────│
│  MANAGE              │
│  ▸ Observability     │
│  ▸ Dashboards        │
│──────────────────────│
│  CONFIGURE           │  ← Admin only
│  ▸ Settings          │
│──────────────────────│
│       [collapse ‹]   │
└──────────────────────┘
```

### Key details
- Width: `w-52` (208px) expanded, `w-16` (64px) collapsed
- Sidebar BG: `#0a2a4a`
- "Addons" expands/collapses to show sub-items (Catalog, Upgrades, Version Drift)
- No "Help" section — AI is accessed via floating button + top bar, Docs via readthedocs.org
- Version shown directly under "Sharko" text in logo area
- Auto-collapses when AI panel opens

---

## 3. Dashboard — Summary + Problem Clusters

**Patterns from:** Rancher (cluster cards), current Sharko (stat cards, health bars)

### What the dashboard shows

1. **Hero banner** — `sharko-banner.png` + tagline + wave decoration
2. **Needs Attention** — amber banner with issue counts (apps with issues, disconnected clusters, addons with drift)
3. **Stats cards** — Total Clusters, Applications healthy/total, Available Addons, Active Deployments
4. **Problem cluster cards** — ONLY clusters that need attention (disconnected, degraded apps). Max 5-6 cards. "View all N issues →" link if more.
5. **All systems operational** — green banner when nothing is wrong (replaces problem cards)
6. **Health bars** — Application health + cluster connectivity bars
7. **Bottom row** — Quick Actions, Recent Activity, Version Drift summary

### What the dashboard does NOT show
- ALL clusters (that's the Clusters page)
- Full addon catalog (that's the Addons page)
- Detailed tables (dashboards are for glancing, not reading)

---

## 4. Addons — Hub Page with Context-First Operations

**Core principle:** Upgrade and drift checking happen IN CONTEXT, not on separate pages.

### Addons Catalog (`/addons`)

Grid of addon cards. Each card shows:
- Addon name
- Current catalog version
- Health dots across clusters (ArgoCD-inspired)
- **Upgrade badge** — "2 newer versions available" indicator if upgrades exist
- Click → addon detail

**Action bar:** Prominent [+ Add Addon] button, top-right.

### Addon Detail (`/addons/:name`)

**Left secondary nav panel** (Akuity Settings pattern) + main content:

```
┌─────────────────────────────────────────────────────────┐
│  ← Back to Catalog    external-dns                      │
│─────────────────────────────────────────────────────────│
│  ┌──────────┐  ┌──────────────────────────────────────┐ │
│  │ Overview  │  │  [Main content area]                 │ │
│  │ Clusters  │  │                                      │ │
│  │ Upgrade   │  │  Shows content based on selected     │ │
│  │ Config    │  │  nav item                            │ │
│  │           │  │                                      │ │
│  └──────────┘  └──────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────┘
```

**Nav items:**
- **Overview** — addon info, stat cards, health progress bar, chart info
- **Clusters** — which clusters have this addon, per-cluster health/version, enable/disable toggles
- **Upgrade** — THE upgrader (not a separate page). Two modes:
  - **Default: Recent versions list** — shows available newer versions vertically. Each version has: badges (LATEST, SECURITY, PATCH), one-line summary, changelog link, one-click upgrade button (global). Quick scanning.
  - **Compare mode** — triggered by clicking "Compare" or selecting a specific version. Shows FROM→TO comparison: security fixes, new features, bug fixes. With AI: parsed release notes, security analysis. Without AI: raw changelog from Helm chart (still useful). Choose scope (all clusters or specific).
  - **Per-cluster versions** — shown below the version list. Each cluster shows its current version. **Drifted clusters are actionable** — "Upgrade to v1.14.3" button appears next to any cluster running an older version than catalog. One click to fix drift for that specific cluster.
  - API endpoints: `GET /api/v1/addons/:name/upgrades` (available versions), `GET /api/v1/addons/:name/changelog?from=v1&to=v2` (comparison)
- **Config** — global default values (YAML viewer), per-cluster overrides

### Upgrade Availability on Catalog

The catalog page shows upgrade badges on each addon card. A user can see at a glance "3 addons have upgrades available" without clicking into each one. But the actual upgrade action happens inside the addon's detail view.

### Version Drift

Accessible from:
1. Sidebar: Addons → Version Drift (dedicated view showing drift across ALL addons)
2. Addon detail → Clusters tab (shows drift for THIS addon specifically)
3. Dashboard → Version Drift summary widget (links to the full view)

---

## 5. Clusters — Hub Page

### Clusters Overview (`/clusters`)

Table/card view of all clusters (toggle between views). Each entry shows:
- Cluster name
- Connection status (green/red dot)
- Server version
- Addon count (healthy/total)
- Node count

**Action bar:** Prominent [+ Add Cluster] button, top-right.

### Cluster Detail (`/clusters/:name`)

**Top tab bar** (Backstage/Akuity pattern): Overview | Addons | Config

- **Overview** — cluster info, connection status, node count, labels
- **Addons** — table of addons on this cluster with health, version, drift. Enable/disable toggles. This is where per-cluster addon operations happen (in context).
- **Config** — cluster values YAML, global vs cluster config diff

Prominent [Remove Cluster] button in header (admin only, with confirmation).

---

## 6. Settings — Left Secondary Nav Panel

**Pattern from:** Akuity (Image 36 — settings with left category nav)

Single page at `/settings` with left nav panel:

```
┌──────────────────────────────────────────────────────────┐
│  Settings                                                │
│──────────────────────────────────────────────────────────│
│  ┌───────────────┐  ┌──────────────────────────────────┐ │
│  │ CONNECTIONS    │  │  [Settings content area]         │ │
│  │ · Connections  │  │                                  │ │
│  │               │  │                                  │ │
│  │ ACCESS        │  │                                  │ │
│  │ · Users       │  │                                  │ │
│  │ · API Keys    │  │                                  │ │
│  │               │  │                                  │ │
│  │ PLATFORM      │  │                                  │ │
│  │ · AI Provider │  │                                  │ │
│  │ · Platform    │  │                                  │ │
│  └───────────────┘  └──────────────────────────────────┘ │
└──────────────────────────────────────────────────────────┘
```

Admin-only sections (Users, API Keys) hidden for non-admin users.

---

## 7. AI Panel — Right Side Drawer

**Pattern from:** Amazon Q (right-side slide-in panel)

- Triggered by: floating button (bottom-right) OR "Ask AI" in top bar
- Opens as right-side panel (380px wide)
- Auto-collapses sidebar when opened
- Page-context aware ("Viewing the Dashboard")
- Full chat features: suggested prompts, new session, export conversation
- Persists across navigation

---

## 8. Notification Bell

**Pattern from:** Akuity (Image 35)

Top bar notification bell icon with:
- Badge count for unread notifications
- Dropdown showing recent notifications:
  - Addon upgrade available ("external-dns v6.0.0 available")
  - Cluster disconnected alerts
  - PR merge confirmations
- "Mark all as read"
- Link to full notification history (future)

---

## 9. Login Page

**Pattern from:** Old AAP design (wider panel, logo + name + description)

- Left panel: full background image (`sharko-login-bg.png`)
- Right panel: wider (`440px`), contains:
  - `sharko-banner.png` logo
  - "Sharko" name
  - "Addon management for Kubernetes clusters" description
  - Username/password form
  - Sign In button
  - Version footer

---

## 10. Prominent Action Buttons

**Pattern from:** Akuity ("+ Create", "+ Connect a cluster")

Core operations are NOT hidden in small corner buttons. They are:
- Full-width or prominently placed in the action bar area
- Colored (primary style), not ghost/outline
- Right-aligned in the page header area
- Examples:
  - Clusters page: `[+ Add Cluster]`
  - Addon catalog: `[+ Add Addon]`
  - Addon detail/upgrade: `[Upgrade to v6.0.0]`
  - Cluster detail: `[Remove Cluster]` (destructive, red)

---

## 11. Top Tab Bar for Detail Pages

**Pattern from:** Akuity (Summary | Clusters | Audit | Security)

Used on:
- Cluster detail: Overview | Addons | Config
- (Addon detail uses left nav panel instead — more items, needs categories)

Tab state synced to URL params (`?tab=addons`) for bookmarkability.

---

## 12. API Parity Principle

**Everything the UI does, the API must expose.**

The UI is ONE consumer of Sharko's capabilities. Others include:
- CLI (`sharko` command)
- IDP systems (Backstage, Port, custom portals)
- CI/CD pipelines
- Custom integrations

Any feature added to the UI (upgrade checking, version drift, release notes comparison) MUST have a corresponding API endpoint. The UI calls the API — it never has logic that isn't available through the API.

This means:
- Upgrade availability → `GET /api/v1/addons/:name/upgrades` (returns available versions)
- Release notes comparison → `GET /api/v1/addons/:name/changelog?from=v1&to=v2`
- Version drift → already exists at `/api/v1/addons/version-matrix`
- Notifications → `GET /api/v1/notifications` (future)

---

## 13. What We Are NOT Doing (renumbered)

- No separate Upgrade Checker page (lives inside addon detail)
- No separate Version Drift page route (lives in sidebar sub-nav + addon detail)
- No Docs page (future readthedocs.org)
- No AI Assistant as a dedicated route (floating panel only)
- No light sidebar theme
- No custom fonts (Inter works)
- No mobile-specific layouts beyond responsive breakpoints

---

## 13. Still To Brainstorm

- Addon detail layout — exact left nav panel design and content per section
- Cluster detail layout — exact tab content
- Notification system — what events generate notifications, persistence
- Addon upgrade UX — how release notes/changelog comparison works
- AI-parsed release notes — scope and feasibility
- Dark mode refinements with the new sky-blue palette

---

## 14. Implementation Order (preliminary)

1. **Color palette** — CSS variables, sidebar colors, content background
2. **Sidebar** — expandable Addons group, compact width, remove old items
3. **Login page** — wider panel, banner + name + description
4. **Dashboard** — remove all-cluster cards, add problem-only cards with cap
5. **Prominent action buttons** — restyle across all pages
6. **Settings** — convert to left nav panel layout
7. **Addon detail** — left nav panel with Overview/Clusters/Upgrade/Config
8. **Notification bell** — top bar component with dropdown
9. **AI panel** — move to right side, auto-collapse sidebar
10. **Addon catalog** — upgrade availability badges on cards
11. **Version Drift** — sidebar sub-nav item under Addons
12. **Polish** — hover effects, transitions, final color tuning

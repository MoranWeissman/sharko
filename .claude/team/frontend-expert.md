# Frontend Expert Agent

## Scope

**DO:** React components, TypeScript, Tailwind, Vite config, npm run build/test
**DO NOT:** Write Go code, modify Helm charts, change CI pipelines

You are a React/TypeScript specialist for the Sharko UI.

## Tech Stack
- React 18 + TypeScript
- Vite build tool
- TailwindCSS + shadcn/ui
- Lucide-react icons
- React Router v6 (client-side routing)
- Victory (charting)
- Vitest for testing (105 tests across 19 test files)
- Google Fonts: Quicksand (for "Sharko" brand text)

## Actual File Inventory

### Views (`ui/src/views/` — 17 views)
```
AddonCatalog.tsx       AddonDetail.tsx
AIAssistant.tsx        ApiKeys.tsx
ClusterDetail.tsx      ClustersOverview.tsx
Connections.tsx        Dashboard.tsx
Dashboards.tsx         Docs.tsx
Login.tsx              Observability.tsx
Settings.tsx           UpgradeChecker.tsx
UserInfo.tsx           UserManagement.tsx
VersionMatrix.tsx
```

### Custom Components (`ui/src/components/` — 21)
```
AddonDots.tsx          ClusterCard.tsx
CommandPalette.tsx     ConfirmationModal.tsx
ConnectionStatus.tsx   DateTimeDisplay.tsx
DetailNavPanel.tsx     EmptyState.tsx
ErrorFallback.tsx      ErrorState.tsx
FloatingAssistant.tsx  Layout.tsx
LoadingState.tsx       MarkdownRenderer.tsx
NotificationBell.tsx   RoleGuard.tsx
SearchableSelect.tsx   StatCard.tsx
StatusBadge.tsx        WaveDecoration.tsx
YamlViewer.tsx
```

### shadcn/ui Components (`ui/src/components/ui/` — 13)
```
badge  button  card  dialog  dropdown-menu  input
separator  sheet  sidebar  skeleton  table  tabs  tooltip
```

### Hooks (`ui/src/hooks/` — 5)
```
use-mobile.ts    useAuth.tsx       useConnections.tsx
useDashboards.ts useTheme.tsx
```

### Services (`ui/src/services/`)
```
api.ts       Centralized API client (fetchJSON, postJSON, putJSON, deleteJSON)
models.ts    TypeScript types mirroring Go models
```

## Color Palette (Sky-Blue Theme)

**CRITICAL RULE: Zero gray in light mode.** All `text-gray-*`, `bg-gray-*`, `border-gray-*` classes MUST have a `dark:` prefix. Light mode uses blue-tinted hex equivalents exclusively.

### Light Mode Colors

| Element | Class / Value |
|---------|--------------|
| **Main background** | `bg-[#bee0ff]` |
| **Sidebar** | `bg-[#1a3d5c]` (always dark, both modes) |
| **Sidebar border** | `border-[#14466e]` |
| **Cards / panels / top bar** | `bg-[#f0f7ff]` |
| **Card hover / active** | `bg-[#e0f0ff]` / `bg-[#d6eeff]` |
| **Card input fields** | `bg-[#e8f4ff]` |
| **Heading text** | `text-[#0a2a4a]` |
| **Body text** | `text-[#2a5a7a]` |
| **Muted / secondary text** | `text-[#3a6a8a]` |
| **Label / caption text** | `text-[#5a8aaa]` |
| **Sidebar section labels** | `text-[#5a9ad0]` |
| **Card borders** | `ring-2 ring-[#6aade0]` |
| **Dividers** | `border-[#6aade0]` |

### Dark Mode Colors

Dark mode uses standard Tailwind gray scale with `dark:` prefix:
- `dark:bg-gray-950` (main bg), `dark:bg-gray-900` (top bar), `dark:bg-gray-800` (cards)
- `dark:text-white` (headings), `dark:text-gray-300` (body), `dark:text-gray-400` (muted)
- `dark:border-gray-700` (borders)

### Card Border Pattern

**Use `ring-2 ring-[#6aade0]` for all card borders.** Do NOT use `border` or `border-2` with `border-[color]` — the global CSS reset in `index.css` overrides `border-color` to transparent via `*, ::before, ::after { border-color: ... }`. The `ring` utility bypasses this because it uses `box-shadow`, not `border`.

Example:
```tsx
// CORRECT
<div className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:ring-gray-700 dark:bg-gray-800">

// WRONG — border will be invisible
<div className="rounded-lg border-2 border-[#6aade0] bg-[#f0f7ff] p-4">
```

## DetailNavPanel Component

`ui/src/components/DetailNavPanel.tsx` is a reusable left navigation panel for detail pages. It renders a vertical list of tabs/sections with icons.

**Used by 3 pages:**
- `AddonDetail.tsx` — Overview, Version Matrix, Upgrade Checker, etc.
- `ClusterDetail.tsx` — Overview, Addons, Config Diff, Comparison, etc.
- `Settings.tsx` — Connections, Users, API Keys, AI Provider

**All detail pages must use `DetailNavPanel`** instead of building their own tab navigation. This ensures consistent left-panel-with-content layout across the app.

## Quicksand Font

The "Sharko" brand text uses Google Fonts Quicksand, loaded in `ui/index.html`. Applied via inline style (not Tailwind class):

```tsx
<span style={{ fontFamily: '"Quicksand", sans-serif', fontWeight: 700 }}>Sharko</span>
```

Used in:
- Sidebar logo area (`Layout.tsx`)
- AI panel header (`Layout.tsx`)
- Login page banner (`Login.tsx`)
- Dashboard title (`Dashboard.tsx`)

**Every instance of the word "Sharko" as a brand name must use Quicksand.**

## NotificationBell Component

`ui/src/components/NotificationBell.tsx` — bell icon in the top bar with a dropdown notification list. Displays unread count badge when notifications exist.

Currently uses **mock data** (hardcoded notification list). Will be connected to `GET /api/v1/notifications` when the notification backend is implemented.

The dropdown shows notification items with:
- Icon (based on type: upgrade, drift, security)
- Title and description
- Timestamp
- Read/unread state

## AI Panel (Right-Side Drawer)

AI is accessed two ways — both open the same right-side panel:

1. **Floating button** (bottom-right corner) — `FloatingAssistant.tsx` dispatches `open-assistant` custom event
2. **"Ask AI" button** in top bar header — direct state toggle

The panel is a 380px wide right-side drawer rendered in `Layout.tsx`:
- Gradient header (teal-to-blue) with "Sharko AI" title
- Embeds `AIAssistant` view in `embedded` mode with page context
- Page context is auto-detected from the current route (e.g., "the Cluster Detail page for prod-eu")

There is **no dedicated AI page route**. The `AIAssistant.tsx` view component exists but is only rendered inside the drawer.

## App Structure (`ui/src/App.tsx`)
```
BrowserRouter
  ThemeProvider (dark/light via sharko-theme localStorage)
    AuthProvider (sharko-auth-token sessionStorage)
      ConnectionProvider (active connection state)
        Layout (sidebar nav + top bar + AI panel)
          Routes:
            /dashboard, /clusters, /clusters/:name, /addons, /addons/:name,
            /observability, /dashboards, /settings, /user
          Redirects:
            /version-matrix → /addons
            /upgrade → /addons
            /users → /settings?section=users
            /api-keys → /settings?section=api-keys
```

### Removed Pages (v2)

These no longer have dedicated routes — they are either redirects or embedded:
- **Version Matrix** — redirect to `/addons` (version matrix is inside AddonDetail)
- **Upgrade Checker** — redirect to `/addons` (upgrade is inside AddonDetail)
- **Docs** — component exists but not routed (docs content embedded elsewhere)
- **AI Assistant** — no route, embedded in Layout drawer
- **Users / API Keys** — redirects to `/settings?section=...` (unified Settings page)

## Sidebar Navigation Structure

```
Overview:
  Dashboard     /dashboard     LayoutDashboard icon
  Clusters      /clusters      Server icon
  Addons        /addons        Package icon

Manage:
  Observability /observability  Activity icon
  Dashboards    /dashboards     BarChart3 icon

Configure (admin only):
  Settings      /settings       Settings icon
```

## Key Patterns
- Auth token: `sessionStorage.getItem("sharko-auth-token")`
- User/role: `sessionStorage.getItem("sharko-auth-user")` / `sharko-auth-role`
- Theme: `localStorage.getItem("sharko-theme")`
- API calls: all through `api.ts` helpers, auto-redirect to login on 401
- `RoleGuard.tsx` — conditionally renders children based on user role

## v1.4.0 UI Patterns

### FirstRunWizard

`ui/src/components/FirstRunWizard.tsx` — shown on first load when no active connection exists. Multi-step
wizard (connection type → credentials form → test → done). Replaces the old empty Settings state.

The wizard completes by calling `POST /api/v1/connections` then `POST /api/v1/init`. Init is async:
the wizard receives an `operation_id` and transitions to a progress step that polls
`GET /api/v1/operations/{id}` with a `useEffect` + `setInterval`. Heartbeat is sent to
`POST /api/v1/operations/{id}/heartbeat` every 15 seconds to keep the session alive.

```tsx
// Operations polling pattern
useEffect(() => {
  if (!operationId) return;
  const interval = setInterval(async () => {
    const op = await fetchOperation(operationId);
    setOperation(op);
    if (op.status === 'succeeded' || op.status === 'failed') {
      clearInterval(interval);
    }
  }, 2000);
  return () => clearInterval(interval);
}, [operationId]);
```

### Single Connection Edit (Settings)

Settings → Connections shows **one active connection** with an **Edit** button (no Add/Remove list).
Editing opens the connection form pre-populated with existing values. Token field shows a masked
placeholder — user only needs to provide a new token if rotating.

The connection model is singular: Sharko has one ArgoCD connection and one Git connection. The UI
reflects this by showing edit-in-place rather than a list.

### Async Init Flow

`sharko init` via UI:
1. Click **Initialize** in the Connections section
2. API returns `202` + `operation_id`
3. UI shows a progress log component streaming `operation.log` lines
4. Heartbeat sent every 15 seconds
5. On `succeeded`: show success state + ArgoCD sync URL
6. On `failed`: show error with last log line

### Code Splitting

Vite is configured with manual chunks to split large dependencies (Victory charting, shadcn heavy
components) into separate bundles. Improves initial page load — only the main bundle is required
for login and dashboard.

## v1.0.0 UI Patterns

### Synchronous API, No Polling
All write operations are synchronous — the API returns the final result. UI pattern:
1. User submits form
2. Show loading spinner (disable form)
3. Wait for HTTP response
4. Show result (success, partial, or error)

No job IDs, no progress polling, no progress modals.

### Role-Based Rendering
- Fetch user role from session on login
- Admin: all action buttons visible
- Operator: limited actions (refresh, sync)
- Viewer: read-only, no action buttons
- Store role in React context, conditionally render action elements

### Key UI Features
1. **Add Cluster form** — Clusters page -> "Add Cluster" -> name, region, addon multi-select -> spinner -> result
2. **Remove Cluster** — Cluster detail -> "Remove Cluster" -> confirmation modal -> spinner -> result
3. **Toggle addons** — Cluster detail -> addon toggles -> accumulate -> "Apply Changes" -> review -> spinner -> result
4. **Add Addon** — Addons page -> "Add Addon" -> chart, repo, version, namespace, sync wave -> spinner -> PR URL
5. **Remove Addon** — Addon detail -> impact preview (dry-run) -> type-to-confirm -> spinner -> result
6. **Addon secrets config** — Addon detail -> "Secrets" tab -> define secret template
7. **API Keys management** — Settings -> API Keys section -> list, create (show once + copy), revoke
8. **Initialize repo** — Connections section -> status indicators -> "Initialize" button -> spinner -> sync result
9. **Batch cluster add** — Clusters page -> "Add Clusters" -> discover from provider -> select -> configure -> progress table (sequential updates)
10. **Addon upgrade** — Addon detail -> "Upgrade" tab -> current version, available versions from Helm repo, global vs per-cluster

### Components
- `ConfirmationModal` — for destructive operations (red styling, type-to-confirm variant)
- `RoleGuard` — conditionally renders children based on user role
- `ClusterCard` — cluster card with health status and addon summary
- `AddonDots` — dot indicators for addon deployment status
- `WaveDecoration` — decorative wave SVG for page headers
- `EmptyState` — centered empty state with icon, title, description, and optional action

## When Adding New UI Features
1. Add TypeScript types to `ui/src/services/models.ts`
2. Add API methods to `ui/src/services/api.ts`
3. Create view in `ui/src/views/NewView.tsx`
4. Add route in `ui/src/App.tsx`
5. Add nav entry in `ui/src/components/Layout.tsx` (correct section)
6. Add test in `ui/src/views/__tests__/NewView.test.tsx`
7. Verify: `cd ui && npm run build && npm test`

**Design rules:**
- Use `ring-2 ring-[#6aade0]` for card borders (not `border`)
- Use blue-tinted colors for all light mode elements (no gray)
- Use `DetailNavPanel` for any page with multiple sections
- Use Quicksand font for "Sharko" brand text
- Use `NotificationBell` in the top bar (already in Layout)

## Update This File When
- New views or components are added
- shadcn/ui components are added
- Routing structure changes
- API service methods change significantly
- Color palette changes
- New reusable component patterns are established

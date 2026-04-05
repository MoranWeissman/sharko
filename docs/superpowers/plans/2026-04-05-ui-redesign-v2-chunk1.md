# Sharko UI Redesign v2 — Chunk 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Apply the sky-blue ocean color palette, expandable sidebar nav, reusable left nav panel component, login improvements, dashboard problem-only clusters, prominent action buttons, and settings left nav panel.

**Architecture:** CSS custom properties define the sky-blue palette. A reusable `DetailNavPanel` component provides the left nav panel pattern used by addon detail, cluster detail, and settings. Sidebar gains expandable Addons group with sub-items. Dashboard removes all-cluster cards, shows only problem clusters capped at 5.

**Tech Stack:** React 18, TypeScript, Vite, Tailwind CSS v4, shadcn/ui (Radix)

---

## File Structure

### New files
| File | Responsibility |
|------|---------------|
| `ui/src/components/DetailNavPanel.tsx` | Reusable left nav panel for detail pages (addon, cluster, settings) |

### Modified files
| File | Changes |
|------|---------|
| `ui/src/index.css` | Sky-blue palette CSS variables (`#bee0ff` content, `#0a2a4a` sidebar) |
| `ui/src/components/Layout.tsx` | Sidebar: navy `#0a2a4a`, expandable Addons group, active indicator color |
| `ui/src/views/Login.tsx` | Already has banner+name+description, just needs color alignment |
| `ui/src/views/Dashboard.tsx` | Remove all-cluster cards, show problem-only capped at 5 |
| `ui/src/views/Settings.tsx` | Convert from tabs to DetailNavPanel layout |
| `ui/src/views/ClustersOverview.tsx` | Prominent [+ Add Cluster] button |
| `ui/src/views/AddonCatalog.tsx` | Prominent [+ Add Addon] button |

---

### Task 1: Sky-Blue Color Palette

**Files:**
- Modify: `ui/src/index.css`
- Modify: `ui/src/components/Layout.tsx`

- [ ] **Step 1: Update CSS variables in index.css**

In `ui/src/index.css`, replace the `:root` block inside `@layer base` with sky-blue ocean palette values:

```css
  :root {
    --background: oklch(0.88 0.04 220);
    --foreground: oklch(0.15 0.02 240);
    --card: oklch(1 0 0);
    --card-foreground: oklch(0.15 0.02 240);
    --popover: oklch(1 0 0);
    --popover-foreground: oklch(0.15 0.02 240);
    --primary: oklch(0.35 0.08 240);
    --primary-foreground: oklch(0.95 0.02 220);
    --secondary: oklch(0.92 0.03 220);
    --secondary-foreground: oklch(0.25 0.03 240);
    --muted: oklch(0.92 0.03 220);
    --muted-foreground: oklch(0.5 0.02 240);
    --accent: oklch(0.35 0.08 240);
    --accent-foreground: oklch(0.95 0.02 220);
    --destructive: oklch(0.577 0.245 27.325);
    --destructive-foreground: oklch(0.577 0.245 27.325);
    --border: oklch(0.78 0.04 220);
    --input: oklch(0.78 0.04 220);
    --ring: oklch(0.35 0.08 240);
    --chart-1: oklch(0.45 0.1 240);
    --chart-2: oklch(0.6 0.118 184.704);
    --chart-3: oklch(0.398 0.07 227.392);
    --chart-4: oklch(0.828 0.189 84.429);
    --chart-5: oklch(0.769 0.188 70.08);
    --radius: 0.625rem;
    --sidebar: oklch(0.18 0.04 240);
    --sidebar-foreground: oklch(0.88 0.04 220);
    --sidebar-primary: oklch(0.65 0.08 220);
    --sidebar-primary-foreground: oklch(0.95 0 0);
    --sidebar-accent: oklch(0.25 0.04 240);
    --sidebar-accent-foreground: oklch(0.88 0.04 220);
    --sidebar-border: oklch(0.25 0.04 240);
    --sidebar-ring: oklch(0.65 0.08 220);
  }
```

Key change: `--background` is now a noticeable sky-blue (`#bee0ff` equivalent in oklch), `--primary` is dark navy for buttons, `--border` is blue-tinted, `--sidebar` is dark navy.

- [ ] **Step 2: Update Layout.tsx sidebar and content background colors**

In `ui/src/components/Layout.tsx`:

Change the outer container background:
```
bg-[#F0F7FF]  →  bg-[#bee0ff]
```

Change the sidebar background:
```
bg-[#0B1426]  →  bg-[#0a2a4a]
```

Change all sidebar-internal colors to match the new navy:
- `border-[#1A2D4A]` → `border-[#14466e]`
- `hover:bg-[#132038]` → `hover:bg-[#14466e]`
- `bg-[#1A2D4A]` (active) → `bg-[#14466e]`
- `text-[#5A7A9B]` (section labels) → `text-[#5a9ad0]`
- `text-[#94B8DB]` (nav text) → `text-[#7ab0d8]`
- `text-[#6B8FB5]` (collapse) → `text-[#5a9ad0]`

Change active indicator from `border-teal-400` to `border-[#9fcffb]`.

Change card border throughout the app: the `border-[#D6E5F5]` references should become `border-[#90c8ee]`.

- [ ] **Step 3: Update card borders globally**

Search for `border-[#D6E5F5]` across all .tsx files and replace with `border-[#90c8ee]`:

```bash
find ui/src -name "*.tsx" -exec sed -i '' 's/border-\[#D6E5F5\]/border-[#90c8ee]/g' {} +
```

- [ ] **Step 4: Verify build**

```bash
cd ui && npm run build
```

- [ ] **Step 5: Commit**

```bash
git add ui/src/index.css ui/src/components/Layout.tsx ui/src/components/ClusterCard.tsx ui/src/views/AddonCatalog.tsx
git commit -m "feat(ui): sky-blue ocean palette — #bee0ff content, #0a2a4a sidebar"
```

---

### Task 2: Expandable Sidebar Addons Group

**Files:**
- Modify: `ui/src/components/Layout.tsx`

- [ ] **Step 1: Add ChevronDown import and expandable state**

Add `ChevronDown` to the lucide-react imports. Add `TableProperties, ArrowUpCircle` back for the sub-nav items.

Add state for the expanded Addons group:
```tsx
const [addonsExpanded, setAddonsExpanded] = useState(true)
```

- [ ] **Step 2: Restructure navSections to support sub-items**

Replace the `NavItem` and `NavSection` interfaces and `navSections` array:

```tsx
interface NavItem {
  to: string
  label: string
  icon: typeof LayoutDashboard
  children?: { to: string; label: string }[]
}

interface NavSection {
  label: string
  items: NavItem[]
  adminOnly?: boolean
}

const navSections: NavSection[] = [
  {
    label: 'Overview',
    items: [
      { to: '/', label: 'Dashboard', icon: LayoutDashboard },
      { to: '/clusters', label: 'Clusters', icon: Server },
      {
        to: '/addons',
        label: 'Addons',
        icon: Package,
        children: [
          { to: '/addons', label: 'Catalog' },
          { to: '/addons/upgrades', label: 'Upgrades' },
          { to: '/version-matrix', label: 'Version Drift' },
        ],
      },
    ],
  },
  {
    label: 'Manage',
    items: [
      { to: '/observability', label: 'Observability', icon: Activity },
      { to: '/dashboards', label: 'Dashboards', icon: BarChart3 },
    ],
  },
  {
    label: 'Configure',
    adminOnly: true,
    items: [
      { to: '/settings', label: 'Settings', icon: Settings },
    ],
  },
]
```

- [ ] **Step 3: Update nav rendering to handle expandable items**

In the nav rendering JSX, replace the simple `NavLink` rendering with logic that checks for `children`:

For items with `children`:
- Render a button (not NavLink) that toggles `addonsExpanded`
- Show a `ChevronDown` icon that rotates when expanded
- When expanded, render child items as indented NavLinks below

For items without `children`:
- Keep the existing NavLink rendering unchanged

```tsx
{section.items.map((item) => (
  item.children ? (
    <div key={item.to}>
      <button
        onClick={() => setAddonsExpanded(e => !e)}
        className={`flex w-full items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-colors border-l-[3px] border-transparent text-[#7ab0d8] hover:bg-[#14466e] hover:text-white ${collapsed && !mobileOpen ? 'justify-center px-0' : ''}`}
      >
        <item.icon className="h-5 w-5 shrink-0" />
        {(!collapsed || mobileOpen) && (
          <>
            <span className="flex-1 text-left">{item.label}</span>
            <ChevronDown className={`h-4 w-4 transition-transform ${addonsExpanded ? 'rotate-0' : '-rotate-90'}`} />
          </>
        )}
      </button>
      {addonsExpanded && (!collapsed || mobileOpen) && (
        <div className="ml-6 mt-0.5 space-y-0.5 border-l border-[#14466e] pl-3">
          {item.children.map(child => (
            <NavLink
              key={child.to}
              to={child.to}
              end={child.to === '/addons'}
              onClick={() => setMobileOpen(false)}
              className={({ isActive }) =>
                `block rounded px-2 py-1.5 text-xs transition-colors ${
                  isActive
                    ? 'text-[#bee0ff] font-medium'
                    : 'text-[#5a9ad0] hover:text-[#bee0ff]'
                }`
              }
            >
              {child.label}
            </NavLink>
          ))}
        </div>
      )}
    </div>
  ) : (
    <NavLink
      key={item.to}
      to={item.to}
      end={item.to === '/'}
      onClick={() => setMobileOpen(false)}
      className={({ isActive }) =>
        `flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-colors ${
          isActive
            ? 'border-l-[3px] border-[#9fcffb] bg-[#14466e] text-white'
            : 'border-l-[3px] border-transparent text-[#7ab0d8] hover:bg-[#14466e] hover:text-white'
        } ${collapsed && !mobileOpen ? 'justify-center px-0' : ''}`
      }
      title={collapsed && !mobileOpen ? item.label : undefined}
    >
      <item.icon className="h-5 w-5 shrink-0" />
      {(!collapsed || mobileOpen) && <span>{item.label}</span>}
    </NavLink>
  )
))}
```

- [ ] **Step 4: Verify build**

```bash
cd ui && npm run build
```

- [ ] **Step 5: Commit**

```bash
git add ui/src/components/Layout.tsx
git commit -m "feat(ui): expandable Addons group in sidebar with sub-nav items"
```

---

### Task 3: Reusable DetailNavPanel Component

**Files:**
- Create: `ui/src/components/DetailNavPanel.tsx`

- [ ] **Step 1: Create the component**

Create `ui/src/components/DetailNavPanel.tsx`:

```tsx
import { useSearchParams } from 'react-router-dom'

interface NavGroup {
  label?: string
  items: NavPanelItem[]
}

interface NavPanelItem {
  key: string
  label: string
  badge?: string | number
  destructive?: boolean
}

interface DetailNavPanelProps {
  sections: NavGroup[]
  activeKey: string
  onSelect: (key: string) => void
}

export function DetailNavPanel({ sections, activeKey, onSelect }: DetailNavPanelProps) {
  return (
    <div className="w-48 shrink-0 border-r border-[#90c8ee] bg-white dark:border-gray-700 dark:bg-gray-800">
      <nav className="p-3 space-y-4">
        {sections.map((group, gi) => (
          <div key={gi}>
            {group.label && (
              <p className="mb-1 px-2 text-[9px] font-semibold uppercase tracking-wider text-gray-400 dark:text-gray-500">
                {group.label}
              </p>
            )}
            <div className="space-y-0.5">
              {group.items.map((item) => (
                <button
                  key={item.key}
                  onClick={() => onSelect(item.key)}
                  className={`flex w-full items-center justify-between rounded-md px-3 py-2 text-left text-sm transition-colors ${
                    activeKey === item.key
                      ? 'border-l-[3px] border-[#0a2a4a] bg-[#e0f0ff] font-semibold text-[#0a2a4a] dark:border-blue-400 dark:bg-gray-700 dark:text-white'
                      : item.destructive
                        ? 'border-l-[3px] border-transparent text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/20'
                        : 'border-l-[3px] border-transparent text-gray-600 hover:bg-gray-50 dark:text-gray-400 dark:hover:bg-gray-700'
                  }`}
                >
                  <span>{item.label}</span>
                  {item.badge !== undefined && (
                    <span className="rounded-full bg-gray-100 px-1.5 py-0.5 text-[10px] font-medium text-gray-500 dark:bg-gray-700 dark:text-gray-400">
                      {item.badge}
                    </span>
                  )}
                </button>
              ))}
            </div>
          </div>
        ))}
      </nav>
    </div>
  )
}

/** Hook for URL-synced section state */
export function useDetailSection(defaultSection: string) {
  const [searchParams, setSearchParams] = useSearchParams()
  const section = searchParams.get('section') || defaultSection
  const setSection = (s: string) => setSearchParams({ section: s }, { replace: true })
  return [section, setSection] as const
}
```

- [ ] **Step 2: Verify build**

```bash
cd ui && npm run build
```

- [ ] **Step 3: Commit**

```bash
git add ui/src/components/DetailNavPanel.tsx
git commit -m "feat(ui): add reusable DetailNavPanel component with URL-synced section state"
```

---

### Task 4: Dashboard — Problem Clusters Only

**Files:**
- Modify: `ui/src/views/Dashboard.tsx`

- [ ] **Step 1: Filter cluster cards to problems only, cap at 5**

In `ui/src/views/Dashboard.tsx`, find the cluster cards section. Currently it shows `clusters.slice(0, 6)`.

Replace the cluster cards section logic. After the `setClusters(cards)` line in fetchData, filter to only problem clusters:

After building the cards array, filter:
```tsx
// Filter to problem clusters only
const problemClusters = cards.filter(c =>
  c.connectionStatus !== 'Successful' && c.connectionStatus !== 'Connected' ||
  c.healthy < c.total
)
setClusters(problemClusters)
```

Then update the JSX section title and link:

```tsx
{clusters.length > 0 && (
  <div>
    <div className="mb-3 flex items-center justify-between">
      <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">Needs Attention</h2>
      {clusters.length > 5 && (
        <button
          onClick={() => navigate('/clusters?status=issues')}
          className="text-sm text-[#0a2a4a] hover:underline dark:text-blue-400"
        >
          View all {clusters.length} issues
        </button>
      )}
    </div>
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
      {clusters.slice(0, 5).map((cluster) => (
        <ClusterCard
          key={cluster.name}
          name={cluster.name}
          connectionStatus={cluster.connectionStatus}
          addonSummary={cluster.addons}
          healthyCount={cluster.healthy}
          totalCount={cluster.total}
        />
      ))}
    </div>
  </div>
)}
```

When all clusters are healthy, show nothing (the "All systems operational" banner already handles this).

- [ ] **Step 2: Verify build**

```bash
cd ui && npm run build
```

- [ ] **Step 3: Run tests**

```bash
cd ui && npm test -- --run
```

- [ ] **Step 4: Commit**

```bash
git add ui/src/views/Dashboard.tsx
git commit -m "feat(ui): dashboard shows only problem clusters, capped at 5"
```

---

### Task 5: Prominent Action Buttons

**Files:**
- Modify: `ui/src/views/ClustersOverview.tsx`
- Modify: `ui/src/views/AddonCatalog.tsx`

- [ ] **Step 1: Restyle Add Cluster button in ClustersOverview**

In `ui/src/views/ClustersOverview.tsx`, find the "Add Cluster" button (wrapped in RoleGuard). It's currently a small button. Make it more prominent:

Find the existing button styling and replace with:
```tsx
className="inline-flex shrink-0 items-center gap-2 rounded-lg bg-[#0a2a4a] px-5 py-2.5 text-sm font-semibold text-white shadow-sm hover:bg-[#0d3558] dark:bg-blue-700 dark:hover:bg-blue-600"
```

- [ ] **Step 2: Restyle Add Addon button in AddonCatalog**

In `ui/src/views/AddonCatalog.tsx`, find the "Add Addon" button. Same treatment:

Replace the button styling with:
```tsx
className="inline-flex shrink-0 items-center gap-2 rounded-lg bg-[#0a2a4a] px-5 py-2.5 text-sm font-semibold text-white shadow-sm hover:bg-[#0d3558] dark:bg-blue-700 dark:hover:bg-blue-600"
```

- [ ] **Step 3: Verify build**

```bash
cd ui && npm run build
```

- [ ] **Step 4: Commit**

```bash
git add ui/src/views/ClustersOverview.tsx ui/src/views/AddonCatalog.tsx
git commit -m "feat(ui): prominent action buttons with dark navy styling"
```

---

### Task 6: Settings — Left Nav Panel Layout

**Files:**
- Modify: `ui/src/views/Settings.tsx`

- [ ] **Step 1: Replace tabs with DetailNavPanel**

Replace the entire `ui/src/views/Settings.tsx` content:

```tsx
import { useEffect } from 'react'
import { useSearchParams } from 'react-router-dom'
import { DetailNavPanel } from '@/components/DetailNavPanel'
import { Connections } from '@/views/Connections'
import { UserManagement } from '@/views/UserManagement'
import { ApiKeys } from '@/views/ApiKeys'
import { useAuth } from '@/hooks/useAuth'

export function Settings() {
  const [searchParams, setSearchParams] = useSearchParams()
  const { isAdmin } = useAuth()
  const section = searchParams.get('section') || 'connections'
  const setSection = (s: string) => setSearchParams({ section: s }, { replace: true })

  useEffect(() => {
    if (!isAdmin && section !== 'connections') {
      setSearchParams({ section: 'connections' }, { replace: true })
    }
  }, [isAdmin, section, setSearchParams])

  const sections = [
    {
      label: 'Connections',
      items: [{ key: 'connections', label: 'Connections' }],
    },
    ...(isAdmin
      ? [
          {
            label: 'Access',
            items: [
              { key: 'users', label: 'Users' },
              { key: 'api-keys', label: 'API Keys' },
            ],
          },
        ]
      : []),
    {
      label: 'Platform',
      items: [{ key: 'ai', label: 'AI Provider' }],
    },
  ]

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-2xl font-bold text-gray-900 dark:text-gray-100">Settings</h1>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
          Manage connections, users, API keys, and platform configuration.
        </p>
      </div>

      <div className="flex gap-0 rounded-xl border border-[#90c8ee] bg-white overflow-hidden dark:border-gray-700 dark:bg-gray-800">
        <DetailNavPanel
          sections={sections}
          activeKey={section}
          onSelect={setSection}
        />
        <div className="flex-1 p-6">
          {section === 'connections' && <Connections embedded />}
          {section === 'users' && isAdmin && <UserManagement embedded />}
          {section === 'api-keys' && isAdmin && <ApiKeys embedded />}
          {section === 'ai' && (
            <div className="text-sm text-gray-500 dark:text-gray-400">
              AI Provider configuration coming soon.
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Update App.tsx routes for settings**

In `ui/src/App.tsx`, update the redirects to use `section` instead of `tab`:

Change:
```tsx
<Route path="users" element={<Navigate to="/settings?tab=users" replace />} />
<Route path="api-keys" element={<Navigate to="/settings?tab=api-keys" replace />} />
```
To:
```tsx
<Route path="users" element={<Navigate to="/settings?section=users" replace />} />
<Route path="api-keys" element={<Navigate to="/settings?section=api-keys" replace />} />
```

- [ ] **Step 3: Verify build**

```bash
cd ui && npm run build
```

- [ ] **Step 4: Run tests**

```bash
cd ui && npm test -- --run
```

Fix any test failures (the Connections test should still work since it renders `<Connections />` directly).

- [ ] **Step 5: Commit**

```bash
git add ui/src/views/Settings.tsx ui/src/App.tsx
git commit -m "feat(ui): settings uses left nav panel layout instead of tabs"
```

---

### Task 7: Login Page Color Alignment

**Files:**
- Modify: `ui/src/views/Login.tsx`

- [ ] **Step 1: Align login colors with the new palette**

In `ui/src/views/Login.tsx`:

Change the left panel background from `bg-[#0B1426]` to `bg-[#0a2a4a]` (matches new sidebar navy).

Change the right panel background from `bg-[#1a2332]` to `bg-[#0d3558]` (slightly lighter navy for the form panel).

Change the Sign In button from `bg-teal-500` to `bg-[#0a2a4a]` with `hover:bg-[#14466e]` (dark navy button on dark panel — needs light text, already has `text-white`).

Change focus rings from `focus:border-teal-500 focus:ring-teal-500` to `focus:border-[#9fcffb] focus:ring-[#9fcffb]` on both inputs.

Change the Sign In button ring from `focus:ring-teal-400` to `focus:ring-[#9fcffb]`.

- [ ] **Step 2: Verify build**

```bash
cd ui && npm run build
```

- [ ] **Step 3: Commit**

```bash
git add ui/src/views/Login.tsx
git commit -m "feat(ui): align login page colors with sky-blue ocean palette"
```

---

### Task 8: Final Build + Full Test Run

- [ ] **Step 1: Full build**

```bash
cd ui && npm run build
```

- [ ] **Step 2: Run all tests**

```bash
cd ui && npm test -- --run
```

Fix any failures.

- [ ] **Step 3: Visual verification**

```bash
make demo
```

Verify at http://localhost:8080:
- Sky-blue (#bee0ff) content background — noticeably blue, NOT white
- Dark navy (#0a2a4a) sidebar
- "Sharko" text is blue in sidebar
- Expandable Addons group with Catalog/Upgrades/Version Drift sub-items
- Dashboard shows only problem clusters (or none if all healthy)
- Settings page uses left nav panel (not tabs)
- Login page colors match the new palette
- Action buttons are prominent dark navy
- Card borders are #90c8ee blue

- [ ] **Step 4: Commit any final fixes**

```bash
git add -A
git commit -m "fix(ui): final polish and test fixes for sky-blue palette"
```

# Sharko UI Redesign — Ocean Vibe Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Transform the Sharko UI from a functional demo into a polished OSS product with ocean-themed visuals, mascot personality, and patterns stolen from ArgoCD/Backstage/Rancher/Headlamp/Komodor.

**Architecture:** CSS custom properties for theming (ocean default + dark alternative). Sidebar restructure with grouped nav and mascot. Dashboard gains cluster cards and activity feed. Detail pages get shadcn/ui Tabs. Mascot appears in empty/loading/error states. Settings consolidation merges 3 pages into tabbed view.

**Tech Stack:** React 18, TypeScript, Vite, Tailwind CSS v4, shadcn/ui (Radix primitives), Lucide icons

---

## File Structure

### New files
| File | Responsibility |
|------|---------------|
| `ui/src/components/ClusterCard.tsx` | Dashboard cluster card with addon health dots |
| `ui/src/components/AddonDots.tsx` | Reusable colored health dot row with tooltips |
| `ui/src/components/EmptyState.tsx` | Mascot + message + optional action button |
| `ui/src/components/WaveDecoration.tsx` | CSS-only SVG wave for hero section bottom |
| `ui/src/views/Settings.tsx` | Unified settings page with tabs (connections, users, API keys, AI) |

### Modified files
| File | Changes |
|------|---------|
| `ui/src/index.css` | Ocean theme CSS variables under `:root`, dark refinements under `.dark` |
| `ui/src/components/Layout.tsx` | Sidebar: ocean colors, mascot logo, section rename, remove users/api-keys nav items |
| `ui/src/views/Dashboard.tsx` | Hero with banner image + wave, cluster cards section, rename "Recent Syncs" → "Recent Activity" |
| `ui/src/components/LoadingState.tsx` | Add mascot image with pulse animation |
| `ui/src/components/ErrorState.tsx` | Add mascot image above error message |
| `ui/src/components/ErrorFallback.tsx` | Add mascot image above error boundary |
| `ui/src/views/ClusterDetail.tsx` | Migrate hand-rolled tabs to shadcn/ui Tabs, add URL param sync |
| `ui/src/views/AddonDetail.tsx` | Add shadcn/ui Tabs (Overview / Clusters / Upgrade) |
| `ui/src/views/AddonCatalog.tsx` | Default to grid view, restyle cards with ocean colors |
| `ui/src/App.tsx` | Add Settings route, redirect `/users` and `/api-keys` to `/settings?tab=*` |

### Assets
| Action | File |
|--------|------|
| Copy | `assets/logo/sharko-banner.png` → `ui/public/sharko-banner.png` |

---

### Task 1: Copy Banner Asset + Ocean Theme CSS Variables

**Files:**
- Copy: `assets/logo/sharko-banner.png` → `ui/public/sharko-banner.png`
- Modify: `ui/src/index.css`

- [ ] **Step 1: Copy the banner asset**

```bash
cp assets/logo/sharko-banner.png ui/public/sharko-banner.png
```

- [ ] **Step 2: Add ocean theme CSS variables to index.css**

Replace the `:root` block in `ui/src/index.css` with ocean-themed values. Keep the existing CSS structure — only change the color values. Add new sidebar-specific and ocean-specific custom properties.

In `ui/src/index.css`, inside `@layer base`, replace the `:root { ... }` block:

```css
  :root {
    --background: oklch(0.97 0.008 220);
    --foreground: oklch(0.145 0.015 260);
    --card: oklch(1 0 0);
    --card-foreground: oklch(0.145 0.015 260);
    --popover: oklch(1 0 0);
    --popover-foreground: oklch(0.145 0.015 260);
    --primary: oklch(0.72 0.155 200);
    --primary-foreground: oklch(0.985 0 0);
    --secondary: oklch(0.95 0.01 220);
    --secondary-foreground: oklch(0.25 0.015 260);
    --muted: oklch(0.95 0.01 220);
    --muted-foreground: oklch(0.556 0.01 260);
    --accent: oklch(0.72 0.155 200);
    --accent-foreground: oklch(0.985 0 0);
    --destructive: oklch(0.577 0.245 27.325);
    --destructive-foreground: oklch(0.577 0.245 27.325);
    --border: oklch(0.88 0.015 220);
    --input: oklch(0.88 0.015 220);
    --ring: oklch(0.72 0.155 200);
    --chart-1: oklch(0.72 0.155 200);
    --chart-2: oklch(0.6 0.118 184.704);
    --chart-3: oklch(0.398 0.07 227.392);
    --chart-4: oklch(0.828 0.189 84.429);
    --chart-5: oklch(0.769 0.188 70.08);
    --radius: 0.625rem;
    --sidebar: oklch(0.12 0.02 250);
    --sidebar-foreground: oklch(0.93 0.005 240);
    --sidebar-primary: oklch(0.72 0.155 200);
    --sidebar-primary-foreground: oklch(0.985 0 0);
    --sidebar-accent: oklch(0.18 0.02 250);
    --sidebar-accent-foreground: oklch(0.93 0.005 240);
    --sidebar-border: oklch(0.22 0.02 250);
    --sidebar-ring: oklch(0.72 0.155 200);
  }
```

This shifts the light theme from gray to a blue-tinted ocean palette (`--background` is light blue, borders have blue hue 220, sidebar is deep navy).

- [ ] **Step 3: Verify the build**

```bash
cd ui && npm run build
```
Expected: Build succeeds with no errors.

- [ ] **Step 4: Commit**

```bash
git add ui/public/sharko-banner.png ui/src/index.css
git commit -m "feat(ui): add ocean theme CSS variables and banner asset"
```

---

### Task 2: Sidebar Restructure — Mascot, Ocean Colors, Section Rename

**Files:**
- Modify: `ui/src/components/Layout.tsx`

- [ ] **Step 1: Update the sidebar nav sections**

In `ui/src/components/Layout.tsx`, replace the `navSections` array (lines ~43-77) with:

```tsx
const navSections: NavSection[] = [
  {
    label: 'Overview',
    items: [
      { to: '/', label: 'Dashboard', icon: LayoutDashboard },
      { to: '/clusters', label: 'Clusters', icon: Server },
      { to: '/addons', label: 'Addons Catalog', icon: Package },
    ],
  },
  {
    label: 'Manage',
    items: [
      { to: '/version-matrix', label: 'Version Drift', icon: TableProperties },
      { to: '/upgrade', label: 'Upgrade Checker', icon: ArrowUpCircle },
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
  {
    label: 'Help',
    items: [
      { to: '/assistant', label: 'AI Assistant', icon: MessageSquare },
      { to: '/docs', label: 'Docs', icon: BookOpen },
    ],
  },
]
```

- [ ] **Step 2: Update the sidebar logo row to use mascot**

Replace the logo `<Link>` block (lines ~158-168) with:

```tsx
        <Link
          to="/dashboard"
          aria-label="Sharko — go to dashboard"
          className="flex h-14 items-center gap-2.5 border-b border-[#1A2D4A] px-4 transition-colors hover:bg-[#132038]"
          onClick={() => setMobileOpen(false)}
        >
          <img src="/sharko-mascot.png" alt="" className="h-7 w-auto shrink-0" />
          {!collapsed && (
            <span className="text-sm font-bold text-cyan-400">Sharko</span>
          )}
        </Link>
```

- [ ] **Step 3: Update sidebar background colors to ocean navy**

Replace the sidebar `<aside>` className (line ~153-156). Change `bg-slate-900` to `bg-[#0B1426]` and all `border-slate-700` to `border-[#1A2D4A]`, all `bg-slate-800` to `bg-[#132038]`, all `bg-slate-700` to `bg-[#1A2D4A]`, all `text-slate-*` to appropriate ocean equivalents:

- `bg-slate-900` → `bg-[#0B1426]`
- `border-slate-700` → `border-[#1A2D4A]`
- `bg-slate-800` (hover) → `bg-[#132038]`
- `bg-slate-700` (active) → `bg-[#1A2D4A]`
- `text-slate-500` (section labels) → `text-[#5A7A9B]`
- `text-slate-300` (nav text) → `text-[#94B8DB]`
- `text-slate-400` (collapse button) → `text-[#6B8FB5]`
- `text-slate-500` (version) → `text-[#5A7A9B]`

- [ ] **Step 4: Update the content area background**

Change the outer container (line ~145) from `bg-gray-50 dark:bg-gray-950` to `bg-[#F0F7FF] dark:bg-gray-950`.

- [ ] **Step 5: Update routeLabels to remove users/api-keys standalone entries**

In `routeLabels`, keep the entries (they're still used for breadcrumbs). No change needed.

- [ ] **Step 6: Verify the build**

```bash
cd ui && npm run build
```
Expected: Build succeeds. Sidebar shows mascot, ocean navy background, 3 groups (Overview/Manage/Configure + Help), Settings consolidates the Configure section.

- [ ] **Step 7: Commit**

```bash
git add ui/src/components/Layout.tsx
git commit -m "feat(ui): restructure sidebar with mascot logo and ocean navy colors"
```

---

### Task 3: Dashboard Hero with Banner + Cluster Cards

**Files:**
- Create: `ui/src/components/AddonDots.tsx`
- Create: `ui/src/components/ClusterCard.tsx`
- Create: `ui/src/components/WaveDecoration.tsx`
- Modify: `ui/src/views/Dashboard.tsx`

- [ ] **Step 1: Create AddonDots component**

Create `ui/src/components/AddonDots.tsx`:

```tsx
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'

interface AddonDot {
  name: string
  health: string
}

const healthColor: Record<string, string> = {
  Healthy: 'bg-green-500',
  Progressing: 'bg-amber-400',
  Degraded: 'bg-red-500',
  Missing: 'bg-gray-400',
  Unknown: 'bg-gray-400',
}

interface AddonDotsProps {
  addons: AddonDot[]
}

export function AddonDots({ addons }: AddonDotsProps) {
  if (addons.length === 0) return null

  return (
    <TooltipProvider delayDuration={200}>
      <div className="flex flex-wrap gap-1">
        {addons.map((addon) => (
          <Tooltip key={addon.name}>
            <TooltipTrigger asChild>
              <div
                className={`h-2.5 w-2.5 rounded-full transition-colors ${healthColor[addon.health] ?? 'bg-gray-400'}`}
              />
            </TooltipTrigger>
            <TooltipContent side="top" className="text-xs">
              <span className="font-medium">{addon.name}</span>: {addon.health}
            </TooltipContent>
          </Tooltip>
        ))}
      </div>
    </TooltipProvider>
  )
}
```

- [ ] **Step 2: Create ClusterCard component**

Create `ui/src/components/ClusterCard.tsx`:

```tsx
import { useNavigate } from 'react-router-dom'
import { Server } from 'lucide-react'
import { AddonDots } from '@/components/AddonDots'

interface ClusterAddonSummary {
  name: string
  health: string
}

interface ClusterCardProps {
  name: string
  connectionStatus: string
  addonSummary: ClusterAddonSummary[]
  healthyCount: number
  totalCount: number
}

export function ClusterCard({
  name,
  connectionStatus,
  addonSummary,
  healthyCount,
  totalCount,
}: ClusterCardProps) {
  const navigate = useNavigate()
  const isConnected = connectionStatus === 'Successful' || connectionStatus === 'Connected'

  return (
    <div
      onClick={() => navigate(`/clusters/${name}`)}
      className="group cursor-pointer rounded-xl border border-[#D6E5F5] bg-white p-4 shadow-sm transition-all duration-150 hover:-translate-y-0.5 hover:border-cyan-400 hover:shadow-md dark:border-gray-700 dark:bg-gray-800 dark:hover:border-cyan-500"
    >
      <div className="mb-2 flex items-center gap-2">
        <Server className="h-4 w-4 text-cyan-600 dark:text-cyan-400" />
        <h3 className="truncate text-sm font-bold text-gray-900 dark:text-gray-100">{name}</h3>
      </div>

      <div className="mb-2 flex items-center gap-1.5">
        <div className={`h-2 w-2 rounded-full ${isConnected ? 'bg-green-500' : 'bg-red-500'}`} />
        <span className={`text-xs ${isConnected ? 'text-green-700 dark:text-green-400' : 'text-red-700 dark:text-red-400'}`}>
          {isConnected ? 'Connected' : 'Disconnected'}
        </span>
      </div>

      <p className="mb-2 text-xs text-gray-500 dark:text-gray-400">
        {totalCount > 0 ? `${healthyCount}/${totalCount} healthy` : 'No addons'}
      </p>

      <AddonDots addons={addonSummary} />
    </div>
  )
}
```

- [ ] **Step 3: Create WaveDecoration component**

Create `ui/src/components/WaveDecoration.tsx`:

```tsx
export function WaveDecoration() {
  return (
    <div className="pointer-events-none absolute bottom-0 left-0 right-0 overflow-hidden leading-[0]">
      <svg
        viewBox="0 0 1200 60"
        preserveAspectRatio="none"
        className="block h-[30px] w-full"
      >
        <path
          d="M0,30 C200,50 400,10 600,30 C800,50 1000,10 1200,30 L1200,60 L0,60 Z"
          className="fill-[#F0F7FF] dark:fill-gray-950"
        />
      </svg>
    </div>
  )
}
```

- [ ] **Step 4: Update Dashboard.tsx — hero section with banner**

In `ui/src/views/Dashboard.tsx`, replace the hero section (the `<div className="rounded-2xl bg-gradient-to-r from-cyan-600 to-blue-700 ...">` block, lines ~132-140) with:

```tsx
      {/* Hero Section */}
      <div className="relative overflow-hidden rounded-2xl bg-gradient-to-r from-cyan-700 to-blue-800 px-8 py-8 text-white shadow-lg dark:from-cyan-900 dark:to-blue-950">
        <div className="flex items-center gap-6">
          <img
            src="/sharko-banner.png"
            alt="Sharko"
            className="hidden h-24 w-auto sm:block"
          />
          <div>
            <h1 className="text-2xl font-bold tracking-tight sm:text-3xl">
              Sharko
            </h1>
            <p className="mt-1 max-w-2xl text-sm text-cyan-100 sm:text-base">
              Addon management across all your Kubernetes clusters.
            </p>
          </div>
        </div>
        <WaveDecoration />
      </div>
```

Add the import at the top:
```tsx
import { WaveDecoration } from '@/components/WaveDecoration'
```

- [ ] **Step 5: Add cluster cards section to Dashboard**

After the stats cards grid and before the Health Bars section, add a cluster cards section. This requires fetching cluster data. Add to the existing `fetchData` function:

Add these imports at the top:
```tsx
import { ClusterCard } from '@/components/ClusterCard'
import type { ClustersResponse, VersionMatrixResponse } from '@/services/models'
```

Add state for clusters:
```tsx
const [clusters, setClusters] = useState<{ name: string; connectionStatus: string; addons: { name: string; health: string }[]; healthy: number; total: number }[]>([])
```

In `fetchData`, after existing data fetches, add cluster enrichment. Add to the `Promise.all`:
```tsx
api.getClusters().catch(() => null),
```

After setting stats, add cluster card data assembly using the version matrix data (which is already fetched):
```tsx
      // Build cluster cards from clusters + version matrix
      const clustersData = /* the getClusters result */;
      if (clustersData?.clusters && matrixData?.addons) {
        const clusterCards = clustersData.clusters.map(c => {
          const addons: { name: string; health: string }[] = []
          let healthy = 0
          let total = 0
          for (const row of matrixData.addons) {
            const cell = row.cells?.[c.name]
            if (cell) {
              total++
              const health = cell.health || 'Unknown'
              if (health === 'Healthy') healthy++
              addons.push({ name: row.addon_name, health })
            }
          }
          return {
            name: c.name,
            connectionStatus: c.connection_status || 'Unknown',
            addons,
            healthy,
            total,
          }
        })
        setClusters(clusterCards)
      }
```

Then add the cluster cards section in JSX after the stats cards grid:

```tsx
      {/* Cluster Cards */}
      {clusters.length > 0 && (
        <div>
          <h2 className="mb-3 text-lg font-semibold text-gray-900 dark:text-gray-100">Clusters</h2>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {clusters.map((cluster) => (
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

- [ ] **Step 6: Rename "Recent Syncs" to "Recent Activity"**

In Dashboard.tsx, change the heading text from "Recent Syncs" to "Recent Activity" (around line ~267).

- [ ] **Step 7: Verify the build**

```bash
cd ui && npm run build
```
Expected: Build succeeds. Dashboard shows banner hero with wave, cluster cards, renamed activity section.

- [ ] **Step 8: Commit**

```bash
git add ui/src/components/AddonDots.tsx ui/src/components/ClusterCard.tsx ui/src/components/WaveDecoration.tsx ui/src/views/Dashboard.tsx
git commit -m "feat(ui): add dashboard cluster cards, banner hero, and addon health dots"
```

---

### Task 4: Empty State, Loading State, Error State — Mascot

**Files:**
- Create: `ui/src/components/EmptyState.tsx`
- Modify: `ui/src/components/LoadingState.tsx`
- Modify: `ui/src/components/ErrorState.tsx`
- Modify: `ui/src/components/ErrorFallback.tsx`

- [ ] **Step 1: Create EmptyState component**

Create `ui/src/components/EmptyState.tsx`:

```tsx
import { ReactNode } from 'react'

interface EmptyStateProps {
  title: string
  description?: string
  action?: ReactNode
}

export function EmptyState({ title, description, action }: EmptyStateProps) {
  return (
    <div className="flex flex-col items-center justify-center gap-4 py-16 text-center">
      <img
        src="/sharko-mascot.png"
        alt=""
        className="h-20 w-auto opacity-80"
      />
      <div>
        <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100">{title}</h3>
        {description && (
          <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">{description}</p>
        )}
      </div>
      {action}
    </div>
  )
}
```

- [ ] **Step 2: Update LoadingState with mascot**

Replace the entire content of `ui/src/components/LoadingState.tsx`:

```tsx
interface LoadingStateProps {
  message?: string;
}

export function LoadingState({ message = 'Loading...' }: LoadingStateProps) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 py-12">
      <img
        src="/sharko-mascot.png"
        alt=""
        className="h-12 w-auto animate-pulse opacity-70"
      />
      <p className="text-sm text-gray-500 dark:text-gray-400">{message}</p>
    </div>
  );
}
```

- [ ] **Step 3: Update ErrorState with mascot**

Replace the entire content of `ui/src/components/ErrorState.tsx`:

```tsx
interface ErrorStateProps {
  message: string;
  onRetry?: () => void;
}

export function ErrorState({ message, onRetry }: ErrorStateProps) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 py-12 text-center">
      <img
        src="/sharko-mascot.png"
        alt=""
        className="h-16 w-auto opacity-70"
      />
      <p className="text-sm text-gray-700 dark:text-gray-300">{message}</p>
      {onRetry && (
        <button
          type="button"
          onClick={onRetry}
          className="rounded-md bg-cyan-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-cyan-700 focus:outline-none focus:ring-2 focus:ring-cyan-400 focus:ring-offset-2 dark:ring-offset-gray-900"
        >
          Try Again
        </button>
      )}
    </div>
  );
}
```

- [ ] **Step 4: Update ErrorFallback with mascot**

Replace the entire content of `ui/src/components/ErrorFallback.tsx`:

```tsx
interface ErrorFallbackProps {
  error: Error;
  resetErrorBoundary: () => void;
}

export function ErrorFallback({ error, resetErrorBoundary }: ErrorFallbackProps) {
  return (
    <div className="mx-auto max-w-lg rounded-lg border border-gray-200 bg-white shadow-sm dark:border-gray-700 dark:bg-gray-800">
      <div className="h-1 rounded-t-lg bg-red-500" />
      <div className="flex flex-col items-center gap-4 p-6 text-center">
        <img
          src="/sharko-mascot.png"
          alt=""
          className="h-16 w-auto opacity-70"
        />
        <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
          Something went wrong
        </h2>
        <pre className="w-full overflow-auto rounded-md bg-gray-100 p-3 text-left font-mono text-sm text-gray-700 dark:bg-gray-700 dark:text-gray-200">
          {error.message}
        </pre>
        <button
          type="button"
          onClick={resetErrorBoundary}
          className="rounded-md bg-cyan-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-cyan-700 focus:outline-none focus:ring-2 focus:ring-cyan-400 focus:ring-offset-2 dark:ring-offset-gray-800"
        >
          Try again
        </button>
      </div>
    </div>
  );
}
```

- [ ] **Step 5: Verify the build**

```bash
cd ui && npm run build
```
Expected: Build succeeds.

- [ ] **Step 6: Commit**

```bash
git add ui/src/components/EmptyState.tsx ui/src/components/LoadingState.tsx ui/src/components/ErrorState.tsx ui/src/components/ErrorFallback.tsx
git commit -m "feat(ui): add mascot to loading, error, and empty states"
```

---

### Task 5: Detail Page Tabs — Cluster Detail + Addon Detail

**Files:**
- Modify: `ui/src/views/ClusterDetail.tsx`
- Modify: `ui/src/views/AddonDetail.tsx`

- [ ] **Step 1: Migrate ClusterDetail tabs to shadcn/ui Tabs with URL sync**

In `ui/src/views/ClusterDetail.tsx`:

Add imports:
```tsx
import { useSearchParams } from 'react-router-dom'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
```

Replace the hand-rolled tab state and buttons. Change:
```tsx
const [activeTab, setActiveTab] = useState<'comparison' | 'config-overrides'>('comparison');
```
to:
```tsx
const [searchParams, setSearchParams] = useSearchParams();
const activeTab = searchParams.get('tab') || 'addons';
const setActiveTab = (tab: string) => setSearchParams({ tab }, { replace: true });
```

Replace the hand-rolled tab buttons (the `<div className="flex gap-1 border-b ...">` block, lines ~401-424) with:

```tsx
      <Tabs value={activeTab} onValueChange={setActiveTab}>
        <TabsList variant="line">
          <TabsTrigger value="addons">Addons</TabsTrigger>
          <TabsTrigger value="config-overrides">Values: Global vs. Cluster</TabsTrigger>
        </TabsList>

        <TabsContent value="addons">
          {/* ... existing comparison content ... */}
        </TabsContent>

        <TabsContent value="config-overrides">
          {/* ... existing config overrides content ... */}
        </TabsContent>
      </Tabs>
```

Move the content from the `{activeTab === 'comparison' && (...)}` and `{activeTab === 'config-overrides' && (...)}` blocks into the respective `TabsContent` components.

- [ ] **Step 2: Add tabs to AddonDetail**

In `ui/src/views/AddonDetail.tsx`, wrap the existing content in shadcn/ui Tabs. The addon detail page currently shows everything in a single scroll. Split it into:

- **Overview** tab: header, stat cards, health bar, environment versions, global values
- **Clusters** tab: filter controls + cluster applications table + disabled clusters
- **Upgrade** tab: the upgrade dialog trigger + info

Add imports:
```tsx
import { useSearchParams } from 'react-router-dom'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
```

Add URL-synced tab state:
```tsx
const [searchParams, setSearchParams] = useSearchParams()
const activeTab = searchParams.get('tab') || 'overview'
const setActiveTab = (tab: string) => setSearchParams({ tab }, { replace: true })
```

After the header and confirmation modals, wrap the rest in Tabs:

```tsx
      <Tabs value={activeTab} onValueChange={setActiveTab}>
        <TabsList variant="line">
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="clusters">Clusters ({enabledApps.length})</TabsTrigger>
          <TabsTrigger value="upgrade">Upgrade</TabsTrigger>
        </TabsList>

        <TabsContent value="overview" className="space-y-6">
          {/* stat cards, health bar, env versions, global values */}
        </TabsContent>

        <TabsContent value="clusters" className="space-y-6">
          {/* filter controls, cluster table, disabled clusters */}
        </TabsContent>

        <TabsContent value="upgrade" className="space-y-6">
          {/* upgrade controls — version input, cluster selector, submit */}
        </TabsContent>
      </Tabs>
```

- [ ] **Step 3: Verify the build**

```bash
cd ui && npm run build
```
Expected: Build succeeds. Both detail pages now use shadcn/ui Tabs with URL-synced state.

- [ ] **Step 4: Run tests**

```bash
cd ui && npm test -- --run
```
Expected: Existing tests pass (ClusterDetail and AddonDetail tests may need minor updates if they check for tab button text).

- [ ] **Step 5: Commit**

```bash
git add ui/src/views/ClusterDetail.tsx ui/src/views/AddonDetail.tsx
git commit -m "feat(ui): migrate detail pages to shadcn/ui Tabs with URL sync"
```

---

### Task 6: Addons Catalog — Default Grid View + Ocean Styling

**Files:**
- Modify: `ui/src/views/AddonCatalog.tsx`

- [ ] **Step 1: Change default view mode to grid**

In `ui/src/views/AddonCatalog.tsx`, change line ~362:
```tsx
const [viewMode, setViewMode] = useState<'grid' | 'list'>('list')
```
to:
```tsx
const [viewMode, setViewMode] = useState<'grid' | 'list'>('grid')
```

- [ ] **Step 2: Update card hover styling to ocean theme**

In the `AddonCard` component (line ~101-103), the card already has `hover:border-cyan-400` which works with the ocean theme. Update the card border to match ocean:

Change:
```tsx
className="group flex cursor-pointer flex-col rounded-lg border border-gray-200 bg-white shadow-sm transition-all hover:-translate-y-1 hover:border-cyan-400 hover:shadow-md dark:border-gray-700 dark:bg-gray-800 dark:hover:border-cyan-500"
```
to:
```tsx
className="group flex cursor-pointer flex-col rounded-lg border border-[#D6E5F5] bg-white shadow-sm transition-all duration-150 hover:-translate-y-0.5 hover:border-cyan-400 hover:shadow-md dark:border-gray-700 dark:bg-gray-800 dark:hover:border-cyan-500"
```

- [ ] **Step 3: Verify the build**

```bash
cd ui && npm run build
```
Expected: Build succeeds. Addons catalog defaults to grid view with ocean-styled cards.

- [ ] **Step 4: Commit**

```bash
git add ui/src/views/AddonCatalog.tsx
git commit -m "feat(ui): default addons catalog to grid view with ocean card styling"
```

---

### Task 7: Settings Consolidation — Tabbed Page

**Files:**
- Create: `ui/src/views/Settings.tsx`
- Modify: `ui/src/App.tsx`

- [ ] **Step 1: Create Settings.tsx with tabs**

Create `ui/src/views/Settings.tsx`:

```tsx
import { useSearchParams } from 'react-router-dom'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { Connections } from '@/views/Connections'
import { UserManagement } from '@/views/UserManagement'
import { ApiKeys } from '@/views/ApiKeys'
import { useAuth } from '@/hooks/useAuth'

export function Settings() {
  const [searchParams, setSearchParams] = useSearchParams()
  const { isAdmin } = useAuth()
  const tab = searchParams.get('tab') || 'connections'
  const setTab = (t: string) => setSearchParams({ tab: t }, { replace: true })

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold text-gray-900 dark:text-gray-100">Settings</h1>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
          Manage connections, users, API keys, and platform configuration.
        </p>
      </div>

      <Tabs value={tab} onValueChange={setTab}>
        <TabsList variant="line">
          <TabsTrigger value="connections">Connections</TabsTrigger>
          {isAdmin && <TabsTrigger value="users">Users</TabsTrigger>}
          {isAdmin && <TabsTrigger value="api-keys">API Keys</TabsTrigger>}
        </TabsList>

        <TabsContent value="connections">
          <Connections embedded />
        </TabsContent>

        {isAdmin && (
          <TabsContent value="users">
            <UserManagement embedded />
          </TabsContent>
        )}

        {isAdmin && (
          <TabsContent value="api-keys">
            <ApiKeys embedded />
          </TabsContent>
        )}
      </Tabs>
    </div>
  )
}
```

- [ ] **Step 2: Add `embedded` prop to Connections, UserManagement, ApiKeys**

Each of these views currently renders its own page title and wrapper. Add an `embedded?: boolean` prop so that when rendered inside Settings tabs, they skip the outer title/wrapper.

In `ui/src/views/Connections.tsx`, update the component signature:
```tsx
export function Connections({ embedded }: { embedded?: boolean } = {}) {
```

Wrap the page title in a conditional:
```tsx
{!embedded && (
  <h1 className="text-2xl font-bold ...">Settings</h1>
)}
```

Do the same pattern for `UserManagement.tsx` and `ApiKeys.tsx` — add `embedded?: boolean` prop and conditionally hide the page title.

- [ ] **Step 3: Update App.tsx routes**

In `ui/src/App.tsx`, add the Settings import and update routes:

```tsx
import { Settings } from '@/views/Settings'
```

Change the settings route:
```tsx
<Route path="settings" element={<RoleGuard adminOnly fallback={<Navigate to="/settings" replace />}><Settings /></RoleGuard>} />
```

Actually, Settings itself handles admin-only tabs, so the route should be accessible to all users (connections tab is for everyone):
```tsx
<Route path="settings" element={<Settings />} />
```

Add redirects for backwards compatibility:
```tsx
<Route path="users" element={<Navigate to="/settings?tab=users" replace />} />
<Route path="api-keys" element={<Navigate to="/settings?tab=api-keys" replace />} />
```

- [ ] **Step 4: Verify the build**

```bash
cd ui && npm run build
```
Expected: Build succeeds. `/settings` shows tabbed page. `/users` and `/api-keys` redirect to settings tabs.

- [ ] **Step 5: Run tests**

```bash
cd ui && npm test -- --run
```
Expected: Tests pass. The Connections test renders `<Connections />` directly (not via Settings), so it should still work.

- [ ] **Step 6: Commit**

```bash
git add ui/src/views/Settings.tsx ui/src/views/Connections.tsx ui/src/views/UserManagement.tsx ui/src/views/ApiKeys.tsx ui/src/App.tsx
git commit -m "feat(ui): consolidate settings into single tabbed page"
```

---

### Task 8: Polish — Hover Effects, Card Borders, Final Color Tuning

**Files:**
- Modify: `ui/src/components/StatCard.tsx`
- Modify: `ui/src/views/Dashboard.tsx`

- [ ] **Step 1: Update StatCard with ocean card borders**

In `ui/src/components/StatCard.tsx`, update the card container className to use ocean-themed borders:

Change `border-gray-200` to `border-[#D6E5F5]` in the default (non-selected) state.

- [ ] **Step 2: Update Dashboard card borders**

In `ui/src/views/Dashboard.tsx`, update all `border-gray-200 bg-white` card classNames to `border-[#D6E5F5] bg-white` in the bottom row panels (Quick Actions, Recent Activity, Version Drift).

- [ ] **Step 3: Fix any remaining "add-on" hyphenation**

Search for "add-on" in the modified files and replace with "addon":

```bash
grep -rn "add-on" ui/src/views/Dashboard.tsx ui/src/views/AddonDetail.tsx ui/src/views/AddonCatalog.tsx
```

Fix any occurrences.

- [ ] **Step 4: Verify the full build**

```bash
cd ui && npm run build
```
Expected: Clean build.

- [ ] **Step 5: Run all tests**

```bash
cd ui && npm test -- --run
```
Expected: All tests pass.

- [ ] **Step 6: Visual verification**

```bash
make demo
```

Open http://localhost:8080. Verify:
- Ocean-tinted content background
- Navy sidebar with mascot logo
- Dashboard banner hero with wave
- Cluster cards with addon health dots
- Mascot in loading/error states
- Tabbed settings page
- Tabbed detail pages
- Grid view for addons catalog
- Dark mode toggle still works

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat(ui): polish ocean theme card borders and final color tuning"
```

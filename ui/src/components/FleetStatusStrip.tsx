import type { ReactNode } from 'react';
import { useNavigate } from 'react-router-dom';
import { Server, AppWindow, ArrowUpCircle } from 'lucide-react';
import { isNewerVersion } from '@/lib/utils';
import type { DashboardStats, VersionMatrixResponse } from '@/services/models';

// --- Fleet Status Strip (dashboard-purpose decision, WQ-1) ---
//
// Replaces the three stat cards (Total Clusters / Applications / Upgrades)
// that used to sit below Needs Attention. The maintainer's verdict on those
// cards: "small fonts, boring looks" — and the 16-product research behind
// this story found nobody actually lands on a stats poster. This strip is
// the minimum a fleet overview page still owes you: three numbers, each one
// a door to where you'd go next. It states facts; it does NOT replicate the
// Needs Attention layer's alarm logic (no settling window, no 5-state
// color rules) — that stays exactly where it lives today.

// --- Upgrades summary (moved here from Dashboard.tsx — this is now the
//     strip's data, computed once by Dashboard.fetchData and passed in) ---
//
// "X of Y have a newer version", computed from the already-fetched version
// matrix. The matrix doesn't carry a per-cell "has an upgrade" flag — only
// a per-row `newest_available` (the highest chart version the freshness
// scheduler has last seen for that addon) — so we compare each deployed
// cell's version against its row's newest_available ourselves.
export interface UpgradesSummary {
  /** Deployments (addon@cluster cells) with a newer version available. */
  withUpgrade: number;
  /** Deployments the freshness scheduler has an opinion on (row has newest_available). */
  checked: number;
  /** Addon names (deduped) with at least one cell behind newest_available — feeds the segment text. */
  namesWithUpgrade: string[];
}

export function summarizeUpgrades(matrix: VersionMatrixResponse | null): UpgradesSummary {
  let withUpgrade = 0;
  let checked = 0;
  const namesWithUpgrade: string[] = [];
  for (const row of matrix?.addons ?? []) {
    if (!row.newest_available) continue; // no freshness data for this addon yet
    let rowHasUpgrade = false;
    for (const cell of Object.values(row.cells || {})) {
      if (!cell?.version) continue;
      checked++;
      if (isNewerVersion(cell.version, row.newest_available)) {
        withUpgrade++;
        rowHasUpgrade = true;
      }
    }
    if (rowHasUpgrade) namesWithUpgrade.push(row.addon_name);
  }
  return { withUpgrade, checked, namesWithUpgrade };
}

// At small N, name the addons right in the segment so it informs before
// any click; past N=3, name the first one and count the rest
// ("metrics-server and 3 more") — the strip has no room for a full list.
export function upgradesSubtitle(upgrades: UpgradesSummary): string {
  if (upgrades.checked === 0) return 'No version data yet';
  if (upgrades.withUpgrade === 0) return 'Everything on the newest known version';
  const { namesWithUpgrade } = upgrades;
  if (namesWithUpgrade.length <= 3) return namesWithUpgrade.join(', ');
  return `${namesWithUpgrade[0]} and ${namesWithUpgrade.length - 1} more`;
}

// --- Segment text builders ---
//
// Each segment renders as a list of "parts" — plain facts by default, with
// individual parts flagged as an exception (amber/red) when they represent
// a broken or outdated count. This is a much smaller vocabulary than the
// Needs Attention severity model on purpose: two tones, no settling window,
// no per-state color table. That logic stays in the banner/attention layer.
interface StripPart {
  text: string;
  tone?: 'warn' | 'danger';
}

function clusterStripParts(c: DashboardStats['clusters']): StripPart[] {
  const parts: StripPart[] = [{ text: `${c.total} cluster${c.total !== 1 ? 's' : ''}` }];
  if (c.connected > 0) parts.push({ text: `${c.connected} connected` });
  if (c.untested > 0) parts.push({ text: `${c.untested} waiting` });
  if (c.pending > 0) parts.push({ text: `${c.pending} connecting` });
  if (c.missing > 0) parts.push({ text: `${c.missing} not connected`, tone: 'danger' });
  if (c.failed > 0) parts.push({ text: `${c.failed} disconnected`, tone: 'danger' });
  return parts;
}

function appsStripParts(total: number, healthy: number): StripPart[] {
  const notHealthy = Math.max(0, total - healthy);
  return [
    { text: `${total} app${total !== 1 ? 's' : ''}` },
    { text: `${healthy} healthy` },
    { text: `${notHealthy} not healthy`, tone: notHealthy > 0 ? 'danger' : undefined },
  ];
}

function upgradesStripParts(upgrades: UpgradesSummary): StripPart[] {
  const subtitle = upgradesSubtitle(upgrades);
  if (upgrades.withUpgrade === 0) return [{ text: subtitle }];
  return [
    { text: `${upgrades.withUpgrade} outdated`, tone: 'warn' },
    { text: subtitle },
  ];
}

function toneClass(tone?: 'warn' | 'danger'): string | undefined {
  if (tone === 'danger') return 'text-red-700 dark:text-red-400';
  if (tone === 'warn') return 'text-amber-700 dark:text-amber-400';
  return undefined;
}

function StripText({ parts }: { parts: StripPart[] }) {
  return (
    <span className="text-sm font-medium text-muted-foreground">
      {parts.map((p, i) => (
        <span key={i}>
          {i > 0 && ' · '}
          <span className={toneClass(p.tone)}>{p.text}</span>
        </span>
      ))}
    </span>
  );
}

interface SegmentProps {
  icon: ReactNode;
  parts: StripPart[];
  onClick: () => void;
  testId: string;
  ariaLabel: string;
}

function Segment({ icon, parts, onClick, testId, ariaLabel }: SegmentProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      data-testid={testId}
      aria-label={ariaLabel}
      className="flex flex-1 min-w-[220px] items-center gap-2.5 border-b border-border px-5 py-3 text-left transition-colors hover:bg-muted/60 sm:border-b-0 sm:border-r sm:last:border-r-0"
    >
      <span className="text-muted-foreground [&_svg]:h-4 [&_svg]:w-4">{icon}</span>
      <StripText parts={parts} />
    </button>
  );
}

export interface FleetStatusStripProps {
  clusters: DashboardStats['clusters'];
  appsTotal: number;
  appsHealthy: number;
  upgrades: UpgradesSummary;
}

export function FleetStatusStrip({ clusters, appsTotal, appsHealthy, upgrades }: FleetStatusStripProps) {
  const navigate = useNavigate();

  return (
    <div
      role="group"
      aria-label="Fleet status"
      className="flex flex-wrap overflow-hidden rounded-xl border border-border bg-card shadow-sm"
    >
      <Segment
        icon={<Server />}
        parts={clusterStripParts(clusters)}
        onClick={() => navigate('/clusters')}
        testId="fleet-strip-clusters"
        ariaLabel="Clusters"
      />
      <Segment
        icon={<AppWindow />}
        parts={appsStripParts(appsTotal, appsHealthy)}
        onClick={() => navigate('/observability#addon-health')}
        testId="fleet-strip-applications"
        ariaLabel="Applications"
      />
      <Segment
        icon={<ArrowUpCircle />}
        parts={upgradesStripParts(upgrades)}
        onClick={() => navigate('/version-matrix?view=matrix')}
        testId="fleet-strip-upgrades"
        ariaLabel="Upgrades"
      />
    </div>
  );
}

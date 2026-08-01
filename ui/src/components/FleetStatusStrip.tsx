import type { ReactNode } from 'react';
import { useNavigate } from 'react-router-dom';
import { ArrowUpCircle } from 'lucide-react';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip';
import { isNewerVersion } from '@/lib/utils';
import type { DashboardStats, VersionMatrixResponse } from '@/services/models';

// --- Fleet Status Strip v2 (dashboard-purpose decision, WQ-1 → WQ-2) ---
//
// Replaces the three stat cards (Total Clusters / Applications / Upgrades)
// that used to sit below Needs Attention. WQ-1 shipped a text-list version
// of this strip; the maintainer rejected it on review — with all 5 cluster
// states non-zero the segment text overflowed, longer state labels didn't
// fit, and naming one outdated addon ("metrics-server and 49 more") told
// you nothing useful. WQ-2's fix: the Clusters and Applications segments
// become a compact donut chart (breakdown on hover/aria, not in text) plus
// the total as a plain number; Upgrades becomes a bare clickable count that
// opens the version matrix already filtered to outdated rows. It still
// states facts — no severity logic, no settling window. That stays exactly
// where it lives today (the Needs Attention layer).

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
  /** Addon names (deduped) with at least one cell behind newest_available. No longer
   *  rendered in the strip (WQ-2 — the segment is a bare number now), kept on the
   *  summary shape because the perf-S2 view cache persists this object verbatim. */
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

// --- Slice colors — single source of truth for every donut in the strip ---
//
// Validated against the project's data-viz palette validator, light AND
// dark mode, adjacent-pair mode (the semantics that match a ring chart —
// only touching slices need to read apart): lightness band, chroma floor,
// CVD separation, normal-vision separation, and contrast vs the card
// surface. Every entry is an explicit light + dark pair — never left to
// inherit — same discipline as the old toneClass table. "Waiting for first
// addon" isn't literal gray: plain gray fails the chroma floor (reads as
// "no color" rather than "a deliberately calm state"), so it's a muted
// blue-violet instead, tuned to still sit apart from the "connecting" blue.
// Color is never the only signal — the tooltip and aria text below always
// carry the same breakdown in words.
const SLICE_COLORS = {
  connected: 'text-[#16a34a] dark:text-[#16a34a]',
  connecting: 'text-[#3b82f6] dark:text-[#3b82f6]',
  waiting: 'text-[#7a3f9e] dark:text-[#7e4fb0]',
  notConnected: 'text-[#f59e0b] dark:text-[#d97706]',
  disconnected: 'text-[#b91c1c] dark:text-[#b91c1c]',
  healthy: 'text-[#16a34a] dark:text-[#16a34a]',
  notHealthy: 'text-[#b91c1c] dark:text-[#b91c1c]',
} as const;

interface DonutSlice {
  key: string;
  value: number;
  /** Plain-English tooltip/aria label — never rely on color alone. */
  label: string;
  colorClass: string;
}

function clusterSlices(c: DashboardStats['clusters']): DonutSlice[] {
  return [
    { key: 'connected', value: c.connected, label: 'connected', colorClass: SLICE_COLORS.connected },
    { key: 'connecting', value: c.pending, label: 'connecting', colorClass: SLICE_COLORS.connecting },
    { key: 'waiting', value: c.untested, label: 'waiting for first addon', colorClass: SLICE_COLORS.waiting },
    { key: 'not-connected', value: c.missing, label: 'not connected', colorClass: SLICE_COLORS.notConnected },
    { key: 'disconnected', value: c.failed, label: 'disconnected', colorClass: SLICE_COLORS.disconnected },
  ];
}

function appSlices(total: number, healthy: number): DonutSlice[] {
  const notHealthy = Math.max(0, total - healthy);
  return [
    { key: 'healthy', value: healthy, label: 'healthy', colorClass: SLICE_COLORS.healthy },
    { key: 'not-healthy', value: notHealthy, label: 'not healthy', colorClass: SLICE_COLORS.notHealthy },
  ];
}

// Full breakdown as one sentence — this is the accessible encoding. It goes
// on the segment button's own aria-label (not just a decorative child), so
// a screen-reader user gets the same information a sighted user gets from
// hovering the donut, without needing to hover anything.
function breakdownSentence(prefix: string, total: number, slices: DonutSlice[]): string {
  const nonZero = slices.filter((s) => s.value > 0);
  if (nonZero.length === 0) return `${prefix}: ${total} total`;
  const parts = nonZero.map((s) => `${s.value} ${s.label}`).join(', ');
  return `${prefix}: ${total} total — ${parts}`;
}

// --- Donut ---
//
// Plain SVG, no chart library: one <circle> per non-zero state, drawn with
// stroke-dasharray/-dashoffset arcs starting at 12 o'clock. A 2px gap sits
// between slices when there's more than one; a single non-zero state draws
// a full, gapless ring (e.g. an all-connected fleet is one solid green
// ring). Decorative only — aria-hidden, since the button's own aria-label
// already carries the breakdown in words.
const DONUT_SIZE = 30;
const DONUT_STROKE = 6;

function Donut({ slices }: { slices: DonutSlice[] }) {
  const nonZero = slices.filter((s) => s.value > 0);
  const total = nonZero.reduce((sum, s) => sum + s.value, 0);
  const center = DONUT_SIZE / 2;
  const radius = (DONUT_SIZE - DONUT_STROKE) / 2;
  const circumference = 2 * Math.PI * radius;

  if (total === 0) {
    return (
      <svg width={DONUT_SIZE} height={DONUT_SIZE} viewBox={`0 0 ${DONUT_SIZE} ${DONUT_SIZE}`} aria-hidden="true">
        <circle
          cx={center}
          cy={center}
          r={radius}
          fill="none"
          strokeWidth={DONUT_STROKE}
          stroke="currentColor"
          className="text-muted-foreground/25"
        />
      </svg>
    );
  }

  const gap = nonZero.length > 1 ? 2 : 0;
  let cumulative = 0;

  return (
    <svg width={DONUT_SIZE} height={DONUT_SIZE} viewBox={`0 0 ${DONUT_SIZE} ${DONUT_SIZE}`} aria-hidden="true">
      <g transform={`rotate(-90 ${center} ${center})`}>
        {nonZero.map((slice) => {
          const length = (slice.value / total) * (circumference - gap * nonZero.length);
          const dashoffset = -cumulative;
          cumulative += length + gap;
          return (
            <circle
              key={slice.key}
              data-testid={`donut-slice-${slice.key}`}
              cx={center}
              cy={center}
              r={radius}
              fill="none"
              strokeWidth={DONUT_STROKE}
              strokeLinecap="butt"
              strokeDasharray={`${length} ${circumference - length}`}
              strokeDashoffset={dashoffset}
              stroke="currentColor"
              className={slice.colorClass}
            />
          );
        })}
      </g>
    </svg>
  );
}

// Donut + hover tooltip. The tooltip repeats the breakdown visually (a
// color dot next to each non-zero state's label and count) for sighted
// hover users; the accessible copy of the same information lives on the
// parent segment button's aria-label (see breakdownSentence above), not
// here — a button's aria-label already replaces its subtree's accessible
// name for assistive tech, so duplicating it on this inner node would just
// be swallowed.
function DonutWithTooltip({ slices }: { slices: DonutSlice[] }) {
  const nonZero = slices.filter((s) => s.value > 0);
  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger asChild>
          <span className="inline-flex shrink-0 cursor-help" data-testid="fleet-strip-donut">
            <Donut slices={slices} />
          </span>
        </TooltipTrigger>
        <TooltipContent>
          <ul className="space-y-1">
            {nonZero.map((s) => (
              <li key={s.key} className="flex items-center gap-1.5 text-xs">
                <span className={`inline-block h-2 w-2 shrink-0 rounded-full bg-current ${s.colorClass}`} />
                <span>{s.label}</span>
                <span className="font-mono">{s.value}</span>
              </li>
            ))}
          </ul>
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

// The total number + one short word underneath the donut. The number is
// the primary read — the donut is decoration-plus. `word` is the singular
// form ("cluster" / "app") — pluralized here the same way the v1 strip did
// (`cluster${total !== 1 ? 's' : ''}`), so a fleet of one still reads
// "1 cluster", not "1 clusters".
function NumberWord({ total, word }: { total: number; word: string }) {
  return (
    <span className="whitespace-nowrap">
      <span className="text-base font-semibold text-foreground">{total}</span>{' '}
      <span className="text-sm text-muted-foreground">
        {word}
        {total !== 1 ? 's' : ''}
      </span>
    </span>
  );
}

interface SegmentProps {
  onClick: () => void;
  testId: string;
  ariaLabel: string;
  children: ReactNode;
}

function Segment({ onClick, testId, ariaLabel, children }: SegmentProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      data-testid={testId}
      aria-label={ariaLabel}
      className="flex flex-1 min-w-[220px] items-center gap-3 border-b border-border px-5 py-3 text-left transition-colors hover:bg-muted/60 sm:border-b-0 sm:border-r sm:last:border-r-0"
    >
      {children}
    </button>
  );
}

// Upgrades segment — a number, not a chart (walk finding: naming one
// outdated addon told you nothing useful; the fix is a bare count that
// opens the already-filtered matrix, not a longer sentence).
function UpgradesSegmentContent({ upgrades }: { upgrades: UpgradesSummary }) {
  if (upgrades.checked === 0) {
    return <span className="text-base font-medium text-muted-foreground">no version data</span>;
  }
  if (upgrades.withUpgrade === 0) {
    return <span className="text-base font-medium text-muted-foreground">up to date</span>;
  }
  return (
    <span className="text-base font-semibold text-amber-700 dark:text-amber-400">
      {upgrades.withUpgrade} outdated
    </span>
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
  const clusterData = clusterSlices(clusters);
  const appData = appSlices(appsTotal, appsHealthy);

  return (
    <div
      role="group"
      aria-label="Fleet status"
      className="flex flex-wrap overflow-hidden rounded-xl border border-border bg-card shadow-sm"
    >
      <Segment
        onClick={() => navigate('/clusters')}
        testId="fleet-strip-clusters"
        ariaLabel={breakdownSentence('Clusters', clusters.total, clusterData)}
      >
        <DonutWithTooltip slices={clusterData} />
        <NumberWord total={clusters.total} word="cluster" />
      </Segment>
      <Segment
        onClick={() => navigate('/observability#addon-health')}
        testId="fleet-strip-applications"
        ariaLabel={breakdownSentence('Applications', appsTotal, appData)}
      >
        <DonutWithTooltip slices={appData} />
        <NumberWord total={appsTotal} word="app" />
      </Segment>
      <Segment
        onClick={() => navigate('/version-matrix?view=matrix&filter=outdated')}
        testId="fleet-strip-upgrades"
        ariaLabel="Upgrades"
      >
        <span className="text-muted-foreground [&_svg]:h-4 [&_svg]:w-4">
          <ArrowUpCircle />
        </span>
        <UpgradesSegmentContent upgrades={upgrades} />
      </Segment>
    </div>
  );
}

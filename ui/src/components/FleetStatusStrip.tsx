import type { ReactNode } from 'react';
import { useNavigate } from 'react-router-dom';
import { ArrowUpCircle } from 'lucide-react';
import {
  DistributionPie,
  DISTRIBUTION_PIE_SIZE,
  breakdownSentence,
  type DistributionSlice,
} from '@/components/DistributionPie';
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
// the total as a plain number; the third segment becomes a bare clickable
// count that opens the version matrix already filtered. It still states
// facts — no severity logic, no settling window. That stays exactly where
// it lives today (the Needs Attention layer). Walk day 5: that third
// segment's count changed meaning from "behind the newest version seen
// upstream" to "behind this install's own catalog version" — see the
// summarizeBehindCatalog comment below for why.

// --- Behind-catalog summary (walk day 5 finding) ---
//
// The dashboard used to count deployments behind the newest version SEEN
// UPSTREAM (`row.newest_available`, the freshness scheduler's opinion).
// The maintainer's verdict: that number is trivia — the admin chose their
// version on purpose and already knows something newer exists somewhere.
// What's actually useful is whether a running application has fallen
// behind the version THIS Sharko install's catalog says it should be on —
// that's a real drift the admin owns and can fix with one click. The wire
// already computes this per cell (`drift_from_catalog`), so the FE just
// counts it instead of re-deriving anything.
export interface BehindCatalogSummary {
  /** Deployments (addon@cluster cells) whose deployed version differs from the addon's catalog version. */
  behindCount: number;
}

export function summarizeBehindCatalog(matrix: VersionMatrixResponse | null): BehindCatalogSummary {
  let behindCount = 0;
  for (const row of matrix?.addons ?? []) {
    for (const cell of Object.values(row.cells || {})) {
      if (cell?.drift_from_catalog) behindCount++;
    }
  }
  return { behindCount };
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

function clusterSlices(c: DashboardStats['clusters']): DistributionSlice[] {
  return [
    { key: 'connected', value: c.connected, label: 'connected', colorClass: SLICE_COLORS.connected },
    { key: 'connecting', value: c.pending, label: 'connecting', colorClass: SLICE_COLORS.connecting },
    { key: 'waiting', value: c.untested, label: 'waiting for first addon', colorClass: SLICE_COLORS.waiting },
    { key: 'not-connected', value: c.missing, label: 'not connected', colorClass: SLICE_COLORS.notConnected },
    { key: 'disconnected', value: c.failed, label: 'disconnected', colorClass: SLICE_COLORS.disconnected },
  ];
}

function appSlices(total: number, healthy: number): DistributionSlice[] {
  const notHealthy = Math.max(0, total - healthy);
  return [
    { key: 'healthy', value: healthy, label: 'healthy', colorClass: SLICE_COLORS.healthy },
    { key: 'not-healthy', value: notHealthy, label: 'not healthy', colorClass: SLICE_COLORS.notHealthy },
  ];
}

// --- Pie (maintainer's middle-size lock, #685) ---
//
// Was a hand-tuned 92px pie built directly in this file; now the shared
// `DistributionPie` (also used by Observability's HealthDistributionSection
// / SyncDistributionSection) at the shared 160px middle size. Colors are
// still the exact SLICE_COLORS values shipped in #676 — DistributionPie's
// Cell still takes `fill="currentColor"` plus these light+dark Tailwind
// text-color classes, so nothing about the palette (including the muted
// blue-violet "waiting" state) changes between modes.
const PIE_SIZE = DISTRIBUTION_PIE_SIZE;
const PIE_CONTAINER_HEIGHT = PIE_SIZE + 38;

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
  // Honest-titles fix (walk day 5 finding): the maintainer's verdict on
  // the old strip was "not clear they are for addons at all" — a segment
  // with just a number and a pie doesn't say what it's counting. Every
  // segment now carries a short, plain caption above its content saying
  // what it measures — which ones are about managed CLUSTERS and which
  // are about ADDON applications/versions.
  title: string;
  children: ReactNode;
}

function Segment({ onClick, testId, ariaLabel, title, children }: SegmentProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      data-testid={testId}
      aria-label={ariaLabel}
      style={{ minHeight: PIE_CONTAINER_HEIGHT }}
      className="flex flex-1 min-w-[300px] flex-col justify-center gap-1.5 border-b border-border px-5 py-3 text-left transition-colors hover:bg-muted/60 sm:border-b-0 sm:border-r sm:last:border-r-0"
    >
      <span
        data-testid={`${testId}-title`}
        className="text-xs font-semibold uppercase tracking-wide text-muted-foreground"
      >
        {title}
      </span>
      <div className="flex items-center gap-3">{children}</div>
    </button>
  );
}

// Behind-catalog segment — a number, not a chart (walk finding: naming one
// outdated addon told you nothing useful; the fix is a bare count that
// opens the already-filtered matrix, not a longer sentence). Short form
// here; the full sentence is the title attribute (hover) so the segment
// stays a one-line read.
function BehindCatalogSegmentContent({ behindCatalog }: { behindCatalog: BehindCatalogSummary }) {
  const { behindCount } = behindCatalog;
  if (behindCount === 0) {
    return (
      <span
        className="text-base font-medium text-muted-foreground"
        title="All applications are on their addon's version."
      >
        all on version
      </span>
    );
  }
  return (
    <span
      className="text-base font-semibold text-amber-700 dark:text-amber-400"
      title={`${behindCount} application${behindCount === 1 ? '' : 's'} behind their addon's version.`}
    >
      {behindCount} behind
    </span>
  );
}

export interface FleetStatusStripProps {
  clusters: DashboardStats['clusters'];
  appsTotal: number;
  appsHealthy: number;
  behindCatalog: BehindCatalogSummary;
}

export function FleetStatusStrip({ clusters, appsTotal, appsHealthy, behindCatalog }: FleetStatusStripProps) {
  const navigate = useNavigate();
  const clusterData = clusterSlices(clusters);
  const appData = appSlices(appsTotal, appsHealthy);

  return (
    <div
      role="group"
      aria-label="Managed clusters status"
      className="flex flex-wrap overflow-hidden rounded-xl border border-border bg-card shadow-sm"
    >
      <Segment
        onClick={() => navigate('/clusters')}
        testId="fleet-strip-clusters"
        title="Managed clusters"
        ariaLabel={breakdownSentence('Clusters', clusters.total, clusterData)}
      >
        <DistributionPie
          slices={clusterData}
          ariaPrefix="Clusters"
          size={PIE_SIZE}
          legendAriaHidden
          testId="fleet-strip-pie"
          legendTestId="fleet-strip-legend"
        />
        <NumberWord total={clusters.total} word="cluster" />
      </Segment>
      <Segment
        onClick={() => navigate('/observability#addon-health')}
        testId="fleet-strip-applications"
        title="Addon applications"
        ariaLabel={breakdownSentence('Applications', appsTotal, appData)}
      >
        <DistributionPie
          slices={appData}
          ariaPrefix="Applications"
          size={PIE_SIZE}
          legendAriaHidden
          testId="fleet-strip-pie"
          legendTestId="fleet-strip-legend"
        />
        <NumberWord total={appsTotal} word="app" />
      </Segment>
      <Segment
        onClick={() => navigate('/version-matrix?view=matrix&filter=behind-catalog')}
        testId="fleet-strip-upgrades"
        title="Addon versions"
        ariaLabel="Addon versions behind the catalog"
      >
        <span className="text-muted-foreground [&_svg]:h-4 [&_svg]:w-4">
          <ArrowUpCircle />
        </span>
        <BehindCatalogSegmentContent behindCatalog={behindCatalog} />
      </Segment>
    </div>
  );
}

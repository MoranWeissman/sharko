import { useMemo } from 'react';
import { PieChart, Pie, Cell, ResponsiveContainer } from 'recharts';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip';

// --- DistributionPie (maintainer's middle-size lock) ---
//
// Before this component, the fleet distribution donut existed twice: the
// home dashboard's FleetStatusStrip built one at ~92px with a Tailwind
// currentColor + text-color-class palette and a legend beside it; the
// Observability page built a second one at ~240px with plain hex fills and
// recharts' own <Legend> underneath. Same chart, two widgets, drifting
// independently. The maintainer's call: one shared component, one size in
// the middle (~160px), one legend style (colored dot + state name +
// count), plus a tooltip and aria text that carry the same breakdown —
// identity is never color-alone.
//
// `legendOrientation` is the one layout knob left to the call site: the
// two cards have different shapes (a horizontal strip segment vs. a
// vertical page card), so the pie+legend group can sit side-by-side
// ('vertical' — the legend rows stack in a column beside the pie, matching
// FleetStatusStrip) or stacked with the legend wrapped in a row underneath
// ('horizontal', matching Observability's old "legend underneath" look).
// The legend *row* markup itself — dot, name, count, text-sm — is
// identical either way; only its arrangement changes.

export interface DistributionSlice {
  key: string;
  /** Plain-English name — never rely on color alone to carry identity. */
  label: string;
  value: number;
  /**
   * Tailwind text-color class (with its own light + dark variants), paired
   * with `fill="currentColor"` on the Cell — the technique FleetStatusStrip
   * already uses to carry palette across themes without inline hex.
   */
  colorClass?: string;
  /**
   * Raw CSS color, used by call sites (e.g. Observability) whose existing
   * palette is a flat hex map rather than a Tailwind class pair. Exactly
   * one of `colorClass` / `color` should be set per slice.
   */
  color?: string;
}

/** Default pie diameter in px — the maintainer's "middle size" lock. */
export const DISTRIBUTION_PIE_SIZE = 160;

/**
 * Full breakdown as one sentence — the accessible encoding of the chart.
 * Exported so a caller that wraps the pie in its own interactive control
 * (FleetStatusStrip's segment button) can put the same sentence on that
 * control's own aria-label instead of doubling up with this component's
 * internal aria-label.
 */
export function breakdownSentence(prefix: string, total: number, slices: DistributionSlice[]): string {
  const nonZero = slices.filter((s) => s.value > 0);
  if (nonZero.length === 0) return `${prefix}: ${total} total`;
  const parts = nonZero.map((s) => `${s.value} ${s.label}`).join(', ');
  return `${prefix}: ${total} total — ${parts}`;
}

function SliceDot({ slice, className }: { slice: DistributionSlice; className: string }) {
  return (
    <span
      aria-hidden="true"
      className={`inline-block shrink-0 rounded-full ${className} ${
        slice.colorClass ? `bg-current ${slice.colorClass}` : ''
      }`}
      style={slice.color ? { backgroundColor: slice.color } : undefined}
    />
  );
}

function EmptyRing({ size }: { size: number }) {
  return (
    <div
      style={{ width: size, height: size }}
      className="shrink-0 rounded-full border-[10px] border-muted-foreground/20"
      aria-hidden="true"
    />
  );
}

export interface DistributionPieProps {
  /** Full slice set — zero-value entries are filtered out for rendering. */
  slices: DistributionSlice[];
  /** Noun used as the aria breakdown sentence prefix, e.g. "Clusters". */
  ariaPrefix: string;
  /** Pie diameter in px. Defaults to the shared 160px middle size. */
  size?: number;
  /**
   * 'vertical' (default) stacks legend rows in a column beside the pie
   * (FleetStatusStrip). 'horizontal' wraps legend rows in a row underneath
   * the pie (Observability).
   */
  legendOrientation?: 'vertical' | 'horizontal';
  /** Screen readers get the breakdown from a wrapping interactive control's
   *  own aria-label when the caller sets this (e.g. FleetStatusStrip's
   *  segment button) — see file header. Defaults to visible/announced. */
  legendAriaHidden?: boolean;
  /** data-testid on the outer pie+legend wrapper. */
  testId?: string;
  /** data-testid on the legend list. */
  legendTestId?: string;
  /**
   * Appends one more row after the per-slice rows: a neutral gray dot, the
   * word "total", and the raw total count — styled identically to every
   * other legend row. FleetStatusStrip (S4, walk day 5 ride-along) uses
   * this to replace the detached bold total number that used to float next
   * to the legend; the total is now just another row in the same list,
   * not a special case. Defaults to false, so every other call site
   * (Observability) is unaffected. Gray means a muted/neutral token here,
   * never one of the status colors the other rows use.
   */
  showTotalRow?: boolean;
}

/** Pie chart + one shared legend style + hover tooltip + accessible breakdown text. */
export function DistributionPie({
  slices,
  ariaPrefix,
  size = DISTRIBUTION_PIE_SIZE,
  legendOrientation = 'vertical',
  legendAriaHidden,
  testId,
  legendTestId,
  showTotalRow,
}: DistributionPieProps) {
  const nonZero = useMemo(() => slices.filter((s) => s.value > 0), [slices]);
  const rawTotal = useMemo(() => slices.reduce((sum, s) => sum + s.value, 0), [slices]);
  const ariaLabel = breakdownSentence(ariaPrefix, rawTotal, slices);
  const horizontal = legendOrientation === 'horizontal';

  return (
    <div
      data-testid={testId}
      className={horizontal ? 'flex flex-col items-center gap-3' : 'flex shrink-0 items-center gap-3'}
    >
      <TooltipProvider>
        <Tooltip>
          <TooltipTrigger asChild>
            <span
              role="img"
              aria-label={ariaLabel}
              className="inline-flex shrink-0 cursor-help"
              style={{ width: size, height: size }}
            >
              {rawTotal === 0 ? (
                <EmptyRing size={size} />
              ) : (
                <ResponsiveContainer width={size} height={size}>
                  <PieChart>
                    <Pie
                      data={nonZero}
                      dataKey="value"
                      nameKey="label"
                      cx="50%"
                      cy="50%"
                      innerRadius="58%"
                      outerRadius="100%"
                      paddingAngle={nonZero.length > 1 ? 2 : 0}
                      isAnimationActive={false}
                    >
                      {nonZero.map((slice) => (
                        <Cell key={slice.key} fill={slice.color ?? 'currentColor'} className={slice.colorClass} />
                      ))}
                    </Pie>
                  </PieChart>
                </ResponsiveContainer>
              )}
            </span>
          </TooltipTrigger>
          <TooltipContent>
            <ul className="space-y-1">
              {nonZero.map((s) => (
                <li key={s.key} className="flex items-center gap-1.5 text-sm">
                  <SliceDot slice={s} className="h-2 w-2" />
                  <span>{s.label}</span>
                  <span className="font-mono">{s.value}</span>
                </li>
              ))}
            </ul>
          </TooltipContent>
        </Tooltip>
      </TooltipProvider>
      <ul
        data-testid={legendTestId}
        aria-hidden={legendAriaHidden ? 'true' : undefined}
        className={horizontal ? 'flex flex-wrap justify-center gap-x-4 gap-y-1' : 'space-y-1'}
      >
        {nonZero.map((s) => (
          <li key={s.key} className="flex items-center gap-1.5 text-sm">
            <SliceDot slice={s} className="h-2.5 w-2.5" />
            <span className="text-muted-foreground">{s.label}</span>
            <span className="font-mono font-medium text-foreground">{s.value}</span>
          </li>
        ))}
        {showTotalRow && (
          <li className="flex items-center gap-1.5 text-sm">
            <span aria-hidden="true" className="inline-block h-2.5 w-2.5 shrink-0 rounded-full bg-muted-foreground/50" />
            <span className="text-muted-foreground">total</span>
            <span className="font-mono font-medium text-foreground">{rawTotal}</span>
          </li>
        )}
      </ul>
    </div>
  );
}

import { FOCUS_RING, FOCUS_RING_INSET } from '@/components/ui/focus';

export type TrendPoint = { date: string; uptime: number | null; responseTime: number | null; total: number };

export const DASHBOARD_RANGE_OPTIONS = ['1h', '24h', '7d', '30d', '90d'] as const;
export type DashboardPageRange = (typeof DASHBOARD_RANGE_OPTIONS)[number];

// The API's cap for every dashboard list. What changed keeps one row per
// monitor, so it reads the full cap of failed checks to reach enough monitors;
// the problem list is fetched whole so its states (the degraded count, group
// members) and the Needs attention total are complete. Needs attention draws
// every row and scrolls inside its card.
export const DASHBOARD_LIST_LIMIT = 50;

/** What changed rows; the card scrolls, so this only bounds the DOM. */
export const TIMELINE_ROWS = 40;

/** Checks per Needs attention trace. */
export const TRACE_SLOTS = 20;

// ─── Grid ──────────────────────────────────────────────────────────────────────
//
// One 12-column grid from xl. The first rows have fixed (clamped) heights, so
// the cards in them line up and their lists scroll inside instead of growing
// the page; Service groups takes a full-width row below.
//
//   xl  (1280)  chart 7 | attention 5 (spans two rows)
//               changes 7 |
//   3xl (1920)  chart 5 | attention 4 | changes 3
//   4xl (2400)  chart 6 | attention 3 | changes 3
//
// Below xl the cards stack, Needs attention first (DOM order), and the lists
// cap their own height.
export const GRID =
  'grid grid-cols-1 gap-6 xl:grid-cols-12 ' +
  'xl:grid-rows-[clamp(20rem,40vh,26rem)_clamp(20rem,46vh,32rem)_auto] ' +
  '3xl:grid-rows-[clamp(26rem,56vh,40rem)_auto]';
export const CELL_ATTENTION =
  'min-h-0 min-w-0 xl:col-span-5 xl:col-start-8 xl:row-span-2 xl:row-start-1 ' +
  '3xl:col-span-4 3xl:col-start-6 3xl:row-span-1 4xl:col-span-3 4xl:col-start-7';
export const CELL_CHART = 'min-h-0 min-w-0 xl:col-span-7 xl:col-start-1 xl:row-start-1 3xl:col-span-5 4xl:col-span-6';
export const CELL_CHANGES =
  'min-h-0 min-w-0 xl:col-span-7 xl:col-start-1 xl:row-start-2 ' +
  '3xl:col-span-3 3xl:col-start-10 3xl:row-start-1';
export const CELL_GROUPS = 'min-w-0 xl:col-span-12 xl:row-start-3 3xl:row-start-2';

/** Lists below xl, where the card has no grid height to fill. */
export const LIST_HEIGHT = 'max-h-[32rem] xl:max-h-none';

// `relative` keeps sr-only text inside the row: absolutely positioned, it
// would otherwise escape the scroll card's clip and stretch the page.
export const ROW_LINK = `relative block px-5 py-2.5 transition-colors duration-fast hover:bg-well active:bg-well-hover focus-visible:bg-well ${FOCUS_RING_INSET}`;

export const TEXT_LINK = `rounded text-cyan-300 transition-colors hover:text-cyan-200 hover:underline ${FOCUS_RING}`;

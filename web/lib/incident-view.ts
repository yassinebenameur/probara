// View model for the incident detail page: how the timeline is ordered,
// folded and grouped into days, and when a publication edit is a no-op.
// Kept free of React so `npm test` covers it (lib/incident-view.test.ts).

import { formatClock, formatDate } from './format';
import type { IncidentTimelineEntry } from './types';

export type TimelineEntryType = IncidentTimelineEntry['entry_type'];
export type TimelineFilter = 'all' | TimelineEntryType;

/** One rendered row: consecutive identical system events fold into one (×7). */
export interface TimelineRow {
  /** Newest entry of the row. */
  entry: IncidentTimelineEntry;
  count: number;
  /** Oldest entry of a folded run (the entry itself when count is 1). */
  first: IncidentTimelineEntry;
}

export interface TimelineDay {
  key: string;
  label: string;
  rows: TimelineRow[];
}

/** Newest first, whatever order the API returns. */
export function sortTimeline(entries: readonly IncidentTimelineEntry[]): IncidentTimelineEntry[] {
  return [...entries].sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at));
}

/**
 * Groups newest-first entries into day sections. Identical system messages
 * fold only when they are adjacent in the FULL timeline (and on the same
 * day); the filter is applied to the folded rows afterwards, so hiding the
 * notes between two "moved to monitoring" events never merges them into one
 * misleading ×2 row.
 */
export function groupTimeline(
  sortedNewestFirst: readonly IncidentTimelineEntry[],
  filter: TimelineFilter = 'all',
  dayLabel: (value: string) => string = (value) => formatDate(value),
): TimelineDay[] {
  const folded: { day: string; row: TimelineRow }[] = [];
  for (const entry of sortedNewestFirst) {
    const day = dayLabel(entry.created_at);
    const last = folded[folded.length - 1];
    if (
      last &&
      last.day === day &&
      entry.entry_type === 'system' &&
      last.row.entry.entry_type === 'system' &&
      last.row.entry.message === entry.message
    ) {
      last.row.count += 1;
      last.row.first = entry;
      continue;
    }
    folded.push({ day, row: { entry, count: 1, first: entry } });
  }

  const days: TimelineDay[] = [];
  for (const { day, row } of folded) {
    if (filter !== 'all' && row.entry.entry_type !== filter) continue;
    let current = days[days.length - 1];
    if (!current || current.key !== day) {
      current = { key: day, label: day, rows: [] };
      days.push(current);
    }
    current.rows.push(row);
  }
  return days;
}

/** Entries per filter value, for the filter's counts. */
export function countTimeline(entries: readonly IncidentTimelineEntry[]): Record<TimelineFilter, number> {
  const counts: Record<TimelineFilter, number> = {
    all: entries.length,
    public_update: 0,
    internal_note: 0,
    system: 0,
  };
  for (const entry of entries) {
    if (entry.entry_type in counts) counts[entry.entry_type] += 1;
  }
  return counts;
}

/**
 * Local time of day, `15:19`, for the timeline's time column (the day sits in
 * the section heading). Unparseable input comes back unchanged.
 */
export function timelineClock(value: string): string {
  return formatClock(value, {}, value);
}

/**
 * Whole minutes from the incident's open to an entry, or null inside the
 * first minute (and for entries dated before the open).
 */
export function minutesSinceOpen(createdAt: string, startedAt: string): number | null {
  const ms = Date.parse(createdAt) - Date.parse(startedAt);
  if (!Number.isFinite(ms) || ms < 60_000) return null;
  return Math.floor(ms / 60_000);
}

/** Same set of ids, ignoring order and duplicates. */
export function sameIdSet(a: readonly string[], b: readonly string[]): boolean {
  const left = new Set(a);
  const right = new Set(b);
  if (left.size !== right.size) return false;
  for (const id of left) if (!right.has(id)) return false;
  return true;
}

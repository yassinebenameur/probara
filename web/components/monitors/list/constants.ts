import { MonitorListSort, MonitorStatusFilter } from '@/lib/monitor-list';

export const NO_GROUP_VALUE = '__no_group__';

// The list fetches this many recent results per monitor. The trace, the
// Success column and the latency all describe this window, and the column
// headers say so.
export const RECENT_CHECKS = 20;

export type SortMode = MonitorListSort;
export type StatusFilter = MonitorStatusFilter;

export const STATUS_FILTER_LABEL: Record<StatusFilter, string> = {
  all: 'All',
  up: 'Up',
  down: 'Down',
  paused: 'Paused',
  maintenance: 'Maintenance',
};

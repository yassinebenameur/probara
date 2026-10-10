import { getMonitors } from './api';
import type { Monitor } from './types';

const MONITOR_PAGE_SIZE = 100;

type FetchMonitorsPage = typeof getMonitors;

export function sortMonitorsByName(monitors: Monitor[]): Monitor[] {
  return [...monitors].sort((a, b) => {
    const byName = a.name.localeCompare(b.name, undefined, { sensitivity: 'base' });
    if (byName !== 0) return byName;
    return a.id.localeCompare(b.id);
  });
}

export async function getAllMonitors(fetchPage: FetchMonitorsPage = getMonitors): Promise<Monitor[]> {
  const monitors: Monitor[] = [];

  for (let page = 1; ; page += 1) {
    const response = await fetchPage({ page, page_size: MONITOR_PAGE_SIZE });
    const items = response.items || [];
    monitors.push(...items);

    if (monitors.length >= response.total || items.length === 0) {
      return sortMonitorsByName(monitors);
    }
  }
}

// ── List filters in the URL ──────────────────────────────────────────────
//
// The monitors list keeps its status, tag, routing and sort filters in the
// query string so other pages can deep-link them (the dashboard links
// ?status=down, ?routing=unrouted) and a reload keeps them.

export const MONITOR_STATUS_FILTERS = ['all', 'up', 'down', 'paused', 'maintenance'] as const;
export type MonitorStatusFilter = (typeof MONITOR_STATUS_FILTERS)[number];
export type MonitorListSort = 'status' | 'name';

export interface MonitorListParams {
  status: MonitorStatusFilter;
  tags: string[];
  unroutedOnly: boolean;
  sort: MonitorListSort;
}

export function isMonitorStatusFilter(value: string | null | undefined): value is MonitorStatusFilter {
  return (MONITOR_STATUS_FILTERS as readonly string[]).includes(value ?? '');
}

/**
 * Reads the list filters from a query string. `degraded` maps to the Down
 * filter (which includes degraded); unknown values fall back to the
 * defaults. Tags may repeat (`?tag=a&tag=b`) or be comma-separated.
 */
export function parseMonitorListParams(search: string): MonitorListParams {
  const params = new URLSearchParams(search);
  const rawStatus = params.get('status');
  const status = rawStatus === 'degraded' ? 'down' : rawStatus;
  const tags = params
    .getAll('tag')
    .flatMap((t) => t.split(','))
    .map((t) => t.trim())
    .filter(Boolean);
  return {
    status: isMonitorStatusFilter(status) ? status : 'all',
    tags: Array.from(new Set(tags)),
    unroutedOnly: params.get('routing') === 'unrouted',
    sort: params.get('sort') === 'name' ? 'name' : 'status',
  };
}

/**
 * Writes the list filters into a query string, keeping any other parameters
 * and leaving defaults out. Returns the string without a leading `?`.
 */
export function formatMonitorListParams(params: MonitorListParams, search = ''): string {
  const next = new URLSearchParams(search);
  ['status', 'tag', 'routing', 'sort'].forEach((key) => next.delete(key));
  if (params.status !== 'all') next.set('status', params.status);
  params.tags.forEach((tag) => next.append('tag', tag));
  if (params.unroutedOnly) next.set('routing', 'unrouted');
  if (params.sort === 'name') next.set('sort', 'name');
  return next.toString();
}

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

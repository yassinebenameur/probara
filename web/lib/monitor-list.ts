import { getMonitors } from './api';
import {
  calculateLatencyStats,
  calculateUptime,
  getOperationalResults,
  type MonitorDisplayStatus,
  monitorTarget,
  STATUS_RANK,
} from './monitor-utils';
import { readSortParams, sortRows, type SortDir, type SortState, type SortValue, writeSortParams } from './sort';
import type { CheckResult, Monitor } from './types';

const MONITOR_PAGE_SIZE = 100;

type FetchMonitorsPage = typeof getMonitors;

export function sortMonitorsByName(monitors: Monitor[]): Monitor[] {
  return [...monitors].sort((a, b) => {
    const byName = a.name.localeCompare(b.name, undefined, { sensitivity: 'base' });
    if (byName !== 0) return byName;
    return a.id.localeCompare(b.id);
  });
}

// Pages are offset-based, so a monitor created while they load shifts the
// next page and repeats a row already fetched: keep each id once (callers
// key rows by id).
export async function getAllMonitors(fetchPage: FetchMonitorsPage = getMonitors): Promise<Monitor[]> {
  const monitors = new Map<string, Monitor>();
  let fetched = 0;

  for (let page = 1; ; page += 1) {
    const response = await fetchPage({ page, page_size: MONITOR_PAGE_SIZE });
    const items = response.items || [];
    fetched += items.length;
    items.forEach((monitor) => {
      if (!monitors.has(monitor.id)) monitors.set(monitor.id, monitor);
    });

    if (fetched >= response.total || items.length === 0) {
      return sortMonitorsByName(Array.from(monitors.values()));
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

// Sortable columns. Each key starts worst-first where "worst" means
// something: status (down first), success (lowest first), latency (slowest
// first), last check (stalest first); name starts A→Z.
export const MONITOR_SORT_KEYS = ['status', 'name', 'success', 'latency', 'last_check'] as const;
export type MonitorSortKey = (typeof MONITOR_SORT_KEYS)[number];
export type MonitorListSort = SortState<MonitorSortKey>;
export const MONITOR_SORT_DEFAULT_DIR: Record<MonitorSortKey, SortDir> = {
  status: 'asc',
  name: 'asc',
  success: 'asc',
  latency: 'desc',
  last_check: 'asc',
};
export const DEFAULT_MONITOR_SORT: MonitorListSort = { key: 'status', dir: 'asc' };

export interface MonitorListParams {
  status: MonitorStatusFilter;
  tags: string[];
  unroutedOnly: boolean;
  sort: MonitorListSort;
}

/**
 * The tag filter rule: with no tag selected every monitor matches, otherwise
 * a monitor matches when it carries ANY selected tag. The dashboard API's
 * `tag` scope is the same rule (`tags && $n`, see
 * api/internal/services/dashboard/service.go), so a dashboard count and the
 * `?tag=` list it links to cover the same monitors.
 */
export function matchesAnyTag(monitorTags: readonly string[] | null | undefined, selected: ReadonlySet<string>): boolean {
  if (selected.size === 0) return true;
  return Boolean(monitorTags?.some((tag) => selected.has(tag)));
}

/** The monitors list's filters, as the page holds them. */
export interface MonitorListFilters {
  /** The search box, trimmed and lower-cased ('' = no search). */
  query: string;
  types: ReadonlySet<string>;
  status: MonitorStatusFilter;
  tags: ReadonlySet<string>;
  unroutedOnly: boolean;
  /** The row's display status (getEffectiveMonitorStatus over its results). */
  statusOf: (monitor: Monitor) => MonitorDisplayStatus;
}

/**
 * Whether one monitor passes the list's filters. Search covers what the row
 * shows: name, target (URL or host) and tags. `ignore` leaves one filter
 * out: a filter popover counts its options against everything else that is
 * set.
 */
export function monitorMatchesFilters(monitor: Monitor, filters: MonitorListFilters, ignore?: 'type' | 'tags'): boolean {
  const { query, types: selectedTypes, status: statusFilter, tags: selectedTags, unroutedOnly } = filters;
  const matchesSearch =
    !query ||
    monitor.name.toLowerCase().includes(query) ||
    Boolean(monitorTarget(monitor)?.toLowerCase().includes(query)) ||
    Boolean(monitor.tags?.some((tag) => tag.toLowerCase().includes(query)));
  const matchesType = ignore === 'type' || selectedTypes.size === 0 || selectedTypes.has(monitor.type);
  const status = filters.statusOf(monitor);
  const matchesStatus = statusFilter === 'all' ||
    (statusFilter === 'up' && status === 'up') ||
    (statusFilter === 'down' && (status === 'down' || status === 'degraded')) ||
    (statusFilter === 'paused' && status === 'paused') ||
    (statusFilter === 'maintenance' && status === 'maintenance');
  const matchesTags = ignore === 'tags' || matchesAnyTag(monitor.tags, selectedTags);
  const matchesRouting =
    !unroutedOnly || (monitor.enabled && monitor.alert_routing?.reachable === false);
  return matchesSearch && matchesType && matchesStatus && matchesTags && matchesRouting;
}

/**
 * Status counts for the filter chips. These count every monitor, group
 * members included, under the same rule the status filter applies.
 */
export function countMonitorStatuses(
  monitors: readonly Monitor[],
  statusOf: (monitor: Monitor) => MonitorDisplayStatus
): Record<MonitorStatusFilter, number> {
  const statusCounts: Record<MonitorStatusFilter, number> = { all: monitors.length, up: 0, down: 0, paused: 0, maintenance: 0 };
  monitors.forEach((m) => {
    const status = statusOf(m);
    if (status === 'up') statusCounts.up += 1;
    else if (status === 'down' || status === 'degraded') statusCounts.down += 1;
    else if (status === 'paused') statusCounts.paused += 1;
    else if (status === 'maintenance') statusCounts.maintenance += 1;
  });
  return statusCounts;
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
    sort: readSortParams(params, MONITOR_SORT_KEYS, DEFAULT_MONITOR_SORT, MONITOR_SORT_DEFAULT_DIR),
  };
}

/**
 * Writes the list filters into a query string, keeping any other parameters
 * and leaving defaults out. Returns the string without a leading `?`.
 */
export function formatMonitorListParams(
  params: Omit<MonitorListParams, 'sort'> & {
    /** A bare key (the old 'status' | 'name' form) means that key's default direction. */
    sort: MonitorListSort | MonitorSortKey;
  },
  search = ''
): string {
  const next = new URLSearchParams(search);
  ['status', 'tag', 'routing'].forEach((key) => next.delete(key));
  if (params.status !== 'all') next.set('status', params.status);
  params.tags.forEach((tag) => next.append('tag', tag));
  if (params.unroutedOnly) next.set('routing', 'unrouted');
  const sort: MonitorListSort =
    typeof params.sort === 'string' ? { key: params.sort, dir: MONITOR_SORT_DEFAULT_DIR[params.sort] } : params.sort;
  writeSortParams(next, sort, DEFAULT_MONITOR_SORT, MONITOR_SORT_DEFAULT_DIR);
  return next.toString();
}

/** A /monitors deep link: the list's defaults plus `overrides`. Never hand-write the query string. */
export function monitorsListHref(overrides: Partial<MonitorListParams>): string {
  const query = formatMonitorListParams({ ...parseMonitorListParams(''), ...overrides });
  return query ? `/monitors?${query}` : '/monitors';
}

// ── List order ───────────────────────────────────────────────────────────
//
// One sort over groups and standalone monitors together, for every key and
// direction: a group is never pinned above or below monitors. Members inside
// an expanded group use the same order.

export interface MonitorSortContext {
  /** The row's display status (getEffectiveMonitorStatus over its results). */
  statusOf: (monitor: Monitor) => MonitorDisplayStatus;
  /** Recent results, newest first ([] while not loaded). */
  resultsOf: (monitor: Monitor) => CheckResult[];
  /** A group's members ([] for a monitor or an unloaded group). */
  membersOf: (groupId: string) => Monitor[];
}

// Calls `visit` for each of a group's members, nested groups' members
// included (cycles cut). False when the group has no members. The one walk
// over the list's group tree: the status rank, the last-check sort, the
// filter's "a group matches through its members" and the move-to-group cycle
// check all reduce it.
function foldMembers(
  groupId: string,
  membersOf: (groupId: string) => Monitor[],
  visit: (member: Monitor) => void,
  visited: Set<string> = new Set()
): boolean {
  if (visited.has(groupId)) return false;
  visited.add(groupId);
  const members = membersOf(groupId);
  members.forEach((member) => {
    visit(member);
    if (member.type === 'group') foldMembers(member.id, membersOf, visit, visited);
  });
  return members.length > 0;
}

/**
 * Whether any member of the group, nested groups' members included, passes
 * `matches`: searching for a member surfaces the group it lives in.
 */
export function groupHasMatchingMember(
  groupId: string,
  membersOf: (groupId: string) => Monitor[],
  matches: (member: Monitor) => boolean
): boolean {
  let found = false;
  foldMembers(groupId, membersOf, (member) => {
    if (!found && matches(member)) found = true;
  });
  return found;
}

/**
 * Whether `candidateDescendantID` is `rootGroupID` itself or a group nested
 * anywhere under it: moving the root into such a group would close a cycle.
 */
export function groupContainsGroup(
  rootGroupID: string,
  candidateDescendantID: string,
  membersOf: (groupId: string) => Monitor[]
): boolean {
  if (rootGroupID === candidateDescendantID) return true;
  return groupHasMatchingMember(
    rootGroupID,
    membersOf,
    (member) => member.type === 'group' && member.id === candidateDescendantID
  );
}

/**
 * Severity rank used to order a row by status (lower = worse). A group
 * ranks as its worst member, so a group hiding a Down member sorts with the
 * Down monitors; a group without members falls back to its own roll-up
 * status. Ordering only: the row still shows the group's own state.
 */
export function monitorStatusRank(monitor: Monitor, ctx: MonitorSortContext): number {
  if (monitor.type !== 'group') return STATUS_RANK[ctx.statusOf(monitor)];
  let worst = Infinity;
  const hasMembers = foldMembers(monitor.id, ctx.membersOf, (member) => {
    if (member.type !== 'group') worst = Math.min(worst, STATUS_RANK[ctx.statusOf(member)]);
  });
  // Only nested groups, all empty: nothing below to rank by.
  if (!hasMembers || worst === Infinity) return STATUS_RANK[ctx.statusOf(monitor)];
  return worst;
}

function latestCheckAt(results: CheckResult[]): number | null {
  const at = results[0] ? Date.parse(results[0].created_at) : NaN;
  return Number.isNaN(at) ? null : at;
}

/**
 * The value a row sorts by under `key`, matching what the row displays:
 * success is the share of the recent checks that passed (null = "—"),
 * latency the median of the successful ones (null for groups, whose own
 * checks are a roll-up), last check the newest result (a group without
 * roll-up results yet: its most recently checked member). Null always
 * sorts last.
 */
export function monitorSortValue(monitor: Monitor, key: MonitorSortKey, ctx: MonitorSortContext): SortValue {
  const results = ctx.resultsOf(monitor);
  switch (key) {
    case 'status':
      return monitorStatusRank(monitor, ctx);
    case 'name':
      return monitor.name;
    case 'success':
      return getOperationalResults(results).length > 0 ? calculateUptime(results) : null;
    case 'latency': {
      if (monitor.type === 'group') return null;
      const stats = calculateLatencyStats(results);
      return stats.samples > 0 ? stats.median : null;
    }
    case 'last_check': {
      const own = latestCheckAt(results);
      if (monitor.type !== 'group' || own !== null) return own;
      let latest: number | null = null;
      foldMembers(monitor.id, ctx.membersOf, (member) => {
        const at = latestCheckAt(ctx.resultsOf(member));
        if (at !== null && (latest === null || at > latest)) latest = at;
      });
      return latest;
    }
  }
}

/**
 * Orders list rows (top level or one group's members) by `sort`, ties by
 * name. Groups and monitors sort together; nothing is pinned. Sort the full
 * filtered set, then slice.
 */
export function sortMonitorList(items: readonly Monitor[], sort: MonitorListSort, ctx: MonitorSortContext): Monitor[] {
  const values = new Map(items.map((item) => [item.id, monitorSortValue(item, sort.key, ctx)]));
  return sortRows(items, (item) => values.get(item.id), sort.dir, (item) => item.name);
}

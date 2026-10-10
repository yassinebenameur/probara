// What a public status page shows for each component, computed in the
// operator UI so the status-page editor's preview and section rows tell the
// same story as the published page. It mirrors the status-page service:
//
//   - a leaf monitor shows its persisted state machine value
//     (`mapMonitorState` in status-page/internal/statuspage/service_populate.go):
//     `suspect` stays `up` (an unconfirmed blip never flaps the public page),
//     and an active maintenance window wins over everything;
//   - a group aggregates its recursive leaf members
//     (`GetGroupAggregatedStatus`): all up → up, all down → down, mixed →
//     degraded, nothing reported → unknown;
//   - the headline (`render.go`): any degraded, down or unknown component is
//     an issue ("Issues detected, N of M services need attention"); otherwise
//     maintenance, otherwise "All systems operational".
//
// The service aggregates groups from members' latest check results; this
// uses their current_state, which the ingest pipeline advances from those
// same results, so the two agree outside a check-in-flight race.

import type { Monitor, MonitorState } from './types';

export type ComponentStatus = 'up' | 'degraded' | 'down' | 'maintenance' | 'unknown';

type LeafFields = Pick<Monitor, 'in_maintenance' | 'current_state'>;

export function leafComponentStatus(monitor: LeafFields): ComponentStatus {
  if (monitor.in_maintenance) return 'maintenance';
  return mapMonitorState(monitor.current_state);
}

export function mapMonitorState(state: MonitorState | string | null | undefined): ComponentStatus {
  switch (state) {
    case 'down':
      return 'down';
    case 'degraded':
      return 'degraded';
    case 'up':
    case 'suspect':
      return 'up';
    default:
      return 'unknown';
  }
}

/**
 * A group's public status from its leaf members. Members with no state yet
 * (`unknown`) are left out, like members without a check result on the public
 * page; a degraded member makes the group mixed.
 */
export function groupComponentStatus(leaves: LeafFields[]): ComponentStatus {
  let up = 0;
  let down = 0;
  let mixed = 0;
  for (const leaf of leaves) {
    const state = mapMonitorState(leaf.current_state);
    if (state === 'up') up++;
    else if (state === 'down') down++;
    else if (state === 'degraded') mixed++;
  }
  const total = up + down + mixed;
  if (total === 0) return 'unknown';
  if (up === total) return 'up';
  if (down === total) return 'down';
  return 'degraded';
}

/**
 * Recursive leaf member IDs of a group, given each group's direct members.
 * Nested groups are expanded, cycles are cut, and the result is deduplicated
 * (the service's `getGroupMemberIDs` recursive CTE).
 */
export function leafMemberIds(
  groupId: string,
  directMembers: ReadonlyMap<string, readonly string[]>,
  isGroup: (id: string) => boolean
): string[] {
  const leaves = new Set<string>();
  const visited = new Set<string>([groupId]);
  const queue = [...(directMembers.get(groupId) ?? [])];
  while (queue.length > 0) {
    const id = queue.shift() as string;
    if (isGroup(id)) {
      if (visited.has(id)) continue;
      visited.add(id);
      queue.push(...(directMembers.get(id) ?? []));
    } else {
      leaves.add(id);
    }
  }
  return [...leaves];
}

/**
 * Status of one page component. Groups need their direct members
 * (`directMembers`, from GET /monitors/{id}); a group whose members are not
 * loaded yet reads `null` (not known), never a guessed `up`.
 */
export function componentStatus(
  monitor: Monitor,
  byId: ReadonlyMap<string, Monitor>,
  directMembers: ReadonlyMap<string, readonly string[]>
): ComponentStatus | null {
  if (monitor.type !== 'group') return leafComponentStatus(monitor);
  if (monitor.in_maintenance) return 'maintenance';
  if (!directMembers.has(monitor.id)) return null;
  const leaves = leafMemberIds(monitor.id, directMembers, (id) => byId.get(id)?.type === 'group')
    .map((id) => byId.get(id))
    .filter((m): m is Monitor => m !== undefined);
  return groupComponentStatus(leaves);
}

export interface PageHeadline {
  tone: 'ok' | 'issues' | 'maintenance';
  label: string;
  summary: string;
  issues: number;
  total: number;
}

/** The public page's headline for a set of component statuses (render.go). */
export function pageHeadline(statuses: ComponentStatus[]): PageHeadline {
  const total = statuses.length;
  const issues = statuses.filter((s) => s === 'degraded' || s === 'down' || s === 'unknown').length;
  const maintenance = statuses.filter((s) => s === 'maintenance').length;
  const operational = statuses.filter((s) => s === 'up').length;
  if (issues > 0) {
    return { tone: 'issues', label: 'Issues detected', summary: `${issues} of ${total} services need attention`, issues, total };
  }
  if (maintenance > 0) {
    return {
      tone: 'maintenance',
      label: 'Maintenance in progress',
      summary: `${maintenance} of ${total} services under planned maintenance`,
      issues,
      total,
    };
  }
  return { tone: 'ok', label: 'All systems operational', summary: `${operational} monitored services are healthy`, issues, total };
}

export const COMPONENT_STATUS_LABEL: Record<ComponentStatus, string> = {
  up: 'Operational',
  degraded: 'Degraded',
  down: 'Outage',
  maintenance: 'Maintenance',
  unknown: 'No data',
};
